package spans

import (
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestProjectCombinesProviderSpanEnvelopesDeterministically(t *testing.T) {
	at := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	events := []canonical.Event{
		spanEvent("claude-child", "trace-a", "child", "root", "2000000000", "3000000000", map[string]any{"status_code": int64(1)}),
		spanEvent("codex-root", "trace-b", "root", "", "1000000000", "1500000000", map[string]any{"status": map[string]any{"code": float64(2)}}),
		spanEvent("claude-root", "trace-a", "root", "", "1000000000", "2500000000", map[string]any{"status_code": int64(0)}),
		spanEvent("duplicate-child", "trace-a", "child", "root", "2000000000", "3000000000", map[string]any{"status_code": int64(1)}),
	}
	for index := range events {
		events[index].OccurredAt = at.Add(time.Duration(index) * time.Second)
	}

	records := Project(events)
	if len(records) != 3 {
		t.Fatalf("record count = %d, want 3", len(records))
	}
	if records[0].TraceID != "trace-a" || records[0].SpanID != "root" || records[2].SpanID != "child" || records[1].TraceID != "trace-b" {
		t.Fatalf("records = %#v", records)
	}
	if records[2].DurationMs == nil || *records[2].DurationMs != 1000 || records[2].StatusCode == nil || *records[2].StatusCode != 1 {
		t.Fatalf("child record = %#v", records[2])
	}
	if got := records[2].SourceEventIDs; len(got) != 2 || got[0] != "claude-child" || got[1] != "duplicate-child" {
		t.Fatalf("source event IDs = %#v", got)
	}
}

func TestPageDistinguishesLoadedUnloadedAndAbsentParents(t *testing.T) {
	records := Project([]canonical.Event{
		spanEvent("root", "trace", "root", "", "1000000000", "2000000000", nil),
		spanEvent("child", "trace", "child", "root", "2000000000", "3000000000", nil),
		spanEvent("orphan", "trace", "orphan", "missing", "3000000000", "4000000000", nil),
	})
	first, cursor := Page(records, 1, "", "")
	if len(first) != 1 || first[0].ParentAvailability != ParentRoot || cursor == nil {
		t.Fatalf("first page = %#v, cursor = %#v", first, cursor)
	}
	second, cursor := Page(records, 1, cursor.TraceID, cursor.SpanID)
	if len(second) != 1 || second[0].ParentAvailability != ParentNotLoaded || cursor == nil {
		t.Fatalf("second page = %#v, cursor = %#v", second, cursor)
	}
	third, cursor := Page(records, 1, cursor.TraceID, cursor.SpanID)
	if len(third) != 1 || third[0].ParentAvailability != ParentAbsent || cursor != nil {
		t.Fatalf("third page = %#v, cursor = %#v", third, cursor)
	}
	all, _ := Page(records, 3, "", "")
	if all[1].ParentAvailability != ParentLoaded {
		t.Fatalf("full page child parent state = %q", all[1].ParentAvailability)
	}
}

func TestProjectRetainsInvalidAndMissingIntervalsWithoutFabrication(t *testing.T) {
	records := Project([]canonical.Event{
		spanEvent("invalid", "trace", "invalid", "", "3000000000", "1000000000", nil),
		spanEvent("missing", "trace", "missing", "", "", "", nil),
	})
	if records[0].IntervalAvailability != IntervalInvalid || records[0].DurationMs != nil || records[1].IntervalAvailability != IntervalMissing || records[1].DurationMs != nil {
		t.Fatalf("records = %#v", records)
	}
}

func TestProjectDoesNotMergeSameSpanIDAcrossTracesOrRecurseCycles(t *testing.T) {
	records := Project([]canonical.Event{
		spanEvent("cycle-a", "trace-a", "shared", "other", "1000000000", "2000000000", nil),
		spanEvent("cycle-b", "trace-a", "other", "shared", "2000000000", "3000000000", nil),
		spanEvent("other-trace", "trace-b", "shared", "", "1000000000", "2000000000", nil),
	})
	page, _ := Page(records, len(records), "", "")
	if len(page) != 3 || page[0].ParentAvailability != ParentLoaded || page[2].ParentAvailability != ParentLoaded {
		t.Fatalf("page = %#v", page)
	}
}

func spanEvent(eventID, traceID, spanID, parentID, start, end string, extra map[string]any) canonical.Event {
	envelope := map[string]any{"trace_id": traceID, "span_id": spanID, "start_unix_nano": start, "end_unix_nano": end, "name": "synthetic.span"}
	if parentID != "" {
		envelope["parent_span_id"] = parentID
	}
	for key, value := range extra {
		envelope[key] = value
	}
	return canonical.Event{
		EventID: eventID, EventType: "synthetic.span", Provider: "synthetic", Tool: "test", SourceVersion: "1",
		Attributes: map[string]any{}, ProviderExtensions: map[string]any{"span": envelope},
	}
}
