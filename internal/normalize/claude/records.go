package claude

import (
	"fmt"
	"strconv"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// promotedRecordFields are the sample-event keys mapped onto a typed
// ModelInteraction. They are excluded from provider_extensions.event so
// evidence is not duplicated between the typed record and its extensions.
// cache_creation_tokens is deliberately absent: the canonical record has no
// cache-creation field, so it is preserved verbatim under provider_extensions
// rather than conflated with cache_read_tokens.
var promotedRecordFields = []string{"event_name", "event_timestamp", "event_sequence", "session_id", "request_id", "model", "input_tokens", "output_tokens", "cache_read_tokens", "duration_ms"}

// ExtractModelInteractions maps api_request sample events into stable-primitive
// canonical.ModelInteraction records. Only signals the P2 Claude Code
// capability matrix marks supported/partial are extracted: model identity,
// input/output tokens, and cache-read tokens. Reasoning tokens, tool calls, and
// task outcome are left nil/"unknown", never fabricated. mcp_server_connection
// events are not tool-call invocations and yield no Operation record.
//
// completed_at is the observed api_request event timestamp; started_at is
// derived as completed_at minus the observed duration_ms and labelled as such
// under provider_extensions, so it is never mistaken for a directly observed
// endpoint.
func ExtractModelInteractions(data []byte) ([]canonical.ModelInteraction, error) {
	document, _, err := decodeDocument(data)
	if err != nil {
		return nil, err
	}
	switch document.Payload.SourceType {
	case sourceTypeOTLPEvents:
	case sourceTypeCapabilityProbe:
		return []canonical.ModelInteraction{}, nil
	default:
		return nil, fmt.Errorf("unsupported Claude payload source_type %q", document.Payload.SourceType)
	}
	var records []canonical.ModelInteraction
	for _, raw := range document.Payload.SampleEvents {
		interaction, ok, err := sampleModelInteraction(raw)
		if err != nil {
			return nil, err
		}
		if ok {
			records = append(records, interaction)
		}
	}
	return normalize.CorrelateModelInteractions(records), nil
}

func sampleModelInteraction(raw map[string]any) (canonical.ModelInteraction, bool, error) {
	if name, _ := raw["event_name"].(string); name != eventAPIRequest {
		return canonical.ModelInteraction{}, false, nil
	}
	model, modelObserved := normalize.ObservedString(raw["model"])
	inputTokens := normalize.OptionalTokenCount(raw["input_tokens"])
	outputTokens := normalize.OptionalTokenCount(raw["output_tokens"])
	if !modelObserved && inputTokens == nil && outputTokens == nil {
		return canonical.ModelInteraction{}, false, nil
	}
	sessionID, err := normalize.RequiredString(raw, "session_id")
	if err != nil {
		return canonical.ModelInteraction{}, false, err
	}
	completed, err := eventTime(raw, "event_timestamp")
	if err != nil {
		return canonical.ModelInteraction{}, false, err
	}

	nativeSessionID := normalize.ProviderNativeSessionID("claude-code:", sessionID)
	requestID := nativeSessionID + ":" + sequenceSuffix(raw, completed)
	if rawRequest := normalize.OptionalString(raw, "request_id"); rawRequest != nil {
		requestID = "claude-code:" + *rawRequest
	}

	duration := normalize.OptionalTokenCount(raw["duration_ms"])
	started, startedDerived := completed, false
	if duration != nil {
		started = completed.Add(-time.Duration(*duration) * time.Millisecond)
		startedDerived = true
	}

	result := "success"
	var errorCode *string
	extensions := recordExtensions(raw, requestID, started, startedDerived)
	attachOutcomeContract(extensions, raw, eventAPIRequest)
	interaction := canonical.ModelInteraction{
		SchemaVersion:      canonical.RecordSchemaVersion,
		RequestID:          requestID,
		SessionID:          nativeSessionID,
		Provider:           provider,
		Tool:               tool,
		Model:              model,
		StartedAt:          started,
		CompletedAt:        completed,
		DurationMs:         duration,
		InputTokens:        inputTokens,
		OutputTokens:       outputTokens,
		CachedInputTokens:  normalize.OptionalTokenCount(raw["cache_read_tokens"]),
		ReasoningTokens:    nil,
		Result:             result,
		ErrorCode:          errorCode,
		Provenance:         normalize.InteractionProvenance(modelObserved, inputTokens, outputTokens),
		ProviderExtensions: extensions,
	}
	return interaction, true, nil
}

func recordExtensions(raw map[string]any, requestID string, started time.Time, startedDerived bool) map[string]any {
	startedProvenance := "observed_equals_completed"
	if startedDerived {
		startedProvenance = "derived_from_duration"
	}
	return map[string]any{
		"correlation": map[string]any{
			"dedup_key":    requestID,
			"ordering_key": fmt.Sprintf("%020d:%s", started.UnixNano(), requestID),
			"task_boundary": map[string]any{
				"confidence": "unknown",
				"reason":     "Claude Code event telemetry has no reviewed task-boundary signal",
			},
		},
		"timestamps": map[string]any{
			"completed_at": "observed",
			"started_at":   startedProvenance,
		},
		"event": normalize.UnknownFields(raw, promotedRecordFields...),
	}
}

// sequenceSuffix renders the request-ID suffix used when no request_id is
// observed. It prefers the integral event_sequence, falling back to the
// observed completed timestamp so two api_request events in one session that
// both lack request_id and event_sequence stay distinct rather than colliding
// on a constant suffix and being silently deduplicated.
func sequenceSuffix(raw map[string]any, completed time.Time) string {
	if number, ok := raw["event_sequence"].(float64); ok && number == float64(int64(number)) {
		return strconv.FormatInt(int64(number), 10)
	}
	return "ts" + strconv.FormatInt(completed.UnixNano(), 10)
}
