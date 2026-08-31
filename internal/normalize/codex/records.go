package codex

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

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
var extractedLogFields = []string{"model", "input_token_count", "output_token_count"}

// ExtractLogModelInteractions maps the reviewed Codex OTLP log shape into
// stable-primitive canonical.ModelInteraction records. It is the honest,
// capability-bounded counterpart to NormalizeLogs: only signals the P0 Codex
// capability matrix marks supported/partial are extracted (model identity;
// input/output tokens). Cache and reasoning tokens, tool calls and task
// outcome are left unknown (nil / "unknown"), never fabricated as zero. It
// neither persists nor logs the payload.
//
// Codex 0.145.0 logs expose no stable session or trace ID, so an
// installation-specific HMAC fingerprint identifies the record and its session
// without storing a sensitive conversation identifier.
func ExtractLogModelInteractions(data []byte, receivedAt time.Time, fingerprint func([]byte) string) ([]canonical.ModelInteraction, error) {
	if fingerprint == nil {
		return nil, fmt.Errorf("codex log fingerprint is required")
	}
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
				interaction, ok, err := logRecordModelInteraction(resource, record, receivedAt, fingerprint)
				if err != nil {
					return nil, err
				}
				if ok {
					records = append(records, interaction)
				}
			}
		}
	}
	return records, nil
}

// logRecordModelInteraction builds one ModelInteraction from a log record,
// returning ok=false when the record is not an eligible model interaction.
func logRecordModelInteraction(resource map[string]any, record logRecord, receivedAt time.Time, fingerprint func([]byte) string) (canonical.ModelInteraction, bool, error) {
	fields := attributes(record.Attributes)
	if !isModelInteraction(fields) {
		return canonical.ModelInteraction{}, false, nil
	}
	recordData, err := json.Marshal(record)
	if err != nil {
		return canonical.ModelInteraction{}, false, fmt.Errorf("marshal Codex log record: %w", err)
	}
	id := "codex-log:" + fingerprint(recordData)

	model, modelObserved := observedString(fields["model"])
	inputTokens := optionalTokenCount(fields["input_token_count"])
	outputTokens := optionalTokenCount(fields["output_token_count"])

	started := tolerantNano(record.TimeUnixNano, receivedAt)
	completed := tolerantNano(record.ObservedTimeUnixNano, receivedAt)

	interaction := canonical.ModelInteraction{
		SchemaVersion:      canonical.RecordSchemaVersion,
		RequestID:          id,
		SessionID:          id,
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
		Provenance:         interactionProvenance(modelObserved, inputTokens, outputTokens),
		ProviderExtensions: logProviderExtensions(resource, fields, record.SeverityText),
	}
	return interaction, true, nil
}

// isModelInteraction reports whether a log record is an eligible model
// interaction: its event.name is whitelisted AND it carries at least a model
// or a token count. This prevents fabricating an all-unknown record from a
// bare event.
func isModelInteraction(fields map[string]any) bool {
	name, _ := fields["event.name"].(string)
	if _, ok := modelInteractionEvents[name]; !ok {
		return false
	}
	if _, ok := observedString(fields["model"]); ok {
		return true
	}
	return fields["input_token_count"] != nil || fields["output_token_count"] != nil
}

// observedString returns the trimmed string value and whether a non-empty
// value was actually observed. Absent, non-string, or whitespace-only values
// yield ("unknown", false) so a blank model is never mistaken for a real one.
func observedString(value any) (string, bool) {
	text, ok := value.(string)
	if !ok {
		return "unknown", false
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "unknown", false
	}
	return text, true
}

// optionalTokenCount parses an OTLP token attribute into a *int64. OTLP JSON
// encodes intValue as a string and doubleValue as a JSON number (float64). An
// absent, unparseable, negative, non-integral, or out-of-range value yields nil
// — never a fabricated or silently truncated count — so a genuine absence stays
// distinguishable from a real 0.
func optionalTokenCount(value any) *int64 {
	var count int64
	switch typed := value.(type) {
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		if err != nil {
			return nil
		}
		count = parsed
	case float64:
		if math.Trunc(typed) != typed || typed < math.MinInt64 || typed >= math.MaxInt64 {
			return nil
		}
		count = int64(typed)
	default:
		return nil
	}
	if count < 0 {
		return nil
	}
	return &count
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

// interactionProvenance is observed when the model and at least one token count
// were directly observed; otherwise the record is explicitly unknown.
func interactionProvenance(modelObserved bool, inputTokens, outputTokens *int64) canonical.Provenance {
	if modelObserved && (inputTokens != nil || outputTokens != nil) {
		return canonical.ProvenanceObserved
	}
	return canonical.ProvenanceUnknown
}

// logProviderExtensions preserves the non-extracted evidence verbatim, mirroring
// the Event path in normalizeLogRecord: full resource attributes, the log
// attributes not already promoted onto the typed record, and the severity.
func logProviderExtensions(resource, fields map[string]any, severity string) map[string]any {
	return map[string]any{
		"resource_attributes": resource,
		"log_attributes":      unknownFields(fields, extractedLogFields...),
		"severity":            severity,
	}
}
