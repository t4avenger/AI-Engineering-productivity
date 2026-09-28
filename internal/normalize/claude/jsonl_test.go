package claude

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// FuzzNormalizeTranscript keeps the JSONL normaliser boundaries from panicking on
// arbitrary input (QUALITY_GATES: fuzz smoke when normalisation changes). Both the
// event path (NormalizeTranscript) and the MCP operation path
// (ExtractTranscriptOperations) share the same line walk, so both are exercised.
func FuzzNormalizeTranscript(f *testing.F) {
	f.Add([]byte(`{"type":"assistant","uuid":"a1","sessionId":"s1","timestamp":"2026-09-12T09:00:00Z","version":"2.1.269","message":{"model":"claude-opus-4-8","usage":{"input_tokens":1,"output_tokens":2}}}`))
	f.Add([]byte("not json\n{\"type\":\"user\"}"))
	f.Add([]byte(""))
	f.Add([]byte("{\"type\":\"assistant\"}"))
	// An MCP tool_use with no paired tool_result, then the pairing result — exercises
	// the cross-line MCP pairing/outcome walk added by #104 (J17).
	f.Add([]byte(`{"type":"assistant","uuid":"a1","sessionId":"s1","timestamp":"2026-09-12T09:00:00Z","version":"2.1.269","message":{"model":"claude-opus-4-8","content":[{"type":"tool_use","id":"t1","name":"mcp__srv__do","input":{"k":"v"}}]}}` +
		"\n" +
		`{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"2026-09-12T09:00:01Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":[{"type":"text","text":"x"}]}]}}`))
	// Generic tool IO added by J18 (#105): a thinking block, an Edit diff, a
	// TodoWrite snapshot, and a sidechain (sub-agent) Task tool_use — exercises the
	// generalized collector, content decode, and the user_message/thinking paths.
	f.Add([]byte(`{"type":"assistant","uuid":"a1","sessionId":"s1","timestamp":"2026-09-12T09:00:00Z","version":"2.1.269","message":{"model":"claude-opus-4-8","content":[{"type":"thinking","thinking":"reason"},{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":"a.go","old_string":"x","new_string":"y"}},{"type":"tool_use","id":"w1","name":"TodoWrite","input":{"todos":[{"content":"do it","status":"pending"}]}}]}}` +
		"\n" +
		`{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"2026-09-12T09:00:01Z","message":{"content":"/review the diff"}}` +
		"\n" +
		`{"type":"assistant","uuid":"a2","sessionId":"s1","timestamp":"2026-09-12T09:00:02Z","version":"2.1.269","isSidechain":true,"parentUuid":"a1","message":{"model":"claude-opus-4-8","content":[{"type":"tool_use","id":"g1","name":"Grep","input":{"pattern":"TODO"}}]}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = NormalizeTranscript(data, time.Unix(0, 0).UTC())
		_, _ = ExtractTranscriptOperations(data, time.Unix(0, 0).UTC())
	})
}

// transcriptFixtureNDJSON extracts the sanitised transcript records from the
// fixture wrapper and re-serialises them to newline-delimited JSON — the exact
// on-disk format the /v1/claude/transcript route hands to the adapter — so the
// test replays a real transcript shape rather than a bespoke test-only encoding.
// Numbers are preserved via UseNumber so large token counts are not rounded
// through float64 before reaching NormalizeTranscript.
func transcriptFixtureNDJSON(t *testing.T, name string) []byte {
	t.Helper()
	return transcriptFixtureNDJSONFrom(t, "observed-sanitised", name)
}

// transcriptFixtureNDJSONFrom is transcriptFixtureNDJSON reading from an explicit
// fixture subdir (observed-sanitised or synthetic), so a hand-constructed synthetic
// transcript can be replayed the same way as a captured one.
func transcriptFixtureNDJSONFrom(t *testing.T, subdir, name string) []byte {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(string(readFixtureFrom(t, subdir, name))))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	payload, ok := document["payload"].(map[string]any)
	if !ok {
		t.Fatalf("fixture payload must be an object")
	}
	lines, ok := payload["transcript_lines"].([]any)
	if !ok {
		t.Fatalf("fixture payload.transcript_lines must be an array")
	}
	var builder strings.Builder
	for index, line := range lines {
		encoded, err := json.Marshal(line)
		if err != nil {
			t.Fatalf("marshal transcript line %d: %v", index, err)
		}
		if index > 0 {
			builder.WriteByte('\n')
		}
		builder.Write(encoded)
	}
	return []byte(builder.String())
}

func TestNormalizeTranscriptGolden(t *testing.T) {
	data := transcriptFixtureNDJSON(t, "claude-code-2.1.269-session-transcript.json")
	receivedAt := time.Date(2026, 9, 12, 9, 14, 3, 0, time.UTC)

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
	assertTranscriptAssistantEvents(t, first)
	assertMatchesGolden(t, "claude-code-2.1.269-session-transcript.events.json", first)
}

// assertTranscriptAssistantEvents proves F4's core contract: the assistant records
// become assistant_message events (system/auxiliary are parsed-and-skipped, while a
// content-bearing user record becomes a user_message event, J18/#105), they
// correlate by the in-record sessionId, and the model + token usage surface under
// the shared canonical attribute keys.
func assertTranscriptAssistantEvents(t *testing.T, events []canonical.Event) {
	t.Helper()
	assistants := eventsOfType(events, eventTypeAssistantMessage)
	if len(assistants) != 2 {
		t.Fatalf("assistant event count = %d, want 2 assistant records", len(assistants))
	}
	for _, event := range assistants {
		assertTranscriptEventShape(t, event)
	}
	assertTranscriptTokenAttributes(t, assistants[0])
}

// eventsOfType returns the events of a given type in their existing (time-sorted)
// order, so a case can assert over one record type in a mixed event stream.
func eventsOfType(events []canonical.Event, eventType string) []canonical.Event {
	var matched []canonical.Event
	for _, event := range events {
		if event.EventType == eventType {
			matched = append(matched, event)
		}
	}
	return matched
}

// firstEventOfType returns the first event of a given type, failing the test when
// none is present.
func firstEventOfType(t *testing.T, events []canonical.Event, eventType string) canonical.Event {
	t.Helper()
	matched := eventsOfType(events, eventType)
	if len(matched) == 0 {
		t.Fatalf("no %s event in %d events", eventType, len(events))
	}
	return matched[0]
}

// eventByID returns the event with the given EventID, if present.
func eventByID(events []canonical.Event, eventID string) (canonical.Event, bool) {
	for _, event := range events {
		if event.EventID == eventID {
			return event, true
		}
	}
	return canonical.Event{}, false
}

func assertTranscriptEventShape(t *testing.T, event canonical.Event) {
	t.Helper()
	if event.EventType != eventTypeAssistantMessage {
		t.Fatalf("event type = %q", event.EventType)
	}
	if event.Provider != provider || event.Tool != tool {
		t.Fatalf("provider/tool = %q/%q", event.Provider, event.Tool)
	}
	if event.SourceSchema != sourceSchemaTranscript {
		t.Fatalf("source schema = %q", event.SourceSchema)
	}
	if event.SessionID != "claude-code:11111111-1111-4111-8111-111111111111" {
		t.Fatalf("session id = %q, want the in-record sessionId", event.SessionID)
	}
	if event.Attributes["model"] != "claude-opus-4-8" {
		t.Fatalf("model = %#v", event.Attributes["model"])
	}
}

func assertTranscriptTokenAttributes(t *testing.T, event canonical.Event) {
	t.Helper()
	for key, want := range map[string]int64{
		"input_token_count":             1820,
		"output_token_count":            254,
		"cached_input_token_count":      12480,
		"cache_write_input_token_count": 96,
		"reasoning_token_count":         71,
	} {
		got, ok := event.Attributes[key].(int64)
		if !ok || got != want {
			t.Fatalf("attribute %q = %#v, want %d", key, event.Attributes[key], want)
		}
	}
	extra, ok := event.ProviderExtensions["cache_usage_extra"].(map[string]any)
	if !ok {
		t.Fatalf("first event missing cache_usage_extra: %#v", event.ProviderExtensions)
	}
	if extra["cache_creation.ephemeral_1h_input_tokens"] != int64(32) {
		t.Fatalf("ephemeral_1h = %#v", extra["cache_creation.ephemeral_1h_input_tokens"])
	}
}

// TestNormalizeTranscriptCorrelation proves the DAG linkage the transcript
// exposes (uuid/parent_uuid) survives, so downstream consumers can rebuild the
// conversation tree.
func TestNormalizeTranscriptCorrelation(t *testing.T) {
	data := transcriptFixtureNDJSON(t, "claude-code-2.1.269-session-transcript.json")
	events, err := NormalizeTranscript(data, time.Now().UTC())
	if err != nil {
		t.Fatalf("NormalizeTranscript: %v", err)
	}
	event := firstEventOfType(t, events, eventTypeAssistantMessage)
	correlation, ok := event.ProviderExtensions["correlation"].(map[string]any)
	if !ok {
		t.Fatalf("missing correlation: %#v", event.ProviderExtensions)
	}
	if correlation["uuid"] != "bbbbbbbb-0000-4000-8000-000000000002" {
		t.Fatalf("uuid = %#v", correlation["uuid"])
	}
	if correlation["parent_uuid"] != "aaaaaaaa-0000-4000-8000-000000000001" {
		t.Fatalf("parent_uuid = %#v", correlation["parent_uuid"])
	}
	if event.EventID != "claude-code:11111111-1111-4111-8111-111111111111:bbbbbbbb-0000-4000-8000-000000000002" {
		t.Fatalf("event id = %q", event.EventID)
	}
}

// TestNormalizeTranscriptSkipsUnknownAndBlank proves the type set is open: system,
// auxiliary, and unrecognised types are skipped (never hard-fail), and blank lines
// between records are ignored — so a content-heavy out-of-scope record can never
// fail F4. A content-bearing user record does become a user_message event now
// (J18/#105), so exactly that one event is emitted here.
func TestNormalizeTranscriptSkipsUnknownAndBlank(t *testing.T) {
	data := []byte(strings.Join([]string{
		`{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"2026-09-12T09:00:00Z","message":{"content":"hi"}}`,
		``,
		`{"type":"totally-new-type-2027","uuid":"x1","sessionId":"s1","timestamp":"2026-09-12T09:00:01Z"}`,
		`   `,
		`{"type":"system","uuid":"y1","sessionId":"s1","timestamp":"2026-09-12T09:00:02Z","content":"turn_duration"}`,
	}, "\n"))
	events, err := NormalizeTranscript(data, time.Now().UTC())
	if err != nil {
		t.Fatalf("NormalizeTranscript: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1 (the user prompt; unknown/system/blank skipped)", len(events))
	}
	if events[0].EventType != eventTypeUserMessage {
		t.Fatalf("event type = %q, want user_message", events[0].EventType)
	}
	if events[0].ProviderExtensions["transcript"].(map[string]any)["prompt_content"] != "hi" {
		t.Fatalf("prompt_content = %#v, want the raw prompt", events[0].ProviderExtensions["transcript"])
	}
}

// TestNormalizeTranscriptMissingStructuralFieldIsError proves that a supported
// (assistant) record missing a structural field is a hard error, not a silent
// drop — matching the traces adapter. A missing version, by contrast, is reported
// as "unavailable" rather than failing.
func TestNormalizeTranscriptMissingStructuralFieldIsError(t *testing.T) {
	cases := map[string]string{
		"missing uuid":         `{"type":"assistant","sessionId":"s1","timestamp":"2026-09-12T09:00:00Z","message":{"model":"claude-opus-4-8"}}`,
		"missing sessionId":    `{"type":"assistant","uuid":"a1","timestamp":"2026-09-12T09:00:00Z","message":{"model":"claude-opus-4-8"}}`,
		"missing timestamp":    `{"type":"assistant","uuid":"a1","sessionId":"s1","message":{"model":"claude-opus-4-8"}}`,
		"bad timestamp":        `{"type":"assistant","uuid":"a1","sessionId":"s1","timestamp":"not-a-time","message":{"model":"claude-opus-4-8"}}`,
		"whitespace uuid":      `{"type":"assistant","uuid":"   ","sessionId":"s1","timestamp":"2026-09-12T09:00:00Z","message":{"model":"claude-opus-4-8"}}`,
		"whitespace sessionId": `{"type":"assistant","uuid":"a1","sessionId":" \t ","timestamp":"2026-09-12T09:00:00Z","message":{"model":"claude-opus-4-8"}}`,
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NormalizeTranscript([]byte(line), time.Now().UTC()); err == nil {
				t.Fatal("want a hard normalisation error, got nil")
			}
		})
	}
}

func TestNormalizeTranscriptMissingVersionIsUnavailable(t *testing.T) {
	line := `{"type":"assistant","uuid":"a1","sessionId":"s1","timestamp":"2026-09-12T09:00:00Z","message":{"model":"claude-opus-4-8"}}`
	events, err := NormalizeTranscript([]byte(line), time.Now().UTC())
	if err != nil {
		t.Fatalf("NormalizeTranscript: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	if events[0].SourceVersion != unavailable {
		t.Fatalf("source version = %q, want %q", events[0].SourceVersion, unavailable)
	}
}

// TestNormalizeTranscriptMalformedAssistantAbortsImport documents the abort-not-
// partial contract: one malformed assistant line fails the whole import rather
// than persisting the valid records around it — so a caller never sees a
// half-imported session and can safely re-ship the corrected transcript.
func TestNormalizeTranscriptMalformedAssistantAbortsImport(t *testing.T) {
	data := []byte(strings.Join([]string{
		`{"type":"assistant","uuid":"a1","sessionId":"s1","timestamp":"2026-09-12T09:00:00Z","version":"2.1.269","message":{"model":"claude-opus-4-8"}}`,
		`{"type":"assistant","uuid":"a2","sessionId":"s1","message":{"model":"claude-opus-4-8"}}`,
		`{"type":"assistant","uuid":"a3","sessionId":"s1","timestamp":"2026-09-12T09:00:02Z","version":"2.1.269","message":{"model":"claude-opus-4-8"}}`,
	}, "\n"))
	if _, err := NormalizeTranscript(data, time.Now().UTC()); err == nil {
		t.Fatal("want the whole import to abort on one malformed assistant record, got nil")
	}
}

// TestNormalizeTranscriptCapturesContentRaw is the raw-capture invariant guard
// (epic #87, extended by #173). It is built from direct in-test NDJSON (NOT the
// committed fixture, which is scanned for likely secret values): an assistant record
// with response text, thinking, and Bash/Edit tool_use blocks, plus the paired user
// tool_result carrying a structured toolUseResult sibling. The behaviour content IS
// captured — response + thinking on the assistant event, the raw file_path/
// full_command on the tool_call event, and the full input body (Edit diff strings) +
// result on the Operation. cwd (the workspace path) and the record-scoped
// toolUseResult are now retained raw too (#173, Sites 2 & 3), no longer amputated at
// ingest; a downstream visibility decision is layered over the retained value.
func TestNormalizeTranscriptCapturesContentRaw(t *testing.T) {
	data := []byte(strings.Join([]string{
		`{` +
			`"type":"assistant","uuid":"a1","sessionId":"s1","timestamp":"2026-09-12T09:00:00Z","version":"2.1.269",` +
			`"cwd":"/home/tiq-canary-user/secret-project",` +
			`"message":{"role":"assistant","model":"claude-opus-4-8",` +
			`"content":[` +
			`{"type":"text","text":"tiq-canary-response-body"},` +
			`{"type":"thinking","thinking":"tiq-canary-thinking-body"},` +
			`{"type":"tool_use","id":"toolu_bash","name":"Bash","input":{"command":"tiq-canary-command"}},` +
			`{"type":"tool_use","id":"toolu_edit","name":"Edit","input":{"file_path":"/tiq-canary-path","old_string":"tiq-canary-old","new_string":"tiq-canary-new"}}` +
			`],` +
			`"usage":{"input_tokens":10,"output_tokens":20}}` +
			`}`,
		`{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"2026-09-12T09:00:01Z",` +
			`"cwd":"/home/tiq-canary-user/secret-project",` +
			`"toolUseResult":{"stdout":"tiq-canary-toolresult-meta","exit_code":0},` +
			`"message":{"content":[{"type":"tool_result","tool_use_id":"toolu_bash","is_error":false,` +
			`"content":[{"type":"text","text":"tiq-canary-stdout"}]}]}}`,
	}, "\n"))

	events, err := NormalizeTranscript(data, time.Now().UTC())
	if err != nil {
		t.Fatalf("NormalizeTranscript: %v", err)
	}
	operations, err := ExtractTranscriptOperations(data, time.Now().UTC())
	if err != nil {
		t.Fatalf("ExtractTranscriptOperations: %v", err)
	}
	eventsJSON := marshalForCanary(t, events)
	opsJSON := marshalForCanary(t, operations)

	// Raw content is captured on the event surface (epic #87): response + thinking
	// on the assistant event; the raw command/path on the tool_call event; cwd on the
	// assistant event's transcript envelope (#173).
	for _, present := range []string{
		"tiq-canary-response-body",
		"tiq-canary-thinking-body",
		"tiq-canary-command",
		"tiq-canary-path",
		"secret-project",
	} {
		if !strings.Contains(eventsJSON, present) {
			t.Fatalf("content %q must be captured on events (epic #87): %s", present, eventsJSON)
		}
	}
	// The full input body (Edit diff strings), the tool result, cwd, and the record-
	// scoped toolUseResult ride on the Operation, raw and complete (#173).
	for _, present := range []string{
		"tiq-canary-command",
		"tiq-canary-path",
		"tiq-canary-old",
		"tiq-canary-new",
		"tiq-canary-stdout",
		"tiq-canary-toolresult-meta",
		"secret-project",
	} {
		if !strings.Contains(opsJSON, present) {
			t.Fatalf("content %q must be captured on operations (#173): %s", present, opsJSON)
		}
	}
	// A single-result record attaches the structured result unambiguously — no
	// "record" scope marker (that marks a result shared across several blocks).
	if strings.Contains(opsJSON, "tool_use_result_scope") {
		t.Fatalf("single-result record must not carry a record scope marker: %s", opsJSON)
	}
	// The numeric usage and model are still captured alongside the raw content.
	assistant := firstEventOfType(t, events, eventTypeAssistantMessage)
	if assistant.Attributes["model"] != "claude-opus-4-8" {
		t.Fatalf("model dropped: %#v", assistant.Attributes["model"])
	}
	if assistant.Attributes["input_token_count"] != int64(10) {
		t.Fatalf("input tokens dropped: %#v", assistant.Attributes["input_token_count"])
	}
}

// TestNormalizeTranscriptRetainsRecordScopedResult proves the record-scoped
// toolUseResult retention (#173, Site 3) across the shapes a user record can take:
// a single result block attaches the structured result to the matched call
// unambiguously; several result blocks attach it to the first matched call with a
// "record" scope marker so it is retained raw yet never mis-read as one call's own;
// an unmatched result block leaves nothing to ride and is not retained (the residual
// unmatched-record scope this change does not widen); and a large integer inside the
// structured result round-trips exactly via the UseNumber decode, not through a
// lossy float64. The cases share one operation-extraction + assertion helper so the
// four shapes are not four pasted assertion blocks.
func TestNormalizeTranscriptRetainsRecordScopedResult(t *testing.T) {
	const assistant = `{"type":"assistant","uuid":"a1","sessionId":"s1","timestamp":"2026-09-12T09:00:00Z","version":"2.1.269",` +
		`"message":{"model":"claude-opus-4-8","content":[` +
		`{"type":"tool_use","id":"toolu_a","name":"Bash","input":{"command":"one"}},` +
		`{"type":"tool_use","id":"toolu_b","name":"Bash","input":{"command":"two"}}]}}`
	cases := []struct {
		name      string
		user      string
		wantMeta  string // substring the structured result must contain, "" when none retained
		wantScope bool   // whether a "record" scope marker must be present
	}{
		{
			name: "single result attaches unambiguously",
			user: `{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"2026-09-12T09:00:01Z",` +
				`"toolUseResult":{"stdout":"tiq-single-meta"},` +
				`"message":{"content":[{"type":"tool_result","tool_use_id":"toolu_a","content":[{"type":"text","text":"x"}]}]}}`,
			wantMeta:  "tiq-single-meta",
			wantScope: false,
		},
		{
			name: "several result blocks carry a record scope marker",
			user: `{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"2026-09-12T09:00:01Z",` +
				`"toolUseResult":{"stdout":"tiq-shared-meta"},` +
				`"message":{"content":[` +
				`{"type":"tool_result","tool_use_id":"toolu_a","content":[{"type":"text","text":"x"}]},` +
				`{"type":"tool_result","tool_use_id":"toolu_b","content":[{"type":"text","text":"y"}]}]}}`,
			wantMeta:  "tiq-shared-meta",
			wantScope: true,
		},
		{
			name: "unmatched result block retains nothing",
			user: `{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"2026-09-12T09:00:01Z",` +
				`"toolUseResult":{"stdout":"tiq-orphan-meta"},` +
				`"message":{"content":[{"type":"tool_result","tool_use_id":"toolu_never","content":[{"type":"text","text":"x"}]}]}}`,
			wantMeta:  "",
			wantScope: false,
		},
		{
			name: "large integer round-trips exactly",
			user: `{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"2026-09-12T09:00:01Z",` +
				`"toolUseResult":{"bytes":9007199254740993},` +
				`"message":{"content":[{"type":"tool_result","tool_use_id":"toolu_a","content":[{"type":"text","text":"x"}]}]}}`,
			wantMeta:  "9007199254740993",
			wantScope: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			operations, err := ExtractTranscriptOperations([]byte(assistant+"\n"+tc.user), time.Now().UTC())
			if err != nil {
				t.Fatalf("ExtractTranscriptOperations: %v", err)
			}
			assertRecordScopedResult(t, marshalForCanary(t, operations), tc.wantMeta, tc.wantScope)
		})
	}
}

// assertRecordScopedResult checks how a record-scoped toolUseResult surfaces in
// the marshalled operations: wantMeta "" means nothing was retained (unmatched
// block), otherwise the raw value must appear and the record-scope marker must be
// present only for a multi-result record.
func assertRecordScopedResult(t *testing.T, opsJSON, wantMeta string, wantScope bool) {
	t.Helper()
	if wantMeta == "" {
		if strings.Contains(opsJSON, "tool_use_result") {
			t.Fatalf("unmatched record must retain no structured result: %s", opsJSON)
		}
		return
	}
	if !strings.Contains(opsJSON, wantMeta) {
		t.Fatalf("structured result %q must be retained raw: %s", wantMeta, opsJSON)
	}
	if got := strings.Contains(opsJSON, "tool_use_result_scope"); got != wantScope {
		t.Fatalf("record scope marker present = %v, want %v: %s", got, wantScope, opsJSON)
	}
}

// marshalForCanary renders a value to JSON for a substring-based capture assertion.
func marshalForCanary(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(encoded)
}

// TestNormalizeTranscriptPreservesLargeTokenCounts proves json.Number keeps
// token integers beyond float64 mantissa precision (2^53) instead of rounding.
func TestNormalizeTranscriptPreservesLargeTokenCounts(t *testing.T) {
	const large = "9007199254740993" // 2^53 + 1
	line := `{"type":"assistant","uuid":"a1","sessionId":"s1","timestamp":"2026-09-12T09:00:00Z","version":"2.1.269",` +
		`"message":{"model":"claude-opus-4-8","usage":{"input_tokens":` + large + `,"output_tokens":1}}}`
	events, err := NormalizeTranscript([]byte(line), time.Now().UTC())
	if err != nil {
		t.Fatalf("NormalizeTranscript: %v", err)
	}
	got, ok := events[0].Attributes["input_token_count"].(int64)
	if !ok || got != 9007199254740993 {
		t.Fatalf("input_token_count = %#v, want exact %s", events[0].Attributes["input_token_count"], large)
	}
}
