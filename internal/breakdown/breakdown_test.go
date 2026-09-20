package breakdown

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestCalculateTableDriven(t *testing.T) {
	base := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	for _, test := range breakdownCases(base) {
		t.Run(test.name, func(t *testing.T) {
			assertCalculateResult(t, Calculate(test.events, test.session), test)
		})
	}
}

type breakdownCase struct {
	name               string
	events             []canonical.Event
	session            *canonical.Session
	wantAvailability   string
	wantCategories     map[string]int64
	wantOverlap        int64
	wantUnclassified   int64
	wantCoverage       string
	wantWindowMs       int64
	wantUnavailableSub string
}

func breakdownCases(base time.Time) []breakdownCase {
	return []breakdownCase{
		{
			name: "nested same-kind spans count once",
			events: []canonical.Event{
				typedSpan("parent", "trace", "p", "", "tool", base, base.Add(10*time.Second)),
				typedSpan("child", "trace", "c", "p", "tool.execution", base.Add(2*time.Second), base.Add(8*time.Second)),
			},
			wantAvailability: AvailabilityAvailable,
			wantCategories:   map[string]int64{CategoryToolCalls: 10_000},
			wantWindowMs:     10_000,
			wantCoverage:     CoverageComplete,
		},
		{
			name: "cross-kind overlap is exclusive overlap segment",
			events: []canonical.Event{
				typedSpan("llm", "trace", "llm", "", "llm_request", base, base.Add(10*time.Second)),
				typedSpan("tool", "trace", "tool", "", "tool", base.Add(4*time.Second), base.Add(8*time.Second)),
			},
			wantAvailability: AvailabilityAvailable,
			wantCategories: map[string]int64{
				CategoryModelGeneration: 6_000,
			},
			wantOverlap:  4_000,
			wantWindowMs: 10_000,
			wantCoverage: CoverageComplete,
		},
		{
			name: "duplicate identity collapses to one interval",
			events: []canonical.Event{
				typedSpan("a", "trace", "same", "", "llm_request", base, base.Add(5*time.Second)),
				typedSpan("b", "trace", "same", "", "llm_request", base, base.Add(5*time.Second)),
			},
			wantAvailability: AvailabilityAvailable,
			wantCategories:   map[string]int64{CategoryModelGeneration: 5_000},
			wantWindowMs:     5_000,
			wantCoverage:     CoverageComplete,
		},
		{
			name: "out-of-order events still partition correctly",
			events: []canonical.Event{
				typedSpan("later", "trace", "wait", "", "tool.blocked_on_user", base.Add(6*time.Second), base.Add(10*time.Second)),
				typedSpan("earlier", "trace", "llm", "", "llm_request", base, base.Add(4*time.Second)),
			},
			wantAvailability: AvailabilityAvailable,
			wantCategories: map[string]int64{
				CategoryModelGeneration: 4_000,
				CategoryUserWait:        4_000,
			},
			wantUnclassified: 2_000,
			wantWindowMs:     10_000,
			wantCoverage:     CoverageComplete,
		},
		{
			name: "invalid inverted interval is ignored with partial coverage",
			events: []canonical.Event{
				typedSpan("good", "trace", "good", "", "llm_request", base, base.Add(5*time.Second)),
				invertedSpan("bad", "trace", "bad", "llm_request", base.Add(6*time.Second), base.Add(5*time.Second)),
			},
			wantAvailability: AvailabilityAvailable,
			wantCategories:   map[string]int64{CategoryModelGeneration: 5_000},
			wantWindowMs:     5_000,
			wantCoverage:     CoveragePartial,
		},
		{
			name: "missing times do not invent durations",
			events: []canonical.Event{
				missingTimeSpan("missing", "trace", "m", "tool"),
				typedSpan("ok", "trace", "ok", "", "tool", base, base.Add(3*time.Second)),
			},
			wantAvailability: AvailabilityAvailable,
			wantCategories:   map[string]int64{CategoryToolCalls: 3_000},
			wantWindowMs:     3_000,
			wantCoverage:     CoveragePartial,
		},
		{
			name:               "no classified intervals yields unavailable",
			events:             []canonical.Event{untypedSpan("u", "trace", "u", base, base.Add(2*time.Second))},
			wantAvailability:   AvailabilityUnavailable,
			wantUnavailableSub: "no valid classified intervals",
		},
		{
			name:               "zero window yields unavailable",
			events:             nil,
			session:            &canonical.Session{StartedAt: base, CompletedAt: &base},
			wantAvailability:   AvailabilityUnavailable,
			wantUnavailableSub: "no positive duration",
		},
		{
			name: "session window is the percentage denominator",
			events: []canonical.Event{
				typedSpan("llm", "trace", "llm", "", "llm_request", base.Add(time.Second), base.Add(3*time.Second)),
			},
			session: &canonical.Session{
				StartedAt:   base,
				CompletedAt: ptrTime(base.Add(10 * time.Second)),
			},
			wantAvailability: AvailabilityAvailable,
			wantCategories:   map[string]int64{CategoryModelGeneration: 2_000},
			wantUnclassified: 8_000,
			wantWindowMs:     10_000,
			wantCoverage:     CoverageComplete,
		},
		{
			name: "planning requires explicit evidence",
			events: []canonical.Event{
				typedSpan("plan", "trace", "plan", "", "planning", base, base.Add(2*time.Second)),
				typedSpan("llm", "trace", "llm", "", "llm_request", base.Add(2*time.Second), base.Add(5*time.Second)),
			},
			wantAvailability: AvailabilityAvailable,
			wantCategories: map[string]int64{
				CategoryPlanning:        2_000,
				CategoryModelGeneration: 3_000,
			},
			wantWindowMs: 5_000,
			wantCoverage: CoverageComplete,
		},
		{
			name: "interaction and hook spans are not invented categories",
			events: []canonical.Event{
				typedSpan("root", "trace", "root", "", "interaction", base, base.Add(10*time.Second)),
				typedSpan("hook", "trace", "hook", "root", "hook", base.Add(time.Second), base.Add(2*time.Second)),
				typedSpan("llm", "trace", "llm", "root", "llm_request", base.Add(3*time.Second), base.Add(7*time.Second)),
			},
			wantAvailability: AvailabilityAvailable,
			wantCategories:   map[string]int64{CategoryModelGeneration: 4_000},
			wantUnclassified: 6_000,
			wantWindowMs:     10_000,
			wantCoverage:     CoverageComplete,
		},
	}
}

func assertCalculateResult(t *testing.T, result Result, test breakdownCase) {
	t.Helper()
	if result.Availability != test.wantAvailability {
		t.Fatalf("availability = %q, want %q", result.Availability, test.wantAvailability)
	}
	if result.CalculationVersion != CalculationVersion {
		t.Fatalf("calculation_version = %q", result.CalculationVersion)
	}
	if test.wantAvailability == AvailabilityUnavailable {
		assertUnavailableResult(t, result, test.wantUnavailableSub)
		return
	}
	assertAvailableResult(t, result, test)
}

func assertUnavailableResult(t *testing.T, result Result, wantSub string) {
	t.Helper()
	if result.Window != nil || result.Overlap != nil || len(result.Categories) != 0 {
		t.Fatalf("unavailable payload leaked values: %#v", result)
	}
	if result.UnavailableReason == nil || !strings.Contains(*result.UnavailableReason, wantSub) {
		t.Fatalf("unavailable_reason = %#v, want substring %q", result.UnavailableReason, wantSub)
	}
}

func assertAvailableResult(t *testing.T, result Result, test breakdownCase) {
	t.Helper()
	if result.Window == nil || result.Window.DurationMs != test.wantWindowMs {
		t.Fatalf("window = %#v, want duration %d", result.Window, test.wantWindowMs)
	}
	if result.Coverage == nil || result.Coverage.State != test.wantCoverage {
		t.Fatalf("coverage = %#v, want %q", result.Coverage, test.wantCoverage)
	}
	gotCategories := map[string]int64{}
	sumCategories := int64(0)
	for _, category := range result.Categories {
		gotCategories[category.ID] = category.DurationMs
		sumCategories += category.DurationMs
		if category.Percent == nil {
			t.Fatalf("category %s missing percent", category.ID)
		}
	}
	if len(gotCategories) != len(test.wantCategories) {
		t.Fatalf("categories = %#v, want %#v", gotCategories, test.wantCategories)
	}
	for id, want := range test.wantCategories {
		if gotCategories[id] != want {
			t.Fatalf("category %s = %d, want %d (all %#v)", id, gotCategories[id], want, gotCategories)
		}
	}
	gotOverlap := segmentMs(result.Overlap)
	if gotOverlap != test.wantOverlap {
		t.Fatalf("overlap = %d, want %d", gotOverlap, test.wantOverlap)
	}
	gotUnclassified := segmentMs(result.Unclassified)
	if gotUnclassified != test.wantUnclassified {
		t.Fatalf("unclassified = %d, want %d", gotUnclassified, test.wantUnclassified)
	}
	if sumCategories+gotOverlap+gotUnclassified != test.wantWindowMs {
		t.Fatalf("partition sum %d != window %d", sumCategories+gotOverlap+gotUnclassified, test.wantWindowMs)
	}
}

func segmentMs(segment *SegmentDuration) int64 {
	if segment == nil {
		return 0
	}
	return segment.DurationMs
}

func TestCalculateKeepsFirstCategoryForDuplicateIdentity(t *testing.T) {
	base := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	first := typedSpan("earlier-event", "trace", "same", "", "llm_request", base, base.Add(5*time.Second))
	later := typedSpan("later-event", "trace", "same", "", "tool", base.Add(time.Second), base.Add(6*time.Second))
	later.OccurredAt = base.Add(time.Minute)
	later.ReceivedAt = later.OccurredAt
	result := Calculate([]canonical.Event{later, first}, nil)
	if result.Availability != AvailabilityAvailable {
		t.Fatalf("availability = %#v", result)
	}
	got := map[string]int64{}
	for _, category := range result.Categories {
		got[category.ID] = category.DurationMs
	}
	if got[CategoryModelGeneration] != 5_000 || got[CategoryToolCalls] != 0 {
		t.Fatalf("categories = %#v, want first-wins model_generation only", got)
	}
}

func TestCalculateSubMillisecondFragmentsStillPartition(t *testing.T) {
	base := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	// 1500µs tool span inside a 2ms window leaves sub-ms unclassified residue that
	// must still partition exactly after nanosecond accumulation.
	events := []canonical.Event{
		typedSpan("tool", "trace", "tool", "", "tool", base, base.Add(1500*time.Microsecond)),
	}
	session := &canonical.Session{StartedAt: base, CompletedAt: ptrTime(base.Add(2 * time.Millisecond))}
	result := Calculate(events, session)
	if result.Availability != AvailabilityAvailable {
		t.Fatalf("availability = %#v", result)
	}
	if result.Window == nil || result.Window.DurationMs != 2 {
		t.Fatalf("window = %#v, want 2ms", result.Window)
	}
	sum := int64(0)
	for _, category := range result.Categories {
		sum += category.DurationMs
	}
	sum += segmentMs(result.Overlap) + segmentMs(result.Unclassified)
	if sum != result.Window.DurationMs {
		t.Fatalf("partition sum %d != window %d (%#v)", sum, result.Window.DurationMs, result)
	}
}

func typedSpan(eventID, traceID, spanID, parentID, spanType string, start, end time.Time) canonical.Event {
	envelope := map[string]any{
		"trace_id":        traceID,
		"span_id":         spanID,
		"name":            "claude_code." + spanType,
		"start_unix_nano": nano(start),
		"end_unix_nano":   nano(end),
		"status_code":     int64(0),
	}
	if parentID != "" {
		envelope["parent_span_id"] = parentID
	}
	return canonical.Event{
		SchemaVersion: "0.1.0", EventID: eventID, EventType: "claude_code." + spanType,
		OccurredAt: start, ReceivedAt: start, Provider: "anthropic", Tool: "claude-code",
		SourceSchema: "otel", SourceVersion: "2.1.268", ActorID: "unavailable", DeviceID: "unavailable",
		SessionID: "session-test", PrivacyLevel: "operational",
		Attributes:         map[string]any{"span_type": spanType},
		ProviderExtensions: map[string]any{"span": envelope},
	}
}

func invertedSpan(eventID, traceID, spanID, spanType string, start, end time.Time) canonical.Event {
	event := typedSpan(eventID, traceID, spanID, "", spanType, start, end)
	envelope := event.ProviderExtensions["span"].(map[string]any)
	envelope["start_unix_nano"] = nano(start)
	envelope["end_unix_nano"] = nano(end) // caller passes inverted pair
	return event
}

func missingTimeSpan(eventID, traceID, spanID, spanType string) canonical.Event {
	event := typedSpan(eventID, traceID, spanID, "", spanType, time.Unix(1, 0).UTC(), time.Unix(2, 0).UTC())
	envelope := event.ProviderExtensions["span"].(map[string]any)
	delete(envelope, "start_unix_nano")
	delete(envelope, "end_unix_nano")
	event.OccurredAt = time.Time{}
	return event
}

func untypedSpan(eventID, traceID, spanID string, start, end time.Time) canonical.Event {
	event := typedSpan(eventID, traceID, spanID, "", "interaction", start, end)
	event.Attributes = map[string]any{}
	event.EventType = "synthetic.span"
	envelope := event.ProviderExtensions["span"].(map[string]any)
	envelope["name"] = "synthetic.span"
	return event
}

func nano(value time.Time) string {
	return strconv.FormatInt(value.UnixNano(), 10)
}

func ptrTime(value time.Time) *time.Time { return &value }
