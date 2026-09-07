package claude

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wayne/telemetryiq/internal/privacy"
)

// canaryTokens are sentinel values placed in sanitiser-handled fields. None may
// survive into canonical output once the payload has crossed the shared #23
// redaction boundary.
var canaryTokens = []string{
	"tiq-canary-email",
	"tiq-canary-api-key",
	"tiq-canary-output",
	"tiq-canary-arguments",
	"tiq-canary-account",
	"tiq-canary-embedded",
}

// TestClaudeAdapterHonoursRedactionBoundary proves the adapter never emits
// sensitive values: a synthetic Claude payload carrying canaries in
// sanitiser-handled fields is passed through the shared privacy.Sanitizer
// (issue #23) exactly as the live ingest path does, and neither the canonical
// events nor the model-interaction records contain a canary.
func TestClaudeAdapterHonoursRedactionBoundary(t *testing.T) {
	sanitizer, err := privacy.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	raw := map[string]any{
		"fixture_version": 1, "fixture_origin": "synthetic", "provider": "anthropic", "tool": "claude-code",
		"tool_version": "2.1.251", "captured_at": "2026-08-31T13:37:07Z", "sanitisation_reviewed": true,
		"payload": map[string]any{
			"source_type": "otlp_http_json_logs",
			"sample_events": []any{map[string]any{
				"event_name": "api_request", "session_id": "synthetic-session-canary-safe", "event_sequence": float64(9),
				"event_timestamp": "2026-08-31T13:37:07.791Z", "model": "claude-opus-4-8",
				"input_tokens": float64(2), "output_tokens": float64(4), "cache_read_tokens": float64(0),
				"duration_ms": float64(3077),
				"user.email":  "tiq-canary-email@example.test", "api_key": "tiq-canary-api-key",
				"output": "tiq-canary-output", "arguments": "--flag tiq-canary-arguments",
				"account_id": "tiq-canary-account", "custom_note": "token=tiq-canary-embedded",
			}},
		},
	}

	result := sanitizer.Sanitize(raw)
	safe, err := json.Marshal(result.Value)
	if err != nil {
		t.Fatalf("marshal sanitized: %v", err)
	}

	events, err := NormalizeEvents(safe, func(value []byte) string { return sanitizer.Fingerprint(value) })
	if err != nil {
		t.Fatalf("normalise events: %v", err)
	}
	records, err := ExtractModelInteractions(safe, func(value []byte) string { return sanitizer.Fingerprint(value) })
	if err != nil {
		t.Fatalf("extract records: %v", err)
	}
	if len(events) != 1 || len(records) != 1 {
		t.Fatalf("expected one event and one record, got %d/%d", len(events), len(records))
	}

	if events[0].SessionID != "claude-code:synthetic-session-canary-safe" || records[0].SessionID != events[0].SessionID {
		t.Fatalf("native session ID not retained consistently: event=%q record=%q", events[0].SessionID, records[0].SessionID)
	}

	// The sanitiser removes/redacts content fields while retaining local provider
	// session IDs by policy; canaries in sensitive fields must still be absent.
	for _, value := range []any{events, records} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal output: %v", err)
		}
		assertNoCanaryLeak(t, data)
	}
}

func assertNoCanaryLeak(t *testing.T, data []byte) {
	t.Helper()
	for _, canary := range canaryTokens {
		if strings.Contains(string(data), canary) {
			t.Fatalf("redaction boundary leaked %q in %s", canary, data)
		}
	}
}
