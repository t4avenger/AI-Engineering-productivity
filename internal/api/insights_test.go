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
)

func TestMCPInventoryInsightAPI(t *testing.T) {
	repo := sessionTestRepository(t)
	request := sessionTestEvent(t, "mcp-request", "mcp-session", "claude-code", "active", "2026-01-04T09:00:00Z", "")
	request.Provider = "anthropic"
	request.EventType = "api_request"
	request.ProviderExtensions = map[string]any{"event": map[string]any{"input_tokens": 10, "output_tokens": 5, "cache_creation_tokens": 7}}
	connected := sessionTestEvent(t, "mcp-connected", "mcp-session", "claude-code", "active", "2026-01-04T09:00:01Z", "")
	connected.Provider = "anthropic"
	connected.EventType = "mcp_server_connection"
	connected.ProviderExtensions = map[string]any{"event": map[string]any{"server_fingerprint": "mcp:hmac:filesystem", "status": "connected", "server_scope": "user", "transport_type": "stdio"}}
	used := sessionTestEvent(t, "mcp-used", "mcp-session", "claude-code", "active", "2026-01-04T09:00:02Z", "")
	used.Provider = "anthropic"
	used.EventType = "mcp_call"
	used.ProviderExtensions = map[string]any{"mcp_call": map[string]any{"server_fingerprint": "mcp:hmac:filesystem"}}
	if err := repo.SaveEvents(context.Background(), []canonical.Event{request, connected, used}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(slog.Default(), repo))
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/api/v1/insights/mcp-inventory")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("mcp insight status = %d", response.StatusCode)
	}
	var body mcpInventoryResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Totals.ConnectedServers != 1 || body.Data.Totals.UsedServers != 1 {
		t.Fatalf("mcp insight totals = %#v", body.Data.Totals)
	}
	serialized, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(serialized), "not exact per-MCP allocation") {
		t.Fatalf("mcp insight did not label token context heuristic: %s", serialized)
	}
}

func TestInsightsPathRequiresManagementAuth(t *testing.T) {
	if !isManagementPath("/api/v1/insights/mcp-inventory") {
		t.Fatal("MCP insight endpoint must require local API authentication")
	}
}
