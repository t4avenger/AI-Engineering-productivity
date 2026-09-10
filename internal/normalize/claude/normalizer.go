// Package claude normalises reviewed, sanitised Claude Code fixtures (OTLP
// HTTP/JSON log events) into canonical events and stable-primitive records.
//
// It is capability-bounded: only signals the P2 Claude Code capability matrix
// marks supported/partial are extracted; every absent signal is reported as an
// explicit unavailable/unknown state, never fabricated. Session and request
// identifiers are retained verbatim as provider-prefixed native IDs; no
// ingest-time hiding is applied (epic #87).
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

	eventAPIRequest     = "api_request"
	eventAPIError       = "api_error"
	eventSkillActivated = "skill_activated"
)

// NormalizeEvents maps the reviewed Claude Code OTLP event fixture into
// canonical events, one per sample event. It validates through the shared
// fixture boundary (fixture.Validate rejects prohibited fields and likely
// secrets) before mapping; it does not itself run the privacy sanitiser, so a
// live payload must be sanitised upstream first. It never persists or logs the
// fixture.
//
// A capability-probe fixture carries no events, so it yields an empty slice
// rather than a fabricated all-unknown record. Session and request identifiers
// are retained raw.
func NormalizeEvents(data []byte) ([]canonical.Event, error) {
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
		event, err := normaliseSampleEvent(document, capturedAt, index, raw)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return normalize.CorrelateEvents(events), nil
}

func normaliseSampleEvent(document fixtureDocument, capturedAt time.Time, index int, raw map[string]any) (canonical.Event, error) {
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
	nativeSessionID := normalize.ProviderNativeSessionID("claude-code:", sessionID)
	eventID := nativeSessionID + ":" + sequenceKey(raw, index)

	extensions := map[string]any{
		"correlation": eventCorrelation(eventID, occurredAt),
		"event":       normalize.UnknownFields(raw, promotedEventFields(name)...),
	}
	if requestID := normalize.OptionalString(raw, "request_id"); requestID != nil {
		extensions["request_id"] = "claude-code:" + *requestID
	}
	attachSkillDetection(extensions, raw, name)
	attachOutcomeContract(extensions, raw, name)
	return canonical.Event{
		SchemaVersion: canonicalSchemaVersion, EventID: eventID, EventType: name,
		OccurredAt: occurredAt, ReceivedAt: capturedAt, Provider: provider, Tool: tool,
		SourceSchema: sourceSchema, SourceVersion: document.ToolVersion, ActorID: unavailable, DeviceID: unavailable,
		SessionID: nativeSessionID, TaskID: nil, RepositoryID: nil, PrivacyLevel: "operational",
		Attributes:         map[string]any{"unavailable_fields": unavailableFields(name)},
		ProviderExtensions: extensions,
	}, nil
}

// attachSkillDetection stamps explicit skill identity on skill_activated events.
// Other events omit skill_detection so the insight reports unknown rather than a
// fabricated unavailable state for a surface we have not proven cannot carry skills.
func attachSkillDetection(extensions map[string]any, raw map[string]any, eventName string) {
	if eventName != eventSkillActivated {
		return
	}
	extensions["skill_detection"] = "explicit"
	skill := map[string]any{}
	if name := skillName(raw); name != "" {
		skill["name"] = name
	}
	if outcome := skillOutcome(raw); outcome != "" {
		skill["outcome"] = outcome
	}
	if trigger := normalize.OptionalString(raw, "invocation_trigger"); trigger != nil {
		skill["invocation_trigger"] = *trigger
	}
	if source := firstString(raw, "skill.source", "skill_source"); source != "" {
		skill["source"] = source
	}
	if len(skill) > 0 {
		extensions["skill"] = skill
	}
}

// attachOutcomeContract stamps a provider-completion outcome contract for
// reviewed Claude signals: api_request → success, api_error → failed. Broader
// task contracts (PR/revert/abandon) remain unobserved and are not fabricated.
func attachOutcomeContract(extensions map[string]any, raw map[string]any, eventName string) {
	var status string
	switch eventName {
	case eventAPIRequest:
		status = "success"
	case eventAPIError:
		status = "failed"
	default:
		return
	}
	contract := map[string]any{
		"source":     "provider_completion",
		"status":     status,
		"confidence": "observed",
	}
	if model := firstString(raw, "model"); model != "" {
		contract["model"] = model
	}
	if duration := normalize.OptionalTokenCount(raw["duration_ms"]); duration != nil {
		contract["duration_ms"] = *duration
	}
	if input := normalize.OptionalTokenCount(raw["input_tokens"]); input != nil {
		contract["input_tokens"] = *input
	}
	if output := normalize.OptionalTokenCount(raw["output_tokens"]); output != nil {
		contract["output_tokens"] = *output
	}
	if code := outcomeErrorCode(raw); code != "" {
		contract["error_code"] = code
	}
	if attempt := normalize.OptionalTokenCount(raw["attempt"]); attempt != nil && *attempt > 0 {
		contract["retry_attempt"] = *attempt
	}
	extensions["outcome_contract"] = contract
}

func outcomeErrorCode(raw map[string]any) string {
	if code := firstString(raw, "error", "error_code"); code != "" {
		return code
	}
	if status := normalize.OptionalTokenCount(raw["status_code"]); status != nil {
		return "http_" + strconv.FormatInt(*status, 10)
	}
	return ""
}

func skillName(raw map[string]any) string {
	return firstString(raw, "skill.name", "skill_name")
}

func skillOutcome(raw map[string]any) string {
	if status := firstString(raw, "skill.status", "skill_status"); status != "" {
		switch status {
		case "success", "ok":
			return "success"
		case "error", "failed", "failure":
			return "failed"
		default:
			return status
		}
	}
	return ""
}

func firstString(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := normalize.OptionalString(raw, key); value != nil && *value != "" {
			return *value
		}
	}
	return ""
}

func promotedEventFields(eventName string) []string {
	fields := []string{"event_name", "event_timestamp", "event_sequence", "session_id", "request_id"}
	if eventName == eventSkillActivated {
		return append(fields, "skill.name", "skill_name", "skill.status", "skill_status", "invocation_trigger", "skill.source", "skill_source")
	}
	return fields
}

// unavailableFields lists the behaviour signals a Claude Code event does not
// carry, so an absent signal is explicit rather than silently missing. The
// api_request event carries model and token identity; the connection and
// skill_activated events carry neither. Skill identity is reported only on
// skill_activated via skill_detection — it is never listed as unavailable on
// other events just because those events are not skill events.
func unavailableFields(eventName string) []string {
	common := []string{"tool_calls", "mcp_calls", "file_operations", "reasoning_tokens", "repository_context", "prompt_content", "response_content", "provider_cost", "trace_span_correlation"}
	switch eventName {
	case eventAPIRequest, eventAPIError:
		// Provider-completion outcome contracts are stamped for these events.
		return common
	default:
		return append([]string{"model", "token_usage", "cache_usage", "task_outcome"}, common...)
	}
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
