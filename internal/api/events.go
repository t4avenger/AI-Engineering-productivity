package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/wayne/telemetryiq/internal/cost"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/privacy"
	"github.com/wayne/telemetryiq/internal/storage"
)

type timelineEvent struct {
	EventID           string   `json:"event_id"`
	EventType         string   `json:"event_type"`
	OccurredAt        string   `json:"occurred_at"`
	ReceivedAt        string   `json:"received_at"`
	Provider          string   `json:"provider"`
	Tool              string   `json:"tool"`
	SourceVersion     string   `json:"source_version"`
	Model             *string  `json:"model"`
	InputTokenCount   *string  `json:"input_token_count"`
	OutputTokenCount  *string  `json:"output_token_count"`
	UnavailableFields []string `json:"unavailable_fields"`
}

type eventListResponse struct {
	Data       []timelineEvent `json:"data"`
	Pagination eventPagination `json:"pagination"`
}

type eventPagination struct {
	Limit      int     `json:"limit"`
	NextCursor *string `json:"next_cursor"`
}

type eventCursor struct {
	OccurredAt string `json:"occurred_at"`
	EventID    string `json:"event_id"`
}

type eventProvenanceResponse struct {
	Data []privacy.Provenance `json:"data"`
}

func (a sessionAPI) events(w http.ResponseWriter, r *http.Request) {
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
	events, err := a.eventReader.ListEvents(r.Context(), storage.EventFilter{SessionID: id, Cursor: storageEventCursor(cursor), Limit: limit})
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "event_query_failed", "unable to query session events")
		return
	}
	page, next := eventPage(events, limit)
	response := eventListResponse{Data: make([]timelineEvent, len(page)), Pagination: eventPagination{Limit: limit, NextCursor: next}}
	for index, event := range page {
		response.Data[index] = publicTimelineEvent(event)
	}
	writeSessionJSON(w, http.StatusOK, response)
}

func (a sessionAPI) provenance(w http.ResponseWriter, r *http.Request) {
	if a.eventReader == nil {
		writeSessionError(w, http.StatusServiceUnavailable, "sessions_unavailable", sessionUnavailable)
		return
	}
	provenance, found, err := a.eventReader.EventProvenance(r.Context(), r.PathValue("id"))
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "event_query_failed", "unable to query event provenance")
		return
	}
	if !found {
		writeSessionError(w, http.StatusNotFound, "event_not_found", "event was not found")
		return
	}
	writeSessionJSON(w, http.StatusOK, eventProvenanceResponse{Data: provenance})
}

func parseEventListQuery(r *http.Request) (int, *eventCursor, error) {
	limit, err := parseSessionLimit(r.URL.Query().Get("limit"))
	if err != nil {
		return 0, nil, err
	}
	raw := r.URL.Query().Get("cursor")
	if raw == "" {
		return limit, nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, nil, sessionQueryError(invalidCursorMessage)
	}
	var cursor eventCursor
	if json.Unmarshal(data, &cursor) != nil || cursor.EventID == "" {
		return 0, nil, sessionQueryError(invalidCursorMessage)
	}
	if _, err := time.Parse(time.RFC3339Nano, cursor.OccurredAt); err != nil {
		return 0, nil, sessionQueryError(invalidCursorMessage)
	}
	return limit, &cursor, nil
}

func storageEventCursor(cursor *eventCursor) *storage.EventCursor {
	if cursor == nil {
		return nil
	}
	occurredAt, _ := time.Parse(time.RFC3339Nano, cursor.OccurredAt)
	return &storage.EventCursor{OccurredAt: occurredAt, EventID: cursor.EventID}
}

func eventPage(events []canonical.Event, limit int) ([]canonical.Event, *string) {
	if len(events) <= limit {
		return events, nil
	}
	page := events[:limit]
	last := page[len(page)-1]
	data, _ := json.Marshal(eventCursor{OccurredAt: last.OccurredAt.UTC().Format(time.RFC3339Nano), EventID: last.EventID})
	cursor := base64.RawURLEncoding.EncodeToString(data)
	return page, &cursor
}

func publicTimelineEvent(event canonical.Event) timelineEvent {
	return timelineEvent{
		EventID: event.EventID, EventType: event.EventType, OccurredAt: event.OccurredAt.UTC().Format(time.RFC3339Nano), ReceivedAt: event.ReceivedAt.UTC().Format(time.RFC3339Nano), Provider: event.Provider, Tool: event.Tool, SourceVersion: event.SourceVersion,
		Model: optionalString(event.Attributes["model"]), InputTokenCount: optionalString(event.Attributes["input_token_count"]), OutputTokenCount: optionalString(event.Attributes["output_token_count"]), UnavailableFields: unavailableFields(event.Attributes["unavailable_fields"]),
	}
}

type costRecordsResponse struct {
	Data []cost.Record `json:"data"`
}

type costSummary struct {
	Currency                 string         `json:"currency"`
	CalculatedAmountMicrousd *int64         `json:"calculated_amount_microusd"`
	Statuses                 map[string]int `json:"statuses"`
}

type costSummaryResponse struct {
	Data costSummary `json:"data"`
}

func (a sessionAPI) costs(w http.ResponseWriter, r *http.Request) {
	if a.costReader == nil {
		writeSessionError(w, http.StatusServiceUnavailable, "costs_unavailable", "cost storage is unavailable")
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
	records, err := a.costReader.ListCostRecords(r.Context(), id)
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "cost_query_failed", "unable to query costs")
		return
	}
	writeSessionJSON(w, http.StatusOK, costRecordsResponse{Data: records})
}

func (a sessionAPI) costSummary(w http.ResponseWriter, r *http.Request) {
	if a.costReader == nil {
		writeSessionError(w, http.StatusServiceUnavailable, "costs_unavailable", "cost storage is unavailable")
		return
	}
	records, err := a.costReader.ListCostRecords(r.Context(), "")
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "cost_query_failed", "unable to query costs")
		return
	}
	summary := costSummary{Statuses: map[string]int{}}
	var amount int64
	for _, record := range records {
		if summary.Currency == "" {
			summary.Currency = record.Currency
		}
		summary.Statuses[record.Status]++
		if record.AmountMicrousd != nil {
			amount += *record.AmountMicrousd
		}
	}
	if amount != 0 {
		summary.CalculatedAmountMicrousd = &amount
	}
	writeSessionJSON(w, http.StatusOK, costSummaryResponse{Data: summary})
}

func optionalString(value any) *string {
	switch value := value.(type) {
	case string:
		return &value
	case float64:
		result := fmt.Sprint(value)
		return &result
	case float32, int, int32, int64, uint, uint32, uint64:
		result := fmt.Sprint(value)
		return &result
	default:
		return nil
	}
}

func unavailableFields(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return []string{}
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if field, ok := value.(string); ok {
			result = append(result, field)
		}
	}
	return result
}
