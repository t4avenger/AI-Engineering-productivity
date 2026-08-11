// Package storage defines storage-agnostic persistence contracts.
package storage

import (
	"context"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/privacy"
)

// SessionFilter constrains a session query. Empty fields do not filter results.
// Model matching uses observed model metadata only; sessions with unavailable
// model information never match a model filter.
type SessionFilter struct {
	Tool          string
	Model         string
	Outcome       string
	StartedAfter  *time.Time
	StartedBefore *time.Time
	Cursor        *SessionCursor
	Limit         int
}

// SessionCursor identifies the last session returned by a reverse-chronological
// session page.
type SessionCursor struct {
	StartedAt time.Time
	SessionID string
}

// EventFilter constrains a chronological event timeline for one session.
// Limit is the number of events visible to the caller, not the repository
// result size.
type EventFilter struct {
	SessionID string
	Cursor    *EventCursor
	Limit     int
}

// EventCursor identifies the last event returned by a chronological page.
type EventCursor struct {
	OccurredAt time.Time
	EventID    string
}

// SessionReader is the read-only session contract used by the local API.
type SessionReader interface {
	Session(context.Context, string) (canonical.Session, bool, error)
	ListSessions(context.Context, SessionFilter) ([]canonical.Session, error)
}

// SessionDeleter removes one session and all events belonging to it.
type SessionDeleter interface {
	DeleteSession(context.Context, string) error
	DeleteAllSessions(context.Context) error
}

// EventReader exposes sanitised canonical events and their provenance.
// ListEvents returns at most EventFilter.Limit+1 events so callers can detect
// whether another cursor page exists without issuing a separate count query.
type EventReader interface {
	ListEvents(context.Context, EventFilter) ([]canonical.Event, error)
	EventProvenance(context.Context, string) ([]privacy.Provenance, bool, error)
}

type Repository interface {
	SaveEvents(context.Context, []canonical.Event) error
	SessionReader
	SessionDeleter
	EventReader
	Close() error
}
