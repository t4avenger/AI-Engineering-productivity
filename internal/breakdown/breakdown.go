// Package breakdown calculates evidence-based session duration segments (T09).
// It unions proven span intervals over the observed wall window and never
// invents Planning or User wait from idle gaps.
package breakdown

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/spans"
)

const (
	// CalculationVersion identifies the interval contract implementation.
	CalculationVersion = "1"

	AvailabilityAvailable   = "available"
	AvailabilityUnavailable = "unavailable"

	CategoryPlanning        = "planning"
	CategoryToolCalls       = "tool_calls"
	CategoryModelGeneration = "model_generation"
	CategoryUserWait        = "user_wait"

	CoverageComplete = "complete"
	CoveragePartial  = "partial"
)

// Result is the full-session duration breakdown. Availability is unavailable
// when the observed window has no positive duration or no classified intervals.
type Result struct {
	Availability       string             `json:"availability"`
	CalculationVersion string             `json:"calculation_version"`
	Window             *Window            `json:"window"`
	Categories         []CategoryDuration `json:"categories"`
	Overlap            *SegmentDuration   `json:"overlap"`
	Unclassified       *SegmentDuration   `json:"unclassified"`
	Coverage           *Coverage          `json:"coverage"`
	UnavailableReason  *string            `json:"unavailable_reason"`
}

// Window is the observed wall-clock denominator for percentages.
type Window struct {
	StartAt    string `json:"start_at"`
	EndAt      string `json:"end_at"`
	DurationMs int64  `json:"duration_ms"`
}

// CategoryDuration is one exclusive category after disjoint segmentation.
type CategoryDuration struct {
	ID             string   `json:"id"`
	Label          string   `json:"label"`
	DurationMs     int64    `json:"duration_ms"`
	Percent        *float64 `json:"percent"`
	SourceEventIDs []string `json:"source_event_ids"`
}

// SegmentDuration is Overlap or Unclassified wall time.
type SegmentDuration struct {
	DurationMs int64    `json:"duration_ms"`
	Percent    *float64 `json:"percent"`
}

// Coverage reports whether every retained span interval contributed cleanly.
type Coverage struct {
	State        string `json:"state"`
	ClassifiedMs int64  `json:"classified_ms"`
	WindowMs     int64  `json:"window_ms"`
}

type classifiedInterval struct {
	start          time.Time
	end            time.Time
	category       string
	sourceEventIDs []string
}

// Calculate projects retained span envelopes and partitions the observed
// session window into exclusive category, overlap, and unclassified segments.
func Calculate(events []canonical.Event, session *canonical.Session) Result {
	records := spans.Project(events)
	categoryByIdentity := categoryIndex(events)
	intervals, sawInvalid := collectIntervals(records, categoryByIdentity)
	windowStart, windowEnd, windowOK := observeWindow(session, records)
	if !windowOK || !windowEnd.After(windowStart) {
		reason := "observed session window has no positive duration"
		return unavailable(reason)
	}
	if len(intervals) == 0 {
		reason := "no valid classified intervals in retained session evidence"
		return unavailable(reason)
	}

	windowNs := windowEnd.Sub(windowStart).Nanoseconds()
	if windowNs <= 0 {
		reason := "observed session window has no positive duration"
		return unavailable(reason)
	}
	totalsNs, evidence := segment(windowStart, windowEnd, intervals)
	windowMs := windowNs / int64(time.Millisecond)
	if windowMs <= 0 {
		reason := "observed session window has no positive duration"
		return unavailable(reason)
	}
	totalsMs := durationsToMilliseconds(windowNs, totalsNs)
	coverageState := CoverageComplete
	if sawInvalid {
		coverageState = CoveragePartial
	}

	categories := make([]CategoryDuration, 0, 4)
	classifiedMs := int64(0)
	for _, id := range []string{CategoryPlanning, CategoryToolCalls, CategoryModelGeneration, CategoryUserWait} {
		ms := totalsMs[id]
		if ms <= 0 {
			continue
		}
		classifiedMs += ms
		categories = append(categories, CategoryDuration{
			ID: id, Label: categoryLabel(id), DurationMs: ms, Percent: percentOf(ms, windowMs),
			SourceEventIDs: evidence[id],
		})
	}
	overlapMs := totalsMs["overlap"]
	unclassifiedMs := totalsMs["unclassified"]
	if overlapMs > 0 {
		classifiedMs += overlapMs
	}

	result := Result{
		Availability:       AvailabilityAvailable,
		CalculationVersion: CalculationVersion,
		Window: &Window{
			StartAt:    windowStart.UTC().Format(time.RFC3339Nano),
			EndAt:      windowEnd.UTC().Format(time.RFC3339Nano),
			DurationMs: windowMs,
		},
		Categories: categories,
		Coverage: &Coverage{
			State: coverageState, ClassifiedMs: classifiedMs, WindowMs: windowMs,
		},
	}
	if overlapMs > 0 {
		result.Overlap = &SegmentDuration{DurationMs: overlapMs, Percent: percentOf(overlapMs, windowMs)}
	}
	if unclassifiedMs > 0 {
		result.Unclassified = &SegmentDuration{DurationMs: unclassifiedMs, Percent: percentOf(unclassifiedMs, windowMs)}
	}
	return result
}

func unavailable(reason string) Result {
	return Result{
		Availability:       AvailabilityUnavailable,
		CalculationVersion: CalculationVersion,
		Categories:         []CategoryDuration{},
		UnavailableReason:  &reason,
	}
}

func categoryIndex(events []canonical.Event) map[string]string {
	// Match spans.Project: sort by occurred_at then event_id, keep the first
	// classified category for each (trace_id, span_id) identity.
	ordered := append([]canonical.Event(nil), events...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if !ordered[i].OccurredAt.Equal(ordered[j].OccurredAt) {
			return ordered[i].OccurredAt.Before(ordered[j].OccurredAt)
		}
		return ordered[i].EventID < ordered[j].EventID
	})
	index := make(map[string]string, len(ordered))
	for _, event := range ordered {
		identity := spanIdentity(event)
		if identity == "" {
			continue
		}
		if _, found := index[identity]; found {
			continue
		}
		if category, ok := classify(event, events); ok {
			index[identity] = category
		}
	}
	return index
}

func spanIdentity(event canonical.Event) string {
	envelope, ok := event.ProviderExtensions["span"].(map[string]any)
	if !ok {
		return ""
	}
	correlation, _ := event.ProviderExtensions["correlation"].(map[string]any)
	traceID := textValue(envelope["trace_id"])
	if traceID == "" {
		traceID = textValue(envelope["traceId"])
	}
	if traceID == "" {
		traceID = textValue(correlation["trace_id"])
	}
	spanID := textValue(envelope["span_id"])
	if spanID == "" {
		spanID = textValue(envelope["spanId"])
	}
	if spanID == "" {
		spanID = textValue(correlation["span_id"])
	}
	if traceID == "" || spanID == "" {
		return ""
	}
	return traceID + "\x00" + spanID
}

func classify(event canonical.Event, events []canonical.Event) (string, bool) {
	if codexTurnHasObservedModelResponse(event, events) {
		return CategoryModelGeneration, true
	}
	if spanType := textValue(event.Attributes["span_type"]); spanType != "" {
		if category, ok := categoryFromType(spanType); ok {
			return category, true
		}
	}
	name := event.EventType
	if envelope, ok := event.ProviderExtensions["span"].(map[string]any); ok {
		if spanName := textValue(envelope["name"]); spanName != "" {
			name = spanName
		}
	}
	return categoryFromType(stripProviderPrefix(name))
}

// codexTurnHasObservedModelResponse is the bounded Codex duration-category
// mapping from #218. session_task.turn is classified only when its retained
// provider turn.id exactly equals a same-session codex.sse_event turn.id that
// reports a model. Span names, timestamps, token values, and thread.id are not
// category evidence.
func codexTurnHasObservedModelResponse(span canonical.Event, events []canonical.Event) bool {
	if span.Provider != "openai" || span.Tool != "codex" || span.EventType != "session_task.turn" {
		return false
	}
	turnID := codexExtensionValue(span, "span_attributes", "turn.id")
	if turnID == "" {
		return false
	}
	for _, event := range events {
		if event.Provider != "openai" || event.Tool != "codex" || event.SessionID != span.SessionID || event.EventType != "codex.sse_event" {
			continue
		}
		if textValue(event.Attributes["model"]) == "" {
			continue
		}
		if codexExtensionValue(event, "log_attributes", "turn.id") == turnID {
			return true
		}
	}
	return false
}

func codexExtensionValue(event canonical.Event, extension, key string) string {
	values, _ := event.ProviderExtensions[extension].(map[string]any)
	return textValue(values[key])
}

func stripProviderPrefix(name string) string {
	name = strings.TrimSpace(name)
	for _, prefix := range []string{"claude_code.", "claude."} {
		if strings.HasPrefix(name, prefix) {
			return strings.TrimPrefix(name, prefix)
		}
	}
	return name
}

func categoryFromType(spanType string) (string, bool) {
	switch strings.TrimSpace(spanType) {
	case "llm_request":
		return CategoryModelGeneration, true
	case "tool", "tool.execution":
		return CategoryToolCalls, true
	case "tool.blocked_on_user":
		return CategoryUserWait, true
	case "planning":
		return CategoryPlanning, true
	default:
		return "", false
	}
}

func categoryLabel(id string) string {
	switch id {
	case CategoryPlanning:
		return "Planning"
	case CategoryToolCalls:
		return "Tool calls"
	case CategoryModelGeneration:
		return "Model generation"
	case CategoryUserWait:
		return "User wait"
	default:
		return id
	}
}

func collectIntervals(records []spans.Record, categoryByIdentity map[string]string) ([]classifiedInterval, bool) {
	intervals := make([]classifiedInterval, 0, len(records))
	sawInvalid := false
	for _, record := range records {
		if record.IntervalAvailability != spans.IntervalAvailable {
			sawInvalid = true
			continue
		}
		category, ok := categoryByIdentity[record.TraceID+"\x00"+record.SpanID]
		if !ok {
			continue
		}
		start, end, ok := parseInterval(record.StartAt, record.EndAt)
		if !ok {
			sawInvalid = true
			continue
		}
		intervals = append(intervals, classifiedInterval{
			start: start, end: end, category: category,
			sourceEventIDs: append([]string(nil), record.SourceEventIDs...),
		})
	}
	return intervals, sawInvalid
}

func parseInterval(startAt, endAt *string) (time.Time, time.Time, bool) {
	if startAt == nil || endAt == nil {
		return time.Time{}, time.Time{}, false
	}
	start, startErr := time.Parse(time.RFC3339Nano, *startAt)
	end, endErr := time.Parse(time.RFC3339Nano, *endAt)
	if startErr != nil || endErr != nil || !end.After(start) {
		return time.Time{}, time.Time{}, false
	}
	return start.UTC(), end.UTC(), true
}

func observeWindow(session *canonical.Session, records []spans.Record) (time.Time, time.Time, bool) {
	if session != nil && !session.StartedAt.IsZero() && session.CompletedAt != nil && session.CompletedAt.After(session.StartedAt) {
		return session.StartedAt.UTC(), session.CompletedAt.UTC(), true
	}
	var start time.Time
	var end time.Time
	found := false
	for _, record := range records {
		if record.IntervalAvailability != spans.IntervalAvailable {
			continue
		}
		intervalStart, intervalEnd, ok := parseInterval(record.StartAt, record.EndAt)
		if !ok {
			continue
		}
		if !found || intervalStart.Before(start) {
			start = intervalStart
		}
		if !found || intervalEnd.After(end) {
			end = intervalEnd
		}
		found = true
	}
	return start, end, found
}

func segment(windowStart, windowEnd time.Time, intervals []classifiedInterval) (map[string]int64, map[string][]string) {
	ordered := boundaryPoints(windowStart, windowEnd, intervals)
	totals := map[string]int64{}
	evidence := map[string][]string{}
	for index := 0; index+1 < len(ordered); index++ {
		segStart := time.Unix(0, ordered[index]).UTC()
		segEnd := time.Unix(0, ordered[index+1]).UTC()
		if !segEnd.After(segStart) || segStart.Before(windowStart) || !segStart.Before(windowEnd) {
			continue
		}
		applySegment(totals, evidence, segEnd.Sub(segStart).Nanoseconds(), coveringCategories(segStart, segEnd, intervals))
	}
	for category, ids := range evidence {
		sort.Strings(ids)
		evidence[category] = ids
	}
	return totals, evidence
}

// durationsToMilliseconds converts nanosecond bucket totals to milliseconds once
// at the result boundary and assigns any truncation residual to unclassified so
// category + overlap + unclassified always equals the observed window.
func durationsToMilliseconds(windowNs int64, totalsNs map[string]int64) map[string]int64 {
	windowMs := windowNs / int64(time.Millisecond)
	keys := []string{
		CategoryPlanning, CategoryToolCalls, CategoryModelGeneration, CategoryUserWait,
		"overlap", "unclassified",
	}
	result := make(map[string]int64, len(keys))
	assigned := int64(0)
	for _, key := range keys {
		if key == "unclassified" {
			continue
		}
		ms := totalsNs[key] / int64(time.Millisecond)
		if ms > 0 {
			result[key] = ms
			assigned += ms
		}
	}
	remainder := windowMs - assigned
	if remainder > 0 {
		result["unclassified"] = remainder
	}
	return result
}

func boundaryPoints(windowStart, windowEnd time.Time, intervals []classifiedInterval) []int64 {
	points := map[int64]struct{}{
		windowStart.UnixNano(): {},
		windowEnd.UnixNano():   {},
	}
	for _, interval := range intervals {
		clippedStart, clippedEnd, ok := clipInterval(interval.start, interval.end, windowStart, windowEnd)
		if !ok {
			continue
		}
		points[clippedStart.UnixNano()] = struct{}{}
		points[clippedEnd.UnixNano()] = struct{}{}
	}
	ordered := make([]int64, 0, len(points))
	for point := range points {
		ordered = append(ordered, point)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	return ordered
}

func clipInterval(start, end, windowStart, windowEnd time.Time) (time.Time, time.Time, bool) {
	if start.Before(windowStart) {
		start = windowStart
	}
	if end.After(windowEnd) {
		end = windowEnd
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, false
	}
	return start, end, true
}

func coveringCategories(segStart, segEnd time.Time, intervals []classifiedInterval) map[string][]string {
	categories := map[string][]string{}
	for _, interval := range intervals {
		if !interval.end.After(segStart) || !interval.start.Before(segEnd) {
			continue
		}
		categories[interval.category] = appendUnique(categories[interval.category], interval.sourceEventIDs...)
	}
	return categories
}

func applySegment(totals map[string]int64, evidence map[string][]string, duration int64, categories map[string][]string) {
	switch len(categories) {
	case 0:
		totals["unclassified"] += duration
	case 1:
		for category, ids := range categories {
			totals[category] += duration
			evidence[category] = appendUnique(evidence[category], ids...)
		}
	default:
		totals["overlap"] += duration
		for _, ids := range categories {
			evidence["overlap"] = appendUnique(evidence["overlap"], ids...)
		}
	}
}

func percentOf(part, whole int64) *float64 {
	if whole <= 0 {
		return nil
	}
	value := math.Round((float64(part)/float64(whole))*10000) / 100
	return &value
}

func textValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func appendUnique(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, value := range additions {
		if value == "" {
			continue
		}
		if _, found := seen[value]; found {
			continue
		}
		values = append(values, value)
		seen[value] = struct{}{}
	}
	return values
}
