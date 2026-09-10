package normalize

import (
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestRequiredString(t *testing.T) {
	value := map[string]any{"present": " span ", "blank": "   ", "number": 3.0}
	if got, err := RequiredString(value, "present"); err != nil || got != " span " {
		t.Fatalf("RequiredString(present) = %q, %v; want %q, nil", got, err, " span ")
	}
	for _, key := range []string{"blank", "number", "absent"} {
		if _, err := RequiredString(value, key); err == nil {
			t.Fatalf("RequiredString(%q) = nil error; want error", key)
		}
	}
}

func TestOptionalString(t *testing.T) {
	value := map[string]any{"present": "  keep  ", "blank": "  ", "number": 3.0}
	got := OptionalString(value, "present")
	if got == nil || *got != "keep" {
		t.Fatalf("OptionalString(present) = %v; want pointer to %q", got, "keep")
	}
	for _, key := range []string{"blank", "number", "absent"} {
		if OptionalString(value, key) != nil {
			t.Fatalf("OptionalString(%q) = non-nil; want nil", key)
		}
	}
}

func TestObservedString(t *testing.T) {
	if text, ok := ObservedString("  claude-opus-4-8  "); !ok || text != "claude-opus-4-8" {
		t.Fatalf("ObservedString(model) = %q, %v; want %q, true", text, ok, "claude-opus-4-8")
	}
	for name, value := range map[string]any{"blank": "   ", "number": 3.0, "absent": nil} {
		if text, ok := ObservedString(value); ok || text != "unknown" {
			t.Fatalf("ObservedString(%s) = %q, %v; want \"unknown\", false", name, text, ok)
		}
	}
}

func TestProviderNativeSessionIDRetainsLocalProviderID(t *testing.T) {
	if got := ProviderNativeSessionID("codex:", " synthetic-session "); got != "codex:synthetic-session" {
		t.Fatalf("session id = %q, want provider-native ID", got)
	}
}

// TestProviderNativeSessionIDPassesRawValueThrough asserts the no-hiding
// invariant (issue #88): the raw provider-native value is retained verbatim,
// never fingerprinted or redacted, regardless of how secret-like it looks. Only
// an absent value falls back to the explicit "unknown" sentinel.
func TestProviderNativeSessionIDPassesRawValueThrough(t *testing.T) {
	if got := ProviderNativeSessionID("claude-code:", "token=synthetic-secret"); got != "claude-code:token=synthetic-secret" {
		t.Fatalf("session id = %q, want raw value retained", got)
	}
	if got := ProviderNativeSessionID("cursor-agent:", "   "); got != "cursor-agent:unknown" {
		t.Fatalf("empty session id = %q, want unknown sentinel", got)
	}
}

func TestOptionalTokenCount(t *testing.T) {
	cases := map[string]struct {
		value any
		want  *int64
	}{
		"integer string":  {value: "3077", want: int64Ptr(3077)},
		"json number":     {value: 6258.0, want: int64Ptr(6258)},
		"explicit zero":   {value: "0", want: int64Ptr(0)},
		"negative string": {value: "-1", want: nil},
		"negative number": {value: -1.0, want: nil},
		"fractional":      {value: 1.5, want: nil},
		"unparseable":     {value: "abc", want: nil},
		"wrong type":      {value: true, want: nil},
		"absent":          {value: nil, want: nil},
	}
	for name, tc := range cases {
		got := OptionalTokenCount(tc.value)
		switch {
		case tc.want == nil && got != nil:
			t.Fatalf("OptionalTokenCount(%s) = %d; want nil", name, *got)
		case tc.want != nil && (got == nil || *got != *tc.want):
			t.Fatalf("OptionalTokenCount(%s) = %v; want %d", name, got, *tc.want)
		}
	}
}

func TestUnknownFields(t *testing.T) {
	value := map[string]any{"model": "x", "input_tokens": 1.0, "extra": "keep"}
	unknown := UnknownFields(value, "model", "input_tokens")
	if len(unknown) != 1 || unknown["extra"] != "keep" {
		t.Fatalf("UnknownFields = %v; want only {extra: keep}", unknown)
	}
	// Known keys are excluded even when knownKeys names an absent key.
	if got := UnknownFields(value, "model", "input_tokens", "absent", "extra"); len(got) != 0 {
		t.Fatalf("UnknownFields(all) = %v; want empty", got)
	}
}

func TestInteractionProvenance(t *testing.T) {
	tokens := int64Ptr(1)
	cases := map[string]struct {
		modelObserved bool
		input, output *int64
		want          canonical.Provenance
	}{
		"model and input observed":  {modelObserved: true, input: tokens, want: canonical.ProvenanceObserved},
		"model and output observed": {modelObserved: true, output: tokens, want: canonical.ProvenanceObserved},
		"model without tokens":      {modelObserved: true, want: canonical.ProvenanceUnknown},
		"tokens without model":      {input: tokens, want: canonical.ProvenanceUnknown},
	}
	for name, tc := range cases {
		if got := InteractionProvenance(tc.modelObserved, tc.input, tc.output); got != tc.want {
			t.Fatalf("InteractionProvenance(%s) = %q; want %q", name, got, tc.want)
		}
	}
}

func TestCorrelateEventsOrdersAndDedupes(t *testing.T) {
	base := time.Date(2026, 8, 31, 13, 37, 0, 0, time.UTC)
	events := []canonical.Event{
		{EventID: "b", OccurredAt: base.Add(time.Second)},
		{EventID: "a", OccurredAt: base},
		{EventID: "a", OccurredAt: base}, // duplicate ID collapsed
	}
	got := CorrelateEvents(events)
	if len(got) != 2 {
		t.Fatalf("CorrelateEvents length = %d; want 2 (deduped)", len(got))
	}
	if got[0].EventID != "a" || got[1].EventID != "b" {
		t.Fatalf("CorrelateEvents order = %q, %q; want a, b", got[0].EventID, got[1].EventID)
	}
}

func TestCorrelateEventsTieBreakers(t *testing.T) {
	at := time.Date(2026, 8, 31, 13, 37, 0, 0, time.UTC)
	later := at.Add(time.Hour)
	// Equal OccurredAt and EventID: EventType then ReceivedAt break the tie.
	events := []canonical.Event{
		{EventID: "x", EventType: "b", OccurredAt: at, ReceivedAt: later},
		{EventID: "x", EventType: "a", OccurredAt: at, ReceivedAt: at},
	}
	got := CorrelateEvents(events)
	// Both share EventID "x", so only the first after stable sort survives dedup.
	if len(got) != 1 || got[0].EventType != "a" {
		t.Fatalf("CorrelateEvents tie-break = %+v; want single event_type a", got)
	}
}

func TestCorrelateModelInteractionsOrdersAndDedupes(t *testing.T) {
	base := time.Date(2026, 8, 31, 13, 37, 0, 0, time.UTC)
	records := []canonical.ModelInteraction{
		{RequestID: "b", StartedAt: base.Add(time.Second)},
		{RequestID: "a", StartedAt: base},
		{RequestID: "a", StartedAt: base}, // duplicate request ID collapsed
	}
	got := CorrelateModelInteractions(records)
	if len(got) != 2 {
		t.Fatalf("CorrelateModelInteractions length = %d; want 2 (deduped)", len(got))
	}
	if got[0].RequestID != "a" || got[1].RequestID != "b" {
		t.Fatalf("CorrelateModelInteractions order = %q, %q; want a, b", got[0].RequestID, got[1].RequestID)
	}
}

func int64Ptr(value int64) *int64 { return &value }
