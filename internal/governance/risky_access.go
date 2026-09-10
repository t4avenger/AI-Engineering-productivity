// Package governance implements TelemetryIQ's detect-and-report policies
// (PRODUCT_MAP.md §14). Policies never block; they surface findings and use the
// indeterminate outcome whenever the underlying telemetry cannot support a
// verdict, never a fabricated clean result.
package governance

import (
	"strings"

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

// Finding is a single risky-access observation. It carries the coarse
// classification (class/boundary) alongside the raw path or command that
// triggered it: TelemetryIQ captures and displays raw evidence (epic #87 — no
// ingest-time hiding), so the finding references the real value rather than a
// hashed or tokenised placeholder.
type Finding struct {
	RuleID       string       `json:"rule_id"`
	AccessMethod AccessMethod `json:"access_method"`
	Class        string       `json:"class"`
	Boundary     string       `json:"boundary"`
	Confidence   string       `json:"confidence"`
	Reference    string       `json:"reference"`
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
// over canonical events. Detection runs ClassifyPath/ClassifyCommandAccess over
// the raw file paths and command lines now present in event attributes and
// provider extensions (epic #87 — no ingest-time hiding). When no
// filesystem-read or shell-command access is observed at all, the outcome is
// indeterminate — the policy never claims a clean read from absent visibility.
func RiskyAccessFromEvents(events []canonical.Event) RiskyAccess {
	report := RiskyAccess{Findings: []Finding{}, Outcome: OutcomeIndeterminate, Visibility: "unavailable"}
	observedAccess := false

	for _, event := range events {
		observedAt := event.OccurredAt.UTC().Format("2006-01-02T15:04:05Z07:00")
		for _, access := range collectAccesses(event) {
			finding, observed := classifyAccess(access, observedAt)
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

// classifyAccess classifies one raw access. It reports whether the access
// records a file or command access at all (observed) and, when the access is a
// credential/secret read, the resulting finding referencing the raw value.
func classifyAccess(access access, observedAt string) (finding *Finding, observed bool) {
	switch access.method {
	case AccessFilesystemRead:
		class, boundary := privacy.ClassifyPath(access.value)
		if _, secret := secretPathClasses[class]; secret {
			finding = &Finding{
				RuleID:       riskyAccessRuleID,
				AccessMethod: AccessFilesystemRead,
				Class:        string(class),
				Boundary:     string(boundary),
				Confidence:   "high",
				Reference:    access.value,
				ObservedAt:   observedAt,
			}
		}
		return finding, true
	case AccessShellCommand:
		class, boundary := privacy.ClassifyCommandAccess(access.value)
		if class == privacy.CommandAccessCredential {
			finding = &Finding{
				RuleID:       riskyAccessRuleID,
				AccessMethod: AccessShellCommand,
				Class:        string(class),
				Boundary:     string(boundary),
				Confidence:   "medium",
				Reference:    access.value,
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
			Reference: string(finding.AccessMethod) + ";class:" + finding.Class + ";boundary:" + finding.Boundary + ";confidence:" + finding.Confidence + ";path:" + finding.Reference,
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

// access is one raw file path or command line observed on an event, tagged with
// how it was reached so the correct classifier is applied.
type access struct {
	method AccessMethod
	value  string
}

// pathFieldKeys name event attributes that carry a raw file path reached by a
// filesystem read.
var pathFieldKeys = map[string]struct{}{
	"file_path": {}, "path": {}, "file": {}, "filename": {},
}

// commandFieldKeys name event attributes that carry a raw shell command line.
var commandFieldKeys = map[string]struct{}{
	"command": {}, "command_line": {}, "cmd": {},
}

// collectAccesses walks an event's attributes and provider extensions
// recursively and returns every raw path or command it finds under a known
// path/command field key, so detection runs on the raw value wherever a
// normaliser placed it (top-level attributes, or nested under
// provider_extensions).
func collectAccesses(event canonical.Event) []access {
	var accesses []access
	walkAccesses(event.Attributes, &accesses)
	walkAccesses(event.ProviderExtensions, &accesses)
	return accesses
}

func walkAccesses(value any, out *[]access) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if leafAccess(key, nested, out) {
				continue
			}
			walkAccesses(nested, out)
		}
	case []any:
		for _, nested := range typed {
			walkAccesses(nested, out)
		}
	}
}

// leafAccess records a raw path or command when nested is a non-empty string
// under a known field key, and reports whether it consumed the value.
func leafAccess(key string, nested any, out *[]access) bool {
	text, ok := nested.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return false
	}
	if _, isPath := pathFieldKeys[key]; isPath {
		*out = append(*out, access{method: AccessFilesystemRead, value: text})
		return true
	}
	if _, isCommand := commandFieldKeys[key]; isCommand {
		*out = append(*out, access{method: AccessShellCommand, value: text})
		return true
	}
	return false
}
