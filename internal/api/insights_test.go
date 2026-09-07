package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wayne/telemetryiq/internal/governance"
	"github.com/wayne/telemetryiq/internal/insights"
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
	// Codex stamps no skill_detection metadata today, so its honest surface state
	// is unknown (missing metadata), never unavailable (a stamped no-skill signal).
	unknown := sessionTestEvent(t, "skill-unknown", "skill-session-2", "codex", "active", "2026-01-04T09:00:01Z", "")
	unknown.Provider = "openai"
	unknown.EventType = "api_request"
	unknown.ProviderExtensions = map[string]any{}
	if err := repo.SaveEvents(context.Background(), []canonical.Event{explicit, unknown}); err != nil {
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
	if body.Data.Totals.ExplicitDetection != 1 || body.Data.Totals.UnavailableDetection != 0 {
		t.Fatalf("skill detection coverage = %#v", body.Data.Totals)
	}
	// The codex surface stamps no skill_detection, so it must be reported as
	// unknown (missing metadata), never fabricated as unavailable.
	if state := coverageStateFor(body.Data.Coverage, "openai", "codex"); state != "unknown" {
		t.Fatalf("codex skill detection = %q, want unknown: %#v", state, body.Data.Coverage)
	}
	if len(body.Data.Skills) != 1 || body.Data.Skills[0].SkillName != "pdf" {
		t.Fatalf("skill records = %#v", body.Data.Skills)
	}
	serialized, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(serialized), `"detection_state":"unknown"`) {
		t.Fatalf("skill insight did not surface unknown coverage honestly: %s", serialized)
	}
}

// coverageStateFor returns the detection state reported for a (provider, tool)
// surface, or empty string when that surface is absent from coverage.
func coverageStateFor(coverage []insights.SkillCoverage, provider, tool string) string {
	for _, row := range coverage {
		if row.Provider == provider && row.Tool == tool {
			return row.DetectionState
		}
	}
	return ""
}

// fetchUnapprovedMCP persists one MCP connection for serverName, serves it
// through a live handler carrying thresholds, and returns the decoded decision.
func fetchUnapprovedMCP(t *testing.T, thresholds InsightThresholds, serverName string) unapprovedMCPResponse {
	t.Helper()
	repo := sessionTestRepository(t)
	connected := sessionTestEvent(t, "mcp-connected", "mcp-session", "claude-code", "active", "2026-01-04T09:00:00Z", "")
	connected.Provider = "anthropic"
	connected.EventType = "mcp_server_connection"
	connected.ProviderExtensions = map[string]any{"event": map[string]any{"server_fingerprint": "mcp:hmac:" + serverName, "server_name": serverName, "status": "connected"}}
	if err := repo.SaveEvents(context.Background(), []canonical.Event{connected}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newHandler(slog.Default(), nil, nil, nil, repo, thresholds))
	t.Cleanup(server.Close)
	return getInsightJSON[unapprovedMCPResponse](t, server.URL+"/api/v1/insights/unapproved-mcp")
}

func TestUnapprovedMCPInsightAPI(t *testing.T) {
	// A configured allowlist that does not include the observed server must yield
	// a violation, threaded from config through InsightThresholds.
	thresholds := DefaultInsightThresholds()
	thresholds.MCPAllowlist = []string{"filesystem"}
	body := fetchUnapprovedMCP(t, thresholds, "rogue-tool")

	if body.Data.Outcome != governance.OutcomeViolation {
		t.Fatalf("expected violation outcome, got %q", body.Data.Outcome)
	}
	if len(body.Data.Findings) != 1 || body.Data.Findings[0].ServerName != "rogue-tool" {
		t.Fatalf("expected one rogue-tool finding, got %#v", body.Data.Findings)
	}
}

func TestUnapprovedMCPInsightAPIIndeterminateWhenUnconfigured(t *testing.T) {
	// The default thresholds carry no allowlist, so the policy must report
	// indeterminate rather than fabricating a clean or violating result.
	body := fetchUnapprovedMCP(t, DefaultInsightThresholds(), "filesystem")

	if body.Data.Outcome != governance.OutcomeIndeterminate {
		t.Fatalf("expected indeterminate outcome, got %q", body.Data.Outcome)
	}
	if body.Data.Visibility != "policy_unconfigured" {
		t.Fatalf("expected policy_unconfigured visibility, got %q", body.Data.Visibility)
	}
}

func TestInsightsPathRequiresManagementAuth(t *testing.T) {
	for _, path := range []string{"/api/v1/insights/mcp-inventory", "/api/v1/insights/skill-usage", "/api/v1/insights/model-performance", "/api/v1/insights/context-waste", "/api/v1/insights/unapproved-mcp"} {
		if !isManagementPath(path) {
			t.Fatalf("insight endpoint %q must require local API authentication", path)
		}
	}
}

func TestModelPerformanceInsightAPI(t *testing.T) {
	repo := sessionTestRepository(t)
	success := sessionTestEvent(t, "mp-success", "mp-session", "claude-code", "active", "2026-01-04T09:00:00Z", "")
	success.Provider = "anthropic"
	success.EventType = "api_request"
	success.ProviderExtensions = map[string]any{"outcome_contract": map[string]any{
		"source": "provider_completion", "status": "success", "confidence": "observed",
		"model": "claude-haiku-4-5", "duration_ms": int64(100), "input_tokens": int64(4), "output_tokens": int64(2),
	}}
	failed := sessionTestEvent(t, "mp-failed", "mp-session", "claude-code", "active", "2026-01-04T09:00:01Z", "")
	failed.Provider = "anthropic"
	failed.EventType = "api_error"
	failed.ProviderExtensions = map[string]any{"outcome_contract": map[string]any{
		"source": "provider_completion", "status": "failed", "confidence": "observed",
		"model": "claude-opus-4-8", "duration_ms": int64(50), "error_code": "rate_limit",
	}}
	if err := repo.SaveEvents(context.Background(), []canonical.Event{success, failed}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(slog.Default(), repo))
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/api/v1/insights/model-performance")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("model performance status = %d", response.StatusCode)
	}
	var body modelPerformanceResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Models) != 2 {
		t.Fatalf("expected 2 models, got %#v", body.Data.Models)
	}
	if body.Data.RankingAvailable {
		t.Fatal("ranking must be suppressed below min sample size")
	}
}

func TestContextWasteInsightAPI(t *testing.T) {
	repo := sessionTestRepository(t)
	request := sessionTestEvent(t, "cw-request", "cw-session", "claude-code", "active", "2026-01-04T09:00:00Z", "")
	request.Provider = "anthropic"
	request.EventType = "api_request"
	request.ProviderExtensions = map[string]any{"event": map[string]any{"input_tokens": 100, "cache_read_tokens": 75}}
	if err := repo.SaveEvents(context.Background(), []canonical.Event{request}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(slog.Default(), repo))
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/api/v1/insights/context-waste")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("context-waste status = %d", response.StatusCode)
	}
	var body contextWasteResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Sessions) != 1 {
		t.Fatalf("expected 1 context-waste session, got %#v", body.Data.Sessions)
	}
	if !body.Data.Sessions[0].Triggered {
		t.Fatalf("expected triggered session, got %#v", body.Data.Sessions[0])
	}
}
