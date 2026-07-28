package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/codex"
	"github.com/wayne/telemetryiq/internal/privacy"
	"github.com/wayne/telemetryiq/internal/storage"
)

const maxOTLPPayloadBytes int64 = 1 << 20 // 1 MiB

// otlpHTTPIngest is deliberately transient. Task 004 proves that TelemetryIQ
// can receive OTLP/HTTP safely; later tasks redact and persist canonical data.
type otlpHTTPIngest struct {
	accepted   atomic.Uint64
	rejected   atomic.Uint64
	inspector  *sanitizedInspector
	sanitizer  *privacy.Sanitizer
	repository storage.Repository
}

type ingestCountersResponse struct {
	AcceptedPayloads uint64 `json:"accepted_payloads"`
	RejectedPayloads uint64 `json:"rejected_payloads"`
}

type ingestErrorResponse struct {
	Error ingestError `json:"error"`
}

type ingestError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func newOTLPHTTPIngest(inspector *sanitizedInspector, sanitizer *privacy.Sanitizer, repository storage.Repository) *otlpHTTPIngest {
	return &otlpHTTPIngest{inspector: inspector, sanitizer: sanitizer, repository: repository}
}

func (i *otlpHTTPIngest) tracesHandler(w http.ResponseWriter, r *http.Request) {
	i.receive(w, r, "resourceSpans")
}

func (i *otlpHTTPIngest) logsHandler(w http.ResponseWriter, r *http.Request) {
	i.receive(w, r, "resourceLogs")
}

func (i *otlpHTTPIngest) receive(w http.ResponseWriter, r *http.Request, resourceField string) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		i.reject(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return
	}
	if r.ContentLength > maxOTLPPayloadBytes {
		i.reject(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds the 1 MiB limit")
		return
	}

	var payload map[string]json.RawMessage
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxOTLPPayloadBytes))
	if err := decoder.Decode(&payload); err != nil {
		if isBodyTooLarge(err) {
			i.reject(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds the 1 MiB limit")
			return
		}
		i.reject(w, http.StatusBadRequest, "malformed_payload", "request body must be valid OTLP JSON")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		i.reject(w, http.StatusBadRequest, "malformed_payload", "request body must contain one JSON value")
		return
	}

	var resources []json.RawMessage
	if len(payload) != 1 || len(payload[resourceField]) == 0 || json.Unmarshal(payload[resourceField], &resources) != nil || len(resources) == 0 {
		i.reject(w, http.StatusBadRequest, "invalid_payload", "request must contain a non-empty "+resourceField+" array")
		return
	}

	if i.inspector != nil {
		i.inspector.capture(payload)
	}
	if resourceField == "resourceLogs" {
		if err := i.persistCodexLogs(r, payload); err != nil {
			status, code, message := ingestPersistenceError(err)
			i.reject(w, status, code, message)
			return
		}
	}
	// Raw payloads are never persisted or logged; only canonical sanitised events are stored.
	i.accepted.Add(1)
	w.WriteHeader(http.StatusAccepted)
}

func (i *otlpHTTPIngest) persistCodexLogs(request *http.Request, payload map[string]json.RawMessage) error {
	if i.repository == nil || i.sanitizer == nil {
		return nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	events, err := codex.NormalizeLogs(raw, time.Now().UTC(), func(value []byte) string { return i.sanitizer.Fingerprint(value) })
	if errors.Is(err, codex.ErrUnsupportedLogs) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("normalise: %w", err)
	}
	if len(events) == 0 {
		return nil
	}
	if err := i.repository.SaveEvents(request.Context(), events); err != nil {
		return fmt.Errorf("persist: %w", err)
	}
	return nil
}

func ingestPersistenceError(err error) (int, string, string) {
	if strings.HasPrefix(err.Error(), "normalise:") {
		return http.StatusUnprocessableEntity, "normalization_failed", "supported telemetry could not be normalised"
	}
	return http.StatusInternalServerError, "persistence_failed", "supported telemetry could not be stored"
}

func (i *otlpHTTPIngest) countersHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ingestCountersResponse{
		AcceptedPayloads: i.accepted.Load(),
		RejectedPayloads: i.rejected.Load(),
	})
}

func (i *otlpHTTPIngest) reject(w http.ResponseWriter, status int, code, message string) {
	i.rejected.Add(1)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ingestErrorResponse{Error: ingestError{Code: code, Message: message}})
}

func isBodyTooLarge(err error) bool {
	var maxBytesErr *http.MaxBytesError
	return errors.As(err, &maxBytesErr)
}
