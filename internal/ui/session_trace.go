package ui

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/conversation"
	"github.com/wayne/telemetryiq/internal/insights"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/spans"
)

const (
	traceLaneConversation = "conversation"
	traceLaneAgent        = "agent"
	traceLaneTools        = "tools"
	traceLaneFiles        = "files"
	traceLaneSpans        = "spans"

	tracePointWidthPercent  = 1.2
	traceMinBarWidthPercent = 0.8

	inspectorSourceTrace = "trace"
)

type sessionTraceView struct {
	OriginKind           string
	OriginLabel          string
	WindowLabel          string
	WindowMs             int64
	PartialCapture       bool
	PlanningAvailability string
	Lanes                []traceLaneView
	Unplaced             []traceMarkerView
	Chronological        []traceMarkerView
	Header               sessionTraceHeader
}

type sessionTraceHeader struct {
	SessionID     string
	StateLabel    string
	Model         string
	ModelObserved bool
	Duration      string
	DurationOK    bool
	Tokens        string
	TokensOK      bool
	OccurredAt    string
	Metadata      []metadataRow
}

type traceLaneView struct {
	ID           string
	Label        string
	Availability string
	EmptyMessage string
	Markers      []traceMarkerView
	TrackHeight  int
}

type traceMarkerView struct {
	EventID        string
	Lane           string
	Kind           string
	Label          string
	Detail         string
	Preview        string
	OffsetMs       int64
	DurationMs     int64
	HasDuration    bool
	Placed         bool
	LeftPercent    float64
	WidthPercent   float64
	StackIndex     int
	NestDepth      int
	SpanID         string
	ParentSpanID   string
	Selected       bool
	SelectPath     string
	SourceEventIDs string
	OffsetLabel    string
	DOMID          string
}

type timedTraceItem struct {
	eventID      string
	lane         string
	kind         string
	label        string
	detail       string
	preview      string
	start        time.Time
	durationMs   int64
	hasDuration  bool
	placed       bool
	sourceIDs    string
	spanID       string
	parentSpanID string
}

func buildSessionTrace(
	session canonical.Session,
	events []canonical.Event,
	operations []canonical.Operation,
	sessionID string,
	r *http.Request,
) sessionTraceView {
	items := collectTraceItems(events, operations)
	origin, originKind, _, windowMs := traceWindow(session, items)
	view := sessionTraceView{
		OriginKind:           originKind,
		OriginLabel:          formatTraceOrigin(origin, originKind),
		WindowMs:             windowMs,
		WindowLabel:          formatDurationMs(windowMs),
		PlanningAvailability: planningAvailability(items),
		Header:               buildTraceHeader(session, events),
	}
	markers := make([]traceMarkerView, 0, len(items))
	for _, item := range items {
		marker := markerFromItem(item, origin, windowMs, sessionID, r)
		markers = append(markers, marker)
	}
	sortTraceMarkers(markers)
	view.Unplaced = filterMarkers(markers, false)
	placed := filterMarkers(markers, true)
	view.Chronological = append([]traceMarkerView(nil), placed...)
	view.Chronological = append(view.Chronological, view.Unplaced...)
	view.Lanes = buildTraceLanes(placed, view.PlanningAvailability)
	view.PartialCapture = computePartialCapture(view, items, windowMs)
	return view
}

func computePartialCapture(view sessionTraceView, items []timedTraceItem, windowMs int64) bool {
	if len(view.Unplaced) > 0 {
		return true
	}
	return windowMs == 0 && len(items) > 0
}

func collectTraceItems(events []canonical.Event, operations []canonical.Operation) []timedTraceItem {
	items := make([]timedTraceItem, 0)
	items = append(items, conversationTraceItems(events)...)
	items = append(items, agentAndToolEventItems(events)...)
	items = append(items, fileTraceItems(events, operations)...)
	items = append(items, spanTraceItems(events)...)
	return items
}

func conversationTraceItems(events []canonical.Event) []timedTraceItem {
	records := conversation.Project(events)
	items := make([]timedTraceItem, 0, len(records))
	for _, record := range records {
		preview := ""
		if record.Text != nil {
			preview, _ = conversationPreview(*record.Text)
		}
		items = append(items, timedTraceItem{
			eventID: record.EventID,
			lane:    traceLaneConversation,
			kind:    "conversation",
			label:   conversationTitle(record.Role),
			detail:  record.Role + " · " + record.ContentAvailability,
			preview: preview,
			start:   record.OccurredAt,
			placed:  !record.OccurredAt.IsZero(),
		})
	}
	return items
}

func agentAndToolEventItems(events []canonical.Event) []timedTraceItem {
	items := make([]timedTraceItem, 0)
	for _, event := range events {
		lane, kind, ok := classifyEventLane(event)
		if !ok {
			continue
		}
		durationMs, hasDuration := eventDurationMs(event)
		items = append(items, timedTraceItem{
			eventID:     event.EventID,
			lane:        lane,
			kind:        kind,
			label:       eventTitle(event.EventType),
			detail:      eventLaneDetail(event, kind),
			start:       event.OccurredAt,
			durationMs:  durationMs,
			hasDuration: hasDuration,
			placed:      !event.OccurredAt.IsZero(),
		})
	}
	return items
}

func classifyEventLane(event canonical.Event) (lane, kind string, ok bool) {
	switch event.EventType {
	case "user_prompt", "assistant_response", "api_request_body", "api_response_body":
		return "", "", false // conversation projection owns these
	case "model_interaction", "codex.sse_event", "api_request", "api_error", "assistant_message":
		return traceLaneAgent, "agent", true
	case "skill_invocation":
		return traceLaneTools, "skill", true
	case "mcp_call", "mcp_server_connection":
		return traceLaneTools, "mcp", true
	case "tool_decision", "codex.tool_decision":
		return traceLaneTools, "tool", true
	case "session.active", "session.completed":
		return traceLaneAgent, "lifecycle", true
	}
	if _, hasOp := observedString(event.Attributes["operation_id"]); hasOp {
		kind = "tool"
		if category, _ := observedString(event.Attributes["category"]); strings.EqualFold(category, "MCP call") {
			kind = "mcp"
		}
		if _, hasSkill := event.ProviderExtensions["skill"]; hasSkill {
			kind = "skill"
		}
		if _, hasMCP := event.ProviderExtensions["mcp_call"]; hasMCP {
			kind = "mcp"
		}
		return traceLaneTools, kind, true
	}
	return "", "", false
}

func eventLaneDetail(event canonical.Event, kind string) string {
	parts := []string{event.EventType}
	if tool, ok := observedString(event.Attributes["tool_name"]); ok {
		parts = append(parts, tool)
	} else if tool, ok := observedString(event.Attributes["tool"]); ok {
		parts = append(parts, tool)
	}
	if kind == "mcp" {
		if call, ok := event.ProviderExtensions["mcp_call"].(map[string]any); ok {
			if server := optionalDisplayString(call["server_name"]); server != "" {
				parts = append(parts, server)
			}
		}
	}
	if kind == "skill" {
		if skill, ok := event.ProviderExtensions["skill"].(map[string]any); ok {
			if name := optionalDisplayString(skill["name"]); name != "" {
				parts = append(parts, name)
			}
		}
	}
	return strings.Join(parts, " · ")
}

func fileTraceItems(events []canonical.Event, operations []canonical.Operation) []timedTraceItem {
	entries := insights.SessionFilesFromEvidence(events, operations)
	items := make([]timedTraceItem, 0, len(entries))
	for _, entry := range entries {
		label := "File operation"
		if entry.Path != nil && *entry.Path != "" {
			label = *entry.Path
		}
		detail := entry.Availability.Action
		if entry.Action != nil && *entry.Action != "" {
			detail = *entry.Action
		}
		start, ok := parseTraceTime(entry.OccurredAt)
		placed := ok && !start.IsZero()
		durationMs := int64(0)
		hasDuration := false
		if entry.DurationMs != nil {
			durationMs = *entry.DurationMs
			hasDuration = true
		}
		items = append(items, timedTraceItem{
			eventID: entry.EventID, lane: traceLaneFiles, kind: "file",
			label: label, detail: detail, start: start, durationMs: durationMs,
			hasDuration: hasDuration, placed: placed,
		})
	}
	return items
}

func spanTraceItems(events []canonical.Event) []timedTraceItem {
	records := spans.Project(events)
	items := make([]timedTraceItem, 0, len(records)*2)
	for _, record := range records {
		eventID := firstSourceEventID(record.SourceEventIDs)
		if eventID == "" {
			continue
		}
		label := "Span"
		if record.Name != nil && *record.Name != "" {
			label = *record.Name
		}
		start, hasStart := parseOptionalTraceTime(record.StartAt)
		placed := hasStart && !start.IsZero()
		durationMs := int64(0)
		hasDuration := false
		if record.DurationMs != nil && record.IntervalAvailability == spans.IntervalAvailable {
			durationMs = *record.DurationMs
			hasDuration = true
		}
		detail := "trace " + record.TraceID + " · span " + record.SpanID
		sourceIDs := strings.Join(record.SourceEventIDs, ", ")
		parentID := ""
		if record.ParentSpanID != nil {
			parentID = *record.ParentSpanID
		}
		items = append(items, timedTraceItem{
			eventID: eventID, lane: traceLaneSpans, kind: "span",
			label: label, detail: detail, start: start, durationMs: durationMs,
			hasDuration: hasDuration, placed: placed,
			sourceIDs: sourceIDs, spanID: record.SpanID, parentSpanID: parentID,
		})
		if lane, kind, ok := spanLaneFromRecord(record); ok {
			items = append(items, timedTraceItem{
				eventID: eventID, lane: lane, kind: kind,
				label: label, detail: detail, start: start, durationMs: durationMs,
				hasDuration: hasDuration, placed: placed,
				sourceIDs: sourceIDs, spanID: record.SpanID, parentSpanID: parentID,
			})
		}
	}
	return items
}

func spanLaneFromRecord(record spans.Record) (lane, kind string, ok bool) {
	name := ""
	if record.Name != nil {
		name = *record.Name
	}
	category, found := spanCategoryName(name)
	if !found {
		return "", "", false
	}
	switch category {
	case "planning", "model_generation":
		return traceLaneAgent, "agent", true
	case "tool_calls", "user_wait":
		return traceLaneTools, "tool", true
	default:
		return "", "", false
	}
}

func spanCategoryName(name string) (string, bool) {
	trimmed := strings.TrimSpace(name)
	for _, prefix := range []string{"claude_code.", "claude."} {
		trimmed = strings.TrimPrefix(trimmed, prefix)
	}
	switch trimmed {
	case "llm_request":
		return "model_generation", true
	case "tool", "tool.execution":
		return "tool_calls", true
	case "tool.blocked_on_user":
		return "user_wait", true
	case "planning":
		return "planning", true
	default:
		return "", false
	}
}

func traceWindow(session canonical.Session, items []timedTraceItem) (origin time.Time, originKind string, end time.Time, windowMs int64) {
	earliest, latest := traceItemBounds(items)
	origin, originKind = resolveTraceOrigin(session, earliest)
	end = resolveTraceEnd(session, latest)
	return origin, originKind, end, traceWindowMs(origin, end, earliest)
}

func traceItemBounds(items []timedTraceItem) (earliest, latest time.Time) {
	for _, item := range items {
		if !item.placed || item.start.IsZero() {
			continue
		}
		start := item.start.UTC()
		finish := start
		if item.hasDuration && item.durationMs > 0 {
			finish = start.Add(time.Duration(item.durationMs) * time.Millisecond)
		}
		if earliest.IsZero() || start.Before(earliest) {
			earliest = start
		}
		if latest.IsZero() || finish.After(latest) {
			latest = finish
		}
	}
	return earliest, latest
}

func resolveTraceOrigin(session canonical.Session, earliest time.Time) (time.Time, string) {
	if !session.StartedAt.IsZero() {
		return session.StartedAt.UTC(), "session_start"
	}
	return earliest, "observed_window"
}

func resolveTraceEnd(session canonical.Session, latest time.Time) time.Time {
	end := latest
	if session.CompletedAt == nil || session.CompletedAt.IsZero() {
		return end
	}
	completed := session.CompletedAt.UTC()
	if end.IsZero() || completed.After(end) {
		return completed
	}
	return end
}

func traceWindowMs(origin, end, earliest time.Time) int64 {
	if origin.IsZero() {
		return 0
	}
	if end.IsZero() || !end.After(origin) {
		// Point-only sessions still need a positive window so markers can sit at 0%.
		if !earliest.IsZero() {
			return 1
		}
		return 0
	}
	return end.Sub(origin).Milliseconds()
}

func markerFromItem(item timedTraceItem, origin time.Time, windowMs int64, sessionID string, r *http.Request) traceMarkerView {
	marker := traceMarkerView{
		EventID:        item.eventID,
		Lane:           item.lane,
		Kind:           item.kind,
		Label:          item.label,
		Detail:         item.detail,
		Preview:        item.preview,
		HasDuration:    item.hasDuration,
		DurationMs:     item.durationMs,
		Placed:         item.placed && !origin.IsZero() && windowMs > 0,
		SourceEventIDs: item.sourceIDs,
		SpanID:         item.spanID,
		ParentSpanID:   item.parentSpanID,
		SelectPath:     sessionInspectorPath(sessionID, item.eventID, inspectorTabDetails, inspectorSourceTrace, r, false),
		WidthPercent:   tracePointWidthPercent,
	}
	if !item.placed || origin.IsZero() {
		marker.Placed = false
		return marker
	}
	offset := item.start.UTC().Sub(origin).Milliseconds()
	if offset < 0 {
		offset = 0
	}
	marker.OffsetMs = offset
	if windowMs <= 0 {
		marker.Placed = false
		return marker
	}
	marker.LeftPercent = float64(offset) / float64(windowMs) * 100
	if item.hasDuration && item.durationMs > 0 {
		width := float64(item.durationMs) / float64(windowMs) * 100
		if width < traceMinBarWidthPercent {
			width = traceMinBarWidthPercent
		}
		marker.WidthPercent = width
	}
	if marker.LeftPercent > 100 {
		marker.LeftPercent = 100
		marker.WidthPercent = tracePointWidthPercent
	}
	marker.OffsetLabel = formatDurationMs(marker.OffsetMs)
	marker.DOMID = "trace-" + item.lane + "-event-" + item.eventID + "-select"
	return marker
}

func buildTraceLanes(placed []traceMarkerView, planningAvailability string) []traceLaneView {
	defs := []struct {
		id, label, empty string
	}{
		{traceLaneConversation, "Conversation", "No retained conversation evidence for this session."},
		{traceLaneAgent, "Agent", "No observed agent or model-work evidence for this session."},
		{traceLaneTools, "Tools & MCP", "No observed tool, MCP, or skill evidence for this session."},
		{traceLaneFiles, "Files", "No retained file-operation evidence for this session."},
		{traceLaneSpans, "Spans", "No retained trace span evidence for this session."},
	}
	lanes := make([]traceLaneView, 0, len(defs))
	for _, def := range defs {
		markers := markersForLane(placed, def.id)
		if def.id == traceLaneSpans {
			assignSpanNestDepth(markers)
		}
		assignOverlapStacks(markers)
		lane := traceLaneView{
			ID: def.id, Label: def.label, Markers: markers, TrackHeight: maxStack(markers) + 1,
		}
		if len(markers) == 0 {
			lane.EmptyMessage = def.empty
			lane.Availability = "none"
		} else {
			lane.Availability = "observed"
		}
		if def.id == traceLaneAgent {
			lane.Availability = planningAvailability
			if planningAvailability == "unavailable" && len(markers) == 0 {
				lane.EmptyMessage = "Planning telemetry is unavailable. No observed agent turns are retained."
			} else if planningAvailability == "unavailable" {
				lane.EmptyMessage = ""
			}
		}
		lanes = append(lanes, lane)
	}
	return lanes
}

func planningAvailability(items []timedTraceItem) string {
	for _, item := range items {
		if item.lane == traceLaneAgent && item.kind == "agent" {
			if strings.Contains(strings.ToLower(item.label), "planning") || strings.Contains(strings.ToLower(item.detail), "planning") {
				return "observed"
			}
		}
	}
	for _, item := range items {
		if item.lane == traceLaneAgent && item.kind == "agent" {
			return "partial"
		}
	}
	return "unavailable"
}

func assignSpanNestDepth(markers []traceMarkerView) {
	bySpan := map[string]int{}
	for i := range markers {
		if markers[i].SpanID == "" {
			continue
		}
		bySpan[markers[i].SpanID] = i
	}
	var depthOf func(spanID string, seen map[string]struct{}) int
	depthOf = func(spanID string, seen map[string]struct{}) int {
		index, ok := bySpan[spanID]
		if !ok {
			return 0
		}
		parent := markers[index].ParentSpanID
		if parent == "" {
			return 0
		}
		if _, loop := seen[spanID]; loop {
			return 0
		}
		seen[spanID] = struct{}{}
		return depthOf(parent, seen) + 1
	}
	for i := range markers {
		if markers[i].SpanID == "" {
			continue
		}
		markers[i].NestDepth = depthOf(markers[i].SpanID, map[string]struct{}{})
	}
}

func markersForLane(markers []traceMarkerView, lane string) []traceMarkerView {
	out := make([]traceMarkerView, 0)
	for _, marker := range markers {
		if marker.Lane == lane {
			out = append(out, marker)
		}
	}
	return out
}

func filterMarkers(markers []traceMarkerView, placed bool) []traceMarkerView {
	out := make([]traceMarkerView, 0)
	for _, marker := range markers {
		if marker.Placed == placed {
			out = append(out, marker)
		}
	}
	return out
}

func sortTraceMarkers(markers []traceMarkerView) {
	sort.SliceStable(markers, func(i, j int) bool {
		if markers[i].Placed != markers[j].Placed {
			return markers[i].Placed
		}
		if markers[i].OffsetMs != markers[j].OffsetMs {
			return markers[i].OffsetMs < markers[j].OffsetMs
		}
		return markers[i].EventID < markers[j].EventID
	})
}

func assignOverlapStacks(markers []traceMarkerView) {
	type active struct {
		endMs int64
		stack int
	}
	activeBars := make([]active, 0)
	for i := range markers {
		start := markers[i].OffsetMs
		end := start + 1
		if markers[i].HasDuration && markers[i].DurationMs > 0 {
			end = start + markers[i].DurationMs
		}
		remaining := activeBars[:0]
		used := map[int]struct{}{}
		for _, bar := range activeBars {
			if bar.endMs > start {
				remaining = append(remaining, bar)
				used[bar.stack] = struct{}{}
			}
		}
		activeBars = remaining
		stack := 0
		for {
			if _, taken := used[stack]; !taken {
				break
			}
			stack++
		}
		markers[i].StackIndex = stack
		activeBars = append(activeBars, active{endMs: end, stack: stack})
	}
}

func maxStack(markers []traceMarkerView) int {
	highest := 0
	for _, marker := range markers {
		if marker.StackIndex > highest {
			highest = marker.StackIndex
		}
	}
	return highest
}

func buildTraceHeader(session canonical.Session, events []canonical.Event) sessionTraceHeader {
	header := sessionTraceHeader{
		SessionID:  session.SessionID,
		StateLabel: sessionStateLabel(session.State),
		Metadata:   sessionMetadata(session),
		OccurredAt: formatTimestamp(session.StartedAt),
	}
	if model, observed := sessionModelLabel(session, events); observed {
		header.Model = model
		header.ModelObserved = true
	}
	if !session.StartedAt.IsZero() && session.CompletedAt != nil && !session.CompletedAt.IsZero() {
		ms := session.CompletedAt.Sub(session.StartedAt).Milliseconds()
		if ms > 0 {
			header.Duration = formatDurationMs(ms)
			header.DurationOK = true
		}
	}
	if tokens := sessionTokenTotal(session); tokens != "" {
		header.Tokens = tokens
		header.TokensOK = true
	}
	return header
}

func sessionModelLabel(session canonical.Session, events []canonical.Event) (string, bool) {
	models := map[string]struct{}{}
	if model := sessionAttribute(session, "model"); model != "" {
		models[model] = struct{}{}
	}
	for _, event := range events {
		if model, ok := observedString(event.Attributes["model"]); ok {
			models[model] = struct{}{}
		}
	}
	switch len(models) {
	case 0:
		return "", false
	case 1:
		for model := range models {
			return model, true
		}
		return "", false
	default:
		return "multiple models", true
	}
}

func sessionTokenTotal(session canonical.Session) string {
	for _, key := range []string{"total_tokens", "token_count"} {
		if value, ok := int64Attr(session.Attributes[key]); ok {
			return strconv.FormatInt(value, 10)
		}
	}
	return ""
}

func int64Attr(raw any) (int64, bool) {
	switch value := raw.(type) {
	case int64:
		return value, true
	case int:
		return int64(value), true
	case float64:
		return int64(value), true
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func eventDurationMs(event canonical.Event) (int64, bool) {
	raw, ok := event.Attributes["duration_ms"]
	if !ok {
		return 0, false
	}
	value, ok := int64Attr(raw)
	return value, ok && value >= 0
}

func parseTraceTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, raw)
	}
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func parseOptionalTraceTime(raw *string) (time.Time, bool) {
	if raw == nil {
		return time.Time{}, false
	}
	return parseTraceTime(*raw)
}

func firstSourceEventID(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func formatTraceOrigin(origin time.Time, kind string) string {
	if origin.IsZero() {
		return "No observed time window"
	}
	stamp := origin.UTC().Format("2006-01-02 15:04:05 UTC")
	if kind == "session_start" {
		return "Session start · " + stamp
	}
	return "Observed window origin · " + stamp
}

func markSelectedTrace(data *sessionDetailData, eventID string) {
	if data.Trace.Lanes == nil {
		return
	}
	focusAssigned := false
	assign := func(markers []traceMarkerView) []traceMarkerView {
		for i := range markers {
			markers[i].Selected = markers[i].EventID == eventID
			if markers[i].Selected && !focusAssigned {
				markers[i].DOMID = inspectorFocusID(inspectorSourceTrace, eventID)
				focusAssigned = true
			}
		}
		return markers
	}
	for i := range data.Trace.Lanes {
		data.Trace.Lanes[i].Markers = assign(data.Trace.Lanes[i].Markers)
	}
	data.Trace.Unplaced = assign(data.Trace.Unplaced)
	data.Trace.Chronological = assign(data.Trace.Chronological)
	if focusAssigned && data.Inspector.Source == inspectorSourceTrace {
		data.Inspector.FocusTargetID = inspectorFocusID(inspectorSourceTrace, eventID)
	}
}
