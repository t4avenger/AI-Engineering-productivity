package codex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

const rolloutOperationBoundaryReason = "Codex rollout operation records have no reviewed task-boundary signal"

type rolloutOperationEvidence struct {
	record     rolloutRecord
	item       map[string]any
	id         string
	kind       string
	category   canonical.OperationCategory
	outcome    string
	durationMs *int64
}

func rolloutOperations(records []rolloutRecord, metadata rolloutMetadata, receivedAt time.Time) ([]canonical.Operation, []canonical.Event) {
	rich := collectCompletedRolloutItems(records)
	if len(rich) > 0 {
		return shapeRolloutOperations(rich, metadata, receivedAt)
	}
	return shapeRolloutOperations(collectLegacyRolloutCalls(records), metadata, receivedAt)
}

func collectCompletedRolloutItems(records []rolloutRecord) []rolloutOperationEvidence {
	var evidence []rolloutOperationEvidence
	for _, record := range records {
		payload := rolloutMap(record.decoded["payload"])
		if record.recordType != "event_msg" || rolloutString(payload["type"]) != "item_completed" {
			continue
		}
		item := rolloutMap(payload["item"])
		kind := rolloutString(item["type"])
		category, supported := completedItemCategory(kind, item)
		id := rolloutString(item["id"])
		if !supported || id == "" {
			continue
		}
		evidence = append(evidence, rolloutOperationEvidence{
			record: record, item: item, id: id, kind: kind, category: category,
			outcome: rolloutOutcome(item["status"]), durationMs: rolloutDurationMs(item["duration"]),
		})
	}
	return evidence
}

func completedItemCategory(kind string, item map[string]any) (canonical.OperationCategory, bool) {
	switch kind {
	case "CommandExecution":
		return canonical.OperationCategoryShellCommand, true
	case "FileChange":
		return rolloutFileChangeOperationCategory(item), true
	case "McpToolCall":
		return canonical.OperationCategoryMCPCall, true
	default:
		return "", false
	}
}

func rolloutFileChangeOperationCategory(item map[string]any) canonical.OperationCategory {
	changes := rolloutMap(item["changes"])
	category := canonical.OperationCategoryUnknown
	for _, value := range changes {
		switch fileChangeCategory(rolloutString(rolloutMap(value)["type"])) {
		case canonical.OperationCategoryFilesystemWrite:
			return canonical.OperationCategoryFilesystemWrite
		case canonical.OperationCategoryFilesystemDelete:
			category = canonical.OperationCategoryFilesystemDelete
		}
	}
	return category
}

func collectLegacyRolloutCalls(records []rolloutRecord) []rolloutOperationEvidence {
	outputs := make(map[string]map[string]any)
	for _, record := range records {
		payload := rolloutMap(record.decoded["payload"])
		if record.recordType == "response_item" && rolloutString(payload["type"]) == "custom_tool_call_output" {
			if callID := rolloutString(payload["call_id"]); callID != "" {
				outputs[callID] = payload
			}
		}
	}
	var evidence []rolloutOperationEvidence
	for _, record := range records {
		payload := rolloutMap(record.decoded["payload"])
		if record.recordType != "response_item" || rolloutString(payload["type"]) != "custom_tool_call" {
			continue
		}
		callID := rolloutString(payload["call_id"])
		if callID == "" {
			continue
		}
		item := cloneRolloutMap(payload)
		if output, ok := outputs[callID]; ok {
			item["result"] = output["output"]
		}
		evidence = append(evidence, rolloutOperationEvidence{
			record: record, item: item, id: callID, kind: rolloutString(payload["name"]),
			category: legacyRolloutCategory(rolloutString(payload["name"])), outcome: "unknown",
		})
	}
	return evidence
}

func legacyRolloutCategory(name string) canonical.OperationCategory {
	switch name {
	case "exec", "exec_command":
		return canonical.OperationCategoryShellCommand
	case "apply_patch":
		return canonical.OperationCategoryFilesystemWrite
	default:
		return canonical.OperationCategoryUnknown
	}
}

func shapeRolloutOperations(evidence []rolloutOperationEvidence, metadata rolloutMetadata, receivedAt time.Time) ([]canonical.Operation, []canonical.Event) {
	operations := make([]canonical.Operation, 0, len(evidence))
	var events []canonical.Event
	for _, item := range evidence {
		operationID := normalize.ProviderNativeSessionID(codexSessionPrefix, metadata.sessionID) + ":tool:" + item.id
		extensions := rolloutOperationExtensions(item, operationID, metadata.sessionID)
		operations = append(operations, canonical.Operation{
			SchemaVersion: canonical.RecordSchemaVersion, OperationID: operationID,
			SessionID: normalize.ProviderNativeSessionID(codexSessionPrefix, metadata.sessionID),
			Provider:  "openai", Tool: "codex", Category: item.category, Outcome: item.outcome,
			Provenance: canonical.ProvenanceObserved, ProviderExtensions: extensions,
		})
		events = append(events, rolloutOperationEvents(item, metadata, receivedAt, operationID)...)
	}
	return operations, events
}

func rolloutOperationExtensions(item rolloutOperationEvidence, operationID, sessionID string) map[string]any {
	call := cloneRolloutMap(item.item)
	call["operation_id"] = operationID
	call["provenance"] = string(canonical.ProvenanceObserved)
	if item.durationMs != nil {
		call["duration_ms"] = *item.durationMs
	}
	if item.category == canonical.OperationCategoryMCPCall {
		if server := rolloutString(item.item["server"]); server != "" {
			call["server_name"] = server
		}
		if tool := rolloutString(item.item["tool"]); tool != "" {
			call["tool_name"] = tool
		}
	}
	extensions := rolloutCorrelationExtensions(item, operationID, sessionID)
	if item.category == canonical.OperationCategoryMCPCall {
		extensions["mcp_call"] = call
	} else {
		extensions["tool_call"] = call
	}
	return extensions
}

// rolloutEventExtensions is the content-free sibling of rolloutOperationExtensions.
// Correlation events carry identity only; raw command output, MCP bodies, and file
// diffs stay on the retained rollout record and the Operation.
func rolloutEventExtensions(item rolloutOperationEvidence, operationID, sessionID string) map[string]any {
	extensions := rolloutCorrelationExtensions(item, operationID, sessionID)
	identity := map[string]any{
		"id": item.id, "type": item.kind, "operation_id": operationID,
		"provenance": string(canonical.ProvenanceObserved),
	}
	if item.category == canonical.OperationCategoryMCPCall {
		if server := rolloutString(item.item["server"]); server != "" {
			identity["server_name"] = server
		}
		if tool := rolloutString(item.item["tool"]); tool != "" {
			identity["tool_name"] = tool
		}
		extensions["mcp_call"] = identity
		return extensions
	}
	extensions["tool_call"] = identity
	return extensions
}

func rolloutCorrelationExtensions(item rolloutOperationEvidence, operationID, sessionID string) map[string]any {
	return map[string]any{
		"correlation": map[string]any{
			"dedup_key":       operationID,
			"ordering_key":    fmt.Sprintf("%020d:%s", item.record.timestamp.UTC().UnixNano(), operationID),
			"task_boundary":   map[string]any{"confidence": "unknown", "reason": rolloutOperationBoundaryReason},
			"source_event_id": rolloutEventID(item.record, sessionID),
		},
		"event": map[string]any{"tool_name": rolloutToolName(item), "tool_use_id": item.id},
	}
}

func rolloutToolName(item rolloutOperationEvidence) string {
	switch item.kind {
	case "CommandExecution":
		return "exec_command"
	case "FileChange":
		return "apply_patch"
	case "McpToolCall":
		return rolloutString(item.item["tool"])
	default:
		return item.kind
	}
}

func rolloutOperationEvents(item rolloutOperationEvidence, metadata rolloutMetadata, receivedAt time.Time, operationID string) []canonical.Event {
	if item.kind == "FileChange" {
		return rolloutFileEvents(item, metadata, receivedAt, operationID)
	}
	eventType := "tool_call"
	attributes := map[string]any{"category": string(item.category), "operation_id": operationID, "unavailable_fields": rolloutOperationUnavailableFields(item.category)}
	extensions := rolloutEventExtensions(item, operationID, metadata.sessionID)
	if item.category == canonical.OperationCategoryMCPCall {
		eventType = "mcp_call"
	} else {
		attributes["tool"] = rolloutToolAttributes(item)
	}
	if item.durationMs != nil {
		attributes["duration_ms"] = *item.durationMs
	}
	return []canonical.Event{rolloutDerivedEvent(item, metadata, receivedAt, eventType, attributes, extensions, "")}
}

func rolloutFileEvents(item rolloutOperationEvidence, metadata rolloutMetadata, receivedAt time.Time, operationID string) []canonical.Event {
	changes := rolloutMap(item.item["changes"])
	paths := make([]string, 0, len(changes))
	for path := range changes {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return []canonical.Event{rolloutDerivedEvent(item, metadata, receivedAt, "tool_call", map[string]any{
			"category": string(item.category), "operation_id": operationID, "unavailable_fields": rolloutOperationUnavailableFields(item.category),
			"tool": rolloutToolAttributes(item),
		}, rolloutEventExtensions(item, operationID, metadata.sessionID), "")}
	}
	events := make([]canonical.Event, 0, len(paths))
	for _, path := range paths {
		change := rolloutMap(changes[path])
		category := fileChangeCategory(rolloutString(change["type"]))
		attributes := map[string]any{
			"category": string(category), "operation_id": operationID, "unavailable_fields": rolloutOperationUnavailableFields(category),
			"tool": map[string]any{"tool_name": "apply_patch", "tool_use_id": item.id, "file_path": path},
		}
		events = append(events, rolloutDerivedEvent(item, metadata, receivedAt, "tool_call", attributes, rolloutEventExtensions(item, operationID, metadata.sessionID), path))
	}
	return events
}

func rolloutOperationUnavailableFields(category canonical.OperationCategory) []string {
	fields := []string{
		"model", "token_usage", "cache_usage", "task_outcome", "reasoning_tokens",
		"repository_context", "prompt_content", "response_content", "provider_cost",
		"trace_span_correlation", "approvals", "command_execution", "file_operations",
	}
	switch category {
	case canonical.OperationCategoryShellCommand:
		return removeUnavailableField(fields, "command_execution")
	case canonical.OperationCategoryFilesystemWrite, canonical.OperationCategoryFilesystemDelete:
		return removeUnavailableField(fields, "file_operations")
	default:
		return fields
	}
}

func fileChangeCategory(changeType string) canonical.OperationCategory {
	switch changeType {
	case "delete":
		return canonical.OperationCategoryFilesystemDelete
	case "add", "create", "update":
		return canonical.OperationCategoryFilesystemWrite
	default:
		return canonical.OperationCategoryUnknown
	}
}

func rolloutToolAttributes(item rolloutOperationEvidence) map[string]any {
	tool := map[string]any{"tool_name": rolloutToolName(item), "tool_use_id": item.id}
	if cwd := rolloutString(item.item["cwd"]); cwd != "" {
		tool["cwd"] = cwd
	}
	if command, ok := item.item["command"].([]any); ok {
		parts := make([]string, 0, len(command))
		for _, value := range command {
			if text := rolloutString(value); text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) > 0 {
			tool["full_command"] = strings.Join(parts, " ")
		}
	}
	return tool
}

func rolloutDerivedEvent(item rolloutOperationEvidence, metadata rolloutMetadata, receivedAt time.Time, eventType string, attributes, extensions map[string]any, suffix string) canonical.Event {
	eventID := normalize.ProviderNativeSessionID(codexSessionPrefix, metadata.sessionID) + ":toolcall:" + item.id
	if suffix != "" {
		digest := sha256.Sum256([]byte(suffix))
		eventID += ":file:" + hex.EncodeToString(digest[:8])
	}
	return canonical.Event{
		SchemaVersion: canonicalSchemaVersion, EventID: eventID, EventType: eventType,
		OccurredAt: item.record.timestamp.UTC(), ReceivedAt: receivedAt.UTC(), Provider: "openai", Tool: "codex",
		SourceSchema: sourceSchemaRollout, SourceVersion: metadata.sourceVersion, ActorID: metadata.actorID,
		DeviceID: unavailable, SessionID: normalize.ProviderNativeSessionID(codexSessionPrefix, metadata.sessionID),
		PrivacyLevel: "governed-content", Attributes: attributes, ProviderExtensions: extensions,
	}
}

func rolloutOutcome(value any) string {
	switch strings.ToLower(strings.TrimSpace(rolloutString(value))) {
	case "completed", "success", "succeeded":
		return "success"
	case "failed", "error":
		return "failed"
	case "cancelled", "canceled":
		return "cancelled"
	default:
		return "unknown"
	}
}

func rolloutDurationMs(value any) *int64 {
	duration := rolloutMap(value)
	seconds := rolloutDurationPart(duration["secs"])
	nanos := rolloutDurationPart(duration["nanos"])
	if seconds == nil && nanos == nil {
		return nil
	}
	var result int64
	if seconds != nil {
		if *seconds > math.MaxInt64/1000 {
			return nil
		}
		result += *seconds * 1000
	}
	if nanos != nil {
		nanosMs := *nanos / 1_000_000
		if nanosMs > math.MaxInt64-result {
			return nil
		}
		result += nanosMs
	}
	return &result
}

func rolloutDurationPart(value any) *int64 {
	if number, ok := value.(json.Number); ok {
		parsed, err := number.Int64()
		if err != nil || parsed < 0 {
			return nil
		}
		return &parsed
	}
	return normalize.OptionalTokenCount(value)
}

func rolloutMap(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func rolloutString(value any) string {
	result, _ := normalize.ObservedString(value)
	return result
}

func cloneRolloutMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
