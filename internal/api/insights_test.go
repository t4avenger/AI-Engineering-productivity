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

func TestSkillUsageInsightAPI(t *testing.T) {
	repo := sessionTestRepository(t)
	explicit := sessionTestEvent(t, "skill-explicit", "skill-session", "claude-code", "active", "2026-01-04T09:00:00Z", "")
	explicit.Provider = "anthropic"
	explicit.EventType = "skill_invocation"
	explicit.ProviderExtensions = map[string]any{"skill_detection": "explicit", "skill": map[string]any{"name": "pdf", "outcome": "success"}}
	unavailable := sessionTestEvent(t, "skill-unavailable", "skill-session-2", "codex-cli", "active", "2026-01-04T09:00:01Z", "")
	unavailable.Provider = "openai"
	unavailable.EventType = "api_request"
	unavailable.ProviderExtensions = map[string]any{"skill_detection": "unavailable"}
	if err := repo.SaveEvents(context.Background(), []canonical.Event{explicit, unavailable}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(slog.Default(), repo))
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/api/v1/insights/skill-usage")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("skill insight status = %d", response.StatusCode)
	}
	var body skillUsageResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Totals.ObservedSkills != 1 || body.Data.Totals.Invocations != 1 {
		t.Fatalf("skill insight totals = %#v", body.Data.Totals)
	}
	if body.Data.Totals.ExplicitDetection != 1 || body.Data.Totals.UnavailableDetection != 1 {
		t.Fatalf("skill detection coverage = %#v", body.Data.Totals)
	}
	if len(body.Data.Skills) != 1 || body.Data.Skills[0].SkillName != "pdf" {
		t.Fatalf("skill records = %#v", body.Data.Skills)
	}
	serialized, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(serialized), `"detection_state":"unavailable"`) {
		t.Fatalf("skill insight did not surface unavailable coverage honestly: %s", serialized)
	}
}

func TestInsightsPathRequiresManagementAuth(t *testing.T) {
	for _, path := range []string{"/api/v1/insights/mcp-inventory", "/api/v1/insights/skill-usage"} {
		if !isManagementPath(path) {
			t.Fatalf("insight endpoint %q must require local API authentication", path)
		}
	}
}
