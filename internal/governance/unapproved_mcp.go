package governance

import (
	"strings"

	"github.com/wayne/telemetryiq/internal/insights"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// unapprovedMCPRuleID identifies the unapproved MCP server policy.
const unapprovedMCPRuleID = "unapproved_mcp_server"

// unapprovedMCPPolicyVersion is the detector's own contract version.
const unapprovedMCPPolicyVersion = "0.1.0"

// MCPServerState classifies a single observed server against the allowlist.
type MCPServerState string

const (
	// MCPServerUnapproved is a server whose provider-reported name is not on the
	// configured allowlist.
	MCPServerUnapproved MCPServerState = "unapproved"
	// MCPServerUnidentified is a server the telemetry connected/used but for
	// which no provider-reported name is available, so approval cannot be judged.
	MCPServerUnidentified MCPServerState = "unidentified"
)

// MCPServerFinding is a single privacy-safe per-server allowlist observation. It
// carries only the provider-reported server name — the same identity the MCP
// inventory already surfaces — and the coarse allowlist state; never a raw path,
// command, or secret value.
type MCPServerFinding struct {
	RuleID        string         `json:"rule_id"`
	ServerName    string         `json:"server_name"`
	State         MCPServerState `json:"state"`
	IdentityState string         `json:"identity_state"`
}

// UnapprovedMCP is the aggregate unapproved-MCP report for a set of events.
type UnapprovedMCP struct {
	Findings   []MCPServerFinding `json:"findings"`
	Outcome    Outcome            `json:"outcome"`
	Visibility string             `json:"visibility"`
}

// UnapprovedMCPFromEvents evaluates the unapproved MCP server policy over
// canonical events. Server identity is derived from insights.MCPInventoryFromEvents
// (issue #27), which builds it from provider-reported names and privacy-safe
// fingerprints — the detector never reads raw connection detail itself.
//
// The policy is honest about what it can and cannot conclude:
//   - an unset/empty allowlist means the policy is not configured, so approval
//     cannot be decided either way → indeterminate (never a fabricated clean
//     result from an absent policy);
//   - no MCP servers observed at all → indeterminate;
//   - a server with no provider-reported name → indeterminate for that server,
//     since it can be neither confirmed approved nor flagged;
//   - a named server absent from the allowlist → violation;
//   - all identifiable servers allowlisted → not_violation.
func UnapprovedMCPFromEvents(events []canonical.Event, allowlist []string) UnapprovedMCP {
	report := UnapprovedMCP{Findings: []MCPServerFinding{}, Outcome: OutcomeIndeterminate, Visibility: "unavailable"}

	allowed := normaliseAllowlist(allowlist)
	if len(allowed) == 0 {
		report.Visibility = "policy_unconfigured"
		return report
	}

	inventory := insights.MCPInventoryFromEvents(events)
	if len(inventory.Servers) == 0 {
		return report
	}

	report.Visibility = "observed"
	sawUnapproved := false
	sawUnidentified := false
	for _, server := range inventory.Servers {
		name := strings.TrimSpace(server.ServerName)
		switch {
		case name == "":
			sawUnidentified = true
			report.Findings = append(report.Findings, MCPServerFinding{
				RuleID:        unapprovedMCPRuleID,
				State:         MCPServerUnidentified,
				IdentityState: server.IdentityState,
			})
		case allowed[normaliseServerName(name)]:
			// Approved. Mirroring risky_access, only actionable observations are
			// recorded as findings; an allowlisted server is not one.
		default:
			sawUnapproved = true
			report.Findings = append(report.Findings, MCPServerFinding{
				RuleID:        unapprovedMCPRuleID,
				ServerName:    name,
				State:         MCPServerUnapproved,
				IdentityState: server.IdentityState,
			})
		}
	}

	switch {
	case sawUnapproved:
		report.Outcome = OutcomeViolation
	case sawUnidentified:
		report.Outcome = OutcomeIndeterminate
	default:
		report.Outcome = OutcomeNotViolation
	}
	return report
}

// Decision renders the report as a schemas/policy.schema.json decision. Evidence
// is one entry per finding, or a single visibility marker when there is no
// finding, so a consumer always learns why.
func (u UnapprovedMCP) Decision() PolicyDecision {
	evidence := make([]PolicyEvidence, 0, len(u.Findings))
	for _, finding := range u.Findings {
		reference := "state:" + string(finding.State) + ";identity:" + finding.IdentityState
		if finding.ServerName != "" {
			reference = "server:" + finding.ServerName + ";" + reference
		}
		evidence = append(evidence, PolicyEvidence{Kind: "mcp_server", Reference: reference})
	}
	if len(evidence) == 0 {
		reference := "no_mcp_servers_observed"
		switch u.Visibility {
		case "policy_unconfigured":
			reference = "mcp_allowlist_not_configured"
		case "observed":
			reference = "all_observed_servers_allowlisted"
		}
		evidence = append(evidence, PolicyEvidence{Kind: "visibility", Reference: reference})
	}
	return PolicyDecision{
		SchemaVersion: policySchemaVersion,
		PolicyID:      "governance.unapproved_mcp",
		PolicyVersion: unapprovedMCPPolicyVersion,
		Outcome:       u.Outcome,
		Evidence:      evidence,
	}
}

// normaliseAllowlist builds a set of approved server names, comparing on a
// trimmed, lower-cased key so trivial casing/spacing differences do not cause a
// configured server to be flagged.
func normaliseAllowlist(allowlist []string) map[string]bool {
	allowed := make(map[string]bool, len(allowlist))
	for _, entry := range allowlist {
		if key := normaliseServerName(entry); key != "" {
			allowed[key] = true
		}
	}
	return allowed
}

func normaliseServerName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
