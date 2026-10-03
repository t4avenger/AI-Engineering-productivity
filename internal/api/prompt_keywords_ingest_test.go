package api

import (
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/wayne/telemetryiq/internal/config"
	"github.com/wayne/telemetryiq/internal/governance"
	"github.com/wayne/telemetryiq/internal/storage/sqlite"
)

// TestPromptKeywordsScanTranscriptPromptsThroughReadAPI is the #259 daemon
// ingest→read gate. It POSTs the paired 2.1.283 observed captures (OTLP logs and
// the session JSONL transcript of the same run) through the real routes and
// proves the prompt retained by both surfaces is reported once, joined on the
// provider message uuid, with the transcript copy kept as corroborating
// provenance. A transcript-only ingest proves the JSONL prompt alone is scanned
// and reported as observed rather than unavailable.
func TestPromptKeywordsScanTranscriptPromptsThroughReadAPI(t *testing.T) {
	transcript := string(transcriptFixturePayloadNDJSON(t, "claude-code-2.1.283-transcript-conversation.json"))
	tests := []struct {
		name          string
		withOTLP      bool
		wantType      string
		corroborating int
	}{
		{name: "otlp and transcript join on message uuid", withOTLP: true, wantType: "user_prompt", corroborating: 1},
		{name: "transcript only prompt is scanned", wantType: "user_message"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, repository := promptKeywordsTestServer(t)
			if test.withOTLP {
				postAcceptedOTLP(t, server.URL, "/v1/logs", string(metricsFixturePayloadBytes(t, "claude-code-2.1.283-log-conversation-otlp.json")))
			}
			postAcceptedTranscript(t, server.URL, transcript)

			report := getInsightJSON[promptKeywordsResponse](t, server.URL+"/api/v1/insights/prompt-keywords").Data
			if report.Outcome != governance.OutcomeViolation || report.Visibility != "observed" || len(report.Findings) != 1 {
				t.Fatalf("prompt keyword report = %#v", report)
			}
			finding := report.Findings[0]
			if finding.SessionID != "claude-code:tiq-corr-210" || len(finding.CorroboratingEventIDs) != test.corroborating {
				t.Fatalf("finding = %#v", finding)
			}
			assertStoredEventType(t, repository, finding.SessionID, finding.SourceEventID, test.wantType)
			for _, id := range finding.CorroboratingEventIDs {
				assertStoredEventType(t, repository, finding.SessionID, id, "user_message")
			}
		})
	}
}

func assertStoredEventType(t *testing.T, repository *sqlite.Repository, sessionID, eventID, want string) {
	t.Helper()
	event, ok, err := repository.GetEvent(t.Context(), sessionID, eventID)
	if err != nil || !ok || event.EventType != want {
		t.Fatalf("stored event %s = %q (found %t, %v), want %s", eventID, event.EventType, ok, err, want)
	}
}

func promptKeywordsTestServer(t *testing.T) (*httptest.Server, *sqlite.Repository) {
	t.Helper()
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	thresholds := DefaultInsightThresholds()
	thresholds.PromptKeywords = []config.PromptKeyword{{ID: "correlation", Label: "Correlation", Group: "custom", Enabled: true, Kind: "literal", Value: "correlation prompt"}}
	server := httptest.NewServer(newHandler(slog.Default(), nil, repository, repository, thresholds))
	t.Cleanup(server.Close)
	return server, repository
}
