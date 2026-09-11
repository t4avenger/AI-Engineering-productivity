package codex

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}}]},"scopeMetrics":[{"metrics":[{"name":"codex.turn.memory","sum":{"dataPoints":[{"attributes":[{"key":"state","value":{"stringValue":"ok"}}],"asInt":"1"}]}}]}]}]}`)
	_, err := NormalizeMetrics(payload, time.Date(2026, 9, 10, 20, 9, 21, 0, time.UTC))
	if !errors.Is(err, ErrUnsupportedMetrics) {
		t.Fatalf("got %v, want ErrUnsupportedMetrics for intentionally ignored metrics", err)
	}
}

func TestNormalizeMetricsTokenUsageFiltersSensitiveMetricAttributes(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}}]},"scopeMetrics":[{"metrics":[{"name":"codex.turn.token_usage","histogram":{"dataPoints":[{"attributes":[{"key":"token_type","value":{"stringValue":"input"}},{"key":"api_key","value":{"stringValue":"tiq-canary-api-key"}},{"key":"user.email","value":{"stringValue":"synthetic@example.test"}},{"key":"originator","value":{"stringValue":"codex_exec"}}],"count":"1","sum":"12","timeUnixNano":"1789042160000000000"}]}}]}]}]}`)
	events, err := NormalizeMetrics(payload, time.Date(2026, 9, 10, 20, 9, 21, 0, time.UTC))
	if err != nil {
		t.Fatalf("NormalizeMetrics: %v", err)
	}
	metricAttributes := events[0].ProviderExtensions["metric_attributes"].(map[string]any)
	if _, ok := metricAttributes["api_key"]; ok {
		t.Fatalf("api_key leaked into metric attributes: %#v", metricAttributes)
	}
	if _, ok := metricAttributes["user.email"]; ok {
		t.Fatalf("user.email leaked into metric attributes: %#v", metricAttributes)
	}
	if metricAttributes["originator"] != "codex_exec" {
		t.Fatalf("safe metric attributes not preserved: %#v", metricAttributes)
	}
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
