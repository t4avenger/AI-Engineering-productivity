package insights

import (
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestGovernanceStatesKeepProviderValuesAndAuthRecoveryDistinct(t *testing.T) {
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	events := []canonical.Event{
		governanceStateTestEvent("context", at, map[string]any{
			"approval_policy": "never", "sandbox_policy": "workspace-write", "auth_mode": "Chatgpt",
		}),
		governanceStateTestEvent("recovery", at.Add(time.Second), map[string]any{
			"auth_mode": "managed", "lifecycle_kind": "auth_recovery", "lifecycle_phase": "reload", "lifecycle_status": "recovery_not_run",
		}),
	}
	report := GovernanceStatesFromEvents(events)
	if len(report.States) != 5 {
		t.Fatalf("states = %#v", report.States)
	}
	if report.States[0].Kind != "auth_mode" || report.States[0].Value != "managed" {
		t.Fatalf("first state = %#v", report.States[0])
	}
	if report.States[1].Kind != "auth_recovery" || report.States[1].Value != "recovery_not_run" || report.States[1].Phase == nil || *report.States[1].Phase != "reload" {
		t.Fatalf("auth recovery = %#v", report.States[1])
	}
	for _, state := range report.States {
		if state.Provenance != "observed" || state.SourceEventID == "" {
			t.Fatalf("provenance = %#v", state)
		}
	}
}

func TestGovernanceStatesRetainChangesAndDeduplicateRepeatedEvidence(t *testing.T) {
	at := time.Unix(1, 0).UTC()
	events := []canonical.Event{
		governanceStateTestEvent("old-never", at, map[string]any{"approval_policy": "never"}),
		governanceStateTestEvent("new-never", at.Add(time.Second), map[string]any{"approval_policy": "never"}),
		governanceStateTestEvent("on-request", at.Add(2*time.Second), map[string]any{"approval_policy": "on-request"}),
	}
	report := GovernanceStatesFromEvents(events)
	if len(report.States) != 2 || report.States[0].Value != "on-request" || report.States[1].SourceEventID != "new-never" {
		t.Fatalf("states = %#v", report.States)
	}
}

func governanceStateTestEvent(id string, at time.Time, attributes map[string]any) canonical.Event {
	return canonical.Event{
		EventID: id, SessionID: "codex:synthetic", OccurredAt: at, Provider: "openai", Tool: "codex", Attributes: attributes,
	}
}
