package cursor

import (
	"errors"
	"fmt"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// promotedRecordFields are Cursor result/init keys mapped onto a typed
// ModelInteraction. They are excluded from provider_extensions to avoid
// duplicating evidence between the typed record and its preserved raw shape.
var promotedRecordFields = []string{
	"type", "subtype", "is_error", "duration_ms", "duration_api_ms", "result_summary", "session_id", "request_id", "usage",
}

// ExtractModelInteractions maps a reviewed Cursor Agent fixture wrapper into
// stable-primitive canonical.ModelInteraction records (one per result).
//
// It promotes only the verified subset: model identity (partial, stream init
// only), input/output token counts, and cache-read tokens. Cache-write tokens
// are preserved under provider_extensions. Reasoning tokens and any tool/file
// operations remain unknown (nil), never fabricated.
//
// completed_at is captured_at; started_at is derived as captured_at minus
// duration_ms when duration is observed, and this derivation is recorded under
// provider_extensions.timestamps so it is never mistaken for an observed
// endpoint.
func ExtractModelInteractions(data []byte, fingerprint func([]byte) string) ([]canonical.ModelInteraction, error) {
	if fingerprint == nil {
		return nil, fmt.Errorf("cursor fingerprint is required")
	}
	document, capturedAt, err := decodeFixtureDocument(data)
	if err != nil {
		return nil, err
	}
	switch document.Payload.SourceType {
	case sourceTypePrintJSON, sourceTypeStreamJSON:
	case sourceTypeCapabilityProbe:
		return []canonical.ModelInteraction{}, nil
	default:
		return nil, fmt.Errorf("unsupported Cursor payload source_type %q", document.Payload.SourceType)
	}
	interaction, ok, err := resultInteraction(document, capturedAt, fingerprint)
	if err != nil {
		return nil, err
	}
	if !ok {
		return []canonical.ModelInteraction{}, nil
	}
	return normalize.CorrelateModelInteractions([]canonical.ModelInteraction{interaction}), nil
}

func resultInteraction(document adapterDocument, capturedAt time.Time, fingerprint func([]byte) string) (canonical.ModelInteraction, bool, error) {
	if document.Payload.Result == nil {
		return canonical.ModelInteraction{}, false, errors.New("cursor payload.result must be present for result source_type")
	}
	sessionID, err := normalize.RequiredString(document.Payload.Result, "session_id")
	if err != nil {
		return canonical.ModelInteraction{}, false, err
	}
	sessionFingerprint := "cursor-agent:" + fingerprint([]byte(sessionID))

	requestID := sessionFingerprint + ":result"
	if rawRequest := normalize.OptionalString(document.Payload.Result, "request_id"); rawRequest != nil {
		requestID = "cursor-agent:" + fingerprint([]byte(*rawRequest))
	}

	model := "unknown"
	modelObserved := false
	if document.Payload.SourceType == sourceTypeStreamJSON && document.Payload.Init != nil {
		model, modelObserved = normalize.ObservedString(document.Payload.Init["model"])
	}

	usage, ok := document.Payload.Result["usage"].(map[string]any)
	if !ok {
		usage = map[string]any{}
	}
	inputTokens := normalize.OptionalTokenCount(usage["inputTokens"])
	outputTokens := normalize.OptionalTokenCount(usage["outputTokens"])
	cacheReadTokens := normalize.OptionalTokenCount(usage["cacheReadTokens"])
	cacheWriteTokens := normalize.OptionalTokenCount(usage["cacheWriteTokens"])

	// If the fixture has neither model nor any token, it is not a model interaction.
	if !modelObserved && inputTokens == nil && outputTokens == nil && cacheReadTokens == nil {
		return canonical.ModelInteraction{}, false, nil
	}

	duration := normalize.OptionalTokenCount(document.Payload.Result["duration_ms"])
	completed := capturedAt
	started := completed
	startedDerived := false
	if duration != nil {
		started = completed.Add(-time.Duration(*duration) * time.Millisecond)
		startedDerived = true
	}

	result := "unknown"
	var errorCode *string
	if status, ok := resultStatus(document.Payload.Result); ok {
		result = status
		if status == "failed" {
			// Cursor fixtures do not carry a reviewed error code; keep it nil.
			errorCode = nil
		}
	}

	extensions := recordExtensions(document, requestID, started, startedDerived, cacheWriteTokens)
	attachOutcomeContract(extensions, document.Payload.Result, modelObserved, model, inputTokens, outputTokens)

	return canonical.ModelInteraction{
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
		CachedInputTokens:  cacheReadTokens,
		ReasoningTokens:    nil,
		Result:             result,
		ErrorCode:          errorCode,
		Provenance:         normalize.InteractionProvenance(modelObserved, inputTokens, outputTokens),
		ProviderExtensions: extensions,
	}, true, nil
}

func recordExtensions(document adapterDocument, requestID string, started time.Time, startedDerived bool, cacheWriteTokens *int64) map[string]any {
	startedProvenance := "observed_equals_completed"
	if startedDerived {
		startedProvenance = "derived_from_duration"
	}
	extensions := map[string]any{
		"correlation": map[string]any{
			"dedup_key":    requestID,
			"ordering_key": fmt.Sprintf("%020d:%s", started.UnixNano(), requestID),
			"task_boundary": map[string]any{
				"confidence": "unknown",
				"reason":     "Cursor Agent fixtures have no reviewed task-boundary signal",
			},
		},
		"timestamps": map[string]any{
			"completed_at": "captured_at",
			"started_at":   startedProvenance,
		},
		"source_type": document.Payload.SourceType,
		"result":      normalize.UnknownFields(document.Payload.Result, promotedRecordFields...),
	}
	if document.Payload.Init != nil {
		extensions["init"] = normalize.UnknownFields(document.Payload.Init, "type", "subtype", "model", "session_id")
	}
	if cacheWriteTokens != nil {
		extensions["cache_write_tokens"] = *cacheWriteTokens
	}
	return extensions
}
