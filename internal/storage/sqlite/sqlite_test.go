package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/privacy"
)

func TestPersistenceAcceptance(t *testing.T) {
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
	assertMigration(t, repo)

	completed := event("event-completed", "session-1", "session.completed", "2026-01-02T10:00:00Z")
	created := event("event-created", "session-1", "session.created", "2026-01-02T09:00:00Z")
	if err := repo.SaveEvents(ctx, []canonical.Event{completed, created, completed}); err != nil {
		t.Fatal(err)
	}
	assertReconstructedSession(t, ctx, repo, created, completed)
	if err := repo.DeleteSession(ctx, "session-1"); err != nil {
		t.Fatal(err)
	}
	assertSessionDeleted(t, ctx, repo)
}

func assertMigration(t *testing.T, repo *Repository) {
	t.Helper()
	var migrationVersion int
	if err := repo.db.QueryRow("SELECT version FROM schema_migrations").Scan(&migrationVersion); err != nil {
		t.Fatal(err)
	}
	if migrationVersion != 1 {
		t.Fatalf("migration version = %d, want 1", migrationVersion)
	}
}

func assertReconstructedSession(t *testing.T, ctx context.Context, repo *Repository, created, completed canonical.Event) {
	t.Helper()
	session, found, err := repo.Session(ctx, "session-1")
	if err != nil || !found {
		t.Fatalf("session = %v, %v", found, err)
	}
	if session.State != "completed" || !session.StartedAt.Equal(created.OccurredAt) || session.CompletedAt == nil || !session.CompletedAt.Equal(completed.OccurredAt) {
		t.Fatalf("out-of-order reconstruction = %#v", session)
	}
	if session.Attributes["event_count"] != float64(2) {
		t.Fatalf("duplicate event persisted: %#v", session.Attributes)
	}
}

func assertSessionDeleted(t *testing.T, ctx context.Context, repo *Repository) {
	t.Helper()
	if _, found, err := repo.Session(ctx, "session-1"); err != nil || found {
		t.Fatalf("deleted session = %v, %v", found, err)
	}
	var eventCount int
	if err := repo.db.QueryRow("SELECT COUNT(*) FROM events WHERE session_id=?", "session-1").Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 0 {
		t.Fatalf("deleted session retained %d events", eventCount)
	}
}

func TestPersistenceSanitizesBeforeStorage(t *testing.T) {
	sanitizer, err := privacy.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo, err := Open(":memory:", sanitizer)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	input := event("event-private", "session-private", "session.created", "2026-01-02T09:00:00Z")
	input.ProviderExtensions = map[string]any{"prompt": "synthetic secret-like prompt content"}
	if err := repo.SaveEvents(context.Background(), []canonical.Event{input}); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := repo.db.QueryRow("SELECT event_json FROM events WHERE event_id=?", input.EventID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "synthetic secret-like prompt content") || strings.Contains(stored, "\"prompt\"") {
		t.Fatalf("prohibited field reached storage: %s", stored)
	}
}

func event(id, sessionID, kind, at string) canonical.Event {
	occurred, _ := time.Parse(time.RFC3339, at)
	return canonical.Event{SchemaVersion: "0.1.0", EventID: id, EventType: kind, OccurredAt: occurred, ReceivedAt: occurred, Provider: "openai", Tool: "codex", SourceSchema: "otel", SourceVersion: "test", ActorID: "unavailable", DeviceID: "unavailable", SessionID: sessionID, PrivacyLevel: "operational", Attributes: map[string]any{}, ProviderExtensions: map[string]any{}}
}
