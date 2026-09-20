package api

import (
	"net/http"
	"strings"

	"github.com/wayne/telemetryiq/internal/spans"
)

type spanListResponse struct {
	Data       []spans.Record  `json:"data"`
	Pagination eventPagination `json:"pagination"`
}

// spans returns flat provider-emitted span nodes. Clients derive nesting from
// stable IDs; the handler never infers cross-session or conversation joins.
func (a sessionAPI) spans(w http.ResponseWriter, r *http.Request) {
	id, limit, cursor, ok := a.requireSessionSubresource(w, r)
	if !ok {
		return
	}
	events, err := a.insightSessionEvents(r, id)
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "span_query_failed", "unable to query session spans")
		return
	}
	traceID, spanID := spanCursor(cursor)
	if cursor != nil && (traceID == "" || spanID == "") {
		writeSessionError(w, http.StatusBadRequest, "invalid_query", "cursor is not a span cursor")
		return
	}
	page, last := spans.Page(spans.Project(events), limit, traceID, spanID)
	var next *string
	if last != nil {
		next = encodeEventCursor("1970-01-01T00:00:00Z", last.TraceID+"\x00"+last.SpanID)
	}
	writeSessionJSON(w, http.StatusOK, spanListResponse{
		Data: page, Pagination: eventPagination{Limit: limit, NextCursor: next},
	})
}

func spanCursor(cursor *eventCursor) (string, string) {
	if cursor == nil {
		return "", ""
	}
	parts := strings.SplitN(cursor.EventID, "\x00", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", ""
	}
	return parts[0], parts[1]
}
