package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/privacy"
	"github.com/wayne/telemetryiq/internal/storage"
	"github.com/wayne/telemetryiq/internal/storage/sqlite"
)

// rawClaudeOTLPLogs is a raw Claude Code OTLP/HTTP log payload in the observed
// wire shape (service.name claude-code, dotted attribute keys, intValue as a
// string, boolValue for is_plugin), carrying an MCP connection, an api_request,
// operator identifiers, and secret canaries. Values are synthetic.
const rawClaudeOTLPLogs = `{"resourceLogs":[{"resource":{"attributes":[
  {"key":"service.name","value":{"stringValue":"claude-code"}},
  {"key":"service.version","value":{"stringValue":"2.1.263"}}]},
 "scopeLogs":[{"logRecords":[
   {"attributes":[
     {"key":"event.name","value":{"stringValue":"mcp_server_connection"}},
     {"key":"event.timestamp","value":{"stringValue":"2026-09-06T08:39:58.840Z"}},
     {"key":"event.sequence","value":{"intValue":"7"}},
     {"key":"session.id","value":{"stringValue":"tiq-canary-session"}},
     {"key":"status","value":{"stringValue":"connected"}},
     {"key":"transport_type","value":{"stringValue":"stdio"}},
     {"key":"server_scope","value":{"stringValue":"user"}},
     {"key":"is_plugin","value":{"boolValue":false}},
     {"key":"server_name","value":{"stringValue":"tiq-canary-server"}},
     {"key":"user.email","value":{"stringValue":"tiq-canary@example.test"}},
     {"key":"api_key","value":{"stringValue":"tiq-canary-api-key"}}]},
   {"attributes":[
     {"key":"event.name","value":{"stringValue":"api_request"}},
     {"key":"event.timestamp","value":{"stringValue":"2026-09-06T08:40:00.000Z"}},
     {"key":"event.sequence","value":{"intValue":"9"}},
     {"key":"session.id","value":{"stringValue":"tiq-canary-session"}},
     {"key":"request_id","value":{"stringValue":"synthetic-request"}},
     {"key":"model","value":{"stringValue":"claude-opus-4-8"}},
     {"key":"input_tokens","value":{"intValue":"1200"}},
     {"key":"output_tokens","value":{"intValue":"340"}}]}]}]}]}`

// TestClaudeLogsIngestEndToEnd is the end-to-end gate the reorientation roadmap
// never had: it POSTs a raw Claude Code OTLP log payload to the live /v1/logs
// receiver, then reads it back through the real HTTP read API. It proves the
// full path — ingest, sanitise, normalise, persist, serve — actually surfaces
// real Claude behaviour data, not that a fixture round-trips in isolation.
func TestClaudeLogsIngestEndToEnd(t *testing.T) {
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

	response := postOTLPToPath(t, server.URL, "/v1/logs", []byte(rawClaudeOTLPLogs), "application/json")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("ingest status = %d", response.StatusCode)
	}
	closeBody(t, response)

	sessions := requireClaudeSession(t, repository)
	inventory := fetchMCPInventory(t, server.URL)
	if inventory.Data.Totals.ConnectedServers != 1 {
		t.Fatalf("connected_servers = %d, want 1: %#v", inventory.Data.Totals.ConnectedServers, inventory.Data.Totals)
	}

	// Nothing sensitive survived the round trip through the read API.
	assertNoRawIdentifiers(t,
		[]string{"tiq-canary-session", "tiq-canary-server", "tiq-canary@example.test", "tiq-canary-api-key"},
		marshalJSON(t, sessions), marshalJSON(t, inventory))
}

// requireClaudeSession asserts exactly one persisted anthropic/claude-code
// session and returns it for further inspection.
func requireClaudeSession(t *testing.T, repository storage.Repository) []canonical.Session {
	t.Helper()
	sessions, err := repository.ListSessions(context.Background(), storage.SessionFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 Claude session, got %d: %#v", len(sessions), sessions)
	}
	if sessions[0].Provider != "anthropic" || sessions[0].Tool != "claude-code" {
		t.Fatalf("session provider/tool = %q/%q", sessions[0].Provider, sessions[0].Tool)
	}
	return sessions
}

// fetchMCPInventory reads the MCP inventory through the live HTTP read API.
func fetchMCPInventory(t *testing.T, baseURL string) mcpInventoryResponse {
	t.Helper()
	response, err := http.Get(baseURL + "/api/v1/insights/mcp-inventory")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("mcp inventory status = %d", response.StatusCode)
	}
	var body mcpInventoryResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

// assertNoRawIdentifiers fails if any prohibited raw identifier appears in the
// serialised read-API output.
func assertNoRawIdentifiers(t *testing.T, prohibited []string, documents ...[]byte) {
	t.Helper()
	for _, document := range documents {
		text := string(document)
		for _, secret := range prohibited {
			if strings.Contains(text, secret) {
				t.Fatalf("privacy leak %q in end-to-end read output", secret)
			}
		}
	}
}

func marshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
