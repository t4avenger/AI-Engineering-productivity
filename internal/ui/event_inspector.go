package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/wayne/telemetryiq/internal/inspector"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
)

const (
	inspectorTabDetails    = "details"
	inspectorTabAttributes = "attributes"
	inspectorTabEvents     = "events"
)

type eventInspectorView struct {
	Open                bool
	SelectedEventID     string
	ActiveTab           string
	Missing             bool
	Error               string
	Title               string
	EventType           string
	OccurredAt          string
	Outcome             string
	Duration            string
	Provider            string
	Tool                string
	SourceVersion       string
	OperationID         string
	AttributesJSON      string
	ExtensionsJSON      string
	Truncated           bool
	ExpandURL           string
	CloseURL            string
	DetailsTabURL       string
	AttributesTabURL    string
	EventsTabURL        string
	Files               []inspectorRelationRow
	Spans               []inspectorRelationRow
	RelatedEvents       []inspectorRelationRow
	FilesAvailability   string
	SpansAvailability   string
	RelatedAvailability string
	FocusTargetID       string
}

type inspectorRelationRow struct {
	Label string
	Href  string
	Meta  string
}

func parseInspectorTab(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case inspectorTabAttributes:
		return inspectorTabAttributes
	case inspectorTabEvents:
		return inspectorTabEvents
	default:
		return inspectorTabDetails
	}
}

func (s *Server) populateEventInspector(r *http.Request, id string, events []canonical.Event, data *sessionDetailData) {
	eventID := strings.TrimSpace(r.URL.Query().Get("event"))
	if eventID == "" {
		data.Inspector = eventInspectorView{CloseURL: sessionInspectorPath(id, "", "", r, false)}
		return
	}
	tab := parseInspectorTab(r.URL.Query().Get("inspector"))
	expand := r.URL.Query().Get("expand") == "1"
	view := eventInspectorView{
		Open:             true,
		SelectedEventID:  eventID,
		ActiveTab:        tab,
		FocusTargetID:    "timeline-event-" + eventID + "-select",
		CloseURL:         sessionInspectorPath(id, "", "", r, false) + "#timeline-event-" + url.PathEscape(eventID) + "-select",
		DetailsTabURL:    sessionInspectorPath(id, eventID, inspectorTabDetails, r, expand),
		AttributesTabURL: sessionInspectorPath(id, eventID, inspectorTabAttributes, r, expand),
		EventsTabURL:     sessionInspectorPath(id, eventID, inspectorTabEvents, r, expand),
		ExpandURL:        sessionInspectorPath(id, eventID, tab, r, true),
	}
	if s.events == nil {
		view.Error = "Event inspector is unavailable because event storage is offline."
		data.Inspector = view
		return
	}
	event, found, err := s.events.GetEvent(r.Context(), id, eventID)
	if err != nil {
		view.Error = "Unable to load the selected event."
		data.Inspector = view
		return
	}
	if !found {
		view.Missing = true
		data.Inspector = view
		markSelectedRows(data, eventID)
		return
	}
	operations := []canonical.Operation{}
	if s.operations != nil {
		listed, opErr := s.operations.ListOperations(r.Context(), storage.OperationFilter{SessionID: id})
		if opErr != nil {
			view.Error = "Unable to load proven relationships for the selected event."
			data.Inspector = view
			markSelectedRows(data, eventID)
			return
		}
		operations = listed
	}
	detail := inspector.Build(event, events, operations, expand)
	view.Title = event.EventType
	view.EventType = event.EventType
	view.OccurredAt = event.OccurredAt.UTC().Format("2006-01-02 15:04:05 UTC")
	view.Provider = event.Provider
	view.Tool = event.Tool
	view.SourceVersion = event.SourceVersion
	if outcome := optionalDisplayString(event.Attributes["outcome"]); outcome != "" {
		view.Outcome = outcome
	}
	if duration := optionalDisplayString(event.Attributes["duration_ms"]); duration != "" {
		view.Duration = duration + " ms"
	}
	if operationID := optionalDisplayString(event.Attributes["operation_id"]); operationID != "" {
		view.OperationID = operationID
	}
	view.AttributesJSON = mustJSON(detail.Attributes)
	view.ExtensionsJSON = mustJSON(detail.ProviderExtensions)
	view.Truncated = detail.Truncation.Applied
	view.FilesAvailability = detail.Relationships.Availability.Files
	view.SpansAvailability = detail.Relationships.Availability.Spans
	view.RelatedAvailability = detail.Relationships.Availability.RelatedEvents
	view.Files = fileRelationRows(id, detail, r)
	view.Spans = spanRelationRows(detail)
	view.RelatedEvents = relatedRelationRows(id, detail, r)
	data.Inspector = view
	markSelectedRows(data, eventID)
}

func markSelectedRows(data *sessionDetailData, eventID string) {
	for i := range data.Events {
		data.Events[i].Selected = data.Events[i].EventID == eventID
	}
	for i := range data.Conversation {
		data.Conversation[i].Selected = data.Conversation[i].EventID == eventID
	}
}

func attachSelectionPaths(data *sessionDetailData, r *http.Request) {
	for i := range data.Events {
		data.Events[i].SelectPath = sessionInspectorPath(data.SessionID, data.Events[i].EventID, inspectorTabDetails, r, false)
	}
	for i := range data.Conversation {
		data.Conversation[i].SelectPath = sessionInspectorPath(data.SessionID, data.Conversation[i].EventID, inspectorTabDetails, r, false)
	}
}

func fileRelationRows(sessionID string, detail inspector.Detail, r *http.Request) []inspectorRelationRow {
	rows := make([]inspectorRelationRow, 0, len(detail.Relationships.Files))
	for _, entry := range detail.Relationships.Files {
		pathLabel := "path unavailable"
		if entry.Path != nil {
			pathLabel = *entry.Path
		}
		action := "unknown"
		if entry.Action != nil {
			action = *entry.Action
		}
		rows = append(rows, inspectorRelationRow{
			Label: pathLabel,
			Meta:  action,
			Href:  sessionInspectorPath(sessionID, entry.EventID, inspectorTabDetails, r, false),
		})
	}
	return rows
}

func spanRelationRows(detail inspector.Detail) []inspectorRelationRow {
	rows := make([]inspectorRelationRow, 0, len(detail.Relationships.Spans))
	for _, span := range detail.Relationships.Spans {
		name := span.SpanID
		if span.Name != nil {
			name = *span.Name
		}
		meta := span.IntervalAvailability
		if span.DurationMs != nil {
			meta = fmt.Sprintf("%d ms · %s", *span.DurationMs, span.IntervalAvailability)
		}
		rows = append(rows, inspectorRelationRow{
			Label: fmt.Sprintf("%s / %s (%s)", span.TraceID, span.SpanID, name),
			Meta:  meta,
		})
	}
	return rows
}

func relatedRelationRows(sessionID string, detail inspector.Detail, r *http.Request) []inspectorRelationRow {
	rows := make([]inspectorRelationRow, 0, len(detail.Relationships.RelatedEvents))
	for _, related := range detail.Relationships.RelatedEvents {
		rows = append(rows, inspectorRelationRow{
			Label: related.EventType,
			Meta:  related.Relation + " · " + related.EventID,
			Href:  sessionInspectorPath(sessionID, related.EventID, inspectorTabDetails, r, false),
		})
	}
	return rows
}

func sessionInspectorPath(sessionID, eventID, tab string, r *http.Request, expand bool) string {
	values := url.Values{}
	if r != nil {
		if cursor := r.URL.Query().Get("cursor"); cursor != "" {
			values.Set("cursor", cursor)
		}
		if conversationCursor := r.URL.Query().Get("conversation_cursor"); conversationCursor != "" {
			values.Set("conversation_cursor", conversationCursor)
		}
		if r.URL.Query().Get("confirm") == "1" {
			values.Set("confirm", "1")
		}
	}
	if eventID != "" {
		values.Set("event", eventID)
		if tab == "" {
			tab = inspectorTabDetails
		}
		values.Set("inspector", tab)
		if expand {
			values.Set("expand", "1")
		}
	}
	path := "/sessions/" + url.PathEscape(sessionID)
	encoded := values.Encode()
	if encoded == "" {
		return path
	}
	return path + "?" + encoded
}

func optionalDisplayString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64, float32, int, int32, int64, uint, uint32, uint64:
		return fmt.Sprint(typed)
	default:
		return ""
	}
}

func mustJSON(value any) string {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(payload)
}
