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

	"github.com/wayne/telemetryiq/internal/insights"
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
	token                  string
	expected               [32]byte
	sessions               storage.SessionReader
	deleter                storage.SessionDeleter
	events                 storage.EventReader
	costs                  storage.CostReader
	contextWasteThresholds insights.ContextWasteThresholds
	templates              *template.Template
	static                 http.Handler
}

// New builds a dashboard server. sessions may be a full Repository.
func New(token string, sessions storage.SessionReader, contextWasteThresholds insights.ContextWasteThresholds) (*Server, error) {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"avail":       availabilityLabel,
		"statusLabel": statusLabel,
		"statusClass": statusClass,
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
		// deref unwraps a *float64 so printf receives the value, not the pointer
		// (printing the pointer with %f yields %!f(*float64=0x...)). Callers guard
		// nil via {{with}}/{{if}}; the zero fallback is only defensive.
		"deref": func(v *float64) float64 {
			if v == nil {
				return 0
			}
			return *v
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
		token:                  token,
		expected:               sha256.Sum256([]byte(token)),
		sessions:               sessions,
		deleter:                deleter,
		events:                 events,
		costs:                  costs,
		contextWasteThresholds: contextWasteThresholds,
		templates:              tmpl,
		static:                 http.FileServer(http.FS(staticRoot)),
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

func setAuthCookie(w http.ResponseWriter, token string) {
	// Secure is required for cookie hygiene (Sonar S2092). Local HTTP unlock
	// works when the daemon is reached as http://localhost (Chromium treats
	// localhost as a secure context for Secure cookies). Prefer
	// TELEMETRYIQ_HOST=localhost over bare 127.0.0.1 for the HTML UI.
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   true,
	})
}

func clearAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   true,
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

// statusLabels maps every machine enum in the docs/ui/field-glossary.md
// "Enum Label Map" to its plain UI label. Kept as data (not a switch) so the
// shared status-badge partial can render any of these enums without tripping
// cyclomatic-complexity limits, and so the table stays a 1:1 mirror of the doc.
var statusLabels = map[string]string{
	// Availability / detection states.
	"observed":    "Seen in telemetry",
	"partial":     "Partially seen",
	"unavailable": "Not available from this provider",
	"unsupported": "Not supported",
	"unknown":     "Not proven yet",
	// MCP inventory states.
	"not_observed":         "Not seen in telemetry",
	"connected_but_unused": "Connected, never invoked",
	"used":                 "Invoked",
	"usage_unavailable":    "Usage not available",
	"fingerprinted":        "Fingerprint only",
	// Skill detection.
	"explicit": "Explicitly identified",
	"inferred": "Inferred by provider",
	// Outcome contracts.
	"success":   "Succeeded",
	"failed":    "Failed",
	"abandoned": "Abandoned",
	// Cost calculation.
	"calculated":     "Calculated estimate",
	"unknown_price":  "Price unknown",
	"not_calculable": "Not calculable",
}

// availabilityBadgeClasses are the availability/detection states that have a
// dedicated badge colour in app.css. Every other enum (MCP/outcome/cost) gets
// the neutral "status-unknown" treatment until its surface (#77/#78) styles it.
var availabilityBadgeClasses = map[string]bool{
	"observed": true, "partial": true, "unavailable": true,
	"unsupported": true, "unknown": true,
}

// statusLabel maps a machine enum to the plain UI label documented in
// docs/ui/field-glossary.md. An empty value is treated as "unknown"; any
// value outside the glossary falls back to the raw string so a state is never
// silently dropped.
func statusLabel(value string) string {
	if value == "" {
		return statusLabels["unknown"]
	}
	if label, ok := statusLabels[value]; ok {
		return label
	}
	return value
}

// statusClass maps an enum to its badge CSS class. Availability/detection
// states get their dedicated class; everything else (including empty or
// unrecognised values) uses the neutral "status-unknown" treatment.
func statusClass(value string) string {
	if availabilityBadgeClasses[value] {
		return "status-" + value
	}
	return "status-unknown"
}

func formatMicroUSD(amount *int64) string {
	if amount == nil {
		return "unavailable"
	}
	return fmt.Sprintf("%.6f", float64(*amount)/1_000_000)
}
