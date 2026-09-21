package ui_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/ui"
)

func TestSessionTraceRendersFiveLanesAndSharedAxis(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	completed := now.Add(10 * time.Second)
	session := syntheticSession("trace-session", now)
	session.CompletedAt = &completed
	session.Attributes = map[string]any{
		"model":      "claude-sonnet-test",
		"git_branch": "feature/159-trace",
	}

	events := []canonical.Event{
		{
			EventID: "conv-user", EventType: "user_prompt", SessionID: "trace-session",
			OccurredAt: now, ReceivedAt: now, Provider: "anthropic", Tool: "claude-code",
			ProviderExtensions: map[string]any{"event": map[string]any{"prompt": "trace user prompt"}},
		},
		{
			EventID: "conv-assistant", EventType: "assistant_response", SessionID: "trace-session",
			OccurredAt: now.Add(2 * time.Second), ReceivedAt: now.Add(2 * time.Second),
			Provider: "anthropic", Tool: "claude-code",
			ProviderExtensions: map[string]any{"event": map[string]any{"response": "trace assistant reply"}},
		},
		{
			EventID: "model-1", EventType: "model_interaction", SessionID: "trace-session",
			OccurredAt: now.Add(3 * time.Second), ReceivedAt: now.Add(3 * time.Second),
			Provider: "anthropic", Tool: "claude-code",
			Attributes: map[string]any{"duration_ms": int64(500)},
		},
		{
			EventID: "tool-1", EventType: "operation", SessionID: "trace-session",
			OccurredAt: now.Add(4 * time.Second), ReceivedAt: now.Add(4 * time.Second),
			Provider: "anthropic", Tool: "claude-code",
			Attributes: map[string]any{
				"operation_id": "op-1", "category": "shell command", "tool_name": "rg",
				"duration_ms": int64(250), "outcome": "success",
			},
		},
		{
			EventID: "file-1", EventType: "claude_code.tool", SessionID: "trace-session",
			OccurredAt: now.Add(5 * time.Second), ReceivedAt: now.Add(5 * time.Second),
			Provider: "anthropic", Tool: "claude-code",
			Attributes: map[string]any{
				"tool": map[string]any{
					"file_path": "/workspace/trace.go", "tool_name": "Read", "duration_ms": int64(40),
				},
			},
			ProviderExtensions: map[string]any{
				"span": map[string]any{
					"trace_id": "trace-aaa", "span_id": "span-root", "name": "llm_request",
					"start_unix_nano": now.Add(3 * time.Second).UnixNano(),
					"end_unix_nano":   now.Add(3500 * time.Millisecond).UnixNano(),
				},
			},
		},
		{
			EventID: "span-tool", EventType: "span", SessionID: "trace-session",
			OccurredAt: now.Add(4 * time.Second), ReceivedAt: now.Add(4 * time.Second),
			Provider: "anthropic", Tool: "claude-code",
			ProviderExtensions: map[string]any{
				"span": map[string]any{
					"trace_id": "trace-aaa", "span_id": "span-tool", "parent_span_id": "span-root",
					"name":            "tool.execution",
					"start_unix_nano": now.Add(4 * time.Second).UnixNano(),
					"end_unix_nano":   now.Add(4250 * time.Millisecond).UnixNano(),
				},
			},
		},
		{
			EventID: "unplaced-1", EventType: "model_interaction", SessionID: "trace-session",
			Provider: "anthropic", Tool: "claude-code",
		},
	}

	body := renderSessionDetail(t, &fullStub{
		sessions: []canonical.Session{session},
		events:   map[string][]canonical.Event{"trace-session": events},
	}, nil, "trace-session")

	assertContainsAll(t, body, []string{
		"Session Trace",
		`aria-label="Shared session time axis"`,
		`data-lane="conversation"`,
		`data-lane="agent"`,
		`data-lane="tools"`,
		`data-lane="files"`,
		`data-lane="spans"`,
		"User message",
		"trace user prompt",
		"Model interaction",
		"Operation",
		"/workspace/trace.go",
		"llm_request",
		"Unplaced events",
		"unplaced-1",
		"Accessible chronological lists",
		"source=trace",
	})
	if strings.Contains(body, "Plan -> Inspect") {
		t.Fatalf("trace must not invent stage sequences: %q", body)
	}
}

func TestSessionTraceSelectionHighlightsLaneMarker(t *testing.T) {
	now := time.Now().UTC()
	session := syntheticSession("trace-select", now)
	events := []canonical.Event{{
		EventID: "pick-me", EventType: "user_prompt", SessionID: "trace-select",
		OccurredAt: now, ReceivedAt: now, Provider: "anthropic", Tool: "claude-code",
		ProviderExtensions: map[string]any{"event": map[string]any{"prompt": "select me"}},
	}}
	server, err := ui.New("test-token", &fullStub{
		sessions: []canonical.Session{session},
		events:   map[string][]canonical.Event{"trace-select": events},
	}, defaultContextWasteThresholds, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Wrap(http.NotFoundHandler())
	cookie := unlock(t, handler)
	body := getAuthed(t, handler, cookie, "/sessions/trace-select?event=pick-me&inspector=details&source=trace").Body.String()
	if !strings.Contains(body, `id="trace-event-pick-me-select"`) {
		t.Fatalf("selected trace marker missing focus id: %q", body)
	}
	if !strings.Contains(body, "is-selected") {
		t.Fatalf("selected lane marker missing is-selected: %q", body)
	}
}
