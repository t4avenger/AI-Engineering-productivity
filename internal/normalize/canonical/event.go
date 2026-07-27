// Package canonical defines the stable event representation shared by provider
// normalisers. Provider-specific fields belong in ProviderExtensions.
package canonical

import "time"

// Event is a canonical event envelope at schema version 0.1.0.
type Event struct {
	SchemaVersion      string         `json:"schema_version"`
	EventID            string         `json:"event_id"`
	EventType          string         `json:"event_type"`
	OccurredAt         time.Time      `json:"occurred_at"`
	ReceivedAt         time.Time      `json:"received_at"`
	Provider           string         `json:"provider"`
	Tool               string         `json:"tool"`
	SourceSchema       string         `json:"source_schema"`
	SourceVersion      string         `json:"source_version"`
	ActorID            string         `json:"actor_id"`
	DeviceID           string         `json:"device_id"`
	SessionID          string         `json:"session_id"`
	TaskID             *string        `json:"task_id"`
	RepositoryID       *string        `json:"repository_id"`
	PrivacyLevel       string         `json:"privacy_level"`
	Attributes         map[string]any `json:"attributes"`
	ProviderExtensions map[string]any `json:"provider_extensions"`
}
