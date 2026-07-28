package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/privacy"
	"github.com/wayne/telemetryiq/internal/storage"
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

	completed := event(t, "event-completed", "session-1", "session.completed", "2026-01-02T10:00:00Z")
	created := event(t, "event-created", "session-1", "session.created", "2026-01-02T09:00:00Z")
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
	input := event(t, "event-private", "session-private", "session.created", "2026-01-02T09:00:00Z")
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

func TestPersistenceClearsCompletionForLaterActiveEvent(t *testing.T) {
	sanitizer, err := privacy.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo, err := Open(":memory:", sanitizer)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	events := []canonical.Event{
		event(t, "completed", "session-state", "session.completed", "2026-01-02T09:00:00Z"),
		event(t, "active", "session-state", "session.active", "2026-01-02T10:00:00Z"),
	}
	if err := repo.SaveEvents(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	session, found, err := repo.Session(context.Background(), "session-state")
	if err != nil || !found {
		t.Fatalf("session = %v, %v", found, err)
	}
	if session.State != "active" || session.CompletedAt != nil {
		t.Fatalf("inconsistent lifecycle state: %#v", session)
	}
}

func TestOpenSecuresDatabaseAndDirectory(t *testing.T) {
	sanitizer, err := privacy.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "private", "telemetry.db")
	repo, err := Open(path, sanitizer)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	assertMode(t, filepath.Dir(path), 0o700)
	assertMode(t, path, 0o600)
}

func TestListSessionsFiltersAndOrdersDeterministically(t *testing.T) {
	sanitizer, err := privacy.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo, err := Open(":memory:", sanitizer)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	early := event(t, "early", "early", "session.completed", "2026-01-02T09:00:00Z")
	early.Attributes["model"] = "model-a"
	late := event(t, "late", "late", "session.failed", "2026-01-02T11:00:00Z")
	late.Attributes["model"] = "model-b"
	if err := repo.SaveEvents(context.Background(), []canonical.Event{early, late}); err != nil {
		t.Fatal(err)
	}
	startedAfter, err := time.Parse(time.RFC3339, "2026-01-02T10:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := repo.ListSessions(context.Background(), storage.SessionFilter{Model: "model-b", Outcome: "failed", StartedAfter: &startedAfter})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "late" {
		t.Fatalf("filtered sessions = %#v", sessions)
	}
	all, err := repo.ListSessions(context.Background(), storage.SessionFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].SessionID != "late" || all[1].SessionID != "early" {
		t.Fatalf("session order = %#v", all)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

func event(t *testing.T, id, sessionID, kind, at string) canonical.Event {
	t.Helper()
	occurred, err := time.Parse(time.RFC3339, at)
	if err != nil {
		t.Fatalf("parse event time: %v", err)
	}
	return canonical.Event{SchemaVersion: "0.1.0", EventID: id, EventType: kind, OccurredAt: occurred, ReceivedAt: occurred, Provider: "openai", Tool: "codex", SourceSchema: "otel", SourceVersion: "test", ActorID: "unavailable", DeviceID: "unavailable", SessionID: sessionID, PrivacyLevel: "operational", Attributes: map[string]any{}, ProviderExtensions: map[string]any{}}
}
