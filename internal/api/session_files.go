package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/wayne/telemetryiq/internal/insights"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
)

type fileListResponse struct {
	Data       []insights.SessionFileEntry `json:"data"`
	Pagination eventPagination             `json:"pagination"`
}

type fileCursor struct {
	OccurredAt string `json:"occurred_at"`
	EventID    string `json:"event_id"`
}

func (a sessionAPI) files(w http.ResponseWriter, r *http.Request) {
	if a.sessions == nil || a.eventReader == nil {
		writeSessionError(w, http.StatusServiceUnavailable, "sessions_unavailable", sessionUnavailable)
		return
	}
	id := r.PathValue("id")
	if _, found, err := a.sessions.Session(r.Context(), id); err != nil {
		writeSessionError(w, http.StatusInternalServerError, "session_query_failed", "unable to query session")
		return
	} else if !found {
		writeSessionError(w, http.StatusNotFound, "session_not_found", sessionNotFound)
		return
	}
	limit, cursor, err := parseEventListQuery(r)
	if err != nil {
		writeSessionError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	events, err := a.insightSessionEvents(r, id)
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "event_query_failed", "unable to query session events")
		return
	}
	operations := []canonical.Operation{}
	if a.operationReader != nil {
		operations, err = a.operationReader.ListOperations(r.Context(), storage.OperationFilter{SessionID: id})
		if err != nil {
			writeSessionError(w, http.StatusInternalServerError, "operation_query_failed", "unable to query session operations")
			return
		}
	}
	entries := insights.SessionFilesFromEvidence(events, operations)
	cursorOccurredAt, cursorEventID := "", ""
	if cursor != nil {
		cursorOccurredAt, cursorEventID = cursor.OccurredAt, cursor.EventID
	}
	page, last := insights.PageSessionFiles(entries, limit, cursorOccurredAt, cursorEventID)
	var next *string
	if last != nil {
		payload, _ := json.Marshal(fileCursor{OccurredAt: last.OccurredAt, EventID: last.EventID})
		encoded := base64.RawURLEncoding.EncodeToString(payload)
		next = &encoded
	}
	writeSessionJSON(w, http.StatusOK, fileListResponse{
		Data:       page,
		Pagination: eventPagination{Limit: limit, NextCursor: next},
	})
}
