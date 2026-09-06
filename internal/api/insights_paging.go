package api

import (
	"net/http"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
)

func (a sessionAPI) mcpInventoryEvents(r *http.Request) ([]canonical.Event, error) {
	events := []canonical.Event{}
	var sessionCursor *storage.SessionCursor
	for {
		sessions, err := a.sessions.ListSessions(r.Context(), storage.SessionFilter{Limit: maximumSessionLimit, Cursor: sessionCursor})
		if err != nil {
			return nil, err
		}
		page := sessions
		sessionCursor = nil
		if len(sessions) > maximumSessionLimit {
			page = sessions[:maximumSessionLimit]
			last := page[len(page)-1]
			sessionCursor = &storage.SessionCursor{StartedAt: last.StartedAt, SessionID: last.SessionID}
		}
		for _, session := range page {
			sessionEvents, err := a.mcpInventorySessionEvents(r, session.SessionID)
			if err != nil {
				return nil, err
			}
			events = append(events, sessionEvents...)
		}
		if sessionCursor == nil {
			return events, nil
		}
	}
}

func (a sessionAPI) mcpInventorySessionEvents(r *http.Request, sessionID string) ([]canonical.Event, error) {
	events := []canonical.Event{}
	var eventCursor *storage.EventCursor
	for {
		sessionEvents, err := a.eventReader.ListEvents(r.Context(), storage.EventFilter{SessionID: sessionID, Limit: insightEventLimit, Cursor: eventCursor})
		if err != nil {
			return nil, err
		}
		page := sessionEvents
		eventCursor = nil
		if len(sessionEvents) > insightEventLimit {
			page = sessionEvents[:insightEventLimit]
			last := page[len(page)-1]
			eventCursor = &storage.EventCursor{OccurredAt: last.OccurredAt, EventID: last.EventID}
		}
		events = append(events, page...)
		if eventCursor == nil {
			return events, nil
		}
	}
}
