package insights

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

const ContextWasteSchemaVersion = "0.1.0"

// ContextWasteThresholds configures the §13.10 context-waste insight.
type ContextWasteThresholds struct {
	CachedContextRatioThreshold float64 `json:"cached_context_ratio_threshold"`
	InputTokenGrowthThreshold   float64 `json:"input_token_growth_threshold"`
}

type ContextWaste struct {
	SchemaVersion string                 `json:"schema_version"`
	Thresholds    ContextWasteThresholds `json:"thresholds"`
	Totals        ContextWasteTotals     `json:"totals"`
	Sessions      []ContextWasteSession  `json:"sessions"`
	Notes         []string               `json:"notes"`
}

type ContextWasteTotals struct {
	Sessions                 int `json:"sessions"`
	TriggeredSessions        int `json:"triggered_sessions"`
	CachedRatioObserved      int `json:"cached_ratio_observed_sessions"`
	InputTokenGrowthObserved int `json:"input_token_growth_observed_sessions"`
	BothMetricsUnavailable   int `json:"both_metrics_unavailable_sessions"`
}

type ContextWasteSession struct {
	Provider                string               `json:"provider"`
	Tool                    string               `json:"tool"`
	SessionID               string               `json:"session_id"`
	CachedContextRatio      *float64             `json:"cached_context_ratio"`
	CachedContextRatioState string               `json:"cached_context_ratio_state"`
	InputTokenGrowth        *float64             `json:"input_token_growth"`
	InputTokenGrowthState   string               `json:"input_token_growth_state"`
	Triggered               bool                 `json:"triggered"`
	TriggerReasons          []string             `json:"trigger_reasons"`
	Evidence                ContextWasteEvidence `json:"evidence"`
}

type ContextWasteEvidence struct {
	TokenPoints              int    `json:"token_points"`
	InputTokenSamples        int    `json:"input_token_samples"`
	FirstInputTokens         *int64 `json:"first_input_tokens"`
	MaxInputTokens           *int64 `json:"max_input_tokens"`
	OverlapInputTokens       *int64 `json:"overlap_input_tokens"`
	OverlapCachedInputTokens *int64 `json:"overlap_cached_input_tokens"`
}

type tokenPoint struct {
	occurredAt time.Time
	eventID    string
	input      *int64
	cached     *int64
	provider   string
	tool       string
	sessionID  string
}

// ContextWasteFromEvents derives the §13.10 context-waste insight from retained
// sanitised canonical events. It triggers when cached-context ratio or input-token
// growth meets/exceeds the configured threshold, and it is explicit about
// unavailable metrics rather than fabricating zeros.
func ContextWasteFromEvents(events []canonical.Event, thresholds ContextWasteThresholds) ContextWaste {
	bySession := map[string][]tokenPoint{}
	meta := map[string]struct {
		provider string
		tool     string
	}{}

	for _, event := range events {
		input, cached, ok := tokenSignals(event)
		if !ok {
			continue
		}
		point := tokenPoint{
			occurredAt: event.OccurredAt,
			eventID:    event.EventID,
			input:      input,
			cached:     cached,
			provider:   event.Provider,
			tool:       event.Tool,
			sessionID:  event.SessionID,
		}
		bySession[event.SessionID] = append(bySession[event.SessionID], point)
		if _, exists := meta[event.SessionID]; !exists {
			meta[event.SessionID] = struct {
				provider string
				tool     string
			}{provider: event.Provider, tool: event.Tool}
		}
	}

	result := ContextWaste{
		SchemaVersion: ContextWasteSchemaVersion,
		Thresholds:    thresholds,
		Sessions:      []ContextWasteSession{},
		Notes: []string{
			"Cached-context ratio uses only events where both input tokens and cached-input tokens are observed; absent values are reported as unavailable.",
			"Input-token growth uses the earliest observed input-token sample and the maximum observed input-token sample within the same session.",
			"Triggered sessions indicate potential context pressure; they are not a productivity score and do not imply causation of service degradation.",
		},
	}

	for sessionID, points := range bySession {
		sort.Slice(points, func(i, j int) bool {
			left, right := points[i], points[j]
			if !left.occurredAt.Equal(right.occurredAt) {
				return left.occurredAt.Before(right.occurredAt)
			}
			return left.eventID < right.eventID
		})
		row := contextWasteSession(meta[sessionID].provider, meta[sessionID].tool, sessionID, points, thresholds)
		result.Sessions = append(result.Sessions, row)
	}

	sort.Slice(result.Sessions, func(i, j int) bool {
		left, right := result.Sessions[i], result.Sessions[j]
		if left.Provider != right.Provider {
			return left.Provider < right.Provider
		}
		if left.Tool != right.Tool {
			return left.Tool < right.Tool
		}
		return left.SessionID < right.SessionID
	})
	result.Totals = contextWasteTotals(result.Sessions)
	return result
}

func contextWasteTotals(rows []ContextWasteSession) ContextWasteTotals {
	totals := ContextWasteTotals{Sessions: len(rows)}
	for _, row := range rows {
		if row.Triggered {
			totals.TriggeredSessions++
		}
		if row.CachedContextRatioState == "observed" {
			totals.CachedRatioObserved++
		}
		if row.InputTokenGrowthState == "observed" {
			totals.InputTokenGrowthObserved++
		}
		if row.CachedContextRatioState != "observed" && row.InputTokenGrowthState != "observed" {
			totals.BothMetricsUnavailable++
		}
	}
	return totals
}

func contextWasteSession(provider, tool, sessionID string, points []tokenPoint, thresholds ContextWasteThresholds) ContextWasteSession {
	row := ContextWasteSession{
		Provider:                provider,
		Tool:                    tool,
		SessionID:               sessionID,
		CachedContextRatioState: "unavailable",
		InputTokenGrowthState:   "unavailable",
		TriggerReasons:          []string{},
		Evidence: ContextWasteEvidence{
			TokenPoints:       len(points),
			InputTokenSamples: 0,
		},
	}

	inputSamples, firstInput, maxInput := inputStats(points)
	overlapObserved, overlapInput, overlapCached := overlapSums(points)

	row.Evidence.InputTokenSamples = inputSamples
	row.Evidence.FirstInputTokens = firstInput
	row.Evidence.MaxInputTokens = maxInput
	if overlapObserved {
		row.Evidence.OverlapInputTokens = &overlapInput
		row.Evidence.OverlapCachedInputTokens = &overlapCached
	}

	if ratio, ok := cachedContextRatio(overlapObserved, overlapInput, overlapCached); ok {
		row.CachedContextRatio = ratio
		row.CachedContextRatioState = "observed"
		if *ratio >= thresholds.CachedContextRatioThreshold {
			row.TriggerReasons = append(row.TriggerReasons, "cached_context_ratio")
		}
	}

	if growth, ok := inputTokenGrowth(inputSamples, firstInput, maxInput); ok {
		row.InputTokenGrowth = growth
		row.InputTokenGrowthState = "observed"
		if *growth >= thresholds.InputTokenGrowthThreshold {
			row.TriggerReasons = append(row.TriggerReasons, "input_token_growth")
		}
	}

	row.Triggered = len(row.TriggerReasons) > 0
	return row
}

func inputStats(points []tokenPoint) (samples int, first *int64, max *int64) {
	for _, point := range points {
		if point.input == nil {
			continue
		}
		samples++
		if first == nil {
			first = point.input
		}
		if max == nil || *point.input > *max {
			max = point.input
		}
	}
	return samples, first, max
}

func overlapSums(points []tokenPoint) (observed bool, inputSum int64, cachedSum int64) {
	for _, point := range points {
		if point.input == nil || point.cached == nil {
			continue
		}
		inputSum += *point.input
		cachedSum += *point.cached
		observed = true
	}
	return observed, inputSum, cachedSum
}

func cachedContextRatio(observed bool, inputSum int64, cachedSum int64) (*float64, bool) {
	if !observed || inputSum <= 0 {
		return nil, false
	}
	ratio := float64(cachedSum) / float64(inputSum)
	return &ratio, true
}

func inputTokenGrowth(samples int, first *int64, max *int64) (*float64, bool) {
	if samples < 2 || first == nil || max == nil || *first <= 0 {
		return nil, false
	}
	growth := float64(*max) / float64(*first)
	return &growth, true
}

func tokenSignals(event canonical.Event) (input *int64, cached *int64, ok bool) {
	input = optionalInt64(event.Attributes["input_token_count"])
	cached = optionalInt64(event.Attributes["cached_input_tokens"])

	if rawEvent, ok := event.ProviderExtensions["event"].(map[string]any); ok {
		input = firstOptionalInt64(input, optionalInt64(rawEvent["input_tokens"]))
		cached = firstOptionalInt64(cached, optionalInt64(rawEvent["cache_read_tokens"]))
	}
	if contract, ok := event.ProviderExtensions["outcome_contract"].(map[string]any); ok {
		input = firstOptionalInt64(input, optionalInt64(contract["input_tokens"]))
	}
	return input, cached, input != nil || cached != nil
}

func firstOptionalInt64(primary, fallback *int64) *int64 {
	if primary != nil {
		return primary
	}
	return fallback
}

func optionalInt64(value any) *int64 {
	switch value := value.(type) {
	case int:
		v := int64(value)
		return &v
	case int64:
		return &value
	case float64:
		if value == float64(int64(value)) {
			v := int64(value)
			return &v
		}
	case string:
		if strings.TrimSpace(value) == "" {
			return nil
		}
		var parsed int64
		if _, err := fmt.Sscan(value, &parsed); err == nil {
			return &parsed
		}
	}
	return nil
}
