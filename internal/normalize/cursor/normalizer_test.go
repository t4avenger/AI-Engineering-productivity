package cursor

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeCapabilityProbeYieldsNoEvents(t *testing.T) {
	events, err := Normalize(readFixture(t, "cursor-agent-2026.05.16-0338208-capability-probe.json"))
	if err != nil {
		t.Fatalf("normalise probe: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("capability probe must not fabricate events, got %d", len(events))
	}
}

// TestNormalizeRetainsNativeSessionAndRequestIDs asserts the no-hiding invariant
// (issue #88): the raw provider-native session and request identifiers are
// retained verbatim in canonical output, never fingerprinted or redacted.
func TestNormalizeRetainsNativeSessionAndRequestIDs(t *testing.T) {
	events, err := Normalize(readFixture(t, "cursor-agent-2026.05.16-0338208-print-result.json"))
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	event := events[0]
	if event.SessionID != "cursor-agent:synthetic-session-id" {
		t.Fatalf("session id = %q, want native provider ID", event.SessionID)
	}
	if event.EventID != "cursor-agent:synthetic-request-id" {
		t.Fatalf("event id = %q, want raw native request ID retained", event.EventID)
	}
	serialized, _ := json.Marshal(events)
	if !strings.Contains(string(serialized), "cursor-agent:synthetic-request-id") {
		t.Fatalf("raw request identifier must be retained in canonical output: %s", serialized)
	}
}
