package claude

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// eventTypeMCPCall is the correlation event emitted for an MCP tool call observed
// in the session JSONL transcript. It carries the server/tool identity the
// MCP-inventory insight consumes to mark a connected server as actually used,
// matching the shape insights.mcpUseEvent expects (EventType "mcp_call").
const eventTypeMCPCall = "mcp_call"

// mcpContentEnvelope decodes only the scalar envelope plus the message content
// array needed to reconstruct an MCP tool call. Every other transcript field is
// left undeclared, so no unrelated content body is read (J18/#105 owns generic
// tool IO).
type mcpContentEnvelope struct {
	UUID       string          `json:"uuid"`
	ParentUUID *string         `json:"parentUuid"`
	SessionID  string          `json:"sessionId"`
	Timestamp  string          `json:"timestamp"`
	Version    string          `json:"version"`
	Message    *mcpContentBody `json:"message"`
}

// mcpContentBody holds the raw content payload. It is decoded as json.RawMessage
// because message.content is an array for assistant/tool records but a bare string
// for ordinary user text — only the array form carries tool_use/tool_result blocks.
type mcpContentBody struct {
	Content json.RawMessage `json:"content"`
}

// mcpContentBlock decodes the two content-block shapes #104 reads: a tool_use
// invocation (id/name/input) and its paired tool_result (tool_use_id/content/
// is_error). Blocks of any other type decode to an empty struct and are skipped.
type mcpContentBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   *bool           `json:"is_error"`
}

// mcpTranscriptCall is one MCP tool call reconstructed from a transcript: the
// tool_use invocation (an assistant record) paired with its tool_result (a later
// user record) by tool_use_id. A call whose result is absent (still in flight when
// the transcript shipped) keeps outcome "unknown" rather than a fabricated value.
type mcpTranscriptCall struct {
	sessionID  string
	uuid       string
	parentUUID *string
	occurredAt time.Time
	version    string
	fullName   string
	server     string
	toolName   string
	toolUseID  string
	arguments  any
	result     any
	outcome    string
}

// mcpCallCollector accumulates tool_use invocations across a transcript walk and
// pairs each with its later tool_result by tool_use_id. It preserves first-seen
// order so the reconstructed calls are deterministic before CorrelateOperations /
// CorrelateEvents sort them.
type mcpCallCollector struct {
	order []string
	calls map[string]*mcpTranscriptCall
}

func newMCPCallCollector() *mcpCallCollector {
	return &mcpCallCollector{calls: map[string]*mcpTranscriptCall{}}
}

// collectToolUses records every MCP-named tool_use block in an assistant record.
// The record's structural fields are already validated by assistantEvent before
// this runs, so a timestamp parse failure here is treated as a skip rather than a
// second hard error. Non-MCP tool_use blocks and any other content are ignored.
func (c *mcpCallCollector) collectToolUses(line []byte) {
	envelope, blocks, ok := decodeMCPContent(line)
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
		if !isMCP {
			continue
		}
		if _, exists := c.calls[block.ID]; !exists {
			c.order = append(c.order, block.ID)
		}
		c.calls[block.ID] = &mcpTranscriptCall{
			sessionID:  sessionID,
			uuid:       strings.TrimSpace(envelope.UUID),
			parentUUID: trimmedParentUUID(envelope.ParentUUID),
			occurredAt: occurredAt.UTC(),
			version:    envelope.Version,
			fullName:   block.Name,
			server:     server,
			toolName:   toolName,
			toolUseID:  block.ID,
			arguments:  decodeRawContent(block.Input),
			outcome:    "unknown",
		}
	}
}

// collectToolResults pairs each tool_result block with a pending MCP tool_use of
// the same tool_use_id. A result for a tool_use never seen (or a non-MCP one) is
// ignored, so only MCP invocations gain an outcome and result body.
func (c *mcpCallCollector) collectToolResults(line []byte) {
	_, blocks, ok := decodeMCPContent(line)
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

// reconstructed returns the collected MCP calls in first-seen order.
func (c *mcpCallCollector) reconstructed() []mcpTranscriptCall {
	calls := make([]mcpTranscriptCall, 0, len(c.order))
	for _, id := range c.order {
		calls = append(calls, *c.calls[id])
	}
	return calls
}

// decodeMCPContent decodes a transcript line's envelope and its message.content
// array. ok is false when the line has no decodable content array (a bare-string
// user message, or a record with no message), so callers skip it without error.
func decodeMCPContent(line []byte) (mcpContentEnvelope, []mcpContentBlock, bool) {
	var envelope mcpContentEnvelope
	if err := json.Unmarshal(line, &envelope); err != nil {
		return mcpContentEnvelope{}, nil, false
	}
	if envelope.Message == nil || len(envelope.Message.Content) == 0 {
		return mcpContentEnvelope{}, nil, false
	}
	var blocks []mcpContentBlock
	if err := json.Unmarshal(envelope.Message.Content, &blocks); err != nil {
		return mcpContentEnvelope{}, nil, false
	}
	return envelope, blocks, true
}

// decodeRawContent decodes a tool_use input or tool_result body into a generic
// value so it round-trips deterministically under provider_extensions (epic #87 —
// MCP arguments and results are captured raw for #104). An absent or unparseable
// payload yields nil rather than a fabricated empty object.
func decodeRawContent(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
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

// correlationEvent builds the canonical mcp_call event the MCP-inventory insight
// reads. It carries only the server/tool identity and correlation linkage — never
// the arguments or result body — so the content-free contract of the transcript
// event path (TestNormalizeTranscriptDoesNotEmitContent) holds; the raw arguments
// and result ride on the Operation instead.
func (c mcpTranscriptCall) correlationEvent(receivedAt time.Time) canonical.Event {
	eventID := c.sessionID + ":mcp:" + c.toolUseID
	call := mcpCallExtension(c.server, c.toolName)
	call["tool_use_id"] = c.toolUseID
	return canonical.Event{
		SchemaVersion: canonicalSchemaVersion,
		EventID:       eventID,
		EventType:     eventTypeMCPCall,
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
			"category":           string(canonical.OperationCategoryMCPCall),
			"unavailable_fields": mcpCallUnavailableFields(),
		},
		ProviderExtensions: map[string]any{
			"correlation": transcriptCorrelation(eventID, c.occurredAt, c.uuid, c.parentUUID),
			"mcp_call":    call,
		},
	}
}

// operation builds the canonical Operation for an MCP tool call. The operation ID
// shares the :tool:<tool_use_id> namespace with the OTLP tool_result operation, so
// the same call observed on both ingest routes correlates to one record rather
// than double-counting. The raw arguments and result ride under
// provider_extensions.mcp_call (epic #87 — #104 captures MCP arguments and results
// raw); an absent argument or result is omitted, never fabricated.
func (c mcpTranscriptCall) operation() canonical.Operation {
	operationID := c.sessionID + ":tool:" + c.toolUseID
	call := mcpCallExtension(c.server, c.toolName)
	call["tool_use_id"] = c.toolUseID
	if c.arguments != nil {
		call["arguments"] = c.arguments
	}
	if c.result != nil {
		call["result"] = c.result
	}
	return canonical.Operation{
		SchemaVersion: canonical.RecordSchemaVersion,
		OperationID:   operationID,
		SessionID:     c.sessionID,
		Provider:      provider,
		Tool:          tool,
		Category:      canonical.OperationCategoryMCPCall,
		Outcome:       c.outcome,
		Provenance:    canonical.ProvenanceObserved,
		ProviderExtensions: map[string]any{
			"correlation": operationCorrelation(operationID, c.occurredAt, "Claude Code transcript tool_use records have no reviewed task-boundary signal"),
			"event":       map[string]any{"tool_name": c.fullName, "tool_use_id": c.toolUseID},
			"mcp_call":    call,
		},
	}
}

// mcpCallUnavailableFields lists the behaviour signals an mcp_call correlation
// event does not carry, so an absent signal is explicit rather than silently
// missing. The event proves the MCP call happened (mcp_calls / tool_calls are
// available and so absent from this list); it carries no model, token, or content
// identity of its own — those live on the api_request and assistant records.
func mcpCallUnavailableFields() []string {
	return []string{
		"model",
		"token_usage",
		"cache_usage",
		"task_outcome",
		"reasoning_tokens",
		"file_operations",
		"repository_context",
		"prompt_content",
		"response_content",
		"provider_cost",
		"trace_span_correlation",
		"approvals",
	}
}

// ExtractTranscriptOperations reconstructs MCP tool-call Operations from a Claude
// Code session JSONL transcript (#104), the sibling of NormalizeTranscript's event
// path and a mirror of log_operations.go:ExtractLogOperations. It walks the
// transcript once, pairing each MCP tool_use with its tool_result by tool_use_id,
// and emits one MCP-call Operation per invocation. A transcript with no MCP calls
// yields an empty slice and no error.
func ExtractTranscriptOperations(data []byte, _ time.Time) ([]canonical.Operation, error) {
	_, calls, err := walkTranscript(data, time.Time{})
	if err != nil {
		return nil, err
	}
	operations := make([]canonical.Operation, 0, len(calls))
	for _, call := range calls {
		operations = append(operations, call.operation())
	}
	return normalize.CorrelateOperations(operations), nil
}
