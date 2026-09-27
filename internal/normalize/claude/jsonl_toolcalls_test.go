package claude

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

const toolIOTranscriptFixture = "claude-code-2.1.269-tool-io-diffs-subagent-transcript.json"

// TestNormalizeToolCallTranscriptGolden proves the generic tool-call event path
// (J18/#105) emits the assistant/user_message events plus one content-free
// tool_call correlation event per non-MCP tool_use, deterministically and matching
// the committed golden.
func TestNormalizeToolCallTranscriptGolden(t *testing.T) {
	data := transcriptFixtureNDJSONFrom(t, "synthetic", toolIOTranscriptFixture)
	receivedAt := time.Date(2026, 9, 12, 11, 0, 11, 0, time.UTC)

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
	assertMatchesGolden(t, "claude-code-2.1.269-tool-io-diffs-subagent-transcript.events.json", first)
}

// TestExtractToolCallOperationsGolden proves the generic tool-call operation path
// reconstructs one Operation per tool_use (Read/Edit/Write/Bash/TodoWrite/Task and
// the sidechain Grep), deterministically and matching the committed golden.
func TestExtractToolCallOperationsGolden(t *testing.T) {
	data := transcriptFixtureNDJSONFrom(t, "synthetic", toolIOTranscriptFixture)

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
	assertMatchesGolden(t, "claude-code-2.1.269-tool-io-diffs-subagent-transcript.operations.json", first)
}

// TestToolCallClassification proves every built-in tool_use is classified through
// the shared operationCategory helper (no second classifier): the file tools map to
// filesystem read/write, Bash to shell command, WebFetch to network request, an
// mcp__* name to an MCP call, and an unrecognised tool (Task) stays unknown — a
// generic Operation is still promoted, never dropped.
func TestToolCallClassification(t *testing.T) {
	cases := []struct {
		name string
		want canonical.OperationCategory
	}{
		{"Read", canonical.OperationCategoryFilesystemRead},
		{"Glob", canonical.OperationCategoryFilesystemRead},
		{"Grep", canonical.OperationCategoryFilesystemRead},
		{"NotebookRead", canonical.OperationCategoryFilesystemRead},
		{"Write", canonical.OperationCategoryFilesystemWrite},
		{"Edit", canonical.OperationCategoryFilesystemWrite},
		{"MultiEdit", canonical.OperationCategoryFilesystemWrite},
		{"NotebookEdit", canonical.OperationCategoryFilesystemWrite},
		{"Bash", canonical.OperationCategoryShellCommand},
		{"WebFetch", canonical.OperationCategoryNetworkRequest},
		{"WebSearch", canonical.OperationCategoryNetworkRequest},
		{"mcp__srv__do", canonical.OperationCategoryMCPCall},
		{"Task", canonical.OperationCategoryUnknown},
	}
	var blocks []string
	for i, tc := range cases {
		blocks = append(blocks, `{"type":"tool_use","id":"tu`+itoa(i)+`","name":"`+tc.name+`","input":{"k":"v"}}`)
	}
	line := `{"type":"assistant","uuid":"a1","sessionId":"s1","timestamp":"2026-09-12T09:00:00Z","version":"2.1.269",` +
		`"message":{"model":"claude-opus-4-8","content":[` + strings.Join(blocks, ",") + `]}}`

	operations, err := ExtractTranscriptOperations([]byte(line), time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	byID := operationsByID(operations)
	for i, tc := range cases {
		id := "claude-code:s1:tool:tu" + itoa(i)
		operation, ok := byID[id]
		if !ok {
			t.Fatalf("%s: missing operation %q", tc.name, id)
		}
		if operation.Category != tc.want {
			t.Errorf("%s category = %q, want %q", tc.name, operation.Category, tc.want)
		}
	}
}

// itoa renders a small non-negative int without importing strconv into the test.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// TestNormalizeTranscriptCapturesThinkingAndSlash proves the message-content path:
// an assistant record's thinking block lands under transcript.thinking (present
// only, no unavailable placeholder), and a slash-command-expanded user prompt is
// captured raw under the user_message event's transcript.prompt_content.
func TestNormalizeTranscriptCapturesThinkingAndSlash(t *testing.T) {
	data := transcriptFixtureNDJSONFrom(t, "synthetic", toolIOTranscriptFixture)
	events, err := NormalizeTranscript(data, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	assistant := firstEventOfType(t, events, eventTypeAssistantMessage)
	transcript := assistant.ProviderExtensions["transcript"].(map[string]any)
	if thinking, _ := transcript["thinking"].(string); !strings.Contains(thinking, "Plan: read the module") {
		t.Fatalf("assistant thinking not captured: %#v", transcript["thinking"])
	}
	user := firstEventOfType(t, events, eventTypeUserMessage)
	userTranscript := user.ProviderExtensions["transcript"].(map[string]any)
	prompt, _ := userTranscript["prompt_content"].(string)
	if !strings.Contains(prompt, "<command-name>/review</command-name>") || !strings.Contains(prompt, "Review the recent changes") {
		t.Fatalf("slash-expanded prompt not captured raw: %#v", userTranscript["prompt_content"])
	}
}

// TestNormalizeTranscriptCapturesDiffAndTodoRaw proves Edit/Write diffs and a
// TodoWrite task-list snapshot are captured raw under tool_call.input on the
// Operation as a single copy, satisfying the diff and task-list capture requirement.
func TestNormalizeTranscriptCapturesDiffAndTodoRaw(t *testing.T) {
	data := transcriptFixtureNDJSONFrom(t, "synthetic", toolIOTranscriptFixture)
	operations, err := ExtractTranscriptOperations(data, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	byID := operationsByID(operations)
	const prefix = "claude-code:33333333-3333-4333-8333-333333333333:tool:"

	edit := toolCallInput(t, byID, prefix+"toolu_edit")
	if edit["old_string"] != "// module" || edit["new_string"] != "// module: reviewed" {
		t.Errorf("Edit diff not captured raw: %#v", edit)
	}
	write := toolCallInput(t, byID, prefix+"toolu_write")
	if !strings.Contains(write["content"].(string), "Reviewed the module") {
		t.Errorf("Write content not captured raw: %#v", write)
	}
	todo := toolCallInput(t, byID, prefix+"toolu_todo")
	todos, ok := todo["todos"].([]any)
	if !ok || len(todos) != 2 {
		t.Fatalf("TodoWrite snapshot not captured raw: %#v", todo["todos"])
	}
}

// toolCallInput fetches the raw tool_call.input map for one operation id.
func toolCallInput(t *testing.T, byID map[string]canonical.Operation, id string) map[string]any {
	t.Helper()
	operation, ok := byID[id]
	if !ok {
		t.Fatalf("missing operation %q", id)
	}
	call, ok := operation.ProviderExtensions["tool_call"].(map[string]any)
	if !ok {
		t.Fatalf("%s missing tool_call: %#v", id, operation.ProviderExtensions)
	}
	input, ok := call["input"].(map[string]any)
	if !ok {
		t.Fatalf("%s missing tool_call.input: %#v", id, call)
	}
	return input
}

// TestToolCallSidechainCaptured proves a sub-agent (sidechain) tool_use flows
// through the same collector and self-identifies as sub-agent work on both the
// operation and the event: the Grep operation's event block carries is_sidechain
// plus the parent_uuid pointing at the sidechain assistant record, and the tool_call
// event carries the same is_sidechain marker the assistant_message event uses — so
// the conversation tree can attribute it to the sub-agent without a separate
// sidechain walker or a multi-hop join.
func TestToolCallSidechainCaptured(t *testing.T) {
	data := transcriptFixtureNDJSONFrom(t, "synthetic", toolIOTranscriptFixture)
	operations, err := ExtractTranscriptOperations(data, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	byID := operationsByID(operations)
	grep, ok := byID["claude-code:33333333-3333-4333-8333-333333333333:tool:toolu_grep"]
	if !ok {
		t.Fatal("sidechain Grep tool_use must become an Operation")
	}
	if grep.Category != canonical.OperationCategoryFilesystemRead {
		t.Errorf("Grep category = %q, want filesystem read", grep.Category)
	}
	event, ok := grep.ProviderExtensions["event"].(map[string]any)
	if !ok {
		t.Fatalf("Grep operation missing event block: %#v", grep.ProviderExtensions)
	}
	if event["is_sidechain"] != true {
		t.Errorf("Grep operation event.is_sidechain = %#v, want true", event["is_sidechain"])
	}
	if event["parent_uuid"] != "a8000000-0000-4000-8000-000000000008" {
		t.Errorf("Grep operation event.parent_uuid = %#v, want the sidechain parent", event["parent_uuid"])
	}

	events, err := NormalizeTranscript(data, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	grepEvent, ok := eventByID(events, "claude-code:33333333-3333-4333-8333-333333333333:toolcall:toolu_grep")
	if !ok {
		t.Fatal("sidechain Grep tool_use must become a tool_call event")
	}
	transcript, ok := grepEvent.ProviderExtensions["transcript"].(map[string]any)
	if !ok || transcript["is_sidechain"] != true {
		t.Fatalf("Grep tool_call event missing is_sidechain marker: %#v", grepEvent.ProviderExtensions)
	}
}

// TestToolCallDuplicateToolUseIDLastSeenWins documents the flat-collector contract:
// when the same tool_use_id appears twice (a main-line record and a sidechain
// record), the last-seen invocation wins, so a duplicate id yields exactly one
// deterministic Operation rather than two conflicting ones.
func TestToolCallDuplicateToolUseIDLastSeenWins(t *testing.T) {
	data := []byte(strings.Join([]string{
		`{"type":"assistant","uuid":"a1","sessionId":"s1","timestamp":"2026-09-12T09:00:00Z","version":"2.1.269",` +
			`"message":{"model":"claude-opus-4-8","content":[{"type":"tool_use","id":"dup","name":"Read","input":{"file_path":"first.go"}}]}}`,
		`{"type":"assistant","uuid":"a2","sessionId":"s1","timestamp":"2026-09-12T09:00:01Z","version":"2.1.269","isSidechain":true,"parentUuid":"a1",` +
			`"message":{"model":"claude-opus-4-8","content":[{"type":"tool_use","id":"dup","name":"Read","input":{"file_path":"second.go"}}]}}`,
	}, "\n"))
	operations, err := ExtractTranscriptOperations(data, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(operations) != 1 {
		t.Fatalf("operation count = %d, want 1 (last-seen wins for a duplicate tool_use_id)", len(operations))
	}
	input := toolCallInput(t, operationsByID(operations), "claude-code:s1:tool:dup")
	if input["file_path"] != "second.go" {
		t.Errorf("duplicate id file_path = %#v, want the last-seen invocation", input["file_path"])
	}
}
