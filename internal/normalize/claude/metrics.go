package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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

// tokenUsageMetric and costUsageMetric are the Claude Code metrics mapped
// end-to-end: #89 landed token.usage, #97 (M10) adds cost.usage plus the
// per-datapoint attribution dimensions (which skill / MCP tool / sub-agent /
// plugin burned the tokens and cost). The remaining exported metrics
// (session/active-time/lines-of-code/commits/PRs/edit-decisions) are
// route-tolerated — decoded without error but not yet mapped; their per-metric
// canonical mapping is owned by the later M-phase issues (#98–#99).
const (
	tokenUsageMetric = "claude_code.token.usage"
	costUsageMetric  = "claude_code.cost.usage"
)

// OTLP resource attribute keys read from a Claude Code metrics payload.
const (
	attrServiceName    = "service.name"
	attrServiceVersion = "service.version"
)

// safeMetricAttributeKeys is the allow-list of datapoint/resource attribute keys
// carried into provider_extensions. Ingest-time storage sanitising was removed
// in #88, so this adapter is now the only guard: an allow-list (not a deny-list)
// ensures an unforeseen identity or secret-bearing attribute is dropped by
// default rather than persisted. model and type are promoted onto the canonical
// event and session.id becomes the session identity, so they are not repeated here.
//
// The attribution dimensions (#97, M10) answer the epic's central efficiency
// question — which skill / MCP tool / sub-agent / plugin burned the tokens and
// cost. Claude Code emits these dotted on the wire (skill.name, mcp_server.name,
// mcp_tool.name, agent.name, plugin.name, marketplace.name; see
// https://code.claude.com/docs/en/monitoring-usage); the underscore spellings
// are allow-listed too so the same key survives whichever form an exporter
// build emits (the match lower-cases and trims). speed/effort are the model
// query-tuning dims already modelled in the traces (traces.go) and JSONL
// (jsonl.go) paths. All are pre-redacted, non-identity behaviour metadata.
var safeMetricAttributeKeys = map[string]struct{}{
	"query_source":     {},
	"host.arch":        {},
	"os.type":          {},
	"skill.name":       {},
	"skill_name":       {},
	"mcp_server.name":  {},
	"mcp_server_name":  {},
	"mcp_tool.name":    {},
	"mcp_tool_name":    {},
	"agent.name":       {},
	"agent_name":       {},
	"plugin.name":      {},
	"plugin_name":      {},
	"marketplace.name": {},
	"marketplace_name": {},
	"speed":            {},
	"effort":           {},
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
// misattributed. Within a Claude resource, claude_code.token.usage and
// claude_code.cost.usage datapoints become events; every other metric is
// accepted but yields no event.
//
// Routing contract:
//   - No claude-code resource at all, or a claude-code resource with no mapped
//     metric, yields no events and ErrUnsupportedMetrics — a sentinel the ingest
//     path skips so the payload is not misattributed.
//   - A claude_code.token.usage datapoint with a recognised token type but an
//     unparseable value, or a claude_code.cost.usage datapoint with an
//     unparseable USD value, is a real normalisation error, not the sentinel, so
//     the route does not silently 202-accept and drop supported Claude data
//     (#89 token, #97 cost).
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
		resourceEvents, err := metricEventsFromResource(resource, receivedAt)
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

// metricContext carries the resource- and scope-derived values shared by every
// datapoint of a metric, so the per-datapoint helpers stay within the argument
// limit rather than threading each field separately.
type metricContext struct {
	scopeName        string
	resourceIdentity string
	safeResource     map[string]any
	version          string
	receivedAt       time.Time
	// costCaptured is true when the same claude-code resource also emits a
	// cost.usage datapoint, so a token.usage event stops declaring provider_cost
	// unavailable — the sibling cost.usage event carries it, correlated by
	// session. It stays declared unavailable when no cost.usage is captured.
	costCaptured bool
}

// datapointBuilder maps one sum/gauge datapoint into a canonical event. ok=false
// skips the datapoint (e.g. an unrecognised token type) without failing the
// batch; a non-nil error is a hard normalisation failure per the routing
// contract. token.usage and cost.usage share this shape so one datapoint loop
// serves both.
type datapointBuilder func(point metricDataPoint, index int, ctx metricContext, unit string) (canonical.Event, bool, error)

// metricEventsFromResource normalises a single resourceMetrics entry, returning
// events only when its service.name is claude-code (nil otherwise, so a mixed
// payload is safe).
func metricEventsFromResource(resource resourceMetric, receivedAt time.Time) ([]canonical.Event, error) {
	resourceAttrs := attributeValues(resource.Resource.Attributes)
	if service, _ := resourceAttrs[attrServiceName].(string); service != claudeLogService {
		return nil, nil
	}
	ctx := metricContext{
		resourceIdentity: resourceIdentityKey(resourceAttrs),
		safeResource:     safeMetricAttributes(resourceAttrs),
		version:          fallbackString(stringAttr(resourceAttrs, attrServiceVersion), unavailable),
		receivedAt:       receivedAt,
		costCaptured:     resourceEmitsCostUsage(resource),
	}
	var events []canonical.Event
	for _, scope := range resource.ScopeMetrics {
		ctx.scopeName = scope.Scope.Name
		for _, item := range scope.Metrics {
			metricEvents, err := metricEventsFromInstrument(item, ctx)
			if err != nil {
				return nil, err
			}
			events = append(events, metricEvents...)
		}
	}
	return events, nil
}

// resourceEmitsCostUsage reports whether a resource carries at least one
// cost.usage datapoint, so its token.usage events can honestly stop marking
// provider_cost unavailable. A cost.usage metric with no datapoints does not
// count — nothing is actually captured.
func resourceEmitsCostUsage(resource resourceMetric) bool {
	for _, scope := range resource.ScopeMetrics {
		for _, item := range scope.Metrics {
			if item.Name == costUsageMetric && len(metricDataPoints(item)) > 0 {
				return true
			}
		}
	}
	return false
}

// metricEventsFromInstrument dispatches a single metric to its per-datapoint
// builder; any unmapped metric yields none (route-tolerated).
func metricEventsFromInstrument(item otlpMetric, ctx metricContext) ([]canonical.Event, error) {
	switch item.Name {
	case tokenUsageMetric:
		return datapointEvents(item, ctx, tokenUsageEvent)
	case costUsageMetric:
		return datapointEvents(item, ctx, costUsageEvent)
	default:
		return nil, nil
	}
}

// datapointEvents applies build to every datapoint of a metric. The value points
// are read from the sum envelope (the observed instrument), falling back to gauge
// so a future exporter variant still parses.
func datapointEvents(item otlpMetric, ctx metricContext, build datapointBuilder) ([]canonical.Event, error) {
	var events []canonical.Event
	for index, point := range metricDataPoints(item) {
		event, ok, err := build(point, index, ctx, item.Unit)
		if err != nil {
			return nil, err
		}
		if ok {
			events = append(events, event)
		}
	}
	return events, nil
}

// metricDataPoints returns a metric's datapoints from whichever numeric
// instrument envelope carries them (sum preferred, gauge fallback).
func metricDataPoints(item otlpMetric) []metricDataPoint {
	points := item.Sum
	if points == nil {
		points = item.Gauge
	}
	if points == nil {
		return nil
	}
	return points.DataPoints
}

// tokenUsageEvent maps one claude_code.token.usage datapoint into a canonical
// event. An unrecognised token type is skipped (ok=false) so a future Claude
// token category does not fail the whole batch; a recognised token type whose
// value cannot be parsed is a hard error per the #89 routing contract.
func tokenUsageEvent(point metricDataPoint, index int, ctx metricContext, unit string) (canonical.Event, bool, error) {
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

	occurredAt := metricTime(point.TimeUnixNano, ctx.receivedAt)
	model, modelObserved := normalize.ObservedString(fields["model"])
	sessionID := normalize.ProviderNativeSessionID(nativeSessionPrefix, stringAttr(fields, "session.id"))
	safeFields := safeMetricAttributes(fields)

	// session.id is part of the event's semantic identity but is not in the
	// safe-attribute allow-list, so it is added to the hash input explicitly:
	// two concurrent sessions emitting the same metric/type/model/timestamp must
	// not collide on one event ID and have one silently dropped by CorrelateEvents
	// (or the global event_id primary key in storage).
	identity := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%d|%d", tokenUsageMetric, sessionID, model, tokenType, point.TimeUnixNano, ctx.resourceIdentity, ctx.scopeName, index, *tokens)
	identity += "|" + stableJSON(safeFields)
	eventID := contentID("claude-code:token:", []byte(identity))

	attributes := map[string]any{
		"unavailable_fields": tokenUnavailableFields(ctx.costCaptured),
		attributeKey:         *tokens,
	}
	if modelObserved {
		attributes["model"] = model
	}
	extensions := map[string]any{
		"correlation": metricCorrelation(eventID, occurredAt),
		"metric": map[string]any{
			"name":       tokenUsageMetric,
			"token_type": tokenType,
			"unit":       unit,
		},
		"resource":          ctx.safeResource,
		"metric_attributes": safeFields,
	}
	return canonical.Event{
		SchemaVersion:      canonicalSchemaVersion,
		EventID:            eventID,
		EventType:          tokenUsageMetric,
		OccurredAt:         occurredAt,
		ReceivedAt:         ctx.receivedAt.UTC(),
		Provider:           provider,
		Tool:               tool,
		SourceSchema:       sourceSchema,
		SourceVersion:      ctx.version,
		ActorID:            unavailable,
		DeviceID:           unavailable,
		SessionID:          sessionID,
		PrivacyLevel:       "operational",
		Attributes:         attributes,
		ProviderExtensions: extensions,
	}, true, nil
}

// costUsageEvent maps one claude_code.cost.usage datapoint into a canonical
// event. Cost is secondary/descriptive (PRODUCT_MAP §0): the provider-reported
// USD amount is preserved verbatim under provider_cost as evidence, never fed
// into cost.Calculate (that would double-count request-level pricing). A
// datapoint whose value cannot be parsed is a hard error per the routing
// contract so the route does not silently drop supported Claude cost data.
func costUsageEvent(point metricDataPoint, index int, ctx metricContext, unit string) (canonical.Event, bool, error) {
	fields := attributeValues(point.Attributes)
	cost := optionalCostUSD(dataPointValue(point))
	if cost == nil {
		return canonical.Event{}, false, errors.New("claude cost.usage datapoint has no parseable USD value")
	}

	occurredAt := metricTime(point.TimeUnixNano, ctx.receivedAt)
	model, modelObserved := normalize.ObservedString(fields["model"])
	sessionID := normalize.ProviderNativeSessionID(nativeSessionPrefix, stringAttr(fields, "session.id"))
	safeFields := safeMetricAttributes(fields)

	// The formatted cost joins the identity so two same-timestamp cost points in
	// one session (e.g. distinct attribution dims) stay distinct under
	// CorrelateEvents, mirroring the token.usage collision-safety.
	identity := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%d|%s", costUsageMetric, sessionID, model, point.TimeUnixNano, ctx.resourceIdentity, ctx.scopeName, index, strconv.FormatFloat(*cost, 'f', -1, 64))
	identity += "|" + stableJSON(safeFields)
	eventID := contentID("claude-code:cost:", []byte(identity))

	attributes := map[string]any{
		"unavailable_fields": costUnavailableFields(),
		"provider_cost":      *cost,
	}
	if modelObserved {
		attributes["model"] = model
	}
	extensions := map[string]any{
		"correlation": metricCorrelation(eventID, occurredAt),
		"metric": map[string]any{
			"name": costUsageMetric,
			"unit": unit,
		},
		"resource":          ctx.safeResource,
		"metric_attributes": safeFields,
	}
	return canonical.Event{
		SchemaVersion:      canonicalSchemaVersion,
		EventID:            eventID,
		EventType:          costUsageMetric,
		OccurredAt:         occurredAt,
		ReceivedAt:         ctx.receivedAt.UTC(),
		Provider:           provider,
		Tool:               tool,
		SourceSchema:       sourceSchema,
		SourceVersion:      ctx.version,
		ActorID:            unavailable,
		DeviceID:           unavailable,
		SessionID:          sessionID,
		PrivacyLevel:       "operational",
		Attributes:         attributes,
		ProviderExtensions: extensions,
	}, true, nil
}

// tokenUnavailableFields lists the canonical fields a token.usage event does not
// carry. provider_cost is included only when no sibling cost.usage datapoint was
// captured on the same resource; when cost is captured, the correlated
// cost.usage event carries it, so declaring it unavailable here would be false.
func tokenUnavailableFields(costCaptured bool) []string {
	fields := []string{"tool_calls", "mcp_calls", "file_operations", "command_execution", "prompt_content", "response_content", "repository_context", "task_outcome"}
	if !costCaptured {
		fields = append(fields, "provider_cost")
	}
	return append(fields, "trace_span_correlation")
}

// costUnavailableFields lists the canonical fields a cost.usage event does not
// carry. It carries provider_cost (so that is absent from the list) but no token
// counts or behaviour signals.
func costUnavailableFields() []string {
	return []string{"token_usage", "tool_calls", "mcp_calls", "file_operations", "command_execution", "prompt_content", "response_content", "repository_context", "task_outcome", "trace_span_correlation"}
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

func metricCorrelation(eventID string, occurredAt time.Time) map[string]any {
	return map[string]any{
		"dedup_key":    eventID,
		"ordering_key": fmt.Sprintf("%020d:%s", occurredAt.UnixNano(), eventID),
		"task_boundary": map[string]any{
			"confidence": "unknown",
			"reason":     "Claude Code usage metrics have no reviewed task-boundary signal",
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
		attrServiceName:    stringAttr(resourceAttrs, attrServiceName),
		attrServiceVersion: stringAttr(resourceAttrs, attrServiceVersion),
		"resource":         safeMetricAttributes(resourceAttrs),
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

// optionalCostUSD parses a cost.usage datapoint value into a *float64 USD amount.
// Decoded JSON encodes a number as float64 and a numeric string as string; both
// are accepted. An absent, unparseable, negative, or non-finite (NaN/Inf) value
// yields nil — never a fabricated cost — so a genuine 0.0 stays distinguishable
// from a missing cost, mirroring OptionalTokenCount's never-fabricate discipline.
func optionalCostUSD(value any) *float64 {
	var cost float64
	switch typed := value.(type) {
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return nil
		}
		cost = parsed
	case float64:
		cost = typed
	default:
		return nil
	}
	if math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 {
		return nil
	}
	return &cost
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
