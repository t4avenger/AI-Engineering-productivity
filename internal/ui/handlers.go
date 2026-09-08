package ui

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/insights"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
)

type layoutData struct {
	Title   string
	Nav     string
	Health  healthStatus
	Content any
	Error   string
}

// healthStatus is the honest daemon health rendered in the app shell. Text is
// the human label ("Healthy"/"Unknown"/"Degraded"); State selects the badge
// class (ok/unknown/degraded).
type healthStatus struct {
	Text  string
	State string
}

type unlockData struct {
	Error string
}

type homeData struct {
	Successful int
	Failed     int
	Abandoned  int
	Other      int
	Tools      []string
	MCPUnused  int
	SkillCount int
	InsightErr string
	Empty      bool
}

type sessionsData struct {
	Sessions []canonical.Session
	Error    string
}

type sessionDetailData struct {
	Session      canonical.Session
	SessionID    string
	Availability map[string]string
	Events       []timelineRow
	NextCursor   string
	Error        string
	Confirm      bool
}

type timelineRow struct {
	EventID           string
	EventType         string
	OccurredAt        string
	Provider          string
	Tool              string
	Model             string
	InputTokens       string
	OutputTokens      string
	UnavailableFields []string
}

type insightsData struct {
	MCP              insights.MCPInventory
	Skills           insights.SkillUsage
	ModelPerformance insights.ModelPerformance
	ContextWaste     insights.ContextWaste
	Error            string
}

type integrationsData struct {
	Tools []integrationRow
	Empty bool
}

type integrationRow struct {
	Tool     string
	Provider string
}

type privacyData struct {
	Error   string
	Confirm bool
}

type costsData struct {
	Currency string
	Amount   *int64
	Statuses map[string]int
	Error    string
}

func (s *Server) unlockGet(w http.ResponseWriter, r *http.Request) {
	if s.authenticated(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, tmplUnlock, layoutData{Title: "Unlock", Nav: "", Content: unlockData{}})
}

func (s *Server) unlockPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, tmplUnlock, layoutData{Title: "Unlock", Content: unlockData{Error: "Unable to read form."}})
		return
	}
	token := strings.TrimSpace(r.FormValue("token"))
	if !tokenMatches(s.expected, token) {
		s.render(w, tmplUnlock, layoutData{Title: "Unlock", Content: unlockData{Error: "Token rejected. Run make auth-token and try again."}})
		return
	}
	setAuthCookie(w, token)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	clearAuthCookie(w)
	http.Redirect(w, r, pathUnlock, http.StatusSeeOther)
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	data := homeData{}
	sessions, err := s.listAllSessions(r)
	if err != nil {
		s.render(w, tmplHome, layoutData{Title: "Home", Nav: "home", Health: s.healthLabel(r), Error: "Unable to load sessions.", Content: data})
		return
	}
	data.Empty = len(sessions) == 0
	tools := map[string]struct{}{}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	for _, session := range sessions {
		tools[session.Tool] = struct{}{}
		if session.StartedAt.UTC().Before(today) {
			continue
		}
		switch session.State {
		case "completed":
			data.Successful++
		case "failed":
			data.Failed++
		case "abandoned":
			data.Abandoned++
		default:
			data.Other++
		}
	}
	for tool := range tools {
		data.Tools = append(data.Tools, tool)
	}
	events, err := s.insightEvents(r)
	if err != nil {
		data.InsightErr = "Insight highlights unavailable."
	} else {
		mcp := insights.MCPInventoryFromEvents(events)
		skills := insights.SkillUsageFromEvents(events)
		data.MCPUnused = mcp.Totals.UnusedServers
		data.SkillCount = skills.Totals.ObservedSkills
	}
	s.render(w, tmplHome, layoutData{Title: "Home", Nav: "home", Health: s.healthLabel(r), Content: data})
}

func (s *Server) sessionsList(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.listAllSessions(r)
	data := sessionsData{Sessions: sessions}
	if err != nil {
		data.Error = "Unable to load sessions."
	}
	s.render(w, tmplSessions, layoutData{Title: "Sessions", Nav: "sessions", Health: s.healthLabel(r), Content: data})
}

func (s *Server) sessionDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := safePathID(strings.TrimPrefix(r.URL.Path, pathSessionsPrefix))
	if !ok {
		http.NotFound(w, r)
		return
	}
	session, found, err := s.sessions.Session(r.Context(), id)
	if err != nil {
		s.render(w, tmplSessionDetail, layoutData{Title: "Session", Nav: "sessions", Health: s.healthLabel(r), Error: "Unable to load session.", Content: sessionDetailData{}})
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	rows, next, err := s.loadTimeline(r, id, r.URL.Query().Get("cursor"))
	data := sessionDetailData{Session: session, SessionID: id, Availability: sessionAvailability(session), Events: rows, NextCursor: next, Confirm: r.URL.Query().Get("confirm") == "1"}
	if err != nil {
		data.Error = "Unable to load timeline."
	}
	s.render(w, tmplSessionDetail, layoutData{Title: "Session", Nav: "sessions", Health: s.healthLabel(r), Content: data})
}

func (s *Server) sessionTimelinePartial(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, pathSessionsPrefix)
	id, ok := safePathID(strings.TrimSuffix(path, "/timeline"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	rows, next, err := s.loadTimeline(r, id, r.URL.Query().Get("cursor"))
	if err != nil {
		http.Error(w, "unable to load timeline", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.templates.ExecuteTemplate(w, tmplTimelineRows, struct {
		SessionID  string
		Events     []timelineRow
		NextCursor string
	}{SessionID: id, Events: rows, NextCursor: next})
}

func (s *Server) sessionDelete(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, pathSessionsPrefix)
	id, ok := safePathID(strings.TrimSuffix(path, "/delete"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	if s.deleter == nil {
		http.Error(w, "delete unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil || r.FormValue("confirm") != "1" {
		// Confirm flow uses query on the detail page; avoid open redirects by
		// bouncing to the sessions list when confirmation is missing.
		http.Redirect(w, r, pathSessions, http.StatusSeeOther)
		return
	}
	if err := s.deleter.DeleteSession(r.Context(), id); err != nil {
		http.Error(w, "unable to delete session", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, pathSessions, http.StatusSeeOther)
}

func (s *Server) insightsPage(w http.ResponseWriter, r *http.Request) {
	data := insightsData{}
	events, err := s.insightEvents(r)
	if err != nil {
		data.Error = "Unable to load insights."
	} else {
		data.MCP = insights.MCPInventoryFromEvents(events)
		data.Skills = insights.SkillUsageFromEvents(events)
		data.ModelPerformance = insights.ModelPerformanceFromEvents(events)
		data.ContextWaste = insights.ContextWasteFromEvents(events, s.contextWasteThresholds)
	}
	s.render(w, tmplInsights, layoutData{Title: "Insights", Nav: "insights", Health: s.healthLabel(r), Content: data})
}

func (s *Server) integrationsPage(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.listAllSessions(r)
	data := integrationsData{}
	if err != nil {
		s.render(w, tmplIntegrations, layoutData{Title: "Integrations", Nav: "integrations", Health: s.healthLabel(r), Error: "Unable to load integrations.", Content: data})
		return
	}
	seen := map[string]integrationRow{}
	for _, session := range sessions {
		key := session.Tool + "|" + session.Provider
		seen[key] = integrationRow{Tool: session.Tool, Provider: session.Provider}
	}
	for _, row := range seen {
		data.Tools = append(data.Tools, row)
	}
	data.Empty = len(data.Tools) == 0
	s.render(w, tmplIntegrations, layoutData{Title: "Integrations", Nav: "integrations", Health: s.healthLabel(r), Content: data})
}

func (s *Server) privacyPage(w http.ResponseWriter, r *http.Request) {
	errMsg := ""
	switch r.URL.Query().Get("error") {
	case "phrase":
		errMsg = "Type DELETE ALL exactly to confirm."
	case "form":
		errMsg = "Unable to read form."
	}
	s.render(w, tmplPrivacy, layoutData{
		Title:   "Privacy",
		Nav:     "privacy",
		Health:  s.healthLabel(r),
		Content: privacyData{Confirm: r.URL.Query().Get("confirm") == "1", Error: errMsg},
	})
}

func (s *Server) privacyDeleteAll(w http.ResponseWriter, r *http.Request) {
	if s.deleter == nil {
		http.Error(w, "delete unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/privacy?confirm=1&error=form", http.StatusSeeOther)
		return
	}
	if r.FormValue("confirmation") != bulkDeletePhrase {
		http.Redirect(w, r, "/privacy?confirm=1&error=phrase", http.StatusSeeOther)
		return
	}
	if err := s.deleter.DeleteAllSessions(r.Context()); err != nil {
		http.Error(w, "unable to delete sessions", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, pathSessions, http.StatusSeeOther)
}

func (s *Server) costsPage(w http.ResponseWriter, r *http.Request) {
	data := costsData{Statuses: map[string]int{}}
	if s.costs == nil {
		data.Error = "Cost storage is unavailable."
		s.render(w, tmplCosts, layoutData{Title: "Costs", Nav: "costs", Health: s.healthLabel(r), Content: data})
		return
	}
	records, err := s.costs.ListCostRecords(r.Context(), "")
	if err != nil {
		data.Error = "Unable to load costs."
		s.render(w, tmplCosts, layoutData{Title: "Costs", Nav: "costs", Health: s.healthLabel(r), Content: data})
		return
	}
	var amount int64
	var hasKnownAmount bool
	for _, record := range records {
		if data.Currency == "" {
			data.Currency = record.Currency
		}
		data.Statuses[record.Status]++
		if record.AmountMicrousd != nil {
			hasKnownAmount = true
			amount += *record.AmountMicrousd
		}
	}
	if hasKnownAmount {
		data.Amount = &amount
	}
	s.render(w, tmplCosts, layoutData{Title: "Costs", Nav: "costs", Health: s.healthLabel(r), Content: data})
}

func (s *Server) render(w http.ResponseWriter, page string, data layoutData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, page, data); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

// healthLabel reports daemon health honestly. Rendering this page already
// proves the HTTP and auth path is up, so storage is the only uncertain
// dependency: a bounded one-row probe distinguishes a reachable store
// (Healthy) from a missing one (Unknown) or a failing one (Degraded). The
// happy path keeps the literal "Healthy" text the dashboard journey e2e
// asserts.
func (s *Server) healthLabel(r *http.Request) healthStatus {
	if s.sessions == nil {
		return healthStatus{Text: "Unknown", State: "unknown"}
	}
	if _, err := s.sessions.ListSessions(r.Context(), storage.SessionFilter{Limit: 1}); err != nil {
		return healthStatus{Text: "Degraded", State: "degraded"}
	}
	return healthStatus{Text: "Healthy", State: "ok"}
}

func (s *Server) listAllSessions(r *http.Request) ([]canonical.Session, error) {
	if s.sessions == nil {
		return nil, errUnavailable
	}
	var all []canonical.Session
	var cursor *storage.SessionCursor
	for {
		page, err := s.sessions.ListSessions(r.Context(), storage.SessionFilter{Limit: 100, Cursor: cursor})
		if err != nil {
			return nil, err
		}
		batch := page
		cursor = nil
		if len(page) > 100 {
			batch = page[:100]
			last := batch[len(batch)-1]
			cursor = &storage.SessionCursor{StartedAt: last.StartedAt, SessionID: last.SessionID}
		}
		all = append(all, batch...)
		if cursor == nil {
			return all, nil
		}
	}
}

func (s *Server) insightEvents(r *http.Request) ([]canonical.Event, error) {
	if s.sessions == nil || s.events == nil {
		return nil, errUnavailable
	}
	sessions, err := s.listAllSessions(r)
	if err != nil {
		return nil, err
	}
	var events []canonical.Event
	for _, session := range sessions {
		var cursor *storage.EventCursor
		for {
			page, err := s.events.ListEvents(r.Context(), storage.EventFilter{SessionID: session.SessionID, Limit: 1000, Cursor: cursor})
			if err != nil {
				return nil, err
			}
			batch := page
			cursor = nil
			if len(page) > 1000 {
				batch = page[:1000]
				last := batch[len(batch)-1]
				cursor = &storage.EventCursor{OccurredAt: last.OccurredAt, EventID: last.EventID}
			}
			events = append(events, batch...)
			if cursor == nil {
				break
			}
		}
	}
	return events, nil
}

func (s *Server) loadTimeline(r *http.Request, sessionID, cursorRaw string) ([]timelineRow, string, error) {
	if s.events == nil {
		return nil, "", errUnavailable
	}
	var cursor *storage.EventCursor
	if cursorRaw != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursorRaw)
		if err != nil {
			return nil, "", err
		}
		var decoded struct {
			OccurredAt string `json:"occurred_at"`
			EventID    string `json:"event_id"`
		}
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, "", err
		}
		at, err := time.Parse(time.RFC3339Nano, decoded.OccurredAt)
		if err != nil {
			return nil, "", err
		}
		cursor = &storage.EventCursor{OccurredAt: at, EventID: decoded.EventID}
	}
	const limit = 50
	events, err := s.events.ListEvents(r.Context(), storage.EventFilter{SessionID: sessionID, Limit: limit, Cursor: cursor})
	if err != nil {
		return nil, "", err
	}
	page := events
	var next string
	if len(events) > limit {
		page = events[:limit]
		last := page[len(page)-1]
		encoded, _ := json.Marshal(struct {
			OccurredAt string `json:"occurred_at"`
			EventID    string `json:"event_id"`
		}{OccurredAt: last.OccurredAt.UTC().Format(time.RFC3339Nano), EventID: last.EventID})
		next = base64.RawURLEncoding.EncodeToString(encoded)
	}
	rows := make([]timelineRow, len(page))
	for i, event := range page {
		rows[i] = timelineRow{
			EventID:           event.EventID,
			EventType:         event.EventType,
			OccurredAt:        event.OccurredAt.UTC().Format(time.RFC3339Nano),
			Provider:          event.Provider,
			Tool:              event.Tool,
			Model:             attrString(event.Attributes["model"]),
			InputTokens:       attrString(event.Attributes["input_token_count"]),
			OutputTokens:      attrString(event.Attributes["output_token_count"]),
			UnavailableFields: unavailableFields(event.Attributes["unavailable_fields"]),
		}
	}
	return rows, next, nil
}

func attrString(value any) string {
	switch v := value.(type) {
	case string:
		if v == "" {
			return "unavailable"
		}
		return v
	default:
		return "unavailable"
	}
}

func unavailableFields(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

var errUnavailable = errors.New("storage unavailable")

func sessionAvailability(session canonical.Session) map[string]string {
	return map[string]string{
		"provider":     observedIf(session.Provider != ""),
		"tool":         observedIf(session.Tool != ""),
		"outcome":      outcomeAvailability(session.State),
		"started_at":   observedIf(!session.StartedAt.IsZero()),
		"completed_at": observedIf(session.CompletedAt != nil),
		"model":        modelAvailability(session),
	}
}

func observedIf(ok bool) string {
	if ok {
		return "observed"
	}
	return "unavailable"
}

func outcomeAvailability(state string) string {
	if state == "" {
		return "unavailable"
	}
	if state == "unknown" {
		return "unknown"
	}
	return "observed"
}

func modelAvailability(session canonical.Session) string {
	if raw, ok := session.Attributes["model"]; ok {
		if s, ok := raw.(string); ok && s != "" {
			return "observed"
		}
	}
	return "unavailable"
}

func safePathID(raw string) (string, bool) {
	id := strings.Trim(raw, "/")
	if id == "" || strings.Contains(id, "/") || strings.Contains(id, `\`) || strings.Contains(id, "..") {
		return "", false
	}
	for _, r := range id {
		if !isSessionIDRune(r) {
			return "", false
		}
	}
	return id, true
}

func isSessionIDRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == '-' || r == '_' || r == '.' || r == ':':
		return true
	default:
		return false
	}
}
