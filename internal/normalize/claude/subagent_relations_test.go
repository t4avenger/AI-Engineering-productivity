package claude

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// subAgentReceivedAt is a fixed receive stamp so the reconstruction is
// deterministic across runs (relations carry no receivedAt, but NormalizeTraces
// requires one).
var subAgentReceivedAt = time.Date(2026, 9, 20, 10, 5, 51, 0, time.UTC)

// relationsFromFixture normalises an observed-sanitised OTLP traces fixture and
// reconstructs its sub-agent relations, exactly as the storage rebuild path does
// over a session's persisted events.
func relationsFromFixture(t *testing.T, name string) []canonical.AgentRelation {
	t.Helper()
	events, err := NormalizeTraces(tracesFixturePayload(t, name), subAgentReceivedAt)
	if err != nil {
		t.Fatalf("normalize traces: %v", err)
	}
	return ReconstructSubAgentRelations(events)
}

// TestReconstructSubAgentRelationsGolden proves the full two-level sub-agent tree
// is reconstructed from the span set: agent_a spawned by the main session
// (main_session), agent_b spawned by agent_a (sub_agent), each with its own
// token/duration rollups. It is deterministic (run twice) and byte-stable against
// the committed golden.
func TestReconstructSubAgentRelationsGolden(t *testing.T) {
	first := relationsFromFixture(t, "claude-code-2.1.268-subagent-spans-otlp.json")
	second := relationsFromFixture(t, "claude-code-2.1.268-subagent-spans-otlp.json")
	if !reflect.DeepEqual(first, second) {
		t.Fatal("reconstruction must be deterministic")
	}
	assertSubAgentTree(t, first)
	assertMatchesGolden(t, "claude-code-2.1.268-subagent-spans.relations.json", first)
}

// assertSubAgentTree checks the reconstructed rollups against the hand-computed
// expectations for the fixture, so the golden is anchored to a reasoned result
// rather than only to itself.
func assertSubAgentTree(t *testing.T, relations []canonical.AgentRelation) {
	t.Helper()
	if len(relations) != 2 {
		t.Fatalf("relation count = %d, want 2 (agent_a, agent_b)", len(relations))
	}
	byAgent := map[string]canonical.AgentRelation{}
	for _, relation := range relations {
		assertRelationCommon(t, relation)
		byAgent[relation.AgentID] = relation
	}

	agentA, ok := byAgent["agent_a"]
	if !ok {
		t.Fatalf("missing agent_a in %#v", relations)
	}
	assertAgentA(t, agentA)

	agentB, ok := byAgent["agent_b"]
	if !ok {
		t.Fatalf("missing agent_b in %#v", relations)
	}
	assertAgentB(t, agentB)
}

// assertRelationCommon checks the provider-native fields every reconstructed
// relation in the golden fixture must carry, regardless of which agent it is.
func assertRelationCommon(t *testing.T, relation canonical.AgentRelation) {
	t.Helper()
	if relation.SchemaVersion != canonical.RecordSchemaVersion {
		t.Fatalf("%s schema_version = %q", relation.AgentID, relation.SchemaVersion)
	}
	if relation.Provider != provider || relation.Tool != tool {
		t.Fatalf("%s provider/tool = %q/%q", relation.AgentID, relation.Provider, relation.Tool)
	}
	if relation.SessionID != "claude-code:00000000-0000-4000-8000-000000000201" {
		t.Fatalf("%s session id = %q, want raw provider-native id", relation.AgentID, relation.SessionID)
	}
	if relation.Provenance != canonical.ProvenanceObserved {
		t.Fatalf("%s provenance = %q, want observed", relation.AgentID, relation.Provenance)
	}
}

// assertAgentA checks agent_a: the main-session-spawned root sub-agent with its
// full token/duration rollup across three spans (1 llm_request, 2 tool).
func assertAgentA(t *testing.T, agentA canonical.AgentRelation) {
	t.Helper()
	if agentA.RelationID != "00000000000000000000000000000201:agent_a" {
		t.Fatalf("agent_a relation_id = %q, want trace-namespaced id", agentA.RelationID)
	}
	if agentA.ParentKind != canonical.ParentKindMainSession {
		t.Fatalf("agent_a parent_kind = %q, want main_session (spawned by main session)", agentA.ParentKind)
	}
	if agentA.ParentAgentID != nil {
		t.Fatalf("agent_a parent_agent_id = %v, want nil (no parent agent)", *agentA.ParentAgentID)
	}
	assertStringPtr(t, "agent_a.subagent_type", agentA.SubagentType, "code-reviewer")
	assertStringPtr(t, "agent_a.workflow_run_id", agentA.WorkflowRunID, "wf_synthetic0001")
	assertStringPtr(t, "agent_a.workflow_name", agentA.WorkflowName, "custom")
	if agentA.SpanCount != 3 || agentA.LLMRequestCount != 1 || agentA.ToolCount != 2 {
		t.Fatalf("agent_a counts span/llm/tool = %d/%d/%d, want 3/1/2", agentA.SpanCount, agentA.LLMRequestCount, agentA.ToolCount)
	}
	assertInt64Ptr(t, "agent_a.input_tokens", agentA.InputTokens, 100)
	assertInt64Ptr(t, "agent_a.output_tokens", agentA.OutputTokens, 200)
	assertInt64Ptr(t, "agent_a.cache_read_tokens", agentA.CacheReadTokens, 10)
	assertInt64Ptr(t, "agent_a.cache_creation_tokens", agentA.CacheCreationTokens, 20)
	assertInt64Ptr(t, "agent_a.llm_duration_ms_total", agentA.LLMDurationMsTotal, 1500)
	assertInt64Ptr(t, "agent_a.tool_duration_ms_total", agentA.ToolDurationMsTotal, 2300)
	// WallClockMs is the elapsed span (601000ms→605000ms = 4000ms), deliberately
	// less than the summed durations (1500+2300 = 3800ms of overlapping work) so a
	// summed value is never mistaken for wall-clock.
	assertInt64Ptr(t, "agent_a.wall_clock_ms", agentA.WallClockMs, 4000)
}

// assertAgentB checks agent_b: the child sub-agent spawned by agent_a, with a
// single llm_request span and cache/tool fields deliberately absent (nil, never a
// fabricated zero).
func assertAgentB(t *testing.T, agentB canonical.AgentRelation) {
	t.Helper()
	if agentB.ParentKind != canonical.ParentKindSubAgent {
		t.Fatalf("agent_b parent_kind = %q, want sub_agent (spawned by agent_a)", agentB.ParentKind)
	}
	assertStringPtr(t, "agent_b.parent_agent_id", agentB.ParentAgentID, "agent_a")
	if agentB.SubagentType != nil {
		t.Fatalf("agent_b subagent_type = %q, want nil (never observed)", *agentB.SubagentType)
	}
	if agentB.SpanCount != 1 || agentB.LLMRequestCount != 1 || agentB.ToolCount != 0 {
		t.Fatalf("agent_b counts span/llm/tool = %d/%d/%d, want 1/1/0", agentB.SpanCount, agentB.LLMRequestCount, agentB.ToolCount)
	}
	assertInt64Ptr(t, "agent_b.input_tokens", agentB.InputTokens, 50)
	assertInt64Ptr(t, "agent_b.output_tokens", agentB.OutputTokens, 80)
	if agentB.CacheReadTokens != nil || agentB.CacheCreationTokens != nil {
		t.Fatalf("agent_b cache tokens must stay nil (never observed): read=%v creation=%v", agentB.CacheReadTokens, agentB.CacheCreationTokens)
	}
	assertInt64Ptr(t, "agent_b.llm_duration_ms_total", agentB.LLMDurationMsTotal, 800)
	if agentB.ToolDurationMsTotal != nil {
		t.Fatalf("agent_b tool_duration_ms_total = %d, want nil (no tool spans)", *agentB.ToolDurationMsTotal)
	}
	assertInt64Ptr(t, "agent_b.wall_clock_ms", agentB.WallClockMs, 800)
}

// TestReconstructSubAgentRelationsMultiTracePerSession proves the (trace_id,
// agent_id) key: two traces in one session that reuse the same agent_id yield two
// distinct relations, never a collision that would fold one agent's rollup into
// the other. This is the regression guard for bare-agent_id keying.
func TestReconstructSubAgentRelationsMultiTracePerSession(t *testing.T) {
	const sessionID = "00000000-0000-4000-8000-0000000003ff"
	var events []canonical.Event
	for _, traceID := range []string{
		"00000000000000000000000000000301",
		"00000000000000000000000000000302",
	} {
		payload := singleAgentSpanPayload(traceID, sessionID, "agent_a", 30)
		traceEvents, err := NormalizeTraces(payload, subAgentReceivedAt)
		if err != nil {
			t.Fatalf("normalize trace %s: %v", traceID, err)
		}
		events = append(events, traceEvents...)
	}
	relations := ReconstructSubAgentRelations(events)
	if len(relations) != 2 {
		t.Fatalf("relation count = %d, want 2 (one per trace, no collision)", len(relations))
	}
	ids := map[string]struct{}{}
	for _, relation := range relations {
		if relation.AgentID != "agent_a" {
			t.Fatalf("agent id = %q, want agent_a", relation.AgentID)
		}
		ids[relation.RelationID] = struct{}{}
		assertInt64Ptr(t, relation.RelationID+".input_tokens", relation.InputTokens, 30)
	}
	if len(ids) != 2 {
		t.Fatalf("relation ids = %v, want 2 distinct trace-namespaced ids", ids)
	}
}

// TestReconstructSubAgentRelationsNoSubAgents proves a main-session-only span set
// (an interaction + llm_request with no agent_id, the existing trace-spans
// fixture) reconstructs to no relations — the main session is never itself a
// sub-agent node.
func TestReconstructSubAgentRelationsNoSubAgents(t *testing.T) {
	if relations := relationsFromFixture(t, "claude-code-2.1.268-trace-spans-otlp.json"); len(relations) != 0 {
		t.Fatalf("relation count = %d, want 0 (main session carries no agent_id)", len(relations))
	}
}

// TestReconstructSubAgentRelationsIgnoresNonSpanEvents proves an event without a
// span envelope (a log/metric event that shares the session) contributes nothing,
// so the reconstruction is safe over the full mixed event set a session holds.
func TestReconstructSubAgentRelationsIgnoresNonSpanEvents(t *testing.T) {
	nonSpan := canonical.Event{
		EventID:            "claude-code:log:synthetic",
		SessionID:          "claude-code:00000000-0000-4000-8000-0000000004ff",
		Provider:           provider,
		Tool:               tool,
		ProviderExtensions: map[string]any{"log_attributes": map[string]any{"agent_id": "agent_a"}},
	}
	if relations := ReconstructSubAgentRelations([]canonical.Event{nonSpan}); relations != nil {
		t.Fatalf("relations = %#v, want nil (no span envelope)", relations)
	}
}

// singleAgentSpanPayload builds a minimal one-span OTLP traces payload: a
// claude-code llm_request span carrying agent_id and input_tokens, for the
// multi-trace keying test.
func singleAgentSpanPayload(traceID, sessionID, agentID string, inputTokens int) []byte {
	return []byte(fmt.Sprintf(`{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeSpans":[{"spans":[{"traceId":%q,"spanId":"0000000000000b01","name":"claude_code.llm_request","kind":1,"startTimeUnixNano":"1789117601000000000","endTimeUnixNano":"1789117602000000000","attributes":[{"key":"session.id","value":{"stringValue":%q}},{"key":"span.type","value":{"stringValue":"llm_request"}},{"key":"agent_id","value":{"stringValue":%q}},{"key":"input_tokens","value":{"intValue":%d}}]}]}]}]}`, traceID, sessionID, agentID, inputTokens))
}

// assertStringPtr fails unless got is non-nil and equals want.
func assertStringPtr(t *testing.T, name string, got *string, want string) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %q", name, want)
	}
	if *got != want {
		t.Fatalf("%s = %q, want %q", name, *got, want)
	}
}

// assertInt64Ptr fails unless got is non-nil and equals want.
func assertInt64Ptr(t *testing.T, name string, got *int64, want int64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %d", name, want)
	}
	if *got != want {
		t.Fatalf("%s = %d, want %d", name, *got, want)
	}
}
