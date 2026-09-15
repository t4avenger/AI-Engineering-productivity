package claude

import (
	"reflect"
	"testing"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

const sessionGovernanceFixture = "claude-code-2.1.273-session-governance.json"

// TestNormalizeSessionGovernanceGolden pins the canonical events produced from the
// session-state & governance fixture (E9, #96).
func TestNormalizeSessionGovernanceGolden(t *testing.T) {
	events, err := NormalizeEvents(readFixture(t, sessionGovernanceFixture))
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	if updateGolden() {
		writeGolden(t, "claude-code-2.1.273-session-governance.events.json", events)
	}
	assertMatchesGolden(t, "claude-code-2.1.273-session-governance.events.json", events)
}

// TestNormalizeSessionGovernanceContracts proves each of the three lifecycle events
// is persisted with the right EventType and a governance contract: a
// bypassPermissions transition is flagged, a benign transition is not, and auth
// success/failure (with status_code) are surfaced.
func TestNormalizeSessionGovernanceContracts(t *testing.T) {
	events, err := NormalizeEvents(readFixture(t, sessionGovernanceFixture))
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("event count = %d, want 5", len(events))
	}

	var sawPlugin, sawBenignMode, sawBypassMode, sawAuthOK, sawAuthFail bool
	for _, event := range events {
		governance := requireGovernance(t, event)
		switch event.EventType {
		case eventPluginLoaded:
			sawPlugin = true
			assertPluginLoaded(t, governance)
		case eventPermissionModeChanged:
			if governance["to_mode"] == permissionModeBypass {
				sawBypassMode = true
				assertBool(t, governance, "bypass_permissions", true)
			} else {
				sawBenignMode = true
				assertBool(t, governance, "bypass_permissions", false)
			}
		case eventAuth:
			if governance["success"] == true {
				sawAuthOK = true
				if _, present := governance["status_code"]; present {
					t.Fatalf("successful auth must not carry status_code: %#v", governance)
				}
			} else {
				sawAuthFail = true
				assertAuthFailure(t, governance)
			}
		default:
			t.Fatalf("unexpected governance event type %q", event.EventType)
		}
	}
	if !sawPlugin || !sawBenignMode || !sawBypassMode || !sawAuthOK || !sawAuthFail {
		t.Fatalf("missing a governance case: plugin=%v benignMode=%v bypassMode=%v authOK=%v authFail=%v",
			sawPlugin, sawBenignMode, sawBypassMode, sawAuthOK, sawAuthFail)
	}
}

// TestNormalizeSessionGovernanceOTLPParity proves the raw-wire (/v1/logs) form and
// the reviewed sample-event form yield identical governance contracts, so the live
// ingest path is not silently divergent.
func TestNormalizeSessionGovernanceOTLPParity(t *testing.T) {
	sample, err := NormalizeEvents(readFixture(t, sessionGovernanceFixture))
	if err != nil {
		t.Fatalf("sample normalise: %v", err)
	}
	wire := normalizeObservedOTLPLogs(t, "claude-code-2.1.273-session-governance-otlp.json")
	if len(wire) != len(sample) {
		t.Fatalf("wire events = %d, sample events = %d", len(wire), len(sample))
	}
	for i := range sample {
		if wire[i].EventType != sample[i].EventType {
			t.Fatalf("event type parity mismatch at %d: %q vs %q", i, wire[i].EventType, sample[i].EventType)
		}
		if !reflect.DeepEqual(wire[i].ProviderExtensions["governance"], sample[i].ProviderExtensions["governance"]) {
			t.Fatalf("governance parity mismatch at %d:\n sample: %#v\n wire:   %#v",
				i, sample[i].ProviderExtensions["governance"], wire[i].ProviderExtensions["governance"])
		}
	}
}

// TestNormalizeGovernanceStampingIsEventNameGated is the negative guard: a
// non-governance event that happens to carry a governance-looking field (a stray
// from_mode) is never stamped with a governance extension, proving the stamp is
// gated on the event name rather than sniffing fields.
func TestNormalizeGovernanceStampingIsEventNameGated(t *testing.T) {
	extensions := map[string]any{}
	attachGovernanceContext(extensions, map[string]any{"from_mode": "default", "to_mode": "plan"}, eventAPIRequest)
	if _, present := extensions["governance"]; present {
		t.Fatalf("non-governance event was stamped with a governance extension: %#v", extensions)
	}
}

func requireGovernance(t *testing.T, event canonical.Event) map[string]any {
	t.Helper()
	governance, ok := event.ProviderExtensions["governance"].(map[string]any)
	if !ok {
		t.Fatalf("governance extension missing on %q: %#v", event.EventType, event.ProviderExtensions)
	}
	if governance["kind"] != event.EventType {
		t.Fatalf("governance kind = %v, want %q", governance["kind"], event.EventType)
	}
	return governance
}

func assertPluginLoaded(t *testing.T, governance map[string]any) {
	t.Helper()
	if governance["plugin_scope"] != "user-local" {
		t.Fatalf("plugin_scope = %v, want user-local", governance["plugin_scope"])
	}
	if governance["plugin_id_hash"] != "4c6529ae909eac7d" {
		t.Fatalf("plugin_id_hash = %v", governance["plugin_id_hash"])
	}
	assertBool(t, governance, "has_hooks", false)
	assertBool(t, governance, "safe_mode", false)
	if governance["skill_path_count"] != int64(0) {
		t.Fatalf("skill_path_count = %#v, want int64(0)", governance["skill_path_count"])
	}
}

func assertAuthFailure(t *testing.T, governance map[string]any) {
	t.Helper()
	if governance["error_category"] != "oauth_error" {
		t.Fatalf("error_category = %v, want oauth_error", governance["error_category"])
	}
	if governance["status_code"] != int64(403) {
		t.Fatalf("status_code = %#v, want int64(403)", governance["status_code"])
	}
}

func assertBool(t *testing.T, governance map[string]any, key string, want bool) {
	t.Helper()
	got, ok := governance[key].(bool)
	if !ok {
		t.Fatalf("%s missing or not a bool: %#v", key, governance[key])
	}
	if got != want {
		t.Fatalf("%s = %v, want %v", key, got, want)
	}
}
