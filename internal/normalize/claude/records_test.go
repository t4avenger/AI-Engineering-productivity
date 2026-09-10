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
