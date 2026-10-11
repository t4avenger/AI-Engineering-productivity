package insights

import (
	"testing"
	"time"

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

func TestModelPerformanceSummarisesObservationScopedEfficiencyMetrics(t *testing.T) {
	events := []canonical.Event{
		performanceEvent("delta-1", "codex.turn.e2e_duration_ms", "turn_latency", "histogram", 1, time.Unix(1, 0), map[string]any{
			"sample_count": int64(1), "sum": float64(1200), "min": float64(1200), "max": float64(1200),
		}),
		performanceEvent("delta-2", "codex.turn.e2e_duration_ms", "turn_latency", "histogram", 1, time.Unix(2, 0), map[string]any{
			"sample_count": int64(2), "sum": float64(3000), "min": float64(1400), "max": float64(1600),
		}),
		performanceEvent("counter", "codex.turn.memory", "turn_efficiency", "sum", 1, time.Unix(3, 0), map[string]any{"value": int64(1)}),
	}

	got := ModelPerformanceFromEvents(events)
	if len(got.Models) != 0 {
		t.Fatalf("efficiency telemetry must not create outcome scorecards: %#v", got.Models)
	}
	if len(got.EfficiencyMetrics) != 2 {
		t.Fatalf("efficiency metrics = %#v", got.EfficiencyMetrics)
	}
	turn := findEfficiencyMetric(t, got, "codex.turn.e2e_duration_ms")
	assertEfficiencyHistogram(t, turn)
	memory := findEfficiencyMetric(t, got, "codex.turn.memory")
	if memory.Value == nil || *memory.Value != 1 || memory.Model != "gpt-6.1-sol-synthetic" {
		t.Fatalf("counter summary = %#v", memory)
	}
}

func assertEfficiencyHistogram(t *testing.T, turn PerformanceMetricSummary) {
	t.Helper()
	if turn.MetricName != "codex.turn.e2e_duration_ms" || turn.SampleCount == nil || *turn.SampleCount != 3 {
		t.Fatalf("delta histogram count = %#v", turn)
	}
	if turn.Total == nil || *turn.Total != 4200 || turn.Average == nil || *turn.Average != 1400 {
		t.Fatalf("delta histogram summary = %#v", turn)
	}
	if turn.Min == nil || *turn.Min != 1200 || turn.Max == nil || *turn.Max != 1600 || turn.Scope != "observation" {
		t.Fatalf("histogram bounds/scope = %#v", turn)
	}
}

func TestModelPerformanceKeepsLatestCumulativeMetric(t *testing.T) {
	events := []canonical.Event{
		performanceEvent("cumulative-new", "codex.websocket.request", "transport_activity", "sum", 2, time.Unix(2, 0), map[string]any{"value": int64(5)}),
		performanceEvent("cumulative-old", "codex.websocket.request", "transport_activity", "sum", 2, time.Unix(1, 0), map[string]any{"value": int64(3)}),
	}
	got := ModelPerformanceFromEvents(events)
	if len(got.EfficiencyMetrics) != 1 || got.EfficiencyMetrics[0].Value == nil || *got.EfficiencyMetrics[0].Value != 5 {
		t.Fatalf("cumulative metric must use latest datapoint: %#v", got.EfficiencyMetrics)
	}
}

func performanceEvent(id, name, family, kind string, temporality int64, occurredAt time.Time, measures map[string]any) canonical.Event {
	metric := map[string]any{
		"name": name, "family": family, "kind": kind, "unit": "ms", "scope": "observation",
		"aggregation_temporality": temporality, "dimensions": map[string]any{"model": "gpt-6.1-sol-synthetic"},
	}
	for key, value := range measures {
		metric[key] = value
	}
	return canonical.Event{
		EventID: id, OccurredAt: occurredAt, Provider: "openai", Tool: "codex",
		Attributes:         map[string]any{"model": "gpt-6.1-sol-synthetic"},
		ProviderExtensions: map[string]any{"performance_metric": metric},
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

func findEfficiencyMetric(t *testing.T, got ModelPerformance, metricName string) PerformanceMetricSummary {
	t.Helper()
	for _, row := range got.EfficiencyMetrics {
		if row.MetricName == metricName {
			return row
		}
	}
	t.Fatalf("efficiency metric %s not found in %#v", metricName, got.EfficiencyMetrics)
	return PerformanceMetricSummary{}
}
