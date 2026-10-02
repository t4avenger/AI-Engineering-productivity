package ui_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func agentTreeStub(relations []canonical.AgentRelation, eventErr, relationErr error) *fullStub {
	now := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	span := canonical.Event{
		EventID: "span-event-a", EventType: "claude_code.llm_request", SessionID: "agents",
		OccurredAt: now, ReceivedAt: now, Provider: "anthropic", Tool: "claude-code",
		ProviderExtensions: map[string]any{"span": map[string]any{"trace_id": "t", "span_id": "sa"}},
	}
	return &fullStub{
		sessions:    []canonical.Session{syntheticSession("agents", now)},
		events:      map[string][]canonical.Event{"agents": {span}},
		eventErr:    eventErr,
		relations:   relations,
		relationErr: relationErr,
	}
}

func int64Ptr(value int64) *int64    { return &value }
func stringPtr(value string) *string { return &value }

func storedAgentRelations() []canonical.AgentRelation {
	return []canonical.AgentRelation{
		{
			TraceID: "t", AgentID: "agent-<a>", SubagentType: stringPtr("tiq-delegator"), SpanCount: int64Ptr(2), LLMRequestCount: int64Ptr(1), ToolCount: int64Ptr(1),
			InputTokens: int64Ptr(0), WallClockMs: int64Ptr(1200),
			ProviderExtensions: map[string]any{"spawn": map[string]any{"span_id": "s0"}, "span_ids": []any{"sa", "gone"}},
		},
		{TraceID: "t", AgentID: "agent-b", ParentAgentID: stringPtr("agent-<a>"), SpanCount: int64Ptr(1), LLMRequestCount: int64Ptr(1)},
		{TraceID: "t", AgentID: "agent-orphan", ParentAgentID: stringPtr("agent-missing")},
	}
}

func TestSessionAgentLaneRendersStoredSubAgentTree(t *testing.T) {
	body := renderSessionDetail(t, agentTreeStub(storedAgentRelations(), nil, nil), nil, "agents")
	tree := htmlSection(t, body, `<section class="agent-tree"`)
	assertContainsAll(t, tree, []string{
		"agent-&lt;a&gt;", "tiq-delegator", "Spawned by the main session",
		`data-parent-state="parent_agent_observed"`, "Spawned by agent-&lt;a&gt;",
		"Parent agent-missing not retained", "not reported",
		"<dt>Input tokens</dt><dd>0</dd>", "<dt>Output tokens</dt><dd>not reported</dd>",
		"Wall clock (elapsed)", "1200 ms", "LLM time (summed)",
		"source=trace", "Span evidence <code>sa</code>", "Evidence not retained: <code>gone</code>",
	})
	// agent-b nests inside agent-<a>'s list item; the orphan stays a root.
	nested := strings.Index(tree, `data-agent-id="agent-b"`)
	if nested < 0 || strings.Count(tree[:nested], `<ul class="agent-tree-list">`) != 2 {
		t.Fatalf("agent-b must render in a nested list under its parent: %s", tree)
	}
	if strings.Contains(body, "No observed agent turns are retained") {
		t.Fatal("a retained sub-agent tree must not be described as no agent turns")
	}
}

func TestSessionAgentLaneHonestStates(t *testing.T) {
	cases := []struct {
		name        string
		stub        *fullStub
		want, avoid string
	}{
		{"empty", agentTreeStub(nil, nil, nil), "No sub-agent relations retained for this session.", `class="agent-tree-node"`},
		{"reader error", agentTreeStub(nil, nil, errors.New("boom")), "retained relations could not be loaded", "No sub-agent relations retained"},
		{"events error keeps tree", agentTreeStub(storedAgentRelations(), errors.New("boom"), nil), "Span evidence links are unavailable", "Span evidence <code>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tree := htmlSection(t, renderSessionDetail(t, tc.stub, nil, "agents"), `<section class="agent-tree"`)
			if !strings.Contains(tree, tc.want) || strings.Contains(tree, tc.avoid) {
				t.Fatalf("tree must contain %q and not %q: %s", tc.want, tc.avoid, tree)
			}
		})
	}
}

func TestSessionAgentLaneLabelsEveryParentState(t *testing.T) {
	relations := []canonical.AgentRelation{
		{TraceID: "t", AgentID: "cyc-a", ParentAgentID: stringPtr("cyc-b")},
		{TraceID: "t", AgentID: "cyc-b", ParentAgentID: stringPtr("cyc-a")},
		{TraceID: "t", AgentID: "conflict", ProviderExtensions: map[string]any{"parent_agent_ids": []any{"p1", "p2"}}},
		{TraceID: "t", AgentID: "spawned-elsewhere", ProviderExtensions: map[string]any{"spawn": map[string]any{"span_id": "s", "agent_id": "z"}}},
		{TraceID: "t", AgentID: "bare", WorkflowName: stringPtr("custom")},
	}
	tree := htmlSection(t, renderSessionDetail(t, agentTreeStub(relations, nil, nil), nil, "agents"), `<section class="agent-tree"`)
	assertContainsAll(t, tree, []string{
		"Parent cyc-b forms a cycle; shown as a root",
		"Conflicting parents reported: [p1 p2]",
		"Parent unknown; spawning span belongs to [z]",
		"Parent unknown · Workflow custom · run not reported",
	})
}
