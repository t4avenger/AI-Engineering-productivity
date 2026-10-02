package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/agenttree"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/normalize/claude"
	"github.com/wayne/telemetryiq/internal/normalize/codex"
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
	if derefInt64(t, afterFirst.LLMRequestCount) != 1 || derefInt64(t, afterFirst.InputTokens) != 100 {
		t.Fatalf("after first batch: llm_count=%v input=%v, want 1/100", afterFirst.LLMRequestCount, afterFirst.InputTokens)
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
	if derefInt64(t, replay.LLMRequestCount) != 2 || derefInt64(t, replay.InputTokens) != 150 {
		t.Fatalf("after replay: llm_count=%v input=%v, want 2/150 (idempotent)", replay.LLMRequestCount, replay.InputTokens)
	}

	assertDeleteRemovesRelations(t, ctx, repo)
}

func TestCodexAgentRelationsRebuildAfterDeleteSession(t *testing.T) {
	for _, test := range codexAgentMutationCases() {
		t.Run(test.name, func(t *testing.T) {
			repo := saveCodexAgentFixture(t, codexMultiAgentFixtureEvents(t))
			if err := repo.DeleteSession(context.Background(), test.targetSession); err != nil {
				t.Fatal(err)
			}
			assertCodexAgentGraph(t, repo, test.want)
		})
	}
}

func TestCodexAgentRelationsRebuildAfterRetention(t *testing.T) {
	for _, test := range codexAgentMutationCases() {
		t.Run(test.name, func(t *testing.T) {
			events := codexMultiAgentFixtureEvents(t)
			for i := range events {
				if events[i].SessionID == test.targetSession {
					events[i].OccurredAt = time.Date(2026, 8, 1, 0, 0, i, 0, time.UTC)
				} else {
					events[i].OccurredAt = time.Date(2026, 10, 1, 0, 0, i, 0, time.UTC)
				}
			}
			repo := saveCodexAgentFixture(t, events)
			deleted, err := repo.ApplyRetention(context.Background(), 30, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))
			if err != nil || deleted != 1 {
				t.Fatalf("ApplyRetention() = %d, %v, want 1, nil", deleted, err)
			}
			assertCodexAgentGraph(t, repo, test.want)
		})
	}
}

type codexAgentNode struct {
	agent, parent string
	state         agenttree.ParentState
}

type codexAgentMutationCase struct {
	name, targetSession string
	want                map[string][]codexAgentNode
}

func codexAgentMutationCases() []codexAgentMutationCase {
	return []codexAgentMutationCase{
		{
			name: "root", targetSession: codexFixtureSession("235"),
			want: map[string][]codexAgentNode{
				codexFixtureSession("236"): {{agent: "236", parent: "235", state: agenttree.ParentAgentNotRetained}},
				codexFixtureSession("237"): {
					{agent: "237", parent: "235", state: agenttree.ParentAgentNotRetained},
					{agent: "239", parent: "237", state: agenttree.ParentAgentObserved},
				},
				codexFixtureSession("238"): {{agent: "238", parent: "235", state: agenttree.ParentAgentNotRetained}},
			},
		},
		{
			name: "nested parent", targetSession: codexFixtureSession("237"),
			want: map[string][]codexAgentNode{
				codexFixtureSession("235"): {
					{agent: "236", state: agenttree.MainSessionObserved},
					{agent: "238", state: agenttree.MainSessionObserved},
				},
				codexFixtureSession("239"): {{agent: "239", parent: "237", state: agenttree.ParentAgentNotRetained}},
			},
		},
	}
}

func assertCodexAgentGraph(t *testing.T, repo *Repository, want map[string][]codexAgentNode) {
	t.Helper()
	ctx := context.Background()
	all, err := repo.ListAgentRelations(ctx, storage.AgentRelationFilter{})
	if err != nil {
		t.Fatal(err)
	}
	wantCount := 0
	for sessionID, expected := range want {
		wantCount += len(expected)
		relations, err := repo.ListAgentRelations(ctx, storage.AgentRelationFilter{SessionID: sessionID})
		if err != nil {
			t.Fatal(err)
		}
		nodes := flattenAgentTree(agenttree.Build(relations, nil))
		if len(nodes) != len(expected) {
			t.Fatalf("session %s nodes = %#v, want %#v", sessionID, nodes, expected)
		}
		for _, item := range expected {
			node, ok := nodes[codexFixtureThread(item.agent)]
			if !ok || node.ParentState != item.state || relationParent(node.AgentRelation) != codexFixtureThread(item.parent) {
				t.Fatalf("session %s agent %s = %#v, want %#v", sessionID, item.agent, node, item)
			}
		}
	}
	if len(all) != wantCount {
		t.Fatalf("all relations = %d, want %d: %#v", len(all), wantCount, all)
	}
}

func flattenAgentTree(roots []agenttree.Node) map[string]agenttree.Node {
	result := map[string]agenttree.Node{}
	var visit func([]agenttree.Node)
	visit = func(nodes []agenttree.Node) {
		for _, node := range nodes {
			result[node.AgentID] = node
			visit(node.Children)
		}
	}
	visit(roots)
	return result
}

func relationParent(relation canonical.AgentRelation) string {
	if relation.ParentAgentID == nil {
		return ""
	}
	return *relation.ParentAgentID
}

func codexFixtureSession(suffix string) string {
	return "codex:" + codexFixtureThread(suffix)
}

func codexFixtureThread(suffix string) string {
	if suffix == "" {
		return ""
	}
	return "00000000-0000-4000-8000-000000000" + suffix
}

func saveCodexAgentFixture(t *testing.T, events []canonical.Event) *Repository {
	t.Helper()
	repo, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.SaveEvents(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	return repo
}

func codexMultiAgentFixtureEvents(t *testing.T) []canonical.Event {
	t.Helper()
	base := filepath.Join("..", "..", "..", "fixtures", "codex", "observed-sanitised")
	names := []string{
		"codex-0.160.0-multi-agent-root.jsonl", "codex-0.160.0-multi-agent-alpha.jsonl",
		"codex-0.160.0-multi-agent-beta.jsonl", "codex-0.160.0-multi-agent-cancel.jsonl",
		"codex-0.160.0-multi-agent-gamma.jsonl",
	}
	var events []canonical.Event
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(base, name))
		if err != nil {
			t.Fatal(err)
		}
		normalized, err := codex.NormalizeRollout(data, time.Unix(0, 0).UTC())
		if err != nil {
			t.Fatalf("normalize %s: %v", name, err)
		}
		events = append(events, normalized...)
	}
	return events
}

// assertCompleteRollup verifies the second batch's span is summed into the
// existing relation: both llm_request spans counted, tokens/durations summed, and
// WallClockMs the true elapsed span rather than the summed duration.
func assertCompleteRollup(t *testing.T, complete canonical.AgentRelation) {
	t.Helper()
	if derefInt64(t, complete.LLMRequestCount) != 2 {
		t.Fatalf("llm_request_count = %v, want 2 (both batches summed)", complete.LLMRequestCount)
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
