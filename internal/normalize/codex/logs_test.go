package codex

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
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

func TestNormalizeLogsUsesObservedThreadIdentityWhenConversationIsAbsent(t *testing.T) {
	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.sse_event"}},{"key":"thread.id","value":{"stringValue":"thread-only-session"}},{"key":"model","value":{"stringValue":"synthetic-model"}}]}]}]}]}`)
	events, err := NormalizeLogs(data, time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC))
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	if events[0].SessionID != "codex:thread-only-session" {
		t.Fatalf("session id = %q", events[0].SessionID)
	}
}

func TestNormalizeLogsMapsCachedAndReasoningTokens(t *testing.T) {
	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}},{"key":"service.version","value":{"stringValue":"0.153.4"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.sse_event"}},{"key":"model","value":{"stringValue":"gpt-5-codex-synthetic"}},{"key":"input_token_count","value":{"stringValue":"1200"}},{"key":"cached_token_count","value":{"stringValue":"300"}},{"key":"output_token_count","value":{"stringValue":"144"}},{"key":"reasoning_token_count","value":{"stringValue":"55"}}],"severityText":"INFO"}]}]}]}`)
	events, err := NormalizeLogs(data, time.Date(2026, 9, 10, 20, 9, 20, 0, time.UTC))
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	attributes := events[0].Attributes
	for key, want := range map[string]int64{
		"input_token_count":        1200,
		"cached_input_token_count": 300,
		"output_token_count":       144,
		"reasoning_token_count":    55,
	} {
		if attributes[key] != want {
			t.Fatalf("%s = %#v, want %d", key, attributes[key], want)
		}
	}
	unavailable := attributes["unavailable_fields"].([]string)
	if slices.Contains(unavailable, "cache_usage") || slices.Contains(unavailable, "reasoning_tokens") {
		t.Fatalf("token availability fields should be removed: %#v", unavailable)
	}
}

func TestNormalizeLogsMapsCodexSessionLifecycle(t *testing.T) {
	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}},{"key":"service.version","value":{"stringValue":"0.153.4"}},{"key":"host.name","value":{"stringValue":"lifecycle-host.example.test"}},{"key":"user.account_id","value":{"stringValue":"lifecycle-account-123"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.conversation_starts"}},{"key":"conversation.id","value":{"stringValue":"lifecycle-session"}},{"key":"model","value":{"stringValue":"gpt-6-astra"}},{"key":"approval_policy","value":{"stringValue":"on-request"}},{"key":"sandbox_policy","value":{"stringValue":"workspace-write"}},{"key":"auth_mode","value":{"stringValue":"api-key"}},{"key":"terminal.type","value":{"stringValue":"pty"}},{"key":"reasoning_summary","value":{"stringValue":"tiq-canary-lifecycle-reasoning"}},{"key":"slug","value":{"stringValue":"tiq-canary-lifecycle-slug"}},{"key":"user.email","value":{"stringValue":"lifecycle-user@example.test"}}],"body":{"stringValue":"tiq-canary-lifecycle-body"},"severityText":"INFO"},{"attributes":[{"key":"event.name","value":{"stringValue":"codex.startup_phase"}},{"key":"conversation.id","value":{"stringValue":"lifecycle-session"}},{"key":"startup.phase","value":{"stringValue":"init"}},{"key":"startup.status","value":{"stringValue":"ok"}},{"key":"duration_ms","value":{"stringValue":"17"}}],"severityText":"INFO"},{"attributes":[{"key":"event.name","value":{"stringValue":"codex.websocket_connect"}},{"key":"conversation.id","value":{"stringValue":"lifecycle-session"}},{"key":"success","value":{"boolValue":true}},{"key":"duration_ms","value":{"stringValue":"23"}},{"key":"endpoint","value":{"stringValue":"wss://tiq-canary-lifecycle-endpoint.example.test/ws"}}],"severityText":"INFO","timeUnixNano":"1788717763000000002"}]}]}]}`)
	events, err := NormalizeLogs(data, time.Date(2026, 9, 10, 20, 9, 20, 0, time.UTC))
	if err != nil || len(events) != 3 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	assertCodexSessionStartEvent(t, events[0])
	assertCodexStartupPhaseEvent(t, events[1])
	assertCodexWebsocketLifecycleEvent(t, events[2])

	encoded, _ := json.Marshal(events)
	assertNoStringCanaries(t, string(encoded), []string{"tiq-canary-lifecycle-slug", "lifecycle-user@example.test", "tiq-canary-lifecycle-body", "lifecycle-host.example.test", "lifecycle-account-123", "tiq-canary-lifecycle-reasoning", "tiq-canary-lifecycle-endpoint.example.test"})
}

func assertCodexSessionStartEvent(t *testing.T, event canonical.Event) {
	t.Helper()
	if event.EventType != "session.active" || event.SessionID != "codex:lifecycle-session" {
		t.Fatalf("start event = %#v", event)
	}
	if event.Attributes["lifecycle_kind"] != "session_start" || event.Attributes["entrypoint"] != "codex exec" {
		t.Fatalf("start lifecycle attributes = %#v", event.Attributes)
	}
	if unavailable := event.Attributes["unavailable_fields"].([]string); slices.Contains(unavailable, "session_lifecycle") {
		t.Fatalf("session_lifecycle must be available for lifecycle event: %#v", unavailable)
	}
	startLifecycle := event.ProviderExtensions["session_lifecycle"].(map[string]any)
	if startLifecycle["source_event"] != codexConversationStarts || startLifecycle["approval_policy"] != "on-request" || startLifecycle["sandbox_policy"] != "workspace-write" || startLifecycle["provenance"] != "observed" {
		t.Fatalf("start lifecycle extension = %#v", startLifecycle)
	}
	if logAttributes := event.ProviderExtensions["log_attributes"].(map[string]any); logAttributes[codexEventNameKey] != codexConversationStarts {
		t.Fatalf("log attributes should retain provider event name: %#v", logAttributes)
	}
}

func assertCodexStartupPhaseEvent(t *testing.T, event canonical.Event) {
	t.Helper()
	if event.EventType != codexStartupPhaseEvent || event.Attributes["lifecycle_kind"] != "startup_phase" || event.Attributes["lifecycle_phase"] != "init" || event.Attributes["lifecycle_status"] != "ok" || event.Attributes["duration_ms"] != int64(17) {
		t.Fatalf("startup lifecycle attributes = %#v", event.Attributes)
	}
}

func assertCodexWebsocketLifecycleEvent(t *testing.T, event canonical.Event) {
	t.Helper()
	if event.EventType != codexWebsocketConnect || event.Attributes["lifecycle_kind"] != "websocket_connect" || event.Attributes["lifecycle_status"] != "success" || event.Attributes["duration_ms"] != int64(23) {
		t.Fatalf("websocket lifecycle attributes = %#v", event.Attributes)
	}
}

func TestNormalizeLogsDoesNotFabricateMalformedTokenCounts(t *testing.T) {
	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.sse_event"}},{"key":"cached_token_count","value":{"stringValue":"not-a-number"}},{"key":"reasoning_token_count","value":{"doubleValue":1.5}}],"severityText":"INFO"}]}]}]}`)
	events, err := NormalizeLogs(data, time.Date(2026, 9, 10, 20, 9, 20, 0, time.UTC))
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	if _, ok := events[0].Attributes["cached_input_token_count"]; ok {
		t.Fatalf("malformed cached token count must stay absent: %#v", events[0].Attributes)
	}
	if _, ok := events[0].Attributes["reasoning_token_count"]; ok {
		t.Fatalf("malformed reasoning token count must stay absent: %#v", events[0].Attributes)
	}
	unavailable := events[0].Attributes["unavailable_fields"].([]string)
	if !slices.Contains(unavailable, "cache_usage") || !slices.Contains(unavailable, "reasoning_tokens") {
		t.Fatalf("unavailable fields should remain when counts are absent: %#v", unavailable)
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

func TestNormalizeLogsRetainsToolEvidenceAndFindsProviderNeutralPRLinks(t *testing.T) {
	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.tool_result"}},{"key":"conversation.id","value":{"stringValue":"pr-link-session"}},{"key":"arguments","value":{"stringValue":"gh pr view https://github.com/example/repository/pull/184"}},{"key":"output","value":{"stringValue":"https://gitlab.example.test/group/project/-/merge_requests/12"}}]}]}]}]}`)
	events, err := NormalizeLogs(data, time.Date(2026, 9, 19, 20, 0, 0, 0, time.UTC))
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	event := events[0]
	if got := event.ProviderExtensions["log_attributes"].(map[string]any)["arguments"]; got != "gh pr view https://github.com/example/repository/pull/184" {
		t.Fatalf("arguments = %#v", got)
	}
	if got := event.ProviderExtensions["log_attributes"].(map[string]any)["output"]; got != "https://gitlab.example.test/group/project/-/merge_requests/12" {
		t.Fatalf("output = %#v", got)
	}
	if got := event.Attributes["pr_link_candidates"]; !slices.Equal(got.([]string), []string{"https://github.com/example/repository/pull/184", "https://gitlab.example.test/group/project/-/merge_requests/12"}) {
		t.Fatalf("pr candidates = %#v", got)
	}
}

func TestPRLinkURLsAcceptsSupportedHostsAndRejectsNonPRURLs(t *testing.T) {
	got := normalize.PRLinkURLs("https://bitbucket.org/workspace/repo/pull-requests/5 https://dev.azure.com/org/project/_git/repo/pullrequest/9 https://example.test/docs/pull/10 https://example.test/issues/10")
	want := []string{"https://bitbucket.org/workspace/repo/pull-requests/5", "https://dev.azure.com/org/project/_git/repo/pullrequest/9", "https://example.test/docs/pull/10"}
	if !slices.Equal(got, want) {
		t.Fatalf("urls = %#v, want %#v", got, want)
	}
}

func TestNormalizeLogsMapsCodexToolDecisionSignal(t *testing.T) {
	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}},{"key":"service.version","value":{"stringValue":"0.153.4"}},{"key":"host.name","value":{"stringValue":"decision-host.example.test"}},{"key":"user.account_id","value":{"stringValue":"decision-account-123"}},{"key":"authorization","value":{"stringValue":"Bearer tiq-canary-decision-resource-token"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.tool_decision"}},{"key":"conversation.id","value":{"stringValue":"decision-session"}},{"key":"call_id","value":{"stringValue":"decision-call-approved"}},{"key":"decision","value":{"stringValue":"allow"}},{"key":"source","value":{"stringValue":"policy"}},{"key":"tool_name","value":{"stringValue":"exec_command"}},{"key":"tool_namespace","value":{"stringValue":"functions"}},{"key":"model","value":{"stringValue":"gpt-6-astra"}},{"key":"slug","value":{"stringValue":"tiq-canary-decision-slug"}},{"key":"authorization","value":{"stringValue":"Bearer tiq-canary-decision-token"}},{"key":"command","value":{"stringValue":"tiq-canary-decision-command"}},{"key":"command_args","value":{"stringValue":"tiq-canary-decision-command-args"}},{"key":"command_line","value":{"stringValue":"tiq-canary-decision-command-line"}},{"key":"cwd","value":{"stringValue":"/tmp/tiq-canary-decision-cwd"}},{"key":"path","value":{"stringValue":"/tmp/tiq-canary-decision-path"}},{"key":"file_path","value":{"stringValue":"/tmp/tiq-canary-decision-file-path"}},{"key":"arguments","value":{"stringValue":"--token=tiq-canary-decision-argument"}},{"key":"output","value":{"stringValue":"tiq-canary-decision-output"}},{"key":"api_key","value":{"stringValue":"tiq-canary-decision-api-key"}},{"key":"user.email","value":{"stringValue":"decision-user@example.test"}}],"body":{"stringValue":"tiq-canary-decision-body"},"severityText":"INFO"},{"attributes":[{"key":"event.name","value":{"stringValue":"codex.tool_decision"}},{"key":"conversation.id","value":{"stringValue":"decision-session"}},{"key":"call_id","value":{"stringValue":"decision-call-denied"}},{"key":"decision","value":{"stringValue":"deny"}},{"key":"source","value":{"stringValue":"sandbox"}},{"key":"tool_name","value":{"stringValue":"apply_patch"}},{"key":"tool_namespace","value":{"stringValue":"functions"}}],"severityText":"INFO"}]}]}]}`)
	events, err := NormalizeLogs(data, time.Date(2026, 9, 10, 20, 9, 20, 0, time.UTC))
	if err != nil || len(events) != 2 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	assertApprovedToolDecisionEvent(t, events[0])
	assertDeniedToolDecisionEvent(t, events[1])
	encoded, _ := json.Marshal(events)
	assertNoStringCanaries(t, string(encoded), []string{"tiq-canary-decision-argument", "tiq-canary-decision-output", "tiq-canary-decision-api-key", "decision-user@example.test", "tiq-canary-decision-body", "decision-host.example.test", "decision-account-123", "tiq-canary-decision-resource-token", "tiq-canary-decision-slug", "tiq-canary-decision-token", "tiq-canary-decision-command", "tiq-canary-decision-command-args", "tiq-canary-decision-command-line", "tiq-canary-decision-cwd", "tiq-canary-decision-path", "tiq-canary-decision-file-path"})
}

func assertApprovedToolDecisionEvent(t *testing.T, event canonical.Event) {
	t.Helper()
	if event.EventType != codexToolDecisionEvent || event.Attributes["approval_id"] != "codex:decision-session:approval:decision-call-approved" {
		t.Fatalf("approval identity = %#v", event)
	}
	if event.Attributes["approval_decision"] != "approved" || event.Attributes["approval_reason_class"] != "policy" || event.Attributes["tool_name"] != "exec_command" || event.Attributes["tool_namespace"] != "functions" {
		t.Fatalf("approval fields = %#v", event.Attributes)
	}
	assertToolDecisionUnavailableFields(t, event.Attributes["unavailable_fields"].([]string))
	decision := event.ProviderExtensions["tool_decision"].(map[string]any)
	if decision["decision"] != "approved" || decision["raw_decision"] != "allow" || decision["source"] != "policy" || decision["provenance"] != "observed" {
		t.Fatalf("tool_decision extension = %#v", decision)
	}
	if logAttributes := event.ProviderExtensions["log_attributes"].(map[string]any); logAttributes[codexEventNameKey] != codexToolDecisionEvent || logAttributes["model"] != "gpt-6-astra" {
		t.Fatalf("tool-decision log attributes should preserve only safe source evidence: %#v", logAttributes)
	}
}

func assertDeniedToolDecisionEvent(t *testing.T, event canonical.Event) {
	t.Helper()
	if event.Attributes["approval_id"] != "codex:decision-session:approval:decision-call-denied" || event.Attributes["approval_decision"] != "denied" || event.Attributes["approval_reason_class"] != "sandbox" {
		t.Fatalf("denied approval fields = %#v", event.Attributes)
	}
}

func assertToolDecisionUnavailableFields(t *testing.T, unavailable []string) {
	t.Helper()
	if slices.Contains(unavailable, "approvals") {
		t.Fatalf("approvals must be available for tool_decision: %#v", unavailable)
	}
	if !slices.Contains(unavailable, "tool_calls") {
		t.Fatalf("tool_calls must remain unavailable for tool_decision: %#v", unavailable)
	}
}

func TestNormalizeLogsToolDecisionUnknownDecisionIsExplicit(t *testing.T) {
	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.tool_decision"}},{"key":"call_id","value":{"stringValue":"decision-call"}}],"severityText":"INFO"}]}]}]}`)
	events, err := NormalizeLogs(data, time.Date(2026, 9, 10, 20, 9, 20, 0, time.UTC))
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	if events[0].Attributes["approval_decision"] != "unknown" {
		t.Fatalf("missing decision must be explicit unknown, got %#v", events[0].Attributes["approval_decision"])
	}
}

func TestNormalizeLogsMapsCodexSandboxOutcomeSignal(t *testing.T) {
	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}},{"key":"service.version","value":{"stringValue":"0.153.4"}},{"key":"host.name","value":{"stringValue":"sandbox-host.example.test"}},{"key":"user.account_id","value":{"stringValue":"sandbox-account-123"}},{"key":"authorization","value":{"stringValue":"Bearer tiq-canary-resource-token"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.sandbox_outcome"}},{"key":"conversation.id","value":{"stringValue":"sandbox-session"}},{"key":"call_id","value":{"stringValue":"sandbox-call-success"}},{"key":"tool_name","value":{"stringValue":"exec_command"}},{"key":"initial_duration_ms","value":{"stringValue":"123"}},{"key":"outcome","value":{"stringValue":"success"}},{"key":"model","value":{"stringValue":"gpt-6-astra"}},{"key":"slug","value":{"stringValue":"tiq-canary-sandbox-slug"}},{"key":"command","value":{"stringValue":"tiq-canary-sandbox-command"}},{"key":"command_args","value":{"stringValue":"tiq-canary-sandbox-command-args"}},{"key":"cwd","value":{"stringValue":"/tmp/tiq-canary-sandbox-cwd"}},{"key":"path","value":{"stringValue":"/tmp/tiq-canary-sandbox-path"}},{"key":"arguments","value":{"stringValue":"--token=tiq-canary-sandbox-argument"}},{"key":"output","value":{"stringValue":"tiq-canary-sandbox-output"}},{"key":"user.email","value":{"stringValue":"sandbox-user@example.test"}}],"body":{"stringValue":"tiq-canary-sandbox-body"},"severityText":"INFO"}]}]}]}`)
	events, err := NormalizeLogs(data, time.Date(2026, 9, 10, 20, 9, 20, 0, time.UTC))
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	event := events[0]
	if event.EventType != codexSandboxOutcomeEvent || event.Attributes["operation_id"] != "codex:sandbox-session:sandbox:sandbox-call-success" {
		t.Fatalf("sandbox event identity = %#v", event)
	}
	if event.Attributes["category"] != "shell command" || event.Attributes["outcome"] != "success" || event.Attributes["duration_ms"] != int64(123) {
		t.Fatalf("sandbox operation attributes = %#v", event.Attributes)
	}
	assertSandboxUnavailableFields(t, event.Attributes["unavailable_fields"].([]string))
	sandbox, ok := event.ProviderExtensions["sandbox_outcome"].(map[string]any)
	if !ok || sandbox["call_id"] != "sandbox-call-success" || sandbox["initial_duration_ms"] != "123" || sandbox["provenance"] != "observed" {
		t.Fatalf("sandbox_outcome extension = %#v", event.ProviderExtensions["sandbox_outcome"])
	}
	encoded, _ := json.Marshal(event)
	assertNoStringCanaries(t, string(encoded), []string{"tiq-canary-sandbox-argument", "tiq-canary-sandbox-output", "sandbox-user@example.test", "tiq-canary-sandbox-body", "sandbox-host.example.test", "sandbox-account-123", "tiq-canary-resource-token", "tiq-canary-sandbox-slug", "tiq-canary-sandbox-command", "tiq-canary-sandbox-command-args", "tiq-canary-sandbox-cwd", "tiq-canary-sandbox-path"})
}

func assertSandboxUnavailableFields(t *testing.T, unavailable []string) {
	t.Helper()
	if slices.Contains(unavailable, "command_execution") {
		t.Fatalf("command_execution must be available for sandbox_outcome: %#v", unavailable)
	}
	for _, field := range []string{"file_operations", "tool_calls"} {
		if !slices.Contains(unavailable, field) {
			t.Fatalf("%s should remain unavailable: %#v", field, unavailable)
		}
	}
}

func assertNoStringCanaries(t *testing.T, haystack string, canaries []string) {
	t.Helper()
	for _, canary := range canaries {
		if strings.Contains(haystack, canary) {
			t.Fatalf("leaked canary %q: %s", canary, haystack)
		}
	}
}

func TestNormalizeLogsSandboxOutcomeUnknownDurationIsAbsent(t *testing.T) {
	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.sandbox_outcome"}},{"key":"call_id","value":{"stringValue":"sandbox-call"}},{"key":"initial_duration_ms","value":{"stringValue":"not-a-number"}}],"severityText":"INFO"}]}]}]}`)
	events, err := NormalizeLogs(data, time.Date(2026, 9, 10, 20, 9, 20, 0, time.UTC))
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	if _, ok := events[0].Attributes["duration_ms"]; ok {
		t.Fatalf("malformed sandbox duration must stay absent, got %#v", events[0].Attributes["duration_ms"])
	}
	if events[0].Attributes["outcome"] != "unknown" {
		t.Fatalf("outcome = %#v, want unknown", events[0].Attributes["outcome"])
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
