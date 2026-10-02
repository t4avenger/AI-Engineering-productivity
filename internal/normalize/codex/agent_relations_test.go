package codex

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestReconstructAgentRelationsFromObservedMultiAgentRollouts(t *testing.T) {
	events := multiAgentFixtureEvents(t)
	relations := ReconstructAgentRelations(events)
	if len(relations) != 4 {
		t.Fatalf("relations = %d, want 4: %#v", len(relations), relations)
	}
	byID := relationsByAgent(relations)
	rootSession := "codex:00000000-0000-4000-8000-000000000235"
	alpha := requireAgentRelation(t, byID, "00000000-0000-4000-8000-000000000236")
	if alpha.SessionID != rootSession || alpha.ParentAgentID != nil || alpha.ParentKind != canonical.ParentKindMainSession {
		t.Fatalf("alpha lineage = session %q parent %#v kind %q", alpha.SessionID, alpha.ParentAgentID, alpha.ParentKind)
	}
	assertAgentRelationValue(t, "alpha input", alpha.InputTokens, 15584)
	assertAgentRelationValue(t, "alpha operations", alpha.OperationCount, 1)
	assertAgentRelationValue(t, "alpha duration", alpha.WallClockMs, 12000)
	assertAgentRelationString(t, "alpha outcome", alpha.Outcome, "completed")

	beta := requireAgentRelation(t, byID, "00000000-0000-4000-8000-000000000237")
	if beta.ParentAgentID != nil || beta.SessionID != rootSession {
		t.Fatalf("beta must be a direct child of the root: %#v", beta)
	}
	gamma := requireAgentRelation(t, byID, "00000000-0000-4000-8000-000000000239")
	assertAgentRelationString(t, "gamma parent", gamma.ParentAgentID, beta.AgentID)
	assertAgentRelationValue(t, "gamma reasoning", gamma.ReasoningTokens, 0)
	assertAgentRelationValue(t, "gamma operations", gamma.OperationCount, 1)
	assertAgentRelationString(t, "gamma outcome", gamma.Outcome, "completed")

	cancelled := requireAgentRelation(t, byID, "00000000-0000-4000-8000-000000000238")
	assertAgentRelationString(t, "cancel outcome", cancelled.Outcome, "interrupted")
	if cancelled.OperationCount != nil || cancelled.WallClockMs != nil {
		t.Fatalf("cancelled unreported rollups must stay nil: %#v", cancelled)
	}
	if cancelled.TraceID != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaa235" || len(agentEvidence(cancelled)) == 0 {
		t.Fatalf("cancelled relation lost trace/evidence: %#v", cancelled)
	}
	assertGoldenJSON(t, "codex-0.160.0-multi-agent.relations.json", projectAgentRelations(t, relations), "Codex multi-agent relations")
}

func TestReconstructAgentRelationsIsOrderIndependent(t *testing.T) {
	events := multiAgentFixtureEvents(t)
	want := ReconstructAgentRelations(events)
	rand.New(rand.NewSource(235)).Shuffle(len(events), func(i, j int) { events[i], events[j] = events[j], events[i] })
	if got := ReconstructAgentRelations(events); !reflect.DeepEqual(got, want) {
		t.Fatalf("shuffled reconstruction differs\ngot: %#v\nwant: %#v", got, want)
	}
}

func TestReconstructAgentRelationsKeepsUnretainedParent(t *testing.T) {
	raw := []byte("{\"timestamp\":\"2026-10-02T19:30:00Z\",\"type\":\"session_meta\",\"payload\":{\"id\":\"orphan-child\",\"cli_version\":\"0.160.0\",\"source\":{\"subagent\":{\"thread_spawn\":{\"parent_thread_id\":\"missing-parent\",\"depth\":2,\"agent_path\":\"/root/missing/orphan\"}}}}}\n" +
		"{\"timestamp\":\"2026-10-02T19:30:01Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"task_started\",\"turn_id\":\"orphan-turn\",\"trace_id\":\"orphan-trace\"}}\n")
	events, err := NormalizeRollout(raw, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	relations := ReconstructAgentRelations(events)
	if len(relations) != 1 || relations[0].ParentAgentID == nil || *relations[0].ParentAgentID != "missing-parent" {
		t.Fatalf("orphan relation = %#v", relations)
	}
}

func multiAgentFixtureEvents(t *testing.T) []canonical.Event {
	t.Helper()
	base := filepath.Join(codexFixturesDir(t), "observed-sanitised")
	names := []string{
		"codex-0.160.0-multi-agent-root.jsonl",
		"codex-0.160.0-multi-agent-alpha.jsonl",
		"codex-0.160.0-multi-agent-beta.jsonl",
		"codex-0.160.0-multi-agent-cancel.jsonl",
		"codex-0.160.0-multi-agent-gamma.jsonl",
	}
	var events []canonical.Event
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(base, name))
		if err != nil {
			t.Fatal(err)
		}
		normalized, err := NormalizeRollout(data, time.Unix(0, 0).UTC())
		if err != nil {
			t.Fatalf("normalize %s: %v", name, err)
		}
		events = append(events, normalized...)
	}
	return events
}

func relationsByAgent(relations []canonical.AgentRelation) map[string]canonical.AgentRelation {
	result := make(map[string]canonical.AgentRelation, len(relations))
	for _, relation := range relations {
		result[relation.AgentID] = relation
	}
	return result
}

func requireAgentRelation(t *testing.T, relations map[string]canonical.AgentRelation, id string) canonical.AgentRelation {
	t.Helper()
	relation, ok := relations[id]
	if !ok {
		t.Fatalf("missing relation %s: %#v", id, relations)
	}
	return relation
}

func assertAgentRelationValue(t *testing.T, name string, got *int64, want int64) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("%s = %v, want %d", name, got, want)
	}
}

func assertAgentRelationString(t *testing.T, name string, got *string, want string) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("%s = %v, want %q", name, got, want)
	}
}

func agentEvidence(relation canonical.AgentRelation) []map[string]any {
	evidence, _ := relation.ProviderExtensions["evidence_events"].([]map[string]any)
	return evidence
}

type agentRelationProjection struct {
	AgentID         string  `json:"agent_id"`
	ParentAgentID   *string `json:"parent_agent_id"`
	SessionID       string  `json:"session_id"`
	TraceID         string  `json:"trace_id"`
	Outcome         *string `json:"outcome"`
	OperationCount  *int64  `json:"operation_count"`
	InputTokens     *int64  `json:"input_tokens"`
	OutputTokens    *int64  `json:"output_tokens"`
	CachedTokens    *int64  `json:"cached_tokens"`
	ReasoningTokens *int64  `json:"reasoning_tokens"`
	ToolDurationMs  *int64  `json:"tool_duration_ms"`
	WallClockMs     *int64  `json:"wall_clock_ms"`
	Depth           *int64  `json:"depth"`
	AgentPath       string  `json:"agent_path"`
	EvidenceCount   int     `json:"evidence_count"`
}

func projectAgentRelations(t *testing.T, relations []canonical.AgentRelation) []agentRelationProjection {
	t.Helper()
	projection := make([]agentRelationProjection, 0, len(relations))
	for _, relation := range relations {
		encoded, err := json.Marshal(relation.ProviderExtensions["codex"])
		if err != nil {
			t.Fatal(err)
		}
		var extension struct {
			Depth     *int64 `json:"depth"`
			AgentPath string `json:"agent_path"`
		}
		if err := json.Unmarshal(encoded, &extension); err != nil {
			t.Fatal(err)
		}
		projection = append(projection, agentRelationProjection{
			AgentID: relation.AgentID, ParentAgentID: relation.ParentAgentID,
			SessionID: relation.SessionID, TraceID: relation.TraceID, Outcome: relation.Outcome,
			OperationCount: relation.OperationCount, InputTokens: relation.InputTokens,
			OutputTokens: relation.OutputTokens, CachedTokens: relation.CacheReadTokens,
			ReasoningTokens: relation.ReasoningTokens, ToolDurationMs: relation.ToolDurationMsTotal,
			WallClockMs: relation.WallClockMs, Depth: extension.Depth, AgentPath: extension.AgentPath,
			EvidenceCount: len(agentEvidence(relation)),
		})
	}
	return projection
}
