package insights

import (
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestSourceEventsFromSessionKeepsMCPIdentityWithTokenFields(t *testing.T) {
	now := time.Now().UTC()
	events := []canonical.Event{{
		EventID: "mcp-connection", EventType: "mcp_server_connection", SessionID: "s1",
		OccurredAt: now, ReceivedAt: now, Provider: "anthropic", Tool: "claude-code",
		ProviderExtensions: map[string]any{
			"event": map[string]any{
				"server_name":       "tiq-mcp",
				"status":            "connected",
				"input_tokens":      int64(1200),
				"output_tokens":     int64(50),
				"cache_read_tokens": int64(900),
			},
		},
	}}
	thin := SourceEventsFromSession(events)
	inventory := MCPInventoryFromEvents(thin)
	if inventory.Totals.ConnectedServers != 1 || len(inventory.Servers) != 1 {
		t.Fatalf("inventory = %#v", inventory)
	}
	if inventory.Servers[0].ServerName != "tiq-mcp" {
		t.Fatalf("server name = %q, want tiq-mcp", inventory.Servers[0].ServerName)
	}
	if inventory.Servers[0].IdentityState != "provider_reported" {
		t.Fatalf("identity state = %q, want provider_reported", inventory.Servers[0].IdentityState)
	}
}
