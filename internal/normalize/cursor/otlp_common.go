package cursor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// Shared OTLP attribute decoding for Enterprise OpenTelemetry Export (#130).
// Cursor Agent fixture normalisation stays in normalizer.go and does not use
// these helpers.

const (
	otelServiceName   = "cursor"
	otelTool          = "cursor"
	otelSourceSchema  = "otel"
	otelSessionPrefix = "cursor:"
	otelScopeName     = "cursor.telemetry"

	attrServiceName    = "service.name"
	attrServiceVersion = "service.version"
)

type otlpAttribute struct {
	Key   string         `json:"key"`
	Value map[string]any `json:"value"`
}

type otlpResource struct {
	Attributes []otlpAttribute `json:"attributes"`
}

type otlpScope struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// otelContext carries resource/scope identity shared by log and metric mappers.
type otelContext struct {
	scopeName        string
	resourceIdentity string
	safeResource     map[string]any
	version          string
	receivedAt       time.Time
}

// safeOTELAttributeKeys is the shared allow-list of behavioural Cursor OTEL
// attributes retained in provider_extensions. Account identifiers
// (cursor.team.id, cursor.user.id) are deliberately omitted.
var safeOTELAttributeKeys = map[string]struct{}{
	"cursor.surface":         {},
	"cursor.entrypoint":      {},
	"cursor.api.status":      {},
	"cursor.api.billable":    {},
	"cursor.source_event.id": {},
	"cursor.usage_event.id":  {},
}

func attributeValues(attributes []otlpAttribute) map[string]any {
	values := make(map[string]any, len(attributes))
	for _, attribute := range attributes {
		if value, ok := attributeValue(attribute.Value); ok {
			values[attribute.Key] = value
		}
	}
	return values
}

func attributeValue(value map[string]any) (any, bool) {
	if text, ok := value["stringValue"].(string); ok {
		return text, true
	}
	if boolean, ok := value["boolValue"].(bool); ok {
		return boolean, true
	}
	if number, ok := value["doubleValue"].(float64); ok {
		return number, true
	}
	switch integer := value["intValue"].(type) {
	case float64:
		return integer, true
	case string:
		if parsed, err := strconv.ParseFloat(integer, 64); err == nil {
			return parsed, true
		}
	}
	return nil, false
}

func stringAttr(fields map[string]any, key string) string {
	text, _ := fields[key].(string)
	return text
}

func fallbackString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func otlpTime(nano string, fallback time.Time) time.Time {
	if strings.TrimSpace(nano) == "" {
		return fallback.UTC()
	}
	parsed, err := strconv.ParseInt(nano, 10, 64)
	if err != nil || parsed <= 0 {
		return fallback.UTC()
	}
	return time.Unix(0, parsed).UTC()
}

func allowListed(fields map[string]any, allowed map[string]struct{}) map[string]any {
	safe := make(map[string]any)
	for key, value := range fields {
		if _, ok := allowed[strings.ToLower(strings.TrimSpace(key))]; ok {
			safe[key] = value
		}
	}
	return safe
}

func isCursorTelemetryScope(name string) bool {
	return strings.TrimSpace(name) == otelScopeName
}

func cursorResourceContext(resourceAttrs map[string]any, receivedAt time.Time) (otelContext, bool) {
	if service, _ := resourceAttrs[attrServiceName].(string); service != otelServiceName {
		return otelContext{}, false
	}
	return otelContext{
		resourceIdentity: stableJSON(map[string]any{
			attrServiceName:    stringAttr(resourceAttrs, attrServiceName),
			attrServiceVersion: stringAttr(resourceAttrs, attrServiceVersion),
			"resource":         allowListed(resourceAttrs, safeOTELAttributeKeys),
		}),
		safeResource: allowListed(resourceAttrs, safeOTELAttributeKeys),
		version:      fallbackString(stringAttr(resourceAttrs, attrServiceVersion), unavailable),
		receivedAt:   receivedAt,
	}, true
}

func finishOTELEvents(events []canonical.Event, unsupported error) ([]canonical.Event, error) {
	if len(events) == 0 {
		return nil, unsupported
	}
	return normalize.CorrelateEvents(events), nil
}

func otelCanonicalEvent(eventID, eventType, sessionID string, occurredAt time.Time, ctx otelContext, attributes, extensions map[string]any) canonical.Event {
	return canonical.Event{
		SchemaVersion:      canonicalSchemaVersion,
		EventID:            eventID,
		EventType:          eventType,
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
	}
}

func otelUnavailableFields(extra ...string) []string {
	fields := []string{
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
	}
	return append(fields, extra...)
}

func stableJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func contentID(prefix string, data []byte) string {
	sum := sha256.Sum256(data)
	return prefix + hex.EncodeToString(sum[:])
}

func otelCorrelation(eventID string, occurredAt time.Time, reason string) map[string]any {
	return map[string]any{
		"dedup_key":    eventID,
		"ordering_key": fmt.Sprintf("%020d:%s", occurredAt.UnixNano(), eventID),
		"task_boundary": map[string]any{
			"confidence": "unknown",
			"reason":     reason,
		},
	}
}
