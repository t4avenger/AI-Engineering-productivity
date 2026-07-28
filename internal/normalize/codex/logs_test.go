package codex

import (
	"errors"
	"testing"
	"time"
)

func TestNormalizeLogsObservedShape(t *testing.T) {
	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_cli_rs"}},{"key":"service.version","value":{"stringValue":"0.145.0"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.sse_event"}},{"key":"model","value":{"stringValue":"synthetic-model"}},{"key":"input_token_count","value":{"stringValue":"100"}}],"severityText":"INFO"}]}]}]}`)
	events, err := NormalizeLogs(data, time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC), func([]byte) string { return "local-fingerprint" })
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	if events[0].SessionID != "codex-log:local-fingerprint" || events[0].Attributes["model"] != "synthetic-model" {
		t.Fatalf("event = %#v", events[0])
	}
}

func TestNormalizeLogsFingerprintIncludesBodyAndTimestamps(t *testing.T) {
	data := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_cli_rs"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.tool_result"}}],"body":{"stringValue":"synthetic-first"},"observedTimeUnixNano":"1","timeUnixNano":"1"},{"attributes":[{"key":"event.name","value":{"stringValue":"codex.tool_result"}}],"body":{"stringValue":"synthetic-second"},"observedTimeUnixNano":"2","timeUnixNano":"2"}]}]}]}`)
	events, err := NormalizeLogs(data, time.Now(), func(value []byte) string { return string(value) })
	if err != nil || len(events) != 2 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	if events[0].EventID == events[1].EventID {
		t.Fatalf("event IDs must differ: %#v", events)
	}
}

func TestNormalizeLogsRejectsUnobservedService(t *testing.T) {
	_, err := NormalizeLogs([]byte(`{"resourceLogs":[{"resource":{"attributes":[]},"scopeLogs":[]}]}`), time.Now(), func([]byte) string { return "x" })
	if !errors.Is(err, ErrUnsupportedLogs) {
		t.Fatalf("error = %v", err)
	}
}
