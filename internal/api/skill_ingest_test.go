package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wayne/telemetryiq/internal/privacy"
	"github.com/wayne/telemetryiq/internal/storage/sqlite"
)

// TestSkillUsageInsightIngestEndToEnd is the live-data DoD gate for the skill
// usage insight: it POSTs a raw Claude Code OTLP log payload to the live
// /v1/logs receiver and reads the skill-usage insight back through the real HTTP
// read API. Claude Code stamps no explicit skill identity today, so the honest
// end-to-end result is a coverage row marking the provider unavailable rather
// than a silent zero, with no secret canary surviving the round trip.
func TestSkillUsageInsightIngestEndToEnd(t *testing.T) {
	sanitizer, err := privacy.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repository, err := sqlite.Open(":memory:", sanitizer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	server := httptest.NewServer(NewPersistentHandler(slog.Default(), sanitizer, repository))
	t.Cleanup(server.Close)

	response := postOTLPToPath(t, server.URL, "/v1/logs", []byte(rawClaudeOTLPLogs), "application/json")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("ingest status = %d", response.StatusCode)
	}
	closeBody(t, response)

	usage := fetchSkillUsageInsight(t, server.URL)
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

// fetchSkillUsageInsight reads the skill usage insight through the live HTTP read API.
func fetchSkillUsageInsight(t *testing.T, baseURL string) skillUsageResponse {
	t.Helper()
	response, err := http.Get(baseURL + "/api/v1/insights/skill-usage")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("skill usage status = %d", response.StatusCode)
	}
	var body skillUsageResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}
