package api

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/codex"
	"github.com/wayne/telemetryiq/internal/storage"
)

// codexRolloutIngest deliberately has no development inspector: rollout files
// contain raw prompts, responses, tool bodies, paths, commands, identity, and
// provider extensions which must be stored locally but never echoed as diagnostics.
type codexRolloutIngest struct {
	repository storage.Repository
	counters   *otlpHTTPIngest
}

func newCodexRolloutIngest(counters *otlpHTTPIngest, repository storage.Repository) *codexRolloutIngest {
	return &codexRolloutIngest{repository: repository, counters: counters}
}

func (i *codexRolloutIngest) handler(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !isTranscriptMediaType(mediaType) {
		i.counters.reject(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/x-ndjson or application/jsonl")
		return
	}
	if r.ContentLength > maxTranscriptPayloadBytes {
		i.counters.reject(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds the 32 MiB limit")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxTranscriptPayloadBytes))
	if err != nil {
		if isBodyTooLarge(err) {
			i.counters.reject(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds the 32 MiB limit")
			return
		}
		i.counters.reject(w, http.StatusBadRequest, "malformed_payload", "request body could not be read")
		return
	}
	events, err := codex.NormalizeRollout(body, time.Now().UTC())
	if err != nil {
		if errors.Is(err, codex.ErrMalformedRollout) {
			i.counters.reject(w, http.StatusBadRequest, "malformed_payload", "request body must be valid newline-delimited JSON")
			return
		}
		i.counters.reject(w, http.StatusUnprocessableEntity, "normalization_failed", "supported rollout records could not be normalised")
		return
	}
	if i.repository != nil {
		if err := i.repository.SaveEvents(r.Context(), events); err != nil {
			i.counters.reject(w, http.StatusInternalServerError, "persistence_failed", "supported telemetry could not be stored")
			return
		}
	}
	i.counters.accepted.Add(1)
	w.WriteHeader(http.StatusAccepted)
}
