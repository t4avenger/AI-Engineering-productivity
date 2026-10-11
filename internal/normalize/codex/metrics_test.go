package codex

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestNormalizeMetricsSkillInjectedGolden(t *testing.T) {
	input := readCodexFixture(t, "codex-0.153.4-skill-injected-metrics.json")
	// Fixture wrapper includes metadata; extract payload for NormalizeMetrics.
	var document map[string]any
	if err := json.Unmarshal(input, &document); err != nil {
		t.Fatalf("decode: %v", err)
	}
	payload, err := json.Marshal(document["payload"])
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	receivedAt := time.Date(2026, 9, 6, 14, 53, 41, 0, time.UTC)
	first, err := NormalizeMetrics(payload, receivedAt)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := NormalizeMetrics(payload, receivedAt)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("normalisation must be deterministic")
	}
	if len(first) != 2 {
		t.Fatalf("event count = %d, want 2 (implicit + explicit)", len(first))
	}
	for _, event := range first {
		if event.ProviderExtensions["skill_detection"] != "explicit" {
			t.Fatalf("skill_detection = %v", event.ProviderExtensions["skill_detection"])
		}
		skill := event.ProviderExtensions["skill"].(map[string]any)
		if skill["name"] != "tiq-probe" || skill["outcome"] != "success" {
			t.Fatalf("skill = %#v", skill)
		}
	}
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		writeCodexGolden(t, "codex-0.153.4-skill-injected-metrics.events.json", first)
	}
	assertCodexGolden(t, "codex-0.153.4-skill-injected-metrics.events.json", first)
}

func TestNormalizeMetricsTokenUsageGolden(t *testing.T) {
	input := readCodexFixture(t, "codex-0.153.4-turn-token-usage-metrics.json")
	var document map[string]any
	if err := json.Unmarshal(input, &document); err != nil {
		t.Fatalf("decode: %v", err)
	}
	payload, err := json.Marshal(document["payload"])
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	receivedAt := time.Date(2026, 9, 10, 20, 9, 21, 0, time.UTC)
	first, err := NormalizeMetrics(payload, receivedAt)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := NormalizeMetrics(payload, receivedAt)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("normalisation must be deterministic")
	}
	assertTokenUsageEvents(t, first)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		writeCodexGolden(t, "codex-0.153.4-turn-token-usage-metrics.events.json", first)
	}
	assertCodexGolden(t, "codex-0.153.4-turn-token-usage-metrics.events.json", first)
}

func TestNormalizeMetricsPerformanceGolden(t *testing.T) {
	payload := readCodexFixturePayload(t, "codex-0.162.1-performance-metrics.json")
	receivedAt := time.Date(2026, 10, 11, 11, 11, 0, 0, time.UTC)
	first, err := NormalizeMetrics(payload, receivedAt)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := NormalizeMetrics(payload, receivedAt)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("performance metric normalisation must be deterministic")
	}
	if len(first) != 10 {
		t.Fatalf("event count = %d, want 10", len(first))
	}

	byType := assertPerformanceMetricEvidence(t, first)
	assertPerformanceMetricProjection(t, byType)

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		writeCodexGolden(t, "codex-0.162.1-performance-metrics.events.json", first)
	}
	assertCodexGolden(t, "codex-0.162.1-performance-metrics.events.json", first)
}

func assertPerformanceMetricEvidence(t *testing.T, events []canonical.Event) map[string]canonical.Event {
	t.Helper()
	byType := make(map[string]canonical.Event, len(events))
	for _, event := range events {
		byType[event.EventType] = event
		if event.SourceVersion != "0.162.1" || event.SessionID != event.EventID {
			t.Fatalf("performance event must remain versioned observation: %#v", event)
		}
		metric := event.ProviderExtensions["performance_metric"].(map[string]any)
		if metric["scope"] != "observation" || metric["provenance"] != "observed" {
			t.Fatalf("performance evidence = %#v", metric)
		}
	}
	return byType
}

func assertPerformanceMetricProjection(t *testing.T, byType map[string]canonical.Event) {
	t.Helper()
	turn := byType["codex.turn.e2e_duration_ms"]
	if turn.Attributes["sample_count"] != int64(2) || turn.Attributes["metric_average"] != float64(1800) {
		t.Fatalf("turn histogram projection = %#v", turn.Attributes)
	}
	metric := turn.ProviderExtensions["performance_metric"].(map[string]any)
	if !reflect.DeepEqual(metric["bucket_counts"], []any{"0", "1", "1"}) || !reflect.DeepEqual(metric["explicit_bounds"], []any{float64(1000), float64(2000)}) {
		t.Fatalf("histogram structure not retained: %#v", metric)
	}
	dimensions := metric["dimensions"].(map[string]any)
	if dimensions["originator"] != "codex_cli_rs" || dimensions["session_source"] != "cli" {
		t.Fatalf("histogram dimensions not retained: %#v", dimensions)
	}
	futureDimensions := byType["codex.turn.ttft.duration_ms"].ProviderExtensions["performance_metric"].(map[string]any)["dimensions"].(map[string]any)
	if futureDimensions["future.dimension"] != "retained-raw" {
		t.Fatalf("unknown dimension not retained: %#v", futureDimensions)
	}
	if byType["codex.turn.network_proxy"].Attributes["metric_value"] != int64(2) {
		t.Fatalf("sum projection = %#v", byType["codex.turn.network_proxy"].Attributes)
	}
	transport := byType["codex.websocket.request"]
	transportMetric := transport.ProviderExtensions["performance_metric"].(map[string]any)
	if transport.Attributes["metric_value"] != int64(4) || transportMetric["aggregation_temporality"] != json.Number("2") {
		t.Fatalf("transport activity projection = %#v", transportMetric)
	}
	transportDimensions := transportMetric["dimensions"].(map[string]any)
	if transportDimensions["model"] != "gpt-6.1-sol-synthetic" || transportDimensions["success"] != true {
		t.Fatalf("transport activity dimensions = %#v", transportDimensions)
	}
	resource := turn.ProviderExtensions["resource"].(map[string]any)
	if resource["future.resource"] != "retained-raw" {
		t.Fatalf("unknown resource field missing: %#v", resource)
	}
}

func TestNormalizeMetricsPerformanceRejectsMalformedMeasures(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}}]},"scopeMetrics":[{"metrics":[{"name":"codex.turn.e2e_duration_ms","histogram":{"dataPoints":[{"sum":"12"},{"count":"invalid","sum":"12"}]}},{"name":"codex.turn.memory","sum":{"dataPoints":[{"asInt":"-1"},{"asInt":"invalid"}]}}]}]}]}`)
	_, err := NormalizeMetrics(payload, time.Unix(1, 0))
	if !errors.Is(err, ErrUnsupportedMetrics) {
		t.Fatalf("got %v, want ErrUnsupportedMetrics", err)
	}
}

func TestNormalizeMetricsPerformancePreservesZeroCountAndMissingSum(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}}]},"scopeMetrics":[{"metrics":[{"name":"codex.turn.e2e_duration_ms","histogram":{"dataPoints":[{"count":"0","sum":"12","bucketCounts":["0"]},{"count":"2","bucketCounts":["2"]}]}}]}]}]}`)
	events, err := NormalizeMetrics(payload, time.Unix(1, 0))
	if err != nil || len(events) != 2 {
		t.Fatalf("events = %#v, err = %v", events, err)
	}
	zeroCount := performanceEventBySampleCount(t, events, 0)
	zeroMetric := zeroCount.ProviderExtensions["performance_metric"].(map[string]any)
	if zeroCount.Attributes["sample_count"] != int64(0) || zeroCount.Attributes["metric_sum"] != float64(12) || zeroMetric["sum"] != float64(12) {
		t.Fatalf("zero-count total not preserved: %#v", zeroCount)
	}
	if _, present := zeroCount.Attributes["metric_average"]; present {
		t.Fatalf("zero-count average must be absent: %#v", zeroCount.Attributes)
	}

	missingSum := performanceEventBySampleCount(t, events, 2)
	missingMetric := missingSum.ProviderExtensions["performance_metric"].(map[string]any)
	if missingSum.Attributes["sample_count"] != int64(2) {
		t.Fatalf("missing-sum count not preserved: %#v", missingSum.Attributes)
	}
	for _, key := range []string{"metric_sum", "metric_average"} {
		if _, present := missingSum.Attributes[key]; present {
			t.Fatalf("missing sum fabricated %s: %#v", key, missingSum.Attributes)
		}
	}
	if _, present := missingMetric["sum"]; present {
		t.Fatalf("missing sum fabricated in evidence: %#v", missingMetric)
	}
}

func performanceEventBySampleCount(t *testing.T, events []canonical.Event, count int64) canonical.Event {
	t.Helper()
	for _, event := range events {
		if event.Attributes["sample_count"] == count {
			return event
		}
	}
	t.Fatalf("sample count %d not found in %#v", count, events)
	return canonical.Event{}
}

func TestNormalizeMetricsPerformanceIdentityIncludesCompleteEnvelopes(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}},{"key":"service.instance.id","value":{"stringValue":"one"}}]},"scopeMetrics":[{"scope":{"name":"codex","version":"one"},"metrics":[{"name":"codex.turn.memory","sum":{"dataPoints":[{"asInt":"1","timeUnixNano":"1791717000000000000"}]}}]},{"scope":{"name":"codex","version":"two"},"metrics":[{"name":"codex.turn.memory","sum":{"dataPoints":[{"asInt":"1","timeUnixNano":"1791717000000000000"}]}}]}]},{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}},{"key":"service.instance.id","value":{"stringValue":"two"}}]},"scopeMetrics":[{"scope":{"name":"codex","version":"one"},"metrics":[{"name":"codex.turn.memory","sum":{"dataPoints":[{"asInt":"1","timeUnixNano":"1791717000000000000"}]}}]}]}]}`)
	events, err := NormalizeMetrics(payload, time.Unix(1, 0))
	if err != nil || len(events) != 3 {
		t.Fatalf("events = %#v, err = %v", events, err)
	}
	ids := map[string]struct{}{}
	for _, event := range events {
		ids[event.EventID] = struct{}{}
	}
	if len(ids) != len(events) {
		t.Fatalf("complete resource/scope envelopes must distinguish event IDs: %#v", events)
	}
}

func TestNormalizeMetricsProjectsDistinctIntegrationStates(t *testing.T) {
	events, err := NormalizeMetrics(readCodexFixturePayload(t, "codex-0.160.0-integration-states-metrics.json"), time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"plugin/cache_hit":    "codex.plugins.loaded_cache.request",
		"plugin/cache_load":   "codex.plugins.loaded_cache.request",
		"mcp/discovered":      "codex.mcp.protocol_discovery",
		"mcp/cache_published": "codex.mcp.tools.cache_publish.duration_ms",
		"app/refreshed":       "codex.apps.refresh.duration_ms",
	}
	if len(events) != len(want) {
		t.Fatalf("integration events = %d, want %d: %#v", len(events), len(want), events)
	}
	for _, event := range events {
		key := event.Attributes["integration_kind"].(string) + "/" + event.Attributes["integration_state"].(string)
		if event.EventType != want[key] {
			t.Fatalf("integration state %q = %#v", key, event)
		}
		if event.ProviderExtensions["integration_state"].(map[string]any)["provenance"] != "observed" {
			t.Fatalf("integration provenance = %#v", event.ProviderExtensions)
		}
	}
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		writeCodexGolden(t, "codex-0.160.0-integration-states-metrics.events.json", events)
	}
	assertCodexGolden(t, "codex-0.160.0-integration-states-metrics.events.json", events)
}

func TestIntegrationMetricsRequirePositiveIntegerCount(t *testing.T) {
	for name, count := range map[string]any{
		"absent": nil, "malformed": "invalid", "fractional": 1.5, "zero": "0", "negative": "-1",
	} {
		t.Run(name, func(t *testing.T) {
			resource := map[string]any{serviceNameAttribute: "codex_exec"}
			metrics := []otlpMetric{
				{Name: mcpDiscoveryMetric, Sum: &metricSum{DataPoints: []metricDataPoint{{AsInt: count}}}},
				{Name: appRefreshMetric, Histogram: &metricHistogram{DataPoints: []histogramDataPoint{{Count: count}}}},
			}
			for _, metric := range metrics {
				if events := integrationEventsFromMetric(resource, "", "0.160.0", metric, time.Unix(1, 0)); len(events) != 0 {
					t.Fatalf("%s with count %#v produced %#v", metric.Name, count, events)
				}
			}
		})
	}
}

func TestIntegrationMetricStateUsesOnlyReviewedProviderValues(t *testing.T) {
	for _, test := range []struct {
		name, metric, key, value, wantKind, wantState string
	}{
		{name: "cache hit", metric: pluginCacheRequestMetric, key: "outcome", value: "hit", wantKind: "plugin", wantState: "cache_hit"},
		{name: "cache load", metric: pluginCacheRequestMetric, key: "outcome", value: "load", wantKind: "plugin", wantState: "cache_load"},
		{name: "unknown cache outcome", metric: pluginCacheRequestMetric, key: "outcome", value: "error", wantKind: "plugin"},
		{name: "published cache", metric: mcpCachePublishMetric, key: "result", value: "published", wantKind: "mcp", wantState: "cache_published"},
		{name: "missing publish result", metric: mcpCachePublishMetric, wantKind: "mcp"},
		{name: "unknown publish result", metric: mcpCachePublishMetric, key: "result", value: "error", wantKind: "mcp"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fields := map[string]any{}
			if test.key != "" {
				fields[test.key] = test.value
			}
			kind, state := integrationMetricState(test.metric, fields)
			if kind != test.wantKind || state != test.wantState {
				t.Fatalf("integrationMetricState() = %q, %q; want %q, %q", kind, state, test.wantKind, test.wantState)
			}
		})
	}
}

func assertTokenUsageEvents(t *testing.T, events []canonical.Event) {
	t.Helper()
	if len(events) != 6 {
		t.Fatalf("event count = %d, want 6 token categories", len(events))
	}
	for key, want := range map[string]int64{
		"input_token_count":             1200,
		"cached_input_token_count":      300,
		"cache_write_input_token_count": 75,
		"output_token_count":            144,
		"reasoning_token_count":         55,
		"total_token_count":             1774,
	} {
		assertTokenUsageAttribute(t, events, key, want)
	}
}

func assertTokenUsageAttribute(t *testing.T, events []canonical.Event, key string, want int64) {
	t.Helper()
	for _, event := range events {
		if event.EventType != turnTokenUsageMetric {
			t.Fatalf("event type = %q", event.EventType)
		}
		if event.Attributes["model"] != "gpt-5-codex-synthetic" {
			t.Fatalf("model = %#v", event.Attributes["model"])
		}
		if got, ok := event.Attributes[key]; ok {
			if got != want {
				t.Fatalf("%s = %#v, want %d", key, got, want)
			}
			return
		}
	}
	t.Fatalf("missing token attribute %s in %#v", key, events)
}

func TestNormalizeMetricsTokenUsageDoesNotFabricateZero(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}}]},"scopeMetrics":[{"metrics":[{"name":"codex.turn.token_usage","histogram":{"dataPoints":[{"attributes":[{"key":"token_type","value":{"stringValue":"input"}}],"count":"1","sum":"not-a-number","timeUnixNano":"1789042160000000000"},{"attributes":[{"key":"token_type","value":{"stringValue":"output"}}],"count":"1","timeUnixNano":"1789042160000000001"}]}}]}]}]}`)
	_, err := NormalizeMetrics(payload, time.Date(2026, 9, 10, 20, 9, 21, 0, time.UTC))
	if !errors.Is(err, ErrUnsupportedMetrics) {
		t.Fatalf("got %v, want ErrUnsupportedMetrics when no valid token counts exist", err)
	}
}

func TestNormalizeMetricsUnknownInstrumentIsIgnored(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}}]},"scopeMetrics":[{"metrics":[{"name":"codex.internal.future_metric","sum":{"dataPoints":[{"attributes":[{"key":"state","value":{"stringValue":"ok"}}],"asInt":"1"}]}}]}]}]}`)
	_, err := NormalizeMetrics(payload, time.Date(2026, 9, 10, 20, 9, 21, 0, time.UTC))
	if !errors.Is(err, ErrUnsupportedMetrics) {
		t.Fatalf("got %v, want ErrUnsupportedMetrics for intentionally ignored metrics", err)
	}
}

func TestNormalizeMetricsTokenUsageRetainsAllAttributes(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}},{"key":"api_key","value":{"stringValue":"tiq-canary-resource-key"}},{"key":"user.email","value":{"stringValue":"resource@example.test"}},{"key":"deployment.environment","value":{"stringValue":"telemetryiq-synthetic"}}]},"scopeMetrics":[{"metrics":[{"name":"codex.turn.token_usage","histogram":{"dataPoints":[{"attributes":[{"key":"token_type","value":{"stringValue":"input"}},{"key":"api_key","value":{"stringValue":"tiq-canary-api-key"}},{"key":"authorization","value":{"stringValue":"Bearer tiq-canary-token"}},{"key":"user.email","value":{"stringValue":"synthetic@example.test"}},{"key":"originator","value":{"stringValue":"codex_exec"}}],"count":"1","sum":"12","timeUnixNano":"1789042160000000000"}]}}]}]}]}`)
	events, err := NormalizeMetrics(payload, time.Date(2026, 9, 10, 20, 9, 21, 0, time.UTC))
	if err != nil {
		t.Fatalf("NormalizeMetrics: %v", err)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	for _, retained := range []string{"tiq-canary-resource-key", "resource@example.test", "tiq-canary-api-key", "Bearer tiq-canary-token", "synthetic@example.test"} {
		if !strings.Contains(string(encoded), retained) {
			t.Fatalf("raw value %q missing from %s", retained, encoded)
		}
	}
	metricAttributes := events[0].ProviderExtensions["metric_attributes"].(map[string]any)
	if metricAttributes["originator"] != "codex_exec" {
		t.Fatalf("safe metric attributes not preserved: %#v", metricAttributes)
	}
	resource := events[0].ProviderExtensions["resource"].(map[string]any)
	if resource["deployment.environment"] != "telemetryiq-synthetic" {
		t.Fatalf("safe resource attributes not preserved: %#v", resource)
	}
}

func TestNormalizeMetricsRetainsRawEnvelopesAndDatapoint(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"schemaUrl":"resource-schema-canary","resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}}],"droppedAttributesCount":7},"scopeMetrics":[{"schemaUrl":"scope-schema-canary","scope":{"name":"codex","version":"scope-version-canary"},"metrics":[{"name":"codex.skill.injected","description":"metric-description-canary","unit":"1","sum":{"aggregationTemporality":1,"isMonotonic":true,"dataPoints":[{"attributes":[{"key":"skill","value":{"stringValue":"synthetic-skill"}}],"asInt":"1","flags":1,"exemplars":[{"asInt":"42"}]}]}}]}]}]}`)
	events, err := NormalizeMetrics(payload, time.Unix(1, 0))
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	encoded, err := json.Marshal(events[0].ProviderExtensions)
	if err != nil {
		t.Fatal(err)
	}
	for _, retained := range []string{
		"resource-schema-canary", "scope-schema-canary", "scope-version-canary",
		"metric-description-canary", "aggregationTemporality", "isMonotonic", "exemplars", "flags",
	} {
		if !strings.Contains(string(encoded), retained) {
			t.Fatalf("raw envelope value %q missing from %s", retained, encoded)
		}
	}
}

func TestNormalizeMetricsTokenUsageIdentityIncludesSeries(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}},{"key":"deployment.environment","value":{"stringValue":"one"}}]},"scopeMetrics":[{"metrics":[{"name":"codex.turn.token_usage","histogram":{"dataPoints":[{"attributes":[{"key":"token_type","value":{"stringValue":"input"}},{"key":"originator","value":{"stringValue":"codex_exec"}}],"count":"1","sum":"12","timeUnixNano":"1789042160000000000"}]}}]}]},{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}},{"key":"deployment.environment","value":{"stringValue":"two"}}]},"scopeMetrics":[{"metrics":[{"name":"codex.turn.token_usage","histogram":{"dataPoints":[{"attributes":[{"key":"token_type","value":{"stringValue":"input"}},{"key":"originator","value":{"stringValue":"codex_exec"}}],"count":"1","sum":"12","timeUnixNano":"1789042160000000000"}]}}]}]}]}`)
	events, err := NormalizeMetrics(payload, time.Date(2026, 9, 10, 20, 9, 21, 0, time.UTC))
	if err != nil {
		t.Fatalf("NormalizeMetrics: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2 distinct resource series", len(events))
	}
	if events[0].EventID == events[1].EventID {
		t.Fatalf("event IDs must include resource/series identity: %#v", events)
	}
}

func TestNormalizeMetricsRawRetentionDoesNotChangeHistoricalID(t *testing.T) {
	payload := func(extra string) []byte {
		return []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}}` + extra + `]},"scopeMetrics":[{"metrics":[{"name":"codex.turn.token_usage","histogram":{"dataPoints":[{"attributes":[{"key":"token_type","value":{"stringValue":"input"}}` + extra + `],"count":"1","sum":"12","timeUnixNano":"1789042160000000000"}]}}]}]}]}`)
	}
	base, err := NormalizeMetrics(payload(""), time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := NormalizeMetrics(payload(`,{"key":"api_key","value":{"stringValue":"synthetic-secret"}}`), time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if base[0].EventID != raw[0].EventID {
		t.Fatalf("raw-only attribute changed historical event ID: %q != %q", base[0].EventID, raw[0].EventID)
	}
	encoded, _ := json.Marshal(raw)
	if !strings.Contains(string(encoded), "synthetic-secret") {
		t.Fatalf("raw metric attribute missing: %s", encoded)
	}
}

func FuzzNormalizeMetrics(f *testing.F) {
	f.Add([]byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}}]},"scopeMetrics":[{"metrics":[{"name":"codex.turn.token_usage","histogram":{"dataPoints":[{"attributes":[{"key":"token_type","value":{"stringValue":"input"}}],"count":"1","sum":"12"}]}}]}]}]}`))
	f.Add([]byte("not json"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = NormalizeMetrics(data, time.Unix(1, 0))
	})
}

func TestNormalizeMetricsSkillTurnDurationIsInferred(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}},{"key":"service.version","value":{"stringValue":"0.153.4"}},{"key":"env","value":{"stringValue":"telemetryiq-synthetic"}}]},"scopeMetrics":[{"metrics":[{"name":"codex.skill.turn.duration_seconds","histogram":{"dataPoints":[{"attributes":[{"key":"status","value":{"stringValue":"completed"}},{"key":"plugin_id","value":{"stringValue":"unattributed"}}],"count":"1","timeUnixNano":"1788709355426961808"}]}}]}]}]}`)
	events, err := NormalizeMetrics(payload, time.Date(2026, 9, 6, 15, 42, 35, 0, time.UTC))
	if err != nil {
		t.Fatalf("NormalizeMetrics: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	event := events[0]
	if event.EventType != skillTurnDurationMetric {
		t.Fatalf("event type = %q", event.EventType)
	}
	if event.ProviderExtensions["skill_detection"] != "inferred" {
		t.Fatalf("skill_detection = %v", event.ProviderExtensions["skill_detection"])
	}
	if _, ok := event.ProviderExtensions["skill"]; ok {
		t.Fatalf("inferred skill turn must not fabricate a named skill: %#v", event.ProviderExtensions["skill"])
	}
}

func TestNormalizeMetricsRejectsNonCodexService(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"other"}}]},"scopeMetrics":[{"metrics":[{"name":"codex.skill.injected","sum":{"dataPoints":[{"attributes":[{"key":"skill","value":{"stringValue":"x"}}],"asInt":1}]}}]}]}]}`)
	_, err := NormalizeMetrics(payload, time.Now().UTC())
	if !errors.Is(err, ErrUnsupportedMetrics) {
		t.Fatalf("got %v, want ErrUnsupportedMetrics", err)
	}
}

func readCodexFixture(t *testing.T, name string) []byte {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "fixtures", "codex", "observed-sanitised", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

func readCodexFixturePayload(t *testing.T, name string) []byte {
	t.Helper()
	raw := readCodexFixture(t, name)
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(document["payload"])
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func writeCodexGolden(t *testing.T, name string, value any) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "fixtures", "codex", "expected", name)
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write golden: %v", err)
	}
}

func assertCodexGolden(t *testing.T, name string, value any) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "fixtures", "codex", "expected", name)
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	got, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got = append(got, '\n')
	if string(got) != string(want) {
		t.Fatalf("golden mismatch for %s\ngot:\n%s\nwant:\n%s", name, got, want)
	}
}
