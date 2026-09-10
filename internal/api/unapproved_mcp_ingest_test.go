package api

import (
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/wayne/telemetryiq/internal/storage/sqlite"
)

// TestUnapprovedMCPInsightIngestEndToEnd exercises the live-data DoD: a real
// wire-shaped Claude OTLP payload is POSTed to the running /v1/logs receiver and
// the HTTP read API is asserted to serve the expected policy decision, with a
// configured allowlist threaded end-to-end and operator/machine identifiers
// absent from the response.
func TestUnapprovedMCPInsightIngestEndToEnd(t *testing.T) {
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })

	// A configured allowlist that does not include the connected server, threaded
	// from InsightThresholds through the live read path.
	thresholds := DefaultInsightThresholds()
	thresholds.MCPAllowlist = []string{"approved-filesystem-server"}
	server := httptest.NewServer(newHandler(slog.Default(), nil, repository, repository, thresholds))
	t.Cleanup(server.Close)

	postAcceptedOTLP(t, server.URL, "/v1/logs", rawClaudeUnapprovedMCPOTLP(t))

	report := getInsightJSON[unapprovedMCPResponse](t, server.URL+"/api/v1/insights/unapproved-mcp")
	if string(report.Data.Outcome) != "violation" {
		t.Fatalf("expected violation outcome, got %#v", report.Data)
	}
	if len(report.Data.Findings) != 1 || report.Data.Findings[0].ServerName != "unapproved-rogue-server" {
		t.Fatalf("expected one unapproved-rogue-server finding, got %#v", report.Data.Findings)
	}

	// Operator/machine identifiers from the raw payload must never survive to the
	// persisted read output.
	assertNoRawIdentifiers(t,
		[]string{"synthetic-user-hash", "synthetic-org", "synthetic-prompt"},
		marshalJSON(t, report))
}

func rawClaudeUnapprovedMCPOTLP(t *testing.T) string {
	t.Helper()
	return claudeOTLPLogPayload(t, []any{
		otlpStringAttr("event.name", "mcp_server_connection"),
		otlpStringAttr("event.timestamp", "2026-09-06T19:00:00.000Z"),
		map[string]any{"key": "event.sequence", "value": map[string]any{"intValue": "1"}},
		otlpStringAttr("session.id", "tiq-canary-session"),
		otlpStringAttr("status", "connected"),
		otlpStringAttr("transport_type", "stdio"),
		otlpStringAttr("server_scope", "user"),
		map[string]any{"key": "is_plugin", "value": map[string]any{"boolValue": false}},
		otlpStringAttr("server_name", "unapproved-rogue-server"),
		otlpStringAttr("user.id", "synthetic-user-hash"),
		otlpStringAttr("organization.id", "synthetic-org"),
		otlpStringAttr("prompt.id", "synthetic-prompt"),
	})
}
