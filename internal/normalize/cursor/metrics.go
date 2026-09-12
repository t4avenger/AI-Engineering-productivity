package cursor

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// ErrUnsupportedMetrics indicates a valid OTLP metrics payload is not the
// Cursor Enterprise cursor.telemetry shape this adapter maps (#130).
var ErrUnsupportedMetrics = errors.New("unsupported Cursor OTEL metrics payload")

const tokenUsageMetric = "cursor.token.usage"

type metricsPayload struct {
	ResourceMetrics []resourceMetric `json:"resourceMetrics"`
}

type resourceMetric struct {
	Resource     otlpResource  `json:"resource"`
	ScopeMetrics []scopeMetric `json:"scopeMetrics"`
}

type scopeMetric struct {
	Scope   otlpScope    `json:"scope"`
	Metrics []otlpMetric `json:"metrics"`
}

type otlpMetric struct {
	Name      string        `json:"name"`
	Unit      string        `json:"unit"`
	Sum       *metricPoints `json:"sum"`
	Gauge     *metricPoints `json:"gauge"`
	Histogram *metricPoints `json:"histogram"`
}

type metricPoints struct {
	DataPoints []metricDataPoint `json:"dataPoints"`
}

type metricDataPoint struct {
	Attributes   []otlpAttribute `json:"attributes"`
	AsInt        any             `json:"asInt"`
	AsDouble     any             `json:"asDouble"`
	TimeUnixNano string          `json:"timeUnixNano"`
}

type metricContext struct {
	scopeName        string
	resourceIdentity string
	safeResource     map[string]any
	version          string
	receivedAt       time.Time
}

// NormalizeMetrics maps Cursor Enterprise OTLP/HTTP metrics into canonical
// events. Only resources with service.name=cursor and instrumentation scope
// cursor.telemetry are considered. Within that scope, cursor.token.usage sum
// datapoints become token-usage events; every other metric is route-tolerated.
//
// Metrics carry no conversation correlation IDs on the wire, so session
// identity falls back to the content-derived event ID (same pattern as Codex
// turn-token metrics). Account identifiers are never promoted.
func NormalizeMetrics(data []byte, receivedAt time.Time) ([]canonical.Event, error) {
	var payload metricsPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode Cursor OTLP metrics: %w", err)
	}
	var events []canonical.Event
	for _, resource := range payload.ResourceMetrics {
		resourceEvents, err := tokenEventsFromResource(resource, receivedAt)
		if err != nil {
			return nil, err
		}
		events = append(events, resourceEvents...)
	}
	if len(events) == 0 {
		return nil, ErrUnsupportedMetrics
	}
	return normalize.CorrelateEvents(events), nil
}

func tokenEventsFromResource(resource resourceMetric, receivedAt time.Time) ([]canonical.Event, error) {
	resourceAttrs := attributeValues(resource.Resource.Attributes)
	if service, _ := resourceAttrs[attrServiceName].(string); service != otelServiceName {
		return nil, nil
	}
	ctx := metricContext{
		resourceIdentity: otelResourceIdentity(resourceAttrs),
		safeResource:     allowListed(resourceAttrs, safeOTELAttributeKeys),
		version:          fallbackString(stringAttr(resourceAttrs, attrServiceVersion), unavailable),
		receivedAt:       receivedAt,
	}
	var events []canonical.Event
	for _, scope := range resource.ScopeMetrics {
		if !isCursorTelemetryScope(scope.Scope.Name) {
			continue
		}
		ctx.scopeName = scope.Scope.Name
		for _, item := range scope.Metrics {
			metricEvents, err := tokenEventsFromMetric(item, ctx)
			if err != nil {
				return nil, err
			}
			events = append(events, metricEvents...)
		}
	}
	return events, nil
}

func tokenEventsFromMetric(item otlpMetric, ctx metricContext) ([]canonical.Event, error) {
	if item.Name != tokenUsageMetric {
		return nil, nil
	}
	points := item.Sum
	if points == nil {
		points = item.Gauge
	}
	if points == nil {
		return nil, nil
	}
	var events []canonical.Event
	for index, point := range points.DataPoints {
		event, ok, err := tokenUsageEvent(point, index, ctx, item.Unit)
		if err != nil {
			return nil, err
		}
		if ok {
			events = append(events, event)
		}
	}
	return events, nil
}

func tokenUsageEvent(point metricDataPoint, index int, ctx metricContext, unit string) (canonical.Event, bool, error) {
	fields := attributeValues(point.Attributes)
	tokenType := strings.TrimSpace(stringAttr(fields, "cursor.token.type"))
	attributeKey, ok := tokenUsageAttribute(tokenType)
	if !ok {
		return canonical.Event{}, false, nil
	}
	tokens := normalize.OptionalTokenCount(dataPointValue(point))
	if tokens == nil {
		return canonical.Event{}, false, fmt.Errorf("cursor token.usage %q datapoint has no parseable value", tokenType)
	}

	occurredAt := otlpTime(point.TimeUnixNano, ctx.receivedAt)
	model, modelObserved := normalize.ObservedString(fields["cursor.model.name"])
	safeFields := allowListed(fields, safeOTELAttributeKeys)

	identity := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%d|%d", tokenUsageMetric, model, tokenType, point.TimeUnixNano, ctx.resourceIdentity, ctx.scopeName, index, *tokens)
	identity += "|" + stableJSON(safeFields)
	eventID := contentID("cursor:token:", []byte(identity))

	attributes := map[string]any{
		"unavailable_fields": otelUnavailableFields("session_lifecycle"),
		attributeKey:         *tokens,
	}
	if modelObserved {
		attributes["model"] = model
	}
	extensions := map[string]any{
		"correlation": otelCorrelation(eventID, occurredAt, "Cursor Enterprise token metrics have no reviewed task-boundary signal"),
		"metric": map[string]any{
			"name":       tokenUsageMetric,
			"token_type": tokenType,
			"unit":       unit,
		},
		"resource":          ctx.safeResource,
		"metric_attributes": safeFields,
	}
	return canonical.Event{
		SchemaVersion:      canonicalSchemaVersion,
		EventID:            eventID,
		EventType:          tokenUsageMetric,
		OccurredAt:         occurredAt,
		ReceivedAt:         ctx.receivedAt.UTC(),
		Provider:           provider,
		Tool:               otelTool,
		SourceSchema:       otelSourceSchema,
		SourceVersion:      ctx.version,
		ActorID:            unavailable,
		DeviceID:           unavailable,
		SessionID:          eventID,
		PrivacyLevel:       "operational",
		Attributes:         attributes,
		ProviderExtensions: extensions,
	}, true, nil
}

func tokenUsageAttribute(tokenType string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(tokenType)) {
	case "input":
		return "input_token_count", true
	case "output":
		return "output_token_count", true
	case "cache_read":
		return "cached_input_token_count", true
	case "cache_creation":
		return "cache_write_input_token_count", true
	default:
		return "", false
	}
}

func dataPointValue(point metricDataPoint) any {
	if point.AsDouble != nil {
		return point.AsDouble
	}
	return point.AsInt
}
