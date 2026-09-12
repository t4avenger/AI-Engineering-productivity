package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// ErrUnsupportedTraces indicates a valid OTLP traces payload is not the observed
// Claude Code span shape (no claude-code resource, or no span this adapter
// maps). It mirrors ErrUnsupportedMetrics/ErrUnsupportedLogs so the ingest path
// can skip a payload that belongs to another tool without treating it as an
// error.
var ErrUnsupportedTraces = errors.New("unsupported Claude Code traces payload")

// safeSpanAttributeKeys is the allow-list of span attributes carried verbatim
// into provider_extensions.span_attributes. Ingest-time storage sanitising was
// removed in #88, so this adapter is the sole guard: an allow-list (not a
// deny-list) drops an unforeseen identity- or secret-bearing attribute by
// default. session.id becomes the canonical session identity and model is
// promoted onto the event, so neither is repeated here. Operator/machine
// identity (user.*, organization.*, terminal.*) and the redacted user_prompt
// are deliberately absent so they never reach provider_extensions. Detailed
// per-span-type field mapping is owned by the T-phase issues (#100–#103); this
// list carries the low-risk behaviour signals proven by the F3 fixture.
// gen_ai.response.finish_reasons is deliberately absent: Claude Code emits it as
// an OTLP arrayValue, which the shared scalar attributeValue decoder does not
// read, so allow-listing it would silently drop it. Its structured mapping is
// owned by the T-phase (#100–#103) alongside array-value decoding; carrying the
// scalar stop_reason here keeps the F3 skeleton honest about what it captures.
var safeSpanAttributeKeys = map[string]struct{}{
	"span.type":               {},
	"gen_ai.system":           {},
	"gen_ai.request.model":    {},
	"gen_ai.response.id":      {},
	"llm_request.context":     {},
	"query_source_safe":       {},
	"speed":                   {},
	"success":                 {},
	"attempt":                 {},
	"stop_reason":             {},
	"duration_ms":             {},
	"ttft_ms":                 {},
	"first_content_ms":        {},
	"input_tokens":            {},
	"output_tokens":           {},
	"cache_read_tokens":       {},
	"cache_creation_tokens":   {},
	"request_id":              {},
	"interaction.sequence":    {},
	"interaction.duration_ms": {},
	"parent.source":           {},
	"queued_sends":            {},
	"user_prompt_length":      {},
}

type tracesPayload struct {
	ResourceSpans []resourceSpan `json:"resourceSpans"`
}

type resourceSpan struct {
	Resource struct {
		Attributes []otlpAttribute `json:"attributes"`
	} `json:"resource"`
	ScopeSpans []scopeSpan `json:"scopeSpans"`
}

type scopeSpan struct {
	Scope struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"scope"`
	Spans []otlpSpan `json:"spans"`
}

// otlpSpan decodes the span fields F3 needs. Per-span-type attributes stay in
// Attributes for the T-phase issues to map; only the structural envelope
// (ids/name/kind/timestamps/status) is promoted here.
type otlpSpan struct {
	TraceID           string          `json:"traceId"`
	SpanID            string          `json:"spanId"`
	ParentSpanID      string          `json:"parentSpanId"`
	Name              string          `json:"name"`
	Kind              int             `json:"kind"`
	StartTimeUnixNano string          `json:"startTimeUnixNano"`
	EndTimeUnixNano   string          `json:"endTimeUnixNano"`
	Attributes        []otlpAttribute `json:"attributes"`
	Status            struct {
		Code int `json:"code"`
	} `json:"status"`
}

// spanContext carries resource- and scope-derived values shared by every span
// under one resource, keeping per-span helpers within the argument limit.
type spanContext struct {
	scopeName        string
	resourceIdentity string
	safeResource     map[string]any
	version          string
	receivedAt       time.Time
}

// NormalizeTraces maps a raw, already-sanitised Claude Code OTLP/HTTP traces
// payload into canonical events, one per span. Only resources whose
// service.name is claude-code are considered, so a mixed payload from another
// tool is skipped rather than misattributed; a payload with no Claude spans
// yields ErrUnsupportedTraces.
//
// Routing contract mirrors the metrics adapter (#89): the sentinel lets the
// ingest path skip a foreign payload, while a claude-code span that is
// structurally malformed (missing trace/span id) is a real normalisation error
// — not a sentinel — so the route never silently 202-accepts and drops
// supported Claude trace data (#50/#49).
//
// Raw span identity (traceId/spanId/parentSpanId) and the raw session id are
// retained verbatim; no ingest-time hiding is applied (epic #87). Span
// attributes are reduced to safeSpanAttributeKeys. Per-span-type field mapping
// (interaction/llm_request/tool/hook/sub-agent) is owned by #100–#103.
func NormalizeTraces(data []byte, receivedAt time.Time) ([]canonical.Event, error) {
	var payload tracesPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode Claude OTLP traces: %w", err)
	}
	var events []canonical.Event
	for _, resource := range payload.ResourceSpans {
		resourceEvents, err := spanEventsFromResource(resource, receivedAt)
		if err != nil {
			return nil, err
		}
		events = append(events, resourceEvents...)
	}
	if len(events) == 0 {
		return nil, ErrUnsupportedTraces
	}
	return normalize.CorrelateEvents(events), nil
}

// spanEventsFromResource normalises a single resourceSpans entry, returning
// events only when its service.name is claude-code (nil otherwise, so a mixed
// payload is safe).
func spanEventsFromResource(resource resourceSpan, receivedAt time.Time) ([]canonical.Event, error) {
	resourceAttrs := attributeValues(resource.Resource.Attributes)
	if service, _ := resourceAttrs[attrServiceName].(string); service != claudeLogService {
		return nil, nil
	}
	ctx := spanContext{
		resourceIdentity: resourceIdentityKey(resourceAttrs),
		safeResource:     safeMetricAttributes(resourceAttrs),
		version:          fallbackString(stringAttr(resourceAttrs, attrServiceVersion), unavailable),
		receivedAt:       receivedAt,
	}
	var events []canonical.Event
	for _, scope := range resource.ScopeSpans {
		ctx.scopeName = scope.Scope.Name
		for _, span := range scope.Spans {
			event, err := spanEvent(span, ctx)
			if err != nil {
				return nil, err
			}
			events = append(events, event)
		}
	}
	return events, nil
}

// spanEvent maps one Claude Code span into a canonical event. A span missing
// its trace or span id is a hard error per the routing contract, so malformed
// supported data is never silently dropped.
func spanEvent(span otlpSpan, ctx spanContext) (canonical.Event, error) {
	traceID := strings.TrimSpace(span.TraceID)
	spanID := strings.TrimSpace(span.SpanID)
	name := strings.TrimSpace(span.Name)
	// name becomes the canonical event_type, which the schema requires to be
	// non-empty; like the trace/span ids it is a structural field, so a supported
	// claude-code span missing it is a hard error, never a silently dropped or
	// schema-invalid event.
	if traceID == "" || spanID == "" || name == "" {
		return canonical.Event{}, fmt.Errorf("claude trace span %q missing trace id, span id, or name", span.Name)
	}
	// A span's chronology orders the session timeline, so start time is a required
	// structural field: a missing or malformed value is a hard error rather than a
	// fabricated receivedAt stamp (mirrors the Codex trace normaliser).
	occurredAt, err := spanStartTime(span.StartTimeUnixNano)
	if err != nil {
		return canonical.Event{}, fmt.Errorf("claude trace span %q: %w", name, err)
	}
	// A root span has no parent; keep it nil so consumers can distinguish a
	// genuine root from a literal empty parent (mirrors the Codex trace
	// normaliser and the correlation contract that unknowns are never empty).
	var parentSpanID any
	if trimmed := strings.TrimSpace(span.ParentSpanID); trimmed != "" {
		parentSpanID = trimmed
	}

	fields := attributeValues(span.Attributes)
	sessionID := spanSessionID(fields, traceID)
	model, modelObserved := normalize.ObservedString(fields["model"])

	// trace_id + span_id is globally unique; the resource identity is folded in
	// so two resources cannot collide on one event ID and have one silently
	// dropped by CorrelateEvents (or the storage event_id primary key).
	identity := fmt.Sprintf("%s|%s|%s", traceID, spanID, ctx.resourceIdentity)
	eventID := contentID("claude-code:span:", []byte(identity))

	attributes := map[string]any{
		"unavailable_fields": []string{"tool_io", "mcp_calls", "file_operations", "command_execution", "prompt_content", "response_content", "repository_context", "provider_cost"},
		"span_type":          fallbackString(stringAttr(fields, "span.type"), unavailable),
	}
	if modelObserved {
		attributes["model"] = model
	}
	extensions := map[string]any{
		"correlation": spanCorrelation(eventID, occurredAt, traceID, spanID, parentSpanID),
		"span": map[string]any{
			"trace_id":        traceID,
			"span_id":         spanID,
			"parent_span_id":  parentSpanID,
			"name":            name,
			"kind":            span.Kind,
			"scope":           ctx.scopeName,
			"start_unix_nano": strings.TrimSpace(span.StartTimeUnixNano),
			"end_unix_nano":   strings.TrimSpace(span.EndTimeUnixNano),
			"status_code":     span.Status.Code,
		},
		"resource":        ctx.safeResource,
		"span_attributes": safeSpanAttributes(fields),
	}
	return canonical.Event{
		SchemaVersion:      canonicalSchemaVersion,
		EventID:            eventID,
		EventType:          name,
		OccurredAt:         occurredAt,
		ReceivedAt:         ctx.receivedAt.UTC(),
		Provider:           provider,
		Tool:               tool,
		SourceSchema:       sourceSchema,
		SourceVersion:      ctx.version,
		ActorID:            unavailable,
		DeviceID:           unavailable,
		SessionID:          sessionID,
		PrivacyLevel:       "operational",
		Attributes:         attributes,
		ProviderExtensions: extensions,
	}, nil
}

// spanCorrelation carries the span-tree linkage (trace/span/parent) alongside
// the dedup/ordering keys, so downstream consumers can rebuild the interaction
// → llm_request/tool/hook hierarchy that spans expose and logs do not.
func spanCorrelation(eventID string, occurredAt time.Time, traceID, spanID string, parentSpanID any) map[string]any {
	return map[string]any{
		"dedup_key":      eventID,
		"ordering_key":   fmt.Sprintf("%020d:%s", occurredAt.UnixNano(), eventID),
		"trace_id":       traceID,
		"span_id":        spanID,
		"parent_span_id": parentSpanID,
		"task_boundary": map[string]any{
			"confidence": "unknown",
			"reason":     "Claude Code trace spans: per-span task-boundary mapping deferred to T-phase (#100–#103)",
		},
	}
}

// spanStartTime requires a positive Unix-nanoseconds start time. A span's
// chronology is structural — it orders the session timeline — so a missing or
// malformed value is a hard normalisation error rather than a fabricated
// receivedAt stamp (mirrors the Codex trace normaliser's unixNanoTime).
func spanStartTime(nano string) (time.Time, error) {
	raw := strings.TrimSpace(nano)
	if raw == "" {
		return time.Time{}, errors.New("startTimeUnixNano is required")
	}
	nanoseconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || nanoseconds <= 0 {
		return time.Time{}, errors.New("startTimeUnixNano must be a positive Unix-nanoseconds string")
	}
	return time.Unix(0, nanoseconds).UTC(), nil
}

// spanSessionID uses the raw provider-native session id when present, otherwise
// falls back to the trace id so spans from different traces are not merged into
// one synthetic "unknown" session (mirrors the Codex trace normaliser's
// trace-scoped session identity). Raw identifiers are kept verbatim (epic #87).
func spanSessionID(fields map[string]any, traceID string) string {
	if raw := strings.TrimSpace(stringAttr(fields, "session.id")); raw != "" {
		return normalize.ProviderNativeSessionID("claude-code:", raw)
	}
	return "claude-code:trace:" + traceID
}

// safeSpanAttributes reduces span attributes to the allow-listed safe keys,
// dropping operator/identity and unforeseen attributes by default (#88 removed
// storage-side sanitising, so the adapter is the sole guard).
func safeSpanAttributes(fields map[string]any) map[string]any {
	safe := make(map[string]any)
	for key, value := range fields {
		if _, ok := safeSpanAttributeKeys[strings.ToLower(strings.TrimSpace(key))]; ok {
			safe[key] = value
		}
	}
	return safe
}
