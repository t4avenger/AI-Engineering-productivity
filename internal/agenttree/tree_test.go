package agenttree

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func relation(traceID, agentID string, parent *string, extensions map[string]any) canonical.AgentRelation {
	return canonical.AgentRelation{TraceID: traceID, AgentID: agentID, ParentAgentID: parent, ProviderExtensions: extensions}
}

func ptr(value string) *string { return &value }

var mainSpawn = map[string]any{"spawn": map[string]any{"span_id": "s0"}}

// shape flattens a tree to "agent:state:depth" in pre-order so cases compare
// structure and state in one assertion.
func shape(nodes []Node, depth int) []string {
	out := []string{}
	for _, node := range nodes {
		out = append(out, node.AgentID+":"+string(node.ParentState)+":"+string(rune('0'+depth)))
		out = append(out, shape(node.Children, depth+1)...)
	}
	return out
}

func TestBuildParentStates(t *testing.T) {
	cases := []struct {
		name      string
		relations []canonical.AgentRelation
		want      []string
	}{
		{"empty", nil, []string{}},
		{"main session spawn nests child", []canonical.AgentRelation{
			relation("t", "a", nil, mainSpawn),
			relation("t", "b", ptr("a"), map[string]any{"spawn": map[string]any{"span_id": "s1", "agent_id": "a"}}),
		}, []string{"a:main_session_observed:0", "b:parent_agent_observed:1"}},
		{"siblings keep stored order", []canonical.AgentRelation{
			relation("t", "a", nil, mainSpawn), relation("t", "b", nil, mainSpawn),
		}, []string{"a:main_session_observed:0", "b:main_session_observed:0"}},
		{"no spawn span is unknown", []canonical.AgentRelation{relation("t", "a", nil, nil)},
			[]string{"a:unknown:0"}},
		{"spawn by another agent without parent_agent_id is unknown", []canonical.AgentRelation{
			relation("t", "a", nil, map[string]any{"spawn": map[string]any{"span_id": "s", "agent_id": "z"}}),
		}, []string{"a:unknown:0"}},
		{"parent not retained stays root", []canonical.AgentRelation{relation("t", "b", ptr("gone"), nil)},
			[]string{"b:parent_agent_not_retained:0"}},
		{"same agent id in another trace is not joined", []canonical.AgentRelation{
			relation("t1", "a", nil, mainSpawn), relation("t2", "b", ptr("a"), nil),
		}, []string{"a:main_session_observed:0", "b:parent_agent_not_retained:0"}},
		{"conflicting parent ids", []canonical.AgentRelation{
			relation("t", "a", nil, mainSpawn),
			relation("t", "c", ptr("a"), map[string]any{"parent_agent_ids": []any{"a", "b"}}),
		}, []string{"a:main_session_observed:0", "c:parent_conflict:0"}},
		{"spawner disagrees with parent_agent_id", []canonical.AgentRelation{
			relation("t", "a", nil, mainSpawn),
			relation("t", "c", ptr("a"), map[string]any{"spawn": map[string]any{"span_id": "s", "agent_id": "x"}}),
		}, []string{"a:main_session_observed:0", "c:parent_conflict:0"}},
		{"cycle and self parent are flagged roots", []canonical.AgentRelation{
			relation("t", "a", ptr("b"), nil), relation("t", "b", ptr("a"), nil),
			relation("t", "c", ptr("a"), nil), relation("t", "s", ptr("s"), nil),
		}, []string{"a:cycle:0", "c:parent_agent_observed:1", "b:cycle:0", "s:cycle:0"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shape(Build(tc.relations, nil), 0); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("tree = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildKeepsCandidatesAndRawParent(t *testing.T) {
	tree := Build([]canonical.AgentRelation{
		relation("t", "c", ptr("a"), map[string]any{"parent_agent_ids": []string{"a", "b"}}),
	}, nil)
	if !reflect.DeepEqual(tree[0].ParentCandidates, []string{"a", "b"}) || *tree[0].ParentAgentID != "a" {
		t.Fatalf("candidates = %v parent = %v, want both raw candidates and raw parent kept", tree[0].ParentCandidates, *tree[0].ParentAgentID)
	}
	withSpawner := Build([]canonical.AgentRelation{relation("t", "c", ptr("a"), map[string]any{
		"parent_agent_ids": []any{"a", "b"}, "spawn": map[string]any{"span_id": "s", "agent_id": "x"},
	})}, nil)
	if !reflect.DeepEqual(withSpawner[0].ParentCandidates, []string{"a", "b", "x"}) {
		t.Fatalf("candidates = %v, want the spawning span's owner merged into the conflict", withSpawner[0].ParentCandidates)
	}
}

// TestBuildDeepChainIsLinear guards the single-pass cycle check: a long valid
// chain nests fully with no node flagged, and a chain feeding into a loop flags
// only the loop members.
func TestBuildDeepChainIsLinear(t *testing.T) {
	const depth = 5000
	relations := []canonical.AgentRelation{relation("t", "n0", nil, mainSpawn)}
	for i := 1; i < depth; i++ {
		relations = append(relations, relation("t", fmt.Sprintf("n%d", i), ptr(fmt.Sprintf("n%d", i-1)), nil))
	}
	node, levels := Build(relations, nil)[0], 1
	for len(node.Children) == 1 {
		if node.Children[0].ParentState != ParentAgentObserved {
			t.Fatalf("level %d state = %s", levels, node.Children[0].ParentState)
		}
		node, levels = node.Children[0], levels+1
	}
	if levels != depth {
		t.Fatalf("nested levels = %d, want %d", levels, depth)
	}
	tail := shape(Build([]canonical.AgentRelation{
		relation("t", "x", ptr("y"), nil), relation("t", "y", ptr("z"), nil), relation("t", "z", ptr("y"), nil),
	}, nil), 0)
	if want := []string{"y:cycle:0", "x:parent_agent_observed:1", "z:cycle:0"}; !reflect.DeepEqual(tail, want) {
		t.Fatalf("tree = %v, want %v", tail, want)
	}
}

func TestBuildResolvesEvidenceWithinTrace(t *testing.T) {
	spanEvent := func(eventID, traceID, spanID string) canonical.Event {
		return canonical.Event{EventID: eventID, ProviderExtensions: map[string]any{
			"span": map[string]any{"trace_id": traceID, "span_id": spanID},
		}}
	}
	events := []canonical.Event{
		{EventID: "log-without-span"},
		spanEvent("other-trace", "t2", "s1"),
		spanEvent("first", "t1", "s1"),
		spanEvent("redelivered", "t1", "s1"),
	}
	tree := Build([]canonical.AgentRelation{
		relation("t1", "a", nil, map[string]any{"span_ids": []any{"s1", 7, "missing"}}),
	}, events)
	evidence := tree[0].Evidence
	if len(evidence) != 2 || evidence[0].SpanID == nil || *evidence[0].SpanID != "s1" || evidence[0].EventID == nil || *evidence[0].EventID != "first" {
		t.Fatalf("evidence = %#v, want s1 resolved to the first same-trace event", evidence)
	}
	if evidence[1].SpanID == nil || *evidence[1].SpanID != "missing" || evidence[1].EventID != nil {
		t.Fatalf("evidence[1] = %#v, want unresolved raw span id", evidence[1])
	}
}

// TestBuildLiveFixtureGolden nests the stored relations derived from the live
// 2.1.287 nested-spawn capture: delegator (main session) -> Explore.
func TestBuildLiveFixtureGolden(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "claude", "expected", "claude-code-2.1.287-subagent-spans.relations.json"))
	if err != nil {
		t.Fatal(err)
	}
	var relations []canonical.AgentRelation
	if err := json.Unmarshal(data, &relations); err != nil {
		t.Fatal(err)
	}
	got := shape(Build(relations, nil), 0)
	want := []string{"a0000000000287001:main_session_observed:0", "a0000000000287002:parent_agent_observed:1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tree = %v, want %v", got, want)
	}
}
