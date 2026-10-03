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
		{EventID: "other-provider", EventType: "user_prompt", Provider: "cursor", Tool: "cursor"},
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

// TestIsConversationEventGuardsProviderAndTool proves the shared eligibility
// predicate applies the same provider/tool guard as Project. Claude and Codex
// retained content events are eligible; unrelated providers carrying a generic
// event type are not. This keeps the session-availability conversation signal
// aligned with what the conversation endpoint actually emits.
func TestIsConversationEventGuardsProviderAndTool(t *testing.T) {
	tests := []struct {
		name  string
		event canonical.Event
		want  bool
	}{
		{name: "claude content event", event: canonical.Event{Provider: "anthropic", Tool: "claude-code", EventType: "user_prompt"}, want: true},
		{name: "codex content event", event: canonical.Event{Provider: "openai", Tool: "codex", EventType: "user_prompt"}, want: true},
		{name: "unrelated provider content event", event: canonical.Event{Provider: "cursor", Tool: "cursor", EventType: "user_prompt"}, want: false},
		{name: "claude non-content event", event: canonical.Event{Provider: "anthropic", Tool: "claude-code", EventType: "api_request"}, want: false},
		{name: "claude transcript prompt", event: transcriptEvent("p", "user_message", time.Time{}, map[string]any{"prompt_content": "hi"}), want: true},
		{name: "claude transcript response", event: transcriptEvent("r", "assistant_message", time.Time{}, map[string]any{"response_content": "ok"}), want: true},
		{name: "claude transcript thinking only", event: transcriptEvent("t", "assistant_message", time.Time{}, map[string]any{"thinking": "plan"}), want: true},
		{name: "claude transcript tool_use only", event: transcriptEvent("u", "assistant_message", time.Time{}, map[string]any{"cwd": "/repo"}), want: false},
		{name: "claude transcript empty prompt", event: transcriptEvent("e", "user_message", time.Time{}, map[string]any{"prompt_content": "  "}), want: false},
		{name: "claude transcript non-string response", event: transcriptEvent("n", "assistant_message", time.Time{}, map[string]any{"response_content": 42}), want: false},
		{name: "codex transcript-shaped event", event: canonical.Event{Provider: "openai", Tool: "codex", EventType: "user_message", ProviderExtensions: map[string]any{"transcript": map[string]any{"prompt_content": "hi"}}}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsConversationEvent(test.event); got != test.want {
				t.Fatalf("IsConversationEvent = %v, want %v", got, test.want)
			}
		})
	}
}

// TestProjectClaudeTranscriptRecords proves Claude session JSONL prompt,
// response, and thinking text project under the same availability rules as the
// OTLP content events, with thinking carried separately and never synthesised.
func TestProjectClaudeTranscriptRecords(t *testing.T) {
	at := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	events := []canonical.Event{
		transcriptEvent("prompt", "user_message", at, map[string]any{"prompt_content": "transcript prompt"}),
		transcriptEvent("answer", "assistant_message", at.Add(time.Second), map[string]any{"response_content": "transcript answer", "thinking": "transcript plan"}),
		transcriptEvent("thought", "assistant_message", at.Add(2*time.Second), map[string]any{"thinking": "only thinking"}),
		transcriptEvent("redacted", "user_message", at.Add(3*time.Second), map[string]any{"prompt_content": "<redacted>"}),
		transcriptEvent("tool-only", "assistant_message", at.Add(4*time.Second), map[string]any{"cwd": "/repo"}),
	}

	records := Project(events)
	if len(records) != 4 {
		t.Fatalf("records = %#v", records)
	}
	assertRecord(t, records[0], RoleUser, AvailabilityAvailable, "transcript prompt")
	assertRecord(t, records[1], RoleAssistant, AvailabilityAvailable, "transcript answer")
	assertRecord(t, records[2], RoleAssistant, AvailabilityUnavailable, "")
	assertRecord(t, records[3], RoleUser, AvailabilityProviderRedacted, "<redacted>")
	wantThinking := []string{"", "transcript plan", "only thinking", ""}
	for index, want := range wantThinking {
		got := records[index].Thinking
		if (want == "") != (got == nil) || (got != nil && *got != want) {
			t.Fatalf("record %d thinking = %#v, want %q", index, got, want)
		}
	}
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
	return claudeEvent(id, eventType, at, "event", echo)
}

func transcriptEvent(id, eventType string, at time.Time, transcript map[string]any) canonical.Event {
	return claudeEvent(id, eventType, at, "transcript", transcript)
}

func claudeEvent(id, eventType string, at time.Time, namespace string, extension map[string]any) canonical.Event {
	return canonical.Event{
		EventID: id, EventType: eventType, OccurredAt: at, Provider: "anthropic", Tool: "claude-code",
		ProviderExtensions: map[string]any{namespace: extension},
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
