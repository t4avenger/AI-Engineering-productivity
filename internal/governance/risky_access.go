// Package governance implements TelemetryIQ's detect-and-report policies
// (PRODUCT_MAP.md §14). Policies never block; they surface privacy-safe findings
// and use the indeterminate outcome whenever the underlying telemetry cannot
// support a verdict, never a fabricated clean result.
package governance

import (
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/privacy"
)

// riskyAccessRuleID identifies the risky credential/secret file-access policy.
const riskyAccessRuleID = "risky_credential_file_access"

// riskyAccessPolicyVersion is this detector's own contract version.
const riskyAccessPolicyVersion = "0.1.0"

// policySchemaVersion matches schemas/policy.schema.json.
const policySchemaVersion = "0.1.0"

// AccessMethod records how a credential/secret file was reached.
type AccessMethod string

const (
	AccessFilesystemRead AccessMethod = "filesystem_read"
	AccessShellCommand   AccessMethod = "shell_command"
)

// Outcome mirrors the policy.schema.json decision vocabulary.
type Outcome string

const (
	OutcomeViolation     Outcome = "violation"
	OutcomeNotViolation  Outcome = "not_violation"
	OutcomeIndeterminate Outcome = "indeterminate"
)

// Finding is a single privacy-safe risky-access observation. It deliberately
// carries only coarse, non-reversible evidence — never a raw path, command, or
// secret value.
type Finding struct {
	RuleID       string       `json:"rule_id"`
	AccessMethod AccessMethod `json:"access_method"`
	Class        string       `json:"class"`
	Boundary     string       `json:"boundary"`
	Confidence   string       `json:"confidence"`
	ObservedAt   string       `json:"observed_at"`
}

// RiskyAccess is the aggregate risky-access report for a set of events.
type RiskyAccess struct {
	Findings   []Finding `json:"findings"`
	Outcome    Outcome   `json:"outcome"`
	Visibility string    `json:"visibility"`
}

// PolicyEvidence is one privacy-safe evidence reference in a policy decision.
type PolicyEvidence struct {
	Kind      string `json:"kind"`
	Reference string `json:"reference"`
}

// PolicyDecision is the schemas/policy.schema.json representation of the report,
// suitable for persistence or export. Every evidence reference is privacy-safe.
type PolicyDecision struct {
	SchemaVersion string           `json:"schema_version"`
	PolicyID      string           `json:"policy_id"`
	PolicyVersion string           `json:"policy_version"`
	Outcome       Outcome          `json:"outcome"`
	Evidence      []PolicyEvidence `json:"evidence"`
}

// secretPathClasses are the ClassifyPath classes that denote a credential or
// secret file rather than ordinary project source.
var secretPathClasses = map[privacy.PathClass]struct{}{
	privacy.PathDotenv:          {},
	privacy.PathSSHKey:          {},
	privacy.PathCert:            {},
	privacy.PathCredentialsFile: {},
}

// RiskyAccessFromEvents evaluates the risky credential/secret file-access policy
// over canonical events. Detection runs entirely on the privacy-safe tokens the
// sanitiser emits in place of raw paths and commands, so no raw evidence is ever
// read here. When no filesystem-read or shell-command access is observed at all,
// the outcome is indeterminate — the policy never claims a clean read from absent
// visibility.
func RiskyAccessFromEvents(events []canonical.Event) RiskyAccess {
	report := RiskyAccess{Findings: []Finding{}, Outcome: OutcomeIndeterminate, Visibility: "unavailable"}
	observedAccess := false

	for _, event := range events {
		observedAt := event.OccurredAt.UTC().Format("2006-01-02T15:04:05Z07:00")
		for _, token := range collectTokens(event) {
			finding, observed := classifyToken(token, observedAt)
			if observed {
				observedAccess = true
			}
			if finding != nil {
				report.Findings = append(report.Findings, *finding)
			}
		}
	}

	switch {
	case len(report.Findings) > 0:
		report.Outcome = OutcomeViolation
		report.Visibility = "observed"
	case observedAccess:
		report.Outcome = OutcomeNotViolation
		report.Visibility = "observed"
	default:
		report.Outcome = OutcomeIndeterminate
		report.Visibility = "unavailable"
	}
	return report
}

// classifyToken inspects one privacy-safe token. It reports whether the token
// records a file or command access at all (observed) and, when the access is a
// credential/secret read, the resulting finding.
func classifyToken(token, observedAt string) (finding *Finding, observed bool) {
	if class, boundary, ok := privacy.ParsePathToken(token); ok {
		if _, secret := secretPathClasses[class]; secret {
			finding = &Finding{
				RuleID:       riskyAccessRuleID,
				AccessMethod: AccessFilesystemRead,
				Class:        string(class),
				Boundary:     string(boundary),
				Confidence:   "high",
				ObservedAt:   observedAt,
			}
		}
		return finding, true
	}
	if class, boundary, ok := privacy.ParseCommandAccessToken(token); ok {
		if class == privacy.CommandAccessCredential {
			finding = &Finding{
				RuleID:       riskyAccessRuleID,
				AccessMethod: AccessShellCommand,
				Class:        string(privacy.PathDotenv),
				Boundary:     string(boundary),
				Confidence:   "medium",
				ObservedAt:   observedAt,
			}
		}
		return finding, true
	}
	return nil, false
}

// Decision renders the report as a schemas/policy.schema.json decision. Evidence
// is one entry per finding, or a single visibility marker when there is no
// finding, so a consumer always learns why.
func (r RiskyAccess) Decision() PolicyDecision {
	evidence := make([]PolicyEvidence, 0, len(r.Findings))
	for _, finding := range r.Findings {
		evidence = append(evidence, PolicyEvidence{
			Kind:      "credential_access",
			Reference: string(finding.AccessMethod) + ";class:" + finding.Class + ";boundary:" + finding.Boundary + ";confidence:" + finding.Confidence,
		})
	}
	if len(evidence) == 0 {
		reference := "file_and_command_access_unavailable"
		if r.Outcome == OutcomeNotViolation {
			reference = "no_credential_file_access_observed"
		}
		evidence = append(evidence, PolicyEvidence{Kind: "visibility", Reference: reference})
	}
	return PolicyDecision{
		SchemaVersion: policySchemaVersion,
		PolicyID:      "governance.risky_access",
		PolicyVersion: riskyAccessPolicyVersion,
		Outcome:       r.Outcome,
		Evidence:      evidence,
	}
}

// collectTokens returns every string value in an event's attributes and provider
// extensions, walked recursively, so a path/command token is found wherever a
// normaliser placed it (top-level attributes, or nested under provider_extensions).
func collectTokens(event canonical.Event) []string {
	var tokens []string
	walkStrings(event.Attributes, &tokens)
	walkStrings(event.ProviderExtensions, &tokens)
	return tokens
}

func walkStrings(value any, out *[]string) {
	switch typed := value.(type) {
	case string:
		*out = append(*out, typed)
	case map[string]any:
		for _, nested := range typed {
			walkStrings(nested, out)
		}
	case []any:
		for _, nested := range typed {
			walkStrings(nested, out)
		}
	}
}
