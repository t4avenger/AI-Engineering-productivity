package codex

import (
	"strconv"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

const codexIntegrationStateEvent = "codex.integration_state"

func applyRolloutLifecycleAndGovernance(event *canonical.Event, record rolloutRecord) {
	payload, _ := record.decoded["payload"].(map[string]any)
	if payload == nil {
		return
	}
	if record.recordType == "turn_context" {
		attachRolloutGovernance(event, payload)
		return
	}
	if record.recordType != "event_msg" {
		return
	}

	kind, _ := normalize.ObservedString(payload["type"])
	switch kind {
	case "task_started":
		attachRolloutLifecycle(event, payload, "task_start", "active", "session.active")
		useRolloutBoundary(event, payload["started_at"])
	case "task_complete":
		status, eventType := "completed", "session.completed"
		if rolloutErrorObserved(payload["error"]) {
			status, eventType = "failed", "session.failed"
		}
		attachRolloutLifecycle(event, payload, "task_complete", status, eventType)
		useRolloutBoundary(event, payload["completed_at"])
	case "turn_aborted":
		status := "cancelled"
		if reason, ok := normalize.ObservedString(payload["reason"]); ok {
			status = rolloutAbortStatus(reason)
		}
		eventType := "session.cancelled"
		if status == "failed" {
			eventType = "session.failed"
		}
		attachRolloutLifecycle(event, payload, "turn_aborted", status, eventType)
		useRolloutBoundary(event, payload["completed_at"])
	}
}

func attachRolloutLifecycle(event *canonical.Event, payload map[string]any, kind, status, eventType string) {
	event.EventType = eventType
	event.Attributes["lifecycle_kind"] = kind
	event.Attributes["lifecycle_status"] = status
	lifecycle := map[string]any{
		"kind":       kind,
		"status":     status,
		"provenance": string(canonical.ProvenanceObserved),
		"source":     "rollout.event_msg",
	}
	for _, key := range []string{"turn_id", "trace_id", "root_turn_id", "reason", "started_at", "completed_at", "duration_ms", "error"} {
		if value, ok := payload[key]; ok {
			lifecycle[key] = value
		}
	}
	if duration := codexRolloutInt(payload["duration_ms"]); duration != nil {
		event.Attributes["duration_ms"] = *duration
	}
	event.ProviderExtensions["session_lifecycle"] = lifecycle
}

func useRolloutBoundary(event *canonical.Event, value any) {
	if raw, ok := normalize.ObservedString(value); ok {
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			event.OccurredAt = parsed.UTC()
			return
		}
	}
	if seconds := codexRolloutInt(value); seconds != nil && *seconds > 0 {
		event.OccurredAt = time.Unix(*seconds, 0).UTC()
	}
}

func rolloutErrorObserved(value any) bool {
	if value == nil {
		return false
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) != ""
	case map[string]any:
		return len(typed) > 0
	default:
		return true
	}
}

func rolloutAbortStatus(reason string) string {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "failed", "failure", "error":
		return "failed"
	default:
		return "cancelled"
	}
}

func attachRolloutGovernance(event *canonical.Event, payload map[string]any) {
	governance := map[string]any{
		"provenance": string(canonical.ProvenanceObserved),
		"source":     "rollout.turn_context",
	}
	if approval, ok := normalize.ObservedString(payload["approval_policy"]); ok {
		event.Attributes["approval_policy"] = approval
		governance["approval_policy"] = approval
	}
	if sandbox, ok := payload["sandbox_policy"].(map[string]any); ok {
		governance["sandbox_policy"] = sandbox
		if kind, ok := normalize.ObservedString(sandbox["type"]); ok {
			event.Attributes["sandbox_policy"] = kind
		}
	}
	for _, key := range []string{"file_system_sandbox_policy", "permission_profile", "disabled_plugin_ids"} {
		if value, ok := payload[key]; ok {
			governance[key] = value
		}
	}
	if len(governance) > 2 {
		event.ProviderExtensions["governance_state"] = governance
	}
}

func rolloutIntegrationEvents(records []rolloutRecord, metadata rolloutMetadata, receivedAt time.Time) []canonical.Event {
	var events []canonical.Event
	for _, record := range records {
		if record.recordType != "turn_context" {
			continue
		}
		payload, _ := record.decoded["payload"].(map[string]any)
		ids, _ := payload["disabled_plugin_ids"].([]any)
		for index, value := range ids {
			name, ok := normalize.ObservedString(value)
			if !ok {
				continue
			}
			events = append(events, rolloutIntegrationStateEvent(record, metadata, receivedAt, index, name))
		}
	}
	return events
}

func rolloutIntegrationStateEvent(record rolloutRecord, metadata rolloutMetadata, receivedAt time.Time, index int, name string) canonical.Event {
	id := contentID("codex:integration:", []byte(metadata.sessionID+"|"+record.timestamp.Format(time.RFC3339Nano)+"|"+name+"|"+strconv.Itoa(index)))
	return canonical.Event{
		SchemaVersion: canonicalSchemaVersion, EventID: id, EventType: codexIntegrationStateEvent,
		OccurredAt: record.timestamp.UTC(), ReceivedAt: receivedAt.UTC(), Provider: "openai", Tool: "codex",
		SourceSchema: sourceSchemaRollout, SourceVersion: metadata.sourceVersion, ActorID: metadata.actorID,
		DeviceID: unavailable, SessionID: normalize.ProviderNativeSessionID(codexSessionPrefix, metadata.sessionID), PrivacyLevel: "operational",
		Attributes: map[string]any{
			"integration_kind": "plugin", "integration_name": name, "integration_state": "disabled",
			"unavailable_fields": []string{},
		},
		ProviderExtensions: map[string]any{"integration_state": map[string]any{
			"kind": "plugin", "name": name, "state": "disabled", "provenance": string(canonical.ProvenanceObserved),
			"source": "rollout.turn_context.disabled_plugin_ids", "source_line": record.line,
		}},
	}
}
