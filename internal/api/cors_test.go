package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthenticatedManagementAPIPermitsOPTIONSPreflight(t *testing.T) {
	repository := sessionTestRepository(t)
	handler := NewAuthenticatedPersistentHandler(slog.Default(), repository, "test-token", DefaultInsightThresholds())
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/sessions", nil)
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	request.Header.Set("Access-Control-Request-Headers", "authorization")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Headers"); got != "Authorization, Content-Type" {
		t.Fatalf("allow headers = %q", got)
	}
}
