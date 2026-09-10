package codex

import (
	"errors"
	"slices"
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

func TestNormalizeLogsMapsCodexToolResultSignal(t *testing.T) {
	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}},{"key":"service.version","value":{"stringValue":"0.153.4"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.tool_result"}},{"key":"conversation.id","value":{"stringValue":"tool-signal-session"}},{"key":"tool_name","value":{"stringValue":"exec_command"}},{"key":"tool_namespace","value":{"stringValue":"functions"}},{"key":"call_id","value":{"stringValue":"synthetic-call-success"}},{"key":"duration_ms","value":{"stringValue":"92"}},{"key":"success","value":{"stringValue":"true"}},{"key":"output_truncated","value":{"boolValue":false}},{"key":"tool_result_seq","value":{"stringValue":"1"}}],"severityText":"INFO"},{"attributes":[{"key":"event.name","value":{"stringValue":"codex.tool_result"}},{"key":"conversation.id","value":{"stringValue":"tool-signal-session"}},{"key":"tool_name","value":{"stringValue":"apply_patch"}},{"key":"tool_namespace","value":{"stringValue":"functions"}},{"key":"call_id","value":{"stringValue":"synthetic-call-failed"}},{"key":"duration_ms","value":{"stringValue":"87"}},{"key":"success","value":{"stringValue":"false"}}],"severityText":"INFO"}]}]}]}`)
	events, err := NormalizeLogs(data, time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC))
	if err != nil || len(events) != 2 {
		t.Fatalf("events = %#v, %v", events, err)
	}

	first := events[0]
	if first.Attributes["operation_id"] != "codex:tool-signal-session:tool:synthetic-call-success" || first.Attributes["category"] != "shell command" || first.Attributes["outcome"] != "success" || first.Attributes["duration_ms"] != int64(92) {
		t.Fatalf("first tool-call attributes = %#v", first.Attributes)
	}
	if unavailable := first.Attributes["unavailable_fields"].([]string); slices.Contains(unavailable, "tool_calls") {
		t.Fatalf("tool_calls must be available for tool_result: %#v", unavailable)
	}
	toolCall := first.ProviderExtensions["tool_call"].(map[string]any)
	if toolCall["tool_name"] != "exec_command" || toolCall["tool_namespace"] != "functions" || toolCall["provenance"] != "observed" {
		t.Fatalf("tool_call extension = %#v", toolCall)
	}
	if logAttributes := first.ProviderExtensions["log_attributes"].(map[string]any); logAttributes[codexEventNameKey] != codexToolResultEvent {
		t.Fatalf("tool-result log attributes should preserve source event evidence: %#v", logAttributes)
	}

	second := events[1]
	if second.Attributes["operation_id"] != "codex:tool-signal-session:tool:synthetic-call-failed" || second.Attributes["category"] != "filesystem write" || second.Attributes["outcome"] != "failed" {
		t.Fatalf("second tool-call attributes = %#v", second.Attributes)
	}
}

func TestNormalizeLogsKeepsToolCallsUnavailableForNonToolResults(t *testing.T) {
	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.sse_event"}},{"key":"model","value":{"stringValue":"synthetic-model"}}],"severityText":"INFO"}]}]}]}`)
	events, err := NormalizeLogs(data, time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC))
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	if unavailable := events[0].Attributes["unavailable_fields"].([]string); !slices.Contains(unavailable, "tool_calls") {
		t.Fatalf("tool_calls must remain unavailable for non-tool_result: %#v", unavailable)
	}
	if logAttributes := events[0].ProviderExtensions["log_attributes"].(map[string]any); logAttributes["event.name"] != "codex.sse_event" {
		t.Fatalf("non-tool log attributes should preserve event.name evidence: %#v", logAttributes)
	}
}

func TestNormalizeLogsUnknownToolCategoryStaysUnknown(t *testing.T) {
	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.tool_result"}},{"key":"tool_name","value":{"stringValue":"future_tool"}},{"key":"success","value":{"boolValue":true}}],"severityText":"INFO"}]}]}]}`)
	events, err := NormalizeLogs(data, time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC))
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	if events[0].Attributes["category"] != "unknown" {
		t.Fatalf("category = %#v", events[0].Attributes["category"])
	}
}

func TestNormalizeLogsRejectsUnobservedService(t *testing.T) {
	_, err := NormalizeLogs([]byte(`{"resourceLogs":[{"resource":{"attributes":[]},"scopeLogs":[]}]}`), time.Now())
	if !errors.Is(err, ErrUnsupportedLogs) {
		t.Fatalf("error = %v", err)
	}
}
