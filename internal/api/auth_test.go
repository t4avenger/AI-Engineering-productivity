package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagementAuthProtectsSessionsOnly(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := withManagementAuth("test-token", next)
	for _, test := range []struct {
		name   string
		path   string
		token  string
		status int
	}{
		{name: "health remains available", path: "/api/v1/health", status: http.StatusNoContent},
		{name: "missing token", path: "/api/v1/sessions", status: http.StatusUnauthorized},
		{name: "invalid token", path: "/api/v1/sessions/a", token: "wrong", status: http.StatusUnauthorized},
		{name: "valid token", path: "/api/v1/sessions", token: "test-token", status: http.StatusNoContent},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, test.path, nil)
			if test.token != "" {
				req.Header.Set("Authorization", "Bearer "+test.token)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d", recorder.Code, test.status)
			}
		})
	}
}
