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

func TestNormalizeLogsAPIRequestGolden(t *testing.T) {
	payload := otlpFixturePayload(t, "cursor-otel-0.1.0-api-request-logs.json")
	receivedAt := time.Date(2026, 9, 12, 21, 5, 1, 0, time.UTC)

	first, err := NormalizeLogs(payload, receivedAt)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := NormalizeLogs(payload, receivedAt)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("normalisation must be deterministic")
	}
	assertCursorAPIRequestEvents(t, first)
	if updateGolden() {
		writeGolden(t, "cursor-otel-0.1.0-api-request-logs.events.json", first)
	}
	assertMatchesGolden(t, "cursor-otel-0.1.0-api-request-logs.events.json", first)
}

func assertCursorAPIRequestEvents(t *testing.T, events []canonical.Event) {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1 api.request (skill_activated route-tolerated)", len(events))
	}
	event := events[0]
	if event.EventType != "cursor.api.request" {
		t.Fatalf("event type = %q", event.EventType)
	}
	if event.Provider != provider || event.Tool != otelTool {
		t.Fatalf("provider/tool = %q/%q", event.Provider, event.Tool)
	}
	if event.SessionID != "cursor:00000000-0000-4000-8000-0000000000c1" {
		t.Fatalf("session id = %q", event.SessionID)
	}
	if event.EventID != "cursor:customer-telemetry:v1:synthetic-event-001" {
		t.Fatalf("event id = %q", event.EventID)
	}
	if event.Attributes["model"] != "Thinking" {
		t.Fatalf("model = %#v", event.Attributes["model"])
	}
	for key, want := range map[string]int64{
		"input_token_count":             100,
		"output_token_count":            40,
		"cached_input_token_count":      20,
		"cache_write_input_token_count": 5,
	} {
		if event.Attributes[key] != want {
			t.Fatalf("%s = %#v, want %d", key, event.Attributes[key], want)
		}
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	for _, canary := range []string{"424242", "434343", "cursor.team.id", "cursor.user.id", "tiq-probe"} {
		if strings.Contains(body, canary) {
			t.Fatalf("persisted logs must not retain canary %q: %s", canary, body)
		}
	}
	resource, _ := event.ProviderExtensions["resource"].(map[string]any)
	if resource["cursor.surface"] != "desktop" {
		t.Fatalf("safe resource attrs = %#v", resource)
	}
}

func TestNormalizeLogsRejectsNonCursorService(t *testing.T) {
	payload := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeLogs":[{"logRecords":[{"body":{"stringValue":"api_request"},"attributes":[{"key":"cursor.api.request.input_tokens","value":{"intValue":"1"}}]}]}]}]}`)
	_, err := NormalizeLogs(payload, time.Now().UTC())
	if !errors.Is(err, ErrUnsupportedLogs) {
		t.Fatalf("got %v, want ErrUnsupportedLogs", err)
	}
}

func TestNormalizeLogsRejectsAPIRequestWithoutTokens(t *testing.T) {
	payload := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"cursor"}}]},"scopeLogs":[{"scope":{"name":"cursor.telemetry","version":"0.1.0"},"logRecords":[{"body":{"stringValue":"api_request"},"attributes":[{"key":"cursor.conversation.id","value":{"stringValue":"c1"}}]}]}]}]}`)
	_, err := NormalizeLogs(payload, time.Now().UTC())
	if err == nil || errors.Is(err, ErrUnsupportedLogs) {
		t.Fatalf("got %v, want hard normalisation error", err)
	}
}

func TestNormalizeLogsSkipsNonTelemetryScope(t *testing.T) {
	payload := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"cursor"}}]},"scopeLogs":[{"scope":{"name":"other.scope","version":"1.0.0"},"logRecords":[{"body":{"stringValue":"api_request"},"attributes":[{"key":"cursor.api.request.input_tokens","value":{"intValue":"1"}},{"key":"cursor.conversation.id","value":{"stringValue":"c1"}}]}]}]}]}`)
	_, err := NormalizeLogs(payload, time.Now().UTC())
	if !errors.Is(err, ErrUnsupportedLogs) {
		t.Fatalf("got %v, want ErrUnsupportedLogs for non-cursor.telemetry scope", err)
	}
}

func TestNormalizeLogsFallbackEventIDsIncludeResourceAndScope(t *testing.T) {
	payload := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"cursor"}},{"key":"service.version","value":{"stringValue":"a"}}]},"scopeLogs":[{"scope":{"name":"cursor.telemetry","version":"0.1.0"},"logRecords":[{"timeUnixNano":"1790000000000000001","body":{"stringValue":"api_request"},"attributes":[{"key":"cursor.conversation.id","value":{"stringValue":"same"}},{"key":"cursor.api.request.input_tokens","value":{"intValue":"1"}}]}]}]},{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"cursor"}},{"key":"service.version","value":{"stringValue":"b"}}]},"scopeLogs":[{"scope":{"name":"cursor.telemetry","version":"0.1.0"},"logRecords":[{"timeUnixNano":"1790000000000000001","body":{"stringValue":"api_request"},"attributes":[{"key":"cursor.conversation.id","value":{"stringValue":"same"}},{"key":"cursor.api.request.input_tokens","value":{"intValue":"1"}}]}]}]}]}`)
	events, err := NormalizeLogs(payload, time.Date(2026, 9, 12, 21, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 distinct fallback ids", len(events))
	}
	if events[0].EventID == events[1].EventID {
		t.Fatalf("fallback event ids collided across resources: %q", events[0].EventID)
	}
}
