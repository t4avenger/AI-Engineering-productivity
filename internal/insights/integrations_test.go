package insights

import (
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestIntegrationStatesKeepDiscoveryCacheAndUseDistinct(t *testing.T) {
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	events := []canonical.Event{
		integrationStateTestEvent("plugin-cache", at, "plugin", "", "cache_hit"),
		integrationStateTestEvent("mcp-discovery", at.Add(time.Second), "mcp", "", "discovered"),
		{EventID: "mcp-use", EventType: "codex.tool_result", OccurredAt: at.Add(2 * time.Second), Provider: "openai", Tool: "codex", ProviderExtensions: map[string]any{"mcp_call": map[string]any{"server_name": "tiq_probe"}}},
	}
	report := IntegrationStatesFromEvents(events)
	if len(report.States) != 3 {
		t.Fatalf("states = %#v", report.States)
	}
	if report.States[0].State != "used" || report.States[0].Name == nil || *report.States[0].Name != "tiq_probe" {
		t.Fatalf("latest state = %#v", report.States[0])
	}
	if report.States[1].State != "discovered" || report.States[2].State != "cache_hit" {
		t.Fatalf("state ordering/distinction = %#v", report.States)
	}
}

func TestIntegrationStatesDeduplicatesByExactStateAndKeepsLatestEvidence(t *testing.T) {
	old := integrationStateTestEvent("old", time.Unix(1, 0), "app", "", "refreshed")
	latest := integrationStateTestEvent("latest", time.Unix(2, 0), "app", "", "refreshed")
	report := IntegrationStatesFromEvents([]canonical.Event{latest, old})
	if len(report.States) != 1 || report.States[0].SourceEventID != "latest" {
		t.Fatalf("states = %#v", report.States)
	}
}

func integrationStateTestEvent(id string, at time.Time, kind, name, state string) canonical.Event {
	attributes := map[string]any{"integration_kind": kind, "integration_state": state}
	if name != "" {
		attributes["integration_name"] = name
	}
	return canonical.Event{EventID: id, OccurredAt: at, Provider: "openai", Tool: "codex", Attributes: attributes}
}
