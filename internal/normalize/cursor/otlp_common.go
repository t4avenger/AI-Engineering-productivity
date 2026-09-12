package cursor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
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

func otelResourceIdentity(resourceAttrs map[string]any) string {
	return stableJSON(map[string]any{
		attrServiceName:    stringAttr(resourceAttrs, attrServiceName),
		attrServiceVersion: stringAttr(resourceAttrs, attrServiceVersion),
		"resource":         allowListed(resourceAttrs, safeOTELAttributeKeys),
	})
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
