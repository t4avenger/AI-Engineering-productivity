package ui

import (
	"fmt"
	"net/http"

	"github.com/wayne/telemetryiq/internal/agenttree"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
)

const agentValueNotReported = "not reported"

// agentTreeView is the Agent lane's stored sub-agent tree. Error and an empty
// Roots slice are distinct states: a failed or missing reader is never shown as
// "no sub-agents".
type agentTreeView struct {
	Loaded          bool
	Error           string
	EvidenceMissing bool
	Roots           []agentNodeView
}

type agentNodeView struct {
	AgentID            string
	Type               string
	ParentState        string
	ParentLabel        string
	Workflow           string
	Counts             string
	Stats              []agentStatView
	EvidencePath       string
	EvidenceLabel      string
	EvidenceAriaLabel  string
	EvidenceID         string
	UnresolvedEvidence []string
	Children           []agentNodeView
}

type agentStatView struct {
	Label string
	Value string
}

// loadAgentTree reads the session's stored relations independently of the event
// load, so a relation tree still renders (without evidence links) when events
// fail, and a reader failure is reported rather than shown as an empty tree.
func (s *Server) loadAgentTree(r *http.Request, id string, events []canonical.Event, eventsLoaded bool) agentTreeView {
	if s.agentRelations == nil {
		return agentTreeView{Error: "Sub-agent relations are unavailable because this store does not retain them."}
	}
	relations, err := s.agentRelations.ListAgentRelations(r.Context(), storage.AgentRelationFilter{SessionID: id})
	if err != nil {
		return agentTreeView{Error: "Sub-agent relations are unavailable because retained relations could not be loaded."}
	}
	view := agentTreeView{Loaded: true, EvidenceMissing: !eventsLoaded && len(relations) > 0}
	for _, node := range agenttree.Build(relations, events) {
		view.Roots = append(view.Roots, agentNodeFromTree(node, id, r))
	}
	return view
}

func agentNodeFromTree(node agenttree.Node, sessionID string, r *http.Request) agentNodeView {
	view := agentNodeView{
		AgentID:     node.AgentID,
		Type:        optionalAgentText(node.SubagentType),
		ParentState: string(node.ParentState),
		ParentLabel: agentParentLabel(node),
		Counts:      agentCounts(node),
		Stats:       agentStats(node),
	}
	if node.WorkflowName != nil || node.WorkflowRunID != nil {
		view.Workflow = optionalAgentText(node.WorkflowName) + " · run " + optionalAgentText(node.WorkflowRunID)
	}
	attachAgentEvidence(&view, node, sessionID, r)
	for _, child := range node.Children {
		view.Children = append(view.Children, agentNodeFromTree(child, sessionID, r))
	}
	return view
}

func agentStats(node agenttree.Node) []agentStatView {
	return []agentStatView{
		{"Input tokens", optionalAgentInt(node.InputTokens, "")},
		{"Output tokens", optionalAgentInt(node.OutputTokens, "")},
		{"Cache read tokens", optionalAgentInt(node.CacheReadTokens, "")},
		{"Cache creation tokens", optionalAgentInt(node.CacheCreationTokens, "")},
		{"Reasoning tokens", optionalAgentInt(node.ReasoningTokens, "")},
		{"Outcome", optionalAgentText(node.Outcome)},
		{"LLM time (summed)", optionalAgentInt(node.LLMDurationMsTotal, "ms")},
		{"Tool time (summed)", optionalAgentInt(node.ToolDurationMsTotal, "ms")},
		{"Wall clock (elapsed)", optionalAgentInt(node.WallClockMs, "ms")},
	}
}

func attachAgentEvidence(view *agentNodeView, node agenttree.Node, sessionID string, r *http.Request) {
	for _, evidence := range node.Evidence {
		if evidence.EventID == nil {
			if evidence.SpanID != nil {
				view.UnresolvedEvidence = append(view.UnresolvedEvidence, *evidence.SpanID)
			}
			continue
		}
		if view.EvidencePath != "" {
			continue
		}
		evidenceSessionID := sessionID
		if evidence.SessionID != nil {
			evidenceSessionID = *evidence.SessionID
		}
		view.EvidencePath = sessionInspectorPath(evidenceSessionID, *evidence.EventID, inspectorTabDetails, inspectorSourceTrace, r, false)
		view.EvidenceLabel = "Event evidence"
		view.EvidenceAriaLabel = "Open event evidence for " + node.AgentID
		view.EvidenceID = *evidence.EventID
		if evidence.SpanID != nil {
			view.EvidenceLabel = "Span evidence"
			view.EvidenceAriaLabel = "Open span evidence for " + node.AgentID
			view.EvidenceID = *evidence.SpanID
		}
	}
}

func agentCounts(node agenttree.Node) string {
	if node.Tool == "codex" {
		return optionalAgentInt(node.OperationCount, " operations")
	}
	return fmt.Sprintf("%s spans · %s LLM requests · %s tools",
		optionalAgentInt(node.SpanCount, ""), optionalAgentInt(node.LLMRequestCount, ""), optionalAgentInt(node.ToolCount, ""))
}

func agentParentLabel(node agenttree.Node) string {
	parent := optionalAgentText(node.ParentAgentID)
	switch node.ParentState {
	case agenttree.MainSessionObserved:
		return "Spawned by the main session"
	case agenttree.ParentAgentObserved:
		return "Spawned by " + parent
	case agenttree.ParentAgentNotRetained:
		return "Parent " + parent + " not retained"
	case agenttree.ParentCycle:
		return "Parent " + parent + " forms a cycle; shown as a root"
	case agenttree.ParentConflict:
		return fmt.Sprintf("Conflicting parents reported: %v", node.ParentCandidates)
	}
	if len(node.ParentCandidates) > 0 {
		return fmt.Sprintf("Parent unknown; spawning span belongs to %v", node.ParentCandidates)
	}
	return "Parent unknown"
}

func optionalAgentText(value *string) string {
	if value == nil || *value == "" {
		return agentValueNotReported
	}
	return *value
}

func optionalAgentInt(value *int64, unit string) string {
	if value == nil {
		return agentValueNotReported
	}
	return formatOptionalInt64(value, unit)
}
