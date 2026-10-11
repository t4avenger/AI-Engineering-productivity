package codex

import (
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
// observed Codex metrics shape (or carries no skill-injection datapoints this
// adapter persists).
var ErrUnsupportedMetrics = errors.New("unsupported Codex metrics payload")

const (
	skillInjectedMetric      = "codex.skill.injected"
	skillTurnDurationMetric  = "codex.skill.turn.duration_seconds"
	turnTokenUsageMetric     = "codex.turn.token_usage"
	pluginCacheRequestMetric = "codex.plugins.loaded_cache.request"
	mcpDiscoveryMetric       = "codex.mcp.protocol_discovery"
	mcpCachePublishMetric    = "codex.mcp.tools.cache_publish.duration_ms"
	appRefreshMetric         = "codex.apps.refresh.duration_ms"
	serviceNameAttribute     = "service.name"
	serviceVersionAttribute  = "service.version"
)

type productMetricSpec struct {
	family      string
	kind        string
	defaultUnit string
	dimensions  []string
}

var productMetricSpecs = map[string]productMetricSpec{
	"codex.turn.e2e_duration_ms":                          {family: "turn_latency", kind: "histogram", defaultUnit: "ms", dimensions: []string{"model"}},
	"codex.turn.ttft.duration_ms":                         {family: "turn_latency", kind: "histogram", defaultUnit: "ms", dimensions: []string{"model"}},
	"codex.turn.ttfm.duration_ms":                         {family: "turn_latency", kind: "histogram", defaultUnit: "ms", dimensions: []string{"model"}},
	"codex.responses_api_inference_time.duration_ms":      {family: "response_latency", kind: "histogram", defaultUnit: "ms", dimensions: []string{"model"}},
	"codex.responses_api_overhead.duration_ms":            {family: "response_latency", kind: "histogram", defaultUnit: "ms", dimensions: []string{"model"}},
	"codex.responses_api_engine_iapi_ttft.duration_ms":    {family: "response_latency", kind: "histogram", defaultUnit: "ms", dimensions: []string{"model"}},
	"codex.responses_api_engine_service_ttft.duration_ms": {family: "response_latency", kind: "histogram", defaultUnit: "ms", dimensions: []string{"model"}},
	"codex.responses_api_engine_iapi_tbt.duration_ms":     {family: "response_latency", kind: "histogram", defaultUnit: "ms", dimensions: []string{"model"}},
	"codex.responses_api_engine_service_tbt.duration_ms":  {family: "response_latency", kind: "histogram", defaultUnit: "ms", dimensions: []string{"model"}},
	"codex.tool.call.duration_ms":                         {family: "tool_efficiency", kind: "histogram", defaultUnit: "ms", dimensions: []string{"model", "tool", "success", "command_category"}},
	"codex.turn.tool.call":                                {family: "tool_efficiency", kind: "histogram", defaultUnit: "1", dimensions: []string{"model"}},
	"codex.rollout.size_bytes":                            {family: "rollout_efficiency", kind: "histogram", defaultUnit: "By"},
	"codex.rollout_compression.read.io_duration_ms":       {family: "rollout_efficiency", kind: "histogram", defaultUnit: "ms", dimensions: []string{"outcome", "format", "stage", "reason"}},
	"codex.websocket.request.duration_ms":                 {family: "transport_latency", kind: "histogram", defaultUnit: "ms", dimensions: []string{"model", "success"}},
	"codex.websocket.event.duration_ms":                   {family: "transport_latency", kind: "histogram", defaultUnit: "ms", dimensions: []string{"model", "kind", "success"}},
	"codex.tool.call":                                     {family: "tool_efficiency", kind: "sum", defaultUnit: "1", dimensions: []string{"model", "tool", "success", "command_category"}},
	"codex.turn.network_proxy":                            {family: "turn_efficiency", kind: "sum", defaultUnit: "1", dimensions: []string{"model", "active"}},
	"codex.turn.memory":                                   {family: "turn_efficiency", kind: "sum", defaultUnit: "1", dimensions: []string{"model", "config_use_memories", "feature_enabled", "has_citations", "read_allowed"}},
	"codex.rollout_compression.materialize":               {family: "rollout_efficiency", kind: "sum", defaultUnit: "1", dimensions: []string{"outcome"}},
	"codex.rollout_compression.read":                      {family: "rollout_efficiency", kind: "sum", defaultUnit: "1", dimensions: []string{"outcome", "format", "stage", "reason"}},
	"codex.websocket.request":                             {family: "transport_activity", kind: "sum", defaultUnit: "1", dimensions: []string{"model", "success"}},
	"codex.websocket.event":                               {family: "transport_activity", kind: "sum", defaultUnit: "1", dimensions: []string{"model", "kind", "success"}},
	"codex.websocket.continuation":                        {family: "transport_activity", kind: "sum", defaultUnit: "1", dimensions: []string{"model", "mode", "phase", "after_prewarm"}},
}

type metricsPayload struct {
	ResourceMetrics []resourceMetric `json:"resourceMetrics"`
}

type integrationMetricEventInput struct {
	resource                map[string]any
	scopeID, version        string
	metricName, kind, state string
	fields, datapoint       map[string]any
	timeUnixNano            string
	count, index            int
	receivedAt              time.Time
}

type metricNormalizationContext struct {
	resource         map[string]any
	resourceEnvelope map[string]any
	scopeID          string
	scopeEnvelope    map[string]any
	version          string
	receivedAt       time.Time
}

type productMetricEventInput struct {
	context        metricNormalizationContext
	item           otlpMetric
	fields         map[string]any
	datapoint      map[string]any
	timeUnixNano   string
	index          int
	attributes     map[string]any
	metricEvidence map[string]any
}

type productHistogramEventInput struct {
	context metricNormalizationContext
	item    otlpMetric
	spec    productMetricSpec
	point   histogramDataPoint
	index   int
}

type productSumEventInput struct {
	context metricNormalizationContext
	item    otlpMetric
	spec    productMetricSpec
	point   metricDataPoint
	index   int
}

type resourceMetric struct {
	Resource struct {
		Attributes []attribute `json:"attributes"`
	} `json:"resource"`
	ScopeMetrics []scopeMetric `json:"scopeMetrics"`
	raw          map[string]any
}

type scopeMetric struct {
	Scope struct {
		Name       string      `json:"name"`
		Attributes []attribute `json:"attributes"`
	} `json:"scope"`
	Metrics []otlpMetric `json:"metrics"`
	raw     map[string]any
}

type otlpMetric struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Unit        string           `json:"unit"`
	Sum         *metricSum       `json:"sum"`
	Histogram   *metricHistogram `json:"histogram"`
	raw         map[string]any
}

type metricSum struct {
	DataPoints []metricDataPoint `json:"dataPoints"`
}

type metricDataPoint struct {
	Attributes        []attribute `json:"attributes"`
	AsInt             any         `json:"asInt"`
	TimeUnixNano      string      `json:"timeUnixNano"`
	StartTimeUnixNano string      `json:"startTimeUnixNano"`
	raw               map[string]any
}

type metricHistogram struct {
	DataPoints []histogramDataPoint `json:"dataPoints"`
}

type histogramDataPoint struct {
	Attributes     []attribute `json:"attributes"`
	Count          any         `json:"count"`
	Sum            any         `json:"sum"`
	Min            any         `json:"min"`
	Max            any         `json:"max"`
	BucketCounts   []any       `json:"bucketCounts"`
	ExplicitBounds []any       `json:"explicitBounds"`
	TimeUnixNano   string      `json:"timeUnixNano"`
	raw            map[string]any
}

func (value *resourceMetric) UnmarshalJSON(data []byte) error {
	type decoded resourceMetric
	var typed decoded
	raw, err := decodeRawJSONObject(data, &typed)
	if err != nil {
		return err
	}
	*value = resourceMetric(typed)
	value.raw = raw
	return nil
}

func (value *scopeMetric) UnmarshalJSON(data []byte) error {
	type decoded scopeMetric
	var typed decoded
	raw, err := decodeRawJSONObject(data, &typed)
	if err != nil {
		return err
	}
	*value = scopeMetric(typed)
	value.raw = raw
	return nil
}

func (value *otlpMetric) UnmarshalJSON(data []byte) error {
	type decoded otlpMetric
	var typed decoded
	raw, err := decodeRawJSONObject(data, &typed)
	if err != nil {
		return err
	}
	*value = otlpMetric(typed)
	value.raw = raw
	return nil
}

func (value *metricDataPoint) UnmarshalJSON(data []byte) error {
	type decoded metricDataPoint
	var typed decoded
	raw, err := decodeRawJSONObject(data, &typed)
	if err != nil {
		return err
	}
	*value = metricDataPoint(typed)
	value.raw = raw
	return nil
}

func (value *histogramDataPoint) UnmarshalJSON(data []byte) error {
	type decoded histogramDataPoint
	var typed decoded
	raw, err := decodeRawJSONObject(data, &typed)
	if err != nil {
		return err
	}
	*value = histogramDataPoint(typed)
	value.raw = raw
	return nil
}

// NormalizeMetrics maps reviewed Codex OTLP metrics into canonical events.
// codex.skill.injected datapoints with a skill name become explicit skill
// records; codex.skill.turn.duration_seconds histogram datapoints become
// inferred skill-detection coverage only; codex.turn.token_usage histogram
// datapoints become token-usage events. Other metrics are ignored so exporters
// can POST a full metrics batch without inventing unrelated insight rows.
// Resources whose service.name is not an observed Codex metric service are
// skipped. CLI 0.162.1 interactive product metrics are exported by the shared
// codex-app-server process while TUI bookkeeping remains on codex_cli_rs.
func NormalizeMetrics(data []byte, receivedAt time.Time) ([]canonical.Event, error) {
	var payload metricsPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("decode Codex OTLP metrics: %w", err)
	}
	var events []canonical.Event
	for _, resource := range payload.ResourceMetrics {
		extracted, err := skillEventsFromResource(resource, receivedAt)
		if err != nil {
			return nil, err
		}
		events = append(events, extracted...)
	}
	if len(events) == 0 {
		return nil, ErrUnsupportedMetrics
	}
	return normalize.CorrelateEvents(events), nil
}

func skillEventsFromResource(resource resourceMetric, receivedAt time.Time) ([]canonical.Event, error) {
	resourceAttrs := attributes(resource.Resource.Attributes)
	if !isCodexMetricService(resourceAttrs[serviceNameAttribute]) {
		return nil, nil
	}
	version := stringValue(resourceAttrs[serviceVersionAttribute], unavailable)
	var events []canonical.Event
	for _, scope := range resource.ScopeMetrics {
		context := metricNormalizationContext{
			resource:         resourceAttrs,
			resourceEnvelope: rawObjectWithout(resource.raw, "scopeMetrics"),
			scopeID:          metricScopeIdentity(scope),
			scopeEnvelope:    rawObjectWithout(scope.raw, "metrics"),
			version:          version,
			receivedAt:       receivedAt,
		}
		for _, item := range scope.Metrics {
			extracted, err := skillEventsFromMetric(context, item)
			if err != nil {
				return nil, err
			}
			for index := range extracted {
				extracted[index].ProviderExtensions["scope"] = map[string]any{
					"name":       scope.Scope.Name,
					"attributes": rawCodexAttributes(attributes(scope.Scope.Attributes)),
				}
				extracted[index].ProviderExtensions["resource_metric"] = rawObjectWithout(resource.raw, "scopeMetrics")
				extracted[index].ProviderExtensions["scope_metric"] = rawObjectWithout(scope.raw, "metrics")
				extracted[index].ProviderExtensions["metric_envelope"] = rawMetricEnvelope(item.raw)
			}
			events = append(events, extracted...)
		}
	}
	return events, nil
}

func rawMetricEnvelope(raw map[string]any) map[string]any {
	envelope := rawObjectWithout(raw, "sum", "histogram")
	for _, instrument := range []string{"sum", "histogram"} {
		if fields, ok := raw[instrument].(map[string]any); ok {
			envelope[instrument] = rawObjectWithout(fields, "dataPoints")
		}
	}
	return envelope
}

func skillEventsFromMetric(context metricNormalizationContext, item otlpMetric) ([]canonical.Event, error) {
	if events := productEventsFromMetric(context, item); len(events) > 0 {
		return events, nil
	}
	if events := integrationEventsFromMetric(context.resource, context.scopeID, context.version, item, context.receivedAt); len(events) > 0 {
		return events, nil
	}
	if item.Name == skillTurnDurationMetric && item.Histogram != nil {
		return skillTurnEventsFromHistogram(context.resource, context.version, item.Histogram, context.receivedAt), nil
	}
	if item.Name == turnTokenUsageMetric && item.Histogram != nil {
		return tokenUsageEventsFromHistogram(context.resource, context.scopeID, context.version, item.Histogram, context.receivedAt), nil
	}
	if item.Name != skillInjectedMetric || item.Sum == nil {
		return nil, nil
	}
	var events []canonical.Event
	for index, point := range item.Sum.DataPoints {
		event, ok, err := skillInjectedEvent(context.resource, context.version, point, index, context.receivedAt)
		if err != nil {
			return nil, err
		}
		if ok {
			events = append(events, event)
		}
	}
	return events, nil
}

func isCodexMetricService(value any) bool {
	name, ok := value.(string)
	if !ok {
		return false
	}
	return name == "codex_exec" || name == "codex_cli_rs" || name == "codex-app-server"
}

func productEventsFromMetric(context metricNormalizationContext, item otlpMetric) []canonical.Event {
	spec, supported := productMetricSpecs[item.Name]
	if !supported {
		return nil
	}
	if spec.kind == "histogram" && item.Histogram != nil {
		events := make([]canonical.Event, 0, len(item.Histogram.DataPoints))
		for index, point := range item.Histogram.DataPoints {
			input := productHistogramEventInput{context: context, item: item, spec: spec, point: point, index: index}
			if event, ok := productHistogramEvent(input); ok {
				events = append(events, event)
			}
		}
		return events
	}
	if spec.kind == "sum" && item.Sum != nil {
		events := make([]canonical.Event, 0, len(item.Sum.DataPoints))
		for index, point := range item.Sum.DataPoints {
			input := productSumEventInput{context: context, item: item, spec: spec, point: point, index: index}
			if event, ok := productSumEvent(input); ok {
				events = append(events, event)
			}
		}
		return events
	}
	return nil
}

func productHistogramEvent(input productHistogramEventInput) (canonical.Event, bool) {
	count := optionalMetricInt(input.point.Count)
	if count == nil {
		return canonical.Event{}, false
	}
	fields := attributes(input.point.Attributes)
	metric := productMetricEvidence(input.item, input.spec, fields)
	metric["sample_count"] = *count
	attributes := productMetricAttributes(input.item, input.spec, fields)
	attributes["sample_count"] = *count
	if sum := optionalMetricFloat(input.point.Sum); sum != nil {
		metric["sum"] = *sum
		attributes["metric_sum"] = *sum
		if *count > 0 {
			average := *sum / float64(*count)
			attributes["metric_average"] = average
			metric["average"] = average
		}
	}
	if minimum := optionalMetricFloat(input.point.Min); minimum != nil {
		attributes["metric_min"] = *minimum
		metric["min"] = *minimum
	}
	if maximum := optionalMetricFloat(input.point.Max); maximum != nil {
		attributes["metric_max"] = *maximum
		metric["max"] = *maximum
	}
	metric["bucket_counts"] = input.point.BucketCounts
	metric["explicit_bounds"] = input.point.ExplicitBounds
	return productMetricEvent(productMetricEventInput{
		context: input.context, item: input.item, fields: fields,
		datapoint: input.point.raw, timeUnixNano: input.point.TimeUnixNano, index: input.index,
		attributes: attributes, metricEvidence: metric,
	}), true
}

func productSumEvent(input productSumEventInput) (canonical.Event, bool) {
	value := optionalMetricInt(input.point.AsInt)
	if value == nil {
		return canonical.Event{}, false
	}
	fields := attributes(input.point.Attributes)
	metric := productMetricEvidence(input.item, input.spec, fields)
	metric["value"] = *value
	attributes := productMetricAttributes(input.item, input.spec, fields)
	attributes["metric_value"] = *value
	return productMetricEvent(productMetricEventInput{
		context: input.context, item: input.item, fields: fields,
		datapoint: input.point.raw, timeUnixNano: input.point.TimeUnixNano, index: input.index,
		attributes: attributes, metricEvidence: metric,
	}), true
}

func productMetricEvidence(item otlpMetric, spec productMetricSpec, fields map[string]any) map[string]any {
	unit := strings.TrimSpace(item.Unit)
	if unit == "" {
		unit = spec.defaultUnit
	}
	metric := map[string]any{
		"name": item.Name, "family": spec.family, "kind": spec.kind, "unit": unit,
		"description": item.Description, "dimensions": productMetricDimensions(fields, spec.dimensions),
		"provenance": string(canonical.ProvenanceObserved), "scope": "observation",
	}
	if instrument, ok := item.raw[spec.kind].(map[string]any); ok {
		if temporality, found := instrument["aggregationTemporality"]; found {
			metric["aggregation_temporality"] = temporality
		}
		if monotonic, found := instrument["isMonotonic"]; found {
			metric["is_monotonic"] = monotonic
		}
	}
	return metric
}

func productMetricAttributes(item otlpMetric, spec productMetricSpec, fields map[string]any) map[string]any {
	unit := strings.TrimSpace(item.Unit)
	if unit == "" {
		unit = spec.defaultUnit
	}
	result := map[string]any{
		"metric_name": item.Name, "metric_family": spec.family, "metric_kind": spec.kind, "metric_unit": unit,
		"unavailable_fields": []string{"session_lifecycle", "task_outcome", "prompt_content", "response_content", "repository_context", "provider_cost"},
	}
	if model, observed := normalize.ObservedString(fields["model"]); observed {
		result["model"] = model
	}
	return result
}

func productMetricDimensions(fields map[string]any, names []string) map[string]any {
	dimensions := make(map[string]any, len(fields))
	for _, name := range names {
		if value, found := fields[name]; found {
			dimensions[name] = value
		}
	}
	// Unknown datapoint attributes are still dimensions. Retain them in the
	// product projection as well as the raw datapoint so distinct future series
	// cannot be merged merely because this adapter predates their names.
	for name, value := range fields {
		if _, reviewed := dimensions[name]; !reviewed {
			dimensions[name] = value
		}
	}
	return dimensions
}

func productMetricEvent(input productMetricEventInput) canonical.Event {
	occurredAt := metricTime(input.timeUnixNano, input.context.receivedAt)
	identity := strings.Join([]string{
		input.item.Name, input.timeUnixNano, stableJSON(input.context.resourceEnvelope), stableJSON(input.context.scopeEnvelope),
		strconv.Itoa(input.index), stableJSON(input.fields), stableJSON(input.datapoint),
	}, "|")
	eventID := contentID("codex:performance:", []byte(identity))
	extensions := map[string]any{
		"correlation": productMetricCorrelation(eventID, occurredAt), "performance_metric": input.metricEvidence,
		"datapoint": input.datapoint, "resource": rawCodexAttributes(input.context.resource), "metric_attributes": rawCodexAttributes(input.fields),
	}
	event := tokenEvent(eventID, occurredAt, input.context.receivedAt, input.context.version, input.attributes, extensions)
	event.EventType = input.item.Name
	applyCodexEnvironment(&event, input.fields, input.context.resource)
	return event
}

func productMetricCorrelation(eventID string, occurredAt time.Time) map[string]any {
	return map[string]any{
		"dedup_key": eventID, "ordering_key": fmt.Sprintf("%020d:%s", occurredAt.UnixNano(), eventID),
		"task_boundary": map[string]any{"confidence": "unknown", "reason": "Codex 0.162.1 performance metrics have no provider session or task key"},
	}
}

func optionalMetricInt(value any) *int64 {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil || parsed < 0 {
			return nil
		}
		return &parsed
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err != nil || parsed < 0 {
			return nil
		}
		return &parsed
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || typed < 0 || math.Trunc(typed) != typed || typed >= math.MaxInt64 {
			return nil
		}
		parsed := int64(typed)
		return &parsed
	default:
		return nil
	}
}

func optionalMetricFloat(value any) *float64 {
	var parsed float64
	switch typed := value.(type) {
	case json.Number:
		value, err := typed.Float64()
		if err != nil {
			return nil
		}
		parsed = value
	case string:
		value, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return nil
		}
		parsed = value
	case float64:
		parsed = typed
	default:
		return nil
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < 0 {
		return nil
	}
	return &parsed
}

func integrationEventsFromMetric(resource map[string]any, scopeID, version string, item otlpMetric, receivedAt time.Time) []canonical.Event {
	switch item.Name {
	case pluginCacheRequestMetric, mcpDiscoveryMetric:
		return integrationSumEvents(resource, scopeID, version, item, receivedAt)
	case mcpCachePublishMetric, appRefreshMetric:
		return integrationHistogramEvents(resource, scopeID, version, item, receivedAt)
	default:
		return nil
	}
}

func integrationSumEvents(resource map[string]any, scopeID, version string, item otlpMetric, receivedAt time.Time) []canonical.Event {
	if item.Sum == nil {
		return nil
	}
	events := make([]canonical.Event, 0, len(item.Sum.DataPoints))
	for index, point := range item.Sum.DataPoints {
		fields := attributes(point.Attributes)
		kind, state := integrationMetricState(item.Name, fields)
		count, countObserved := positiveMetricCount(point.AsInt)
		if state != "" && countObserved {
			events = append(events, integrationMetricEvent(integrationMetricEventInput{
				resource: resource, scopeID: scopeID, version: version, metricName: item.Name, kind: kind, state: state,
				fields: fields, datapoint: point.raw, timeUnixNano: point.TimeUnixNano, count: count, index: index, receivedAt: receivedAt,
			}))
		}
	}
	return events
}

func integrationHistogramEvents(resource map[string]any, scopeID, version string, item otlpMetric, receivedAt time.Time) []canonical.Event {
	if item.Histogram == nil {
		return nil
	}
	events := make([]canonical.Event, 0, len(item.Histogram.DataPoints))
	for index, point := range item.Histogram.DataPoints {
		count, countObserved := positiveMetricCount(point.Count)
		if !countObserved {
			continue
		}
		fields := attributes(point.Attributes)
		kind, state := integrationMetricState(item.Name, fields)
		if state == "" {
			continue
		}
		events = append(events, integrationMetricEvent(integrationMetricEventInput{
			resource: resource, scopeID: scopeID, version: version, metricName: item.Name, kind: kind, state: state,
			fields: fields, datapoint: point.raw, timeUnixNano: point.TimeUnixNano, count: count, index: index, receivedAt: receivedAt,
		}))
	}
	return events
}

func integrationMetricState(metricName string, fields map[string]any) (string, string) {
	switch metricName {
	case pluginCacheRequestMetric:
		outcome := strings.ToLower(strings.TrimSpace(stringValue(fields["outcome"], "")))
		switch outcome {
		case "hit", "load":
			return "plugin", "cache_" + outcome
		default:
			return "plugin", ""
		}
	case mcpDiscoveryMetric:
		return "mcp", "discovered"
	case mcpCachePublishMetric:
		if strings.ToLower(strings.TrimSpace(stringValue(fields["result"], ""))) == "published" {
			return "mcp", "cache_published"
		}
		return "mcp", ""
	case appRefreshMetric:
		return "app", "refreshed"
	default:
		return "", ""
	}
}

func positiveMetricCount(value any) (int, bool) {
	count := normalize.OptionalTokenCount(value)
	if count == nil || *count <= 0 || uint64(*count) > uint64(^uint(0)>>1) {
		return 0, false
	}
	return int(*count), true
}

func integrationMetricEvent(input integrationMetricEventInput) canonical.Event {
	occurredAt := metricTime(input.timeUnixNano, input.receivedAt)
	identity := strings.Join([]string{input.metricName, input.state, input.timeUnixNano, metricResourceIdentity(input.resource, codexMetricIdentityAttributes(input.resource)), input.scopeID, strconv.Itoa(input.index), strconv.Itoa(input.count), stableJSON(input.fields)}, "|")
	eventID := contentID("codex:integration:", []byte(identity))
	extensions := map[string]any{
		"correlation": integrationCorrelation(eventID, occurredAt),
		"integration_state": map[string]any{
			"kind": input.kind, "state": input.state, "count": input.count,
			"source_metric": input.metricName, "provenance": string(canonical.ProvenanceObserved),
		},
		"metric":    map[string]any{"name": input.metricName, "count": input.count},
		"datapoint": input.datapoint, "resource": rawCodexAttributes(input.resource), "metric_attributes": rawCodexAttributes(input.fields),
	}
	event := tokenEvent(eventID, occurredAt, input.receivedAt, input.version, map[string]any{
		"integration_kind": input.kind, "integration_state": input.state,
		"unavailable_fields": []string{"model", "token_usage", "cache_usage", "tool_calls", "file_operations", "command_execution", "approvals", "prompt_content", "response_content", "repository_context", "task_outcome", "provider_cost", "session_lifecycle"},
	}, extensions)
	event.EventType = input.metricName
	applyCodexEnvironment(&event, input.fields, input.resource)
	return event
}

func integrationCorrelation(eventID string, occurredAt time.Time) map[string]any {
	return map[string]any{
		"dedup_key": eventID, "ordering_key": fmt.Sprintf("%020d:%s", occurredAt.UnixNano(), eventID),
		"task_boundary": map[string]any{
			"confidence": "unknown", "reason": "Codex integration metrics have no reviewed provider session key",
		},
	}
}

func tokenUsageEventsFromHistogram(resourceAttrs map[string]any, scopeID string, version string, histogram *metricHistogram, receivedAt time.Time) []canonical.Event {
	events := make([]canonical.Event, 0, len(histogram.DataPoints))
	for index, point := range histogram.DataPoints {
		if event, ok := tokenUsageEvent(resourceAttrs, scopeID, version, point, index, receivedAt); ok {
			events = append(events, event)
		}
	}
	return events
}

func tokenUsageEvent(resource map[string]any, scopeID string, version string, point histogramDataPoint, index int, receivedAt time.Time) (canonical.Event, bool) {
	fields := attributes(point.Attributes)
	tokenType := strings.TrimSpace(stringValue(fields["token_type"], ""))
	attributeKey, ok := tokenUsageAttribute(tokenType)
	if !ok {
		return canonical.Event{}, false
	}
	tokens := normalize.OptionalTokenCount(point.Sum)
	if tokens == nil {
		return canonical.Event{}, false
	}
	occurredAt := metricTime(point.TimeUnixNano, receivedAt)
	model, modelObserved := normalize.ObservedString(fields["model"])
	identityResource := codexMetricIdentityAttributes(normalize.UnknownFields(resource, serviceNameAttribute, serviceVersionAttribute))
	identityFields := codexMetricIdentityAttributes(normalize.UnknownFields(fields, "model", "token_type"))
	identity := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%d|%d", turnTokenUsageMetric, model, tokenType, point.TimeUnixNano, metricResourceIdentity(resource, identityResource), scopeID, index, *tokens)
	identity += "|" + stableJSON(identityFields)
	eventID := contentID("codex:token:", []byte(identity))

	attributes := map[string]any{
		"unavailable_fields": []string{"tool_calls", "file_operations", "command_execution", "approvals", "prompt_content", "response_content", "repository_context", "task_outcome", "provider_cost", "session_lifecycle"},
		attributeKey:         *tokens,
	}
	if modelObserved {
		attributes["model"] = model
	}
	extensions := map[string]any{
		"correlation": tokenCorrelation(eventID, occurredAt),
		"metric": map[string]any{
			"name":       turnTokenUsageMetric,
			"token_type": tokenType,
			"count":      metricCount(point.Count),
		},
		"datapoint":         point.raw,
		"resource":          rawCodexAttributes(resource),
		"metric_attributes": rawCodexAttributes(fields),
	}
	event := tokenEvent(eventID, occurredAt, receivedAt, version, attributes, extensions)
	applyCodexEnvironment(&event, fields, resource)
	return event, true
}

func metricScopeIdentity(scope scopeMetric) string {
	return stableJSON(map[string]any{
		"name":       scope.Scope.Name,
		"attributes": codexMetricIdentityAttributes(attributes(scope.Scope.Attributes)),
	})
}

func metricResourceIdentity(resource, identityResource map[string]any) string {
	return stableJSON(map[string]any{
		"service.name":    stringValue(resource[serviceNameAttribute], ""),
		"service.version": stringValue(resource[serviceVersionAttribute], ""),
		"resource":        identityResource,
	})
}

func stableJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

// codexMetricIdentityAttributes is the historical allow-list used only to keep
// metric event IDs stable. It is not a retention filter: complete resource and
// datapoint attributes are persisted in provider extensions.
func codexMetricIdentityAttributes(fields map[string]any) map[string]any {
	allowed := map[string]struct{}{
		"app.version":            {},
		"auth_mode":              {},
		"deployment.environment": {},
		"originator":             {},
		"session_source":         {},
		"tmp_mem_enabled":        {},
	}
	identity := make(map[string]any)
	for key, value := range fields {
		if _, ok := allowed[strings.ToLower(strings.TrimSpace(key))]; ok {
			identity[key] = value
		}
	}
	return identity
}

func tokenUsageAttribute(tokenType string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(tokenType)) {
	case "input":
		return "input_token_count", true
	case "output":
		return "output_token_count", true
	case "cached_input":
		return "cached_input_token_count", true
	case "cache_write_input":
		return "cache_write_input_token_count", true
	case "reasoning_output":
		return "reasoning_token_count", true
	case "total":
		return "total_token_count", true
	default:
		return "", false
	}
}

func tokenEvent(eventID string, occurredAt, receivedAt time.Time, version string, attributes, extensions map[string]any) canonical.Event {
	return canonical.Event{
		SchemaVersion:      canonicalSchemaVersion,
		EventID:            eventID,
		EventType:          turnTokenUsageMetric,
		OccurredAt:         occurredAt,
		ReceivedAt:         receivedAt.UTC(),
		Provider:           "openai",
		Tool:               "codex",
		SourceSchema:       sourceSchema,
		SourceVersion:      version,
		ActorID:            unavailable,
		DeviceID:           unavailable,
		SessionID:          eventID,
		PrivacyLevel:       "operational",
		Attributes:         attributes,
		ProviderExtensions: extensions,
	}
}

func tokenCorrelation(eventID string, occurredAt time.Time) map[string]any {
	return map[string]any{
		"dedup_key":    eventID,
		"ordering_key": fmt.Sprintf("%020d:%s", occurredAt.UnixNano(), eventID),
		"task_boundary": map[string]any{
			"confidence": "unknown",
			"reason":     "Codex token metrics have no reviewed task-boundary signal",
		},
	}
}

func skillTurnEventsFromHistogram(resourceAttrs map[string]any, version string, histogram *metricHistogram, receivedAt time.Time) []canonical.Event {
	events := make([]canonical.Event, 0, len(histogram.DataPoints))
	for index, point := range histogram.DataPoints {
		events = append(events, skillTurnEvent(resourceAttrs, version, point, index, receivedAt))
	}
	return events
}

func skillTurnEvent(resource map[string]any, version string, point histogramDataPoint, index int, receivedAt time.Time) canonical.Event {
	fields := attributes(point.Attributes)
	occurredAt := metricTime(point.TimeUnixNano, receivedAt)
	identity := fmt.Sprintf("%s|%s|%s|%d", skillTurnDurationMetric, stringValue(fields["status"], ""), point.TimeUnixNano, index)
	eventID := contentID("codex:skill-turn:", []byte(identity))
	extensions := map[string]any{
		"correlation":     skillCorrelation(eventID, occurredAt),
		"skill_detection": "inferred",
		"metric": map[string]any{
			"name":  skillTurnDurationMetric,
			"count": metricCount(point.Count),
		},
		"datapoint":         point.raw,
		"resource":          rawCodexAttributes(resource),
		"metric_attributes": rawCodexAttributes(fields),
		"skill_turn": map[string]any{
			"outcome":   mapSkillStatus(stringValue(fields["status"], "")),
			"plugin_id": stringValue(fields["plugin_id"], unavailable),
		},
	}
	event := skillEvent(eventID, skillTurnDurationMetric, occurredAt, receivedAt, version, extensions)
	applyCodexEnvironment(&event, fields, resource)
	return event
}

func skillInjectedEvent(resource map[string]any, version string, point metricDataPoint, index int, receivedAt time.Time) (canonical.Event, bool, error) {
	fields := attributes(point.Attributes)
	skillName := strings.TrimSpace(stringValue(fields["skill"], ""))
	if skillName == "" {
		return canonical.Event{}, false, nil
	}
	occurredAt := metricTime(point.TimeUnixNano, receivedAt)
	identity := fmt.Sprintf("%s|%s|%s|%s|%d", skillName, stringValue(fields["status"], ""), stringValue(fields["invoke_type"], ""), point.TimeUnixNano, index)
	eventID := contentID("codex:skill:", []byte(identity))

	skill := skillPayload(fields, skillName)
	extensions := map[string]any{
		"correlation":     skillCorrelation(eventID, occurredAt),
		"skill_detection": "explicit",
		"skill":           skill,
		"metric": map[string]any{
			"name":  skillInjectedMetric,
			"count": metricCount(point.AsInt),
		},
		"datapoint":         point.raw,
		"resource":          rawCodexAttributes(resource),
		"metric_attributes": rawCodexAttributes(fields),
	}

	event := skillEvent(eventID, skillInjectedMetric, occurredAt, receivedAt, version, extensions)
	applyCodexEnvironment(&event, fields, resource)
	return event, true, nil
}

func skillEvent(eventID, eventType string, occurredAt, receivedAt time.Time, version string, extensions map[string]any) canonical.Event {
	return canonical.Event{
		SchemaVersion:      canonicalSchemaVersion,
		EventID:            eventID,
		EventType:          eventType,
		OccurredAt:         occurredAt,
		ReceivedAt:         receivedAt.UTC(),
		Provider:           "openai",
		Tool:               "codex",
		SourceSchema:       sourceSchema,
		SourceVersion:      version,
		ActorID:            unavailable,
		DeviceID:           unavailable,
		SessionID:          eventID,
		PrivacyLevel:       "operational",
		Attributes:         map[string]any{"unavailable_fields": []string{"model", "token_usage", "cache_usage", "tool_calls", "file_operations", "command_execution", "approvals", "prompt_content", "response_content", "repository_context", "task_outcome", "provider_cost", "session_lifecycle"}},
		ProviderExtensions: extensions,
	}
}

func skillCorrelation(eventID string, occurredAt time.Time) map[string]any {
	return map[string]any{
		"dedup_key":    eventID,
		"ordering_key": fmt.Sprintf("%020d:%s", occurredAt.UnixNano(), eventID),
		"task_boundary": map[string]any{
			"confidence": "unknown",
			"reason":     "Codex skill metrics have no reviewed task-boundary signal",
		},
	}
}

func skillPayload(fields map[string]any, skillName string) map[string]any {
	skill := map[string]any{"name": skillName}
	if outcome := mapSkillStatus(stringValue(fields["status"], "")); outcome != "" {
		skill["outcome"] = outcome
	}
	if invoke := strings.TrimSpace(stringValue(fields["invoke_type"], "")); invoke != "" {
		skill["invoke_type"] = invoke
	}
	return skill
}

func mapSkillStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "success":
		return "success"
	case "error", "failed", "failure":
		return "failed"
	case "":
		return ""
	default:
		return status
	}
}

func metricCount(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case string:
		if parsed, err := strconv.Atoi(typed); err == nil {
			return parsed
		}
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return int(parsed)
		}
	}
	return 1
}

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
