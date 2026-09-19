package insights

import (
	"sort"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

const (
	FileActionRead    = "read"
	FileActionWrite   = "write"
	FileActionDelete  = "delete"
	FileActionUnknown = "unknown"

	fileAvailabilityObserved    = "observed"
	fileAvailabilityInferred    = "inferred"
	fileAvailabilityUnavailable = "unavailable"
)

// SessionFileEntry is one fixture-honest Files-lane projection row (#156 / T06).
// Additions/deletions stay null unless a future per-file fixture proves them;
// aggregate lines_of_code counters never fill those fields.
type SessionFileEntry struct {
	EventID      string                       `json:"event_id"`
	OperationID  *string                      `json:"operation_id"`
	Path         *string                      `json:"path"`
	Action       *string                      `json:"action"`
	OccurredAt   string                       `json:"occurred_at"`
	DurationMs   *int64                       `json:"duration_ms"`
	Additions    *int64                       `json:"additions"`
	Deletions    *int64                       `json:"deletions"`
	Availability SessionFileFieldAvailability `json:"availability"`
	Source       SessionFileSource            `json:"source"`
}

// SessionFileFieldAvailability labels each projected field honestly.
type SessionFileFieldAvailability struct {
	Path          string `json:"path"`
	Action        string `json:"action"`
	OperationLink string `json:"operation_link"`
	Duration      string `json:"duration"`
	LineDiff      string `json:"line_diff"`
}

// SessionFileSource records provenance for a projected file evidence row.
type SessionFileSource struct {
	Provider   string `json:"provider"`
	Tool       string `json:"tool"`
	EventType  string `json:"event_type"`
	Provenance string `json:"provenance"`
}

// SessionFilesFromEvidence projects retained events and operations into Files-lane
// rows. A row is emitted only when a retained path is present on a tool block, a
// filesystem operation category is proven, or both. Tool names alone never invent
// a path; aggregate LOC metrics never become per-file additions/deletions.
func SessionFilesFromEvidence(events []canonical.Event, operations []canonical.Operation) []SessionFileEntry {
	opsByToolUse := indexFilesystemOpsByToolUseID(operations)
	opsByID := indexFilesystemOpsByID(operations)

	entries := make([]SessionFileEntry, 0)
	for _, event := range events {
		path, pathObserved := toolFilePath(event)
		op := matchFilesystemOperation(event, opsByToolUse, opsByID)
		category := filesystemCategoryFromEvent(event, op)
		if path == "" && category == "" {
			continue
		}

		action, actionAvailability := fileAction(category, event, op)
		operationID, operationAvailability := fileOperationID(event, op)
		duration, durationAvailability := fileDuration(event, op)

		entry := SessionFileEntry{
			EventID:    event.EventID,
			OccurredAt: event.OccurredAt.UTC().Format(time.RFC3339Nano),
			Action:     stringPtr(action),
			Availability: SessionFileFieldAvailability{
				Path:          fileAvailabilityUnavailable,
				Action:        actionAvailability,
				OperationLink: operationAvailability,
				Duration:      durationAvailability,
				LineDiff:      fileAvailabilityUnavailable,
			},
			Source: SessionFileSource{
				Provider:   event.Provider,
				Tool:       event.Tool,
				EventType:  event.EventType,
				Provenance: operationProvenance(op),
			},
		}
		if pathObserved {
			entry.Path = stringPtr(path)
			entry.Availability.Path = fileAvailabilityObserved
		}
		if operationID != "" {
			entry.OperationID = stringPtr(operationID)
		}
		if duration != nil {
			entry.DurationMs = duration
		}
		entries = append(entries, entry)
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].OccurredAt != entries[j].OccurredAt {
			return entries[i].OccurredAt < entries[j].OccurredAt
		}
		return entries[i].EventID < entries[j].EventID
	})
	return entries
}

func indexFilesystemOpsByToolUseID(operations []canonical.Operation) map[string]canonical.Operation {
	out := map[string]canonical.Operation{}
	for _, operation := range operations {
		if !isFilesystemCategory(operation.Category) {
			continue
		}
		if id := operationToolUseID(operation); id != "" {
			out[id] = operation
		}
	}
	return out
}

func indexFilesystemOpsByID(operations []canonical.Operation) map[string]canonical.Operation {
	out := map[string]canonical.Operation{}
	for _, operation := range operations {
		if !isFilesystemCategory(operation.Category) {
			continue
		}
		out[operation.OperationID] = operation
	}
	return out
}

func matchFilesystemOperation(event canonical.Event, byToolUse, byID map[string]canonical.Operation) *canonical.Operation {
	if id := eventToolUseID(event); id != "" {
		if operation, ok := byToolUse[id]; ok {
			return &operation
		}
	}
	if id, ok := attributeString(event.Attributes, "operation_id"); ok {
		if operation, ok := byID[id]; ok {
			return &operation
		}
	}
	return nil
}

func toolFilePath(event canonical.Event) (string, bool) {
	if tool := nestedMap(event.Attributes, "tool"); tool != nil {
		if path, ok := attributeString(tool, "file_path"); ok {
			return path, true
		}
	}
	return "", false
}

func filesystemCategoryFromEvent(event canonical.Event, op *canonical.Operation) canonical.OperationCategory {
	if op != nil && isFilesystemCategory(op.Category) {
		return op.Category
	}
	if category, ok := attributeString(event.Attributes, "category"); ok {
		parsed := canonical.OperationCategory(category)
		if isFilesystemCategory(parsed) {
			return parsed
		}
	}
	return ""
}

func fileAction(category canonical.OperationCategory, event canonical.Event, op *canonical.Operation) (string, string) {
	if action, ok := actionFromCategory(category); ok {
		if op != nil || attributeHas(event.Attributes, "category") {
			return action, fileAvailabilityObserved
		}
		return action, fileAvailabilityInferred
	}
	if action, ok := actionFromToolName(eventToolName(event)); ok {
		return action, fileAvailabilityInferred
	}
	return FileActionUnknown, fileAvailabilityUnavailable
}

func fileOperationID(event canonical.Event, op *canonical.Operation) (string, string) {
	if op != nil && strings.TrimSpace(op.OperationID) != "" {
		return op.OperationID, fileAvailabilityObserved
	}
	if id, ok := attributeString(event.Attributes, "operation_id"); ok {
		return id, fileAvailabilityObserved
	}
	return "", fileAvailabilityUnavailable
}

func fileDuration(event canonical.Event, op *canonical.Operation) (*int64, string) {
	if duration, ok := attributeDuration(event.Attributes, "duration_ms"); ok {
		return &duration, fileAvailabilityObserved
	}
	if tool := nestedMap(event.Attributes, "tool"); tool != nil {
		if duration, ok := attributeDuration(tool, "duration_ms"); ok {
			return &duration, fileAvailabilityObserved
		}
	}
	if op != nil {
		if duration, ok := operationDurationMs(*op); ok {
			return &duration, fileAvailabilityObserved
		}
	}
	return nil, fileAvailabilityUnavailable
}

func actionFromCategory(category canonical.OperationCategory) (string, bool) {
	switch category {
	case canonical.OperationCategoryFilesystemRead:
		return FileActionRead, true
	case canonical.OperationCategoryFilesystemWrite:
		return FileActionWrite, true
	case canonical.OperationCategoryFilesystemDelete:
		return FileActionDelete, true
	default:
		return "", false
	}
}

func actionFromToolName(toolName string) (string, bool) {
	switch strings.TrimSpace(toolName) {
	case "Read", "Glob", "Grep", "NotebookRead":
		return FileActionRead, true
	case "Write", "Edit", "MultiEdit", "NotebookEdit", "apply_patch":
		return FileActionWrite, true
	default:
		return "", false
	}
}

func isFilesystemCategory(category canonical.OperationCategory) bool {
	switch category {
	case canonical.OperationCategoryFilesystemRead, canonical.OperationCategoryFilesystemWrite, canonical.OperationCategoryFilesystemDelete:
		return true
	default:
		return false
	}
}

func eventToolName(event canonical.Event) string {
	if tool := nestedMap(event.Attributes, "tool"); tool != nil {
		if name, ok := attributeString(tool, "tool_name"); ok {
			return name
		}
	}
	if name, ok := attributeString(event.Attributes, "tool_name"); ok {
		return name
	}
	if echo := nestedMap(event.ProviderExtensions, "event"); echo != nil {
		if name, ok := attributeString(echo, "tool_name"); ok {
			return name
		}
	}
	return ""
}

func eventToolUseID(event canonical.Event) string {
	if tool := nestedMap(event.Attributes, "tool"); tool != nil {
		if id, ok := attributeString(tool, "tool_use_id"); ok {
			return id
		}
	}
	if id, ok := attributeString(event.Attributes, "tool_use_id"); ok {
		return id
	}
	if echo := nestedMap(event.ProviderExtensions, "event"); echo != nil {
		if id, ok := attributeString(echo, "tool_use_id"); ok {
			return id
		}
	}
	return ""
}

func operationToolUseID(operation canonical.Operation) string {
	if echo := nestedMap(operation.ProviderExtensions, "event"); echo != nil {
		if id, ok := attributeString(echo, "tool_use_id"); ok {
			return id
		}
	}
	if call := nestedMap(operation.ProviderExtensions, "tool_call"); call != nil {
		if id, ok := attributeString(call, "call_id"); ok {
			return id
		}
	}
	return ""
}

func operationProvenance(op *canonical.Operation) string {
	if op == nil {
		return string(canonical.ProvenanceObserved)
	}
	if op.Provenance == "" {
		return string(canonical.ProvenanceUnknown)
	}
	return string(op.Provenance)
}

func nestedMap(values map[string]any, key string) map[string]any {
	if values == nil {
		return nil
	}
	nested, _ := values[key].(map[string]any)
	return nested
}

func attributeString(values map[string]any, key string) (string, bool) {
	if values == nil {
		return "", false
	}
	raw, ok := values[key]
	if !ok || raw == nil {
		return "", false
	}
	switch value := raw.(type) {
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return "", false
		}
		return trimmed, true
	default:
		return "", false
	}
}

func attributeHas(values map[string]any, key string) bool {
	_, ok := attributeString(values, key)
	return ok
}

func attributeDuration(values map[string]any, key string) (int64, bool) {
	if values == nil {
		return 0, false
	}
	return durationValue(values[key])
}

func stringPtr(value string) *string {
	return &value
}

// PageSessionFiles returns a cursor page over pre-projected file evidence.
// Cursor is opaque base64 of occurred_at + event_id (same convention as events).
func PageSessionFiles(entries []SessionFileEntry, limit int, cursorOccurredAt, cursorEventID string) ([]SessionFileEntry, *SessionFileEntry) {
	start := 0
	if cursorOccurredAt != "" || cursorEventID != "" {
		for index, entry := range entries {
			if entry.OccurredAt == cursorOccurredAt && entry.EventID == cursorEventID {
				start = index + 1
				break
			}
		}
	}
	if start >= len(entries) {
		return nil, nil
	}
	end := start + limit
	if end >= len(entries) {
		return entries[start:], nil
	}
	last := entries[end-1]
	return entries[start:end], &last
}
