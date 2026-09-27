package governance

import (
	"bytes"
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/config"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestPromptKeywordsFromEvents(t *testing.T) {
	literal := config.PromptKeyword{ID: "secret-word", Label: "Secret word", Group: "credentials", Enabled: true, Kind: "literal", Value: "Secret"}
	folded := config.PromptKeyword{ID: "token-ci", Label: "Token", Group: "custom", Enabled: true, Kind: "regex", Value: "(?i)token"}
	sensitive := config.PromptKeyword{ID: "token-cs", Label: "Token case", Group: "custom", Enabled: true, Kind: "regex", Value: "Token"}
	disabled := config.PromptKeyword{ID: "off", Label: "Off", Group: "customer_data", Enabled: false, Kind: "literal", Value: "Secret"}
	tests := []struct {
		name       string
		rules      []config.PromptKeyword
		events     []canonical.Event
		outcome    Outcome
		visibility string
		ruleID     string
	}{
		{name: "empty config is unconfigured", outcome: OutcomeIndeterminate, visibility: "policy_unconfigured"},
		{name: "disabled only stays unconfigured", rules: []config.PromptKeyword{disabled}, events: []canonical.Event{promptEvent("p", "available", "Secret")}, outcome: OutcomeIndeterminate, visibility: "policy_unconfigured"},
		{name: "literal is case sensitive", rules: []config.PromptKeyword{literal}, events: []canonical.Event{promptEvent("p", "available", "secret")}, outcome: OutcomeNotViolation, visibility: "observed"},
		{name: "literal match is versioned", rules: []config.PromptKeyword{literal}, events: []canonical.Event{promptEvent("p", "available", "has Secret inside")}, outcome: OutcomeViolation, visibility: "observed", ruleID: "secret-word"},
		{name: "regex flag folds case", rules: []config.PromptKeyword{folded}, events: []canonical.Event{promptEvent("p", "available", "TOKEN")}, outcome: OutcomeViolation, visibility: "observed", ruleID: "token-ci"},
		{name: "regex without flag stays case sensitive", rules: []config.PromptKeyword{sensitive}, events: []canonical.Event{promptEvent("p", "available", "token")}, outcome: OutcomeNotViolation, visibility: "observed"},
		{name: "disabled rule does not match", rules: []config.PromptKeyword{disabled, sensitive}, events: []canonical.Event{promptEvent("p", "available", "Secret")}, outcome: OutcomeNotViolation, visibility: "observed"},
		{name: "redacted body is not a clean pass", rules: []config.PromptKeyword{literal}, events: []canonical.Event{promptEvent("p", "redacted", "<redacted>")}, outcome: OutcomeIndeterminate, visibility: "unavailable"},
		{name: "length only body is not a clean pass", rules: []config.PromptKeyword{literal}, events: []canonical.Event{promptEvent("p", "length", "")}, outcome: OutcomeIndeterminate, visibility: "unavailable"},
		{name: "mixed coverage stays partial", rules: []config.PromptKeyword{literal}, events: []canonical.Event{promptEvent("ok", "available", "hello"), promptEvent("missing", "length", "")}, outcome: OutcomeIndeterminate, visibility: "partial"},
		{name: "assistant text is not prompt evidence", rules: []config.PromptKeyword{literal}, events: []canonical.Event{otherContentEvent("assistant_response", map[string]any{"response": "has Secret inside"})}, outcome: OutcomeIndeterminate, visibility: "unavailable"},
		{name: "no prompt events stay unavailable", rules: []config.PromptKeyword{literal}, events: []canonical.Event{{EventID: "other", EventType: "tool_use"}}, outcome: OutcomeIndeterminate, visibility: "unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertPromptKeywordReport(t, PromptKeywordsFromEvents(test.events, test.rules), test.outcome, test.visibility, test.ruleID)
		})
	}
}

func assertPromptKeywordReport(t *testing.T, report PromptKeywordReport, outcome Outcome, visibility, ruleID string) {
	t.Helper()
	if report.Outcome != outcome || report.Visibility != visibility {
		t.Fatalf("report = %#v", report)
	}
	if ruleID == "" {
		if len(report.Findings) != 0 {
			t.Fatalf("unexpected findings %#v", report.Findings)
		}
		return
	}
	if len(report.Findings) != 1 || report.Findings[0].RuleID != ruleID || report.Findings[0].PolicyVersion != promptKeywordsPolicyVersion || report.Findings[0].DetectorVersion != promptKeywordsDetector {
		t.Fatalf("finding = %#v", report.Findings)
	}
	decision := report.Decision()
	if decision.PolicyID != promptKeywordsPolicyID || decision.Outcome != OutcomeViolation || !strings.Contains(decision.Evidence[0].Reference, "rule:"+ruleID) {
		t.Fatalf("decision = %#v", decision)
	}
	if strings.Contains(decision.Evidence[0].Reference, "Secret") || strings.Contains(decision.Evidence[0].Reference, "TOKEN") {
		t.Fatalf("decision evidence leaked matched text: %#v", decision.Evidence)
	}
}

func TestPromptKeywordRegexStaysBounded(t *testing.T) {
	pattern := `(a+)+b`
	compiled := regexp.MustCompile(pattern)
	text := strings.Repeat("a", 20000) + "c"
	started := time.Now()
	if matchPromptKeyword("regex", pattern, compiled, text) {
		t.Fatal("adversarial input unexpectedly matched")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("RE2 match took %s", elapsed)
	}
}

func TestPromptKeywordMatchDoesNotLogRuleOrPrompt(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	secret := "tiq-super-secret-pattern"
	prompt := "user wrote tiq-super-secret-pattern here"
	report := PromptKeywordsFromEvents([]canonical.Event{promptEvent("p", "available", prompt)}, []config.PromptKeyword{{
		ID: "logged", Label: secret, Group: "credentials", Enabled: true, Kind: "literal", Value: secret,
	}})
	if report.Outcome != OutcomeViolation {
		t.Fatalf("report = %#v", report)
	}
	if strings.Contains(buf.String(), secret) || strings.Contains(buf.String(), prompt) {
		t.Fatalf("diagnostics leaked rule or prompt: %s", buf.String())
	}
}

func promptEvent(id, shape, text string) canonical.Event {
	echo := map[string]any{}
	switch shape {
	case "available", "redacted":
		echo["prompt"] = text
	case "length":
		echo["prompt_length"] = int64(12)
	}
	return canonical.Event{
		EventID: id, SessionID: "session-1", EventType: "user_prompt", Provider: "anthropic", Tool: "claude-code",
		OccurredAt: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC), ProviderExtensions: map[string]any{"event": echo},
	}
}

func otherContentEvent(eventType string, echo map[string]any) canonical.Event {
	event := promptEvent("other", "available", "")
	event.EventType = eventType
	event.ProviderExtensions = map[string]any{"event": echo}
	return event
}
