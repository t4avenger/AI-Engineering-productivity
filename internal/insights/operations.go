package insights

import (
	"sort"
	"strconv"
	"strings"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

type OperationStats struct {
	Totals     OperationTotals         `json:"totals"`
	ByCategory []OperationCategoryStat `json:"by_category"`
	ByOutcome  []OperationOutcomeStat  `json:"by_outcome"`
	Notes      []string                `json:"notes"`
}

type OperationTotals struct {
	TotalOperations       int      `json:"total_operations"`
	DurationObservedCount int      `json:"duration_observed_count"`
	AverageDurationMs     *float64 `json:"average_duration_ms"`
}

type OperationCategoryStat struct {
	Category              string   `json:"category"`
	Count                 int      `json:"count"`
	DurationObservedCount int      `json:"duration_observed_count"`
	AverageDurationMs     *float64 `json:"average_duration_ms"`
}

type OperationOutcomeStat struct {
	Outcome string `json:"outcome"`
	Count   int    `json:"count"`
}

type durationAccumulator struct {
	count int
	sum   int64
}

func OperationStatsFromOperations(operations []canonical.Operation) OperationStats {
	stats := OperationStats{Notes: []string{"Durations are averaged only when a provider reports duration_ms; unavailable durations are not treated as zero."}}
	byCategory := map[string]int{}
	byOutcome := map[string]int{}
	durations := map[string]durationAccumulator{}
	var totalDuration durationAccumulator
	for _, operation := range operations {
		stats.Totals.TotalOperations++
		category := strings.TrimSpace(string(operation.Category))
		if category == "" {
			category = string(canonical.OperationCategoryUnknown)
		}
		outcome := strings.TrimSpace(operation.Outcome)
		if outcome == "" {
			outcome = "unknown"
		}
		byCategory[category]++
		byOutcome[outcome]++
		if duration, ok := operationDurationMs(operation); ok {
			acc := durations[category]
			acc.count++
			acc.sum += duration
			durations[category] = acc
			totalDuration.count++
			totalDuration.sum += duration
		}
	}
	stats.Totals.DurationObservedCount = totalDuration.count
	stats.Totals.AverageDurationMs = averageDuration(totalDuration)
	for _, category := range sortedCountKeys(byCategory) {
		acc := durations[category]
		stats.ByCategory = append(stats.ByCategory, OperationCategoryStat{Category: category, Count: byCategory[category], DurationObservedCount: acc.count, AverageDurationMs: averageDuration(acc)})
	}
	for _, outcome := range sortedCountKeys(byOutcome) {
		stats.ByOutcome = append(stats.ByOutcome, OperationOutcomeStat{Outcome: outcome, Count: byOutcome[outcome]})
	}
	return stats
}

func operationDurationMs(operation canonical.Operation) (int64, bool) {
	for _, path := range [][]string{{"tool_call", "duration_ms"}, {"sandbox_outcome", "duration_ms"}, {"event", "duration_ms"}} {
		if duration, ok := nestedDuration(operation.ProviderExtensions, path...); ok {
			return duration, true
		}
	}
	return 0, false
}

func nestedDuration(values map[string]any, path ...string) (int64, bool) {
	var current any = values
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return 0, false
		}
		current, ok = object[key]
		if !ok {
			return 0, false
		}
	}
	return durationValue(current)
}

func durationValue(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func averageDuration(acc durationAccumulator) *float64 {
	if acc.count == 0 {
		return nil
	}
	avg := float64(acc.sum) / float64(acc.count)
	return &avg
}

func sortedCountKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
