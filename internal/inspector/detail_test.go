package inspector

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestBuildTruncatesLongStringsAndExpandReturnsFull(t *testing.T) {
	long := strings.Repeat("a", DefaultMaxStringRunes+10)
	event := canonical.Event{
		EventID: "evt-1", EventType: "user_prompt", OccurredAt: time.Unix(1, 0).UTC(),
		Attributes:         map[string]any{"note": long},
		ProviderExtensions: map[string]any{"event": map[string]any{"prompt": long}},
	}
	truncated := Build(event, nil, nil, false)
	note, _ := truncated.Attributes["note"].(string)
	if utf8.RuneCountInString(note) != DefaultMaxStringRunes+1 || !strings.HasSuffix(note, "…") {
		t.Fatalf("truncated note = %q (runes=%d)", note, utf8.RuneCountInString(note))
	}
	if !truncated.Truncation.Applied || !truncated.Truncation.ExpandAvailable {
		t.Fatalf("truncation = %#v", truncated.Truncation)
	}
	full := Build(event, nil, nil, true)
	fullNote, _ := full.Attributes["note"].(string)
	if fullNote != long || full.Truncation.Applied || full.Truncation.ExpandAvailable {
		t.Fatalf("expanded = %#v truncation=%#v", full.Attributes["note"], full.Truncation)
	}
}

func TestBuildSharedSpanRelationships(t *testing.T) {
	at := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	spanA := canonical.Event{
		EventID: "evt-span-a", EventType: "span", OccurredAt: at,
		ProviderExtensions: map[string]any{
			"span": map[string]any{"trace_id": "trace-1", "span_id": "span-1", "name": "tool"},
		},
	}
	spanB := canonical.Event{
		EventID: "evt-span-b", EventType: "span", OccurredAt: at.Add(time.Millisecond),
		ProviderExtensions: map[string]any{
			"span": map[string]any{"trace_id": "trace-1", "span_id": "span-1", "name": "tool"},
		},
	}
	nearby := canonical.Event{
		EventID: "evt-nearby", EventType: "user_prompt", OccurredAt: at.Add(2 * time.Second),
	}
	detail := Build(spanA, []canonical.Event{spanA, spanB, nearby}, nil, true)
	assertRelatedContains(t, detail.Relationships.RelatedEvents, "evt-span-b", RelationSharedSpan)
	assertRelatedOmits(t, detail.Relationships.RelatedEvents, "evt-nearby")
}

func TestBuildSharedOperationRelationships(t *testing.T) {
	at := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	selected := canonical.Event{
		EventID: "evt-tool", EventType: "tool_result", OccurredAt: at,
		Attributes: map[string]any{"operation_id": "op-1"},
	}
	peer := canonical.Event{
		EventID: "evt-peer", EventType: "tool_result", OccurredAt: at.Add(time.Second),
		Attributes: map[string]any{"operation_id": "op-1"},
	}
	nearby := canonical.Event{
		EventID: "evt-nearby", EventType: "user_prompt", OccurredAt: at.Add(2 * time.Second),
	}
	detail := Build(selected, []canonical.Event{selected, peer, nearby}, nil, true)
	assertRelatedContains(t, detail.Relationships.RelatedEvents, "evt-peer", RelationSharedOperation)
	assertRelatedOmits(t, detail.Relationships.RelatedEvents, "evt-nearby")
}

func TestBuildEmptyRelationshipsAreExplicit(t *testing.T) {
	event := canonical.Event{EventID: "lonely", EventType: "user_prompt", OccurredAt: time.Unix(1, 0).UTC()}
	detail := Build(event, []canonical.Event{event}, nil, true)
	if detail.Relationships.Availability.Files != AvailabilityNone ||
		detail.Relationships.Availability.Spans != AvailabilityNone ||
		detail.Relationships.Availability.RelatedEvents != AvailabilityNone {
		t.Fatalf("availability = %#v", detail.Relationships.Availability)
	}
}

func assertRelatedContains(t *testing.T, related []RelatedEvent, eventID, relation string) {
	t.Helper()
	for _, item := range related {
		if item.EventID == eventID && item.Relation == relation {
			return
		}
	}
	t.Fatalf("expected %s/%s in %#v", eventID, relation, related)
}

func assertRelatedOmits(t *testing.T, related []RelatedEvent, eventID string) {
	t.Helper()
	for _, item := range related {
		if item.EventID == eventID {
			t.Fatalf("unexpected related event %s in %#v", eventID, related)
		}
	}
}
