package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wayne/telemetryiq/internal/config"
	"github.com/wayne/telemetryiq/internal/governance"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
)

func TestAuthenticatedManagementAPIRequiresTokenAndDeletesAll(t *testing.T) {
	repository := sessionTestRepository(t)
	server := httptest.NewServer(NewAuthenticatedPersistentHandler(slog.Default(), repository, "test-token", DefaultInsightThresholds()))
	t.Cleanup(server.Close)

	unauthenticated, err := http.Get(server.URL + "/api/v1/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unauthenticated.Body.Close() }()
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthenticated.StatusCode)
	}

	request, err := http.NewRequest(http.MethodDelete, server.URL+"/api/v1/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("bulk delete status = %d", response.StatusCode)
	}
	sessions, err := repository.ListSessions(context.Background(), storage.SessionFilter{Limit: 10})
	if err != nil || len(sessions) != 0 {
		t.Fatalf("sessions after deletion = %#v, %v", sessions, err)
	}
}

func TestAuthenticatedAllowlistSavePersistsAndReloadsJSONPolicy(t *testing.T) {
	repository := sessionTestRepository(t)
	connected := sessionTestEvent(t, "mcp-save", "mcp-save-session", "claude-code", "active", "2026-01-04T09:00:00Z", "")
	connected.Provider = "anthropic"
	connected.EventType = "mcp_server_connection"
	connected.ProviderExtensions = map[string]any{"event": map[string]any{"server_name": "filesystem", "status": "connected"}}
	if err := repository.SaveEvents(context.Background(), []canonical.Event{connected}); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	manager, err := config.NewManager(configPath, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	thresholds := DefaultInsightThresholds()
	thresholds.MCPAllowlistSource = manager
	server := httptest.NewServer(NewAuthenticatedPersistentHandler(slog.Default(), repository, "test-token", thresholds, manager))
	t.Cleanup(server.Close)

	response := authenticatedManagementForm(t, server.URL+"/governance/mcp-allowlist", url.Values{"mcp_server": {"filesystem"}})
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("save status = %d", response.StatusCode)
	}
	loaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Governance.MCPAllowlist) != 1 || loaded.Governance.MCPAllowlist[0] != "filesystem" {
		t.Fatalf("persisted allowlist = %#v", loaded.Governance.MCPAllowlist)
	}

	response = authenticatedManagementGet(t, server.URL+"/api/v1/insights/unapproved-mcp")
	defer func() { _ = response.Body.Close() }()
	var body unapprovedMCPResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Outcome != governance.OutcomeNotViolation {
		t.Fatalf("reloaded outcome = %q", body.Data.Outcome)
	}
}

func TestAuthenticatedSkillsAllowlistSavePersistsAndReloadsJSONPolicy(t *testing.T) {
	repository := sessionTestRepository(t)
	if err := repository.DeleteAllSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	skill := sessionTestEvent(t, "skill-save", "skill-save-session", "codex", "active", "2026-01-04T09:00:00Z", "")
	skill.Provider = "openai"
	skill.EventType = "skill_invocation"
	skill.ProviderExtensions = map[string]any{"skill_detection": "explicit", "skill": map[string]any{"name": "deploy"}}
	if err := repository.SaveEvents(context.Background(), []canonical.Event{skill}); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	manager, err := config.NewManager(configPath, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	thresholds := DefaultInsightThresholds()
	thresholds.SkillsAllowlistSource = manager
	server := httptest.NewServer(NewAuthenticatedPersistentHandler(slog.Default(), repository, "test-token", thresholds, manager))
	t.Cleanup(server.Close)

	response := authenticatedManagementForm(t, server.URL+"/governance/skills-allowlist", url.Values{"skill": {"deploy"}})
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/governance?rules=skills&saved=1" {
		t.Fatalf("save response = %d location %q", response.StatusCode, response.Header.Get("Location"))
	}
	loaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Governance.SkillsAllowlist, []string{"deploy"}) {
		t.Fatalf("persisted skills allowlist = %#v", loaded.Governance.SkillsAllowlist)
	}

	response = authenticatedManagementGet(t, server.URL+"/api/v1/insights/unapproved-skills")
	defer func() { _ = response.Body.Close() }()
	var body unapprovedSkillsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Outcome != governance.OutcomeNotViolation {
		t.Fatalf("updated skill policy = %#v", body.Data)
	}
}

func authenticatedManagementForm(t *testing.T, target string, form url.Values) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	return authenticatedManagementRequest(t, client, request)
}

func authenticatedManagementGet(t *testing.T, target string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	return authenticatedManagementRequest(t, http.DefaultClient, request)
}

func authenticatedManagementRequest(t *testing.T, client *http.Client, request *http.Request) *http.Response {
	t.Helper()
	request.Header.Set("Authorization", "Bearer test-token")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
