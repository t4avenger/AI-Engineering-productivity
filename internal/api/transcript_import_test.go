package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
	"github.com/wayne/telemetryiq/internal/storage/sqlite"
)

// transcriptE2ESessionID is shared by the OTLP log and the JSONL transcript in
// the end-to-end gate: both must correlate into the one session by session.id
// string equality, proving the transcript path merges with OTLP rather than
// creating a parallel session.
const transcriptE2ESessionID = "transcript-e2e-session"

// e2eTranscriptNDJSON is a synthetic session JSONL transcript in the real
// on-disk newline-delimited format: a skipped user record and one assistant
// record carrying the model + full token usage. Its content bodies
// (message.content[], tool input.command/file_path, toolUseResult, cwd) hold
// canaries that must never reach storage or the read API.
const e2eTranscriptNDJSON = `{"type":"user","uuid":"e2e-user-1","sessionId":"transcript-e2e-session","timestamp":"2026-09-12T10:00:00.000Z","version":"2.1.269","message":{"role":"user","content":"tiq-canary-user-prompt"}}
{"type":"assistant","uuid":"e2e-assistant-1","parentUuid":"e2e-user-1","sessionId":"transcript-e2e-session","timestamp":"2026-09-12T10:00:02.500Z","version":"2.1.269","cwd":"/home/tiq-canary-cwd/project","gitBranch":"main","requestId":"req_e2e_1","message":{"role":"assistant","model":"claude-opus-4-8","stop_reason":"end_turn","content":[{"type":"text","text":"tiq-canary-response"},{"type":"thinking","thinking":"tiq-canary-thinking"},{"type":"tool_use","name":"Bash","input":{"command":"tiq-canary-command"}},{"type":"tool_use","name":"Edit","input":{"file_path":"/tiq-canary-file-path"}}],"usage":{"input_tokens":4096,"output_tokens":512,"cache_read_input_tokens":8192,"cache_creation_input_tokens":128,"output_tokens_details":{"thinking_tokens":64}}},"toolUseResult":{"stdout":"tiq-canary-stdout"}}`

var transcriptContentCanaries = []string{
	"tiq-canary-user-prompt",
	"tiq-canary-response",
	"tiq-canary-thinking",
	"tiq-canary-command",
	"tiq-canary-file-path",
	"tiq-canary-stdout",
	"tiq-canary-cwd",
}

// TestTranscriptImportMergesWithOTLPSession is the live end-to-end gate: it
// ingests a Claude OTLP log for a session, then POSTs a JSONL transcript for the
// SAME session to /v1/claude/transcript, and proves both merge into one
// anthropic/claude-code session whose transcript model + token counts surface
// through the read API, while none of the transcript content bodies (nor the raw
// transcript payload via the dev inspector) are ever exposed.
func TestTranscriptImportMergesWithOTLPSession(t *testing.T) {
	repository, server := transcriptTestServer(t, true)
	postAcceptedOTLP(t, server.URL, "/v1/logs", claudeOTLPLogPayload(t, []any{
		otlpStringAttr("event.name", "api_request"),
		otlpStringAttr("event.timestamp", "2026-09-12T09:59:58.000Z"),
		otlpStringAttr("session.id", transcriptE2ESessionID),
		otlpStringAttr("model", "claude-opus-4-8"),
	}))
	postAcceptedTranscript(t, server.URL, e2eTranscriptNDJSON)

	wantSessionID := "claude-code:" + transcriptE2ESessionID
	sessions := requireMergedClaudeSession(t, repository, wantSessionID)
	timeline := getInsightJSON[eventListResponse](t, server.URL+"/api/v1/sessions/"+wantSessionID+"/events")
	assertTranscriptAssistantTokens(t, requireTranscriptAssistantEvent(t, timeline))
	assertTranscriptContentAbsent(t, repository, wantSessionID, timeline, sessions, server.URL)
	if sessions[0].State == "" {
		t.Fatalf("session state must not be empty: %#v", sessions[0])
	}
}

// TestTranscriptImportRejectsWrongMediaType proves the route rejects a body that
// is not NDJSON with 415 and increments the shared rejected counter, so a
// misconfigured client is refused rather than silently mis-parsed.
func TestTranscriptImportRejectsWrongMediaType(t *testing.T) {
	_, server := transcriptTestServer(t, false)

	resp := postOTLPToPath(t, server.URL, "/v1/claude/transcript", []byte(e2eTranscriptNDJSON), "application/json")
	assertIngestError(t, resp, http.StatusUnsupportedMediaType, "unsupported_media_type")

	counters := getInsightJSON[ingestCountersResponse](t, server.URL+"/api/v1/ingest/counters")
	if counters.RejectedPayloads != 1 {
		t.Fatalf("rejected payloads = %d, want 1 (shared with OTLP counters)", counters.RejectedPayloads)
	}
}

// TestTranscriptImportRejectsMalformedJSONWith400 mirrors OTLP: invalid JSONL
// syntax is malformed_payload (400), not normalization_failed (422).
func TestTranscriptImportRejectsMalformedJSONWith400(t *testing.T) {
	_, server := transcriptTestServer(t, false)
	resp := postOTLPToPath(t, server.URL, "/v1/claude/transcript", []byte("not-json\n"), "application/x-ndjson")
	assertIngestError(t, resp, http.StatusBadRequest, "malformed_payload")
}

// TestTranscriptImportRejectsStructuralFailureWith422 reserves 422 for valid
// JSON whose supported assistant records are missing required fields.
func TestTranscriptImportRejectsStructuralFailureWith422(t *testing.T) {
	_, server := transcriptTestServer(t, false)
	body := `{"type":"assistant","uuid":"a1","sessionId":"s1","message":{"model":"claude-opus-4-8"}}`
	resp := postOTLPToPath(t, server.URL, "/v1/claude/transcript", []byte(body), "application/x-ndjson")
	assertIngestError(t, resp, http.StatusUnprocessableEntity, "normalization_failed")
}

// TestTranscriptImportSharesAcceptedCounter proves a successful transcript import
// increments the same accepted counter the OTLP routes use, so ingest
// observability stays consistent across every push route.
func TestTranscriptImportSharesAcceptedCounter(t *testing.T) {
	_, server := transcriptTestServer(t, false)
	postAcceptedTranscript(t, server.URL, e2eTranscriptNDJSON)

	counters := getInsightJSON[ingestCountersResponse](t, server.URL+"/api/v1/ingest/counters")
	if counters.AcceptedPayloads != 1 {
		t.Fatalf("accepted payloads = %d, want 1 (shared with OTLP counters)", counters.AcceptedPayloads)
	}
}

func transcriptTestServer(t *testing.T, development bool) (*sqlite.Repository, *httptest.Server) {
	t.Helper()
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	var handler http.Handler
	if development {
		handler = NewPersistentDevelopmentHandler(slog.Default(), repository)
	} else {
		handler = NewPersistentHandler(slog.Default(), repository)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return repository, server
}

func postAcceptedTranscript(t *testing.T, baseURL, body string) {
	t.Helper()
	resp := postOTLPToPath(t, baseURL, "/v1/claude/transcript", []byte(body), "application/x-ndjson")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("transcript import status = %d, want 202", resp.StatusCode)
	}
	closeBody(t, resp)
}

func requireMergedClaudeSession(t *testing.T, repository storage.Repository, wantSessionID string) []canonical.Session {
	t.Helper()
	sessions, err := repository.ListSessions(context.Background(), storage.SessionFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("session count = %d, want 1 (OTLP + transcript merged): %#v", len(sessions), sessions)
	}
	if sessions[0].Provider != "anthropic" || sessions[0].Tool != "claude-code" {
		t.Fatalf("session provider/tool = %q/%q", sessions[0].Provider, sessions[0].Tool)
	}
	if sessions[0].SessionID != wantSessionID {
		t.Fatalf("session id = %q, want %q", sessions[0].SessionID, wantSessionID)
	}
	return sessions
}

func assertTranscriptAssistantTokens(t *testing.T, assistant timelineEvent) {
	t.Helper()
	if assistant.Model == nil || *assistant.Model != "claude-opus-4-8" {
		t.Fatalf("transcript event model = %#v", assistant.Model)
	}
	if assistant.InputTokenCount == nil || *assistant.InputTokenCount != "4096" {
		t.Fatalf("transcript input token count = %#v", assistant.InputTokenCount)
	}
	if assistant.OutputTokenCount == nil || *assistant.OutputTokenCount != "512" {
		t.Fatalf("transcript output token count = %#v", assistant.OutputTokenCount)
	}
}

func assertTranscriptContentAbsent(t *testing.T, repository storage.Repository, sessionID string, timeline eventListResponse, sessions []canonical.Session, baseURL string) {
	t.Helper()
	events, err := repository.ListEvents(context.Background(), storage.EventFilter{SessionID: sessionID, Limit: 20})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	assertNoRawIdentifiers(t, transcriptContentCanaries, marshalJSON(t, timeline), marshalJSON(t, sessions), marshalJSON(t, events))
	lastIngest := getInsightJSON[map[string]any](t, baseURL+"/api/v1/development/last-ingest")
	assertNoRawIdentifiers(t, transcriptContentCanaries, marshalJSON(t, lastIngest))
}

func requireTranscriptAssistantEvent(t *testing.T, timeline eventListResponse) timelineEvent {
	t.Helper()
	for _, event := range timeline.Data {
		if event.EventType == "assistant_message" {
			return event
		}
	}
	t.Fatalf("no assistant_message event in timeline: %#v", timeline.Data)
	return timelineEvent{}
}
