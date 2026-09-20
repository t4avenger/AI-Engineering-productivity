package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
)

func TestDeleteAllSessionsRemovesRetainedTelemetry(t *testing.T) {
	ctx := context.Background()
	repo, err := Open(filepath.Join(t.TempDir(), "telemetry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	created := event(t, "event-created", "session-1", "session.created", "2026-01-02T09:00:00Z")
	completed := event(t, "event-completed", "session-1", "session.completed", "2026-01-02T10:00:00Z")
	if err := repo.SaveEvents(ctx, []canonical.Event{created, completed}); err != nil {
		t.Fatalf("SaveEvents() error = %v", err)
	}
	if err := repo.SaveOperations(ctx, []canonical.Operation{{SchemaVersion: canonical.RecordSchemaVersion, OperationID: "op-1", SessionID: "session-1", Provider: "openai", Tool: "codex", Category: canonical.OperationCategoryShellCommand, Outcome: "success", Provenance: canonical.ProvenanceObserved, ProviderExtensions: map[string]any{}}}); err != nil {
		t.Fatalf("SaveOperations() error = %v", err)
	}
	if err := repo.SaveEvents(ctx, normalizeTwoSpanAgent(t)); err != nil {
		t.Fatalf("SaveEvents(sub-agent spans) error = %v", err)
	}
	// Precondition: the sub-agent spans must have produced relations, otherwise the
	// post-delete assertion below would pass even if rebuildSession stopped writing
	// them (0 before, 0 after) — a silent regression.
	var relationsBefore int
	if err := repo.db.QueryRow("SELECT COUNT(*) FROM agent_relations").Scan(&relationsBefore); err != nil || relationsBefore == 0 {
		t.Fatalf("agent relations before delete = %d, %v, want at least one", relationsBefore, err)
	}
	if err := repo.DeleteAllSessions(ctx); err != nil {
		t.Fatalf("DeleteAllSessions() error = %v", err)
	}
	sessions, err := repo.ListSessions(ctx, storage.SessionFilter{Limit: 10})
	if err != nil || len(sessions) != 0 {
		t.Fatalf("ListSessions() = %#v, %v", sessions, err)
	}
	var events int
	if err := repo.db.QueryRow("SELECT COUNT(*) FROM events").Scan(&events); err != nil || events != 0 {
		t.Fatalf("event count = %d, %v", events, err)
	}
	var operations int
	if err := repo.db.QueryRow("SELECT COUNT(*) FROM operations").Scan(&operations); err != nil || operations != 0 {
		t.Fatalf("operation count = %d, %v", operations, err)
	}
	var relations int
	if err := repo.db.QueryRow("SELECT COUNT(*) FROM agent_relations").Scan(&relations); err != nil || relations != 0 {
		t.Fatalf("agent relation count = %d, %v", relations, err)
	}
}
