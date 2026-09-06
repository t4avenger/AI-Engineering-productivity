package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
)

const (
	defaultSessionLimit  = 50
	maximumSessionLimit  = 100
	invalidCursorMessage = "cursor is invalid"
	sessionUnavailable   = "session storage is unavailable"
	sessionNotFound      = "session was not found"
)

type sessionAPI struct {
	sessions    storage.SessionReader
	deleter     storage.SessionDeleter
	eventReader storage.EventReader
	costReader  storage.CostReader
	thresholds  InsightThresholds
}

type sessionListResponse struct {
	Data       []publicSession   `json:"data"`
	Pagination sessionPagination `json:"pagination"`
}

type sessionPagination struct {
	Limit      int     `json:"limit"`
	NextCursor *string `json:"next_cursor"`
}

type sessionDetailResponse struct {
	Data publicSession `json:"data"`
}

type publicSession struct {
	SchemaVersion      string            `json:"schema_version"`
	SessionID          string            `json:"session_id"`
	Provider           string            `json:"provider"`
	Tool               string            `json:"tool"`
	State              string            `json:"state"`
	StartedAt          time.Time         `json:"started_at"`
	CompletedAt        *time.Time        `json:"completed_at"`
	Attributes         map[string]any    `json:"attributes"`
	ProviderExtensions map[string]any    `json:"provider_extensions"`
	Availability       map[string]string `json:"availability"`
}

type sessionErrorResponse struct {
	Error sessionError `json:"error"`
}

type sessionError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type sessionCursor struct {
	StartedAt string `json:"started_at"`
	SessionID string `json:"session_id"`
}

func newSessionAPI(sessions storage.SessionReader, thresholds InsightThresholds) sessionAPI {
	deleter, _ := sessions.(storage.SessionDeleter)
	eventReader, _ := sessions.(storage.EventReader)
	costReader, _ := sessions.(storage.CostReader)
	return sessionAPI{sessions: sessions, deleter: deleter, eventReader: eventReader, costReader: costReader, thresholds: thresholds}
}

func (a sessionAPI) list(w http.ResponseWriter, r *http.Request) {
	if a.sessions == nil {
		writeSessionError(w, http.StatusServiceUnavailable, "sessions_unavailable", sessionUnavailable)
		return
	}
	filter, limit, cursor, err := parseSessionListQuery(r)
	if err != nil {
		writeSessionError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	filter.Cursor = storageCursor(cursor)
	filter.Limit = limit
	sessions, err := a.sessions.ListSessions(r.Context(), filter)
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "session_query_failed", "unable to query sessions")
		return
	}
	page, next := sessionPage(sessions, limit)
	response := sessionListResponse{Data: publicSessions(page), Pagination: sessionPagination{Limit: limit, NextCursor: next}}
	writeSessionJSON(w, http.StatusOK, response)
}

func (a sessionAPI) detail(w http.ResponseWriter, r *http.Request) {
	if a.sessions == nil {
		writeSessionError(w, http.StatusServiceUnavailable, "sessions_unavailable", sessionUnavailable)
		return
	}
	id := r.PathValue("id")
	if strings.TrimSpace(id) == "" {
		writeSessionError(w, http.StatusNotFound, "session_not_found", sessionNotFound)
		return
	}
	session, found, err := a.sessions.Session(r.Context(), id)
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "session_query_failed", "unable to query session")
		return
	}
	if !found {
		writeSessionError(w, http.StatusNotFound, "session_not_found", sessionNotFound)
		return
	}
	writeSessionJSON(w, http.StatusOK, sessionDetailResponse{Data: newPublicSession(session)})
}

func (a sessionAPI) delete(w http.ResponseWriter, r *http.Request) {
	if a.deleter == nil {
		writeSessionError(w, http.StatusServiceUnavailable, "sessions_unavailable", sessionUnavailable)
		return
	}
	id := r.PathValue("id")
	if strings.TrimSpace(id) == "" {
		writeSessionError(w, http.StatusNotFound, "session_not_found", sessionNotFound)
		return
	}
	_, found, err := a.sessions.Session(r.Context(), id)
	if err != nil {
		writeSessionError(w, http.StatusInternalServerError, "session_query_failed", "unable to query session")
		return
	}
	if !found {
		writeSessionError(w, http.StatusNotFound, "session_not_found", sessionNotFound)
		return
	}
	if err := a.deleter.DeleteSession(r.Context(), id); err != nil {
		writeSessionError(w, http.StatusInternalServerError, "session_delete_failed", "unable to delete session")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseSessionListQuery(r *http.Request) (storage.SessionFilter, int, *sessionCursor, error) {
	query := r.URL.Query()
	limit, err := parseSessionLimit(query.Get("limit"))
	if err != nil {
		return storage.SessionFilter{}, 0, nil, err
	}
	after, err := parseOptionalTime(query.Get("started_after"))
	if err != nil {
		return storage.SessionFilter{}, 0, nil, err
	}
	before, err := parseOptionalTime(query.Get("started_before"))
	if err != nil {
		return storage.SessionFilter{}, 0, nil, err
	}
	if after != nil && before != nil && !after.Before(*before) {
		return storage.SessionFilter{}, 0, nil, sessionQueryError("started_after must be before started_before")
	}
	cursor, err := decodeSessionCursor(query.Get("cursor"))
	if err != nil {
		return storage.SessionFilter{}, 0, nil, err
	}
	return storage.SessionFilter{Tool: query.Get("tool"), Model: query.Get("model"), Outcome: query.Get("outcome"), StartedAfter: after, StartedBefore: before}, limit, cursor, nil
}

func parseSessionLimit(raw string) (int, error) {
	if raw == "" {
		return defaultSessionLimit, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > maximumSessionLimit {
		return 0, sessionQueryError("limit must be an integer from 1 to 100")
	}
	return limit, nil
}

func parseOptionalTime(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, sessionQueryError("dates must use RFC3339 format")
	}
	result := parsed.UTC()
	return &result, nil
}

func decodeSessionCursor(raw string) (*sessionCursor, error) {
	if raw == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, sessionQueryError(invalidCursorMessage)
	}
	var cursor sessionCursor
	if json.Unmarshal(data, &cursor) != nil || cursor.SessionID == "" {
		return nil, sessionQueryError(invalidCursorMessage)
	}
	if _, err := time.Parse(time.RFC3339Nano, cursor.StartedAt); err != nil {
		return nil, sessionQueryError(invalidCursorMessage)
	}
	return &cursor, nil
}

func storageCursor(cursor *sessionCursor) *storage.SessionCursor {
	if cursor == nil {
		return nil
	}
	at, _ := time.Parse(time.RFC3339Nano, cursor.StartedAt)
	return &storage.SessionCursor{StartedAt: at, SessionID: cursor.SessionID}
}

func sessionPage(sessions []canonical.Session, limit int) ([]canonical.Session, *string) {
	if len(sessions) <= limit {
		return sessions, nil
	}
	page := sessions[:limit]
	last := page[len(page)-1]
	data, _ := json.Marshal(sessionCursor{StartedAt: last.StartedAt.UTC().Format(time.RFC3339Nano), SessionID: last.SessionID})
	cursor := base64.RawURLEncoding.EncodeToString(data)
	return page, &cursor
}

func publicSessions(sessions []canonical.Session) []publicSession {
	result := make([]publicSession, len(sessions))
	for index, session := range sessions {
		result[index] = newPublicSession(session)
	}
	return result
}

func newPublicSession(session canonical.Session) publicSession {
	return publicSession{
		SchemaVersion:      session.SchemaVersion,
		SessionID:          session.SessionID,
		Provider:           session.Provider,
		Tool:               session.Tool,
		State:              session.State,
		StartedAt:          session.StartedAt,
		CompletedAt:        session.CompletedAt,
		Attributes:         session.Attributes,
		ProviderExtensions: session.ProviderExtensions,
		Availability:       sessionAvailability(session),
	}
}

func sessionAvailability(session canonical.Session) map[string]string {
	return map[string]string{
		"provider":        observedIf(session.Provider != ""),
		"tool":            observedIf(session.Tool != ""),
		"outcome":         outcomeAvailability(session.State),
		"started_at":      observedIf(!session.StartedAt.IsZero()),
		"completed_at":    observedIf(session.CompletedAt != nil),
		"model":           modelAvailability(session),
		"observed_events": observedIf(numericAttribute(session.Attributes, "event_count")),
		"token_usage":     tokenUsageAvailability(session),
	}
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

func observedIf(ok bool) string {
	if ok {
		return "observed"
	}
	return "unavailable"
}

func modelAvailability(session canonical.Session) string {
	if value, ok := session.Attributes["model"].(string); ok && strings.TrimSpace(value) != "" {
		return "observed"
	}
	switch session.Tool {
	case "codex", "claude-code":
		return "unavailable"
	default:
		return "unknown"
	}
}

func tokenUsageAvailability(session canonical.Session) string {
	for _, key := range []string{"input_token_count", "output_token_count", "cached_input_tokens"} {
		if numericAttribute(session.Attributes, key) {
			return "observed"
		}
	}
	switch session.Tool {
	case "codex":
		return "partial"
	case "claude-code":
		return "unavailable"
	default:
		return "unknown"
	}
}

func numericAttribute(attributes map[string]any, key string) bool {
	switch value := attributes[key].(type) {
	case int, int32, int64, uint, uint32, uint64, float32, float64:
		return true
	case string:
		return strings.TrimSpace(value) != ""
	default:
		return false
	}
}

func writeSessionJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeSessionError(w http.ResponseWriter, status int, code, message string) {
	writeSessionJSON(w, status, sessionErrorResponse{Error: sessionError{Code: code, Message: message}})
}

type sessionQueryError string

func (e sessionQueryError) Error() string { return string(e) }
