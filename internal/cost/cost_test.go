package cost

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"testing"
	"time"
)

func TestCalculateStatusesAndRates(t *testing.T) {
	calculator := &Calculator{catalog: Catalog{SchemaVersion: schemaVersion, CatalogVersion: "test", Currency: "USD", Records: []PriceRecord{{ID: "one", Provider: "openai", ModelMatcher: "model-*", EffectiveAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Source: "test", RatesMicrousdPM: map[string]int64{"input": 1000000, "output": 2000000}}}}}
	event := canonical.Event{EventID: "e", SessionID: "s", Provider: "openai", OccurredAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), ReceivedAt: time.Now(), Attributes: map[string]any{"model": "model-a", "input_token_count": "100", "output_token_count": "10"}}
	r := calculator.Calculate(event)
	if r.Status != "calculated" || r.AmountMicrousd == nil || *r.AmountMicrousd != 120 {
		t.Fatalf("record=%#v", r)
	}
	event.Attributes["cached_input_token_count"] = "10"
	if got := calculator.Calculate(event).Status; got != "partial" {
		t.Fatalf("status=%s", got)
	}
	event.Attributes = map[string]any{"input_token_count": "1"}
	if got := calculator.Calculate(event).Status; got != "unknown_model" {
		t.Fatalf("status=%s", got)
	}
	event.Attributes = map[string]any{"model": "other", "input_token_count": "1"}
	if got := calculator.Calculate(event).Status; got != "unknown_price" {
		t.Fatalf("status=%s", got)
	}
	event.Attributes = map[string]any{"model": "model-a"}
	if got := calculator.Calculate(event).Status; got != "not_applicable" {
		t.Fatalf("status=%s", got)
	}
}

func TestLoadDefaultCatalog(t *testing.T) {
	calculator, err := LoadDefault("")
	if err != nil {
		t.Fatal(err)
	}
	event := canonical.Event{EventID: "e", SessionID: "s", Provider: "openai", OccurredAt: time.Now(), ReceivedAt: time.Now(), Attributes: map[string]any{"model": "model", "input_token_count": "1"}}
	if got := calculator.Calculate(event).Status; got != "unknown_price" {
		t.Fatalf("status=%s", got)
	}
}

func TestCatalogValidationAndMerge(t *testing.T) {
	valid := []byte("schema_version: \"0.1.0\"\ncatalog_version: test\ncurrency: USD\nrecords:\n  - id: record-a\n    provider: openai\n    model_matcher: model-*\n    effective_at: 2026-01-01T00:00:00Z\n    source: synthetic\n    rates_microusd_per_million_tokens: {input: 1000000}\n")
	catalog, err := decodeCatalog(valid)
	if err != nil {
		t.Fatal(err)
	}
	merged := merge(catalog, Catalog{CatalogVersion: "override", Records: []PriceRecord{{ID: "record-a", Provider: "openai", ModelMatcher: "model-a", EffectiveAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Source: "synthetic", RatesMicrousdPM: map[string]int64{"input": 2000000}}}})
	if merged.CatalogVersion != "override" || merged.Records[0].RatesMicrousdPM["input"] != 2000000 {
		t.Fatalf("merged=%v", merged)
	}
	if _, err := decodeCatalog([]byte("schema_version: nope\ncatalog_version: test\ncurrency: USD\nrecords: []\n")); err == nil {
		t.Fatal("invalid catalog accepted")
	}
}

func TestTokenAndMatcherHelpers(t *testing.T) {
	if _, ok := token("bad"); ok {
		t.Fatal("bad string accepted")
	}
	if _, ok := token(-1.0); ok {
		t.Fatal("negative float accepted")
	}
	if got, ok := token(float64(3)); !ok || got != 3 {
		t.Fatalf("float token=%d %t", got, ok)
	}
	if got, ok := token(json.Number("4")); !ok || got != 4 {
		t.Fatalf("json token=%d %t", got, ok)
	}
	if _, ok := token(json.Number("bad")); ok {
		t.Fatal("bad json accepted")
	}
	if _, ok := match("openai", "other", "model-*", "model-a"); ok {
		t.Fatal("provider matched")
	}
	if score, ok := match("openai", "openai", "model-*", "model-a"); !ok || score != 6 {
		t.Fatalf("wildcard=%d %t", score, ok)
	}
	if _, ok := match("openai", "openai", "model-a", "model-b"); ok {
		t.Fatal("exact mismatch accepted")
	}
	if !validCategory("reasoning") || validCategory("unknown") {
		t.Fatal("categories invalid")
	}
	if roundedProduct(1, 500000) != 1 {
		t.Fatal("rounding failed")
	}
}

func TestLoadDefaultOverrideFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "override.yaml")
	contents := "schema_version: \"0.1.0\"\ncatalog_version: override\ncurrency: USD\nrecords: []\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	calculator, err := LoadDefault(path)
	if err != nil {
		t.Fatal(err)
	}
	if calculator.catalog.CatalogVersion != "override" {
		t.Fatalf("version=%s", calculator.catalog.CatalogVersion)
	}
}
