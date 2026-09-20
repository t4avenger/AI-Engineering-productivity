package ui

import (
	"testing"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestSpanEvidenceRowsRendersObservedParentAndIntervalStates(t *testing.T) {
	events := []canonical.Event{
		uiSpanEvent("root", "root", "", "1000000000", "2000000000"),
		uiSpanEvent("child", "child", "root", "2000000000", "3000000000"),
	}
	rows, partial := spanEvidenceRows(events)
	if partial || len(rows) != 2 {
		t.Fatalf("rows = %#v, partial = %v", rows, partial)
	}
	if rows[1].ParentID != "root" || rows[1].ParentAvailability != "loaded" || rows[1].Duration != "1000 ms" || rows[1].IntervalState != "available" || rows[1].SourceEvents != "child" {
		t.Fatalf("child row = %#v", rows[1])
	}
}

func uiSpanEvent(eventID, spanID, parentID, start, end string) canonical.Event {
	envelope := map[string]any{
		"trace_id": "trace", "span_id": spanID, "name": "synthetic.span",
		"start_unix_nano": start, "end_unix_nano": end,
	}
	if parentID != "" {
		envelope["parent_span_id"] = parentID
	}
	return canonical.Event{EventID: eventID, ProviderExtensions: map[string]any{"span": envelope}}
}
