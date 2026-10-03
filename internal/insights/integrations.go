package insights

import (
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

type IntegrationStates struct {
	States []IntegrationState `json:"states"`
}

type IntegrationState struct {
	Provider      string    `json:"provider"`
	Tool          string    `json:"tool"`
	Kind          string    `json:"kind"`
	Name          *string   `json:"name"`
	State         string    `json:"state"`
	Provenance    string    `json:"provenance"`
	SourceEventID string    `json:"source_event_id"`
	ObservedAt    time.Time `json:"observed_at"`
}

func IntegrationStatesFromEvents(events []canonical.Event) IntegrationStates {
	return IntegrationStates{States: latestObservedStates(
		events, integrationStatesFromEvent, integrationStateKey,
		func(state IntegrationState) time.Time { return state.ObservedAt },
		func(state IntegrationState) string { return state.SourceEventID },
	)}
}

func integrationStatesFromEvent(event canonical.Event) []IntegrationState {
	state, ok := integrationStateFromEvent(event)
	if !ok {
		return nil
	}
	return []IntegrationState{state}
}

func integrationStateFromEvent(event canonical.Event) (IntegrationState, bool) {
	kind, kindOK := observedAttribute(event.Attributes, "integration_kind")
	state, stateOK := observedAttribute(event.Attributes, "integration_state")
	name, _ := observedAttribute(event.Attributes, "integration_name")
	if kindOK && stateOK {
		return newIntegrationState(event, kind, name, state), true
	}
	mcp, ok := event.ProviderExtensions["mcp_call"].(map[string]any)
	if !ok {
		return IntegrationState{}, false
	}
	server, _ := mcp["server_name"].(string)
	return newIntegrationState(event, "mcp", strings.TrimSpace(server), "used"), true
}

func newIntegrationState(event canonical.Event, kind, name, state string) IntegrationState {
	result := IntegrationState{
		Provider: event.Provider, Tool: event.Tool, Kind: kind, State: state,
		Provenance: string(canonical.ProvenanceObserved), SourceEventID: event.EventID, ObservedAt: event.OccurredAt.UTC(),
	}
	if name = strings.TrimSpace(name); name != "" {
		result.Name = &name
	}
	return result
}

func observedAttribute(attributes map[string]any, key string) (string, bool) {
	value, ok := attributes[key].(string)
	value = strings.TrimSpace(value)
	return value, ok && value != ""
}

func optionalIntegrationName(name *string) string {
	if name == nil {
		return ""
	}
	return *name
}

func integrationStateKey(state IntegrationState) string {
	return strings.Join([]string{state.Provider, state.Tool, state.Kind, optionalIntegrationName(state.Name), state.State}, "\x00")
}
