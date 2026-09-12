package cursor

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func otlpFixturePayload(t *testing.T, name string) []byte {
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
	payload := otlpFixturePayload(t, "cursor-otel-0.1.0-token-usage-metrics.json")
	receivedAt := time.Date(2026, 9, 12, 21, 0, 1, 0, time.UTC)

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
	assertCursorTokenUsageEvents(t, first)
	if updateGolden() {
		writeGolden(t, "cursor-otel-0.1.0-token-usage-metrics.events.json", first)
	}
	assertMatchesGolden(t, "cursor-otel-0.1.0-token-usage-metrics.events.json", first)
}

func assertCursorTokenUsageEvents(t *testing.T, events []canonical.Event) {
	t.Helper()
	if len(events) != 4 {
		t.Fatalf("event count = %d, want 4 token categories", len(events))
	}
	for key, want := range map[string]int64{
		"input_token_count":             128,
		"output_token_count":            64,
		"cached_input_token_count":      512,
		"cache_write_input_token_count": 32,
	} {
		assertCursorTokenAttribute(t, events, key, want)
	}
	assertNoAccountCanaries(t, events)
	for _, event := range events {
		assertCursorTokenUsageEventShape(t, event)
	}
}

func assertCursorTokenUsageEventShape(t *testing.T, event canonical.Event) {
	t.Helper()
	if event.EventType != tokenUsageMetric {
		t.Fatalf("event type = %q", event.EventType)
	}
	if event.Provider != provider || event.Tool != otelTool {
		t.Fatalf("provider/tool = %q/%q", event.Provider, event.Tool)
	}
	if event.SourceSchema != otelSourceSchema {
		t.Fatalf("source schema = %q", event.SourceSchema)
	}
	if event.Attributes["model"] != "Auto" {
		t.Fatalf("model = %#v", event.Attributes["model"])
	}
	if !strings.HasPrefix(event.SessionID, "cursor:token:") {
		t.Fatalf("session id = %q, want content-derived cursor:token: prefix", event.SessionID)
	}
	resource, _ := event.ProviderExtensions["resource"].(map[string]any)
	if resource["cursor.surface"] != "cli" || resource["cursor.entrypoint"] != "cli" {
		t.Fatalf("safe resource attrs = %#v", resource)
	}
}

func assertNoAccountCanaries(t *testing.T, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	for _, canary := range []string{"424242", "434343", "cursor.team.id", "cursor.user.id"} {
		if strings.Contains(body, canary) {
			t.Fatalf("persisted output must not retain account canary %q: %s", canary, body)
		}
	}
}

func assertCursorTokenAttribute(t *testing.T, events []canonical.Event, key string, want int64) {
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

func TestNormalizeMetricsRejectsNonCursorService(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.token.usage","sum":{"dataPoints":[{"attributes":[{"key":"type","value":{"stringValue":"input"}}],"asDouble":12,"timeUnixNano":"1790000000000000001"}]}}]}]}]}`)
	_, err := NormalizeMetrics(payload, time.Now().UTC())
	if !errors.Is(err, ErrUnsupportedMetrics) {
		t.Fatalf("got %v, want ErrUnsupportedMetrics", err)
	}
}

func TestNormalizeMetricsRejectsUnparseableMappedValue(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"cursor"}}]},"scopeMetrics":[{"scope":{"name":"cursor.telemetry","version":"0.1.0"},"metrics":[{"name":"cursor.token.usage","sum":{"dataPoints":[{"attributes":[{"key":"cursor.token.type","value":{"stringValue":"input"}}],"asDouble":-1,"timeUnixNano":"1790000000000000001"}]}}]}]}]}`)
	_, err := NormalizeMetrics(payload, time.Now().UTC())
	if err == nil || errors.Is(err, ErrUnsupportedMetrics) {
		t.Fatalf("got %v, want hard normalisation error", err)
	}
}

func TestNormalizeMetricsToleratesUnmappedMetricsOnly(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"cursor"}}]},"scopeMetrics":[{"scope":{"name":"cursor.telemetry","version":"0.1.0"},"metrics":[{"name":"cursor.tool.calls","sum":{"dataPoints":[{"attributes":[{"key":"cursor.tool.name","value":{"stringValue":"read"}}],"asInt":"1","timeUnixNano":"1790000000000000001"}]}}]}]}]}`)
	_, err := NormalizeMetrics(payload, time.Now().UTC())
	if !errors.Is(err, ErrUnsupportedMetrics) {
		t.Fatalf("got %v, want ErrUnsupportedMetrics when only unmapped metrics present", err)
	}
}

func TestNormalizeMetricsSkipsNonTelemetryScope(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"cursor"}}]},"scopeMetrics":[{"scope":{"name":"other.scope","version":"1.0.0"},"metrics":[{"name":"cursor.token.usage","sum":{"dataPoints":[{"attributes":[{"key":"cursor.token.type","value":{"stringValue":"input"}}],"asDouble":12,"timeUnixNano":"1790000000000000001"}]}}]}]}]}`)
	_, err := NormalizeMetrics(payload, time.Now().UTC())
	if !errors.Is(err, ErrUnsupportedMetrics) {
		t.Fatalf("got %v, want ErrUnsupportedMetrics for non-cursor.telemetry scope", err)
	}
}
