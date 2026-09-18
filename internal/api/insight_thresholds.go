package api

import "github.com/wayne/telemetryiq/internal/insights"

// MCPAllowlistSource exposes the current policy input. Implementations must be
// safe for concurrent API and dashboard reads.
type MCPAllowlistSource interface {
	MCPAllowlist() []string
}

// InsightThresholds carries configurable thresholds for insight handlers.
// It is passed to both the JSON API and the HTMX dashboard so they render
// consistent results from the same retained telemetry.
type InsightThresholds struct {
	ContextWaste insights.ContextWasteThresholds
	// MCPAllowlist is the set of approved MCP server names for the §14.4
	// unapproved-MCP policy. Empty means the policy is unconfigured and reports
	// indeterminate rather than fabricating a clean result.
	MCPAllowlist []string
	// MCPAllowlistSource, when set, supersedes the startup snapshot above so a
	// successful local configuration save takes effect without daemon restart.
	MCPAllowlistSource MCPAllowlistSource
}

func (t InsightThresholds) currentMCPAllowlist() []string {
	if t.MCPAllowlistSource != nil {
		return t.MCPAllowlistSource.MCPAllowlist()
	}
	return append([]string(nil), t.MCPAllowlist...)
}

func DefaultInsightThresholds() InsightThresholds {
	return InsightThresholds{
		ContextWaste: insights.ContextWasteThresholds{
			CachedContextRatioThreshold: 0.75,
			InputTokenGrowthThreshold:   2.0,
		},
	}
}
