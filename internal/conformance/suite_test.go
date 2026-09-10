package conformance

import (
	"testing"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// forEachAdapter runs fn as a parallel subtest for every adapter, factoring out
// the shared adapter-iteration boilerplate (and one level of nesting) so each
// test body stays flat.
func forEachAdapter(t *testing.T, fn func(t *testing.T, a adapter)) {
	t.Helper()
	for _, a := range adapters() {
		a := a
		t.Run(a.name, func(t *testing.T) {
			t.Parallel()
			fn(t, a)
		})
	}
}

// recordsFor replays an adapter's reviewed fixture through its record entry
// point, requiring at least one record so downstream assertions are not
// vacuously satisfied by an empty slice.
func recordsFor(t *testing.T, a adapter) []canonical.ModelInteraction {
	t.Helper()
	records, err := a.records(a.reviewed(t))
	if err != nil {
		t.Fatalf("%s: extract records: %v", a.name, err)
	}
	if len(records) == 0 {
		t.Fatalf("%s: reviewed fixture produced no records", a.name)
	}
	return records
}

// TestUnsupportedSignalsNotPromoted is the core capability contract: a signal a
// provider's matrix cell marks unknown or unsupported must never surface as a
// populated first-class canonical field. Assertions are driven off each
// adapter's declared profile, so a matrix cell and its enforcement cannot drift
// apart silently.
func TestUnsupportedSignalsNotPromoted(t *testing.T) {
	t.Parallel()
	forEachAdapter(t, func(t *testing.T, a adapter) {
		for i, record := range recordsFor(t, a) {
			assertUnsupportedNotPromoted(t, a, i, record)
		}
	})
}

// assertUnsupportedNotPromoted checks a single record against its adapter's
// profile: any signal that is not promotable must be absent (nil token, or the
// explicit "unknown" outcome), never a fabricated value.
func assertUnsupportedNotPromoted(t *testing.T, a adapter, i int, record canonical.ModelInteraction) {
	t.Helper()
	if !a.profile.reasoningTokens.promotable() && record.ReasoningTokens != nil {
		t.Errorf("record %d: reasoning tokens are %q for %s but promoted to %d", i, a.profile.reasoningTokens, a.name, *record.ReasoningTokens)
	}
	if !a.profile.cachedTokens.promotable() && record.CachedInputTokens != nil {
		t.Errorf("record %d: cached tokens are %q for %s but promoted to %d", i, a.profile.cachedTokens, a.name, *record.CachedInputTokens)
	}
	if a.profile.taskOutcome.promotable() {
		return
	}
	if record.Result != "unknown" {
		t.Errorf("record %d: task outcome is %q for %s but result promoted to %q", i, a.profile.taskOutcome, a.name, record.Result)
	}
	if record.ErrorCode != nil {
		t.Errorf("record %d: task outcome is %q for %s but error code promoted to %q", i, a.profile.taskOutcome, a.name, *record.ErrorCode)
	}
}

// TestSupportedSignalsPromotedWithProvenance is the converse contract: a signal
// the matrix marks supported/partial is actually promoted, and when both model
// identity and tokens are observed the record's provenance says so rather than
// leaving the promotion unattributed. It requires at least one such record so a
// provider cannot pass by simply emitting nothing.
func TestSupportedSignalsPromotedWithProvenance(t *testing.T) {
	t.Parallel()
	forEachAdapter(t, func(t *testing.T, a adapter) {
		observed := 0
		for i, record := range recordsFor(t, a) {
			if assertSupportedPromoted(t, a, i, record) {
				observed++
			}
		}
		if promotesModelAndTokens(a.profile) && observed == 0 {
			t.Errorf("%s marks model and tokens promotable but no record promoted both with observed provenance", a.name)
		}
	})
}

// promotesModelAndTokens reports whether a profile promotes both model identity
// and token counts, so the suite can require at least one honestly-attributed
// record.
func promotesModelAndTokens(p profile) bool {
	return p.modelIdentity.promotable() && p.inputOutputTokens.promotable()
}

// assertSupportedPromoted checks that a supported signal is promoted, returning
// whether this record promoted both model and tokens with observed provenance.
func assertSupportedPromoted(t *testing.T, a adapter, i int, record canonical.ModelInteraction) bool {
	t.Helper()
	if a.profile.modelIdentity.promotable() && record.Model == "" {
		t.Errorf("record %d: model identity is %q for %s but no model was promoted", i, a.profile.modelIdentity, a.name)
	}
	if record.Model == "" || (record.InputTokens == nil && record.OutputTokens == nil) {
		return false
	}
	if record.Provenance != canonical.ProvenanceObserved {
		t.Errorf("record %d: %s promoted observed model+tokens but provenance is %q", i, a.name, record.Provenance)
	}
	return true
}

// TestUnknownTaskOutcomeReportedExplicitly asserts an unknown signal is reported
// as an explicit unknown, not silently dropped: the record's task-boundary
// confidence under provider_extensions is "unknown". An honest "unknown" is a
// first-class output of the capability-bounded design, not an omission.
func TestUnknownTaskOutcomeReportedExplicitly(t *testing.T) {
	t.Parallel()
	forEachAdapter(t, func(t *testing.T, a adapter) {
		if a.profile.taskOutcome.promotable() {
			t.Skipf("%s promotes task outcome; explicit-unknown contract does not apply", a.name)
		}
		for i, record := range recordsFor(t, a) {
			confidence := taskBoundaryConfidence(t, a.name, i, record.ProviderExtensions)
			if confidence != "unknown" {
				t.Errorf("record %d: %s task outcome is unknown but task_boundary confidence is %q", i, a.name, confidence)
			}
		}
	})
}

// taskBoundaryConfidence navigates provider_extensions.correlation.task_boundary
// .confidence, failing the test if the honest-unknown structure is absent.
func taskBoundaryConfidence(t *testing.T, name string, index int, extensions map[string]any) string {
	t.Helper()
	correlation, ok := extensions["correlation"].(map[string]any)
	if !ok {
		t.Fatalf("record %d: %s has no provider_extensions.correlation", index, name)
	}
	boundary, ok := correlation["task_boundary"].(map[string]any)
	if !ok {
		t.Fatalf("record %d: %s has no correlation.task_boundary", index, name)
	}
	confidence, ok := boundary["confidence"].(string)
	if !ok {
		t.Fatalf("record %d: %s task_boundary has no confidence", index, name)
	}
	return confidence
}
