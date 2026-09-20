package ui

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/breakdown"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

type breakdownView struct {
	Availability  string
	Reason        string
	Version       string
	Coverage      string
	TotalLabel    string
	ShowDonut     bool
	ConicGradient string
	DonutLabel    string
	Categories    []breakdownCategoryView
	Overlap       *breakdownSegmentView
	Unclassified  *breakdownSegmentView
}

type breakdownCategoryView struct {
	ID            string
	Label         string
	DurationLabel string
	PercentLabel  string
	EvidencePath  string
}

type breakdownSegmentView struct {
	DurationLabel string
	PercentLabel  string
}

type legendEntry struct {
	Label  string
	Detail string
}

func populateBreakdownView(result breakdown.Result, sessionID string) breakdownView {
	view := breakdownView{
		Availability: result.Availability,
		Version:      result.CalculationVersion,
	}
	if result.UnavailableReason != nil {
		view.Reason = *result.UnavailableReason
	}
	if result.Availability != breakdown.AvailabilityAvailable || result.Window == nil {
		return view
	}
	if result.Coverage != nil {
		view.Coverage = result.Coverage.State
	}
	view.TotalLabel = formatDurationMs(result.Window.DurationMs)
	parts, cursor := appendCategoryViews(&view, result.Categories, sessionID)
	parts, cursor = appendOptionalSegment(parts, cursor, "overlap", result.Overlap, func(seg breakdownSegmentView) {
		view.Overlap = &seg
	})
	parts, _ = appendOptionalSegment(parts, cursor, "unclassified", result.Unclassified, func(seg breakdownSegmentView) {
		view.Unclassified = &seg
	})
	if len(parts) > 0 {
		view.ShowDonut = true
		view.ConicGradient = "conic-gradient(" + strings.Join(parts, ", ") + ")"
		view.DonutLabel = "Session duration breakdown totaling " + view.TotalLabel
	}
	return view
}

func appendCategoryViews(view *breakdownView, categories []breakdown.CategoryDuration, sessionID string) ([]string, float64) {
	view.Categories = make([]breakdownCategoryView, 0, len(categories))
	parts := make([]string, 0, len(categories)+2)
	cursor := 0.0
	for _, category := range categories {
		percent := optionalPercent(category.Percent)
		parts = append(parts, conicSlice(categoryColor(category.ID), cursor, percent))
		cursor += percent
		evidencePath := ""
		if len(category.SourceEventIDs) > 0 {
			evidencePath = sessionPath(sessionID) + "?event=" + url.QueryEscape(category.SourceEventIDs[0]) + "&inspector=details#event-inspector"
		}
		view.Categories = append(view.Categories, breakdownCategoryView{
			ID: category.ID, Label: category.Label,
			DurationLabel: formatDurationMs(category.DurationMs),
			PercentLabel:  formatBreakdownPercent(category.Percent),
			EvidencePath:  evidencePath,
		})
	}
	return parts, cursor
}

func appendOptionalSegment(
	parts []string,
	cursor float64,
	id string,
	segment *breakdown.SegmentDuration,
	assign func(breakdownSegmentView),
) ([]string, float64) {
	if segment == nil || segment.DurationMs <= 0 {
		return parts, cursor
	}
	percent := optionalPercent(segment.Percent)
	parts = append(parts, conicSlice(categoryColor(id), cursor, percent))
	assign(breakdownSegmentView{
		DurationLabel: formatDurationMs(segment.DurationMs),
		PercentLabel:  formatBreakdownPercent(segment.Percent),
	})
	return parts, cursor + percent
}

func optionalPercent(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func conicSlice(color string, start, width float64) string {
	return fmt.Sprintf("%s %.2f%% %.2f%%", color, start, start+width)
}

func eventLegend(events []timelineRow, spans []spanEvidenceRow, conversation []conversationRow) []legendEntry {
	seen := map[string]struct{}{}
	entries := make([]legendEntry, 0)
	add := func(label, detail string) {
		key := label + "\x00" + detail
		if _, found := seen[key]; found {
			return
		}
		seen[key] = struct{}{}
		entries = append(entries, legendEntry{Label: label, Detail: detail})
	}
	for _, row := range conversation {
		add("Conversation", row.Role)
	}
	for _, row := range spans {
		label := "Span"
		if row.Name != "" {
			label = row.Name
		}
		add("Span", label)
	}
	for _, row := range events {
		detail := row.RawEventType
		if detail == "" {
			detail = row.Title
		}
		add("Timeline", detail)
	}
	return entries
}

func formatDurationMs(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%d ms", ms)
	}
	seconds := time.Duration(ms) * time.Millisecond
	if seconds < time.Minute {
		return fmt.Sprintf("%.1fs", seconds.Seconds())
	}
	minutes := int(seconds / time.Minute)
	rem := seconds % time.Minute
	return fmt.Sprintf("%dm %02ds", minutes, int(rem.Seconds()))
}

func formatBreakdownPercent(value *float64) string {
	if value == nil {
		return "unavailable"
	}
	return fmt.Sprintf("%.0f%%", *value)
}

func categoryColor(id string) string {
	switch id {
	case breakdown.CategoryPlanning:
		return "#8b5cf6"
	case breakdown.CategoryToolCalls:
		return "#3b82f6"
	case breakdown.CategoryModelGeneration:
		return "#22d3ee"
	case breakdown.CategoryUserWait:
		return "#94a3b8"
	case "overlap":
		return "#f59e0b"
	default:
		return "#64748b"
	}
}

func calculateSessionBreakdown(events []canonical.Event, session canonical.Session) breakdown.Result {
	return breakdown.Calculate(events, &session)
}
