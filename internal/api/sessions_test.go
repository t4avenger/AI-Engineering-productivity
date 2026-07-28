package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/privacy"
	"github.com/wayne/telemetryiq/internal/storage/sqlite"
)

func TestSessionAPIContract(t *testing.T) {
	repo := sessionTestRepository(t)
	server := httptest.NewServer(NewHandler(slog.Default(), repo))
	t.Cleanup(server.Close)

	first := getSessionList(t, server.URL+"/api/v1/sessions?limit=1")
	if len(first.Data) != 1 || first.Data[0].SessionID != "session-newest" || first.Pagination.NextCursor == nil {
		t.Fatalf("first page = %#v", first)
	}
	second := getSessionList(t, server.URL+"/api/v1/sessions?limit=1&cursor="+url.QueryEscape(*first.Pagination.NextCursor))
	if len(second.Data) != 1 || second.Data[0].SessionID != "session-middle" {
		t.Fatalf("second page = %#v", second)
	}

	filtered := getSessionList(t, server.URL+"/api/v1/sessions?tool=codex&model=model-a&outcome=completed&started_after=2026-01-02T09:30:00Z")
	if len(filtered.Data) != 1 || filtered.Data[0].SessionID != "session-newest" {
		t.Fatalf("filtered response = %#v", filtered)
	}

	response, err := http.Get(server.URL + "/api/v1/sessions/session-middle")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d", response.StatusCode)
	}
	var detail sessionDetailResponse
	if err := json.NewDecoder(response.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if detail.Data.SessionID != "session-middle" || detail.Data.State != "failed" {
		t.Fatalf("detail = %#v", detail)
	}
}

func TestSessionAPIRejectsInvalidQueriesAndMissingSessions(t *testing.T) {
	repo := sessionTestRepository(t)
	server := httptest.NewServer(NewHandler(slog.Default(), repo))
	t.Cleanup(server.Close)

	for _, path := range []string{"/api/v1/sessions?limit=0", "/api/v1/sessions?started_after=nope", "/api/v1/sessions?cursor=not-a-cursor"} {
		assertInvalidSessionQuery(t, server.URL+path)
	}

	response, err := http.Get(server.URL + "/api/v1/sessions/missing")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("missing session status = %d", response.StatusCode)
	}
}

func assertInvalidSessionQuery(t *testing.T, rawURL string) {
	t.Helper()
	response, err := http.Get(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("%s status = %d", rawURL, response.StatusCode)
	}
	var body sessionErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "invalid_query" {
		t.Fatalf("%s error = %#v", rawURL, body.Error)
	}
}

func sessionTestRepository(t *testing.T) *sqlite.Repository {
	t.Helper()
	sanitizer, err := privacy.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo, err := sqlite.Open(":memory:", sanitizer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	events := []canonical.Event{
		sessionTestEvent(t, "event-oldest", "session-oldest", "claude", "completed", "2026-01-02T09:00:00Z", "model-c"),
		sessionTestEvent(t, "event-middle", "session-middle", "codex", "failed", "2026-01-02T10:00:00Z", "model-b"),
		sessionTestEvent(t, "event-newest", "session-newest", "codex", "completed", "2026-01-02T11:00:00Z", "model-a"),
	}
	if err := repo.SaveEvents(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	return repo
}

func sessionTestEvent(t *testing.T, eventID, sessionID, tool, outcome, occurredAt, model string) canonical.Event {
	t.Helper()
	at, err := time.Parse(time.RFC3339, occurredAt)
	if err != nil {
		t.Fatal(err)
	}
	return canonical.Event{SchemaVersion: "0.1.0", EventID: eventID, EventType: "session." + outcome, OccurredAt: at, ReceivedAt: at, Provider: "openai", Tool: tool, SourceSchema: "otel", SourceVersion: "test", ActorID: "unavailable", DeviceID: "unavailable", SessionID: sessionID, PrivacyLevel: "operational", Attributes: map[string]any{"model": model}, ProviderExtensions: map[string]any{}}
}

func getSessionList(t *testing.T, rawURL string) sessionListResponse {
	t.Helper()
	response, err := http.Get(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", response.StatusCode)
	}
	var list sessionListResponse
	if err := json.NewDecoder(response.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	return list
}
