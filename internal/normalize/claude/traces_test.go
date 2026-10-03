package claude

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// tracesFixturePayload extracts the OTLP payload from the fixture wrapper so it
// is replayed exactly as the /v1/traces route would hand it to the adapter.
func tracesFixturePayload(t testing.TB, name string) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(readFixture(t, name), &document); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	payload, err := json.Marshal(document["payload"])
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return payload
}

func TestNormalizeTracesGolden(t *testing.T) {
	payload := tracesFixturePayload(t, "claude-code-2.1.268-trace-spans-otlp.json")
	receivedAt := time.Date(2026, 9, 11, 10, 5, 51, 0, time.UTC)

	first, err := NormalizeTraces(payload, receivedAt)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := NormalizeTraces(payload, receivedAt)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("normalisation must be deterministic")
	}
	assertClaudeSpanTree(t, first)
	assertMatchesGolden(t, "claude-code-2.1.268-trace-spans.events.json", first)
}

// assertClaudeSpanTree proves the interaction → llm_request hierarchy survives:
// the root interaction span carries no parent, and the llm_request span's parent
// is the interaction span — the correlation that spans expose and logs do not.
func assertClaudeSpanTree(t *testing.T, events []canonical.Event) {
	t.Helper()
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2 spans (interaction + llm_request)", len(events))
	}
	byType := make(map[string]canonical.Event, len(events))
	for _, event := range events {
		byType[event.EventType] = event
		if event.Provider != provider || event.Tool != tool {
			t.Fatalf("provider/tool = %q/%q", event.Provider, event.Tool)
		}
		if event.SessionID != "claude-code:00000000-0000-4000-8000-000000000001" {
			t.Fatalf("session id = %q, want raw provider-native session id", event.SessionID)
		}
	}

	interaction, ok := byType["claude_code.interaction"]
	if !ok {
		t.Fatalf("missing interaction span in %#v", events)
	}
	if parent := spanField(t, interaction, "parent_span_id"); parent != "" {
		t.Fatalf("interaction parent_span_id = %q, want empty (root)", parent)
	}

	llm, ok := byType["claude_code.llm_request"]
	if !ok {
		t.Fatalf("missing llm_request span in %#v", events)
	}
	if got, want := spanField(t, llm, "parent_span_id"), spanField(t, interaction, "span_id"); got != want {
		t.Fatalf("llm_request parent_span_id = %q, want interaction span_id %q", got, want)
	}
	if llm.Attributes["model"] != "claude-haiku-4-5-20251001" {
		t.Fatalf("llm_request model = %#v", llm.Attributes["model"])
	}
	if attrs, _ := llm.ProviderExtensions["span_attributes"].(map[string]any); attrs["stop_reason"] != "end_turn" {
		t.Fatalf("llm_request stop_reason not preserved: %#v", llm.ProviderExtensions["span_attributes"])
	}

	assertTypedSpanFields(t, interaction, llm)
}

// assertTypedSpanFields proves the #100 typed field mapping on the two per-prompt
// span types: the interaction root's raised task boundary and prompt-shape block,
// and the llm_request child's unchanged boundary plus its decoded finish_reasons
// and latency/token/outcome block.
func assertTypedSpanFields(t *testing.T, interaction, llm canonical.Event) {
	t.Helper()
	// The interaction root is a genuine task boundary, so #100 raises its
	// confidence above the deferred "unknown"; the llm_request child is not
	// itself a boundary and stays "unknown".
	if got := boundaryConfidence(t, interaction); got != "observed" {
		t.Fatalf("interaction task_boundary confidence = %q, want observed (per-prompt root)", got)
	}
	if got := boundaryConfidence(t, llm); got != "unknown" {
		t.Fatalf("llm_request task_boundary confidence = %q, want unknown (not a boundary)", got)
	}

	// The typed interaction block carries prompt-shape/queueing signals only —
	// never tokens or the prompt text.
	interactionBlock, ok := interaction.Attributes["interaction"].(map[string]any)
	if !ok {
		t.Fatalf("interaction span missing typed interaction block: %#v", interaction.Attributes)
	}
	assertBlockFields(t, "interaction", interactionBlock, map[string]any{
		"sequence":           int64(1),
		"duration_ms":        int64(2600),
		"user_prompt_length": int64(33),
		"queued_sends":       int64(0),
		"parent_source":      "none",
	})

	// The typed llm_request block carries decoded finish_reasons (the OTLP
	// arrayValue the scalar decoder cannot read) plus latency/token/outcome.
	llmBlock, ok := llm.Attributes["llm_request"].(map[string]any)
	if !ok {
		t.Fatalf("llm_request span missing typed llm_request block: %#v", llm.Attributes)
	}
	if reasons, _ := llmBlock["finish_reasons"].([]string); !reflect.DeepEqual(reasons, []string{"end_turn"}) {
		t.Fatalf("llm_request finish_reasons = %#v, want [end_turn] (arrayValue decoded)", llmBlock["finish_reasons"])
	}
	assertBlockFields(t, "llm_request", llmBlock, map[string]any{
		"stop_reason":  "end_turn",
		"context":      "interaction",
		"input_tokens": int64(10),
		"ttft_ms":      int64(1918),
		"success":      true,
	})
}

// assertBlockFields fails if any want entry is missing or unequal in block.
func assertBlockFields(t *testing.T, name string, block map[string]any, want map[string]any) {
	t.Helper()
	for key, value := range want {
		if block[key] != value {
			t.Fatalf("%s.%s = %#v, want %#v", name, key, block[key], value)
		}
	}
}

// boundaryConfidence reads provider_extensions.correlation.task_boundary.confidence.
func boundaryConfidence(t *testing.T, event canonical.Event) string {
	t.Helper()
	correlation, ok := event.ProviderExtensions["correlation"].(map[string]any)
	if !ok {
		t.Fatalf("event %q missing correlation extension: %#v", event.EventType, event.ProviderExtensions)
	}
	boundary, ok := correlation["task_boundary"].(map[string]any)
	if !ok {
		t.Fatalf("event %q missing task_boundary: %#v", event.EventType, correlation)
	}
	confidence, _ := boundary["confidence"].(string)
	return confidence
}

func spanField(t *testing.T, event canonical.Event, key string) string {
	t.Helper()
	span, ok := event.ProviderExtensions["span"].(map[string]any)
	if !ok {
		t.Fatalf("event %q missing span extension: %#v", event.EventType, event.ProviderExtensions)
	}
	value, _ := span[key].(string)
	return value
}

// correlationField reads a string value from provider_extensions.correlation,
// reporting whether the key is present (so present-only promotion can be asserted
// without treating an absent key as an empty string).
func correlationField(t *testing.T, event canonical.Event, key string) (string, bool) {
	t.Helper()
	correlation, ok := event.ProviderExtensions["correlation"].(map[string]any)
	if !ok {
		t.Fatalf("event %q missing correlation extension: %#v", event.EventType, event.ProviderExtensions)
	}
	value, present := correlation[key].(string)
	return value, present
}

// TestNormalizeTracesPromotesWorkflowCorrelation proves the sub-agent workflow
// identifiers ride into span provider_extensions.correlation (#106): every span in
// the sub-agent fixture that carries workflow.run_id/workflow.name on the wire
// exposes the same workflow_run_id/workflow_name under correlation, so a consumer
// can group a workflow's spans. Present-only: a main-session span set that never
// carries workflow.* exposes no such key (never fabricated or zero-filled).
func TestNormalizeTracesPromotesWorkflowCorrelation(t *testing.T) {
	events, err := NormalizeTraces(tracesFixturePayload(t, "claude-code-2.1.268-subagent-spans-otlp.json"), subAgentReceivedAt)
	if err != nil {
		t.Fatalf("normalize sub-agent spans: %v", err)
	}
	var promoted int
	for _, event := range events {
		runID, present := correlationField(t, event, "workflow_run_id")
		if !present {
			continue
		}
		promoted++
		if runID != "wf_synthetic0001" {
			t.Fatalf("workflow_run_id = %q, want wf_synthetic0001", runID)
		}
		if name, ok := correlationField(t, event, "workflow_name"); !ok || name != "custom" {
			t.Fatalf("workflow_name = %q (present=%v), want custom", name, ok)
		}
	}
	if promoted == 0 {
		t.Fatal("no span exposed workflow_run_id under correlation; #106 promotion missing")
	}

	// Present-only: the main-session trace-spans fixture carries no workflow.*, so
	// no span may fabricate a workflow_run_id.
	mainSession, err := NormalizeTraces(tracesFixturePayload(t, "claude-code-2.1.268-trace-spans-otlp.json"), subAgentReceivedAt)
	if err != nil {
		t.Fatalf("normalize main-session spans: %v", err)
	}
	for _, event := range mainSession {
		if _, present := correlationField(t, event, "workflow_run_id"); present {
			t.Fatalf("main-session span fabricated workflow_run_id: %#v", event.ProviderExtensions["correlation"])
		}
	}
}

// TestNormalizeTracesRejectsNonClaudeService proves a non-Claude spans payload
// yields the skip sentinel (not misattributed), so a mixed/other-tool batch is
// safe — matching the metrics/logs routing contract.
func TestNormalizeTracesRejectsNonClaudeService(t *testing.T) {
	payload := []byte(`{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}}]},"scopeSpans":[{"spans":[{"traceId":"0123456789abcdef0123456789abcdef","spanId":"0123456789abcdef","name":"codex.turn"}]}]}]}`)
	_, err := NormalizeTraces(payload, time.Now().UTC())
	if !errors.Is(err, ErrUnsupportedTraces) {
		t.Fatalf("got %v, want ErrUnsupportedTraces", err)
	}
}

// TestNormalizeTracesMalformedSpanIsError proves the routing contract: a
// claude-code span missing a required structural field (span id, name, or start
// time) is a real error, NOT the skip sentinel, so the route does not silently
// 202-accept and drop — or persist a schema-invalid — supported Claude span. An
// empty name would otherwise become an empty event_type, which the canonical
// event schema forbids; a missing start time would fabricate chronology.
func TestNormalizeTracesMalformedSpanIsError(t *testing.T) {
	cases := map[string]string{
		"missing span id":    `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeSpans":[{"spans":[{"traceId":"0123456789abcdef0123456789abcdef","name":"claude_code.interaction","startTimeUnixNano":"1789117549920000000"}]}]}]}`,
		"empty name":         `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeSpans":[{"spans":[{"traceId":"0123456789abcdef0123456789abcdef","spanId":"0123456789abcdef","name":"","startTimeUnixNano":"1789117549920000000"}]}]}]}`,
		"missing start time": `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeSpans":[{"spans":[{"traceId":"0123456789abcdef0123456789abcdef","spanId":"0123456789abcdef","name":"claude_code.interaction"}]}]}]}`,
		"non-positive time":  `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeSpans":[{"spans":[{"traceId":"0123456789abcdef0123456789abcdef","spanId":"0123456789abcdef","name":"claude_code.interaction","startTimeUnixNano":"0"}]}]}]}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NormalizeTraces([]byte(payload), time.Now().UTC())
			if err == nil || errors.Is(err, ErrUnsupportedTraces) {
				t.Fatalf("got %v, want a hard normalisation error", err)
			}
		})
	}
}

// TestNormalizeTracesRootSpanParentIsNull proves a root span's absent parent is
// preserved as null, not an empty string, so consumers can distinguish a genuine
// root from a literal empty parent (correlation contract: unknowns are never
// empty strings).
func TestNormalizeTracesRootSpanParentIsNull(t *testing.T) {
	payload := []byte(`{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeSpans":[{"spans":[{"traceId":"0123456789abcdef0123456789abcdef","spanId":"0123456789abcdef","name":"claude_code.interaction","startTimeUnixNano":"1789117549920000000"}]}]}]}`)
	events, err := NormalizeTraces(payload, time.Now().UTC())
	if err != nil {
		t.Fatalf("NormalizeTraces: %v", err)
	}
	span := events[0].ProviderExtensions["span"].(map[string]any)
	if span["parent_span_id"] != nil {
		t.Fatalf("root span parent_span_id = %#v, want nil", span["parent_span_id"])
	}
	correlation := events[0].ProviderExtensions["correlation"].(map[string]any)
	if correlation["parent_span_id"] != nil {
		t.Fatalf("root correlation parent_span_id = %#v, want nil", correlation["parent_span_id"])
	}
}

// TestNormalizeTracesSessionFallbackIsTraceScoped proves that when a span carries
// no session.id the session identity falls back to the trace id, so spans from
// different traces are not merged into one synthetic "unknown" session.
func TestNormalizeTracesSessionFallbackIsTraceScoped(t *testing.T) {
	payload := []byte(`{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeSpans":[{"spans":[{"traceId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","spanId":"0123456789abcdef","name":"claude_code.interaction","startTimeUnixNano":"1789117549920000000"},{"traceId":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","spanId":"fedcba9876543210","name":"claude_code.interaction","startTimeUnixNano":"1789117549920000000"}]}]}]}`)
	events, err := NormalizeTraces(payload, time.Now().UTC())
	if err != nil {
		t.Fatalf("NormalizeTraces: %v", err)
	}
	sessions := map[string]struct{}{}
	for _, event := range events {
		if event.SessionID == "claude-code:unknown" {
			t.Fatalf("session id collapsed to the shared unknown sentinel: %#v", event)
		}
		sessions[event.SessionID] = struct{}{}
	}
	if len(sessions) != 2 {
		t.Fatalf("distinct traces merged into %d session(s), want 2: %#v", len(sessions), sessions)
	}
}

// TestNormalizeClaudeSharedSessionCorrelation proves, from one version-pinned
// synthetic run, that all THREE Claude surfaces — OTLP trace spans, OTLP
// conversation logs, and the on-disk session JSONL transcript — normalise to the
// SAME provider-native session id. This is the fixture-backed evidence for the
// #210 correlation contract: the join key is the raw session.id, and nothing
// heuristic (time/model/prompt) participates. The three fixtures
// (…-trace-conversation-otlp.json / …-log-conversation-otlp.json /
// …-transcript-conversation.json) were captured from one run whose transcript
// file was named by, and whose records carried, the same session UUID as the
// OTLP session.id before sanitisation.
func TestNormalizeClaudeSharedSessionCorrelation(t *testing.T) {
	receivedAt := time.Date(2026, 9, 27, 9, 35, 30, 0, time.UTC)
	const wantSession = "claude-code:tiq-corr-210"

	tracePayload := tracesFixturePayload(t, "claude-code-2.1.283-trace-conversation-otlp.json")
	logPayload := tracesFixturePayload(t, "claude-code-2.1.283-log-conversation-otlp.json")
	transcriptPayload := transcriptFixtureNDJSON(t, "claude-code-2.1.283-transcript-conversation.json")

	traceEvents, err := NormalizeTraces(tracePayload, receivedAt)
	if err != nil {
		t.Fatalf("NormalizeTraces: %v", err)
	}
	traceRepeat, err := NormalizeTraces(tracePayload, receivedAt)
	if err != nil {
		t.Fatalf("NormalizeTraces repeat: %v", err)
	}
	logEvents, err := NormalizeLogs(logPayload, receivedAt)
	if err != nil {
		t.Fatalf("NormalizeLogs: %v", err)
	}
	logRepeat, err := NormalizeLogs(logPayload, receivedAt)
	if err != nil {
		t.Fatalf("NormalizeLogs repeat: %v", err)
	}
	transcriptEvents, err := NormalizeTranscript(transcriptPayload, receivedAt)
	if err != nil {
		t.Fatalf("NormalizeTranscript: %v", err)
	}
	transcriptRepeat, err := NormalizeTranscript(transcriptPayload, receivedAt)
	if err != nil {
		t.Fatalf("NormalizeTranscript repeat: %v", err)
	}
	if !reflect.DeepEqual(traceEvents, traceRepeat) ||
		!reflect.DeepEqual(logEvents, logRepeat) ||
		!reflect.DeepEqual(transcriptEvents, transcriptRepeat) {
		t.Fatal("normalisation must be deterministic across surfaces")
	}
	if len(traceEvents) == 0 || len(logEvents) == 0 || len(transcriptEvents) == 0 {
		t.Fatalf("empty events: %d trace, %d log, %d transcript", len(traceEvents), len(logEvents), len(transcriptEvents))
	}
	assertAllEventsShareSession(t, "trace", traceEvents, wantSession)
	assertAllEventsShareSession(t, "log", logEvents, wantSession)
	assertAllEventsShareSession(t, "transcript", transcriptEvents, wantSession)
	// #259: the OTLP user_prompt message.uuid and the transcript user record
	// uuid are the same provider message id — the governance prompt join key.
	otlpUUID := correlationString(t, logEvents, "user_prompt", "message_uuid")
	if transcriptUUID := correlationString(t, transcriptEvents, "user_message", "uuid"); otlpUUID != transcriptUUID {
		t.Fatalf("user prompt uuid: otlp %q, transcript %q", otlpUUID, transcriptUUID)
	}
}

// correlationString returns the non-blank provider_extensions.correlation value
// of the single event of eventType, failing on zero or multiple matches.
func correlationString(t *testing.T, events []canonical.Event, eventType, key string) string {
	t.Helper()
	found := ""
	for _, event := range events {
		if event.EventType != eventType {
			continue
		}
		correlation, _ := event.ProviderExtensions["correlation"].(map[string]any)
		value, _ := correlation[key].(string)
		if found != "" || value == "" {
			t.Fatalf("%s correlation.%s must be one non-blank value, got %q after %q", eventType, key, value, found)
		}
		found = value
	}
	if found == "" {
		t.Fatalf("no %s event carries correlation.%s", eventType, key)
	}
	return found
}

// assertAllEventsShareSession fails unless every event carries the expected raw
// provider-native session id, proving the join key is uniform across a surface.
func assertAllEventsShareSession(t *testing.T, surface string, events []canonical.Event, want string) {
	t.Helper()
	for _, event := range events {
		if event.SessionID != want {
			t.Fatalf("%s event %q session id = %q, want %q", surface, event.EventType, event.SessionID, want)
		}
	}
}

// TestNormalizeTracesRetainsIdentityAndUnforeseenAttributes proves the raw-capture
// stance on an llm_request span (#107 X20, #253): the known identity attribute
// (user.email) rides raw into its typed home provider_extensions.environment and is
// not duplicated into span_attributes, while attributes with no typed home —
// including unforeseen ones (api_key, authorization) and the gated user_prompt — are
// echoed verbatim into span_attributes rather than dropped at the local-only ingest
// boundary (owner directive / epic #87), mirroring the logs path.
func TestNormalizeTracesRetainsIdentityAndUnforeseenAttributes(t *testing.T) {
	payload := []byte(`{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}},{"key":"service.version","value":{"stringValue":"2.1.268"}}]},"scopeSpans":[{"scope":{"name":"com.anthropic.claude_code.tracing"},"spans":[{"traceId":"0123456789abcdef0123456789abcdef","spanId":"0123456789abcdef","name":"claude_code.llm_request","startTimeUnixNano":"1789117549650000000","attributes":[{"key":"span.type","value":{"stringValue":"llm_request"}},{"key":"stop_reason","value":{"stringValue":"end_turn"}},{"key":"session.id","value":{"stringValue":"synthetic-session"}},{"key":"user.email","value":{"stringValue":"synthetic@example.test"}},{"key":"user_prompt","value":{"stringValue":"tiq-canary-prompt"}},{"key":"api_key","value":{"stringValue":"tiq-canary-api-key"}},{"key":"authorization","value":{"stringValue":"Bearer tiq-canary-token"}}]}]}]}]}`)
	events, err := NormalizeTraces(payload, time.Now().UTC())
	if err != nil {
		t.Fatalf("NormalizeTraces: %v", err)
	}
	assertIdentityHomedUnforeseenRaw(t, events[0], map[string]any{
		"stop_reason":   "end_turn",
		"session.id":    "synthetic-session",
		"user_prompt":   "tiq-canary-prompt",
		"api_key":       "tiq-canary-api-key",
		"authorization": "Bearer tiq-canary-token",
	})
}

// llmRequestBlock extracts the typed llm_request block from the single event a
// one-span payload produces, failing if the span was not mapped as llm_request.
func llmRequestBlock(t *testing.T, payload string) map[string]any {
	t.Helper()
	events, err := NormalizeTraces([]byte(payload), time.Now().UTC())
	if err != nil {
		t.Fatalf("NormalizeTraces: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	block, ok := events[0].Attributes["llm_request"].(map[string]any)
	if !ok {
		t.Fatalf("event missing typed llm_request block: %#v", events[0].Attributes)
	}
	return block
}

// spanPayload wraps one claude-code llm_request span carrying attrsJSON so the
// synthetic tests below stay to the attribute list under test.
func spanPayload(attrsJSON string) string {
	return `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}},{"key":"service.version","value":{"stringValue":"2.1.268"}}]},"scopeSpans":[{"scope":{"name":"com.anthropic.claude_code.tracing"},"spans":[{"traceId":"0123456789abcdef0123456789abcdef","spanId":"0123456789abcdef","name":"claude_code.llm_request","startTimeUnixNano":"1789117549650000000","attributes":[{"key":"span.type","value":{"stringValue":"llm_request"}}` + attrsJSON + `]}]}]}]}`
}

// TestNormalizeTracesFinishReasonsMultiValue proves the arrayValue decoder reads
// every string member, not just the first, so a multi-reason response survives.
func TestNormalizeTracesFinishReasonsMultiValue(t *testing.T) {
	block := llmRequestBlock(t, spanPayload(`,{"key":"gen_ai.response.finish_reasons","value":{"arrayValue":{"values":[{"stringValue":"tool_use"},{"stringValue":"max_tokens"}]}}}`))
	reasons, _ := block["finish_reasons"].([]string)
	if !reflect.DeepEqual(reasons, []string{"tool_use", "max_tokens"}) {
		t.Fatalf("finish_reasons = %#v, want [tool_use max_tokens]", block["finish_reasons"])
	}
}

// TestNormalizeTracesResponseHasToolCall proves the tool-calling signal is mapped
// present-only as a bool: true when observed, absent (not false) when unobserved.
func TestNormalizeTracesResponseHasToolCall(t *testing.T) {
	withCall := llmRequestBlock(t, spanPayload(`,{"key":"response.has_tool_call","value":{"boolValue":true}}`))
	if withCall["response_has_tool_call"] != true {
		t.Fatalf("response_has_tool_call = %#v, want true", withCall["response_has_tool_call"])
	}
	without := llmRequestBlock(t, spanPayload(`,{"key":"stop_reason","value":{"stringValue":"end_turn"}}`))
	if _, present := without["response_has_tool_call"]; present {
		t.Fatalf("absent response.has_tool_call must be omitted, not fabricated: %#v", without)
	}
}

// TestNormalizeTracesLLMRequestErrorRetry proves a failed, retried request maps
// the bounded outcome fields (attempt, success, status_code, error_class) AND the
// raw free-text error message (captured raw under epic #87 — the raw failure text
// is governance signal, matching tool.execution rather than being dropped "for
// consistency" with the pre-#87 default). The raw error lives in the typed block,
// never duplicated into the span_attributes allow-list.
func TestNormalizeTracesLLMRequestErrorRetry(t *testing.T) {
	payload := spanPayload(`,{"key":"attempt","value":{"intValue":2}},{"key":"success","value":{"boolValue":false}},{"key":"status_code","value":{"intValue":529}},{"key":"error_class","value":{"stringValue":"server_overload"}},{"key":"error","value":{"stringValue":"upstream connection reset"}}`)
	block := llmRequestBlock(t, payload)
	for key, want := range map[string]any{
		"attempt":     int64(2),
		"success":     false,
		"status_code": int64(529),
		"error_class": "server_overload",
		"error":       "upstream connection reset",
	} {
		if block[key] != want {
			t.Fatalf("llm_request.%s = %#v, want %#v", key, block[key], want)
		}
	}
	events := singleSpanEvent(t, payload)
	attrs, _ := events.ProviderExtensions["span_attributes"].(map[string]any)
	if _, present := attrs["error"]; present {
		t.Fatalf("raw error must live only in the typed block, not span_attributes: %#v", attrs)
	}
}

// TestNormalizeTracesToolSpansGolden replays the tool-span fixture (a Read tool
// call, and a Bash tool call that waits on a user permission then fails) and
// asserts the three typed tool blocks map every documented field raw — including
// the OTEL_LOG_TOOL_DETAILS-gated file_path/full_command/error — plus the
// span-type-aware unavailable_fields and the observed, non-boundary correlation.
func TestNormalizeTracesToolSpansGolden(t *testing.T) {
	payload := tracesFixturePayload(t, "claude-code-2.1.268-tool-spans-otlp.json")
	receivedAt := time.Date(2026, 9, 17, 10, 5, 51, 0, time.UTC)

	first, err := NormalizeTraces(payload, receivedAt)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := NormalizeTraces(payload, receivedAt)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("normalisation must be deterministic")
	}
	assertClaudeToolSpanTree(t, first)
	assertMatchesGolden(t, "claude-code-2.1.268-tool-spans.events.json", first)
}

// TestNormalizeTracesToolSpanPromotesPRLink proves the #183 contract: a verbatim
// pull-request URL in a tool span's raw full_command is extracted into
// pr_link_candidates (attributes) + pr_link_evidence (extensions) by the shared
// normalize.AttachPRLinkEvidence, while a span with no such URL (the interaction
// parent) stays silent — no fabricated or empty candidate. The session-level
// promotion to session.Attributes["pr_link"] is exercised by the storage
// aggregation (attachSessionPRLink) and the live ingest→read gate, not here.
func TestNormalizeTracesToolSpanPromotesPRLink(t *testing.T) {
	payload := tracesFixturePayload(t, "claude-code-2.1.273-tool-pr-link-otlp.json")
	receivedAt := time.Date(2026, 9, 20, 11, 20, 1, 0, time.UTC)

	first, err := NormalizeTraces(payload, receivedAt)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := NormalizeTraces(payload, receivedAt)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("normalisation must be deterministic")
	}

	bySpanID := make(map[string]canonical.Event, len(first))
	for _, event := range first {
		bySpanID[spanField(t, event, "span_id")] = event
	}

	const wantURL = "https://github.com/acme-synthetic/telemetryiq/pull/183"
	bash := bySpanID["0000000000000b02"]
	candidates, ok := bash.Attributes["pr_link_candidates"].([]string)
	if !ok || len(candidates) != 1 || candidates[0] != wantURL {
		t.Fatalf("bash pr_link_candidates = %#v, want [%q]", bash.Attributes["pr_link_candidates"], wantURL)
	}
	evidence, ok := bash.ProviderExtensions["pr_link_evidence"].([]map[string]string)
	if !ok || len(evidence) != 1 || evidence[0]["field"] != "full_command" || evidence[0]["url"] != wantURL {
		t.Fatalf("bash pr_link_evidence = %#v", bash.ProviderExtensions["pr_link_evidence"])
	}

	// The interaction parent has no command surface, so it must not carry a
	// candidate key at all (genuine absence, never an empty slice).
	interaction := bySpanID["0000000000000b01"]
	if _, present := interaction.Attributes["pr_link_candidates"]; present {
		t.Fatalf("interaction span must not carry pr_link_candidates, got %#v", interaction.Attributes["pr_link_candidates"])
	}
	if _, present := interaction.ProviderExtensions["pr_link_evidence"]; present {
		t.Fatalf("interaction span must not carry pr_link_evidence, got %#v", interaction.ProviderExtensions["pr_link_evidence"])
	}

	assertMatchesGolden(t, "claude-code-2.1.273-tool-pr-link.events.json", first)
}

// assertClaudeToolSpanTree proves the raw-capture contract for #101: each tool
// span type carries its typed block with every documented field (paths, commands,
// and free-text errors present verbatim), those raw values live only in the typed
// block (never duplicated into span_attributes), and tool spans stop declaring the
// tool/file/command surfaces unavailable.
func assertClaudeToolSpanTree(t *testing.T, events []canonical.Event) {
	t.Helper()
	if len(events) != 6 {
		t.Fatalf("event count = %d, want 6 (interaction + 2 tool + 2 tool.execution + tool.blocked_on_user)", len(events))
	}
	bySpanID := make(map[string]canonical.Event, len(events))
	for _, event := range events {
		bySpanID[spanField(t, event, "span_id")] = event
	}

	// The Read tool span carries the gated file_path raw, plus bounded identifiers.
	readTool := bySpanID["0000000000000a02"]
	assertBlockFields(t, "tool", toolBlock(t, readTool, "tool"), map[string]any{
		"tool_name":           "Read",
		"tool_name_safe":      "Read",
		"file_path":           "internal/service/handler.go",
		"tool_use_id":         "toolu_synthetic_read_0001",
		"gen_ai_tool_call_id": "toolu_synthetic_read_0001",
		"result_tokens":       int64(512),
		"duration_ms":         int64(200),
	})
	// A tool span carries tool/file/command evidence, so those surfaces must not be
	// declared unavailable (that would be a lie under the provider rule).
	assertUnavailableExcludes(t, readTool, "tool_io", "file_operations", "command_execution")
	if got := boundaryConfidence(t, readTool); got != "observed" {
		t.Fatalf("tool span task_boundary confidence = %q, want observed", got)
	}
	// The raw file_path lives only in the typed block, never in span_attributes.
	assertAbsentFromSpanAttributes(t, readTool, "file_path")

	// The Bash tool span carries the gated full_command raw plus the bash classes.
	bashTool := bySpanID["0000000000000a04"]
	assertBlockFields(t, "tool", toolBlock(t, bashTool, "tool"), map[string]any{
		"tool_name":          "Bash",
		"bash_command_class": "other",
		"bash_argv0":         "cat",
		"full_command":       "cat config/app.yaml",
	})
	assertAbsentFromSpanAttributes(t, bashTool, "full_command")

	// The failed tool.execution carries success:false, the bounded error_class, and
	// the raw free-text error message (captured raw under epic #87).
	exec := bySpanID["0000000000000a06"]
	assertBlockFields(t, "tool_execution", toolBlock(t, exec, "tool_execution"), map[string]any{
		"success":     false,
		"error_class": "ShellError",
		"error":       "cat: config/app.yaml: No such file or directory",
		"tool_use_id": "toolu_synthetic_bash_0002",
	})
	assertAbsentFromSpanAttributes(t, exec, "error")

	// The blocked_on_user span carries the permission decision, source, and wait.
	blocked := bySpanID["0000000000000a05"]
	assertBlockFields(t, "tool_blocked_on_user", toolBlock(t, blocked, "tool_blocked_on_user"), map[string]any{
		"decision":    "accept",
		"source":      "user_permanent",
		"duration_ms": int64(2000),
	})
}

// toolBlock extracts a typed span block (tool / tool_execution /
// tool_blocked_on_user) from an event, failing if the span was not mapped.
func toolBlock(t *testing.T, event canonical.Event, key string) map[string]any {
	t.Helper()
	block, ok := event.Attributes[key].(map[string]any)
	if !ok {
		t.Fatalf("event %q missing typed %q block: %#v", event.EventType, key, event.Attributes)
	}
	return block
}

// assertUnavailableExcludes fails if any of names is present in the event's
// unavailable_fields list.
func assertUnavailableExcludes(t *testing.T, event canonical.Event, names ...string) {
	t.Helper()
	fields, _ := event.Attributes["unavailable_fields"].([]string)
	for _, field := range fields {
		for _, name := range names {
			if field == name {
				t.Fatalf("%q must not be in unavailable_fields for a tool span: %#v", name, fields)
			}
		}
	}
}

// assertAbsentFromSpanAttributes proves a raw content key with a typed home is not
// duplicated into the span_attributes echo (its only home is the typed block).
func assertAbsentFromSpanAttributes(t *testing.T, event canonical.Event, key string) {
	t.Helper()
	attrs, _ := event.ProviderExtensions["span_attributes"].(map[string]any)
	if _, present := attrs[key]; present {
		t.Fatalf("raw content key %q must not appear in span_attributes: %#v", key, attrs)
	}
}

// toolSpanPayload wraps one claude-code span of the given span type carrying
// attrsJSON, for the present-only tool-span tests below.
func toolSpanPayload(spanType, attrsJSON string) string {
	return `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}},{"key":"service.version","value":{"stringValue":"2.1.268"}}]},"scopeSpans":[{"scope":{"name":"com.anthropic.claude_code.tracing"},"spans":[{"traceId":"0123456789abcdef0123456789abcdef","spanId":"0123456789abcdef","name":"claude_code.` + spanType + `","startTimeUnixNano":"1789117549650000000","attributes":[{"key":"span.type","value":{"stringValue":"` + spanType + `"}}` + attrsJSON + `]}]}]}]}`
}

// singleSpanEvent normalises a one-span payload and returns the event.
func singleSpanEvent(t *testing.T, payload string) canonical.Event {
	t.Helper()
	events, err := NormalizeTraces([]byte(payload), time.Now().UTC())
	if err != nil {
		t.Fatalf("NormalizeTraces: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	return events[0]
}

// TestNormalizeTracesToolExecutionPresentOnly proves a tool.execution maps its
// present fields and omits (never fabricates) the raw error and identifiers a
// failure-free, gate-off execution does not carry.
func TestNormalizeTracesToolExecutionPresentOnly(t *testing.T) {
	event := singleSpanEvent(t, toolSpanPayload("tool.execution", `,{"key":"success","value":{"boolValue":false}},{"key":"error_class","value":{"stringValue":"Error_ENOENT"}}`))
	block := toolBlock(t, event, "tool_execution")
	if block["success"] != false || block["error_class"] != "Error_ENOENT" {
		t.Fatalf("tool_execution block = %#v", block)
	}
	for _, key := range []string{"error", "tool_use_id", "gen_ai_tool_call_id", "duration_ms"} {
		if _, present := block[key]; present {
			t.Fatalf("absent %s must be omitted, not fabricated: %#v", key, block)
		}
	}
}

// TestNormalizeTracesToolBlockedOnUserReject proves a rejected permission wait
// maps decision:reject (the denial is the point — it never shows on tool_result).
func TestNormalizeTracesToolBlockedOnUserReject(t *testing.T) {
	event := singleSpanEvent(t, toolSpanPayload("tool.blocked_on_user", `,{"key":"decision","value":{"stringValue":"reject"}},{"key":"source","value":{"stringValue":"hook"}},{"key":"duration_ms","value":{"intValue":15000}}`))
	assertBlockFields(t, "tool_blocked_on_user", toolBlock(t, event, "tool_blocked_on_user"), map[string]any{
		"decision":    "reject",
		"source":      "hook",
		"duration_ms": int64(15000),
	})
}

// TestNormalizeTracesToolSpanUnavailableFields proves a tool span drops the
// tool/file/command surfaces from unavailable_fields (it now carries them),
// while a non-tool span keeps declaring them unavailable.
func TestNormalizeTracesToolSpanUnavailableFields(t *testing.T) {
	tool := singleSpanEvent(t, toolSpanPayload("tool", `,{"key":"tool_name","value":{"stringValue":"Read"}}`))
	assertUnavailableExcludes(t, tool, "tool_io", "file_operations", "command_execution")

	llm := singleSpanEvent(t, spanPayload(`,{"key":"stop_reason","value":{"stringValue":"end_turn"}}`))
	fields, _ := llm.Attributes["unavailable_fields"].([]string)
	found := false
	for _, field := range fields {
		if field == "command_execution" {
			found = true
		}
	}
	if !found {
		t.Fatalf("non-tool span must still declare command_execution unavailable: %#v", fields)
	}
}

// TestNormalizeTracesToolSpanRetainsIdentityAndUnknownAttribute proves the #107
// X20 / #253 stance on a tool span: a known identity attribute (user.email) rides
// raw into provider_extensions.environment, an unforeseen attribute (api_key) is
// echoed raw into span_attributes (nothing dropped at the local-only ingest
// boundary — owner directive / epic #87), and the raw full_command is captured in
// the typed block (its canonical home) without a second copy in span_attributes.
func TestNormalizeTracesToolSpanRetainsIdentityAndUnknownAttribute(t *testing.T) {
	event := singleSpanEvent(t, toolSpanPayload("tool", `,{"key":"tool_name","value":{"stringValue":"Bash"}},{"key":"full_command","value":{"stringValue":"cat config/app.yaml"}},{"key":"user.email","value":{"stringValue":"synthetic@example.test"}},{"key":"api_key","value":{"stringValue":"tiq-canary-tool-key"}}`))
	assertIdentityHomedUnforeseenRaw(t, event, map[string]any{"api_key": "tiq-canary-tool-key"})
	if block := toolBlock(t, event, "tool"); block["full_command"] != "cat config/app.yaml" {
		t.Fatalf("raw full_command must be captured in the typed block: %#v", block)
	}
	assertAbsentFromSpanAttributes(t, event, "full_command")
}

// assertIdentityHomedUnforeseenRaw proves the stance shared by the span identity
// tests: a known identity attribute (user.email) rides raw into its typed home
// provider_extensions.environment and is not duplicated into span_attributes,
// while each wantRaw attribute — with no typed home — is echoed verbatim into
// span_attributes rather than dropped (owner directive / epic #87, #253).
func assertIdentityHomedUnforeseenRaw(t *testing.T, event canonical.Event, wantRaw map[string]any) {
	t.Helper()
	environment, ok := event.ProviderExtensions["environment"].(map[string]any)
	if !ok || environment["user_email"] != "synthetic@example.test" {
		t.Fatalf("user.email must ride raw into provider_extensions.environment: %#v", event.ProviderExtensions["environment"])
	}
	assertAbsentFromSpanAttributes(t, event, "user.email")
	attrs, _ := event.ProviderExtensions["span_attributes"].(map[string]any)
	for key, want := range wantRaw {
		if attrs[key] != want {
			t.Fatalf("span_attributes[%q] = %#v, want raw %#v: %#v", key, attrs[key], want, attrs)
		}
	}
}

// TestNormalizeTracesSubAgentWorkflowPresentOnly proves sub-agent workflow
// correlation (agent_id/parent_agent_id/workflow.*) is mapped when present and
// omitted (not fabricated) on a main-session request that carries none.
func TestNormalizeTracesSubAgentWorkflowPresentOnly(t *testing.T) {
	sub := llmRequestBlock(t, spanPayload(`,{"key":"agent_id","value":{"stringValue":"agent_child"}},{"key":"parent_agent_id","value":{"stringValue":"agent_root"}},{"key":"workflow.run_id","value":{"stringValue":"wf_synthetic0001"}},{"key":"workflow.name","value":{"stringValue":"custom"}}`))
	for key, want := range map[string]any{
		"agent_id":        "agent_child",
		"parent_agent_id": "agent_root",
		"workflow_run_id": "wf_synthetic0001",
		"workflow_name":   "custom",
	} {
		if sub[key] != want {
			t.Fatalf("llm_request.%s = %#v, want %#v", key, sub[key], want)
		}
	}
	main := llmRequestBlock(t, spanPayload(`,{"key":"stop_reason","value":{"stringValue":"end_turn"}}`))
	for _, key := range []string{"agent_id", "parent_agent_id", "workflow_run_id", "workflow_name"} {
		if _, present := main[key]; present {
			t.Fatalf("main-session request must omit %s, not fabricate it: %#v", key, main)
		}
	}
}

func TestNormalizeTracesHookSpansGolden(t *testing.T) {
	payload := tracesFixturePayload(t, "claude-code-2.1.268-hook-spans-otlp.json")
	receivedAt := time.Date(2026, 9, 20, 9, 14, 23, 0, time.UTC)

	first, err := NormalizeTraces(payload, receivedAt)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := NormalizeTraces(payload, receivedAt)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("normalisation must be deterministic")
	}
	assertClaudeHookSpanTree(t, first)
	assertMatchesGolden(t, "claude-code-2.1.268-hook-spans.events.json", first)
}

// assertClaudeHookSpanTree proves the #103 contract: each hook span carries its
// typed block with every documented count/duration, the gated hook_definitions
// is present verbatim but lives only in the typed block (never duplicated into
// span_attributes), and a hook span is observed to not be a task boundary.
func assertClaudeHookSpanTree(t *testing.T, events []canonical.Event) {
	t.Helper()
	if len(events) != 3 {
		t.Fatalf("event count = %d, want 3 (interaction + 2 hook)", len(events))
	}
	bySpanID := make(map[string]canonical.Event, len(events))
	for _, event := range events {
		bySpanID[spanField(t, event, "span_id")] = event
	}

	// The successful PreToolUse:Write hook: every count present, gated
	// hook_definitions captured raw.
	writeHook := bySpanID["0000000000000c02"]
	assertBlockFields(t, "hook", toolBlock(t, writeHook, "hook"), map[string]any{
		"hook_event":             "PreToolUse",
		"hook_name":              "PreToolUse:Write",
		"hook_definitions":       "[{\"type\":\"command\",\"command\":\"./scripts/format-guard.sh\"},{\"type\":\"command\",\"command\":\"./scripts/audit-log.sh\"}]",
		"num_hooks":              int64(2),
		"num_success":            int64(2),
		"num_blocking":           int64(0),
		"num_non_blocking_error": int64(0),
		"num_cancelled":          int64(0),
		"duration_ms":            int64(45),
	})
	if got := boundaryConfidence(t, writeHook); got != "observed" {
		t.Fatalf("hook span task_boundary confidence = %q, want observed", got)
	}
	// The gated hook_definitions is content: its only home is the typed block.
	assertAbsentFromSpanAttributes(t, writeHook, "hook_definitions")

	// The blocking PreToolUse:Bash hook records the denial via num_blocking.
	bashHook := bySpanID["0000000000000c03"]
	assertBlockFields(t, "hook", toolBlock(t, bashHook, "hook"), map[string]any{
		"hook_event":   "PreToolUse",
		"hook_name":    "PreToolUse:Bash",
		"num_success":  int64(0),
		"num_blocking": int64(1),
		"duration_ms":  int64(12),
	})
	assertAbsentFromSpanAttributes(t, bashHook, "hook_definitions")
}

// TestNormalizeTracesHookSpanPresentOnly proves a hook span maps its present
// counts and omits (never fabricates as zero) the fields a minimal, gate-off
// hook span does not carry.
func TestNormalizeTracesHookSpanPresentOnly(t *testing.T) {
	event := singleSpanEvent(t, toolSpanPayload("hook", `,{"key":"hook_event","value":{"stringValue":"PreToolUse"}},{"key":"num_blocking","value":{"intValue":1}}`))
	block := toolBlock(t, event, "hook")
	if block["hook_event"] != "PreToolUse" || block["num_blocking"] != int64(1) {
		t.Fatalf("hook block = %#v", block)
	}
	for _, key := range []string{"hook_name", "hook_definitions", "num_hooks", "num_success", "num_non_blocking_error", "num_cancelled", "duration_ms"} {
		if _, present := block[key]; present {
			t.Fatalf("absent %s must be omitted, not fabricated: %#v", key, block)
		}
	}
}

// TestNormalizeTracesHookSpanUnavailableFields proves a hook span keeps declaring
// the tool/file/command surfaces unavailable — a hook span genuinely carries none
// of them, so (unlike a tool span) it must not drop them from unavailable_fields.
func TestNormalizeTracesHookSpanUnavailableFields(t *testing.T) {
	event := singleSpanEvent(t, toolSpanPayload("hook", `,{"key":"hook_event","value":{"stringValue":"PreToolUse"}}`))
	fields, _ := event.Attributes["unavailable_fields"].([]string)
	for _, want := range []string{"tool_io", "file_operations", "command_execution"} {
		found := false
		for _, field := range fields {
			if field == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("hook span must declare %q unavailable: %#v", want, fields)
		}
	}
}

// TestNormalizeTracesHookSpanRetainsIdentityAndUnknownAttribute proves the #107
// X20 / #253 stance on a hook span: a known identity attribute (user.email) rides
// raw into provider_extensions.environment, an unforeseen attribute (api_key) is
// echoed raw into span_attributes, and the gated hook_definitions stays raw in the
// typed block (its canonical home) without a second copy in span_attributes.
func TestNormalizeTracesHookSpanRetainsIdentityAndUnknownAttribute(t *testing.T) {
	event := singleSpanEvent(t, toolSpanPayload("hook", `,{"key":"hook_event","value":{"stringValue":"PreToolUse"}},{"key":"hook_definitions","value":{"stringValue":"[{\"type\":\"command\",\"command\":\"./scripts/guard.sh\"}]"}},{"key":"user.email","value":{"stringValue":"synthetic@example.test"}},{"key":"api_key","value":{"stringValue":"tiq-canary-hook-key"}}`))
	assertIdentityHomedUnforeseenRaw(t, event, map[string]any{"api_key": "tiq-canary-hook-key"})
	if block := toolBlock(t, event, "hook"); block["hook_definitions"] != "[{\"type\":\"command\",\"command\":\"./scripts/guard.sh\"}]" {
		t.Fatalf("raw hook_definitions must be captured in the typed block: %#v", block)
	}
	assertAbsentFromSpanAttributes(t, event, "hook_definitions")
}

// TestNormalizeTracesHookSpanPreservesRawDefinitions proves the gated
// hook_definitions is retained byte-for-byte (epic #87 raw capture): surrounding
// whitespace observed on the wire survives, unlike the trimmed form the shared
// putSpanString helper would store.
func TestNormalizeTracesHookSpanPreservesRawDefinitions(t *testing.T) {
	const padded = "  [{\"type\":\"command\",\"command\":\"./scripts/guard.sh\"}]  \n"
	event := singleSpanEvent(t, toolSpanPayload("hook", `,{"key":"hook_event","value":{"stringValue":"PreToolUse"}},{"key":"hook_definitions","value":{"stringValue":"  [{\"type\":\"command\",\"command\":\"./scripts/guard.sh\"}]  \n"}}`))
	if block := toolBlock(t, event, "hook"); block["hook_definitions"] != padded {
		t.Fatalf("hook_definitions must be retained verbatim (untrimmed): %q", block["hook_definitions"])
	}
}

// TestNormalizeTracesRetainsToolContentSurfaces proves the #253 contract over the
// live 2.1.287 capture (OTEL_LOG_TOOL_CONTENT + detailed beta tracing): the
// claude_code.tool span's tool.output span event is retained raw with its
// output, new_context is homed verbatim in the tool, interaction, and llm_request
// typed blocks (never duplicated into span_attributes), the previously dropped
// content attributes (tool_input, system_prompt_preview, response.model_output)
// ride raw in span_attributes, and the llm_request span link survives.
func TestNormalizeTracesRetainsToolContentSurfaces(t *testing.T) {
	events := normalizeTraceFixture(t, tracesFixturePayload(t, liveToolContentSpans))
	bySpanID := make(map[string]canonical.Event, len(events))
	for _, event := range events {
		bySpanID[spanField(t, event, "span_id")] = event
	}
	tool := bySpanID["0000000000002536"]
	wantEvents := []map[string]any{{
		"name":                     "tool.output",
		"time_unix_nano":           "1790886003967308954",
		"dropped_attributes_count": int64(0),
		"attributes": map[string]any{
			"bash_command": "printf 'https://github.com/%s/pull/%s\\n' acme-synthetic/telemetryiq 253",
			"output":       liveSpanPRLinkURL,
		},
	}}
	if got := tool.ProviderExtensions["span_events"]; !reflect.DeepEqual(got, wantEvents) {
		t.Fatalf("tool span_events = %#v, want %#v", got, wantEvents)
	}
	for spanID, block := range map[string]string{"0000000000002536": "tool", "0000000000002531": "interaction", "0000000000002537": "llm_request"} {
		event := bySpanID[spanID]
		if context, _ := toolBlock(t, event, block)["new_context"].(string); !strings.Contains(context, "pull/") {
			t.Fatalf("%s new_context not retained raw: %q", block, context)
		}
		assertAbsentFromSpanAttributes(t, event, "new_context")
	}
	attrs, _ := tool.ProviderExtensions["span_attributes"].(map[string]any)
	if input, _ := attrs["tool_input"].(string); !strings.HasPrefix(input, "[TOOL INPUT: Bash]") {
		t.Fatalf("tool_input not retained raw in span_attributes: %#v", attrs)
	}
	final, _ := bySpanID["0000000000002537"].ProviderExtensions["span_attributes"].(map[string]any)
	if final["response.model_output"] == nil || final["system_prompt_preview"] == nil {
		t.Fatalf("llm_request content attributes dropped: %#v", final)
	}
	links, _ := bySpanID["0000000000002532"].ProviderExtensions["span_links"].([]map[string]any)
	if len(links) != 1 || !reflect.DeepEqual(links[0]["attributes"], map[string]any{"link.type": "parent_of"}) {
		t.Fatalf("llm_request span link not retained: %#v", links)
	}
}

// TestNormalizeTracesEchoDecodesEveryAnyValueShape proves the raw span_attributes
// echo is lossless (#253): bytesValue, kvlistValue, non-string and whitespace-
// bearing array members, and an empty AnyValue all survive verbatim rather than
// being dropped by a scalar-only decoder.
func TestNormalizeTracesEchoDecodesEveryAnyValueShape(t *testing.T) {
	event := singleSpanEvent(t, toolSpanPayload("hook", `,{"key":"blob","value":{"bytesValue":"dGlx"}},{"key":"nested","value":{"kvlistValue":{"values":[{"key":"k","value":{"intValue":"7"}},{"key":"inner","value":{"arrayValue":{"values":[{"boolValue":true}]}}}]}}},{"key":"mixed","value":{"arrayValue":{"values":[{"stringValue":" padded "},{"intValue":"2"},{"stringValue":""}]}}},{"key":"null_value","value":{}}`))
	want := map[string]any{
		"blob":       "dGlx",
		"nested":     map[string]any{"k": float64(7), "inner": []any{true}},
		"mixed":      []any{" padded ", float64(2), ""},
		"null_value": nil,
	}
	attrs, _ := event.ProviderExtensions["span_attributes"].(map[string]any)
	for key, value := range want {
		if got, present := attrs[key]; !present || !reflect.DeepEqual(got, value) {
			t.Fatalf("span_attributes[%q] = %#v (present %v), want %#v", key, got, present, value)
		}
	}
}

// TestNormalizeTracesContentAvailabilityFollowsObservedContent proves prompt and
// response content are declared unavailable only when a span does not carry them:
// the gate-off "<REDACTED>" user_prompt is not content, while an un-redacted
// prompt or new_context (interaction/llm_request) and response.model_output
// (llm_request) make the surface available (#253).
func TestNormalizeTracesContentAvailabilityFollowsObservedContent(t *testing.T) {
	cases := []struct {
		name, spanType, attrs string
		wantPrompt, wantResp  bool
	}{
		{"redacted prompt", "interaction", `,{"key":"user_prompt","value":{"stringValue":"<REDACTED>"}}`, false, false},
		{"observed prompt", "interaction", `,{"key":"user_prompt","value":{"stringValue":"synthetic prompt"}}`, true, false},
		{"llm_request context and output", "llm_request", `,{"key":"new_context","value":{"stringValue":"[USER]\nsynthetic"}},{"key":"response.model_output","value":{"stringValue":"done"}}`, true, true},
		{"tool result is not prompt content", "tool", `,{"key":"new_context","value":{"stringValue":"[TOOL RESULT: Bash]"}}`, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fields, _ := singleSpanEvent(t, toolSpanPayload(tc.spanType, tc.attrs)).Attributes["unavailable_fields"].([]string)
			if gotPrompt := !slices.Contains(fields, "prompt_content"); gotPrompt != tc.wantPrompt {
				t.Fatalf("prompt_content available = %v, want %v: %#v", gotPrompt, tc.wantPrompt, fields)
			}
			if gotResp := !slices.Contains(fields, "response_content"); gotResp != tc.wantResp {
				t.Fatalf("response_content available = %v, want %v: %#v", gotResp, tc.wantResp, fields)
			}
		})
	}
}

// FuzzNormalizeTraces keeps the OTLP trace/span normaliser boundary from
// panicking on arbitrary input (QUALITY_GATES: fuzz smoke when a normalisation
// boundary changes). Seeds cover a well-formed hook span, the interaction root,
// and malformed envelopes so the span dispatch and typed mappers are exercised.
func FuzzNormalizeTraces(f *testing.F) {
	f.Add([]byte(toolSpanPayload("hook", `,{"key":"hook_event","value":{"stringValue":"PreToolUse"}},{"key":"hook_definitions","value":{"stringValue":"[]"}},{"key":"num_blocking","value":{"intValue":1}}`)))
	f.Add([]byte(toolSpanPayload("tool", `,{"key":"tool_name","value":{"stringValue":"Bash"}},{"key":"full_command","value":{"stringValue":"echo hi"}}`)))
	f.Add(tracesFixturePayload(f, liveToolContentSpans))
	f.Add(tracesFixturePayload(f, "claude-code-2.1.287-subagent-spans-otlp.json"))
	f.Add([]byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{}]}]}]}`))
	f.Add([]byte("not json"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		events, err := NormalizeTraces(data, time.Unix(0, 0).UTC())
		if err == nil {
			// The sub-agent reconstruction walks span parent chains, so arbitrary
			// (including cyclic) parentage must terminate without panicking.
			_ = ReconstructSubAgentRelations(events)
		}
	})
}
