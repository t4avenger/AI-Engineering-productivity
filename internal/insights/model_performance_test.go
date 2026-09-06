package insights

import (
	"testing"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestModelPerformanceFromEventsAggregatesOutcomeContracts(t *testing.T) {
	events := []canonical.Event{
		outcomeEvent("anthropic", "claude-code", "claude-haiku-4-5", "provider_completion", "success", 842, 16, 0, ""),
		outcomeEvent("anthropic", "claude-code", "claude-opus-4-8", "provider_completion", "failed", 62462, 0, 1, "rate_limit"),
		outcomeEvent("openai", "codex", "gpt-6-astra", "tool_result", "success", 92, 0, 0, ""),
		outcomeEvent("openai", "codex", "gpt-6-astra", "tool_result", "failed", 87, 0, 0, ""),
		outcomeEvent("openai", "codex", "gpt-6-astra", "provider_completion", "failed", 268, 0, 2, "http_401"),
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
		events = append(events, outcomeEvent("openai", "codex", "model-a", "provider_completion", "success", 100, 10, 0, ""))
		events = append(events, outcomeEvent("openai", "codex", "model-b", "provider_completion", "failed", 200, 10, 0, "err"))
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

func outcomeEvent(provider, tool, model, source, status string, durationMs, tokens, retryAttempt int64, errorCode string) canonical.Event {
	contract := map[string]any{
		"source":     source,
		"status":     status,
		"confidence": "observed",
		"model":      model,
	}
	if durationMs > 0 {
		contract["duration_ms"] = durationMs
	}
	if tokens > 0 {
		contract["input_tokens"] = tokens / 2
		contract["output_tokens"] = tokens - tokens/2
	}
	if retryAttempt > 0 {
		contract["retry_attempt"] = retryAttempt
	}
	if errorCode != "" {
		contract["error_code"] = errorCode
	}
	return canonical.Event{
		Provider: provider, Tool: tool, EventType: source,
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
