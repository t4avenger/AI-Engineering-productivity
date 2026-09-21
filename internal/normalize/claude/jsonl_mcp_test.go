package claude

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

const mcpTranscriptFixture = "claude-code-2.1.269-mcp-tool-call-transcript.json"

// TestNormalizeTranscriptMCPGolden proves the transcript event path emits the
// assistant_message events (as before) plus one mcp_call correlation event per
// reconstructed MCP tool call, deterministically and matching the committed
// golden. The mcp_call events are what the MCP-inventory insight reads to mark a
// server used.
func TestNormalizeTranscriptMCPGolden(t *testing.T) {
	data := transcriptFixtureNDJSON(t, mcpTranscriptFixture)
	receivedAt := time.Date(2026, 9, 12, 10, 0, 7, 0, time.UTC)

	first, err := NormalizeTranscript(data, receivedAt)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := NormalizeTranscript(data, receivedAt)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("normalisation must be deterministic")
	}
	if updateGolden() {
		writeGolden(t, "claude-code-2.1.269-mcp-tool-call-transcript.events.json", first)
	}
	assertMatchesGolden(t, "claude-code-2.1.269-mcp-tool-call-transcript.events.json", first)
}

// TestExtractTranscriptOperationsGolden proves the transcript operation path
// reconstructs one MCP-call Operation per invocation, deterministically and
// matching the committed golden.
func TestExtractTranscriptOperationsGolden(t *testing.T) {
	data := transcriptFixtureNDJSON(t, mcpTranscriptFixture)

	first, err := ExtractTranscriptOperations(data, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := ExtractTranscriptOperations(data, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("extraction must be deterministic")
	}
	if updateGolden() {
		writeGolden(t, "claude-code-2.1.269-mcp-tool-call-transcript.operations.json", first)
	}
	assertMatchesGolden(t, "claude-code-2.1.269-mcp-tool-call-transcript.operations.json", first)
}

// TestExtractTranscriptOperationsPairsAndClassifies proves the tool_use↔tool_result
// pairing: a paired success, a paired failure, and an unpaired call (outcome
// unknown, never fabricated), all classified as MCP calls with their server/tool
// split out. The non-MCP Bash tool_use must not become an Operation (J18/#105 owns
// generic tool IO).
func TestExtractTranscriptOperationsPairsAndClassifies(t *testing.T) {
	data := transcriptFixtureNDJSON(t, mcpTranscriptFixture)
	operations, err := ExtractTranscriptOperations(data, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	byID := operationsByID(operations)
	if len(operations) != 3 {
		t.Fatalf("operation count = %d, want 3 MCP calls (Bash tool_use is not an MCP operation)", len(operations))
	}
	const prefix = "claude-code:22222222-2222-4222-8222-222222222222:tool:"
	assertMCPOperation(t, byID, prefix+"toolu_mcp_read", "success", "synthetic-fs", "read_file")
	assertMCPOperation(t, byID, prefix+"toolu_mcp_write", "failed", "synthetic-fs", "write_file")
	assertMCPOperation(t, byID, prefix+"toolu_mcp_pending", "unknown", "synthetic-github", "list_issues")
	if _, present := byID[prefix+"toolu_bash_ls"]; present {
		t.Fatal("Bash tool_use must not become an MCP operation")
	}
}

// operationsByID indexes reconstructed operations by their OperationID so a case
// can look one up by its stable `<session>:tool:<tool_use_id>` key.
func operationsByID(operations []canonical.Operation) map[string]canonical.Operation {
	byID := map[string]canonical.Operation{}
	for _, operation := range operations {
		byID[operation.OperationID] = operation
	}
	return byID
}

// assertMCPOperation checks one reconstructed MCP-call operation: category,
// paired outcome, observed provenance, and the server/tool split under mcp_call.
func assertMCPOperation(t *testing.T, byID map[string]canonical.Operation, id, outcome, server, tool string) {
	t.Helper()
	operation, ok := byID[id]
	if !ok {
		t.Fatalf("missing operation %q", id)
	}
	if operation.Category != canonical.OperationCategoryMCPCall {
		t.Errorf("%s category = %q, want MCP call", id, operation.Category)
	}
	if operation.Outcome != outcome {
		t.Errorf("%s outcome = %q, want %q", id, operation.Outcome, outcome)
	}
	if operation.Provenance != canonical.ProvenanceObserved {
		t.Errorf("%s provenance = %q, want observed", id, operation.Provenance)
	}
	call, ok := operation.ProviderExtensions["mcp_call"].(map[string]any)
	if !ok || call["server_name"] != server || call["tool_name"] != tool {
		t.Errorf("%s mcp_call = %#v, want server %q tool %q", id, operation.ProviderExtensions["mcp_call"], server, tool)
	}
}

// TestExtractTranscriptOperationsCapturesRawArgumentsAndResult proves the raw MCP
// arguments and result body are captured under provider_extensions.mcp_call (epic
// #87 — #104 captures MCP arguments and results raw), while an unpaired call omits
// the result rather than fabricating one.
func TestExtractTranscriptOperationsCapturesRawArgumentsAndResult(t *testing.T) {
	data := transcriptFixtureNDJSON(t, mcpTranscriptFixture)
	operations, err := ExtractTranscriptOperations(data, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	byID := operationsByID(operations)
	read := byID["claude-code:22222222-2222-4222-8222-222222222222:tool:toolu_mcp_read"].ProviderExtensions["mcp_call"].(map[string]any)
	arguments, ok := read["arguments"].(map[string]any)
	if !ok || arguments["path"] != "docs/architecture/overview.md" {
		t.Fatalf("read arguments = %#v, want the raw path", read["arguments"])
	}
	if read["result"] == nil {
		t.Fatalf("paired read call must carry the raw result: %#v", read)
	}
	pending := byID["claude-code:22222222-2222-4222-8222-222222222222:tool:toolu_mcp_pending"].ProviderExtensions["mcp_call"].(map[string]any)
	if _, present := pending["result"]; present {
		t.Fatalf("unpaired call must omit result, got %#v", pending["result"])
	}
}

// TestNormalizeTranscriptEmitsMCPCorrelationEvents proves the event path emits an
// mcp_call event per MCP invocation carrying the server/tool identity the
// MCP-inventory matcher consumes, and that these events never carry the arguments
// or result body (that content rides on the Operation, keeping the event path
// content-free).
func TestNormalizeTranscriptEmitsMCPCorrelationEvents(t *testing.T) {
	data := transcriptFixtureNDJSON(t, mcpTranscriptFixture)
	events, err := NormalizeTranscript(data, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	var mcpEvents int
	for _, event := range events {
		if event.EventType != eventTypeMCPCall {
			continue
		}
		mcpEvents++
		if event.Attributes["category"] != string(canonical.OperationCategoryMCPCall) {
			t.Errorf("mcp_call event %q category = %#v", event.EventID, event.Attributes["category"])
		}
		call, ok := event.ProviderExtensions["mcp_call"].(map[string]any)
		if !ok || strings.TrimSpace(call["server_name"].(string)) == "" || strings.TrimSpace(call["tool_name"].(string)) == "" {
			t.Errorf("mcp_call event %q mcp_call = %#v", event.EventID, event.ProviderExtensions["mcp_call"])
		}
		if _, leaked := call["arguments"]; leaked {
			t.Errorf("mcp_call event %q must not carry arguments: %#v", event.EventID, call)
		}
		if _, leaked := call["result"]; leaked {
			t.Errorf("mcp_call event %q must not carry result: %#v", event.EventID, call)
		}
	}
	if mcpEvents != 3 {
		t.Fatalf("mcp_call event count = %d, want 3", mcpEvents)
	}
}

// TestExtractTranscriptOperationsDoesNotEmitNonMCPContent is the operation-path
// canary guard: a non-MCP tool_use body (a Bash command) must never surface in an
// MCP operation, so J18/#105's generic tool IO is not smuggled in through #104.
func TestExtractTranscriptOperationsDoesNotEmitNonMCPContent(t *testing.T) {
	data := transcriptFixtureNDJSON(t, mcpTranscriptFixture)
	operations, err := ExtractTranscriptOperations(data, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	encoded, err := json.Marshal(operations)
	if err != nil {
		t.Fatalf("marshal operations: %v", err)
	}
	for _, leaked := range []string{"ls docs", "toolu_bash_ls", "\"Bash\""} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("non-MCP content %q leaked into MCP operations: %s", leaked, encoded)
		}
	}
}

// TestExtractTranscriptOperationsNoMCPYieldsEmpty proves a transcript with only
// non-MCP tool calls produces no MCP operations and no error — the extractor never
// fabricates an operation for a tool it does not own.
func TestExtractTranscriptOperationsNoMCPYieldsEmpty(t *testing.T) {
	line := `{"type":"assistant","uuid":"a1","sessionId":"s1","timestamp":"2026-09-12T09:00:00Z","version":"2.1.269",` +
		`"message":{"model":"claude-opus-4-8","content":[{"type":"tool_use","id":"toolu_b","name":"Bash","input":{"command":"echo hi"}}],"usage":{"input_tokens":1,"output_tokens":1}}}`
	operations, err := ExtractTranscriptOperations([]byte(line), time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(operations) != 0 {
		t.Fatalf("operation count = %d, want 0 (no MCP calls)", len(operations))
	}
}

// TestExtractTranscriptOperationsMalformedAborts proves a malformed line aborts the
// operation walk the same way it aborts the event walk, so a caller never sees a
// half-extracted transcript.
func TestExtractTranscriptOperationsMalformedAborts(t *testing.T) {
	data := []byte("{ this is not json\n")
	if _, err := ExtractTranscriptOperations(data, time.Unix(0, 0).UTC()); err == nil {
		t.Fatal("want a malformed-transcript error, got nil")
	}
}
