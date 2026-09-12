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

// safeLogAttributeKeys is the allow-list of log/resource attribute keys carried
// into provider_extensions. Conversation/request/event IDs are promoted onto
// canonical identity fields and are not repeated here. Account identifiers
// (cursor.team.id, cursor.user.id) are deliberately omitted.
var safeLogAttributeKeys = map[string]struct{}{
	"cursor.surface":         {},
	"cursor.entrypoint":      {},
	"cursor.api.billable":    {},
	"cursor.source_event.id": {},
	"cursor.usage_event.id":  {},
}

type logsPayload struct {
	ResourceLogs []resourceLog `json:"resourceLogs"`
}

type resourceLog struct {
	Resource struct {
		Attributes []otlpAttribute `json:"attributes"`
	} `json:"resource"`
	ScopeLogs []scopeLog `json:"scopeLogs"`
}

type scopeLog struct {
	Scope struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"scope"`
	LogRecords []logRecord `json:"logRecords"`
}

type logRecord struct {
	TimeUnixNano   string          `json:"timeUnixNano"`
	SeverityNumber int             `json:"severityNumber"`
	Body           map[string]any  `json:"body"`
	Attributes     []otlpAttribute `json:"attributes"`
}

// NormalizeLogs maps Cursor Enterprise OTLP/HTTP logs into canonical events.
// Only resources with service.name=cursor are considered. Within a Cursor
// resource, log records whose body is api_request become token-bearing events;
// every other event family is route-tolerated until fixtures prove them.
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
	version := fallbackString(stringAttr(resourceAttrs, attrServiceVersion), unavailable)
	safeResource := allowListed(resourceAttrs, safeLogAttributeKeys)
	var events []canonical.Event
	for _, scope := range resource.ScopeLogs {
		for index, record := range scope.LogRecords {
			event, ok, err := apiRequestEvent(record, index, version, safeResource, receivedAt)
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

func apiRequestEvent(record logRecord, index int, version string, safeResource map[string]any, receivedAt time.Time) (canonical.Event, bool, error) {
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

	occurredAt := otlpTime(record.TimeUnixNano, receivedAt)
	model, modelObserved := normalize.ObservedString(fields["cursor.model.name"])
	conversationID := strings.TrimSpace(stringAttr(fields, "cursor.conversation.id"))
	sessionID := normalize.ProviderNativeSessionID(otelSessionPrefix, conversationID)

	eventID := eventIDFromRecord(fields, sessionID, index, occurredAt)
	safeFields := allowListed(fields, safeLogAttributeKeys)

	attributes := map[string]any{
		"unavailable_fields": []string{
			"tool_calls",
			"mcp_calls",
			"skill_invocations",
			"file_operations",
			"command_execution",
			"approvals",
			"prompt_content",
			"response_content",
			"repository_context",
			"task_outcome",
			"provider_cost",
			"trace_span_correlation",
		},
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
		"resource":       safeResource,
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
		ReceivedAt:         receivedAt.UTC(),
		Provider:           provider,
		Tool:               otelTool,
		SourceSchema:       otelSourceSchema,
		SourceVersion:      version,
		ActorID:            unavailable,
		DeviceID:           unavailable,
		SessionID:          sessionID,
		PrivacyLevel:       "operational",
		Attributes:         attributes,
		ProviderExtensions: extensions,
	}, true, nil
}

func eventIDFromRecord(fields map[string]any, sessionID string, index int, occurredAt time.Time) string {
	if raw := strings.TrimSpace(stringAttr(fields, "cursor.event.id")); raw != "" {
		return normalize.ProviderNativeSessionID(otelSessionPrefix, raw)
	}
	identity, err := json.Marshal(map[string]any{
		"session": sessionID,
		"index":   index,
		"at":      occurredAt.UnixNano(),
		"body":    apiRequestBody,
	})
	if err != nil {
		return contentID("cursor:api:", []byte(fmt.Sprintf("%s|%d", sessionID, index)))
	}
	return contentID("cursor:api:", identity)
}
