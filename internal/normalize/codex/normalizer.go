// Package codex normalises validated, sanitised Codex OTLP trace fixtures.
package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/fixture"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

const (
	canonicalSchemaVersion = "0.1.0"
	sourceSchema           = "otel"
	unavailable            = "unavailable"
)

// Normalize converts the supported OTLP trace fixture shape into canonical
// events. It does not persist or log the fixture.
func Normalize(data []byte) ([]canonical.Event, error) {
	if err := fixture.Validate(data); err != nil {
		return nil, fmt.Errorf("validate Codex fixture: %w", err)
	}

	var document fixtureDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, errors.New("decode Codex fixture")
	}
	capturedAt, err := time.Parse(time.RFC3339, document.CapturedAt)
	if err != nil {
		return nil, errors.New("codex fixture captured_at must be RFC3339")
	}
	if document.Provider != "openai" || document.Tool != "codex" {
		return nil, errors.New("supported fixture provider and tool are openai and codex")
	}

	resourceSpans, ok := document.Payload["resourceSpans"].([]any)
	if !ok || len(resourceSpans) == 0 {
		return nil, errors.New("supported Codex fixture requires non-empty payload.resourceSpans")
	}
	events, err := normaliseResourceSpans(document, capturedAt.UTC(), resourceSpans)
	if err != nil {
		return nil, err
	}
	return correlateEvents(events), nil
}

func normaliseResourceSpans(document fixtureDocument, capturedAt time.Time, resourceSpans []any) ([]canonical.Event, error) {
	var events []canonical.Event
	for resourceIndex, resourceValue := range resourceSpans {
		resource, ok := resourceValue.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("payload.resourceSpans[%d] must be an object", resourceIndex)
		}
		resourceEvents, err := normaliseResource(document, capturedAt, resourceIndex, resource)
		if err != nil {
			return nil, err
		}
		events = append(events, resourceEvents...)
	}
	return events, nil
}

func normaliseResource(document fixtureDocument, capturedAt time.Time, resourceIndex int, resource map[string]any) ([]canonical.Event, error) {
	scopeSpans, ok := resource["scopeSpans"].([]any)
	if !ok || len(scopeSpans) == 0 {
		return nil, fmt.Errorf("payload.resourceSpans[%d].scopeSpans must be a non-empty array", resourceIndex)
	}
	var events []canonical.Event
	for scopeIndex, scopeValue := range scopeSpans {
		scope, ok := scopeValue.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("payload.resourceSpans[%d].scopeSpans[%d] must be an object", resourceIndex, scopeIndex)
		}
		scopeEvents, err := normaliseScope(document, capturedAt, resourceIndex, scopeIndex, resource, scope)
		if err != nil {
			return nil, err
		}
		events = append(events, scopeEvents...)
	}
	return events, nil
}

func normaliseScope(document fixtureDocument, capturedAt time.Time, resourceIndex, scopeIndex int, resource, scope map[string]any) ([]canonical.Event, error) {
	spans, ok := scope["spans"].([]any)
	if !ok || len(spans) == 0 {
		return nil, fmt.Errorf("payload.resourceSpans[%d].scopeSpans[%d].spans must be a non-empty array", resourceIndex, scopeIndex)
	}
	events := make([]canonical.Event, 0, len(spans))
	for spanIndex, spanValue := range spans {
		span, ok := spanValue.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("payload.resourceSpans[%d].scopeSpans[%d].spans[%d] must be an object", resourceIndex, scopeIndex, spanIndex)
		}
		event, err := normaliseSpan(document, capturedAt, resource, scope, span)
		if err != nil {
			return nil, fmt.Errorf("normalise span %d/%d/%d: %w", resourceIndex, scopeIndex, spanIndex, err)
		}
		events = append(events, event)
	}
	return events, nil
}

type fixtureDocument struct {
	Provider    string         `json:"provider"`
	Tool        string         `json:"tool"`
	ToolVersion string         `json:"tool_version"`
	CapturedAt  string         `json:"captured_at"`
	Payload     map[string]any `json:"payload"`
}

func normaliseSpan(document fixtureDocument, capturedAt time.Time, resource, scope, span map[string]any) (canonical.Event, error) {
	traceID, err := requiredString(span, "traceId")
	if err != nil {
		return canonical.Event{}, err
	}
	spanID, err := requiredString(span, "spanId")
	if err != nil {
		return canonical.Event{}, err
	}
	name, err := requiredString(span, "name")
	if err != nil {
		return canonical.Event{}, err
	}
	occurredAt, err := unixNanoTime(span, "startTimeUnixNano")
	if err != nil {
		return canonical.Event{}, err
	}
	parentSpanID := optionalString(span, "parentSpanId")
	eventID := "codex:" + traceID + ":" + spanID
	return canonical.Event{
		SchemaVersion: canonicalSchemaVersion, EventID: eventID, EventType: name,
		OccurredAt: occurredAt, ReceivedAt: capturedAt, Provider: document.Provider, Tool: document.Tool,
		SourceSchema: sourceSchema, SourceVersion: document.ToolVersion, ActorID: unavailable, DeviceID: unavailable,
		SessionID: "codex:" + traceID, TaskID: nil, RepositoryID: nil, PrivacyLevel: "operational",
		Attributes: map[string]any{"unavailable_fields": []string{"model", "token_usage", "cache_usage", "tool_calls", "file_operations", "command_execution", "approvals", "prompt_content", "response_content", "repository_context", "task_outcome", "provider_cost"}},
		ProviderExtensions: map[string]any{
			"correlation": traceCorrelation(eventID, traceID, spanID, parentSpanID, occurredAt),
			"resource":    unknownFields(resource, "scopeSpans"),
			"scope":       unknownFields(scope, "spans"),
			"span":        unknownFields(span, "traceId", "spanId", "parentSpanId", "name", "startTimeUnixNano"),
		},
	}, nil
}

func correlateEvents(events []canonical.Event) []canonical.Event {
	ordered := append([]canonical.Event(nil), events...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return eventLess(ordered[i], ordered[j])
	})
	seen := make(map[string]struct{}, len(ordered))
	correlated := make([]canonical.Event, 0, len(ordered))
	for _, event := range ordered {
		if _, ok := seen[event.EventID]; ok {
			continue
		}
		seen[event.EventID] = struct{}{}
		correlated = append(correlated, event)
	}
	return correlated
}

func eventLess(left, right canonical.Event) bool {
	if !left.OccurredAt.Equal(right.OccurredAt) {
		return left.OccurredAt.Before(right.OccurredAt)
	}
	if left.EventID != right.EventID {
		return left.EventID < right.EventID
	}
	if left.EventType != right.EventType {
		return left.EventType < right.EventType
	}
	return left.ReceivedAt.Before(right.ReceivedAt)
}

func traceCorrelation(eventID, traceID, spanID string, parentSpanID *string, occurredAt time.Time) map[string]any {
	var parent any
	if parentSpanID != nil {
		parent = *parentSpanID
	}
	return map[string]any{
		"dedup_key":      eventID,
		"ordering_key":   fmt.Sprintf("%020d:%s", occurredAt.UnixNano(), eventID),
		"trace_id":       traceID,
		"span_id":        spanID,
		"parent_span_id": parent,
		"task_boundary": map[string]any{
			"confidence": "unknown",
			"reason":     "Codex trace telemetry has no reviewed task-boundary signal",
		},
	}
}

func requiredString(value map[string]any, key string) (string, error) {
	stringValue, ok := value[key].(string)
	if !ok || strings.TrimSpace(stringValue) == "" {
		return "", fmt.Errorf("%s must be a non-empty string", key)
	}
	return stringValue, nil
}

func optionalString(value map[string]any, key string) *string {
	stringValue, ok := value[key].(string)
	if !ok {
		return nil
	}
	stringValue = strings.TrimSpace(stringValue)
	if stringValue == "" {
		return nil
	}
	return &stringValue
}

func unixNanoTime(value map[string]any, key string) (time.Time, error) {
	raw, err := requiredString(value, key)
	if err != nil {
		return time.Time{}, err
	}
	nanoseconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || nanoseconds <= 0 {
		return time.Time{}, fmt.Errorf("%s must be a positive Unix-nanoseconds string", key)
	}
	return time.Unix(0, nanoseconds).UTC(), nil
}

func unknownFields(value map[string]any, knownKeys ...string) map[string]any {
	known := make(map[string]struct{}, len(knownKeys))
	for _, key := range knownKeys {
		known[key] = struct{}{}
	}
	unknown := make(map[string]any)
	for key, fieldValue := range value {
		if _, found := known[key]; !found {
			unknown[key] = fieldValue
		}
	}
	return unknown
}
