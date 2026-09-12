package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/wayne/telemetryiq/internal/storage"
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

	canaries := []string{
		"tiq-canary-argument-token",
		"tiq-canary-output",
		"tiq-canary-provider-extension",
		"tiq-canary-api-key",
		"synthetic@example.test",
		"synthetic body",
	}
	assertNoRawIdentifiers(t, canaries, marshalJSON(t, sessions))

	stored, err := repository.ListEvents(t.Context(), storage.EventFilter{SessionID: "codex:synthetic-conversation", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	assertNoRawIdentifiers(t, canaries, marshalJSON(t, stored))
}

const rawCodexToolResultOTLPLogs = `{"resourceLogs":[{"resource":{"attributes":[
  {"key":"service.name","value":{"stringValue":"codex_exec"}},
  {"key":"service.version","value":{"stringValue":"0.153.4"}}]},
 "scopeLogs":[{"logRecords":[
   {"attributes":[
     {"key":"event.name","value":{"stringValue":"codex.tool_result"}},
     {"key":"conversation.id","value":{"stringValue":"synthetic-tool-session"}},
     {"key":"tool_name","value":{"stringValue":"exec_command"}},
     {"key":"tool_namespace","value":{"stringValue":"functions"}},
     {"key":"call_id","value":{"stringValue":"synthetic-call-success"}},
     {"key":"duration_ms","value":{"stringValue":"92"}},
     {"key":"success","value":{"stringValue":"true"}},
     {"key":"output_truncated","value":{"boolValue":false}},
     {"key":"arguments","value":{"stringValue":"--token=tiq-canary-tool-argument"}},
     {"key":"output","value":{"stringValue":"tiq-canary-tool-output"}},
     {"key":"api_key","value":{"stringValue":"tiq-canary-tool-api-key"}},
     {"key":"user.email","value":{"stringValue":"tool-user@example.test"}}],
    "body":{"stringValue":"tiq-canary-tool-body"}}]}]}]}`

func TestCodexToolResultIngestExposesToolCallSignal(t *testing.T) {
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	server := httptest.NewServer(NewPersistentHandler(slog.Default(), repository))
	t.Cleanup(server.Close)

	response := postOTLPToPath(t, server.URL, "/v1/logs", []byte(rawCodexToolResultOTLPLogs), "application/json")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("ingest status = %d", response.StatusCode)
	}
	closeBody(t, response)

	events := timelinePage(t, server.URL+"/api/v1/sessions/codex:synthetic-tool-session/events?limit=10")
	if len(events.Data) != 1 {
		t.Fatalf("timeline events = %d, want 1: %#v", len(events.Data), events.Data)
	}
	event := events.Data[0]
	if event.EventType != "codex.tool_result" || event.OperationID == nil || *event.OperationID != "codex:synthetic-tool-session:tool:synthetic-call-success" {
		t.Fatalf("operation identity = %#v", event)
	}
	if event.Category == nil || *event.Category != "shell command" || event.Outcome == nil || *event.Outcome != "success" || event.DurationMs == nil || *event.DurationMs != "92" {
		t.Fatalf("operation fields = %#v", event)
	}
	if slices.Contains(event.UnavailableFields, "tool_calls") {
		t.Fatalf("tool_calls must be available for codex.tool_result: %#v", event.UnavailableFields)
	}

	canaries := []string{
		"tiq-canary-tool-argument",
		"tiq-canary-tool-output",
		"tiq-canary-tool-api-key",
		"tool-user@example.test",
		"tiq-canary-tool-body",
	}
	assertNoRawIdentifiers(t, canaries, marshalJSON(t, events))

	stored, err := repository.ListEvents(t.Context(), storage.EventFilter{SessionID: "codex:synthetic-tool-session", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	assertNoRawIdentifiers(t, canaries, marshalJSON(t, stored))
}

const rawCodexSandboxOutcomeOTLPLogs = `{"resourceLogs":[{"resource":{"attributes":[
  {"key":"service.name","value":{"stringValue":"codex_exec"}},
  {"key":"service.version","value":{"stringValue":"0.153.4"}}]},
 "scopeLogs":[{"logRecords":[
   {"attributes":[
     {"key":"event.name","value":{"stringValue":"codex.sandbox_outcome"}},
     {"key":"conversation.id","value":{"stringValue":"synthetic-sandbox-session"}},
     {"key":"call_id","value":{"stringValue":"synthetic-sandbox-call-success"}},
     {"key":"tool_name","value":{"stringValue":"exec_command"}},
     {"key":"initial_duration_ms","value":{"stringValue":"123"}},
     {"key":"outcome","value":{"stringValue":"success"}},
     {"key":"model","value":{"stringValue":"gpt-6-astra"}},
     {"key":"arguments","value":{"stringValue":"--token=tiq-canary-sandbox-argument"}},
     {"key":"output","value":{"stringValue":"tiq-canary-sandbox-output"}},
     {"key":"api_key","value":{"stringValue":"tiq-canary-sandbox-api-key"}},
     {"key":"user.email","value":{"stringValue":"sandbox-user@example.test"}}],
    "body":{"stringValue":"tiq-canary-sandbox-body"}}]}]}]}`

func TestCodexSandboxOutcomeIngestExposesCommandExecutionSignal(t *testing.T) {
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	server := httptest.NewServer(NewPersistentHandler(slog.Default(), repository))
	t.Cleanup(server.Close)

	response := postOTLPToPath(t, server.URL, "/v1/logs", []byte(rawCodexSandboxOutcomeOTLPLogs), "application/json")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("ingest status = %d", response.StatusCode)
	}
	closeBody(t, response)

	events := timelinePage(t, server.URL+"/api/v1/sessions/codex:synthetic-sandbox-session/events?limit=10")
	if len(events.Data) != 1 {
		t.Fatalf("timeline events = %d, want 1: %#v", len(events.Data), events.Data)
	}
	event := events.Data[0]
	assertCodexSandboxTimelineEvent(t, event)

	canaries := []string{
		"tiq-canary-sandbox-argument",
		"tiq-canary-sandbox-output",
		"tiq-canary-sandbox-api-key",
		"sandbox-user@example.test",
		"tiq-canary-sandbox-body",
	}
	assertNoRawIdentifiers(t, canaries, marshalJSON(t, events))

	stored, err := repository.ListEvents(t.Context(), storage.EventFilter{SessionID: "codex:synthetic-sandbox-session", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	assertNoRawIdentifiers(t, canaries, marshalJSON(t, stored))
}

func assertCodexSandboxTimelineEvent(t *testing.T, event timelineEvent) {
	t.Helper()
	if event.EventType != "codex.sandbox_outcome" || event.OperationID == nil || *event.OperationID != "codex:synthetic-sandbox-session:sandbox:synthetic-sandbox-call-success" {
		t.Fatalf("operation identity = %#v", event)
	}
	if event.Category == nil || *event.Category != "shell command" || event.Outcome == nil || *event.Outcome != "success" || event.DurationMs == nil || *event.DurationMs != "123" {
		t.Fatalf("operation fields = %#v", event)
	}
	if slices.Contains(event.UnavailableFields, "command_execution") {
		t.Fatalf("command_execution must be available for codex.sandbox_outcome: %#v", event.UnavailableFields)
	}
	if !slices.Contains(event.UnavailableFields, "file_operations") {
		t.Fatalf("file_operations must remain unavailable until evidenced: %#v", event.UnavailableFields)
	}
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
