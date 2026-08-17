package cost

import (
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
	if got := calculator.Calculate(event).Status; got != "missing_usage" {
		t.Fatalf("status=%s", got)
	}
}
