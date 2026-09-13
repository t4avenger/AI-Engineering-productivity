package insights

import (
	"testing"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestOperationStatsAggregatesCategoriesOutcomesAndDurations(t *testing.T) {
	stats := OperationStatsFromOperations([]canonical.Operation{
		testOperation("op-1", canonical.OperationCategoryShellCommand, "success", map[string]any{"tool_call": map[string]any{"duration_ms": "92"}}),
		testOperation("op-2", canonical.OperationCategoryShellCommand, "failed", map[string]any{"sandbox_outcome": map[string]any{"initial_duration_ms": float64(108)}}),
		testOperation("op-3", canonical.OperationCategoryFilesystemWrite, "", map[string]any{"event": map[string]any{"duration_ms": int64(50)}}),
	})
	if stats.Totals.TotalOperations != 3 || stats.Totals.DurationObservedCount != 3 {
		t.Fatalf("totals = %#v", stats.Totals)
	}
	if stats.Totals.AverageDurationMs == nil || *stats.Totals.AverageDurationMs != 250.0/3.0 {
		t.Fatalf("average duration = %#v", stats.Totals.AverageDurationMs)
	}
	if len(stats.ByCategory) != 2 || stats.ByCategory[0].Category != "filesystem write" || stats.ByCategory[1].Category != "shell command" {
		t.Fatalf("categories = %#v", stats.ByCategory)
	}
	if outcomeCount(stats, "unknown") != 1 || outcomeCount(stats, "success") != 1 || outcomeCount(stats, "failed") != 1 {
		t.Fatalf("outcomes = %#v", stats.ByOutcome)
	}
}

func TestOperationStatsIgnoresMalformedDurations(t *testing.T) {
	stats := OperationStatsFromOperations([]canonical.Operation{
		testOperation("op-negative", canonical.OperationCategoryShellCommand, "success", map[string]any{"tool_call": map[string]any{"duration_ms": "-1"}}),
		testOperation("op-float", canonical.OperationCategoryShellCommand, "success", map[string]any{"tool_call": map[string]any{"duration_ms": 12.5}}),
		testOperation("op-text", canonical.OperationCategoryShellCommand, "success", map[string]any{"tool_call": map[string]any{"duration_ms": "not-a-number"}}),
	})
	if stats.Totals.TotalOperations != 3 {
		t.Fatalf("total operations = %d", stats.Totals.TotalOperations)
	}
	if stats.Totals.DurationObservedCount != 0 || stats.Totals.AverageDurationMs != nil {
		t.Fatalf("malformed durations must be unavailable, got %#v", stats.Totals)
	}
	if len(stats.ByCategory) != 1 || stats.ByCategory[0].DurationObservedCount != 0 || stats.ByCategory[0].AverageDurationMs != nil {
		t.Fatalf("category durations = %#v", stats.ByCategory)
	}
}

func testOperation(id string, category canonical.OperationCategory, outcome string, extensions map[string]any) canonical.Operation {
	return canonical.Operation{SchemaVersion: canonical.RecordSchemaVersion, OperationID: id, SessionID: "session", Provider: "openai", Tool: "codex", Category: category, Outcome: outcome, Provenance: canonical.ProvenanceObserved, ProviderExtensions: extensions}
}

func outcomeCount(stats OperationStats, outcome string) int {
	for _, row := range stats.ByOutcome {
		if row.Outcome == outcome {
			return row.Count
		}
	}
	return 0
}
