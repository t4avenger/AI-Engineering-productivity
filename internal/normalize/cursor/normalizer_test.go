package cursor

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeCapabilityProbeYieldsNoEvents(t *testing.T) {
	events, err := Normalize(readFixture(t, "cursor-agent-2026.05.16-0338208-capability-probe.json"), stubFingerprint)
	if err != nil {
		t.Fatalf("normalise probe: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("capability probe must not fabricate events, got %d", len(events))
	}
}

func TestNormalizeKeepsNativeSessionAndDoesNotLeakProtectedIDs(t *testing.T) {
	events, err := Normalize(readFixture(t, "cursor-agent-2026.05.16-0338208-print-result.json"), stubFingerprint)
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
	serialized, _ := json.Marshal(events)
	for _, leaked := range []string{"synthetic-request-id"} {
		if strings.Contains(string(serialized), leaked) {
			t.Fatalf("protected identifier leaked into canonical output: %q", leaked)
		}
	}
}
