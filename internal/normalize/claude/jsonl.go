package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// ErrMalformedTranscript marks a JSON syntax failure in a transcript line so the
// ingest route can return HTTP 400 (matching OTLP malformed_payload) rather than
// conflating it with a structural normalisation failure (HTTP 422).
var ErrMalformedTranscript = errors.New("malformed claude transcript")

// sourceSchemaTranscript marks events derived from the on-disk session JSONL
// transcript (~/.claude/projects/**/<session>.jsonl), distinguishing them from
// the OTLP paths (sourceSchema = "otel") while they still correlate into the
// same session by session_id (F4, #91).
const sourceSchemaTranscript = "session_jsonl"

// eventTypeAssistantMessage is the F4 record type carried end-to-end: the only
// transcript line that carries the model and full token usage, mirroring the
// OTLP api_request/ModelInteraction slice.
const eventTypeAssistantMessage = "assistant_message"

// eventTypeUserMessage is the correlation event emitted for a content-bearing user
// record (J18/#105): it carries the raw prompt / slash-command-expanded content so
// the prompt surface is captured from the transcript, not only from OTLP (#94).
const eventTypeUserMessage = "user_message"

// transcriptRecord decodes the envelope shared by conversation records. Only the
// scalar fields F4 promotes or allow-lists are declared; content-bearing nested
// shapes (message.content[], toolUseResult, …) are deliberately absent so they
// are never read into a canonical event (owned by E7 #94 / J18 #105). Fields are
// read from the record body — no filename or path is passed to the normaliser,
// so correlation is the in-record sessionId, not the on-disk file stem.
type transcriptRecord struct {
	Type          string            `json:"type"`
	UUID          string            `json:"uuid"`
	ParentUUID    *string           `json:"parentUuid"`
	SessionID     string            `json:"sessionId"`
	Timestamp     string            `json:"timestamp"`
	Version       string            `json:"version"`
	GitBranch     string            `json:"gitBranch"`
	Entrypoint    string            `json:"entrypoint"`
	UserType      string            `json:"userType"`
	RequestID     string            `json:"requestId"`
	Effort        string            `json:"effort"`
	APIBlockIndex *int64            `json:"apiBlockIndex"`
	IsSidechain   *bool             `json:"isSidechain"`
	Message       *assistantMessage `json:"message"`
}

// assistantMessage decodes only the model and usage from an assistant record's
// message. content (the response/tool bodies) is intentionally not declared.
type assistantMessage struct {
	Model string          `json:"model"`
	Usage *assistantUsage `json:"usage"`
}

// assistantUsage decodes the numeric token counts. json.Number preserves large
// integers exactly rather than coercing them through float64 before
// normalize.OptionalTokenCount parses them (a genuinely absent field stays "" and
// yields nil, distinguishable from a real 0).
type assistantUsage struct {
	InputTokens              json.Number `json:"input_tokens"`
	OutputTokens             json.Number `json:"output_tokens"`
	CacheCreationInputTokens json.Number `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     json.Number `json:"cache_read_input_tokens"`
	OutputTokensDetails      *struct {
		ThinkingTokens json.Number `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
	CacheCreation *struct {
		Ephemeral1hInputTokens json.Number `json:"ephemeral_1h_input_tokens"`
		Ephemeral5mInputTokens json.Number `json:"ephemeral_5m_input_tokens"`
	} `json:"cache_creation"`
}

// transcriptContentFields are the content/body signals a transcript event may not
// carry; they are listed as unavailable so an absent signal is explicit rather
// than silently missing. Per-event shapers prune an entry when that event does
// carry the signal: the assistant event drops response_content when it carries the
// response text (J18/#105), the user event drops prompt_content when it carries the
// prompt (J18/#105), and the tool_call/mcp_call events carry their own lists.
var transcriptContentFields = []string{
	"prompt_content",
	"response_content",
	"tool_io",
	"mcp_calls",
	"file_operations",
	"repository_context",
	"provider_cost",
}

// NormalizeTranscript maps a Claude Code session JSONL transcript (newline-
// delimited JSON, the real on-disk format) into canonical events, one per
// assistant record — the only line carrying the model and token usage, mirroring
// the OTLP api_request slice. Every event correlates to the same session as the
// OTLP logs/metrics/traces via ProviderNativeSessionID over the in-record
// sessionId.
//
// The type set is open: user, system, and the ~12 auxiliary metadata types are
// decoded only far enough to read .type and then skipped, so an unrecognised or
// content-heavy out-of-scope record can never fail F4. An assistant record
// missing a structural field (uuid, sessionId, timestamp) is a hard error — a
// malformed supported record aborts the whole import rather than being silently
// dropped or half-persisted (mirrors the traces adapter). A missing version is
// reported as source_version "unavailable", not an error. A transcript with no
// assistant records yields an empty slice and no error.
//
// Lines are walked one at a time (no bytes.Split) so a newline-dense 32 MiB body
// cannot amplify into hundreds of MiB of slice headers before parsing starts.
//
// The per-adapter allow-list is the sole guard (epic #88 removed ingest-time
// hiding): only safe scalar envelope fields are copied into provider_extensions,
// and no content body is ever read.
func NormalizeTranscript(data []byte, receivedAt time.Time) ([]canonical.Event, error) {
	events, calls, err := walkTranscript(data, receivedAt)
	if err != nil {
		return nil, err
	}
	// Each reconstructed tool call also emits a content-free correlation event: an
	// mcp_call event so the MCP-inventory insight can mark a connected server as
	// actually used (#104), or a generic tool_call event carrying the raw
	// file_path/full_command for the Files-lane and risky-access consumers (#105).
	// The invocation detail (arguments/result) rides on the Operation instead.
	for _, call := range calls {
		events = append(events, call.correlationEvent(receivedAt))
	}
	return normalize.CorrelateEvents(events), nil
}

// walkTranscript is the single-pass reader shared by the transcript event path
// (NormalizeTranscript) and the operation path (ExtractTranscriptOperations). It
// walks the NDJSON one line at a time (no bytes.Split, so a newline-dense 32 MiB
// body cannot amplify into slice headers), emits an event per assistant record and
// per content-bearing user record, and reconstructs every tool call by pairing an
// assistant tool_use with its later user tool_result by tool_use_id. A malformed
// line or a malformed supported (assistant) record aborts the whole walk rather
// than being silently dropped.
func walkTranscript(data []byte, receivedAt time.Time) ([]canonical.Event, []transcriptToolCall, error) {
	var events []canonical.Event
	collector := newToolCallCollector()
	lineNum := 0
	for remaining := data; len(remaining) > 0; {
		lineNum++
		var line []byte
		line, remaining = nextTranscriptLine(remaining)
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		// Decode only far enough to read the discriminator, so a content-heavy
		// out-of-scope record's nested shape is never validated.
		var discriminator struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(trimmed, &discriminator); err != nil {
			return nil, nil, fmt.Errorf("%w: line %d is not valid JSON", ErrMalformedTranscript, lineNum)
		}
		event, ok, err := collector.dispatchTranscriptRecord(discriminator.Type, trimmed, receivedAt)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			events = append(events, event)
		}
	}
	return events, collector.reconstructed(), nil
}

// nextTranscriptLine splits off the next newline-delimited line from remaining,
// returning the line and the bytes that follow it (nil once the last line is
// consumed). Kept separate so walkTranscript stays within the cognitive-complexity
// budget without a bytes.Split allocation over a newline-dense body.
func nextTranscriptLine(remaining []byte) (line, rest []byte) {
	if i := bytes.IndexByte(remaining, '\n'); i >= 0 {
		return remaining[:i], remaining[i+1:]
	}
	return remaining, nil
}

// dispatchTranscriptRecord routes one already-discriminated transcript line: an
// assistant record yields an event and collects its tool_uses; a content-bearing
// user record yields an event, and every user record collects its tool_results. The
// bool reports whether an event was produced; auxiliary record types produce none.
func (c *toolCallCollector) dispatchTranscriptRecord(recordType string, trimmed []byte, receivedAt time.Time) (canonical.Event, bool, error) {
	switch recordType {
	case "assistant":
		event, err := assistantEvent(trimmed, receivedAt)
		if err != nil {
			return canonical.Event{}, false, err
		}
		c.collectToolUses(trimmed)
		return event, true, nil
	case "user":
		c.collectToolResults(trimmed)
		event, ok := userEvent(trimmed, receivedAt)
		return event, ok, nil
	}
	return canonical.Event{}, false, nil
}

// assistantEvent maps one assistant transcript record into a canonical event.
func assistantEvent(line []byte, receivedAt time.Time) (canonical.Event, error) {
	var record transcriptRecord
	if err := json.Unmarshal(line, &record); err != nil {
		return canonical.Event{}, fmt.Errorf("%w: decode assistant record: %v", ErrMalformedTranscript, err)
	}
	uuid := strings.TrimSpace(record.UUID)
	sessionIDRaw := strings.TrimSpace(record.SessionID)
	timestamp := strings.TrimSpace(record.Timestamp)
	if uuid == "" || sessionIDRaw == "" || timestamp == "" {
		return canonical.Event{}, fmt.Errorf("claude assistant record missing uuid, sessionId, or timestamp")
	}
	occurredAt, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return canonical.Event{}, fmt.Errorf("claude assistant record %q timestamp must be RFC3339", uuid)
	}
	occurredAt = occurredAt.UTC()

	sessionID := normalize.ProviderNativeSessionID(nativeSessionPrefix, sessionIDRaw)
	// The per-line uuid makes the event ID deterministic, so a SessionEnd hook
	// re-shipping a now-complete transcript fills in earlier gaps idempotently
	// via INSERT OR IGNORE rather than duplicating events.
	eventID := sessionID + ":" + uuid

	thinking, responseText := assistantMessageContent(line)
	attributes := map[string]any{
		"unavailable_fields": assistantUnavailableFields(responseText != ""),
	}
	var model string
	var modelObserved bool
	if record.Message != nil {
		model, modelObserved = normalize.ObservedString(record.Message.Model)
		if modelObserved {
			attributes["model"] = model
		}
		if record.Message.Usage != nil {
			addTokenCounts(attributes, record.Message.Usage)
		}
	}

	// The assistant record's reasoning (thinking) and response text are captured
	// raw on the event (epic #87 — nothing dropped); the response supersedes the
	// former "response_content → E7" deferral now that the transcript carries it.
	envelope := transcriptEnvelope(record)
	if thinking != "" {
		envelope["thinking"] = thinking
	}
	if responseText != "" {
		envelope["response_content"] = responseText
	}
	extensions := map[string]any{
		"correlation": transcriptCorrelation(eventID, occurredAt, uuid, trimmedParentUUID(record.ParentUUID)),
		"transcript":  envelope,
	}
	if extra := cacheExtraTokens(record.Message); len(extra) > 0 {
		extensions["cache_usage_extra"] = extra
	}

	return canonical.Event{
		SchemaVersion:      canonicalSchemaVersion,
		EventID:            eventID,
		EventType:          eventTypeAssistantMessage,
		OccurredAt:         occurredAt,
		ReceivedAt:         receivedAt.UTC(),
		Provider:           provider,
		Tool:               tool,
		SourceSchema:       sourceSchemaTranscript,
		SourceVersion:      fallbackString(record.Version, unavailable),
		ActorID:            unavailable,
		DeviceID:           unavailable,
		SessionID:          sessionID,
		PrivacyLevel:       "operational",
		Attributes:         attributes,
		ProviderExtensions: extensions,
	}, nil
}

// assistantMessageContent decodes an assistant record's message.content array and
// returns its reasoning (thinking) blocks and response (text) blocks, each joined
// with a newline when a record carries several. A record with no decodable content
// array (or none of these block types) yields empty strings, so a content-free
// assistant record adds nothing.
func assistantMessageContent(line []byte) (thinking, response string) {
	_, blocks, ok := decodeContentBlocks(line)
	if !ok {
		return "", ""
	}
	var thinkingParts, responseParts []string
	for _, block := range blocks {
		switch block.Type {
		case "thinking":
			if strings.TrimSpace(block.Thinking) != "" {
				thinkingParts = append(thinkingParts, block.Thinking)
			}
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				responseParts = append(responseParts, block.Text)
			}
		}
	}
	return strings.Join(thinkingParts, "\n"), strings.Join(responseParts, "\n")
}

// assistantUnavailableFields lists the content signals an assistant event does not
// carry. response_content is pruned when the event carries the response text, so an
// absent signal is explicit while a present one is not falsely reported missing.
func assistantUnavailableFields(hasResponse bool) []string {
	if !hasResponse {
		return append([]string(nil), transcriptContentFields...)
	}
	return removeUnavailableField(transcriptContentFields, "response_content")
}

// userEvent maps one content-bearing user transcript record into a canonical
// user_message event carrying the raw prompt / slash-command-expanded content
// (J18/#105). ok is false — the record is skipped without error — when the record
// is unparseable, lacks a structural field, or carries no text content (a pure
// tool_result turn, whose body rides on the paired tool Operation instead). User
// records are auxiliary, so a malformed one never aborts the import (unlike a
// malformed assistant record); a real user record always carries these fields.
func userEvent(line []byte, receivedAt time.Time) (canonical.Event, bool) {
	var record transcriptRecord
	if err := json.Unmarshal(line, &record); err != nil {
		return canonical.Event{}, false
	}
	uuid := strings.TrimSpace(record.UUID)
	sessionIDRaw := strings.TrimSpace(record.SessionID)
	timestamp := strings.TrimSpace(record.Timestamp)
	if uuid == "" || sessionIDRaw == "" || timestamp == "" {
		return canonical.Event{}, false
	}
	content := transcriptUserContent(line)
	if content == "" {
		return canonical.Event{}, false
	}
	occurredAt, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return canonical.Event{}, false
	}
	occurredAt = occurredAt.UTC()

	sessionID := normalize.ProviderNativeSessionID(nativeSessionPrefix, sessionIDRaw)
	eventID := sessionID + ":" + uuid

	envelope := transcriptEnvelope(record)
	envelope["prompt_content"] = content

	return canonical.Event{
		SchemaVersion: canonicalSchemaVersion,
		EventID:       eventID,
		EventType:     eventTypeUserMessage,
		OccurredAt:    occurredAt,
		ReceivedAt:    receivedAt.UTC(),
		Provider:      provider,
		Tool:          tool,
		SourceSchema:  sourceSchemaTranscript,
		SourceVersion: fallbackString(record.Version, unavailable),
		ActorID:       unavailable,
		DeviceID:      unavailable,
		SessionID:     sessionID,
		PrivacyLevel:  "operational",
		Attributes: map[string]any{
			"unavailable_fields": userMessageUnavailableFields(),
		},
		ProviderExtensions: map[string]any{
			"correlation": transcriptCorrelation(eventID, occurredAt, uuid, trimmedParentUUID(record.ParentUUID)),
			"transcript":  envelope,
		},
	}, true
}

// userMessageUnavailableFields lists the content signals a user event does not
// carry. prompt_content is pruned because the user event carries the raw prompt.
func userMessageUnavailableFields() []string {
	return removeUnavailableField(transcriptContentFields, "prompt_content")
}

// transcriptUserContent extracts the raw text content of a user record. content is
// a bare string for an ordinary or slash-command-expanded prompt, or an array of
// blocks for a tool turn — only text blocks are prompt content (a tool_result body
// rides on the paired tool Operation, not here). Several text blocks are joined
// with a newline. An empty or non-text-bearing record yields "".
func transcriptUserContent(line []byte) string {
	var envelope transcriptContentEnvelope
	if err := json.Unmarshal(line, &envelope); err != nil || envelope.Message == nil {
		return ""
	}
	raw := envelope.Message.Content
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var blocks []transcriptContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var texts []string
	for _, block := range blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			texts = append(texts, block.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// addTokenCounts stamps the numeric token signals under the same canonical
// attribute keys the metrics adapter uses (tokenUsageAttribute), so cross-tool
// token/cost logic reads one schema. Reasoning (thinking) tokens are captured
// here — they are a numeric behaviour signal, not a content body. An absent or
// unparseable count is simply omitted, never fabricated.
func addTokenCounts(attributes map[string]any, usage *assistantUsage) {
	if count := transcriptTokenCount(usage.InputTokens); count != nil {
		attributes["input_token_count"] = *count
	}
	if count := transcriptTokenCount(usage.OutputTokens); count != nil {
		attributes["output_token_count"] = *count
	}
	if count := transcriptTokenCount(usage.CacheReadInputTokens); count != nil {
		attributes["cached_input_token_count"] = *count
	}
	if count := transcriptTokenCount(usage.CacheCreationInputTokens); count != nil {
		attributes["cache_write_input_token_count"] = *count
	}
	if usage.OutputTokensDetails != nil {
		if count := transcriptTokenCount(usage.OutputTokensDetails.ThinkingTokens); count != nil {
			attributes["reasoning_token_count"] = *count
		}
	}
}

// cacheExtraTokens carries the ephemeral cache-window token counts that have no
// canonical attribute key, so the raw behaviour signal is retained without
// overloading the shared token schema.
func cacheExtraTokens(message *assistantMessage) map[string]any {
	if message == nil || message.Usage == nil || message.Usage.CacheCreation == nil {
		return nil
	}
	extra := map[string]any{}
	if count := transcriptTokenCount(message.Usage.CacheCreation.Ephemeral1hInputTokens); count != nil {
		extra["cache_creation.ephemeral_1h_input_tokens"] = *count
	}
	if count := transcriptTokenCount(message.Usage.CacheCreation.Ephemeral5mInputTokens); count != nil {
		extra["cache_creation.ephemeral_5m_input_tokens"] = *count
	}
	return extra
}

// transcriptTokenCount parses a json.Number token field. normalize.OptionalTokenCount
// reads a decoded-JSON string, so the json.Number is passed as its string form;
// an absent field (empty json.Number) yields nil.
func transcriptTokenCount(number json.Number) *int64 {
	return normalize.OptionalTokenCount(number.String())
}

// transcriptEnvelope reduces the assistant record to the allow-listed safe scalar
// envelope. cwd and every content body are deliberately excluded — the allow-list
// is the sole guard (#88), so only proven-safe behaviour scalars are carried.
func transcriptEnvelope(record transcriptRecord) map[string]any {
	envelope := map[string]any{}
	if record.GitBranch != "" {
		envelope["git_branch"] = record.GitBranch
	}
	if record.Entrypoint != "" {
		envelope["entrypoint"] = record.Entrypoint
	}
	if record.UserType != "" {
		envelope["user_type"] = record.UserType
	}
	if record.RequestID != "" {
		envelope["request_id"] = nativeSessionPrefix + record.RequestID
	}
	if record.Effort != "" {
		envelope["effort"] = record.Effort
	}
	if record.APIBlockIndex != nil {
		envelope["api_block_index"] = *record.APIBlockIndex
	}
	if record.IsSidechain != nil {
		envelope["is_sidechain"] = *record.IsSidechain
	}
	return envelope
}

// transcriptCorrelation carries the DAG linkage (uuid/parent_uuid) alongside the
// dedup/ordering keys, so downstream consumers can rebuild the conversation tree
// the same way the traces adapter carries trace/span/parent linkage.
func transcriptCorrelation(eventID string, occurredAt time.Time, uuid string, parentUUID *string) map[string]any {
	var parent any
	if parentUUID != nil {
		parent = *parentUUID
	}
	return map[string]any{
		"dedup_key":    eventID,
		"ordering_key": fmt.Sprintf("%020d:%s", occurredAt.UnixNano(), eventID),
		"uuid":         uuid,
		"parent_uuid":  parent,
		"task_boundary": map[string]any{
			"confidence": "unknown",
			"reason":     "Claude Code transcript records have no reviewed task-boundary signal",
		},
	}
}

// trimmedParentUUID returns a trimmed non-empty parent uuid, or nil when absent
// or whitespace-only — matching RequiredString's blank-is-missing contract.
func trimmedParentUUID(parent *string) *string {
	if parent == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*parent)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
