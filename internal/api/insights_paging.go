package api

import (
	"errors"
	"net/http"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
)

var errInsightSourceUnavailable = errors.New("insight source storage is unavailable")

// insightEvents loads thin insight-source events persisted at write time so
// dashboard insight endpoints do not scan the full event_json corpus.
func (a sessionAPI) insightEvents(r *http.Request) ([]canonical.Event, error) {
	if a.insightSources == nil {
		return nil, errInsightSourceUnavailable
	}
	return a.insightSources.ListInsightSourceEvents(r.Context())
}

func (a sessionAPI) insightSessionEvents(r *http.Request, sessionID string) ([]canonical.Event, error) {
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
