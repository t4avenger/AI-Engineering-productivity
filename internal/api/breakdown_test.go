package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/breakdown"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestSessionBreakdownAPIContract(t *testing.T) {
	repo := sessionTestRepository(t)
	base := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	events := []canonical.Event{
		apiTypedSpanEvent(apiTypedSpan{
			EventID: "evt-llm", SessionID: "session-newest", TraceID: "trace", SpanID: "llm",
			SpanType: "llm_request", Start: base, End: base.Add(4 * time.Second),
		}),
		apiTypedSpanEvent(apiTypedSpan{
			EventID: "evt-tool", SessionID: "session-newest", TraceID: "trace", SpanID: "tool",
			SpanType: "tool", Start: base.Add(2 * time.Second), End: base.Add(6 * time.Second),
		}),
		apiTypedSpanEvent(apiTypedSpan{
			EventID: "evt-other", SessionID: "session-middle", TraceID: "other", SpanID: "llm",
			SpanType: "llm_request", Start: base, End: base.Add(time.Second),
		}),
	}
	if err := repo.SaveEvents(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(slog.Default(), repo))
	t.Cleanup(server.Close)

	result := getBreakdown(t, server.URL+"/api/v1/sessions/session-newest/breakdown")
	if result.Availability != breakdown.AvailabilityAvailable {
		t.Fatalf("availability = %#v", result)
	}
	if result.CalculationVersion != breakdown.CalculationVersion {
		t.Fatalf("calculation_version = %q", result.CalculationVersion)
	}
	if result.Window == nil || result.Window.DurationMs <= 0 {
		t.Fatalf("window = %#v", result.Window)
	}
	got := map[string]int64{}
	for _, category := range result.Categories {
		got[category.ID] = category.DurationMs
	}
	if got[breakdown.CategoryModelGeneration] <= 0 || got[breakdown.CategoryToolCalls] <= 0 {
		t.Fatalf("categories = %#v", got)
	}
	if result.Overlap == nil || result.Overlap.DurationMs <= 0 {
		t.Fatalf("expected overlap, got %#v", result.Overlap)
	}

	again := getBreakdown(t, server.URL+"/api/v1/sessions/session-newest/breakdown?limit=1")
	if again.Window == nil || result.Window == nil || again.Window.DurationMs != result.Window.DurationMs {
		t.Fatalf("limit changed breakdown window: %#v vs %#v", again.Window, result.Window)
	}
}

func TestSessionBreakdownAPIUnavailableWithoutClassifiedIntervals(t *testing.T) {
	repo := sessionTestRepository(t)
	events := []canonical.Event{
		apiSpanEvent("span-root", "session-newest", "trace", "root", "", "1000000000", "2000000000"),
	}
	if err := repo.SaveEvents(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(slog.Default(), repo))
	t.Cleanup(server.Close)
	result := getBreakdown(t, server.URL+"/api/v1/sessions/session-newest/breakdown")
	if result.Availability != breakdown.AvailabilityUnavailable {
		t.Fatalf("availability = %#v", result)
	}
	if result.Window != nil || len(result.Categories) != 0 {
		t.Fatalf("unavailable payload leaked values: %#v", result)
	}
}

func TestSessionBreakdownAPIReportsMissingSession(t *testing.T) {
	repo := sessionTestRepository(t)
	server := httptest.NewServer(NewHandler(slog.Default(), repo))
	t.Cleanup(server.Close)
	response, err := http.Get(server.URL + "/api/v1/sessions/missing/breakdown")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func getBreakdown(t *testing.T, address string) breakdown.Result {
	t.Helper()
	response, err := http.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("breakdown status = %d", response.StatusCode)
	}
	var payload breakdownResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload.Data
}

type apiTypedSpan struct {
	EventID, SessionID, TraceID, SpanID, ParentID, SpanType string
	Start, End                                              time.Time
}

func apiTypedSpanEvent(span apiTypedSpan) canonical.Event {
	return apiSpanEventOpts(apiSpanOpts{
		EventID: span.EventID, SessionID: span.SessionID, TraceID: span.TraceID, SpanID: span.SpanID,
		ParentID: span.ParentID, StartUnixNano: strconv.FormatInt(span.Start.UnixNano(), 10),
		EndUnixNano: strconv.FormatInt(span.End.UnixNano(), 10),
		Name:        "claude_code." + span.SpanType, EventType: "claude_code." + span.SpanType,
		Provider: "anthropic", Tool: "claude-code", SourceVersion: "2.1.268",
		OccurredAt: span.Start, Attributes: map[string]any{"span_type": span.SpanType},
	})
}
