package claude

import (
	"sort"
	"strconv"
	"strings"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// Raw span-attribute and span-envelope keys the sub-agent reconstruction reads.
// They are the provider-native wire keys carried verbatim into
// provider_extensions.span_attributes (safeSpanAttributeKeys) and
// provider_extensions.span by the trace normaliser (#100/#101), so the tree is
// rebuilt from what those issues already capture — no new per-span mapping.
const (
	subAgentKeyAgentID       = "agent_id"
	subAgentKeyParentAgentID = "parent_agent_id"
	subAgentKeySubagentType  = "subagent_type"
	subAgentKeyInputTokens   = "input_tokens"
	subAgentKeyOutputTokens  = "output_tokens"
	subAgentKeyCacheRead     = "cache_read_tokens"
	subAgentKeyCacheCreation = "cache_creation_tokens"
	subAgentKeyDurationMs    = "duration_ms"
)

// subAgentSpan is the reduced per-span view the reconstruction needs: the span
// envelope (identity, ancestry, bounds), the sub-agent correlation attributes,
// and the token/duration cost. Every field is present-only — a genuinely absent
// attribute stays nil/empty rather than a fabricated zero.
type subAgentSpan struct {
	traceID       string
	spanID        string
	parentSpanID  string
	spanType      string
	orderingKey   string
	startNano     int64
	endNano       int64
	haveBounds    bool
	sessionID     string
	provider      string
	tool          string
	agentID       string
	parentAgentID string
	subagentType  string
	workflowRunID string
	workflowName  string
	inputTokens   *int64
	outputTokens  *int64
	cacheRead     *int64
	cacheCreation *int64
	durationMs    *int64
}

// ReconstructSubAgentRelations rebuilds a session's sub-agent tree from its
// canonical span events, producing one canonical.AgentRelation per distinct
// sub-agent (a span carrying a non-empty agent_id). It is a pure function over
// the already-persisted event set, so the storage layer can re-derive the tree
// from every event a session has (idempotent, complete across OTLP batches)
// rather than aggregating a single ingest payload — the latter would undercount a
// rollup whose spans arrived in separate batches.
//
// Grouping is keyed by (trace_id, agent_id): agent_id is unique only within a
// trace, so a session spanning multiple traces never collides. Non-span events
// (logs/metrics) and spans without an agent_id contribute nothing. Rollups are
// summed present-only; LLMDurationMsTotal/ToolDurationMsTotal are honest per-span
// sums (they overlap), while WallClockMs is the true elapsed span. Output is
// sorted by (trace_id, agent_id) for a stable golden.
func ReconstructSubAgentRelations(events []canonical.Event) []canonical.AgentRelation {
	spans := collectSubAgentSpans(events)
	if len(spans) == 0 {
		return nil
	}
	groups := map[string][]subAgentSpan{}
	order := []string{}
	for _, span := range spans {
		if span.agentID == "" {
			continue
		}
		key := span.traceID + "\x00" + span.agentID
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], span)
	}
	relations := make([]canonical.AgentRelation, 0, len(order))
	for _, key := range order {
		relations = append(relations, relationFromGroup(groups[key]))
	}
	sort.SliceStable(relations, func(i, j int) bool {
		if relations[i].TraceID != relations[j].TraceID {
			return relations[i].TraceID < relations[j].TraceID
		}
		return relations[i].AgentID < relations[j].AgentID
	})
	return relations
}

// collectSubAgentSpans reduces the events to span views, deduping by
// (trace_id, span_id) — a span id is unique only within its trace, so deduping
// on the id alone would wrongly drop a same-id span from another trace. The
// first occurrence wins so a re-delivered span never double-counts a rollup.
// Events without a span envelope are skipped.
func collectSubAgentSpans(events []canonical.Event) []subAgentSpan {
	seen := map[string]struct{}{}
	var spans []subAgentSpan
	for _, event := range events {
		span, ok := subAgentSpanFromEvent(event)
		if !ok {
			continue
		}
		if span.spanID != "" {
			key := span.traceID + "\x00" + span.spanID
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
		}
		spans = append(spans, span)
	}
	return spans
}

// subAgentSpanFromEvent extracts a span view from one event's
// provider_extensions.span (envelope) and .span_attributes (raw allow-listed
// attributes). It returns false for a non-span event so log/metric events for the
// same session are ignored.
func subAgentSpanFromEvent(event canonical.Event) (subAgentSpan, bool) {
	extensions := event.ProviderExtensions
	envelope := nestedMap(extensions, "span")
	if envelope == nil {
		return subAgentSpan{}, false
	}
	attrs := nestedMap(extensions, "span_attributes")
	span := subAgentSpan{
		traceID:       stringValue(envelope["trace_id"]),
		spanID:        stringValue(envelope["span_id"]),
		parentSpanID:  stringValue(envelope["parent_span_id"]),
		orderingKey:   orderingKeyFromExtensions(extensions),
		sessionID:     event.SessionID,
		provider:      event.Provider,
		tool:          event.Tool,
		spanType:      stringValue(attrs["span.type"]),
		agentID:       stringValue(attrs[subAgentKeyAgentID]),
		parentAgentID: stringValue(attrs[subAgentKeyParentAgentID]),
		subagentType:  stringValue(attrs[subAgentKeySubagentType]),
		workflowRunID: stringValue(attrs[attrWorkflowRunID]),
		workflowName:  stringValue(attrs[attrWorkflowName]),
		inputTokens:   normalize.OptionalTokenCount(attrs[subAgentKeyInputTokens]),
		outputTokens:  normalize.OptionalTokenCount(attrs[subAgentKeyOutputTokens]),
		cacheRead:     normalize.OptionalTokenCount(attrs[subAgentKeyCacheRead]),
		cacheCreation: normalize.OptionalTokenCount(attrs[subAgentKeyCacheCreation]),
		durationMs:    normalize.OptionalTokenCount(attrs[subAgentKeyDurationMs]),
	}
	span.startNano, span.endNano, span.haveBounds = spanBounds(envelope)
	return span, true
}

// relationFromGroup rolls one agent's spans (already grouped by trace + agent_id)
// into a canonical.AgentRelation. Present-only fields take the value from the
// earliest span (by ordering key) that carries one, so a conflicting later value
// never makes the record nondeterministic.
func relationFromGroup(spans []subAgentSpan) canonical.AgentRelation {
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].orderingKey != spans[j].orderingKey {
			return spans[i].orderingKey < spans[j].orderingKey
		}
		return spans[i].spanID < spans[j].spanID
	})
	first := spans[0]
	relation := canonical.AgentRelation{
		SchemaVersion: canonical.RecordSchemaVersion,
		RelationID:    first.traceID + ":" + first.agentID,
		SessionID:     first.sessionID,
		TraceID:       first.traceID,
		Provider:      first.provider,
		Tool:          first.tool,
		AgentID:       first.agentID,
		ParentKind:    canonical.ParentKindMainSession,
		Provenance:    canonical.ProvenanceObserved,
		SpanCount:     len(spans),
	}
	spanIDs := make([]string, 0, len(spans))
	var bounds boundsTracker
	for _, span := range spans {
		spanIDs = append(spanIDs, span.spanID)
		accumulateSpan(&relation, span)
		bounds.add(span)
	}
	if wall, ok := bounds.wallClockMs(); ok {
		relation.WallClockMs = &wall
	}
	relation.ProviderExtensions = map[string]any{"span_ids": spanIDs}
	return relation
}

// accumulateSpan folds one span into the relation: the parent edge (first
// observed parent_agent_id promotes ParentKind to sub_agent), the earliest-wins
// metadata, and the per-span-type counts/token/duration rollup.
func accumulateSpan(relation *canonical.AgentRelation, span subAgentSpan) {
	if span.parentAgentID != "" && relation.ParentAgentID == nil {
		parent := span.parentAgentID
		relation.ParentAgentID = &parent
		relation.ParentKind = canonical.ParentKindSubAgent
	}
	setStringPtrIfEmpty(&relation.SubagentType, span.subagentType)
	setStringPtrIfEmpty(&relation.WorkflowRunID, span.workflowRunID)
	setStringPtrIfEmpty(&relation.WorkflowName, span.workflowName)
	switch span.spanType {
	case "llm_request":
		relation.LLMRequestCount++
		relation.InputTokens = addOptional(relation.InputTokens, span.inputTokens)
		relation.OutputTokens = addOptional(relation.OutputTokens, span.outputTokens)
		relation.CacheReadTokens = addOptional(relation.CacheReadTokens, span.cacheRead)
		relation.CacheCreationTokens = addOptional(relation.CacheCreationTokens, span.cacheCreation)
		relation.LLMDurationMsTotal = addOptional(relation.LLMDurationMsTotal, span.durationMs)
	case spanTypeTool:
		relation.ToolCount++
		relation.ToolDurationMsTotal = addOptional(relation.ToolDurationMsTotal, span.durationMs)
	}
}

// boundsTracker tracks the earliest start and latest end across an agent's spans
// so WallClockMs is the true elapsed span, from genuinely observed bounds only.
type boundsTracker struct {
	minStart int64
	maxEnd   int64
	observed bool
}

func (b *boundsTracker) add(span subAgentSpan) {
	if !span.haveBounds {
		return
	}
	if !b.observed || span.startNano < b.minStart {
		b.minStart = span.startNano
	}
	if !b.observed || span.endNano > b.maxEnd {
		b.maxEnd = span.endNano
	}
	b.observed = true
}

func (b *boundsTracker) wallClockMs() (int64, bool) {
	if !b.observed || b.maxEnd < b.minStart {
		return 0, false
	}
	return (b.maxEnd - b.minStart) / 1_000_000, true
}

// nestedMap returns the map stored at key, tolerating both the in-memory
// map[string]any (from the normaliser) and the decoded-JSON map[string]any (from
// storage). A missing or non-map value yields nil.
func nestedMap(container map[string]any, key string) map[string]any {
	if container == nil {
		return nil
	}
	nested, ok := container[key].(map[string]any)
	if !ok {
		return nil
	}
	return nested
}

// orderingKeyFromExtensions reads provider_extensions.correlation.ordering_key,
// the stable per-span ordinal the trace normaliser stamps, used to pick the
// earliest span deterministically.
func orderingKeyFromExtensions(extensions map[string]any) string {
	correlation := nestedMap(extensions, "correlation")
	if correlation == nil {
		return ""
	}
	return stringValue(correlation["ordering_key"])
}

// spanBounds parses the raw Unix-nanosecond start/end strings from the span
// envelope. Bounds are reported present only when both parse to positive values,
// so WallClockMs is computed only from genuinely observed bounds.
func spanBounds(envelope map[string]any) (start, end int64, ok bool) {
	start, startOK := parseUnixNano(envelope["start_unix_nano"])
	end, endOK := parseUnixNano(envelope["end_unix_nano"])
	if !startOK || !endOK {
		return 0, 0, false
	}
	return start, end, true
}

func parseUnixNano(value any) (int64, bool) {
	text := strings.TrimSpace(stringValue(value))
	if text == "" {
		return 0, false
	}
	parsed, err := strconv.ParseInt(text, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, false
	}
	return parsed, true
}

// stringValue returns the trimmed string form of an attribute value, or "" for a
// nil or non-string value (parent_span_id is nil on a root span).
func stringValue(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

// setStringPtrIfEmpty sets *dst to a copy of value the first time a non-empty
// value is seen, so the earliest observed value wins and later values do not
// overwrite it (determinism).
func setStringPtrIfEmpty(dst **string, value string) {
	if *dst != nil || value == "" {
		return
	}
	copied := value
	*dst = &copied
}

// addOptional accumulates present-only counts: an absent addend leaves the
// accumulator untouched, and the first observed value promotes it from nil, so a
// genuine absence stays distinguishable from a summed zero.
func addOptional(acc, addend *int64) *int64 {
	if addend == nil {
		return acc
	}
	if acc == nil {
		total := *addend
		return &total
	}
	*acc += *addend
	return acc
}
