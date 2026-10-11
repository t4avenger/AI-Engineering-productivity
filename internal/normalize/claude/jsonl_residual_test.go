package claude

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// transcriptFixtures are every committed transcript fixture (subdir, name) the
// #263 completeness gate replays.
var transcriptFixtures = []struct{ subdir, name string }{
	{"observed-sanitised", "claude-code-2.1.269-session-transcript.json"},
	{"observed-sanitised", "claude-code-2.1.269-mcp-tool-call-transcript.json"},
	{"observed-sanitised", "claude-code-2.1.283-transcript-conversation.json"},
	{"observed-sanitised", "claude-code-2.1.286-tool-output-pr-link-transcript.json"},
	{"synthetic", "claude-code-2.1.269-tool-io-diffs-subagent-transcript.json"},
}

// TestTranscriptRetainsEveryRecordValue is the #263 "nothing dropped" gate. For
// every leaf of every user/assistant record in every transcript fixture, the leaf
// must be found, key and value together, in the output derived from that same
// record: its own event(s), the tool-call events and Operations its tool_use /
// tool_result blocks pair into, or the transcript_result envelope it rides on. A
// leaf matches under its wire key (the residual echo keeps wire names) or under the
// canonical key its typed home renames it to (transcriptTypedRenames). Scoping to
// the record means a value repeated elsewhere in the session cannot stand in for a
// dropped one, and key-qualified needles mean a common value such as 0 cannot match
// unrelated JSON. It inspects the produced output rather than the residual
// registry, so it is not a restatement of the rules it checks.
func TestTranscriptRetainsEveryRecordValue(t *testing.T) {
	for _, fixture := range transcriptFixtures {
		t.Run(fixture.name, func(t *testing.T) {
			data := transcriptFixtureNDJSONFrom(t, fixture.subdir, fixture.name)
			outputs := normalisedTranscriptOutputs(t, data)
			for lineNum, line := range bytes.Split(data, []byte("\n")) {
				var record map[string]any
				decoder := json.NewDecoder(bytes.NewReader(line))
				decoder.UseNumber()
				if err := decoder.Decode(&record); err != nil {
					t.Fatalf("line %d: %v", lineNum+1, err)
				}
				if record["type"] != "user" && record["type"] != "assistant" {
					continue // auxiliary record types are split into #264
				}
				assertLeavesRetained(t, recordScopedOutput(outputs, line, record), "", record)
			}
		})
	}
}

// normalisedTranscriptOutputs marshals each event and Operation the transcript
// yields to compact JSON, so a record's derived outputs can be selected and searched.
func normalisedTranscriptOutputs(t *testing.T, data []byte) []string {
	t.Helper()
	events, err := NormalizeTranscript(data, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	operations, err := ExtractTranscriptOperations(data, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("operations: %v", err)
	}
	var outputs []string
	for _, item := range append(anySlice(events), anySlice(operations)...) {
		encoded, err := json.Marshal(item)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		outputs = append(outputs, string(encoded))
	}
	return outputs
}

func anySlice[T any](items []T) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}

// recordScopedOutput joins the outputs derived from one record: those carrying its
// uuid (its own event, its tool calls' correlation, or its transcript_result
// envelope) and the Operations paired by the tool_use ids its blocks carry.
func recordScopedOutput(outputs []string, line []byte, record map[string]any) string {
	uuid, _ := json.Marshal(record["uuid"])
	needles := []string{`"uuid":` + string(uuid)}
	_, blocks, _ := decodeContentBlocks(line)
	for _, block := range blocks {
		for _, id := range []string{block.ID, block.ToolUseID} {
			if id != "" {
				encoded, _ := json.Marshal(id)
				needles = append(needles, `"tool_use_id":`+string(encoded))
			}
		}
	}
	var scoped strings.Builder
	for _, output := range outputs {
		for _, needle := range needles {
			if strings.Contains(output, needle) {
				scoped.WriteString(output)
				break
			}
		}
	}
	return scoped.String()
}

// transcriptTypedRenames maps a wire key to the canonical key(s) its typed home
// stores the value under. {v} is the JSON-encoded value and {s} the raw string body
// (for homes that prefix the value). Keys absent here keep their wire name.
var transcriptTypedRenames = map[string][]string{
	"parentUuid":                  {`"parent_uuid":{v}`},
	"promptId":                    {`"prompt_id":{v}`},
	"sessionId":                   {`"session_id":"claude-code:{s}"`},
	"timestamp":                   {`"occurred_at":{v}`},
	"version":                     {`"source_version":{v}`},
	"gitBranch":                   {`"git_branch":{v}`},
	"userType":                    {`"user_type":{v}`},
	"requestId":                   {`"request_id":"claude-code:{s}"`},
	"permissionMode":              {`"permission_mode":{v}`},
	"promptSource":                {`"prompt_source":{v}`},
	"turnOrigin":                  {`"turn_origin":{v}`},
	"apiBlockIndex":               {`"api_block_index":{v}`},
	"isSidechain":                 {`"is_sidechain":{v}`},
	"id":                          {`"message_id":"claude-code:{s}"`, `"tool_use_id":{v}`},
	"name":                        {`"tool_name":{v}`},
	"input_tokens":                {`"input_token_count":{v}`},
	"output_tokens":               {`"output_token_count":{v}`},
	"cache_read_input_tokens":     {`"cached_input_token_count":{v}`},
	"cache_creation_input_tokens": {`"cache_write_input_token_count":{v}`},
	"thinking_tokens":             {`"reasoning_token_count":{v}`},
	"ephemeral_1h_input_tokens":   {`"cache_creation.ephemeral_1h_input_tokens":{v}`},
	"ephemeral_5m_input_tokens":   {`"cache_creation.ephemeral_5m_input_tokens":{v}`},
	"is_error":                    {`"outcome":"success"`, `"outcome":"failed"`},
}

// transcriptBodyKeys are the content bodies a shaper stores inside a joined text
// (prompt_content / response_content / thinking) or a tool result, so only their
// value — not their key — is preserved.
var transcriptBodyKeys = map[string]bool{"text": true, "thinking": true, "content": true}

// transcriptDiscriminators are the record/block types whose typed home encodes
// them (user → user_message, tool_result → the paired Operation's result) rather
// than as a literal; any other "type" value — e.g. message.type or
// usage.iterations[].type — must still appear, and an untyped block keeps its type
// in the residual (TestTranscriptRecordResidual).
var transcriptDiscriminators = map[any]bool{
	"user": true, "assistant": true, "text": true, "thinking": true, "tool_use": true, "tool_result": true,
}

// assertLeavesRetained walks a decoded record and checks every leaf against output.
func assertLeavesRetained(t *testing.T, output, key string, value any) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		for child, childValue := range typed {
			if child != "type" || !transcriptDiscriminators[childValue] {
				assertLeavesRetained(t, output, child, childValue)
			}
		}
	case []any:
		for _, item := range typed {
			// A scalar array element sits under its parent key inside an array, so
			// only its value can be matched; objects recurse with their own keys.
			assertLeavesRetained(t, output, "", item)
		}
	default:
		if !leafRetained(output, key, typed) {
			t.Errorf("%q value %#v dropped", key, typed)
		}
	}
}

// leafRetained reports whether a leaf appears in output under its wire key or a
// typed-home rename, followed by a JSON delimiter so 1 never matches 10.
func leafRetained(output, key string, value any) bool {
	encoded, _ := json.Marshal(value)
	if key == "" || transcriptBodyKeys[key] {
		if text, ok := value.(string); ok {
			body, _ := json.Marshal(text)
			return strings.Contains(output, strings.Trim(string(body), `"`))
		}
		return containsDelimited(output, ":"+string(encoded)) || containsDelimited(output, "["+string(encoded))
	}
	text, _ := value.(string)
	body := strings.Trim(string(encoded), `"`)
	if containsDelimited(output, `"`+key+`":`+string(encoded)) {
		return true
	}
	for _, template := range transcriptTypedRenames[key] {
		needle := strings.NewReplacer("{v}", string(encoded), "{s}", body).Replace(template)
		if text == "" && strings.Contains(template, "{s}") {
			continue
		}
		if containsDelimited(output, needle) {
			return true
		}
	}
	return false
}

// containsDelimited reports whether needle occurs followed by a JSON delimiter.
func containsDelimited(output, needle string) bool {
	for _, end := range []string{",", "}", "]"} {
		if strings.Contains(output, needle+end) {
			return true
		}
	}
	return false
}

// TestTranscriptRecordResidual pins the value-preserving residual rule (#263): a
// field is left out of transcript_record only when its typed home stored that exact
// value. Explicit nulls, empty or wrong-typed values, values a typed home would
// trim or reformat, unknown content blocks, and extra keys on known blocks all stay.
func TestTranscriptRecordResidual(t *testing.T) {
	cases := []struct {
		name  string
		rules objectRules
		line  string
		want  map[string]any
	}{
		{
			name:  "assistant",
			rules: transcriptAssistantRules,
			line:  `{"type":"assistant","uuid":"a1","parentUuid":null,"sessionId":"s1","timestamp":"2026-09-12T09:00:00+01:00","version":"2.1.286","cwd":"","apiBlockIndex":2,"isSidechain":false,"perTurnEffort":null,"message":{"id":"msg_1","model":" padded ","stop_reason":"end_turn","stop_details":null,"content":[{"type":"text","text":"hi","citations":[]},{"type":"text","text":"  "},{"type":"image","source":{"kind":"x"}},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}],"usage":{"input_tokens":3,"output_tokens":"4","output_tokens_details":{"thinking_tokens":1,"extra":5},"speed":"standard"}}}`,
			want: map[string]any{
				"parentUuid":    nil,
				"timestamp":     "2026-09-12T09:00:00+01:00",
				"cwd":           "",
				"perTurnEffort": nil,
				"message": map[string]any{
					"model":        " padded ",
					"stop_details": nil,
					"content": []any{
						map[string]any{"index": 0, "block": map[string]any{"type": "text", "citations": []any{}}},
						map[string]any{"index": 1, "block": map[string]any{"type": "text", "text": "  "}},
						map[string]any{"index": 2, "block": map[string]any{"type": "image", "source": map[string]any{"kind": "x"}}},
					},
					"usage": map[string]any{
						"output_tokens":         "4",
						"output_tokens_details": map[string]any{"extra": json.Number("5")},
						"speed":                 "standard",
					},
				},
			},
		},
		{
			name:  "user",
			rules: transcriptUserRules,
			line:  `{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"2026-09-12T09:00:01Z","promptId":"p1","permissionMode":"default","turnPosition":{"promptIndex":0},"isSidechain":"no","message":{"role":"user","model":"not-read","content":"prompt"}}`,
			want: map[string]any{
				"turnPosition": map[string]any{"promptIndex": json.Number("0")},
				"isSidechain":  "no",
				"message":      map[string]any{"role": "user", "model": "not-read"},
			},
		},
		{
			// PR #265 review: explicit null bool, empty usage object, and null/empty
			// content are not stored by any typed home, so they stay raw.
			name:  "nulls and empties",
			rules: transcriptAssistantRules,
			line:  `{"type":"assistant","uuid":"a1","sessionId":"s1","timestamp":"2026-09-12T09:00:00Z","version":"v","isSidechain":null,"message":{"content":null,"usage":{}}}`,
			want: map[string]any{
				"isSidechain": nil,
				"message":     map[string]any{"content": nil, "usage": map[string]any{}},
			},
		},
		{
			name:  "empty content array",
			rules: transcriptUserRules,
			line:  `{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"2026-09-12T09:00:00Z","version":"v","message":{"content":[]}}`,
			want:  map[string]any{"message": map[string]any{"content": []any{}}},
		},
		{
			// A tool_use without an id is never paired into an Operation, so the whole
			// block — name and input included — is echoed rather than consumed.
			name:  "rejected tool_use block",
			rules: transcriptAssistantRules,
			line:  `{"type":"assistant","uuid":"a1","sessionId":"s1","timestamp":"2026-09-12T09:00:00Z","version":"v","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"x"}},{"type":"tool_use","id":" ","name":"Bash","input":null}]}}`,
			want: map[string]any{"message": map[string]any{"content": []any{
				map[string]any{"index": 0, "block": map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "x"}}},
				map[string]any{"index": 1, "block": map[string]any{"type": "tool_use", "id": " ", "name": "Bash", "input": nil}},
			}}},
		},
		{
			// The Operation shapers omit a decoded nil, so an explicit null tool_result
			// body or toolUseResult stays in the residual.
			name:  "null tool bodies",
			rules: transcriptResultRecordRules,
			line:  `{"type":"user","toolUseResult":null,"message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":null,"is_error":false}]}}`,
			want: map[string]any{
				"type":          "user",
				"toolUseResult": nil,
				"message": map[string]any{"content": []any{
					map[string]any{"index": 0, "block": map[string]any{"type": "tool_result", "content": nil}},
				}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := transcriptRecordResidual([]byte(tc.line), tc.rules)
			if !reflect.DeepEqual(got, tc.want) {
				gotJSON, _ := json.Marshal(got)
				wantJSON, _ := json.Marshal(tc.want)
				t.Fatalf("residual\n got: %s\nwant: %s", gotJSON, wantJSON)
			}
		})
	}
}

// TestTranscriptPromptIDCorrelation proves promptId is retained as
// correlation.prompt_id (#263) — on the user_message event and, via the
// tool_result record, on the tool call's correlation event — and is never invented
// for a 2.1.269 transcript that does not emit it.
func TestTranscriptPromptIDCorrelation(t *testing.T) {
	cases := []struct {
		fixture string
		want    map[string]string // event ID suffix → prompt_id ("" = absent)
	}{
		{"claude-code-2.1.283-transcript-conversation.json", map[string]string{
			":00000000-0000-4000-8000-0000000000c1": "00000000-0000-4000-8000-0000000000b1",
			":00000000-0000-4000-8000-0000000000c2": "",
		}},
		{"claude-code-2.1.286-tool-output-pr-link-transcript.json", map[string]string{
			":00000001-0000-4000-8000-000000000251":    "synthetic-pr-link-prompt",
			":toolcall:toolu_synthetic_pr_link_output": "synthetic-pr-link-prompt",
			":00000002-0000-4000-8000-000000000251":    "",
		}},
		{"claude-code-2.1.269-session-transcript.json", map[string]string{
			":aaaaaaaa-0000-4000-8000-000000000001": "",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			events, err := NormalizeTranscript(transcriptFixtureNDJSON(t, tc.fixture), time.Unix(0, 0).UTC())
			if err != nil {
				t.Fatalf("normalise: %v", err)
			}
			for suffix, want := range tc.want {
				correlation, _ := eventWithIDSuffix(t, events, suffix).ProviderExtensions["correlation"].(map[string]any)
				if got, present := correlation["prompt_id"]; present != (want != "") || (present && got != want) {
					t.Fatalf("%s prompt_id = %#v (present %v), want %q", suffix, got, present, want)
				}
			}
		})
	}
}

// TestTranscriptWrongTypedFieldsRetained proves a provider type change on a field
// #263 promoted to a typed home neither aborts the import (assistant) nor skips the
// record (user, tool_result-only), and the unexpected value is echoed raw.
func TestTranscriptWrongTypedFieldsRetained(t *testing.T) {
	data := []byte(`{"type":"user","uuid":"u0","sessionId":"s1","timestamp":"2026-09-12T09:00:00Z","promptId":7,"permissionMode":false,"message":{"content":"go"}}` + "\n" +
		`{"type":"assistant","uuid":"a1","sessionId":"s1","timestamp":"2026-09-12T09:00:01Z","message":{"id":{"x":1},"stop_reason":2,"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}` + "\n" +
		`{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"2026-09-12T09:00:02Z","promptId":[1],"message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`)
	events, err := NormalizeTranscript(data, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("wrong-typed fields must not abort the import: %v", err)
	}
	encoded, _ := json.Marshal(events)
	for _, want := range []string{`"promptId":7`, `"permissionMode":false`, `"id":{"x":1}`, `"stop_reason":2`, `"promptId":[1]`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("wrong-typed value %s not retained raw", want)
		}
	}
	if strings.Contains(string(encoded), `"prompt_id"`) {
		t.Errorf("a non-string promptId must not become correlation.prompt_id")
	}
}

// TestNormalizeTranscriptConversationGolden pins the full 2.1.283 transcript slice,
// including the permission/prompt-source typed homes and the raw residual.
func TestNormalizeTranscriptConversationGolden(t *testing.T) {
	events, err := NormalizeTranscript(transcriptFixtureNDJSON(t, "claude-code-2.1.283-transcript-conversation.json"), time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	user := firstEventOfType(t, events, eventTypeUserMessage)
	envelope, _ := user.ProviderExtensions["transcript"].(map[string]any)
	for key, want := range map[string]string{"permission_mode": "default", "prompt_source": "sdk", "turn_origin": "sdk"} {
		if envelope[key] != want {
			t.Fatalf("transcript.%s = %#v, want %q", key, envelope[key], want)
		}
	}
	assertMatchesGolden(t, "claude-code-2.1.283-transcript-conversation.events.json", events)
}

func eventWithIDSuffix(t *testing.T, events []canonical.Event, suffix string) canonical.Event {
	t.Helper()
	for _, event := range events {
		if strings.HasSuffix(event.EventID, suffix) {
			return event
		}
	}
	t.Fatalf("no event with ID suffix %q", suffix)
	return canonical.Event{}
}
