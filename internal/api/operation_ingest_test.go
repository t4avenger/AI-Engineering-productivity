package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wayne/telemetryiq/internal/storage"
	"github.com/wayne/telemetryiq/internal/storage/sqlite"
)

const rawClaudeToolResultOTLPLogs = `{"resourceLogs":[{"resource":{"attributes":[
  {"key":"service.name","value":{"stringValue":"claude-code"}},
  {"key":"service.version","value":{"stringValue":"2.1.263"}}]},
 "scopeLogs":[{"logRecords":[
   {"attributes":[
     {"key":"event.name","value":{"stringValue":"tool_result"}},
     {"key":"event.timestamp","value":{"stringValue":"2026-09-06T14:50:01Z"}},
     {"key":"event.sequence","value":{"intValue":"11"}},
     {"key":"session.id","value":{"stringValue":"synthetic-claude-tool-session"}},
     {"key":"tool_name","value":{"stringValue":"Bash"}},
     {"key":"tool_use_id","value":{"stringValue":"toolu_synthetic_bash"}},
     {"key":"duration_ms","value":{"intValue":"1234"}},
     {"key":"success","value":{"stringValue":"true"}},
     {"key":"tool_input","value":{"stringValue":"tiq-canary-claude-tool-input"}},
     {"key":"tool_result","value":{"stringValue":"tiq-canary-claude-tool-result"}},
     {"key":"user.email","value":{"stringValue":"claude-tool@example.test"}}],
    "body":{"stringValue":"tiq-canary-claude-tool-body"}}]}]}]}`

func TestOperationStatsInsightIngestEndToEnd(t *testing.T) {
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	server := httptest.NewServer(NewPersistentHandler(slog.Default(), repository))
	t.Cleanup(server.Close)

	for _, payload := range []string{rawCodexToolResultOTLPLogs, rawClaudeToolResultOTLPLogs} {
		response := postOTLPToPath(t, server.URL, "/v1/logs", []byte(payload), "application/json")
		if response.StatusCode != http.StatusAccepted {
			t.Fatalf("ingest status = %d", response.StatusCode)
		}
		closeBody(t, response)
	}

	stored, err := repository.ListOperations(context.Background(), storage.OperationFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 {
		t.Fatalf("stored operations = %d, want 2: %#v", len(stored), stored)
	}

	stats := getInsightJSON[operationStatsResponse](t, server.URL+"/api/v1/insights/operations")
	if stats.Data.Totals.TotalOperations != 2 || stats.Data.Totals.DurationObservedCount != 2 {
		t.Fatalf("operation totals = %#v", stats.Data.Totals)
	}
	if stats.Data.Totals.AverageDurationMs == nil || *stats.Data.Totals.AverageDurationMs != 663 {
		t.Fatalf("average duration = %#v", stats.Data.Totals.AverageDurationMs)
	}
	serialized := marshalJSON(t, stats)
	canaries := []string{"tiq-canary-tool-resource-token", "tool-host.example.test", "tool-account-123", "tiq-canary-claude-tool-input", "tiq-canary-claude-tool-result", "tiq-canary-claude-tool-body", "claude-tool@example.test"}
	assertNoRawIdentifiers(t, canaries, serialized)
	assertNoRawIdentifiers(t, canaries, marshalJSON(t, stored))
}
