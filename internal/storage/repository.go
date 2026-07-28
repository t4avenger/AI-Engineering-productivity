// Package storage defines storage-agnostic persistence contracts.
package storage

import (
	"context"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
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

// SessionReader is the read-only session contract used by the local API.
type SessionReader interface {
	Session(context.Context, string) (canonical.Session, bool, error)
	ListSessions(context.Context, SessionFilter) ([]canonical.Session, error)
}

// SessionDeleter removes one session and all events belonging to it.
type SessionDeleter interface {
	DeleteSession(context.Context, string) error
}

type Repository interface {
	SaveEvents(context.Context, []canonical.Event) error
	SessionReader
	SessionDeleter
	Close() error
}
