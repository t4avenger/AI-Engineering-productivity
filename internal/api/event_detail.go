package api

import (
	"net/http"

	"github.com/wayne/telemetryiq/internal/inspector"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
)

const eventNotFound = "event was not found in this session"

type eventDetailResponse struct {
	Data eventDetail `json:"data"`
}

type eventDetail struct {
	timelineEvent
	Attributes         map[string]any          `json:"attributes"`
	ProviderExtensions map[string]any          `json:"provider_extensions"`
	Truncation         inspector.Truncation    `json:"truncation"`
	Relationships      inspector.Relationships `json:"relationships"`
}

// event returns one retained session event and proven relationships (#189 / T08).
// Missing, deleted, or foreign event IDs share a structured 404.
func (a sessionAPI) event(w http.ResponseWriter, r *http.Request) {
	if a.sessions == nil || a.eventReader == nil {
		writeSessionError(w, http.StatusServiceUnavailable, "sessions_unavailable", sessionUnavailable)
		return
	}
	sessionID := r.PathValue("id")
	eventID := r.PathValue("event_id")
	if eventID == "" {
		writeSessionError(w, http.StatusNotFound, "event_not_found", eventNotFound)
		return
	}
	if _, found, err := a.sessions.Session(r.Context(), sessionID); err != nil {
		writeSessionError(w, http.StatusInternalServerError, "session_query_failed", "unable to query session")
		return
	} else if !found {
		writeSessionError(w, http.StatusNotFound, "session_not_found", sessionNotFound)
		return
	}
	event, found, err := a.eventReader.GetEvent(r.Context(), sessionID, eventID)
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "event_query_failed", "unable to query session event")
		return
	}
	if !found {
		writeSessionError(w, http.StatusNotFound, "event_not_found", eventNotFound)
		return
	}
	sessionEvents, err := a.insightSessionEvents(r, sessionID)
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "event_query_failed", "unable to query session events")
		return
	}
	operations := []canonical.Operation{}
	if a.operationReader != nil {
		operations, err = a.operationReader.ListOperations(r.Context(), storage.OperationFilter{SessionID: sessionID})
		if err != nil {
			writeSessionError(w, http.StatusInternalServerError, "operation_query_failed", "unable to query session operations")
			return
		}
	}
	expand := r.URL.Query().Get("expand") == "1"
	detail := inspector.Build(event, sessionEvents, operations, expand)
	writeSessionJSON(w, http.StatusOK, eventDetailResponse{Data: publicEventDetail(detail)})
}

func publicEventDetail(detail inspector.Detail) eventDetail {
	return eventDetail{
		timelineEvent:      publicTimelineEvent(detail.Event),
		Attributes:         detail.Attributes,
		ProviderExtensions: detail.ProviderExtensions,
		Truncation:         detail.Truncation,
		Relationships:      detail.Relationships,
	}
}
