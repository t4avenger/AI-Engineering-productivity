package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/wayne/telemetryiq/internal/normalize/cursor"
	"github.com/wayne/telemetryiq/internal/privacy"
	"github.com/wayne/telemetryiq/internal/storage"
)

const maxCursorPayloadBytes int64 = 1 << 20 // 1 MiB

type cursorAgentIngest struct {
	inspector  *sanitizedInspector
	sanitizer  *privacy.Sanitizer
	repository storage.Repository
}

func newCursorAgentIngest(inspector *sanitizedInspector, sanitizer *privacy.Sanitizer, repository storage.Repository) *cursorAgentIngest {
	return &cursorAgentIngest{inspector: inspector, sanitizer: sanitizer, repository: repository}
}

func (i *cursorAgentIngest) handler(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeCursorIngestError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return
	}
	if r.ContentLength > maxCursorPayloadBytes {
		writeCursorIngestError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds the 1 MiB limit")
		return
	}

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCursorPayloadBytes))
	var raw map[string]any
	if err := decoder.Decode(&raw); err != nil {
		if isBodyTooLarge(err) {
			writeCursorIngestError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds the 1 MiB limit")
			return
		}
		writeCursorIngestError(w, http.StatusBadRequest, "malformed_payload", "request body must be valid JSON")
		return
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		writeCursorIngestError(w, http.StatusBadRequest, "malformed_payload", "request body must contain one JSON value")
		return
	}

	if err := validateCursorAgentEnvelope(raw); err != nil {
		writeCursorIngestError(w, http.StatusBadRequest, "invalid_payload", err.Error())
		return
	}

	if i.repository == nil || i.sanitizer == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Sanitise before the adapter sees the payload, mirroring OTLP ingest.
	sanitized := i.sanitizer.Sanitize(raw)
	if i.inspector != nil {
		i.inspector.captureValue(raw)
	}
	safeBytes, err := json.Marshal(sanitized.Value)
	if err != nil {
		writeCursorIngestError(w, http.StatusInternalServerError, "persistence_failed", "supported telemetry could not be stored")
		return
	}
	fingerprint := func(value []byte) string { return i.sanitizer.Fingerprint(value) }

	events, err := cursor.NormalizeIngest(safeBytes, fingerprint)
	if err != nil {
		writeCursorIngestError(w, http.StatusUnprocessableEntity, "normalization_failed", "supported telemetry could not be normalised")
		return
	}
	if len(events) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if err := i.repository.SaveEvents(r.Context(), events); err != nil {
		writeCursorIngestError(w, http.StatusInternalServerError, "persistence_failed", "supported telemetry could not be stored")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func writeCursorIngestError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ingestErrorResponse{Error: ingestError{Code: code, Message: message}})
}

func validateCursorAgentEnvelope(raw map[string]any) error {
	if err := validateAllowedKeys(raw, map[string]struct{}{
		"provider":     {},
		"tool":         {},
		"tool_version": {},
		"captured_at":  {},
		"payload":      {},
	}, "top-level"); err != nil {
		return err
	}

	if provider, _ := raw["provider"].(string); strings.TrimSpace(provider) != "cursor" {
		return errors.New("provider must be cursor")
	}
	if tool, _ := raw["tool"].(string); strings.TrimSpace(tool) != "cursor-agent" {
		return errors.New("tool must be cursor-agent")
	}
	if version, _ := raw["tool_version"].(string); strings.TrimSpace(version) == "" {
		return errors.New("tool_version must be a non-empty string")
	}
	if captured, _ := raw["captured_at"].(string); strings.TrimSpace(captured) == "" {
		return errors.New("captured_at must be an RFC3339 timestamp")
	}

	payload, ok := raw["payload"].(map[string]any)
	if !ok {
		return errors.New("payload must be an object")
	}
	return validateCursorAgentPayload(payload)
}

func validateCursorAgentPayload(payload map[string]any) error {
	if err := validateAllowedKeys(payload, map[string]struct{}{
		"source_type": {},
		"init":        {},
		"result":      {},
	}, "payload"); err != nil {
		return err
	}

	sourceType, _ := payload["source_type"].(string)
	sourceType = strings.TrimSpace(sourceType)
	if !isSupportedCursorSourceType(sourceType) {
		return errors.New("payload.source_type must be local_cli_print_json, local_cli_stream_json, or local_cli_capability_probe")
	}

	if init, ok := payload["init"]; ok {
		m, ok := init.(map[string]any)
		if !ok {
			return errors.New("payload.init must be an object")
		}
		if err := validateCursorInit(m); err != nil {
			return err
		}
	}

	if result, ok := payload["result"]; ok {
		m, ok := result.(map[string]any)
		if !ok {
			return errors.New("payload.result must be an object")
		}
		return validateCursorResult(m)
	}
	if sourceType == "local_cli_capability_probe" {
		return nil
	}
	return errors.New("payload.result is required for result source_type")
}

func validateAllowedKeys(value map[string]any, allowed map[string]struct{}, label string) error {
	for key := range value {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("unsupported %s field %q", label, key)
		}
	}
	return nil
}

func isSupportedCursorSourceType(sourceType string) bool {
	switch sourceType {
	case "local_cli_print_json", "local_cli_stream_json", "local_cli_capability_probe":
		return true
	default:
		return false
	}
}

func validateCursorInit(init map[string]any) error {
	allowed := map[string]struct{}{
		"type":       {},
		"subtype":    {},
		"model":      {},
		"session_id": {},
	}
	for key := range init {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("unsupported init field %q", key)
		}
	}
	return nil
}

func validateCursorResult(result map[string]any) error {
	allowed := map[string]struct{}{
		"type":            {},
		"subtype":         {},
		"is_error":        {},
		"duration_ms":     {},
		"duration_api_ms": {},
		"session_id":      {},
		"request_id":      {},
		"usage":           {},
	}
	for key := range result {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("unsupported result field %q", key)
		}
	}
	usage, ok := result["usage"].(map[string]any)
	if !ok {
		return errors.New("result.usage must be an object")
	}
	allowedUsage := map[string]struct{}{
		"inputTokens":      {},
		"outputTokens":     {},
		"cacheReadTokens":  {},
		"cacheWriteTokens": {},
	}
	for key := range usage {
		if _, ok := allowedUsage[key]; !ok {
			return fmt.Errorf("unsupported usage field %q", key)
		}
	}
	return nil
}
