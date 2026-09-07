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

	"github.com/wayne/telemetryiq/internal/privacy"
	"github.com/wayne/telemetryiq/internal/storage"
	"github.com/wayne/telemetryiq/internal/storage/sqlite"
)

func TestCursorAgentIngestPersistsSanitizedCanonicalSession(t *testing.T) {
	sanitizer, err := privacy.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repository, err := sqlite.Open(":memory:", sanitizer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })

	server := httptest.NewServer(NewPersistentHandler(slog.Default(), sanitizer, repository))
	t.Cleanup(server.Close)

	payload := map[string]any{
		"provider":     "cursor",
		"tool":         "cursor-agent",
		"tool_version": "2026.09.02-c22c1a3",
		"captured_at":  "2026-09-07T18:31:51Z",
		"payload": map[string]any{
			"source_type": "local_cli_stream_json",
			"init": map[string]any{
				"type":       "system",
				"subtype":    "init",
				"model":      "GPT-5.2 Medium",
				"session_id": "tiq-canary-cursor-session",
			},
			"result": map[string]any{
				"type":            "result",
				"subtype":         "success",
				"is_error":        false,
				"duration_ms":     1000,
				"session_id":      "tiq-canary-cursor-session",
				"request_id":      "tiq-canary-cursor-request",
				"duration_api_ms": 1000,
				"usage": map[string]any{
					"inputTokens":      123,
					"outputTokens":     4,
					"cacheReadTokens":  5,
					"cacheWriteTokens": 6,
				},
			},
		},
	}
	body, _ := json.Marshal(payload)
	resp := postJSON(t, server.URL+"/v1/cursor-agent", body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	sessions, err := repository.ListSessions(context.Background(), storage.SessionFilter{Limit: 10, Tool: "cursor-agent"})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %#v, %v", sessions, err)
	}
	events, err := repository.ListEvents(context.Background(), storage.EventFilter{SessionID: sessions[0].SessionID, Limit: 10})
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %#v, %v", events, err)
	}

	serialized, _ := json.Marshal(map[string]any{"sessions": sessions, "events": events})
	for _, prohibited := range []string{
		"tiq-canary-cursor-session",
		"tiq-canary-cursor-request",
		"tiq-canary-cursor-prompt",
	} {
		if strings.Contains(string(serialized), prohibited) {
			t.Fatalf("privacy leak %q in %s", prohibited, serialized)
		}
	}
}

func TestCursorAgentIngestRejectsUnexpectedFields(t *testing.T) {
	sanitizer, err := privacy.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repository, err := sqlite.Open(":memory:", sanitizer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })

	server := httptest.NewServer(NewPersistentHandler(slog.Default(), sanitizer, repository))
	t.Cleanup(server.Close)

	// user/assistant message content is not an allowed ingest field.
	payload := []byte(`{"provider":"cursor","tool":"cursor-agent","tool_version":"2026.09.02-c22c1a3","captured_at":"2026-09-07T18:31:51Z","payload":{"source_type":"local_cli_stream_json","user":{"message":{"content":[{"type":"text","text":"tiq-canary-cursor-prompt"}]}},"result":{"type":"result","subtype":"success","is_error":false,"duration_ms":1,"duration_api_ms":1,"session_id":"s","request_id":"r","usage":{"inputTokens":1,"outputTokens":1,"cacheReadTokens":0,"cacheWriteTokens":0}}}}`)
	resp := postJSON(t, server.URL+"/v1/cursor-agent", payload)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	defer func() { _ = resp.Body.Close() }()
	var body ingestErrorResponse
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Error.Code != "invalid_payload" {
		t.Fatalf("error code = %q, want invalid_payload", body.Error.Code)
	}
}

func postJSON(t *testing.T, url string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	return resp
}
