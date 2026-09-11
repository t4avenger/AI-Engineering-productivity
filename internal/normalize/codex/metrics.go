package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// ErrUnsupportedMetrics indicates a valid OTLP metrics payload is not the
// observed Codex metrics shape (or carries no skill-injection datapoints this
// adapter persists).
var ErrUnsupportedMetrics = errors.New("unsupported Codex metrics payload")

const (
	skillInjectedMetric     = "codex.skill.injected"
	skillTurnDurationMetric = "codex.skill.turn.duration_seconds"
	turnTokenUsageMetric    = "codex.turn.token_usage"
	serviceNameAttribute    = "service.name"
	serviceVersionAttribute = "service.version"
)

type metricsPayload struct {
	ResourceMetrics []resourceMetric `json:"resourceMetrics"`
}

type resourceMetric struct {
	Resource struct {
		Attributes []attribute `json:"attributes"`
	} `json:"resource"`
	ScopeMetrics []scopeMetric `json:"scopeMetrics"`
}

type scopeMetric struct {
	Metrics []otlpMetric `json:"metrics"`
}

type otlpMetric struct {
	Name      string           `json:"name"`
	Sum       *metricSum       `json:"sum"`
	Histogram *metricHistogram `json:"histogram"`
}

type metricSum struct {
	DataPoints []metricDataPoint `json:"dataPoints"`
}

type metricDataPoint struct {
	Attributes        []attribute `json:"attributes"`
	AsInt             any         `json:"asInt"`
	TimeUnixNano      string      `json:"timeUnixNano"`
	StartTimeUnixNano string      `json:"startTimeUnixNano"`
}

type metricHistogram struct {
	DataPoints []histogramDataPoint `json:"dataPoints"`
}

type histogramDataPoint struct {
	Attributes   []attribute `json:"attributes"`
	Count        any         `json:"count"`
	Sum          any         `json:"sum"`
	TimeUnixNano string      `json:"timeUnixNano"`
}

// NormalizeMetrics maps reviewed Codex OTLP metrics into canonical events.
// codex.skill.injected datapoints with a skill name become explicit skill
// records; codex.skill.turn.duration_seconds histogram datapoints become
// inferred skill-detection coverage only; codex.turn.token_usage histogram
// datapoints become token-usage events. Other metrics are ignored so exporters
// can POST a full metrics batch without inventing unrelated insight rows.
// Resources whose service.name is not a Codex log/exec service are skipped.
func NormalizeMetrics(data []byte, receivedAt time.Time) ([]canonical.Event, error) {
	var payload metricsPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode Codex OTLP metrics: %w", err)
	}
	var events []canonical.Event
	for _, resource := range payload.ResourceMetrics {
		extracted, err := skillEventsFromResource(resource, receivedAt)
		if err != nil {
			return nil, err
		}
		events = append(events, extracted...)
	}
	if len(events) == 0 {
		return nil, ErrUnsupportedMetrics
	}
	return normalize.CorrelateEvents(events), nil
}

func skillEventsFromResource(resource resourceMetric, receivedAt time.Time) ([]canonical.Event, error) {
	resourceAttrs := attributes(resource.Resource.Attributes)
	if !isCodexLogService(resourceAttrs[serviceNameAttribute]) {
		return nil, nil
	}
	version := stringValue(resourceAttrs[serviceVersionAttribute], unavailable)
	var events []canonical.Event
	for _, scope := range resource.ScopeMetrics {
		for _, item := range scope.Metrics {
			extracted, err := skillEventsFromMetric(resourceAttrs, version, item, receivedAt)
			if err != nil {
				return nil, err
			}
			events = append(events, extracted...)
		}
	}
	return events, nil
}

func skillEventsFromMetric(resourceAttrs map[string]any, version string, item otlpMetric, receivedAt time.Time) ([]canonical.Event, error) {
	if item.Name == skillTurnDurationMetric && item.Histogram != nil {
		return skillTurnEventsFromHistogram(resourceAttrs, version, item.Histogram, receivedAt), nil
	}
	if item.Name == turnTokenUsageMetric && item.Histogram != nil {
		return tokenUsageEventsFromHistogram(resourceAttrs, version, item.Histogram, receivedAt), nil
	}
	if item.Name != skillInjectedMetric || item.Sum == nil {
		return nil, nil
	}
	var events []canonical.Event
	for index, point := range item.Sum.DataPoints {
		event, ok, err := skillInjectedEvent(resourceAttrs, version, point, index, receivedAt)
		if err != nil {
			return nil, err
		}
		if ok {
			events = append(events, event)
		}
	}
	return events, nil
}

func tokenUsageEventsFromHistogram(resourceAttrs map[string]any, version string, histogram *metricHistogram, receivedAt time.Time) []canonical.Event {
	events := make([]canonical.Event, 0, len(histogram.DataPoints))
	for index, point := range histogram.DataPoints {
		if event, ok := tokenUsageEvent(resourceAttrs, version, point, index, receivedAt); ok {
			events = append(events, event)
		}
	}
	return events
}

func tokenUsageEvent(resource map[string]any, version string, point histogramDataPoint, index int, receivedAt time.Time) (canonical.Event, bool) {
	fields := attributes(point.Attributes)
	tokenType := strings.TrimSpace(stringValue(fields["token_type"], ""))
	attributeKey, ok := tokenUsageAttribute(tokenType)
	if !ok {
		return canonical.Event{}, false
	}
	tokens := normalize.OptionalTokenCount(point.Sum)
	if tokens == nil {
		return canonical.Event{}, false
	}
	occurredAt := metricTime(point.TimeUnixNano, receivedAt)
	model, modelObserved := normalize.ObservedString(fields["model"])
	identity := fmt.Sprintf("%s|%s|%s|%s|%d|%d", turnTokenUsageMetric, model, tokenType, point.TimeUnixNano, index, *tokens)
	eventID := contentID("codex:token:", []byte(identity))

	attributes := map[string]any{
		"unavailable_fields": []string{"tool_calls", "file_operations", "command_execution", "approvals", "prompt_content", "response_content", "repository_context", "task_outcome", "provider_cost", "session_lifecycle"},
		attributeKey:         *tokens,
	}
	if modelObserved {
		attributes["model"] = model
	}
	extensions := map[string]any{
		"correlation": tokenCorrelation(eventID, occurredAt),
		"metric": map[string]any{
			"name":       turnTokenUsageMetric,
			"token_type": tokenType,
			"count":      metricCount(point.Count),
		},
		"resource":          normalize.UnknownFields(resource, serviceNameAttribute, serviceVersionAttribute),
		"metric_attributes": safeCodexLogAttributes(normalize.UnknownFields(fields, "model", "token_type")),
	}
	return tokenEvent(eventID, occurredAt, receivedAt, version, attributes, extensions), true
}

func tokenUsageAttribute(tokenType string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(tokenType)) {
	case "input":
		return "input_token_count", true
	case "output":
		return "output_token_count", true
	case "cached_input":
		return "cached_input_token_count", true
	case "cache_write_input":
		return "cache_write_input_token_count", true
	case "reasoning_output":
		return "reasoning_token_count", true
	case "total":
		return "total_token_count", true
	default:
		return "", false
	}
}

func tokenEvent(eventID string, occurredAt, receivedAt time.Time, version string, attributes, extensions map[string]any) canonical.Event {
	return canonical.Event{
		SchemaVersion:      canonicalSchemaVersion,
		EventID:            eventID,
		EventType:          turnTokenUsageMetric,
		OccurredAt:         occurredAt,
		ReceivedAt:         receivedAt.UTC(),
		Provider:           "openai",
		Tool:               "codex",
		SourceSchema:       sourceSchema,
		SourceVersion:      version,
		ActorID:            unavailable,
		DeviceID:           unavailable,
		SessionID:          eventID,
		PrivacyLevel:       "operational",
		Attributes:         attributes,
		ProviderExtensions: extensions,
	}
}

func tokenCorrelation(eventID string, occurredAt time.Time) map[string]any {
	return map[string]any{
		"dedup_key":    eventID,
		"ordering_key": fmt.Sprintf("%020d:%s", occurredAt.UnixNano(), eventID),
		"task_boundary": map[string]any{
			"confidence": "unknown",
			"reason":     "Codex token metrics have no reviewed task-boundary signal",
		},
	}
}

func skillTurnEventsFromHistogram(resourceAttrs map[string]any, version string, histogram *metricHistogram, receivedAt time.Time) []canonical.Event {
	events := make([]canonical.Event, 0, len(histogram.DataPoints))
	for index, point := range histogram.DataPoints {
		events = append(events, skillTurnEvent(resourceAttrs, version, point, index, receivedAt))
	}
	return events
}

func skillTurnEvent(resource map[string]any, version string, point histogramDataPoint, index int, receivedAt time.Time) canonical.Event {
	fields := attributes(point.Attributes)
	occurredAt := metricTime(point.TimeUnixNano, receivedAt)
	identity := fmt.Sprintf("%s|%s|%s|%d", skillTurnDurationMetric, stringValue(fields["status"], ""), point.TimeUnixNano, index)
	eventID := contentID("codex:skill-turn:", []byte(identity))
	extensions := map[string]any{
		"correlation":     skillCorrelation(eventID, occurredAt),
		"skill_detection": "inferred",
		"metric": map[string]any{
			"name":  skillTurnDurationMetric,
			"count": metricCount(point.Count),
		},
		"resource": normalize.UnknownFields(resource, serviceNameAttribute, serviceVersionAttribute),
		"skill_turn": map[string]any{
			"outcome":   mapSkillStatus(stringValue(fields["status"], "")),
			"plugin_id": stringValue(fields["plugin_id"], unavailable),
		},
	}
	return skillEvent(eventID, skillTurnDurationMetric, occurredAt, receivedAt, version, extensions)
}

func skillInjectedEvent(resource map[string]any, version string, point metricDataPoint, index int, receivedAt time.Time) (canonical.Event, bool, error) {
	fields := attributes(point.Attributes)
	skillName := strings.TrimSpace(stringValue(fields["skill"], ""))
	if skillName == "" {
		return canonical.Event{}, false, nil
	}
	occurredAt := metricTime(point.TimeUnixNano, receivedAt)
	identity := fmt.Sprintf("%s|%s|%s|%s|%d", skillName, stringValue(fields["status"], ""), stringValue(fields["invoke_type"], ""), point.TimeUnixNano, index)
	eventID := contentID("codex:skill:", []byte(identity))

	skill := skillPayload(fields, skillName)
	extensions := map[string]any{
		"correlation":     skillCorrelation(eventID, occurredAt),
		"skill_detection": "explicit",
		"skill":           skill,
		"metric": map[string]any{
			"name":  skillInjectedMetric,
			"count": metricCount(point.AsInt),
		},
		"resource": normalize.UnknownFields(resource, serviceNameAttribute, serviceVersionAttribute),
	}

	return skillEvent(eventID, skillInjectedMetric, occurredAt, receivedAt, version, extensions), true, nil
}

func skillEvent(eventID, eventType string, occurredAt, receivedAt time.Time, version string, extensions map[string]any) canonical.Event {
	return canonical.Event{
		SchemaVersion:      canonicalSchemaVersion,
		EventID:            eventID,
		EventType:          eventType,
		OccurredAt:         occurredAt,
		ReceivedAt:         receivedAt.UTC(),
		Provider:           "openai",
		Tool:               "codex",
		SourceSchema:       sourceSchema,
		SourceVersion:      version,
		ActorID:            unavailable,
		DeviceID:           unavailable,
		SessionID:          eventID,
		PrivacyLevel:       "operational",
		Attributes:         map[string]any{"unavailable_fields": []string{"model", "token_usage", "cache_usage", "tool_calls", "file_operations", "command_execution", "approvals", "prompt_content", "response_content", "repository_context", "task_outcome", "provider_cost", "session_lifecycle"}},
		ProviderExtensions: extensions,
	}
}

func skillCorrelation(eventID string, occurredAt time.Time) map[string]any {
	return map[string]any{
		"dedup_key":    eventID,
		"ordering_key": fmt.Sprintf("%020d:%s", occurredAt.UnixNano(), eventID),
		"task_boundary": map[string]any{
			"confidence": "unknown",
			"reason":     "Codex skill metrics have no reviewed task-boundary signal",
		},
	}
}

func skillPayload(fields map[string]any, skillName string) map[string]any {
	skill := map[string]any{"name": skillName}
	if outcome := mapSkillStatus(stringValue(fields["status"], "")); outcome != "" {
		skill["outcome"] = outcome
	}
	if invoke := strings.TrimSpace(stringValue(fields["invoke_type"], "")); invoke != "" {
		skill["invoke_type"] = invoke
	}
	return skill
}

func mapSkillStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "success":
		return "success"
	case "error", "failed", "failure":
		return "failed"
	case "":
		return ""
	default:
		return status
	}
}

func metricCount(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case string:
		if parsed, err := strconv.Atoi(typed); err == nil {
			return parsed
		}
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return int(parsed)
		}
	}
	return 1
}

func metricTime(nano string, fallback time.Time) time.Time {
	if strings.TrimSpace(nano) == "" {
		return fallback.UTC()
	}
	parsed, err := strconv.ParseInt(nano, 10, 64)
	if err != nil || parsed <= 0 {
		return fallback.UTC()
	}
	return time.Unix(0, parsed).UTC()
}
