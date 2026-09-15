package claude

import (
	"reflect"
	"testing"
)

// TestNormalizeAPIRefusalOutcomeGolden pins the canonical events produced from
// the api_refusal outcome fixture (E8, #95).
func TestNormalizeAPIRefusalOutcomeGolden(t *testing.T) {
	events, err := NormalizeEvents(readFixture(t, "claude-code-2.1.263-api-refusal-outcome.json"))
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	if updateGolden() {
		writeGolden(t, "claude-code-2.1.263-api-refusal-outcome.events.json", events)
	}
	assertMatchesGolden(t, "claude-code-2.1.263-api-refusal-outcome.events.json", events)
}

// TestNormalizeAPIRefusalStampsRefusedOutcome proves api_refusal becomes a third
// outcome (refused) and that the server_fallback_hop / category / explanation
// context rides verbatim, with the hop event distinguishable from the
// user-visible refusal.
func TestNormalizeAPIRefusalStampsRefusedOutcome(t *testing.T) {
	events, err := NormalizeEvents(readFixture(t, "claude-code-2.1.263-api-refusal-outcome.json"))
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2 (hop + user-visible refusal)", len(events))
	}
	sawHop, sawFinal := false, false
	for _, event := range events {
		if event.EventType != "api_refusal" {
			t.Fatalf("event type = %q, want api_refusal", event.EventType)
		}
		contract, ok := event.ProviderExtensions["outcome_contract"].(map[string]any)
		if !ok {
			t.Fatalf("outcome_contract missing: %#v", event.ProviderExtensions)
		}
		if contract["status"] != "refused" {
			t.Fatalf("status = %v, want refused", contract["status"])
		}
		if contract["model"] != "claude-opus-4-8" {
			t.Fatalf("model = %v", contract["model"])
		}
		hop, hasHop := contract["server_fallback_hop"].(bool)
		if !hasHop {
			t.Fatalf("server_fallback_hop missing on refusal contract: %#v", contract)
		}
		if hop {
			// A silently-retried hop is not a user-visible refusal, so it carries
			// no category/explanation and must stay distinguishable by the flag.
			sawHop = true
			if contract["has_category"] != false {
				t.Fatalf("hop has_category = %v, want false", contract["has_category"])
			}
			if _, present := contract["category"]; present {
				t.Fatalf("hop event must not carry category: %#v", contract)
			}
			if _, present := contract["explanation"]; present {
				t.Fatalf("hop event must not carry explanation: %#v", contract)
			}
		} else {
			sawFinal = true
			if contract["has_category"] != true || contract["has_explanation"] != true {
				t.Fatalf("final has_* flags = %v / %v, want true", contract["has_category"], contract["has_explanation"])
			}
			if contract["category"] != "cyber" {
				t.Fatalf("final category = %v, want cyber", contract["category"])
			}
			if contract["explanation"] != "synthetic-refusal-explanation" {
				t.Fatalf("final explanation = %v", contract["explanation"])
			}
		}
	}
	if !sawHop || !sawFinal {
		t.Fatalf("expected both a hop (server_fallback_hop=true) and a user-visible (false) refusal; hop=%v final=%v", sawHop, sawFinal)
	}
}

// TestNormalizeAPIRefusalOTLPParity proves the raw-wire (/v1/logs) form and the
// reviewed sample-event form yield the same refusal outcome contracts, so the
// live ingest path is not silently divergent.
func TestNormalizeAPIRefusalOTLPParity(t *testing.T) {
	sample, err := NormalizeEvents(readFixture(t, "claude-code-2.1.263-api-refusal-outcome.json"))
	if err != nil {
		t.Fatalf("sample normalise: %v", err)
	}
	wire := normalizeObservedOTLPLogs(t, "claude-code-2.1.263-api-refusal-outcome-otlp.json")
	if len(wire) != len(sample) {
		t.Fatalf("wire events = %d, sample events = %d", len(wire), len(sample))
	}
	for i := range sample {
		if wire[i].EventType != sample[i].EventType {
			t.Fatalf("event type parity mismatch at %d: %q vs %q", i, wire[i].EventType, sample[i].EventType)
		}
		if !reflect.DeepEqual(wire[i].ProviderExtensions["outcome_contract"], sample[i].ProviderExtensions["outcome_contract"]) {
			t.Fatalf("outcome_contract parity mismatch at %d:\n sample: %#v\n wire:   %#v",
				i, sample[i].ProviderExtensions["outcome_contract"], wire[i].ProviderExtensions["outcome_contract"])
		}
	}
}

// TestNormalizeNonRefusalEventsAreNotRefused is the negative guard: no
// non-api_refusal event is ever stamped with the refused outcome.
func TestNormalizeNonRefusalEventsAreNotRefused(t *testing.T) {
	events, err := NormalizeEvents(readFixture(t, "claude-code-2.1.251-otlp-events.json"))
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	for _, event := range events {
		contract, ok := event.ProviderExtensions["outcome_contract"].(map[string]any)
		if !ok {
			continue
		}
		if contract["status"] == "refused" {
			t.Fatalf("non-refusal event %q wrongly stamped refused: %#v", event.EventType, contract)
		}
	}
}
