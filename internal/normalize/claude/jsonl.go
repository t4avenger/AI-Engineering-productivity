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

// transcriptContentFields are the content/body signals F4 deliberately does not
// emit; they are listed as unavailable so an absent signal is explicit rather
// than silently missing. Ownership: prompts/responses → E7 (#94); MCP calls →
// J17 (#104); tool IO / diffs / sub-agents → J18 (#105).
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
	// Each reconstructed MCP tool call also emits a correlation event so the
	// MCP-inventory insight can mark a connected server as actually used (#104);
	// the invocation detail (arguments/result) rides on the Operation instead.
	for _, call := range calls {
		events = append(events, call.correlationEvent(receivedAt))
	}
	return normalize.CorrelateEvents(events), nil
}

// walkTranscript is the single-pass reader shared by the transcript event path
// (NormalizeTranscript) and the operation path (ExtractTranscriptOperations). It
// walks the NDJSON one line at a time (no bytes.Split, so a newline-dense 32 MiB
// body cannot amplify into slice headers), emits an event per assistant record,
// and reconstructs every MCP tool call by pairing an assistant tool_use with its
// later user tool_result by tool_use_id. A malformed line or a malformed supported
// (assistant) record aborts the whole walk rather than being silently dropped.
func walkTranscript(data []byte, receivedAt time.Time) ([]canonical.Event, []mcpTranscriptCall, error) {
	var events []canonical.Event
	collector := newMCPCallCollector()
	lineNum := 0
	for remaining := data; len(remaining) > 0; {
		lineNum++
		var line []byte
		if i := bytes.IndexByte(remaining, '\n'); i >= 0 {
			line = remaining[:i]
			remaining = remaining[i+1:]
		} else {
			line = remaining
			remaining = nil
		}
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
		switch discriminator.Type {
		case "assistant":
			event, err := assistantEvent(trimmed, receivedAt)
			if err != nil {
				return nil, nil, err
			}
			events = append(events, event)
			collector.collectToolUses(trimmed)
		case "user":
			collector.collectToolResults(trimmed)
		}
	}
	return events, collector.reconstructed(), nil
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

	attributes := map[string]any{
		"unavailable_fields": append([]string(nil), transcriptContentFields...),
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

	extensions := map[string]any{
		"correlation": transcriptCorrelation(eventID, occurredAt, uuid, trimmedParentUUID(record.ParentUUID)),
		"transcript":  transcriptEnvelope(record),
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
			"reason":     "Claude Code transcript assistant records have no reviewed task-boundary signal",
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
