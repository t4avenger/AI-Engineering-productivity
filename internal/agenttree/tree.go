// Package agenttree nests a session's stored sub-agent relations into a
// parent/child tree for read surfaces. Edges come only from provider-native ids
// (parent_agent_id within the same trace, and the observed spawning span); it
// never infers lineage from timestamps, proximity, or model names.
package agenttree

import (
	"slices"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// ParentState says how much of an agent's parent is proven by retained data.
type ParentState string

const (
	// ParentAgentObserved: parent_agent_id names a relation retained in the same trace.
	ParentAgentObserved ParentState = "parent_agent_observed"
	// ParentAgentNotRetained: parent_agent_id is present but no relation for it is retained.
	ParentAgentNotRetained ParentState = "parent_agent_not_retained"
	// ParentConflict: retained evidence names more than one parent.
	ParentConflict ParentState = "parent_conflict"
	// ParentCycle: following parent_agent_id edges loops back to this agent.
	ParentCycle ParentState = "cycle"
	// MainSessionObserved: no parent_agent_id and the observed spawning span
	// belongs to the main session (carries no agent_id).
	MainSessionObserved ParentState = "main_session_observed"
	// ParentUnknown: no parent_agent_id and no spawning span proves the parent.
	ParentUnknown ParentState = "unknown"
)

// Evidence links one of the agent's raw span ids to the retained span event
// carrying it. EventID is nil when no retained event carries that span.
type Evidence struct {
	SpanID  string  `json:"span_id"`
	EventID *string `json:"event_id"`
}

// Node is one stored relation, unchanged, plus its read-time tree position.
type Node struct {
	canonical.AgentRelation
	ParentState      ParentState `json:"parent_state"`
	ParentCandidates []string    `json:"parent_candidates"`
	Evidence         []Evidence  `json:"evidence"`
	Children         []Node      `json:"children"`
}

// Build nests relations (in stored order) into roots and children. Nodes whose
// parent is not provable stay roots with their state recorded; nothing is
// re-parented. events, when given, resolve span ids to evidence event ids.
func Build(relations []canonical.AgentRelation, events []canonical.Event) []Node {
	spanEvents := spanEventIndex(events)
	byKey := make(map[string]int, len(relations))
	for i, relation := range relations {
		byKey[relationKey(relation.TraceID, relation.AgentID)] = i
	}
	nodes := make([]Node, len(relations))
	parents := make([]int, len(relations))
	for i, relation := range relations {
		nodes[i] = Node{
			AgentRelation:    relation,
			ParentCandidates: []string{},
			Evidence:         evidenceFor(relation, spanEvents),
			Children:         []Node{},
		}
		parents[i] = classify(&nodes[i], byKey)
	}
	markCycles(nodes, parents)
	return nest(nodes, parents)
}

// classify sets the node's parent state and returns the index of its observed
// parent, or -1 when the node is a root.
func classify(node *Node, byKey map[string]int) int {
	spawn, hasSpawn := node.ProviderExtensions["spawn"].(map[string]any)
	spawner, _ := spawn["agent_id"].(string)
	if candidates := stringList(node.ProviderExtensions["parent_agent_ids"]); len(candidates) > 1 {
		node.ParentState, node.ParentCandidates = ParentConflict, withCandidate(candidates, spawner)
		return -1
	}
	if node.ParentAgentID == nil {
		switch {
		case hasSpawn && spawner == "":
			node.ParentState = MainSessionObserved
		case hasSpawn:
			node.ParentState, node.ParentCandidates = ParentUnknown, []string{spawner}
		default:
			node.ParentState = ParentUnknown
		}
		return -1
	}
	parent := *node.ParentAgentID
	if spawner != "" && spawner != parent {
		node.ParentState, node.ParentCandidates = ParentConflict, []string{parent, spawner}
		return -1
	}
	index, ok := byKey[relationKey(node.TraceID, parent)]
	if !ok {
		node.ParentState = ParentAgentNotRetained
		return -1
	}
	node.ParentState = ParentAgentObserved
	return index
}

// markCycles turns every node on a parent loop into a flagged root, keeping its
// raw parent_agent_id but drawing no edge. Each parent edge is walked once:
// a walk stops at any node an earlier walk already settled, and only nodes on
// the loop itself (not the ones leading into it) are flagged.
func markCycles(nodes []Node, parents []int) {
	const unvisited, onPath, settled = 0, 1, 2
	state := make([]int, len(nodes))
	for start := range nodes {
		path := []int{}
		current := start
		for current >= 0 && state[current] == unvisited {
			state[current] = onPath
			path = append(path, current)
			current = parents[current]
		}
		if current >= 0 && state[current] == onPath {
			flagLoop(nodes, path, current)
		}
		for _, index := range path {
			state[index] = settled
		}
	}
	for i := range nodes {
		if nodes[i].ParentState == ParentCycle {
			parents[i] = -1
		}
	}
}

// flagLoop marks the tail of path from the re-entered node onward: exactly the
// loop members, never the nodes that merely lead into the loop.
func flagLoop(nodes []Node, path []int, reentered int) {
	for i := len(path) - 1; i >= 0; i-- {
		nodes[path[i]].ParentState = ParentCycle
		if path[i] == reentered {
			return
		}
	}
}

// withCandidate adds the spawning span's owner to the conflict candidates when
// it is present and not already listed.
func withCandidate(candidates []string, spawner string) []string {
	if spawner == "" || slices.Contains(candidates, spawner) {
		return candidates
	}
	return append(candidates, spawner)
}

func nest(nodes []Node, parents []int) []Node {
	children := make([][]int, len(nodes))
	roots := []int{}
	for i, parent := range parents {
		if parent < 0 {
			roots = append(roots, i)
			continue
		}
		children[parent] = append(children[parent], i)
	}
	var build func(int) Node
	build = func(i int) Node {
		node := nodes[i]
		for _, child := range children[i] {
			node.Children = append(node.Children, build(child))
		}
		return node
	}
	tree := make([]Node, 0, len(roots))
	for _, root := range roots {
		tree = append(tree, build(root))
	}
	return tree
}

func evidenceFor(relation canonical.AgentRelation, spanEvents map[string]string) []Evidence {
	spanIDs := stringList(relation.ProviderExtensions["span_ids"])
	evidence := make([]Evidence, 0, len(spanIDs))
	for _, spanID := range spanIDs {
		item := Evidence{SpanID: spanID}
		if eventID, ok := spanEvents[relationKey(relation.TraceID, spanID)]; ok {
			item.EventID = &eventID
		}
		evidence = append(evidence, item)
	}
	return evidence
}

// spanEventIndex maps (trace_id, span_id) to the first retained event carrying
// that span envelope.
func spanEventIndex(events []canonical.Event) map[string]string {
	index := map[string]string{}
	for _, event := range events {
		span, ok := event.ProviderExtensions["span"].(map[string]any)
		if !ok {
			continue
		}
		traceID, _ := span["trace_id"].(string)
		spanID, _ := span["span_id"].(string)
		if traceID == "" || spanID == "" {
			continue
		}
		key := relationKey(traceID, spanID)
		if _, seen := index[key]; !seen {
			index[key] = event.EventID
		}
	}
	return index
}

// stringList reads a raw string list from either the in-memory []string the
// normaliser writes or the []any JSON decoding yields; non-strings are skipped.
func stringList(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && text != "" {
				out = append(out, text)
			}
		}
		return out
	}
	return nil
}

func relationKey(traceID, id string) string {
	return traceID + "\x00" + id
}
