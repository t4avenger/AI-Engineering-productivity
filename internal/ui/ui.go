package ui

import (
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/storage"
)

const (
	cookieName       = "telemetryiq_auth"
	bulkDeletePhrase = "DELETE ALL"

	pathUnlock         = "/unlock"
	pathLogout         = "/logout"
	pathHome           = "/"
	pathSessions       = "/sessions"
	pathSessionsPrefix = "/sessions/"
	pathInsights       = "/insights"
	pathIntegrations   = "/integrations"
	pathPrivacy        = "/privacy"
	pathPrivacyDelete  = "/privacy/delete-all"
	pathCosts          = "/costs"
	pathStaticPrefix   = "/static/"

	tmplUnlock        = "unlock.html"
	tmplHome          = "home.html"
	tmplSessions      = "sessions.html"
	tmplSessionDetail = "session_detail.html"
	tmplTimelineRows  = "timeline_rows.html"
	tmplInsights      = "insights.html"
	tmplIntegrations  = "integrations.html"
	tmplPrivacy       = "privacy.html"
	tmplCosts         = "costs.html"
)

//go:embed templates/*.html static/*
var embedded embed.FS

// Server serves the local HTMX dashboard.
type Server struct {
	token     string
	expected  [32]byte
	sessions  storage.SessionReader
	deleter   storage.SessionDeleter
	events    storage.EventReader
	costs     storage.CostReader
	templates *template.Template
	static    http.Handler
}

// New builds a dashboard server. sessions may be a full Repository.
func New(token string, sessions storage.SessionReader) (*Server, error) {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"avail": availabilityLabel,
		"formatTime": func(t time.Time) string {
			if t.IsZero() {
				return "unavailable"
			}
			return t.UTC().Format(time.RFC3339)
		},
		"formatTimePtr": func(t *time.Time) string {
			if t == nil {
				return "unavailable"
			}
			return t.UTC().Format(time.RFC3339)
		},
		"microusd": formatMicroUSD,
	}).ParseFS(embedded, "templates/*.html")
	if err != nil {
		return nil, err
	}
	staticRoot, err := fs.Sub(embedded, "static")
	if err != nil {
		return nil, err
	}
	deleter, _ := sessions.(storage.SessionDeleter)
	events, _ := sessions.(storage.EventReader)
	costs, _ := sessions.(storage.CostReader)
	return &Server{
		token:     token,
		expected:  sha256.Sum256([]byte(token)),
		sessions:  sessions,
		deleter:   deleter,
		events:    events,
		costs:     costs,
		templates: tmpl,
		static:    http.FileServer(http.FS(staticRoot)),
	}, nil
}

func (s *Server) authenticated(r *http.Request) bool {
	return tokenMatches(s.expected, TokenFromRequest(r))
}

// TokenFromRequest returns a Bearer token or the dashboard auth cookie.
func TokenFromRequest(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if strings.HasPrefix(header, "Bearer ") {
		return strings.TrimPrefix(header, "Bearer ")
	}
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func tokenMatches(expected [32]byte, provided string) bool {
	if provided == "" {
		return false
	}
	actual := sha256.Sum256([]byte(provided))
	return subtle.ConstantTimeCompare(expected[:], actual[:]) == 1
}

func setAuthCookie(w http.ResponseWriter, r *http.Request, token string) {
	// Local-first daemon serves HTTP on loopback by default (ADR 0002).
	// Secure cookies are not sent on http://127.0.0.1; enable Secure under TLS.
	// Owner: maintainers; reason: loopback HTTP MVP; expiry: 2026-12-31.
	//nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil, // NOSONAR S2092 -- Secure under TLS; loopback HTTP otherwise
	})
}

func clearAuthCookie(w http.ResponseWriter, r *http.Request) {
	//nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil, // NOSONAR S2092 -- Secure under TLS; loopback HTTP otherwise
	})
}

func availabilityLabel(value string) string {
	switch value {
	case "observed", "partial", "unavailable", "unsupported", "unknown":
		return value
	case "":
		return "unavailable"
	default:
		return value
	}
}

func formatMicroUSD(amount *int64) string {
	if amount == nil {
		return "unavailable"
	}
	return fmt.Sprintf("%.6f", float64(*amount)/1_000_000)
}
