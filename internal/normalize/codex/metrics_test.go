package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
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
	first, err := NormalizeMetrics(payload, receivedAt, stubCodexFingerprint)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := NormalizeMetrics(payload, receivedAt, stubCodexFingerprint)
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

func TestNormalizeMetricsRejectsNonCodexService(t *testing.T) {
	payload := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"other"}}]},"scopeMetrics":[{"metrics":[{"name":"codex.skill.injected","sum":{"dataPoints":[{"attributes":[{"key":"skill","value":{"stringValue":"x"}}],"asInt":1}]}}]}]}]}`)
	_, err := NormalizeMetrics(payload, time.Now().UTC(), stubCodexFingerprint)
	if !errors.Is(err, ErrUnsupportedMetrics) {
		t.Fatalf("got %v, want ErrUnsupportedMetrics", err)
	}
}

func stubCodexFingerprint(value []byte) string {
	// Deterministic but input-sensitive so distinct skill datapoints stay distinct.
	sum := 0
	for _, b := range value {
		sum = (sum*131 + int(b)) & 0xffff
	}
	return fmt.Sprintf("fixture-%04x", sum)
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
