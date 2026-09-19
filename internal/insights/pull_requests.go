package insights

import (
	"net/url"
	"sort"
	"strings"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

const PullRequestsSchemaVersion = "0.1.0"

// PullRequests is the N02 destination view over retained session PR URLs.
// Only explicit http(s) pr_link values become groups; counters and branch/time
// never invent a PR row.
type PullRequests struct {
	SchemaVersion string             `json:"schema_version"`
	Groups        []PullRequestGroup `json:"groups"`
	Notes         []string           `json:"notes"`
	Query         string             `json:"query,omitempty"`
}

// PullRequestGroup is one retained PR URL and its distinct linked sessions.
type PullRequestGroup struct {
	URL          string               `json:"url"`
	SessionCount int                  `json:"session_count"`
	Sessions     []PullRequestSession `json:"sessions"`
}

// PullRequestSession is one session linked to a retained PR URL.
type PullRequestSession struct {
	SessionID  string `json:"session_id"`
	Provider   string `json:"provider"`
	Tool       string `json:"tool"`
	Branch     string `json:"branch,omitempty"`
	Repository string `json:"repository,omitempty"`
}

// PullRequestsFromSessions groups safe http(s) pr_link values from sessions.
// query filters by case-insensitive substring over URL, repository, and branch.
// Unsafe schemes and non-URL values are ignored (never become href targets).
func PullRequestsFromSessions(sessions []canonical.Session, query string) PullRequests {
	query = strings.TrimSpace(query)
	byURL := map[string]*PullRequestGroup{}
	order := make([]string, 0)

	for _, session := range sessions {
		raw := sessionStringAttr(session, "pr_link")
		safe, ok := safeHTTPURL(raw)
		if !ok {
			continue
		}
		group, exists := byURL[safe]
		if !exists {
			group = &PullRequestGroup{URL: safe}
			byURL[safe] = group
			order = append(order, safe)
		}
		if sessionAlreadyLinked(group, session.SessionID) {
			continue
		}
		group.Sessions = append(group.Sessions, PullRequestSession{
			SessionID:  session.SessionID,
			Provider:   session.Provider,
			Tool:       session.Tool,
			Branch:     sessionStringAttr(session, "git_branch"),
			Repository: sessionStringAttr(session, "repository"),
		})
		group.SessionCount = len(group.Sessions)
	}

	groups := make([]PullRequestGroup, 0, len(order))
	for _, key := range order {
		group := *byURL[key]
		if !matchesPullRequestQuery(group, query) {
			continue
		}
		groups = append(groups, group)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		return groups[i].URL < groups[j].URL
	})

	return PullRequests{
		SchemaVersion: PullRequestsSchemaVersion,
		Groups:        groups,
		Query:         query,
		Notes: []string{
			"Groups use only retained session pr_link HTTP(S) URLs; javascript/data and other schemes are rejected.",
			"Branch, repository, and provider facts appear only when observed on a linked session.",
			"pull_request.count and branch/time proximity never invent a PR URL. Provider URL capture proof is tracked in issues #183 and #184.",
		},
	}
}

func sessionStringAttr(session canonical.Session, key string) string {
	if session.Attributes == nil {
		return ""
	}
	value, ok := session.Attributes[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func sessionAlreadyLinked(group *PullRequestGroup, sessionID string) bool {
	for _, linked := range group.Sessions {
		if linked.SessionID == sessionID {
			return true
		}
	}
	return false
}

func matchesPullRequestQuery(group PullRequestGroup, query string) bool {
	if query == "" {
		return true
	}
	needle := strings.ToLower(query)
	if strings.Contains(strings.ToLower(group.URL), needle) {
		return true
	}
	for _, session := range group.Sessions {
		if strings.Contains(strings.ToLower(session.Branch), needle) {
			return true
		}
		if strings.Contains(strings.ToLower(session.Repository), needle) {
			return true
		}
	}
	return false
}

// safeHTTPURL accepts only http/https absolute URLs suitable for href rendering.
func safeHTTPURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	if parsed.Host == "" {
		return "", false
	}
	// Preserve the exact retained string as evidence; only validate scheme/host.
	return raw, true
}
