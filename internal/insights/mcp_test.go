package insights

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestMCPInventoryFromEventsReportsUsageAndHeuristicTokenContext(t *testing.T) {
	events := []canonical.Event{
		testMCPEvent("request", "api_request", map[string]any{}, map[string]any{"event": map[string]any{"input_tokens": 10, "output_tokens": 5, "cache_read_tokens": 2, "cache_creation_tokens": 7}}),
		testMCPEvent("connected", "mcp_server_connection", map[string]any{}, map[string]any{"event": map[string]any{"server_fingerprint": "mcp:hmac:filesystem", "server_name": "filesystem", "status": "connected", "server_scope": "user", "transport_type": "stdio", "is_plugin": false}}),
		testMCPEvent("used", "mcp_call", map[string]any{}, map[string]any{"mcp_call": map[string]any{"server_fingerprint": "mcp:hmac:filesystem"}}),
		testMCPEvent("unused", "mcp_server_connection", map[string]any{}, map[string]any{"event": map[string]any{"server_fingerprint": "mcp:hmac:git", "status": "connected", "server_scope": "project", "transport_type": "stdio", "is_plugin": true}}),
	}

	inventory := MCPInventoryFromEvents(events)

	if inventory.Totals.ConnectedServers != 2 || inventory.Totals.UsedServers != 1 || inventory.Totals.UnusedServers != 1 {
		t.Fatalf("totals = %#v", inventory.Totals)
	}
	byFingerprint := map[string]MCPServer{}
	for _, server := range inventory.Servers {
		byFingerprint[server.ServerFingerprint] = server
		if !strings.Contains(server.TokenContextLabel, "not exact per-MCP allocation") {
			t.Fatalf("token context label = %q", server.TokenContextLabel)
		}
	}
	if byFingerprint["mcp:hmac:filesystem"].ServerName != "filesystem" || !byFingerprint["mcp:hmac:filesystem"].Used || byFingerprint["mcp:hmac:filesystem"].ContextWasteState != "used" {
		t.Fatalf("used server = %#v", byFingerprint["mcp:hmac:filesystem"])
	}
	if byFingerprint["mcp:hmac:git"].Used || byFingerprint["mcp:hmac:git"].ContextWasteState != "connected_but_unused" {
		t.Fatalf("unused server = %#v", byFingerprint["mcp:hmac:git"])
	}
	if got := valueOf(inventory.Totals.RequestInputTokens); got != 10 {
		t.Fatalf("request input tokens = %d", got)
	}
	if !strings.Contains(inventory.Totals.TokenContextLabel, "not exact per-MCP allocation") {
		t.Fatalf("totals label = %q", inventory.Totals.TokenContextLabel)
	}
}

func TestMCPInventoryReportsInvocationOnlyMCPUse(t *testing.T) {
	inventory := MCPInventoryFromEvents([]canonical.Event{
		testMCPEvent("used", "codex.tool_result", map[string]any{"category": string(canonical.OperationCategoryMCPCall)}, map[string]any{"mcp_call": map[string]any{"server_fingerprint": "codex:hmac:filesystem", "server_name": "filesystem", "tool_name": "read_file"}}),
	})

	if inventory.Totals.ConnectedServers != 1 || inventory.Totals.UsedServers != 1 || inventory.Totals.UnusedServers != 0 {
		t.Fatalf("totals = %#v", inventory.Totals)
	}
	server := inventory.Servers[0]
	if !server.Used || server.UsageState != "observed" || server.ContextWasteState != "used" || server.InvocationCount != 1 {
		t.Fatalf("server = %#v", server)
	}
	if server.ServerName != "filesystem" {
		t.Fatalf("server name = %q", server.ServerName)
	}
	if len(server.ToolNames) != 1 || server.ToolNames[0] != "read_file" {
		t.Fatalf("tool names = %#v", server.ToolNames)
	}
}

func TestMCPInventoryKeepsUsageUnavailableWhenIdentityIsUnavailable(t *testing.T) {
	inventory := MCPInventoryFromEvents([]canonical.Event{
		testMCPEvent("connected", "mcp_server_connection", map[string]any{}, map[string]any{"event": map[string]any{"status": "connected", "transport_type": "stdio"}}),
	})

	if inventory.Totals.ConnectedServers != 1 || inventory.Totals.UsageUnavailableServers != 1 {
		t.Fatalf("totals = %#v", inventory.Totals)
	}
	server := inventory.Servers[0]
	if server.IdentityState != "unavailable" || server.UsageState != "unavailable" || server.ContextWasteState != "usage_unavailable" {
		t.Fatalf("server = %#v", server)
	}
}

func TestMCPInventoryIncludesProviderReportedServerName(t *testing.T) {
	inventory := MCPInventoryFromEvents([]canonical.Event{
		testMCPEvent("connected", "mcp_server_connection", map[string]any{}, map[string]any{"event": map[string]any{"server_name": "filesystem", "status": "connected"}}),
	})
	payload, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), "filesystem") {
		t.Fatalf("MCP server name missing: %s", payload)
	}
}

func TestMCPInventoryEmptyServersEncodeAsArray(t *testing.T) {
	payload, err := json.Marshal(MCPInventoryFromEvents(nil))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), `"servers":null`) {
		t.Fatalf("empty servers must encode as an array: %s", payload)
	}
	if !strings.Contains(string(payload), `"servers":[]`) {
		t.Fatalf("empty servers array missing: %s", payload)
	}
}

func testMCPEvent(id, eventType string, attributes, extensions map[string]any) canonical.Event {
	at := time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)
	return canonical.Event{
		SchemaVersion:      "0.1.0",
		EventID:            id,
		EventType:          eventType,
		OccurredAt:         at,
		ReceivedAt:         at,
		Provider:           "anthropic",
		Tool:               "claude-code",
		SourceSchema:       "otel",
		SourceVersion:      "test",
		ActorID:            "unavailable",
		DeviceID:           "unavailable",
		SessionID:          "session",
		PrivacyLevel:       "operational",
		Attributes:         attributes,
		ProviderExtensions: extensions,
	}
}

func valueOf(value *int64) int64 {
	if value == nil {
		return -1
	}
	return *value
}
