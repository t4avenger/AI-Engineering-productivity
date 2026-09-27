package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wayne/telemetryiq/internal/insights"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
	"github.com/wayne/telemetryiq/internal/storage/sqlite"
)

// transcriptE2ESessionID is shared by the OTLP log and the JSONL transcript in
// the end-to-end gate: both must correlate into the one session by session.id
// string equality, proving the transcript path merges with OTLP rather than
// creating a parallel session.
const transcriptE2ESessionID = "transcript-e2e-session"

// e2eTranscriptNDJSON is a synthetic session JSONL transcript in the real
// on-disk newline-delimited format: a user prompt, an assistant record carrying
// the model + full token usage plus response/thinking text and a Bash + Edit
// tool_use, and a paired tool_result. Under epic #87 the content bodies
// (message.content[] text/thinking, tool input command/file_path/diff, tool_result
// stdout) are captured raw and must reach storage; only cwd — not one of the six
// #105 signals — stays excluded.
const e2eTranscriptNDJSON = `{"type":"user","uuid":"e2e-user-1","sessionId":"transcript-e2e-session","timestamp":"2026-09-12T10:00:00.000Z","version":"2.1.269","message":{"role":"user","content":"tiq-canary-user-prompt"}}
{"type":"assistant","uuid":"e2e-assistant-1","parentUuid":"e2e-user-1","sessionId":"transcript-e2e-session","timestamp":"2026-09-12T10:00:02.500Z","version":"2.1.269","cwd":"/home/tiq-canary-cwd/project","gitBranch":"main","entrypoint":"cli","requestId":"req_e2e_1","message":{"role":"assistant","model":"claude-opus-4-8","stop_reason":"end_turn","content":[{"type":"text","text":"tiq-canary-response"},{"type":"thinking","thinking":"tiq-canary-thinking"},{"type":"tool_use","id":"toolu_bash_e2e_1","name":"Bash","input":{"command":"tiq-canary-command"}},{"type":"tool_use","id":"toolu_edit_e2e_1","name":"Edit","input":{"file_path":"/tiq-canary-file-path","old_string":"before","new_string":"after"}}],"usage":{"input_tokens":4096,"output_tokens":512,"cache_read_input_tokens":8192,"cache_creation_input_tokens":128,"output_tokens_details":{"thinking_tokens":64}}}}
{"type":"user","uuid":"e2e-user-2","parentUuid":"e2e-assistant-1","sessionId":"transcript-e2e-session","timestamp":"2026-09-12T10:00:03.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_bash_e2e_1","is_error":false,"content":[{"type":"text","text":"tiq-canary-stdout"}]}]}}`

// transcriptCapturedContent are the raw content bodies epic #87 now retains and
// surfaces through storage: the user prompt, assistant response + thinking, and the
// tool IO (command, edit target path, command result).
var transcriptCapturedContent = []string{
	"tiq-canary-user-prompt",
	"tiq-canary-response",
	"tiq-canary-thinking",
	"tiq-canary-command",
	"tiq-canary-file-path",
	"tiq-canary-stdout",
}

// transcriptExcludedContent are the values that stay off every surface: cwd is not
// one of the six #105 signals (out of scope, flagged follow-up), so it must never
// reach storage, the read API, or the dev inspector.
var transcriptExcludedContent = []string{
	"tiq-canary-cwd",
}

// TestTranscriptImportMergesWithOTLPSession is the live end-to-end gate: it
// ingests a Claude OTLP log for a session, then POSTs a JSONL transcript for the
// SAME session to /v1/claude/transcript, and proves both merge into one
// anthropic/claude-code session whose transcript model + token counts surface
// through the read API. Per epic #87 it also proves the raw content bodies (prompt,
// response, thinking, tool command/path/result) are captured to storage, while cwd
// stays off every surface including the dev inspector.
func TestTranscriptImportMergesWithOTLPSession(t *testing.T) {
	repository, server := transcriptTestServer(t, true)
	postAcceptedOTLP(t, server.URL, "/v1/logs", claudeOTLPLogPayload(t, []any{
		otlpStringAttr("event.name", "api_request"),
		otlpStringAttr("event.timestamp", "2026-09-12T09:59:58.000Z"),
		otlpStringAttr("session.id", transcriptE2ESessionID),
		otlpStringAttr("model", "claude-opus-4-8"),
	}))
	postAcceptedTranscript(t, server.URL, e2eTranscriptNDJSON)

	wantSessionID := "claude-code:" + transcriptE2ESessionID
	sessions := requireMergedClaudeSession(t, repository, wantSessionID)
	assertSessionHeaderMetadata(t, sessions[0], "cli", "main")
	detail := getInsightJSON[sessionDetailResponse](t, server.URL+"/api/v1/sessions/"+wantSessionID)
	assertSessionHeaderAvailability(t, detail.Data.Availability, "observed", "unavailable")
	timeline := getInsightJSON[eventListResponse](t, server.URL+"/api/v1/sessions/"+wantSessionID+"/events")
	assertTranscriptAssistantTokens(t, requireTranscriptAssistantEvent(t, timeline))
	assertTranscriptContentCapture(t, repository, wantSessionID, timeline, sessions, server.URL)
	if sessions[0].State == "" {
		t.Fatalf("session state must not be empty: %#v", sessions[0])
	}
}

// mcpTranscriptE2ENDJSON is a synthetic MCP-bearing transcript: an assistant
// record invoking one MCP tool (mcp__canary-fs__read_file) alongside a non-MCP
// Bash tool_use, paired with a later user tool_result. Under epic #87 the prompt
// and the Bash command are captured raw (the Bash call becomes a generic shell
// operation); only cwd — not a #105 signal — stays off the read API.
const mcpTranscriptE2ENDJSON = `{"type":"user","uuid":"mcp-e2e-user-1","sessionId":"mcp-transcript-e2e-session","timestamp":"2026-09-12T10:00:00.000Z","version":"2.1.269","message":{"role":"user","content":"tiq-canary-mcp-prompt"}}
{"type":"assistant","uuid":"mcp-e2e-assistant-1","parentUuid":"mcp-e2e-user-1","sessionId":"mcp-transcript-e2e-session","timestamp":"2026-09-12T10:00:02.000Z","version":"2.1.269","cwd":"/home/tiq-canary-mcp-cwd/project","gitBranch":"main","entrypoint":"cli","requestId":"req_mcp_e2e_1","message":{"role":"assistant","model":"claude-opus-4-8","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_mcp_e2e","name":"mcp__canary-fs__read_file","input":{"path":"docs/overview.md"}},{"type":"tool_use","id":"toolu_bash_e2e","name":"Bash","input":{"command":"tiq-canary-mcp-command"}}],"usage":{"input_tokens":64,"output_tokens":8}}}
{"type":"user","uuid":"mcp-e2e-user-2","parentUuid":"mcp-e2e-assistant-1","sessionId":"mcp-transcript-e2e-session","timestamp":"2026-09-12T10:00:03.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_mcp_e2e","is_error":false,"content":[{"type":"text","text":"# Overview"}]}]}}`

var mcpTranscriptExcludedContent = []string{
	"tiq-canary-mcp-cwd",
}

// TestTranscriptImportSurfacesMCPCallsThroughReadAPI is the live ingest→read gate
// for #104: it POSTs an MCP-bearing JSONL transcript to /v1/claude/transcript,
// then proves the reconstructed MCP call surfaces through both the operations
// insight (an MCP-call operation) and the MCP-inventory insight (the connected-but-
// unused vs used state now reports the server as used with its invocation count).
// Under #105 the sibling non-MCP Bash tool_use now also becomes a generic shell
// operation, so the transcript yields two operations; only cwd stays off the read API.
func TestTranscriptImportSurfacesMCPCallsThroughReadAPI(t *testing.T) {
	repository, server := transcriptTestServer(t, true)
	postAcceptedTranscript(t, server.URL, mcpTranscriptE2ENDJSON)

	stats := getInsightJSON[operationStatsResponse](t, server.URL+"/api/v1/insights/operations")
	if stats.Data.Totals.TotalOperations != 2 {
		t.Fatalf("operation totals = %#v, want 2 (one MCP-call + one generic shell operation)", stats.Data.Totals)
	}
	if count := operationCategoryCount(stats.Data.ByCategory, string(canonical.OperationCategoryMCPCall)); count != 1 {
		t.Fatalf("MCP-call category count = %d, want 1: %#v", count, stats.Data.ByCategory)
	}
	if count := operationCategoryCount(stats.Data.ByCategory, string(canonical.OperationCategoryShellCommand)); count != 1 {
		t.Fatalf("shell-command category count = %d, want 1 (Bash tool_use is now a generic operation): %#v", count, stats.Data.ByCategory)
	}

	inventory := fetchMCPInventory(t, server.URL)
	usedServer := requireMCPServer(t, inventory, "canary-fs")
	if !usedServer.Used || usedServer.InvocationCount != 1 {
		t.Fatalf("server used/invocations = %v/%d, want true/1: %#v", usedServer.Used, usedServer.InvocationCount, usedServer)
	}
	if usedServer.UsageState != "observed" {
		t.Fatalf("usage state = %q, want observed", usedServer.UsageState)
	}
	if !containsString(usedServer.ToolNames, "read_file") {
		t.Fatalf("tool names = %#v, want read_file", usedServer.ToolNames)
	}
	if inventory.Data.Totals.UsedServers != 1 {
		t.Fatalf("used servers = %d, want 1", inventory.Data.Totals.UsedServers)
	}

	storedEvents, err := repository.ListEvents(context.Background(), storage.EventFilter{SessionID: "claude-code:mcp-transcript-e2e-session", Limit: 20})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	storedOperations, err := repository.ListOperations(context.Background(), storage.OperationFilter{})
	if err != nil {
		t.Fatalf("list operations: %v", err)
	}
	assertNoRawIdentifiers(t, mcpTranscriptExcludedContent,
		marshalJSON(t, stats), marshalJSON(t, inventory), marshalJSON(t, storedEvents), marshalJSON(t, storedOperations))
	lastIngest := getInsightJSON[map[string]any](t, server.URL+"/api/v1/development/last-ingest")
	assertNoRawIdentifiers(t, mcpTranscriptExcludedContent, marshalJSON(t, lastIngest))
}

// genericToolTranscriptNDJSON is a synthetic transcript exercising the full generic
// tool-call surface (#105): a Read, a Write, a Bash, and an unrecognised Task
// tool_use on one assistant record. It proves every built-in tool_use is promoted to
// an Operation (never dropped) and classified through the shared operationCategory
// helper, and that the Read/Write file paths reach the Files-lane read API.
const genericToolTranscriptNDJSON = `{"type":"user","uuid":"gen-e2e-user-1","sessionId":"generic-tool-e2e-session","timestamp":"2026-09-12T10:00:00.000Z","version":"2.1.269","message":{"role":"user","content":"list the tool calls"}}
{"type":"assistant","uuid":"gen-e2e-assistant-1","parentUuid":"gen-e2e-user-1","sessionId":"generic-tool-e2e-session","timestamp":"2026-09-12T10:00:02.000Z","version":"2.1.269","cwd":"/repo","gitBranch":"main","entrypoint":"cli","requestId":"req_gen_e2e_1","message":{"role":"assistant","model":"claude-opus-4-8","stop_reason":"tool_use","content":[{"type":"tool_use","id":"tu_read","name":"Read","input":{"file_path":"/repo/read_target.go"}},{"type":"tool_use","id":"tu_write","name":"Write","input":{"file_path":"/repo/write_target.go","content":"package main"}},{"type":"tool_use","id":"tu_bash","name":"Bash","input":{"command":"go build ./..."}},{"type":"tool_use","id":"tu_task","name":"Task","input":{"description":"investigate","subagent_type":"Explore","prompt":"look"}}],"usage":{"input_tokens":32,"output_tokens":8}}}`

// TestTranscriptImportSurfacesGenericToolCallsThroughReadAPI is the live ingest→read
// gate for #105: it POSTs a transcript carrying Read/Write/Bash/Task tool_use blocks
// and proves each surfaces as an Operation in the correct by_category bucket (a
// generic Operation is promoted even for the unrecognised Task), and that the
// Read/Write target paths flow through the Files-lane read API with zero changes to
// session_files.go — the direct evidence the JSONL path now feeds the File
// operations capability.
func TestTranscriptImportSurfacesGenericToolCallsThroughReadAPI(t *testing.T) {
	_, server := transcriptTestServer(t, false)
	postAcceptedTranscript(t, server.URL, genericToolTranscriptNDJSON)

	stats := getInsightJSON[operationStatsResponse](t, server.URL+"/api/v1/insights/operations")
	if stats.Data.Totals.TotalOperations != 4 {
		t.Fatalf("operation totals = %#v, want 4 (Read, Write, Bash, Task)", stats.Data.Totals)
	}
	for _, tc := range []struct {
		category string
		want     int
	}{
		{string(canonical.OperationCategoryFilesystemRead), 1},
		{string(canonical.OperationCategoryFilesystemWrite), 1},
		{string(canonical.OperationCategoryShellCommand), 1},
		{string(canonical.OperationCategoryUnknown), 1},
	} {
		if count := operationCategoryCount(stats.Data.ByCategory, tc.category); count != tc.want {
			t.Fatalf("%s category count = %d, want %d: %#v", tc.category, count, tc.want, stats.Data.ByCategory)
		}
	}

	files := getFileList(t, server.URL+"/api/v1/sessions/claude-code:generic-tool-e2e-session/files")
	for _, wantPath := range []string{"/repo/read_target.go", "/repo/write_target.go"} {
		if !fileListHasPath(files, wantPath) {
			t.Fatalf("Files-lane read API missing transcript-sourced path %q: %#v", wantPath, files.Data)
		}
	}
}

// fileListHasPath reports whether any Files-lane row carries the given path.
func fileListHasPath(files fileListResponse, want string) bool {
	for _, entry := range files.Data {
		if entry.Path != nil && *entry.Path == want {
			return true
		}
	}
	return false
}

func operationCategoryCount(categories []insights.OperationCategoryStat, category string) int {
	for _, stat := range categories {
		if stat.Category == category {
			return stat.Count
		}
	}
	return 0
}

func requireMCPServer(t *testing.T, inventory mcpInventoryResponse, serverName string) insights.MCPServer {
	t.Helper()
	for _, server := range inventory.Data.Servers {
		if server.ServerName == serverName {
			return server
		}
	}
	t.Fatalf("no MCP server %q in inventory: %#v", serverName, inventory.Data.Servers)
	return insights.MCPServer{}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestTranscriptImportRejectsWrongMediaType proves the route rejects a body that
// is not NDJSON with 415 and increments the shared rejected counter, so a
// misconfigured client is refused rather than silently mis-parsed.
func TestTranscriptImportRejectsWrongMediaType(t *testing.T) {
	_, server := transcriptTestServer(t, false)

	resp := postOTLPToPath(t, server.URL, "/v1/claude/transcript", []byte(e2eTranscriptNDJSON), "application/json")
	assertIngestError(t, resp, http.StatusUnsupportedMediaType, "unsupported_media_type")

	counters := getInsightJSON[ingestCountersResponse](t, server.URL+"/api/v1/ingest/counters")
	if counters.RejectedPayloads != 1 {
		t.Fatalf("rejected payloads = %d, want 1 (shared with OTLP counters)", counters.RejectedPayloads)
	}
}

// TestTranscriptImportRejectsMalformedJSONWith400 mirrors OTLP: invalid JSONL
// syntax is malformed_payload (400), not normalization_failed (422).
func TestTranscriptImportRejectsMalformedJSONWith400(t *testing.T) {
	_, server := transcriptTestServer(t, false)
	resp := postOTLPToPath(t, server.URL, "/v1/claude/transcript", []byte("not-json\n"), "application/x-ndjson")
	assertIngestError(t, resp, http.StatusBadRequest, "malformed_payload")
}

// TestTranscriptImportRejectsStructuralFailureWith422 reserves 422 for valid
// JSON whose supported assistant records are missing required fields.
func TestTranscriptImportRejectsStructuralFailureWith422(t *testing.T) {
	_, server := transcriptTestServer(t, false)
	body := `{"type":"assistant","uuid":"a1","sessionId":"s1","message":{"model":"claude-opus-4-8"}}`
	resp := postOTLPToPath(t, server.URL, "/v1/claude/transcript", []byte(body), "application/x-ndjson")
	assertIngestError(t, resp, http.StatusUnprocessableEntity, "normalization_failed")
}

// TestTranscriptImportSharesAcceptedCounter proves a successful transcript import
// increments the same accepted counter the OTLP routes use, so ingest
// observability stays consistent across every push route.
func TestTranscriptImportSharesAcceptedCounter(t *testing.T) {
	_, server := transcriptTestServer(t, false)
	postAcceptedTranscript(t, server.URL, e2eTranscriptNDJSON)

	counters := getInsightJSON[ingestCountersResponse](t, server.URL+"/api/v1/ingest/counters")
	if counters.AcceptedPayloads != 1 {
		t.Fatalf("accepted payloads = %d, want 1 (shared with OTLP counters)", counters.AcceptedPayloads)
	}
}

func transcriptTestServer(t *testing.T, development bool) (*sqlite.Repository, *httptest.Server) {
	t.Helper()
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	var handler http.Handler
	if development {
		handler = NewPersistentDevelopmentHandler(slog.Default(), repository)
	} else {
		handler = NewPersistentHandler(slog.Default(), repository)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return repository, server
}

func postAcceptedTranscript(t *testing.T, baseURL, body string) {
	t.Helper()
	resp := postOTLPToPath(t, baseURL, "/v1/claude/transcript", []byte(body), "application/x-ndjson")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("transcript import status = %d, want 202", resp.StatusCode)
	}
	closeBody(t, resp)
}

func requireMergedClaudeSession(t *testing.T, repository storage.Repository, wantSessionID string) []canonical.Session {
	t.Helper()
	sessions, err := repository.ListSessions(context.Background(), storage.SessionFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("session count = %d, want 1 (OTLP + transcript merged): %#v", len(sessions), sessions)
	}
	if sessions[0].Provider != "anthropic" || sessions[0].Tool != "claude-code" {
		t.Fatalf("session provider/tool = %q/%q", sessions[0].Provider, sessions[0].Tool)
	}
	if sessions[0].SessionID != wantSessionID {
		t.Fatalf("session id = %q, want %q", sessions[0].SessionID, wantSessionID)
	}
	return sessions
}

func assertSessionHeaderMetadata(t *testing.T, session canonical.Session, entrypoint, branch string) {
	t.Helper()
	if session.Attributes["entrypoint"] != entrypoint || session.Attributes["git_branch"] != branch {
		t.Fatalf("session header metadata = %#v, want entrypoint=%q git_branch=%q", session.Attributes, entrypoint, branch)
	}
	if _, ok := session.Attributes["pr_link"]; ok {
		t.Fatalf("must not invent pr_link: %#v", session.Attributes)
	}
}

func assertSessionHeaderAvailability(t *testing.T, availability map[string]string, entrypointAndBranch, prLink string) {
	t.Helper()
	if availability["entrypoint"] != entrypointAndBranch || availability["git_branch"] != entrypointAndBranch {
		t.Fatalf("header availability = %#v, want entrypoint/git_branch=%q", availability, entrypointAndBranch)
	}
	if availability["pr_link"] != prLink {
		t.Fatalf("pr_link availability = %q, want %q", availability["pr_link"], prLink)
	}
}

func assertTranscriptAssistantTokens(t *testing.T, assistant timelineEvent) {
	t.Helper()
	if assistant.Model == nil || *assistant.Model != "claude-opus-4-8" {
		t.Fatalf("transcript event model = %#v", assistant.Model)
	}
	if assistant.InputTokenCount == nil || *assistant.InputTokenCount != "4096" {
		t.Fatalf("transcript input token count = %#v", assistant.InputTokenCount)
	}
	if assistant.OutputTokenCount == nil || *assistant.OutputTokenCount != "512" {
		t.Fatalf("transcript output token count = %#v", assistant.OutputTokenCount)
	}
}

func assertTranscriptContentCapture(t *testing.T, repository storage.Repository, sessionID string, timeline eventListResponse, sessions []canonical.Session, baseURL string) {
	t.Helper()
	events, err := repository.ListEvents(context.Background(), storage.EventFilter{SessionID: sessionID, Limit: 20})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	operations, err := repository.ListOperations(context.Background(), storage.OperationFilter{SessionID: sessionID})
	if err != nil {
		t.Fatalf("list operations: %v", err)
	}
	// Epic #87: the raw content bodies must be retained on the stored events and
	// operations.
	assertContainsAll(t, transcriptCapturedContent, marshalJSON(t, events), marshalJSON(t, operations))
	// cwd stays excluded from every read surface and the dev inspector.
	lastIngest := getInsightJSON[map[string]any](t, baseURL+"/api/v1/development/last-ingest")
	assertNoRawIdentifiers(t, transcriptExcludedContent,
		marshalJSON(t, timeline), marshalJSON(t, sessions), marshalJSON(t, events), marshalJSON(t, operations), marshalJSON(t, lastIngest))
}

// assertContainsAll fails unless every required value appears verbatim in at least
// one document — the raw-capture mirror of assertNoRawIdentifiers.
func assertContainsAll(t *testing.T, required []string, documents ...[]byte) {
	t.Helper()
	for _, want := range required {
		found := false
		for _, document := range documents {
			if strings.Contains(string(document), want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("raw-capture gap: %q not present in stored read output", want)
		}
	}
}

func requireTranscriptAssistantEvent(t *testing.T, timeline eventListResponse) timelineEvent {
	t.Helper()
	for _, event := range timeline.Data {
		if event.EventType == "assistant_message" {
			return event
		}
	}
	t.Fatalf("no assistant_message event in timeline: %#v", timeline.Data)
	return timelineEvent{}
}
