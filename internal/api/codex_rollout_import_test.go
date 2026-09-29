package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wayne/telemetryiq/internal/storage"
	"github.com/wayne/telemetryiq/internal/storage/sqlite"
)

const (
	rolloutTestToken   = "rollout-test-token"
	rolloutTestSession = "synthetic-codex-0.157.1-session"
	rolloutOTLPLog     = `{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_cli_rs"}},{"key":"service.version","value":{"stringValue":"0.157.1"}}]},"scopeLogs":[{"logRecords":[{"timeUnixNano":"1790665290000000000","severityText":"INFO","body":{"stringValue":"synthetic"},"attributes":[{"key":"event.name","value":{"stringValue":"codex.synthetic_probe"}},{"key":"conversation.id","value":{"stringValue":"synthetic-codex-0.157.1-session"}}]}]}]}]}`
	rolloutAPIBody     = `{"timestamp":"2026-09-29T07:01:40Z","type":"session_meta","payload":{"id":"synthetic-codex-0.157.1-session","cli_version":"0.157.1"}}` + "\n" +
		`{"timestamp":"2026-09-29T07:01:41Z","type":"response_item","payload":{"type":"message","id":"api-user","role":"user","content":[{"type":"input_text","text":"append synthetic rollout evidence"}]}}` + "\n" +
		`{"timestamp":"2026-09-29T07:01:42Z","type":"response_item","payload":{"type":"message","id":"api-assistant","role":"assistant","content":[{"type":"output_text","text":"I will update the synthetic file."}]}}` + "\n"
)

func TestCodexRolloutImportMergesIdempotentlyAndProjectsConversation(t *testing.T) {
	repository, server := authenticatedRolloutTestServer(t)
	postAcceptedOTLP(t, server.URL, "/v1/logs", rolloutOTLPLog)
	postAcceptedRollout(t, server.URL, []byte(rolloutAPIBody))
	postAcceptedRollout(t, server.URL, []byte(rolloutAPIBody))
	assertRolloutMerged(t, repository)
	conversation := readRolloutConversation(t, server.URL)
	assertRolloutConversation(t, conversation)
}

func assertRolloutMerged(t *testing.T, repository *sqlite.Repository) {
	t.Helper()
	sessions, err := repository.ListSessions(context.Background(), storage.SessionFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "codex:"+rolloutTestSession {
		t.Fatalf("sessions = %#v, want one exact-ID merged session", sessions)
	}
	events, err := repository.ListEvents(context.Background(), storage.EventFilter{SessionID: sessions[0].SessionID, Limit: 100})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("event count after replay = %d, want 4 (3 rollout + 1 OTLP)", len(events))
	}
}

func readRolloutConversation(t *testing.T, baseURL string) conversationResponse {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/sessions/codex:"+rolloutTestSession+"/conversation", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+rolloutTestToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer closeBody(t, response)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("conversation status = %d, want 200", response.StatusCode)
	}
	var conversation conversationResponse
	if err := json.NewDecoder(response.Body).Decode(&conversation); err != nil {
		t.Fatalf("decode conversation: %v", err)
	}
	return conversation
}

func assertRolloutConversation(t *testing.T, conversation conversationResponse) {
	t.Helper()
	if len(conversation.Data) != 2 || conversation.Data[0].Role != "user" || conversation.Data[1].Role != "assistant" {
		t.Fatalf("conversation = %#v, want user then assistant", conversation.Data)
	}
	if conversation.Data[0].Text == nil || *conversation.Data[0].Text != "append synthetic rollout evidence" {
		t.Fatalf("user content = %#v", conversation.Data[0])
	}
	if conversation.Data[1].Text == nil || *conversation.Data[1].Text != "I will update the synthetic file." {
		t.Fatalf("assistant content = %#v", conversation.Data[1])
	}
}

func TestCodexRolloutImportRejectsInvalidRequests(t *testing.T) {
	_, server := authenticatedRolloutTestServer(t)
	for _, test := range []struct {
		name        string
		body        string
		contentType string
		token       string
		want        int
	}{
		{name: "authentication required", body: rolloutAPIBody, contentType: "application/x-ndjson", want: http.StatusUnauthorized},
		{name: "unsupported media", body: `{}`, contentType: "application/json", token: rolloutTestToken, want: http.StatusUnsupportedMediaType},
		{name: "malformed", body: `{`, contentType: "application/x-ndjson", token: rolloutTestToken, want: http.StatusBadRequest},
		{name: "empty", contentType: "application/jsonl", token: rolloutTestToken, want: http.StatusBadRequest},
		{name: "missing session metadata", body: `{"timestamp":"2026-09-29T07:01:40Z","type":"event_msg","payload":{}}`, contentType: "application/jsonl", token: rolloutTestToken, want: http.StatusUnprocessableEntity},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/codex/rollout", strings.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", test.contentType)
			if test.token != "" {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer closeBody(t, response)
			if response.StatusCode != test.want {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.want)
			}
		})
	}
}

func TestCodexRolloutImportRejectsOversizedBodyWithoutReadingIt(t *testing.T) {
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.Close() }()
	handler := NewAuthenticatedPersistentHandler(slog.Default(), repository, rolloutTestToken, DefaultInsightThresholds())
	request := httptest.NewRequest(http.MethodPost, "/v1/codex/rollout", bytes.NewReader(nil))
	request.Header.Set("Content-Type", "application/x-ndjson")
	request.Header.Set("Authorization", "Bearer "+rolloutTestToken)
	request.ContentLength = maxTranscriptPayloadBytes + 1
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", recorder.Code)
	}
}

func authenticatedRolloutTestServer(t *testing.T) (*sqlite.Repository, *httptest.Server) {
	t.Helper()
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	server := httptest.NewServer(NewAuthenticatedPersistentHandler(slog.Default(), repository, rolloutTestToken, DefaultInsightThresholds()))
	t.Cleanup(server.Close)
	return repository, server
}

func postAcceptedRollout(t *testing.T, baseURL string, body []byte) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, baseURL+"/v1/codex/rollout", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-ndjson")
	request.Header.Set("Authorization", "Bearer "+rolloutTestToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer closeBody(t, response)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("rollout status = %d, want 202", response.StatusCode)
	}
}
