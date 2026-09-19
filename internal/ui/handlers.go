package ui

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/capabilities"
	"github.com/wayne/telemetryiq/internal/governance"
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
	Successful       int
	Failed           int
	Abandoned        int
	Other            int
	Tools            []string
	MCP              insights.MCPInventory
	Skills           insights.SkillUsage
	ModelPerformance insights.ModelPerformance
	ContextWaste     insights.ContextWaste
	RiskyAccess      governance.RiskyAccess
	UnapprovedMCP    governance.UnapprovedMCP
	Integrations     []homeIntegrationRow
	InsightErr       string
	IntegrationErr   string
	Empty            bool
}

type homeIntegrationRow struct {
	Provider      string
	Tool          string
	Status        string
	LastSeen      string
	LastSeenState string
}

type homeIntegrationActivity struct {
	provider string
	tool     string
	latest   time.Time
}

type sessionsData struct {
	Sessions []sessionRow
	Scope    string
	Error    string
}

type sessionRow struct {
	SessionID         string
	Path              string
	PrimaryLabel      string
	SecondaryLabel    string
	Provider          string
	Tool              string
	State             string
	StartedAt         string
	StartedAtTitle    string
	ModelAvailability string
	IdentityScope     string
	IdentitySource    string
}

type sessionDetailData struct {
	Session         canonical.Session
	SessionID       string
	SessionPath     string
	Availability    []availabilityRow
	SessionMetadata []metadataRow
	Events          []timelineRow
	NextCursor      string
	RiskyAccess     governance.RiskyAccess
	UnapprovedMCP   governance.UnapprovedMCP
	GovernanceError string
	Error           string
	Confirm         bool
}

type availabilityRow struct {
	Label string
	State string
}

type metadataRow struct {
	Label    string
	Value    string
	Observed bool
	Machine  string
}

type timelineRow struct {
	EventID           string
	Title             string
	RawEventType      string
	OccurredAt        string
	Provider          string
	Tool              string
	Model             string
	InputTokens       tokenDisplay
	OutputTokens      tokenDisplay
	CachedInputTokens tokenDisplay
	ReasoningTokens   tokenDisplay
	HasOperation      bool
	OperationID       string
	OperationCategory fieldDisplay
	OperationTool     fieldDisplay
	OperationDuration fieldDisplay
	OperationOutcome  fieldDisplay
	ApprovalDecision  string
	ApprovalReason    string
	ApprovalTool      string
	LifecycleKind     string
	LifecyclePhase    string
	LifecycleStatus   string
	Entrypoint        string
	UnavailableFields []string
}

type tokenDisplay struct {
	Text     string
	Observed bool
	Machine  string
}

type fieldDisplay struct {
	Text     string
	Observed bool
	Machine  string
}

type insightsData struct {
	MCP              insights.MCPInventory
	Skills           insights.SkillUsage
	ModelPerformance insights.ModelPerformance
	ContextWaste     insights.ContextWaste
	Operations       insights.OperationStats
	OperationErr     string
	Error            string
}

// modelsData is the Models destination (#186 / N03) backed by the same
// model-performance reader as Insights — no separate ranking formula.
type modelsData struct {
	ModelPerformance insights.ModelPerformance
	Error            string
}

// governanceData is the server-rendered Governance findings view (#151)
// plus Access Rules tab shells (#160).
type governanceData struct {
	RiskyAccess    governance.RiskyAccess
	UnapprovedMCP  governance.UnapprovedMCP
	MCPServers     []mcpAllowlistOption
	SaveAvailable  bool
	Saved          bool
	Error          string
	ActiveRulesTab string
	RulesShell     *accessRulesShell // set for unavailable Access Rules tabs
}

// accessRulesShell is honest unavailable copy for Skills / Paths / Prompt tabs (#160).
type accessRulesShell struct {
	PanelID   string
	TabID     string
	Subject   string
	SchemaKey string
}

// Access Rules tab ids for ?rules= (issue #160). MCP is the only editable tab.
const (
	rulesTabMCP     = "mcp"
	rulesTabSkills  = "skills"
	rulesTabPaths   = "paths"
	rulesTabPrompts = "prompts"
)

type mcpAllowlistOption struct {
	Name     string
	Observed bool
	Checked  bool
}

const (
	htmlContentTypeHeader  = "Content-Type"
	htmlContentTypeValue   = "text/html; charset=utf-8"
	homeInsightUnavailable = "Insight and governance highlights unavailable."
)

type integrationsData struct {
	Tools            []integrationRow
	Empty            bool
	Capabilities     []capabilityRow
	Providers        []string
	CursorEnterprise cursorEnterpriseStatus
}

type integrationRow struct {
	Tool          string
	Provider      string
	LastSeen      string
	LastSeenState string
}

// capabilityRow is one headline matrix capability for the Integrations table.
type capabilityRow struct {
	Name   string
	States []string
}

// cursorEnterpriseStatus is honest ingest health for Cursor Enterprise OTEL
// (#132). Status is observed only when retained sessions use tool "cursor"
// (not local-dev "cursor-agent"). Absence uses availability enum unavailable;
// storage failures use unknown. Never a fabricated "connected" state.
type cursorEnterpriseStatus struct {
	Status   string
	LastSeen string
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
	sessions, err := s.listAllSessions(r, storage.SessionScopePrimary)
	if err != nil {
		data.InsightErr = homeInsightUnavailable
		data.IntegrationErr = "Integration health unavailable."
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
	sort.Strings(data.Tools)

	allSessions, err := s.listAllSessions(r, "")
	if err != nil {
		data.InsightErr = homeInsightUnavailable
		data.IntegrationErr = "Integration health unavailable."
		s.render(w, tmplHome, layoutData{Title: "Home", Nav: "home", Health: s.healthLabel(r), Content: data})
		return
	}
	data.Integrations = homeIntegrationRows(allSessions, nil)
	events, err := s.insightEventsForSessions(r, allSessions)
	if err != nil {
		data.InsightErr = homeInsightUnavailable
	} else {
		data.MCP = insights.MCPInventoryFromEvents(events)
		data.Skills = insights.SkillUsageFromEvents(events)
		data.ModelPerformance = insights.ModelPerformanceFromEvents(events)
		data.ContextWaste = insights.ContextWasteFromEvents(events, s.contextWasteThresholds)
		data.RiskyAccess = governance.RiskyAccessFromEvents(events)
		data.UnapprovedMCP = governance.UnapprovedMCPFromEvents(events, s.currentMCPAllowlist())
		data.Integrations = homeIntegrationRows(allSessions, events)
	}
	s.render(w, tmplHome, layoutData{Title: "Home", Nav: "home", Health: s.healthLabel(r), Content: data})
}

// homeIntegrationRows reports only identities and activity retained from the
// provider surface. A retained session proves the integration was observed;
// its latest event/completion/start timestamp is the last known activity. A
// missing timestamp remains unavailable instead of being rendered as zero.
func homeIntegrationRows(sessions []canonical.Session, events []canonical.Event) []homeIntegrationRow {
	byKey, sessionIdentities := homeSessionIntegrationActivities(sessions)
	for _, event := range events {
		provider, tool := homeEventIntegrationIdentity(event, sessionIdentities)
		recordHomeIntegrationActivity(byKey, provider, tool, event.OccurredAt)
	}
	return homeIntegrationRowsFromActivities(byKey)
}

func homeSessionIntegrationActivities(sessions []canonical.Session) (map[string]homeIntegrationActivity, map[string]homeIntegrationActivity) {
	byKey := map[string]homeIntegrationActivity{}
	bySession := make(map[string]homeIntegrationActivity, len(sessions))
	for _, session := range sessions {
		provider := strings.TrimSpace(session.Provider)
		tool := strings.TrimSpace(session.Tool)
		latest := session.StartedAt
		if session.CompletedAt != nil && session.CompletedAt.After(latest) {
			latest = *session.CompletedAt
		}
		bySession[session.SessionID] = homeIntegrationActivity{provider: provider, tool: tool}
		recordHomeIntegrationActivity(byKey, provider, tool, latest)
	}
	return byKey, bySession
}

func homeEventIntegrationIdentity(event canonical.Event, bySession map[string]homeIntegrationActivity) (string, string) {
	provider := strings.TrimSpace(event.Provider)
	tool := strings.TrimSpace(event.Tool)
	identity, ok := bySession[event.SessionID]
	if !ok {
		return provider, tool
	}
	if provider == "" {
		provider = identity.provider
	}
	if tool == "" {
		tool = identity.tool
	}
	return provider, tool
}

func recordHomeIntegrationActivity(byKey map[string]homeIntegrationActivity, provider, tool string, latest time.Time) {
	key := provider + "\x00" + tool
	activity := byKey[key]
	activity.provider = provider
	activity.tool = tool
	if latest.After(activity.latest) {
		activity.latest = latest
	}
	byKey[key] = activity
}

func homeIntegrationRowsFromActivities(byKey map[string]homeIntegrationActivity) []homeIntegrationRow {
	rows := make([]homeIntegrationRow, 0, len(byKey))
	for _, activity := range byKey {
		provider := activity.provider
		if provider == "" {
			provider = statusLabel("unavailable")
		}
		tool := activity.tool
		if tool == "" {
			tool = statusLabel("unavailable")
		}
		row := homeIntegrationRow{
			Provider:      provider,
			Tool:          tool,
			Status:        "observed",
			LastSeen:      statusLabel("unavailable"),
			LastSeenState: "unavailable",
		}
		if activity.provider == "" || activity.tool == "" {
			row.Status = "partial"
		}
		if !activity.latest.IsZero() {
			row.LastSeen = formatTimestamp(activity.latest)
			row.LastSeenState = "observed"
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		left := strings.ToLower(rows[i].Provider + "\x00" + rows[i].Tool)
		right := strings.ToLower(rows[j].Provider + "\x00" + rows[j].Tool)
		return left < right
	})
	return rows
}

func (s *Server) sessionsList(w http.ResponseWriter, r *http.Request) {
	scope, label := dashboardSessionScope(r.URL.Query().Get("scope"))
	sessions, err := s.listAllSessions(r, scope)
	data := sessionsData{Sessions: sessionRows(sessions), Scope: label}
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
	data := sessionDetailData{Session: session, SessionID: id, SessionPath: sessionPath(id), Availability: sessionAvailability(session), SessionMetadata: sessionMetadata(session), Events: rows, NextCursor: next, Confirm: r.URL.Query().Get("confirm") == "1"}
	if err != nil {
		data.Error = "Unable to load timeline."
	}
	allEvents, governanceErr := s.listSessionEvents(r, id)
	if governanceErr != nil {
		data.GovernanceError = "Session governance checks are unavailable because retained events could not be loaded."
		data.RiskyAccess, data.UnapprovedMCP = unavailableGovernanceChecklist()
	} else {
		data.RiskyAccess = governance.RiskyAccessFromEvents(allEvents)
		data.UnapprovedMCP = governance.UnapprovedMCPFromEvents(allEvents, s.currentMCPAllowlist())
	}
	s.render(w, tmplSessionDetail, layoutData{Title: "Session", Nav: "sessions", Health: s.healthLabel(r), Content: data})
}

func unavailableGovernanceChecklist() (governance.RiskyAccess, governance.UnapprovedMCP) {
	riskyAccess := governance.RiskyAccess{
		Findings: []governance.Finding{}, Outcome: governance.OutcomeIndeterminate, Visibility: "unavailable",
	}
	unapprovedMCP := governance.UnapprovedMCP{
		Findings: []governance.MCPServerFinding{}, Outcome: governance.OutcomeIndeterminate, Visibility: "unavailable",
	}
	return riskyAccess, unapprovedMCP
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
	w.Header().Set(htmlContentTypeHeader, htmlContentTypeValue)
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
	operations, err := s.listOperations(r)
	if err != nil {
		data.OperationErr = "Operation stats unavailable."
	} else {
		data.Operations = insights.OperationStatsFromOperations(operations)
	}
	s.render(w, tmplInsights, layoutData{Title: "Insights", Nav: "insights", Health: s.healthLabel(r), Content: data})
}

func (s *Server) modelsPage(w http.ResponseWriter, r *http.Request) {
	data := modelsData{}
	events, err := s.insightEvents(r)
	if err != nil {
		data.Error = "Unable to load model performance."
	} else {
		data.ModelPerformance = insights.ModelPerformanceFromEvents(events)
	}
	s.render(w, tmplModels, layoutData{Title: "Models", Nav: "models", Health: s.healthLabel(r), Content: data})
}

func (s *Server) governancePage(w http.ResponseWriter, r *http.Request) {
	data := s.governancePageData(r, nil)
	data.Saved = r.URL.Query().Get("saved") == "1"
	data.ActiveRulesTab = governanceRulesTab(r.URL.Query().Get("rules"))
	data.RulesShell = accessRulesShellFor(data.ActiveRulesTab)
	s.render(w, tmplGovernance, layoutData{Title: "Governance", Nav: "governance", Health: s.healthLabel(r), Content: data})
}

// governanceRulesTab clamps ?rules= to a known Access Rules tab; default mcp.
func governanceRulesTab(raw string) string {
	switch tab := strings.ToLower(strings.TrimSpace(raw)); tab {
	case rulesTabSkills, rulesTabPaths, rulesTabPrompts:
		return tab
	default:
		return rulesTabMCP
	}
}

// governanceRulesPath builds /governance?rules=… for Access Rules tab links.
func governanceRulesPath(tab string) string {
	tab = governanceRulesTab(tab)
	if tab == rulesTabMCP {
		return pathGovernance
	}
	return pathGovernance + "?rules=" + tab
}

func accessRulesShellFor(tab string) *accessRulesShell {
	switch tab {
	case rulesTabSkills:
		return &accessRulesShell{
			PanelID: "rules-panel-skills", TabID: "rules-tab-skills",
			Subject: "Skills allow/deny policy", SchemaKey: "governance.skills_allowlist",
		}
	case rulesTabPaths:
		return &accessRulesShell{
			PanelID: "rules-panel-paths", TabID: "rules-tab-paths",
			Subject: "Files & Paths allow/deny policy", SchemaKey: "governance.path_rules",
		}
	case rulesTabPrompts:
		return &accessRulesShell{
			PanelID: "rules-panel-prompts", TabID: "rules-tab-prompts",
			Subject: "Prompt Keywords protection policy", SchemaKey: "governance.prompt_keywords",
		}
	default:
		return nil
	}
}

func (s *Server) governanceMCPAllowlistSave(w http.ResponseWriter, r *http.Request) {
	if s.mcpAllowlistController == nil {
		data := s.governancePageData(r, nil)
		data.Error = "MCP allowlist saving is unavailable."
		s.renderGovernanceError(w, r, http.StatusServiceUnavailable, data)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		data := s.governancePageData(r, nil)
		data.Error = "Unable to read the MCP allowlist selection."
		s.renderGovernanceError(w, r, http.StatusUnprocessableEntity, data)
		return
	}
	selected := append([]string{}, r.Form["mcp_server"]...)
	data := s.governancePageData(r, selected)
	if data.Error != "" {
		s.renderGovernanceError(w, r, http.StatusServiceUnavailable, data)
		return
	}
	known := make(map[string]struct{}, len(data.MCPServers))
	configured := make(map[string]struct{})
	for _, name := range s.currentMCPAllowlist() {
		configured[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	for _, server := range data.MCPServers {
		key := strings.ToLower(strings.TrimSpace(server.Name))
		if _, alreadyConfigured := configured[key]; server.Observed || alreadyConfigured {
			known[key] = struct{}{}
		}
	}
	for _, name := range selected {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			data.Error = "MCP server names must not be blank."
		} else if _, ok := known[key]; !ok {
			data.Error = "The MCP allowlist contained an unknown server. Refresh and try again."
		}
		if data.Error != "" {
			s.renderGovernanceError(w, r, http.StatusUnprocessableEntity, data)
			return
		}
	}
	if err := s.mcpAllowlistController.SaveMCPAllowlist(selected); err != nil {
		data.Error = "Unable to save the MCP allowlist. The active policy was not changed."
		s.renderGovernanceError(w, r, http.StatusInternalServerError, data)
		return
	}
	http.Redirect(w, r, pathGovernance+"?saved=1", http.StatusSeeOther)
}

func (s *Server) renderGovernanceError(w http.ResponseWriter, r *http.Request, status int, data governanceData) {
	data.ActiveRulesTab = rulesTabMCP
	w.Header().Set(htmlContentTypeHeader, htmlContentTypeValue)
	w.WriteHeader(status)
	s.render(w, tmplGovernance, layoutData{Title: "Governance", Nav: "governance", Health: s.healthLabel(r), Content: data})
}

func (s *Server) governancePageData(r *http.Request, selected []string) governanceData {
	allowlist := s.currentMCPAllowlist()
	if selected != nil {
		allowlist = selected
	}
	data := governanceData{
		SaveAvailable:  s.mcpAllowlistController != nil,
		ActiveRulesTab: rulesTabMCP,
	}
	events, err := s.insightEvents(r)
	if err != nil {
		data.Error = "Unable to load governance findings."
		data.RiskyAccess = governance.RiskyAccess{Findings: []governance.Finding{}, Outcome: governance.OutcomeIndeterminate, Visibility: "unavailable"}
		data.UnapprovedMCP = governance.UnapprovedMCP{Findings: []governance.MCPServerFinding{}, Outcome: governance.OutcomeIndeterminate, Visibility: "unavailable"}
	} else {
		data.RiskyAccess = governance.RiskyAccessFromEvents(events)
		data.UnapprovedMCP = governance.UnapprovedMCPFromEvents(events, allowlist)
		data.MCPServers = mcpAllowlistOptions(insights.MCPInventoryFromEvents(events), allowlist)
	}
	if data.MCPServers == nil {
		data.MCPServers = mcpAllowlistOptions(insights.MCPInventory{}, allowlist)
	}
	return data
}

func mcpAllowlistOptions(inventory insights.MCPInventory, allowlist []string) []mcpAllowlistOption {
	options := make(map[string]mcpAllowlistOption, len(inventory.Servers)+len(allowlist))
	for _, name := range allowlist {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		options[strings.ToLower(name)] = mcpAllowlistOption{Name: name, Checked: true}
	}
	for _, server := range inventory.Servers {
		name := strings.TrimSpace(server.ServerName)
		if name == "" || server.IdentityState == "unavailable" {
			continue
		}
		key := strings.ToLower(name)
		option := options[key]
		option.Name = name
		option.Observed = true
		options[key] = option
	}
	result := make([]mcpAllowlistOption, 0, len(options))
	for _, option := range options {
		result = append(result, option)
	}
	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result
}

func (s *Server) integrationsPage(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.listAllSessions(r, "")
	data := integrationsData{
		Capabilities:     integrationCapabilityRows(),
		Providers:        append([]string(nil), capabilities.Providers...),
		CursorEnterprise: cursorEnterpriseStatus{Status: "unavailable"},
	}
	if err != nil {
		data.CursorEnterprise.Status = "unknown"
		s.render(w, tmplIntegrations, layoutData{Title: "Integrations", Nav: "integrations", Health: s.healthLabel(r), Error: "Unable to load integrations.", Content: data})
		return
	}
	byKey := map[string]homeIntegrationActivity{}
	for _, session := range sessions {
		latest := s.sessionLastActivity(r, session)
		recordHomeIntegrationActivity(byKey, session.Provider, session.Tool, latest)
	}
	for _, activity := range homeIntegrationRowsFromActivities(byKey) {
		data.Tools = append(data.Tools, integrationRow{
			Tool:          activity.Tool,
			Provider:      activity.Provider,
			LastSeen:      activity.LastSeen,
			LastSeenState: activity.LastSeenState,
		})
	}
	data.Empty = len(data.Tools) == 0
	data.CursorEnterprise = s.cursorEnterpriseFromSessions(r, sessions)
	s.render(w, tmplIntegrations, layoutData{Title: "Integrations", Nav: "integrations", Health: s.healthLabel(r), Content: data})
}

func integrationCapabilityRows() []capabilityRow {
	providers := capabilities.Providers
	rows := capabilities.HeadlineMatrix()
	out := make([]capabilityRow, 0, len(rows))
	for _, row := range rows {
		states := make([]string, 0, len(providers))
		for _, provider := range providers {
			states = append(states, string(row.StateFor(provider)))
		}
		out = append(out, capabilityRow{Name: row.Name, States: states})
	}
	return out
}

// cursorEnterpriseFromSessions derives Enterprise ingest status from retained
// sessions. Only tool "cursor" counts; last-seen is the latest event time when
// events are readable, otherwise max(StartedAt, CompletedAt).
func (s *Server) cursorEnterpriseFromSessions(r *http.Request, sessions []canonical.Session) cursorEnterpriseStatus {
	status := cursorEnterpriseStatus{Status: "unavailable"}
	var latest time.Time
	seen := false
	for _, session := range sessions {
		if session.Tool != "cursor" {
			continue
		}
		seen = true
		if at := s.sessionLastActivity(r, session); !at.IsZero() && (latest.IsZero() || at.After(latest)) {
			latest = at
		}
	}
	if !seen {
		return status
	}
	status.Status = "observed"
	if !latest.IsZero() {
		status.LastSeen = formatTimestamp(latest)
	}
	return status
}

func (s *Server) sessionLastActivity(r *http.Request, session canonical.Session) time.Time {
	latest := session.StartedAt
	if session.CompletedAt != nil && session.CompletedAt.After(latest) {
		latest = *session.CompletedAt
	}
	events, err := s.listSessionEvents(r, session.SessionID)
	if err != nil {
		return latest
	}
	for _, event := range events {
		if !event.OccurredAt.IsZero() && (latest.IsZero() || event.OccurredAt.After(latest)) {
			latest = event.OccurredAt
		}
	}
	return latest
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
	w.Header().Set(htmlContentTypeHeader, htmlContentTypeValue)
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

func (s *Server) listAllSessions(r *http.Request, scope storage.SessionScope) ([]canonical.Session, error) {
	if s.sessions == nil {
		return nil, errUnavailable
	}
	var all []canonical.Session
	var cursor *storage.SessionCursor
	for {
		page, err := s.sessions.ListSessions(r.Context(), storage.SessionFilter{Limit: 100, Cursor: cursor, Scope: scope})
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
	sessions, err := s.listAllSessions(r, "")
	if err != nil {
		return nil, err
	}
	return s.insightEventsForSessions(r, sessions)
}

func (s *Server) insightEventsForSessions(r *http.Request, sessions []canonical.Session) ([]canonical.Event, error) {
	if s.events == nil {
		return nil, errUnavailable
	}
	var events []canonical.Event
	for _, session := range sessions {
		batch, err := s.listSessionEvents(r, session.SessionID)
		if err != nil {
			return nil, err
		}
		events = append(events, batch...)
	}
	return events, nil
}

func (s *Server) listOperations(r *http.Request) ([]canonical.Operation, error) {
	if s.operations == nil {
		return nil, errUnavailable
	}
	return s.operations.ListOperations(r.Context(), storage.OperationFilter{})
}

func (s *Server) listSessionEvents(r *http.Request, sessionID string) ([]canonical.Event, error) {
	if s.events == nil {
		return nil, errUnavailable
	}
	var events []canonical.Event
	var cursor *storage.EventCursor
	for {
		page, err := s.events.ListEvents(r.Context(), storage.EventFilter{SessionID: sessionID, Limit: 1000, Cursor: cursor})
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
		operationID, hasOperation := observedString(event.Attributes["operation_id"])
		rows[i] = timelineRow{
			EventID:           event.EventID,
			Title:             eventTitle(event.EventType),
			RawEventType:      event.EventType,
			OccurredAt:        event.OccurredAt.UTC().Format(time.RFC3339Nano),
			Provider:          event.Provider,
			Tool:              event.Tool,
			Model:             attrString(event.Attributes["model"]),
			InputTokens:       tokenValue(event.Attributes["input_token_count"]),
			OutputTokens:      tokenValue(event.Attributes["output_token_count"]),
			CachedInputTokens: tokenValue(event.Attributes["cached_input_token_count"]),
			ReasoningTokens:   tokenValue(event.Attributes["reasoning_token_count"]),
			HasOperation:      hasOperation,
			OperationID:       operationID,
			OperationCategory: operationString(event.Attributes["category"], "unknown"),
			OperationTool:     operationToolDisplay(event.Attributes, event.ProviderExtensions),
			OperationDuration: operationDuration(event.Attributes["duration_ms"]),
			OperationOutcome:  operationString(event.Attributes["outcome"], "unknown"),
			ApprovalDecision:  attrString(event.Attributes["approval_decision"]),
			ApprovalReason:    attrString(event.Attributes["approval_reason_class"]),
			ApprovalTool:      approvalToolLabel(approvalToolQualifier(event.Attributes), event.Attributes["tool_name"]),
			LifecycleKind:     attrString(event.Attributes["lifecycle_kind"]),
			LifecyclePhase:    attrString(event.Attributes["lifecycle_phase"]),
			LifecycleStatus:   attrString(event.Attributes["lifecycle_status"]),
			Entrypoint:        attrString(event.Attributes["entrypoint"]),
			UnavailableFields: fieldLabels(unavailableFields(event.Attributes["unavailable_fields"])),
		}
	}
	return rows, next, nil
}

func sessionRows(sessions []canonical.Session) []sessionRow {
	rows := make([]sessionRow, len(sessions))
	for i, session := range sessions {
		started := relativeTime(session.StartedAt)
		if started == statusLabel("unavailable") {
			started = "started time unavailable"
		} else {
			started = "started " + started
		}
		tool := session.Tool
		if tool == "" {
			tool = statusLabel("unavailable")
		}
		provider := session.Provider
		if provider == "" {
			provider = statusLabel("unavailable")
		}
		rows[i] = sessionRow{
			SessionID:         session.SessionID,
			Path:              sessionPath(session.SessionID),
			PrimaryLabel:      tool + " · " + started,
			SecondaryLabel:    session.SessionID,
			Provider:          provider,
			Tool:              tool,
			State:             sessionStateLabel(session.State),
			StartedAt:         started,
			StartedAtTitle:    formatTimestamp(session.StartedAt),
			ModelAvailability: modelAvailability(session),
			IdentityScope:     identityScopeLabel(session),
			IdentitySource:    sessionIdentityAttribute(session, "identity_source", "unproven"),
		}
	}
	return rows
}

func dashboardSessionScope(raw string) (storage.SessionScope, string) {
	switch raw {
	case "observation":
		return storage.SessionScopeObservation, "Observations"
	case "all":
		return "", "All retained rows"
	default:
		return storage.SessionScopePrimary, "Primary sessions"
	}
}

func identityScopeLabel(session canonical.Session) string {
	switch sessionIdentityAttribute(session, "identity_scope", "unknown") {
	case "provider":
		return "Provider session"
	case "observation":
		return "Observation only"
	default:
		return "Identity unproven"
	}
}

func sessionIdentityAttribute(session canonical.Session, key, fallback string) string {
	value, ok := session.Attributes[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func attrString(value any) string {
	switch v := value.(type) {
	case string:
		if v == "" {
			return statusLabel("unavailable")
		}
		return v
	default:
		return statusLabel("unavailable")
	}
}

// approvalToolQualifier returns the value that qualifies the approved/denied
// tool label: Codex reports tool_namespace (e.g. "functions"), Claude reports
// tool_source (builtin/mcp). Whichever is present is used, so the origin of the
// tool stays visible in the timeline across providers.
func approvalToolQualifier(attributes map[string]any) any {
	if namespace, ok := attributes["tool_namespace"].(string); ok && strings.TrimSpace(namespace) != "" {
		return namespace
	}
	return attributes["tool_source"]
}

func approvalToolLabel(namespace, name any) string {
	toolName, hasName := name.(string)
	if !hasName || strings.TrimSpace(toolName) == "" {
		return statusLabel("unavailable")
	}
	toolNamespace, hasNamespace := namespace.(string)
	if !hasNamespace || strings.TrimSpace(toolNamespace) == "" {
		return toolName
	}
	return toolNamespace + "/" + toolName
}

func observedString(value any) (string, bool) {
	text, ok := value.(string)
	text = strings.TrimSpace(text)
	return text, ok && text != ""
}

func operationString(value any, missingState string) fieldDisplay {
	if text, ok := observedString(value); ok {
		return fieldDisplay{Text: text, Observed: true}
	}
	return fieldDisplay{Machine: missingState}
}

func operationToolDisplay(attributes, extensions map[string]any) fieldDisplay {
	name, ok := observedString(attributes["tool_name"])
	qualifier, hasQualifier := observedString(approvalToolQualifier(attributes))
	if !ok {
		name, qualifier, hasQualifier, ok = operationToolFromExtensions(extensions)
	}
	if !ok {
		return fieldDisplay{Machine: "unavailable"}
	}
	if hasQualifier {
		name = qualifier + "/" + name
	}
	return fieldDisplay{Text: name, Observed: true}
}

func operationToolFromExtensions(extensions map[string]any) (name, qualifier string, hasQualifier, observed bool) {
	for _, key := range []string{"tool_call", "sandbox_outcome", "mcp_call", "event"} {
		evidence, ok := extensions[key].(map[string]any)
		if !ok {
			continue
		}
		name, observed = observedString(evidence["tool_name"])
		if !observed {
			continue
		}
		qualifier, hasQualifier = observedString(approvalToolQualifier(evidence))
		return name, qualifier, hasQualifier, true
	}
	return "", "", false, false
}

func operationDuration(value any) fieldDisplay {
	duration, ok := durationMilliseconds(value)
	if !ok {
		return fieldDisplay{Machine: "unavailable"}
	}
	return fieldDisplay{Text: strconv.FormatFloat(duration, 'f', -1, 64) + " ms", Observed: true}
}

func durationMilliseconds(value any) (float64, bool) {
	var duration float64
	switch typed := value.(type) {
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, false
		}
		duration = parsed
	case int:
		duration = float64(typed)
	case int32:
		duration = float64(typed)
	case int64:
		duration = float64(typed)
	case uint:
		duration = float64(typed)
	case uint32:
		duration = float64(typed)
	case uint64:
		duration = float64(typed)
	case float32:
		duration = float64(typed)
	case float64:
		duration = typed
	default:
		return 0, false
	}
	if duration < 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return 0, false
	}
	return duration, true
}

func tokenValue(value any) tokenDisplay {
	switch v := value.(type) {
	case string:
		value, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return unavailableToken()
		}
		return tokenDisplay{Text: formatTokenCount(value), Observed: true}
	case int:
		return tokenDisplay{Text: formatTokenCount(int64(v)), Observed: true}
	case int64:
		return tokenDisplay{Text: formatTokenCount(v), Observed: true}
	case float64:
		return tokenDisplay{Text: formatTokenCount(int64(v)), Observed: true}
	default:
		return unavailableToken()
	}
}

func unavailableToken() tokenDisplay {
	return tokenDisplay{Text: statusLabel("unavailable"), Machine: "unavailable"}
}

func formatTokenCount(value int64) string {
	return strings.TrimSpace(formatOptionalInt64(&value, "tokens"))
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

func sessionAvailability(session canonical.Session) []availabilityRow {
	return []availabilityRow{
		{Label: "Provider", State: observedIf(session.Provider != "")},
		{Label: "Tool", State: observedIf(session.Tool != "")},
		{Label: "Session state", State: outcomeAvailability(session.State)},
		{Label: "Started", State: observedIf(!session.StartedAt.IsZero())},
		{Label: "Completed", State: observedIf(session.CompletedAt != nil)},
		{Label: "Model", State: modelAvailability(session)},
		{Label: "Branch", State: observedIf(sessionAttribute(session, "git_branch") != "")},
		{Label: "PR", State: observedIf(sessionAttribute(session, "pr_link") != "")},
		{Label: "Entrypoint", State: observedIf(sessionAttribute(session, "entrypoint") != "")},
		{Label: "Tool version", State: observedIf(sessionAttribute(session, "service_version") != "")},
	}
}

func sessionMetadata(session canonical.Session) []metadataRow {
	rows := make([]metadataRow, 0, 5)
	for _, field := range []struct {
		label    string
		key      string
		required bool
	}{
		{label: "Branch", key: "git_branch", required: true},
		{label: "PR", key: "pr_link", required: true},
		{label: "Entrypoint", key: "entrypoint", required: true},
		{label: "Service", key: "service_name"},
		{label: "Version", key: "service_version"},
	} {
		value := sessionAttribute(session, field.key)
		if value == "" && !field.required {
			continue
		}
		rows = append(rows, metadataRow{
			Label:    field.label,
			Value:    value,
			Observed: value != "",
			Machine:  observedIf(value != ""),
		})
	}
	return rows
}

func sessionAttribute(session canonical.Session, key string) string {
	value, ok := session.Attributes[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
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

func eventTitle(eventType string) string {
	switch eventType {
	case "model_interaction", "codex.sse_event", "api_request":
		return "Model interaction"
	case "api_error":
		return "Model error"
	case "operation":
		return "Operation"
	case "mcp_server_connection":
		return "MCP server connection"
	case "tool_decision", "codex.tool_decision":
		return "Tool decision"
	case "skill_invocation":
		return "Skill invocation"
	case "assistant_message":
		return "Assistant message"
	case "session.active":
		return "Session active"
	case "session.completed":
		return "Session completed"
	default:
		return strings.NewReplacer("_", " ", ".", " ").Replace(eventType)
	}
}

func fieldLabels(fields []string) []string {
	labels := make([]string, len(fields))
	for i, field := range fields {
		labels[i] = fieldLabel(field)
	}
	return labels
}

func fieldLabel(field string) string {
	field = strings.TrimPrefix(field, "attributes.")
	field = strings.TrimPrefix(field, "provider_extensions.")
	switch field {
	case "started_at":
		return "Started"
	case "completed_at":
		return "Completed"
	case "outcome":
		return "Session state"
	case "model":
		return "Model"
	case "latency":
		return "Latency"
	case "input_token_count":
		return "Input tokens"
	case "output_token_count":
		return "Output tokens"
	case "cached_input_token_count":
		return "Cached input tokens"
	case "reasoning_token_count":
		return "Reasoning tokens"
	default:
		return strings.TrimSpace(strings.NewReplacer("_", " ", ".", " ").Replace(field))
	}
}

func sessionStateLabel(state string) string {
	if strings.TrimSpace(state) == "" {
		return statusLabel("unknown")
	}
	return statusLabel(state)
}

func relativeTime(t time.Time) string {
	if t.IsZero() {
		return statusLabel("unavailable")
	}
	now := time.Now().UTC()
	if t.After(now) {
		return formatTimestamp(t)
	}
	d := now.Sub(t.UTC())
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return formatAgo(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return formatAgo(int(d.Hours()), "hour")
	case d < 30*24*time.Hour:
		return formatAgo(int(d.Hours()/24), "day")
	case d < 365*24*time.Hour:
		return formatAgo(int(d.Hours()/(24*30)), "month")
	default:
		return formatAgo(int(d.Hours()/(24*365)), "year")
	}
}

func formatAgo(value int, unit string) string {
	if value <= 1 {
		return "1 " + unit + " ago"
	}
	return fmt.Sprintf("%d %ss ago", value, unit)
}

func formatTimestamp(t time.Time) string {
	if t.IsZero() {
		return statusLabel("unavailable")
	}
	return t.UTC().Format(time.RFC3339)
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
