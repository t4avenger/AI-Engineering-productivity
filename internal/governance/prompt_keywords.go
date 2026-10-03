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
	promptKeywordsPolicyVersion = "0.2.0"
	promptKeywordsDetector      = "0.2.0"
)

// PromptKeywordFinding is one retained user prompt that matched an enabled
// local rule. The matched text and rule pattern stay out of this record so
// diagnostics and API logs cannot echo them; the source event remains
// inspectable in the local session view.
type PromptKeywordFinding struct {
	RuleID        string `json:"rule_id"`
	Label         string `json:"label"`
	Group         string `json:"group"`
	Kind          string `json:"kind"`
	SourceEventID string `json:"source_event_id"`
	// CorroboratingEventIDs are other retained copies of the same prompt (joined
	// on message uuid) that also matched the rule.
	CorroboratingEventIDs []string `json:"corroborating_event_ids,omitempty"`
	SessionID             string   `json:"session_id,omitempty"`
	PolicyVersion         string   `json:"policy_version"`
	DetectorVersion       string   `json:"detector_version"`
}

// PromptKeywordReport is the aggregate detect-and-report result for retained
// user prompts. It never claims a clean result when prompt bodies are missing
// or provider-redacted.
type PromptKeywordReport struct {
	Findings   []PromptKeywordFinding `json:"findings"`
	Outcome    Outcome                `json:"outcome"`
	Visibility string                 `json:"visibility"`
}

// PromptKeywordsFromEvents matches enabled rules against retained user-role
// prompt text from both reviewed surfaces: OTLP user_prompt and Claude session
// JSONL user_message (#259). Assistant responses and raw API bodies are not
// prompt evidence. One prompt retained by both Claude surfaces is joined only on
// the provider message uuid inside the same session (proven by the paired
// 2.1.283 fixtures); without that key each source event reports its own
// finding. The function does not log rule values or matched text.
func PromptKeywordsFromEvents(events []canonical.Event, rules []config.PromptKeyword) PromptKeywordReport {
	report := PromptKeywordReport{Findings: []PromptKeywordFinding{}, Outcome: OutcomeIndeterminate, Visibility: "unavailable"}
	compiled := compilePromptKeywords(rules)
	if len(compiled) == 0 {
		report.Visibility = "policy_unconfigured"
		return report
	}
	prompts := groupPromptIdentities(events)
	sawAvailable := false
	sawIncomplete := false
	for _, prompt := range prompts {
		if !prompt.available {
			sawIncomplete = true
			continue
		}
		sawAvailable = true
		for _, rule := range compiled {
			if finding, ok := prompt.finding(rule); ok {
				report.Findings = append(report.Findings, finding)
			}
		}
	}
	sort.Slice(report.Findings, func(i, j int) bool {
		return report.Findings[i].SourceEventID+"\x00"+report.Findings[i].RuleID < report.Findings[j].SourceEventID+"\x00"+report.Findings[j].RuleID
	})
	return applyPromptKeywordOutcome(report, sawAvailable, sawIncomplete)
}

// groupPromptIdentities projects user-role conversation records and groups the
// ones that are provably the same prompt, in first-seen order.
func groupPromptIdentities(events []canonical.Event) []*promptIdentity {
	byID := make(map[string]canonical.Event, len(events))
	for _, event := range events {
		byID[event.EventID] = event
	}
	index := map[string]*promptIdentity{}
	prompts := []*promptIdentity{}
	for _, record := range conversation.Project(events) {
		if record.Role != conversation.RoleUser {
			continue
		}
		event := byID[record.EventID]
		key := promptIdentityKey(event)
		prompt, ok := index[key]
		if !ok {
			prompt = &promptIdentity{sessionID: strings.TrimSpace(event.SessionID)}
			index[key] = prompt
			prompts = append(prompts, prompt)
		}
		if record.ContentAvailability != conversation.AvailabilityAvailable || record.Text == nil {
			continue
		}
		prompt.available = true
		prompt.sources = append(prompt.sources, promptSource{eventID: record.EventID, eventType: record.EventType, text: *record.Text})
	}
	return prompts
}

// promptIdentity groups the user-role records that are provably the same
// prompt. available is true when any member retained an inline body.
type promptIdentity struct {
	sessionID string
	available bool
	sources   []promptSource
}

type promptSource struct {
	eventID, eventType, text string
}

// finding reports one rule match for the prompt. The primary source prefers the
// OTLP user_prompt so existing findings and links stay stable; every other
// member that also matched the rule is kept as corroborating provenance.
func (p *promptIdentity) finding(rule compiledPromptKeyword) (PromptKeywordFinding, bool) {
	matched := make([]promptSource, 0, len(p.sources))
	for _, source := range p.sources {
		if rule.matches(source.text) {
			matched = append(matched, source)
		}
	}
	if len(matched) == 0 {
		return PromptKeywordFinding{}, false
	}
	sort.Slice(matched, func(i, j int) bool {
		iOTLP, jOTLP := matched[i].eventType == "user_prompt", matched[j].eventType == "user_prompt"
		if iOTLP != jOTLP {
			return iOTLP
		}
		return matched[i].eventID < matched[j].eventID
	})
	finding := PromptKeywordFinding{
		RuleID: rule.id, Label: rule.label, Group: rule.group, Kind: rule.kind,
		SourceEventID: matched[0].eventID, SessionID: p.sessionID,
		PolicyVersion: promptKeywordsPolicyVersion, DetectorVersion: promptKeywordsDetector,
	}
	for _, source := range matched[1:] {
		finding.CorroboratingEventIDs = append(finding.CorroboratingEventIDs, source.eventID)
	}
	return finding, true
}

// promptIdentityKey returns the cross-surface join key for a Claude prompt:
// the same session plus the provider message uuid (OTLP message.uuid retained
// as correlation.message_uuid, transcript uuid retained as correlation.uuid).
// Anything without both values falls back to the event's own identity, so no
// prompt is ever merged by time or text.
func promptIdentityKey(event canonical.Event) string {
	fallback := "event\x00" + event.EventID
	if event.Provider != "anthropic" || event.Tool != "claude-code" {
		return fallback
	}
	uuidKey := ""
	switch event.EventType {
	case "user_prompt":
		uuidKey = "message_uuid"
	case "user_message":
		uuidKey = "uuid"
	default:
		return fallback
	}
	correlation, _ := event.ProviderExtensions["correlation"].(map[string]any)
	uuid, _ := correlation[uuidKey].(string)
	session := strings.TrimSpace(event.SessionID)
	uuid = strings.TrimSpace(uuid)
	if session == "" || uuid == "" {
		return fallback
	}
	return "claude\x00" + session + "\x00" + uuid
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
		reference := "rule:" + finding.RuleID + ";event:" + finding.SourceEventID
		if len(finding.CorroboratingEventIDs) > 0 {
			reference += ";corroborated_by:" + strings.Join(finding.CorroboratingEventIDs, ",")
		}
		evidence = append(evidence, PolicyEvidence{Kind: "prompt_keyword", Reference: reference})
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
