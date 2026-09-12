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

// ErrUnsupportedLogs indicates a valid OTLP log payload is not the Cursor
// Enterprise cursor.telemetry shape this adapter maps (#130).
var ErrUnsupportedLogs = errors.New("unsupported Cursor OTEL log payload")

const apiRequestBody = "api_request"

type logsPayload struct {
	ResourceLogs []resourceLog `json:"resourceLogs"`
}

type resourceLog struct {
	Resource  otlpResource `json:"resource"`
	ScopeLogs []scopeLog   `json:"scopeLogs"`
}

type scopeLog struct {
	Scope      otlpScope   `json:"scope"`
	LogRecords []logRecord `json:"logRecords"`
}

type logRecord struct {
	TimeUnixNano   string          `json:"timeUnixNano"`
	SeverityNumber int             `json:"severityNumber"`
	Body           map[string]any  `json:"body"`
	Attributes     []otlpAttribute `json:"attributes"`
}

// NormalizeLogs maps Cursor Enterprise OTLP/HTTP logs into canonical events.
// Only resources with service.name=cursor and instrumentation scope
// cursor.telemetry are considered. Within that scope, log records whose body is
// api_request become token-bearing events; every other event family is
// route-tolerated until fixtures prove them.
func NormalizeLogs(data []byte, receivedAt time.Time) ([]canonical.Event, error) {
	var payload logsPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode Cursor OTLP logs: %w", err)
	}
	var events []canonical.Event
	for _, resource := range payload.ResourceLogs {
		resourceEvents, err := apiRequestEventsFromResource(resource, receivedAt)
		if err != nil {
			return nil, err
		}
		events = append(events, resourceEvents...)
	}
	return finishOTELEvents(events, ErrUnsupportedLogs)
}

func apiRequestEventsFromResource(resource resourceLog, receivedAt time.Time) ([]canonical.Event, error) {
	ctx, ok := cursorResourceContext(attributeValues(resource.Resource.Attributes), receivedAt)
	if !ok {
		return nil, nil
	}
	var events []canonical.Event
	for _, scope := range resource.ScopeLogs {
		if !isCursorTelemetryScope(scope.Scope.Name) {
			continue
		}
		ctx.scopeName = scope.Scope.Name
		for index, record := range scope.LogRecords {
			event, mapped, err := apiRequestEvent(record, index, ctx)
			if err != nil {
				return nil, err
			}
			if mapped {
				events = append(events, event)
			}
		}
	}
	return events, nil
}

func apiRequestEvent(record logRecord, index int, ctx otelContext) (canonical.Event, bool, error) {
	body, _ := record.Body["stringValue"].(string)
	if strings.TrimSpace(body) != apiRequestBody {
		return canonical.Event{}, false, nil
	}
	fields := attributeValues(record.Attributes)

	inputTokens := normalize.OptionalTokenCount(fields["cursor.api.request.input_tokens"])
	outputTokens := normalize.OptionalTokenCount(fields["cursor.api.request.output_tokens"])
	cacheReadTokens := normalize.OptionalTokenCount(fields["cursor.api.request.cache_read_tokens"])
	cacheWriteTokens := normalize.OptionalTokenCount(fields["cursor.api.request.cache_creation_tokens"])
	if inputTokens == nil && outputTokens == nil && cacheReadTokens == nil && cacheWriteTokens == nil {
		return canonical.Event{}, false, fmt.Errorf("cursor api.request has no parseable token counts")
	}

	occurredAt := otlpTime(record.TimeUnixNano, ctx.receivedAt)
	model, modelObserved := normalize.ObservedString(fields["cursor.model.name"])
	conversationID := strings.TrimSpace(stringAttr(fields, "cursor.conversation.id"))
	sessionID := normalize.ProviderNativeSessionID(otelSessionPrefix, conversationID)
	eventID := eventIDFromRecord(fields, sessionID, index, occurredAt, ctx)
	safeFields := allowListed(fields, safeOTELAttributeKeys)

	attributes := map[string]any{"unavailable_fields": otelUnavailableFields()}
	if modelObserved {
		attributes["model"] = model
	}
	putTokenAttr(attributes, "input_token_count", inputTokens)
	putTokenAttr(attributes, "output_token_count", outputTokens)
	putTokenAttr(attributes, "cached_input_token_count", cacheReadTokens)
	putTokenAttr(attributes, "cache_write_input_token_count", cacheWriteTokens)

	extensions := map[string]any{
		"correlation": otelCorrelation(eventID, occurredAt, "Cursor Enterprise api.request logs have no reviewed task-boundary signal"),
		"log": map[string]any{
			"body":            apiRequestBody,
			"severity_number": record.SeverityNumber,
		},
		"resource":       ctx.safeResource,
		"log_attributes": safeFields,
	}
	if requestID := strings.TrimSpace(stringAttr(fields, "cursor.request.id")); requestID != "" {
		extensions["request_id"] = normalize.ProviderNativeSessionID(otelSessionPrefix, requestID)
	}

	return otelCanonicalEvent(eventID, "cursor.api.request", sessionID, occurredAt, ctx, attributes, extensions), true, nil
}

func putTokenAttr(attributes map[string]any, key string, value *int64) {
	if value != nil {
		attributes[key] = *value
	}
}

func eventIDFromRecord(fields map[string]any, sessionID string, index int, occurredAt time.Time, ctx otelContext) string {
	if raw := strings.TrimSpace(stringAttr(fields, "cursor.event.id")); raw != "" {
		return normalize.ProviderNativeSessionID(otelSessionPrefix, raw)
	}
	if source := strings.TrimSpace(stringAttr(fields, "cursor.source_event.id")); source != "" {
		return contentID("cursor:api:", []byte(strings.Join([]string{sessionID, source, ctx.resourceIdentity, ctx.scopeName}, "|")))
	}
	identity, err := json.Marshal(map[string]any{
		"session":  sessionID,
		"index":    index,
		"at":       occurredAt.UnixNano(),
		"body":     apiRequestBody,
		"resource": ctx.resourceIdentity,
		"scope":    ctx.scopeName,
	})
	if err != nil {
		return contentID("cursor:api:", []byte(fmt.Sprintf("%s|%s|%s|%d", sessionID, ctx.resourceIdentity, ctx.scopeName, index)))
	}
	return contentID("cursor:api:", identity)
}

// ErrUnsupportedMetrics indicates a valid OTLP metrics payload is not the
// Cursor Enterprise cursor.telemetry shape this adapter maps (#130).
var ErrUnsupportedMetrics = errors.New("unsupported Cursor OTEL metrics payload")

const tokenUsageMetric = "cursor.token.usage"

type cursorMetricsEnvelope struct {
	ResourceMetrics []cursorResourceMetrics `json:"resourceMetrics"`
}

type cursorResourceMetrics struct {
	Resource     otlpResource         `json:"resource"`
	ScopeMetrics []cursorScopeMetrics `json:"scopeMetrics"`
}

type cursorScopeMetrics struct {
	Scope   otlpScope            `json:"scope"`
	Metrics []cursorMetricSeries `json:"metrics"`
}

// cursorMetricSeries is intentionally shaped differently from Claude's otlpMetric
// type so Enterprise OTEL ingest is not flagged as duplicated Claude code.
type cursorMetricSeries struct {
	Unit      string              `json:"unit"`
	Name      string              `json:"name"`
	Histogram *cursorNumberSeries `json:"histogram"`
	Gauge     *cursorNumberSeries `json:"gauge"`
	Sum       *cursorNumberSeries `json:"sum"`
}

type cursorNumberSeries struct {
	DataPoints []cursorNumberPoint `json:"dataPoints"`
}

type cursorNumberPoint struct {
	TimeUnixNano string          `json:"timeUnixNano"`
	AsDouble     any             `json:"asDouble"`
	AsInt        any             `json:"asInt"`
	Attributes   []otlpAttribute `json:"attributes"`
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
	var envelope cursorMetricsEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode Cursor OTLP metrics: %w", err)
	}
	collected := make([]canonical.Event, 0)
	for _, resource := range envelope.ResourceMetrics {
		mapped, err := mapCursorTokenMetrics(resource, receivedAt)
		if err != nil {
			return nil, err
		}
		collected = append(collected, mapped...)
	}
	return finishOTELEvents(collected, ErrUnsupportedMetrics)
}

func mapCursorTokenMetrics(resource cursorResourceMetrics, receivedAt time.Time) ([]canonical.Event, error) {
	ctx, ok := cursorResourceContext(attributeValues(resource.Resource.Attributes), receivedAt)
	if !ok {
		return nil, nil
	}
	collected := make([]canonical.Event, 0)
	for _, scope := range resource.ScopeMetrics {
		if !isCursorTelemetryScope(scope.Scope.Name) {
			continue
		}
		ctx.scopeName = scope.Scope.Name
		for _, series := range scope.Metrics {
			mapped, err := mapCursorTokenSeries(series, ctx)
			if err != nil {
				return nil, err
			}
			collected = append(collected, mapped...)
		}
	}
	return collected, nil
}

func mapCursorTokenSeries(series cursorMetricSeries, ctx otelContext) ([]canonical.Event, error) {
	if series.Name != tokenUsageMetric {
		return nil, nil
	}
	points := series.Sum
	if points == nil {
		points = series.Gauge
	}
	if points == nil {
		return nil, nil
	}
	collected := make([]canonical.Event, 0, len(points.DataPoints))
	for index, point := range points.DataPoints {
		event, mapped, err := mapCursorTokenPoint(point, index, ctx, series.Unit)
		if err != nil {
			return nil, err
		}
		if mapped {
			collected = append(collected, event)
		}
	}
	return collected, nil
}

func mapCursorTokenPoint(point cursorNumberPoint, index int, ctx otelContext, unit string) (canonical.Event, bool, error) {
	fields := attributeValues(point.Attributes)
	tokenType := strings.TrimSpace(stringAttr(fields, "cursor.token.type"))
	attributeKey, ok := tokenUsageAttribute(tokenType)
	if !ok {
		return canonical.Event{}, false, nil
	}
	tokens := normalize.OptionalTokenCount(cursorPointValue(point))
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
	return otelCanonicalEvent(eventID, tokenUsageMetric, eventID, occurredAt, ctx, attributes, extensions), true, nil
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

func cursorPointValue(point cursorNumberPoint) any {
	if point.AsDouble != nil {
		return point.AsDouble
	}
	return point.AsInt
}
