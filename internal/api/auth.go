package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// withManagementAuth protects local session data without imposing an
// unverified header requirement on supported OTLP collectors.
func withManagementAuth(token string, next http.Handler) http.Handler {
	expected := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sessions" && !strings.HasPrefix(r.URL.Path, "/api/v1/sessions/") {
			next.ServeHTTP(w, r)
			return
		}
		header := r.Header.Get("Authorization")
		provided := strings.TrimPrefix(header, "Bearer ")
		actual := sha256.Sum256([]byte(provided))
		if !strings.HasPrefix(header, "Bearer ") || subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
			writeSessionError(w, http.StatusUnauthorized, "authentication_required", "local API authentication is required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
