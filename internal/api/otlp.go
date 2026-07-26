package api

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"sync/atomic"
)

const maxOTLPPayloadBytes int64 = 1 << 20 // 1 MiB

// otlpHTTPIngest is deliberately transient. Task 004 proves that TelemetryIQ
// can receive OTLP/HTTP safely; later tasks redact and persist canonical data.
type otlpHTTPIngest struct {
	accepted atomic.Uint64
	rejected atomic.Uint64
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

func newOTLPHTTPIngest() *otlpHTTPIngest {
	return &otlpHTTPIngest{}
}

func (i *otlpHTTPIngest) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		i.reject(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return
	}
	if r.ContentLength > maxOTLPPayloadBytes {
		i.reject(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds the 1 MiB limit")
		return
	}

	var payload struct {
		ResourceSpans json.RawMessage `json:"resourceSpans"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxOTLPPayloadBytes))
	decoder.DisallowUnknownFields()
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

	var resourceSpans []json.RawMessage
	if len(payload.ResourceSpans) == 0 || json.Unmarshal(payload.ResourceSpans, &resourceSpans) != nil || len(resourceSpans) == 0 {
		i.reject(w, http.StatusBadRequest, "invalid_payload", "request must contain a non-empty resourceSpans array")
		return
	}

	// Do not retain or log the raw payload. The proof only records its receipt.
	i.accepted.Add(1)
	w.WriteHeader(http.StatusAccepted)
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
