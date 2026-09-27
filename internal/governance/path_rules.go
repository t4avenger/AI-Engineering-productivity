package governance

import (
	"sort"
	"strings"

	"github.com/wayne/telemetryiq/internal/config"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

const (
	pathRulesPolicyID      = "governance.path_rules"
	pathRulesPolicyVersion = "0.1.0"
	pathRulesDetector      = "0.1.0"
)

// PathFinding records one retained raw path that the configured local policy
// flags. It is evidence only; it does not block or otherwise alter access.
type PathFinding struct {
	RuleID          string `json:"rule_id"`
	Path            string `json:"path"`
	Mode            string `json:"mode"`
	Reason          string `json:"reason"`
	SourceEventID   string `json:"source_event_id"`
	SessionID       string `json:"session_id,omitempty"`
	PolicyVersion   string `json:"policy_version"`
	DetectorVersion string `json:"detector_version"`
}

// PathRules is the aggregate report for raw filesystem path evidence.
type PathRules struct {
	Findings   []PathFinding `json:"findings"`
	Outcome    Outcome       `json:"outcome"`
	Visibility string        `json:"visibility"`
}

// PathRulesFromEvents evaluates only filesystem-read path observations. Shell
// arguments are intentionally excluded because a command string is not a
// proven filesystem-path operation. No filesystem lookup, expansion, or
// symlink resolution occurs.
func PathRulesFromEvents(events []canonical.Event, rules config.PathRules) PathRules {
	report := PathRules{Findings: []PathFinding{}, Outcome: OutcomeIndeterminate, Visibility: "unavailable"}
	if rules.Mode == "" {
		report.Visibility = "policy_unconfigured"
		return report
	}
	sawPath := false
	for _, event := range events {
		for _, observation := range AccessObservations(event) {
			if observation.Method != AccessFilesystemRead {
				continue
			}
			sawPath = true
			if reason, flagged := pathRuleDecision(observation.Value, rules); flagged {
				report.Findings = append(report.Findings, PathFinding{
					RuleID: pathRulesPolicyID, Path: observation.Value, Mode: rules.Mode, Reason: reason,
					SourceEventID: pathRuleSourceEventID(event), SessionID: strings.TrimSpace(event.SessionID),
					PolicyVersion: pathRulesPolicyVersion, DetectorVersion: pathRulesDetector,
				})
			}
		}
	}
	if !sawPath {
		return report
	}
	report.Visibility = "observed"
	if len(report.Findings) > 0 {
		report.Outcome = OutcomeViolation
	} else {
		report.Outcome = OutcomeNotViolation
	}
	sort.Slice(report.Findings, func(i, j int) bool {
		return report.Findings[i].SourceEventID+"\x00"+report.Findings[i].Path < report.Findings[j].SourceEventID+"\x00"+report.Findings[j].Path
	})
	return report
}

func pathRuleSourceEventID(event canonical.Event) string {
	if sourceEventID, ok := event.Attributes["source_event_id"].(string); ok && strings.TrimSpace(sourceEventID) != "" {
		return sourceEventID
	}
	return event.EventID
}

func pathRuleDecision(path string, rules config.PathRules) (string, bool) {
	if matchesPathPatterns(path, rules.Blocked) {
		return "blocked_match", true
	}
	switch rules.Mode {
	case "approved_only":
		return "not_allowed", !matchesPathPatterns(path, rules.Allowed)
	case "flag_all":
		return "flag_all", true
	default: // monitor: only an explicit blocked pattern creates a finding.
		return "", false
	}
}

func matchesPathPatterns(path string, patterns []config.PathRulePattern) bool {
	for _, pattern := range patterns {
		if pattern.Kind == "exact" && path == pattern.Value {
			return true
		}
		if pattern.Kind == "glob" && pathGlobMatch(pattern.Value, path) {
			return true
		}
	}
	return false
}

// pathGlobMatch implements the small documented grammar without filepath.Match:
// * stays within a slash-delimited segment, ** crosses segments, and ? matches
// one non-separator. Paths stay raw and case-sensitive on every host OS.
func pathGlobMatch(pattern, value string) bool {
	matcher := pathMatcher{pattern: []rune(pattern), value: []rune(value)}
	return matcher.match(0, 0)
}

type pathMatcher struct {
	pattern []rune
	value   []rune
}

func (m pathMatcher) match(pi, vi int) bool {
	if pi == len(m.pattern) {
		return vi == len(m.value)
	}
	switch m.pattern[pi] {
	case '*':
		return m.matchStar(pi, vi)
	case '?':
		return m.matchQuestion(pi, vi)
	default:
		return m.matchLiteral(pi, vi)
	}
}

func (m pathMatcher) matchStar(pi, vi int) bool {
	crossSegment := pi+1 < len(m.pattern) && m.pattern[pi+1] == '*'
	next := pi + 1
	if crossSegment {
		next++
		// A **/ group may consume zero complete segments, including its slash.
		if next < len(m.pattern) && m.pattern[next] == '/' && m.match(next+1, vi) {
			return true
		}
	}
	for end := vi; end <= len(m.value); end++ {
		if !crossSegment && end > vi && m.value[end-1] == '/' {
			break
		}
		if m.match(next, end) {
			return true
		}
	}
	return false
}

func (m pathMatcher) matchQuestion(pi, vi int) bool {
	return vi < len(m.value) && m.value[vi] != '/' && m.match(pi+1, vi+1)
}

func (m pathMatcher) matchLiteral(pi, vi int) bool {
	return vi < len(m.value) && m.pattern[pi] == m.value[vi] && m.match(pi+1, vi+1)
}

// Decision renders the report as the existing policy schema vocabulary.
func (p PathRules) Decision() PolicyDecision {
	evidence := make([]PolicyEvidence, 0, len(p.Findings))
	for _, finding := range p.Findings {
		evidence = append(evidence, PolicyEvidence{Kind: "path", Reference: "path:" + finding.Path + ";reason:" + finding.Reason + ";event:" + finding.SourceEventID})
	}
	if len(evidence) == 0 {
		reference := "filesystem_path_unavailable"
		if p.Visibility == "policy_unconfigured" {
			reference = "path_rules_not_configured"
		} else if p.Outcome == OutcomeNotViolation {
			reference = "no_path_rule_findings"
		}
		evidence = append(evidence, PolicyEvidence{Kind: "visibility", Reference: reference})
	}
	return PolicyDecision{SchemaVersion: policySchemaVersion, PolicyID: pathRulesPolicyID, PolicyVersion: pathRulesPolicyVersion, Outcome: p.Outcome, Evidence: evidence}
}
