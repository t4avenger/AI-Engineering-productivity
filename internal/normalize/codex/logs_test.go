package codex

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNormalizeLogsObservedShape(t *testing.T) {
	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_cli_rs"}},{"key":"service.version","value":{"stringValue":"0.145.0"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.sse_event"}},{"key":"model","value":{"stringValue":"synthetic-model"}},{"key":"input_token_count","value":{"stringValue":"100"}}],"severityText":"INFO"}]}]}]}`)
	events, err := NormalizeLogs(data, time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC))
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	if !strings.HasPrefix(events[0].SessionID, "codex-log:") || events[0].Attributes["model"] != "synthetic-model" {
		t.Fatalf("event = %#v", events[0])
	}
}

// TestNormalizeLogsContentIDIncludesBodyAndTimestamps confirms records with no
// provider-native ID still get a stable, unique content ID (a plain dedup hash,
// not a privacy transform) so distinct records never collide.
func TestNormalizeLogsContentIDIncludesBodyAndTimestamps(t *testing.T) {
	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_cli_rs"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.tool_result"}}],"body":{"stringValue":"synthetic-first"},"observedTimeUnixNano":"1","timeUnixNano":"1"},{"attributes":[{"key":"event.name","value":{"stringValue":"codex.tool_result"}}],"body":{"stringValue":"synthetic-second"},"observedTimeUnixNano":"2","timeUnixNano":"2"}]}]}]}`)
	events, err := NormalizeLogs(data, time.Now())
	if err != nil || len(events) != 2 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	if events[0].EventID == events[1].EventID {
		t.Fatalf("event IDs must differ: %#v", events)
	}
}

func TestNormalizeLogsAcceptsExecService(t *testing.T) {
	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}},{"key":"service.version","value":{"stringValue":"0.145.0"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.sse_event"}},{"key":"model","value":{"stringValue":"synthetic-model"}}],"severityText":"INFO"}]}]}]}`)
	events, err := NormalizeLogs(data, time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC))
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	if events[0].Attributes["model"] != "synthetic-model" {
		t.Fatalf("event = %#v", events[0])
	}
}

func TestNormalizeLogsMapsCodexMCPToolResult(t *testing.T) {
	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_cli_rs"}},{"key":"service.version","value":{"stringValue":"0.153.4"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.tool_result"}},{"key":"mcp_server","value":{"stringValue":"synthetic-filesystem-server"}},{"key":"mcp_server_origin","value":{"stringValue":"config"}},{"key":"tool_name","value":{"stringValue":"read_file"}},{"key":"tool_namespace","value":{"stringValue":"mcp"}},{"key":"call_id","value":{"stringValue":"call_synthetic"}},{"key":"duration_ms","value":{"intValue":"42"}},{"key":"success","value":{"boolValue":true}}],"severityText":"INFO"}]}]}]}`)
	events, err := NormalizeLogs(data, time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC))
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	if events[0].Attributes["category"] != "MCP call" {
		t.Fatalf("category = %#v", events[0].Attributes["category"])
	}
	mcpCall, ok := events[0].ProviderExtensions["mcp_call"].(map[string]any)
	if !ok {
		t.Fatalf("mcp_call missing: %#v", events[0].ProviderExtensions)
	}
	if mcpCall["server_name"] != "synthetic-filesystem-server" || mcpCall["identity_state"] != "provider_reported" || mcpCall["tool_name"] != "read_file" || mcpCall["success"] != true {
		t.Fatalf("mcp_call = %#v", mcpCall)
	}
	logAttributes := events[0].ProviderExtensions["log_attributes"].(map[string]any)
	if _, promoted := logAttributes["mcp_server"]; promoted {
		t.Fatalf("mcp_server should be promoted to mcp_call, not duplicated in log attributes: %#v", logAttributes)
	}
}

func TestNormalizeLogsRejectsUnobservedService(t *testing.T) {
	_, err := NormalizeLogs([]byte(`{"resourceLogs":[{"resource":{"attributes":[]},"scopeLogs":[]}]}`), time.Now())
	if !errors.Is(err, ErrUnsupportedLogs) {
		t.Fatalf("error = %v", err)
	}
}
