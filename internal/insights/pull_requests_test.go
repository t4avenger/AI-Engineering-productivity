package insights

import (
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestPullRequestsFromSessionsGroupsSafeURLsOnly(t *testing.T) {
	now := time.Now().UTC()
	sessions := make([]canonical.Session, 0, 8)
	for _, tc := range []struct {
		id, provider, tool string
		attrs              map[string]any
	}{
		{"s-https", "anthropic", "claude-code", map[string]any{
			"pr_link": "https://github.com/org/repo/pull/12", "git_branch": "feature/pr-12",
			"repository": "org/repo", "pull_request.count": 3,
		}},
		{"s-dup", "anthropic", "claude-code", map[string]any{
			"pr_link": "https://github.com/org/repo/pull/12", "git_branch": "feature/pr-12",
		}},
		{"s-dup", "anthropic", "claude-code", map[string]any{
			"pr_link": "https://github.com/org/repo/pull/12",
		}},
		{"s-http", "openai", "codex", map[string]any{"pr_link": "http://example.test/pulls/1"}},
		{"s-branch-only", "openai", "codex", map[string]any{"git_branch": "same-time-branch"}},
		{"s-js", "openai", "codex", map[string]any{"pr_link": "javascript:alert(1)"}},
		{"s-data", "openai", "codex", map[string]any{"pr_link": "data:text/html,hi"}},
		{"s-long", "openai", "codex", map[string]any{
			"pr_link": "https://github.com/org/repo/pull/99?utm=" + strings.Repeat("a", 200),
		}},
	} {
		sessions = append(sessions, canonical.Session{
			SessionID: tc.id, Provider: tc.provider, Tool: tc.tool, StartedAt: now, Attributes: tc.attrs,
		})
	}

	got := PullRequestsFromSessions(sessions, "")
	if len(got.Groups) != 3 {
		t.Fatalf("groups = %d, want 3 (https, http, long); got %#v", len(got.Groups), got.Groups)
	}

	byURL := map[string]PullRequestGroup{}
	for _, group := range got.Groups {
		byURL[group.URL] = group
	}

	httpsGroup := byURL["https://github.com/org/repo/pull/12"]
	if httpsGroup.SessionCount != 2 {
		t.Fatalf("https session_count = %d, want 2", httpsGroup.SessionCount)
	}
	if httpsGroup.Sessions[0].Repository != "org/repo" || httpsGroup.Sessions[0].Branch != "feature/pr-12" {
		t.Fatalf("https first session metadata = %#v", httpsGroup.Sessions[0])
	}
	for _, bad := range []string{"javascript:alert(1)", "data:text/html,hi"} {
		if _, ok := byURL[bad]; ok {
			t.Fatalf("%q must not become a group", bad)
		}
	}
	for _, group := range got.Groups {
		if strings.Contains(group.URL, "pull_request") {
			t.Fatalf("must not invent URL from counter: %q", group.URL)
		}
	}
}

func TestPullRequestsFromSessionsSearchAndEmpty(t *testing.T) {
	sessions := []canonical.Session{
		{SessionID: "a", Provider: "anthropic", Tool: "claude-code", Attributes: map[string]any{
			"pr_link": "https://github.com/acme/one/pull/1", "git_branch": "feat/one", "repository": "acme/one",
		}},
		{SessionID: "b", Provider: "openai", Tool: "codex", Attributes: map[string]any{
			"pr_link": "https://github.com/acme/two/pull/2", "git_branch": "feat/two",
		}},
	}

	cases := []struct {
		name  string
		query string
		want  int
		url   string
	}{
		{name: "branch", query: "feat/two", want: 1, url: "https://github.com/acme/two/pull/2"},
		{name: "repository", query: "acme/one", want: 1, url: "https://github.com/acme/one/pull/1"},
		{name: "url", query: "pull/1", want: 1, url: "https://github.com/acme/one/pull/1"},
		{name: "no-match", query: "missing-query", want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PullRequestsFromSessions(sessions, tc.query)
			if len(got.Groups) != tc.want {
				t.Fatalf("groups = %#v, want %d", got.Groups, tc.want)
			}
			if tc.want == 1 && got.Groups[0].URL != tc.url {
				t.Fatalf("url = %q, want %q", got.Groups[0].URL, tc.url)
			}
		})
	}

	empty := PullRequestsFromSessions(nil, "")
	if len(empty.Groups) != 0 {
		t.Fatalf("empty sessions should yield no groups: %#v", empty.Groups)
	}
}

func TestSafeHTTPURL(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"https://github.com/a/b/pull/1", true},
		{"http://example.test/x", true},
		{"  https://example.test/y  ", true},
		{"javascript:alert(1)", false},
		{"data:text/plain,hi", false},
		{"file:///etc/passwd", false},
		{"ftp://example.test/x", false},
		{"", false},
		{"not a url", false},
		{"https://", false},
		{"/relative/path", false},
	}
	for _, tc := range cases {
		_, ok := safeHTTPURL(tc.raw)
		if ok != tc.want {
			t.Fatalf("safeHTTPURL(%q) = %v, want %v", tc.raw, ok, tc.want)
		}
	}
}
