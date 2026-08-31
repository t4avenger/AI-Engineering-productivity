package canonical

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func int64Ptr(v int64) *int64 { return &v }

func TestModelInteractionJSONRoundTrip(t *testing.T) {
	t.Parallel()

	original := ModelInteraction{
		SchemaVersion:     RecordSchemaVersion,
		RequestID:         "req-001",
		SessionID:         "session-001",
		Provider:          "openai",
		Tool:              "codex",
		Model:             "gpt-5-codex",
		StartedAt:         time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
		CompletedAt:       time.Date(2026, time.July, 24, 12, 0, 2, 0, time.UTC),
		DurationMs:        int64Ptr(2000),
		InputTokens:       int64Ptr(9000),
		OutputTokens:      int64Ptr(1200),
		CachedInputTokens: int64Ptr(5000),
		ReasoningTokens:   nil,
		Result:            "success",
		ErrorCode:         nil,
		Provenance:        ProvenanceObserved,
		ProviderExtensions: map[string]any{
			"mcp_call": map[string]any{"server": "filesystem", "method": "read_file"},
			"skill":    map[string]any{"name": "pdf"},
		},
	}

	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded ModelInteraction
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(original, decoded) {
		t.Fatalf("round-trip mismatch:\n got %#v\nwant %#v", decoded, original)
	}
}

func TestOperationJSONRoundTrip(t *testing.T) {
	t.Parallel()

	original := Operation{
		SchemaVersion: RecordSchemaVersion,
		OperationID:   "op-001",
		SessionID:     "session-001",
		Provider:      "openai",
		Tool:          "codex",
		Category:      OperationCategoryMCPCall,
		Outcome:       "success",
		Provenance:    ProvenanceInferred,
		ProviderExtensions: map[string]any{
			"file_operation": map[string]any{"path": "/tmp/out.txt"},
		},
	}

	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Operation
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(original, decoded) {
		t.Fatalf("round-trip mismatch:\n got %#v\nwant %#v", decoded, original)
	}
}

func TestRecordsCarryProvenanceMarker(t *testing.T) {
	t.Parallel()

	for name, encoded := range map[string][]byte{
		"model interaction": mustMarshal(t, ModelInteraction{Provenance: ProvenanceObserved, ProviderExtensions: map[string]any{}}),
		"operation":         mustMarshal(t, Operation{Provenance: ProvenanceObserved, ProviderExtensions: map[string]any{}}),
	} {
		var fields map[string]any
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatalf("%s: unmarshal: %v", name, err)
		}
		if fields["provenance"] != string(ProvenanceObserved) {
			t.Fatalf("%s: provenance = %v, want %q", name, fields["provenance"], ProvenanceObserved)
		}
	}
}

// TestProviderSpecificsStayInProviderExtensions is the no-promotion guard: MCP,
// skill and file specifics must survive a round-trip inside provider_extensions
// and must NOT appear as typed top-level keys. When two providers validate
// identical semantics these may be promoted (a later phase), and this test is
// the tripwire for an accidental early promotion.
func TestProviderSpecificsStayInProviderExtensions(t *testing.T) {
	t.Parallel()

	extensions := map[string]any{
		"mcp_call":       map[string]any{"server": "filesystem", "method": "read_file"},
		"skill":          map[string]any{"name": "pdf"},
		"file_operation": map[string]any{"path": "/tmp/report.md", "bytes": float64(2048)},
	}

	for name, encoded := range map[string][]byte{
		"model interaction": mustMarshal(t, ModelInteraction{SchemaVersion: RecordSchemaVersion, Provenance: ProvenanceObserved, ProviderExtensions: extensions}),
		"operation":         mustMarshal(t, Operation{SchemaVersion: RecordSchemaVersion, Provenance: ProvenanceObserved, ProviderExtensions: extensions}),
	} {
		var fields map[string]any
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatalf("%s: unmarshal: %v", name, err)
		}
		for _, promoted := range []string{"mcp_call", "skill", "file_operation"} {
			if _, found := fields[promoted]; found {
				t.Fatalf("%s: %q must not be promoted to a top-level field", name, promoted)
			}
		}
		preserved, ok := fields["provider_extensions"].(map[string]any)
		if !ok {
			t.Fatalf("%s: provider_extensions missing or wrong type", name)
		}
		if !reflect.DeepEqual(preserved, extensions) {
			t.Fatalf("%s: provider_extensions not preserved:\n got %#v\nwant %#v", name, preserved, extensions)
		}
	}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return encoded
}
