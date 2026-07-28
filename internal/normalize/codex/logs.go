package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// ErrUnsupportedLogs indicates that a valid OTLP log payload is not the
// observed Codex log shape and therefore must not be normalised by this adapter.
var ErrUnsupportedLogs = errors.New("unsupported Codex log payload")

type logsPayload struct {
	ResourceLogs []resourceLog `json:"resourceLogs"`
}

type resourceLog struct {
	Resource struct {
		Attributes []attribute `json:"attributes"`
	} `json:"resource"`
	ScopeLogs []scopeLog `json:"scopeLogs"`
}

type scopeLog struct {
	LogRecords []logRecord `json:"logRecords"`
}

type logRecord struct {
	Attributes   []attribute `json:"attributes"`
	SeverityText string      `json:"severityText"`
}

// NormalizeLogs maps the reviewed Codex OTLP log shape directly to canonical
// events. Codex 0.145.0 log records expose no stable session or trace ID, so a
// local HMAC fingerprint identifies one retained event/session without storing
// the sensitive conversation identifier.
func NormalizeLogs(data []byte, receivedAt time.Time, fingerprint func([]byte) string) ([]canonical.Event, error) {
	if fingerprint == nil {
		return nil, errors.New("codex log fingerprint is required")
	}
	var payload logsPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode Codex OTLP logs: %w", err)
	}
	var events []canonical.Event
	for _, raw := range payload.ResourceLogs {
		normalized, err := normalizeResourceLog(raw, receivedAt, fingerprint)
		if err != nil {
			return nil, err
		}
		events = append(events, normalized...)
	}
	if len(events) == 0 {
		return nil, ErrUnsupportedLogs
	}
	return events, nil
}

func normalizeResourceLog(raw resourceLog, receivedAt time.Time, fingerprint func([]byte) string) ([]canonical.Event, error) {
	resource := attributes(raw.Resource.Attributes)
	if resource["service.name"] != "codex_cli_rs" {
		return nil, nil
	}
	var events []canonical.Event
	for _, scope := range raw.ScopeLogs {
		for _, record := range scope.LogRecords {
			event, err := normalizeLogRecord(resource, record, receivedAt, fingerprint)
			if err != nil {
				return nil, err
			}
			events = append(events, event)
		}
	}
	return events, nil
}

func normalizeLogRecord(resource map[string]any, record logRecord, receivedAt time.Time, fingerprint func([]byte) string) (canonical.Event, error) {
	recordData, err := json.Marshal(record)
	if err != nil {
		return canonical.Event{}, fmt.Errorf("marshal Codex log record: %w", err)
	}
	id := "codex-log:" + fingerprint(recordData)
	fields := attributes(record.Attributes)
	attributes := map[string]any{"unavailable_fields": []string{"session_lifecycle", "cache_usage", "reasoning_tokens", "tool_calls", "file_operations", "command_execution", "approvals", "prompt_content", "response_content", "repository_context", "task_outcome", "provider_cost"}}
	for _, key := range []string{"model", "input_token_count", "output_token_count"} {
		if value, ok := fields[key]; ok {
			attributes[key] = value
		}
	}
	return canonical.Event{SchemaVersion: canonicalSchemaVersion, EventID: id, EventType: stringValue(fields["event.name"], "codex.log.received"), OccurredAt: receivedAt.UTC(), ReceivedAt: receivedAt.UTC(), Provider: "openai", Tool: "codex", SourceSchema: sourceSchema, SourceVersion: stringValue(resource["service.version"], unavailable), ActorID: unavailable, DeviceID: unavailable, SessionID: id, PrivacyLevel: "operational", Attributes: attributes, ProviderExtensions: map[string]any{"resource_attributes": resource, "log_attributes": fields, "severity": record.SeverityText}}, nil
}

type attribute struct {
	Key   string         `json:"key"`
	Value map[string]any `json:"value"`
}

func attributes(values []attribute) map[string]any {
	result := make(map[string]any, len(values))
	for _, attribute := range values {
		for _, valueKey := range []string{"stringValue", "intValue", "boolValue", "doubleValue"} {
			if value, found := attribute.Value[valueKey]; found {
				result[attribute.Key] = value
				break
			}
		}
		if _, found := result[attribute.Key]; !found && len(attribute.Value) > 0 {
			result[attribute.Key] = attribute.Value
		}
	}
	return result
}

func stringValue(value any, fallback string) string {
	result, ok := value.(string)
	if !ok || result == "" {
		return fallback
	}
	return result
}
