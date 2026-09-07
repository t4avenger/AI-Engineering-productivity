package claude

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// rawClaudeLogs mirrors the observed Claude Code OTLP/HTTP log wire shape
// (service.version 2.1.263): dotted attribute keys, intValue as a string,
// boolValue for is_plugin, and operator/machine identifiers that must not
// reach canonical output. Values are synthetic.
const rawClaudeLogs = `{"resourceLogs":[
  {"resource":{"attributes":[
    {"key":"service.name","value":{"stringValue":"claude-code"}},
    {"key":"service.version","value":{"stringValue":"2.1.263"}}]},
   "scopeLogs":[{"logRecords":[
     {"attributes":[
       {"key":"event.name","value":{"stringValue":"mcp_server_connection"}},
       {"key":"event.timestamp","value":{"stringValue":"2026-09-06T08:39:58.840Z"}},
       {"key":"event.sequence","value":{"intValue":"7"}},
       {"key":"session.id","value":{"stringValue":"synthetic-session-uuid"}},
       {"key":"status","value":{"stringValue":"connected"}},
       {"key":"transport_type","value":{"stringValue":"stdio"}},
       {"key":"server_scope","value":{"stringValue":"user"}},
       {"key":"is_plugin","value":{"boolValue":false}},
       {"key":"server_name","value":{"stringValue":"synthetic-filesystem-server"}},
       {"key":"user.id","value":{"stringValue":"synthetic-user-hash"}},
       {"key":"organization.id","value":{"stringValue":"synthetic-org"}},
       {"key":"prompt.id","value":{"stringValue":"synthetic-prompt"}}]},
     {"attributes":[
       {"key":"event.name","value":{"stringValue":"api_request"}},
       {"key":"event.timestamp","value":{"stringValue":"2026-09-06T08:40:00.000Z"}},
       {"key":"event.sequence","value":{"intValue":"9"}},
       {"key":"session.id","value":{"stringValue":"synthetic-session-uuid"}},
       {"key":"request_id","value":{"stringValue":"synthetic-request"}},
       {"key":"model","value":{"stringValue":"claude-opus-4-8"}},
       {"key":"input_tokens","value":{"intValue":"1200"}},
       {"key":"output_tokens","value":{"intValue":"340"}},
       {"key":"cache_read_tokens","value":{"intValue":"500"}},
       {"key":"duration_ms","value":{"intValue":"1589"}}]}]}]},
  {"resource":{"attributes":[
    {"key":"service.name","value":{"stringValue":"codex_cli_rs"}}]},
   "scopeLogs":[{"logRecords":[
     {"attributes":[{"key":"event.name","value":{"stringValue":"codex.sse_event"}}]}]}]}]}`

func TestNormalizeLogsMapsClaudeWireEventsAndSkipsOtherServices(t *testing.T) {
	events, err := NormalizeLogs([]byte(rawClaudeLogs), time.Unix(0, 0).UTC(), stubFingerprint)
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events (codex_cli_rs resource skipped), got %d", len(events))
	}

	byType := map[string]int{}
	for _, event := range events {
		byType[event.EventType]++
		if event.Provider != "anthropic" || event.Tool != "claude-code" {
			t.Fatalf("event provider/tool = %q/%q", event.Provider, event.Tool)
		}
		if event.SourceVersion != "2.1.263" {
			t.Fatalf("source version = %q, want 2.1.263", event.SourceVersion)
		}
	}
	if byType["mcp_server_connection"] != 1 || byType["api_request"] != 1 {
		t.Fatalf("event types = %#v", byType)
	}
}

func TestNormalizeLogsKeepsNativeSessionAndDropsOperatorFields(t *testing.T) {
	events, err := NormalizeLogs([]byte(rawClaudeLogs), time.Unix(0, 0).UTC(), stubFingerprint)
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	connection := requireConnectionExtensions(t, events)
	// Behaviour fields the MCP inventory reads survive.
	for key, want := range map[string]any{"status": "connected", "transport_type": "stdio", "server_scope": "user"} {
		if connection[key] != want {
			t.Fatalf("connection[%q] = %#v, want %#v", key, connection[key], want)
		}
	}
	if plugin, ok := connection["is_plugin"].(bool); !ok || plugin {
		t.Fatalf("is_plugin = %#v, want false bool", connection["is_plugin"])
	}
	// Provider-reported MCP server name is retained for inventory display, and a fingerprint remains available for correlation.
	if connection["server_name"] != "synthetic-filesystem-server" {
		t.Fatalf("server_name = %#v", connection["server_name"])
	}
	if fp, ok := connection["server_fingerprint"].(string); !ok || !strings.HasPrefix(fp, "claude-code:") {
		t.Fatalf("server_fingerprint = %#v, want claude-code:* fingerprint", connection["server_fingerprint"])
	}

	// No operator, machine, or prompt identifier reaches canonical output; local provider session IDs are retained by policy.
	serialized, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	for _, prohibited := range []string{"synthetic-user-hash", "synthetic-org", "synthetic-prompt"} {
		if strings.Contains(string(serialized), prohibited) {
			t.Fatalf("identity leaked into canonical events: %q", prohibited)
		}
	}
}

// requireConnectionExtensions returns the provider_extensions.event map of the
// single mcp_server_connection event, failing the test if it is absent.
func requireConnectionExtensions(t *testing.T, events []canonical.Event) map[string]any {
	t.Helper()
	for _, event := range events {
		if event.EventType != "mcp_server_connection" {
			continue
		}
		connection, _ := event.ProviderExtensions["event"].(map[string]any)
		if connection == nil {
			t.Fatal("mcp_server_connection event extensions missing")
		}
		return connection
	}
	t.Fatal("no mcp_server_connection event")
	return nil
}

func TestNormalizeLogsRejectsPayloadWithoutClaudeResources(t *testing.T) {
	other := `{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_cli_rs"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.sse_event"}}]}]}]}]}`
	if _, err := NormalizeLogs([]byte(other), time.Unix(0, 0).UTC(), stubFingerprint); err != ErrUnsupportedLogs {
		t.Fatalf("expected ErrUnsupportedLogs, got %v", err)
	}
}
