package api

import (
	"testing"
)

// TestSkillUsageInsightIngestEndToEnd is the live-data DoD gate for the skill
// usage insight: it POSTs a raw Claude Code OTLP log payload to the live
// /v1/logs receiver and reads the skill-usage insight back through the real HTTP
// read API. Claude Code stamps no explicit skill identity today, so the honest
// end-to-end result is a coverage row marking the provider unavailable rather
// than a silent zero, with no secret canary surviving the round trip.
func TestSkillUsageInsightIngestEndToEnd(t *testing.T) {
	server, _ := newPersistentTestServer(t)
	ingestClaudeOTLP(t, server)

	usage := getInsightJSON[skillUsageResponse](t, server.URL+"/api/v1/insights/skill-usage")
	if usage.Data.Totals.ObservedSkills != 0 || len(usage.Data.Skills) != 0 {
		t.Fatalf("Claude ingest must not fabricate skills: %#v", usage.Data)
	}
	if usage.Data.Totals.UnavailableDetection < 1 {
		t.Fatalf("expected an unavailable-detection surface, got totals %#v", usage.Data.Totals)
	}
	found := false
	for _, row := range usage.Data.Coverage {
		if row.Provider == "anthropic" && row.Tool == "claude-code" {
			found = true
			if row.DetectionState != "unavailable" {
				t.Fatalf("Claude skill detection = %q, want unavailable", row.DetectionState)
			}
		}
	}
	if !found {
		t.Fatalf("Claude surface missing from skill-usage coverage: %#v", usage.Data.Coverage)
	}

	assertNoRawIdentifiers(t,
		[]string{"tiq-canary-session", "tiq-canary@example.test", "tiq-canary-api-key"},
		marshalJSON(t, usage))
}
