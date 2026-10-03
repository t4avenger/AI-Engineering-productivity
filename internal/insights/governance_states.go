package insights

import (
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

type GovernanceStates struct {
	States []GovernanceState `json:"states"`
}

type GovernanceState struct {
	Provider      string    `json:"provider"`
	Tool          string    `json:"tool"`
	SessionID     string    `json:"session_id"`
	Kind          string    `json:"kind"`
	Value         string    `json:"value"`
	Phase         *string   `json:"phase"`
	Provenance    string    `json:"provenance"`
	SourceEventID string    `json:"source_event_id"`
	ObservedAt    time.Time `json:"observed_at"`
}

func GovernanceStatesFromEvents(events []canonical.Event) GovernanceStates {
	return GovernanceStates{States: latestObservedStates(
		events, governanceStatesFromEvent, governanceStateKey,
		func(state GovernanceState) time.Time { return state.ObservedAt },
		func(state GovernanceState) string { return state.SourceEventID },
	)}
}

func governanceStatesFromEvent(event canonical.Event) []GovernanceState {
	states := make([]GovernanceState, 0, 4)
	for _, kind := range []string{"approval_policy", "sandbox_policy", "auth_mode"} {
		if value, ok := observedAttribute(event.Attributes, kind); ok {
			states = append(states, newGovernanceState(event, kind, value, ""))
		}
	}
	if kind, ok := observedAttribute(event.Attributes, "lifecycle_kind"); ok && kind == "auth_recovery" {
		if status, observed := observedAttribute(event.Attributes, "lifecycle_status"); observed {
			phase, _ := observedAttribute(event.Attributes, "lifecycle_phase")
			states = append(states, newGovernanceState(event, kind, status, phase))
		}
	}
	return states
}

func newGovernanceState(event canonical.Event, kind, value, phase string) GovernanceState {
	state := GovernanceState{
		Provider: event.Provider, Tool: event.Tool, SessionID: event.SessionID, Kind: kind, Value: value,
		Provenance: string(canonical.ProvenanceObserved), SourceEventID: event.EventID, ObservedAt: event.OccurredAt.UTC(),
	}
	if phase = strings.TrimSpace(phase); phase != "" {
		state.Phase = &phase
	}
	return state
}

func governanceStateKey(state GovernanceState) string {
	phase := ""
	if state.Phase != nil {
		phase = *state.Phase
	}
	return strings.Join([]string{state.Provider, state.Tool, state.SessionID, state.Kind, state.Value, phase}, "\x00")
}
