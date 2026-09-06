package ui_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/cost"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/privacy"
	"github.com/wayne/telemetryiq/internal/storage"
	"github.com/wayne/telemetryiq/internal/ui"
)

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

func (s *fullStub) EventProvenance(context.Context, string) ([]privacy.Provenance, bool, error) {
	return nil, false, nil
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
	server, err := ui.New("test-token", repo)
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
		{"/sessions/s1", "Availability"},
		{"/sessions/s1", "model_interaction"},
		{"/insights", "MCP inventory"},
		{"/insights", "Skill usage"},
		{"/insights", "Model performance"},
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
	server, err := ui.New("test-token", repo)
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
	if strings.Contains(strings.ToLower(body), "calculated amount") {
		t.Fatal("home must not headline costs")
	}
}

func TestUnavailableNotZeroOnCosts(t *testing.T) {
	server, err := ui.New("test-token", &fullStub{})
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
	server, err := ui.New("test-token", repo)
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
	server, err := ui.New("test-token", &fullStub{})
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
