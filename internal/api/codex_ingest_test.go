package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wayne/telemetryiq/internal/storage/sqlite"
)

// rawCodexOTLPLogs is a raw Codex OTLP/HTTP log payload in the observed wire
// shape (service.name codex_cli_rs, model + token attributes, operator
// identifiers, and secret canaries). Values are synthetic.
const rawCodexOTLPLogs = `{"resourceLogs":[{"resource":{"attributes":[
  {"key":"service.name","value":{"stringValue":"codex_cli_rs"}},
  {"key":"service.version","value":{"stringValue":"0.145.0"}}]},
 "scopeLogs":[{"logRecords":[
   {"attributes":[
     {"key":"event.name","value":{"stringValue":"codex.sse_event"}},
     {"key":"model","value":{"stringValue":"tiq-live-codex-model"}},
     {"key":"input_token_count","value":{"stringValue":"42"}},
     {"key":"output_token_count","value":{"stringValue":"7"}},
     {"key":"arguments","value":{"stringValue":"--token=tiq-canary-argument-token"}},
     {"key":"output","value":{"stringValue":"tiq-canary-output"}},
     {"key":"custom_metadata","value":{"stringValue":"token=tiq-canary-provider-extension"}},
     {"key":"api_key","value":{"stringValue":"tiq-canary-api-key"}},
     {"key":"user.email","value":{"stringValue":"synthetic@example.test"}},
     {"key":"conversation.id","value":{"stringValue":"synthetic-conversation"}}],
    "body":{"stringValue":"synthetic body"}}]}]}]}`

// TestCodexLogsIngestEndToEnd is the Codex counterpart of the Claude live gate:
// POST a raw OTLP log payload to /v1/logs, then prove the HTTP read API serves
// the resulting session (tool, model) without leaking identity or secrets.
func TestCodexLogsIngestEndToEnd(t *testing.T) {
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	server := httptest.NewServer(NewPersistentHandler(slog.Default(), repository))
	t.Cleanup(server.Close)

	response := postOTLPToPath(t, server.URL, "/v1/logs", []byte(rawCodexOTLPLogs), "application/json")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("ingest status = %d", response.StatusCode)
	}
	closeBody(t, response)

	sessions := fetchSessionList(t, server.URL+"/api/v1/sessions?limit=10")
	if len(sessions.Data) != 1 {
		t.Fatalf("expected 1 Codex session from read API, got %d: %#v", len(sessions.Data), sessions.Data)
	}
	session := sessions.Data[0]
	if session.Provider != "openai" || session.Tool != "codex" {
		t.Fatalf("session provider/tool = %q/%q", session.Provider, session.Tool)
	}
	if session.SessionID != "codex:synthetic-conversation" {
		t.Fatalf("session id = %q, want native provider conversation ID", session.SessionID)
	}
	model, _ := session.Attributes["model"].(string)
	if model != "tiq-live-codex-model" {
		t.Fatalf("session model = %#v, want tiq-live-codex-model", session.Attributes["model"])
	}
	if session.Availability["model"] != "observed" {
		t.Fatalf("model availability = %#v", session.Availability)
	}

	assertNoRawIdentifiers(t,
		[]string{
			"tiq-canary-argument-token",
			"tiq-canary-output",
			"tiq-canary-provider-extension",
			"tiq-canary-api-key",
			"synthetic@example.test",
			"synthetic body",
		},
		marshalJSON(t, sessions))
}

func fetchSessionList(t *testing.T, url string) sessionListResponse {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("sessions status = %d", response.StatusCode)
	}
	var body sessionListResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}
