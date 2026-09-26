package governance

import (
	"reflect"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestUnapprovedSkillsFromEvents(t *testing.T) {
	tests := []struct {
		name       string
		events     []canonical.Event
		allowlist  []string
		outcome    Outcome
		visibility string
		findings   []SkillFinding
	}{
		{
			name:       "empty policy is indeterminate",
			events:     []canonical.Event{skillPolicyEvent("skill-1", "deploy", "explicit")},
			outcome:    OutcomeIndeterminate,
			visibility: "policy_unconfigured",
			findings:   []SkillFinding{},
		},
		{
			name:       "exact allowlisted identity passes",
			events:     []canonical.Event{skillPolicyEvent("skill-1", "Deploy", "explicit")},
			allowlist:  []string{"Deploy"},
			outcome:    OutcomeNotViolation,
			visibility: "observed",
			findings:   []SkillFinding{},
		},
		{
			name:       "case changes do not match",
			events:     []canonical.Event{skillPolicyEvent("skill-1", "Deploy", "explicit")},
			allowlist:  []string{"deploy"},
			outcome:    OutcomeViolation,
			visibility: "observed",
			findings: []SkillFinding{{
				RuleID: "unapproved_skill", SkillName: "Deploy", Provider: "openai", Tool: "codex",
				SourceEventIDs: []string{"skill-1"}, PolicyVersion: unapprovedSkillPolicyVersion, DetectorVersion: unapprovedSkillDetector,
			}},
		},
		{
			name:       "inferred and unnamed evidence remains indeterminate",
			events:     []canonical.Event{skillPolicyEvent("skill-1", "", "inferred"), skillPolicyEvent("skill-2", "", "explicit")},
			allowlist:  []string{"deploy"},
			outcome:    OutcomeIndeterminate,
			visibility: "partial",
			findings:   []SkillFinding{},
		},
		{
			name: "violation wins over incomplete coverage",
			events: []canonical.Event{
				skillPolicyEvent("skill-2", "", "inferred"),
				skillPolicyEvent("skill-1", "deploy", "explicit"),
			},
			allowlist:  []string{"review"},
			outcome:    OutcomeViolation,
			visibility: "observed",
			findings: []SkillFinding{{
				RuleID: "unapproved_skill", SkillName: "deploy", Provider: "openai", Tool: "codex",
				SourceEventIDs: []string{"skill-1"}, PolicyVersion: unapprovedSkillPolicyVersion, DetectorVersion: unapprovedSkillDetector,
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := UnapprovedSkillsFromEvents(test.events, test.allowlist)
			if result.Outcome != test.outcome || result.Visibility != test.visibility {
				t.Fatalf("result outcome=%q visibility=%q", result.Outcome, result.Visibility)
			}
			if !reflect.DeepEqual(result.Findings, test.findings) {
				t.Fatalf("findings=%#v want %#v", result.Findings, test.findings)
			}
		})
	}
}

func TestUnapprovedSkillsDecisionReferencesSourceEvents(t *testing.T) {
	result := UnapprovedSkillsFromEvents([]canonical.Event{
		skillPolicyEvent("event-b", "deploy", "explicit"),
		skillPolicyEvent("event-a", "deploy", "explicit"),
	}, []string{"review"})
	decision := result.Decision()
	if decision.PolicyID != unapprovedSkillPolicyID || decision.PolicyVersion != unapprovedSkillPolicyVersion {
		t.Fatalf("decision policy = %#v", decision)
	}
	if len(decision.Evidence) != 2 || decision.Evidence[0].Reference != "skill:deploy;provider:openai;tool:codex;event:event-a" {
		t.Fatalf("decision evidence = %#v", decision.Evidence)
	}
}

func skillPolicyEvent(id, name, detection string) canonical.Event {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	extensions := map[string]any{"skill_detection": detection}
	if name != "" {
		extensions["skill"] = map[string]any{"name": name}
	}
	return canonical.Event{
		EventID: id, SessionID: "session-1", Provider: "openai", Tool: "codex",
		OccurredAt: now, ReceivedAt: now, ProviderExtensions: extensions,
	}
}
