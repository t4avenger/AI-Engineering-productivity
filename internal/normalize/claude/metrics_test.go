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
// carrying only metrics #89 does not map (e.g. cost.usage) is accepted without a
// hard error — it yields the skip sentinel so the route 202-accepts it.
func TestNormalizeMetricsUnmappedClaudeMetricIsTolerated(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.cost.usage","sum":{"dataPoints":[{"attributes":[{"key":"model","value":{"stringValue":"claude-haiku-4-5-20251001"}}],"asDouble":0.01,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`)
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
