package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
)

func TestClaudeTraceIngestProjectsSpanEvidence(t *testing.T) {
	server, _ := newPersistentTestServer(t)
	response := postOTLPToPath(t, server.URL, "/v1/traces", metricsFixturePayloadBytes(t, "claude-code-2.1.268-trace-spans-otlp.json"), otlpContentTypeJSON)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("trace ingest status = %d", response.StatusCode)
	}
	closeBody(t, response)

	const sessionID = "claude-code:00000000-0000-4000-8000-000000000001"
	first := getSpanPage(t, server.URL+"/api/v1/sessions/"+url.PathEscape(sessionID)+"/spans?limit=1")
	assertFirstClaudeSpanPage(t, first)
	second := getSpanPage(t, server.URL+"/api/v1/sessions/"+url.PathEscape(sessionID)+"/spans?limit=1&cursor="+url.QueryEscape(*first.Pagination.NextCursor))
	assertSecondClaudeSpanPage(t, second)
}

const claudeToolContentSpansFixture = "claude-code-2.1.287-tool-content-spans-otlp.json"

// TestClaudeToolContentSpanIngestServesRawEvidence is the #253 live daemon
// ingest→read gate: the 2.1.287 capture (OTEL_LOG_TOOL_CONTENT + detailed beta
// tracing) is POSTed to /v1/traces and the event-detail read API serves the
// claude_code.tool span's raw tool.output span event, its new_context tool result,
// the previously dropped tool_input attribute, and the operator identity raw.
func TestClaudeToolContentSpanIngestServesRawEvidence(t *testing.T) {
	server, repository := newPersistentTestServer(t)
	response := postOTLPToPath(t, server.URL, "/v1/traces", metricsFixturePayloadBytes(t, claudeToolContentSpansFixture), otlpContentTypeJSON)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("trace ingest status = %d", response.StatusCode)
	}
	closeBody(t, response)
	stored, err := repository.ListEvents(context.Background(), storage.EventFilter{SessionID: "claude-code:25325325-3253-4253-8253-253253253253", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(stored, func(event canonical.Event) bool { return event.EventType == "claude_code.tool" })
	if len(stored) != 7 || index < 0 {
		t.Fatalf("stored span events = %d (tool index %d), want 7 with a tool span", len(stored), index)
	}
	assertContainsAll(t, []string{
		`"span_events":[`,
		`"name":"tool.output"`,
		`"output":"https://github.com/acme-synthetic/telemetryiq/pull/253"`,
		`"new_context":"[TOOL RESULT: Bash]`,
		`"tool_input":"[TOOL INPUT: Bash]`,
		"synthetic-253@example.test",
	}, marshalJSON(t, fetchStoredEventDetail(t, server.URL, stored[index])))
}

// TestClaudeToolIngestPromotesObservedPRLink is the #183 / #251 live daemon
// ingest→read gate. Each retained Claude tool surface — a tool span's raw
// full_command or (#253) its new_context and tool.output span event captured live
// from 2.1.287 (/v1/traces), the tool_decision/tool_result tool_parameters and
// tool_input captured live from 2.1.286 (/v1/logs), and a JSONL tool_result
// output captured live from 2.1.286 (/v1/claude/transcript) — is POSTed on its
// own route, and the session read API reports pr_link through the same
// provider-agnostic aggregation (attachSessionPRLink → PRLinkAvailability) the
// Codex path uses. Two distinct URLs in one session stay partial, and the
// pull_request.count metric alone never becomes a link.
func TestClaudeToolIngestPromotesObservedPRLink(t *testing.T) {
	const liveURL = "https://github.com/acme-synthetic/telemetryiq/pull/251"
	logs := metricsFixturePayloadBytes(t, "claude-code-2.1.286-tool-params-pr-link-otlp.json")
	cases := []struct {
		name        string
		path        string
		contentType string
		body        []byte
		wantLink    string
		wantState   string
	}{
		{name: "tool span full_command", path: "/v1/traces", contentType: otlpContentTypeJSON, body: metricsFixturePayloadBytes(t, "claude-code-2.1.273-tool-pr-link-otlp.json"), wantLink: "https://github.com/acme-synthetic/telemetryiq/pull/183", wantState: "observed"},
		{name: "tool span new_context and tool.output", path: "/v1/traces", contentType: otlpContentTypeJSON, body: metricsFixturePayloadBytes(t, claudeToolContentSpansFixture), wantLink: "https://github.com/acme-synthetic/telemetryiq/pull/253", wantState: "observed"},
		{name: "log tool_parameters and tool_input", path: "/v1/logs", contentType: otlpContentTypeJSON, body: logs, wantLink: liveURL, wantState: "observed"},
		{name: "transcript tool output", path: "/v1/claude/transcript", contentType: "application/x-ndjson", body: transcriptFixturePayloadNDJSON(t, "claude-code-2.1.286-tool-output-pr-link-transcript.json"), wantLink: liveURL, wantState: "observed"},
		// The first URL occurrence is the tool_decision tool_parameters; rewriting
		// only it leaves the tool_result URL intact, so one session holds two.
		{name: "conflicting candidates", path: "/v1/logs", contentType: otlpContentTypeJSON, body: bytes.Replace(logs, []byte("pull/251"), []byte("pull/252"), 1), wantState: "partial"},
		{name: "pull_request.count only", path: "/v1/metrics", contentType: otlpContentTypeJSON, body: metricsFixturePayloadBytes(t, "claude-code-2.1.268-code-output-metrics.json"), wantState: "unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := newPersistentTestServer(t)
			response := postOTLPToPath(t, server.URL, tc.path, tc.body, tc.contentType)
			if response.StatusCode != http.StatusAccepted {
				t.Fatalf("%s ingest status = %d", tc.path, response.StatusCode)
			}
			closeBody(t, response)
			assertEverySessionPRLink(t, server.URL, tc.wantLink, tc.wantState)
		})
	}
}

// assertEverySessionPRLink requires at least one session and that every session
// reports exactly the wanted pr_link value and availability state.
func assertEverySessionPRLink(t *testing.T, baseURL, wantLink, wantState string) {
	t.Helper()
	sessions := fetchSessionList(t, baseURL+"/api/v1/sessions?limit=10")
	if len(sessions.Data) == 0 {
		t.Fatal("ingest must produce a session")
	}
	for _, session := range sessions.Data {
		link, _ := session.Attributes["pr_link"].(string)
		if link != wantLink || session.Availability["pr_link"] != wantState {
			t.Fatalf("session %s pr_link = %q/%q, want %q/%q", session.SessionID, link, session.Availability["pr_link"], wantLink, wantState)
		}
	}
}

// transcriptFixturePayloadNDJSON re-serialises a transcript fixture's
// transcript_lines as the newline-delimited JSON the /v1/claude/transcript route
// accepts, preserving numbers verbatim.
func transcriptFixturePayloadNDJSON(t *testing.T, name string) []byte {
	t.Helper()
	var payload struct {
		Lines []json.RawMessage `json:"transcript_lines"`
	}
	if err := json.Unmarshal(metricsFixturePayloadBytes(t, name), &payload); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	for _, line := range payload.Lines {
		if err := json.Compact(&body, line); err != nil {
			t.Fatal(err)
		}
		body.WriteByte('\n')
	}
	return body.Bytes()
}

// hookSpanUnforeseenCanary is the synthetic value withHookSpanCanaries injects
// under an unforeseen attribute key (api_key); having no typed home, it is echoed
// raw into the persisted span_attributes (#253 — nothing dropped at the local-only
// ingest boundary, owner directive / epic #87, mirroring the logs path). The
// identity canary injected alongside is homed raw under
// provider_extensions.environment per #107 X20.
const hookSpanUnforeseenCanary = "tiq-canary-api-key"

// hookSpanIdentityCanary is the synthetic operator email withHookSpanCanaries
// injects; per #107 X20 it is retained raw under provider_extensions.environment
// (nothing dropped at the local-only boundary — owner directive / epic #87).
const hookSpanIdentityCanary = "tiq-canary@example.test"

// TestClaudeHookSpansIngestEndToEnd is the #103 (T16) live daemon ingest→read
// gate: the synthetic claude_code.hook spans fixture — augmented with a canary
// identity attribute and a canary secret — is POSTed to /v1/traces, and the HTTP
// read API serves the two hook events (typed hook block, duration, honest
// unavailable_fields). The unforeseen attribute is retained raw in the persisted
// span_attributes echo (#253) and the identity attribute raw under
// provider_extensions.environment (#107 X20 — nothing dropped at the local-only
// boundary). The gated hook_definitions is retained raw in the typed
// block only (epic #87), proving TelemetryIQ carries the hook intervention surface
// rather than dropping it.
func TestClaudeHookSpansIngestEndToEnd(t *testing.T) {
	server, repository := newPersistentTestServer(t)
	payload := withHookSpanCanaries(t, metricsFixturePayloadBytes(t, "claude-code-2.1.268-hook-spans-otlp.json"))
	response := postOTLPToPath(t, server.URL, "/v1/traces", payload, otlpContentTypeJSON)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("hook span ingest status = %d", response.StatusCode)
	}
	closeBody(t, response)

	const sessionID = "claude-code:00000000-0000-4000-8000-000000000103"
	assertHookSpanTimeline(t, server, sessionID)
	assertHookSpanStorage(t, repository, sessionID)
}

// withHookSpanCanaries appends a synthetic identity attribute and an unforeseen
// attribute to the first claude_code.hook span so the live gate proves both homes
// end to end: the unforeseen key rides raw in span_attributes (#253), and the
// identity key raw in provider_extensions.environment (#107 X20).
func withHookSpanCanaries(t *testing.T, payload []byte) []byte {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatal(err)
	}
	spans := hookFixtureSpans(t, envelope)
	hookSpan, ok := spans[1].(map[string]any)
	if !ok {
		t.Fatalf("hook fixture span[1] is not an object: %#v", spans[1])
	}
	attributes, ok := hookSpan["attributes"].([]any)
	if !ok {
		t.Fatalf("hook fixture span[1] has no attributes: %#v", hookSpan)
	}
	hookSpan["attributes"] = append(attributes,
		otlpStringAttribute("user.email", "tiq-canary@example.test"),
		otlpStringAttribute("api_key", hookSpanUnforeseenCanary),
	)
	return marshalJSON(t, envelope)
}

// hookFixtureSpans navigates the single resource/scope of the hook fixture to
// its span slice, failing loudly if the committed shape drifts.
func hookFixtureSpans(t *testing.T, envelope map[string]any) []any {
	t.Helper()
	resourceSpans, ok := envelope["resourceSpans"].([]any)
	if !ok || len(resourceSpans) == 0 {
		t.Fatalf("hook fixture missing resourceSpans: %#v", envelope)
	}
	resource, ok := resourceSpans[0].(map[string]any)
	if !ok {
		t.Fatalf("hook fixture resourceSpans[0] is not an object: %#v", resourceSpans[0])
	}
	scopeSpans, ok := resource["scopeSpans"].([]any)
	if !ok || len(scopeSpans) == 0 {
		t.Fatalf("hook fixture missing scopeSpans: %#v", resource)
	}
	scope, ok := scopeSpans[0].(map[string]any)
	if !ok {
		t.Fatalf("hook fixture scopeSpans[0] is not an object: %#v", scopeSpans[0])
	}
	spans, ok := scope["spans"].([]any)
	if !ok || len(spans) < 2 {
		t.Fatalf("hook fixture needs the interaction root plus hook spans: %#v", scope)
	}
	return spans
}

// otlpStringAttribute builds one OTLP key/stringValue attribute for injection.
func otlpStringAttribute(key, value string) map[string]any {
	return map[string]any{"key": key, "value": map[string]any{"stringValue": value}}
}

// assertHookSpanTimeline reads the session timeline through the live HTTP read
// API and asserts both hook events surface with their honest unavailable_fields;
// the span envelopes (interval and status) are proven through the /spans
// projection.
func assertHookSpanTimeline(t *testing.T, server *httptest.Server, sessionID string) {
	t.Helper()
	timeline := timelinePage(t, server.URL+"/api/v1/sessions/"+url.PathEscape(sessionID)+"/events?limit=10")
	hooks := 0
	for _, event := range timeline.Data {
		if event.EventType != "claude_code.hook" {
			continue
		}
		hooks++
		if !slices.Contains(event.UnavailableFields, "command_execution") {
			t.Fatalf("hook event dropped honest unavailable_fields: %#v", event.UnavailableFields)
		}
	}
	if hooks != 2 {
		t.Fatalf("hook timeline events = %d, want 2: %#v", hooks, timeline.Data)
	}
	assertHookSpanEnvelopes(t, server, sessionID)
}

// assertHookSpanEnvelopes proves the /spans projection serves both hook spans
// with their derived interval and provider status (success and blocking).
func assertHookSpanEnvelopes(t *testing.T, server *httptest.Server, sessionID string) {
	t.Helper()
	page := getSpanPage(t, server.URL+"/api/v1/sessions/"+url.PathEscape(sessionID)+"/spans?limit=10")
	statusByDuration := map[int64]int64{}
	for _, record := range page.Data {
		if record.Name == nil || *record.Name != "claude_code.hook" {
			continue
		}
		if record.DurationMs == nil || record.StatusCode == nil {
			t.Fatalf("hook span missing interval/status: %#v", record)
		}
		statusByDuration[*record.DurationMs] = *record.StatusCode
	}
	success, successOK := statusByDuration[45]
	blocking, blockingOK := statusByDuration[12]
	if len(statusByDuration) != 2 || !successOK || success != 0 || !blockingOK || blocking != 2 {
		t.Fatalf("hook span envelopes = %v, want {45:0, 12:2}", statusByDuration)
	}
}

// assertHookSpanStorage proves the persisted hook events carry the typed hook
// block with the gated hook_definitions retained raw; that the injected unforeseen
// attribute is retained raw in the span_attributes echo (#253); and that the
// injected identity attribute is retained raw under provider_extensions.environment
// (#107 X20), reading back on the span that carried it.
func assertHookSpanStorage(t *testing.T, repository storage.Repository, sessionID string) {
	t.Helper()
	stored, err := repository.ListEvents(context.Background(), storage.EventFilter{SessionID: sessionID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	hooks := 0
	for _, event := range stored {
		if event.EventType != "claude_code.hook" {
			continue
		}
		hooks++
		assertHookBlock(t, event)
	}
	if hooks != 2 {
		t.Fatalf("stored hook events = %d, want 2", hooks)
	}
	encoded := marshalJSON(t, stored)
	assertRawToolEvidence(t, encoded, `"api_key":"`+hookSpanUnforeseenCanary+`"`)
	assertEnvironmentIdentity(t, stored, map[string]string{"user_email": hookSpanIdentityCanary})
	assertRawToolEvidence(t, encoded, "./scripts/format-guard.sh", "./scripts/deny-network.sh")
}

// assertHookBlock checks one persisted hook event exposes the typed hook block
// (including gated hook_definitions) and that neither the identity canary nor the
// gated field is duplicated into the span_attributes echo — each has its own typed
// home (provider_extensions.environment, #107; the hook block, #103).
func assertHookBlock(t *testing.T, event canonical.Event) {
	t.Helper()
	block, ok := event.Attributes["hook"].(map[string]any)
	if !ok {
		t.Fatalf("hook event missing typed hook block: %#v", event.Attributes)
	}
	if block["hook_event"] != "PreToolUse" {
		t.Fatalf("hook_event = %v, want PreToolUse", block["hook_event"])
	}
	if _, present := block["hook_definitions"]; !present {
		t.Fatalf("gated hook_definitions dropped from typed block: %#v", block)
	}
	extensions, ok := event.ProviderExtensions["span_attributes"].(map[string]any)
	if !ok {
		t.Fatalf("hook event missing span_attributes passthrough: %#v", event.ProviderExtensions)
	}
	for _, key := range []string{"user.email", "hook_definitions"} {
		if _, present := extensions[key]; present {
			t.Fatalf("span_attributes duplicated homed key %q: %#v", key, extensions)
		}
	}
}

func assertFirstClaudeSpanPage(t *testing.T, page spanListResponse) {
	t.Helper()
	if len(page.Data) != 1 || page.Data[0].TraceID != "00000000000000000000000000000001" || page.Data[0].SpanID != "0000000000000002" || page.Data[0].ParentAvailability != "root" || page.Data[0].DurationMs == nil || page.Data[0].StatusCode == nil || *page.Data[0].StatusCode != 0 || page.Pagination.NextCursor == nil {
		t.Fatalf("unexpected first Claude span page")
	}
}

func assertSecondClaudeSpanPage(t *testing.T, page spanListResponse) {
	t.Helper()
	if len(page.Data) != 1 || page.Data[0].SpanID != "0000000000000001" || page.Data[0].ParentSpanID == nil || *page.Data[0].ParentSpanID != "0000000000000002" || page.Data[0].ParentAvailability != "not_loaded" || page.Data[0].DurationMs == nil || page.Data[0].StatusCode == nil || *page.Data[0].StatusCode != 0 {
		t.Fatalf("unexpected second Claude span page")
	}
}
