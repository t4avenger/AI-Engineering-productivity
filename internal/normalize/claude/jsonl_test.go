package claude

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// FuzzNormalizeTranscript keeps the JSONL normaliser boundary from panicking on
// arbitrary input (QUALITY_GATES: fuzz smoke when normalisation changes).
func FuzzNormalizeTranscript(f *testing.F) {
	f.Add([]byte(`{"type":"assistant","uuid":"a1","sessionId":"s1","timestamp":"2026-09-12T09:00:00Z","version":"2.1.269","message":{"model":"claude-opus-4-8","usage":{"input_tokens":1,"output_tokens":2}}}`))
	f.Add([]byte("not json\n{\"type\":\"user\"}"))
	f.Add([]byte(""))
	f.Add([]byte("{\"type\":\"assistant\"}"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = NormalizeTranscript(data, time.Unix(0, 0).UTC())
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
	decoder := json.NewDecoder(strings.NewReader(string(readFixture(t, name))))
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

// assertTranscriptAssistantEvents proves F4's core contract: only the assistant
// records become events (user/system/auxiliary are parsed-and-skipped), they
// correlate by the in-record sessionId, and the model + token usage surface under
// the shared canonical attribute keys.
func assertTranscriptAssistantEvents(t *testing.T, events []canonical.Event) {
	t.Helper()
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2 assistant records", len(events))
	}
	for _, event := range events {
		assertTranscriptEventShape(t, event)
	}
	assertTranscriptTokenAttributes(t, events[0])
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
	correlation, ok := events[0].ProviderExtensions["correlation"].(map[string]any)
	if !ok {
		t.Fatalf("missing correlation: %#v", events[0].ProviderExtensions)
	}
	if correlation["uuid"] != "bbbbbbbb-0000-4000-8000-000000000002" {
		t.Fatalf("uuid = %#v", correlation["uuid"])
	}
	if correlation["parent_uuid"] != "aaaaaaaa-0000-4000-8000-000000000001" {
		t.Fatalf("parent_uuid = %#v", correlation["parent_uuid"])
	}
	if events[0].EventID != "claude-code:11111111-1111-4111-8111-111111111111:bbbbbbbb-0000-4000-8000-000000000002" {
		t.Fatalf("event id = %q", events[0].EventID)
	}
}

// TestNormalizeTranscriptSkipsUnknownAndBlank proves the type set is open: user,
// system, auxiliary, and unrecognised types are skipped (never hard-fail), and
// blank lines between records are ignored — so a content-heavy out-of-scope
// record can never fail F4.
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
	if len(events) != 0 {
		t.Fatalf("event count = %d, want 0 (no assistant records)", len(events))
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

// TestNormalizeTranscriptDoesNotEmitContent is the canary-leakage guard. It is
// built from direct in-test NDJSON (NOT the committed fixture, which cannot hold
// prohibited field names and still pass fixture.Validate): an assistant record
// whose message.content[], tool input.command/file_path, and toolUseResult carry
// fake secrets and content. None of it may appear in the marshalled events —
// F4 reads only the model and numeric usage.
func TestNormalizeTranscriptDoesNotEmitContent(t *testing.T) {
	line := `{` +
		`"type":"assistant","uuid":"a1","sessionId":"s1","timestamp":"2026-09-12T09:00:00Z","version":"2.1.269",` +
		`"cwd":"/home/tiq-canary-user/secret-project",` +
		`"message":{"role":"assistant","model":"claude-opus-4-8",` +
		`"content":[` +
		`{"type":"text","text":"tiq-canary-response-body"},` +
		`{"type":"thinking","thinking":"tiq-canary-thinking-body"},` +
		`{"type":"tool_use","name":"Bash","input":{"command":"tiq-canary-command"}},` +
		`{"type":"tool_use","name":"Edit","input":{"file_path":"/tiq-canary-path","oldString":"tiq-canary-old","newString":"tiq-canary-new"}}` +
		`],` +
		`"usage":{"input_tokens":10,"output_tokens":20}},` +
		`"toolUseResult":{"stdout":"tiq-canary-stdout"}` +
		`}`
	events, err := NormalizeTranscript([]byte(line), time.Now().UTC())
	if err != nil {
		t.Fatalf("NormalizeTranscript: %v", err)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	for _, leaked := range []string{
		"tiq-canary-response-body",
		"tiq-canary-thinking-body",
		"tiq-canary-command",
		"tiq-canary-path",
		"tiq-canary-old",
		"tiq-canary-new",
		"tiq-canary-stdout",
		"tiq-canary-user",
		"secret-project",
	} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("content %q leaked into events: %s", leaked, encoded)
		}
	}
	// The numeric usage and model are still captured — the drop is content-only.
	if events[0].Attributes["model"] != "claude-opus-4-8" {
		t.Fatalf("model dropped: %#v", events[0].Attributes["model"])
	}
	if events[0].Attributes["input_token_count"] != int64(10) {
		t.Fatalf("input tokens dropped: %#v", events[0].Attributes["input_token_count"])
	}
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
