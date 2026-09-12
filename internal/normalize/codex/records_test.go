package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// fixtureReceivedAt is the fixed ingestion time used for the golden output so
// timestamps derived from receivedAt (absent record timestamps) are stable.
var fixtureReceivedAt = time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

func assertGoldenJSON[T any](t *testing.T, filename string, got T, label string) {
	t.Helper()
	goldenPath := filepath.Join("..", "..", "..", "fixtures", "codex", "expected", filename)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		data, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatalf("marshal golden: %v", err)
		}
		if err := os.WriteFile(goldenPath, append(data, 10), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	goldenBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var want T
	if err := json.Unmarshal(goldenBytes, &want); err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.MarshalIndent(got, "", "  ")
		t.Fatalf("%s != golden:\n got %s\nwant %s", label, gotJSON, goldenBytes)
	}
}

func TestExtractLogModelInteractionsGolden(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "fixtures", "codex", "observed-sanitised", "codex-0.145.0-logs.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var wrapper struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		t.Fatalf("unwrap payload: %v", err)
	}

	got, err := ExtractLogModelInteractions([]byte(wrapper.Payload), fixtureReceivedAt)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	assertGoldenJSON(t, "codex-0.145.0-logs.records.json", got, "records")
}

func TestExtractLogOperationsGolden(t *testing.T) {
	t.Parallel()
	assertOperationsGolden(t, "codex-0.153.4-outcome-contracts-otlp.json", "codex-0.153.4-tool-result.operations.json", "operations")
}

func TestExtractLogSandboxOutcomeOperationsGolden(t *testing.T) {
	t.Parallel()
	assertOperationsGolden(t, "codex-0.153.4-sandbox-outcome-otlp.json", "codex-0.153.4-sandbox-outcome.operations.json", "sandbox operations")
}

func TestExtractLogSandboxOutcomeDropsSensitiveProviderExtensions(t *testing.T) {
	t.Parallel()
	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}},{"key":"service.version","value":{"stringValue":"0.153.4"}},{"key":"host.name","value":{"stringValue":"sandbox-host.example.test"}},{"key":"user.account_id","value":{"stringValue":"sandbox-account-123"}},{"key":"authorization","value":{"stringValue":"Bearer tiq-canary-resource-token"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.sandbox_outcome"}},{"key":"conversation.id","value":{"stringValue":"sandbox-session"}},{"key":"call_id","value":{"stringValue":"sandbox-call"}},{"key":"tool_name","value":{"stringValue":"exec_command"}},{"key":"initial_duration_ms","value":{"stringValue":"123"}},{"key":"outcome","value":{"stringValue":"success"}},{"key":"model","value":{"stringValue":"gpt-6-astra"}},{"key":"slug","value":{"stringValue":"tiq-canary-sandbox-slug"}},{"key":"command","value":{"stringValue":"tiq-canary-sandbox-command"}},{"key":"command_args","value":{"stringValue":"tiq-canary-sandbox-command-args"}},{"key":"cwd","value":{"stringValue":"/tmp/tiq-canary-sandbox-cwd"}},{"key":"path","value":{"stringValue":"/tmp/tiq-canary-sandbox-path"}},{"key":"arguments","value":{"stringValue":"--token=tiq-canary-sandbox-argument"}},{"key":"output","value":{"stringValue":"tiq-canary-sandbox-output"}},{"key":"user.email","value":{"stringValue":"sandbox-user@example.test"}}],"body":{"stringValue":"tiq-canary-sandbox-body"},"severityText":"INFO","timeUnixNano":"1788717763000000000"}]}]}]}`)
	operations, err := ExtractLogOperations(data, fixtureReceivedAt)
	if err != nil {
		t.Fatalf("extract operations: %v", err)
	}
	if len(operations) != 1 {
		t.Fatalf("operations = %d, want 1: %#v", len(operations), operations)
	}
	encoded, _ := json.Marshal(operations[0])
	assertNoStringCanaries(t, string(encoded), []string{"tiq-canary-sandbox-argument", "tiq-canary-sandbox-output", "sandbox-user@example.test", "tiq-canary-sandbox-body", "sandbox-host.example.test", "sandbox-account-123", "tiq-canary-resource-token", "tiq-canary-sandbox-slug", "tiq-canary-sandbox-command", "tiq-canary-sandbox-command-args", "tiq-canary-sandbox-cwd", "tiq-canary-sandbox-path"})
}

func assertOperationsGolden(t *testing.T, fixture, golden, label string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "fixtures", "codex", "observed-sanitised", fixture))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var wrapper struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		t.Fatalf("unwrap payload: %v", err)
	}
	got, err := ExtractLogOperations([]byte(wrapper.Payload), fixtureReceivedAt)
	if err != nil {
		t.Fatalf("extract operations: %v", err)
	}
	assertGoldenJSON(t, golden, got, label)
}

func TestExtractLogOperationsMapsMCPToolResult(t *testing.T) {
	t.Parallel()

	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}},{"key":"service.version","value":{"stringValue":"0.153.4"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.tool_result"}},{"key":"conversation.id","value":{"stringValue":"mcp-session"}},{"key":"mcp_server","value":{"stringValue":"synthetic-filesystem-server"}},{"key":"mcp_server_origin","value":{"stringValue":"config"}},{"key":"tool_name","value":{"stringValue":"read_file"}},{"key":"tool_namespace","value":{"stringValue":"mcp"}},{"key":"call_id","value":{"stringValue":"mcp-call"}},{"key":"duration_ms","value":{"stringValue":"42"}},{"key":"success","value":{"boolValue":true}}],"severityText":"INFO","timeUnixNano":"1788717763000000000","observedTimeUnixNano":"1788717763000000000"}]}]}]}`)
	operations, err := ExtractLogOperations(data, fixtureReceivedAt)
	if err != nil {
		t.Fatalf("extract operations: %v", err)
	}
	if len(operations) != 1 {
		t.Fatalf("operations = %d, want 1: %#v", len(operations), operations)
	}
	operation := operations[0]
	if operation.OperationID != "codex:mcp-session:tool:mcp-call" || operation.Category != canonical.OperationCategoryMCPCall || operation.Outcome != "success" {
		t.Fatalf("operation = %#v", operation)
	}
	mcpCall, ok := operation.ProviderExtensions["mcp_call"].(map[string]any)
	if !ok || mcpCall["server_name"] != "synthetic-filesystem-server" || mcpCall["tool_name"] != "read_file" {
		t.Fatalf("mcp_call = %#v", operation.ProviderExtensions["mcp_call"])
	}
}

func TestExtractLogModelInteractionsCorrelatesShuffledDuplicates(t *testing.T) {
	t.Parallel()

	ordered := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_cli_rs"}}]},"scopeLogs":[{"logRecords":[{"timeUnixNano":"1785059999000000000","observedTimeUnixNano":"1785059999001000000","attributes":[{"key":"event.name","value":{"stringValue":"codex.sse_event"}},{"key":"model","value":{"stringValue":"a"}},{"key":"input_token_count","value":{"stringValue":"1"}}],"severityText":"INFO"},{"timeUnixNano":"1785060000000000000","observedTimeUnixNano":"1785060000001000000","attributes":[{"key":"event.name","value":{"stringValue":"codex.sse_event"}},{"key":"model","value":{"stringValue":"b"}},{"key":"input_token_count","value":{"stringValue":"2"}}],"severityText":"INFO"}]}]}]}`)
	shuffledDuplicate := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_cli_rs"}}]},"scopeLogs":[{"logRecords":[{"timeUnixNano":"1785060000000000000","observedTimeUnixNano":"1785060000001000000","attributes":[{"key":"event.name","value":{"stringValue":"codex.sse_event"}},{"key":"model","value":{"stringValue":"b"}},{"key":"input_token_count","value":{"stringValue":"2"}}],"severityText":"INFO"},{"timeUnixNano":"1785059999000000000","observedTimeUnixNano":"1785059999001000000","attributes":[{"key":"event.name","value":{"stringValue":"codex.sse_event"}},{"key":"model","value":{"stringValue":"a"}},{"key":"input_token_count","value":{"stringValue":"1"}}],"severityText":"INFO"},{"timeUnixNano":"1785060000000000000","observedTimeUnixNano":"1785060000001000000","attributes":[{"key":"event.name","value":{"stringValue":"codex.sse_event"}},{"key":"model","value":{"stringValue":"b"}},{"key":"input_token_count","value":{"stringValue":"2"}}],"severityText":"INFO"}]}]}]}`)

	want, err := ExtractLogModelInteractions(ordered, fixtureReceivedAt)
	if err != nil {
		t.Fatalf("extract ordered: %v", err)
	}
	got, err := ExtractLogModelInteractions(shuffledDuplicate, fixtureReceivedAt)
	if err != nil {
		t.Fatalf("extract shuffled: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("deduplicated records = %d, want 2", len(got))
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal ordered: %v", err)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal shuffled: %v", err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("shuffled log replay must be byte-identical\nwant: %s\n got: %s", wantJSON, gotJSON)
	}
	correlation := got[0].ProviderExtensions["correlation"].(map[string]any)
	taskBoundary := correlation["task_boundary"].(map[string]any)
	if correlation["dedup_key"] != got[0].RequestID || taskBoundary["confidence"] != "unknown" {
		t.Fatalf("correlation = %#v", correlation)
	}
}

// logPayload builds a raw OTLP logs payload wrapping a single record's
// attributes, for the negative tests.
func logPayload(t *testing.T, service string, recordAttrs map[string]any) []byte {
	t.Helper()
	attrs := make([]map[string]any, 0, len(recordAttrs))
	for key, value := range recordAttrs {
		attrs = append(attrs, map[string]any{"key": key, "value": map[string]any{"stringValue": value}})
	}
	payload := map[string]any{
		"resourceLogs": []map[string]any{{
			"resource": map[string]any{"attributes": []map[string]any{
				{"key": "service.name", "value": map[string]any{"stringValue": service}},
			}},
			"scopeLogs": []map[string]any{{
				"logRecords": []map[string]any{{"attributes": attrs, "severityText": "INFO"}},
			}},
		}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return data
}

func extractOne(t *testing.T, service string, recordAttrs map[string]any) []canonical.ModelInteraction {
	t.Helper()
	records, err := ExtractLogModelInteractions(logPayload(t, service, recordAttrs), fixtureReceivedAt)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	return records
}

func TestExtractAcceptsBothServiceNames(t *testing.T) {
	t.Parallel()

	for _, service := range []string{"codex_cli_rs", "codex_exec"} {
		records := extractOne(t, service, map[string]any{"event.name": "codex.sse_event", "model": "m", "input_token_count": "5"})
		if len(records) != 1 {
			t.Fatalf("service %q: records = %d, want 1", service, len(records))
		}
	}
}

func TestExtractMissingTokensStayNil(t *testing.T) {
	t.Parallel()

	records := extractOne(t, "codex_cli_rs", map[string]any{"event.name": "codex.sse_event", "model": "m"})
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0].InputTokens != nil || records[0].OutputTokens != nil {
		t.Fatalf("absent tokens must stay nil, got in=%v out=%v", records[0].InputTokens, records[0].OutputTokens)
	}
	// A nil token must serialise as null, never a fabricated 0.
	encoded, _ := json.Marshal(records[0])
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if fields["input_tokens"] != nil || fields["output_tokens"] != nil {
		t.Fatalf("absent tokens must serialise as null, got %v / %v", fields["input_tokens"], fields["output_tokens"])
	}
	// Model observed but no tokens observed => unknown provenance, not fabricated observed.
	if records[0].Provenance != canonical.ProvenanceUnknown {
		t.Fatalf("provenance = %q, want %q", records[0].Provenance, canonical.ProvenanceUnknown)
	}
}

func TestExtractMalformedTokenIsNilNotZero(t *testing.T) {
	t.Parallel()

	records := extractOne(t, "codex_cli_rs", map[string]any{"event.name": "codex.sse_event", "model": "m", "input_token_count": "not-a-number"})
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0].InputTokens != nil {
		t.Fatalf("malformed token must be nil, got %v", *records[0].InputTokens)
	}
}

func TestExtractParsesNumericTokenValue(t *testing.T) {
	t.Parallel()

	// OTLP doubleValue arrives as a JSON number (float64 after decoding). An
	// integral value parses; a non-integral one must be nil, never truncated.
	payload := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_cli_rs"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.sse_event"}},{"key":"model","value":{"stringValue":"m"}},{"key":"input_token_count","value":{"doubleValue":42}},{"key":"output_token_count","value":{"doubleValue":7.5}}],"severityText":"INFO"}]}]}]}`)
	records, err := ExtractLogModelInteractions(payload, fixtureReceivedAt)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0].InputTokens == nil || *records[0].InputTokens != 42 {
		t.Fatalf("numeric input token = %v, want 42", records[0].InputTokens)
	}
	if records[0].OutputTokens != nil {
		t.Fatalf("non-integral token must be nil (not truncated), got %v", *records[0].OutputTokens)
	}
}

func TestExtractMissingModelIsUnknown(t *testing.T) {
	t.Parallel()

	records := extractOne(t, "codex_cli_rs", map[string]any{"event.name": "codex.sse_event", "input_token_count": "5"})
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0].Model != "unknown" {
		t.Fatalf("model = %q, want %q", records[0].Model, "unknown")
	}
	if records[0].Provenance != canonical.ProvenanceUnknown {
		t.Fatalf("provenance = %q, want %q", records[0].Provenance, canonical.ProvenanceUnknown)
	}
}

func TestExtractSkipsNonModelRecords(t *testing.T) {
	t.Parallel()

	// Non-whitelisted event.name => no record.
	if records := extractOne(t, "codex_cli_rs", map[string]any{"event.name": "codex.tool_result", "model": "m"}); len(records) != 0 {
		t.Fatalf("tool_result must yield no record, got %d", len(records))
	}
	// Whitelisted event but neither model nor tokens => no record.
	if records := extractOne(t, "codex_cli_rs", map[string]any{"event.name": "codex.sse_event"}); len(records) != 0 {
		t.Fatalf("empty sse_event must yield no record, got %d", len(records))
	}
}

func TestExtractSkipsUnobservedService(t *testing.T) {
	t.Parallel()

	if records := extractOne(t, "some_other_service", map[string]any{"event.name": "codex.sse_event", "model": "m", "input_token_count": "5"}); len(records) != 0 {
		t.Fatalf("unobserved service must yield no record, got %d", len(records))
	}
}
