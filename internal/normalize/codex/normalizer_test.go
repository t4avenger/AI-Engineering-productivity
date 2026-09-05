package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNormalizeGoldenFixtureIsDeterministic(t *testing.T) {
	input := readFixture(t, "fixture-001.json")
	first, err := Normalize(input)
	if err != nil {
		t.Fatalf("first normalisation: %v", err)
	}
	second, err := Normalize(input)
	if err != nil {
		t.Fatalf("second normalisation: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("normalisation must be deterministic")
	}
	got, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal canonical events: %v", err)
	}
	want := readGolden(t, "fixture-001.canonical.json")
	if string(got) != strings.TrimSpace(string(want)) {
		t.Fatalf("golden output mismatch\nwant: %s\n got: %s", want, got)
	}
}

func TestNormalizeCorrelatesShuffledDuplicateSpansDeterministically(t *testing.T) {
	root := span("root", map[string]any{"spanId": "0000000000000001", "startTimeUnixNano": "1785059999000000000"})
	child := span("child", map[string]any{"spanId": "0000000000000002", "parentSpanId": "0000000000000001", "startTimeUnixNano": "1785060000000000000"})
	later := span("later", map[string]any{"spanId": "0000000000000003", "parentSpanId": "0000000000000001", "startTimeUnixNano": "1785060001000000000"})

	ordered := fixtureJSON(t, map[string]any{"resourceSpans": []any{map[string]any{"scopeSpans": []any{map[string]any{"spans": []any{root, child, later}}}}}})
	shuffledWithDuplicate := fixtureJSON(t, map[string]any{"resourceSpans": []any{map[string]any{"scopeSpans": []any{map[string]any{"spans": []any{later, child, root, child}}}}}})

	want, err := Normalize(ordered)
	if err != nil {
		t.Fatalf("normalise ordered: %v", err)
	}
	got, err := Normalize(shuffledWithDuplicate)
	if err != nil {
		t.Fatalf("normalise shuffled: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("deduplicated event count = %d, want 3", len(got))
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal ordered: %v", err)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal shuffled: %v", err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("shuffled replay must be byte-identical\nwant: %s\n got: %s", wantJSON, gotJSON)
	}
	if got[0].EventType != "root" || got[1].EventType != "child" || got[2].EventType != "later" {
		t.Fatalf("event order = %s, %s, %s", got[0].EventType, got[1].EventType, got[2].EventType)
	}
}

func TestNormalizeAddsTraceCorrelationAndTaskBoundaryConfidence(t *testing.T) {
	input := fixtureJSON(t, map[string]any{"resourceSpans": []any{map[string]any{
		"scopeSpans": []any{map[string]any{
			"spans": []any{span("synthetic", map[string]any{"parentSpanId": "fedcba9876543210"})},
		}},
	}}})
	events, err := Normalize(input)
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	correlation := events[0].ProviderExtensions["correlation"].(map[string]any)
	if correlation["dedup_key"] != events[0].EventID || correlation["trace_id"] != "0123456789abcdef0123456789abcdef" || correlation["span_id"] != "0123456789abcdef" {
		t.Fatalf("correlation identifiers = %#v", correlation)
	}
	if correlation["parent_span_id"] != "fedcba9876543210" {
		t.Fatalf("parent span = %#v", correlation["parent_span_id"])
	}
	taskBoundary := correlation["task_boundary"].(map[string]any)
	if taskBoundary["confidence"] != "unknown" || events[0].TaskID != nil {
		t.Fatalf("task boundary = %#v, task id = %v", taskBoundary, events[0].TaskID)
	}
	if _, duplicated := events[0].ProviderExtensions["span"].(map[string]any)["parentSpanId"]; duplicated {
		t.Fatal("parentSpanId should be promoted to correlation metadata, not duplicated in unknown span fields")
	}
}

func TestNormalizePreservesUnknownSafeFields(t *testing.T) {
	input := fixtureJSON(t, map[string]any{"resourceSpans": []any{map[string]any{
		"resource_unknown": "preserved",
		"scopeSpans": []any{map[string]any{
			"scope_unknown": true,
			"spans":         []any{span("synthetic", map[string]any{"span_unknown": map[string]any{"safe": "preserved"}})},
		}},
	}}})
	events, err := Normalize(input)
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	if got := events[0].ProviderExtensions["resource"].(map[string]any)["resource_unknown"]; got != "preserved" {
		t.Fatalf("resource unknown field = %v", got)
	}
	if got := events[0].ProviderExtensions["scope"].(map[string]any)["scope_unknown"]; got != true {
		t.Fatalf("scope unknown field = %v", got)
	}
	if got := events[0].ProviderExtensions["span"].(map[string]any)["span_unknown"].(map[string]any)["safe"]; got != "preserved" {
		t.Fatalf("span unknown field = %v", got)
	}
}

func TestNormalizeRejectsUnsupportedOrIncompleteRecord(t *testing.T) {
	for _, test := range []struct {
		name, want string
		payload    map[string]any
	}{
		{name: "logs", payload: map[string]any{"resourceLogs": []any{map[string]any{}}}, want: "resourceSpans"},
		{name: "missing timestamp", payload: map[string]any{"resourceSpans": []any{map[string]any{"scopeSpans": []any{map[string]any{"spans": []any{map[string]any{"name": "synthetic", "traceId": "0123456789abcdef0123456789abcdef", "spanId": "0123456789abcdef"}}}}}}}, want: "startTimeUnixNano"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Normalize(fixtureJSON(t, test.payload))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q error, got %v", test.want, err)
			}
		})
	}
}

func TestNormalizeRejectsMalformedSupportedShape(t *testing.T) {
	for _, test := range []struct {
		name, want string
		payload    map[string]any
	}{
		{name: "resource is not object", payload: map[string]any{"resourceSpans": []any{"not-an-object"}}, want: "must be an object"},
		{name: "scope missing", payload: map[string]any{"resourceSpans": []any{map[string]any{}}}, want: "scopeSpans"},
		{name: "scope is not object", payload: map[string]any{"resourceSpans": []any{map[string]any{"scopeSpans": []any{"not-an-object"}}}}, want: "must be an object"},
		{name: "spans missing", payload: map[string]any{"resourceSpans": []any{map[string]any{"scopeSpans": []any{map[string]any{}}}}}, want: "spans"},
		{name: "span is not object", payload: map[string]any{"resourceSpans": []any{map[string]any{"scopeSpans": []any{map[string]any{"spans": []any{"not-an-object"}}}}}}, want: "must be an object"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Normalize(fixtureJSON(t, test.payload))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q error, got %v", test.want, err)
			}
		})
	}
}

func TestNormalizeRejectsUnsupportedProviderOrTool(t *testing.T) {
	data := fixtureJSON(t, map[string]any{
		"resourceSpans": []any{map[string]any{
			"scopeSpans": []any{map[string]any{
				"spans": []any{span("synthetic", nil)},
			}},
		}},
	})
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	document["provider"] = "other"
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	_, err = Normalize(data)
	if err == nil || !strings.Contains(err.Error(), "provider and tool are openai and codex") {
		t.Fatalf("expected provider/tool error, got %v", err)
	}
}

func TestNormaliseSpanRejectsRequiredValues(t *testing.T) {
	for _, key := range []string{"traceId", "spanId", "name", "startTimeUnixNano"} {
		t.Run(key, func(t *testing.T) {
			fields := span("synthetic", nil)
			delete(fields, key)
			_, err := normaliseSpan(fixtureDocument{Provider: "openai", Tool: "codex", ToolVersion: "synthetic-0.0.0"}, mustTime(t), map[string]any{}, map[string]any{}, fields)
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("expected %q error, got %v", key, err)
			}
		})
	}
}

func TestNormaliseSpanRejectsWhitespaceOnlyRequiredValues(t *testing.T) {
	fields := span("synthetic", nil)
	fields["traceId"] = "   "
	_, err := normaliseSpan(fixtureDocument{Provider: "openai", Tool: "codex", ToolVersion: "synthetic-0.0.0"}, mustTime(t), map[string]any{}, map[string]any{}, fields)
	if err == nil || !strings.Contains(err.Error(), "traceId") {
		t.Fatalf("expected traceId error, got %v", err)
	}
}

func TestUnixNanoTimeRejectsInvalidValues(t *testing.T) {
	for _, value := range []any{"not-a-number", "0", float64(1)} {
		_, err := unixNanoTime(map[string]any{"time": value}, "time")
		if err == nil {
			t.Fatalf("expected invalid value %v to fail", value)
		}
	}
}

func FuzzNormalize(f *testing.F) {
	f.Add(fixtureSeed(f))
	f.Add([]byte("{\"fixture_version\":1}"))
	f.Add([]byte("not json"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Normalize(data)
	})
}

func fixtureSeed(f *testing.F) []byte {
	f.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		f.Fatal("locate test source")
	}
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "fixtures", "codex", "synthetic", "fixture-001.json"))
	if err != nil {
		f.Fatalf("read valid fuzz seed: %v", err)
	}
	return contents
}

func span(name string, fields map[string]any) map[string]any {
	result := map[string]any{"name": name, "traceId": "0123456789abcdef0123456789abcdef", "spanId": "0123456789abcdef", "startTimeUnixNano": "1785060000000000000"}
	for key, value := range fields {
		result[key] = value
	}
	return result
}

func mustTime(t *testing.T) time.Time {
	t.Helper()
	value, err := time.Parse(time.RFC3339, "2026-07-26T10:00:00Z")
	if err != nil {
		t.Fatalf("parse time: %v", err)
	}
	return value
}

func fixtureJSON(t *testing.T, payload map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"fixture_version": 1, "fixture_origin": "synthetic", "provider": "openai", "tool": "codex", "tool_version": "synthetic-0.0.0", "captured_at": "2026-07-26T10:00:00Z", "sanitisation_reviewed": true, "payload": payload})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return data
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	return readFile(t, filepath.Join(repositoryRoot(t), "fixtures", "codex", "synthetic", name))
}

func readGolden(t *testing.T, name string) []byte {
	t.Helper()
	return readFile(t, filepath.Join(repositoryRoot(t), "fixtures", "codex", "expected", name))
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return contents
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}
