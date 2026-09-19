// Package conversation projects retained provider content into an explicit
// evidence view. It does not parse provider payloads or infer message joins.
package conversation

import (
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleUnknown   = "unknown"

	AvailabilityAvailable        = "available"
	AvailabilityProviderRedacted = "provider_redacted"
	AvailabilityLengthOnly       = "length_only"
	AvailabilityBodyReference    = "body_reference"
	AvailabilityUnavailable      = "unavailable"
)

// Record is one retained, provider-emitted content observation. Text is nil
// when the provider did not retain an inline body; callers must use
// ContentAvailability rather than synthesising a replacement value.
type Record struct {
	EventID             string
	EventType           string
	OccurredAt          time.Time
	Provider            string
	Tool                string
	SourceVersion       string
	Role                string
	Text                *string
	ContentAvailability string
}

// Project selects only the reviewed Claude Code log content surface. Raw API
// bodies intentionally remain unknown-role evidence: a body is not parsed into
// messages because no stable identity proves its relationship to log records.
func Project(events []canonical.Event) []Record {
	result := make([]Record, 0)
	for _, event := range events {
		record, ok := projectEvent(event)
		if ok {
			result = append(result, record)
		}
	}
	return result
}

// Page returns a cursor page over already projected records. The cursor is the
// last returned source event, so filtering out non-content events cannot make
// a caller skip retained conversation evidence.
func Page(records []Record, limit int, cursorOccurredAt, cursorEventID string) ([]Record, *Record) {
	start := 0
	if cursorOccurredAt != "" || cursorEventID != "" {
		for index, record := range records {
			if record.OccurredAt.UTC().Format(time.RFC3339Nano) == cursorOccurredAt && record.EventID == cursorEventID {
				start = index + 1
				break
			}
		}
	}
	end := start + limit
	if end >= len(records) {
		return records[start:], nil
	}
	return records[start:end], &records[end-1]
}

func projectEvent(event canonical.Event) (Record, bool) {
	if event.Provider != "anthropic" || event.Tool != "claude-code" {
		return Record{}, false
	}
	key, role, lengthKeys, ok := contentShape(event.EventType)
	if !ok {
		return Record{}, false
	}
	echo, _ := event.ProviderExtensions["event"].(map[string]any)
	text, availability := contentValue(echo, key, lengthKeys...)
	return Record{
		EventID: event.EventID, EventType: event.EventType, OccurredAt: event.OccurredAt,
		Provider: event.Provider, Tool: event.Tool, SourceVersion: event.SourceVersion,
		Role: role, Text: text, ContentAvailability: availability,
	}, true
}

func contentShape(eventType string) (key, role string, lengthKeys []string, ok bool) {
	switch eventType {
	case "user_prompt":
		return "prompt", RoleUser, []string{"prompt_length"}, true
	case "assistant_response":
		return "response", RoleAssistant, []string{"response_length"}, true
	case "api_request_body", "api_response_body":
		return "body", RoleUnknown, []string{"body_length"}, true
	default:
		return "", "", nil, false
	}
}

func contentValue(echo map[string]any, key string, lengthKeys ...string) (*string, string) {
	if text, ok := echo[key].(string); ok {
		if providerRedacted(text) {
			return &text, AvailabilityProviderRedacted
		}
		return &text, AvailabilityAvailable
	}
	if _, ok := echo["body_ref"].(string); ok && key == "body" {
		return nil, AvailabilityBodyReference
	}
	for _, lengthKey := range lengthKeys {
		if _, ok := echo[lengthKey]; ok {
			return nil, AvailabilityLengthOnly
		}
	}
	return nil, AvailabilityUnavailable
}

func providerRedacted(text string) bool {
	return strings.EqualFold(strings.TrimSpace(text), "<redacted>")
}
