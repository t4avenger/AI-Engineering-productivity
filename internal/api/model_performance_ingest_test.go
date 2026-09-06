package api

import (
	"testing"
)

// Sanitised Claude api_request + api_error outcome-contract payloads for the
// live ingest→read DoD gate (synthetic identifiers only).
const rawClaudeOutcomeSuccessOTLP = `{"resourceLogs":[{"resource":{"attributes":[
  {"key":"service.name","value":{"stringValue":"claude-code"}},
  {"key":"service.version","value":{"stringValue":"2.1.263"}}]},
 "scopeLogs":[{"logRecords":[{"attributes":[
  {"key":"event.name","value":{"stringValue":"api_request"}},
  {"key":"event.timestamp","value":{"stringValue":"2026-09-06T18:05:00.100Z"}},
  {"key":"event.sequence","value":{"intValue":"3"}},
  {"key":"session.id","value":{"stringValue":"tiq-canary-outcome-session"}},
  {"key":"model","value":{"stringValue":"claude-haiku-4-5-20251001"}},
  {"key":"input_tokens","value":{"intValue":"12"}},
  {"key":"output_tokens","value":{"intValue":"4"}},
  {"key":"duration_ms","value":{"intValue":"842"}},
  {"key":"user.email","value":{"stringValue":"tiq-canary@example.test"}}]}]}]}]}`

const rawClaudeOutcomeFailedOTLP = `{"resourceLogs":[{"resource":{"attributes":[
  {"key":"service.name","value":{"stringValue":"claude-code"}},
  {"key":"service.version","value":{"stringValue":"2.1.263"}}]},
 "scopeLogs":[{"logRecords":[{"attributes":[
  {"key":"event.name","value":{"stringValue":"api_error"}},
  {"key":"event.timestamp","value":{"stringValue":"2026-09-06T17:57:57.860Z"}},
  {"key":"event.sequence","value":{"intValue":"1"}},
  {"key":"session.id","value":{"stringValue":"tiq-canary-outcome-session"}},
  {"key":"model","value":{"stringValue":"claude-opus-4-8"}},
  {"key":"status_code","value":{"intValue":"429"}},
  {"key":"error","value":{"stringValue":"rate_limit"}},
  {"key":"duration_ms","value":{"intValue":"62462"}},
  {"key":"attempt","value":{"intValue":"1"}}]}]}]}]}`

const rawCodexOutcomeOTLP = `{"resourceLogs":[{"resource":{"attributes":[
  {"key":"service.name","value":{"stringValue":"codex_exec"}},
  {"key":"service.version","value":{"stringValue":"0.153.4"}}]},
 "scopeLogs":[{"logRecords":[
  {"attributes":[
    {"key":"event.name","value":{"stringValue":"codex.tool_result"}},
    {"key":"tool_name","value":{"stringValue":"exec_command"}},
    {"key":"success","value":{"stringValue":"true"}},
    {"key":"model","value":{"stringValue":"gpt-6-astra"}},
    {"key":"duration_ms","value":{"stringValue":"92"}},
    {"key":"call_id","value":{"stringValue":"synthetic-call"}}],
   "severityText":"INFO"},
  {"attributes":[
    {"key":"event.name","value":{"stringValue":"codex.api_request"}},
    {"key":"success","value":{"boolValue":false}},
    {"key":"model","value":{"stringValue":"gpt-6-astra"}},
    {"key":"attempt","value":{"stringValue":"2"}},
    {"key":"duration_ms","value":{"stringValue":"268"}},
    {"key":"http.status_code","value":{"stringValue":"401"}},
    {"key":"error_code","value":{"stringValue":"http_401"}}],
   "severityText":"INFO"}]}]}]}`

func TestModelPerformanceInsightIngestEndToEnd(t *testing.T) {
	server, _ := newPersistentTestServer(t)
	postAcceptedOTLP(t, server.URL, "/v1/logs", rawClaudeOutcomeSuccessOTLP)
	postAcceptedOTLP(t, server.URL, "/v1/logs", rawClaudeOutcomeFailedOTLP)
	postAcceptedOTLP(t, server.URL, "/v1/logs", rawCodexOutcomeOTLP)

	scorecard := getInsightJSON[modelPerformanceResponse](t, server.URL+"/api/v1/insights/model-performance")
	if len(scorecard.Data.Models) < 2 {
		t.Fatalf("expected models from Claude and Codex contracts, got %#v", scorecard.Data.Models)
	}
	if scorecard.Data.RankingAvailable {
		t.Fatal("ranking must stay suppressed below min sample size")
	}
	foundAstra := false
	for _, row := range scorecard.Data.Models {
		if row.Model == "gpt-6-astra" {
			foundAstra = true
			if row.SampleSize < 2 {
				t.Fatalf("gpt-6-astra sample_size = %d", row.SampleSize)
			}
		}
	}
	if !foundAstra {
		t.Fatalf("missing gpt-6-astra row: %#v", scorecard.Data.Models)
	}
	assertNoRawIdentifiers(t,
		[]string{"tiq-canary-outcome-session", "tiq-canary@example.test"},
		marshalJSON(t, scorecard))
}
