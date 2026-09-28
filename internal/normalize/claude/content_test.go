package claude

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// TestNormalizeEventsUserPromptGolden, ...AssistantResponseGolden, and
// ...APIBodiesGolden pin the canonical shape of the three content-bearing log
// events (epic #87 / #94) via the reviewed-fixture path.
func TestNormalizeEventsUserPromptGolden(t *testing.T) {
	assertContentGolden(t, "claude-code-2.1.270-user-prompt.json", "claude-code-2.1.270-user-prompt.events.json")
}

func TestNormalizeEventsAssistantResponseGolden(t *testing.T) {
	assertContentGolden(t, "claude-code-2.1.270-assistant-response.json", "claude-code-2.1.270-assistant-response.events.json")
}

func TestNormalizeEventsAPIBodiesGolden(t *testing.T) {
	assertContentGolden(t, "claude-code-2.1.270-api-bodies.json", "claude-code-2.1.270-api-bodies.events.json")
}

// assertContentGolden normalises the reviewed fixture twice, gates determinism,
// then byte-compares against the committed golden.
func assertContentGolden(t *testing.T, fixture, golden string) {
	t.Helper()
	input := readFixture(t, fixture)
	first, err := NormalizeEvents(input)
	if err != nil {
		t.Fatalf("first normalisation: %v", err)
	}
	second, err := NormalizeEvents(input)
	if err != nil {
		t.Fatalf("second normalisation: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("normalisation must be deterministic")
	}
	if updateGolden() {
		writeGolden(t, golden, first)
	}
	assertMatchesGolden(t, golden, first)
}

// TestNormalizeEventsUserPromptCapturesContentRaw proves the prompt text rides
// verbatim under provider_extensions.event when logged, prompt_content is marked
// available, and the length-only event reports prompt_length with no fabricated
// prompt (epic #87 — capture raw, never synthesise).
func TestNormalizeEventsUserPromptCapturesContentRaw(t *testing.T) {
	byID := normalizeByID(t, "claude-code-2.1.270-user-prompt.json")

	present := byID["claude-code:synthetic-content-session:2"]
	echo := eventEcho(t, present)
	if echo["prompt"] != "synthetic probe prompt for E7 content capture" {
		t.Fatalf("prompt not echoed raw: %#v", echo)
	}
	for key, want := range map[string]any{"prompt_length": float64(45), "command_name": "tiq-probe", "command_source": "custom"} {
		if echo[key] != want {
			t.Fatalf("%s = %v, want %v", key, echo[key], want)
		}
	}
	fields := present.Attributes["unavailable_fields"].([]string)
	if containsField(fields, "prompt_content") {
		t.Fatalf("prompt_content must be available on user_prompt: %v", fields)
	}
	if !containsField(fields, "response_content") || !containsField(fields, "model") {
		t.Fatalf("response_content and model must stay unavailable on user_prompt: %v", fields)
	}

	absent := byID["claude-code:synthetic-content-session:8"]
	lengthEcho := eventEcho(t, absent)
	if lengthEcho["prompt_length"] != float64(128) {
		t.Fatalf("prompt_length not echoed on length-only event: %#v", lengthEcho)
	}
	if _, present := lengthEcho["prompt"]; present {
		t.Fatalf("prompt must be absent (never fabricated) when not logged: %#v", lengthEcho)
	}
}

// TestNormalizeEventsAssistantResponseCapturesContentRaw proves the response
// text rides raw, the model is available, request_id is namespaced, and the
// provider-emitted "<REDACTED>" sentinel is captured as received rather than
// dropped or re-redacted at ingest.
func TestNormalizeEventsAssistantResponseCapturesContentRaw(t *testing.T) {
	byID := normalizeByID(t, "claude-code-2.1.270-assistant-response.json")

	present := byID["claude-code:synthetic-content-session:12"]
	echo := eventEcho(t, present)
	if echo["response"] != "synthetic assistant response body for E7 capture" {
		t.Fatalf("response not echoed raw: %#v", echo)
	}
	if echo["model"] != "claude-opus-4-8" || echo["response_length"] != float64(52) {
		t.Fatalf("response metadata not echoed: %#v", echo)
	}
	if got := present.ProviderExtensions["request_id"]; got != "claude-code:req_synthetic_e7_present" {
		t.Fatalf("request_id = %v, want namespaced", got)
	}
	if _, echoed := echo["request_id"]; echoed {
		t.Fatalf("request_id must not double-echo under provider_extensions.event: %#v", echo)
	}
	fields := present.Attributes["unavailable_fields"].([]string)
	if containsField(fields, "response_content") || containsField(fields, "model") {
		t.Fatalf("response_content and model must be available on assistant_response: %v", fields)
	}

	redacted := byID["claude-code:synthetic-content-session:20"]
	if got := eventEcho(t, redacted)["response"]; got != "<REDACTED>" {
		t.Fatalf("provider sentinel must be captured as received, got %v", got)
	}
}

// TestNormalizeEventsAPIBodiesCaptureRaw proves inline bodies and body_ref
// pointers both ride raw, and prompt_content/response_content availability is
// precise per body direction (request carries the prompts, response the output).
func TestNormalizeEventsAPIBodiesCaptureRaw(t *testing.T) {
	byID := normalizeByID(t, "claude-code-2.1.270-api-bodies.json")

	request := byID["claude-code:synthetic-content-session:9"]
	if got := eventEcho(t, request)["body"]; !strings.Contains(got.(string), "synthetic E7 request body") {
		t.Fatalf("request body not echoed raw: %v", got)
	}
	reqFields := request.Attributes["unavailable_fields"].([]string)
	if containsField(reqFields, "prompt_content") {
		t.Fatalf("prompt_content must be available on api_request_body: %v", reqFields)
	}
	if !containsField(reqFields, "response_content") {
		t.Fatalf("response_content must stay unavailable on api_request_body: %v", reqFields)
	}

	response := byID["claude-code:synthetic-content-session:11"]
	respFields := response.Attributes["unavailable_fields"].([]string)
	if containsField(respFields, "response_content") {
		t.Fatalf("response_content must be available on api_response_body: %v", respFields)
	}
	if !containsField(respFields, "prompt_content") {
		t.Fatalf("prompt_content must stay unavailable on api_response_body: %v", respFields)
	}

	ref := byID["claude-code:synthetic-content-session:15"]
	refEcho := eventEcho(t, ref)
	if refEcho["body_ref"] != "raw-api-bodies/synthetic-e7-request.json" {
		t.Fatalf("body_ref not echoed raw: %#v", refEcho)
	}
	if _, inline := refEcho["body"]; inline {
		t.Fatalf("file-mode event must carry body_ref, not an inline body: %#v", refEcho)
	}
}

// TestNormalizeLogsContentEventsPassThroughRawAndRetainCorrelationIDs runs the raw
// OTLP captures through the wire adapter and proves the content survives verbatim
// and the prompt.id/message.uuid correlation identifiers are retained under
// provider_extensions.correlation (#106 (X19)).
func TestNormalizeLogsContentEventsPassThroughRawAndRetainCorrelationIDs(t *testing.T) {
	for _, test := range []struct {
		fixture string
		want    []string
	}{
		{"claude-code-2.1.270-user-prompt-otlp.json", []string{"synthetic probe prompt for E7 content capture"}},
		// "REDACTED" (not "<REDACTED>"): json.Marshal HTML-escapes the angle
		// brackets to </>, so the sentinel survives in escaped form.
		{"claude-code-2.1.270-assistant-response-otlp.json", []string{"synthetic assistant response body for E7 capture", "REDACTED"}},
		{"claude-code-2.1.270-api-bodies-otlp.json", []string{"synthetic E7 request body", "synthetic E7 response body", "raw-api-bodies/synthetic-e7-request.json"}},
	} {
		t.Run(test.fixture, func(t *testing.T) {
			assertWireContentRaw(t, test.fixture, test.want)
		})
	}
}

// assertWireContentRaw runs one -otlp fixture through NormalizeLogs and asserts
// its content survives verbatim and the prompt.id/message.uuid correlation ids are
// retained under provider_extensions.correlation (#106).
func assertWireContentRaw(t *testing.T, fixture string, want []string) {
	t.Helper()
	serialized, err := json.Marshal(normalizeObservedOTLPLogs(t, fixture))
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	for _, content := range want {
		if !strings.Contains(string(serialized), content) {
			t.Fatalf("content not captured raw through wire path: %q", content)
		}
	}
	for _, retained := range []string{`"prompt_id":"synthetic-prompt-id"`, `"message_uuid":"synthetic-message-uuid"`} {
		if !strings.Contains(string(serialized), retained) {
			t.Fatalf("correlation identifier not retained under provider_extensions.correlation: %q; events=%s", retained, serialized)
		}
	}
}

// TestNormalizeLogsEventsSharingPromptIDExposeSameCorrelation proves the #106
// retention invariant against a committed synthetic fixture + golden: two
// user_prompt events carrying the same prompt.id on the wire both expose the
// identical prompt_id under provider_extensions.correlation, so a downstream
// session/prompt view can group them (issue #106's per-prompt UI rollup non-goal
// consumes this key; it is not the shared cross-provider comparator's concern —
// that ordering stays untouched, proven by TestCorrelateEventsIgnoresCorrelationMetadata).
// Byte-exact golden coverage lives in fixtures/claude/expected; the input is a
// clearly-labelled synthetic fixture (not under observed-sanitised) so it never
// implies a real capture. Synthetic values only.
func TestNormalizeLogsEventsSharingPromptIDExposeSameCorrelation(t *testing.T) {
	const sharedPromptID = "constructed-shared-prompt-id"
	events := normalizeOTLPLogsFixture(t, "synthetic", "claude-code-synthetic-shared-prompt-otlp.json")
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	const golden = "claude-code-synthetic-shared-prompt.events.json"
	if updateGolden() {
		writeGolden(t, golden, events)
	}
	assertMatchesGolden(t, golden, events)
	for _, event := range events {
		correlation, ok := event.ProviderExtensions["correlation"].(map[string]any)
		if !ok {
			t.Fatalf("event %q missing correlation extension: %#v", event.EventID, event.ProviderExtensions)
		}
		if correlation["prompt_id"] != sharedPromptID {
			t.Fatalf("event %q prompt_id = %#v, want shared %q", event.EventID, correlation["prompt_id"], sharedPromptID)
		}
	}
}

// TestNormalizeLogsRetainsSessionEnvironmentIdentityRaw proves the #107 X20
// invariant against a committed synthetic fixture + golden: a log event carries
// its operator/organisation identity (record level) and machine/app environment
// (resource level, including array-valued workspace.host_paths and user.groups)
// raw under provider_extensions.environment/resource, and the canonical actor_id
// and repository_id are derived from that retained raw metadata. Nothing is
// dropped at the local-only ingest boundary (owner directive / epic #87). The
// input is a clearly-labelled synthetic fixture (not under observed-sanitised) so
// it never implies a real capture. Synthetic values only.
func TestNormalizeLogsRetainsSessionEnvironmentIdentityRaw(t *testing.T) {
	events := normalizeOTLPLogsFixture(t, "synthetic", "claude-code-synthetic-env-identity-otlp.json")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	const golden = "claude-code-synthetic-env-identity.events.json"
	if updateGolden() {
		writeGolden(t, golden, events)
	}
	assertMatchesGolden(t, golden, events)

	event := events[0]
	if event.ActorID != "claude-code:synthetic-user" {
		t.Fatalf("actor_id = %q, want claude-code:synthetic-user (derived from user.id)", event.ActorID)
	}
	if event.DeviceID != "unavailable" {
		t.Fatalf("device_id = %q, want unavailable (no device identity in Claude telemetry)", event.DeviceID)
	}
	if event.RepositoryID == nil || *event.RepositoryID != "/home/synthetic/workspace" {
		t.Fatalf("repository_id = %#v, want first workspace.host_paths entry", event.RepositoryID)
	}

	environment, ok := event.ProviderExtensions["environment"].(map[string]any)
	if !ok {
		t.Fatalf("missing provider_extensions.environment: %#v", event.ProviderExtensions)
	}
	if environment["user_email"] != "synthetic@example.test" {
		t.Fatalf("user.email must ride raw into environment: %#v", environment["user_email"])
	}
	for key, want := range map[string]any{
		"organization_id": "00000000-0000-4000-8000-0000000000aa",
		"terminal_type":   "gnome-terminal",
		"app_entrypoint":  "cli",
		"app_version":     "2.1.283",
		"os_type":         "linux",
		"host_arch":       "amd64",
		"identity_source": "gateway",
	} {
		if environment[key] != want {
			t.Fatalf("environment[%q] = %#v, want %#v", key, environment[key], want)
		}
	}
	assertStringMembers(t, environment["workspace_host_paths"], []string{"/home/synthetic/workspace", "/home/synthetic/other"})
	assertStringMembers(t, environment["user_groups"], []string{"team-synthetic", "dept-synthetic"})

	resource, ok := event.ProviderExtensions["resource"].(map[string]any)
	if !ok {
		t.Fatalf("missing provider_extensions.resource: %#v", event.ProviderExtensions)
	}
	assertStringMembers(t, resource["workspace.host_paths"], []string{"/home/synthetic/workspace", "/home/synthetic/other"})
	assertStringMembers(t, resource["user.groups"], []string{"team-synthetic", "dept-synthetic"})
}

// assertStringMembers asserts a retained array-valued attribute decoded to the
// expected ordered string members, accepting either the []string the decoder
// produces or the []any a JSON round-trip yields.
func assertStringMembers(t *testing.T, value any, want []string) {
	t.Helper()
	var got []string
	switch members := value.(type) {
	case []string:
		got = members
	case []any:
		for _, item := range members {
			text, ok := item.(string)
			if !ok {
				t.Fatalf("array member %#v is not a string", item)
			}
			got = append(got, text)
		}
	default:
		t.Fatalf("value %#v is not a string array", value)
	}
	if len(got) != len(want) {
		t.Fatalf("array = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("array[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestNormalizeLogsUserPromptLengthOnlyNeverFabricatesContent proves the wire
// adapter reports prompt_length with no prompt key when content logging is off.
func TestNormalizeLogsUserPromptLengthOnlyNeverFabricatesContent(t *testing.T) {
	payload := `{"resourceLogs":[{"resource":{"attributes":[
	  {"key":"service.name","value":{"stringValue":"claude-code"}},
	  {"key":"service.version","value":{"stringValue":"2.1.270"}}]},
	 "scopeLogs":[{"logRecords":[{"attributes":[
	   {"key":"event.name","value":{"stringValue":"user_prompt"}},
	   {"key":"event.timestamp","value":{"stringValue":"2026-09-13T18:53:08.640Z"}},
	   {"key":"event.sequence","value":{"intValue":"8"}},
	   {"key":"session.id","value":{"stringValue":"synthetic-content-session"}},
	   {"key":"prompt_length","value":{"intValue":"128"}}]}]}]}]}`
	events, err := NormalizeLogs([]byte(payload), time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	echo := eventEcho(t, events[0])
	if echo["prompt_length"] != float64(128) {
		t.Fatalf("prompt_length not captured: %#v", echo)
	}
	if _, present := echo["prompt"]; present {
		t.Fatalf("prompt must never be fabricated when unlogged: %#v", echo)
	}
}

// normalizeByID normalises a reviewed fixture and keys the events by EventID.
func normalizeByID(t *testing.T, fixture string) map[string]canonical.Event {
	t.Helper()
	events, err := NormalizeEvents(readFixture(t, fixture))
	if err != nil {
		t.Fatalf("normalise %s: %v", fixture, err)
	}
	byID := make(map[string]canonical.Event, len(events))
	for _, event := range events {
		byID[event.EventID] = event
	}
	return byID
}

// eventEcho returns the provider_extensions.event map for one canonical event.
func eventEcho(t *testing.T, event canonical.Event) map[string]any {
	t.Helper()
	echo, ok := event.ProviderExtensions["event"].(map[string]any)
	if !ok {
		t.Fatalf("provider_extensions.event missing for %q", event.EventID)
	}
	return echo
}
