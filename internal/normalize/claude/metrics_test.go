package claude

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// metricsFixturePayload extracts the OTLP payload from the fixture wrapper so it
// is replayed exactly as the /v1/metrics route would hand it to the adapter.
func metricsFixturePayload(t *testing.T, name string) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(readFixture(t, name), &document); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	payload, err := json.Marshal(document["payload"])
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return payload
}

func TestNormalizeMetricsTokenUsageGolden(t *testing.T) {
	payload := metricsFixturePayload(t, "claude-code-2.1.268-token-usage-metrics.json")
	receivedAt := time.Date(2026, 9, 11, 8, 50, 31, 0, time.UTC)

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
	assertClaudeTokenUsageEvents(t, first)
	assertMatchesGolden(t, "claude-code-2.1.268-token-usage-metrics.events.json", first)
}

func assertClaudeTokenUsageEvents(t *testing.T, events []canonical.Event) {
	t.Helper()
	if len(events) != 4 {
		t.Fatalf("event count = %d, want 4 token categories", len(events))
	}
	for key, want := range map[string]int64{
		"input_token_count":             10,
		"output_token_count":            42,
		"cached_input_token_count":      0,
		"cache_write_input_token_count": 18856,
	} {
		assertClaudeTokenAttribute(t, events, key, want)
	}
	for _, event := range events {
		if event.EventType != tokenUsageMetric {
			t.Fatalf("event type = %q", event.EventType)
		}
		if event.Provider != provider || event.Tool != tool {
			t.Fatalf("provider/tool = %q/%q", event.Provider, event.Tool)
		}
		if event.Attributes["model"] != "claude-haiku-4-5-20251001" {
			t.Fatalf("model = %#v", event.Attributes["model"])
		}
		if event.SessionID != "claude-code:00000000-0000-4000-8000-000000000001" {
			t.Fatalf("session id = %q, want raw provider-native session id", event.SessionID)
		}
	}
}

func assertClaudeTokenAttribute(t *testing.T, events []canonical.Event, key string, want int64) {
	t.Helper()
	for _, event := range events {
		got, ok := event.Attributes[key]
		if !ok {
			continue
		}
		if got != want {
			t.Fatalf("%s = %#v, want %d", key, got, want)
		}
		return
	}
	t.Fatalf("missing token attribute %s in %#v", key, events)
}

func TestNormalizeMetricsCostAttributionGolden(t *testing.T) {
	payload := metricsFixturePayload(t, "claude-code-2.1.268-cost-attribution-metrics.json")
	receivedAt := time.Date(2026, 9, 16, 8, 50, 31, 0, time.UTC)

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
	assertClaudeCostAttributionEvents(t, first)
	assertMatchesGolden(t, "claude-code-2.1.268-cost-attribution-metrics.events.json", first)
}

// assertClaudeCostAttributionEvents proves the M10 outcome: the attribution dims
// survive into provider_extensions.metric_attributes on both token.usage and
// cost.usage events, the provider-reported USD cost is carried under
// provider_cost, and — because cost.usage is captured on the same resource — the
// token events no longer declare provider_cost unavailable.
func assertClaudeCostAttributionEvents(t *testing.T, events []canonical.Event) {
	t.Helper()
	var tokenEvents, costEvents int
	for _, event := range events {
		switch event.EventType {
		case tokenUsageMetric:
			tokenEvents++
			assertProviderCostAvailable(t, event)
		case costUsageMetric:
			costEvents++
			assertPositiveProviderCost(t, event)
		default:
			t.Fatalf("unexpected event type %q", event.EventType)
		}
		assertNoIdentityLeak(t, event.ProviderExtensions["metric_attributes"].(map[string]any))
	}
	if tokenEvents != 2 || costEvents != 2 {
		t.Fatalf("event mix = %d token / %d cost, want 2/2", tokenEvents, costEvents)
	}
	assertAttribution(t, events, "code-reviewer", map[string]any{"skill.name": "code-reviewer", "agent.name": "general-purpose"})
	assertAttribution(t, events, "review-suite", map[string]any{"mcp_server.name": "github", "mcp_tool.name": "create_issue", "plugin.name": "review-suite", "marketplace.name": "acme-marketplace"})
}

// assertProviderCostAvailable proves a token.usage event does not mark
// provider_cost unavailable when a sibling cost.usage datapoint is captured on
// the same resource.
func assertProviderCostAvailable(t *testing.T, event canonical.Event) {
	t.Helper()
	for _, field := range event.Attributes["unavailable_fields"].([]string) {
		if field == "provider_cost" {
			t.Fatalf("token event must not mark provider_cost unavailable when cost.usage is captured: %#v", event.Attributes)
		}
	}
}

// assertPositiveProviderCost proves a cost.usage event carries the provider USD
// amount under provider_cost.
func assertPositiveProviderCost(t *testing.T, event canonical.Event) {
	t.Helper()
	cost, ok := event.Attributes["provider_cost"].(float64)
	if !ok || cost <= 0 {
		t.Fatalf("cost event must carry a positive provider_cost, got %#v", event.Attributes["provider_cost"])
	}
}

// assertNoIdentityLeak proves the allow-list dropped identity-bearing attributes
// from provider_extensions.metric_attributes.
func assertNoIdentityLeak(t *testing.T, attrs map[string]any) {
	t.Helper()
	for _, blocked := range []string{"user.id", "user.email", "session.id"} {
		if _, leaked := attrs[blocked]; leaked {
			t.Fatalf("identity attribute %q leaked into metric_attributes: %#v", blocked, attrs)
		}
	}
}

// assertAttribution finds an event whose metric_attributes carry the marker
// value and asserts every expected attribution dim is present and equal.
func assertAttribution(t *testing.T, events []canonical.Event, marker string, want map[string]any) {
	t.Helper()
	for _, event := range events {
		attrs := event.ProviderExtensions["metric_attributes"].(map[string]any)
		matched := false
		for _, value := range attrs {
			if value == marker {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		for key, value := range want {
			if attrs[key] != value {
				t.Fatalf("attribution dim %q = %#v, want %#v (event %s)", key, attrs[key], value, event.EventType)
			}
		}
		return
	}
	t.Fatalf("no event carried attribution marker %q", marker)
}

func TestNormalizeMetricsCodeOutputGolden(t *testing.T) {
	payload := metricsFixturePayload(t, "claude-code-2.1.268-code-output-metrics.json")
	receivedAt := time.Date(2026, 9, 16, 8, 50, 31, 0, time.UTC)

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
	assertClaudeCodeOutputEvents(t, first)
	assertMatchesGolden(t, "claude-code-2.1.268-code-output-metrics.events.json", first)
}

// assertClaudeCodeOutputEvents proves the M11 outcome: the three code-output
// counters land on their canonical keys (lines_added/lines_removed on
// lines_of_code, plus commit/pull_request counts), model rides only lines_of_code,
// commit/pull_request carry no model, and the lines_of_code attribution dims
// survive into provider_extensions.metric_attributes so produced code is
// attributable to the skill/sub-agent that produced it — with no identity leak.
func assertClaudeCodeOutputEvents(t *testing.T, events []canonical.Event) {
	t.Helper()
	counts := map[string]any{}
	for _, event := range events {
		assertNoIdentityLeak(t, event.ProviderExtensions["metric_attributes"].(map[string]any))
		collectCodeOutputCount(t, event, counts)
	}
	assertCountsEqual(t, counts, map[string]int64{
		"lines_added_count":   128,
		"lines_removed_count": 12,
		"commit_count":        2,
		"pull_request_count":  1,
	})
	assertAttribution(t, events, "code-reviewer", map[string]any{"skill.name": "code-reviewer", "agent.name": "general-purpose"})
}

// collectCodeOutputCount records the canonical count(s) carried by one code-output
// event and enforces the model-promotion rule: lines_of_code promotes the observed
// model, commit/pull_request carry standard attrs only.
func collectCodeOutputCount(t *testing.T, event canonical.Event, counts map[string]any) {
	t.Helper()
	switch event.EventType {
	case linesOfCodeMetric:
		if event.Attributes["model"] != "claude-sonnet-5" {
			t.Fatalf("lines_of_code event must promote model, got %#v", event.Attributes["model"])
		}
		for _, key := range []string{"lines_added_count", "lines_removed_count"} {
			if value, ok := event.Attributes[key]; ok {
				counts[key] = value
			}
		}
	case commitMetric:
		assertNoModelPromotion(t, event)
		counts["commit_count"] = event.Attributes["commit_count"]
	case pullRequestMetric:
		assertNoModelPromotion(t, event)
		counts["pull_request_count"] = event.Attributes["pull_request_count"]
	default:
		t.Fatalf("unexpected event type %q", event.EventType)
	}
}

// assertNoModelPromotion proves a commit/pull_request event carries standard
// attrs only, never a promoted model.
func assertNoModelPromotion(t *testing.T, event canonical.Event) {
	t.Helper()
	if _, hasModel := event.Attributes["model"]; hasModel {
		t.Fatalf("%s must not promote model (standard attrs only): %#v", event.EventType, event.Attributes)
	}
}

// assertCountsEqual proves every expected canonical count landed with its exact
// value.
func assertCountsEqual(t *testing.T, counts map[string]any, want map[string]int64) {
	t.Helper()
	for key, value := range want {
		if counts[key] != value {
			t.Fatalf("%s = %#v, want %d", key, counts[key], value)
		}
	}
}

// TestNormalizeMetricsMalformedCodeOutputValueIsError proves the routing contract
// for the M11 counters: a lines_of_code/commit/pull_request datapoint with an
// unparseable count is a real error, NOT the skip sentinel, so the route does not
// silently 202-accept and drop supported Claude code-output data.
func TestNormalizeMetricsMalformedCodeOutputValueIsError(t *testing.T) {
	cases := map[string]string{
		"lines_of_code": `{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.lines_of_code.count","sum":{"dataPoints":[{"attributes":[{"key":"type","value":{"stringValue":"added"}}],"asDouble":-4,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`,
		"commit":        `{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.commit.count","sum":{"dataPoints":[{"attributes":[],"asDouble":-1,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`,
		"pull_request":  `{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.pull_request.count","sum":{"dataPoints":[{"attributes":[],"asDouble":1.5,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NormalizeMetrics([]byte(payload), time.Now().UTC())
			if err == nil || errors.Is(err, ErrUnsupportedMetrics) {
				t.Fatalf("got %v, want a hard normalisation error for an unparseable %s count", err, name)
			}
		})
	}
}

// TestNormalizeMetricsUnrecognisedLinesOfCodeTypeIsSkipped proves a lines_of_code
// datapoint whose type is neither added nor removed is skipped (route-tolerated),
// not a hard error — a future category must not fail the batch.
func TestNormalizeMetricsUnrecognisedLinesOfCodeTypeIsSkipped(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.lines_of_code.count","sum":{"dataPoints":[{"attributes":[{"key":"type","value":{"stringValue":"moved"}}],"asDouble":9,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`)
	_, err := NormalizeMetrics(payload, time.Now().UTC())
	if !errors.Is(err, ErrUnsupportedMetrics) {
		t.Fatalf("got %v, want ErrUnsupportedMetrics for an unrecognised lines_of_code type", err)
	}
}

// TestNormalizeMetricsMalformedCostValueIsError proves the routing contract for
// cost.usage: a Claude cost datapoint with an unparseable USD value is a real
// error, NOT the skip sentinel, so the route does not silently 202-accept and
// drop supported Claude cost data.
func TestNormalizeMetricsMalformedCostValueIsError(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.cost.usage","sum":{"dataPoints":[{"attributes":[{"key":"model","value":{"stringValue":"claude-sonnet-5"}}],"asDouble":-0.5,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`)
	_, err := NormalizeMetrics(payload, time.Now().UTC())
	if err == nil || errors.Is(err, ErrUnsupportedMetrics) {
		t.Fatalf("got %v, want a hard normalisation error for an unparseable cost value", err)
	}
}

// TestNormalizeMetricsRejectsNonClaudeService proves a Codex metrics payload is
// left for the Codex adapter (sentinel, not misattributed) so mixed-tool batches
// are safe.
func TestNormalizeMetricsRejectsNonClaudeService(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}}]},"scopeMetrics":[{"metrics":[{"name":"codex.turn.token_usage","histogram":{"dataPoints":[{"attributes":[{"key":"token_type","value":{"stringValue":"input"}}],"sum":"12","timeUnixNano":"1789042160000000000"}]}}]}]}]}`)
	_, err := NormalizeMetrics(payload, time.Now().UTC())
	if !errors.Is(err, ErrUnsupportedMetrics) {
		t.Fatalf("got %v, want ErrUnsupportedMetrics", err)
	}
}

// TestNormalizeMetricsUnmappedClaudeMetricIsTolerated proves a Claude resource
// carrying only a still-unmapped metric (e.g. claude_code.session.count, owned by
// #99 / M12) is accepted without a hard error — it yields the skip sentinel so the
// route 202-accepts it. token.usage (#89), cost.usage (#97) and the code-output
// counters (#98) are now mapped, so the tolerated example uses a metric none of
// those issues claim.
func TestNormalizeMetricsUnmappedClaudeMetricIsTolerated(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.session.count","sum":{"dataPoints":[{"attributes":[{"key":"model","value":{"stringValue":"claude-haiku-4-5-20251001"}}],"asDouble":1,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`)
	_, err := NormalizeMetrics(payload, time.Now().UTC())
	if !errors.Is(err, ErrUnsupportedMetrics) {
		t.Fatalf("got %v, want ErrUnsupportedMetrics for an unmapped Claude metric", err)
	}
}

// TestNormalizeMetricsMalformedTokenValueIsError proves the #89 routing contract:
// a Claude token.usage datapoint with a recognised token type but an unparseable
// value is a real error, NOT the skip sentinel, so the route does not silently
// 202-accept and drop supported Claude data.
func TestNormalizeMetricsMalformedTokenValueIsError(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.token.usage","sum":{"dataPoints":[{"attributes":[{"key":"type","value":{"stringValue":"input"}}],"asDouble":-1,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`)
	_, err := NormalizeMetrics(payload, time.Now().UTC())
	if err == nil || errors.Is(err, ErrUnsupportedMetrics) {
		t.Fatalf("got %v, want a hard normalisation error for an unparseable token value", err)
	}
}

// TestNormalizeMetricsFiltersSensitiveAttributes proves the allow-list drops
// identity/secret-bearing attributes (the adapter is the sole guard after #88
// removed storage-side sanitising) while keeping safe behaviour metadata.
func TestNormalizeMetricsFiltersSensitiveAttributes(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}},{"key":"service.version","value":{"stringValue":"2.1.268"}},{"key":"os.type","value":{"stringValue":"linux"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.token.usage","sum":{"dataPoints":[{"attributes":[{"key":"type","value":{"stringValue":"input"}},{"key":"model","value":{"stringValue":"claude-haiku-4-5-20251001"}},{"key":"query_source","value":{"stringValue":"main"}},{"key":"session.id","value":{"stringValue":"synthetic-session"}},{"key":"api_key","value":{"stringValue":"tiq-canary-api-key"}},{"key":"authorization","value":{"stringValue":"Bearer tiq-canary-token"}},{"key":"user.email","value":{"stringValue":"synthetic@example.test"}},{"key":"file.path","value":{"stringValue":"/home/tiq-canary/secret"}}],"asDouble":12,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`)
	events, err := NormalizeMetrics(payload, time.Now().UTC())
	if err != nil {
		t.Fatalf("NormalizeMetrics: %v", err)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	for _, leaked := range []string{"tiq-canary-api-key", "Bearer tiq-canary-token", "synthetic@example.test", "/home/tiq-canary/secret"} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("sensitive value %q leaked in %s", leaked, encoded)
		}
	}
	metricAttributes := events[0].ProviderExtensions["metric_attributes"].(map[string]any)
	if metricAttributes["query_source"] != "main" {
		t.Fatalf("safe metric attribute query_source not preserved: %#v", metricAttributes)
	}
	resource := events[0].ProviderExtensions["resource"].(map[string]any)
	if resource["os.type"] != "linux" {
		t.Fatalf("safe resource attribute os.type not preserved: %#v", resource)
	}
}

// TestNormalizeMetricsDistinctEventIDsForSameTimestamp proves two token
// datapoints sharing a session, type, and timestamp do not collide under
// CorrelateEvents (finding #8): the datapoint index keeps them distinct.
func TestNormalizeMetricsDistinctEventIDsForSameTimestamp(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.token.usage","sum":{"dataPoints":[{"attributes":[{"key":"type","value":{"stringValue":"input"}},{"key":"session.id","value":{"stringValue":"s1"}}],"asDouble":10,"timeUnixNano":"1789042160000000000"},{"attributes":[{"key":"type","value":{"stringValue":"input"}},{"key":"session.id","value":{"stringValue":"s1"}}],"asDouble":20,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`)
	events, err := NormalizeMetrics(payload, time.Now().UTC())
	if err != nil {
		t.Fatalf("NormalizeMetrics: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2 distinct datapoints", len(events))
	}
	if events[0].EventID == events[1].EventID {
		t.Fatalf("event IDs must stay distinct for same-timestamp datapoints: %#v", events)
	}
}
