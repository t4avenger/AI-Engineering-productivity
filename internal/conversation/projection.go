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
	// Thinking is the provider's retained reasoning text, verbatim, or nil when
	// none was retained. ContentAvailability describes Text only.
	Thinking *string
}

// Project selects only the reviewed content surfaces: Claude Code OTLP logs and
// session JSONL transcripts, and Codex. Raw API bodies intentionally remain
// unknown-role evidence: a body is not parsed into messages because no stable
// identity proves its relationship to log records.
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
	if !IsConversationEvent(event) {
		return Record{}, false
	}
	shape, _ := contentShape(event)
	extension, _ := event.ProviderExtensions[shape.namespace].(map[string]any)
	text, availability := contentValue(extension, shape.key, shape.lengthKeys...)
	return Record{
		EventID: event.EventID, EventType: event.EventType, OccurredAt: event.OccurredAt,
		Provider: event.Provider, Tool: event.Tool, SourceVersion: event.SourceVersion,
		Role: shape.role, Text: text, ContentAvailability: availability,
		Thinking: thinkingValue(event, shape),
	}, true
}

// IsConversationEvent reports whether an event projects into a conversation
// record. It is the single source of truth used by both Project and
// session-availability reporting, so the "conversation coverage" signal can
// never drift from what Project actually emits: it applies the same provider/tool
// guard (only Claude Code content events are conversation records) as the
// projection, not just the event-type shape.
func IsConversationEvent(event canonical.Event) bool {
	if !isConversationProvider(event.Provider, event.Tool) {
		return false
	}
	shape, ok := contentShape(event)
	if !ok {
		return false
	}
	if shape.namespace != transcriptNamespace {
		return true
	}
	// A transcript record projects only when it carries retained text: a
	// tool_use-only assistant record is model work, its body rides on the
	// Operation, and projecting it would invent an empty conversation turn.
	transcript, _ := event.ProviderExtensions[transcriptNamespace].(map[string]any)
	return nonEmptyString(transcript, shape.key) || nonEmptyString(transcript, shape.thinkingKey)
}

func isConversationProvider(provider, tool string) bool {
	return isClaudeCode(provider, tool) || (provider == "openai" && tool == "codex")
}

func isClaudeCode(provider, tool string) bool {
	return provider == "anthropic" && tool == "claude-code"
}

const (
	eventNamespace      = "event"
	transcriptNamespace = "transcript"
)

// contentLocation locates an event type's retained content: the provider_extensions
// namespace and key holding the body, the length-only fallbacks, and (for
// transcript assistant records) the provider thinking key.
type contentLocation struct {
	namespace   string
	key         string
	role        string
	lengthKeys  []string
	thinkingKey string
}

func contentShape(event canonical.Event) (contentLocation, bool) {
	switch event.EventType {
	case "user_prompt":
		return contentLocation{namespace: eventNamespace, key: "prompt", role: RoleUser, lengthKeys: []string{"prompt_length"}}, true
	case "assistant_response":
		return contentLocation{namespace: eventNamespace, key: "response", role: RoleAssistant, lengthKeys: []string{"response_length"}}, true
	case "api_request_body", "api_response_body":
		return contentLocation{namespace: eventNamespace, key: "body", role: RoleUnknown, lengthKeys: []string{"body_length"}}, true
	}
	// Session JSONL transcript records are a Claude Code-only surface.
	if !isClaudeCode(event.Provider, event.Tool) {
		return contentLocation{}, false
	}
	switch event.EventType {
	case "user_message":
		return contentLocation{namespace: transcriptNamespace, key: "prompt_content", role: RoleUser}, true
	case "assistant_message":
		return contentLocation{namespace: transcriptNamespace, key: "response_content", role: RoleAssistant, thinkingKey: "thinking"}, true
	default:
		return contentLocation{}, false
	}
}

func thinkingValue(event canonical.Event, shape contentLocation) *string {
	if shape.thinkingKey == "" {
		return nil
	}
	extension, _ := event.ProviderExtensions[shape.namespace].(map[string]any)
	if !nonEmptyString(extension, shape.thinkingKey) {
		return nil
	}
	thinking := extension[shape.thinkingKey].(string)
	return &thinking
}

func nonEmptyString(values map[string]any, key string) bool {
	if key == "" {
		return false
	}
	text, ok := values[key].(string)
	return ok && strings.TrimSpace(text) != ""
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
