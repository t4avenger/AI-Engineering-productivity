package api

import (
	"net/http"

	"github.com/wayne/telemetryiq/internal/governance"
	"github.com/wayne/telemetryiq/internal/insights"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
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

type contextWasteResponse struct {
	Data insights.ContextWaste `json:"data"`
}

type operationStatsResponse struct {
	Data insights.OperationStats `json:"data"`
}

type riskyAccessResponse struct {
	Data governance.RiskyAccess `json:"data"`
}

type unapprovedMCPResponse struct {
	Data governance.UnapprovedMCP `json:"data"`
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

func (a sessionAPI) contextWaste(w http.ResponseWriter, r *http.Request) {
	events, ok := a.loadInsightEvents(w, r)
	if !ok {
		return
	}
	writeSessionJSON(w, http.StatusOK, contextWasteResponse{Data: insights.ContextWasteFromEvents(events, a.thresholds.ContextWaste)})
}

func (a sessionAPI) operations(w http.ResponseWriter, r *http.Request) {
	if a.operationReader == nil {
		writeSessionError(w, http.StatusServiceUnavailable, insightUnavailableCode, insightUnavailableMsg)
		return
	}
	operations, err := a.operationReader.ListOperations(r.Context(), storage.OperationFilter{})
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, insightQueryFailedCode, insightQueryFailedMsg)
		return
	}
	writeSessionJSON(w, http.StatusOK, operationStatsResponse{Data: insights.OperationStatsFromOperations(operations)})
}

func (a sessionAPI) riskyAccess(w http.ResponseWriter, r *http.Request) {
	events, ok := a.loadInsightEvents(w, r)
	if !ok {
		return
	}
	writeSessionJSON(w, http.StatusOK, riskyAccessResponse{Data: governance.RiskyAccessFromEvents(events)})
}

func (a sessionAPI) unapprovedMCP(w http.ResponseWriter, r *http.Request) {
	events, ok := a.loadInsightEvents(w, r)
	if !ok {
		return
	}
	writeSessionJSON(w, http.StatusOK, unapprovedMCPResponse{Data: governance.UnapprovedMCPFromEvents(events, a.thresholds.currentMCPAllowlist())})
}

// loadInsightEvents returns retained events for an insight handler, or writes
// the shared unavailable/query-failed response and returns ok=false.
func (a sessionAPI) loadInsightEvents(w http.ResponseWriter, r *http.Request) ([]canonical.Event, bool) {
	if a.insightSources == nil {
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
