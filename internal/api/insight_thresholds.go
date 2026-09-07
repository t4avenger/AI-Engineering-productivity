package api

import "github.com/wayne/telemetryiq/internal/insights"

// InsightThresholds carries configurable thresholds for insight handlers.
// It is passed to both the JSON API and the HTMX dashboard so they render
// consistent results from the same retained telemetry.
type InsightThresholds struct {
	ContextWaste insights.ContextWasteThresholds
	// MCPAllowlist is the set of approved MCP server names for the §14.4
	// unapproved-MCP policy. Empty means the policy is unconfigured and reports
	// indeterminate rather than fabricating a clean result.
	MCPAllowlist []string
}

func DefaultInsightThresholds() InsightThresholds {
	return InsightThresholds{
		ContextWaste: insights.ContextWasteThresholds{
			CachedContextRatioThreshold: 0.75,
			InputTokenGrowthThreshold:   2.0,
		},
	}
}
