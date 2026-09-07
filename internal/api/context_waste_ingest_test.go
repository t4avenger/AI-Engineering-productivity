package api

import (
	"encoding/json"
	"testing"
)

func TestContextWasteInsightIngestEndToEnd(t *testing.T) {
	server, _ := newPersistentTestServer(t)
	postAcceptedOTLP(t, server.URL, "/v1/logs", rawClaudeContextWasteOTLP(t))

	waste := getInsightJSON[contextWasteResponse](t, server.URL+"/api/v1/insights/context-waste")
	if waste.Data.Totals.Sessions < 1 {
		t.Fatalf("expected at least one context-waste session, got %#v", waste.Data.Totals)
	}
	if waste.Data.Totals.TriggeredSessions < 1 {
		t.Fatalf("expected triggered session, got %#v", waste.Data.Totals)
	}

	assertNoRawIdentifiers(t,
		[]string{"tiq-canary@example.test", "tiq-canary-api-key"},
		marshalJSON(t, waste))
}

func rawClaudeContextWasteOTLP(t *testing.T) string {
	t.Helper()
	payload := map[string]any{
		"resourceLogs": []any{
			map[string]any{
				"resource": map[string]any{
					"attributes": []any{
						map[string]any{"key": "service.name", "value": map[string]any{"stringValue": "claude-code"}},
						map[string]any{"key": "service.version", "value": map[string]any{"stringValue": "2.1.263"}},
					},
				},
				"scopeLogs": []any{
					map[string]any{
						"logRecords": []any{
							map[string]any{"attributes": []any{
								map[string]any{"key": "event.name", "value": map[string]any{"stringValue": "api_request"}},
								map[string]any{"key": "event.timestamp", "value": map[string]any{"stringValue": "2026-09-06T19:00:00.000Z"}},
								map[string]any{"key": "event.sequence", "value": map[string]any{"intValue": "1"}},
								map[string]any{"key": "session.id", "value": map[string]any{"stringValue": "tiq-canary-session"}},
								map[string]any{"key": "request_id", "value": map[string]any{"stringValue": "synthetic-request-1"}},
								map[string]any{"key": "model", "value": map[string]any{"stringValue": "claude-opus-4-8"}},
								map[string]any{"key": "input_tokens", "value": map[string]any{"intValue": "100"}},
								map[string]any{"key": "cache_read_tokens", "value": map[string]any{"intValue": "75"}},
							}},
							map[string]any{"attributes": []any{
								map[string]any{"key": "event.name", "value": map[string]any{"stringValue": "api_request"}},
								map[string]any{"key": "event.timestamp", "value": map[string]any{"stringValue": "2026-09-06T19:00:01.000Z"}},
								map[string]any{"key": "event.sequence", "value": map[string]any{"intValue": "2"}},
								map[string]any{"key": "session.id", "value": map[string]any{"stringValue": "tiq-canary-session"}},
								map[string]any{"key": "request_id", "value": map[string]any{"stringValue": "synthetic-request-2"}},
								map[string]any{"key": "model", "value": map[string]any{"stringValue": "claude-opus-4-8"}},
								map[string]any{"key": "input_tokens", "value": map[string]any{"intValue": "200"}},
								map[string]any{"key": "cache_read_tokens", "value": map[string]any{"intValue": "150"}},
								map[string]any{"key": "user.email", "value": map[string]any{"stringValue": "tiq-canary@example.test"}},
								map[string]any{"key": "api_key", "value": map[string]any{"stringValue": "tiq-canary-api-key"}},
							}},
						},
					},
				},
			},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
