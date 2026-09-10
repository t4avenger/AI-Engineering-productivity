// Package conformance is the capability-driven conformance suite every provider
// adapter must pass. Unlike a per-adapter unit test, it asserts cross-adapter
// contracts derived from the capability matrix
// (docs/integrations/capability-matrix.md, PRODUCT_MAP.md §15.2, §16.2): a
// signal a provider's matrix cell marks unsupported or unknown is never promoted
// into a populated first-class canonical field, and a supported signal is
// emitted with honest provenance. The suite refuses to let a shared adapter
// interface force unsupported providers into fake equivalence.
package conformance

import (
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/normalize/claude"
	"github.com/wayne/telemetryiq/internal/normalize/codex"
	"github.com/wayne/telemetryiq/internal/normalize/cursor"
)

// signalState mirrors a capability-matrix cell for one canonical, promotable
// signal. States above unknown require committed fixture evidence in the matrix.
type signalState string

const (
	stateSupported   signalState = "supported"
	statePartial     signalState = "partial"
	stateUnsupported signalState = "unsupported"
	stateUnknown     signalState = "unknown"
)

// promotable reports whether a signal in this state may appear as a populated
// first-class canonical field. Only supported and partial cells may; unknown and
// unsupported cells must never be promoted (absent evidence stays nil / "unknown",
// never a fabricated value). Provider-specific raw evidence may still be preserved
// under provider_extensions — that is governed by the redaction contract, not
// forbidden here.
func (s signalState) promotable() bool {
	return s == stateSupported || s == statePartial
}

// profile is the matrix-derived capability profile for one adapter's promotable
// canonical signals. Each field is that adapter's own column in
// docs/integrations/capability-matrix.md.
type profile struct {
	modelIdentity     signalState
	inputOutputTokens signalState
	cachedTokens      signalState
	reasoningTokens   signalState
	taskOutcome       signalState
	promptResponse    signalState
}

// eventFn and recordFn are the two capability-bounded entry points every adapter
// exposes. Codex consumes the inner OTLP logs payload while Claude Code consumes
// the full reviewed-fixture wrapper, so each adapter also carries the byte shape
// it expects (reviewed).
type eventFn func(data []byte) ([]canonical.Event, error)

type recordFn func(data []byte) ([]canonical.ModelInteraction, error)

// adapter is one provider adapter under conformance test.
type adapter struct {
	name    string
	profile profile
	events  eventFn
	records recordFn
	// reviewed returns the reviewed, committed fixture in the byte shape the
	// adapter consumes.
	reviewed func(t *testing.T) []byte
}

// fixtureReceivedAt is the fixed ingestion clock so Codex records whose
// timestamps are derived from receivedAt stay deterministic across runs.
var fixtureReceivedAt = time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

// adapters returns every provider adapter the suite runs against. Adding a
// provider here subjects it to the full capability contract.
func adapters() []adapter {
	return []adapter{codexAdapter(), claudeAdapter(), cursorAdapter()}
}

func codexAdapter() adapter {
	return adapter{
		name: "codex",
		// Matrix (docs/integrations/capability-matrix.md), Codex column.
		profile: profile{
			modelIdentity:     stateSupported,
			inputOutputTokens: statePartial,
			cachedTokens:      stateUnknown,
			reasoningTokens:   stateUnknown,
			taskOutcome:       statePartial,
			promptResponse:    stateUnsupported,
		},
		events: func(data []byte) ([]canonical.Event, error) {
			return codex.NormalizeLogs(data, fixtureReceivedAt)
		},
		records: func(data []byte) ([]canonical.ModelInteraction, error) {
			return codex.ExtractLogModelInteractions(data, fixtureReceivedAt)
		},
		reviewed: codexReviewedInput,
	}
}

func claudeAdapter() adapter {
	return adapter{
		name: "claude-code",
		// Matrix (docs/integrations/capability-matrix.md), Claude Code column.
		profile: profile{
			modelIdentity:     stateSupported,
			inputOutputTokens: stateSupported,
			cachedTokens:      stateSupported,
			reasoningTokens:   stateUnknown,
			taskOutcome:       statePartial,
			promptResponse:    stateUnsupported,
		},
		events:   claude.NormalizeEvents,
		records:  claude.ExtractModelInteractions,
		reviewed: claudeReviewedInput,
	}
}

func cursorAdapter() adapter {
	return adapter{
		name: "cursor-agent",
		// Matrix (docs/integrations/capability-matrix.md), Cursor column.
		profile: profile{
			modelIdentity:     statePartial,
			inputOutputTokens: stateSupported,
			cachedTokens:      stateSupported,
			reasoningTokens:   stateUnknown,
			taskOutcome:       statePartial,
			promptResponse:    stateUnsupported,
		},
		events:   cursor.Normalize,
		records:  cursor.ExtractModelInteractions,
		reviewed: cursorReviewedInput,
	}
}
