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

// droppedKeys are attributes dropped at the wire boundary. tool_parameters is
// gated content: on tool_decision (and with OTEL_LOG_TOOL_DETAILS=1 more broadly)
// it carries the full command and MCP server/tool names, so it is not surfaced
// here. That drop is pre-existing and owned by #173; whether the epic #87
// raw-capture stance should retain it is #173's decision, not settled here. It is
// the only remaining wire-boundary drop: operator/machine identity
// (user.*/organization.*/terminal.*) is no longer dropped — per the owner directive
// (and epic #87 / PRODUCT_MAP §11.3) nothing is dropped at the local-only ingest
// boundary; those keys ride raw into provider_extensions.environment (#107 X20),
// the per-field visibility decision deferred downstream and re-evaluated only at
// the cloud/cross-device upload boundary. prompt.id/message.uuid are the per-prompt
// / per-message correlation ids retained under provider_extensions.correlation
// (#106), not dropped — the normaliser lifts them there and excludes them from the
// event echo.
var droppedKeys = map[string]struct{}{"tool_parameters": {}}

// serverIdentityKeys carry a provider-reported MCP server identity. The raw name
// is retained for local inventory display and correlation (epic #87 — no hiding).
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
// records yields ErrUnsupportedLogs. The raw payload is normalised verbatim —
// no ingest-time hiding is applied (epic #87).
func NormalizeLogs(data []byte, receivedAt time.Time) ([]canonical.Event, error) {
	var payload logsPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode Claude OTLP logs: %w", err)
	}
	var events []canonical.Event
	for _, resource := range payload.ResourceLogs {
		resourceEvents, err := normaliseResourceLogs(resource, receivedAt, len(events))
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
func normaliseResourceLogs(resource resourceLog, receivedAt time.Time, indexBase int) ([]canonical.Event, error) {
	resourceAttrs := attributeValues(resource.Resource.Attributes)
	if service, _ := resourceAttrs["service.name"].(string); service != claudeLogService {
		return nil, nil
	}
	version, _ := resourceAttrs["service.version"].(string)
	document := fixtureDocument{Provider: provider, Tool: tool, ToolVersion: fallbackString(version, unavailable)}
	var events []canonical.Event
	for _, scope := range resource.ScopeLogs {
		for _, record := range scope.LogRecords {
			sample := sampleEventFromRecord(record)
			if _, ok := sample["event_name"].(string); !ok {
				continue
			}
			event, err := normaliseSampleEvent(document, receivedAt.UTC(), indexBase+len(events), sample)
			if err != nil {
				return nil, err
			}
			attachResourceEnvironment(event.ProviderExtensions, resourceAttrs)
			events = append(events, event)
		}
	}
	return events, nil
}

// sampleEventFromRecord reduces one OTLP log record to the underscore-keyed
// sample-event map normaliseSampleEvent consumes: it maps the dotted semantic
// keys, retains provider session, operator/machine identity, and MCP server
// display identities raw (nothing dropped at the local-only ingest boundary —
// owner directive / epic #87; identity rides into provider_extensions.environment,
// #107), and forwards the remaining behaviour attributes verbatim. Only the gated
// droppedKeys (tool_parameters, #173) are withheld.
func sampleEventFromRecord(record logRecord) map[string]any {
	sample := make(map[string]any, len(record.Attributes))
	for _, attribute := range record.Attributes {
		if _, dropped := droppedKeys[attribute.Key]; dropped {
			continue
		}
		value, ok := attributeValue(attribute.Value)
		if !ok {
			continue
		}
		if isServerIdentityKey(attribute.Key) {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
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

// attachResourceEnvironment retains the OTLP resource attributes on a log event:
// the full raw attribute set under provider_extensions.resource (nothing dropped —
// owner directive / epic #87) and the machine/app environment keys merged into the
// present-only provider_extensions.environment block, so a log event carries the
// same environment/identity surface a trace span does (#107 X20). The environment
// block already holds the record-level identity keys (built in normaliseSampleEvent
// from the log record); resource keys fill only the slots the record did not carry,
// so record identity is never overwritten by a resource value.
func attachResourceEnvironment(extensions, resourceAttrs map[string]any) {
	if len(resourceAttrs) == 0 {
		return
	}
	extensions["resource"] = copyAttributes(resourceAttrs)
	resourceEnvironment := claudeEnvironment(resourceAttrs)
	if resourceEnvironment == nil {
		return
	}
	existing, ok := extensions["environment"].(map[string]any)
	if !ok || existing == nil {
		extensions["environment"] = resourceEnvironment
		return
	}
	for key, value := range resourceEnvironment {
		if _, present := existing[key]; !present {
			existing[key] = value
		}
	}
}

// copyAttributes returns a shallow copy so per-event provider_extensions.resource
// blocks do not alias the shared per-resource attribute map.
func copyAttributes(attrs map[string]any) map[string]any {
	out := make(map[string]any, len(attrs))
	for key, value := range attrs {
		out[key] = value
	}
	return out
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

// arrayAttributeValues finds attribute key among OTLP attributes and decodes its
// arrayValue string members into a []string. It complements attributeValues,
// which reads only scalar values: Claude Code encodes gen_ai.response.finish_reasons
// as an OTLP arrayValue that the scalar attributeValue decoder cannot read (so it
// was silently dropped before #100). The scalar decoder is left untouched so the
// logs/metrics goldens do not shift. Returns nil when the key is absent or carries
// no non-empty string members, so a genuine absence never becomes an empty slice.
func arrayAttributeValues(attributes []otlpAttribute, key string) []string {
	for _, attribute := range attributes {
		if attribute.Key == key {
			return decodeStringArray(attribute.Value)
		}
	}
	return nil
}

// decodeStringArray decodes an OTLP arrayValue attribute value into its non-empty
// string members, returning nil when the value is not a string array or carries no
// non-empty members so a genuine absence never becomes an empty slice.
func decodeStringArray(value map[string]any) []string {
	array, ok := value["arrayValue"].(map[string]any)
	if !ok {
		return nil
	}
	rawValues, ok := array["values"].([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(rawValues))
	for _, item := range rawValues {
		member, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := member["stringValue"].(string); ok && strings.TrimSpace(text) != "" {
			result = append(result, strings.TrimSpace(text))
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func fallbackString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
