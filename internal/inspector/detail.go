// Package inspector builds the session-scoped event inspector payload (#189 / T08).
// Relationships come only from proven file/span/operation identities — never
// from temporal proximity.
package inspector

import (
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wayne/telemetryiq/internal/insights"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/spans"
)

const (
	// DefaultMaxStringRunes truncates retained attribute/extension strings in
	// the default inspector payload. expand=1 returns full retained values.
	DefaultMaxStringRunes = 2048

	RelationSharedOperation = "shared_operation"
	RelationSharedSpan      = "shared_span"

	AvailabilityObserved = "observed"
	AvailabilityNone     = "none"
)

// RelatedEvent is another retained event linked by a proven shared identity.
type RelatedEvent struct {
	EventID    string `json:"event_id"`
	EventType  string `json:"event_type"`
	OccurredAt string `json:"occurred_at"`
	Relation   string `json:"relation"`
}

// RelationshipsAvailability labels each relationship collection honestly.
type RelationshipsAvailability struct {
	Files         string `json:"files"`
	Spans         string `json:"spans"`
	RelatedEvents string `json:"related_events"`
}

// Relationships holds proven file, span, and related-event evidence for one event.
type Relationships struct {
	Files         []insights.SessionFileEntry `json:"files"`
	Spans         []spans.Record              `json:"spans"`
	RelatedEvents []RelatedEvent              `json:"related_events"`
	Availability  RelationshipsAvailability   `json:"availability"`
}

// Truncation describes whether large retained values were shortened.
type Truncation struct {
	Applied         bool `json:"applied"`
	MaxStringRunes  int  `json:"max_string_runes"`
	ExpandAvailable bool `json:"expand_available"`
}

// Detail is the shared inspector view for JSON and HTML readers.
type Detail struct {
	Event              canonical.Event
	Attributes         map[string]any
	ProviderExtensions map[string]any
	Truncation         Truncation
	Relationships      Relationships
}

// Build assembles an inspector detail from one retained event and the session's
// evidence corpus. expand=true returns full attribute/extension values.
func Build(event canonical.Event, sessionEvents []canonical.Event, operations []canonical.Operation, expand bool) Detail {
	attrs, attrsTruncated := projectMap(event.Attributes, expand)
	exts, extsTruncated := projectMap(event.ProviderExtensions, expand)
	truncation := Truncation{
		Applied:         attrsTruncated || extsTruncated,
		MaxStringRunes:  DefaultMaxStringRunes,
		ExpandAvailable: (attrsTruncated || extsTruncated) && !expand,
	}
	return Detail{
		Event:              event,
		Attributes:         attrs,
		ProviderExtensions: exts,
		Truncation:         truncation,
		Relationships:      relationshipsFor(event, sessionEvents, operations),
	}
}

func relationshipsFor(event canonical.Event, sessionEvents []canonical.Event, operations []canonical.Operation) Relationships {
	operationID := strings.TrimSpace(optionalAttrString(event.Attributes["operation_id"]))
	files := filterFiles(insights.SessionFilesFromEvidence(sessionEvents, operations), event.EventID, operationID)
	spanRecords := filterSpans(spans.Project(sessionEvents), event.EventID, operationID)
	related := relatedEvents(event, sessionEvents, operationID, spanRecords)
	return Relationships{
		Files:         files,
		Spans:         spanRecords,
		RelatedEvents: related,
		Availability: RelationshipsAvailability{
			Files:         availabilityLabel(len(files) > 0),
			Spans:         availabilityLabel(len(spanRecords) > 0),
			RelatedEvents: availabilityLabel(len(related) > 0),
		},
	}
}

func filterFiles(entries []insights.SessionFileEntry, eventID, operationID string) []insights.SessionFileEntry {
	out := make([]insights.SessionFileEntry, 0)
	for _, entry := range entries {
		if entry.EventID == eventID {
			out = append(out, entry)
			continue
		}
		if operationID != "" && entry.OperationID != nil && *entry.OperationID == operationID {
			out = append(out, entry)
		}
	}
	return out
}

func filterSpans(records []spans.Record, eventID, operationID string) []spans.Record {
	out := make([]spans.Record, 0)
	for _, record := range records {
		if containsString(record.SourceEventIDs, eventID) {
			out = append(out, record)
			continue
		}
		if operationID != "" && containsString(record.OperationIDs, operationID) {
			out = append(out, record)
		}
	}
	return out
}

func relatedEvents(event canonical.Event, sessionEvents []canonical.Event, operationID string, linkedSpans []spans.Record) []RelatedEvent {
	byID := make(map[string]canonical.Event, len(sessionEvents))
	for _, candidate := range sessionEvents {
		byID[candidate.EventID] = candidate
	}
	queue := append(operationPeers(event.EventID, operationID, sessionEvents), spanPeers(event.EventID, linkedSpans)...)
	seen := map[string]struct{}{}
	out := make([]RelatedEvent, 0)
	for _, item := range queue {
		if _, exists := seen[item.id]; exists {
			continue
		}
		candidate, ok := byID[item.id]
		if !ok {
			continue
		}
		seen[item.id] = struct{}{}
		out = append(out, RelatedEvent{
			EventID:    candidate.EventID,
			EventType:  candidate.EventType,
			OccurredAt: candidate.OccurredAt.UTC().Format(time.RFC3339Nano),
			Relation:   item.relation,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OccurredAt != out[j].OccurredAt {
			return out[i].OccurredAt < out[j].OccurredAt
		}
		return out[i].EventID < out[j].EventID
	})
	return out
}

type relatedPending struct {
	id       string
	relation string
}

func operationPeers(eventID, operationID string, sessionEvents []canonical.Event) []relatedPending {
	if operationID == "" {
		return nil
	}
	out := make([]relatedPending, 0)
	for _, candidate := range sessionEvents {
		if candidate.EventID == eventID {
			continue
		}
		if strings.TrimSpace(optionalAttrString(candidate.Attributes["operation_id"])) == operationID {
			out = append(out, relatedPending{id: candidate.EventID, relation: RelationSharedOperation})
		}
	}
	return out
}

func spanPeers(eventID string, linkedSpans []spans.Record) []relatedPending {
	out := make([]relatedPending, 0)
	for _, span := range linkedSpans {
		for _, sourceID := range span.SourceEventIDs {
			if sourceID == eventID {
				continue
			}
			out = append(out, relatedPending{id: sourceID, relation: RelationSharedSpan})
		}
	}
	return out
}

func projectMap(values map[string]any, expand bool) (map[string]any, bool) {
	if values == nil {
		return map[string]any{}, false
	}
	out := make(map[string]any, len(values))
	truncated := false
	for key, value := range values {
		projected, didTruncate := projectValue(value, expand)
		if didTruncate {
			truncated = true
		}
		out[key] = projected
	}
	return out, truncated
}

func projectValue(value any, expand bool) (any, bool) {
	switch typed := value.(type) {
	case string:
		return truncateString(typed, expand)
	case map[string]any:
		return projectMap(typed, expand)
	case []any:
		out := make([]any, len(typed))
		truncated := false
		for i, item := range typed {
			projected, didTruncate := projectValue(item, expand)
			if didTruncate {
				truncated = true
			}
			out[i] = projected
		}
		return out, truncated
	default:
		return value, false
	}
}

func truncateString(value string, expand bool) (any, bool) {
	if expand || utf8.RuneCountInString(value) <= DefaultMaxStringRunes {
		return value, false
	}
	runes := []rune(value)
	return string(runes[:DefaultMaxStringRunes]) + "…", true
}

func optionalAttrString(value any) string {
	text, _ := value.(string)
	return text
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func availabilityLabel(observed bool) string {
	if observed {
		return AvailabilityObserved
	}
	return AvailabilityNone
}
