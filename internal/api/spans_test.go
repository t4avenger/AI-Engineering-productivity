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
)

func TestSessionSpansAPIContract(t *testing.T) {
	repo := sessionTestRepository(t)
	events := []canonical.Event{
		apiSpanEvent("span-root", "session-newest", "trace", "root", "", "1000000000", "2000000000"),
		apiSpanEvent("span-child", "session-newest", "trace", "child", "root", "2000000000", "3000000000"),
		apiSpanEvent("span-other", "session-middle", "other-trace", "root", "", "1000000000", "2000000000"),
	}
	if err := repo.SaveEvents(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(slog.Default(), repo))
	t.Cleanup(server.Close)

	first := getSpanPage(t, server.URL+"/api/v1/sessions/session-newest/spans?limit=1")
	if len(first.Data) != 1 || first.Data[0].SpanID != "root" || first.Data[0].ParentAvailability != "root" || first.Pagination.NextCursor == nil {
		t.Fatalf("first page = %#v", first)
	}
	second := getSpanPage(t, server.URL+"/api/v1/sessions/session-newest/spans?limit=1&cursor="+url.QueryEscape(*first.Pagination.NextCursor))
	if len(second.Data) != 1 || second.Data[0].SpanID != "child" || second.Data[0].ParentAvailability != "not_loaded" || second.Pagination.NextCursor != nil {
		t.Fatalf("second page = %#v", second)
	}
}

func TestSessionSpansAPIReportsMissingSessionAndInvalidCursor(t *testing.T) {
	repo := sessionTestRepository(t)
	server := httptest.NewServer(NewHandler(slog.Default(), repo))
	t.Cleanup(server.Close)
	for _, test := range []struct {
		path   string
		status int
	}{
		{path: "/api/v1/sessions/missing/spans", status: http.StatusNotFound},
		{path: "/api/v1/sessions/session-newest/spans?cursor=not-a-cursor", status: http.StatusBadRequest},
		{path: "/api/v1/sessions/session-newest/spans?cursor=ZXlKdmMyTnZjbVZrWDJGMElqb2lNVGszTUMwd01TMHdNVlF3TURvd01Eb3dNRm9pTENKbGRtVnVkRjlwWkNJNkltVjJaVzUwSW4w", status: http.StatusBadRequest},
	} {
		response, err := http.Get(server.URL + test.path)
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

func TestSpanCursorRejectsNonSpanEventCursor(t *testing.T) {
	traceID, spanID := spanCursor(&eventCursor{EventID: "event-id"})
	if traceID != "" || spanID != "" {
		t.Fatalf("cursor = %q, %q", traceID, spanID)
	}
}

func getSpanPage(t *testing.T, address string) spanListResponse {
	t.Helper()
	response, err := http.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("spans status = %d", response.StatusCode)
	}
	var page spanListResponse
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	return page
}

func apiSpanEvent(eventID, sessionID, traceID, spanID, parentID, start, end string) canonical.Event {
	return apiSpanEventOpts(apiSpanOpts{
		EventID: eventID, SessionID: sessionID, TraceID: traceID, SpanID: spanID, ParentID: parentID,
		StartUnixNano: start, EndUnixNano: end,
	})
}

type apiSpanOpts struct {
	EventID, SessionID, TraceID, SpanID, ParentID  string
	StartUnixNano, EndUnixNano                     string
	Name, EventType, Provider, Tool, SourceVersion string
	OccurredAt                                     time.Time
	Attributes                                     map[string]any
}

func apiSpanEventOpts(opts apiSpanOpts) canonical.Event {
	at := opts.OccurredAt
	if at.IsZero() {
		at = time.Unix(0, 1_000_000_000).UTC()
	}
	name := opts.Name
	if name == "" {
		name = "synthetic.span"
	}
	eventType := opts.EventType
	if eventType == "" {
		eventType = "synthetic.span"
	}
	provider := opts.Provider
	if provider == "" {
		provider = "synthetic"
	}
	tool := opts.Tool
	if tool == "" {
		tool = "test"
	}
	version := opts.SourceVersion
	if version == "" {
		version = "1"
	}
	attributes := opts.Attributes
	if attributes == nil {
		attributes = map[string]any{}
	}
	envelope := map[string]any{
		"trace_id": opts.TraceID, "span_id": opts.SpanID, "name": name,
		"start_unix_nano": opts.StartUnixNano, "end_unix_nano": opts.EndUnixNano, "status_code": int64(0),
	}
	if opts.ParentID != "" {
		envelope["parent_span_id"] = opts.ParentID
	}
	return canonical.Event{
		SchemaVersion: "0.1.0", EventID: opts.EventID, EventType: eventType, OccurredAt: at, ReceivedAt: at,
		Provider: provider, Tool: tool, SourceSchema: "otel", SourceVersion: version,
		ActorID: "unavailable", DeviceID: "unavailable",
		SessionID: opts.SessionID, PrivacyLevel: "operational", Attributes: attributes,
		ProviderExtensions: map[string]any{"span": envelope},
	}
}
