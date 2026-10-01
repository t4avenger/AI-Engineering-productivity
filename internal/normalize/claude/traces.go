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
// redactedContent is the value Claude Code emits for gated span content when its
// gate is off (e.g. user_prompt without OTEL_LOG_USER_PROMPTS).
const redactedContent = "<REDACTED>"

const (
	spanTypeInteraction     = "interaction"
	spanTypeLLMRequest      = "llm_request"
	spanTypeTool            = "tool"
	spanTypeToolExecution   = "tool.execution"
	spanTypeToolBlockedUser = "tool.blocked_on_user"
	spanTypeHook            = "hook"
)

// Span attribute keys shared between spanHomedKeys and the typed attribute
// mappers (llm_request / tool / tool.execution). Kept as named constants so the
// wire key is defined once (go:S1192).
const (
	attrWorkflowRunID   = "workflow.run_id"
	attrWorkflowName    = "workflow.name"
	attrGenAIToolCallID = "gen_ai.tool.call.id"
	attrNewContext      = "new_context"
	attrFinishReasons   = "gen_ai.response.finish_reasons"
)

// spanHomedKeys lists, per span type, the raw content attributes that already
// have a canonical typed home (attributes.<span type>), so the
// provider_extensions.span_attributes echo excludes them rather than carrying a
// second copy: the gated file_path/full_command (#101) and new_context (#253) in
// the tool block, the free-text error in the llm_request/tool_execution blocks
// (epic #87), the gated hook_definitions in the hook block (#103), and
// new_context plus the arrayValue finish_reasons in the llm_request block. A key
// is excluded only on the span types that home it, so it can never fall through
// to being dropped elsewhere. Operator/machine identity is homed in
// provider_extensions.environment (#107 X20) and excluded via
// environmentEventFields. Every other attribute — including unforeseen ones —
// is echoed raw (owner directive: nothing dropped at the local-only ingest
// boundary), mirroring the logs path's provider_extensions.event echo.
var spanHomedKeys = map[string][]string{
	spanTypeInteraction:   {attrNewContext},
	spanTypeLLMRequest:    {"error", attrNewContext, attrFinishReasons},
	spanTypeTool:          {"file_path", "full_command", attrNewContext},
	spanTypeToolExecution: {"error"},
	spanTypeHook:          {"hook_definitions"},
}

// claudePRLinkScanFields are the reviewed Claude span fields that can carry a
// pull/merge-request URL verbatim. full_command is the OTEL_LOG_TOOL_DETAILS-gated
// raw command line on tool spans (#101), so a `gh pr create` / `gh pr view <url>`
// invocation surfaces a PR URL here. The URL grammar and extraction live in
// normalize.AttachPRLinkEvidence, shared with Codex (no CPD-duplicated block).
// OTEL_LOG_TOOL_CONTENT adds the tool span's new_context (the tool call's
// result) and the tool.output span event's output (#253), assembled as
// tool_output by toolSpanPRLinkFields; both were observed carrying a printf'd
// URL on Claude Code 2.1.287 (fixtures/claude/observed-sanitised/
// claude-code-2.1.287-tool-content-spans-otlp.json). The retained log and
// transcript tool surfaces are scanned too (#251): see claudeLogPRLinkScanFields
// and transcriptToolCall.attachPRLinkEvidence.
var claudePRLinkScanFields = []string{"full_command", attrNewContext, "tool_output"}

// claudeLogPRLinkScanFields are the OTEL_LOG_TOOL_DETAILS-gated log attributes
// scanned for a verbatim pull/merge-request URL (#251): tool_parameters on
// tool_decision and tool_result, and tool_input on tool_result. Observed on
// Claude Code 2.1.286 (fixtures/claude/observed-sanitised/
// claude-code-2.1.286-tool-params-pr-link-otlp.json).
var claudeLogPRLinkScanFields = []string{"tool_parameters", "tool_input"}

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
	Events            []otlpSpanEvent `json:"events"`
	Links             []otlpSpanLink  `json:"links"`
	Status            struct {
		Code int `json:"code"`
	} `json:"status"`
}

// otlpSpanEvent decodes an OTLP span event: the documented tool.output event on a
// claude_code.tool span (OTEL_LOG_TOOL_CONTENT, #253) and the
// gen_ai.request.attempt retry event on claude_code.llm_request.
// Optional members are pointers so an omitted wire field stays absent rather than
// becoming a fabricated zero value.
type otlpSpanEvent struct {
	Name                   string          `json:"name"`
	TimeUnixNano           *string         `json:"timeUnixNano"`
	Attributes             []otlpAttribute `json:"attributes"`
	DroppedAttributesCount *int64          `json:"droppedAttributesCount"`
}

// otlpSpanLink decodes an OTLP span link (observed on claude_code.llm_request
// with link.type=parent_of under detailed beta tracing, #253).
type otlpSpanLink struct {
	TraceID                *string         `json:"traceId"`
	SpanID                 *string         `json:"spanId"`
	TraceState             *string         `json:"traceState"`
	Flags                  *int64          `json:"flags"`
	Attributes             []otlpAttribute `json:"attributes"`
	DroppedAttributesCount *int64          `json:"droppedAttributesCount"`
}

// spanContext carries resource- and scope-derived values shared by every span
// under one resource, keeping per-span helpers within the argument limit.
type spanContext struct {
	scopeName        string
	resourceIdentity string
	safeResource     map[string]any
	resourceAttrs    map[string]any
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
// attributes are echoed raw minus keys with a typed home (spanHomedKeys), and span
// events and links are retained raw (#253). Per-span-type field mapping
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
	resourceAttrs := resourceAttributeValues(resource.Resource.Attributes)
	if service, _ := resourceAttrs[attrServiceName].(string); service != claudeLogService {
		return nil, nil
	}
	ctx := spanContext{
		resourceIdentity: resourceIdentityKey(resourceAttrs),
		safeResource:     safeMetricAttributes(resourceAttrs),
		resourceAttrs:    resourceAttrs,
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
		"unavailable_fields": spanUnavailableFields(spanType, fields),
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
	case spanTypeInteraction:
		attributes[spanTypeInteraction] = interactionAttributes(fields)
	case spanTypeLLMRequest:
		attributes[spanTypeLLMRequest] = llmRequestAttributes(fields, span.Attributes)
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
		"correlation": spanCorrelation(fields, eventID, occurredAt, traceID, spanID, parentSpanID, spanType),
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
		"span_attributes": spanAttributesEcho(span.Attributes, spanType),
	}
	attachSpanEventsAndLinks(extensions, span)
	// Session environment and identity (#107 X20): identity keys (user.*/
	// organization.id/terminal.type) ride on the span attributes, the machine/app
	// keys (os.*/host.arch/app.*/workspace.host_paths) on the resource; both are
	// retained raw here — per the owner directive nothing is dropped at the
	// local-only ingest boundary — giving a trace span the same environment surface
	// a log event carries. Present-only, so a span without them omits the block.
	if environment := claudeEnvironment(fields, ctx.resourceAttrs); environment != nil {
		extensions["environment"] = environment
	}
	// A tool span's raw full_command carries the exact command line (#101), so a
	// `gh pr create` / `gh pr view <url>` invocation surfaces a pull-request URL
	// verbatim here; its new_context and tool.output output carry the tool's
	// result (#253). The shared extractor promotes only URLs present in the wire
	// value; it never derives one from repo metadata (honesty invariant). Claude
	// events carrying pr_link_candidates flow through the provider-agnostic
	// session aggregation (attachSessionPRLink) into session.Attributes["pr_link"].
	normalize.AttachPRLinkEvidence(attributes, extensions, toolSpanPRLinkFields(fields, spanType, span.Events), claudePRLinkScanFields)
	event := canonical.Event{
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
	}
	// Derive actor_id/repository_id from the span + resource identity (#107 X20).
	applyEnvironmentIdentity(&event)
	return event, nil
}

// spanCorrelation carries the span-tree linkage (trace/span/parent) alongside
// the dedup/ordering keys, so downstream consumers can rebuild the interaction
// → llm_request/tool/hook hierarchy that spans expose and logs do not.
func spanCorrelation(fields map[string]any, eventID string, occurredAt time.Time, traceID, spanID string, parentSpanID any, spanType string) map[string]any {
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
	case spanTypeInteraction:
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
	correlation := map[string]any{
		"dedup_key":      eventID,
		"ordering_key":   fmt.Sprintf("%020d:%s", occurredAt.UnixNano(), eventID),
		"trace_id":       traceID,
		"span_id":        spanID,
		"parent_span_id": parentSpanID,
		"task_boundary":  boundary,
	}
	// workflow.run_id / workflow.name group a sub-agent workflow's spans (#106).
	// Present-only: absent on a non-subagent span, so it never appears there.
	if value, ok := normalize.ObservedString(fields[attrWorkflowRunID]); ok {
		correlation["workflow_run_id"] = value
	}
	if value, ok := normalize.ObservedString(fields[attrWorkflowName]); ok {
		correlation["workflow_name"] = value
	}
	return correlation
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
// absent attribute is omitted, never fabricated. The gated user_prompt text rides
// raw in the span_attributes echo; new_context (the prompt as context, under
// detailed beta tracing + OTEL_LOG_USER_PROMPTS) is homed here verbatim (#253).
func interactionAttributes(fields map[string]any) map[string]any {
	block := map[string]any{}
	putSpanInt(block, fields, "sequence", "interaction.sequence")
	putSpanInt(block, fields, "duration_ms", "interaction.duration_ms")
	putSpanInt(block, fields, "user_prompt_length", "user_prompt_length")
	putSpanInt(block, fields, "queued_sends", "queued_sends")
	putSpanString(block, fields, "parent_source", "parent.source")
	putSpanRaw(block, fields, attrNewContext, attrNewContext)
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
// llm_request.context (the raw query_source, observed on 2.1.287 spans, rides in
// the span_attributes echo). finish_reasons is decoded from the OTLP arrayValue
// that the scalar attribute decoder cannot read. new_context — the request's new
// user messages and tool results under detailed beta tracing — is homed here
// verbatim (#253).
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
	if reasons := arrayAttributeValues(attributes, attrFinishReasons); reasons != nil {
		block["finish_reasons"] = reasons
	}
	putSpanRaw(block, fields, attrNewContext, attrNewContext)
	return block
}

// spanUnavailableFields lists the canonical surfaces a span genuinely cannot
// carry, per span type. Tool spans carry tool/file/command evidence (#101), so
// those surfaces are removed from their list rather than falsely declared
// unavailable (the provider rule forbids marking a signal unavailable merely
// because capturing it elsewhere is possible). Prompt and response content are
// gated (OTEL_LOG_USER_PROMPTS, detailed beta tracing), so they are declared
// unavailable unless this span observably carries them (spanContentObserved,
// #253). Provider cost never rides on a span.
func spanUnavailableFields(spanType string, fields map[string]any) []string {
	var candidates []string
	switch spanType {
	case spanTypeTool, spanTypeToolExecution, spanTypeToolBlockedUser:
		candidates = []string{"mcp_calls", "prompt_content", "response_content", "repository_context", "provider_cost"}
	default:
		candidates = []string{"tool_io", "mcp_calls", "file_operations", "command_execution", "prompt_content", "response_content", "repository_context", "provider_cost"}
	}
	prompt, response := spanContentObserved(spanType, fields)
	unavailableFields := make([]string, 0, len(candidates))
	for _, field := range candidates {
		if (field == "prompt_content" && prompt) || (field == "response_content" && response) {
			continue
		}
		unavailableFields = append(unavailableFields, field)
	}
	return unavailableFields
}

// spanContentObserved reports whether a span carries prompt or response content.
// Prompt content: an interaction's un-redacted user_prompt, or new_context on an
// interaction (the prompt) or llm_request (the request's new user messages and
// tool results). Response content: an llm_request's response.model_output. The
// tool span's new_context is the tool result (tool_io), not prompt content. A
// redacted value ("<REDACTED>", the gate-off default) is not content.
func spanContentObserved(spanType string, fields map[string]any) (prompt, response bool) {
	switch spanType {
	case spanTypeInteraction:
		prompt = spanContentPresent(fields, "user_prompt") || spanContentPresent(fields, attrNewContext)
	case spanTypeLLMRequest:
		prompt = spanContentPresent(fields, attrNewContext)
		response = spanContentPresent(fields, "response.model_output")
	}
	return prompt, response
}

func spanContentPresent(fields map[string]any, key string) bool {
	text, ok := normalize.ObservedString(fields[key])
	return ok && text != redactedContent
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
	putSpanRaw(block, fields, attrNewContext, attrNewContext)
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
// here — its canonical home is this typed block (not the span_attributes echo,
// mirroring the gated tool file_path/full_command), and the per-field hide
// decision is deferred downstream. Every field is present-only: a genuinely
// absent attribute is omitted, never fabricated.
func hookAttributes(fields map[string]any) map[string]any {
	block := map[string]any{}
	putSpanString(block, fields, "hook_event", "hook_event")
	putSpanString(block, fields, "hook_name", "hook_name")
	putSpanRaw(block, fields, "hook_definitions", "hook_definitions")
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

// putSpanRaw sets dst on block from the src span attribute preserving the
// observed string byte-for-byte, unlike putSpanString which stores the trimmed
// form. It still requires a genuinely non-blank value, so an absent or
// whitespace-only attribute is omitted rather than fabricated. Reserved for raw
// content fields (e.g. the gated hook_definitions) whose exact bytes must be
// retained verbatim under the raw-capture stance (epic #87).
func putSpanRaw(block, fields map[string]any, dst, src string) {
	text, ok := fields[src].(string)
	if !ok || strings.TrimSpace(text) == "" {
		return
	}
	block[dst] = text
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

// spanAttributesEcho returns every span attribute raw except those with a typed
// home for this span type (spanHomedKeys) or in provider_extensions.environment.
// It decodes every OTLP AnyValue shape losslessly (rawAttributeValues), so a
// list-, map- or bytes-valued attribute is not lost to the scalar decoder.
func spanAttributesEcho(attributes []otlpAttribute, spanType string) map[string]any {
	homed := append(environmentEventFields(), spanHomedKeys[spanType]...)
	return normalize.UnknownFields(rawAttributeValues(attributes), homed...)
}

// attachSpanEventsAndLinks retains a span's OTLP events (tool.output under
// OTEL_LOG_TOOL_CONTENT, gen_ai.request.attempt) and links raw and present-only
// (#253): the tool.output output/content/diff is the tool call's actual result,
// captured verbatim.
func attachSpanEventsAndLinks(extensions map[string]any, span otlpSpan) {
	if events := spanEventsRaw(span.Events); events != nil {
		extensions["span_events"] = events
	}
	if links := spanLinksRaw(span.Links); links != nil {
		extensions["span_links"] = links
	}
}

// spanEventsRaw retains OTLP span events verbatim — name, timestamp, decoded
// attributes, dropped-attribute count — returning nil when the span has none so a
// genuine absence stays an absent key. Each optional member is present-only.
func spanEventsRaw(events []otlpSpanEvent) []map[string]any {
	if len(events) == 0 {
		return nil
	}
	raw := make([]map[string]any, 0, len(events))
	for _, event := range events {
		entry := map[string]any{"name": event.Name}
		putPresent(entry, "time_unix_nano", event.TimeUnixNano)
		putPresentAttributes(entry, event.Attributes)
		putPresent(entry, "dropped_attributes_count", event.DroppedAttributesCount)
		raw = append(raw, entry)
	}
	return raw
}

// spanLinksRaw retains OTLP span links verbatim, returning nil when absent. Each
// member is present-only: an omitted traceState/flags/count is never fabricated
// as an empty string or zero.
func spanLinksRaw(links []otlpSpanLink) []map[string]any {
	if len(links) == 0 {
		return nil
	}
	raw := make([]map[string]any, 0, len(links))
	for _, link := range links {
		entry := map[string]any{}
		putPresent(entry, "trace_id", link.TraceID)
		putPresent(entry, "span_id", link.SpanID)
		putPresent(entry, "trace_state", link.TraceState)
		putPresent(entry, "flags", link.Flags)
		putPresentAttributes(entry, link.Attributes)
		putPresent(entry, "dropped_attributes_count", link.DroppedAttributesCount)
		raw = append(raw, entry)
	}
	return raw
}

// putPresent sets key only when the wire member was present.
func putPresent[T any](entry map[string]any, key string, value *T) {
	if value != nil {
		entry[key] = *value
	}
}

// putPresentAttributes sets the losslessly decoded attributes only when the wire
// carried an attributes member (an explicit empty list stays an empty map).
func putPresentAttributes(entry map[string]any, attributes []otlpAttribute) {
	if attributes != nil {
		entry["attributes"] = rawAttributeValues(attributes)
	}
}

// rawAttributeValues decodes OTLP attributes losslessly for the raw echoes
// (span_attributes, span_events, span_links): every AnyValue shape is retained
// verbatim via rawAnyValue, so an unforeseen attribute is never amputated by a
// decoder that only understands scalars (#253).
func rawAttributeValues(attributes []otlpAttribute) map[string]any {
	values := make(map[string]any, len(attributes))
	for _, attribute := range attributes {
		values[attribute.Key] = rawAnyValue(attribute.Value)
	}
	return values
}

// rawAnyValue decodes one OTLP AnyValue recursively and verbatim. Scalars reuse
// attributeValue (so existing scalar shapes are unchanged); an intValue that does
// not parse and a base64 bytesValue are kept as their wire strings; arrayValue
// keeps every member (no trimming or filtering) and kvlistValue becomes a map. An
// empty AnyValue (OTLP's null) is retained as nil rather than dropped.
func rawAnyValue(value map[string]any) any {
	if scalar, ok := attributeValue(value); ok {
		return scalar
	}
	if text, ok := value["intValue"].(string); ok {
		return text
	}
	if bytes, ok := value["bytesValue"].(string); ok {
		return bytes
	}
	if array, ok := value["arrayValue"].(map[string]any); ok {
		return rawArrayValue(array)
	}
	if kvlist, ok := value["kvlistValue"].(map[string]any); ok {
		return rawKVListValue(kvlist)
	}
	return nil
}

func rawArrayValue(array map[string]any) []any {
	members, _ := array["values"].([]any)
	values := make([]any, 0, len(members))
	for _, member := range members {
		decoded, _ := member.(map[string]any)
		values = append(values, rawAnyValue(decoded))
	}
	return values
}

func rawKVListValue(kvlist map[string]any) map[string]any {
	entries, _ := kvlist["values"].([]any)
	values := make(map[string]any, len(entries))
	for _, item := range entries {
		entry, _ := item.(map[string]any)
		key, _ := entry["key"].(string)
		decoded, _ := entry["value"].(map[string]any)
		values[key] = rawAnyValue(decoded)
	}
	return values
}

// toolSpanPRLinkFields assembles the span surfaces claudePRLinkScanFields names.
// full_command is scanned on every span (it only occurs on tool spans). The
// tool-result surfaces — new_context and the tool.output events' output, joined
// as tool_output — are scanned only on a claude_code.tool span: on
// interaction/llm_request spans new_context is conversation context, where a URL
// is a mention rather than the tool's own result, so it is never promoted (#253).
func toolSpanPRLinkFields(fields map[string]any, spanType string, events []otlpSpanEvent) map[string]any {
	scan := map[string]any{"full_command": fields["full_command"]}
	if spanType != spanTypeTool {
		return scan
	}
	scan[attrNewContext] = fields[attrNewContext]
	outputs := make([]string, 0, len(events))
	for _, event := range events {
		if event.Name != "tool.output" {
			continue
		}
		if output, ok := normalize.ObservedString(attributeValues(event.Attributes)["output"]); ok {
			outputs = append(outputs, output)
		}
	}
	if len(outputs) > 0 {
		scan["tool_output"] = strings.Join(outputs, "\n")
	}
	return scan
}
