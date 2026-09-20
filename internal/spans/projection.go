// Package spans projects retained trace envelopes into a flat, paged evidence
// view. It deliberately does not infer joins between sessions or providers.
package spans

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

const (
	ParentRoot      = "root"
	ParentLoaded    = "loaded"
	ParentNotLoaded = "not_loaded"
	ParentAbsent    = "absent"

	IntervalAvailable = "available"
	IntervalMissing   = "missing"
	IntervalInvalid   = "invalid"
)

// Record is a flat node from a provider-emitted trace envelope. Parent nesting
// is intentionally a client concern so malformed cycles cannot recurse here.
type Record struct {
	TraceID              string   `json:"trace_id"`
	SpanID               string   `json:"span_id"`
	ParentSpanID         *string  `json:"parent_span_id"`
	ParentAvailability   string   `json:"parent_availability"`
	Name                 *string  `json:"name"`
	StartAt              *string  `json:"start_at"`
	EndAt                *string  `json:"end_at"`
	DurationMs           *int64   `json:"duration_ms"`
	IntervalAvailability string   `json:"interval_availability"`
	StatusCode           *int64   `json:"status_code"`
	Provider             string   `json:"provider"`
	Tool                 string   `json:"tool"`
	SourceVersion        string   `json:"source_version"`
	SourceEventIDs       []string `json:"source_event_ids"`
	OperationIDs         []string `json:"operation_ids"`
}

// Project returns one deterministic record per (trace_id, span_id). Replayed
// evidence contributes its source event ID but cannot duplicate a span node.
func Project(events []canonical.Event) []Record {
	ordered := append([]canonical.Event(nil), events...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if !ordered[i].OccurredAt.Equal(ordered[j].OccurredAt) {
			return ordered[i].OccurredAt.Before(ordered[j].OccurredAt)
		}
		return ordered[i].EventID < ordered[j].EventID
	})

	byIdentity := map[string]*Record{}
	for _, event := range ordered {
		record, ok := recordFromEvent(event)
		if !ok {
			continue
		}
		key := record.TraceID + "\x00" + record.SpanID
		existing, found := byIdentity[key]
		if !found {
			byIdentity[key] = &record
			continue
		}
		existing.SourceEventIDs = appendUnique(existing.SourceEventIDs, record.SourceEventIDs...)
		existing.OperationIDs = appendUnique(existing.OperationIDs, record.OperationIDs...)
	}

	result := make([]Record, 0, len(byIdentity))
	for _, record := range byIdentity {
		record.SourceEventIDs = sortedUnique(record.SourceEventIDs)
		record.OperationIDs = sortedUnique(record.OperationIDs)
		result = append(result, *record)
	}
	sort.Slice(result, func(i, j int) bool { return recordLess(result[i], result[j]) })
	return result
}

// Page returns records after the cursor identity. Parent availability is
// evaluated against the whole retained projection, not just the current page.
func Page(records []Record, limit int, cursorTraceID, cursorSpanID string) ([]Record, *Record) {
	start := 0
	if cursorTraceID != "" || cursorSpanID != "" {
		for index, record := range records {
			if record.TraceID == cursorTraceID && record.SpanID == cursorSpanID {
				start = index + 1
				break
			}
		}
	}
	end := start + limit
	last := (*Record)(nil)
	if end < len(records) {
		last = &records[end-1]
	} else {
		end = len(records)
	}
	page := make([]Record, end-start)
	copy(page, records[start:end])
	setParentAvailability(page, records)
	return page, last
}

func recordFromEvent(event canonical.Event) (Record, bool) {
	extensions := event.ProviderExtensions
	envelope, ok := extensions["span"].(map[string]any)
	if !ok {
		return Record{}, false
	}
	correlation, _ := extensions["correlation"].(map[string]any)
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
		return Record{}, false
	}
	parent := textValue(envelope["parent_span_id"])
	if parent == "" {
		parent = textValue(envelope["parentSpanId"])
	}
	if parent == "" {
		parent = textValue(correlation["parent_span_id"])
	}
	var parentID *string
	if parent != "" {
		parentID = &parent
	}
	name := textValue(envelope["name"])
	var spanName *string
	if name == "" {
		name = event.EventType
	}
	if name != "" {
		spanName = &name
	}
	start := spanTime(envelope, "start_unix_nano", "startTimeUnixNano")
	if start == nil && !event.OccurredAt.IsZero() {
		value := event.OccurredAt.UTC().Format(time.RFC3339Nano)
		start = &value
	}
	end := spanTime(envelope, "end_unix_nano", "endTimeUnixNano")
	duration, intervalAvailability := spanDuration(start, end)
	return Record{
		TraceID: traceID, SpanID: spanID, ParentSpanID: parentID, Name: spanName,
		StartAt: start, EndAt: end, DurationMs: duration, IntervalAvailability: intervalAvailability,
		StatusCode: spanStatusCode(envelope), Provider: event.Provider, Tool: event.Tool,
		SourceVersion: event.SourceVersion, SourceEventIDs: []string{event.EventID},
		OperationIDs: operationIDs(event.Attributes),
	}, true
}

func spanTime(envelope map[string]any, keys ...string) *string {
	for _, key := range keys {
		if nanoseconds, ok := int64Value(envelope[key]); ok && nanoseconds > 0 {
			value := time.Unix(0, nanoseconds).UTC().Format(time.RFC3339Nano)
			return &value
		}
	}
	return nil
}

func spanDuration(start, end *string) (*int64, string) {
	if start == nil || end == nil {
		return nil, IntervalMissing
	}
	startTime, startErr := time.Parse(time.RFC3339Nano, *start)
	endTime, endErr := time.Parse(time.RFC3339Nano, *end)
	if startErr != nil || endErr != nil || endTime.Before(startTime) {
		return nil, IntervalInvalid
	}
	duration := endTime.Sub(startTime).Milliseconds()
	return &duration, IntervalAvailable
}

func spanStatusCode(envelope map[string]any) *int64 {
	if value, ok := int64Value(envelope["status_code"]); ok {
		return &value
	}
	status, ok := envelope["status"].(map[string]any)
	if !ok {
		return nil
	}
	if value, ok := int64Value(status["code"]); ok {
		return &value
	}
	return nil
}

func operationIDs(attributes map[string]any) []string {
	if value := textValue(attributes["operation_id"]); value != "" {
		return []string{value}
	}
	return []string{}
}

func setParentAvailability(page, all []Record) {
	allByIdentity := make(map[string]struct{}, len(all))
	for _, record := range all {
		allByIdentity[record.TraceID+"\x00"+record.SpanID] = struct{}{}
	}
	pageByIdentity := make(map[string]struct{}, len(page))
	for _, record := range page {
		pageByIdentity[record.TraceID+"\x00"+record.SpanID] = struct{}{}
	}
	for index := range page {
		if page[index].ParentSpanID == nil {
			page[index].ParentAvailability = ParentRoot
			continue
		}
		key := page[index].TraceID + "\x00" + *page[index].ParentSpanID
		if _, found := pageByIdentity[key]; found {
			page[index].ParentAvailability = ParentLoaded
		} else if _, found := allByIdentity[key]; found {
			page[index].ParentAvailability = ParentNotLoaded
		} else {
			page[index].ParentAvailability = ParentAbsent
		}
	}
}

func recordLess(left, right Record) bool {
	if left.StartAt != nil && right.StartAt != nil && *left.StartAt != *right.StartAt {
		return *left.StartAt < *right.StartAt
	}
	if left.StartAt != nil && right.StartAt == nil {
		return true
	}
	if left.StartAt == nil && right.StartAt != nil {
		return false
	}
	if left.TraceID != right.TraceID {
		return left.TraceID < right.TraceID
	}
	return left.SpanID < right.SpanID
}

func textValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func int64Value(value any) (int64, bool) {
	switch value := value.(type) {
	case int64:
		return value, true
	case int:
		return int64(value), true
	case float64:
		if value == float64(int64(value)) {
			return int64(value), true
		}
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		return parsed, err == nil
	}
	return 0, false
}

func appendUnique(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, value := range additions {
		if value != "" {
			if _, found := seen[value]; !found {
				values = append(values, value)
				seen[value] = struct{}{}
			}
		}
	}
	return values
}

func sortedUnique(values []string) []string {
	result := appendUnique(nil, values...)
	sort.Strings(result)
	return result
}
