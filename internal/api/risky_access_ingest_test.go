package api

import (
	"strings"
	"testing"
)

func TestRiskyAccessInsightIngestEndToEnd(t *testing.T) {
	server, _ := newPersistentTestServer(t)
	postAcceptedOTLP(t, server.URL, "/v1/logs", rawClaudeRiskyAccessOTLP(t))

	risky := getInsightJSON[riskyAccessResponse](t, server.URL+"/api/v1/insights/risky-access")
	if string(risky.Data.Outcome) != "violation" {
		t.Fatalf("expected violation outcome, got %#v", risky.Data)
	}
	methods := map[string]bool{}
	for _, finding := range risky.Data.Findings {
		methods[string(finding.AccessMethod)] = true
	}
	if !methods["filesystem_read"] || !methods["shell_command"] {
		t.Fatalf("expected both filesystem_read and shell_command findings, got %#v", risky.Data.Findings)
	}

	// Issue #88 removed ingest-time hiding: the raw path and command survive to
	// the persisted finding so an operator sees exactly what tripped the policy.
	serialized := string(marshalJSON(t, risky))
	for _, evidence := range []string{"/home/dev/secret-app/.env", "cat /home/dev/secret-app/.env --password s3cr3t-value"} {
		if !strings.Contains(serialized, evidence) {
			t.Fatalf("raw evidence %q must survive to the finding, got %s", evidence, serialized)
		}
	}
}

func TestCodexRiskyAccessClassifiesRetainedRawEvidence(t *testing.T) {
	server, _ := newPersistentTestServer(t)
	payload := `{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.sandbox_outcome"}},{"key":"conversation.id","value":{"stringValue":"codex-risk-session"}},{"key":"path","value":{"stringValue":".env"}},{"key":"command","value":{"stringValue":"cat .env"}}]}]}]}]}`
	postAcceptedOTLP(t, server.URL, "/v1/logs", payload)

	risky := getInsightJSON[riskyAccessResponse](t, server.URL+"/api/v1/insights/risky-access")
	if string(risky.Data.Outcome) != "violation" || len(risky.Data.Findings) != 2 {
		t.Fatalf("Codex risky access = %#v", risky.Data)
	}
	serialized := string(marshalJSON(t, risky))
	for _, evidence := range []string{".env", "cat .env", `"class":"dotenv"`, `"class":"credential_access"`, `"boundary":"project"`} {
		if !strings.Contains(serialized, evidence) {
			t.Fatalf("Codex classification missing %q: %s", evidence, serialized)
		}
	}
}

func rawClaudeRiskyAccessOTLP(t *testing.T) string {
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
								map[string]any{"key": "event.name", "value": map[string]any{"stringValue": "api_request"}},
								map[string]any{"key": "event.timestamp", "value": map[string]any{"stringValue": "2026-09-06T19:00:00.000Z"}},
								map[string]any{"key": "event.sequence", "value": map[string]any{"intValue": "1"}},
								map[string]any{"key": "session.id", "value": map[string]any{"stringValue": "tiq-canary-session"}},
								map[string]any{"key": "model", "value": map[string]any{"stringValue": "claude-opus-4-8"}},
								map[string]any{"key": "file_path", "value": map[string]any{"stringValue": "/home/dev/secret-app/.env"}},
								map[string]any{"key": "command", "value": map[string]any{"stringValue": "cat /home/dev/secret-app/.env --password s3cr3t-value"}},
							}},
						},
					},
				},
			},
		},
	}))
}
