package ui_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestSessionEventInspectorDeepLinkAndMissingEvent(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	repo := &fullStub{
		sessions: []canonical.Session{{
			SessionID: "inspect-session", Provider: "openai", Tool: "codex",
			State: "completed", StartedAt: now,
		}},
		events: map[string][]canonical.Event{
			"inspect-session": {{
				SchemaVersion: "0.1.0", EventID: "evt-inspect", EventType: "tool_result",
				OccurredAt: now, ReceivedAt: now, Provider: "openai", Tool: "codex",
				SourceSchema: "otel", SourceVersion: "test", ActorID: "unavailable", DeviceID: "unavailable",
				SessionID: "inspect-session", PrivacyLevel: "operational",
				Attributes: map[string]any{
					"operation_id": "op-shared",
					"outcome":      "success",
					"duration_ms":  "42",
				},
				ProviderExtensions: map[string]any{"event": map[string]any{"body": "synthetic"}},
			}, {
				SchemaVersion: "0.1.0", EventID: "evt-peer", EventType: "tool_result",
				OccurredAt: now.Add(time.Second), ReceivedAt: now.Add(time.Second),
				Provider: "openai", Tool: "codex", SourceSchema: "otel", SourceVersion: "test",
				ActorID: "unavailable", DeviceID: "unavailable", SessionID: "inspect-session", PrivacyLevel: "operational",
				Attributes:         map[string]any{"operation_id": "op-shared"},
				ProviderExtensions: map[string]any{},
			}},
		},
	}
	handler := wrapUI(t, repo)
	cookie := unlock(t, handler)

	selected := getAuthed(t, handler, cookie, "/sessions/inspect-session?event=evt-inspect&inspector=events")
	body := selected.Body.String()
	if selected.Code != http.StatusOK {
		t.Fatalf("status = %d", selected.Code)
	}
	for _, want := range []string{
		`id="event-inspector"`,
		`aria-selected="true">Events</a>`,
		`evt-inspect`,
		`is-selected`,
		`shared_operation`,
		`evt-peer`,
		`href="/sessions/inspect-session?event=evt-inspect&amp;inspector=details"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in inspector body", want)
		}
	}

	missing := getAuthed(t, handler, cookie, "/sessions/inspect-session?event=missing-event&inspector=details")
	if missing.Code != http.StatusOK {
		t.Fatalf("missing event status = %d", missing.Code)
	}
	if !strings.Contains(missing.Body.String(), "No retained event with this ID exists") {
		t.Fatalf("missing event body = %s", missing.Body.String())
	}

	attrs := getAuthed(t, handler, cookie, "/sessions/inspect-session?event=evt-inspect&inspector=attributes")
	attrsBody := attrs.Body.String()
	if !strings.Contains(attrsBody, "operation_id") || !strings.Contains(attrsBody, "op-shared") {
		t.Fatalf("attributes tab missing raw JSON: %s", attrsBody)
	}
}
