package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
	"github.com/wayne/telemetryiq/internal/storage/sqlite"
)

func TestCursorTokenUsageMetricsPersistThroughMetricsReceiver(t *testing.T) {
	repository, server := newCursorOTLPTestServer(t)
	postCursorOTLPAccepted(t, server, "/v1/metrics", "cursor-otel-0.1.0-token-usage-metrics.json")

	sessions := listCursorSessions(t, repository, 20)
	if len(sessions) != 4 {
		t.Fatalf("sessions = %#v, want one per token category (content-derived session ids)", sessions)
	}
	events := eventsForSessions(t, repository, sessions)
	if len(events) != 4 {
		t.Fatalf("events = %#v, want 4 token categories", events)
	}
	encoded := marshalJSON(t, events)
	assertNoRawIdentifiers(t, []string{"424242", "434343", "cursor.team.id", "cursor.user.id"}, encoded)
	assertCursorTokenMetricEvents(t, events, encoded)
}

func TestCursorAPIRequestLogsPersistThroughLogsReceiver(t *testing.T) {
	repository, server := newCursorOTLPTestServer(t)
	postCursorOTLPAccepted(t, server, "/v1/logs", "cursor-otel-0.1.0-api-request-logs.json")

	sessions := listCursorSessions(t, repository, 20)
	if len(sessions) != 1 || sessions[0].SessionID != "cursor:00000000-0000-4000-8000-0000000000c1" {
		t.Fatalf("sessions = %#v, want one cursor conversation session", sessions)
	}
	events, err := repository.ListEvents(context.Background(), storage.EventFilter{SessionID: sessions[0].SessionID, Limit: 10})
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %#v, %v, want 1 api.request", events, err)
	}
	if events[0].EventType != "cursor.api.request" {
		t.Fatalf("event type = %q", events[0].EventType)
	}
	encoded := marshalJSON(t, events)
	assertNoRawIdentifiers(t, []string{"424242", "434343", "cursor.team.id", "cursor.user.id", "tiq-probe"}, encoded)
	if events[0].Attributes["input_token_count"] != float64(100) && events[0].Attributes["input_token_count"] != int64(100) {
		t.Fatalf("input_token_count = %#v", events[0].Attributes["input_token_count"])
	}
}

func newCursorOTLPTestServer(t *testing.T) (storage.Repository, *httptest.Server) {
	t.Helper()
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	server := httptest.NewServer(NewPersistentHandler(slog.Default(), repository))
	t.Cleanup(server.Close)
	return repository, server
}

func postCursorOTLPAccepted(t *testing.T, server *httptest.Server, path, fixture string) {
	t.Helper()
	response := postOTLPToPath(t, server.URL, path, metricsFixturePayloadBytes(t, fixture), "application/json")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("%s status = %d", path, response.StatusCode)
	}
	closeBody(t, response)
}

func listCursorSessions(t *testing.T, repository storage.Repository, limit int) []canonical.Session {
	t.Helper()
	sessions, err := repository.ListSessions(context.Background(), storage.SessionFilter{Limit: limit, Tool: "cursor"})
	if err != nil {
		t.Fatal(err)
	}
	return sessions
}

func eventsForSessions(t *testing.T, repository storage.Repository, sessions []canonical.Session) []canonical.Event {
	t.Helper()
	var events []canonical.Event
	for _, session := range sessions {
		listed, err := repository.ListEvents(context.Background(), storage.EventFilter{SessionID: session.SessionID, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, listed...)
	}
	return events
}

func assertCursorTokenMetricEvents(t *testing.T, events []canonical.Event, encoded []byte) {
	t.Helper()
	var sawInput bool
	for _, event := range events {
		if event.EventType != "cursor.token.usage" {
			t.Fatalf("event type = %q", event.EventType)
		}
		if event.Tool != "cursor" {
			t.Fatalf("tool = %q", event.Tool)
		}
		if event.Attributes["input_token_count"] == float64(128) || event.Attributes["input_token_count"] == int64(128) {
			sawInput = true
		}
	}
	if !sawInput {
		t.Fatalf("expected a persisted input token count of 128, got %s", encoded)
	}
}
