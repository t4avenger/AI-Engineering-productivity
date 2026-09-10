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

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/normalize/claude"
	"github.com/wayne/telemetryiq/internal/normalize/codex"
	"github.com/wayne/telemetryiq/internal/storage"
)

const maxOTLPPayloadBytes int64 = 1 << 20 // 1 MiB

// otlpHTTPIngest receives OTLP/HTTP payloads, normalises them, and persists the
// resulting canonical events verbatim — no ingest-time hiding is applied
// (epic #87).
type otlpHTTPIngest struct {
	accepted   atomic.Uint64
	rejected   atomic.Uint64
	inspector  *ingestInspector
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

func newOTLPHTTPIngest(inspector *ingestInspector, repository storage.Repository) *otlpHTTPIngest {
	return &otlpHTTPIngest{inspector: inspector, repository: repository}
}

func (i *otlpHTTPIngest) tracesHandler(w http.ResponseWriter, r *http.Request) {
	// Observed first-class providers export behaviour signals as OTLP logs.
	// Accepting traces with 202 while dropping them misleads exporters (issue #50).
	i.rejectUnsupportedSignal(w, r, "traces")
}

func (i *otlpHTTPIngest) metricsHandler(w http.ResponseWriter, r *http.Request) {
	i.receive(w, r, "resourceMetrics")
}

func (i *otlpHTTPIngest) logsHandler(w http.ResponseWriter, r *http.Request) {
	i.receive(w, r, "resourceLogs")
}

// rejectUnsupportedSignal refuses OTLP signal paths that are not persisted in
// the MVP. The body is drained within the size limit so clients can reuse the
// connection; the payload is never inspected, normalised, or stored.
func (i *otlpHTTPIngest) rejectUnsupportedSignal(w http.ResponseWriter, r *http.Request, signal string) {
	if r.Body != nil {
		_, _ = io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, maxOTLPPayloadBytes+1))
	}
	i.reject(
		w,
		http.StatusNotImplemented,
		"not_implemented",
		fmt.Sprintf("OTLP %s are not persisted; export logs to POST /v1/logs", signal),
	)
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
	if err := i.persistSignal(r, resourceField, payload); err != nil {
		status, code, message := ingestPersistenceError(err)
		i.reject(w, status, code, message)
		return
	}
	i.accepted.Add(1)
	w.WriteHeader(http.StatusAccepted)
}

func (i *otlpHTTPIngest) persistSignal(request *http.Request, resourceField string, payload map[string]json.RawMessage) error {
	switch resourceField {
	case "resourceLogs":
		return i.persistLogs(request, payload)
	case "resourceMetrics":
		return i.persistMetrics(request, payload)
	default:
		return nil
	}
}

func rawPayload(payload map[string]json.RawMessage) ([]byte, error) {
	raw := make(map[string]any, len(payload))
	for key, value := range payload {
		var decoded any
		if err := json.Unmarshal(value, &decoded); err != nil {
			return nil, fmt.Errorf("decode payload: %w", err)
		}
		raw[key] = decoded
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return data, nil
}

// persistLogs routes a raw OTLP log payload through every per-tool log adapter.
// Each adapter normalises only the resources whose service.name it recognises
// (Codex: codex_cli_rs/codex_exec; Claude Code: claude-code) and returns
// ErrUnsupportedLogs for a payload with none of its own, so a payload from an
// unknown tool — or one mixing tools — is handled safely rather than
// misattributed. The payload is normalised verbatim — no ingest-time hiding is
// applied (epic #87).
func (i *otlpHTTPIngest) persistLogs(request *http.Request, payload map[string]json.RawMessage) error {
	if i.repository == nil {
		return nil
	}
	rawBytes, err := rawPayload(payload)
	if err != nil {
		return err
	}
	receivedAt := time.Now().UTC()

	var events []canonical.Event
	codexEvents, err := codex.NormalizeLogs(rawBytes, receivedAt)
	if err != nil && !errors.Is(err, codex.ErrUnsupportedLogs) {
		return fmt.Errorf("normalise: %w", err)
	}
	events = append(events, codexEvents...)

	claudeEvents, err := claude.NormalizeLogs(rawBytes, receivedAt)
	if err != nil && !errors.Is(err, claude.ErrUnsupportedLogs) {
		return fmt.Errorf("normalise: %w", err)
	}
	events = append(events, claudeEvents...)

	if len(events) == 0 {
		return nil
	}
	if err := i.repository.SaveEvents(request.Context(), events); err != nil {
		return fmt.Errorf("persist: %w", err)
	}
	return nil
}

// persistMetrics normalises a raw OTLP metrics payload and persists only
// reviewed Codex skill-injection datapoints as canonical events. Non-skill
// metrics are accepted at the HTTP layer (so exporters do not retry) but produce
// no events.
func (i *otlpHTTPIngest) persistMetrics(request *http.Request, payload map[string]json.RawMessage) error {
	if i.repository == nil {
		return nil
	}
	rawBytes, err := rawPayload(payload)
	if err != nil {
		return err
	}
	receivedAt := time.Now().UTC()

	events, err := codex.NormalizeMetrics(rawBytes, receivedAt)
	if err != nil && !errors.Is(err, codex.ErrUnsupportedMetrics) {
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
