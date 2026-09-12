package api

import (
	"bytes"
	"compress/gzip"
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
	"github.com/wayne/telemetryiq/internal/normalize/cursor"
	"github.com/wayne/telemetryiq/internal/storage"
)

const maxOTLPPayloadBytes int64 = 1 << 20 // 1 MiB

const (
	otlpErrPayloadTooLarge      = "request body exceeds the 1 MiB limit"
	otlpErrUnsupportedMediaType = "Content-Type must be application/json or application/x-protobuf"
)

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
	// Claude Code's enhanced-telemetry beta exports a span hierarchy
	// (interaction → llm_request/tool/hook) that logs do not carry; it is
	// normalised and persisted here, resolving the /v1/traces 501 (issue #50).
	i.receive(w, r, "resourceSpans")
}

func (i *otlpHTTPIngest) metricsHandler(w http.ResponseWriter, r *http.Request) {
	i.receive(w, r, "resourceMetrics")
}

func (i *otlpHTTPIngest) logsHandler(w http.ResponseWriter, r *http.Request) {
	i.receive(w, r, "resourceLogs")
}

func (i *otlpHTTPIngest) receive(w http.ResponseWriter, r *http.Request, resourceField string) {
	mediaType, ok := i.parseOTLPMediaType(w, r, resourceField)
	if !ok {
		return
	}
	if r.ContentLength > maxOTLPPayloadBytes {
		i.reject(w, http.StatusRequestEntityTooLarge, "payload_too_large", otlpErrPayloadTooLarge)
		return
	}

	payload, ok := i.decodeOTLPBody(w, r, mediaType, resourceField)
	if !ok {
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

func (i *otlpHTTPIngest) parseOTLPMediaType(w http.ResponseWriter, r *http.Request, resourceField string) (string, bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		i.reject(w, http.StatusUnsupportedMediaType, "unsupported_media_type", otlpErrUnsupportedMediaType)
		return "", false
	}
	switch mediaType {
	case otlpContentTypeJSON:
		return mediaType, true
	case otlpContentTypeProtobuf:
		if !otlpAcceptsProtobuf(resourceField) {
			i.reject(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type application/x-protobuf is supported on /v1/logs and /v1/metrics only")
			return "", false
		}
		return mediaType, true
	default:
		i.reject(w, http.StatusUnsupportedMediaType, "unsupported_media_type", otlpErrUnsupportedMediaType)
		return "", false
	}
}

func (i *otlpHTTPIngest) decodeOTLPBody(w http.ResponseWriter, r *http.Request, mediaType, resourceField string) (map[string]json.RawMessage, bool) {
	raw, ok := i.readOTLPBodyBytes(w, r)
	if !ok {
		return nil, false
	}
	switch mediaType {
	case otlpContentTypeJSON:
		return i.decodeOTLPJSON(w, bytes.NewReader(raw))
	case otlpContentTypeProtobuf:
		return i.decodeOTLPProtobufBody(w, raw, resourceField)
	default:
		i.reject(w, http.StatusUnsupportedMediaType, "unsupported_media_type", otlpErrUnsupportedMediaType)
		return nil, false
	}
}

// readOTLPBodyBytes reads the request entity (max 1 MiB) and optionally gunzips
// it when Content-Encoding is gzip, also capping decompressed size at 1 MiB.
func (i *otlpHTTPIngest) readOTLPBodyBytes(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	limited := http.MaxBytesReader(w, r.Body, maxOTLPPayloadBytes)
	raw, err := io.ReadAll(limited)
	if err != nil {
		if isBodyTooLarge(err) {
			i.reject(w, http.StatusRequestEntityTooLarge, "payload_too_large", otlpErrPayloadTooLarge)
			return nil, false
		}
		i.reject(w, http.StatusBadRequest, "malformed_payload", "request body could not be read")
		return nil, false
	}

	encoding := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding")))
	switch encoding {
	case "", "identity":
		return raw, true
	case "gzip":
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			i.reject(w, http.StatusBadRequest, "malformed_payload", "request body must be valid gzip")
			return nil, false
		}
		decompressed, readErr := io.ReadAll(io.LimitReader(zr, maxOTLPPayloadBytes+1))
		closeErr := zr.Close()
		if readErr != nil || closeErr != nil {
			i.reject(w, http.StatusBadRequest, "malformed_payload", "request body must be valid gzip")
			return nil, false
		}
		if int64(len(decompressed)) > maxOTLPPayloadBytes {
			i.reject(w, http.StatusRequestEntityTooLarge, "payload_too_large", otlpErrPayloadTooLarge)
			return nil, false
		}
		return decompressed, true
	default:
		i.reject(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Encoding must be identity or gzip")
		return nil, false
	}
}

func (i *otlpHTTPIngest) decodeOTLPJSON(w http.ResponseWriter, body io.Reader) (map[string]json.RawMessage, bool) {
	var payload map[string]json.RawMessage
	decoder := json.NewDecoder(body)
	if err := decoder.Decode(&payload); err != nil {
		i.reject(w, http.StatusBadRequest, "malformed_payload", "request body must be valid OTLP JSON")
		return nil, false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		i.reject(w, http.StatusBadRequest, "malformed_payload", "request body must contain one JSON value")
		return nil, false
	}
	return payload, true
}

func (i *otlpHTTPIngest) decodeOTLPProtobufBody(w http.ResponseWriter, raw []byte, resourceField string) (map[string]json.RawMessage, bool) {
	payload, err := decodeOTLPProtobuf(raw, resourceField)
	if err != nil {
		i.reject(w, http.StatusBadRequest, "malformed_payload", "request body must be valid OTLP protobuf")
		return nil, false
	}
	return payload, true
}

func (i *otlpHTTPIngest) persistSignal(request *http.Request, resourceField string, payload map[string]json.RawMessage) error {
	switch resourceField {
	case "resourceLogs":
		return i.persistLogs(request, payload)
	case "resourceMetrics":
		return i.persistMetrics(request, payload)
	case "resourceSpans":
		return i.persistTraces(request, payload)
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
// (Codex: codex_cli_rs/codex_exec; Claude Code: claude-code; Cursor Enterprise:
// cursor) and returns ErrUnsupportedLogs for a payload with none of its own, so
// a payload from an unknown tool — or one mixing tools — is handled safely
// rather than misattributed. The payload is normalised verbatim — no ingest-time
// hiding is applied (epic #87).
func (i *otlpHTTPIngest) persistLogs(request *http.Request, payload map[string]json.RawMessage) error {
	return i.persistWithAdapters(request, payload,
		adapterPass{fn: codex.NormalizeLogs, unsupported: codex.ErrUnsupportedLogs},
		adapterPass{fn: claude.NormalizeLogs, unsupported: claude.ErrUnsupportedLogs},
		adapterPass{fn: cursor.NormalizeLogs, unsupported: cursor.ErrUnsupportedLogs},
	)
}

// persistMetrics routes a raw OTLP metrics payload through every per-tool metrics
// adapter. Each adapter normalises only the resources whose service.name it
// recognises (Codex: codex_cli_rs/codex_exec; Claude Code: claude-code; Cursor
// Enterprise: cursor) and returns ErrUnsupportedMetrics for a payload with none
// of its own, so a payload from an unknown tool — or one mixing tools — is
// handled safely rather than misattributed. A non-sentinel error from any
// adapter aborts the whole batch: a malformed resource never lets half a mixed
// batch persist. Metrics the adapters do not map are accepted at the HTTP layer
// (so exporters do not retry) but produce no events. The payload is normalised
// verbatim — no ingest-time hiding is applied (epic #87).
func (i *otlpHTTPIngest) persistMetrics(request *http.Request, payload map[string]json.RawMessage) error {
	return i.persistWithAdapters(request, payload,
		adapterPass{fn: codex.NormalizeMetrics, unsupported: codex.ErrUnsupportedMetrics},
		adapterPass{fn: claude.NormalizeMetrics, unsupported: claude.ErrUnsupportedMetrics},
		adapterPass{fn: cursor.NormalizeMetrics, unsupported: cursor.ErrUnsupportedMetrics},
	)
}

// persistTraces routes a raw OTLP traces payload through every per-tool traces
// adapter. Each adapter normalises only the resources whose service.name it
// recognises (Claude Code: claude-code) and returns ErrUnsupportedTraces for a
// payload with none of its own, so a payload from an unknown tool — or one
// mixing tools — is handled safely rather than misattributed. A non-sentinel
// error aborts the whole batch: a malformed span never lets half a mixed batch
// persist, so the route never silently 202-accepts and drops supported Claude
// trace data (#50/#49). Codex trace ingest is tracked separately (#112); this
// path keeps the multi-adapter shape so it can slot in. The payload is
// normalised verbatim — no ingest-time hiding is applied (epic #87).
func (i *otlpHTTPIngest) persistTraces(request *http.Request, payload map[string]json.RawMessage) error {
	return i.persistWithAdapters(request, payload,
		adapterPass{fn: claude.NormalizeTraces, unsupported: claude.ErrUnsupportedTraces},
	)
}

type adapterPass struct {
	fn          func([]byte, time.Time) ([]canonical.Event, error)
	unsupported error
}

func (i *otlpHTTPIngest) persistWithAdapters(request *http.Request, payload map[string]json.RawMessage, passes ...adapterPass) error {
	rawBytes, receivedAt, skip, err := i.beginPersist(payload)
	if skip || err != nil {
		return err
	}
	var events []canonical.Event
	for _, pass := range passes {
		next, normErr := pass.fn(rawBytes, receivedAt)
		if normErr != nil && !errors.Is(normErr, pass.unsupported) {
			return fmt.Errorf("normalise: %w", normErr)
		}
		events = append(events, next...)
	}
	return i.savePersistedEvents(request, events)
}

func (i *otlpHTTPIngest) beginPersist(payload map[string]json.RawMessage) ([]byte, time.Time, bool, error) {
	if i.repository == nil {
		return nil, time.Time{}, true, nil
	}
	rawBytes, err := rawPayload(payload)
	if err != nil {
		return nil, time.Time{}, false, err
	}
	return rawBytes, time.Now().UTC(), false, nil
}

func (i *otlpHTTPIngest) savePersistedEvents(request *http.Request, events []canonical.Event) error {
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
