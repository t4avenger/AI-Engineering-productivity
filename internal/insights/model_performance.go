package insights

import (
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

const (
	ModelPerformanceSchemaVersion = "0.1.0"
	// MinRankingSampleSize is the minimum per-model sample size required before
	// any cross-model ranking list may be emitted.
	MinRankingSampleSize = 10
)

// ModelPerformance is the per-model outcome-contract scorecard.
type ModelPerformance struct {
	SchemaVersion    string           `json:"schema_version"`
	Models           []ModelScorecard `json:"models"`
	RankingAvailable bool             `json:"ranking_available"`
	RankedModels     []string         `json:"ranked_models"`
	Notes            []string         `json:"notes"`
}

// ModelScorecard holds raw metrics for one model with sample size.
type ModelScorecard struct {
	Model                  string         `json:"model"`
	Provider               string         `json:"provider"`
	Tool                   string         `json:"tool"`
	SampleSize             int            `json:"sample_size"`
	Success                int            `json:"success"`
	Failed                 int            `json:"failed"`
	Abandoned              int            `json:"abandoned"`
	RetryRate              *float64       `json:"retry_rate"`
	RetryRateState         string         `json:"retry_rate_state"`
	ErrorCodes             map[string]int `json:"error_codes"`
	TokensPerCompletedTask *float64       `json:"tokens_per_completed_task"`
	LatencyP50Ms           *float64       `json:"latency_p50_ms"`
	LatencyP95Ms           *float64       `json:"latency_p95_ms"`
	OutcomeContractSources []string       `json:"outcome_contract_sources"`
}

type modelKey struct {
	provider string
	tool     string
	model    string
}

type modelAccumulator struct {
	success, failed, abandoned int
	retries                    int
	retryObserved              bool
	errorCodes                 map[string]int
	completedTokens            []float64
	latencies                  []float64
	sources                    map[string]struct{}
}

// ModelPerformanceFromEvents derives the scorecard from sanitised canonical
// events that carry provider_extensions.outcome_contract. Session lifecycle
// state is never used as an outcome.
func ModelPerformanceFromEvents(events []canonical.Event) ModelPerformance {
	models := scorecardsFromAccumulators(accumulateContracts(events))
	rankingAvailable, ranked := rankingFor(models)
	return ModelPerformance{
		SchemaVersion:    ModelPerformanceSchemaVersion,
		Models:           models,
		RankingAvailable: rankingAvailable,
		RankedModels:     ranked,
		Notes: []string{
			"Metrics are built only from provider_extensions.outcome_contract signals, never from raw session lifecycle state.",
			fmt.Sprintf("Cross-model ranking is suppressed unless every compared model has sample_size >= %d.", MinRankingSampleSize),
			"Workload and task-mix caveats apply; raw counts are not a productivity score.",
		},
	}
}

func accumulateContracts(events []canonical.Event) map[modelKey]*modelAccumulator {
	acc := map[modelKey]*modelAccumulator{}
	for _, event := range events {
		contract, ok := outcomeContract(event)
		if !ok {
			continue
		}
		model, _ := contract["model"].(string)
		if model == "" || model == "unknown" {
			continue
		}
		status, _ := contract["status"].(string)
		key := modelKey{provider: event.Provider, tool: event.Tool, model: model}
		row := accumulatorFor(acc, key)
		if !applyOutcomeStatus(row, contract, status) {
			continue
		}
		recordContractMeta(row, contract)
	}
	return acc
}

func accumulatorFor(acc map[modelKey]*modelAccumulator, key modelKey) *modelAccumulator {
	row, exists := acc[key]
	if exists {
		return row
	}
	row = &modelAccumulator{errorCodes: map[string]int{}, sources: map[string]struct{}{}}
	acc[key] = row
	return row
}

func applyOutcomeStatus(row *modelAccumulator, contract map[string]any, status string) bool {
	switch status {
	case "success":
		row.success++
		if tokens := contractTokens(contract); tokens != nil {
			row.completedTokens = append(row.completedTokens, *tokens)
		}
	case "failed":
		row.failed++
	case "abandoned":
		row.abandoned++
	default:
		return false
	}
	return true
}

func recordContractMeta(row *modelAccumulator, contract map[string]any) {
	if source, _ := contract["source"].(string); source != "" {
		row.sources[source] = struct{}{}
	}
	if code, _ := contract["error_code"].(string); code != "" {
		row.errorCodes[code]++
	}
	if attempt := contractInt(contract["retry_attempt"]); attempt != nil && *attempt > 0 {
		row.retryObserved = true
		row.retries++
	}
	if duration := contractFloat(contract["duration_ms"]); duration != nil {
		row.latencies = append(row.latencies, *duration)
	}
}

func scorecardsFromAccumulators(acc map[modelKey]*modelAccumulator) []ModelScorecard {
	models := make([]ModelScorecard, 0, len(acc))
	for key, row := range acc {
		sample := row.success + row.failed + row.abandoned
		card := ModelScorecard{
			Model:                  key.model,
			Provider:               key.provider,
			Tool:                   key.tool,
			SampleSize:             sample,
			Success:                row.success,
			Failed:                 row.failed,
			Abandoned:              row.abandoned,
			RetryRateState:         "unavailable",
			ErrorCodes:             row.errorCodes,
			OutcomeContractSources: sortedKeys(row.sources),
		}
		if row.retryObserved {
			rate := float64(row.retries) / float64(sample)
			card.RetryRate = &rate
			card.RetryRateState = "observed"
		}
		if len(row.completedTokens) > 0 {
			mean := mean(row.completedTokens)
			card.TokensPerCompletedTask = &mean
		}
		card.LatencyP50Ms = percentile(row.latencies, 0.50)
		card.LatencyP95Ms = percentile(row.latencies, 0.95)
		models = append(models, card)
	}
	sort.Slice(models, func(i, j int) bool {
		left, right := models[i], models[j]
		return left.Provider+"\x00"+left.Tool+"\x00"+left.Model < right.Provider+"\x00"+right.Tool+"\x00"+right.Model
	})
	return models
}

func rankingFor(models []ModelScorecard) (bool, []string) {
	if len(models) < 2 {
		return false, []string{}
	}
	for _, model := range models {
		if model.SampleSize < MinRankingSampleSize {
			return false, []string{}
		}
	}
	ordered := append([]ModelScorecard(nil), models...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return successRate(ordered[i]) > successRate(ordered[j])
	})
	ranked := make([]string, 0, len(ordered))
	for _, model := range ordered {
		ranked = append(ranked, model.Model)
	}
	return true, ranked
}

func outcomeContract(event canonical.Event) (map[string]any, bool) {
	raw, ok := event.ProviderExtensions["outcome_contract"].(map[string]any)
	if !ok || raw == nil {
		return nil, false
	}
	return raw, true
}

func contractTokens(contract map[string]any) *float64 {
	input := contractFloat(contract["input_tokens"])
	output := contractFloat(contract["output_tokens"])
	if input == nil && output == nil {
		return nil
	}
	total := 0.0
	if input != nil {
		total += *input
	}
	if output != nil {
		total += *output
	}
	return &total
}

func contractFloat(value any) *float64 {
	switch typed := value.(type) {
	case float64:
		return &typed
	case int64:
		v := float64(typed)
		return &v
	case int:
		v := float64(typed)
		return &v
	case string:
		if parsed, err := strconv.ParseFloat(typed, 64); err == nil {
			return &parsed
		}
	}
	return nil
}

func contractInt(value any) *int64 {
	switch typed := value.(type) {
	case float64:
		v := int64(typed)
		return &v
	case int64:
		return &typed
	case int:
		v := int64(typed)
		return &v
	case string:
		if parsed, err := strconv.ParseInt(typed, 10, 64); err == nil {
			return &parsed
		}
	}
	return nil
}

func mean(values []float64) float64 {
	sum := 0.0
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

func percentile(values []float64, p float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	if len(sorted) == 1 {
		v := sorted[0]
		return &v
	}
	rank := p * float64(len(sorted)-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	if lower == upper {
		v := sorted[lower]
		return &v
	}
	weight := rank - float64(lower)
	v := sorted[lower]*(1-weight) + sorted[upper]*weight
	return &v
}

func successRate(card ModelScorecard) float64 {
	if card.SampleSize == 0 {
		return 0
	}
	return float64(card.Success) / float64(card.SampleSize)
}

func sortedKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
