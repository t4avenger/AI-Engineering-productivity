package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/wayne/telemetryiq/internal/privacy"
)

// Reviewed, committed fixtures the suite replays. Codex adapters consume the
// inner OTLP logs payload, so its fixture is unwrapped; the Claude adapter
// consumes the full reviewed wrapper.
const (
	codexLogsFixture   = "codex/observed-sanitised/codex-0.145.0-logs.json"
	claudeEventFixture = "claude/observed-sanitised/claude-code-2.1.251-otlp-events.json"
	cursorEventFixture = "cursor/observed-sanitised/cursor-agent-2026.09.02-c22c1a3-stream-result-with-model.json"
)

// stubFingerprint keeps record and session identifiers deterministic; the suite
// asserts contracts, not fingerprint values.
func stubFingerprint([]byte) string { return "fixture" }

// fixturePath resolves a path under the repository fixtures/ directory. The suite
// package lives at internal/conformance, two levels below the repository root.
func fixturePath(t *testing.T, relative string) string {
	t.Helper()
	return filepath.Join("..", "..", "fixtures", relative)
}

func readFixture(t *testing.T, relative string) []byte {
	t.Helper()
	data, err := os.ReadFile(fixturePath(t, relative))
	if err != nil {
		t.Fatalf("read fixture %s: %v", relative, err)
	}
	return data
}

// codexReviewedInput returns the inner OTLP logs payload of the reviewed Codex
// fixture, the byte shape the Codex log adapters consume.
func codexReviewedInput(t *testing.T) []byte {
	t.Helper()
	var wrapper struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(readFixture(t, codexLogsFixture), &wrapper); err != nil {
		t.Fatalf("unwrap codex payload: %v", err)
	}
	return wrapper.Payload
}

// claudeReviewedInput returns the full reviewed Claude Code fixture wrapper, the
// byte shape the Claude adapters consume (they validate the wrapper themselves).
func claudeReviewedInput(t *testing.T) []byte {
	t.Helper()
	return readFixture(t, claudeEventFixture)
}

// cursorReviewedInput returns the full reviewed Cursor Agent fixture wrapper,
// the byte shape the Cursor adapters consume (they validate the wrapper
// themselves).
func cursorReviewedInput(t *testing.T) []byte {
	t.Helper()
	return readFixture(t, cursorEventFixture)
}

// sanitise runs a provider-shaped payload through the real privacy sanitiser and
// marshals the result, mirroring the production pipeline (sanitise before an
// adapter ever sees a payload). A fixed salt keeps fingerprints deterministic.
func sanitise(t *testing.T, raw map[string]any) []byte {
	t.Helper()
	sanitizer, err := privacy.New(make([]byte, 32))
	if err != nil {
		t.Fatalf("new sanitizer: %v", err)
	}
	data, err := json.Marshal(sanitizer.Sanitize(raw).Value)
	if err != nil {
		t.Fatalf("marshal sanitised payload: %v", err)
	}
	return data
}

// codexCanaryPayload builds a Codex OTLP logs payload whose sensitive attributes
// carry canaryMarker across every redaction vector the sanitiser must cover: an
// OTLP attribute the classifier removes by name (prompt, response, an email), one
// it redacts by name (arguments), and a retained-key attribute whose value is a
// secret-pattern string caught by value inspection. It stays an eligible model
// interaction (event.name, model, token counts) so a record is still produced —
// proving the marker is stripped without suppressing the whole record.
func codexCanaryPayload() map[string]any {
	attr := func(key, value string) map[string]any {
		return map[string]any{"key": key, "value": map[string]any{"stringValue": value}}
	}
	return map[string]any{
		"resourceLogs": []any{
			map[string]any{
				"resource": map[string]any{
					"attributes": []any{attr("service.name", "codex_exec")},
				},
				"scopeLogs": []any{
					map[string]any{
						"logRecords": []any{
							map[string]any{
								"timeUnixNano":         "1753704476000000000",
								"observedTimeUnixNano": "1753704476500000000",
								"severityText":         "INFO",
								"attributes": []any{
									attr("event.name", "codex.sse_event"),
									attr("model", "gpt-5-codex"),
									attr("input_token_count", "1200"),
									attr("output_token_count", "340"),
									attr("prompt", canaryMarker+"-prompt-leak"),
									attr("response", canaryMarker+"-response-leak"),
									attr("user.email", canaryMarker+"-email-leak@example.test"),
									attr("arguments", "--secret "+canaryMarker+"-arguments-leak"),
									attr("note", "token="+canaryMarker+"-secret-leak"),
								},
							},
						},
					},
				},
			},
		},
	}
}

// claudeCanaryPayload builds a full Claude Code fixture wrapper whose api_request
// sample event carries canaryMarker across the same redaction vectors as the
// Codex canary, adapted to the Claude flat-event shape: fields the classifier
// removes by name (prompt, response, an email), one it redacts by name
// (arguments), and a retained-key field holding a secret-pattern value. The event
// stays eligible (model, token counts) so a record is still produced, and the
// sanitised wrapper still passes the shared fixture validator the adapter runs.
func claudeCanaryPayload() map[string]any {
	return map[string]any{
		"fixture_version":       1,
		"fixture_origin":        "observed-sanitised",
		"provider":              "anthropic",
		"tool":                  "claude-code",
		"tool_version":          "2.1.251",
		"captured_at":           "2026-08-31T13:37:07Z",
		"sanitisation_reviewed": true,
		"payload": map[string]any{
			"source_type": "otlp_http_json_logs",
			"sample_events": []any{
				map[string]any{
					"event_name":        "api_request",
					"event_timestamp":   "2026-08-31T13:37:07Z",
					"event_sequence":    1,
					"session_id":        "canary-session",
					"request_id":        "canary-request",
					"model":             "claude-opus-4",
					"input_tokens":      900,
					"output_tokens":     120,
					"cache_read_tokens": 50,
					"prompt":            canaryMarker + "-prompt-leak",
					"response":          canaryMarker + "-response-leak",
					"user_email":        canaryMarker + "-email-leak@example.test",
					"arguments":         "--secret " + canaryMarker + "-arguments-leak",
					"note":              "token=" + canaryMarker + "-secret-leak",
				},
			},
		},
	}
}

// cursorCanaryPayload builds a full Cursor Agent fixture wrapper carrying the
// canaryMarker across sanitiser-handled fields. It stays eligible (usage tokens
// present) so a ModelInteraction record is produced, proving the marker is
// stripped without suppressing the whole record.
func cursorCanaryPayload() map[string]any {
	return map[string]any{
		"fixture_version":       1,
		"fixture_origin":        "observed-sanitised",
		"provider":              "cursor",
		"tool":                  "cursor-agent",
		"tool_version":          "2026.09.02-c22c1a3",
		"captured_at":           "2026-09-06T18:00:30Z",
		"sanitisation_reviewed": true,
		"payload": map[string]any{
			"source_type": "local_cli_stream_json",
			"capture": map[string]any{
				"workspace":    "isolated_tmp",
				"mode":         "ask",
				"user.email":   canaryMarker + "-email-leak@example.test",
				"arguments":    "--secret " + canaryMarker + "-arguments-leak",
				"note":         "token=" + canaryMarker + "-secret-leak",
				"prompt":       canaryMarker + "-prompt-leak",
				"response":     canaryMarker + "-response-leak",
				"account_id":   canaryMarker + "-account-leak",
				"custom_field": "ok",
			},
			"init": map[string]any{
				"type":       "system",
				"subtype":    "init",
				"model":      "GPT-5.2 Medium",
				"session_id": "canary-session",
			},
			"result": map[string]any{
				"type":        "result",
				"subtype":     "success",
				"is_error":    false,
				"duration_ms": 1000,
				"session_id":  "canary-session",
				"request_id":  "canary-request",
				"usage": map[string]any{
					"inputTokens":      123,
					"outputTokens":     4,
					"cacheReadTokens":  5,
					"cacheWriteTokens": 6,
				},
			},
		},
	}
}
