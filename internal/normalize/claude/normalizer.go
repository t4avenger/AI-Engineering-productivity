// Package claude normalises reviewed, sanitised Claude Code fixtures (OTLP
// HTTP/JSON log events) into canonical events and stable-primitive records.
//
// It is capability-bounded: only signals the P2 Claude Code capability matrix
// marks supported/partial are extracted; every absent signal is reported as an
// explicit unavailable/unknown state, never fabricated. Session and request
// identifiers are retained verbatim as provider-prefixed native IDs; no
// ingest-time hiding is applied (epic #87).
package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/fixture"
	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

const (
	canonicalSchemaVersion = "0.1.0"
	sourceSchema           = "otel"
	unavailable            = "unavailable"
	provider               = "anthropic"
	tool                   = "claude-code"
	// nativeSessionPrefix namespaces provider-native session and request IDs so
	// they never collide with another tool's identifiers.
	nativeSessionPrefix = tool + ":"

	sourceTypeOTLPEvents      = "otlp_http_json_logs"
	sourceTypeCapabilityProbe = "local_cli_capability_probe"

	eventAPIRequest     = "api_request"
	eventAPIError       = "api_error"
	eventAPIRefusal     = "api_refusal"
	eventSkillActivated = "skill_activated"
	eventToolResult     = "tool_result"
	eventToolDecision   = "tool_decision"
	// Content-bearing log events (epic #87 / #94). Their length and metadata
	// primitives and their gated raw content (prompt/response/body/body_ref) ride
	// verbatim under provider_extensions.event via normalize.UnknownFields — the
	// same generic path api_request's numeric primitives take; there is no typed
	// content record. Content keys are deliberately absent from gatedEventFields
	// and logs.go droppedKeys so nothing is re-redacted at ingest.
	eventUserPrompt        = "user_prompt"
	eventAssistantResponse = "assistant_response"
	eventAPIRequestBody    = "api_request_body"
	eventAPIResponseBody   = "api_response_body"

	// Session-state & governance lifecycle events (epic #87 / #96). Each is a pure
	// lifecycle signal carrying no model/token/tool identity, so the default
	// unavailableFields set (everything unavailable) already fits them and
	// attachGovernanceContext stamps a stable provider_extensions.governance
	// contract — one key downstream governance work (internal/governance) can read
	// (e.g. a bypassPermissions transition or a failed auth) without building any
	// rule here, which is out of scope for #96.
	eventPermissionModeChanged = "permission_mode_changed"
	eventAuth                  = "auth"
	eventPluginLoaded          = "plugin_loaded"

	// permissionModeBypass disables every tool-permission prompt for the rest of the
	// session, so a transition into it is the governance red flag callers key on.
	permissionModeBypass = "bypassPermissions"
)

// NormalizeEvents maps the reviewed Claude Code OTLP event fixture into
// canonical events, one per sample event. It validates through the shared
// fixture boundary (fixture.Validate rejects prohibited fields and likely
// secrets) before mapping; it does not itself run the privacy sanitiser, so a
// live payload must be sanitised upstream first. It never persists or logs the
// fixture.
//
// A capability-probe fixture carries no events, so it yields an empty slice
// rather than a fabricated all-unknown record. Session and request identifiers
// are retained raw.
func NormalizeEvents(data []byte) ([]canonical.Event, error) {
	document, capturedAt, err := decodeDocument(data)
	if err != nil {
		return nil, err
	}
	switch document.Payload.SourceType {
	case sourceTypeOTLPEvents:
	case sourceTypeCapabilityProbe:
		return []canonical.Event{}, nil
	default:
		return nil, fmt.Errorf("unsupported Claude payload source_type %q", document.Payload.SourceType)
	}
	events := make([]canonical.Event, 0, len(document.Payload.SampleEvents))
	for index, raw := range document.Payload.SampleEvents {
		event, err := normaliseSampleEvent(document, capturedAt, index, raw)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return normalize.CorrelateEvents(events), nil
}

func normaliseSampleEvent(document fixtureDocument, capturedAt time.Time, index int, raw map[string]any) (canonical.Event, error) {
	name, err := normalize.RequiredString(raw, "event_name")
	if err != nil {
		return canonical.Event{}, err
	}
	sessionID, err := normalize.RequiredString(raw, "session_id")
	if err != nil {
		return canonical.Event{}, err
	}
	occurredAt, err := eventTime(raw, "event_timestamp")
	if err != nil {
		return canonical.Event{}, err
	}
	nativeSessionID := normalize.ProviderNativeSessionID(nativeSessionPrefix, sessionID)
	eventID := nativeSessionID + ":" + sequenceKey(raw, index)

	extensions := map[string]any{
		"correlation": eventCorrelation(eventID, occurredAt),
		"event":       normalize.UnknownFields(raw, append(promotedEventFields(name), gatedEventFields()...)...),
	}
	if requestID := normalize.OptionalString(raw, "request_id"); requestID != nil {
		extensions["request_id"] = nativeSessionPrefix + *requestID
	}
	attributes := map[string]any{"unavailable_fields": unavailableFields(name)}
	attachSkillDetection(extensions, raw, name)
	attachOutcomeContract(extensions, raw, name)
	attachGovernanceContext(extensions, raw, name)
	attachToolDecision(extensions, attributes, raw, name, nativeSessionID, eventID)
	return canonical.Event{
		SchemaVersion: canonicalSchemaVersion, EventID: eventID, EventType: name,
		OccurredAt: occurredAt, ReceivedAt: capturedAt, Provider: provider, Tool: tool,
		SourceSchema: sourceSchema, SourceVersion: document.ToolVersion, ActorID: unavailable, DeviceID: unavailable,
		SessionID: nativeSessionID, TaskID: nil, RepositoryID: nil, PrivacyLevel: "operational",
		Attributes:         attributes,
		ProviderExtensions: extensions,
	}, nil
}

// attachToolDecision stamps canonical approval attributes and a raw-preserving
// provider extension on tool_decision events — the only Claude signal that
// reports a permission grant or denial (accept/reject) together with its source
// (config/hook/user_permanent/user_temporary/user_abort/user_reject). Reject
// decisions appear only here: the tool_result event's decision_type is always
// "accept", so without this event a denied tool call is invisible.
//
// The canonical approval_decision is normalised to the cross-provider
// approved/denied vocabulary (matching codex) so the capability matrix reads
// consistently; the untouched wire value is preserved under
// provider_extensions.tool_decision.raw_decision, so nothing is hidden (epic
// #87). The approval_id shares the tool_use_id key with the E5 operation id
// (...:tool:<tool_use_id>), so a decision correlates to its result.
func attachToolDecision(extensions, attributes, raw map[string]any, eventName, nativeSessionID, eventID string) {
	if eventName != eventToolDecision {
		return
	}
	// Without a provider tool_use_id, fall back to the per-event ID (unique per
	// session and sequence) rather than a constant literal, so two decisions that
	// both lack a tool_use_id do not collapse onto one approval_id.
	approvalID := eventID
	if toolUseID, ok := normalize.ObservedString(raw["tool_use_id"]); ok {
		approvalID = nativeSessionID + ":approval:" + toolUseID
	}
	decision := claudeApprovalDecisionStatus(raw["decision"])
	attributes["approval_id"] = approvalID
	attributes["approval_decision"] = decision
	if reasonClass, ok := normalize.ObservedString(raw["source"]); ok {
		attributes["approval_reason_class"] = reasonClass
	}
	for _, key := range []string{"tool_name", "tool_source"} {
		if value, ok := raw[key]; ok {
			attributes[key] = value
		}
	}
	extension := map[string]any{
		"approval_id": approvalID,
		"decision":    decision,
		"provenance":  string(canonical.ProvenanceObserved),
	}
	for _, key := range toolDecisionFieldKeys() {
		if value, ok := raw[key]; ok {
			if key == "decision" {
				extension["raw_decision"] = value
				continue
			}
			extension[key] = value
		}
	}
	extensions["tool_decision"] = extension
}

// claudeApprovalDecisionStatus normalises the wire decision onto the
// cross-provider approved/denied vocabulary. Claude emits bare accept/reject; a
// missing decision is reported as unknown and never inferred from any other
// signal, and an unrecognised value passes through verbatim rather than being
// forced into a bucket.
func claudeApprovalDecisionStatus(value any) string {
	decision, ok := normalize.ObservedString(value)
	if !ok {
		return "unknown"
	}
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "accept", "accepted", "approve", "approved", "allow", "allowed":
		return "approved"
	case "reject", "rejected", "deny", "denied", "abort", "aborted", "blocked":
		return "denied"
	default:
		return decision
	}
}

// toolDecisionFieldKeys are the wire fields preserved verbatim under
// provider_extensions.tool_decision. tool_parameters is deliberately excluded —
// it carries gated content (full commands, MCP server/tool names) and is dropped
// at the wire boundary by NormalizeLogs (logs.go droppedKeys).
func toolDecisionFieldKeys() []string {
	return []string{"decision", "source", "tool_name", "tool_source", "tool_use_id"}
}

// attachSkillDetection stamps explicit skill identity on skill_activated events.
// Other events omit skill_detection so the insight reports unknown rather than a
// fabricated unavailable state for a surface we have not proven cannot carry skills.
func attachSkillDetection(extensions map[string]any, raw map[string]any, eventName string) {
	if eventName != eventSkillActivated {
		return
	}
	extensions["skill_detection"] = "explicit"
	skill := map[string]any{}
	if name := skillName(raw); name != "" {
		skill["name"] = name
	}
	if outcome := skillOutcome(raw); outcome != "" {
		skill["outcome"] = outcome
	}
	if trigger := normalize.OptionalString(raw, "invocation_trigger"); trigger != nil {
		skill["invocation_trigger"] = *trigger
	}
	if source := firstString(raw, "skill.source", "skill_source"); source != "" {
		skill["source"] = source
	}
	if len(skill) > 0 {
		extensions["skill"] = skill
	}
}

// attachOutcomeContract stamps a provider-completion outcome contract for
// reviewed Claude signals: api_request → success, api_error → failed,
// api_refusal → refused. A refusal (Messages API stop_reason: "refusal") is a
// distinct outcome — neither success nor error — that arrives as HTTP 200 and
// is invisible to error-rate monitoring. Broader task contracts
// (PR/revert/abandon) remain unobserved and are not fabricated.
func attachOutcomeContract(extensions map[string]any, raw map[string]any, eventName string) {
	var status string
	switch eventName {
	case eventAPIRequest:
		status = "success"
	case eventAPIError:
		status = "failed"
	case eventAPIRefusal:
		status = "refused"
	default:
		return
	}
	contract := map[string]any{
		"source":     "provider_completion",
		"status":     status,
		"confidence": "observed",
	}
	if model := firstString(raw, "model"); model != "" {
		contract["model"] = model
	}
	if duration := normalize.OptionalTokenCount(raw["duration_ms"]); duration != nil {
		contract["duration_ms"] = *duration
	}
	if input := normalize.OptionalTokenCount(raw["input_tokens"]); input != nil {
		contract["input_tokens"] = *input
	}
	if output := normalize.OptionalTokenCount(raw["output_tokens"]); output != nil {
		contract["output_tokens"] = *output
	}
	if code := outcomeErrorCode(raw); code != "" {
		contract["error_code"] = code
	}
	if attempt := normalize.OptionalTokenCount(raw["attempt"]); attempt != nil && *attempt > 0 {
		contract["retry_attempt"] = *attempt
	}
	if eventName == eventAPIRefusal {
		attachRefusalContext(contract, raw)
	}
	extensions["outcome_contract"] = contract
}

// attachRefusalContext carries the api_refusal-specific attributes verbatim (epic
// #87 — raw capture, no ingest redaction). server_fallback_hop distinguishes a
// user-visible refusal (false) from one the server-side model fallback silently
// retried on another model (true); a single turn can emit a true hop event and a
// later false final event, so callers counting user-visible refusals must filter
// on server_fallback_hop == false rather than counting every api_refusal. The
// category/explanation strings are present only under OTEL_LOG_TOOL_DETAILS=1;
// the has_* booleans are always emitted, so an absent category is explicit rather
// than silently missing.
func attachRefusalContext(contract, raw map[string]any) {
	if hop, ok := firstBool(raw, "server_fallback_hop"); ok {
		contract["server_fallback_hop"] = hop
	}
	if hasCategory, ok := firstBool(raw, "has_category"); ok {
		contract["has_category"] = hasCategory
	}
	if hasExplanation, ok := firstBool(raw, "has_explanation"); ok {
		contract["has_explanation"] = hasExplanation
	}
	if category := firstString(raw, "category"); category != "" {
		contract["category"] = category
	}
	if explanation := firstString(raw, "explanation"); explanation != "" {
		contract["explanation"] = explanation
	}
}

// attachGovernanceContext stamps a stable provider_extensions.governance contract
// for the three Claude Code session-state & governance lifecycle events
// (permission_mode_changed, auth, plugin_loaded — epic #87 / #96). These events
// carry no model/token/tool identity, so they are neither outcome nor operation
// records; they describe how the session was governed and configured. The contract
// gives downstream governance work (internal/governance) one key to read — a
// bypassPermissions transition, a failed auth — without building any rule here
// (out of scope for #96). Values ride verbatim (epic #87 — no ingest redaction);
// no credential-adjacent field is promoted, and account identifiers
// (user.email/user.account_id) are already dropped at the wire boundary
// (logs.go droppedKeyPrefixes).
//
// Both the reviewed sample-event path and the raw /v1/logs wire path reach this
// with the same keys: the wire path leaves event-specific attributes under their
// emitted names (dotted for plugin.*), so the plugin fields are read under both
// dotted and underscore spellings for cross-path parity.
func attachGovernanceContext(extensions, raw map[string]any, eventName string) {
	var governance map[string]any
	switch eventName {
	case eventPermissionModeChanged:
		governance = permissionModeContext(raw)
	case eventAuth:
		governance = authContext(raw)
	case eventPluginLoaded:
		governance = pluginLoadedContext(raw)
	default:
		return
	}
	extensions["governance"] = governance
}

// permissionModeContext normalises a permission_mode_changed event. Claude Code
// emits it only on a real transition (from_mode != to_mode), with an optional
// trigger (shift_tab/exit_plan_mode/auto_gate_denied/auto_opt_in). bypass_permissions
// is stamped explicit rather than left to be re-derived from to_mode downstream.
func permissionModeContext(raw map[string]any) map[string]any {
	governance := map[string]any{"kind": eventPermissionModeChanged}
	if from := firstString(raw, "from_mode"); from != "" {
		governance["from_mode"] = from
	}
	to := firstString(raw, "to_mode")
	if to != "" {
		governance["to_mode"] = to
	}
	if trigger := firstString(raw, "trigger"); trigger != "" {
		governance["trigger"] = trigger
	}
	governance["bypass_permissions"] = to == permissionModeBypass
	return governance
}

// authContext normalises an auth (login/logout) event. success is emitted as a
// stringified boolean; error_category/status_code are present only on a failed
// auth, so their presence is itself the failure signal and they ride verbatim.
func authContext(raw map[string]any) map[string]any {
	governance := map[string]any{"kind": eventAuth}
	if action := firstString(raw, "action"); action != "" {
		governance["action"] = action
	}
	if success, ok := optionalBool(raw, "success"); ok {
		governance["success"] = success
	}
	if method := firstString(raw, "auth_method"); method != "" {
		governance["auth_method"] = method
	}
	if category := firstString(raw, "error_category"); category != "" {
		governance["error_category"] = category
	}
	if status := normalize.OptionalTokenCount(raw["status_code"]); status != nil {
		governance["status_code"] = *status
	}
	return governance
}

// pluginLoadedContext normalises a plugin_loaded event. Claude Code already
// collapses a third-party plugin's name and marketplace to the literal
// "third-party" and pre-hashes the identity (plugin_id_hash) at emit time, so no
// plugin-author identity is captured here. The string identity fields are dotted
// on the wire (plugin.name), so both spellings are read for cross-path parity.
func pluginLoadedContext(raw map[string]any) map[string]any {
	governance := map[string]any{"kind": eventPluginLoaded}
	for key, sources := range map[string][]string{
		"plugin_name":      {"plugin.name", "plugin_name"},
		"plugin_version":   {"plugin.version", "plugin_version"},
		"plugin_scope":     {"plugin.scope", "plugin_scope"},
		"marketplace_name": {"marketplace.name", "marketplace_name"},
		"enabled_via":      {"enabled_via"},
		"plugin_id_hash":   {"plugin_id_hash"},
	} {
		if value := firstString(raw, sources...); value != "" {
			governance[key] = value
		}
	}
	for _, key := range []string{"has_hooks", "has_mcp", "host_owned_mcp"} {
		if value, ok := firstBool(raw, key); ok {
			governance[key] = value
		}
	}
	for _, key := range []string{"skill_path_count", "command_path_count", "agent_path_count"} {
		if value := normalize.OptionalTokenCount(raw[key]); value != nil {
			governance[key] = *value
		}
	}
	if safeMode, ok := optionalBool(raw, "safe_mode"); ok {
		governance["safe_mode"] = safeMode
	}
	return governance
}

// optionalBool reads a bool Claude Code may emit either as a native boolean
// (has_hooks) or as a stringified boolean (success, safe_mode — emitted via
// String(bool)). It returns ok=false when the key is absent or unparseable, so an
// absent flag stays absent rather than defaulting to false.
func optionalBool(raw map[string]any, key string) (bool, bool) {
	if value, ok := raw[key].(bool); ok {
		return value, true
	}
	if text, ok := normalize.ObservedString(raw[key]); ok {
		if parsed, err := strconv.ParseBool(strings.TrimSpace(text)); err == nil {
			return parsed, true
		}
	}
	return false, false
}

func outcomeErrorCode(raw map[string]any) string {
	if code := firstString(raw, "error", "error_code"); code != "" {
		return code
	}
	if status := normalize.OptionalTokenCount(raw["status_code"]); status != nil {
		return "http_" + strconv.FormatInt(*status, 10)
	}
	return ""
}

func skillName(raw map[string]any) string {
	return firstString(raw, "skill.name", "skill_name")
}

func skillOutcome(raw map[string]any) string {
	if status := firstString(raw, "skill.status", "skill_status"); status != "" {
		switch status {
		case "success", "ok":
			return "success"
		case "error", "failed", "failure":
			return "failed"
		default:
			return status
		}
	}
	return ""
}

func firstString(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := normalize.OptionalString(raw, key); value != nil && *value != "" {
			return *value
		}
	}
	return ""
}

// firstBool returns the first key that decodes to a bool. Both the sample-event
// fixture path (native JSON bool) and the OTLP wire path (logs.go attributeValue
// decodes boolValue) present these as Go bools, so a typed assertion suffices.
// The second return distinguishes an absent key from an explicit false.
func firstBool(raw map[string]any, keys ...string) (bool, bool) {
	for _, key := range keys {
		if value, ok := raw[key].(bool); ok {
			return value, true
		}
	}
	return false, false
}

func promotedEventFields(eventName string) []string {
	fields := []string{"event_name", "event_timestamp", "event_sequence", "session_id", "request_id"}
	switch eventName {
	case eventSkillActivated:
		return append(fields, "skill.name", "skill_name", "skill.status", "skill_status", "invocation_trigger", "skill.source", "skill_source")
	case eventToolDecision:
		// The decision fields are promoted verbatim into
		// provider_extensions.tool_decision, so they must not double-echo under
		// provider_extensions.event.
		return append(fields, "decision", "source", "tool_name", "tool_source", "tool_use_id")
	case eventPermissionModeChanged:
		return append(fields, "from_mode", "to_mode", "trigger")
	case eventAuth:
		return append(fields, "action", "success", "auth_method", "error_category", "status_code")
	case eventPluginLoaded:
		// Both dotted (wire) and underscore spellings are excluded so the fields
		// promoted into provider_extensions.governance never double-echo under
		// provider_extensions.event on either path.
		return append(fields,
			"plugin.name", "plugin_name", "plugin.version", "plugin_version",
			"plugin.scope", "plugin_scope", "marketplace.name", "marketplace_name",
			"enabled_via", "plugin_id_hash", "has_hooks", "has_mcp", "host_owned_mcp",
			"skill_path_count", "command_path_count", "agent_path_count", "safe_mode")
	}
	return fields
}

// gatedEventFields carry content that must never surface under
// provider_extensions.event, whatever the event type. tool_parameters holds the
// full command and MCP server/tool names (present under OTEL_LOG_TOOL_DETAILS=1);
// NormalizeLogs drops it at the wire boundary (logs.go droppedKeys), and it is
// dropped from the event echo here too — NormalizeEvents replays reviewed
// fixtures whose validator does not prohibit this key, so this is the
// defence-in-depth that makes "gated content never surfaces" hold on both paths
// (epic #87 keeps behaviour, not command bodies).
func gatedEventFields() []string {
	return []string{"tool_parameters"}
}

// unavailableFields lists the behaviour signals a Claude Code event does not
// carry, so an absent signal is explicit rather than silently missing. The
// api_request event carries model and token identity; the connection and
// skill_activated events carry neither. Skill identity is reported only on
// skill_activated via skill_detection — it is never listed as unavailable on
// other events just because those events are not skill events.
func unavailableFields(eventName string) []string {
	common := []string{"tool_calls", "mcp_calls", "file_operations", "reasoning_tokens", "repository_context", "prompt_content", "response_content", "provider_cost", "trace_span_correlation", "approvals"}
	switch eventName {
	case eventAPIRequest, eventAPIError, eventAPIRefusal:
		// Provider-completion outcome contracts are stamped for these events;
		// none carries a permission decision, so approvals stays unavailable.
		return common
	case eventToolResult:
		// tool_result is the first real evidence of an executed tool call, so
		// tool_calls is not unavailable here; the typed tool-call signal is
		// promoted into canonical.Operation by ExtractOperations. It carries no
		// canonical approval attributes (its decision_type is always accept — the
		// authoritative grant/denial is tool_decision), so approvals stays
		// unavailable. The event carries no model/token identity of its own.
		return []string{"model", "token_usage", "cache_usage", "task_outcome", "approvals", "mcp_calls", "file_operations", "reasoning_tokens", "repository_context", "prompt_content", "response_content", "provider_cost", "trace_span_correlation"}
	case eventToolDecision:
		// tool_decision is the authoritative permission grant/denial signal, so
		// approvals is available (removed) here; the decision references a tool
		// but is not evidence of an executed call, so tool_calls stays
		// unavailable. It carries no model/token identity of its own.
		return []string{"model", "token_usage", "cache_usage", "task_outcome", "tool_calls", "mcp_calls", "file_operations", "reasoning_tokens", "repository_context", "prompt_content", "response_content", "provider_cost", "trace_span_correlation"}
	case eventUserPrompt:
		// user_prompt carries the prompt text (behind OTEL_LOG_USER_PROMPTS), so
		// prompt_content is available (removed). It carries no model, tokens, or
		// response of its own.
		return []string{"model", "token_usage", "cache_usage", "task_outcome", "tool_calls", "mcp_calls", "file_operations", "reasoning_tokens", "repository_context", "response_content", "provider_cost", "trace_span_correlation", "approvals"}
	case eventAssistantResponse:
		// assistant_response carries the response text (behind
		// OTEL_LOG_ASSISTANT_RESPONSES) and the answering model, so response_content
		// and model are available (removed). It reports response_length, not tokens.
		return []string{"token_usage", "cache_usage", "task_outcome", "tool_calls", "mcp_calls", "file_operations", "reasoning_tokens", "repository_context", "prompt_content", "provider_cost", "trace_span_correlation", "approvals"}
	case eventAPIRequestBody:
		// api_request_body is the full Messages API request (behind
		// OTEL_LOG_RAW_API_BODIES): its body is the conversation/prompts, so
		// prompt_content and model are available (removed); it carries no response.
		return []string{"token_usage", "cache_usage", "task_outcome", "tool_calls", "mcp_calls", "file_operations", "reasoning_tokens", "repository_context", "response_content", "provider_cost", "trace_span_correlation", "approvals"}
	case eventAPIResponseBody:
		// api_response_body is the full Messages API response (behind
		// OTEL_LOG_RAW_API_BODIES): its body is the model output, so
		// response_content and model are available (removed); it carries no prompt.
		return []string{"token_usage", "cache_usage", "task_outcome", "tool_calls", "mcp_calls", "file_operations", "reasoning_tokens", "repository_context", "prompt_content", "provider_cost", "trace_span_correlation", "approvals"}
	default:
		return append([]string{"model", "token_usage", "cache_usage", "task_outcome"}, common...)
	}
}

func eventCorrelation(eventID string, occurredAt time.Time) map[string]any {
	return map[string]any{
		"dedup_key":    eventID,
		"ordering_key": fmt.Sprintf("%020d:%s", occurredAt.UnixNano(), eventID),
		"task_boundary": map[string]any{
			"confidence": "unknown",
			"reason":     "Claude Code event telemetry has no reviewed task-boundary signal",
		},
	}
}

// fixtureDocument is the reviewed Claude Code fixture wrapper. Sample events are
// kept as raw maps so unmapped safe fields survive verbatim in
// provider_extensions.
type fixtureDocument struct {
	Provider    string  `json:"provider"`
	Tool        string  `json:"tool"`
	ToolVersion string  `json:"tool_version"`
	CapturedAt  string  `json:"captured_at"`
	Payload     payload `json:"payload"`
}

type payload struct {
	SourceType   string           `json:"source_type"`
	SampleEvents []map[string]any `json:"sample_events"`
}

// decodeDocument validates the fixture through the shared boundary, then
// decodes and checks the provider/tool identity and captured_at timestamp.
func decodeDocument(data []byte) (fixtureDocument, time.Time, error) {
	if err := fixture.Validate(data); err != nil {
		return fixtureDocument{}, time.Time{}, fmt.Errorf("validate Claude fixture: %w", err)
	}
	var document fixtureDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return fixtureDocument{}, time.Time{}, errors.New("decode Claude fixture")
	}
	if document.Provider != provider || document.Tool != tool {
		return fixtureDocument{}, time.Time{}, errors.New("supported fixture provider and tool are anthropic and claude-code")
	}
	capturedAt, err := time.Parse(time.RFC3339, document.CapturedAt)
	if err != nil {
		return fixtureDocument{}, time.Time{}, errors.New("claude fixture captured_at must be RFC3339")
	}
	return document, capturedAt.UTC(), nil
}

// eventTime parses an RFC3339 event timestamp (fractional seconds allowed). A
// missing or invalid timestamp is rejected rather than defaulted, so a
// canonical event never carries a fabricated time.
func eventTime(value map[string]any, key string) (time.Time, error) {
	raw, err := normalize.RequiredString(value, key)
	if err != nil {
		return time.Time{}, err
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be an RFC3339 timestamp", key)
	}
	return parsed.UTC(), nil
}

// sequenceKey renders event_sequence as a stable integer key, falling back to
// the fixture ordinal when the sequence is absent, so an event ID is always
// deterministic.
func sequenceKey(value map[string]any, index int) string {
	if number, ok := value["event_sequence"].(float64); ok && number == float64(int64(number)) {
		return strconv.FormatInt(int64(number), 10)
	}
	return "idx" + strconv.Itoa(index)
}
