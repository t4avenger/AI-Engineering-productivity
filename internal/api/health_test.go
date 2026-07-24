package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthEndpointReturnsHealthy(t *testing.T) {
	server := httptest.NewServer(NewHandler(slog.Default()))
	t.Cleanup(server.Close)

	resp, err := http.Get(server.URL + "/api/v1/health")
	if err != nil {
		t.Fatalf("GET health: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Fatalf("close response body: %v", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	var body HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "healthy" {
		t.Fatalf("expected healthy status, got %q", body.Status)
	}
	if body.Service != "telemetryiq-daemon" {
		t.Fatalf("expected daemon service name, got %q", body.Service)
	}
	if body.Timestamp == "" {
		t.Fatal("expected timestamp")
	}
}

func TestHealthEndpointRejectsUnsupportedMethod(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/health", nil)
	rec := httptest.NewRecorder()

	NewHandler(slog.Default()).ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rec.Code)
	}
}
