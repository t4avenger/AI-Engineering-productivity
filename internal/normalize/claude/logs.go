package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// ErrUnsupportedLogs indicates a valid OTLP log payload is not the observed
// Claude Code log shape and so must not be normalised by this adapter. It
// mirrors codex.ErrUnsupportedLogs so the ingest path can skip a payload that
// belongs to another tool without treating it as an error.
var ErrUnsupportedLogs = errors.New("unsupported Claude Code log payload")

// claudeLogService is the OTLP resource service.name emitted by Claude Code's
// telemetry exporter, confirmed by a live capture (service.version 2.1.263).
const claudeLogService = "claude-code"

type logsPayload struct {
	ResourceLogs []resourceLog `json:"resourceLogs"`
}

type resourceLog struct {
	Resource struct {
		Attributes []otlpAttribute `json:"attributes"`
	} `json:"resource"`
	ScopeLogs []scopeLog `json:"scopeLogs"`
}

type scopeLog struct {
	LogRecords []logRecord `json:"logRecords"`
}

type logRecord struct {
	Attributes []otlpAttribute `json:"attributes"`
}

type otlpAttribute struct {
	Key   string         `json:"key"`
	Value map[string]any `json:"value"`
}

// wireKeyMapping maps the dotted OTLP attribute keys that the reviewed-fixture
// path expects under underscore names onto that contract, so normaliseSampleEvent
// finds the required event_name/session_id/event_timestamp/event_sequence.
var wireKeyMapping = map[string]string{
	"event.name":      "event_name",
	"event.timestamp": "event_timestamp",
	"event.sequence":  "event_sequence",
	"session.id":      "session_id",
	"request.id":      "request_id",
}

// droppedKeyPrefixes are attribute keys that identify the operator, machine, or
// conversation rather than behaviour. They are dropped here so they never reach
// provider_extensions.event, belt-and-braces with the upstream sanitiser (which
// already removes user.email/user.account_id but retains user.id, session.id,
// organization.id, and user.account_uuid).
var droppedKeyPrefixes = []string{"user.", "organization.", "terminal."}

// droppedKeys are individual identifier attributes with no behavioural value.
var droppedKeys = map[string]struct{}{"prompt.id": {}, "message.uuid": {}}

// serverIdentityKeys carry a raw MCP server identity. Any such value is reduced
// to an installation HMAC fingerprint under server_fingerprint (which the MCP
// inventory reads) and the raw value is dropped, so a server name or path is
// never persisted verbatim.
var serverIdentityKeys = []string{"server_name", "mcp_server_name", "server.name", "mcp.server_name"}

// NormalizeLogs maps a raw, already-sanitised Claude Code OTLP/HTTP log payload
// into canonical events. It reuses normaliseSampleEvent so event IDs,
// fingerprinting, unavailable-field accounting, and provider-extension shape stay
// identical to the reviewed-fixture path (NormalizeEvents); this adapter owns
// only the wire concerns the fixture path does not: parsing resourceLogs,
// per-resource service.name routing, and reducing wire attributes to the
// sample-event contract while dropping identity attributes.
//
// Only resources whose service.name is claude-code are normalised; any other
// resource is skipped so a mixed payload is safe. A payload with no Claude
// records yields ErrUnsupportedLogs. The caller supplies the installation HMAC
// fingerprint, and must have run the shared privacy sanitiser over the payload
// first, exactly as the Codex ingest path does.
func NormalizeLogs(data []byte, receivedAt time.Time, fingerprint func([]byte) string) ([]canonical.Event, error) {
	if fingerprint == nil {
		return nil, errors.New("claude log fingerprint is required")
	}
	var payload logsPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode Claude OTLP logs: %w", err)
	}
	var events []canonical.Event
	for _, resource := range payload.ResourceLogs {
		resourceEvents, err := normaliseResourceLogs(resource, receivedAt, fingerprint, len(events))
		if err != nil {
			return nil, err
		}
		events = append(events, resourceEvents...)
	}
	if len(events) == 0 {
		return nil, ErrUnsupportedLogs
	}
	return normalize.CorrelateEvents(events), nil
}

// normaliseResourceLogs normalises a single resourceLogs entry, returning events
// only when its service.name is claude-code (nil otherwise, so a mixed payload is
// safe). indexBase is the count of events already produced, keeping event indexing
// stable and contiguous across resources.
func normaliseResourceLogs(resource resourceLog, receivedAt time.Time, fingerprint func([]byte) string, indexBase int) ([]canonical.Event, error) {
	resourceAttrs := attributeValues(resource.Resource.Attributes)
	if service, _ := resourceAttrs["service.name"].(string); service != claudeLogService {
		return nil, nil
	}
	version, _ := resourceAttrs["service.version"].(string)
	document := fixtureDocument{Provider: provider, Tool: tool, ToolVersion: fallbackString(version, unavailable)}
	var events []canonical.Event
	for _, scope := range resource.ScopeLogs {
		for _, record := range scope.LogRecords {
			sample := sampleEventFromRecord(record, fingerprint)
			if _, ok := sample["event_name"].(string); !ok {
				continue
			}
			event, err := normaliseSampleEvent(document, receivedAt.UTC(), fingerprint, indexBase+len(events), sample)
			if err != nil {
				return nil, err
			}
			events = append(events, event)
		}
	}
	return events, nil
}

// sampleEventFromRecord reduces one OTLP log record to the underscore-keyed
// sample-event map normaliseSampleEvent consumes: it maps the dotted semantic
// keys, drops operator/machine/conversation identifiers, fingerprints any raw
// MCP server identity, and forwards the remaining behaviour attributes verbatim.
func sampleEventFromRecord(record logRecord, fingerprint func([]byte) string) map[string]any {
	sample := make(map[string]any, len(record.Attributes))
	for _, attribute := range record.Attributes {
		if _, dropped := droppedKeys[attribute.Key]; dropped || hasDroppedPrefix(attribute.Key) {
			continue
		}
		value, ok := attributeValue(attribute.Value)
		if !ok {
			continue
		}
		if isServerIdentityKey(attribute.Key) {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				sample["server_fingerprint"] = "claude-code:" + fingerprint([]byte(text))
				sample["server_name"] = strings.TrimSpace(text)
			}
			continue
		}
		key := attribute.Key
		if mapped, ok := wireKeyMapping[key]; ok {
			key = mapped
		}
		sample[key] = value
	}
	return sample
}

func hasDroppedPrefix(key string) bool {
	for _, prefix := range droppedKeyPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func isServerIdentityKey(key string) bool {
	for _, candidate := range serverIdentityKeys {
		if key == candidate {
			return true
		}
	}
	return false
}

// attributeValues flattens OTLP resource attributes into a key/value map.
func attributeValues(attributes []otlpAttribute) map[string]any {
	values := make(map[string]any, len(attributes))
	for _, attribute := range attributes {
		if value, ok := attributeValue(attribute.Value); ok {
			values[attribute.Key] = value
		}
	}
	return values
}

// attributeValue decodes a single OTLP attribute value. intValue is decoded to
// float64 (JSON encodes it as either a number or an integer string) so it
// matches the decoded-JSON shape the fixture path and OptionalTokenCount expect,
// and so event_sequence stays usable as a stable ordinal. A sanitiser-emptied
// value ({}) yields ok=false and is skipped.
func attributeValue(value map[string]any) (any, bool) {
	if text, ok := value["stringValue"].(string); ok {
		return text, true
	}
	if boolean, ok := value["boolValue"].(bool); ok {
		return boolean, true
	}
	if number, ok := value["doubleValue"].(float64); ok {
		return number, true
	}
	switch integer := value["intValue"].(type) {
	case float64:
		return integer, true
	case string:
		if parsed, err := strconv.ParseFloat(integer, 64); err == nil {
			return parsed, true
		}
	}
	return nil, false
}

func fallbackString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
