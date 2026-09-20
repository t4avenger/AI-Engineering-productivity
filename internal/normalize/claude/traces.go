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

// Span-type values from Claude Code's span.type attribute (#100/#101). Shared
// across the dispatch switch, task-boundary mapping, and unavailable_fields so
// Sonar S1192 does not fire on the repeated literals.
const (
	spanTypeTool            = "tool"
	spanTypeToolExecution   = "tool.execution"
	spanTypeToolBlockedUser = "tool.blocked_on_user"
	spanTypeHook            = "hook"
)

// Span attribute keys shared between the allow-list and the typed attribute
// mappers (llm_request / tool / tool.execution). Kept as named constants so the
// wire key is defined once (go:S1192).
const (
	attrWorkflowRunID   = "workflow.run_id"
	attrWorkflowName    = "workflow.name"
	attrGenAIToolCallID = "gen_ai.tool.call.id"
)

// safeSpanAttributeKeys is the allow-list of span attributes carried verbatim
// into provider_extensions.span_attributes, alongside the typed per-span-type
// blocks in attributes.interaction / attributes.llm_request (#100). Ingest-time
// storage sanitising was removed in #88, so this adapter is the sole guard: an
// allow-list (not a deny-list) drops an unforeseen identity- or secret-bearing
// attribute by default. session.id becomes the canonical session identity and
// model is promoted onto the event, so neither is repeated here. Operator/machine
// identity (user.*, organization.*, terminal.*) and the redacted user_prompt are
// absent so they never reach provider_extensions. The free-text `error` message is
// captured raw (epic #87) but lives in the typed llm_request/tool_execution block
// (its canonical home, which governance walks), not in this allow-list, so it is
// not duplicated into span_attributes.
// agent_id/parent_agent_id/workflow.* are sub-agent workflow correlation, not
// operator identity, so they are safe. gen_ai.response.finish_reasons is not
// listed here because Claude Code emits it as an OTLP arrayValue the scalar
// attributeValue decoder cannot read; it is decoded and surfaced in the typed
// llm_request block instead (arrayAttributeValues in logs.go). Bounded tool-span
// identifiers (tool_name, tool_use_id, gen_ai.tool.call.id, result_tokens, …) are
// allow-listed for verbatim passthrough (#101); the raw file_path/full_command/error
// content lives only in the typed tool block (its canonical home, which governance
// walks), so it is not duplicated here. The gated hook_definitions (#103) is
// likewise kept only in its typed hook block, not duplicated in this allow-list.
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
	"status_code":             {},
	"error_class":             {},
	"response.has_tool_call":  {},
	"agent_id":                {},
	"parent_agent_id":         {},
	attrWorkflowRunID:         {},
	attrWorkflowName:          {},
	"tool_name":               {},
	"tool_name_safe":          {},
	"bash_command_class":      {},
	"bash_argv0":              {},
	"tool_use_id":             {},
	attrGenAIToolCallID:       {},
	"result_tokens":           {},
	"skill_name":              {},
	"subagent_type":           {},
	"decision":                {},
	"source":                  {},
	"interaction.sequence":    {},
	"interaction.duration_ms": {},
	"parent.source":           {},
	"queued_sends":            {},
	"user_prompt_length":      {},
	"hook_event":              {},
	"hook_name":               {},
	"num_hooks":               {},
	"num_success":             {},
	"num_blocking":            {},
	"num_non_blocking_error":  {},
	"num_cancelled":           {},
}

// claudePRLinkScanFields are the reviewed Claude span fields that can carry a
// pull/merge-request URL verbatim. full_command is the OTEL_LOG_TOOL_DETAILS-gated
// raw command line on tool spans (#101), so a `gh pr create` / `gh pr view <url>`
// invocation surfaces a PR URL here. The URL grammar and extraction live in
// normalize.AttachPRLinkEvidence, shared with Codex (no CPD-duplicated block).
// tool_decision tool_parameters (dropped, pending #173) and JSONL tool output
// (allow-listed out, pending #105) are the other candidate surfaces, documented
// as unavailable until those issues retain them raw.
var claudePRLinkScanFields = []string{"full_command"}

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
// covers interaction/llm_request (#100), tool spans (#101), and hook spans
// (#103); the sub-agent span tree is reconstructed by a separate post-pass (#102).
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
	spanType := fallbackString(stringAttr(fields, "span.type"), unavailable)

	// trace_id + span_id is globally unique; the resource identity is folded in
	// so two resources cannot collide on one event ID and have one silently
	// dropped by CorrelateEvents (or the storage event_id primary key).
	identity := fmt.Sprintf("%s|%s|%s", traceID, spanID, ctx.resourceIdentity)
	eventID := contentID("claude-code:span:", []byte(identity))

	attributes := map[string]any{
		"unavailable_fields": spanUnavailableFields(spanType),
		"span_type":          spanType,
	}
	if modelObserved {
		attributes["model"] = model
	}
	// Dispatch on span.type so each span type gains a typed, present-only field
	// block: the per-prompt interaction/llm_request spans (#100), the tool /
	// tool.execution / tool.blocked_on_user spans (#101), and the hook span (#103).
	// Every documented field is captured raw, including the gated
	// file_path/full_command/error and hook_definitions — this is a
	// governance/timeline product and the raw values are the signal (epic #87);
	// the per-field hide decision is deferred downstream. Tool spans carry
	// file/command/tool_io surfaces, so spanUnavailableFields drops those from
	// their unavailable set. The sub-agent span tree is reconstructed by a
	// separate post-pass (claude.ReconstructSubAgentRelations, #102), not a
	// span.type case here.
	switch spanType {
	case "interaction":
		attributes["interaction"] = interactionAttributes(fields)
	case "llm_request":
		attributes["llm_request"] = llmRequestAttributes(fields, span.Attributes)
	case spanTypeTool:
		attributes["tool"] = toolAttributes(fields)
	case spanTypeToolExecution:
		attributes["tool_execution"] = toolExecutionAttributes(fields)
	case spanTypeToolBlockedUser:
		attributes["tool_blocked_on_user"] = toolBlockedOnUserAttributes(fields)
	case spanTypeHook:
		attributes["hook"] = hookAttributes(fields)
	}
	extensions := map[string]any{
		"correlation": spanCorrelation(eventID, occurredAt, traceID, spanID, parentSpanID, spanType),
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
	// A tool span's raw full_command carries the exact command line (#101), so a
	// `gh pr create` / `gh pr view <url>` invocation surfaces a pull-request URL
	// verbatim here. The shared extractor promotes only URLs present in the wire
	// value; it never derives one from repo metadata (honesty invariant). Claude
	// events carrying pr_link_candidates flow through the provider-agnostic
	// session aggregation (attachSessionPRLink) into session.Attributes["pr_link"].
	normalize.AttachPRLinkEvidence(attributes, extensions, fields, claudePRLinkScanFields)
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
func spanCorrelation(eventID string, occurredAt time.Time, traceID, spanID string, parentSpanID any, spanType string) map[string]any {
	// The interaction span is the per-user-prompt root — a genuine, observed task
	// boundary — so its confidence is raised (#100). The llm_request span is a
	// child of that root (one model request within the prompt), not itself a
	// boundary, so it stays "unknown". Tool spans are intra-interaction operations,
	// observed to not be task boundaries (#101); hook spans are intra-interaction
	// interventions, likewise not task boundaries (#103).
	boundary := map[string]any{
		"confidence": "unknown",
		"reason":     "Claude Code trace spans: per-span task-boundary mapping deferred to T-phase",
	}
	switch spanType {
	case "interaction":
		boundary = map[string]any{
			"confidence": "observed",
			"reason":     "Claude Code interaction span is the per-user-prompt root, a genuine task boundary",
		}
	case spanTypeTool, spanTypeToolExecution, spanTypeToolBlockedUser:
		boundary = map[string]any{
			"confidence": "observed",
			"reason":     "Claude Code tool spans are intra-interaction operations, not task boundaries",
		}
	case spanTypeHook:
		boundary = map[string]any{
			"confidence": "observed",
			"reason":     "Claude Code hook span is an intra-interaction intervention, not a task boundary",
		}
	}
	return map[string]any{
		"dedup_key":      eventID,
		"ordering_key":   fmt.Sprintf("%020d:%s", occurredAt.UnixNano(), eventID),
		"trace_id":       traceID,
		"span_id":        spanID,
		"parent_span_id": parentSpanID,
		"task_boundary":  boundary,
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
		return normalize.ProviderNativeSessionID(nativeSessionPrefix, raw)
	}
	return "claude-code:trace:" + traceID
}

// interactionAttributes maps the claude_code.interaction span (the per-user-prompt
// root) into structured, present-only fields. The interaction span carries no
// tokens or latency of its own — those live on its llm_request children — only
// prompt-shape and queueing metadata. Every field is present-only: a genuinely
// absent attribute is omitted, never fabricated. user_prompt_length is a length
// only; the prompt text is never emitted here (and is dropped regardless, #87).
func interactionAttributes(fields map[string]any) map[string]any {
	block := map[string]any{}
	putSpanInt(block, fields, "sequence", "interaction.sequence")
	putSpanInt(block, fields, "duration_ms", "interaction.duration_ms")
	putSpanInt(block, fields, "user_prompt_length", "user_prompt_length")
	putSpanInt(block, fields, "queued_sends", "queued_sends")
	putSpanString(block, fields, "parent_source", "parent.source")
	return block
}

// llmRequestAttributes maps the claude_code.llm_request span (one model request
// under an interaction) into structured, present-only fields: identity/model,
// latency, token counts, outcome, and correlation to any sub-agent workflow. All
// fields are present-only. The raw free-text `error` message is captured raw too
// (epic #87 — a governance/timeline product needs the actual failure text, and the
// per-field hide decision is deferred downstream; this now matches tool.execution
// rather than dropping it "for consistency" with the pre-#87 default), alongside
// the bounded error_class, status_code, and success flag. context comes from
// llm_request.context (query_source is a metrics-only dimension, not a span
// attribute, so it is never invented here). finish_reasons is decoded from the
// OTLP arrayValue that the scalar attribute decoder cannot read.
func llmRequestAttributes(fields map[string]any, attributes []otlpAttribute) map[string]any {
	block := map[string]any{}
	putSpanString(block, fields, "model", "model")
	putSpanString(block, fields, "gen_ai_system", "gen_ai.system")
	putSpanString(block, fields, "gen_ai_request_model", "gen_ai.request.model")
	putSpanString(block, fields, "gen_ai_response_id", "gen_ai.response.id")
	putSpanString(block, fields, "request_id", "request_id")
	putSpanString(block, fields, "context", "llm_request.context")
	putSpanString(block, fields, "query_source_safe", "query_source_safe")
	putSpanString(block, fields, "speed", "speed")
	putSpanString(block, fields, "stop_reason", "stop_reason")
	putSpanString(block, fields, "error_class", "error_class")
	putSpanString(block, fields, "error", "error")
	putSpanString(block, fields, "agent_id", "agent_id")
	putSpanString(block, fields, "parent_agent_id", "parent_agent_id")
	putSpanString(block, fields, "workflow_run_id", attrWorkflowRunID)
	putSpanString(block, fields, "workflow_name", attrWorkflowName)
	putSpanInt(block, fields, "duration_ms", "duration_ms")
	putSpanInt(block, fields, "ttft_ms", "ttft_ms")
	putSpanInt(block, fields, "first_content_ms", "first_content_ms")
	putSpanInt(block, fields, "input_tokens", "input_tokens")
	putSpanInt(block, fields, "output_tokens", "output_tokens")
	putSpanInt(block, fields, "cache_read_tokens", "cache_read_tokens")
	putSpanInt(block, fields, "cache_creation_tokens", "cache_creation_tokens")
	putSpanInt(block, fields, "attempt", "attempt")
	putSpanInt(block, fields, "status_code", "status_code")
	if value, ok := optionalBool(fields, "success"); ok {
		block["success"] = value
	}
	if value, ok := optionalBool(fields, "response.has_tool_call"); ok {
		block["response_has_tool_call"] = value
	}
	if reasons := arrayAttributeValues(attributes, "gen_ai.response.finish_reasons"); reasons != nil {
		block["finish_reasons"] = reasons
	}
	return block
}

// spanUnavailableFields lists the canonical surfaces a span genuinely cannot
// carry, per span type. Tool spans carry tool/file/command evidence (#101), so
// those surfaces are removed from their list rather than falsely declared
// unavailable (the provider rule forbids marking a signal unavailable merely
// because capturing it elsewhere is possible). prompt/response content and
// provider cost never ride on a span.
func spanUnavailableFields(spanType string) []string {
	switch spanType {
	case spanTypeTool, spanTypeToolExecution, spanTypeToolBlockedUser:
		return []string{"mcp_calls", "prompt_content", "response_content", "repository_context", "provider_cost"}
	default:
		return []string{"tool_io", "mcp_calls", "file_operations", "command_execution", "prompt_content", "response_content", "repository_context", "provider_cost"}
	}
}

// toolAttributes maps a claude_code.tool span into a present-only block. Per the
// raw-capture stance (epic #87), every documented field is captured verbatim,
// including the OTEL_LOG_TOOL_DETAILS-gated file_path and full_command. Those two
// use their provider-native keys so the governance layer (internal/governance)
// reaches them and classifies over the raw value.
func toolAttributes(fields map[string]any) map[string]any {
	block := map[string]any{}
	putSpanString(block, fields, "tool_name", "tool_name")
	putSpanString(block, fields, "tool_name_safe", "tool_name_safe")
	putSpanString(block, fields, "bash_command_class", "bash_command_class")
	putSpanString(block, fields, "bash_argv0", "bash_argv0")
	putSpanString(block, fields, "file_path", "file_path")
	putSpanString(block, fields, "full_command", "full_command")
	putSpanString(block, fields, "skill_name", "skill_name")
	putSpanString(block, fields, "subagent_type", "subagent_type")
	putSpanString(block, fields, "tool_use_id", "tool_use_id")
	putSpanString(block, fields, "gen_ai_tool_call_id", attrGenAIToolCallID)
	putSpanString(block, fields, "agent_id", "agent_id")
	putSpanString(block, fields, "parent_agent_id", "parent_agent_id")
	putSpanString(block, fields, "workflow_run_id", attrWorkflowRunID)
	putSpanString(block, fields, "workflow_name", attrWorkflowName)
	putSpanInt(block, fields, "duration_ms", "duration_ms")
	putSpanInt(block, fields, "result_tokens", "result_tokens")
	return block
}

// toolExecutionAttributes maps a claude_code.tool.execution span. success is the
// bounded outcome and error_class the bounded failure category; the free-text
// error message is captured raw as well — a governance/timeline product needs the
// actual error, and the per-field hide decision is deferred downstream (epic #87).
func toolExecutionAttributes(fields map[string]any) map[string]any {
	block := map[string]any{}
	putSpanString(block, fields, "tool_use_id", "tool_use_id")
	putSpanString(block, fields, "gen_ai_tool_call_id", attrGenAIToolCallID)
	putSpanString(block, fields, "error_class", "error_class")
	putSpanString(block, fields, "error", "error")
	putSpanInt(block, fields, "duration_ms", "duration_ms")
	if value, ok := optionalBool(fields, "success"); ok {
		block["success"] = value
	}
	return block
}

// toolBlockedOnUserAttributes maps a claude_code.tool.blocked_on_user span — the
// wall-clock time a tool call spent waiting on a user permission decision, the
// decision itself (accept/reject), and its source.
func toolBlockedOnUserAttributes(fields map[string]any) map[string]any {
	block := map[string]any{}
	putSpanString(block, fields, "decision", "decision")
	putSpanString(block, fields, "source", "source")
	putSpanInt(block, fields, "duration_ms", "duration_ms")
	return block
}

// hookAttributes maps a claude_code.hook span (detailed beta tracing, #103 T16)
// into a present-only block. Hooks are user-configured automation that can block
// or alter agent actions, so these counts and durations make an otherwise-
// invisible intervention observable: hook_event/hook_name identify the hook,
// num_hooks/num_success/num_blocking/num_non_blocking_error/num_cancelled are the
// outcome breakdown, and duration_ms is the wall-clock cost of all matching hooks.
// hook_definitions is the OTEL_LOG_TOOL_DETAILS-gated JSON-serialized hook
// configuration; per the raw-capture stance (epic #87) it is retained verbatim
// here — its canonical home is this typed block (not the allow-listed passthrough,
// mirroring the gated tool file_path/full_command), and the per-field hide
// decision is deferred downstream. Every field is present-only: a genuinely
// absent attribute is omitted, never fabricated.
func hookAttributes(fields map[string]any) map[string]any {
	block := map[string]any{}
	putSpanString(block, fields, "hook_event", "hook_event")
	putSpanString(block, fields, "hook_name", "hook_name")
	putSpanString(block, fields, "hook_definitions", "hook_definitions")
	putSpanInt(block, fields, "num_hooks", "num_hooks")
	putSpanInt(block, fields, "num_success", "num_success")
	putSpanInt(block, fields, "num_blocking", "num_blocking")
	putSpanInt(block, fields, "num_non_blocking_error", "num_non_blocking_error")
	putSpanInt(block, fields, "num_cancelled", "num_cancelled")
	putSpanInt(block, fields, "duration_ms", "duration_ms")
	return block
}

// putSpanString sets dst on block from the src span attribute only when a
// non-empty string was observed, so an absent value is omitted (never fabricated).
func putSpanString(block, fields map[string]any, dst, src string) {
	if value, ok := normalize.ObservedString(fields[src]); ok {
		block[dst] = value
	}
}

// putSpanInt sets dst on block from the src span attribute only when a
// non-negative integer was observed. It reuses OptionalTokenCount (as the
// outcome-contract mapping does for duration_ms), so an absent or unparseable
// value is omitted rather than defaulted to a fabricated 0.
func putSpanInt(block, fields map[string]any, dst, src string) {
	if value := normalize.OptionalTokenCount(fields[src]); value != nil {
		block[dst] = *value
	}
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
