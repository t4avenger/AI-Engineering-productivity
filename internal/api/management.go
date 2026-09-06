package api

import (
	"log/slog"
	"net/http"

	"github.com/wayne/telemetryiq/internal/privacy"
	"github.com/wayne/telemetryiq/internal/storage"
	"github.com/wayne/telemetryiq/internal/ui"
)

// NewAuthenticatedPersistentHandler enables persistent ingestion and protects
// management endpoints with a local bearer token or dashboard cookie.
func NewAuthenticatedPersistentHandler(logger *slog.Logger, sanitizer *privacy.Sanitizer, repository storage.Repository, token string) http.Handler {
	return wrapUI(token, repository, withManagementAuth(token, withBulkDelete(repository, newHandler(logger, nil, repository, sanitizer, repository))))
}

// NewAuthenticatedPersistentDevelopmentHandler retains the development-only
// sanitized inspector while protecting management endpoints.
func NewAuthenticatedPersistentDevelopmentHandler(logger *slog.Logger, sanitizer *privacy.Sanitizer, repository storage.Repository, token string) http.Handler {
	return wrapUI(token, repository, withManagementAuth(token, withBulkDelete(repository, newHandler(logger, newSanitizedInspector(sanitizer), repository, sanitizer, repository))))
}

func wrapUI(token string, repository storage.Repository, next http.Handler) http.Handler {
	dashboard, err := ui.New(token, repository)
	if err != nil {
		panic("ui templates: " + err.Error())
	}
	return dashboard.Wrap(next)
}

func withBulkDelete(repository storage.Repository, next http.Handler) http.Handler {
	api := newSessionAPI(repository)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/api/v1/sessions" {
			api.deleteAll(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a sessionAPI) deleteAll(w http.ResponseWriter, r *http.Request) {
	if a.deleter == nil {
		writeSessionError(w, http.StatusServiceUnavailable, "sessions_unavailable", sessionUnavailable)
		return
	}
	if err := a.deleter.DeleteAllSessions(r.Context()); err != nil {
		writeSessionError(w, http.StatusInternalServerError, "session_delete_failed", "unable to delete sessions")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
