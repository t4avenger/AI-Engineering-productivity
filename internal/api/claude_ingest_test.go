package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
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
     {"key":"output_tokens","value":{"intValue":"340"}}]},
   {"attributes":[
     {"key":"event.name","value":{"stringValue":"tool_decision"}},
     {"key":"event.timestamp","value":{"stringValue":"2026-09-06T08:40:02.000Z"}},
     {"key":"event.sequence","value":{"intValue":"11"}},
     {"key":"session.id","value":{"stringValue":"tiq-canary-session"}},
     {"key":"decision","value":{"stringValue":"reject"}},
     {"key":"source","value":{"stringValue":"hook"}},
     {"key":"tool_name","value":{"stringValue":"Bash"}},
     {"key":"tool_source","value":{"stringValue":"builtin"}},
     {"key":"tool_use_id","value":{"stringValue":"toolu_canary_decision"}},
     {"key":"tool_parameters","value":{"stringValue":"tiq-canary-gated-params"}}]}]}]}]}`

// TestClaudeLogsIngestEndToEnd is the end-to-end gate the reorientation roadmap
// never had: it POSTs a raw Claude Code OTLP log payload to the live /v1/logs
// receiver, then reads it back through the real HTTP read API. It proves the
// full path — ingest, sanitise, normalise, persist, serve — actually surfaces
// real Claude behaviour data, not that a fixture round-trips in isolation.
func TestClaudeLogsIngestEndToEnd(t *testing.T) {
	server, repository := newPersistentTestServer(t)
	ingestClaudeOTLP(t, server)

	sessions := requireClaudeSession(t, repository)
	inventory := fetchMCPInventory(t, server.URL)
	if inventory.Data.Totals.ConnectedServers != 1 {
		t.Fatalf("connected_servers = %d, want 1: %#v", inventory.Data.Totals.ConnectedServers, inventory.Data.Totals)
	}
	if len(inventory.Data.Servers) != 1 || inventory.Data.Servers[0].ServerName != "tiq-canary-server" {
		t.Fatalf("server name missing from MCP inventory: %#v", inventory.Data.Servers)
	}

	// The tool_decision approval signal survives ingest→persist→serve on the
	// read API, with the wire reject normalised to denied and builtin/mcp origin
	// preserved via tool_source.
	decision := requireToolDecisionEvent(t, server.URL, "claude-code:tiq-canary-session")
	if got := stringValue(decision.ApprovalDecision); got != "denied" {
		t.Fatalf("approval_decision = %q, want denied", got)
	}
	if got := stringValue(decision.ApprovalReasonClass); got != "hook" {
		t.Fatalf("approval_reason_class = %q, want hook", got)
	}
	if got := stringValue(decision.ToolName); got != "Bash" {
		t.Fatalf("tool_name = %q, want Bash", got)
	}
	if got := stringValue(decision.ToolSource); got != "builtin" {
		t.Fatalf("tool_source = %q, want builtin", got)
	}

	// Sensitive identifiers — including the gated tool_decision tool_parameters
	// (full command / MCP names on the wire) — do not survive the round trip.
	assertNoRawIdentifiers(t,
		[]string{"tiq-canary@example.test", "tiq-canary-api-key", "tiq-canary-gated-params"},
		marshalJSON(t, sessions), marshalJSON(t, inventory), marshalJSON(t, decision))
}

// newPersistentTestServer starts a live persistent daemon backed by an in-memory
// sqlite repository, returning both for ingest→read gates.
// rawClaudeUserPromptLogs is a raw Claude Code OTLP/HTTP log payload carrying a
// single content-present user_prompt event (content logging enabled) plus a
// prompt.id drop canary. The prompt is synthetic.
const rawClaudeUserPromptLogs = `{"resourceLogs":[{"resource":{"attributes":[
  {"key":"service.name","value":{"stringValue":"claude-code"}},
  {"key":"service.version","value":{"stringValue":"2.1.270"}}]},
 "scopeLogs":[{"logRecords":[
   {"attributes":[
     {"key":"event.name","value":{"stringValue":"user_prompt"}},
     {"key":"event.timestamp","value":{"stringValue":"2026-09-13T18:53:02.100Z"}},
     {"key":"event.sequence","value":{"intValue":"2"}},
     {"key":"session.id","value":{"stringValue":"tiq-content-session"}},
     {"key":"prompt.id","value":{"stringValue":"tiq-content-prompt-id"}},
     {"key":"prompt_length","value":{"intValue":"45"}},
     {"key":"command_name","value":{"stringValue":"tiq-probe"}},
     {"key":"command_source","value":{"stringValue":"custom"}},
     {"key":"prompt","value":{"stringValue":"synthetic probe prompt for E7 content capture"}}]}]}]}]}`

// TestClaudeUserPromptContentPersistsRawEndToEnd proves the E7 content path holds
// through the live daemon: a user_prompt event POSTed to /v1/logs persists as a
// canonical.Event whose prompt text survives raw in provider_extensions.event
// (epic #87 — capture raw, no ingest-time re-redaction), the bare prompt.id
// correlation id is dropped, and the timeline read API reports the event with
// prompt_content marked available.
func TestClaudeUserPromptContentPersistsRawEndToEnd(t *testing.T) {
	server, repository := newPersistentTestServer(t)
	response := postOTLPToPath(t, server.URL, "/v1/logs", []byte(rawClaudeUserPromptLogs), "application/json")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("ingest status = %d", response.StatusCode)
	}
	closeBody(t, response)

	events, err := repository.ListEvents(context.Background(), storage.EventFilter{SessionID: "claude-code:tiq-content-session", Limit: 10})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 || events[0].EventType != "user_prompt" {
		t.Fatalf("expected 1 persisted user_prompt event, got %#v", events)
	}
	echo, ok := events[0].ProviderExtensions["event"].(map[string]any)
	if !ok {
		t.Fatalf("provider_extensions.event missing: %#v", events[0].ProviderExtensions)
	}
	if echo["prompt"] != "synthetic probe prompt for E7 content capture" {
		t.Fatalf("prompt content not persisted raw: %#v", echo)
	}

	// The bare prompt.id correlation id never reaches storage (owned by #106).
	if strings.Contains(string(marshalJSON(t, events)), "tiq-content-prompt-id") {
		t.Fatal("prompt.id correlation id leaked into persisted event")
	}

	// The timeline read API reports the event with prompt_content available.
	timeline := getInsightJSON[eventListResponse](t, server.URL+"/api/v1/sessions/"+url.PathEscape("claude-code:tiq-content-session")+"/events")
	if len(timeline.Data) != 1 || timeline.Data[0].EventType != "user_prompt" {
		t.Fatalf("timeline missing user_prompt event: %#v", timeline.Data)
	}
	for _, field := range timeline.Data[0].UnavailableFields {
		if field == "prompt_content" {
			t.Fatalf("prompt_content must be available on user_prompt: %v", timeline.Data[0].UnavailableFields)
		}
	}
	conversation := getInsightJSON[conversationResponse](t, server.URL+"/api/v1/sessions/"+url.PathEscape("claude-code:tiq-content-session")+"/conversation")
	assertRetainedConversationProjection(t, conversation)
}

func assertRetainedConversationProjection(t *testing.T, conversation conversationResponse) {
	t.Helper()
	if len(conversation.Data) != 1 || conversation.Data[0].Role != "user" || conversation.Data[0].Text == nil || *conversation.Data[0].Text != "synthetic probe prompt for E7 content capture" || conversation.Data[0].ContentAvailability != "available" {
		t.Fatalf("retained conversation projection = %#v", conversation)
	}
}

func newPersistentTestServer(t *testing.T) (*httptest.Server, storage.Repository) {
	t.Helper()
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	server := httptest.NewServer(NewPersistentHandler(slog.Default(), repository))
	t.Cleanup(server.Close)
	return server, repository
}

// ingestClaudeOTLP POSTs the raw Claude OTLP log payload to the live /v1/logs
// receiver and asserts it was accepted.
func ingestClaudeOTLP(t *testing.T, server *httptest.Server) {
	t.Helper()
	response := postOTLPToPath(t, server.URL, "/v1/logs", []byte(rawClaudeOTLPLogs), "application/json")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("ingest status = %d", response.StatusCode)
	}
	closeBody(t, response)
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
	if sessions[0].SessionID != "claude-code:tiq-canary-session" {
		t.Fatalf("session id = %q, want native provider ID", sessions[0].SessionID)
	}
	return sessions
}

// requireToolDecisionEvent reads a session's event timeline through the live
// HTTP read API and returns its single tool_decision event.
func requireToolDecisionEvent(t *testing.T, baseURL, sessionID string) timelineEvent {
	t.Helper()
	response := getInsightJSON[eventListResponse](t, baseURL+"/api/v1/sessions/"+url.PathEscape(sessionID)+"/events")
	for _, event := range response.Data {
		if event.EventType == "tool_decision" {
			return event
		}
	}
	t.Fatalf("no tool_decision event in timeline: %#v", response.Data)
	return timelineEvent{}
}

// stringValue dereferences an optional read-API string field for comparison.
func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// fetchMCPInventory reads the MCP inventory through the live HTTP read API.
func fetchMCPInventory(t *testing.T, baseURL string) mcpInventoryResponse {
	t.Helper()
	return getInsightJSON[mcpInventoryResponse](t, baseURL+"/api/v1/insights/mcp-inventory")
}

// getInsightJSON reads an insight endpoint through the live HTTP read API and
// decodes its JSON response into T.
func getInsightJSON[T any](t *testing.T, url string) T {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("insight status = %d for %s", response.StatusCode, url)
	}
	var body T
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

// otlpStringAttr builds a single string-valued OTLP attribute entry.
func otlpStringAttr(key, value string) map[string]any {
	return map[string]any{"key": key, "value": map[string]any{"stringValue": value}}
}

// claudeOTLPLogPayload wraps one log record's attributes in the Claude Code
// OTLP/HTTP envelope (service.name claude-code) used by the live ingest tests.
func claudeOTLPLogPayload(t *testing.T, attributes []any) string {
	t.Helper()
	return string(marshalJSON(t, map[string]any{
		"resourceLogs": []any{map[string]any{
			"resource": map[string]any{"attributes": []any{
				otlpStringAttr("service.name", "claude-code"),
				otlpStringAttr("service.version", "2.1.263"),
			}},
			"scopeLogs": []any{map[string]any{"logRecords": []any{
				map[string]any{"attributes": attributes},
			}}},
		}},
	}))
}
