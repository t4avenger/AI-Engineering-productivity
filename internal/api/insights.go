package api

import (
	"net/http"

	"github.com/wayne/telemetryiq/internal/insights"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

const (
	insightEventLimit      = 1000
	insightUnavailableMsg  = "insight source data is unavailable"
	insightQueryFailedMsg  = "unable to query source data for insight"
	insightUnavailableCode = "insights_unavailable"
	insightQueryFailedCode = "insight_query_failed"
)

type mcpInventoryResponse struct {
	Data insights.MCPInventory `json:"data"`
}

type skillUsageResponse struct {
	Data insights.SkillUsage `json:"data"`
}

type modelPerformanceResponse struct {
	Data insights.ModelPerformance `json:"data"`
}

func (a sessionAPI) mcpInventory(w http.ResponseWriter, r *http.Request) {
	events, ok := a.loadInsightEvents(w, r)
	if !ok {
		return
	}
	writeSessionJSON(w, http.StatusOK, mcpInventoryResponse{Data: insights.MCPInventoryFromEvents(events)})
}

func (a sessionAPI) skillUsage(w http.ResponseWriter, r *http.Request) {
	events, ok := a.loadInsightEvents(w, r)
	if !ok {
		return
	}
	writeSessionJSON(w, http.StatusOK, skillUsageResponse{Data: insights.SkillUsageFromEvents(events)})
}

func (a sessionAPI) modelPerformance(w http.ResponseWriter, r *http.Request) {
	events, ok := a.loadInsightEvents(w, r)
	if !ok {
		return
	}
	writeSessionJSON(w, http.StatusOK, modelPerformanceResponse{Data: insights.ModelPerformanceFromEvents(events)})
}

// loadInsightEvents returns retained events for an insight handler, or writes
// the shared unavailable/query-failed response and returns ok=false.
func (a sessionAPI) loadInsightEvents(w http.ResponseWriter, r *http.Request) ([]canonical.Event, bool) {
	if a.sessions == nil || a.eventReader == nil {
		writeSessionError(w, http.StatusServiceUnavailable, insightUnavailableCode, insightUnavailableMsg)
		return nil, false
	}
	events, err := a.insightEvents(r)
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, insightQueryFailedCode, insightQueryFailedMsg)
		return nil, false
	}
	return events, true
}
