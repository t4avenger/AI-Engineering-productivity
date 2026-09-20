package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/cost"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
)

func TestPersistenceAcceptance(t *testing.T) {
	ctx := context.Background()
	repo, err := Open(filepath.Join(t.TempDir(), "telemetry.db"))
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

func TestReconstructedCodexSessionCarriesEnvironmentMetadata(t *testing.T) {
	tests := []struct {
		name       string
		service    string
		version    string
		entrypoint string
	}{
		{name: "interactive", service: "codex_cli_rs", version: "0.153.4", entrypoint: "interactive"},
		{name: "exec", service: "codex_exec", version: "0.153.4", entrypoint: "codex exec"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			session := codexSessionWithEnvironment(t, tc.name, tc.service, tc.version)
			assertSessionEnvironment(t, session, tc.service, tc.version, tc.entrypoint)
			assertSessionCorrelation(t, session, "session-"+tc.name)
		})
	}
}

func TestReconstructedSessionPromotesOnlyUnambiguousPRLink(t *testing.T) {
	tests := []struct {
		name       string
		candidates []string
		wantLink   string
		wantCount  int
	}{
		{name: "one provider URL", candidates: []string{"https://gitlab.example.test/group/project/-/merge_requests/12"}, wantLink: "https://gitlab.example.test/group/project/-/merge_requests/12"},
		{name: "duplicate provider URL", candidates: []string{"https://bitbucket.org/workspace/repository/pull-requests/4", "https://bitbucket.org/workspace/repository/pull-requests/4"}, wantLink: "https://bitbucket.org/workspace/repository/pull-requests/4"},
		{name: "conflicting provider URLs", candidates: []string{"https://github.com/example/repository/pull/184", "https://dev.azure.com/org/project/_git/repository/pullrequest/9"}, wantCount: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := event(t, "pr-link-"+tc.name, "codex:pr-link-"+tc.name, "codex.tool_result", "2026-09-19T20:00:00Z")
			e.Attributes["pr_link_candidates"] = tc.candidates
			session := reconstructSession(e.SessionID, []canonical.Event{e})
			if got, _ := session.Attributes["pr_link"].(string); got != tc.wantLink {
				t.Fatalf("pr_link = %q, want %q", got, tc.wantLink)
			}
			if got, _ := session.Attributes["pr_link_candidate_count"].(int); got != tc.wantCount {
				t.Fatalf("candidate count = %d, want %d", got, tc.wantCount)
			}
		})
	}
}

func codexSessionWithEnvironment(t *testing.T, name, service, version string) canonical.Session {
	t.Helper()
	repo, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	e := event(t, "codex-"+name, "codex:session-"+name, "session.active", "2026-01-02T09:00:00Z")
	e.ProviderExtensions = map[string]any{
		"log_attributes":      map[string]any{"event.name": "codex.conversation_starts"},
		"resource_attributes": map[string]any{"service.name": service, "service.version": version},
	}
	if err := repo.SaveEvents(context.Background(), []canonical.Event{e}); err != nil {
		t.Fatal(err)
	}
	session, found, err := repo.Session(context.Background(), e.SessionID)
	if err != nil || !found {
		t.Fatalf("session = %v, %v", found, err)
	}
	return session
}

func assertSessionEnvironment(t *testing.T, session canonical.Session, service, version, entrypoint string) {
	t.Helper()
	if session.Attributes["service_name"] != service || session.Attributes["service_version"] != version || session.Attributes["entrypoint"] != entrypoint {
		t.Fatalf("session metadata = %#v", session.Attributes)
	}
	resource := session.ProviderExtensions["resource_attributes"].(map[string]any)
	if resource["service.name"] != service || resource["service.version"] != version {
		t.Fatalf("session resource metadata = %#v", resource)
	}
}

func assertSessionCorrelation(t *testing.T, session canonical.Session, providerSessionID string) {
	t.Helper()
	correlation := session.ProviderExtensions["correlation"].(map[string]any)
	if correlation["session_id_source"] != "conversation.id" || correlation["provider_session_id"] != providerSessionID {
		t.Fatalf("session correlation = %#v", correlation)
	}
}

func TestReconstructedClaudeTranscriptSessionPromotesHeaderMetadata(t *testing.T) {
	repo, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	e := event(t, "claude-transcript", "claude-code:session-header", "assistant_message", "2026-09-12T09:14:00.457Z")
	e.Provider = "anthropic"
	e.Tool = "claude-code"
	e.SourceSchema = "session_jsonl"
	e.ProviderExtensions = map[string]any{
		"transcript": map[string]any{
			"entrypoint": "cli",
			"git_branch": "main",
		},
	}
	if err := repo.SaveEvents(context.Background(), []canonical.Event{e}); err != nil {
		t.Fatal(err)
	}
	session, found, err := repo.Session(context.Background(), e.SessionID)
	if err != nil || !found {
		t.Fatalf("session = %v, %v", found, err)
	}
	if session.Attributes["entrypoint"] != "cli" || session.Attributes["git_branch"] != "main" {
		t.Fatalf("session header metadata = %#v", session.Attributes)
	}
	if _, ok := session.Attributes["pr_link"]; ok {
		t.Fatalf("must not invent pr_link: %#v", session.Attributes)
	}
}

func TestReconstructedCodexMetricSessionDoesNotFabricateConversationCorrelation(t *testing.T) {
	repo, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	e := event(t, "codex-token", "codex:token:synthetic", "codex.turn.token_usage", "2026-01-02T09:00:00Z")
	e.ProviderExtensions = map[string]any{"resource": map[string]any{"service.name": "codex_exec", "service.version": "0.153.4"}}
	if err := repo.SaveEvents(context.Background(), []canonical.Event{e}); err != nil {
		t.Fatal(err)
	}
	session, found, err := repo.Session(context.Background(), e.SessionID)
	if err != nil || !found {
		t.Fatalf("session = %v, %v", found, err)
	}
	assertSessionEnvironment(t, session, "codex_exec", "0.153.4", "codex exec")
	if _, ok := session.ProviderExtensions["correlation"]; ok {
		t.Fatalf("metric session must not fabricate conversation correlation: %#v", session.ProviderExtensions)
	}
	if session.Attributes[identityScopeKey] != identityObservation || session.Attributes[identitySourceKey] != "content-derived" {
		t.Fatalf("metric session identity = %#v", session.Attributes)
	}
}

func TestSessionIdentityScopesFilterWithoutDroppingRows(t *testing.T) {
	repo, provider, observation, unknown := identityScopeRepository(t)
	assertPrimaryIdentityScope(t, repo, provider, unknown)
	assertObservationIdentityScope(t, repo, observation)
	assertAllIdentityScopes(t, repo)
	if _, err := repo.ListSessions(context.Background(), storage.SessionFilter{Limit: 10, Scope: "invalid"}); err == nil {
		t.Fatal("expected invalid session scope error")
	}
}

func TestCodexTraceSessionIsObservation(t *testing.T) {
	repo, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	e := event(t, "codex-trace", "codex:trace:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "turn/start", "2026-09-17T19:05:53Z")
	if err := repo.SaveEvents(context.Background(), []canonical.Event{e}); err != nil {
		t.Fatal(err)
	}
	session, found, err := repo.Session(context.Background(), e.SessionID)
	if err != nil || !found {
		t.Fatalf("session = %v, %v", found, err)
	}
	if session.Attributes[identityScopeKey] != identityObservation || session.Attributes[identitySourceKey] != "trace.id" {
		t.Fatalf("trace session identity = %#v", session.Attributes)
	}
}

func identityScopeRepository(t *testing.T) (*Repository, canonical.Event, canonical.Event, canonical.Event) {
	t.Helper()
	repo, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	provider := event(t, "provider-event", "codex:provider-session", "session.active", "2026-01-02T11:00:00Z")
	provider.ProviderExtensions = map[string]any{"log_attributes": map[string]any{"event.name": "codex.conversation_starts"}}
	observation := event(t, "metric-event", "codex:token:metric-event", "codex.turn.token_usage", "2026-01-02T10:00:00Z")
	unknown := event(t, "legacy-event", "legacy-session", "session.active", "2026-01-02T09:00:00Z")
	if err := repo.SaveEvents(context.Background(), []canonical.Event{provider, observation, unknown}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Exec("UPDATE sessions SET session_json=json_remove(session_json, '$.attributes.identity_scope', '$.attributes.identity_source') WHERE session_id=?", observation.SessionID); err != nil {
		t.Fatal(err)
	}
	return repo, provider, observation, unknown
}

func assertPrimaryIdentityScope(t *testing.T, repo *Repository, provider, unknown canonical.Event) {
	t.Helper()
	primary, err := repo.ListSessions(context.Background(), storage.SessionFilter{Limit: 10, Scope: storage.SessionScopePrimary})
	if err != nil || len(primary) != 2 || primary[0].SessionID != provider.SessionID || primary[1].SessionID != unknown.SessionID {
		t.Fatalf("primary sessions = %#v, %v", primary, err)
	}
}

func assertObservationIdentityScope(t *testing.T, repo *Repository, observation canonical.Event) {
	t.Helper()
	observations, err := repo.ListSessions(context.Background(), storage.SessionFilter{Limit: 10, Scope: storage.SessionScopeObservation})
	if err != nil || len(observations) != 1 || observations[0].SessionID != observation.SessionID {
		t.Fatalf("observation sessions = %#v, %v", observations, err)
	}
	if observations[0].Attributes[identityScopeKey] != identityObservation || observations[0].Attributes[identitySourceKey] != "content-derived" {
		t.Fatalf("legacy observation identity = %#v", observations[0].Attributes)
	}
	legacyDetail, found, err := repo.Session(context.Background(), observation.SessionID)
	if err != nil || !found || legacyDetail.Attributes[identityScopeKey] != identityObservation {
		t.Fatalf("legacy observation detail = %#v, %v, %v", legacyDetail, found, err)
	}
}

func assertAllIdentityScopes(t *testing.T, repo *Repository) {
	t.Helper()
	all, err := repo.ListSessions(context.Background(), storage.SessionFilter{Limit: 10})
	if err != nil || len(all) != 3 {
		t.Fatalf("all sessions = %#v, %v", all, err)
	}
}

func TestCostRecordsPersistAndDelete(t *testing.T) {
	calculator, err := cost.LoadDefault("")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := Open(":memory:", calculator)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	e := event(t, "cost-event", "cost-session", "session.completed", "2026-01-02T09:00:00Z")
	e.Attributes = map[string]any{"model": "unpriced", "input_token_count": "10"}
	if err := repo.SaveEvents(context.Background(), []canonical.Event{e, e}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := repo.db.QueryRow("SELECT COUNT(*) FROM cost_records WHERE event_id=?", e.EventID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("cost records=%d", count)
	}
	records, err := repo.ListCostRecords(context.Background(), e.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Status != "unknown_price" {
		t.Fatalf("cost records = %#v", records)
	}
	all, err := repo.ListCostRecords(context.Background(), "")
	if err != nil || len(all) != 1 {
		t.Fatalf("all cost records = %#v, %v", all, err)
	}
	if err := repo.DeleteSession(context.Background(), e.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.QueryRow("SELECT COUNT(*) FROM cost_records").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("cost records after delete=%d", count)
	}
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

// TestPersistenceStoresEventRaw asserts the no-hiding invariant (issue #88):
// storage persists the canonical event verbatim, with no ingest-time
// sanitisation. A raw provider-native identifier and file path survive into
// event_json exactly as normalised.
func TestPersistenceStoresEventRaw(t *testing.T) {
	repo, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	input := event(t, "event-raw", "session-raw", "session.created", "2026-01-02T09:00:00Z")
	input.ProviderExtensions = map[string]any{"mcp_call": map[string]any{"server_name": "filesystem"}}
	input.Attributes = map[string]any{"file_path": "/home/dev/app/main.go"}
	if err := repo.SaveEvents(context.Background(), []canonical.Event{input}); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := repo.db.QueryRow("SELECT event_json FROM events WHERE event_id=?", input.EventID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored, "/home/dev/app/main.go") || !strings.Contains(stored, "filesystem") {
		t.Fatalf("raw values must be stored verbatim, got: %s", stored)
	}
}

func TestPersistenceClearsCompletionForLaterActiveEvent(t *testing.T) {
	repo, err := Open(":memory:")
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
	path := filepath.Join(t.TempDir(), "private", "telemetry.db")
	repo, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	assertMode(t, filepath.Dir(path), 0o700)
	assertMode(t, path, 0o600)
}

func TestListSessionsFiltersAndOrdersDeterministically(t *testing.T) {
	repo, err := Open(":memory:")
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sessions, err := repo.ListSessions(ctx, storage.SessionFilter{Model: "model-b", Outcome: "failed", StartedAfter: &startedAfter, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "late" {
		t.Fatalf("filtered sessions = %#v", sessions)
	}
	all, err := repo.ListSessions(context.Background(), storage.SessionFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].SessionID != "late" || all[1].SessionID != "early" {
		t.Fatalf("session order = %#v", all)
	}
}

func TestListSessionsModelFilterMatchesLaterEventModel(t *testing.T) {
	repo, err := Open(memoryDBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	first := event(t, "first-model", "multi-model", "session.active", "2026-01-02T09:00:00Z")
	first.Attributes["model"] = "model-a"
	second := event(t, "second-model", "multi-model", "session.completed", "2026-01-02T10:00:00Z")
	second.Attributes["model"] = "model-b"
	if err := repo.SaveEvents(context.Background(), []canonical.Event{first, second}); err != nil {
		t.Fatal(err)
	}
	sessions, err := repo.ListSessions(context.Background(), storage.SessionFilter{Model: "model-b", Limit: 10})
	if err != nil || len(sessions) != 1 || sessions[0].SessionID != "multi-model" {
		t.Fatalf("later-model filter = %#v, %v", sessions, err)
	}
	session, found, err := repo.Session(context.Background(), "multi-model")
	if err != nil || !found || session.Attributes["model"] != "model-a" {
		t.Fatalf("display model should remain first observed, got %#v found=%v err=%v", session.Attributes, found, err)
	}
}

func TestMigrationSixRunsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "once.db")
	repo, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEvents(context.Background(), []canonical.Event{event(t, "e1", "s1", "session.completed", "2026-01-02T10:00:00Z")}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	applied, err := reopened.migrationApplied(context.Background(), 6)
	if err != nil || !applied {
		t.Fatalf("migration 6 applied=%v err=%v", applied, err)
	}
	// Corrupt a denormalized column; a no-op re-open must not rewrite it via backfill.
	if _, err := reopened.db.Exec(`UPDATE sessions SET model='tampered' WHERE session_id='s1'`); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = again.Close() }()
	var model string
	if err := again.db.QueryRow(`SELECT model FROM sessions WHERE session_id='s1'`).Scan(&model); err != nil {
		t.Fatal(err)
	}
	if model != "tampered" {
		t.Fatalf("model = %q, want tampered (migration 6 must not re-backfill)", model)
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

func event(t testing.TB, id, sessionID, kind, at string) canonical.Event {
	t.Helper()
	occurred, err := time.Parse(time.RFC3339, at)
	if err != nil {
		t.Fatalf("parse event time: %v", err)
	}
	return canonical.Event{SchemaVersion: "0.1.0", EventID: id, EventType: kind, OccurredAt: occurred, ReceivedAt: occurred, Provider: "openai", Tool: "codex", SourceSchema: "otel", SourceVersion: "test", ActorID: "unavailable", DeviceID: "unavailable", SessionID: sessionID, PrivacyLevel: "operational", Attributes: map[string]any{}, ProviderExtensions: map[string]any{}}
}

func TestEventTimelineReads(t *testing.T) {
	repo, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	first := event(t, "first", "timeline", "session.active", "2026-01-02T09:00:00Z")
	second := event(t, "second", "timeline", "session.completed", "2026-01-02T10:00:00Z")
	if err := repo.SaveEvents(context.Background(), []canonical.Event{second, first}); err != nil {
		t.Fatal(err)
	}
	page, err := repo.ListEvents(context.Background(), storage.EventFilter{SessionID: "timeline", Limit: 1})
	if err != nil || len(page) != 2 || page[0].EventID != "first" {
		t.Fatalf("page = %#v, %v", page, err)
	}
	cursor := &storage.EventCursor{OccurredAt: page[0].OccurredAt, EventID: page[0].EventID}
	remaining, err := repo.ListEvents(context.Background(), storage.EventFilter{SessionID: "timeline", Cursor: cursor, Limit: 1})
	if err != nil || len(remaining) != 1 || remaining[0].EventID != "second" {
		t.Fatalf("remaining = %#v, %v", remaining, err)
	}
	if _, err := repo.ListEvents(context.Background(), storage.EventFilter{SessionID: "timeline"}); err == nil {
		t.Fatal("expected invalid limit")
	}
}

func TestGetEventScopedToSession(t *testing.T) {
	repo, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	owned := event(t, "owned", "session-a", "session.active", "2026-01-02T09:00:00Z")
	foreign := event(t, "foreign", "session-b", "session.active", "2026-01-02T09:00:00Z")
	if err := repo.SaveEvents(context.Background(), []canonical.Event{owned, foreign}); err != nil {
		t.Fatal(err)
	}
	got, found, err := repo.GetEvent(context.Background(), "session-a", "owned")
	if err != nil || !found || got.EventID != "owned" {
		t.Fatalf("owned = %#v found=%v err=%v", got, found, err)
	}
	if _, found, err := repo.GetEvent(context.Background(), "session-a", "foreign"); err != nil || found {
		t.Fatalf("foreign leak found=%v err=%v", found, err)
	}
	if _, found, err := repo.GetEvent(context.Background(), "session-a", "missing"); err != nil || found {
		t.Fatalf("missing found=%v err=%v", found, err)
	}
}

func TestOperationsPersistIdempotentlyAndDeleteWithSessions(t *testing.T) {
	repo, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	ctx := context.Background()
	operations := []canonical.Operation{{
		SchemaVersion: canonical.RecordSchemaVersion,
		OperationID:   "op-1",
		SessionID:     "session-ops",
		Provider:      "openai",
		Tool:          "codex",
		Category:      canonical.OperationCategoryShellCommand,
		Outcome:       "success",
		Provenance:    canonical.ProvenanceObserved,
		ProviderExtensions: map[string]any{"tool_call": map[string]any{
			"duration_ms": "92",
		}},
	}}
	if err := repo.SaveOperations(ctx, operations); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveOperations(ctx, operations); err != nil {
		t.Fatal(err)
	}
	got, err := repo.ListOperations(ctx, storage.OperationFilter{SessionID: "session-ops"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].OperationID != "op-1" {
		t.Fatalf("operations = %#v", got)
	}
	if err := repo.DeleteSession(ctx, "session-ops"); err != nil {
		t.Fatal(err)
	}
	got, err = repo.ListOperations(ctx, storage.OperationFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("operations after delete = %#v", got)
	}
}
