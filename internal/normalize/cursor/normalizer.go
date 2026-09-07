// Package cursor normalises reviewed, sanitised Cursor Agent fixtures into
// canonical events and stable-primitive records.
//
// It is capability-bounded per docs/integrations/capability-matrix.md: only the
// verified subset (session boundaries; token usage incl. cache-read; partial
// model identity; partial outcome contract) is promoted. Everything else remains
// unavailable/unknown and is never fabricated.
package cursor

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wayne/telemetryiq/internal/fixture"
	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

const (
	canonicalSchemaVersion = "0.1.0"
	sourceSchema           = "fixture"

	provider = "cursor"
	tool     = "cursor-agent"

	sourceTypePrintJSON       = "local_cli_print_json"
	sourceTypeStreamJSON      = "local_cli_stream_json"
	sourceTypeCapabilityProbe = "local_cli_capability_probe"

	unavailable = "unavailable"
)

// Normalize maps a reviewed Cursor Agent fixture wrapper into one canonical
// event for the result (or no events for a capability probe). It validates the
// fixture through the shared fixture boundary and does not persist or log it.
// The caller supplies an installation-scoped HMAC fingerprint function.
func Normalize(data []byte, fingerprint func([]byte) string) ([]canonical.Event, error) {
	if fingerprint == nil {
		return nil, errors.New("cursor fingerprint is required")
	}
	document, capturedAt, err := decodeDocument(data)
	if err != nil {
		return nil, err
	}
	switch document.Payload.SourceType {
	case sourceTypePrintJSON, sourceTypeStreamJSON:
		event, err := normaliseResult(document, capturedAt, fingerprint)
		if err != nil {
			return nil, err
		}
		return normalize.CorrelateEvents([]canonical.Event{event}), nil
	case sourceTypeCapabilityProbe:
		return []canonical.Event{}, nil
	default:
		return nil, fmt.Errorf("unsupported Cursor payload source_type %q", document.Payload.SourceType)
	}
}

type fixtureDocument struct {
	Provider    string  `json:"provider"`
	Tool        string  `json:"tool"`
	ToolVersion string  `json:"tool_version"`
	CapturedAt  string  `json:"captured_at"`
	Payload     payload `json:"payload"`
}

type payload struct {
	SourceType string         `json:"source_type"`
	Capture    map[string]any `json:"capture"`
	Init       map[string]any `json:"init"`
	Result     map[string]any `json:"result"`
}

func decodeDocument(data []byte) (fixtureDocument, time.Time, error) {
	if err := fixture.Validate(data); err != nil {
		return fixtureDocument{}, time.Time{}, fmt.Errorf("validate Cursor fixture: %w", err)
	}
	var document fixtureDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return fixtureDocument{}, time.Time{}, errors.New("decode Cursor fixture")
	}
	if document.Provider != provider || document.Tool != tool {
		return fixtureDocument{}, time.Time{}, errors.New("supported fixture provider and tool are cursor and cursor-agent")
	}
	capturedAt, err := time.Parse(time.RFC3339, document.CapturedAt)
	if err != nil {
		return fixtureDocument{}, time.Time{}, errors.New("cursor fixture captured_at must be RFC3339")
	}
	return document, capturedAt.UTC(), nil
}

func normaliseResult(document fixtureDocument, capturedAt time.Time, fingerprint func([]byte) string) (canonical.Event, error) {
	if document.Payload.Result == nil {
		return canonical.Event{}, errors.New("cursor payload.result must be present for result source_type")
	}
	sessionID, err := normalize.RequiredString(document.Payload.Result, "session_id")
	if err != nil {
		return canonical.Event{}, err
	}
	sessionFingerprint := "cursor-agent:" + fingerprint([]byte(sessionID))

	var eventID string
	if requestID := normalize.OptionalString(document.Payload.Result, "request_id"); requestID != nil {
		eventID = "cursor-agent:" + fingerprint([]byte(*requestID))
	} else {
		eventID = sessionFingerprint + ":result"
	}

	model := "unknown"
	modelObserved := false
	if document.Payload.SourceType == sourceTypeStreamJSON && document.Payload.Init != nil {
		model, modelObserved = normalize.ObservedString(document.Payload.Init["model"])
	}

	usage, ok := document.Payload.Result["usage"].(map[string]any)
	if !ok {
		usage = map[string]any{}
	}
	inputTokens := normalize.OptionalTokenCount(usage["inputTokens"])
	outputTokens := normalize.OptionalTokenCount(usage["outputTokens"])
	cacheReadTokens := normalize.OptionalTokenCount(usage["cacheReadTokens"])
	cacheWriteTokens := normalize.OptionalTokenCount(usage["cacheWriteTokens"])

	attributes := map[string]any{
		"unavailable_fields": unavailableFields(modelObserved, inputTokens, outputTokens, cacheReadTokens, document.Payload.Result),
	}
	if modelObserved {
		attributes["model"] = model
	}
	if inputTokens != nil {
		attributes["input_token_count"] = *inputTokens
	}
	if outputTokens != nil {
		attributes["output_token_count"] = *outputTokens
	}

	extensions := map[string]any{
		"correlation": eventCorrelation(eventID, capturedAt),
		"source_type": document.Payload.SourceType,
		"capture":     document.Payload.Capture,
		"result":      normalize.UnknownFields(document.Payload.Result, "type", "subtype", "is_error", "duration_ms", "duration_api_ms", "result_summary", "session_id", "request_id", "usage"),
	}
	if document.Payload.Init != nil {
		extensions["init"] = normalize.UnknownFields(document.Payload.Init, "type", "subtype", "model", "session_id")
	}

	extensions["cursor"] = map[string]any{
		"usage": map[string]any{
			"cache_read_tokens":  valueOrNull(cacheReadTokens),
			"cache_write_tokens": valueOrNull(cacheWriteTokens),
		},
	}

	attachOutcomeContract(extensions, document.Payload.Result, modelObserved, model, inputTokens, outputTokens)

	eventType := "result"
	if typed, ok := document.Payload.Result["type"].(string); ok && typed != "" {
		eventType = typed
	}

	return canonical.Event{
		SchemaVersion:      canonicalSchemaVersion,
		EventID:            eventID,
		EventType:          eventType,
		OccurredAt:         capturedAt,
		ReceivedAt:         capturedAt,
		Provider:           provider,
		Tool:               tool,
		SourceSchema:       sourceSchema,
		SourceVersion:      document.ToolVersion,
		ActorID:            unavailable,
		DeviceID:           unavailable,
		SessionID:          sessionFingerprint,
		TaskID:             nil,
		RepositoryID:       nil,
		PrivacyLevel:       "operational",
		Attributes:         attributes,
		ProviderExtensions: extensions,
	}, nil
}

func eventCorrelation(eventID string, occurredAt time.Time) map[string]any {
	return map[string]any{
		"dedup_key":    eventID,
		"ordering_key": fmt.Sprintf("%020d:%s", occurredAt.UnixNano(), eventID),
		"task_boundary": map[string]any{
			"confidence": "unknown",
			"reason":     "Cursor Agent fixtures have no reviewed task-boundary signal",
		},
	}
}

func unavailableFields(modelObserved bool, inputTokens, outputTokens, cacheReadTokens *int64, result map[string]any) []string {
	fields := []string{
		"tool_calls",
		"mcp_calls",
		"skill_invocations",
		"file_operations",
		"command_execution",
		"approvals",
		"reasoning_tokens",
		"repository_context",
		"prompt_content",
		"response_content",
		"provider_cost",
		"trace_span_correlation",
	}
	if !modelObserved {
		fields = append(fields, "model")
	}
	if inputTokens == nil && outputTokens == nil {
		fields = append(fields, "token_usage")
	}
	if cacheReadTokens == nil {
		fields = append(fields, "cache_usage")
	}
	if !hasOutcome(result) {
		fields = append(fields, "task_outcome")
	}
	return fields
}

func hasOutcome(result map[string]any) bool {
	if _, ok := result["is_error"].(bool); ok {
		return true
	}
	if subtype, ok := result["subtype"].(string); ok && subtype != "" {
		return true
	}
	return false
}

func valueOrNull(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func attachOutcomeContract(extensions map[string]any, result map[string]any, modelObserved bool, model string, inputTokens, outputTokens *int64) {
	status, ok := resultStatus(result)
	if !ok {
		return
	}
	contract := map[string]any{
		"source":     "provider_completion",
		"status":     status,
		"confidence": "observed",
	}
	if modelObserved {
		contract["model"] = model
	}
	if duration := normalize.OptionalTokenCount(result["duration_ms"]); duration != nil {
		contract["duration_ms"] = *duration
	}
	if inputTokens != nil {
		contract["input_tokens"] = *inputTokens
	}
	if outputTokens != nil {
		contract["output_tokens"] = *outputTokens
	}
	extensions["outcome_contract"] = contract
}

func resultStatus(result map[string]any) (string, bool) {
	if isError, ok := result["is_error"].(bool); ok {
		if isError {
			return "failed", true
		}
		// subtype may still carry cancelled/etc; fall through to subtype mapping.
	}
	subtype, ok := result["subtype"].(string)
	if !ok {
		return "", false
	}
	switch subtype {
	case "success", "ok":
		return "success", true
	case "cancelled", "canceled":
		return "cancelled", true
	case "error", "failed", "failure":
		return "failed", true
	default:
		return "unknown", true
	}
}
