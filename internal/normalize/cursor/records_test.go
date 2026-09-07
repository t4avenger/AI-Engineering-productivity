package cursor

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestExtractModelInteractionsGolden_PrintJSON(t *testing.T) {
	cases := []struct {
		name       string
		fixture    string
		goldenFile string
	}{
		{
			name:       "print_json",
			fixture:    "cursor-agent-2026.05.16-0338208-print-result.json",
			goldenFile: "cursor-agent-2026.05.16-0338208-print-result.records.json",
		},
		{
			name:       "stream_json",
			fixture:    "cursor-agent-2026.09.02-c22c1a3-stream-result-with-model.json",
			goldenFile: "cursor-agent-2026.09.02-c22c1a3-stream-result-with-model.records.json",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			input := readFixture(t, tc.fixture)
			first := extractDeterministic(t, input)
			if updateGolden() {
				writeGolden(t, tc.goldenFile, first)
			}
			assertMatchesGolden(t, tc.goldenFile, first)
		})
	}
}

func extractDeterministic(t *testing.T, input []byte) []canonical.ModelInteraction {
	t.Helper()
	first, err := ExtractModelInteractions(input, stubFingerprint)
	if err != nil {
		t.Fatalf("first extraction: %v", err)
	}
	second, err := ExtractModelInteractions(input, stubFingerprint)
	if err != nil {
		t.Fatalf("second extraction: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("extraction must be deterministic")
	}
	return first
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
