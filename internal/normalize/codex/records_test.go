package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// fixtureReceivedAt is the fixed ingestion time used for the golden output so
// timestamps derived from receivedAt (absent record timestamps) are stable.
var fixtureReceivedAt = time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

// stubFingerprint keeps record/session IDs deterministic for golden comparison.
func stubFingerprint([]byte) string { return "fixture" }

func TestExtractLogModelInteractionsGolden(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "fixtures", "codex", "observed-sanitised", "codex-0.145.0-logs.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var wrapper struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		t.Fatalf("unwrap payload: %v", err)
	}

	got, err := ExtractLogModelInteractions([]byte(wrapper.Payload), fixtureReceivedAt, stubFingerprint)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	goldenBytes, err := os.ReadFile(filepath.Join("..", "..", "..", "fixtures", "codex", "expected", "codex-0.145.0-logs.records.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var want []canonical.ModelInteraction
	if err := json.Unmarshal(goldenBytes, &want); err != nil {
		t.Fatalf("decode golden: %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.MarshalIndent(got, "", "  ")
		t.Fatalf("records != golden:\n got %s\nwant %s", gotJSON, goldenBytes)
	}
}

// logPayload builds a raw OTLP logs payload wrapping a single record's
// attributes, for the negative tests.
func logPayload(t *testing.T, service string, recordAttrs map[string]any) []byte {
	t.Helper()
	attrs := make([]map[string]any, 0, len(recordAttrs))
	for key, value := range recordAttrs {
		attrs = append(attrs, map[string]any{"key": key, "value": map[string]any{"stringValue": value}})
	}
	payload := map[string]any{
		"resourceLogs": []map[string]any{{
			"resource": map[string]any{"attributes": []map[string]any{
				{"key": "service.name", "value": map[string]any{"stringValue": service}},
			}},
			"scopeLogs": []map[string]any{{
				"logRecords": []map[string]any{{"attributes": attrs, "severityText": "INFO"}},
			}},
		}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return data
}

func extractOne(t *testing.T, service string, recordAttrs map[string]any) []canonical.ModelInteraction {
	t.Helper()
	records, err := ExtractLogModelInteractions(logPayload(t, service, recordAttrs), fixtureReceivedAt, stubFingerprint)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	return records
}

func TestExtractAcceptsBothServiceNames(t *testing.T) {
	t.Parallel()

	for _, service := range []string{"codex_cli_rs", "codex_exec"} {
		records := extractOne(t, service, map[string]any{"event.name": "codex.sse_event", "model": "m", "input_token_count": "5"})
		if len(records) != 1 {
			t.Fatalf("service %q: records = %d, want 1", service, len(records))
		}
	}
}

func TestExtractMissingTokensStayNil(t *testing.T) {
	t.Parallel()

	records := extractOne(t, "codex_cli_rs", map[string]any{"event.name": "codex.sse_event", "model": "m"})
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0].InputTokens != nil || records[0].OutputTokens != nil {
		t.Fatalf("absent tokens must stay nil, got in=%v out=%v", records[0].InputTokens, records[0].OutputTokens)
	}
	// A nil token must serialise as null, never a fabricated 0.
	encoded, _ := json.Marshal(records[0])
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if fields["input_tokens"] != nil || fields["output_tokens"] != nil {
		t.Fatalf("absent tokens must serialise as null, got %v / %v", fields["input_tokens"], fields["output_tokens"])
	}
	// Model observed but no tokens observed => unknown provenance, not fabricated observed.
	if records[0].Provenance != canonical.ProvenanceUnknown {
		t.Fatalf("provenance = %q, want %q", records[0].Provenance, canonical.ProvenanceUnknown)
	}
}

func TestExtractMalformedTokenIsNilNotZero(t *testing.T) {
	t.Parallel()

	records := extractOne(t, "codex_cli_rs", map[string]any{"event.name": "codex.sse_event", "model": "m", "input_token_count": "not-a-number"})
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0].InputTokens != nil {
		t.Fatalf("malformed token must be nil, got %v", *records[0].InputTokens)
	}
}

func TestExtractParsesNumericTokenValue(t *testing.T) {
	t.Parallel()

	// OTLP doubleValue arrives as a JSON number (float64 after decoding). An
	// integral value parses; a non-integral one must be nil, never truncated.
	payload := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_cli_rs"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.sse_event"}},{"key":"model","value":{"stringValue":"m"}},{"key":"input_token_count","value":{"doubleValue":42}},{"key":"output_token_count","value":{"doubleValue":7.5}}],"severityText":"INFO"}]}]}]}`)
	records, err := ExtractLogModelInteractions(payload, fixtureReceivedAt, stubFingerprint)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0].InputTokens == nil || *records[0].InputTokens != 42 {
		t.Fatalf("numeric input token = %v, want 42", records[0].InputTokens)
	}
	if records[0].OutputTokens != nil {
		t.Fatalf("non-integral token must be nil (not truncated), got %v", *records[0].OutputTokens)
	}
}

func TestExtractMissingModelIsUnknown(t *testing.T) {
	t.Parallel()

	records := extractOne(t, "codex_cli_rs", map[string]any{"event.name": "codex.sse_event", "input_token_count": "5"})
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0].Model != "unknown" {
		t.Fatalf("model = %q, want %q", records[0].Model, "unknown")
	}
	if records[0].Provenance != canonical.ProvenanceUnknown {
		t.Fatalf("provenance = %q, want %q", records[0].Provenance, canonical.ProvenanceUnknown)
	}
}

func TestExtractSkipsNonModelRecords(t *testing.T) {
	t.Parallel()

	// Non-whitelisted event.name => no record.
	if records := extractOne(t, "codex_cli_rs", map[string]any{"event.name": "codex.tool_result", "model": "m"}); len(records) != 0 {
		t.Fatalf("tool_result must yield no record, got %d", len(records))
	}
	// Whitelisted event but neither model nor tokens => no record.
	if records := extractOne(t, "codex_cli_rs", map[string]any{"event.name": "codex.sse_event"}); len(records) != 0 {
		t.Fatalf("empty sse_event must yield no record, got %d", len(records))
	}
}

func TestExtractSkipsUnobservedService(t *testing.T) {
	t.Parallel()

	if records := extractOne(t, "some_other_service", map[string]any{"event.name": "codex.sse_event", "model": "m", "input_token_count": "5"}); len(records) != 0 {
		t.Fatalf("unobserved service must yield no record, got %d", len(records))
	}
}

func TestExtractRequiresFingerprint(t *testing.T) {
	t.Parallel()

	if _, err := ExtractLogModelInteractions([]byte(`{"resourceLogs":[]}`), fixtureReceivedAt, nil); err == nil {
		t.Fatal("nil fingerprint must error")
	}
}
