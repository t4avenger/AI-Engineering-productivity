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
// every leaf value of every user/assistant record in every transcript fixture, the
// value must be reachable from what the normaliser emits (events + operations):
// a string or number appears verbatim, and an explicit null survives under its own
// wire key. It inspects the produced output rather than the residual registry, so
// it is not a restatement of the rules it checks. Booleans are excluded here only
// because their typed homes rename them (isSidechain → is_sidechain, is_error →
// outcome), and a "type" discriminator is skipped because it is carried by the
// event type / typed home it selected (user → user_message, tool_result → the
// paired Operation's result) rather than as a literal; an untyped block keeps its
// type in the residual. TestTranscriptRecordResidual pins both directly.
func TestTranscriptRetainsEveryRecordValue(t *testing.T) {
	for _, fixture := range transcriptFixtures {
		t.Run(fixture.name, func(t *testing.T) {
			data := transcriptFixtureNDJSONFrom(t, fixture.subdir, fixture.name)
			output := normalisedTranscriptJSON(t, data)
			for lineNum, line := range bytes.Split(data, []byte("\n")) {
				var record map[string]any
				decoder := json.NewDecoder(bytes.NewReader(line))
				decoder.UseNumber()
				if err := decoder.Decode(&record); err != nil {
					t.Fatalf("line %d: %v", lineNum+1, err)
				}
				if record["type"] != "user" && record["type"] != "assistant" {
					continue // auxiliary record types are out of #263 scope
				}
				assertLeavesRetained(t, output, "", record)
			}
		})
	}
}

// normalisedTranscriptJSON marshals both transcript outputs (events and
// operations) compactly, so a leaf value can be searched for verbatim.
func normalisedTranscriptJSON(t *testing.T, data []byte) string {
	t.Helper()
	events, err := NormalizeTranscript(data, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	operations, err := ExtractTranscriptOperations(data, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("operations: %v", err)
	}
	encoded, err := json.Marshal([]any{events, operations})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(encoded)
}

// transcriptDiscriminators are the record/block types whose typed home encodes
// them (see TestTranscriptRetainsEveryRecordValue); any other "type" value — e.g.
// message.type or usage.iterations[].type — must still appear verbatim.
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
			assertLeavesRetained(t, output, key, item)
		}
	default:
		if needle, ok := leafNeedle(key, typed); ok && !strings.Contains(output, needle) {
			t.Errorf("%q value %#v dropped (missing %s)", key, typed, needle)
		}
	}
}

// leafNeedle is the text a retained leaf must leave in the compact output: a
// string's JSON-escaped body, a number's literal, or an explicit null under its own
// wire key. ok is false for booleans (see TestTranscriptRetainsEveryRecordValue).
func leafNeedle(key string, value any) (string, bool) {
	switch typed := value.(type) {
	case nil:
		return `"` + key + `":null`, true
	case string:
		encoded, _ := json.Marshal(typed)
		return strings.Trim(string(encoded), `"`), true
	case json.Number:
		return typed.String(), true
	}
	return "", false
}

// TestTranscriptRecordResidual pins the value-preserving residual rule (#263): a
// field is left out of transcript_record only when its typed home stored that exact
// value. Explicit nulls, empty or wrong-typed values, values a typed home would
// trim or reformat, unknown content blocks, and extra keys on known blocks all stay.
func TestTranscriptRecordResidual(t *testing.T) {
	cases := []struct {
		name string
		line string
		want map[string]any
	}{
		{
			name: "assistant",
			line: `{"type":"assistant","uuid":"a1","parentUuid":null,"sessionId":"s1","timestamp":"2026-09-12T09:00:00+01:00","version":"2.1.286","cwd":"","apiBlockIndex":2,"isSidechain":false,"perTurnEffort":null,"message":{"id":"msg_1","model":" padded ","stop_reason":"end_turn","stop_details":null,"content":[{"type":"text","text":"hi","citations":[]},{"type":"text","text":"  "},{"type":"image","source":{"kind":"x"}},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}],"usage":{"input_tokens":3,"output_tokens":"4","output_tokens_details":{"thinking_tokens":1,"extra":5},"speed":"standard"}}}`,
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
			name: "user",
			line: `{"type":"user","uuid":"u1","sessionId":"s1","timestamp":"2026-09-12T09:00:01Z","promptId":"p1","permissionMode":"default","turnPosition":{"promptIndex":0},"isSidechain":"no","message":{"role":"user","model":"not-read","content":"prompt"}}`,
			want: map[string]any{
				"turnPosition": map[string]any{"promptIndex": json.Number("0")},
				"isSidechain":  "no",
				"message":      map[string]any{"role": "user", "model": "not-read"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rules := transcriptUserRules
			if tc.name == "assistant" {
				rules = transcriptAssistantRules
			}
			got := transcriptRecordResidual([]byte(tc.line), rules)
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
