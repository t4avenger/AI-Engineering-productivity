package claude

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

const livePRLinkURL = "https://github.com/acme-synthetic/telemetryiq/pull/251"

// TestRetainedToolSurfacesPromotePRLink proves the #251 contract over two live
// Claude Code 2.1.286 captures: a verbatim pull-request URL in the retained
// tool_decision/tool_result tool_parameters + tool_input (OTLP logs), or only in
// the JSONL tool_result output (the tool_use input is a printf template), becomes
// pr_link_candidates with field provenance. Events without a URL — including the
// real git commit whose tool_parameters carries git_branch/git_commit_id — carry
// no candidate key at all.
func TestRetainedToolSurfacesPromotePRLink(t *testing.T) {
	cases := []struct {
		name       string
		golden     string
		normalise  func(t *testing.T) []canonical.Event
		wantFields map[string][]string // event type + sequence/tool id → evidence fields
	}{
		{
			name:   "otlp logs tool_parameters and tool_input",
			golden: "claude-code-2.1.286-tool-params-pr-link.events.json",
			normalise: func(t *testing.T) []canonical.Event {
				return normalizeObservedOTLPLogs(t, "claude-code-2.1.286-tool-params-pr-link-otlp.json")
			},
			wantFields: map[string][]string{
				"claude-code:synthetic-pr-link-session:17": {"tool_parameters"},
				"claude-code:synthetic-pr-link-session:18": {"tool_input", "tool_parameters"},
			},
		},
		{
			name:   "jsonl tool output only",
			golden: "claude-code-2.1.286-tool-output-pr-link-transcript.events.json",
			normalise: func(t *testing.T) []canonical.Event {
				events, err := NormalizeTranscript(transcriptFixtureNDJSON(t, "claude-code-2.1.286-tool-output-pr-link-transcript.json"), time.Unix(0, 0).UTC())
				if err != nil {
					t.Fatalf("normalise transcript: %v", err)
				}
				return events
			},
			wantFields: map[string][]string{
				"claude-code:25125125-1251-4251-8251-251251251251:toolcall:toolu_synthetic_pr_link_output": {"tool_output", "tool_use_result"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first := tc.normalise(t)
			if second := tc.normalise(t); !reflect.DeepEqual(first, second) {
				t.Fatal("normalisation must be deterministic")
			}
			if got := prLinkEvidenceByEvent(t, first); !reflect.DeepEqual(got, tc.wantFields) {
				t.Fatalf("pr_link evidence fields = %#v, want %#v", got, tc.wantFields)
			}
			assertMatchesGolden(t, tc.golden, first)
		})
	}
}

// TestTranscriptPRLinkKeepsDistinctCandidates pins the honest-conflict rule on
// the transcript path: an MCP result that names both the web permalink and the
// REST API form (`api.github.com/.../pulls/N`) yields two distinct candidates, so
// session aggregation reports partial rather than picking one.
func TestTranscriptPRLinkKeepsDistinctCandidates(t *testing.T) {
	call := transcriptToolCall{
		category: canonical.OperationCategoryMCPCall,
		input:    map[string]any{"owner": "acme-synthetic", "repo": "telemetryiq", "title": "tiq"},
		result:   []any{map[string]any{"type": "text", "text": `{"html_url":"https://github.com/acme-synthetic/telemetryiq/pull/9","url":"https://api.github.com/repos/acme-synthetic/telemetryiq/pulls/9"}`}},
	}
	attributes, extensions := map[string]any{}, map[string]any{}
	call.attachPRLinkEvidence(attributes, extensions)
	want := []string{
		"https://api.github.com/repos/acme-synthetic/telemetryiq/pulls/9",
		"https://github.com/acme-synthetic/telemetryiq/pull/9",
	}
	if got := attributes["pr_link_candidates"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("pr_link_candidates = %#v, want %#v", got, want)
	}
}

// prLinkEvidenceByEvent maps each event carrying the live PR URL to its sorted
// evidence fields, failing on evidence without candidates or a foreign URL.
func prLinkEvidenceByEvent(t *testing.T, events []canonical.Event) map[string][]string {
	t.Helper()
	got := map[string][]string{}
	for _, event := range events {
		candidates, present := event.Attributes["pr_link_candidates"]
		if !present {
			if _, evidence := event.ProviderExtensions["pr_link_evidence"]; evidence {
				t.Fatalf("%s carries pr_link_evidence without candidates", event.EventID)
			}
			continue
		}
		if !reflect.DeepEqual(candidates, []string{livePRLinkURL}) {
			t.Fatalf("%s pr_link_candidates = %#v, want [%q]", event.EventID, candidates, livePRLinkURL)
		}
		got[event.EventID] = evidenceFields(t, event)
	}
	return got
}

func evidenceFields(t *testing.T, event canonical.Event) []string {
	t.Helper()
	evidence, ok := event.ProviderExtensions["pr_link_evidence"].([]map[string]string)
	if !ok {
		t.Fatalf("%s pr_link_evidence = %#v", event.EventID, event.ProviderExtensions["pr_link_evidence"])
	}
	fields := make([]string, 0, len(evidence))
	for _, row := range evidence {
		if row["url"] != livePRLinkURL {
			t.Fatalf("%s evidence url = %q", event.EventID, row["url"])
		}
		fields = append(fields, row["field"])
	}
	sort.Strings(fields)
	return fields
}
