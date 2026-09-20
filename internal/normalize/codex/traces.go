package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// ErrUnsupportedTraces indicates that a valid OTLP traces payload contains no
// resource emitted by an observed Codex service.
var ErrUnsupportedTraces = errors.New("unsupported Codex traces payload")

var codexTraceTokenFields = map[string]string{
	"codex.turn.token_usage.input_tokens":             "input_token_count",
	"codex.turn.token_usage.cached_input_tokens":      "cached_input_token_count",
	"codex.turn.token_usage.cache_write_input_tokens": "cache_write_input_token_count",
	"codex.turn.token_usage.output_tokens":            "output_token_count",
	"codex.turn.token_usage.reasoning_output_tokens":  "reasoning_token_count",
	"codex.turn.token_usage.total_tokens":             "total_token_count",
}

// NormalizeTraces maps the Codex CLI 0.154.0 OTLP trace surface into canonical
// events. The adapter accepts only the observed codex_exec and codex_cli_rs
// resource identities. Foreign resources return ErrUnsupportedTraces; malformed
// spans under a recognized resource are hard errors so /v1/traces can never
// acknowledge and silently drop supported Codex telemetry.
func NormalizeTraces(data []byte, receivedAt time.Time) ([]canonical.Event, error) {
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode Codex OTLP traces: %w", err)
	}
	resources, ok := payload["resourceSpans"].([]any)
	if !ok {
		return nil, ErrUnsupportedTraces
	}

	var events []canonical.Event
	for resourceIndex, value := range resources {
		resource, ok := value.(map[string]any)
		if !ok {
			continue
		}
		resourceAttributes := traceResourceAttributes(resource)
		if !isCodexLogService(resourceAttributes[codexServiceNameKey]) {
			continue
		}
		resourceEvents, err := normalizeLiveTraceResource(resourceIndex, resource, resourceAttributes, receivedAt.UTC())
		if err != nil {
			return nil, err
		}
		events = append(events, resourceEvents...)
	}
	if len(events) == 0 {
		return nil, ErrUnsupportedTraces
	}
	return normalize.CorrelateEvents(events), nil
}

func normalizeLiveTraceResource(resourceIndex int, resource, resourceAttributes map[string]any, receivedAt time.Time) ([]canonical.Event, error) {
	scopes, ok := resource["scopeSpans"].([]any)
	if !ok || len(scopes) == 0 {
		return nil, fmt.Errorf("codex resourceSpans[%d].scopeSpans must be a non-empty array", resourceIndex)
	}
	version := stringValue(resourceAttributes["service.version"], unavailable)
	var events []canonical.Event
	for scopeIndex, value := range scopes {
		scopeEvents, err := normalizeLiveTraceScope(resourceIndex, scopeIndex, value, resource, resourceAttributes, version, receivedAt)
		if err != nil {
			return nil, err
		}
		events = append(events, scopeEvents...)
	}
	return events, nil
}

func normalizeLiveTraceScope(resourceIndex, scopeIndex int, value any, resource, resourceAttributes map[string]any, version string, receivedAt time.Time) ([]canonical.Event, error) {
	scope, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("codex resourceSpans[%d].scopeSpans[%d] must be an object", resourceIndex, scopeIndex)
	}
	spans, ok := scope["spans"].([]any)
	if !ok || len(spans) == 0 {
		return nil, fmt.Errorf("codex resourceSpans[%d].scopeSpans[%d].spans must be a non-empty array", resourceIndex, scopeIndex)
	}
	events := make([]canonical.Event, 0, len(spans))
	for spanIndex, value := range spans {
		span, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("codex resourceSpans[%d].scopeSpans[%d].spans[%d] must be an object", resourceIndex, scopeIndex, spanIndex)
		}
		event, err := normalizeLiveTraceSpan(resource, scope, span, resourceAttributes, version, receivedAt)
		if err != nil {
			return nil, fmt.Errorf("normalise Codex span %d/%d/%d: %w", resourceIndex, scopeIndex, spanIndex, err)
		}
		events = append(events, event)
	}
	return events, nil
}

func normalizeLiveTraceSpan(resource, scope, span, resourceAttributes map[string]any, version string, receivedAt time.Time) (canonical.Event, error) {
	fields, err := requiredTraceSpanFields(span)
	if err != nil {
		return canonical.Event{}, err
	}
	spanAttributes := traceAttributeValues(span["attributes"])
	attributes := codexTraceAttributes(spanAttributes)
	sessionID, sessionIDSource := codexTraceSessionIdentity(resourceAttributes, fields.traceID)

	return canonical.Event{
		SchemaVersion: canonicalSchemaVersion,
		EventID:       fields.eventID,
		EventType:     fields.name,
		OccurredAt:    fields.occurredAt,
		ReceivedAt:    receivedAt,
		Provider:      "openai",
		Tool:          "codex",
		SourceSchema:  sourceSchema,
		SourceVersion: version,
		ActorID:       unavailable,
		DeviceID:      unavailable,
		SessionID:     sessionID,
		PrivacyLevel:  "operational",
		Attributes:    attributes,
		ProviderExtensions: map[string]any{
			"correlation":         liveTraceCorrelation(fields, sessionID, sessionIDSource),
			"resource_attributes": resourceAttributes,
			"resource":            normalize.UnknownFields(resource, "scopeSpans"),
			"scope":               normalize.UnknownFields(scope, "spans"),
			"span":                normalize.UnknownFields(span, "traceId", "spanId", "parentSpanId", "name", "startTimeUnixNano", "attributes"),
			"span_attributes":     spanAttributes,
		},
	}, nil
}

// codexTraceSessionIdentity promotes only the provider-emitted resource-level
// conversation.id observed alongside Codex logs in CLI 0.155.1. It is the same
// raw provider value used by log normalisation, so both surfaces share a
// session only when the provider supplied the exact identifier. All other
// traces remain trace-scoped observations.
func codexTraceSessionIdentity(resourceAttributes map[string]any, traceID string) (string, string) {
	if conversationID, ok := normalize.ObservedString(resourceAttributes["conversation.id"]); ok {
		return normalize.ProviderNativeSessionID("codex:", conversationID), "conversation.id"
	}
	return "codex:trace:" + traceID, "trace.id"
}

func codexTraceAttributes(fields map[string]any) map[string]any {
	unavailableFields := []string{"model", "token_usage", "cache_usage", "tool_calls", "file_operations", "command_execution", "approvals", "prompt_content", "response_content", "repository_context", "task_outcome", "provider_cost"}
	result := map[string]any{}
	hasTokens := false
	hasCache := false
	for source, target := range codexTraceTokenFields {
		value := normalize.OptionalTokenCount(fields[source])
		if value == nil {
			continue
		}
		result[target] = *value
		hasTokens = true
		if target == "cached_input_token_count" || target == "cache_write_input_token_count" {
			hasCache = true
		}
	}
	if hasTokens {
		unavailableFields = removeTraceUnavailable(unavailableFields, "token_usage")
	}
	if hasCache {
		unavailableFields = removeTraceUnavailable(unavailableFields, "cache_usage")
	}
	result["unavailable_fields"] = unavailableFields
	return result
}

func removeTraceUnavailable(values []string, target string) []string {
	result := make([]string, 0, len(values)-1)
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}

func liveTraceCorrelation(fields traceSpanFields, sessionID, sessionIDSource string) map[string]any {
	correlation := traceCorrelation(fields.eventID, fields.traceID, fields.spanID, fields.parentSpanID, fields.occurredAt)
	correlation["session_id_source"] = sessionIDSource
	if sessionIDSource == "conversation.id" {
		correlation["provider_session_id"] = strings.TrimPrefix(sessionID, "codex:")
	}
	return correlation
}

func traceResourceAttributes(resource map[string]any) map[string]any {
	raw, ok := resource["resource"].(map[string]any)
	if !ok {
		return nil
	}
	return traceAttributeValues(raw["attributes"])
}

func traceAttributeValues(raw any) map[string]any {
	values, ok := raw.([]any)
	if !ok {
		return map[string]any{}
	}
	result := make(map[string]any, len(values))
	for _, value := range values {
		entry, ok := value.(map[string]any)
		if !ok {
			continue
		}
		key, ok := entry["key"].(string)
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		wrapped, ok := entry["value"].(map[string]any)
		if !ok {
			continue
		}
		if field, found := traceAttributeValue(wrapped); found {
			result[key] = field
		}
	}
	return result
}

func traceAttributeValue(wrapped map[string]any) (any, bool) {
	for _, valueKey := range []string{"stringValue", "intValue", "boolValue", "doubleValue", "arrayValue", "kvlistValue", "bytesValue"} {
		if field, found := wrapped[valueKey]; found {
			return field, true
		}
	}
	if len(wrapped) > 0 {
		return wrapped, true
	}
	return nil, false
}
