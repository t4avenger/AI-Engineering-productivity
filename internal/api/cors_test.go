package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"

	"github.com/wayne/telemetryiq/internal/privacy"
	"testing"
)

func TestAuthenticatedManagementAPIPermitsBrowserCORSPreflight(t *testing.T) {
	repository := sessionTestRepository(t)
	sanitizer, err := privacy.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	handler := NewAuthenticatedPersistentHandler(slog.Default(), sanitizer, repository, "test-token")
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/sessions", nil)
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	request.Header.Set("Access-Control-Request-Headers", "authorization")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:5173" {
		t.Fatalf("allow origin = %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Headers"); got != "Authorization, Content-Type" {
		t.Fatalf("allow headers = %q", got)
	}
}
