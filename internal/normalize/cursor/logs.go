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

type logContext struct {
	scopeName        string
	resourceIdentity string
	safeResource     map[string]any
	version          string
	receivedAt       time.Time
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
	if len(events) == 0 {
		return nil, ErrUnsupportedLogs
	}
	return normalize.CorrelateEvents(events), nil
}

func apiRequestEventsFromResource(resource resourceLog, receivedAt time.Time) ([]canonical.Event, error) {
	resourceAttrs := attributeValues(resource.Resource.Attributes)
	if service, _ := resourceAttrs[attrServiceName].(string); service != otelServiceName {
		return nil, nil
	}
	ctx := logContext{
		resourceIdentity: otelResourceIdentity(resourceAttrs),
		safeResource:     allowListed(resourceAttrs, safeOTELAttributeKeys),
		version:          fallbackString(stringAttr(resourceAttrs, attrServiceVersion), unavailable),
		receivedAt:       receivedAt,
	}
	var events []canonical.Event
	for _, scope := range resource.ScopeLogs {
		if !isCursorTelemetryScope(scope.Scope.Name) {
			continue
		}
		ctx.scopeName = scope.Scope.Name
		for index, record := range scope.LogRecords {
			event, ok, err := apiRequestEvent(record, index, ctx)
			if err != nil {
				return nil, err
			}
			if ok {
				events = append(events, event)
			}
		}
	}
	return events, nil
}

func apiRequestEvent(record logRecord, index int, ctx logContext) (canonical.Event, bool, error) {
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

	attributes := map[string]any{
		"unavailable_fields": otelUnavailableFields(),
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
	if cacheReadTokens != nil {
		attributes["cached_input_token_count"] = *cacheReadTokens
	}
	if cacheWriteTokens != nil {
		attributes["cache_write_input_token_count"] = *cacheWriteTokens
	}

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

	return canonical.Event{
		SchemaVersion:      canonicalSchemaVersion,
		EventID:            eventID,
		EventType:          "cursor.api.request",
		OccurredAt:         occurredAt,
		ReceivedAt:         ctx.receivedAt.UTC(),
		Provider:           provider,
		Tool:               otelTool,
		SourceSchema:       otelSourceSchema,
		SourceVersion:      ctx.version,
		ActorID:            unavailable,
		DeviceID:           unavailable,
		SessionID:          sessionID,
		PrivacyLevel:       "operational",
		Attributes:         attributes,
		ProviderExtensions: extensions,
	}, true, nil
}

func eventIDFromRecord(fields map[string]any, sessionID string, index int, occurredAt time.Time, ctx logContext) string {
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
