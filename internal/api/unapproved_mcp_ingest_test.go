package api

import (
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/wayne/telemetryiq/internal/privacy"
	"github.com/wayne/telemetryiq/internal/storage/sqlite"
)

// TestUnapprovedMCPInsightIngestEndToEnd exercises the live-data DoD: a real
// wire-shaped Claude OTLP payload is POSTed to the running /v1/logs receiver and
// the HTTP read API is asserted to serve the expected policy decision, with a
// configured allowlist threaded end-to-end and operator/machine identifiers
// absent from the response.
func TestUnapprovedMCPInsightIngestEndToEnd(t *testing.T) {
	sanitizer, err := privacy.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repository, err := sqlite.Open(":memory:", sanitizer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })

	// A configured allowlist that does not include the connected server, threaded
	// from InsightThresholds through the live read path.
	thresholds := DefaultInsightThresholds()
	thresholds.MCPAllowlist = []string{"approved-filesystem-server"}
	server := httptest.NewServer(newHandler(slog.Default(), nil, repository, sanitizer, repository, thresholds))
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
	return string(marshalJSON(t, map[string]any{
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
								map[string]any{"key": "event.name", "value": map[string]any{"stringValue": "mcp_server_connection"}},
								map[string]any{"key": "event.timestamp", "value": map[string]any{"stringValue": "2026-09-06T19:00:00.000Z"}},
								map[string]any{"key": "event.sequence", "value": map[string]any{"intValue": "1"}},
								map[string]any{"key": "session.id", "value": map[string]any{"stringValue": "tiq-canary-session"}},
								map[string]any{"key": "status", "value": map[string]any{"stringValue": "connected"}},
								map[string]any{"key": "transport_type", "value": map[string]any{"stringValue": "stdio"}},
								map[string]any{"key": "server_scope", "value": map[string]any{"stringValue": "user"}},
								map[string]any{"key": "is_plugin", "value": map[string]any{"boolValue": false}},
								map[string]any{"key": "server_name", "value": map[string]any{"stringValue": "unapproved-rogue-server"}},
								map[string]any{"key": "user.id", "value": map[string]any{"stringValue": "synthetic-user-hash"}},
								map[string]any{"key": "organization.id", "value": map[string]any{"stringValue": "synthetic-org"}},
								map[string]any{"key": "prompt.id", "value": map[string]any{"stringValue": "synthetic-prompt"}},
							}},
						},
					},
				},
			},
		},
	}))
}
