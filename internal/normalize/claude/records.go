package claude

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// promotedRecordFields are the sample-event keys mapped onto a typed
// ModelInteraction. They are excluded from provider_extensions.event so
// evidence is not duplicated between the typed record and its extensions.
// cache_creation_tokens is deliberately absent: the canonical record has no
// cache-creation field, so it is preserved verbatim under provider_extensions
// rather than conflated with cache_read_tokens.
var promotedRecordFields = []string{"event_name", "event_timestamp", "event_sequence", "session_id", "request_id", "model", "input_tokens", "output_tokens", "cache_read_tokens", "duration_ms"}

// ExtractModelInteractions maps api_request sample events into stable-primitive
// canonical.ModelInteraction records. Only signals the P2 Claude Code
// capability matrix marks supported/partial are extracted: model identity,
// input/output tokens, and cache-read tokens. Reasoning tokens, tool calls, and
// task outcome are left nil/"unknown", never fabricated. Executed tool calls
// are carried by canonical.Operation via ExtractOperations, not this record;
// mcp_server_connection events are connection lifecycle, not tool-call
// invocations, and yield no Operation.
//
// completed_at is the observed api_request event timestamp; started_at is
// derived as completed_at minus the observed duration_ms and labelled as such
// under provider_extensions, so it is never mistaken for a directly observed
// endpoint.
func ExtractModelInteractions(data []byte) ([]canonical.ModelInteraction, error) {
	document, _, err := decodeDocument(data)
	if err != nil {
		return nil, err
	}
	switch document.Payload.SourceType {
	case sourceTypeOTLPEvents:
	case sourceTypeCapabilityProbe:
		return []canonical.ModelInteraction{}, nil
	default:
		return nil, fmt.Errorf("unsupported Claude payload source_type %q", document.Payload.SourceType)
	}
	var records []canonical.ModelInteraction
	for _, raw := range document.Payload.SampleEvents {
		interaction, ok, err := sampleModelInteraction(raw)
		if err != nil {
			return nil, err
		}
		if ok {
			records = append(records, interaction)
		}
	}
	return normalize.CorrelateModelInteractions(records), nil
}

func sampleModelInteraction(raw map[string]any) (canonical.ModelInteraction, bool, error) {
	if name, _ := raw["event_name"].(string); name != eventAPIRequest {
		return canonical.ModelInteraction{}, false, nil
	}
	model, modelObserved := normalize.ObservedString(raw["model"])
	inputTokens := normalize.OptionalTokenCount(raw["input_tokens"])
	outputTokens := normalize.OptionalTokenCount(raw["output_tokens"])
	if !modelObserved && inputTokens == nil && outputTokens == nil {
		return canonical.ModelInteraction{}, false, nil
	}
	sessionID, err := normalize.RequiredString(raw, "session_id")
	if err != nil {
		return canonical.ModelInteraction{}, false, err
	}
	completed, err := eventTime(raw, "event_timestamp")
	if err != nil {
		return canonical.ModelInteraction{}, false, err
	}

	nativeSessionID := normalize.ProviderNativeSessionID(nativeSessionPrefix, sessionID)
	requestID := nativeSessionID + ":" + sequenceSuffix(raw, completed)
	if rawRequest := normalize.OptionalString(raw, "request_id"); rawRequest != nil {
		requestID = nativeSessionPrefix + *rawRequest
	}

	duration := normalize.OptionalTokenCount(raw["duration_ms"])
	started, startedDerived := completed, false
	if duration != nil {
		started = completed.Add(-time.Duration(*duration) * time.Millisecond)
		startedDerived = true
	}

	result := "success"
	var errorCode *string
	extensions := recordExtensions(raw, requestID, started, startedDerived)
	attachOutcomeContract(extensions, raw, eventAPIRequest)
	interaction := canonical.ModelInteraction{
		SchemaVersion:      canonical.RecordSchemaVersion,
		RequestID:          requestID,
		SessionID:          nativeSessionID,
		Provider:           provider,
		Tool:               tool,
		Model:              model,
		StartedAt:          started,
		CompletedAt:        completed,
		DurationMs:         duration,
		InputTokens:        inputTokens,
		OutputTokens:       outputTokens,
		CachedInputTokens:  normalize.OptionalTokenCount(raw["cache_read_tokens"]),
		ReasoningTokens:    nil,
		Result:             result,
		ErrorCode:          errorCode,
		Provenance:         normalize.InteractionProvenance(modelObserved, inputTokens, outputTokens),
		ProviderExtensions: extensions,
	}
	return interaction, true, nil
}

func recordExtensions(raw map[string]any, requestID string, started time.Time, startedDerived bool) map[string]any {
	startedProvenance := "observed_equals_completed"
	if startedDerived {
		startedProvenance = "derived_from_duration"
	}
	return map[string]any{
		"correlation": map[string]any{
			"dedup_key":    requestID,
			"ordering_key": fmt.Sprintf("%020d:%s", started.UnixNano(), requestID),
			"task_boundary": map[string]any{
				"confidence": "unknown",
				"reason":     "Claude Code event telemetry has no reviewed task-boundary signal",
			},
		},
		"timestamps": map[string]any{
			"completed_at": "observed",
			"started_at":   startedProvenance,
		},
		"event": normalize.UnknownFields(raw, promotedRecordFields...),
	}
}

// sequenceSuffix renders the request-ID suffix used when no request_id is
// observed. It prefers the integral event_sequence, falling back to the
// observed completed timestamp so two api_request events in one session that
// both lack request_id and event_sequence stay distinct rather than colliding
// on a constant suffix and being silently deduplicated.
func sequenceSuffix(raw map[string]any, completed time.Time) string {
	if number, ok := raw["event_sequence"].(float64); ok && number == float64(int64(number)) {
		return strconv.FormatInt(int64(number), 10)
	}
	return "ts" + strconv.FormatInt(completed.UnixNano(), 10)
}

// operationStructuralFields are the tool_result keys represented by the typed
// Operation identity (OperationID, SessionID) or its correlation block, so they
// are excluded from the verbatim event echo under provider_extensions. Every
// remaining field — tool_name, tool_use_id, success, duration_ms, error_type,
// decision_type, decision_source, the size counters, mcp_server_scope, and any
// request_id — is preserved raw (epic #87), including the fields that also
// derive the typed Category/Outcome, since those are derived views, not
// replacements. request_id is deliberately not excluded here: the Operation has
// no request-ID field and the correlation block does not carry it, so dropping
// it would lose a safe correlation signal.
var operationStructuralFields = []string{"event_name", "event_timestamp", "event_sequence", "session_id"}

// ExtractOperations maps tool_result sample events into stable-primitive
// canonical.Operation records — the first real evidence of an executed tool
// call (capability matrix "Tool calls (generic)"). Every non-tool_result event
// is skipped rather than turned into a fabricated operation, mirroring the way
// an mcp_server_connection lifecycle event yields no Operation.
//
// Outcome is derived from the tool_result success field (a string "true"/"false"
// on the wire; a bool is tolerated); an absent success stays "unknown" rather
// than a fabricated value. duration_ms and the size counters are preserved
// verbatim under provider_extensions (Operation carries no typed duration
// field); an absent duration is omitted, never coerced to 0. Tool input
// parameters and result bodies (gated content) are out of scope — owned by the
// JSONL issues — and MCP-call correlation detail is owned by #104.
func ExtractOperations(data []byte) ([]canonical.Operation, error) {
	document, _, err := decodeDocument(data)
	if err != nil {
		return nil, err
	}
	switch document.Payload.SourceType {
	case sourceTypeOTLPEvents:
	case sourceTypeCapabilityProbe:
		return []canonical.Operation{}, nil
	default:
		return nil, fmt.Errorf("unsupported Claude payload source_type %q", document.Payload.SourceType)
	}
	var records []canonical.Operation
	for index, raw := range document.Payload.SampleEvents {
		operation, ok, err := sampleOperation(index, raw)
		if err != nil {
			return nil, err
		}
		if ok {
			records = append(records, operation)
		}
	}
	return normalize.CorrelateOperations(records), nil
}

func sampleOperation(index int, raw map[string]any) (canonical.Operation, bool, error) {
	if name, _ := raw["event_name"].(string); name != eventToolResult {
		return canonical.Operation{}, false, nil
	}
	sessionID, err := normalize.RequiredString(raw, "session_id")
	if err != nil {
		return canonical.Operation{}, false, err
	}
	occurredAt, err := eventTime(raw, "event_timestamp")
	if err != nil {
		return canonical.Operation{}, false, err
	}
	nativeSessionID := normalize.ProviderNativeSessionID(nativeSessionPrefix, sessionID)
	operationID := nativeSessionID + ":tool:" + operationSuffix(raw, index)

	return canonical.Operation{
		SchemaVersion:      canonical.RecordSchemaVersion,
		OperationID:        operationID,
		SessionID:          nativeSessionID,
		Provider:           provider,
		Tool:               tool,
		Category:           operationCategory(firstString(raw, "tool_name"), raw),
		Outcome:            toolResultOutcome(raw),
		Provenance:         canonical.ProvenanceObserved,
		ProviderExtensions: operationExtensions(raw, operationID, occurredAt),
	}, true, nil
}

func operationExtensions(raw map[string]any, operationID string, occurredAt time.Time) map[string]any {
	return map[string]any{
		"correlation": map[string]any{
			"dedup_key":    operationID,
			"ordering_key": fmt.Sprintf("%020d:%s", occurredAt.UnixNano(), operationID),
			"task_boundary": map[string]any{
				"confidence": "unknown",
				"reason":     "Claude Code tool_result telemetry has no reviewed task-boundary signal",
			},
		},
		"event": normalize.UnknownFields(raw, operationStructuralFields...),
	}
}

// toolResultOutcome maps the tool_result success signal to a stable outcome.
// success arrives as a string "true"/"false" on the wire (a bool is tolerated);
// an absent or unrecognised value stays "unknown" rather than a fabricated
// success/failed.
func toolResultOutcome(raw map[string]any) string {
	switch value := raw["success"].(type) {
	case bool:
		if value {
			return "success"
		}
		return "failed"
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "success", "ok":
			return "success"
		case "false", "error", "failure", "failed":
			return "failed"
		}
	}
	return "unknown"
}

// operationCategory classifies an executed Claude tool call into a
// provider-independent OperationCategory (§10.5) from its tool_name, never
// fabricating a category it cannot prove. MCP tools (tool_name prefixed mcp__,
// or carrying an mcp_server_scope) map to "MCP call"; the well-known built-in
// tools map to their filesystem/shell/network category; anything else stays
// "unknown" — a generic tool call still promoted as an Operation.
func operationCategory(toolName string, raw map[string]any) canonical.OperationCategory {
	if strings.HasPrefix(toolName, "mcp__") || firstString(raw, "mcp_server_scope") != "" {
		return canonical.OperationCategoryMCPCall
	}
	switch toolName {
	case "Bash":
		return canonical.OperationCategoryShellCommand
	case "Read", "Glob", "Grep", "NotebookRead":
		return canonical.OperationCategoryFilesystemRead
	case "Write", "Edit", "MultiEdit", "NotebookEdit":
		return canonical.OperationCategoryFilesystemWrite
	case "WebFetch", "WebSearch":
		return canonical.OperationCategoryNetworkRequest
	default:
		return canonical.OperationCategoryUnknown
	}
}

// operationSuffix renders the operation-ID suffix, preferring the provider
// tool_use_id (which also correlates OTel events with hook data), then the
// integral event_sequence, and finally an index-qualified content hash so two
// tool_result events in one session that lack both identifiers stay distinct
// rather than colliding on a shared suffix and being silently deduplicated by
// CorrelateOperations. The event index is the sample's deterministic position
// in the reviewed wrapper, so two byte-identical events never share a suffix.
func operationSuffix(raw map[string]any, index int) string {
	if id := firstString(raw, "tool_use_id"); id != "" {
		return id
	}
	if number, ok := raw["event_sequence"].(float64); ok && number == float64(int64(number)) {
		return strconv.FormatInt(int64(number), 10)
	}
	prefix := "idx" + strconv.Itoa(index) + ":"
	payload, err := json.Marshal(raw)
	if err != nil {
		return prefix[:len(prefix)-1]
	}
	return contentID(prefix, payload)
}
