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
		{name: "transcript only prompt is scanned", rules: []config.PromptKeyword{literal}, events: []canonical.Event{transcriptPromptEvent("t", "u1", "has Secret inside")}, outcome: OutcomeViolation, visibility: "observed", ruleID: "secret-word"},
		{name: "transcript only clean prompt is observed", rules: []config.PromptKeyword{literal}, events: []canonical.Event{transcriptPromptEvent("t", "u1", "hello")}, outcome: OutcomeNotViolation, visibility: "observed"},
		{name: "redacted transcript prompt is not a clean pass", rules: []config.PromptKeyword{literal}, events: []canonical.Event{transcriptPromptEvent("t", "u1", "<redacted>")}, outcome: OutcomeIndeterminate, visibility: "unavailable"},
		{name: "length only otlp corroborated by transcript is observed", rules: []config.PromptKeyword{literal}, events: []canonical.Event{withMessageUUID(promptEvent("p", "length", ""), "u1"), transcriptPromptEvent("t", "u1", "hello")}, outcome: OutcomeNotViolation, visibility: "observed"},
		{name: "redacted otlp corroborated by transcript is observed", rules: []config.PromptKeyword{literal}, events: []canonical.Event{withMessageUUID(promptEvent("p", "redacted", "<redacted>"), "u1"), transcriptPromptEvent("t", "u1", "hello")}, outcome: OutcomeNotViolation, visibility: "observed"},
		{name: "independent incomplete prompts stay unavailable", rules: []config.PromptKeyword{literal}, events: []canonical.Event{withMessageUUID(promptEvent("p", "length", ""), "u1"), transcriptPromptEvent("t", "u2", "<redacted>")}, outcome: OutcomeIndeterminate, visibility: "unavailable"},
		{name: "uncorroborated incomplete prompt stays partial", rules: []config.PromptKeyword{literal}, events: []canonical.Event{withMessageUUID(promptEvent("p", "length", ""), "u1"), transcriptPromptEvent("t", "u2", "hello")}, outcome: OutcomeIndeterminate, visibility: "partial"},
		{name: "transcript assistant text is not prompt evidence", rules: []config.PromptKeyword{literal}, events: []canonical.Event{transcriptAssistantEvent("has Secret inside")}, outcome: OutcomeIndeterminate, visibility: "unavailable"},
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

// TestPromptKeywordsDualSourceJoin pins the #259 dual-source rule: Claude OTLP
// and transcript copies of one prompt join only on session + message uuid;
// every other shape reports one finding per source event.
func TestPromptKeywordsDualSourceJoin(t *testing.T) {
	secret := config.PromptKeyword{ID: "secret", Label: "Secret", Group: "credentials", Enabled: true, Kind: "literal", Value: "Secret"}
	token := config.PromptKeyword{ID: "token", Label: "Token", Group: "custom", Enabled: true, Kind: "literal", Value: "Token"}
	otherSession := transcriptPromptEvent("t", "u1", "Secret")
	otherSession.SessionID = "session-2"
	codex := withMessageUUID(promptEvent("p", "available", "Secret"), "u1")
	codex.Provider, codex.Tool = "openai", "codex"
	blankSession := transcriptPromptEvent("t", "u1", "Secret")
	blankSession.SessionID = " "
	tests := []struct {
		name   string
		events []canonical.Event
		want   []string
	}{
		{name: "same uuid joins with otlp primary", events: []canonical.Event{withMessageUUID(promptEvent("p", "available", "Secret"), "u1"), transcriptPromptEvent("t", "u1", "Secret")}, want: []string{"secret:p:t"}},
		{name: "join ignores input order", events: []canonical.Event{transcriptPromptEvent("t", "u1", "Secret"), withMessageUUID(promptEvent("p", "available", "Secret"), "u1")}, want: []string{"secret:p:t"}},
		{name: "only transcript copy matches", events: []canonical.Event{withMessageUUID(promptEvent("p", "available", "hello"), "u1"), transcriptPromptEvent("t", "u1", "Secret expanded")}, want: []string{"secret:t:"}},
		{name: "only otlp copy matches", events: []canonical.Event{withMessageUUID(promptEvent("p", "available", "Secret"), "u1"), transcriptPromptEvent("t", "u1", "hello")}, want: []string{"secret:p:"}},
		{name: "copies match different rules", events: []canonical.Event{withMessageUUID(promptEvent("p", "available", "Secret"), "u1"), transcriptPromptEvent("t", "u1", "Token")}, want: []string{"secret:p:", "token:t:"}},
		{name: "missing uuid reports each source", events: []canonical.Event{promptEvent("p", "available", "Secret"), transcriptPromptEvent("t", "", "Secret")}, want: []string{"secret:p:", "secret:t:"}},
		{name: "different uuids report each source", events: []canonical.Event{withMessageUUID(promptEvent("p", "available", "Secret"), "u1"), transcriptPromptEvent("t", "u2", "Secret")}, want: []string{"secret:p:", "secret:t:"}},
		{name: "same uuid in another session does not join", events: []canonical.Event{withMessageUUID(promptEvent("p", "available", "Secret"), "u1"), otherSession}, want: []string{"secret:p:", "secret:t:"}},
		{name: "non claude provider does not join", events: []canonical.Event{codex, transcriptPromptEvent("t", "u1", "Secret")}, want: []string{"secret:p:", "secret:t:"}},
		{name: "blank session does not join", events: []canonical.Event{withMessageUUID(promptEvent("p", "available", "Secret"), "u1"), blankSession}, want: []string{"secret:p:", "secret:t:"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := PromptKeywordsFromEvents(test.events, []config.PromptKeyword{secret, token})
			got := make([]string, 0, len(report.Findings))
			for _, finding := range report.Findings {
				got = append(got, finding.RuleID+":"+finding.SourceEventID+":"+strings.Join(finding.CorroboratingEventIDs, ","))
			}
			if strings.Join(got, "|") != strings.Join(test.want, "|") || report.Outcome != OutcomeViolation {
				t.Fatalf("findings = %v, outcome = %s; want %v", got, report.Outcome, test.want)
			}
		})
	}
}

func TestPromptKeywordDecisionCitesCorroboratingSource(t *testing.T) {
	report := PromptKeywordsFromEvents(
		[]canonical.Event{withMessageUUID(promptEvent("p", "available", "has Secret"), "u1"), transcriptPromptEvent("t", "u1", "has Secret")},
		[]config.PromptKeyword{{ID: "secret", Label: "Secret", Group: "credentials", Enabled: true, Kind: "literal", Value: "Secret"}},
	)
	evidence := report.Decision().Evidence
	if len(evidence) != 1 || evidence[0].Reference != "rule:secret;event:p;corroborated_by:t" {
		t.Fatalf("evidence = %#v", evidence)
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

// withMessageUUID stamps the OTLP message.uuid correlation the Claude
// normaliser retains as correlation.message_uuid.
func withMessageUUID(event canonical.Event, uuid string) canonical.Event {
	event.ProviderExtensions["correlation"] = map[string]any{"message_uuid": uuid}
	return event
}

// transcriptPromptEvent is the Claude session JSONL user_message shape: prompt
// text under transcript.prompt_content and the record uuid under
// correlation.uuid.
func transcriptPromptEvent(id, uuid, text string) canonical.Event {
	event := promptEvent(id, "", "")
	event.EventType = "user_message"
	event.ProviderExtensions = map[string]any{
		"transcript":  map[string]any{"prompt_content": text},
		"correlation": map[string]any{"uuid": uuid},
	}
	return event
}

func transcriptAssistantEvent(text string) canonical.Event {
	event := transcriptPromptEvent("assistant", "u1", "")
	event.EventType = "assistant_message"
	event.ProviderExtensions["transcript"] = map[string]any{"response_content": text}
	return event
}

func otherContentEvent(eventType string, echo map[string]any) canonical.Event {
	event := promptEvent("other", "available", "")
	event.EventType = eventType
	event.ProviderExtensions = map[string]any{"event": echo}
	return event
}
