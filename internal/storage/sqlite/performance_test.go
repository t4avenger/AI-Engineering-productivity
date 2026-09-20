package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/cost"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
)

func TestOpenAppliesWALPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.db")
	repo, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	var journal string
	if err := repo.db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if !strings.EqualFold(journal, "wal") {
		t.Fatalf("journal_mode = %q, want wal", journal)
	}
	var synchronous int
	if err := repo.db.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil {
		t.Fatalf("synchronous: %v", err)
	}
	if synchronous != 1 { // NORMAL
		t.Fatalf("synchronous = %d, want 1 (NORMAL)", synchronous)
	}
	var busy int
	if err := repo.db.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}
	if busy != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000", busy)
	}
	var version string
	if err := repo.db.QueryRow("SELECT sqlite_version()").Scan(&version); err != nil {
		t.Fatalf("sqlite_version: %v", err)
	}
	if !sqliteVersionAtLeast(version, 3, 51, 3) {
		t.Fatalf("sqlite_version = %q, want >= 3.51.3", version)
	}
}

func TestSessionListUsesStartedAtIndex(t *testing.T) {
	repo, err := Open(filepath.Join(t.TempDir(), "plan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	if err := repo.SaveEvents(context.Background(), canonicalEvent(t, "indexed-session", "2026-01-02T10:00:00Z")); err != nil {
		t.Fatal(err)
	}
	query, args, err := sessionListQuery(storage.SessionFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	plan := explainQueryPlan(t, repo, query, args...)
	joined := strings.ToLower(strings.Join(plan, "\n"))
	if strings.Contains(joined, "scan sessions") && !strings.Contains(joined, "using index") && !strings.Contains(joined, "covering index") {
		t.Fatalf("expected indexed sessions access, plan=%v", plan)
	}
	if strings.Contains(joined, "use temp b-tree for order by") {
		t.Fatalf("unexpected temp sort, plan=%v", plan)
	}
}

func TestApplyRetentionDeletesExpiredSessions(t *testing.T) {
	repo, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	oldEvent := event(t, "old-event", "old-session", "session.completed", "2020-01-01T00:00:00Z")
	newEvent := event(t, "new-event", "new-session", "session.completed", "2026-01-15T00:00:00Z")
	if err := repo.SaveEvents(context.Background(), []canonical.Event{oldEvent, newEvent}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC)
	deleted, err := repo.ApplyRetention(context.Background(), 30, now)
	if err != nil {
		t.Fatalf("ApplyRetention() error = %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	sessions, err := repo.ListSessions(context.Background(), storage.SessionFilter{Limit: 10})
	if err != nil || len(sessions) != 1 || sessions[0].SessionID != "new-session" {
		t.Fatalf("sessions after retention = %#v, %v", sessions, err)
	}
}

func TestSummarizeCostsAggregatesWithoutFullScanSemantics(t *testing.T) {
	calculator, err := cost.LoadDefault("")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := Open(":memory:", calculator)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	e := event(t, "cost-event", "cost-session", "session.completed", "2026-01-02T10:00:00Z")
	e.Attributes = map[string]any{"model": "unpriced", "input_token_count": "10"}
	if err := repo.SaveEvents(context.Background(), []canonical.Event{e}); err != nil {
		t.Fatal(err)
	}
	summary, err := repo.SummarizeCosts(context.Background())
	if err != nil {
		t.Fatalf("SummarizeCosts() error = %v", err)
	}
	if summary.Currency == "" || summary.Statuses["unknown_price"] != 1 {
		t.Fatalf("summary = %#v", summary)
	}
	records, err := repo.ListCostRecords(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	var wantAmount int64
	hasAmount := false
	for _, record := range records {
		if record.AmountMicrousd != nil {
			hasAmount = true
			wantAmount += *record.AmountMicrousd
		}
	}
	if hasAmount {
		if summary.CalculatedAmountMicrousd == nil || *summary.CalculatedAmountMicrousd != wantAmount {
			t.Fatalf("amount = %#v, want %d", summary.CalculatedAmountMicrousd, wantAmount)
		}
	} else if summary.CalculatedAmountMicrousd != nil {
		t.Fatalf("amount = %#v, want nil", summary.CalculatedAmountMicrousd)
	}
}

func TestSessionHydratesLastEventAtFromColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hydrate.db")
	repo, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	if err := repo.SaveEvents(context.Background(), []canonical.Event{
		event(t, "early", "hydrate-session", "session.created", "2026-01-02T10:00:00Z"),
		event(t, "late", "hydrate-session", "session.completed", "2026-01-02T12:45:00Z"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Exec(`UPDATE sessions SET session_json = json_remove(session_json, '$.attributes.last_event_at') WHERE session_id='hydrate-session'`); err != nil {
		t.Fatal(err)
	}
	session, found, err := repo.Session(context.Background(), "hydrate-session")
	if err != nil || !found {
		t.Fatalf("Session() = found=%v err=%v", found, err)
	}
	raw, _ := session.Attributes["last_event_at"].(string)
	if !strings.HasPrefix(raw, "2026-01-02T12:45:00") {
		t.Fatalf("Session last_event_at = %q, want column hydration", raw)
	}
	listed, err := repo.ListSessions(context.Background(), storage.SessionFilter{Limit: 10})
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListSessions() = %#v, %v", listed, err)
	}
	listedRaw, _ := listed[0].Attributes["last_event_at"].(string)
	if !strings.HasPrefix(listedRaw, "2026-01-02T12:45:00") {
		t.Fatalf("ListSessions last_event_at = %q, want column hydration", listedRaw)
	}
}

func TestOpenDoesNotRebuildAllSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "norebuild.db")
	repo, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEvents(context.Background(), canonicalEvent(t, "persist-session", "2026-01-02T10:00:00Z")); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	seed, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.db.Exec(`UPDATE sessions SET session_json='{"schema_version":"0.1.0","session_id":"persist-session","provider":"openai","tool":"codex","state":"tampered","started_at":"2026-01-02T10:00:00Z","attributes":{},"provider_extensions":{}}', state='tampered' WHERE session_id='persist-session'`); err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	session, found, err := reopened.Session(context.Background(), "persist-session")
	if err != nil || !found {
		t.Fatalf("session = %v, %v", found, err)
	}
	if session.State != "tampered" {
		t.Fatalf("state = %q, want tampered (Open must not rebuild)", session.State)
	}
}

func explainQueryPlan(t *testing.T, repo *Repository, query string, args ...any) []string {
	t.Helper()
	rows, err := repo.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var selectID, order, from int
		var detail string
		if err := rows.Scan(&selectID, &order, &from, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(plan) == 0 {
		t.Fatal("empty query plan")
	}
	return plan
}

func canonicalEvent(t testing.TB, sessionID, occurredAt string) []canonical.Event {
	t.Helper()
	return []canonical.Event{event(t, sessionID+"-event", sessionID, "session.completed", occurredAt)}
}

func sqliteVersionAtLeast(version string, major, minor, patch int) bool {
	var gotMajor, gotMinor, gotPatch int
	if _, err := fmt.Sscanf(version, "%d.%d.%d", &gotMajor, &gotMinor, &gotPatch); err != nil {
		return false
	}
	if gotMajor != major {
		return gotMajor > major
	}
	if gotMinor != minor {
		return gotMinor > minor
	}
	return gotPatch >= patch
}

func BenchmarkListSessions(b *testing.B) {
	repo, err := Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	var events []canonical.Event
	for i := 0; i < 200; i++ {
		id := fmt.Sprintf("session-%d", i)
		occurred := time.Unix(1_700_000_000+int64(i), 0).UTC().Format(time.RFC3339)
		events = append(events, event(b, id+"-e", id, "session.completed", occurred))
	}
	if err := repo.SaveEvents(context.Background(), events); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := repo.ListSessions(context.Background(), storage.SessionFilter{Limit: 50}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListInsightSourceEvents(b *testing.B) {
	repo, err := Open(filepath.Join(b.TempDir(), "signals-bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	var events []canonical.Event
	for i := 0; i < 200; i++ {
		sessionID := fmt.Sprintf("session-%d", i)
		for j := 0; j < 20; j++ {
			occurred := time.Unix(1_700_000_000+int64(i*20+j), 0).UTC()
			events = append(events, canonical.Event{
				SchemaVersion: canonical.RecordSchemaVersion,
				EventID:       fmt.Sprintf("%s-e%d", sessionID, j),
				EventType:     "model_interaction",
				OccurredAt:    occurred,
				ReceivedAt:    occurred,
				Provider:      "anthropic",
				Tool:          "claude-code",
				SessionID:     sessionID,
				PrivacyLevel:  "operational",
				Attributes: map[string]any{
					"input_token_count":   int64(100 + j),
					"cached_input_tokens": int64(50 + j),
				},
				ProviderExtensions: map[string]any{},
			})
		}
	}
	if err := repo.SaveEvents(context.Background(), events); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := repo.ListInsightSourceEvents(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}
