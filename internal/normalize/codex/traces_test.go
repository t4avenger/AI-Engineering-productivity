package codex

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestNormalizeTracesGoldenObservedFixture(t *testing.T) {
	payload := observedTracePayload(t)
	receivedAt := time.Date(2026, 9, 17, 19, 6, 0, 0, time.UTC)
	first, err := NormalizeTraces(payload, receivedAt)
	if err != nil {
		t.Fatalf("normalise observed traces: %v", err)
	}
	second, err := NormalizeTraces(payload, receivedAt)
	if err != nil {
		t.Fatalf("normalise observed traces again: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("Codex trace normalisation must be deterministic")
	}
	got, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	golden := filepath.Join(codexFixturesDir(t), "expected", "codex-0.154.0-trace-spans.events.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, append(got, '\n'), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(got) != strings.TrimSpace(string(want)) {
		t.Fatalf("golden output mismatch\nwant: %s\n got: %s", want, got)
	}
}

func TestNormalizeTracesPreservesObservedCorrelationTokensAndUnknowns(t *testing.T) {
	events, err := NormalizeTraces(observedTracePayload(t), time.Date(2026, 9, 17, 19, 6, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	assertObservedTraceBasics(t, events)
	assertObservedTraceTurn(t, events[2])
	assertTraceCanariesAbsent(t, events)
}

func assertObservedTraceBasics(t *testing.T, events []canonical.Event) {
	t.Helper()
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3", len(events))
	}
	for _, event := range events {
		if event.SessionID != "codex:trace:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
			t.Fatalf("session id = %q", event.SessionID)
		}
		if event.SourceVersion != "0.154.0" {
			t.Fatalf("source version = %q", event.SourceVersion)
		}
		correlation := event.ProviderExtensions["correlation"].(map[string]any)
		if correlation["session_id_source"] != "trace.id" {
			t.Fatalf("correlation = %#v", correlation)
		}
		resource := event.ProviderExtensions["resource_attributes"].(map[string]any)
		if resource["service.name"] != "codex_exec" || resource["service.version"] != "0.154.0" {
			t.Fatalf("resource attributes = %#v", resource)
		}
	}
}

func assertObservedTraceTurn(t *testing.T, turn canonical.Event) {
	t.Helper()
	if turn.EventType != "session_task.turn" {
		t.Fatalf("turn event type = %q", turn.EventType)
	}
	for key, want := range map[string]int64{
		"input_token_count":             1200,
		"cached_input_token_count":      800,
		"cache_write_input_token_count": 0,
		"output_token_count":            12,
		"reasoning_token_count":         3,
		"total_token_count":             1212,
	} {
		if got := turn.Attributes[key]; got != want {
			t.Fatalf("%s = %#v, want %d", key, got, want)
		}
	}
	spanAttributes := turn.ProviderExtensions["span_attributes"].(map[string]any)
	if spanAttributes["codex.custom.observed"] != "retained" {
		t.Fatalf("unknown span attribute = %#v", spanAttributes["codex.custom.observed"])
	}
}

func assertTraceCanariesAbsent(t *testing.T, events []canonical.Event) {
	t.Helper()
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"TRACE_CAPTURE_COMPLETE", "INTERACTIVE_TRACE_CAPTURE_COMPLETE"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("prompt canary %q leaked into normalized events", forbidden)
		}
	}
}

func TestNormalizeTracesDeduplicatesReplay(t *testing.T) {
	payload := observedTracePayloadMap(t)
	resources := payload["resourceSpans"].([]any)
	resource := resources[0].(map[string]any)
	scopes := resource["scopeSpans"].([]any)
	scope := scopes[0].(map[string]any)
	spans := scope["spans"].([]any)
	scope["spans"] = []any{spans[2], spans[0], spans[1], spans[2]}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	events, err := NormalizeTraces(data, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("deduplicated events = %d, want 3", len(events))
	}
	if events[0].EventType != "turn/start" || events[1].EventType != "op.dispatch.turn_input" || events[2].EventType != "session_task.turn" {
		t.Fatalf("event order = %q, %q, %q", events[0].EventType, events[1].EventType, events[2].EventType)
	}
}

func TestNormalizeTracesRoutingAndMalformedSupportedData(t *testing.T) {
	t.Run("foreign resource", func(t *testing.T) {
		payload := observedTracePayloadMap(t)
		setTraceServiceName(t, payload, "other-tool")
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		_, err = NormalizeTraces(data, time.Unix(1, 0))
		if !errors.Is(err, ErrUnsupportedTraces) {
			t.Fatalf("error = %v, want ErrUnsupportedTraces", err)
		}
	})

	t.Run("recognized malformed span", func(t *testing.T) {
		payload := observedTracePayloadMap(t)
		span := firstTraceSpan(t, payload)
		delete(span, "spanId")
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		_, err = NormalizeTraces(data, time.Unix(1, 0))
		if err == nil || !strings.Contains(err.Error(), "spanId") {
			t.Fatalf("error = %v, want spanId failure", err)
		}
	})
}

func FuzzCodexTraces(f *testing.F) {
	f.Add(observedTracePayload(f))
	f.Add([]byte(`{"resourceSpans":[]}`))
	f.Add([]byte("not json"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = NormalizeTraces(data, time.Unix(1, 0))
	})
}

type testHelper interface {
	Helper()
	Fatalf(string, ...any)
}

func observedTracePayload(t testHelper) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(codexFixturesDir(t), "observed-sanitised", "codex-0.154.0-trace-spans-otlp.json"))
	if err != nil {
		t.Fatalf("read observed trace fixture: %v", err)
	}
	var document struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode observed trace fixture: %v", err)
	}
	return document.Payload
}

func observedTracePayloadMap(t *testing.T) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(observedTracePayload(t), &payload); err != nil {
		t.Fatalf("decode trace payload: %v", err)
	}
	return payload
}

func firstTraceSpan(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	return payload["resourceSpans"].([]any)[0].(map[string]any)["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)[0].(map[string]any)
}

func setTraceServiceName(t *testing.T, payload map[string]any, service string) {
	t.Helper()
	resource := payload["resourceSpans"].([]any)[0].(map[string]any)["resource"].(map[string]any)
	for _, value := range resource["attributes"].([]any) {
		attribute := value.(map[string]any)
		if attribute["key"] == "service.name" {
			attribute["value"].(map[string]any)["stringValue"] = service
			return
		}
	}
	t.Fatal("service.name fixture attribute missing")
}

func codexFixturesDir(t testHelper) string {
	t.Helper()
	return filepath.Join("..", "..", "..", "fixtures", "codex")
}
