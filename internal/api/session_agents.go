package api

import (
	"net/http"

	"github.com/wayne/telemetryiq/internal/agenttree"
	"github.com/wayne/telemetryiq/internal/storage"
)

type agentTreeResponse struct {
	Data []agenttree.Node `json:"data"`
}

// agents returns the session's stored sub-agent relations nested into a
// parent/child tree. It is deliberately unpaged: a row page could split a
// subtree and orphan its children.
func (a sessionAPI) agents(w http.ResponseWriter, r *http.Request) {
	id, ok := a.requireSession(w, r, a.agentRelations != nil && a.eventReader != nil)
	if !ok {
		return
	}
	relations, err := a.agentRelations.ListAgentRelations(r.Context(), storage.AgentRelationFilter{SessionID: id})
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "agent_relation_query_failed", "unable to query session agent relations")
		return
	}
	events, err := a.insightSessionEvents(r, id)
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "event_query_failed", "unable to query session events")
		return
	}
	writeSessionJSON(w, http.StatusOK, agentTreeResponse{Data: agenttree.Build(relations, events)})
}
