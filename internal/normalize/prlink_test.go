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
