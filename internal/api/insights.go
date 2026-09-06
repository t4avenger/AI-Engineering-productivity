package api

import (
	"net/http"

	"github.com/wayne/telemetryiq/internal/insights"
)

const insightEventLimit = 1000

type mcpInventoryResponse struct {
	Data insights.MCPInventory `json:"data"`
}

type skillUsageResponse struct {
	Data insights.SkillUsage `json:"data"`
}

func (a sessionAPI) mcpInventory(w http.ResponseWriter, r *http.Request) {
	if a.sessions == nil || a.eventReader == nil {
		writeSessionError(w, http.StatusServiceUnavailable, "insights_unavailable", "insight source data is unavailable")
		return
	}
	events, err := a.insightEvents(r)
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "insight_query_failed", "unable to query source data for insight")
		return
	}
	writeSessionJSON(w, http.StatusOK, mcpInventoryResponse{Data: insights.MCPInventoryFromEvents(events)})
}

func (a sessionAPI) skillUsage(w http.ResponseWriter, r *http.Request) {
	if a.sessions == nil || a.eventReader == nil {
		writeSessionError(w, http.StatusServiceUnavailable, "insights_unavailable", "insight source data is unavailable")
		return
	}
	events, err := a.insightEvents(r)
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "insight_query_failed", "unable to query source data for insight")
		return
	}
	writeSessionJSON(w, http.StatusOK, skillUsageResponse{Data: insights.SkillUsageFromEvents(events)})
}
