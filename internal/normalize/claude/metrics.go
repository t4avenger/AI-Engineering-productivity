package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// ErrUnsupportedMetrics indicates a valid OTLP metrics payload is not the
// observed Claude Code metrics shape (no claude-code resource, or no metric this
// adapter maps). It mirrors codex.ErrUnsupportedMetrics so the ingest path can
// skip a payload that belongs to another tool without treating it as an error.
var ErrUnsupportedMetrics = errors.New("unsupported Claude Code metrics payload")

// tokenUsageMetric is the only Claude Code metric #89 maps end-to-end. The other
// seven exported metrics (cost/session/active-time/lines-of-code/commits/PRs/
// edit-decisions) are route-tolerated — decoded without error but not yet mapped;
// their per-metric canonical mapping is owned by the M-phase issues (#97–#99).
const tokenUsageMetric = "claude_code.token.usage"

// safeMetricAttributeKeys is the allow-list of datapoint/resource attribute keys
// carried into provider_extensions. Ingest-time storage sanitising was removed
// in #88, so this adapter is now the only guard: an allow-list (not a deny-list)
// ensures an unforeseen identity or secret-bearing attribute is dropped by
// default rather than persisted. model and type are promoted onto the canonical
// event and session.id becomes the session identity, so they are not repeated here.
var safeMetricAttributeKeys = map[string]struct{}{
	"query_source": {},
	"host.arch":    {},
	"os.type":      {},
}

type metricsPayload struct {
	ResourceMetrics []resourceMetric `json:"resourceMetrics"`
}

type resourceMetric struct {
	Resource struct {
		Attributes []otlpAttribute `json:"attributes"`
	} `json:"resource"`
	ScopeMetrics []scopeMetric `json:"scopeMetrics"`
}

type scopeMetric struct {
	Scope struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"scope"`
	Metrics []otlpMetric `json:"metrics"`
}

// otlpMetric decodes the sum/gauge/histogram instrument envelopes. Claude Code
// exports claude_code.token.usage as a monotonic sum; gauge and histogram are
// decoded too so a full metrics batch carrying other instrument types never
// fails to parse (unmapped metrics are simply skipped).
type otlpMetric struct {
	Name      string        `json:"name"`
	Unit      string        `json:"unit"`
	Sum       *metricPoints `json:"sum"`
	Gauge     *metricPoints `json:"gauge"`
	Histogram *metricPoints `json:"histogram"`
}

type metricPoints struct {
	DataPoints []metricDataPoint `json:"dataPoints"`
}

// metricDataPoint decodes a sum/gauge numeric datapoint. A decoded JSON value is
// either asDouble (number) or asInt (integer string or number); both are read so
// the observed asDouble token counts and any asInt exporter variant are accepted.
type metricDataPoint struct {
	Attributes   []otlpAttribute `json:"attributes"`
	AsInt        any             `json:"asInt"`
	AsDouble     any             `json:"asDouble"`
	TimeUnixNano string          `json:"timeUnixNano"`
}

// NormalizeMetrics maps a raw, already-sanitised Claude Code OTLP/HTTP metrics
// payload into canonical events. Only resources whose service.name is claude-code
// are considered, so a mixed payload from another tool is skipped rather than
// misattributed. Within a Claude resource, claude_code.token.usage sum datapoints
// become token-usage events; every other metric is accepted but yields no event.
//
// Routing contract:
//   - No claude-code resource at all, or a claude-code resource with no mapped
//     metric, yields no events and ErrUnsupportedMetrics — a sentinel the ingest
//     path skips so the payload is not misattributed.
//   - A claude_code.token.usage datapoint with a recognised token type but an
//     unparseable value is a real normalisation error, not the sentinel, so the
//     route does not silently 202-accept and drop supported Claude data (#89).
//
// The raw session identity is retained verbatim as a provider-native ID; no
// ingest-time hiding is applied (epic #87). Identity and unforeseen attributes
// are dropped by the safeMetricAttributeKeys allow-list.
func NormalizeMetrics(data []byte, receivedAt time.Time) ([]canonical.Event, error) {
	var payload metricsPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode Claude OTLP metrics: %w", err)
	}
	var events []canonical.Event
	for _, resource := range payload.ResourceMetrics {
		resourceEvents, err := tokenEventsFromResource(resource, receivedAt)
		if err != nil {
			return nil, err
		}
		events = append(events, resourceEvents...)
	}
	if len(events) == 0 {
		return nil, ErrUnsupportedMetrics
	}
	return normalize.CorrelateEvents(events), nil
}

// tokenEventsFromResource normalises a single resourceMetrics entry, returning
// events only when its service.name is claude-code (nil otherwise, so a mixed
// payload is safe).
func tokenEventsFromResource(resource resourceMetric, receivedAt time.Time) ([]canonical.Event, error) {
	resourceAttrs := attributeValues(resource.Resource.Attributes)
	if service, _ := resourceAttrs["service.name"].(string); service != claudeLogService {
		return nil, nil
	}
	version := fallbackString(stringAttr(resourceAttrs, "service.version"), unavailable)
	safeResource := safeMetricAttributes(resourceAttrs)
	resourceIdentity := resourceIdentityKey(resourceAttrs)
	var events []canonical.Event
	for _, scope := range resource.ScopeMetrics {
		for _, item := range scope.Metrics {
			metricEvents, err := tokenEventsFromMetric(item, scope.Scope.Name, resourceIdentity, safeResource, version, receivedAt)
			if err != nil {
				return nil, err
			}
			events = append(events, metricEvents...)
		}
	}
	return events, nil
}

// tokenEventsFromMetric maps the datapoints of a single claude_code.token.usage
// metric into events; any other metric yields none (route-tolerated). The value
// points are read from the sum envelope (the observed instrument), falling back
// to gauge so a future exporter variant still parses.
func tokenEventsFromMetric(item otlpMetric, scopeName, resourceIdentity string, safeResource map[string]any, version string, receivedAt time.Time) ([]canonical.Event, error) {
	if item.Name != tokenUsageMetric {
		return nil, nil
	}
	points := item.Sum
	if points == nil {
		points = item.Gauge
	}
	if points == nil {
		return nil, nil
	}
	var events []canonical.Event
	for index, point := range points.DataPoints {
		event, ok, err := tokenUsageEvent(point, index, scopeName, resourceIdentity, safeResource, version, item.Unit, receivedAt)
		if err != nil {
			return nil, err
		}
		if ok {
			events = append(events, event)
		}
	}
	return events, nil
}

// tokenUsageEvent maps one claude_code.token.usage datapoint into a canonical
// event. An unrecognised token type is skipped (ok=false) so a future Claude
// token category does not fail the whole batch; a recognised token type whose
// value cannot be parsed is a hard error per the #89 routing contract.
func tokenUsageEvent(point metricDataPoint, index int, scopeName, resourceIdentity string, safeResource map[string]any, version, unit string, receivedAt time.Time) (canonical.Event, bool, error) {
	fields := attributeValues(point.Attributes)
	tokenType := strings.TrimSpace(stringAttr(fields, "type"))
	attributeKey, ok := tokenUsageAttribute(tokenType)
	if !ok {
		return canonical.Event{}, false, nil
	}
	tokens := normalize.OptionalTokenCount(dataPointValue(point))
	if tokens == nil {
		return canonical.Event{}, false, fmt.Errorf("claude token.usage %q datapoint has no parseable value", tokenType)
	}

	occurredAt := metricTime(point.TimeUnixNano, receivedAt)
	model, modelObserved := normalize.ObservedString(fields["model"])
	sessionID := normalize.ProviderNativeSessionID("claude-code:", stringAttr(fields, "session.id"))
	safeFields := safeMetricAttributes(fields)

	identity := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%d|%d", tokenUsageMetric, model, tokenType, point.TimeUnixNano, resourceIdentity, scopeName, index, *tokens)
	identity += "|" + stableJSON(safeFields)
	eventID := contentID("claude-code:token:", []byte(identity))

	attributes := map[string]any{
		"unavailable_fields": []string{"tool_calls", "mcp_calls", "file_operations", "command_execution", "prompt_content", "response_content", "repository_context", "task_outcome", "provider_cost", "trace_span_correlation"},
		attributeKey:         *tokens,
	}
	if modelObserved {
		attributes["model"] = model
	}
	extensions := map[string]any{
		"correlation": tokenCorrelation(eventID, occurredAt),
		"metric": map[string]any{
			"name":       tokenUsageMetric,
			"token_type": tokenType,
			"unit":       unit,
		},
		"resource":          safeResource,
		"metric_attributes": safeFields,
	}
	return canonical.Event{
		SchemaVersion:      canonicalSchemaVersion,
		EventID:            eventID,
		EventType:          tokenUsageMetric,
		OccurredAt:         occurredAt,
		ReceivedAt:         receivedAt.UTC(),
		Provider:           provider,
		Tool:               tool,
		SourceSchema:       sourceSchema,
		SourceVersion:      version,
		ActorID:            unavailable,
		DeviceID:           unavailable,
		SessionID:          sessionID,
		PrivacyLevel:       "operational",
		Attributes:         attributes,
		ProviderExtensions: extensions,
	}, true, nil
}

// tokenUsageAttribute maps Claude Code's observed token categories (camelCase on
// the wire: input/output/cacheRead/cacheCreation) onto the canonical token count
// keys shared with the Codex adapter, so cross-tool cost logic reads one schema.
func tokenUsageAttribute(tokenType string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(tokenType)) {
	case "input":
		return "input_token_count", true
	case "output":
		return "output_token_count", true
	case "cacheread":
		return "cached_input_token_count", true
	case "cachecreation":
		return "cache_write_input_token_count", true
	default:
		return "", false
	}
}

func tokenCorrelation(eventID string, occurredAt time.Time) map[string]any {
	return map[string]any{
		"dedup_key":    eventID,
		"ordering_key": fmt.Sprintf("%020d:%s", occurredAt.UnixNano(), eventID),
		"task_boundary": map[string]any{
			"confidence": "unknown",
			"reason":     "Claude Code token metrics have no reviewed task-boundary signal",
		},
	}
}

// safeMetricAttributes reduces OTLP attributes to the allow-listed safe keys,
// dropping operator/identity and unforeseen attributes by default (#88 removed
// storage-side sanitising, so the adapter is the sole guard).
func safeMetricAttributes(fields map[string]any) map[string]any {
	safe := make(map[string]any)
	for key, value := range fields {
		if _, ok := safeMetricAttributeKeys[strings.ToLower(strings.TrimSpace(key))]; ok {
			safe[key] = value
		}
	}
	return safe
}

func resourceIdentityKey(resourceAttrs map[string]any) string {
	return stableJSON(map[string]any{
		"service.name":    stringAttr(resourceAttrs, "service.name"),
		"service.version": stringAttr(resourceAttrs, "service.version"),
		"resource":        safeMetricAttributes(resourceAttrs),
	})
}

func stringAttr(fields map[string]any, key string) string {
	text, _ := fields[key].(string)
	return text
}

// dataPointValue returns the datapoint's numeric value, preferring asDouble
// (Claude Code's observed encoding) and falling back to asInt.
func dataPointValue(point metricDataPoint) any {
	if point.AsDouble != nil {
		return point.AsDouble
	}
	return point.AsInt
}

func stableJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

// contentID derives a stable, non-keyed uniqueness key for a datapoint that has
// no provider-native event ID. It is a plain content hash for deduplication only
// — not a privacy transform (epic #87 removed ingest-time hiding).
func contentID(prefix string, data []byte) string {
	sum := sha256.Sum256(data)
	return prefix + hex.EncodeToString(sum[:])
}

// metricTime parses an OTLP nanosecond timestamp, falling back to the receive
// time when it is absent or unparseable so an event never carries a zero time.
func metricTime(nano string, fallback time.Time) time.Time {
	if strings.TrimSpace(nano) == "" {
		return fallback.UTC()
	}
	parsed, err := strconv.ParseInt(nano, 10, 64)
	if err != nil || parsed <= 0 {
		return fallback.UTC()
	}
	return time.Unix(0, parsed).UTC()
}
