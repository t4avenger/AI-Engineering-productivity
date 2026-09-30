package api

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/claude"
	"github.com/wayne/telemetryiq/internal/storage"
)

// maxTranscriptPayloadBytes is larger than the 1 MiB OTLP cap: a real Claude Code
// session JSONL transcript routinely reaches a few MB (the whole conversation in
// one file), and the SessionEnd hook POSTs it whole.
const maxTranscriptPayloadBytes int64 = 32 << 20 // 32 MiB

// transcriptIngest receives a Claude Code session JSONL transcript (F4, #91) as
// newline-delimited JSON — the real on-disk format — normalises it, and persists
// the resulting canonical events and tool-call Operations. Prompt, response,
// thinking, and tool input/output content are retained raw on those normalised
// records (epic #87 / #94 / #105 / #173).
//
// It deliberately does NOT hold an ingestInspector: the dev inspector echoes the
// whole raw request body to a diagnostics surface, which is a separate
// sanitised-diagnostics boundary (AGENTS.md: diagnostic exports must be
// sanitised), not the captured governance record.
//
// Accepted/rejected counters are shared with the OTLP ingest (via counters) so
// /api/v1/ingest/counters reports one consistent ingest tally across every push
// route.
type transcriptIngest struct {
	repository storage.Repository
	counters   *otlpHTTPIngest
}

func newTranscriptIngest(counters *otlpHTTPIngest, repository storage.Repository) *transcriptIngest {
	return &transcriptIngest{repository: repository, counters: counters}
}

func (i *transcriptIngest) handler(w http.ResponseWriter, r *http.Request) {
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

	// A transcript with no assistant records normalises to zero events; still a
	// 202 so a SessionEnd hook shipping an early, assistant-less transcript is
	// not retried. Idempotent re-ingest (a later complete ship) is safe via
	// event_id + INSERT OR IGNORE.
	receivedAt := time.Now().UTC()
	events, err := claude.NormalizeTranscript(body, receivedAt)
	if err != nil {
		if errors.Is(err, claude.ErrMalformedTranscript) {
			i.counters.reject(w, http.StatusBadRequest, "malformed_payload", "request body must be valid newline-delimited JSON")
			return
		}
		i.counters.reject(w, http.StatusUnprocessableEntity, "normalization_failed", "supported transcript records could not be normalised")
		return
	}
	// The MCP tool calls in the transcript also become canonical Operations (#104);
	// they are persisted alongside the events in one batch so the operations and
	// MCP-inventory read paths see them atomically. A re-walk (mirroring the OTLP
	// NormalizeLogs / ExtractLogOperations pair) is safe: a body that normalised
	// cleanly above cannot fail structural extraction here.
	operations, err := claude.ExtractTranscriptOperations(body, receivedAt)
	if err != nil {
		i.counters.reject(w, http.StatusUnprocessableEntity, "normalization_failed", "supported transcript records could not be normalised")
		return
	}
	if i.repository != nil && (len(events) > 0 || len(operations) > 0) {
		if err := i.repository.SaveEventsAndOperations(r.Context(), events, operations); err != nil {
			i.counters.reject(w, http.StatusInternalServerError, "persistence_failed", "supported telemetry could not be stored")
			return
		}
	}
	i.counters.accepted.Add(1)
	w.WriteHeader(http.StatusAccepted)
}

func isTranscriptMediaType(mediaType string) bool {
	switch mediaType {
	case "application/x-ndjson", "application/jsonl":
		return true
	default:
		return false
	}
}
