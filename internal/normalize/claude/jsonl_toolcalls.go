package claude

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// eventTypeToolCall is the correlation event emitted for a non-MCP tool call
// (Read/Write/Edit/Bash/Grep/TodoWrite/Task/…) observed in the session JSONL
// transcript (J18/#105). Like the mcp_call event (jsonl_mcp.go) it is
// content-free: it carries the tool identity plus, under attributes.tool, the raw
// file_path/full_command using the exact key names the OTLP tool-span adapter
// (traces.go:toolAttributes) uses. That reuse is deliberate — the Files-lane
// projection (insights.SessionFilesFromEvidence) and the risky-access governance
// walker (governance.collectAccesses) reach the JSONL surface with no change. The
// full input and result bodies ride on the Operation, never on the event.
const eventTypeToolCall = "tool_call"

// transcriptTaskBoundaryReason is the shared indeterminate task-boundary reason
// stamped on every transcript tool Operation (MCP and generic alike): a bare
// tool_use record carries no reviewed task-boundary signal, so the confidence is
// left unknown rather than fabricated. Defined once so the two operation shapers
// do not re-spell it (SonarCloud duplication is a hard merge blocker).
const transcriptTaskBoundaryReason = "Claude Code transcript tool_use records have no reviewed task-boundary signal"

// transcriptContentEnvelope decodes only the scalar envelope plus the message
// content array needed to reconstruct a tool call from a transcript line. Every
// other transcript field is left undeclared, so no unrelated body is read here.
type transcriptContentEnvelope struct {
	UUID        string                 `json:"uuid"`
	ParentUUID  *string                `json:"parentUuid"`
	SessionID   string                 `json:"sessionId"`
	Timestamp   string                 `json:"timestamp"`
	Version     string                 `json:"version"`
	IsSidechain *bool                  `json:"isSidechain"`
	Message     *transcriptContentBody `json:"message"`
}

// transcriptContentBody holds the raw content payload. It is decoded as
// json.RawMessage because message.content is an array for assistant/tool records
// but a bare string for ordinary user text — only the array form carries
// tool_use/tool_result blocks.
type transcriptContentBody struct {
	Content json.RawMessage `json:"content"`
}

// transcriptContentBlock decodes the content-block shapes the transcript tool-call
// and message-content paths read: a tool_use invocation (id/name/input), its
// paired tool_result (tool_use_id/content/is_error), an assistant thinking block
// (thinking), and assistant/user text (text). Blocks of any other type decode to
// an empty struct and are skipped.
type transcriptContentBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   *bool           `json:"is_error"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
}

// transcriptToolCall is one tool call reconstructed from a transcript: the
// tool_use invocation (an assistant record) paired with its tool_result (a later
// user record) by tool_use_id. category classifies the tool via the shared
// operationCategory helper (MCP call for mcp__* names, filesystem/shell/network
// for the built-ins, unknown otherwise). server/toolName are populated only for a
// well-formed MCP name. A call whose result is absent (still in flight when the
// transcript shipped) keeps outcome "unknown" rather than a fabricated value.
type transcriptToolCall struct {
	sessionID   string
	uuid        string
	parentUUID  *string
	isSidechain bool
	occurredAt  time.Time
	version     string
	category    canonical.OperationCategory
	fullName    string
	server      string // MCP only
	toolName    string // MCP only
	toolUseID   string
	input       any
	result      any
	outcome     string
}

// toolCallCollector accumulates tool_use invocations across a transcript walk and
// pairs each with its later tool_result by tool_use_id. It preserves first-seen
// order so the reconstructed calls are deterministic before CorrelateOperations /
// CorrelateEvents sort them. The map is keyed by tool_use_id: if the same id
// appears on both a main-line and a sub-agent (sidechain) record the last-seen
// invocation wins, which keeps a duplicate id deterministic rather than emitting
// two conflicting records.
type toolCallCollector struct {
	order []string
	calls map[string]*transcriptToolCall
}

func newToolCallCollector() *toolCallCollector {
	return &toolCallCollector{calls: map[string]*transcriptToolCall{}}
}

// collectToolUses records every tool_use block in an assistant record, MCP and
// generic alike (J18/#105 dropped the MCP-only filter J17 shipped with). The
// record's structural fields are already validated by assistantEvent before this
// runs, so a timestamp parse failure here is treated as a skip rather than a
// second hard error. Each call is classified with the shared operationCategory
// helper so there is a single tool classifier, not a second one here.
func (c *toolCallCollector) collectToolUses(line []byte) {
	envelope, blocks, ok := decodeContentBlocks(line)
	if !ok {
		return
	}
	occurredAt, err := time.Parse(time.RFC3339, strings.TrimSpace(envelope.Timestamp))
	if err != nil {
		return
	}
	sessionID := normalize.ProviderNativeSessionID(nativeSessionPrefix, strings.TrimSpace(envelope.SessionID))
	for _, block := range blocks {
		if block.Type != "tool_use" || strings.TrimSpace(block.ID) == "" {
			continue
		}
		server, toolName, isMCP := parseMCPToolName(block.Name)
		category := operationCategory(block.Name, nil)
		if category == canonical.OperationCategoryMCPCall && !isMCP {
			// A malformed mcp__ name (prefix present, server/tool separator absent)
			// is captured as a generic unknown tool call rather than a broken
			// mcp_call with an empty server — still captured, never dropped (#87).
			category = canonical.OperationCategoryUnknown
		}
		if _, exists := c.calls[block.ID]; !exists {
			c.order = append(c.order, block.ID)
		}
		c.calls[block.ID] = &transcriptToolCall{
			sessionID:   sessionID,
			uuid:        strings.TrimSpace(envelope.UUID),
			parentUUID:  trimmedParentUUID(envelope.ParentUUID),
			isSidechain: envelope.IsSidechain != nil && *envelope.IsSidechain,
			occurredAt:  occurredAt.UTC(),
			version:     envelope.Version,
			category:    category,
			fullName:    block.Name,
			server:      server,
			toolName:    toolName,
			toolUseID:   block.ID,
			input:       decodeRawContent(block.Input),
			outcome:     "unknown",
		}
	}
}

// collectToolResults pairs each tool_result block with a pending tool_use of the
// same tool_use_id. A result for a tool_use never seen is ignored, so only
// observed invocations gain an outcome and result body.
func (c *toolCallCollector) collectToolResults(line []byte) {
	_, blocks, ok := decodeContentBlocks(line)
	if !ok {
		return
	}
	for _, block := range blocks {
		if block.Type != "tool_result" || strings.TrimSpace(block.ToolUseID) == "" {
			continue
		}
		call, pending := c.calls[block.ToolUseID]
		if !pending {
			continue
		}
		call.result = decodeRawContent(block.Content)
		call.outcome = toolResultBlockOutcome(block.IsError)
	}
}

// reconstructed returns the collected tool calls in first-seen order.
func (c *toolCallCollector) reconstructed() []transcriptToolCall {
	calls := make([]transcriptToolCall, 0, len(c.order))
	for _, id := range c.order {
		calls = append(calls, *c.calls[id])
	}
	return calls
}

// decodeContentBlocks decodes a transcript line's envelope and its message.content
// array. ok is false when the line has no decodable content array (a bare-string
// user message, or a record with no message), so callers skip it without error.
func decodeContentBlocks(line []byte) (transcriptContentEnvelope, []transcriptContentBlock, bool) {
	var envelope transcriptContentEnvelope
	if err := json.Unmarshal(line, &envelope); err != nil {
		return transcriptContentEnvelope{}, nil, false
	}
	if envelope.Message == nil || len(envelope.Message.Content) == 0 {
		return transcriptContentEnvelope{}, nil, false
	}
	var blocks []transcriptContentBlock
	if err := json.Unmarshal(envelope.Message.Content, &blocks); err != nil {
		return transcriptContentEnvelope{}, nil, false
	}
	return envelope, blocks, true
}

// decodeRawContent decodes a tool_use input or tool_result body into a generic
// value so it round-trips deterministically under provider_extensions (epic #87 —
// tool arguments and results are captured raw). It decodes with UseNumber so a
// large integer argument (e.g. an id or offset beyond a float64's 53-bit
// mantissa) round-trips exactly as a json.Number rather than being rounded through
// float64 — silent precision loss would be a raw-capture violation. An absent or
// unparseable payload yields nil rather than a fabricated empty object.
func decodeRawContent(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil
	}
	return value
}

// toolResultBlockOutcome maps a tool_result is_error flag to a stable outcome. An
// absent flag stays "unknown" (the result was not observed) rather than a
// fabricated success/failed.
func toolResultBlockOutcome(isError *bool) string {
	if isError == nil {
		return "unknown"
	}
	if *isError {
		return "failed"
	}
	return "success"
}

// correlationEvent builds the content-free correlation event for a tool call,
// branching once on category so an MCP call keeps the byte-stable mcp_call shape
// (jsonl_mcp.go) the MCP-inventory insight reads, while every other tool emits the
// generic tool_call event. Neither carries the arguments or result body — those
// ride on the Operation.
func (c transcriptToolCall) correlationEvent(receivedAt time.Time) canonical.Event {
	if c.category == canonical.OperationCategoryMCPCall {
		return c.mcpCorrelationEvent(receivedAt)
	}
	return c.toolCorrelationEvent(receivedAt)
}

// operation builds the canonical Operation for a tool call, branching once on
// category so an MCP call keeps the byte-stable mcp_call operation shape while
// every other tool emits the generic tool_call operation.
func (c transcriptToolCall) operation() canonical.Operation {
	if c.category == canonical.OperationCategoryMCPCall {
		return c.mcpOperation()
	}
	return c.toolOperation()
}

// toolCorrelationEvent builds the generic content-free tool_call event. Its
// attributes.tool block mirrors traces.go:toolAttributes key names so the
// Files-lane and risky-access consumers pick up the raw file_path/full_command
// with no change. The raw input and result body ride on toolOperation instead.
func (c transcriptToolCall) toolCorrelationEvent(receivedAt time.Time) canonical.Event {
	eventID := c.sessionID + ":toolcall:" + c.toolUseID
	extensions := map[string]any{
		"correlation": transcriptCorrelation(eventID, c.occurredAt, c.uuid, c.parentUUID),
	}
	// A sub-agent (sidechain) tool_use self-identifies via the same
	// provider_extensions.transcript.is_sidechain marker the assistant_message event
	// carries, so a consumer can attribute the call to sub-agent work directly rather
	// than joining back through the assistant record (correlation.parent_uuid still
	// carries the DAG linkage regardless).
	if c.isSidechain {
		extensions["transcript"] = map[string]any{"is_sidechain": true}
	}
	return canonical.Event{
		SchemaVersion: canonicalSchemaVersion,
		EventID:       eventID,
		EventType:     eventTypeToolCall,
		OccurredAt:    c.occurredAt,
		ReceivedAt:    receivedAt.UTC(),
		Provider:      provider,
		Tool:          tool,
		SourceSchema:  sourceSchemaTranscript,
		SourceVersion: fallbackString(c.version, unavailable),
		ActorID:       unavailable,
		DeviceID:      unavailable,
		SessionID:     c.sessionID,
		PrivacyLevel:  "operational",
		Attributes: map[string]any{
			"category":           string(c.category),
			"tool":               c.toolEventAttributes(),
			"unavailable_fields": toolCallUnavailableFields(),
		},
		ProviderExtensions: extensions,
	}
}

// toolEventAttributes builds the content-free attributes.tool block: the tool
// identity plus, when the input carries them, the raw file_path/full_command/
// subagent_type under the same key names OTLP tool spans use (traces.go:
// toolAttributes). Only these scalars are lifted onto the event; the full input
// body stays on the Operation.
func (c transcriptToolCall) toolEventAttributes() map[string]any {
	block := map[string]any{
		"tool_name":   c.fullName,
		"tool_use_id": c.toolUseID,
	}
	input, ok := c.input.(map[string]any)
	if !ok {
		return block
	}
	if path := firstString(input, "file_path"); path != "" {
		block["file_path"] = path
	}
	if command := firstString(input, "command"); command != "" {
		block["full_command"] = command
	}
	if subagent := firstString(input, "subagent_type"); subagent != "" {
		block["subagent_type"] = subagent
	}
	return block
}

// toolOperation builds the canonical Operation for a generic (non-MCP) tool call.
// The operation ID shares the :tool:<tool_use_id> namespace with the OTLP
// tool_result operation, so the same call observed on both ingest routes
// correlates to one record rather than double-counting. The raw input and result
// ride under provider_extensions.tool_call (epic #87 — captured raw); an Edit/Write
// diff is the raw old_string/new_string/content already inside tool_call.input, and
// a TodoWrite snapshot is the raw todos array already inside tool_call.input, so no
// second copy of those bytes is emitted under another key. provider_extensions.event
// mirrors the mcp_call operation's event block (tool_name/tool_use_id) so the
// Files-lane matcher (insights.operationToolUseID) pairs the operation to its event.
func (c transcriptToolCall) toolOperation() canonical.Operation {
	operationID := c.sessionID + ":tool:" + c.toolUseID
	event := map[string]any{"tool_name": c.fullName, "tool_use_id": c.toolUseID}
	// Carry the sub-agent linkage onto the operation itself so a sidechain tool call
	// self-identifies without a multi-hop join through the events (the MCP operation
	// shape stays byte-stable — this is the generic path only).
	if c.parentUUID != nil {
		event["parent_uuid"] = *c.parentUUID
	}
	if c.isSidechain {
		event["is_sidechain"] = true
	}
	extensions := map[string]any{
		"correlation": operationCorrelation(operationID, c.occurredAt, transcriptTaskBoundaryReason),
		"event":       event,
	}
	call := map[string]any{}
	if c.input != nil {
		call["input"] = c.input
	}
	if c.result != nil {
		call["result"] = c.result
	}
	if len(call) > 0 {
		extensions["tool_call"] = call
	}
	return canonical.Operation{
		SchemaVersion:      canonical.RecordSchemaVersion,
		OperationID:        operationID,
		SessionID:          c.sessionID,
		Provider:           provider,
		Tool:               tool,
		Category:           c.category,
		Outcome:            c.outcome,
		Provenance:         canonical.ProvenanceObserved,
		ProviderExtensions: extensions,
	}
}

// toolCallUnavailableFields lists the behaviour signals a generic tool_call
// correlation event does not carry. It is the mcp_call list minus file_operations:
// unlike the mcp_call event, the tool_call event does carry file evidence
// (attributes.tool.file_path / full_command), so file_operations is not absent.
// Derived from mcpCallUnavailableFields rather than re-spelling the shared list.
func toolCallUnavailableFields() []string {
	return removeUnavailableField(mcpCallUnavailableFields(), "file_operations")
}
