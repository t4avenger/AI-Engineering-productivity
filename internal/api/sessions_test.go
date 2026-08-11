package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

func TestSessionAPIDeletesAnExistingSession(t *testing.T) {
	repo := sessionTestRepository(t)
	server := httptest.NewServer(NewHandler(slog.Default(), repo))
	t.Cleanup(server.Close)

	request, err := http.NewRequest(http.MethodDelete, server.URL+"/api/v1/sessions/session-middle", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", response.StatusCode)
	}

	response, err = http.Get(server.URL + "/api/v1/sessions/session-middle")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted session status = %d", response.StatusCode)
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

func TestSessionEventTimelineAndProvenanceAPI(t *testing.T) {
	server := timelineTestServer(t)
	firstPage := timelinePage(t, server.URL+"/api/v1/sessions/timeline-session/events?limit=1")
	assertFirstTimelinePage(t, firstPage)
	assertSecondTimelinePage(t, server.URL, firstPage)
	assertTimelineProvenance(t, server.URL)
}

func timelineTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	repo := sessionTestRepository(t)
	first := sessionTestEvent(t, "timeline-first", "timeline-session", "codex", "active", "2026-01-03T09:00:00Z", "model-a")
	first.Attributes["input_token_count"] = "100"
	first.ProviderExtensions = map[string]any{"prompt": "synthetic-private-prompt"}
	second := sessionTestEvent(t, "timeline-second", "timeline-session", "codex", "completed", "2026-01-03T10:00:00Z", "model-a")
	if err := repo.SaveEvents(context.Background(), []canonical.Event{second, first}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(slog.Default(), repo))
	t.Cleanup(server.Close)
	return server
}

func timelinePage(t *testing.T, address string) eventListResponse {
	t.Helper()
	response, err := http.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("timeline status = %d", response.StatusCode)
	}
	var page eventListResponse
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	return page
}

func assertFirstTimelinePage(t *testing.T, page eventListResponse) {
	t.Helper()
	if len(page.Data) != 1 || page.Data[0].EventID != "timeline-first" || page.Pagination.NextCursor == nil {
		t.Fatalf("first timeline page = %#v", page)
	}
	serialized, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), "synthetic-private-prompt") || strings.Contains(string(serialized), "provider_extensions") {
		t.Fatalf("timeline leaked non-public data: %s", serialized)
	}
}

func assertSecondTimelinePage(t *testing.T, baseURL string, first eventListResponse) {
	t.Helper()
	page := timelinePage(t, baseURL+"/api/v1/sessions/timeline-session/events?limit=1&cursor="+url.QueryEscape(*first.Pagination.NextCursor))
	if len(page.Data) != 1 || page.Data[0].EventID != "timeline-second" || page.Pagination.NextCursor != nil {
		t.Fatalf("second timeline page = %#v", page)
	}
}

func assertTimelineProvenance(t *testing.T, baseURL string) {
	t.Helper()
	response, err := http.Get(baseURL + "/api/v1/events/timeline-first/provenance")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var provenance eventProvenanceResponse
	if err := json.NewDecoder(response.Body).Decode(&provenance); err != nil {
		t.Fatal(err)
	}
	for _, entry := range provenance.Data {
		if entry.Action == privacy.ActionRemoved {
			return
		}
	}
	t.Fatalf("provenance = %#v", provenance)
}
