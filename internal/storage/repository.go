// Package storage defines storage-agnostic persistence contracts.
package storage

import (
	"context"
	"time"

	"github.com/wayne/telemetryiq/internal/cost"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// SessionFilter constrains a session query. Empty fields do not filter results,
// including Scope: repository-internal callers retain access to every row.
// Model matching uses observed model metadata only; sessions with unavailable
// model information never match a model filter.
type SessionFilter struct {
	Tool          string
	Model         string
	Outcome       string
	Scope         SessionScope
	StartedAfter  *time.Time
	StartedBefore *time.Time
	Cursor        *SessionCursor
	Limit         int
}

// SessionScope controls whether reconstructed provider sessions or
// observation-only rows are returned. The empty value keeps the repository's
// backwards-compatible all-sessions behaviour; public list surfaces choose an
// explicit scope.
type SessionScope string

const (
	SessionScopePrimary     SessionScope = "primary"
	SessionScopeObservation SessionScope = "observation"
)

// SessionCursor identifies the last session returned by a reverse-chronological
// session page.
type SessionCursor struct {
	StartedAt time.Time
	SessionID string
}

// EventFilter constrains a chronological event timeline for one session.
// Limit is the number of events visible to the caller, not the repository
// result size. EventTypes, when non-empty, restricts rows to those types.
type EventFilter struct {
	SessionID  string
	EventTypes []string
	Cursor     *EventCursor
	Limit      int
}

// EventCursor identifies the last event returned by a chronological page.
type EventCursor struct {
	OccurredAt time.Time
	EventID    string
}

// InsightSourceReader returns thin events for dashboard insight engines without
// scanning the full retained event_json corpus.
type InsightSourceReader interface {
	ListInsightSourceEvents(context.Context) ([]canonical.Event, error)
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

// EventReader exposes raw canonical events (epic #87 — no ingest-time hiding).
// ListEvents returns at most EventFilter.Limit+1 events so callers can detect
// whether another cursor page exists without issuing a separate count query.
// GetEvent returns the retained event for the given session-scoped identity.
// found is false when the event is missing, deleted, or belongs to another
// session — callers must not distinguish those cases to callers of the API.
type EventReader interface {
	ListEvents(context.Context, EventFilter) ([]canonical.Event, error)
	GetEvent(ctx context.Context, sessionID, eventID string) (canonical.Event, bool, error)
}

// OperationFilter constrains operation queries. Empty SessionID returns every
// retained operation; callers that need a session-scoped view pass SessionID.
type OperationFilter struct {
	SessionID string
}

// OperationReader exposes retained stable-primitive operation records.
type OperationReader interface {
	ListOperations(context.Context, OperationFilter) ([]canonical.Operation, error)
}

// AgentRelationFilter constrains sub-agent relation queries. Empty SessionID
// returns every retained relation; callers that need a session-scoped view of
// the reconstructed sub-agent tree pass SessionID.
type AgentRelationFilter struct {
	SessionID string
}

// AgentRelationReader exposes the reconstructed sub-agent tree (#102): the
// parent→child agent relations and per-agent token/duration rollups derived from
// a session's enhanced-telemetry span set.
type AgentRelationReader interface {
	ListAgentRelations(context.Context, AgentRelationFilter) ([]canonical.AgentRelation, error)
}

// OperationWriter persists stable-primitive operation records.
type OperationWriter interface {
	SaveOperations(context.Context, []canonical.Operation) error
}

// EventOperationWriter persists events and operation records as one batch.
type EventOperationWriter interface {
	SaveEventsAndOperations(context.Context, []canonical.Event, []canonical.Operation) error
}

// CostReader returns calculation provenance without exposing raw intake data.
type CostReader interface {
	ListCostRecords(context.Context, string) ([]cost.Record, error)
	SummarizeCosts(context.Context) (cost.Summary, error)
}

// RetentionApplier deletes raw telemetry older than the configured retention window.
type RetentionApplier interface {
	ApplyRetention(ctx context.Context, retentionDays int, now time.Time) (deletedSessions int, err error)
}

type Repository interface {
	SaveEvents(context.Context, []canonical.Event) error
	EventOperationWriter
	OperationWriter
	SessionReader
	SessionDeleter
	EventReader
	OperationReader
	AgentRelationReader
	CostReader
	InsightSourceReader
	Close() error
}
