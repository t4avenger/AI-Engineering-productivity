package normalize

import (
	"reflect"
	"testing"
)

func TestAttachPRLinkEvidenceExtractsSortedUniqueCandidates(t *testing.T) {
	attributes := map[string]any{}
	extensions := map[string]any{}
	fields := map[string]any{
		"full_command": "gh pr view https://github.com/acme/repo/pull/7",
		"output":       "opened https://gitlab.example.test/group/project/-/merge_requests/3 and https://github.com/acme/repo/pull/7",
		"note":         "https://example.test/issues/9 is unrelated",
	}
	AttachPRLinkEvidence(attributes, extensions, fields, []string{"full_command", "output", "note"})

	wantCandidates := []string{
		"https://github.com/acme/repo/pull/7",
		"https://gitlab.example.test/group/project/-/merge_requests/3",
	}
	if got, _ := attributes["pr_link_candidates"].([]string); !reflect.DeepEqual(got, wantCandidates) {
		t.Fatalf("pr_link_candidates = %#v, want %#v", attributes["pr_link_candidates"], wantCandidates)
	}
	evidence, ok := extensions["pr_link_evidence"].([]map[string]string)
	if !ok || len(evidence) != 3 {
		t.Fatalf("pr_link_evidence = %#v, want 3 field/url rows", extensions["pr_link_evidence"])
	}
}

func TestAttachPRLinkEvidenceWritesNothingOnAbsence(t *testing.T) {
	attributes := map[string]any{}
	extensions := map[string]any{}
	AttachPRLinkEvidence(attributes, extensions, map[string]any{
		"full_command": "ls -la",
		"output":       "https://example.test/issues/9",
	}, []string{"full_command", "output"})

	if _, present := attributes["pr_link_candidates"]; present {
		t.Fatalf("absence must not write pr_link_candidates: %#v", attributes)
	}
	if _, present := extensions["pr_link_evidence"]; present {
		t.Fatalf("absence must not write pr_link_evidence: %#v", extensions)
	}
}

func TestPRLinkURLsAcceptsHostsAndRejectsNonPRURLs(t *testing.T) {
	got := PRLinkURLs("https://bitbucket.org/workspace/repo/pull-requests/5 https://dev.azure.com/org/project/_git/repo/pullrequest/9 https://example.test/docs/pull/10 https://example.test/issues/10")
	want := []string{
		"https://bitbucket.org/workspace/repo/pull-requests/5",
		"https://dev.azure.com/org/project/_git/repo/pullrequest/9",
		"https://example.test/docs/pull/10",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PRLinkURLs = %#v, want %#v", got, want)
	}
}

func TestPRLinkScanTextRendersRetainedValues(t *testing.T) {
	cases := []struct {
		name   string
		value  any
		want   string
		wantOK bool
	}{
		{name: "string", value: "  gh pr view https://github.com/acme/repo/pull/7 ", want: "gh pr view https://github.com/acme/repo/pull/7", wantOK: true},
		{name: "object keeps ampersand verbatim", value: map[string]any{"url": "https://github.com/acme/repo/pull/7?a=1&b=2"}, want: `{"url":"https://github.com/acme/repo/pull/7?a=1&b=2"}`, wantOK: true},
		{name: "array", value: []any{map[string]any{"type": "text", "text": "https://github.com/acme/repo/pull/8"}}, want: `[{"text":"https://github.com/acme/repo/pull/8","type":"text"}]`, wantOK: true},
		{name: "blank string", value: "  ", wantOK: false},
		{name: "number", value: 7, wantOK: false},
		{name: "nil", value: nil, wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := PRLinkScanText(tc.value)
			if ok != tc.wantOK || (ok && got != tc.want) {
				t.Fatalf("PRLinkScanText(%#v) = %q, %v; want %q, %v", tc.value, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// A JSON-rendered structured value must still yield exactly the verbatim URL:
// the token pattern stops at the closing quote, and a branch name or a printf
// template (`/pull/%s`) never parses as a pull-request permalink (#251).
func TestPRLinkURLsOverRenderedToolIO(t *testing.T) {
	text, _ := PRLinkScanText(map[string]any{
		"command":    `printf 'https://github.com/acme/repo/pull/%s\n' 251`,
		"git_branch": "feature/pull/251",
		"stdout":     "https://github.com/acme/repo/pull/251",
	})
	want := []string{"https://github.com/acme/repo/pull/251"}
	if got := PRLinkURLs(text); !reflect.DeepEqual(got, want) {
		t.Fatalf("PRLinkURLs(%q) = %#v, want %#v", text, got, want)
	}
}
