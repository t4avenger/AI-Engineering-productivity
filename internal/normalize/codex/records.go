package codex

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// modelInteractionEvents is the whitelist of Codex OTLP log event.name values
// that represent one model request/response. Only these become a
// canonical.ModelInteraction; other log records (tool results, lifecycle) are
// out of scope for this record and are skipped rather than turned into an
// all-unknown record.
var modelInteractionEvents = map[string]struct{}{
	"codex.sse_event": {},
}

// extractedLogFields are the attribute keys promoted onto a ModelInteraction.
// They are excluded from provider_extensions.log_attributes so evidence is not
// duplicated between the typed record and its extensions. event.name is NOT
// listed: it drives eligibility but has no typed field, so it is preserved as
// evidence under provider_extensions.log_attributes rather than dropped.
var extractedLogFields = []string{"model", "input_token_count", "output_token_count", "conversation.id"}

// ExtractLogModelInteractions maps the reviewed Codex OTLP log shape into
// stable-primitive canonical.ModelInteraction records. It is the honest,
// capability-bounded counterpart to NormalizeLogs: only signals the P0 Codex
// capability matrix marks supported/partial are extracted (model identity;
// input/output tokens). Cache and reasoning tokens, tool calls and task
// outcome are left unknown (nil / "unknown"), never fabricated as zero. It
// neither persists nor logs the payload.
//
// When a conversation.id is present, it becomes the provider-prefixed native
// session ID. Records without that field fall back to a non-keyed content ID
// for uniqueness only (epic #87 — no ingest-time hiding).
func ExtractLogModelInteractions(data []byte, receivedAt time.Time) ([]canonical.ModelInteraction, error) {
	var payload logsPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode Codex OTLP logs: %w", err)
	}
	var records []canonical.ModelInteraction
	for _, resourceLog := range payload.ResourceLogs {
		resource := attributes(resourceLog.Resource.Attributes)
		if !isCodexLogService(resource["service.name"]) {
			continue
		}
		for _, scope := range resourceLog.ScopeLogs {
			for _, record := range scope.LogRecords {
				interaction, ok, err := logRecordModelInteraction(resource, record, receivedAt)
				if err != nil {
					return nil, err
				}
				if ok {
					records = append(records, interaction)
				}
			}
		}
	}
	return normalize.CorrelateModelInteractions(records), nil
}

// ExtractLogOperations maps observed Codex tool-result logs into stable-primitive
// Operation records. Only codex.tool_result is eligible; other log records do
// not prove an executed tool call and are skipped.
func ExtractLogOperations(data []byte, receivedAt time.Time) ([]canonical.Operation, error) {
	var payload logsPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode Codex OTLP logs: %w", err)
	}
	var records []canonical.Operation
	for _, resourceLog := range payload.ResourceLogs {
		extracted, err := resourceLogOperations(resourceLog, receivedAt)
		if err != nil {
			return nil, err
		}
		records = append(records, extracted...)
	}
	return normalize.CorrelateOperations(records), nil
}

func resourceLogOperations(resourceLog resourceLog, receivedAt time.Time) ([]canonical.Operation, error) {
	resource := attributes(resourceLog.Resource.Attributes)
	if !isCodexLogService(resource["service.name"]) {
		return nil, nil
	}
	var records []canonical.Operation
	for _, scope := range resourceLog.ScopeLogs {
		extracted, err := scopeLogOperations(resource, scope, receivedAt)
		if err != nil {
			return nil, err
		}
		records = append(records, extracted...)
	}
	return records, nil
}

func scopeLogOperations(resource map[string]any, scope scopeLog, receivedAt time.Time) ([]canonical.Operation, error) {
	var records []canonical.Operation
	for _, record := range scope.LogRecords {
		operation, ok, err := logRecordOperation(resource, record, receivedAt)
		if err != nil {
			return nil, err
		}
		if ok {
			records = append(records, operation)
		}
	}
	return records, nil
}

func logRecordOperation(resource map[string]any, record logRecord, receivedAt time.Time) (canonical.Operation, bool, error) {
	fields := attributes(record.Attributes)
	if stringValue(fields[codexEventNameKey], "") != codexToolResultEvent {
		return canonical.Operation{}, false, nil
	}
	recordData, err := json.Marshal(record)
	if err != nil {
		return canonical.Operation{}, false, fmt.Errorf("marshal Codex log record: %w", err)
	}
	id := contentID("codex-log:", recordData)
	sessionID := codexLogSessionID(fields, id)
	toolCall, _ := codexToolCall(fields, id, sessionID)
	started := tolerantNano(record.TimeUnixNano, receivedAt)
	return canonical.Operation{
		SchemaVersion:      canonical.RecordSchemaVersion,
		OperationID:        toolCall.attributes["operation_id"].(string),
		SessionID:          sessionID,
		Provider:           "openai",
		Tool:               "codex",
		Category:           codexOperationCategory(fields),
		Outcome:            toolCall.attributes["outcome"].(string),
		Provenance:         canonical.ProvenanceObserved,
		ProviderExtensions: operationProviderExtensions(resource, fields, record.SeverityText, id, sessionID, started.value),
	}, true, nil
}

func operationProviderExtensions(resource, fields map[string]any, severity, id, sessionID string, orderingTime time.Time) map[string]any {
	toolCall, _ := codexToolCall(fields, id, sessionID)
	extensions := map[string]any{
		"correlation": map[string]any{
			"dedup_key":    id,
			"ordering_key": fmt.Sprintf("%020d:%s", orderingTime.UTC().UnixNano(), id),
			"task_boundary": map[string]any{
				"confidence": "unknown",
				"reason":     "Codex tool-result telemetry has no reviewed task-boundary signal",
			},
		},
		"resource_attributes": resource,
		"log_attributes":      safeCodexLogAttributes(normalize.UnknownFields(fields, append(codexToolResultFieldKeys(), "conversation.id", "mcp_server")...)),
		"severity":            severity,
		"tool_call":           toolCall.providerExtension,
	}
	if mcpCall, ok := codexMCPCall(fields); ok {
		extensions["mcp_call"] = mcpCall
	}
	return extensions
}

// logRecordModelInteraction builds one ModelInteraction from a log record,
// returning ok=false when the record is not an eligible model interaction.
func logRecordModelInteraction(resource map[string]any, record logRecord, receivedAt time.Time) (canonical.ModelInteraction, bool, error) {
	fields := attributes(record.Attributes)
	if !isModelInteraction(fields) {
		return canonical.ModelInteraction{}, false, nil
	}
	recordData, err := json.Marshal(record)
	if err != nil {
		return canonical.ModelInteraction{}, false, fmt.Errorf("marshal Codex log record: %w", err)
	}
	id := contentID("codex-log:", recordData)
	sessionID := codexLogSessionID(fields, id)

	model, modelObserved := normalize.ObservedString(fields["model"])
	inputTokens := normalize.OptionalTokenCount(fields["input_token_count"])
	outputTokens := normalize.OptionalTokenCount(fields["output_token_count"])

	started := tolerantNano(record.TimeUnixNano, receivedAt)
	completed := tolerantNano(record.ObservedTimeUnixNano, receivedAt)

	interaction := canonical.ModelInteraction{
		SchemaVersion:      canonical.RecordSchemaVersion,
		RequestID:          id,
		SessionID:          sessionID,
		Provider:           "openai",
		Tool:               "codex",
		Model:              model,
		StartedAt:          started.value,
		CompletedAt:        completed.value,
		DurationMs:         durationMs(started, completed),
		InputTokens:        inputTokens,
		OutputTokens:       outputTokens,
		CachedInputTokens:  nil,
		ReasoningTokens:    nil,
		Result:             "unknown",
		ErrorCode:          nil,
		Provenance:         normalize.InteractionProvenance(modelObserved, inputTokens, outputTokens),
		ProviderExtensions: logProviderExtensions(resource, fields, record.SeverityText, id, started.value),
	}
	return interaction, true, nil
}

// isModelInteraction reports whether a log record is an eligible model
// interaction: its event.name is whitelisted AND it carries at least a model
// or a token count. This prevents fabricating an all-unknown record from a
// bare event.
func isModelInteraction(fields map[string]any) bool {
	name, _ := fields[codexEventNameKey].(string)
	if _, ok := modelInteractionEvents[name]; !ok {
		return false
	}
	if _, ok := normalize.ObservedString(fields["model"]); ok {
		return true
	}
	return fields["input_token_count"] != nil || fields["output_token_count"] != nil
}

// nanoTimestamp carries a parsed timestamp and whether it was observed from the
// record (rather than filled from receivedAt).
type nanoTimestamp struct {
	value    time.Time
	observed bool
}

// tolerantNano parses a positive Unix-nanoseconds string, falling back to
// receivedAt when the value is absent or invalid. Unlike unixNanoTime in the
// trace normaliser it never errors, because a missing log timestamp is
// expected, not a rejection.
func tolerantNano(raw string, receivedAt time.Time) nanoTimestamp {
	nanoseconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || nanoseconds <= 0 {
		return nanoTimestamp{value: receivedAt.UTC(), observed: false}
	}
	return nanoTimestamp{value: time.Unix(0, nanoseconds).UTC(), observed: true}
}

// durationMs returns the elapsed milliseconds only when both endpoints were
// observed and completed is not before started; otherwise nil (unknown).
func durationMs(started, completed nanoTimestamp) *int64 {
	if !started.observed || !completed.observed {
		return nil
	}
	if completed.value.Before(started.value) {
		return nil
	}
	ms := completed.value.Sub(started.value).Milliseconds()
	return &ms
}

// logProviderExtensions preserves the non-extracted evidence verbatim, mirroring
// the Event path in normalizeLogRecord: full resource attributes, the log
// attributes not already promoted onto the typed record, and the severity.
func logProviderExtensions(resource, fields map[string]any, severity, id string, startedAt time.Time) map[string]any {
	return map[string]any{
		"correlation": map[string]any{
			"dedup_key":    id,
			"ordering_key": fmt.Sprintf("%020d:%s", startedAt.UnixNano(), id),
			"task_boundary": map[string]any{
				"confidence": "unknown",
				"reason":     "Codex log telemetry has no reviewed task-boundary signal",
			},
		},
		"resource_attributes": resource,
		"log_attributes":      normalize.UnknownFields(fields, extractedLogFields...),
		"severity":            severity,
	}
}
