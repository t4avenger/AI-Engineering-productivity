package codex

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestExtractRolloutOperationsGolden(t *testing.T) {
	t.Parallel()
	data := rolloutOperationsFixture(t)
	operations, err := ExtractRolloutOperations(data, fixtureReceivedAt)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(operations)
	if err != nil {
		t.Fatal(err)
	}
	var projection []map[string]any
	if err := json.Unmarshal(encoded, &projection); err != nil {
		t.Fatal(err)
	}
	assertGoldenJSON(t, "codex-0.159.2-rollout-operations.operations.json", projection, "rollout operations")
}

func TestNormalizeRolloutEvidenceProjectsOperations(t *testing.T) {
	data := rolloutOperationsFixture(t)
	events, operations, err := NormalizeRolloutEvidence(data, fixtureReceivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 5 {
		t.Fatalf("operations = %d, want 5", len(operations))
	}
	want := map[string]struct {
		category canonical.OperationCategory
		outcome  string
	}{
		"file-change":   {canonical.OperationCategoryFilesystemWrite, "success"},
		"mcp-failed":    {canonical.OperationCategoryMCPCall, "failed"},
		"mcp-success":   {canonical.OperationCategoryMCPCall, "success"},
		"shell-failed":  {canonical.OperationCategoryShellCommand, "failed"},
		"shell-success": {canonical.OperationCategoryShellCommand, "success"},
	}
	for _, operation := range operations {
		shortID := operationIDLastSegment(operation.OperationID)
		expected, ok := want[shortID]
		if !ok || operation.Category != expected.category || operation.Outcome != expected.outcome {
			t.Errorf("operation %q = %q/%q", operation.OperationID, operation.Category, operation.Outcome)
		}
	}
	assertRolloutDerivedEvents(t, events)

	replayedEvents, replayedOperations, err := NormalizeRolloutEvidence(data, fixtureReceivedAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(eventIDs(events), eventIDs(replayedEvents)) || !reflect.DeepEqual(operations, replayedOperations) {
		t.Fatal("rollout operation replay must be deterministic")
	}
}

func TestExtractRolloutOperationsLegacyPairsExactCallID(t *testing.T) {
	data := []byte(
		`{"timestamp":"2026-09-29T07:01:40Z","type":"session_meta","payload":{"id":"legacy","cli_version":"0.157.1"}}` + "\n" +
			`{"timestamp":"2026-09-29T07:01:41Z","type":"response_item","payload":{"type":"custom_tool_call","id":"invocation-one","call_id":"call-one","name":"exec","input":"printf synthetic"}}` + "\n" +
			`{"timestamp":"2026-09-29T07:01:42Z","type":"response_item","payload":{"type":"custom_tool_call_output","id":"result-one","call_id":"call-one","output":[{"type":"input_text","text":"synthetic"}]}}` + "\n" +
			`{"timestamp":"2026-09-29T07:01:43Z","type":"response_item","payload":{"type":"custom_tool_call","id":"invocation-pending","call_id":"call-pending","name":"future_tool","input":{"raw":true}}}` + "\n")
	operations, err := ExtractRolloutOperations(data, fixtureReceivedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 2 {
		t.Fatalf("operations = %d, want 2", len(operations))
	}
	if operations[0].OperationID != "codex:legacy:tool:call-one" || operations[0].Outcome != "unknown" {
		t.Fatalf("paired operation = %#v", operations[0])
	}
	call := operations[0].ProviderExtensions["tool_call"].(map[string]any)
	if call["result"] == nil {
		t.Fatalf("exact-ID result not retained: %#v", call)
	}
	if operations[1].OperationID != "codex:legacy:tool:call-pending" || operations[1].Outcome != "unknown" {
		t.Fatalf("pending operation = %#v", operations[1])
	}
}

func assertRolloutDerivedEvents(t *testing.T, events []canonical.Event) {
	t.Helper()
	var mcpCalls, fileChanges int
	paths := map[string]string{}
	for _, event := range events {
		switch event.EventType {
		case "mcp_call":
			mcpCalls++
			call := event.ProviderExtensions["mcp_call"].(map[string]any)
			if call["server_name"] != "tiq_probe" || call["tool_name"] != "echo_probe" {
				t.Errorf("MCP identity = %#v", call)
			}
		case "tool_call":
			tool, _ := event.Attributes["tool"].(map[string]any)
			if path, _ := tool["file_path"].(string); path != "" {
				fileChanges++
				paths[path] = event.Attributes["category"].(string)
			}
		}
	}
	if mcpCalls != 2 || fileChanges != 2 {
		t.Fatalf("derived MCP/file events = %d/%d, want 2/2", mcpCalls, fileChanges)
	}
	if paths["/tmp/synthetic-codex-workspace/deleted.txt"] != string(canonical.OperationCategoryFilesystemDelete) ||
		paths["/tmp/synthetic-codex-workspace/probe.txt"] != string(canonical.OperationCategoryFilesystemWrite) {
		t.Fatalf("file categories = %#v", paths)
	}
}

func rolloutOperationsFixture(t testing.TB) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(codexFixturesDir(t), "observed-sanitised", "codex-0.159.2-rollout-operations.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func operationIDLastSegment(id string) string {
	for index := len(id) - 1; index >= 0; index-- {
		if id[index] == ':' {
			return id[index+1:]
		}
	}
	return id
}

func TestRolloutOperationLargeRawNumber(t *testing.T) {
	data := []byte(`{"timestamp":"2026-10-01T18:19:32Z","type":"session_meta","payload":{"id":"large","cli_version":"0.159.2"}}` + "\n" +
		`{"timestamp":"2026-10-01T18:19:33Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"McpToolCall","id":"mcp","server":"s","tool":"t","arguments":{"offset":9007199254740993},"status":"completed"}}}` + "\n")
	operations, err := ExtractRolloutOperations(data, fixtureReceivedAt)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(operations)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(encoded) || !bytes.Contains(encoded, []byte("9007199254740993")) {
		t.Fatalf("large raw number not retained exactly: %s", encoded)
	}
}
