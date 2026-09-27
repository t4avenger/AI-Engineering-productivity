package governance

import (
	"regexp"
	"sort"
	"strings"

	"github.com/wayne/telemetryiq/internal/config"
	"github.com/wayne/telemetryiq/internal/conversation"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

const (
	promptKeywordsPolicyID      = "governance.prompt_keywords"
	promptKeywordsPolicyVersion = "0.1.0"
	promptKeywordsDetector      = "0.1.0"
)

// PromptKeywordFinding is one retained user prompt that matched an enabled
// local rule. The matched text and rule pattern stay out of this record so
// diagnostics and API logs cannot echo them; the source event remains
// inspectable in the local session view.
type PromptKeywordFinding struct {
	RuleID          string `json:"rule_id"`
	Label           string `json:"label"`
	Group           string `json:"group"`
	Kind            string `json:"kind"`
	SourceEventID   string `json:"source_event_id"`
	SessionID       string `json:"session_id,omitempty"`
	PolicyVersion   string `json:"policy_version"`
	DetectorVersion string `json:"detector_version"`
}

// PromptKeywordReport is the aggregate detect-and-report result for retained
// user prompts. It never claims a clean result when prompt bodies are missing
// or provider-redacted.
type PromptKeywordReport struct {
	Findings   []PromptKeywordFinding `json:"findings"`
	Outcome    Outcome                `json:"outcome"`
	Visibility string                 `json:"visibility"`
}

// PromptKeywordsFromEvents matches enabled rules against available Claude
// user-prompt text only. Assistant responses and raw API bodies are not
// prompt evidence. The function does not log rule values or matched text.
func PromptKeywordsFromEvents(events []canonical.Event, rules []config.PromptKeyword) PromptKeywordReport {
	report := PromptKeywordReport{Findings: []PromptKeywordFinding{}, Outcome: OutcomeIndeterminate, Visibility: "unavailable"}
	compiled := compilePromptKeywords(rules)
	if len(compiled) == 0 {
		report.Visibility = "policy_unconfigured"
		return report
	}
	sessions := map[string]string{}
	for _, event := range events {
		sessions[event.EventID] = strings.TrimSpace(event.SessionID)
	}
	sawAvailable := false
	sawIncomplete := false
	for _, record := range conversation.Project(events) {
		if record.EventType != "user_prompt" {
			continue
		}
		if record.ContentAvailability != conversation.AvailabilityAvailable || record.Text == nil {
			sawIncomplete = true
			continue
		}
		sawAvailable = true
		for _, rule := range compiled {
			if rule.matches(*record.Text) {
				report.Findings = append(report.Findings, PromptKeywordFinding{
					RuleID: rule.id, Label: rule.label, Group: rule.group, Kind: rule.kind,
					SourceEventID: record.EventID, SessionID: sessions[record.EventID],
					PolicyVersion: promptKeywordsPolicyVersion, DetectorVersion: promptKeywordsDetector,
				})
			}
		}
	}
	sort.Slice(report.Findings, func(i, j int) bool {
		return report.Findings[i].SourceEventID+"\x00"+report.Findings[i].RuleID < report.Findings[j].SourceEventID+"\x00"+report.Findings[j].RuleID
	})
	return applyPromptKeywordOutcome(report, sawAvailable, sawIncomplete)
}

type compiledPromptKeyword struct {
	id, label, group, kind, value string
	pattern                       *regexp.Regexp
}

func compilePromptKeywords(rules []config.PromptKeyword) []compiledPromptKeyword {
	compiled := make([]compiledPromptKeyword, 0, len(rules))
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		entry := compiledPromptKeyword{id: rule.ID, label: rule.Label, group: rule.Group, kind: rule.Kind, value: rule.Value}
		if rule.Kind == "regex" {
			pattern, err := regexp.Compile(rule.Value)
			if err != nil {
				continue
			}
			entry.pattern = pattern
		}
		compiled = append(compiled, entry)
	}
	return compiled
}

func (rule compiledPromptKeyword) matches(text string) bool {
	return matchPromptKeyword(rule.kind, rule.value, rule.pattern, text)
}

func matchPromptKeyword(kind, value string, pattern *regexp.Regexp, text string) bool {
	switch kind {
	case "literal":
		return strings.Contains(text, value)
	case "regex":
		if pattern == nil {
			return false
		}
		return pattern.MatchString(text)
	default:
		return false
	}
}

func applyPromptKeywordOutcome(report PromptKeywordReport, sawAvailable, sawIncomplete bool) PromptKeywordReport {
	switch {
	case len(report.Findings) > 0:
		report.Outcome = OutcomeViolation
		report.Visibility = "observed"
	case sawAvailable && sawIncomplete:
		report.Visibility = "partial"
	case sawAvailable:
		report.Outcome = OutcomeNotViolation
		report.Visibility = "observed"
	default:
		report.Visibility = "unavailable"
	}
	return report
}

// Decision renders the report in the shared policy schema vocabulary without
// embedding pattern text or matched prompt content.
func (p PromptKeywordReport) Decision() PolicyDecision {
	evidence := make([]PolicyEvidence, 0, len(p.Findings))
	for _, finding := range p.Findings {
		evidence = append(evidence, PolicyEvidence{Kind: "prompt_keyword", Reference: "rule:" + finding.RuleID + ";event:" + finding.SourceEventID})
	}
	if len(evidence) == 0 {
		reference := "user_prompt_unavailable"
		switch p.Visibility {
		case "policy_unconfigured":
			reference = "prompt_keywords_not_configured"
		case "partial":
			reference = "prompt_coverage_incomplete"
		default:
			if p.Outcome == OutcomeNotViolation {
				reference = "no_prompt_keyword_findings"
			}
		}
		evidence = append(evidence, PolicyEvidence{Kind: "visibility", Reference: reference})
	}
	return PolicyDecision{SchemaVersion: policySchemaVersion, PolicyID: promptKeywordsPolicyID, PolicyVersion: promptKeywordsPolicyVersion, Outcome: p.Outcome, Evidence: evidence}
}
