package ui_test

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/cost"
	"github.com/wayne/telemetryiq/internal/governance"
	"github.com/wayne/telemetryiq/internal/insights"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
	"github.com/wayne/telemetryiq/internal/ui"
)

var defaultContextWasteThresholds = insights.ContextWasteThresholds{
	CachedContextRatioThreshold: 0.75,
	InputTokenGrowthThreshold:   2.0,
}

type testAllowlistController struct {
	mu      sync.RWMutex
	names   []string
	saveErr error
}

type testPolicyController struct {
	mcp     testAllowlistController
	mu      sync.RWMutex
	skills  []string
	saveErr error
}

func (c *testPolicyController) MCPAllowlist() []string {
	return c.mcp.MCPAllowlist()
}

func (c *testPolicyController) SaveMCPAllowlist(names []string) error {
	return c.mcp.SaveMCPAllowlist(names)
}

func (c *testPolicyController) SkillsAllowlist() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]string(nil), c.skills...)
}

func (c *testPolicyController) SaveSkillsAllowlist(names []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.saveErr != nil {
		return c.saveErr
	}
	c.skills = append([]string(nil), names...)
	return nil
}

func (c *testAllowlistController) MCPAllowlist() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]string(nil), c.names...)
}

func (c *testAllowlistController) SaveMCPAllowlist(names []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.saveErr != nil {
		return c.saveErr
	}
	c.names = append([]string(nil), names...)
	return nil
}

type fullStub struct {
	sessions        []canonical.Session
	events          map[string][]canonical.Event
	eventErr        error
	operations      []canonical.Operation
	costs           []cost.Record
	deleted         []string
	cleared         bool
	listEventsCalls int
	insightCalls    int
}

func (s *fullStub) Session(_ context.Context, id string) (canonical.Session, bool, error) {
	for _, session := range s.sessions {
		if session.SessionID == id {
			return session, true, nil
		}
	}
	return canonical.Session{}, false, nil
}

func (s *fullStub) ListSessions(_ context.Context, filter storage.SessionFilter) ([]canonical.Session, error) {
	result := make([]canonical.Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		scope, _ := session.Attributes["identity_scope"].(string)
		if filter.Scope == storage.SessionScopePrimary && scope == "observation" {
			continue
		}
		if filter.Scope == storage.SessionScopeObservation && scope != "observation" {
			continue
		}
		session = withStubLastEventAt(session, s.events[session.SessionID])
		result = append(result, session)
	}
	return result, nil
}

func withStubLastEventAt(session canonical.Session, events []canonical.Event) canonical.Session {
	if session.Attributes != nil {
		if raw, ok := session.Attributes["last_event_at"].(string); ok && raw != "" {
			return session
		}
	}
	var latest time.Time
	for _, event := range events {
		if latest.IsZero() || event.OccurredAt.After(latest) {
			latest = event.OccurredAt
		}
	}
	if latest.IsZero() {
		return session
	}
	attrs := map[string]any{}
	for key, value := range session.Attributes {
		attrs[key] = value
	}
	attrs["last_event_at"] = latest.UTC().Format(time.RFC3339Nano)
	session.Attributes = attrs
	return session
}

func (s *fullStub) DeleteSession(_ context.Context, id string) error {
	s.deleted = append(s.deleted, id)
	out := s.sessions[:0]
	for _, session := range s.sessions {
		if session.SessionID != id {
			out = append(out, session)
		}
	}
	s.sessions = out
	return nil
}

func (s *fullStub) DeleteAllSessions(context.Context) error {
	s.cleared = true
	s.sessions = nil
	return nil
}

func (s *fullStub) ListEvents(_ context.Context, filter storage.EventFilter) ([]canonical.Event, error) {
	s.listEventsCalls++
	if s.eventErr != nil {
		return nil, s.eventErr
	}
	events := append([]canonical.Event(nil), s.events[filter.SessionID]...)
	if len(filter.EventTypes) == 0 {
		return events, nil
	}
	allowed := make(map[string]struct{}, len(filter.EventTypes))
	for _, eventType := range filter.EventTypes {
		allowed[eventType] = struct{}{}
	}
	filtered := make([]canonical.Event, 0, len(events))
	for _, event := range events {
		if _, ok := allowed[event.EventType]; ok {
			filtered = append(filtered, event)
		}
	}
	return filtered, nil
}

func (s *fullStub) GetEvent(_ context.Context, sessionID, eventID string) (canonical.Event, bool, error) {
	if s.eventErr != nil {
		return canonical.Event{}, false, s.eventErr
	}
	for _, event := range s.events[sessionID] {
		if event.EventID == eventID {
			return event, true, nil
		}
	}
	return canonical.Event{}, false, nil
}

func (s *fullStub) ListInsightSourceEvents(context.Context) ([]canonical.Event, error) {
	s.insightCalls++
	if s.eventErr != nil {
		return nil, s.eventErr
	}
	out := []canonical.Event{}
	for _, sessionEvents := range s.events {
		out = append(out, insights.SourceEventsFromSession(sessionEvents)...)
		out = append(out, governance.ThinAccessEvents(sessionEvents)...)
	}
	return out, nil
}

func (s *fullStub) ListOperations(context.Context, storage.OperationFilter) ([]canonical.Operation, error) {
	return append([]canonical.Operation(nil), s.operations...), nil
}

func (s *fullStub) ListCostRecords(context.Context, string) ([]cost.Record, error) {
	return s.costs, nil
}

func (s *fullStub) SummarizeCosts(context.Context) (cost.Summary, error) {
	return cost.SummarizeRecords(s.costs), nil
}

func unlock(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	form := strings.NewReader("token=test-token")
	post := httptest.NewRequest(http.MethodPost, "/unlock", form)
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, post)
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected cookie")
	}
	return cookies[0]
}

func wrapUI(t *testing.T, repo storage.SessionReader) http.Handler {
	t.Helper()
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds, nil)
	if err != nil {
		t.Fatal(err)
	}
	return server.Wrap(http.NotFoundHandler())
}

func assertUnlockGate(t *testing.T, handler http.Handler, path string) *http.Cookie {
	t.Helper()
	unauth := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, unauth)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/unlock" {
		t.Fatalf("unauthenticated %s = %d %s", path, rec.Code, rec.Header().Get("Location"))
	}
	return unlock(t, handler)
}

func authedPageBody(t *testing.T, repo storage.SessionReader, path string) string {
	t.Helper()
	handler := wrapUI(t, repo)
	return getAuthed(t, handler, unlock(t, handler), path).Body.String()
}

func getAuthed(t *testing.T, handler http.Handler, cookie *http.Cookie, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func syntheticSession(id string, now time.Time) canonical.Session {
	return canonical.Session{
		SessionID: id,
		Provider:  "anthropic",
		Tool:      "claude-code",
		State:     "completed",
		StartedAt: now,
	}
}

func renderInsights(t *testing.T, repo *fullStub) string {
	t.Helper()
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)
	return getAuthed(t, handler, cookie, "/insights").Body.String()
}

func TestDashboardPagesAndMutations(t *testing.T) {
	now := time.Now().UTC()
	repo := &fullStub{
		sessions: []canonical.Session{{
			SessionID: "s1",
			Provider:  "openai",
			Tool:      "codex",
			State:     "completed",
			StartedAt: now,
			Attributes: map[string]any{
				"model":           "gpt-test",
				"entrypoint":      "codex exec",
				"service_name":    "codex_exec",
				"service_version": "0.153.4",
			},
		}},
		events: map[string][]canonical.Event{
			"s1": {{
				EventID:    "e1",
				EventType:  "model_interaction",
				OccurredAt: now,
				ReceivedAt: now,
				Provider:   "openai",
				Tool:       "codex",
				Attributes: map[string]any{
					"model":              "gpt-test",
					"input_token_count":  "2",
					"output_token_count": "3",
					"unavailable_fields": []string{"latency"},
				},
			}, {
				// Outcome-contract event with an observed latency so the model
				// performance scorecard renders a non-nil *float64 latency,
				// guarding the pointer-deref template bug (regression for the
				// %!f(*float64=0x...) render).
				EventID:    "e2",
				EventType:  "operation",
				OccurredAt: now,
				ReceivedAt: now,
				Provider:   "openai",
				Tool:       "codex",
				ProviderExtensions: map[string]any{
					"outcome_contract": map[string]any{
						"model":       "gpt-test",
						"status":      "success",
						"duration_ms": 1234.0,
						"source":      "test",
					},
				},
			}},
		},
		operations: []canonical.Operation{{
			SchemaVersion: canonical.RecordSchemaVersion,
			OperationID:   "op-1",
			SessionID:     "s1",
			Provider:      "openai",
			Tool:          "codex",
			Category:      canonical.OperationCategoryShellCommand,
			Outcome:       "success",
			Provenance:    canonical.ProvenanceObserved,
			ProviderExtensions: map[string]any{"tool_call": map[string]any{
				"duration_ms": "92",
			}},
		}},
		costs: []cost.Record{{
			Currency: "USD",
			Status:   "calculated",
			AmountMicrousd: func() *int64 {
				v := int64(1500000)
				return &v
			}(),
		}},
	}
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)

	pages := []struct {
		path string
		want string
	}{
		{"/", "Orchestration overview"},
		{"/sessions", "codex"},
		{"/sessions", "codex · started"},
		{"/sessions", "Model"},
		{"/sessions/s1", "Availability"},
		{"/sessions/s1", "Environment"},
		{"/sessions/s1", "Branch"},
		{"/sessions/s1", "PR"},
		{"/sessions/s1", "codex exec"},
		{"/sessions/s1", "0.153.4"},
		{"/sessions/s1", "Model interaction"},
		{"/sessions/s1", "Input tokens"},
		{"/sessions/s1", "2 tokens"},
		{"/insights", "MCP inventory"},
		{"/insights", "Skill usage"},
		{"/insights", "Operations"},
		{"/insights", "shell command"},
		{"/insights", "92 ms"},
		{"/insights", "Model performance"},
		{"/insights", "1234 ms"},
		{"/insights", "Open Models destination"},
		{"/insights", "Context pressure"},
		{"/models", "Models"},
		{"/models", "1234 ms"},
		{"/models", "Ranking available: no"},
		{"/models", "View detailed Insights evidence"},
		{"/governance", "Risky access"},
		{"/governance", "Unapproved MCP"},
		{"/governance", "does not enforce or publish"},
		{"/governance", "Indeterminate"},
		{"/integrations", "codex"},
		{"/integrations", "Cursor Enterprise OpenTelemetry Export"},
		{"/integrations", "Team Settings"},
		{"/privacy", "local-only"},
		{"/privacy?confirm=1", "Type DELETE ALL"},
		{"/costs", "1.500000"},
		{"/static/app.css", "app-shell"},
	}
	for _, page := range pages {
		rec := getAuthed(t, handler, cookie, page.path)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), page.want) {
			t.Fatalf("%s = %d want containing %q body=%q", page.path, rec.Code, page.want, rec.Body.String())
		}
		// The %! marker only ever comes from Go template/fmt rendering, so the
		// guard applies to templated pages, not static assets — CSS legitimately
		// contains %! (e.g. 50%!important).
		if !strings.HasPrefix(page.path, "/static/") && strings.Contains(rec.Body.String(), "%!") {
			t.Fatalf("%s rendered a fmt error marker (%%!...): body=%q", page.path, rec.Body.String())
		}
	}

	bad := strings.NewReader("token=wrong")
	post := httptest.NewRequest(http.MethodPost, "/unlock", bad)
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, post)
	if !strings.Contains(rec.Body.String(), "Token rejected") {
		t.Fatalf("bad unlock = %q", rec.Body.String())
	}

	del := httptest.NewRequest(http.MethodPost, "/sessions/s1/delete", strings.NewReader("confirm=1"))
	del.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	del.AddCookie(cookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, del)
	if rec.Code != http.StatusSeeOther || len(repo.deleted) != 1 {
		t.Fatalf("delete session = %d deleted=%v", rec.Code, repo.deleted)
	}

	repo.sessions = []canonical.Session{{SessionID: "s2", Tool: "codex", Provider: "openai", StartedAt: now}}
	bulk := httptest.NewRequest(http.MethodPost, "/privacy/delete-all", strings.NewReader("confirmation=DELETE+ALL"))
	bulk.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	bulk.AddCookie(cookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, bulk)
	if rec.Code != http.StatusSeeOther || !repo.cleared {
		t.Fatalf("bulk delete = %d cleared=%v", rec.Code, repo.cleared)
	}

	logout := httptest.NewRequest(http.MethodPost, "/logout", nil)
	logout.AddCookie(cookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, logout)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("logout = %d", rec.Code)
	}
}

func TestSessionDetailRendersRetainedConversationEvidence(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	session := syntheticSession("content-session", now)
	repo := &fullStub{
		sessions: []canonical.Session{session},
		events: map[string][]canonical.Event{"content-session": {
			{
				EventID: "user-content", EventType: "user_prompt", OccurredAt: now, ReceivedAt: now,
				Provider: "anthropic", Tool: "claude-code", SourceVersion: "2.1.270",
				ProviderExtensions: map[string]any{"event": map[string]any{"prompt": "first line\n<script>alert('xss')</script>"}},
			},
			{
				EventID: "redacted-content", EventType: "assistant_response", OccurredAt: now.Add(time.Second), ReceivedAt: now,
				Provider: "anthropic", Tool: "claude-code", SourceVersion: "2.1.270",
				ProviderExtensions: map[string]any{"event": map[string]any{"response": "<REDACTED>"}},
			},
			{
				EventID: "body-reference", EventType: "api_request_body", OccurredAt: now.Add(2 * time.Second), ReceivedAt: now,
				Provider: "anthropic", Tool: "claude-code", SourceVersion: "2.1.270",
				ProviderExtensions: map[string]any{"event": map[string]any{"body_ref": "local/request.json"}},
			},
			{
				EventID: "long-content", EventType: "assistant_response", OccurredAt: now.Add(3 * time.Second), ReceivedAt: now,
				Provider: "anthropic", Tool: "claude-code", SourceVersion: "2.1.270",
				ProviderExtensions: map[string]any{"event": map[string]any{"response": strings.Repeat("retained ", 40)}},
			},
		},
		},
	}
	body := renderSessionDetail(t, repo, nil, session.SessionID)
	assertContainsAll(t, body, []string{
		"Retained conversation evidence", "User message", "Assistant response", "API content evidence",
		"provider_redacted", "body_reference", "first line", "Show full retained text", "&lt;script&gt;alert(&#39;xss&#39;)&lt;/script&gt;",
	})
	assertOmitsAll(t, body, []string{"<script>alert('xss')</script>", "other session"})

	partial := getAuthed(t, wrapUI(t, repo), unlock(t, wrapUI(t, repo)), "/sessions/missing/conversation")
	if partial.Code != http.StatusNotFound {
		t.Fatalf("deleted conversation partial status = %d", partial.Code)
	}
}

func TestSessionViewsKeepObservationsInspectableWithoutCountingThemAsPrimary(t *testing.T) {
	now := time.Now().UTC()
	repo := &fullStub{sessions: []canonical.Session{
		{
			SessionID: "codex:provider-session", Provider: "openai", Tool: "codex", State: "completed", StartedAt: now,
			Attributes: map[string]any{"identity_scope": "provider", "identity_source": "conversation.id"},
		},
		{
			SessionID: "codex:token:observation", Provider: "openai", Tool: "codex", State: "failed", StartedAt: now,
			Attributes: map[string]any{"identity_scope": "observation", "identity_source": "content-derived"},
		},
	}, events: map[string][]canonical.Event{
		"codex:token:observation": {{
			EventID:    "observation-skill",
			EventType:  "skill_invocation",
			SessionID:  "codex:token:observation",
			OccurredAt: now,
			ReceivedAt: now,
			Provider:   "openai",
			Tool:       "codex",
			ProviderExtensions: map[string]any{
				"skill_detection": "explicit",
				"skill":           map[string]any{"name": "observation-skill", "outcome": "success"},
			},
		}},
	}}
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)

	primary := getAuthed(t, handler, cookie, "/sessions").Body.String()
	if !strings.Contains(primary, "codex:provider-session") || strings.Contains(primary, "codex:token:observation") {
		t.Fatalf("primary session view = %q", primary)
	}
	observations := getAuthed(t, handler, cookie, "/sessions?scope=observation").Body.String()
	if !strings.Contains(observations, "codex:token:observation") || !strings.Contains(observations, "Observation only") {
		t.Fatalf("observation session view = %q", observations)
	}
	home := getAuthed(t, handler, cookie, "/").Body.String()
	if !strings.Contains(home, "Successful 1 · Failed 0") {
		t.Fatalf("home counts included observation-only row: %q", home)
	}
	if !strings.Contains(home, "Observed skills 1") {
		t.Fatalf("home hid insight evidence retained in an observation-only row: %q", home)
	}
}

func TestHomeRendersBehaviourGovernanceAndIntegrationHighlights(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	completed := now.Add(-time.Hour)
	repo := &fullStub{
		sessions: []canonical.Session{
			{SessionID: "claude-home", Provider: "anthropic", Tool: "claude-code", State: "completed", StartedAt: now.Add(-2 * time.Hour), CompletedAt: &completed},
			{SessionID: "codex-home", Provider: "openai", Tool: "codex", State: "completed", StartedAt: now.Add(-3 * time.Hour)},
		},
		events: map[string][]canonical.Event{
			"claude-home": {
				{
					EventID: "home-mcp", EventType: "mcp_server_connection", SessionID: "claude-home",
					OccurredAt: now.Add(-30 * time.Minute), ReceivedAt: now.Add(-30 * time.Minute), Provider: "anthropic", Tool: "claude-code",
					ProviderExtensions: map[string]any{"event": map[string]any{"server_name": "rogue-home", "status": "connected"}},
				},
				{
					EventID: "home-skill", EventType: "skill_invocation", SessionID: "claude-home",
					OccurredAt: now.Add(-20 * time.Minute), ReceivedAt: now.Add(-20 * time.Minute), Provider: "anthropic", Tool: "claude-code",
					ProviderExtensions: map[string]any{"skill_detection": "explicit", "skill": map[string]any{"name": "home-probe", "outcome": "success"}},
				},
				{
					EventID: "home-model", EventType: "operation", SessionID: "claude-home",
					OccurredAt: now.Add(-10 * time.Minute), ReceivedAt: now.Add(-10 * time.Minute), Provider: "anthropic", Tool: "claude-code",
					ProviderExtensions: map[string]any{"outcome_contract": map[string]any{"model": "claude-home-model", "status": "success", "source": "test"}},
				},
				{
					EventID: "home-context-1", EventType: "model_interaction", SessionID: "claude-home",
					OccurredAt: now.Add(-5 * time.Minute), ReceivedAt: now.Add(-5 * time.Minute), Provider: "anthropic", Tool: "claude-code",
					Attributes: map[string]any{"input_token_count": int64(100), "cached_input_tokens": int64(75)},
				},
				{
					EventID: "home-risk", EventType: "api_request", SessionID: "claude-home",
					OccurredAt: now, ReceivedAt: now, Provider: "anthropic", Tool: "claude-code",
					Attributes: map[string]any{"file_path": "/synthetic/project/.env"},
				},
			},
		},
	}
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds, []string{"approved-home"})
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	body := getAuthed(t, handler, unlock(t, handler), "/").Body.String()

	assertContainsAll(t, body, []string{
		"Behaviour and efficiency",
		"Connected 1 · Invoked 0 · Unused 1 · Usage unavailable 0",
		"Observed skills 1 · Invocations 1",
		"claude-home-model",
		"1 contracts · 1 succeeded · 0 failed · 0 abandoned",
		"Triggered sessions 1 / 1",
		"Risky access:",
		"Unapproved MCP:",
		"Violation",
		"1 findings",
		"Integration health",
		"anthropic",
		"claude-code",
		now.Format(time.RFC3339),
		`href="/insights#mcp-inventory"`,
		`href="/models"`,
		`href="/governance"`,
		`href="/integrations"`,
	})
	if strings.Contains(body, "Cost") || strings.Contains(body, "$0") {
		t.Fatalf("Home must not surface cost content: %q", body)
	}
	if anthropic, openai := strings.Index(body, "anthropic"), strings.Index(body, "openai"); anthropic < 0 || openai < 0 || anthropic >= openai {
		t.Fatalf("integration rows are not sorted by provider/tool: %q", body)
	}
}

func TestHomeKeepsIntegrationEvidenceWhenInsightEventsFail(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	repo := &fullStub{
		sessions: []canonical.Session{{SessionID: "home-partial", Provider: "openai", Tool: "codex", State: "completed", StartedAt: now}},
		eventErr: errors.New("event store unavailable"),
	}
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	body := getAuthed(t, handler, unlock(t, handler), "/").Body.String()
	assertContainsAll(t, body, []string{
		"Insight and governance highlights unavailable.",
		"Governance outcomes are unavailable",
		"Integration health",
		"openai",
		"codex",
		now.Format(time.RFC3339),
	})
	if strings.Contains(body, "Integration health unavailable.") {
		t.Fatalf("session-backed integration evidence should survive an event-reader failure: %q", body)
	}
}

func TestUnlockAndHome(t *testing.T) {
	repo := &fullStub{sessions: []canonical.Session{{
		SessionID: "s1",
		Provider:  "openai",
		Tool:      "codex",
		State:     "completed",
		StartedAt: time.Now().UTC(),
	}}}
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	unlockPage := httptest.NewRequest(http.MethodGet, "/unlock", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, unlockPage)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Unlock local dashboard") {
		t.Fatalf("unlock page = %d %q", rec.Code, rec.Body.String())
	}

	home := httptest.NewRequest(http.MethodGet, "/", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, home)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/unlock" {
		t.Fatalf("unauthenticated home = %d %s", rec.Code, rec.Header().Get("Location"))
	}

	cookie := unlock(t, handler)
	home = httptest.NewRequest(http.MethodGet, "/", nil)
	home.AddCookie(cookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, home)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "Orchestration overview") || !strings.Contains(body, "codex") {
		t.Fatalf("home = %d %q", rec.Code, body)
	}
	assertContainsAll(t, body, []string{
		"No retained MCP connection or invocation evidence.",
		"No provider-reported outcome contracts.",
		"Indeterminate",
		"Allowlist not configured",
	})
	// Home must not surface any calculated or estimated cost content. Issue #149
	// also removes the former global Costs link from this page.
	for _, forbidden := range []string{"Calculated amount", "Cost estimates", "Secondary estimates"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("home must not surface cost figures %q", forbidden)
		}
	}
}

func navigationSection(t *testing.T, body, label string) string {
	t.Helper()
	start := strings.Index(body, `<nav aria-label="`+label+`"`)
	if start < 0 {
		t.Fatalf("page must have labelled %s: %q", label, body)
	}
	end := strings.Index(body[start:], `</nav>`)
	if end < 0 {
		t.Fatalf("%s must close: %q", label, body[start:])
	}
	return body[start : start+end]
}

func assertPrimaryNavigation(t *testing.T, body string) {
	t.Helper()
	primary := navigationSection(t, body, "Primary navigation")
	if got := strings.Count(primary, `class="nav-link`); got != 5 {
		t.Fatalf("primary navigation link count = %d, want 5: %q", got, primary)
	}
	if !strings.Contains(primary, ">Overview</span>") {
		t.Fatalf("primary navigation must label Overview: %q", primary)
	}
	previous := -1
	for _, href := range []string{`href="/"`, `href="/sessions"`, `href="/pull-requests"`, `href="/models"`, `href="/governance"`} {
		index := strings.Index(primary, href)
		if index <= previous {
			t.Fatalf("primary navigation must contain five ordered destinations; %s index=%d previous=%d: %q", href, index, previous, primary)
		}
		previous = index
	}
	for _, legacy := range []string{`href="/insights"`, `href="/privacy"`, `href="/costs"`, `href="/integrations"`} {
		if strings.Contains(primary, legacy) {
			t.Fatalf("primary navigation must not contain %s: %q", legacy, primary)
		}
	}
}

func assertUtilityNavigation(t *testing.T, body string, wantCosts bool) {
	t.Helper()
	utility := navigationSection(t, body, "Utility navigation")
	previous := -1
	for _, href := range []string{`href="/integrations"`, `href="/insights"`, `href="/privacy"`} {
		index := strings.Index(utility, href)
		if index <= previous {
			t.Fatalf("utility navigation must keep ordered destinations; %s index=%d previous=%d: %q", href, index, previous, utility)
		}
		previous = index
	}
	hasCosts := strings.Contains(utility, `href="/costs"`)
	if hasCosts != wantCosts {
		t.Fatalf("utility navigation Costs presence = %t, want %t: %q", hasCosts, wantCosts, utility)
	}
	if strings.Contains(utility, `href="/pull-requests"`) || strings.Contains(utility, `href="/models"`) {
		t.Fatalf("utility navigation must not duplicate Models/Pull Requests: %q", utility)
	}
}

// TestHomeShellControls covers the #161 dark sidebar shell while preserving
// logout and honest daemon health affordances.
func TestHomeShellControls(t *testing.T) {
	repo := &fullStub{sessions: []canonical.Session{{
		SessionID: "s1", Provider: "openai", Tool: "codex",
		State: "completed", StartedAt: time.Now().UTC(),
	}}}
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)
	body := getAuthed(t, handler, cookie, "/").Body.String()

	assertPrimaryNavigation(t, body)
	assertUtilityNavigation(t, body, false)
	if strings.Contains(body, `href="/costs"`) {
		t.Fatalf("Home must not surface a Costs link: %q", body)
	}
	sessionsBody := getAuthed(t, handler, cookie, "/sessions").Body.String()
	assertUtilityNavigation(t, sessionsBody, true)
	if !strings.Contains(body, `action="/logout"`) {
		t.Fatalf("authenticated home must show a logout control: %q", body)
	}
	if !strings.Contains(body, "Daemon: Healthy") || !strings.Contains(body, `class="health ok"`) {
		t.Fatalf("home must show honest healthy daemon badge: %q", body)
	}

	governance := getAuthed(t, handler, cookie, "/governance")
	if governance.Code != http.StatusOK || !strings.Contains(governance.Body.String(), `class="nav-link active" href="/governance" aria-current="page"`) {
		t.Fatalf("governance must render as the active primary destination: status=%d body=%q", governance.Code, governance.Body.String())
	}
	insights := getAuthed(t, handler, cookie, "/insights")
	if insights.Code != http.StatusOK || !strings.Contains(insights.Body.String(), "MCP inventory") {
		t.Fatalf("direct Insights route must remain available: status=%d body=%q", insights.Code, insights.Body.String())
	}
	request := httptest.NewRequest(http.MethodGet, "/governance", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != "/unlock" {
		t.Fatalf("unauthenticated governance = %d %s", recorder.Code, recorder.Header().Get("Location"))
	}
}

// errStub is a SessionReader whose store always fails, to prove the health
// label degrades honestly instead of always reporting "Healthy".
type errStub struct{}

func (errStub) Session(context.Context, string) (canonical.Session, bool, error) {
	return canonical.Session{}, false, context.DeadlineExceeded
}

func (errStub) ListSessions(context.Context, storage.SessionFilter) ([]canonical.Session, error) {
	return nil, context.DeadlineExceeded
}

func TestTimelineRendersLifecycleSignals(t *testing.T) {
	now := time.Now().UTC()
	event := canonical.Event{
		EventID: "lifecycle-event", EventType: "session.active", SessionID: "lifecycle-session",
		OccurredAt: now, ReceivedAt: now, Provider: "openai", Tool: "codex",
		Attributes: map[string]any{
			"lifecycle_kind":     "session_start",
			"entrypoint":         "codex exec",
			"unavailable_fields": []string{"tool_calls"},
		},
	}
	assertTimelineContains(t, "lifecycle-session", event, "Session active", "Lifecycle", "session_start", "Entrypoint", "codex exec")
}

func TestTimelineRendersToolDecisionApprovals(t *testing.T) {
	now := time.Now().UTC()
	// Flat cases avoid Sonar CPD pairing near-identical Event/map literals.
	cases := []struct {
		name, sessionID, eventID, eventType, provider, tool      string
		decision, reason, qualifierKey, qualifierValue, toolName string
		wantToolLabel                                            string
		extraWants                                               []string
	}{
		{
			name: "codex uses tool_namespace qualifier", sessionID: "decision-session",
			eventID: "decision-event", eventType: "codex.tool_decision", provider: "openai", tool: "codex",
			decision: "approved", reason: "policy", qualifierKey: "tool_namespace", qualifierValue: "functions",
			toolName: "exec_command", wantToolLabel: "functions/exec_command",
		},
		{
			name: "claude uses tool_source qualifier", sessionID: "claude-decision-session",
			eventID: "claude-code:claude-decision-session:28", eventType: "tool_decision", provider: "anthropic", tool: "claude-code",
			decision: "denied", reason: "hook", qualifierKey: "tool_source", qualifierValue: "builtin",
			toolName: "Bash", wantToolLabel: "builtin/Bash",
			extraWants: []string{"Tool decision"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			attrs := map[string]any{
				"approval_decision":     tc.decision,
				"approval_reason_class": tc.reason,
				tc.qualifierKey:         tc.qualifierValue,
				"tool_name":             tc.toolName,
				"unavailable_fields":    []string{"tool_calls"},
			}
			event := canonical.Event{
				EventID: tc.eventID, EventType: tc.eventType, SessionID: tc.sessionID,
				OccurredAt: now, ReceivedAt: now, Provider: tc.provider, Tool: tc.tool,
				Attributes: attrs,
			}
			want := append([]string{"Approval", tc.decision, "Reason", tc.reason, "Tool", tc.wantToolLabel}, tc.extraWants...)
			assertTimelineContains(t, tc.sessionID, event, want...)
		})
	}
}

func TestTimelineRendersObservedOperationDetails(t *testing.T) {
	now := time.Now().UTC()
	event := canonical.Event{
		EventID: "operation-event", EventType: "operation", SessionID: "operation-session",
		OccurredAt: now, ReceivedAt: now, Provider: "openai", Tool: "codex",
		Attributes: map[string]any{
			"operation_id":   "call-123",
			"category":       "shell command",
			"tool_namespace": "functions",
			"tool_name":      "exec_command",
			"duration_ms":    "92",
			"outcome":        "success",
		},
	}
	body := renderSessionDetail(t, &fullStub{
		sessions: []canonical.Session{syntheticSession("operation-session", now)},
		events:   map[string][]canonical.Event{"operation-session": {event}},
	}, nil, "operation-session")
	operation := timelineOperationSection(t, body)
	assertContainsAll(t, operation, []string{
		"Operation details", "call-123", "shell command", "functions/exec_command", "92 ms", "Succeeded",
	})
}

func TestSessionDetailRendersFileEvidence(t *testing.T) {
	now := time.Now().UTC()
	event := canonical.Event{
		EventID: "file-span", EventType: "claude_code.tool", SessionID: "file-session",
		OccurredAt: now, ReceivedAt: now, Provider: "anthropic", Tool: "claude-code",
		Attributes: map[string]any{
			"tool": map[string]any{
				"file_path": "/workspace/proven-file.go", "tool_name": "Read", "duration_ms": int64(200),
			},
		},
	}
	body := renderSessionDetail(t, &fullStub{
		sessions: []canonical.Session{syntheticSession("file-session", now)},
		events:   map[string][]canonical.Event{"file-session": {event}},
	}, nil, "file-session")
	assertContainsAll(t, body, []string{
		"File evidence", "/workspace/proven-file.go", "read", "200 ms", "file-span",
	})
	if strings.Contains(body, "lines_added") || strings.Contains(body, "lines_removed") {
		t.Fatalf("aggregate LOC must not appear as file evidence: %s", body)
	}
}

func TestTimelineRendersOperationToolFromRetainedProviderEvidence(t *testing.T) {
	now := time.Now().UTC()
	event := canonical.Event{
		EventID: "extension-operation", EventType: "codex.tool_result", SessionID: "extension-operation-session",
		OccurredAt: now, ReceivedAt: now, Provider: "openai", Tool: "codex",
		Attributes: map[string]any{"operation_id": "extension-call"},
		ProviderExtensions: map[string]any{
			"tool_call": map[string]any{"tool_name": "exec_command", "tool_namespace": "functions"},
		},
	}
	body := renderSessionDetail(t, &fullStub{
		sessions: []canonical.Session{syntheticSession("extension-operation-session", now)},
		events:   map[string][]canonical.Event{"extension-operation-session": {event}},
	}, nil, "extension-operation-session")
	if operation := timelineOperationSection(t, body); !strings.Contains(operation, "functions/exec_command") {
		t.Fatalf("retained operation tool evidence was not rendered: %q", operation)
	}
}

func TestTimelineOperationDurationHonoursObservedNumericForms(t *testing.T) {
	tests := []struct {
		name string
		raw  any
		want string
	}{
		{name: "string", raw: "92", want: "92 ms"},
		{name: "integer", raw: int64(7), want: "7 ms"},
		{name: "float", raw: 12.5, want: "12.5 ms"},
		{name: "observed zero", raw: float64(0), want: "0 ms"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC()
			event := canonical.Event{
				EventID: "duration-event", EventType: "operation", SessionID: "duration-session",
				OccurredAt: now, ReceivedAt: now, Provider: "openai", Tool: "codex",
				Attributes: map[string]any{"operation_id": "duration-call", "duration_ms": tc.raw},
			}
			body := renderSessionDetail(t, &fullStub{
				sessions: []canonical.Session{syntheticSession("duration-session", now)},
				events:   map[string][]canonical.Event{"duration-session": {event}},
			}, nil, "duration-session")
			if operation := timelineOperationSection(t, body); !strings.Contains(operation, tc.want) {
				t.Fatalf("operation duration missing %q: %q", tc.want, operation)
			}
		})
	}
}

func TestTimelineOperationMissingOrMalformedFieldsStayUnavailable(t *testing.T) {
	invalidDurations := []any{"not-a-number", -1, math.NaN(), math.Inf(1)}
	for index, duration := range invalidDurations {
		now := time.Now().UTC()
		sessionID := "partial-operation-" + strconv.Itoa(index)
		event := canonical.Event{
			EventID: "partial-event", EventType: "operation", SessionID: sessionID,
			OccurredAt: now, ReceivedAt: now, Provider: "openai", Tool: "codex",
			Attributes: map[string]any{"operation_id": "partial-call", "duration_ms": duration},
		}
		body := renderSessionDetail(t, &fullStub{
			sessions: []canonical.Session{syntheticSession(sessionID, now)},
			events:   map[string][]canonical.Event{sessionID: {event}},
		}, nil, sessionID)
		operation := timelineOperationSection(t, body)
		if strings.Count(operation, "Not proven yet") != 2 {
			t.Fatalf("missing category/outcome must stay unknown: %q", operation)
		}
		if strings.Count(operation, "Not available from this provider") != 2 {
			t.Fatalf("missing tool and invalid duration must stay unavailable: %q", operation)
		}
	}
}

func TestTimelineDoesNotInferOperationFromOtherSignals(t *testing.T) {
	now := time.Now().UTC()
	event := canonical.Event{
		EventID: "not-operation", EventType: "tool_decision", SessionID: "not-operation-session",
		OccurredAt: now, ReceivedAt: now, Provider: "openai", Tool: "codex",
		Attributes: map[string]any{
			"category": "shell command", "duration_ms": int64(9), "outcome": "success",
			"approval_decision": "approved", "lifecycle_kind": "session_start",
		},
	}
	body := renderSessionDetail(t, &fullStub{
		sessions: []canonical.Session{syntheticSession("not-operation-session", now)},
		events:   map[string][]canonical.Event{"not-operation-session": {event}},
	}, nil, "not-operation-session")
	if strings.Contains(body, "Operation details") {
		t.Fatalf("timeline inferred an operation without operation_id: %q", body)
	}
}

func TestSessionGovernanceChecklistIsScopedAndHonest(t *testing.T) {
	now := time.Now().UTC()
	repo := &fullStub{
		sessions: []canonical.Session{
			syntheticSession("selected-session", now),
			syntheticSession("other-session", now),
		},
		events: map[string][]canonical.Event{
			"selected-session": {
				governanceEvent("selected-file", "selected-session", now, map[string]any{"file_path": "/workspace/readme.md"}, nil),
				governanceEvent("selected-mcp", "selected-session", now, nil, map[string]any{"server_name": "filesystem", "status": "connected"}),
			},
			"other-session": {
				governanceEvent("other-file", "other-session", now, map[string]any{"file_path": "/workspace/.env"}, nil),
				governanceEvent("other-mcp", "other-session", now, nil, map[string]any{"server_name": "rogue-server", "status": "connected"}),
			},
		},
	}
	body := renderSessionDetail(t, repo, []string{"filesystem"}, "selected-session")
	checklist := sessionGovernanceSection(t, body)
	if strings.Count(checklist, "Not a violation") != 2 || strings.Count(checklist, "0 findings") != 2 {
		t.Fatalf("session-scoped clean evidence rendered incorrectly: %q", checklist)
	}
	if strings.Contains(checklist, "Violation") {
		t.Fatalf("events from another session contaminated the checklist: %q", checklist)
	}

	violation := renderSessionDetail(t, repo, []string{"filesystem"}, "other-session")
	violationChecklist := sessionGovernanceSection(t, violation)
	if strings.Count(violationChecklist, "Violation") != 2 || strings.Count(violationChecklist, "1 finding") != 2 {
		t.Fatalf("session violations and counts missing: %q", violationChecklist)
	}
}

func TestSessionGovernanceChecklistPreservesMissingEvidenceStates(t *testing.T) {
	now := time.Now().UTC()
	repo := &fullStub{
		sessions: []canonical.Session{syntheticSession("empty-governance-session", now)},
		events:   map[string][]canonical.Event{"empty-governance-session": {}},
	}
	body := renderSessionDetail(t, repo, nil, "empty-governance-session")
	checklist := sessionGovernanceSection(t, body)
	assertContainsAll(t, checklist, []string{"Indeterminate", "Not available from this provider", "Allowlist not configured", "0 findings"})
}

func TestSessionGovernanceChecklistUsesEventsBeyondTimelinePage(t *testing.T) {
	now := time.Now().UTC()
	events := make([]canonical.Event, 0, 51)
	for index := range 50 {
		events = append(events, governanceEvent(
			"ordinary-"+strconv.Itoa(index),
			"paged-governance-session",
			now.Add(time.Duration(index)*time.Second),
			map[string]any{"file_path": "/workspace/file.txt"},
			nil,
		))
	}
	events = append(events, governanceEvent(
		"risky-after-page",
		"paged-governance-session",
		now.Add(50*time.Second),
		map[string]any{"file_path": "/workspace/.env"},
		nil,
	))
	repo := &fullStub{
		sessions: []canonical.Session{syntheticSession("paged-governance-session", now)},
		events:   map[string][]canonical.Event{"paged-governance-session": events},
	}
	body := renderSessionDetail(t, repo, []string{"filesystem"}, "paged-governance-session")
	checklist := sessionGovernanceSection(t, body)
	assertContainsAll(t, checklist, []string{"Risky access", "Violation", "1 finding"})
	if !strings.Contains(body, "Load more") {
		t.Fatalf("fixture did not exercise a paginated timeline: %q", body)
	}
}

func TestSessionGovernanceReadFailureKeepsSessionMetadataVisible(t *testing.T) {
	now := time.Now().UTC()
	repo := &fullStub{
		sessions: []canonical.Session{syntheticSession("governance-error-session", now)},
		eventErr: errors.New("synthetic event read failure"),
	}
	body := renderSessionDetail(t, repo, []string{"filesystem"}, "governance-error-session")
	assertContainsAll(t, body, []string{
		"Session governance-error-session",
		"anthropic",
		"Session governance checks are unavailable because retained events could not be loaded.",
		"Unable to load timeline.",
	})
	checklist := sessionGovernanceSection(t, body)
	if strings.Count(checklist, "Indeterminate") != 2 || strings.Count(checklist, "Not available from this provider") != 2 {
		t.Fatalf("failed event read must make both checks explicitly unavailable: %q", checklist)
	}
}

func TestSessionHeaderMetadataShowsBranchAndUnavailablePR(t *testing.T) {
	now := time.Now().UTC()
	session := syntheticSession("header-metadata-session", now)
	session.Attributes = map[string]any{
		"entrypoint": "cli",
		"git_branch": "feature/158-session-header",
	}
	body := renderSessionDetail(t, &fullStub{sessions: []canonical.Session{session}}, nil, "header-metadata-session")
	assertContainsAll(t, body, []string{
		"Environment",
		"Branch",
		"feature/158-session-header",
		"PR",
		"Entrypoint",
		"cli",
		"Availability",
	})
	env := body[strings.Index(body, "Environment"):strings.Index(body, "Availability")]
	if !strings.Contains(env, "Not available from this provider") {
		t.Fatalf("PR must render unavailable in Environment when no fixture proves a link: %q", env)
	}
	if strings.Contains(env, "pull_request") || strings.Contains(strings.ToLower(env), "github.com") {
		t.Fatalf("must not invent a PR link: %q", env)
	}
}

func governanceEvent(eventID, sessionID string, now time.Time, attributes, extensionEvent map[string]any) canonical.Event {
	event := canonical.Event{
		EventID: eventID, EventType: "api_request", SessionID: sessionID,
		OccurredAt: now, ReceivedAt: now, Provider: "anthropic", Tool: "claude-code",
		Attributes: attributes,
	}
	if extensionEvent != nil {
		event.EventType = "mcp_server_connection"
		event.ProviderExtensions = map[string]any{"event": extensionEvent}
	}
	return event
}

func renderSessionDetail(t *testing.T, repo *fullStub, allowlist []string, sessionID string) string {
	t.Helper()
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds, allowlist)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)
	return getAuthed(t, handler, cookie, "/sessions/"+sessionID).Body.String()
}

func timelineOperationSection(t *testing.T, body string) string {
	t.Helper()
	return htmlSection(t, body, `<section class="timeline-operation"`)
}

func sessionGovernanceSection(t *testing.T, body string) string {
	t.Helper()
	return htmlSection(t, body, `<section aria-labelledby="session-governance-heading"`)
}

func htmlSection(t *testing.T, body, opening string) string {
	t.Helper()
	start := strings.Index(body, opening)
	if start == -1 {
		t.Fatalf("section %q missing from body: %q", opening, body)
	}
	end := strings.Index(body[start:], "</section>")
	if end == -1 {
		t.Fatalf("section %q did not close: %q", opening, body[start:])
	}
	return body[start : start+end]
}

func assertTimelineContains(t *testing.T, sessionID string, event canonical.Event, want ...string) {
	t.Helper()
	now := event.OccurredAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	repo := &fullStub{
		sessions: []canonical.Session{syntheticSession(sessionID, now)},
		events:   map[string][]canonical.Event{sessionID: {event}},
	}
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)
	body := getAuthed(t, handler, cookie, "/sessions/"+sessionID).Body.String()
	for _, fragment := range want {
		if !strings.Contains(body, fragment) {
			t.Fatalf("timeline missing %q in body: %q", fragment, body)
		}
	}
}

func TestTimelineInvalidTokenStringIsUnavailable(t *testing.T) {
	now := time.Now().UTC()
	repo := &fullStub{
		sessions: []canonical.Session{syntheticSession("invalid-token-session", now)},
		events: map[string][]canonical.Event{
			"invalid-token-session": {{
				EventID:    "bad-token",
				EventType:  "model_interaction",
				SessionID:  "invalid-token-session",
				OccurredAt: now,
				ReceivedAt: now,
				Provider:   "openai",
				Tool:       "codex",
				Attributes: map[string]any{"input_token_count": "not-a-number"},
			}},
		},
	}
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)
	body := getAuthed(t, handler, cookie, "/sessions/invalid-token-session").Body.String()
	if strings.Contains(body, "not-a-number tokens") {
		t.Fatalf("invalid token strings must not get token units: %q", body)
	}
	if !strings.Contains(body, "Not available from this provider") {
		t.Fatalf("invalid token strings should render unavailable: %q", body)
	}
}

func TestSessionsEmptyStateExplainsIngestNextStep(t *testing.T) {
	server, err := ui.New("test-token", &fullStub{}, defaultContextWasteThresholds, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)
	body := getAuthed(t, handler, cookie, "/sessions").Body.String()
	for _, want := range []string{"No retained sessions yet.", "Cursor Enterprise OTLP", "local-dev Cursor Agent ingest path"} {
		if !strings.Contains(body, want) {
			t.Fatalf("sessions empty state missing %q in body: %q", want, body)
		}
	}
}

func TestHealthDegradesWhenStorageFails(t *testing.T) {
	server, err := ui.New("test-token", errStub{}, defaultContextWasteThresholds, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)
	body := getAuthed(t, handler, cookie, "/").Body.String()
	if !strings.Contains(body, "Daemon: Degraded") || !strings.Contains(body, `class="health degraded"`) {
		t.Fatalf("failing storage must render a degraded daemon badge, got: %q", body)
	}
}

func TestUnlockPageHidesLogout(t *testing.T) {
	server, err := ui.New("test-token", &fullStub{}, defaultContextWasteThresholds, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/unlock", nil))
	body := rec.Body.String()
	if strings.Contains(body, `action="/logout"`) {
		t.Fatalf("unlock page must not show a logout control: %q", body)
	}
	if strings.Contains(body, "Daemon:") {
		t.Fatalf("unlock page must not show a daemon health badge: %q", body)
	}
}

// renderMCPConnectionRow renders the insights page for a single MCP connection
// event and returns the table row containing the given anchor text.
func renderMCPConnectionRow(t *testing.T, sessionID, anchor string, rawEvent map[string]any) string {
	t.Helper()
	now := time.Now().UTC()
	repo := &fullStub{
		sessions: []canonical.Session{syntheticSession(sessionID, now)},
		events: map[string][]canonical.Event{
			sessionID: {{
				EventID:            "connection-only",
				EventType:          "mcp_server_connection",
				SessionID:          sessionID,
				OccurredAt:         now,
				ReceivedAt:         now,
				Provider:           "anthropic",
				Tool:               "claude-code",
				ProviderExtensions: map[string]any{"event": rawEvent},
			}},
		},
	}
	body := renderInsights(t, repo)
	rowStart := strings.Index(body, anchor)
	if rowStart == -1 {
		t.Fatalf("MCP row missing: %q", body)
	}
	rowEnd := strings.Index(body[rowStart:], "</tr>")
	if rowEnd == -1 {
		t.Fatalf("MCP row did not close: %q", body[rowStart:])
	}
	return body[rowStart : rowStart+rowEnd]
}

func TestInsightsMCPInvocationCountUnavailableDoesNotRenderZero(t *testing.T) {
	// A connection event with no server name has no correlatable identity, so
	// usage genuinely cannot be measured — the invocations cell must not imply a
	// measured zero.
	row := renderMCPConnectionRow(t, "mcp-unavailable-session", "Unknown server", map[string]any{"status": "connected"})
	if strings.Contains(row, "0 invocations") {
		t.Fatalf("usage-unavailable MCP row must not imply measured zero invocations: %q", row)
	}
	if !strings.Contains(row, "Usage not available") {
		t.Fatalf("usage-unavailable MCP row should render unavailable badge in invocations cell: %q", row)
	}
}

func TestInsightsMCPConnectedButUnusedRendersMeasuredZero(t *testing.T) {
	// A named server with no observed invocation is connected-but-unused: usage
	// evidence was checked and the count is a genuine zero, so the invocations
	// cell shows the measured count rather than the unavailable badge.
	row := renderMCPConnectionRow(t, "mcp-unused-session", "connected-unused-mcp", map[string]any{"server_name": "connected-unused-mcp"})
	if !strings.Contains(row, "0 invocations") {
		t.Fatalf("connected-but-unused MCP row should render a measured zero count: %q", row)
	}
	if strings.Contains(row, "Usage not available") {
		t.Fatalf("connected-but-unused MCP row must not claim usage was unavailable: %q", row)
	}
}

func TestInsightsRenderGlossaryLabelsUnitsNotesAndLinks(t *testing.T) {
	now := time.Now().UTC()
	repo := &fullStub{
		sessions: []canonical.Session{syntheticSession("insight-session", now)},
		events: map[string][]canonical.Event{
			"insight-session": {{
				EventID:    "mcp-connection",
				EventType:  "mcp_server_connection",
				SessionID:  "insight-session",
				OccurredAt: now,
				ReceivedAt: now,
				Provider:   "anthropic",
				Tool:       "claude-code",
				ProviderExtensions: map[string]any{
					"event": map[string]any{
						"server_name":       "tiq-mcp",
						"server_hash":       "mcp-hash",
						"status":            "connected",
						"server_scope":      "project",
						"transport_type":    "stdio",
						"is_plugin":         false,
						"input_tokens":      int64(1200),
						"output_tokens":     int64(50),
						"cache_read_tokens": int64(900),
					},
				},
			}, {
				EventID:    "skill-1",
				EventType:  "skill_invocation",
				SessionID:  "insight-session",
				OccurredAt: now.Add(time.Second),
				ReceivedAt: now.Add(time.Second),
				Provider:   "anthropic",
				Tool:       "claude-code",
				ProviderExtensions: map[string]any{
					"skill_detection": "explicit",
					"skill": map[string]any{
						"name":    "tiq-probe",
						"outcome": "success",
					},
				},
			}, {
				EventID:    "model-1",
				EventType:  "operation",
				SessionID:  "insight-session",
				OccurredAt: now.Add(2 * time.Second),
				ReceivedAt: now.Add(2 * time.Second),
				Provider:   "anthropic",
				Tool:       "claude-code",
				ProviderExtensions: map[string]any{
					"outcome_contract": map[string]any{
						"model":         "claude-test",
						"status":        "success",
						"retry_attempt": int64(1),
						"duration_ms":   1234.0,
						"input_tokens":  int64(10),
						"output_tokens": int64(5),
						"source":        "test",
					},
				},
			}, {
				EventID:    "ctx-1",
				EventType:  "model_interaction",
				SessionID:  "insight-session",
				OccurredAt: now.Add(3 * time.Second),
				ReceivedAt: now.Add(3 * time.Second),
				Provider:   "anthropic",
				Tool:       "claude-code",
				Attributes: map[string]any{"input_token_count": int64(100), "cached_input_tokens": int64(75)},
			}, {
				EventID:    "ctx-2",
				EventType:  "model_interaction",
				SessionID:  "insight-session",
				OccurredAt: now.Add(4 * time.Second),
				ReceivedAt: now.Add(4 * time.Second),
				Provider:   "anthropic",
				Tool:       "claude-code",
				Attributes: map[string]any{"input_token_count": int64(200), "cached_input_tokens": int64(150)},
			}},
		},
	}
	body := renderInsights(t, repo)

	for _, want := range []string{
		`id="mcp-inventory"`,
		`id="skill-usage"`,
		`id="model-performance"`,
		`id="context-pressure"`,
		"Provider reported",
		"Connected, never invoked",
		"1500 tokens",
		"Explicitly identified",
		"Seen in telemetry",
		"100% retried",
		"1234 ms",
		"75% cached",
		"2.0×",
		`href="/sessions/insight-session"`,
		`href="/models"`,
		"Evidence notes",
		"Tokens per completed task",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("insights page missing %q in body: %q", want, body)
		}
	}
	if strings.Contains(body, "%!f(*float64") {
		t.Fatalf("insights page should not expose pointer formatter output: %q", body)
	}
}

func TestModelsPageRendersScorecardAndUnlockGate(t *testing.T) {
	now := time.Now().UTC()
	repo := &fullStub{
		sessions: []canonical.Session{{
			SessionID: "model-session",
			Provider:  "anthropic",
			Tool:      "claude-code",
			State:     "completed",
			StartedAt: now,
		}},
		events: map[string][]canonical.Event{
			"model-session": {{
				EventID:    "outcome-1",
				EventType:  "model_interaction",
				SessionID:  "model-session",
				OccurredAt: now,
				ReceivedAt: now,
				Provider:   "anthropic",
				Tool:       "claude-code",
				ProviderExtensions: map[string]any{
					"outcome_contract": map[string]any{
						"model":         "claude-test",
						"status":        "success",
						"retry_attempt": int64(1),
						"duration_ms":   1234.0,
						"input_tokens":  int64(10),
						"output_tokens": int64(5),
						"source":        "test",
					},
				},
			}},
		},
	}
	handler := wrapUI(t, repo)
	cookie := assertUnlockGate(t, handler, "/models")
	body := getAuthed(t, handler, cookie, "/models").Body.String()
	assertContainsAll(t, body, []string{
		"<h1>Models</h1>",
		`class="nav-link active" href="/models" aria-current="page"`,
		"Ranking available: no, sample size below the ranking guard",
		"claude-test",
		"anthropic",
		"1234 ms",
		"tokens/task",
		`href="/insights#model-performance"`,
		"Evidence notes",
	})
	assertPrimaryNavigation(t, body)
	assertUtilityNavigation(t, body, true)

	emptyBody := authedPageBody(t, &fullStub{}, "/models")
	if !strings.Contains(emptyBody, "No outcome-contract rows yet") {
		t.Fatalf("empty models page missing unavailable copy: %q", emptyBody)
	}
}

func TestPullRequestsPageRendersGroupsAndUnlockGate(t *testing.T) {
	now := time.Now().UTC()
	repo := &fullStub{sessions: []canonical.Session{
		completedSession("pr-session-a", "anthropic", "claude-code", now, map[string]any{
			"pr_link": "https://github.com/org/repo/pull/12", "git_branch": "feature/pr-12", "repository": "org/repo",
		}),
		completedSession("pr-session-b", "openai", "codex", now, map[string]any{
			"pr_link": "https://github.com/org/repo/pull/12",
		}),
		completedSession("unsafe-session", "openai", "codex", now, map[string]any{
			"pr_link": "javascript:alert(1)",
		}),
		completedSession("branch-only", "openai", "codex", now, map[string]any{
			"git_branch": "feature/no-url",
		}),
	}}
	handler := wrapUI(t, repo)
	cookie := assertUnlockGate(t, handler, "/pull-requests")
	body := getAuthed(t, handler, cookie, "/pull-requests").Body.String()
	assertContainsAll(t, body, []string{
		"<h1>Pull Requests</h1>",
		`class="nav-link active" href="/pull-requests" aria-current="page"`,
		`href="https://github.com/org/repo/pull/12"`,
		"2 linked sessions",
		`href="/sessions/pr-session-a"`,
		`href="/sessions/pr-session-b"`,
		"org/repo",
		"feature/pr-12",
		"#183",
		"#184",
		"Evidence notes",
	})
	if strings.Contains(body, "javascript:alert(1)") {
		t.Fatalf("unsafe URL must not render as evidence link: %q", body)
	}
	if strings.Contains(body, "feature/no-url") {
		t.Fatalf("branch-only session must not invent a PR group: %q", body)
	}
	assertPrimaryNavigation(t, body)
	assertUtilityNavigation(t, body, true)

	filtered := getAuthed(t, handler, cookie, "/pull-requests?q=pr-session-missing").Body.String()
	if !strings.Contains(filtered, "No retained HTTP(S) pull-request URLs yet") {
		t.Fatalf("filtered empty state missing copy: %q", filtered)
	}
	emptyBody := authedPageBody(t, &fullStub{}, "/pull-requests")
	if !strings.Contains(emptyBody, "No retained HTTP(S) pull-request URLs yet") {
		t.Fatalf("empty pull-requests page missing unavailable copy: %q", emptyBody)
	}
	errBody := authedPageBody(t, errStub{}, "/pull-requests")
	if !strings.Contains(errBody, "Unable to load pull-request evidence.") {
		t.Fatalf("reader error missing alert: %q", errBody)
	}
}

func completedSession(id, provider, tool string, started time.Time, attrs map[string]any) canonical.Session {
	return canonical.Session{
		SessionID:  id,
		Provider:   provider,
		Tool:       tool,
		State:      "completed",
		StartedAt:  started,
		Attributes: attrs,
	}
}

func TestContextWasteObservedFloatsRenderNumeric(t *testing.T) {
	now := time.Now().UTC()
	repo := &fullStub{
		sessions: []canonical.Session{{
			SessionID: "context-session",
			Provider:  "anthropic",
			Tool:      "claude-code",
			State:     "completed",
			StartedAt: now,
		}},
		events: map[string][]canonical.Event{
			"context-session": {{
				EventID:    "ctx-1",
				EventType:  "model_interaction",
				SessionID:  "context-session",
				OccurredAt: now,
				ReceivedAt: now,
				Provider:   "anthropic",
				Tool:       "claude-code",
				Attributes: map[string]any{
					"input_token_count":   int64(100),
					"cached_input_tokens": int64(75),
				},
			}, {
				EventID:    "ctx-2",
				EventType:  "model_interaction",
				SessionID:  "context-session",
				OccurredAt: now.Add(time.Second),
				ReceivedAt: now.Add(time.Second),
				Provider:   "anthropic",
				Tool:       "claude-code",
				Attributes: map[string]any{
					"input_token_count":   int64(200),
					"cached_input_tokens": int64(150),
				},
			}},
		},
	}
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)
	rec := getAuthed(t, handler, cookie, "/insights")
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("insights = %d %q", rec.Code, body)
	}
	if strings.Contains(body, "%!f(*float64") {
		t.Fatalf("observed context-waste floats rendered pointer formatter output: %q", body)
	}
	rowStart := strings.Index(body, "context-session")
	if rowStart == -1 {
		t.Fatalf("context-waste row missing: %q", body)
	}
	rowEnd := strings.Index(body[rowStart:], "</tr>")
	if rowEnd == -1 {
		t.Fatalf("context-waste row did not close: %q", body[rowStart:])
	}
	row := body[rowStart : rowStart+rowEnd]
	if !strings.Contains(row, ">75% cached<") || !strings.Contains(row, ">2.0×<") {
		t.Fatalf("context-pressure row should render observed cached ratio and input growth with glossary units: %q", row)
	}
	if !strings.Contains(body, `href="/sessions/context-session"`) {
		t.Fatalf("context-pressure row should link to session detail: %q", body)
	}
}

func TestUnavailableNotZeroOnCosts(t *testing.T) {
	server, err := ui.New("test-token", &fullStub{}, defaultContextWasteThresholds, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)
	rec := getAuthed(t, handler, cookie, "/costs")
	body := rec.Body.String()
	if !strings.Contains(body, "unavailable") {
		t.Fatalf("costs page should label missing amount unavailable: %q", body)
	}
	if strings.Contains(body, "0.000000") {
		t.Fatalf("unknown cost must not render as zero: %q", body)
	}
}

func TestKnownZeroCostRendersZero(t *testing.T) {
	zero := int64(0)
	repo := &fullStub{costs: []cost.Record{{
		Currency:       "EUR",
		Status:         "calculated",
		AmountMicrousd: &zero,
	}}}
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)
	rec := getAuthed(t, handler, cookie, "/costs")
	body := rec.Body.String()
	if !strings.Contains(body, "Calculated amount (EUR): 0.000000") {
		t.Fatalf("known zero should render with currency: %q", body)
	}
}

func TestTimelinePartialRejectsInvalidSessionID(t *testing.T) {
	server, err := ui.New("test-token", &fullStub{}, defaultContextWasteThresholds, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)
	req := httptest.NewRequest(http.MethodGet, "/sessions/../evil/timeline", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("invalid timeline id status = %d", rec.Code)
	}
}

func TestIntegrationsCursorEnterpriseStatus(t *testing.T) {
	start := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	later := start.Add(45 * time.Minute)

	tests := []struct {
		name     string
		repo     storage.SessionReader
		want     []string
		wantNone []string
	}{
		{
			name: "unavailable when only local-dev cursor-agent sessions exist",
			repo: &fullStub{sessions: []canonical.Session{
				{SessionID: "codex:s1", Provider: "openai", Tool: "codex", StartedAt: start},
				{SessionID: "cursor-agent:local", Provider: "cursor", Tool: "cursor-agent", StartedAt: start},
			}},
			want: []string{
				"Capability matrix",
				"Model identity",
				"Supported",
				"Version dependent",
				"Not proven yet",
				"Tools observed",
				"Last seen",
				"codex",
				"cursor-agent",
				"2026-09-13T12:00:00Z",
				"Cursor Enterprise OpenTelemetry Export",
				"Team Settings",
				"OpenTelemetry Export",
				"docs/integrations/cursor-enterprise-otel.md",
				"protected-gateway",
				"Do not expose the unauthenticated loopback daemon",
				"Not available from this provider",
				"Local-dev only",
				"POST /v1/cursor-agent",
				"Privacy",
				`href="/privacy"`,
			},
			wantNone: []string{
				`scripts/cursor-agent-tiq "say ok"`,
				`class="status status-observed"`,
				"Seen in telemetry",
			},
		},
		{
			name: "observed uses latest event time not session start",
			repo: &fullStub{
				sessions: []canonical.Session{{
					SessionID: "cursor:enterprise-session",
					Provider:  "cursor",
					Tool:      "cursor",
					StartedAt: start,
				}},
				events: map[string][]canonical.Event{
					"cursor:enterprise-session": {{
						EventID:    "e1",
						EventType:  "model_interaction",
						OccurredAt: start,
						ReceivedAt: start,
						Provider:   "cursor",
						Tool:       "cursor",
					}, {
						EventID:    "e2",
						EventType:  "model_interaction",
						OccurredAt: later,
						ReceivedAt: later,
						Provider:   "cursor",
						Tool:       "cursor",
					}},
				},
			},
			want: []string{
				"Capability matrix",
				"Cursor Enterprise OpenTelemetry Export",
				"Team Settings",
				"Seen in telemetry",
				"Last seen",
				"2026-09-13T12:45:00Z",
				">cursor<",
				"Tools observed",
			},
			wantNone: []string{
				"Not available from this provider",
				"Awaiting telemetry",
				"2026-09-13T12:00:00Z",
			},
		},
		{
			name: "storage failure is unknown not unavailable",
			repo: errStub{},
			want: []string{
				"Unable to load integrations.",
				"Not proven yet",
				"Capability matrix",
			},
			wantNone: []string{
				"Not available from this provider",
				"Seen in telemetry",
			},
		},
		{
			name: "empty tools still shows capability matrix",
			repo: &fullStub{},
			want: []string{
				"Capability matrix",
				"Codex",
				"Claude Code",
				"Cursor",
				"MCP calls",
				"Awaiting telemetry",
				`href="/privacy"`,
			},
			wantNone: []string{
				"Seen in telemetry",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertIntegrationsBody(t, tc.repo, tc.want, tc.wantNone)
		})
	}
}

func assertIntegrationsBody(t *testing.T, repo storage.SessionReader, want, wantNone []string) {
	t.Helper()
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)
	body := getAuthed(t, handler, cookie, "/integrations").Body.String()
	for _, fragment := range want {
		if !strings.Contains(body, fragment) {
			t.Fatalf("missing %q in body: %q", fragment, body)
		}
	}
	for _, fragment := range wantNone {
		if strings.Contains(body, fragment) {
			t.Fatalf("unexpected %q in body: %q", fragment, body)
		}
	}
}

func TestGovernanceFindingsPage(t *testing.T) {
	repo := governanceFindingsFixture(t)

	t.Run("violation_with_session_link_and_unconfigured_allowlist", func(t *testing.T) {
		body := renderGovernance(t, repo, nil)
		assertContainsAll(t, body, []string{
			"Findings",
			"Risky access",
			"Unapproved MCP",
			"Governance Policies",
			"Local configuration",
			"Access Rules",
			"Policy Preview",
			`aria-label="Local policy summary"`,
			"Violation",
			"/home/dev/app/.env",
			`href="/sessions/gov-session-1"`,
			"Allowlist not configured",
			"does not enforce or publish",
			`role="tablist"`,
			`aria-selected="true"`,
			">MCP servers</a>",
			`id="rules-panel-mcp"`,
			"MCP access rules",
			`data-mcp-policy="unconfigured"`,
			`data-active-checked="false"`,
		})
		if strings.Contains(body, "Governance findings are not available") {
			t.Fatalf("placeholder copy must be gone: %q", body)
		}
		if strings.Contains(body, `data-mcp-policy="not-allowlisted"`) {
			t.Fatalf("empty allowlist must not mark rows as explicitly not-allowlisted: %q", body)
		}
	})

	t.Run("allowlist_flags_unapproved_server", func(t *testing.T) {
		body := renderGovernance(t, repo, []string{"filesystem"})
		assertContainsAll(t, body, []string{
			"Unapproved", "rogue-tool", "Violation",
			`data-mcp-policy="allowlisted"`,
			`data-mcp-policy="not-allowlisted"`,
			`data-active-checked="true"`,
		})
	})
}

func TestGovernanceAccessRulesTabShells(t *testing.T) {
	repo := governanceFindingsFixture(t)
	controller := &testAllowlistController{names: nil}
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds, controller.MCPAllowlist(), controller)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)

	t.Run("default_mcp_tab_is_editable", func(t *testing.T) {
		body := getAuthed(t, handler, cookie, "/governance").Body.String()
		assertContainsAll(t, body, []string{
			`id="rules-panel-mcp"`,
			"Save local changes",
			`href="/governance?rules=skills"`,
			`href="/governance?rules=paths"`,
			`href="/governance?rules=prompts"`,
		})
		assertOmitsAll(t, body, []string{
			"No allow or block counts are shown",
			`id="rules-panel-skills"`,
			"Publish",
		})
	})

	for _, tc := range []struct {
		path, panel, schema string
	}{
		{"/governance?rules=skills", "rules-panel-skills", "governance.skills_allowlist"},
		{"/governance?rules=paths", "rules-panel-paths", "governance.path_rules"},
		{"/governance?rules=prompts", "rules-panel-prompts", "governance.prompt_keywords"},
		{"/governance?rules=UNKNOWN", "rules-panel-mcp", ""}, // clamps to mcp
	} {
		t.Run(strings.TrimPrefix(tc.path, "/governance"), func(t *testing.T) {
			body := getAuthed(t, handler, cookie, tc.path).Body.String()
			assertContainsAll(t, body, []string{`id="` + tc.panel + `"`})
			if tc.schema == "" {
				assertContainsAll(t, body, []string{"Save local changes"})
				assertOmitsAll(t, body, []string{"No allow or block counts are shown"})
				return
			}
			assertContainsAll(t, body, []string{
				tc.schema,
				"No allow or block counts are shown",
				"unavailable",
				"#148",
			})
			assertOmitsAll(t, body, []string{
				"Save local changes",
				`name="mcp_server"`,
				"Publish",
			})
		})
	}
}

func assertOmitsAll(t *testing.T, body string, forbidden []string) {
	t.Helper()
	for _, fragment := range forbidden {
		if strings.Contains(body, fragment) {
			t.Fatalf("unexpected %q in body: %q", fragment, body)
		}
	}
}

func TestGovernanceAllowlistSaveRoundTrip(t *testing.T) {
	repo := governanceFindingsFixture(t)
	controller := &testAllowlistController{names: []string{"configured-only"}}
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds, controller.MCPAllowlist(), controller)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)

	initial := getAuthed(t, handler, cookie, "/governance").Body.String()
	assertContainsAll(t, initial, []string{
		`value="configured-only" data-active-checked="true" checked`,
		"Configured; not currently observed",
		`value="rogue-tool" data-active-checked="false"`,
		"Observed",
		"Save local changes",
	})

	response := postAllowlist(t, handler, cookie, url.Values{
		"mcp_server": {"configured-only", "rogue-tool"},
	})
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/governance?saved=1" {
		t.Fatalf("save response = %d location %q", response.Code, response.Header().Get("Location"))
	}
	if got := controller.MCPAllowlist(); !equalStrings(got, []string{"configured-only", "rogue-tool"}) {
		t.Fatalf("saved allowlist = %#v", got)
	}

	saved := getAuthed(t, handler, cookie, "/governance?saved=1").Body.String()
	assertContainsAll(t, saved, []string{"MCP allowlist saved", "All identifiable observed MCP servers are on the allowlist."})

	response = postAllowlist(t, handler, cookie, url.Values{})
	if response.Code != http.StatusSeeOther {
		t.Fatalf("empty save response = %d", response.Code)
	}
	if got := controller.MCPAllowlist(); len(got) != 0 {
		t.Fatalf("empty selection did not clear allowlist: %#v", got)
	}
}

func TestGovernanceAllowlistRejectsUnknownServerWithoutSaving(t *testing.T) {
	repo := governanceFindingsFixture(t)
	controller := &testAllowlistController{names: []string{"configured-only"}}
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds, controller.MCPAllowlist(), controller)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)

	response := postAllowlist(t, handler, cookie, url.Values{"mcp_server": {"injected-server"}})
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown save response = %d, want 422", response.Code)
	}
	if !strings.Contains(response.Body.String(), "unknown server") {
		t.Fatalf("missing actionable validation error: %q", response.Body.String())
	}
	if got := controller.MCPAllowlist(); !equalStrings(got, []string{"configured-only"}) {
		t.Fatalf("rejected save changed allowlist: %#v", got)
	}
}

func TestGovernanceAllowlistWriteFailureKeepsActivePolicy(t *testing.T) {
	repo := governanceFindingsFixture(t)
	controller := &testAllowlistController{names: []string{"configured-only"}, saveErr: errors.New("disk full")}
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds, controller.MCPAllowlist(), controller)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)

	response := postAllowlist(t, handler, cookie, url.Values{"mcp_server": {"rogue-tool"}})
	body := response.Body.String()
	if response.Code != http.StatusInternalServerError || !strings.Contains(body, "active policy was not changed") {
		t.Fatalf("write failure response = %d: %q", response.Code, body)
	}
	if got := controller.MCPAllowlist(); !equalStrings(got, []string{"configured-only"}) {
		t.Fatalf("failed write changed allowlist: %#v", got)
	}
	// Draft keeps the submitted edit; active baseline stays on the saved policy so
	// dirty/reset/retry remain meaningful. Preview still evaluates the active allowlist.
	assertContainsAll(t, body, []string{
		`value="rogue-tool" data-active-checked="false" checked`,
		`value="configured-only" data-active-checked="true"`,
		"Finding",
		"An observed MCP server is outside the local allowlist",
	})
	if strings.Contains(body, `value="configured-only" data-active-checked="true" checked`) {
		t.Fatalf("failed save draft must not keep configured-only checked when omitted from submit: %q", body)
	}
}

func TestGovernanceSkillsAllowlistWriteFailureKeepsActivePolicy(t *testing.T) {
	repo := governanceFindingsFixture(t)
	now := time.Now().UTC()
	repo.events["gov-session-1"] = append(repo.events["gov-session-1"], canonical.Event{
		EventID: "skill-e1", SessionID: "gov-session-1", EventType: "skill_invocation",
		OccurredAt: now, ReceivedAt: now, Provider: "openai", Tool: "codex",
		ProviderExtensions: map[string]any{"skill_detection": "explicit", "skill": map[string]any{"name": "deploy"}},
	})
	controller := &testPolicyController{skills: []string{"configured-only"}, saveErr: errors.New("disk full")}
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds, controller.MCPAllowlist(), controller)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)

	response := postSkillsAllowlist(t, handler, cookie, url.Values{"skill": {"deploy"}})
	body := response.Body.String()
	if response.Code != http.StatusInternalServerError || !strings.Contains(body, "active policy was not changed") {
		t.Fatalf("write failure response = %d: %q", response.Code, body)
	}
	if got := controller.SkillsAllowlist(); !equalStrings(got, []string{"configured-only"}) {
		t.Fatalf("failed write changed skills allowlist: %#v", got)
	}
	assertContainsAll(t, body, []string{
		`name="skill" value="deploy" data-active-checked="false" checked`,
		`name="skill" value="configured-only" data-active-checked="true"`,
		"Finding",
		"Explicit provider identity",
	})
	if strings.Contains(body, `name="skill" value="configured-only" data-active-checked="true" checked`) {
		t.Fatalf("failed save draft must not keep configured-only checked when omitted from submit: %q", body)
	}
}

func TestGovernanceSkillsAllowlistSaveRoundTrip(t *testing.T) {
	repo := governanceFindingsFixture(t)
	now := time.Now().UTC()
	repo.events["gov-session-1"] = append(repo.events["gov-session-1"], canonical.Event{
		EventID: "skill-e1", SessionID: "gov-session-1", EventType: "skill_invocation",
		OccurredAt: now, ReceivedAt: now, Provider: "openai", Tool: "codex",
		ProviderExtensions: map[string]any{"skill_detection": "explicit", "skill": map[string]any{"name": "deploy"}},
	})
	controller := &testPolicyController{skills: []string{"configured-only"}}
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds, controller.MCPAllowlist(), controller)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)

	initial := getAuthed(t, handler, cookie, "/governance?rules=skills").Body.String()
	assertContainsAll(t, initial, []string{
		`id="skills-allowlist-form"`,
		`name="skill" value="deploy"`,
		"Explicit provider identity",
		"configured-only",
	})

	response := postSkillsAllowlist(t, handler, cookie, url.Values{"skill": {"deploy"}})
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/governance?rules=skills&saved=1" {
		t.Fatalf("save response = %d location %q", response.Code, response.Header().Get("Location"))
	}
	if got := controller.SkillsAllowlist(); !equalStrings(got, []string{"deploy"}) {
		t.Fatalf("saved skills allowlist = %#v", got)
	}
	saved := getAuthed(t, handler, cookie, "/governance?rules=skills&saved=1").Body.String()
	assertContainsAll(t, saved, []string{"Skills allowlist saved", "Explicit provider identity"})
}

func postAllowlist(t *testing.T, handler http.Handler, cookie *http.Cookie, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return postGovernanceAllowlist(t, handler, cookie, "/governance/mcp-allowlist", values, "http://example.com", "")
}

func postSkillsAllowlist(t *testing.T, handler http.Handler, cookie *http.Cookie, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return postGovernanceAllowlist(t, handler, cookie, "/governance/skills-allowlist", values, "http://example.com", "")
}

func postGovernanceAllowlist(t *testing.T, handler http.Handler, cookie *http.Cookie, path string, values url.Values, origin, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestGovernanceAllowlistRejectsCrossOriginCookieMutation(t *testing.T) {
	repo := governanceFindingsFixture(t)
	now := time.Now().UTC()
	repo.events["gov-session-1"] = append(repo.events["gov-session-1"], canonical.Event{
		EventID: "skill-e1", SessionID: "gov-session-1", EventType: "skill_invocation",
		OccurredAt: now, ReceivedAt: now, Provider: "openai", Tool: "codex",
		ProviderExtensions: map[string]any{"skill_detection": "explicit", "skill": map[string]any{"name": "deploy"}},
	})
	controller := &testPolicyController{mcp: testAllowlistController{names: []string{"configured-only"}}, skills: []string{"configured-only"}}
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds, controller.MCPAllowlist(), controller)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)

	tests := []struct {
		name      string
		path      string
		values    url.Values
		unchanged func() []string
	}{
		{"MCP", "/governance/mcp-allowlist", url.Values{"mcp_server": {"rogue-tool"}}, controller.MCPAllowlist},
		{"Skills", "/governance/skills-allowlist", url.Values{"skill": {"deploy"}}, controller.SkillsAllowlist},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := postGovernanceAllowlist(t, handler, cookie, test.path, test.values, "https://attacker.invalid", "")
			if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "Cross-origin") {
				t.Fatalf("cross-origin response = %d: %q", response.Code, response.Body.String())
			}
			if got := test.unchanged(); !equalStrings(got, []string{"configured-only"}) {
				t.Fatalf("cross-origin request changed policy: %#v", got)
			}
			response = postGovernanceAllowlist(t, handler, nil, test.path, test.values, "", "test-token")
			if response.Code != http.StatusSeeOther {
				t.Fatalf("bearer-token response = %d: %q", response.Code, response.Body.String())
			}
		})
	}
}

func equalStrings(left, right []string) bool {
	return strings.Join(left, "\x00") == strings.Join(right, "\x00")
}

func governanceFindingsFixture(t *testing.T) *fullStub {
	t.Helper()
	now := time.Now().UTC()
	return &fullStub{
		sessions: []canonical.Session{{
			SessionID: "gov-session-1",
			Provider:  "anthropic",
			Tool:      "claude-code",
			State:     "completed",
			StartedAt: now,
		}},
		events: map[string][]canonical.Event{
			"gov-session-1": {{
				EventID:    "gov-e1",
				SessionID:  "gov-session-1",
				EventType:  "api_request",
				OccurredAt: now,
				ReceivedAt: now,
				Provider:   "anthropic",
				Tool:       "claude-code",
				Attributes: map[string]any{
					"file_path": "/home/dev/app/.env",
					"command":   "cat /home/dev/app/.env",
				},
			}, {
				EventID:    "gov-e2",
				SessionID:  "gov-session-1",
				EventType:  "mcp_server_connection",
				OccurredAt: now,
				ReceivedAt: now,
				Provider:   "anthropic",
				Tool:       "claude-code",
				ProviderExtensions: map[string]any{
					"event": map[string]any{
						"server_name": "rogue-tool",
						"status":      "connected",
					},
				},
			}},
		},
	}
}

func renderGovernance(t *testing.T, repo *fullStub, allowlist []string) string {
	t.Helper()
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds, allowlist)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)
	return getAuthed(t, handler, cookie, "/governance").Body.String()
}

func assertContainsAll(t *testing.T, body string, want []string) {
	t.Helper()
	for _, fragment := range want {
		if !strings.Contains(body, fragment) {
			t.Fatalf("missing %q in body: %q", fragment, body)
		}
	}
}

func TestCorpusPagesAvoidListEvents(t *testing.T) {
	now := time.Now().UTC()
	repo := &fullStub{
		sessions: []canonical.Session{{
			SessionID: "spy-session", Provider: "anthropic", Tool: "claude-code", State: "completed", StartedAt: now,
		}},
		events: map[string][]canonical.Event{
			"spy-session": {{
				EventID: "spy-mcp", EventType: "mcp_server_connection", SessionID: "spy-session",
				OccurredAt: now, ReceivedAt: now, Provider: "anthropic", Tool: "claude-code",
				ProviderExtensions: map[string]any{"event": map[string]any{"server_name": "filesystem", "status": "connected"}},
			}},
		},
	}
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)
	for _, path := range []string{"/", "/insights", "/models", "/governance", "/integrations"} {
		repo.listEventsCalls = 0
		repo.insightCalls = 0
		rec := getAuthed(t, handler, cookie, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, rec.Code)
		}
		if repo.listEventsCalls != 0 {
			t.Fatalf("%s ListEvents calls = %d, want 0", path, repo.listEventsCalls)
		}
		if path != "/integrations" && repo.insightCalls == 0 {
			t.Fatalf("%s ListInsightSourceEvents calls = 0, want at least 1", path)
		}
	}
	repo.listEventsCalls = 0
	_ = getAuthed(t, handler, cookie, "/sessions/spy-session")
	if repo.listEventsCalls == 0 {
		t.Fatal("session detail must still read retained events via ListEvents")
	}
}
