package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
)

func TestSessionConversationAPIContract(t *testing.T) {
	repo := sessionTestRepository(t)
	at := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	events := []canonical.Event{
		conversationEvent("conversation-user", "user_prompt", "session-newest", at, map[string]any{"prompt": "synthetic user text"}),
		conversationEvent("conversation-redacted", "assistant_response", "session-newest", at.Add(time.Second), map[string]any{"response": "<REDACTED>"}),
		conversationEvent("conversation-length", "user_prompt", "session-newest", at.Add(2*time.Second), map[string]any{"prompt_length": int64(12)}),
		conversationEvent("conversation-body", "api_response_body", "session-newest", at.Add(3*time.Second), map[string]any{"body": "raw API evidence"}),
		conversationEvent("conversation-other", "user_prompt", "session-middle", at, map[string]any{"prompt": "other session"}),
	}
	if err := repo.SaveEvents(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(slog.Default(), repo))
	t.Cleanup(server.Close)

	first := getConversationPage(t, server.URL+"/api/v1/sessions/session-newest/conversation?limit=2")
	assertFirstConversationPage(t, first)
	second := getConversationPage(t, server.URL+"/api/v1/sessions/session-newest/conversation?limit=2&cursor="+url.QueryEscape(*first.Pagination.NextCursor))
	assertSecondConversationPage(t, second)
	assertConversationErrors(t, server.URL)
}

func assertFirstConversationPage(t *testing.T, page conversationResponse) {
	t.Helper()
	if len(page.Data) != 2 || page.Data[0].EventID != "conversation-user" || page.Data[0].Text == nil || *page.Data[0].Text != "synthetic user text" || page.Data[0].Role != "user" || page.Data[1].ContentAvailability != "provider_redacted" || page.Pagination.NextCursor == nil {
		t.Fatalf("first page = %#v", page)
	}
}

func assertSecondConversationPage(t *testing.T, page conversationResponse) {
	t.Helper()
	if len(page.Data) != 2 || page.Data[0].ContentAvailability != "length_only" || page.Data[0].Text != nil || page.Data[1].Role != "unknown" || page.Data[1].Text == nil || *page.Data[1].Text != "raw API evidence" || page.Pagination.NextCursor != nil {
		t.Fatalf("second page = %#v", page)
	}
}

func assertConversationErrors(t *testing.T, baseURL string) {
	t.Helper()
	for _, test := range []struct {
		path   string
		status int
	}{
		{path: "/api/v1/sessions/missing/conversation", status: http.StatusNotFound},
		{path: "/api/v1/sessions/session-newest/conversation?cursor=not-a-cursor", status: http.StatusBadRequest},
	} {
		response, err := http.Get(baseURL + test.path)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != test.status {
			_ = response.Body.Close()
			t.Fatalf("%s status = %d, want %d", test.path, response.StatusCode, test.status)
		}
		_ = response.Body.Close()
	}
}

func TestSessionConversationAPIFailureResponses(t *testing.T) {
	for _, test := range []struct {
		name   string
		api    sessionAPI
		status int
	}{
		{name: "storage unavailable", api: sessionAPI{}, status: http.StatusServiceUnavailable},
		{
			name:   "session lookup failure",
			api:    sessionAPI{sessions: conversationSessionStub{err: errors.New("session failed")}, eventReader: conversationEventReaderStub{}},
			status: http.StatusInternalServerError,
		},
		{
			name:   "event query failure",
			api:    sessionAPI{sessions: conversationSessionStub{found: true}, eventReader: conversationEventReaderStub{err: errors.New("events failed")}},
			status: http.StatusInternalServerError,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/conversation-session/conversation", nil)
			request.SetPathValue("id", "conversation-session")
			response := httptest.NewRecorder()
			test.api.conversation(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
}

func TestSessionEventsReportsEventReaderFailure(t *testing.T) {
	api := sessionAPI{
		sessions:    conversationSessionStub{found: true},
		eventReader: conversationEventReaderStub{err: errors.New("events failed")},
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/conversation-session/events", nil)
	request.SetPathValue("id", "conversation-session")
	response := httptest.NewRecorder()
	api.events(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
}

func TestParseEventListQueryRejectsInvalidStructuredCursors(t *testing.T) {
	for _, cursor := range []string{
		base64.RawURLEncoding.EncodeToString([]byte(`{}`)),
		base64.RawURLEncoding.EncodeToString([]byte(`{"event_id":"event","occurred_at":"not-a-time"}`)),
	} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/session/events?cursor="+url.QueryEscape(cursor), nil)
		if _, _, err := parseEventListQuery(request); err == nil {
			t.Fatalf("cursor %q unexpectedly parsed", cursor)
		}
	}
}

func TestOptionalStringSupportsPublicNumericValuesWithoutFabrication(t *testing.T) {
	for _, test := range []struct {
		value any
		want  string
	}{
		{value: float32(1.5), want: "1.5"},
		{value: uint64(7), want: "7"},
		{value: true, want: ""},
	} {
		got := optionalString(test.value)
		if test.want == "" {
			if got != nil {
				t.Fatalf("optionalString(%#v) = %q, want nil", test.value, *got)
			}
			continue
		}
		if got == nil || *got != test.want {
			t.Fatalf("optionalString(%#v) = %#v, want %q", test.value, got, test.want)
		}
	}
}

func TestCostAPIReportsUnavailableReader(t *testing.T) {
	response := httptest.NewRecorder()
	(sessionAPI{}).costs(response, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/session/costs", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

type conversationSessionStub struct {
	found bool
	err   error
}

func (s conversationSessionStub) Session(context.Context, string) (canonical.Session, bool, error) {
	return canonical.Session{}, s.found, s.err
}

func (conversationSessionStub) ListSessions(context.Context, storage.SessionFilter) ([]canonical.Session, error) {
	return nil, nil
}

type conversationEventReaderStub struct{ err error }

func (s conversationEventReaderStub) ListEvents(context.Context, storage.EventFilter) ([]canonical.Event, error) {
	return nil, s.err
}

func conversationEvent(id, eventType, sessionID string, at time.Time, echo map[string]any) canonical.Event {
	return canonical.Event{
		SchemaVersion: "0.1.0", EventID: id, EventType: eventType, OccurredAt: at, ReceivedAt: at,
		Provider: "anthropic", Tool: "claude-code", SourceSchema: "otel", SourceVersion: "2.1.270",
		ActorID: "unavailable", DeviceID: "unavailable", SessionID: sessionID, PrivacyLevel: "operational",
		Attributes: map[string]any{}, ProviderExtensions: map[string]any{"event": echo},
	}
}

func getConversationPage(t *testing.T, address string) conversationResponse {
	t.Helper()
	response, err := http.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("conversation status = %d", response.StatusCode)
	}
	var page conversationResponse
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	return page
}
