package claude

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// promotedRecordFields are the sample-event keys mapped onto a typed
// ModelInteraction. They are excluded from provider_extensions.event so
// evidence is not duplicated between the typed record and its extensions.
// cache_creation_tokens is deliberately absent: the canonical record has no
// cache-creation field, so it is preserved verbatim under provider_extensions
// rather than conflated with cache_read_tokens.
var promotedRecordFields = []string{"event_name", "event_timestamp", "event_sequence", "session_id", "request_id", "model", "input_tokens", "output_tokens", "cache_read_tokens", "duration_ms"}

// ExtractModelInteractions maps api_request sample events into stable-primitive
// canonical.ModelInteraction records. Only signals the P2 Claude Code
// capability matrix marks supported/partial are extracted: model identity,
// input/output tokens, and cache-read tokens. Reasoning tokens, tool calls, and
// task outcome are left nil/"unknown", never fabricated. mcp_server_connection
// events are not tool-call invocations and yield no Operation record.
//
// completed_at is the observed api_request event timestamp; started_at is
// derived as completed_at minus the observed duration_ms and labelled as such
// under provider_extensions, so it is never mistaken for a directly observed
// endpoint.
func ExtractModelInteractions(data []byte, fingerprint func([]byte) string) ([]canonical.ModelInteraction, error) {
	if fingerprint == nil {
		return nil, fmt.Errorf("claude fingerprint is required")
	}
	document, _, err := decodeDocument(data)
	if err != nil {
		return nil, err
	}
	switch document.Payload.SourceType {
	case sourceTypeOTLPEvents:
	case sourceTypeCapabilityProbe:
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported Claude payload source_type %q", document.Payload.SourceType)
	}
	var records []canonical.ModelInteraction
	for _, raw := range document.Payload.SampleEvents {
		interaction, ok, err := sampleModelInteraction(fingerprint, raw)
		if err != nil {
			return nil, err
		}
		if ok {
			records = append(records, interaction)
		}
	}
	return correlateModelInteractions(records), nil
}

func sampleModelInteraction(fingerprint func([]byte) string, raw map[string]any) (canonical.ModelInteraction, bool, error) {
	if name, _ := raw["event_name"].(string); name != eventAPIRequest {
		return canonical.ModelInteraction{}, false, nil
	}
	model, modelObserved := observedString(raw["model"])
	inputTokens := optionalTokenCount(raw["input_tokens"])
	outputTokens := optionalTokenCount(raw["output_tokens"])
	if !modelObserved && inputTokens == nil && outputTokens == nil {
		return canonical.ModelInteraction{}, false, nil
	}
	sessionID, err := requiredString(raw, "session_id")
	if err != nil {
		return canonical.ModelInteraction{}, false, err
	}
	completed, err := eventTime(raw, "event_timestamp")
	if err != nil {
		return canonical.ModelInteraction{}, false, err
	}

	sessionFingerprint := "claude-code:" + fingerprint([]byte(sessionID))
	requestID := sessionFingerprint + ":" + optionalStringOr(raw, "event_sequence")
	if rawRequest := optionalString(raw, "request_id"); rawRequest != nil {
		requestID = "claude-code:" + fingerprint([]byte(*rawRequest))
	}

	duration := optionalDurationMs(raw["duration_ms"])
	started, startedDerived := completed, false
	if duration != nil {
		started = completed.Add(-time.Duration(*duration) * time.Millisecond)
		startedDerived = true
	}

	interaction := canonical.ModelInteraction{
		SchemaVersion:      canonical.RecordSchemaVersion,
		RequestID:          requestID,
		SessionID:          sessionFingerprint,
		Provider:           provider,
		Tool:               tool,
		Model:              model,
		StartedAt:          started,
		CompletedAt:        completed,
		DurationMs:         duration,
		InputTokens:        inputTokens,
		OutputTokens:       outputTokens,
		CachedInputTokens:  optionalTokenCount(raw["cache_read_tokens"]),
		ReasoningTokens:    nil,
		Result:             "unknown",
		ErrorCode:          nil,
		Provenance:         interactionProvenance(modelObserved, inputTokens, outputTokens),
		ProviderExtensions: recordExtensions(raw, requestID, started, startedDerived),
	}
	return interaction, true, nil
}

func recordExtensions(raw map[string]any, requestID string, started time.Time, startedDerived bool) map[string]any {
	startedProvenance := "observed_equals_completed"
	if startedDerived {
		startedProvenance = "derived_from_duration"
	}
	return map[string]any{
		"correlation": map[string]any{
			"dedup_key":    requestID,
			"ordering_key": fmt.Sprintf("%020d:%s", started.UnixNano(), requestID),
			"task_boundary": map[string]any{
				"confidence": "unknown",
				"reason":     "Claude Code event telemetry has no reviewed task-boundary signal",
			},
		},
		"skill_detection": unavailable,
		"timestamps": map[string]any{
			"completed_at": "observed",
			"started_at":   startedProvenance,
		},
		"event": unknownFields(raw, promotedRecordFields...),
	}
}

// observedString returns the trimmed string value and whether a non-empty value
// was actually observed. Absent, non-string, or whitespace-only values yield
// ("unknown", false) so a blank model is never mistaken for a real one.
func observedString(value any) (string, bool) {
	text, ok := value.(string)
	if !ok {
		return "unknown", false
	}
	if text = strings.TrimSpace(text); text == "" {
		return "unknown", false
	}
	return text, true
}

// optionalTokenCount parses a token attribute into a *int64. JSON numbers decode
// as float64; a string count is also accepted. An absent, unparseable,
// negative, non-integral, or out-of-range value yields nil — never a fabricated
// or truncated count — so a genuine absence stays distinguishable from a real 0.
func optionalTokenCount(value any) *int64 {
	var count int64
	switch typed := value.(type) {
	case float64:
		if math.Trunc(typed) != typed || typed < math.MinInt64 || typed >= math.MaxInt64 {
			return nil
		}
		count = int64(typed)
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		if err != nil {
			return nil
		}
		count = parsed
	default:
		return nil
	}
	if count < 0 {
		return nil
	}
	return &count
}

// optionalDurationMs parses a non-negative millisecond duration, returning nil
// when absent or invalid so an unknown duration is never a fabricated 0.
func optionalDurationMs(value any) *int64 {
	return optionalTokenCount(value)
}

func optionalStringOr(value map[string]any, key string) string {
	if number, ok := value[key].(float64); ok && number == float64(int64(number)) {
		return strconv.FormatInt(int64(number), 10)
	}
	return "0"
}

// interactionProvenance is observed when the model and at least one token count
// were directly observed; otherwise the record is explicitly unknown.
func interactionProvenance(modelObserved bool, inputTokens, outputTokens *int64) canonical.Provenance {
	if modelObserved && (inputTokens != nil || outputTokens != nil) {
		return canonical.ProvenanceObserved
	}
	return canonical.ProvenanceUnknown
}

func correlateModelInteractions(records []canonical.ModelInteraction) []canonical.ModelInteraction {
	ordered := append([]canonical.ModelInteraction(nil), records...)
	sort.SliceStable(ordered, func(i, j int) bool { return modelInteractionLess(ordered[i], ordered[j]) })
	seen := make(map[string]struct{}, len(ordered))
	correlated := make([]canonical.ModelInteraction, 0, len(ordered))
	for _, record := range ordered {
		if _, ok := seen[record.RequestID]; ok {
			continue
		}
		seen[record.RequestID] = struct{}{}
		correlated = append(correlated, record)
	}
	return correlated
}

func modelInteractionLess(left, right canonical.ModelInteraction) bool {
	if !left.StartedAt.Equal(right.StartedAt) {
		return left.StartedAt.Before(right.StartedAt)
	}
	if left.RequestID != right.RequestID {
		return left.RequestID < right.RequestID
	}
	return left.CompletedAt.Before(right.CompletedAt)
}
