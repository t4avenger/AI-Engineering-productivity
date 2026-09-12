package cursor

import (
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
