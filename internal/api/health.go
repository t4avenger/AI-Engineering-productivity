package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/wayne/telemetryiq/internal/privacy"
	"github.com/wayne/telemetryiq/internal/storage"
)

type HealthResponse struct {
	Status    string `json:"status"`
	Service   string `json:"service"`
	Timestamp string `json:"timestamp"`
}

func NewHandler(logger *slog.Logger, sessions ...storage.SessionReader) http.Handler {
	return newHandler(logger, nil, nil, nil, sessionReader(sessions))
}

func NewDevelopmentHandler(logger *slog.Logger, sanitizer *privacy.Sanitizer, sessions ...storage.SessionReader) http.Handler {
	return newHandler(logger, newSanitizedInspector(sanitizer), nil, sanitizer, sessionReader(sessions))
}

// NewPersistentHandler enables the supported live Codex OTLP log path.
func NewPersistentHandler(logger *slog.Logger, sanitizer *privacy.Sanitizer, repository storage.Repository) http.Handler {
	return newHandler(logger, nil, repository, sanitizer, repository)
}

// NewPersistentDevelopmentHandler retains the development-only sanitized
// inspector while enabling the supported persistent Codex log path.
func NewPersistentDevelopmentHandler(logger *slog.Logger, sanitizer *privacy.Sanitizer, repository storage.Repository) http.Handler {
	return newHandler(logger, newSanitizedInspector(sanitizer), repository, sanitizer, repository)
}

func sessionReader(readers []storage.SessionReader) storage.SessionReader {
	if len(readers) == 0 {
		return nil
	}
	return readers[0]
}

func newHandler(logger *slog.Logger, inspector *sanitizedInspector, repository storage.Repository, sanitizer *privacy.Sanitizer, sessions storage.SessionReader) http.Handler {
	ingest := newOTLPHTTPIngest(inspector, sanitizer, repository)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", healthHandler(logger))
	sessionAPI := newSessionAPI(sessions)
	mux.HandleFunc("GET /api/v1/sessions", sessionAPI.list)
	mux.HandleFunc("GET /api/v1/sessions/{id}", sessionAPI.detail)
	mux.HandleFunc("DELETE /api/v1/sessions/{id}", sessionAPI.delete)
	mux.HandleFunc("POST /v1/traces", ingest.tracesHandler)
	mux.HandleFunc("POST /v1/logs", ingest.logsHandler)
	mux.HandleFunc("GET /api/v1/ingest/counters", ingest.countersHandler)
	if inspector != nil {
		mux.HandleFunc("GET /api/v1/development/last-ingest", inspector.handler)
	}
	return requestLogger(logger, withCORS(mux))
}

func healthHandler(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		response := HealthResponse{
			Status:    "healthy",
			Service:   "telemetryiq-daemon",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			logger.Error("failed to write health response", "error", err)
		}
	}
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "http://127.0.0.1:5173" || origin == "http://127.0.0.1:4173" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "DELETE, GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestLogger(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Info("request", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
