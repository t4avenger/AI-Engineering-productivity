package codex

import (
	"errors"
	"testing"
	"time"
)

func TestNormalizeLogsObservedShape(t *testing.T) {
	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_cli_rs"}},{"key":"service.version","value":{"stringValue":"0.145.0"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.sse_event"}},{"key":"model","value":{"stringValue":"synthetic-model"}},{"key":"input_token_count","value":{"stringValue":"100"}}],"severityText":"INFO"}]}]}]}`)
	events, err := NormalizeLogs(data, time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC), func([]byte) string { return "local-fingerprint" })
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	if events[0].SessionID != "codex-log:local-fingerprint" || events[0].Attributes["model"] != "synthetic-model" {
		t.Fatalf("event = %#v", events[0])
	}
}

func TestNormalizeLogsFingerprintIncludesBodyAndTimestamps(t *testing.T) {
	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_cli_rs"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.tool_result"}}],"body":{"stringValue":"synthetic-first"},"observedTimeUnixNano":"1","timeUnixNano":"1"},{"attributes":[{"key":"event.name","value":{"stringValue":"codex.tool_result"}}],"body":{"stringValue":"synthetic-second"},"observedTimeUnixNano":"2","timeUnixNano":"2"}]}]}]}`)
	events, err := NormalizeLogs(data, time.Now(), func(value []byte) string { return string(value) })
	if err != nil || len(events) != 2 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	if events[0].EventID == events[1].EventID {
		t.Fatalf("event IDs must differ: %#v", events)
	}
}

func TestNormalizeLogsAcceptsExecService(t *testing.T) {
	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}},{"key":"service.version","value":{"stringValue":"0.145.0"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.sse_event"}},{"key":"model","value":{"stringValue":"synthetic-model"}}],"severityText":"INFO"}]}]}]}`)
	events, err := NormalizeLogs(data, time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC), func([]byte) string { return "exec-fingerprint" })
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	if events[0].Attributes["model"] != "synthetic-model" {
		t.Fatalf("event = %#v", events[0])
	}
}

func TestNormalizeLogsMapsCodexMCPToolResult(t *testing.T) {
	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_cli_rs"}},{"key":"service.version","value":{"stringValue":"0.153.4"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.tool_result"}},{"key":"mcp_server","value":{"stringValue":"synthetic-filesystem-server"}},{"key":"mcp_server_origin","value":{"stringValue":"config"}},{"key":"tool_name","value":{"stringValue":"read_file"}},{"key":"tool_namespace","value":{"stringValue":"mcp"}},{"key":"call_id","value":{"stringValue":"call_synthetic"}},{"key":"duration_ms","value":{"intValue":"42"}},{"key":"success","value":{"boolValue":true}}],"severityText":"INFO"}]}]}]}`)
	events, err := NormalizeLogs(data, time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC), func(value []byte) string { return "fp-" + string(value) })
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
	if mcpCall["server_fingerprint"] != "codex:fp-synthetic-filesystem-server" || mcpCall["server_name"] != "synthetic-filesystem-server" || mcpCall["tool_name"] != "read_file" || mcpCall["success"] != true {
		t.Fatalf("mcp_call = %#v", mcpCall)
	}
	logAttributes := events[0].ProviderExtensions["log_attributes"].(map[string]any)
	if _, leaked := logAttributes["mcp_server"]; leaked {
		t.Fatalf("raw mcp_server leaked in log attributes: %#v", logAttributes)
	}
}

func TestNormalizeLogsRejectsUnobservedService(t *testing.T) {
	_, err := NormalizeLogs([]byte(`{"resourceLogs":[{"resource":{"attributes":[]},"scopeLogs":[]}]}`), time.Now(), func([]byte) string { return "x" })
	if !errors.Is(err, ErrUnsupportedLogs) {
		t.Fatalf("error = %v", err)
	}
}
