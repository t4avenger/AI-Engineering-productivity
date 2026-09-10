package codex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// contentID derives a stable, non-keyed uniqueness key for a record that
// carries no provider-native ID. It is a plain content hash for deduplication
// only — not a privacy transform (epic #87 removed ingest-time hiding).
func contentID(prefix string, data []byte) string {
	sum := sha256.Sum256(data)
	return prefix + hex.EncodeToString(sum[:])
}

// ErrUnsupportedLogs indicates that a valid OTLP log payload is not the
// observed Codex log shape and therefore must not be normalised by this adapter.
var ErrUnsupportedLogs = errors.New("unsupported Codex log payload")

const (
	codexEventNameKey    = "event.name"
	codexToolResultEvent = "codex.tool_result"
)

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
	Attributes           []attribute     `json:"attributes"`
	Body                 json.RawMessage `json:"body"`
	ObservedTimeUnixNano string          `json:"observedTimeUnixNano"`
	SeverityText         string          `json:"severityText"`
	TimeUnixNano         string          `json:"timeUnixNano"`
}

// NormalizeLogs maps the reviewed Codex OTLP log shape directly to canonical
// events. When a conversation.id is present, it becomes the provider-prefixed
// native session ID. Records without that field fall back to a non-keyed
// content ID for uniqueness only (epic #87 — no ingest-time hiding).
func NormalizeLogs(data []byte, receivedAt time.Time) ([]canonical.Event, error) {
	var payload logsPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode Codex OTLP logs: %w", err)
	}
	var events []canonical.Event
	for _, raw := range payload.ResourceLogs {
		normalized, err := normalizeResourceLog(raw, receivedAt)
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

func normalizeResourceLog(raw resourceLog, receivedAt time.Time) ([]canonical.Event, error) {
	resource := attributes(raw.Resource.Attributes)
	if !isCodexLogService(resource["service.name"]) {
		return nil, nil
	}
	var events []canonical.Event
	for _, scope := range raw.ScopeLogs {
		for _, record := range scope.LogRecords {
			event, err := normalizeLogRecord(resource, record, receivedAt)
			if err != nil {
				return nil, err
			}
			events = append(events, event)
		}
	}
	return events, nil
}

func normalizeLogRecord(resource map[string]any, record logRecord, receivedAt time.Time) (canonical.Event, error) {
	recordData, err := json.Marshal(record)
	if err != nil {
		return canonical.Event{}, fmt.Errorf("marshal Codex log record: %w", err)
	}
	id := contentID("codex-log:", recordData)
	fields := attributes(record.Attributes)
	sessionID := codexLogSessionID(fields, id)
	eventName := stringValue(fields[codexEventNameKey], "codex.log.received")
	attributes := map[string]any{"unavailable_fields": codexLogUnavailableFields(eventName)}
	for _, key := range []string{"model", "input_token_count", "output_token_count"} {
		if value, ok := fields[key]; ok {
			attributes[key] = value
		}
	}
	if toolCall, ok := codexToolCall(fields, id, sessionID); ok {
		for key, value := range toolCall.attributes {
			attributes[key] = value
		}
	}
	if !hasCodexOutcomeContract(eventName) {
		attributes["unavailable_fields"] = append(attributes["unavailable_fields"].([]string), "task_outcome")
	}
	extensions := map[string]any{"resource_attributes": resource, "log_attributes": codexLogAttributes(fields), "severity": record.SeverityText}
	if toolCall, ok := codexToolCall(fields, id, sessionID); ok {
		extensions["tool_call"] = toolCall.providerExtension
	}
	if mcpCall, ok := codexMCPCall(fields); ok {
		attributes["category"] = string(canonical.OperationCategoryMCPCall)
		extensions["mcp_call"] = mcpCall
	}
	attachCodexOutcomeContract(extensions, fields, eventName)
	return canonical.Event{SchemaVersion: canonicalSchemaVersion, EventID: id, EventType: eventName, OccurredAt: receivedAt.UTC(), ReceivedAt: receivedAt.UTC(), Provider: "openai", Tool: "codex", SourceSchema: sourceSchema, SourceVersion: stringValue(resource["service.version"], unavailable), ActorID: unavailable, DeviceID: unavailable, SessionID: sessionID, PrivacyLevel: "operational", Attributes: attributes, ProviderExtensions: extensions}, nil
}

func codexLogSessionID(fields map[string]any, fallback string) string {
	if conversationID, ok := normalize.ObservedString(fields["conversation.id"]); ok {
		return normalize.ProviderNativeSessionID("codex:", conversationID)
	}
	return fallback
}

func hasCodexOutcomeContract(eventName string) bool {
	return eventName == codexToolResultEvent || eventName == "codex.api_request"
}

func attachCodexOutcomeContract(extensions map[string]any, fields map[string]any, eventName string) {
	var source, status string
	switch eventName {
	case codexToolResultEvent:
		source = "tool_result"
		status = codexSuccessStatus(fields["success"])
	case "codex.api_request":
		source = "provider_completion"
		status = codexSuccessStatus(fields["success"])
	default:
		return
	}
	if status == "" {
		return
	}
	contract := map[string]any{
		"source":     source,
		"status":     status,
		"confidence": "observed",
	}
	if model, ok := normalize.ObservedString(fields["model"]); ok {
		contract["model"] = model
	}
	if duration := normalize.OptionalTokenCount(fields["duration_ms"]); duration != nil {
		contract["duration_ms"] = *duration
	}
	if input := normalize.OptionalTokenCount(fields["input_token_count"]); input != nil {
		contract["input_tokens"] = *input
	}
	if output := normalize.OptionalTokenCount(fields["output_token_count"]); output != nil {
		contract["output_tokens"] = *output
	}
	if code := codexErrorCode(fields, status); code != "" {
		contract["error_code"] = code
	}
	if attempt := normalize.OptionalTokenCount(fields["attempt"]); attempt != nil && *attempt > 0 {
		contract["retry_attempt"] = *attempt
	}
	extensions["outcome_contract"] = contract
}

func codexSuccessStatus(value any) string {
	switch typed := value.(type) {
	case bool:
		if typed {
			return "success"
		}
		return "failed"
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "ok", "success":
			return "success"
		case "false", "error", "failed", "failure":
			return "failed"
		}
	}
	return ""
}

func codexErrorCode(fields map[string]any, status string) string {
	if status != "failed" {
		return ""
	}
	for _, key := range []string{"error_code", "error.message", "error"} {
		if text, ok := normalize.ObservedString(fields[key]); ok {
			return text
		}
	}
	for _, key := range []string{"http.status_code", "http.response.status_code"} {
		if text, ok := normalize.ObservedString(fields[key]); ok && text != "" {
			return "http_" + text
		}
		if number := normalize.OptionalTokenCount(fields[key]); number != nil {
			return fmt.Sprintf("http_%d", *number)
		}
	}
	return ""
}

func codexLogAttributes(fields map[string]any) map[string]any {
	known := []string{"mcp_server", "conversation.id"}
	if stringValue(fields[codexEventNameKey], "") == codexToolResultEvent {
		known = append(known, codexToolResultFieldKeys()...)
	}
	return safeCodexLogAttributes(normalize.UnknownFields(fields, known...))
}

func codexLogUnavailableFields(eventName string) []string {
	fields := []string{"session_lifecycle", "cache_usage", "reasoning_tokens", "file_operations", "command_execution", "approvals", "prompt_content", "response_content", "repository_context", "provider_cost"}
	if eventName != codexToolResultEvent {
		fields = append(fields, "tool_calls")
	}
	return fields
}

type codexToolCallSignal struct {
	attributes        map[string]any
	providerExtension map[string]any
}

func codexToolCall(fields map[string]any, fallbackID, sessionID string) (codexToolCallSignal, bool) {
	if stringValue(fields[codexEventNameKey], "") != codexToolResultEvent {
		return codexToolCallSignal{}, false
	}
	operationID := fallbackID
	if callID, ok := normalize.ObservedString(fields["call_id"]); ok {
		operationID = sessionID + ":tool:" + callID
	}
	category := codexOperationCategory(fields)
	outcome := codexSuccessStatus(fields["success"])
	if outcome == "" {
		outcome = "unknown"
	}
	attributes := map[string]any{
		"operation_id": operationID,
		"category":     string(category),
		"outcome":      outcome,
	}
	if duration := normalize.OptionalTokenCount(fields["duration_ms"]); duration != nil {
		attributes["duration_ms"] = *duration
	}
	extension := map[string]any{
		"operation_id": operationID,
		"category":     string(category),
		"outcome":      outcome,
		"provenance":   string(canonical.ProvenanceObserved),
	}
	for _, key := range []string{"tool_name", "tool_namespace", "call_id", "duration_ms", "success", "output_truncated", "tool_result_seq", "decision"} {
		if value, ok := fields[key]; ok {
			extension[key] = value
		}
	}
	return codexToolCallSignal{attributes: attributes, providerExtension: extension}, true
}

func codexOperationCategory(fields map[string]any) canonical.OperationCategory {
	if _, ok := normalize.ObservedString(fields["mcp_server"]); ok {
		return canonical.OperationCategoryMCPCall
	}
	name, _ := normalize.ObservedString(fields["tool_name"])
	switch name {
	case "exec_command":
		return canonical.OperationCategoryShellCommand
	case "apply_patch":
		return canonical.OperationCategoryFilesystemWrite
	default:
		return canonical.OperationCategoryUnknown
	}
}

func codexToolResultFieldKeys() []string {
	return []string{"tool_name", "tool_namespace", "call_id", "duration_ms", "success", "output_truncated", "tool_result_seq", "decision"}
}

func safeCodexLogAttributes(fields map[string]any) map[string]any {
	safe := make(map[string]any, len(fields))
	for key, value := range fields {
		if sensitiveCodexLogAttribute(key) {
			continue
		}
		safe[key] = value
	}
	return safe
}

func sensitiveCodexLogAttribute(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "arguments", "output", "api_key", "user.email", "user.account_id", "custom_metadata", "hostname", "host.name":
		return true
	default:
		return false
	}
}

func codexMCPCall(fields map[string]any) (map[string]any, bool) {
	server, ok := normalize.ObservedString(fields["mcp_server"])
	if !ok {
		return nil, false
	}
	call := map[string]any{
		"server_name":    server,
		"identity_state": "provider_reported",
	}
	for _, key := range []string{"mcp_server_origin", "tool_name", "tool_namespace", "call_id", "duration_ms", "success", "output_truncated", "tool_result_seq", "decision"} {
		if value, ok := fields[key]; ok {
			call[key] = value
		}
	}
	return call, true
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

// isCodexLogService reports whether a resource service.name is one of the
// observed Codex OTLP log exporters: codex_cli_rs (interactive TUI) or
// codex_exec (the non-interactive `codex exec` subcommand). Both were observed
// exporting the same reviewed log shape at Codex CLI 0.145.0.
func isCodexLogService(value any) bool {
	name, ok := value.(string)
	if !ok {
		return false
	}
	return name == "codex_cli_rs" || name == "codex_exec"
}
