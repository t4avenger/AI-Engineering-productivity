package api

import (
	"net/http"

	"github.com/wayne/telemetryiq/internal/insights"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
)

const insightEventLimit = 1000

type mcpInventoryResponse struct {
	Data insights.MCPInventory `json:"data"`
}

func (a sessionAPI) mcpInventory(w http.ResponseWriter, r *http.Request) {
	if a.sessions == nil || a.eventReader == nil {
		writeSessionError(w, http.StatusServiceUnavailable, "insights_unavailable", "insight source data is unavailable")
		return
	}
	sessions, err := a.sessions.ListSessions(r.Context(), storage.SessionFilter{Limit: maximumSessionLimit})
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "insight_query_failed", "unable to query sessions for insight")
		return
	}
	events := []canonical.Event{}
	for _, session := range sessions {
		sessionEvents, err := a.eventReader.ListEvents(r.Context(), storage.EventFilter{SessionID: session.SessionID, Limit: insightEventLimit})
		if err != nil {
			writeSessionError(w, http.StatusInternalServerError, "insight_query_failed", "unable to query events for insight")
			return
		}
		if len(sessionEvents) > insightEventLimit {
			sessionEvents = sessionEvents[:insightEventLimit]
		}
		events = append(events, sessionEvents...)
	}
	writeSessionJSON(w, http.StatusOK, mcpInventoryResponse{Data: insights.MCPInventoryFromEvents(events)})
}
