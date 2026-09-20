package api

import (
	"net/http"

	"github.com/wayne/telemetryiq/internal/breakdown"
)

type breakdownResponse struct {
	Data breakdown.Result `json:"data"`
}

// breakdown returns the full retained-session duration partition (#190 / T09).
// It is independent of UI pagination and never invents Planning or User wait
// from idle gaps.
func (a sessionAPI) breakdown(w http.ResponseWriter, r *http.Request) {
	if a.sessions == nil || a.eventReader == nil {
		writeSessionError(w, http.StatusServiceUnavailable, "sessions_unavailable", sessionUnavailable)
		return
	}
	sessionID := r.PathValue("id")
	session, found, err := a.sessions.Session(r.Context(), sessionID)
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "session_query_failed", "unable to query session")
		return
	}
	if !found {
		writeSessionError(w, http.StatusNotFound, "session_not_found", sessionNotFound)
		return
	}
	events, err := a.insightSessionEvents(r, sessionID)
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "breakdown_query_failed", "unable to query session events for breakdown")
		return
	}
	writeSessionJSON(w, http.StatusOK, breakdownResponse{Data: breakdown.Calculate(events, &session)})
}
