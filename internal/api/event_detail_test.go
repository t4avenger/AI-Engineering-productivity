package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/wayne/telemetryiq/internal/inspector"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
)

func TestSessionEventAPIContract(t *testing.T) {
	repo := sessionTestRepository(t)
	at := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	long := strings.Repeat("x", inspector.DefaultMaxStringRunes+5)
	events := []canonical.Event{
		{
			SchemaVersion: "0.1.0", EventID: "evt-selected", EventType: "tool_result",
			OccurredAt: at, ReceivedAt: at, Provider: "openai", Tool: "codex",
			SourceSchema: "otel", SourceVersion: "test", ActorID: "unavailable", DeviceID: "unavailable",
			SessionID: "session-newest", PrivacyLevel: "operational",
			Attributes: map[string]any{
				"operation_id": "op-shared",
				"outcome":      "success",
				"duration_ms":  "12",
				"note":         long,
			},
			ProviderExtensions: map[string]any{"event": map[string]any{"body": long}},
		},
		{
			SchemaVersion: "0.1.0", EventID: "evt-peer", EventType: "tool_result",
			OccurredAt: at.Add(time.Second), ReceivedAt: at.Add(time.Second),
			Provider: "openai", Tool: "codex", SourceSchema: "otel", SourceVersion: "test",
			ActorID: "unavailable", DeviceID: "unavailable", SessionID: "session-newest", PrivacyLevel: "operational",
			Attributes:         map[string]any{"operation_id": "op-shared"},
			ProviderExtensions: map[string]any{},
		},
		{
			SchemaVersion: "0.1.0", EventID: "evt-other-session", EventType: "user_prompt",
			OccurredAt: at, ReceivedAt: at, Provider: "openai", Tool: "codex",
			SourceSchema: "otel", SourceVersion: "test", ActorID: "unavailable", DeviceID: "unavailable",
			SessionID: "session-middle", PrivacyLevel: "operational",
			Attributes:         map[string]any{"prompt": "foreign"},
			ProviderExtensions: map[string]any{},
		},
	}
	if err := repo.SaveEvents(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(slog.Default(), repo))
	t.Cleanup(server.Close)

	detail := getEventDetail(t, server.URL+"/api/v1/sessions/session-newest/events/evt-selected")
	if detail.Data.EventID != "evt-selected" || detail.Data.Outcome == nil || *detail.Data.Outcome != "success" {
		t.Fatalf("detail = %#v", detail.Data)
	}
	note, _ := detail.Data.Attributes["note"].(string)
	if utf8.RuneCountInString(note) != inspector.DefaultMaxStringRunes+1 || !strings.HasSuffix(note, "…") {
		t.Fatalf("expected truncated note, got %q", note)
	}
	if !detail.Data.Truncation.Applied || !detail.Data.Truncation.ExpandAvailable {
		t.Fatalf("truncation = %#v", detail.Data.Truncation)
	}
	if detail.Data.Relationships.Availability.RelatedEvents != inspector.AvailabilityObserved {
		t.Fatalf("relationships = %#v", detail.Data.Relationships)
	}
	foundPeer := false
	for _, related := range detail.Data.Relationships.RelatedEvents {
		if related.EventID == "evt-peer" {
			foundPeer = true
		}
	}
	if !foundPeer {
		t.Fatalf("missing related peer: %#v", detail.Data.Relationships.RelatedEvents)
	}

	expanded := getEventDetail(t, server.URL+"/api/v1/sessions/session-newest/events/evt-selected?expand=1")
	fullNote, _ := expanded.Data.Attributes["note"].(string)
	if fullNote != long || expanded.Data.Truncation.Applied {
		t.Fatalf("expand failed: note=%q truncation=%#v", fullNote, expanded.Data.Truncation)
	}

	assertEventDetailErrors(t, server.URL)
}

func assertEventDetailErrors(t *testing.T, baseURL string) {
	t.Helper()
	for _, test := range []struct {
		path   string
		status int
		code   string
	}{
		{path: "/api/v1/sessions/missing/events/evt-selected", status: http.StatusNotFound, code: "session_not_found"},
		{path: "/api/v1/sessions/session-newest/events/missing", status: http.StatusNotFound, code: "event_not_found"},
		{path: "/api/v1/sessions/session-newest/events/evt-other-session", status: http.StatusNotFound, code: "event_not_found"},
	} {
		response, err := http.Get(baseURL + test.path)
		if err != nil {
			t.Fatal(err)
		}
		var body sessionErrorResponse
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			_ = response.Body.Close()
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != test.status || body.Error.Code != test.code {
			t.Fatalf("%s status=%d code=%q want %d/%s", test.path, response.StatusCode, body.Error.Code, test.status, test.code)
		}
	}
}

func TestSessionEventAPIFailureResponses(t *testing.T) {
	for _, test := range []struct {
		name   string
		api    sessionAPI
		status int
	}{
		{name: "storage unavailable", api: sessionAPI{}, status: http.StatusServiceUnavailable},
		{
			name: "session lookup failure",
			api: sessionAPI{
				sessions:    conversationSessionStub{err: errors.New("session failed")},
				eventReader: conversationEventReaderStub{},
			},
			status: http.StatusInternalServerError,
		},
		{
			name: "event query failure",
			api: sessionAPI{
				sessions:    conversationSessionStub{found: true},
				eventReader: conversationEventReaderStub{err: errors.New("events failed")},
			},
			status: http.StatusInternalServerError,
		},
		{
			name: "event missing",
			api: sessionAPI{
				sessions:    conversationSessionStub{found: true},
				eventReader: conversationEventReaderStub{},
			},
			status: http.StatusNotFound,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/session/events/evt", nil)
			request.SetPathValue("id", "session")
			request.SetPathValue("event_id", "evt")
			response := httptest.NewRecorder()
			test.api.event(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
}

func getEventDetail(t *testing.T, address string) eventDetailResponse {
	t.Helper()
	response, err := http.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("event detail status = %d", response.StatusCode)
	}
	var body eventDetailResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

var _ storage.EventReader = conversationEventReaderStub{}
