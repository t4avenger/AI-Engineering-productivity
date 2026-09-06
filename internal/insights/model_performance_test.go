package insights

import (
	"testing"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestModelPerformanceFromEventsAggregatesOutcomeContracts(t *testing.T) {
	events := []canonical.Event{
		outcomeEvent(outcomeSpec{provider: "anthropic", tool: "claude-code", model: "claude-haiku-4-5", source: "provider_completion", status: "success", durationMs: 842, tokens: 16}),
		outcomeEvent(outcomeSpec{provider: "anthropic", tool: "claude-code", model: "claude-opus-4-8", source: "provider_completion", status: "failed", durationMs: 62462, retryAttempt: 1, errorCode: "rate_limit"}),
		outcomeEvent(outcomeSpec{provider: "openai", tool: "codex", model: "gpt-6-astra", source: "tool_result", status: "success", durationMs: 92}),
		outcomeEvent(outcomeSpec{provider: "openai", tool: "codex", model: "gpt-6-astra", source: "tool_result", status: "failed", durationMs: 87}),
		outcomeEvent(outcomeSpec{provider: "openai", tool: "codex", model: "gpt-6-astra", source: "provider_completion", status: "failed", durationMs: 268, retryAttempt: 2, errorCode: "http_401"}),
	}
	// Session lifecycle must not contribute.
	events = append(events, canonical.Event{
		Provider: "openai", Tool: "codex", EventType: "session.failed",
		ProviderExtensions: map[string]any{},
	})

	got := ModelPerformanceFromEvents(events)
	if len(got.Models) != 3 {
		t.Fatalf("expected 3 models, got %#v", got.Models)
	}
	astra := findModel(t, got, "gpt-6-astra")
	if astra.SampleSize != 3 || astra.Success != 1 || astra.Failed != 2 {
		t.Fatalf("gpt-6-astra scorecard mismatch: %#v", astra)
	}
	if astra.RetryRateState != "observed" || astra.RetryRate == nil || *astra.RetryRate <= 0 {
		t.Fatalf("expected observed retry rate, got %#v", astra)
	}
	if astra.ErrorCodes["http_401"] != 1 {
		t.Fatalf("expected http_401 error code, got %#v", astra.ErrorCodes)
	}
	if got.RankingAvailable || len(got.RankedModels) != 0 {
		t.Fatalf("ranking must be suppressed below sample size %d", MinRankingSampleSize)
	}
}

func TestModelPerformanceRankingGuardRequiresMinSampleSize(t *testing.T) {
	events := make([]canonical.Event, 0, MinRankingSampleSize*2)
	for i := 0; i < MinRankingSampleSize; i++ {
		events = append(events, outcomeEvent(outcomeSpec{provider: "openai", tool: "codex", model: "model-a", source: "provider_completion", status: "success", durationMs: 100, tokens: 10}))
		events = append(events, outcomeEvent(outcomeSpec{provider: "openai", tool: "codex", model: "model-b", source: "provider_completion", status: "failed", durationMs: 200, tokens: 10, errorCode: "err"}))
	}
	got := ModelPerformanceFromEvents(events)
	if !got.RankingAvailable {
		t.Fatal("expected ranking when every model meets min sample size")
	}
	if len(got.RankedModels) != 2 || got.RankedModels[0] != "model-a" {
		t.Fatalf("expected model-a ranked first, got %#v", got.RankedModels)
	}

	// Drop one observation from model-b so it falls below the guard.
	trimmed := events[:len(events)-1]
	blocked := ModelPerformanceFromEvents(trimmed)
	if blocked.RankingAvailable || len(blocked.RankedModels) != 0 {
		t.Fatalf("ranking must be suppressed when any model is below min sample size: %#v", blocked)
	}
}

func TestModelPerformanceIgnoresSessionStateWithoutContract(t *testing.T) {
	got := ModelPerformanceFromEvents([]canonical.Event{{
		Provider: "openai", Tool: "codex", EventType: "session.completed",
		Attributes: map[string]any{"outcome": "success"},
	}})
	if len(got.Models) != 0 {
		t.Fatalf("session state must not produce scorecard rows, got %#v", got.Models)
	}
}

type outcomeSpec struct {
	provider     string
	tool         string
	model        string
	source       string
	status       string
	durationMs   int64
	tokens       int64
	retryAttempt int64
	errorCode    string
}

func outcomeEvent(spec outcomeSpec) canonical.Event {
	contract := map[string]any{
		"source":     spec.source,
		"status":     spec.status,
		"confidence": "observed",
		"model":      spec.model,
	}
	if spec.durationMs > 0 {
		contract["duration_ms"] = spec.durationMs
	}
	if spec.tokens > 0 {
		contract["input_tokens"] = spec.tokens / 2
		contract["output_tokens"] = spec.tokens - spec.tokens/2
	}
	if spec.retryAttempt > 0 {
		contract["retry_attempt"] = spec.retryAttempt
	}
	if spec.errorCode != "" {
		contract["error_code"] = spec.errorCode
	}
	return canonical.Event{
		Provider: spec.provider, Tool: spec.tool, EventType: spec.source,
		ProviderExtensions: map[string]any{"outcome_contract": contract},
	}
}

func findModel(t *testing.T, got ModelPerformance, model string) ModelScorecard {
	t.Helper()
	for _, row := range got.Models {
		if row.Model == model {
			return row
		}
	}
	t.Fatalf("model %s not found in %#v", model, got.Models)
	return ModelScorecard{}
}
