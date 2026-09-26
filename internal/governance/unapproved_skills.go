package governance

import (
	"sort"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

const (
	unapprovedSkillPolicyID      = "governance.unapproved_skills"
	unapprovedSkillPolicyVersion = "0.1.0"
	unapprovedSkillDetector      = "0.1.0"
)

// SkillFinding is an evidence-backed observation of an explicit provider skill
// that is outside the saved local allowlist. It never represents inferred or
// anonymous telemetry as a named skill.
type SkillFinding struct {
	RuleID          string   `json:"rule_id"`
	SkillName       string   `json:"skill_name"`
	Provider        string   `json:"provider"`
	Tool            string   `json:"tool"`
	SourceEventIDs  []string `json:"source_event_ids"`
	PolicyVersion   string   `json:"policy_version"`
	DetectorVersion string   `json:"detector_version"`
}

// UnapprovedSkills is the local detect-and-report evaluation for explicit
// provider skill identities.
type UnapprovedSkills struct {
	Findings   []SkillFinding `json:"findings"`
	Outcome    Outcome        `json:"outcome"`
	Visibility string         `json:"visibility"`
}

// UnapprovedSkillsFromEvents evaluates only provider-stamped explicit skill
// identities. An empty allowlist, missing identities, and non-explicit signals
// are indeterminate rather than an invented approval or named violation.
func UnapprovedSkillsFromEvents(events []canonical.Event, allowlist []string) UnapprovedSkills {
	result := UnapprovedSkills{
		Findings:   []SkillFinding{},
		Outcome:    OutcomeIndeterminate,
		Visibility: "unavailable",
	}
	if len(allowlist) == 0 {
		result.Visibility = "policy_unconfigured"
		return result
	}

	allowed := exactSkillSet(allowlist)
	findings := make(map[string]*SkillFinding)
	sawExplicit := false
	sawIncomplete := false
	for _, event := range events {
		explicit, incomplete := skillPolicyEvidence(event, allowed, findings)
		sawExplicit = sawExplicit || explicit
		sawIncomplete = sawIncomplete || incomplete
	}

	result.Findings = sortedSkillFindings(findings)
	return applySkillOutcome(result, sawExplicit, sawIncomplete)
}

func exactSkillSet(allowlist []string) map[string]struct{} {
	allowed := make(map[string]struct{}, len(allowlist))
	for _, name := range allowlist {
		allowed[name] = struct{}{}
	}
	return allowed
}

func skillPolicyEvidence(event canonical.Event, allowed map[string]struct{}, findings map[string]*SkillFinding) (explicit, incomplete bool) {
	name, explicit := explicitSkillName(event)
	if !explicit {
		return false, skillSignalPresent(event)
	}
	if _, ok := allowed[name]; ok {
		return true, false
	}
	key := event.Provider + "\x00" + event.Tool + "\x00" + name
	finding := findings[key]
	if finding == nil {
		finding = newSkillFinding(event, name)
		findings[key] = finding
	}
	finding.SourceEventIDs = append(finding.SourceEventIDs, event.EventID)
	return true, false
}

func newSkillFinding(event canonical.Event, name string) *SkillFinding {
	return &SkillFinding{
		RuleID:          "unapproved_skill",
		SkillName:       name,
		Provider:        event.Provider,
		Tool:            event.Tool,
		SourceEventIDs:  []string{},
		PolicyVersion:   unapprovedSkillPolicyVersion,
		DetectorVersion: unapprovedSkillDetector,
	}
}

func sortedSkillFindings(findings map[string]*SkillFinding) []SkillFinding {
	result := make([]SkillFinding, 0, len(findings))
	for _, finding := range findings {
		sort.Strings(finding.SourceEventIDs)
		result = append(result, *finding)
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := result[i], result[j]
		return left.Provider+"\x00"+left.Tool+"\x00"+left.SkillName < right.Provider+"\x00"+right.Tool+"\x00"+right.SkillName
	})
	return result
}

func applySkillOutcome(result UnapprovedSkills, sawExplicit, sawIncomplete bool) UnapprovedSkills {
	switch {
	case len(result.Findings) > 0:
		result.Outcome = OutcomeViolation
		result.Visibility = "observed"
	case !sawExplicit:
		if sawIncomplete {
			result.Visibility = "partial"
		}
	case sawIncomplete:
		result.Visibility = "partial"
	default:
		result.Outcome = OutcomeNotViolation
		result.Visibility = "observed"
	}
	return result
}

func skillSignalPresent(event canonical.Event) bool {
	if event.EventType == "skill_coverage" && event.ProviderExtensions["skill_detection"] == "explicit" {
		// Insight-source storage adds this synthetic coverage marker beside the
		// retained explicit invocation. It is not an anonymous invocation and
		// must not downgrade an otherwise complete allowlist decision.
		return false
	}
	_, present := event.ProviderExtensions["skill_detection"]
	return present
}

func explicitSkillName(event canonical.Event) (string, bool) {
	if event.ProviderExtensions["skill_detection"] != "explicit" {
		return "", false
	}
	skill, ok := event.ProviderExtensions["skill"].(map[string]any)
	if !ok {
		return "", false
	}
	name, ok := skill["name"].(string)
	return name, ok && name != ""
}

// Decision renders the local policy report as a policy-schema decision.
func (u UnapprovedSkills) Decision() PolicyDecision {
	evidence := make([]PolicyEvidence, 0, len(u.Findings))
	for _, finding := range u.Findings {
		for _, eventID := range finding.SourceEventIDs {
			evidence = append(evidence, PolicyEvidence{
				Kind:      "skill",
				Reference: "skill:" + finding.SkillName + ";provider:" + finding.Provider + ";tool:" + finding.Tool + ";event:" + eventID,
			})
		}
	}
	if len(evidence) == 0 {
		reference := "no_explicit_skills_observed"
		if u.Visibility == "policy_unconfigured" {
			reference = "skills_allowlist_not_configured"
		} else if u.Visibility == "partial" {
			reference = "skill_identity_indeterminate"
		} else if u.Outcome == OutcomeNotViolation {
			reference = "all_explicit_skills_allowlisted"
		}
		evidence = append(evidence, PolicyEvidence{Kind: "visibility", Reference: reference})
	}
	return PolicyDecision{
		SchemaVersion: policySchemaVersion,
		PolicyID:      unapprovedSkillPolicyID,
		PolicyVersion: unapprovedSkillPolicyVersion,
		Outcome:       u.Outcome,
		Evidence:      evidence,
	}
}
