package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/privacy"
	"github.com/wayne/telemetryiq/internal/storage"
)

func TestDeleteAllSessionsRemovesRetainedTelemetry(t *testing.T) {
	ctx := context.Background()
	sanitizer, err := privacy.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo, err := Open(filepath.Join(t.TempDir(), "telemetry.db"), sanitizer)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	created := event(t, "event-created", "session-1", "session.created", "2026-01-02T09:00:00Z")
	completed := event(t, "event-completed", "session-1", "session.completed", "2026-01-02T10:00:00Z")
	if err := repo.SaveEvents(ctx, []canonical.Event{created, completed}); err != nil {
		t.Fatalf("SaveEvents() error = %v", err)
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
}
