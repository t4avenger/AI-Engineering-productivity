package governance

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// mcpConnectionEvent builds a canonical mcp_server_connection event whose
// provider-reported detail lives under provider_extensions["event"], mirroring
// what the ingest pipeline persists and what insights.MCPInventoryFromEvents reads.
func mcpConnectionEvent(id string, event map[string]any) canonical.Event {
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	return canonical.Event{
		SchemaVersion:      "0.1.0",
		EventID:            id,
		EventType:          "mcp_server_connection",
		OccurredAt:         at,
		ReceivedAt:         at,
		Provider:           "anthropic",
		Tool:               "claude-code",
		SessionID:          "session",
		Attributes:         map[string]any{},
		ProviderExtensions: map[string]any{"event": event},
	}
}

func TestUnapprovedMCPAllowlistedServerPasses(t *testing.T) {
	report := UnapprovedMCPFromEvents([]canonical.Event{
		mcpConnectionEvent("connected", map[string]any{
			"server_fingerprint": "mcp:hmac:filesystem",
			"server_name":        "filesystem",
			"status":             "connected",
		}),
	}, []string{"filesystem", "git"})

	if report.Outcome != OutcomeNotViolation {
		t.Fatalf("expected not_violation for allowlisted server, got %q", report.Outcome)
	}
	if len(report.Findings) != 0 {
		t.Fatalf("expected no findings, got %#v", report.Findings)
	}
	if report.Visibility != "observed" {
		t.Fatalf("expected observed visibility, got %q", report.Visibility)
	}
}

func TestUnapprovedMCPFlagsUnknownServer(t *testing.T) {
	report := UnapprovedMCPFromEvents([]canonical.Event{
		mcpConnectionEvent("connected", map[string]any{
			"server_fingerprint": "mcp:hmac:rogue",
			"server_name":        "rogue-tool",
			"status":             "connected",
		}),
	}, []string{"filesystem"})

	if report.Outcome != OutcomeViolation {
		t.Fatalf("expected violation for unlisted server, got %q", report.Outcome)
	}
	if len(report.Findings) != 1 || report.Findings[0].State != MCPServerUnapproved {
		t.Fatalf("expected one unapproved finding, got %#v", report.Findings)
	}
	if report.Findings[0].ServerName != "rogue-tool" {
		t.Fatalf("expected finding to name the observed server, got %q", report.Findings[0].ServerName)
	}
}

func TestUnapprovedMCPIndeterminateWhenIdentityUnavailable(t *testing.T) {
	// A connection with neither a fingerprint nor a server name: the server is
	// visible but unidentifiable, so approval cannot be judged and the policy
	// must not fabricate a clean or a violating result.
	report := UnapprovedMCPFromEvents([]canonical.Event{
		mcpConnectionEvent("connected", map[string]any{"status": "connected"}),
	}, []string{"filesystem"})

	if report.Outcome != OutcomeIndeterminate {
		t.Fatalf("expected indeterminate for unidentified server, got %q", report.Outcome)
	}
	if len(report.Findings) != 1 || report.Findings[0].State != MCPServerUnidentified {
		t.Fatalf("expected one unidentified finding, got %#v", report.Findings)
	}
	if report.Findings[0].IdentityState != "unavailable" {
		t.Fatalf("expected unavailable identity state, got %q", report.Findings[0].IdentityState)
	}
}

func TestUnapprovedMCPIndeterminateWhenAllowlistUnconfigured(t *testing.T) {
	report := UnapprovedMCPFromEvents([]canonical.Event{
		mcpConnectionEvent("connected", map[string]any{
			"server_fingerprint": "mcp:hmac:filesystem",
			"server_name":        "filesystem",
		}),
	}, nil)

	if report.Outcome != OutcomeIndeterminate {
		t.Fatalf("expected indeterminate when allowlist is unset, got %q", report.Outcome)
	}
	if report.Visibility != "policy_unconfigured" {
		t.Fatalf("expected policy_unconfigured visibility, got %q", report.Visibility)
	}
	if len(report.Findings) != 0 {
		t.Fatalf("expected no findings when policy is unconfigured, got %#v", report.Findings)
	}
}

func TestUnapprovedMCPIndeterminateWhenNoServersObserved(t *testing.T) {
	report := UnapprovedMCPFromEvents(nil, []string{"filesystem"})

	if report.Outcome != OutcomeIndeterminate {
		t.Fatalf("expected indeterminate when no servers observed, got %q", report.Outcome)
	}
	if report.Visibility != "unavailable" {
		t.Fatalf("expected unavailable visibility, got %q", report.Visibility)
	}
}

func TestUnapprovedMCPAllowlistMatchIsCaseInsensitive(t *testing.T) {
	report := UnapprovedMCPFromEvents([]canonical.Event{
		mcpConnectionEvent("connected", map[string]any{
			"server_fingerprint": "mcp:hmac:filesystem",
			"server_name":        "FileSystem",
		}),
	}, []string{"  filesystem  "})

	if report.Outcome != OutcomeNotViolation {
		t.Fatalf("expected not_violation for case/space-insensitive match, got %q", report.Outcome)
	}
}

func TestUnapprovedMCPDecisionValidatesAgainstPolicySchema(t *testing.T) {
	schema := compilePolicySchema(t)
	for name, report := range map[string]UnapprovedMCP{
		"violation": UnapprovedMCPFromEvents([]canonical.Event{
			mcpConnectionEvent("c", map[string]any{"server_fingerprint": "mcp:hmac:rogue", "server_name": "rogue"}),
		}, []string{"filesystem"}),
		"not_violation": UnapprovedMCPFromEvents([]canonical.Event{
			mcpConnectionEvent("c", map[string]any{"server_fingerprint": "mcp:hmac:filesystem", "server_name": "filesystem"}),
		}, []string{"filesystem"}),
		"indeterminate_unconfigured": UnapprovedMCPFromEvents([]canonical.Event{
			mcpConnectionEvent("c", map[string]any{"server_fingerprint": "mcp:hmac:filesystem", "server_name": "filesystem"}),
		}, nil),
		"indeterminate_unidentified": UnapprovedMCPFromEvents([]canonical.Event{
			mcpConnectionEvent("c", map[string]any{"status": "connected"}),
		}, []string{"filesystem"}),
		"indeterminate_no_servers": UnapprovedMCPFromEvents(nil, []string{"filesystem"}),
	} {
		t.Run(name, func(t *testing.T) {
			requireDecisionMatchesSchema(t, schema, report.Decision())
		})
	}
}

// requireDecisionMatchesSchema marshals a policy decision and asserts it
// validates against the compiled policy schema.
func requireDecisionMatchesSchema(t *testing.T, schema *jsonschema.Schema, decision PolicyDecision) {
	t.Helper()
	raw, err := json.Marshal(decision)
	if err != nil {
		t.Fatalf("marshal decision: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode decision: %v", err)
	}
	if err := schema.Validate(decoded); err != nil {
		t.Fatalf("decision must satisfy policy schema: %v", err)
	}
}
