package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/wayne/telemetryiq/internal/conversation"
	"github.com/wayne/telemetryiq/internal/cost"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
)

type timelineEvent struct {
	EventID               string   `json:"event_id"`
	EventType             string   `json:"event_type"`
	OccurredAt            string   `json:"occurred_at"`
	ReceivedAt            string   `json:"received_at"`
	Provider              string   `json:"provider"`
	Tool                  string   `json:"tool"`
	SourceVersion         string   `json:"source_version"`
	Model                 *string  `json:"model"`
	InputTokenCount       *string  `json:"input_token_count"`
	OutputTokenCount      *string  `json:"output_token_count"`
	CachedInputTokenCount *string  `json:"cached_input_token_count"`
	ReasoningTokenCount   *string  `json:"reasoning_token_count"`
	OperationID           *string  `json:"operation_id"`
	Category              *string  `json:"category"`
	Outcome               *string  `json:"outcome"`
	DurationMs            *string  `json:"duration_ms"`
	ApprovalID            *string  `json:"approval_id"`
	ApprovalDecision      *string  `json:"approval_decision"`
	ApprovalReasonClass   *string  `json:"approval_reason_class"`
	ToolName              *string  `json:"tool_name"`
	ToolNamespace         *string  `json:"tool_namespace"`
	ToolSource            *string  `json:"tool_source"`
	LifecycleKind         *string  `json:"lifecycle_kind"`
	LifecyclePhase        *string  `json:"lifecycle_phase"`
	LifecycleStatus       *string  `json:"lifecycle_status"`
	Entrypoint            *string  `json:"entrypoint"`
	UnavailableFields     []string `json:"unavailable_fields"`
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

type conversationRecord struct {
	EventID             string  `json:"event_id"`
	EventType           string  `json:"event_type"`
	OccurredAt          string  `json:"occurred_at"`
	Provider            string  `json:"provider"`
	Tool                string  `json:"tool"`
	SourceVersion       string  `json:"source_version"`
	Role                string  `json:"role"`
	Text                *string `json:"text"`
	ContentAvailability string  `json:"content_availability"`
}

type conversationResponse struct {
	Data       []conversationRecord `json:"data"`
	Pagination eventPagination      `json:"pagination"`
}

func (a sessionAPI) events(w http.ResponseWriter, r *http.Request) {
	id, limit, cursor, ok := a.requireSessionSubresource(w, r)
	if !ok {
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

// conversation returns reviewed retained content as evidence records. It never
// fetches provider data or converts a raw API body into synthetic messages.
func (a sessionAPI) conversation(w http.ResponseWriter, r *http.Request) {
	id, limit, cursor, ok := a.requireSessionSubresource(w, r)
	if !ok {
		return
	}
	events, err := a.insightSessionEvents(r, id)
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "conversation_query_failed", "unable to query session conversation")
		return
	}
	cursorOccurredAt, cursorEventID := "", ""
	if cursor != nil {
		cursorOccurredAt, cursorEventID = cursor.OccurredAt, cursor.EventID
	}
	page, last := conversation.Page(conversation.Project(events), limit, cursorOccurredAt, cursorEventID)
	var next *string
	if last != nil {
		next = encodeEventCursor(last.OccurredAt.UTC().Format(time.RFC3339Nano), last.EventID)
	}
	response := conversationResponse{Data: make([]conversationRecord, len(page)), Pagination: eventPagination{Limit: limit, NextCursor: next}}
	for index, record := range page {
		response.Data[index] = publicConversationRecord(record)
	}
	writeSessionJSON(w, http.StatusOK, response)
}

func publicConversationRecord(record conversation.Record) conversationRecord {
	return conversationRecord{
		EventID: record.EventID, EventType: record.EventType, OccurredAt: record.OccurredAt.UTC().Format(time.RFC3339Nano),
		Provider: record.Provider, Tool: record.Tool, SourceVersion: record.SourceVersion, Role: record.Role,
		Text: record.Text, ContentAvailability: record.ContentAvailability,
	}
}

// requireSessionSubresource authenticates session existence and list query params
// for cursor-paged session sub-resources (/events, /files).
func (a sessionAPI) requireSessionSubresource(w http.ResponseWriter, r *http.Request) (string, int, *eventCursor, bool) {
	if a.sessions == nil || a.eventReader == nil {
		writeSessionError(w, http.StatusServiceUnavailable, "sessions_unavailable", sessionUnavailable)
		return "", 0, nil, false
	}
	id := r.PathValue("id")
	if _, found, err := a.sessions.Session(r.Context(), id); err != nil {
		writeSessionError(w, http.StatusInternalServerError, "session_query_failed", "unable to query session")
		return "", 0, nil, false
	} else if !found {
		writeSessionError(w, http.StatusNotFound, "session_not_found", sessionNotFound)
		return "", 0, nil, false
	}
	limit, cursor, err := parseEventListQuery(r)
	if err != nil {
		writeSessionError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return "", 0, nil, false
	}
	return id, limit, cursor, true
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
	return page, encodeEventCursor(last.OccurredAt.UTC().Format(time.RFC3339Nano), last.EventID)
}

func encodeEventCursor(occurredAt, eventID string) *string {
	data, _ := json.Marshal(eventCursor{OccurredAt: occurredAt, EventID: eventID})
	cursor := base64.RawURLEncoding.EncodeToString(data)
	return &cursor
}

func publicTimelineEvent(event canonical.Event) timelineEvent {
	return timelineEvent{
		EventID: event.EventID, EventType: event.EventType, OccurredAt: event.OccurredAt.UTC().Format(time.RFC3339Nano), ReceivedAt: event.ReceivedAt.UTC().Format(time.RFC3339Nano), Provider: event.Provider, Tool: event.Tool, SourceVersion: event.SourceVersion,
		Model: optionalString(event.Attributes["model"]), InputTokenCount: optionalString(event.Attributes["input_token_count"]), OutputTokenCount: optionalString(event.Attributes["output_token_count"]),
		CachedInputTokenCount: optionalString(event.Attributes["cached_input_token_count"]), ReasoningTokenCount: optionalString(event.Attributes["reasoning_token_count"]),
		OperationID: optionalString(event.Attributes["operation_id"]), Category: optionalString(event.Attributes["category"]), Outcome: optionalString(event.Attributes["outcome"]), DurationMs: optionalString(event.Attributes["duration_ms"]),
		ApprovalID: optionalString(event.Attributes["approval_id"]), ApprovalDecision: optionalString(event.Attributes["approval_decision"]), ApprovalReasonClass: optionalString(event.Attributes["approval_reason_class"]),
		ToolName: optionalString(event.Attributes["tool_name"]), ToolNamespace: optionalString(event.Attributes["tool_namespace"]), ToolSource: optionalString(event.Attributes["tool_source"]),
		LifecycleKind: optionalString(event.Attributes["lifecycle_kind"]), LifecyclePhase: optionalString(event.Attributes["lifecycle_phase"]), LifecycleStatus: optionalString(event.Attributes["lifecycle_status"]), Entrypoint: optionalString(event.Attributes["entrypoint"]),
		UnavailableFields: unavailableFields(event.Attributes["unavailable_fields"]),
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
	summary, err := a.costReader.SummarizeCosts(r.Context())
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "cost_query_failed", "unable to query costs")
		return
	}
	writeSessionJSON(w, http.StatusOK, costSummaryResponse{Data: costSummary{
		Currency:                 summary.Currency,
		CalculatedAmountMicrousd: summary.CalculatedAmountMicrousd,
		Statuses:                 summary.Statuses,
	}})
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
