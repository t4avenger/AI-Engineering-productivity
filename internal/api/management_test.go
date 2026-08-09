package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wayne/telemetryiq/internal/privacy"
	"github.com/wayne/telemetryiq/internal/storage"
)

func TestAuthenticatedManagementAPIRequiresTokenAndDeletesAll(t *testing.T) {
	repository := sessionTestRepository(t)
	sanitizer, err := privacy.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewAuthenticatedPersistentHandler(slog.Default(), sanitizer, repository, "test-token"))
	t.Cleanup(server.Close)

	unauthenticated, err := http.Get(server.URL + "/api/v1/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unauthenticated.Body.Close() }()
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthenticated.StatusCode)
	}

	request, err := http.NewRequest(http.MethodDelete, server.URL+"/api/v1/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("bulk delete status = %d", response.StatusCode)
	}
	sessions, err := repository.ListSessions(context.Background(), storage.SessionFilter{Limit: 10})
	if err != nil || len(sessions) != 0 {
		t.Fatalf("sessions after deletion = %#v, %v", sessions, err)
	}
}
