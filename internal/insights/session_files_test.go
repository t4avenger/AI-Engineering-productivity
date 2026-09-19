package insights

import (
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestSessionFilesProjectsClaudeToolSpanPath(t *testing.T) {
	occurred := time.Date(2026, 9, 11, 9, 6, 40, 500000000, time.UTC)
	entries := SessionFilesFromEvidence([]canonical.Event{{
		EventID: "span-read", EventType: "claude_code.tool", OccurredAt: occurred,
		Provider: "anthropic", Tool: "claude-code",
		Attributes: map[string]any{
			"tool": map[string]any{
				"file_path": "internal/service/handler.go", "tool_name": "Read",
				"tool_use_id": "toolu_read", "duration_ms": int64(200),
			},
		},
	}}, nil)
	if len(entries) != 1 {
		t.Fatalf("count = %d, want 1", len(entries))
	}
	entry := entries[0]
	assertStringPtr(t, entry.Path, "internal/service/handler.go")
	assertStringPtr(t, entry.Action, FileActionRead)
	if entry.Availability.Path != fileAvailabilityObserved || entry.Availability.Action != fileAvailabilityInferred {
		t.Fatalf("availability = %#v", entry.Availability)
	}
	if entry.Availability.LineDiff != fileAvailabilityUnavailable || entry.Additions != nil || entry.Deletions != nil {
		t.Fatalf("line diff must stay unavailable: %#v", entry)
	}
	if entry.DurationMs == nil || *entry.DurationMs != 200 {
		t.Fatalf("duration = %#v", entry.DurationMs)
	}
}

func TestSessionFilesProjectsClaudeOperationWithoutPath(t *testing.T) {
	occurred := time.Date(2026, 9, 11, 9, 6, 40, 500000000, time.UTC)
	entries := SessionFilesFromEvidence([]canonical.Event{{
		EventID: "tool-result-read", EventType: "tool_result", OccurredAt: occurred,
		Provider: "anthropic", Tool: "claude-code",
		ProviderExtensions: map[string]any{
			"event": map[string]any{
				"tool_name": "Read", "tool_use_id": "toolu_synthetic_read", "duration_ms": int64(12),
			},
		},
	}}, []canonical.Operation{{
		OperationID: "claude-code:session:tool:toolu_synthetic_read",
		SessionID:   "claude-code:session",
		Category:    canonical.OperationCategoryFilesystemRead,
		Provenance:  canonical.ProvenanceObserved,
		ProviderExtensions: map[string]any{
			"event": map[string]any{"tool_use_id": "toolu_synthetic_read", "duration_ms": int64(12)},
		},
	}})
	if len(entries) != 1 {
		t.Fatalf("count = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Path != nil || entry.Availability.Path != fileAvailabilityUnavailable {
		t.Fatalf("path must be unavailable: %#v", entry)
	}
	assertStringPtr(t, entry.OperationID, "claude-code:session:tool:toolu_synthetic_read")
	assertStringPtr(t, entry.Action, FileActionRead)
	if entry.Availability.Action != fileAvailabilityObserved {
		t.Fatalf("action availability = %q", entry.Availability.Action)
	}
}

func TestSessionFilesProjectsCodexWriteWithoutPath(t *testing.T) {
	occurred := time.Date(2026, 9, 11, 9, 6, 40, 500000000, time.UTC)
	entries := SessionFilesFromEvidence([]canonical.Event{{
		EventID: "codex-write", EventType: "codex.tool_result", OccurredAt: occurred,
		Provider: "openai", Tool: "codex",
		Attributes: map[string]any{
			"operation_id": "codex:session:tool:call-1",
			"category":     "filesystem write",
			"duration_ms":  int64(40),
			"outcome":      "success",
		},
	}}, nil)
	if len(entries) != 1 {
		t.Fatalf("count = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Path != nil || entry.Availability.Path != fileAvailabilityUnavailable {
		t.Fatalf("codex path must stay unavailable: %#v", entry)
	}
	assertStringPtr(t, entry.Action, FileActionWrite)
	assertStringPtr(t, entry.OperationID, "codex:session:tool:call-1")
}

func TestSessionFilesIgnoresShellLocAndGovernancePath(t *testing.T) {
	occurred := time.Date(2026, 9, 11, 9, 6, 40, 500000000, time.UTC)
	entries := SessionFilesFromEvidence([]canonical.Event{
		{
			EventID: "shell", EventType: "codex.tool_result", OccurredAt: occurred,
			Attributes: map[string]any{"category": "shell command", "operation_id": "op-shell"},
		},
		{
			EventID: "loc", EventType: "claude_code.lines_of_code.count", OccurredAt: occurred.Add(time.Second),
			Attributes: map[string]any{"lines_added_count": "12", "lines_removed_count": "3"},
		},
		{
			EventID: "governance-path", EventType: "api_request", OccurredAt: occurred.Add(2 * time.Second),
			Attributes: map[string]any{"file_path": "/workspace/.env"},
		},
	}, nil)
	if len(entries) != 0 {
		t.Fatalf("count = %d, want 0 (%#v)", len(entries), entries)
	}
}

func TestSessionFilesLabelsMissingPathWhenOperationMatched(t *testing.T) {
	occurred := time.Date(2026, 9, 11, 9, 6, 40, 500000000, time.UTC)
	entries := SessionFilesFromEvidence([]canonical.Event{{
		EventID: "partial", EventType: "claude_code.tool", OccurredAt: occurred,
		Provider: "anthropic", Tool: "claude-code",
		Attributes: map[string]any{
			"tool": map[string]any{"tool_name": "CustomTool", "tool_use_id": "x"},
		},
	}}, []canonical.Operation{{
		OperationID: "op-unknown-fs",
		Category:    canonical.OperationCategoryFilesystemRead,
		ProviderExtensions: map[string]any{
			"event": map[string]any{"tool_use_id": "x"},
		},
	}})
	if len(entries) != 1 {
		t.Fatalf("count = %d, want 1", len(entries))
	}
	if entries[0].Path != nil || entries[0].Availability.Path != fileAvailabilityUnavailable {
		t.Fatalf("path = %#v", entries[0])
	}
}

func TestPageSessionFiles(t *testing.T) {
	entries := []SessionFileEntry{
		{EventID: "a", OccurredAt: "2026-01-01T00:00:00Z"},
		{EventID: "b", OccurredAt: "2026-01-01T00:00:01Z"},
		{EventID: "c", OccurredAt: "2026-01-01T00:00:02Z"},
	}
	page, last := PageSessionFiles(entries, 2, "", "")
	if len(page) != 2 || last == nil || last.EventID != "b" {
		t.Fatalf("first page = %#v last=%#v", page, last)
	}
	page, last = PageSessionFiles(entries, 2, last.OccurredAt, last.EventID)
	if len(page) != 1 || page[0].EventID != "c" || last != nil {
		t.Fatalf("second page = %#v last=%#v", page, last)
	}
}

func assertStringPtr(t *testing.T, got *string, want string) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("got %#v, want %q", got, want)
	}
}
