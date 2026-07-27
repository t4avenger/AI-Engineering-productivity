package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/wayne/telemetryiq/internal/privacy"
)

type HealthResponse struct {
	Status    string `json:"status"`
	Service   string `json:"service"`
	Timestamp string `json:"timestamp"`
}

func NewHandler(logger *slog.Logger) http.Handler {
	return newHandler(logger, nil)
}

func NewDevelopmentHandler(logger *slog.Logger, sanitizer *privacy.Sanitizer) http.Handler {
	return newHandler(logger, newSanitizedInspector(sanitizer))
}

func newHandler(logger *slog.Logger, inspector *sanitizedInspector) http.Handler {
	ingest := newOTLPHTTPIngest(inspector)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", healthHandler(logger))
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
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
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
