package governance

import (
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/config"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestPathRulesFromEvents(t *testing.T) {
	now := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	event := func(path string) canonical.Event {
		return canonical.Event{EventID: "path-event", SessionID: "session-1", OccurredAt: now, Attributes: map[string]any{"file_path": path}, ProviderExtensions: map[string]any{}}
	}
	tests := []struct {
		name    string
		rules   config.PathRules
		path    string
		outcome Outcome
		reason  string
	}{
		{"unconfigured is indeterminate", config.PathRules{}, "src/main.go", OutcomeIndeterminate, ""},
		{"monitor ignores allowed", config.PathRules{Mode: "monitor", Allowed: []config.PathRulePattern{{Kind: "glob", Value: "src/**"}}}, "src/main.go", OutcomeNotViolation, ""},
		{"blocked wins over allowed", config.PathRules{Mode: "approved_only", Allowed: []config.PathRulePattern{{Kind: "glob", Value: "src/**"}}, Blocked: []config.PathRulePattern{{Kind: "exact", Value: "src/main.go"}}}, "src/main.go", OutcomeViolation, "blocked_match"},
		{"approved only flags unmatched", config.PathRules{Mode: "approved_only", Allowed: []config.PathRulePattern{{Kind: "glob", Value: "src/*"}}}, "docs/readme.md", OutcomeViolation, "not_allowed"},
		{"flag all reports every path", config.PathRules{Mode: "flag_all"}, "docs/readme.md", OutcomeViolation, "flag_all"},
		{"glob star stays in a segment", config.PathRules{Mode: "approved_only", Allowed: []config.PathRulePattern{{Kind: "glob", Value: "src/*"}}}, "src/nested/main.go", OutcomeViolation, "not_allowed"},
		{"glob double star crosses segments", config.PathRules{Mode: "approved_only", Allowed: []config.PathRulePattern{{Kind: "glob", Value: "src/**"}}}, "src/nested/main.go", OutcomeNotViolation, ""},
		{"glob double star accepts zero segments", config.PathRules{Mode: "approved_only", Allowed: []config.PathRulePattern{{Kind: "glob", Value: "src/**/main.go"}}}, "src/main.go", OutcomeNotViolation, ""},
		{"glob question matches one non separator", config.PathRules{Mode: "approved_only", Allowed: []config.PathRulePattern{{Kind: "glob", Value: "src/?.go"}}}, "src/a.go", OutcomeNotViolation, ""},
		{"glob matching is case sensitive", config.PathRules{Mode: "approved_only", Allowed: []config.PathRulePattern{{Kind: "exact", Value: "src/Main.go"}}}, "src/main.go", OutcomeViolation, "not_allowed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := PathRulesFromEvents([]canonical.Event{event(test.path)}, test.rules)
			if report.Outcome != test.outcome {
				t.Fatalf("outcome = %q, want %q", report.Outcome, test.outcome)
			}
			if test.reason == "" && len(report.Findings) != 0 {
				t.Fatalf("unexpected findings: %#v", report.Findings)
			}
			if test.reason != "" && (len(report.Findings) != 1 || report.Findings[0].Reason != test.reason) {
				t.Fatalf("findings = %#v, want reason %q", report.Findings, test.reason)
			}
		})
	}
}

func TestPathRulesRequireFilesystemPathEvidence(t *testing.T) {
	event := canonical.Event{EventID: "command", Attributes: map[string]any{"command": "cat .env"}, ProviderExtensions: map[string]any{}}
	report := PathRulesFromEvents([]canonical.Event{event}, config.PathRules{Mode: "flag_all"})
	if report.Outcome != OutcomeIndeterminate || report.Visibility != "unavailable" {
		t.Fatalf("command-only path policy = %#v", report)
	}
}
