package codex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// contentID derives a stable, non-keyed uniqueness key for a record that
// carries no provider-native ID. It is a plain content hash for deduplication
// only — not a privacy transform (epic #87 removed ingest-time hiding).
func contentID(prefix string, data []byte) string {
	sum := sha256.Sum256(data)
	return prefix + hex.EncodeToString(sum[:])
}

// ErrUnsupportedLogs indicates that a valid OTLP log payload is not the
// observed Codex log shape and therefore must not be normalised by this adapter.
var ErrUnsupportedLogs = errors.New("unsupported Codex log payload")

const (
	codexEventNameKey        = "event.name"
	codexServiceNameKey      = "service.name"
	codexToolResultEvent     = "codex.tool_result"
	codexSandboxOutcomeEvent = "codex.sandbox_outcome"
	codexToolDecisionEvent   = "codex.tool_decision"
	codexConversationStarts  = "codex.conversation_starts"
	codexStartupPhaseEvent   = "codex.startup_phase"
	codexWebsocketConnect    = "codex.websocket_connect"
)

type logsPayload struct {
	ResourceLogs []resourceLog `json:"resourceLogs"`
}

type resourceLog struct {
	Resource struct {
		Attributes []attribute `json:"attributes"`
	} `json:"resource"`
	ScopeLogs []scopeLog `json:"scopeLogs"`
}

type scopeLog struct {
	LogRecords []logRecord `json:"logRecords"`
}

type logRecord struct {
	Attributes           []attribute     `json:"attributes"`
	Body                 json.RawMessage `json:"body"`
	ObservedTimeUnixNano string          `json:"observedTimeUnixNano"`
	SeverityText         string          `json:"severityText"`
	TimeUnixNano         string          `json:"timeUnixNano"`
}

// NormalizeLogs maps the reviewed Codex OTLP log shape directly to canonical
// events. When a conversation.id is present, it becomes the provider-prefixed
// native session ID. Records without that field fall back to a non-keyed
// content ID for uniqueness only (epic #87 — no ingest-time hiding).
func NormalizeLogs(data []byte, receivedAt time.Time) ([]canonical.Event, error) {
	var payload logsPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode Codex OTLP logs: %w", err)
	}
	var events []canonical.Event
	for _, raw := range payload.ResourceLogs {
		normalized, err := normalizeResourceLog(raw, receivedAt)
		if err != nil {
			return nil, err
		}
		events = append(events, normalized...)
	}
	if len(events) == 0 {
		return nil, ErrUnsupportedLogs
	}
	return events, nil
}

func normalizeResourceLog(raw resourceLog, receivedAt time.Time) ([]canonical.Event, error) {
	resource := attributes(raw.Resource.Attributes)
	if !isCodexLogService(resource[codexServiceNameKey]) {
		return nil, nil
	}
	var events []canonical.Event
	for _, scope := range raw.ScopeLogs {
		for _, record := range scope.LogRecords {
			event, err := normalizeLogRecord(resource, record, receivedAt)
			if err != nil {
				return nil, err
			}
			events = append(events, event)
		}
	}
	return events, nil
}

func normalizeLogRecord(resource map[string]any, record logRecord, receivedAt time.Time) (canonical.Event, error) {
	recordData, err := json.Marshal(record)
	if err != nil {
		return canonical.Event{}, fmt.Errorf("marshal Codex log record: %w", err)
	}
	id := contentID("codex-log:", recordData)
	fields := attributes(record.Attributes)
	sessionID := codexLogSessionID(fields, id)
	eventName := stringValue(fields[codexEventNameKey], "codex.log.received")
	canonicalEventType := codexCanonicalEventType(eventName)
	occurredAt := receivedAt.UTC()
	if codexLifecycleEvent(eventName) {
		occurredAt = tolerantNano(record.TimeUnixNano, receivedAt).value
	}
	attributes := map[string]any{"unavailable_fields": codexLogUnavailableFields(eventName)}
	for _, key := range []string{"model"} {
		if value, ok := fields[key]; ok {
			attributes[key] = value
		}
	}
	attachCodexLogTokenCounts(attributes, fields)
	if !hasCodexOutcomeContract(eventName) {
		attributes["unavailable_fields"] = append(attributes["unavailable_fields"].([]string), "task_outcome")
	}
	extensions := map[string]any{"resource_attributes": codexLogResourceAttributes(eventName, resource), "log_attributes": codexLogAttributes(fields), "severity": record.SeverityText}
	attachCodexLifecycleSignal(attributes, extensions, resource, fields, eventName)
	attachCodexLogSignals(attributes, extensions, fields, id, sessionID)
	attachCodexPRLinkEvidence(attributes, extensions, fields)
	if mcpCall, ok := codexMCPCall(fields); ok {
		attributes["category"] = string(canonical.OperationCategoryMCPCall)
		extensions["mcp_call"] = mcpCall
	}
	attachCodexOutcomeContract(extensions, fields, eventName)
	return canonical.Event{SchemaVersion: canonicalSchemaVersion, EventID: id, EventType: canonicalEventType, OccurredAt: occurredAt.UTC(), ReceivedAt: receivedAt.UTC(), Provider: "openai", Tool: "codex", SourceSchema: sourceSchema, SourceVersion: stringValue(resource["service.version"], unavailable), ActorID: unavailable, DeviceID: unavailable, SessionID: sessionID, PrivacyLevel: "operational", Attributes: attributes, ProviderExtensions: extensions}, nil
}

func codexCanonicalEventType(eventName string) string {
	if eventName == codexConversationStarts {
		return "session.active"
	}
	return eventName
}

func attachCodexLogTokenCounts(attributes, fields map[string]any) {
	for _, mapping := range []struct {
		source, target string
	}{
		{source: "input_token_count", target: "input_token_count"},
		{source: "output_token_count", target: "output_token_count"},
		{source: "cached_input_token_count", target: "cached_input_token_count"},
		{source: "cached_token_count", target: "cached_input_token_count"},
		{source: "reasoning_token_count", target: "reasoning_token_count"},
	} {
		if _, exists := attributes[mapping.target]; exists {
			continue
		}
		count := normalize.OptionalTokenCount(fields[mapping.source])
		if count == nil {
			continue
		}
		attributes[mapping.target] = *count
		unavailable := attributes["unavailable_fields"].([]string)
		switch mapping.target {
		case "cached_input_token_count":
			attributes["unavailable_fields"] = removeUnavailableField(unavailable, "cache_usage")
		case "reasoning_token_count":
			attributes["unavailable_fields"] = removeUnavailableField(unavailable, "reasoning_tokens")
		}
	}
}

func codexLogSessionID(fields map[string]any, fallback string) string {
	if conversationID, ok := normalize.ObservedString(fields["conversation.id"]); ok {
		return normalize.ProviderNativeSessionID("codex:", conversationID)
	}
	return fallback
}

func attachCodexLogSignals(attributes, extensions map[string]any, fields map[string]any, id, sessionID string) {
	attachCodexToolCallSignal(attributes, extensions, fields, id, sessionID)
	attachCodexSandboxOutcomeSignal(attributes, extensions, fields, id, sessionID)
	attachCodexToolDecisionSignal(attributes, extensions, fields, id, sessionID)
}

func attachCodexLifecycleSignal(attributes, extensions, resource, fields map[string]any, eventName string) {
	if !codexLifecycleEvent(eventName) {
		return
	}
	lifecycle := map[string]any{
		"source_event": eventName,
		"provenance":   string(canonical.ProvenanceObserved),
	}
	switch eventName {
	case codexConversationStarts:
		attributes["lifecycle_kind"] = "session_start"
		lifecycle["kind"] = "session_start"
	case codexStartupPhaseEvent:
		attributes["lifecycle_kind"] = "startup_phase"
		lifecycle["kind"] = "startup_phase"
		if phase, ok := normalize.ObservedString(fields["startup.phase"]); ok {
			attributes["lifecycle_phase"] = phase
			lifecycle["phase"] = phase
		}
		if status, ok := normalize.ObservedString(fields["startup.status"]); ok {
			attributes["lifecycle_status"] = status
			lifecycle["status"] = status
		}
	case codexWebsocketConnect:
		attributes["lifecycle_kind"] = "websocket_connect"
		lifecycle["kind"] = "websocket_connect"
		if status := codexSuccessStatus(fields["success"]); status != "" {
			attributes["lifecycle_status"] = status
			lifecycle["status"] = status
		}
	}
	if entrypoint := codexEntrypoint(resource[codexServiceNameKey]); entrypoint != "" {
		attributes["entrypoint"] = entrypoint
		lifecycle["entrypoint"] = entrypoint
	}
	if duration := normalize.OptionalTokenCount(fields["duration_ms"]); duration != nil {
		attributes["duration_ms"] = *duration
		lifecycle["duration_ms"] = *duration
	}
	for _, key := range codexLifecycleFieldKeys(eventName) {
		if value, ok := fields[key]; ok {
			lifecycle[key] = value
		}
	}
	extensions["session_lifecycle"] = lifecycle
}

func attachCodexToolCallSignal(attributes, extensions map[string]any, fields map[string]any, id, sessionID string) {
	toolCall, ok := codexToolCall(fields, id, sessionID)
	if !ok {
		return
	}
	mergeSignalAttributes(attributes, toolCall.attributes)
	extensions["tool_call"] = toolCall.providerExtension
}

func attachCodexSandboxOutcomeSignal(attributes, extensions map[string]any, fields map[string]any, id, sessionID string) {
	sandboxOutcome, ok := codexSandboxOutcome(fields, id, sessionID)
	if !ok {
		return
	}
	mergeSignalAttributes(attributes, sandboxOutcome.attributes)
	extensions["sandbox_outcome"] = sandboxOutcome.providerExtension
}

func attachCodexToolDecisionSignal(attributes, extensions map[string]any, fields map[string]any, id, sessionID string) {
	toolDecision, ok := codexToolDecision(fields, id, sessionID)
	if !ok {
		return
	}
	mergeSignalAttributes(attributes, toolDecision.attributes)
	extensions["tool_decision"] = toolDecision.providerExtension
}

func mergeSignalAttributes(target, source map[string]any) {
	for key, value := range source {
		target[key] = value
	}
}

func hasCodexOutcomeContract(eventName string) bool {
	return eventName == codexToolResultEvent || eventName == "codex.api_request"
}

func attachCodexOutcomeContract(extensions map[string]any, fields map[string]any, eventName string) {
	var source, status string
	switch eventName {
	case codexToolResultEvent:
		source = "tool_result"
		status = codexSuccessStatus(fields["success"])
	case "codex.api_request":
		source = "provider_completion"
		status = codexSuccessStatus(fields["success"])
	default:
		return
	}
	if status == "" {
		return
	}
	contract := map[string]any{
		"source":     source,
		"status":     status,
		"confidence": "observed",
	}
	if model, ok := normalize.ObservedString(fields["model"]); ok {
		contract["model"] = model
	}
	if duration := normalize.OptionalTokenCount(fields["duration_ms"]); duration != nil {
		contract["duration_ms"] = *duration
	}
	if input := normalize.OptionalTokenCount(fields["input_token_count"]); input != nil {
		contract["input_tokens"] = *input
	}
	if output := normalize.OptionalTokenCount(fields["output_token_count"]); output != nil {
		contract["output_tokens"] = *output
	}
	if code := codexErrorCode(fields, status); code != "" {
		contract["error_code"] = code
	}
	if attempt := normalize.OptionalTokenCount(fields["attempt"]); attempt != nil && *attempt > 0 {
		contract["retry_attempt"] = *attempt
	}
	extensions["outcome_contract"] = contract
}

func codexSuccessStatus(value any) string {
	switch typed := value.(type) {
	case bool:
		if typed {
			return "success"
		}
		return "failed"
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "ok", "success":
			return "success"
		case "false", "error", "failed", "failure":
			return "failed"
		}
	}
	return ""
}

func codexErrorCode(fields map[string]any, status string) string {
	if status != "failed" {
		return ""
	}
	for _, key := range []string{"error_code", "error.message", "error"} {
		if text, ok := normalize.ObservedString(fields[key]); ok {
			return text
		}
	}
	for _, key := range []string{"http.status_code", "http.response.status_code"} {
		if text, ok := normalize.ObservedString(fields[key]); ok && text != "" {
			return "http_" + text
		}
		if number := normalize.OptionalTokenCount(fields[key]); number != nil {
			return fmt.Sprintf("http_%d", *number)
		}
	}
	return ""
}

func codexLogAttributes(fields map[string]any) map[string]any {
	eventName := stringValue(fields[codexEventNameKey], "")
	switch eventName {
	case codexSandboxOutcomeEvent:
		return allowedCodexAttributes(fields, codexEventNameKey)
	case codexToolDecisionEvent:
		return allowedCodexAttributes(fields, codexEventNameKey, "model")
	}
	if codexLifecycleEvent(eventName) {
		return allowedCodexAttributes(fields, codexEventNameKey)
	}
	known := []string{"mcp_server", "conversation.id"}
	switch eventName {
	case codexToolResultEvent:
		known = append(known, codexToolResultFieldKeys()...)
	}
	return safeCodexLogAttributes(normalize.UnknownFields(fields, known...))
}

func codexLogResourceAttributes(_ string, resource map[string]any) map[string]any {
	return allowedCodexAttributes(resource, codexServiceNameKey, "service.version")
}

func codexLifecycleEvent(eventName string) bool {
	switch eventName {
	case codexConversationStarts, codexStartupPhaseEvent, codexWebsocketConnect:
		return true
	default:
		return false
	}
}

func codexLifecycleFieldKeys(eventName string) []string {
	keys := []string{"auth_mode", "originator", "terminal.type"}
	switch eventName {
	case codexConversationStarts:
		keys = append(keys, "approval_policy", "provider_name", "sandbox_policy")
	case codexStartupPhaseEvent:
		keys = append(keys, "startup.phase", "startup.status", "duration_ms")
	case codexWebsocketConnect:
		keys = append(keys, "duration_ms", "success")
	}
	return keys
}

func codexEntrypoint(value any) string {
	service, ok := normalize.ObservedString(value)
	if !ok {
		return ""
	}
	switch service {
	case "codex_cli_rs":
		return "interactive"
	case "codex_exec":
		return "codex exec"
	default:
		return service
	}
}

func codexLogUnavailableFields(eventName string) []string {
	fields := []string{"session_lifecycle", "cache_usage", "reasoning_tokens", "file_operations", "approvals", "prompt_content", "response_content", "repository_context", "provider_cost"}
	if codexLifecycleEvent(eventName) {
		fields = removeUnavailableField(fields, "session_lifecycle")
	}
	if eventName != codexSandboxOutcomeEvent {
		fields = append(fields, "command_execution")
	}
	if eventName != codexToolResultEvent {
		fields = append(fields, "tool_calls")
	}
	if eventName == codexToolDecisionEvent {
		fields = removeUnavailableField(fields, "approvals")
	}
	return fields
}

func removeUnavailableField(fields []string, target string) []string {
	filtered := fields[:0]
	for _, field := range fields {
		if field != target {
			filtered = append(filtered, field)
		}
	}
	return filtered
}

type codexToolCallSignal struct {
	attributes        map[string]any
	providerExtension map[string]any
}

func codexToolCall(fields map[string]any, fallbackID, sessionID string) (codexToolCallSignal, bool) {
	if stringValue(fields[codexEventNameKey], "") != codexToolResultEvent {
		return codexToolCallSignal{}, false
	}
	operationID := fallbackID
	if callID, ok := normalize.ObservedString(fields["call_id"]); ok {
		operationID = sessionID + ":tool:" + callID
	}
	category := codexOperationCategory(fields)
	outcome := codexSuccessStatus(fields["success"])
	if outcome == "" {
		outcome = "unknown"
	}
	attributes := map[string]any{
		"operation_id": operationID,
		"category":     string(category),
		"outcome":      outcome,
	}
	if duration := normalize.OptionalTokenCount(fields["duration_ms"]); duration != nil {
		attributes["duration_ms"] = *duration
	}
	extension := map[string]any{
		"operation_id": operationID,
		"category":     string(category),
		"outcome":      outcome,
		"provenance":   string(canonical.ProvenanceObserved),
	}
	for _, key := range []string{"tool_name", "tool_namespace", "call_id", "duration_ms", "success", "output_truncated", "tool_result_seq", "decision"} {
		if value, ok := fields[key]; ok {
			extension[key] = value
		}
	}
	return codexToolCallSignal{attributes: attributes, providerExtension: extension}, true
}

func codexOperationCategory(fields map[string]any) canonical.OperationCategory {
	if _, ok := normalize.ObservedString(fields["mcp_server"]); ok {
		return canonical.OperationCategoryMCPCall
	}
	name, _ := normalize.ObservedString(fields["tool_name"])
	switch name {
	case "exec_command":
		return canonical.OperationCategoryShellCommand
	case "apply_patch":
		return canonical.OperationCategoryFilesystemWrite
	default:
		return canonical.OperationCategoryUnknown
	}
}

func codexToolResultFieldKeys() []string {
	return []string{"tool_name", "tool_namespace", "call_id", "duration_ms", "success", "output_truncated", "tool_result_seq", "decision"}
}

func codexSandboxOutcome(fields map[string]any, fallbackID, sessionID string) (codexToolCallSignal, bool) {
	if stringValue(fields[codexEventNameKey], "") != codexSandboxOutcomeEvent {
		return codexToolCallSignal{}, false
	}
	operationID := fallbackID
	if callID, ok := normalize.ObservedString(fields["call_id"]); ok {
		operationID = sessionID + ":sandbox:" + callID
	}
	outcome := codexSandboxOutcomeStatus(fields)
	attributes := map[string]any{
		"operation_id": operationID,
		"category":     string(canonical.OperationCategoryShellCommand),
		"outcome":      outcome,
	}
	if duration := codexSandboxDurationMs(fields); duration != nil {
		attributes["duration_ms"] = *duration
	}
	extension := map[string]any{
		"operation_id": operationID,
		"category":     string(canonical.OperationCategoryShellCommand),
		"outcome":      outcome,
		"provenance":   string(canonical.ProvenanceObserved),
	}
	for _, key := range codexSandboxOutcomeFieldKeys() {
		if value, ok := fields[key]; ok {
			extension[key] = value
		}
	}
	return codexToolCallSignal{attributes: attributes, providerExtension: extension}, true
}

func codexSandboxOutcomeStatus(fields map[string]any) string {
	if outcome, ok := normalize.ObservedString(fields["outcome"]); ok {
		switch strings.ToLower(strings.TrimSpace(outcome)) {
		case "success", "succeeded", "ok", "pass", "passed":
			return "success"
		case "failed", "failure", "error", "denied", "blocked":
			return "failed"
		default:
			return outcome
		}
	}
	if status := codexSuccessStatus(fields["success"]); status != "" {
		return status
	}
	return "unknown"
}

func codexSandboxDurationMs(fields map[string]any) *int64 {
	if duration := normalize.OptionalTokenCount(fields["duration_ms"]); duration != nil {
		return duration
	}
	return normalize.OptionalTokenCount(fields["initial_duration_ms"])
}

func codexSandboxOutcomeFieldKeys() []string {
	return []string{"call_id", "initial_duration_ms", "duration_ms", "outcome", "success", "tool_name", "model", "terminal.type"}
}

func codexToolDecision(fields map[string]any, fallbackID, sessionID string) (codexToolCallSignal, bool) {
	if stringValue(fields[codexEventNameKey], "") != codexToolDecisionEvent {
		return codexToolCallSignal{}, false
	}
	approvalID := fallbackID
	if callID, ok := normalize.ObservedString(fields["call_id"]); ok {
		approvalID = sessionID + ":approval:" + callID
	}
	decision := codexApprovalDecisionStatus(fields["decision"])
	attributes := map[string]any{
		"approval_id":       approvalID,
		"approval_decision": decision,
	}
	if reasonClass, ok := normalize.ObservedString(fields["source"]); ok {
		attributes["approval_reason_class"] = reasonClass
	}
	for _, key := range []string{"tool_name", "tool_namespace"} {
		if value, ok := fields[key]; ok {
			attributes[key] = value
		}
	}
	extension := map[string]any{
		"approval_id": approvalID,
		"decision":    decision,
		"provenance":  string(canonical.ProvenanceObserved),
	}
	for _, key := range codexToolDecisionFieldKeys() {
		if value, ok := fields[key]; ok {
			if key == "decision" {
				extension["raw_decision"] = value
				continue
			}
			extension[key] = value
		}
	}
	return codexToolCallSignal{attributes: attributes, providerExtension: extension}, true
}

func codexApprovalDecisionStatus(value any) string {
	decision, ok := normalize.ObservedString(value)
	if !ok {
		return "unknown"
	}
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "approved", "approve", "allow", "allowed", "granted", "accepted", "yes":
		return "approved"
	case "denied", "deny", "blocked", "rejected", "no":
		return "denied"
	default:
		return decision
	}
}

func codexToolDecisionFieldKeys() []string {
	return []string{"call_id", "decision", "source", "tool_name", "tool_namespace"}
}

func allowedCodexAttributes(fields map[string]any, keys ...string) map[string]any {
	allowed := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, ok := fields[key]; ok {
			allowed[key] = value
		}
	}
	return allowed
}

func safeCodexLogAttributes(fields map[string]any) map[string]any {
	safe := make(map[string]any, len(fields))
	for key, value := range fields {
		if sensitiveCodexLogAttribute(key) {
			continue
		}
		safe[key] = value
	}
	return safe
}

func sensitiveCodexLogAttribute(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "api_key", "authorization", "custom_metadata", "cwd", "file", "file_path", "filename", "files", "host.name", "hostname", "input", "path", "prompt", "response", "slug", "source_code", "user.account_id", "user.email":
		return true
	default:
		return false
	}
}

var (
	prLinkURLPattern  = regexp.MustCompile(`https?://[^\s"'<>\\\]\[(){}]+`)
	prLinkPathPattern = regexp.MustCompile(`(?i)/(?:pull|pulls|pull-requests|pullrequest)/[1-9][0-9]*$|/-/merge_requests/[1-9][0-9]*$`)
)

// attachCodexPRLinkEvidence records only URLs present verbatim in reviewed
// provider fields. It recognises common pull/merge-request paths across
// GitHub, GitLab, Bitbucket, Azure DevOps, and compatible self-hosted hosts;
// it neither calls the host nor derives a URL from repository metadata.
func attachCodexPRLinkEvidence(attributes, extensions, fields map[string]any) {
	seen := map[string]struct{}{}
	evidence := make([]map[string]string, 0)
	for _, field := range []string{"pr_link", "pr_url", "pull_request_url", "arguments", "cmd", "command", "command_args", "command_line", "commandarguments", "output"} {
		text, ok := normalize.ObservedString(fields[field])
		if !ok {
			continue
		}
		for _, candidate := range prLinkURLs(text) {
			key := field + "\x00" + candidate
			if _, found := seen[key]; found {
				continue
			}
			seen[key] = struct{}{}
			evidence = append(evidence, map[string]string{"field": field, "url": candidate})
		}
	}
	if len(evidence) == 0 {
		return
	}
	sort.Slice(evidence, func(i, j int) bool {
		if evidence[i]["url"] == evidence[j]["url"] {
			return evidence[i]["field"] < evidence[j]["field"]
		}
		return evidence[i]["url"] < evidence[j]["url"]
	})
	candidates := make([]string, 0, len(evidence))
	unique := map[string]struct{}{}
	for _, item := range evidence {
		if _, found := unique[item["url"]]; found {
			continue
		}
		unique[item["url"]] = struct{}{}
		candidates = append(candidates, item["url"])
	}
	attributes["pr_link_candidates"] = candidates
	extensions["pr_link_evidence"] = evidence
}

func prLinkURLs(text string) []string {
	seen := map[string]struct{}{}
	var candidates []string
	for _, raw := range prLinkURLPattern.FindAllString(text, -1) {
		parsed, err := url.Parse(raw)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || !prLinkPathPattern.MatchString(strings.TrimSuffix(parsed.Path, "/")) {
			continue
		}
		if _, found := seen[raw]; found {
			continue
		}
		seen[raw] = struct{}{}
		candidates = append(candidates, raw)
	}
	return candidates
}

func codexMCPCall(fields map[string]any) (map[string]any, bool) {
	server, ok := normalize.ObservedString(fields["mcp_server"])
	if !ok {
		return nil, false
	}
	call := map[string]any{
		"server_name":    server,
		"identity_state": "provider_reported",
	}
	for _, key := range []string{"mcp_server_origin", "tool_name", "tool_namespace", "call_id", "duration_ms", "success", "output_truncated", "tool_result_seq", "decision"} {
		if value, ok := fields[key]; ok {
			call[key] = value
		}
	}
	return call, true
}

type attribute struct {
	Key   string         `json:"key"`
	Value map[string]any `json:"value"`
}

func attributes(values []attribute) map[string]any {
	result := make(map[string]any, len(values))
	for _, attribute := range values {
		for _, valueKey := range []string{"stringValue", "intValue", "boolValue", "doubleValue"} {
			if value, found := attribute.Value[valueKey]; found {
				result[attribute.Key] = value
				break
			}
		}
		if _, found := result[attribute.Key]; !found && len(attribute.Value) > 0 {
			result[attribute.Key] = attribute.Value
		}
	}
	return result
}

func stringValue(value any, fallback string) string {
	result, ok := value.(string)
	if !ok || result == "" {
		return fallback
	}
	return result
}

// isCodexLogService reports whether a resource service.name is one of the
// observed Codex OTLP log exporters: codex_cli_rs (interactive TUI) or
// codex_exec (the non-interactive `codex exec` subcommand). Both were observed
// exporting the same reviewed log shape at Codex CLI 0.145.0.
func isCodexLogService(value any) bool {
	name, ok := value.(string)
	if !ok {
		return false
	}
	return name == "codex_cli_rs" || name == "codex_exec"
}
