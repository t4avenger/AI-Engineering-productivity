package claude

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestExtractModelInteractionsGolden(t *testing.T) {
	input := readFixture(t, "claude-code-2.1.251-otlp-events.json")
	first, err := ExtractModelInteractions(input)
	if err != nil {
		t.Fatalf("first extraction: %v", err)
	}
	second, err := ExtractModelInteractions(input)
	if err != nil {
		t.Fatalf("second extraction: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("extraction must be deterministic")
	}

	if updateGolden() {
		writeGolden(t, "claude-code-2.1.251-otlp-events.records.json", first)
	}
	assertMatchesGolden(t, "claude-code-2.1.251-otlp-events.records.json", first)
}

func TestExtractModelInteractionsIsHonestAboutUnknowns(t *testing.T) {
	records, err := ExtractModelInteractions(readFixture(t, "claude-code-2.1.251-otlp-events.json"))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("record count = %d, want 1 (only api_request)", len(records))
	}
	record := records[0]
	if record.Model != "claude-opus-4-8" {
		t.Fatalf("model = %q", record.Model)
	}
	if record.ReasoningTokens != nil {
		t.Fatalf("reasoning tokens must stay nil (unknown), got %v", *record.ReasoningTokens)
	}
	if record.Result != "success" {
		t.Fatalf("result = %q, want success from api_request outcome contract", record.Result)
	}
	if _, ok := record.ProviderExtensions["outcome_contract"].(map[string]any); !ok {
		t.Fatalf("outcome_contract missing: %#v", record.ProviderExtensions)
	}
	if record.CachedInputTokens == nil || *record.CachedInputTokens != 0 {
		t.Fatalf("cached input tokens = %v, want observed 0 (cache_read_tokens)", record.CachedInputTokens)
	}
	if record.Provenance != canonical.ProvenanceObserved {
		t.Fatalf("provenance = %q, want observed", record.Provenance)
	}
	// cache_creation_tokens has no canonical field and must be preserved, not conflated.
	preserved := record.ProviderExtensions["event"].(map[string]any)
	if _, ok := preserved["cache_creation_tokens"]; !ok {
		t.Fatalf("cache_creation_tokens must be preserved in provider_extensions, got %v", preserved)
	}
	timestamps := record.ProviderExtensions["timestamps"].(map[string]any)
	if timestamps["started_at"] != "derived_from_duration" || timestamps["completed_at"] != "observed" {
		t.Fatalf("timestamp provenance = %v", timestamps)
	}
	// started_at derived from the observed completion minus the observed duration.
	if got := record.CompletedAt.Sub(record.StartedAt).Milliseconds(); got != 3077 {
		t.Fatalf("derived duration = %dms, want 3077", got)
	}
}

func TestExtractModelInteractionsProvenanceDowngradesWithoutModel(t *testing.T) {
	payload := map[string]any{
		"fixture_version": 1, "fixture_origin": "synthetic", "provider": "anthropic", "tool": "claude-code",
		"tool_version": "2.1.251", "captured_at": "2026-08-31T13:37:07Z", "sanitisation_reviewed": true,
		"payload": map[string]any{
			"source_type": "otlp_http_json_logs",
			"sample_events": []any{map[string]any{
				"event_name": "api_request", "session_id": "s", "event_sequence": float64(1),
				"event_timestamp": "2026-08-31T13:37:07.000Z", "input_tokens": float64(5),
			}},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	records, err := ExtractModelInteractions(data)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("record count = %d, want 1", len(records))
	}
	if records[0].Model != "unknown" || records[0].Provenance != canonical.ProvenanceUnknown {
		t.Fatalf("model = %q, provenance = %q; want unknown/unknown", records[0].Model, records[0].Provenance)
	}
}

func TestExtractModelInteractionsCapabilityProbeYieldsNoRecords(t *testing.T) {
	records, err := ExtractModelInteractions(readFixture(t, "claude-code-2.1.251-capability-probe.json"))
	if err != nil {
		t.Fatalf("extract probe: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("capability probe must not fabricate records, got %d", len(records))
	}
}

func TestExtractOperationsGolden(t *testing.T) {
	input := readFixture(t, "claude-code-2.1.263-tool-result.json")
	first, err := ExtractOperations(input)
	if err != nil {
		t.Fatalf("first extraction: %v", err)
	}
	second, err := ExtractOperations(input)
	if err != nil {
		t.Fatalf("second extraction: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("extraction must be deterministic")
	}

	if updateGolden() {
		writeGolden(t, "claude-code-2.1.263-tool-result.operations.json", first)
	}
	assertMatchesGolden(t, "claude-code-2.1.263-tool-result.operations.json", first)
}

// toolResultOperations extracts the tool-result fixture's operations and keys
// them by OperationID, asserting that only the three tool_result events (not the
// api_request event) become Operations.
func toolResultOperations(t *testing.T) map[string]canonical.Operation {
	t.Helper()
	operations, err := ExtractOperations(readFixture(t, "claude-code-2.1.263-tool-result.json"))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(operations) != 3 {
		t.Fatalf("operation count = %d, want 3 (tool_result only, api_request skipped)", len(operations))
	}
	byID := map[string]canonical.Operation{}
	for _, operation := range operations {
		byID[operation.OperationID] = operation
	}
	return byID
}

func TestExtractOperationsClassifiesToolCalls(t *testing.T) {
	byID := toolResultOperations(t)
	cases := []struct {
		id       string
		category canonical.OperationCategory
		outcome  string
	}{
		{"claude-code:synthetic-session-tools:tool:toolu_synthetic_bash", canonical.OperationCategoryShellCommand, "success"},
		{"claude-code:synthetic-session-tools:tool:toolu_synthetic_read", canonical.OperationCategoryFilesystemRead, "failed"},
		{"claude-code:synthetic-session-tools:tool:toolu_synthetic_mcp", canonical.OperationCategoryMCPCall, "success"},
	}
	for _, tc := range cases {
		operation := byID[tc.id]
		if operation.Category != tc.category || operation.Outcome != tc.outcome {
			t.Errorf("%s category/outcome = %q/%q, want %q/%q", tc.id, operation.Category, operation.Outcome, tc.category, tc.outcome)
		}
		if operation.Provider != "anthropic" || operation.Tool != "claude-code" || operation.Provenance != canonical.ProvenanceObserved {
			t.Errorf("%s provider/tool/provenance = %q/%q/%q", tc.id, operation.Provider, operation.Tool, operation.Provenance)
		}
	}
}

func TestExtractOperationsPreservesRawFields(t *testing.T) {
	byID := toolResultOperations(t)
	// success arrives as the string "true"; it and tool_name are preserved raw.
	bash := byID["claude-code:synthetic-session-tools:tool:toolu_synthetic_bash"].ProviderExtensions["event"].(map[string]any)
	if bash["success"] != "true" || bash["tool_name"] != "Bash" {
		t.Fatalf("Bash echo did not preserve raw fields: %#v", bash)
	}
	read := byID["claude-code:synthetic-session-tools:tool:toolu_synthetic_read"].ProviderExtensions["event"].(map[string]any)
	if read["error_type"] != "Error:ENOENT" {
		t.Fatalf("Read error_type not preserved: %#v", read)
	}
	// The MCP tool_result carries no duration_ms; it must be omitted, never 0.
	mcp := byID["claude-code:synthetic-session-tools:tool:toolu_synthetic_mcp"].ProviderExtensions["event"].(map[string]any)
	if _, present := mcp["duration_ms"]; present {
		t.Fatalf("absent duration_ms must be omitted, not fabricated: %#v", mcp)
	}
}

func TestExtractOperationsOutcomeUnknownWithoutSuccess(t *testing.T) {
	payload := map[string]any{
		"fixture_version": 1, "fixture_origin": "synthetic", "provider": "anthropic", "tool": "claude-code",
		"tool_version": "2.1.263", "captured_at": "2026-09-06T18:10:00Z", "sanitisation_reviewed": true,
		"payload": map[string]any{
			"source_type": "otlp_http_json_logs",
			"sample_events": []any{map[string]any{
				"event_name": "tool_result", "session_id": "s", "event_sequence": float64(1),
				"event_timestamp": "2026-09-06T18:10:00.000Z", "tool_name": "Task",
			}},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	operations, err := ExtractOperations(data)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(operations) != 1 {
		t.Fatalf("operation count = %d, want 1", len(operations))
	}
	if operations[0].Outcome != "unknown" {
		t.Fatalf("outcome = %q, want unknown when success is absent", operations[0].Outcome)
	}
	if operations[0].Category != canonical.OperationCategoryUnknown {
		t.Fatalf("category = %q, want unknown for an unmapped tool", operations[0].Category)
	}
}

func TestExtractOperationsPreservesRequestID(t *testing.T) {
	payload := map[string]any{
		"fixture_version": 1, "fixture_origin": "synthetic", "provider": "anthropic", "tool": "claude-code",
		"tool_version": "2.1.263", "captured_at": "2026-09-06T18:10:00Z", "sanitisation_reviewed": true,
		"payload": map[string]any{
			"source_type": "otlp_http_json_logs",
			"sample_events": []any{map[string]any{
				"event_name": "tool_result", "session_id": "s", "event_sequence": float64(1),
				"event_timestamp": "2026-09-06T18:10:00.000Z", "tool_name": "Bash",
				"tool_use_id": "toolu_x", "success": "true", "request_id": "req_synthetic",
			}},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	operations, err := ExtractOperations(data)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	// request_id has no typed Operation field, so it must survive raw in the echo
	// rather than being silently dropped as a structural key (epic #87).
	echo := operations[0].ProviderExtensions["event"].(map[string]any)
	if echo["request_id"] != "req_synthetic" {
		t.Fatalf("request_id must be preserved raw in the event echo: %#v", echo)
	}
}

func TestExtractOperationsFallbackIDsStayDistinct(t *testing.T) {
	// Two byte-identical tool_result events with neither tool_use_id nor an
	// event_sequence must not collide on a shared content hash and be silently
	// deduplicated by CorrelateOperations.
	event := map[string]any{
		"event_name": "tool_result", "session_id": "s",
		"event_timestamp": "2026-09-06T18:10:00.000Z", "tool_name": "Bash", "success": "true",
	}
	payload := map[string]any{
		"fixture_version": 1, "fixture_origin": "synthetic", "provider": "anthropic", "tool": "claude-code",
		"tool_version": "2.1.263", "captured_at": "2026-09-06T18:10:00Z", "sanitisation_reviewed": true,
		"payload": map[string]any{
			"source_type":   "otlp_http_json_logs",
			"sample_events": []any{event, event},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	operations, err := ExtractOperations(data)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(operations) != 2 {
		t.Fatalf("operation count = %d, want 2 distinct fallback IDs", len(operations))
	}
	if operations[0].OperationID == operations[1].OperationID {
		t.Fatalf("identical events must not share an OperationID: %q", operations[0].OperationID)
	}
}

func TestExtractOperationsCapabilityProbeYieldsNoRecords(t *testing.T) {
	operations, err := ExtractOperations(readFixture(t, "claude-code-2.1.251-capability-probe.json"))
	if err != nil {
		t.Fatalf("extract probe: %v", err)
	}
	if len(operations) != 0 {
		t.Fatalf("capability probe must not fabricate operations, got %d", len(operations))
	}
}
