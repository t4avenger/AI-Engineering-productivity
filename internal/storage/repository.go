// Package storage defines storage-agnostic persistence contracts.
package storage

import (
	"context"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

type Repository interface {
	SaveEvents(context.Context, []canonical.Event) error
	Session(context.Context, string) (canonical.Session, bool, error)
	DeleteSession(context.Context, string) error
	Close() error
}
