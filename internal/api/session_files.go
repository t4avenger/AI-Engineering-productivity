package api

import (
	"net/http"

	"github.com/wayne/telemetryiq/internal/insights"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
)

type fileListResponse struct {
	Data       []insights.SessionFileEntry `json:"data"`
	Pagination eventPagination             `json:"pagination"`
}

func (a sessionAPI) files(w http.ResponseWriter, r *http.Request) {
	id, limit, cursor, ok := a.requireSessionSubresource(w, r)
	if !ok {
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
		next = encodeEventCursor(last.OccurredAt, last.EventID)
	}
	writeSessionJSON(w, http.StatusOK, fileListResponse{
		Data:       page,
		Pagination: eventPagination{Limit: limit, NextCursor: next},
	})
}
