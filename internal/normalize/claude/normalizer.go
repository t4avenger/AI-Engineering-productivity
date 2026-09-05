// Package claude normalises reviewed, sanitised Claude Code fixtures (OTLP
// HTTP/JSON log events) into canonical events and stable-primitive records.
//
// It is capability-bounded: only signals the P2 Claude Code capability matrix
// marks supported/partial are extracted; every absent signal is reported as an
// explicit unavailable/unknown state, never fabricated. Session and request
// identifiers are reduced to installation-specific HMAC fingerprints so a
// sensitive identifier is never retained verbatim, while correlation stays
// deterministic. Session JSONL is out of scope until a reviewed JSONL fixture
// is committed.
package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/wayne/telemetryiq/internal/fixture"
	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

const (
	canonicalSchemaVersion = "0.1.0"
	sourceSchema           = "otel"
	unavailable            = "unavailable"
	provider               = "anthropic"
	tool                   = "claude-code"

	sourceTypeOTLPEvents      = "otlp_http_json_logs"
	sourceTypeCapabilityProbe = "local_cli_capability_probe"

	eventAPIRequest = "api_request"
)

// NormalizeEvents maps the reviewed Claude Code OTLP event fixture into
// canonical events, one per sample event. It validates through the shared
// fixture boundary (fixture.Validate rejects prohibited fields and likely
// secrets) before mapping; it does not itself run the privacy sanitiser, so a
// live payload must be sanitised upstream first. It never persists or logs the
// fixture.
//
// A capability-probe fixture carries no events, so it yields an empty slice
// rather than a fabricated all-unknown record. session_id/request_id are
// fingerprinted, so the caller supplies the installation HMAC fingerprint.
func NormalizeEvents(data []byte, fingerprint func([]byte) string) ([]canonical.Event, error) {
	if fingerprint == nil {
		return nil, errors.New("claude fingerprint is required")
	}
	document, capturedAt, err := decodeDocument(data)
	if err != nil {
		return nil, err
	}
	switch document.Payload.SourceType {
	case sourceTypeOTLPEvents:
	case sourceTypeCapabilityProbe:
		return []canonical.Event{}, nil
	default:
		return nil, fmt.Errorf("unsupported Claude payload source_type %q", document.Payload.SourceType)
	}
	events := make([]canonical.Event, 0, len(document.Payload.SampleEvents))
	for index, raw := range document.Payload.SampleEvents {
		event, err := normaliseSampleEvent(document, capturedAt, fingerprint, index, raw)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return normalize.CorrelateEvents(events), nil
}

func normaliseSampleEvent(document fixtureDocument, capturedAt time.Time, fingerprint func([]byte) string, index int, raw map[string]any) (canonical.Event, error) {
	name, err := normalize.RequiredString(raw, "event_name")
	if err != nil {
		return canonical.Event{}, err
	}
	sessionID, err := normalize.RequiredString(raw, "session_id")
	if err != nil {
		return canonical.Event{}, err
	}
	occurredAt, err := eventTime(raw, "event_timestamp")
	if err != nil {
		return canonical.Event{}, err
	}
	sessionFingerprint := "claude-code:" + fingerprint([]byte(sessionID))
	eventID := sessionFingerprint + ":" + sequenceKey(raw, index)

	extensions := map[string]any{
		"correlation":     eventCorrelation(eventID, occurredAt),
		"skill_detection": unavailable,
		"event":           normalize.UnknownFields(raw, "event_name", "event_timestamp", "event_sequence", "session_id", "request_id"),
	}
	if requestID := normalize.OptionalString(raw, "request_id"); requestID != nil {
		extensions["request_fingerprint"] = "claude-code:" + fingerprint([]byte(*requestID))
	}
	return canonical.Event{
		SchemaVersion: canonicalSchemaVersion, EventID: eventID, EventType: name,
		OccurredAt: occurredAt, ReceivedAt: capturedAt, Provider: provider, Tool: tool,
		SourceSchema: sourceSchema, SourceVersion: document.ToolVersion, ActorID: unavailable, DeviceID: unavailable,
		SessionID: sessionFingerprint, TaskID: nil, RepositoryID: nil, PrivacyLevel: "operational",
		Attributes:         map[string]any{"unavailable_fields": unavailableFields(name)},
		ProviderExtensions: extensions,
	}, nil
}

// unavailableFields lists the behaviour signals a Claude Code event does not
// carry, so an absent signal is explicit rather than silently missing. The
// api_request event carries model and token identity; the connection event
// carries neither. Neither event proves an executed tool call, an MCP call
// invocation, a skill invocation, a file operation, or a task outcome.
func unavailableFields(eventName string) []string {
	common := []string{"tool_calls", "mcp_calls", "skill_invocations", "file_operations", "reasoning_tokens", "task_outcome", "repository_context", "prompt_content", "response_content", "provider_cost", "trace_span_correlation"}
	if eventName == eventAPIRequest {
		return common
	}
	return append([]string{"model", "token_usage", "cache_usage"}, common...)
}

func eventCorrelation(eventID string, occurredAt time.Time) map[string]any {
	return map[string]any{
		"dedup_key":    eventID,
		"ordering_key": fmt.Sprintf("%020d:%s", occurredAt.UnixNano(), eventID),
		"task_boundary": map[string]any{
			"confidence": "unknown",
			"reason":     "Claude Code event telemetry has no reviewed task-boundary signal",
		},
	}
}

// fixtureDocument is the reviewed Claude Code fixture wrapper. Sample events are
// kept as raw maps so unmapped safe fields survive verbatim in
// provider_extensions.
type fixtureDocument struct {
	Provider    string  `json:"provider"`
	Tool        string  `json:"tool"`
	ToolVersion string  `json:"tool_version"`
	CapturedAt  string  `json:"captured_at"`
	Payload     payload `json:"payload"`
}

type payload struct {
	SourceType   string           `json:"source_type"`
	SampleEvents []map[string]any `json:"sample_events"`
}

// decodeDocument validates the fixture through the shared boundary, then
// decodes and checks the provider/tool identity and captured_at timestamp.
func decodeDocument(data []byte) (fixtureDocument, time.Time, error) {
	if err := fixture.Validate(data); err != nil {
		return fixtureDocument{}, time.Time{}, fmt.Errorf("validate Claude fixture: %w", err)
	}
	var document fixtureDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return fixtureDocument{}, time.Time{}, errors.New("decode Claude fixture")
	}
	if document.Provider != provider || document.Tool != tool {
		return fixtureDocument{}, time.Time{}, errors.New("supported fixture provider and tool are anthropic and claude-code")
	}
	capturedAt, err := time.Parse(time.RFC3339, document.CapturedAt)
	if err != nil {
		return fixtureDocument{}, time.Time{}, errors.New("claude fixture captured_at must be RFC3339")
	}
	return document, capturedAt.UTC(), nil
}

// eventTime parses an RFC3339 event timestamp (fractional seconds allowed). A
// missing or invalid timestamp is rejected rather than defaulted, so a
// canonical event never carries a fabricated time.
func eventTime(value map[string]any, key string) (time.Time, error) {
	raw, err := normalize.RequiredString(value, key)
	if err != nil {
		return time.Time{}, err
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be an RFC3339 timestamp", key)
	}
	return parsed.UTC(), nil
}

// sequenceKey renders event_sequence as a stable integer key, falling back to
// the fixture ordinal when the sequence is absent, so an event ID is always
// deterministic.
func sequenceKey(value map[string]any, index int) string {
	if number, ok := value["event_sequence"].(float64); ok && number == float64(int64(number)) {
		return strconv.FormatInt(int64(number), 10)
	}
	return "idx" + strconv.Itoa(index)
}
