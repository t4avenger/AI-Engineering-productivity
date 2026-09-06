package api

import "testing"

// rawClaudeContextWasteOTLP is a sanitised Claude Code OTLP log payload carrying
// two api_request events with token and cache-read counts (synthetic identifiers).
const rawClaudeContextWasteOTLP = `{"resourceLogs":[{"resource":{"attributes":[
  {"key":"service.name","value":{"stringValue":"claude-code"}},
  {"key":"service.version","value":{"stringValue":"2.1.263"}}]},
 "scopeLogs":[{"logRecords":[
   {"attributes":[
     {"key":"event.name","value":{"stringValue":"api_request"}},
     {"key":"event.timestamp","value":{"stringValue":"2026-09-06T19:00:00.000Z"}},
     {"key":"event.sequence","value":{"intValue":"1"}},
     {"key":"session.id","value":{"stringValue":"tiq-canary-session"}},
     {"key":"request_id","value":{"stringValue":"synthetic-request-1"}},
     {"key":"model","value":{"stringValue":"claude-opus-4-8"}},
     {"key":"input_tokens","value":{"intValue":"100"}},
     {"key":"cache_read_tokens","value":{"intValue":"75"}}]},
   {"attributes":[
     {"key":"event.name","value":{"stringValue":"api_request"}},
     {"key":"event.timestamp","value":{"stringValue":"2026-09-06T19:00:01.000Z"}},
     {"key":"event.sequence","value":{"intValue":"2"}},
     {"key":"session.id","value":{"stringValue":"tiq-canary-session"}},
     {"key":"request_id","value":{"stringValue":"synthetic-request-2"}},
     {"key":"model","value":{"stringValue":"claude-opus-4-8"}},
     {"key":"input_tokens","value":{"intValue":"200"}},
     {"key":"cache_read_tokens","value":{"intValue":"150"}},
     {"key":"user.email","value":{"stringValue":"tiq-canary@example.test"}},
     {"key":"api_key","value":{"stringValue":"tiq-canary-api-key"}}]}]}]}]}`

func TestContextWasteInsightIngestEndToEnd(t *testing.T) {
	server, _ := newPersistentTestServer(t)
	postAcceptedOTLP(t, server.URL, "/v1/logs", rawClaudeContextWasteOTLP)

	waste := getInsightJSON[contextWasteResponse](t, server.URL+"/api/v1/insights/context-waste")
	if waste.Data.Totals.Sessions < 1 {
		t.Fatalf("expected at least one context-waste session, got %#v", waste.Data.Totals)
	}
	if waste.Data.Totals.TriggeredSessions < 1 {
		t.Fatalf("expected triggered session, got %#v", waste.Data.Totals)
	}

	assertNoRawIdentifiers(t,
		[]string{"tiq-canary-session", "tiq-canary@example.test", "tiq-canary-api-key"},
		marshalJSON(t, waste))
}
