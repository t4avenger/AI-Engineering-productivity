package cursor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestExtractModelInteractionsGolden_PrintJSON(t *testing.T) {
	// Golden assertions live in golden_test.go to avoid duplicated scaffolding.
}

func TestExtractModelInteractionsIsHonestAboutUnknowns(t *testing.T) {
	records, err := ExtractModelInteractions(readFixture(t, "cursor-agent-2026.05.16-0338208-print-result.json"), stubFingerprint)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("record count = %d, want 1", len(records))
	}
	record := records[0]
	if record.Model != "unknown" {
		t.Fatalf("model = %q, want unknown (print-json has no model)", record.Model)
	}
	if record.ReasoningTokens != nil {
		t.Fatalf("reasoning tokens must stay nil (unknown), got %v", *record.ReasoningTokens)
	}
	if record.CachedInputTokens == nil || *record.CachedInputTokens != 0 {
		t.Fatalf("cached input tokens = %v, want observed 0", record.CachedInputTokens)
	}
	if record.Result != "success" {
		t.Fatalf("result = %q, want success from observed subtype/is_error", record.Result)
	}
	if _, ok := record.ProviderExtensions["outcome_contract"].(map[string]any); !ok {
		t.Fatalf("outcome_contract missing: %#v", record.ProviderExtensions)
	}
	if record.Provenance != canonical.ProvenanceUnknown {
		t.Fatalf("provenance = %q, want unknown (tokens without model)", record.Provenance)
	}
}

func TestExtractModelInteractionsStreamPromotesModelAndTokensWithObservedProvenance(t *testing.T) {
	records, err := ExtractModelInteractions(readFixture(t, "cursor-agent-2026.09.02-c22c1a3-stream-result-with-model.json"), stubFingerprint)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("record count = %d, want 1", len(records))
	}
	record := records[0]
	if record.Model != "GPT-5.2 Medium" {
		t.Fatalf("model = %q", record.Model)
	}
	if record.InputTokens == nil || record.OutputTokens == nil {
		t.Fatalf("tokens must be observed, got in=%v out=%v", record.InputTokens, record.OutputTokens)
	}
	if record.Provenance != canonical.ProvenanceObserved {
		t.Fatalf("provenance = %q, want observed", record.Provenance)
	}
}

func TestExtractModelInteractionsCapabilityProbeYieldsNoRecords(t *testing.T) {
	records, err := ExtractModelInteractions(readFixture(t, "cursor-agent-2026.05.16-0338208-capability-probe.json"), stubFingerprint)
	if err != nil {
		t.Fatalf("extract probe: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("capability probe must not fabricate records, got %d", len(records))
	}
}

func TestExtractDoesNotLeakRawIDs(t *testing.T) {
	records, err := ExtractModelInteractions(readFixture(t, "cursor-agent-2026.05.16-0338208-print-result.json"), stubFingerprint)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	serialized, _ := json.Marshal(records)
	for _, leaked := range []string{"synthetic-session-id", "synthetic-request-id"} {
		if strings.Contains(string(serialized), leaked) {
			t.Fatalf("raw identifier leaked into canonical output: %q", leaked)
		}
	}
}
