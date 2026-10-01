package claude

import (
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

const (
	livePRLinkURL        = "https://github.com/acme-synthetic/telemetryiq/pull/251"
	liveSpanPRLinkURL    = "https://github.com/acme-synthetic/telemetryiq/pull/253"
	liveToolSpanEventID  = "claude-code:span:356a7515ac7342246c4a6c4e3eede1a8742b4517ea1b15d55ed19e2b56b48115"
	liveToolContentSpans = "claude-code-2.1.287-tool-content-spans-otlp.json"
)

// TestRetainedToolSurfacesPromotePRLink proves the #251/#253 contract over live
// Claude Code captures: a verbatim pull-request URL in the retained
// tool_decision/tool_result tool_parameters + tool_input (OTLP logs), only in
// the JSONL tool_result output (the tool_use input is a printf template), or only
// in a claude_code.tool span's new_context and tool.output event output (2.1.287,
// OTEL_LOG_TOOL_CONTENT; full_command is the printf template) becomes
// pr_link_candidates with field provenance. Stripping new_context proves the
// tool.output event alone is enough. Events without a URL — including the real
// git commit whose tool_parameters carries git_branch/git_commit_id, and the
// llm_request whose new_context echoes the tool result as conversation context —
// carry no candidate key at all.
func TestRetainedToolSurfacesPromotePRLink(t *testing.T) {
	cases := []struct {
		name       string
		golden     string
		normalise  func(t *testing.T) []canonical.Event
		url        string
		wantFields map[string][]string // event type + sequence/tool id → evidence fields
	}{
		{
			name:   "otlp logs tool_parameters and tool_input",
			golden: "claude-code-2.1.286-tool-params-pr-link.events.json",
			url:    livePRLinkURL,
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
			url:    livePRLinkURL,
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
		{
			name:   "otlp tool span new_context and tool.output",
			golden: "claude-code-2.1.287-tool-content-spans.events.json",
			url:    liveSpanPRLinkURL,
			normalise: func(t *testing.T) []canonical.Event {
				return normalizeTraceFixture(t, tracesFixturePayload(t, liveToolContentSpans))
			},
			wantFields: map[string][]string{liveToolSpanEventID: {"new_context", "tool_output"}},
		},
		{
			name: "otlp tool.output event only",
			url:  liveSpanPRLinkURL,
			normalise: func(t *testing.T) []canonical.Event {
				return normalizeTraceFixture(t, withoutSpanAttribute(t, tracesFixturePayload(t, liveToolContentSpans), "new_context"))
			},
			wantFields: map[string][]string{liveToolSpanEventID: {"tool_output"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first := tc.normalise(t)
			if second := tc.normalise(t); !reflect.DeepEqual(first, second) {
				t.Fatal("normalisation must be deterministic")
			}
			if got := prLinkEvidenceByEvent(t, first, tc.url); !reflect.DeepEqual(got, tc.wantFields) {
				t.Fatalf("pr_link evidence fields = %#v, want %#v", got, tc.wantFields)
			}
			if tc.golden != "" {
				assertMatchesGolden(t, tc.golden, first)
			}
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

// normalizeTraceFixture normalises an OTLP traces payload at a fixed receive time.
func normalizeTraceFixture(t *testing.T, payload []byte) []canonical.Event {
	t.Helper()
	events, err := NormalizeTraces(payload, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("normalise traces: %v", err)
	}
	return events
}

// withoutSpanAttribute re-encodes an OTLP traces payload with key removed from
// every span's attributes, leaving span events and links intact.
func withoutSpanAttribute(t *testing.T, payload []byte, key string) []byte {
	t.Helper()
	var decoded tracesPayload
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode traces: %v", err)
	}
	for _, resource := range decoded.ResourceSpans {
		for _, scope := range resource.ScopeSpans {
			for i := range scope.Spans {
				scope.Spans[i].Attributes = slices.DeleteFunc(scope.Spans[i].Attributes, func(attribute otlpAttribute) bool {
					return attribute.Key == key
				})
			}
		}
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("encode traces: %v", err)
	}
	return encoded
}

// prLinkEvidenceByEvent maps each event carrying url to its sorted evidence
// fields, failing on evidence without candidates or a foreign URL.
func prLinkEvidenceByEvent(t *testing.T, events []canonical.Event, url string) map[string][]string {
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
		if !reflect.DeepEqual(candidates, []string{url}) {
			t.Fatalf("%s pr_link_candidates = %#v, want [%q]", event.EventID, candidates, url)
		}
		got[event.EventID] = evidenceFields(t, event, url)
	}
	return got
}

func evidenceFields(t *testing.T, event canonical.Event, url string) []string {
	t.Helper()
	evidence, ok := event.ProviderExtensions["pr_link_evidence"].([]map[string]string)
	if !ok {
		t.Fatalf("%s pr_link_evidence = %#v", event.EventID, event.ProviderExtensions["pr_link_evidence"])
	}
	fields := make([]string, 0, len(evidence))
	for _, row := range evidence {
		if row["url"] != url {
			t.Fatalf("%s evidence url = %q", event.EventID, row["url"])
		}
		fields = append(fields, row["field"])
	}
	sort.Strings(fields)
	return fields
}
