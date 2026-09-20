package sqlite

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/normalize/claude"
	"github.com/wayne/telemetryiq/internal/storage"
)

const agentRelationSessionID = "claude-code:00000000-0000-4000-8000-0000000005ff"

// TestAgentRelationsCompleteAcrossBatches is the undercount regression guard: one
// sub-agent's spans arrive in two separate SaveEvents calls (as separate OTLP
// batches would), and the reconstructed rollup must reflect the COMPLETE summed
// set — not just the first batch. rebuildSession re-derives from the session's
// full persisted event set and REPLACEs the rows, so the second batch's spans are
// summed in; an INSERT OR IGNORE keyed on the first partial row would have frozen
// the rollup at the first batch's values.
func TestAgentRelationsCompleteAcrossBatches(t *testing.T) {
	repo, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repo.Close() }()
	ctx := context.Background()

	events := normalizeTwoSpanAgent(t)
	if len(events) != 2 {
		t.Fatalf("normalized event count = %d, want 2", len(events))
	}

	// First batch: only the first span. The relation reflects one span so far.
	if err := repo.SaveEvents(ctx, events[:1]); err != nil {
		t.Fatal(err)
	}
	afterFirst := singleRelation(t, ctx, repo)
	if afterFirst.LLMRequestCount != 1 || derefInt64(t, afterFirst.InputTokens) != 100 {
		t.Fatalf("after first batch: llm_count=%d input=%v, want 1/100", afterFirst.LLMRequestCount, afterFirst.InputTokens)
	}

	// Second batch: the remaining span. The rollup must now be complete, summing
	// both spans — this is the assertion an INSERT OR IGNORE design would fail.
	if err := repo.SaveEvents(ctx, events[1:]); err != nil {
		t.Fatal(err)
	}
	assertCompleteRollup(t, singleRelation(t, ctx, repo))

	// Replay the whole set: REPLACE is idempotent — still one relation, same rollup.
	if err := repo.SaveEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	replay := singleRelation(t, ctx, repo)
	if replay.LLMRequestCount != 2 || derefInt64(t, replay.InputTokens) != 150 {
		t.Fatalf("after replay: llm_count=%d input=%v, want 2/150 (idempotent)", replay.LLMRequestCount, replay.InputTokens)
	}

	assertDeleteRemovesRelations(t, ctx, repo)
}

// assertCompleteRollup verifies the second batch's span is summed into the
// existing relation: both llm_request spans counted, tokens/durations summed, and
// WallClockMs the true elapsed span rather than the summed duration.
func assertCompleteRollup(t *testing.T, complete canonical.AgentRelation) {
	t.Helper()
	if complete.LLMRequestCount != 2 {
		t.Fatalf("llm_request_count = %d, want 2 (both batches summed)", complete.LLMRequestCount)
	}
	if got := derefInt64(t, complete.InputTokens); got != 150 {
		t.Fatalf("input_tokens = %d, want 150 (100+50 across batches)", got)
	}
	if got := derefInt64(t, complete.LLMDurationMsTotal); got != 1500 {
		t.Fatalf("llm_duration_ms_total = %d, want 1500 (1000+500)", got)
	}
	if got := derefInt64(t, complete.WallClockMs); got != 3000 {
		t.Fatalf("wall_clock_ms = %d, want 3000 (elapsed span, not summed)", got)
	}
}

// assertDeleteRemovesRelations confirms DeleteSession clears the session's
// reconstructed relations alongside its events.
func assertDeleteRemovesRelations(t *testing.T, ctx context.Context, repo *Repository) {
	t.Helper()
	if err := repo.DeleteSession(ctx, agentRelationSessionID); err != nil {
		t.Fatal(err)
	}
	remaining, err := repo.ListAgentRelations(ctx, storage.AgentRelationFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("relations after delete = %#v, want none", remaining)
	}
}

// singleRelation asserts exactly one relation is retained for the test session
// and returns it.
func singleRelation(t *testing.T, ctx context.Context, repo *Repository) canonical.AgentRelation {
	t.Helper()
	relations, err := repo.ListAgentRelations(ctx, storage.AgentRelationFilter{SessionID: agentRelationSessionID})
	if err != nil {
		t.Fatal(err)
	}
	if len(relations) != 1 {
		t.Fatalf("relation count = %d, want 1", len(relations))
	}
	return relations[0]
}

func derefInt64(t *testing.T, value *int64) int64 {
	t.Helper()
	if value == nil {
		t.Fatal("expected a non-nil rollup value")
	}
	return *value
}

// normalizeTwoSpanAgent builds a two-span OTLP traces payload for one sub-agent
// (agent_a) in one trace and normalises it, so the storage test drives the same
// canonical events the ingest path produces. The two spans deliberately have
// distinct token/duration/timestamps so a complete rollup is distinguishable from
// a single-batch one.
func normalizeTwoSpanAgent(t *testing.T) []canonical.Event {
	t.Helper()
	span := func(spanID string, start, end int64, inputTokens, durationMs int) string {
		return fmt.Sprintf(`{"traceId":"00000000000000000000000000000501","spanId":%q,"name":"claude_code.llm_request","kind":1,"startTimeUnixNano":"%d","endTimeUnixNano":"%d","attributes":[{"key":"session.id","value":{"stringValue":"00000000-0000-4000-8000-0000000005ff"}},{"key":"span.type","value":{"stringValue":"llm_request"}},{"key":"agent_id","value":{"stringValue":"agent_a"}},{"key":"input_tokens","value":{"intValue":%d}},{"key":"duration_ms","value":{"intValue":%d}}]}`, spanID, start, end, inputTokens, durationMs)
	}
	payload := []byte(fmt.Sprintf(`{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeSpans":[{"spans":[%s,%s]}]}]}`,
		span("0000000000000c01", 1789117601000000000, 1789117602000000000, 100, 1000),
		span("0000000000000c02", 1789117603000000000, 1789117604000000000, 50, 500),
	))
	events, err := claude.NormalizeTraces(payload, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("normalize traces: %v", err)
	}
	return events
}
