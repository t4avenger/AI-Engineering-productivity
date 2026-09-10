package ui_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/cost"
	"github.com/wayne/telemetryiq/internal/insights"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
	"github.com/wayne/telemetryiq/internal/ui"
)

var defaultContextWasteThresholds = insights.ContextWasteThresholds{
	CachedContextRatioThreshold: 0.75,
	InputTokenGrowthThreshold:   2.0,
}

type fullStub struct {
	sessions []canonical.Session
	events   map[string][]canonical.Event
	costs    []cost.Record
	deleted  []string
	cleared  bool
}

func (s *fullStub) Session(_ context.Context, id string) (canonical.Session, bool, error) {
	for _, session := range s.sessions {
		if session.SessionID == id {
			return session, true, nil
		}
	}
	return canonical.Session{}, false, nil
}

func (s *fullStub) ListSessions(_ context.Context, _ storage.SessionFilter) ([]canonical.Session, error) {
	return s.sessions, nil
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
	return append([]canonical.Event(nil), s.events[filter.SessionID]...), nil
}

func (s *fullStub) ListCostRecords(context.Context, string) ([]cost.Record, error) {
	return s.costs, nil
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
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds)
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
				"model": "gpt-test",
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
		costs: []cost.Record{{
			Currency: "USD",
			Status:   "calculated",
			AmountMicrousd: func() *int64 {
				v := int64(1500000)
				return &v
			}(),
		}},
	}
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds)
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
		{"/sessions/s1", "Model interaction"},
		{"/sessions/s1", "Input tokens"},
		{"/sessions/s1", "2 tokens"},
		{"/insights", "MCP inventory"},
		{"/insights", "Skill usage"},
		{"/insights", "Model performance"},
		{"/insights", "1234 ms"},
		{"/insights", "Context pressure"},
		{"/integrations", "codex"},
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

func TestUnlockAndHome(t *testing.T) {
	repo := &fullStub{sessions: []canonical.Session{{
		SessionID: "s1",
		Provider:  "openai",
		Tool:      "codex",
		State:     "completed",
		StartedAt: time.Now().UTC(),
	}}}
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds)
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
	// Issue #76 adds a global "Costs" nav link (last), so the link itself now
	// legitimately appears on Home. "Never on Home" is about cost *figures*:
	// Home must still not surface any calculated/estimated cost content.
	for _, forbidden := range []string{"Calculated amount", "Cost estimates", "Secondary estimates"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("home must not surface cost figures %q", forbidden)
		}
	}
}

// TestHomeShellControls covers the issue #76 app-shell affordances on an
// authenticated page: the Costs nav link ordered last, a visible logout
// control, and an honest daemon health badge.
func TestHomeShellControls(t *testing.T) {
	repo := &fullStub{sessions: []canonical.Session{{
		SessionID: "s1", Provider: "openai", Tool: "codex",
		State: "completed", StartedAt: time.Now().UTC(),
	}}}
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)
	body := getAuthed(t, handler, cookie, "/").Body.String()

	// Costs is present and ordered last (after Privacy). Require both indices
	// to be found so a missing Privacy link cannot pass the ordering check.
	privacy := strings.Index(body, `href="/privacy"`)
	costs := strings.Index(body, `href="/costs"`)
	if privacy < 0 || costs < 0 {
		t.Fatalf("home nav must link both Privacy and Costs: privacy=%d costs=%d", privacy, costs)
	}
	if privacy > costs {
		t.Fatalf("Costs nav link must come last, after Privacy")
	}
	if !strings.Contains(body, `action="/logout"`) {
		t.Fatalf("authenticated home must show a logout control: %q", body)
	}
	if !strings.Contains(body, "Daemon: Healthy") || !strings.Contains(body, `class="health ok"`) {
		t.Fatalf("home must show honest healthy daemon badge: %q", body)
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
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds)
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
	server, err := ui.New("test-token", &fullStub{}, defaultContextWasteThresholds)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)
	body := getAuthed(t, handler, cookie, "/sessions").Body.String()
	for _, want := range []string{"No retained sessions yet.", "Point Codex or Claude Code OTLP logs", "supported local Cursor Agent ingest path"} {
		if !strings.Contains(body, want) {
			t.Fatalf("sessions empty state missing %q in body: %q", want, body)
		}
	}
}

func TestHealthDegradesWhenStorageFails(t *testing.T) {
	server, err := ui.New("test-token", errStub{}, defaultContextWasteThresholds)
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
	server, err := ui.New("test-token", &fullStub{}, defaultContextWasteThresholds)
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

func TestInsightsMCPInvocationCountUnavailableDoesNotRenderZero(t *testing.T) {
	now := time.Now().UTC()
	repo := &fullStub{
		sessions: []canonical.Session{syntheticSession("mcp-unavailable-session", now)},
		events: map[string][]canonical.Event{
			"mcp-unavailable-session": {{
				EventID:    "connection-only",
				EventType:  "mcp_server_connection",
				SessionID:  "mcp-unavailable-session",
				OccurredAt: now,
				ReceivedAt: now,
				Provider:   "anthropic",
				Tool:       "claude-code",
				ProviderExtensions: map[string]any{
					"event": map[string]any{
						"server_name": "connection-only-mcp",
					},
				},
			}},
		},
	}
	body := renderInsights(t, repo)
	rowStart := strings.Index(body, "connection-only-mcp")
	if rowStart == -1 {
		t.Fatalf("MCP row missing: %q", body)
	}
	rowEnd := strings.Index(body[rowStart:], "</tr>")
	if rowEnd == -1 {
		t.Fatalf("MCP row did not close: %q", body[rowStart:])
	}
	row := body[rowStart : rowStart+rowEnd]
	if strings.Contains(row, "0 invocations") {
		t.Fatalf("usage-unavailable MCP row must not imply measured zero invocations: %q", row)
	}
	if !strings.Contains(row, "Usage not available") {
		t.Fatalf("usage-unavailable MCP row should render unavailable badge in invocations cell: %q", row)
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
		"Evidence notes",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("insights page missing %q in body: %q", want, body)
		}
	}
	if strings.Contains(body, "%!f(*float64") {
		t.Fatalf("insights page should not expose pointer formatter output: %q", body)
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
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds)
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
	server, err := ui.New("test-token", &fullStub{}, defaultContextWasteThresholds)
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
	server, err := ui.New("test-token", repo, defaultContextWasteThresholds)
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
	server, err := ui.New("test-token", &fullStub{}, defaultContextWasteThresholds)
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
