package conversation

import (
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestProjectPreservesContentAvailabilityWithoutInventingMessages(t *testing.T) {
	at := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	events := []canonical.Event{
		contentEvent("user", "user_prompt", at, map[string]any{"prompt": "first\nsecond"}),
		contentEvent("redacted", "assistant_response", at.Add(time.Second), map[string]any{"response": "<REDACTED>"}),
		contentEvent("length", "user_prompt", at.Add(2*time.Second), map[string]any{"prompt_length": int64(42)}),
		contentEvent("body-ref", "api_request_body", at.Add(3*time.Second), map[string]any{"body_ref": "local/body.json"}),
		contentEvent("body", "api_response_body", at.Add(4*time.Second), map[string]any{"body": "raw provider response"}),
		contentEvent("absent", "assistant_response", at.Add(5*time.Second), map[string]any{}),
		{EventID: "other-provider", EventType: "user_prompt", Provider: "openai", Tool: "codex"},
	}

	records := Project(events)
	if len(records) != 6 {
		t.Fatalf("records = %#v", records)
	}
	assertRecord(t, records[0], RoleUser, AvailabilityAvailable, "first\nsecond")
	assertRecord(t, records[1], RoleAssistant, AvailabilityProviderRedacted, "<REDACTED>")
	assertRecord(t, records[2], RoleUser, AvailabilityLengthOnly, "")
	assertRecord(t, records[3], RoleUnknown, AvailabilityBodyReference, "")
	assertRecord(t, records[4], RoleUnknown, AvailabilityAvailable, "raw provider response")
	assertRecord(t, records[5], RoleAssistant, AvailabilityUnavailable, "")
}

func TestPageUsesSourceEventCursorAfterProjection(t *testing.T) {
	at := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	records := []Record{{EventID: "a", OccurredAt: at}, {EventID: "b", OccurredAt: at.Add(time.Second)}, {EventID: "c", OccurredAt: at.Add(2 * time.Second)}}
	first, last := Page(records, 2, "", "")
	if len(first) != 2 || last == nil || last.EventID != "b" {
		t.Fatalf("first page = %#v last=%#v", first, last)
	}
	second, last := Page(records, 2, last.OccurredAt.UTC().Format(time.RFC3339Nano), last.EventID)
	if len(second) != 1 || second[0].EventID != "c" || last != nil {
		t.Fatalf("second page = %#v last=%#v", second, last)
	}
}

func contentEvent(id, eventType string, at time.Time, echo map[string]any) canonical.Event {
	return canonical.Event{
		EventID: id, EventType: eventType, OccurredAt: at, Provider: "anthropic", Tool: "claude-code",
		ProviderExtensions: map[string]any{"event": echo},
	}
}

func assertRecord(t *testing.T, record Record, role, availability, text string) {
	t.Helper()
	if record.Role != role || record.ContentAvailability != availability {
		t.Fatalf("record = %#v, want role=%q availability=%q", record, role, availability)
	}
	if text == "" {
		if record.Text != nil {
			t.Fatalf("record text = %q, want nil", *record.Text)
		}
		return
	}
	if record.Text == nil || *record.Text != text {
		t.Fatalf("record text = %#v, want %q", record.Text, text)
	}
}
