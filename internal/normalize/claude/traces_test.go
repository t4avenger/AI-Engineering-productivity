package claude

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// tracesFixturePayload extracts the OTLP payload from the fixture wrapper so it
// is replayed exactly as the /v1/traces route would hand it to the adapter.
func tracesFixturePayload(t *testing.T, name string) []byte {
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

// TestNormalizeTracesFiltersSensitiveAttributes proves the allow-list drops
// identity/secret-bearing span attributes (the adapter is the sole guard after
// #88 removed storage-side sanitising) while keeping safe behaviour metadata.
func TestNormalizeTracesFiltersSensitiveAttributes(t *testing.T) {
	payload := []byte(`{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}},{"key":"service.version","value":{"stringValue":"2.1.268"}}]},"scopeSpans":[{"scope":{"name":"com.anthropic.claude_code.tracing"},"spans":[{"traceId":"0123456789abcdef0123456789abcdef","spanId":"0123456789abcdef","name":"claude_code.llm_request","startTimeUnixNano":"1789117549650000000","attributes":[{"key":"span.type","value":{"stringValue":"llm_request"}},{"key":"stop_reason","value":{"stringValue":"end_turn"}},{"key":"session.id","value":{"stringValue":"synthetic-session"}},{"key":"user.email","value":{"stringValue":"synthetic@example.test"}},{"key":"user_prompt","value":{"stringValue":"tiq-canary-prompt"}},{"key":"api_key","value":{"stringValue":"tiq-canary-api-key"}},{"key":"authorization","value":{"stringValue":"Bearer tiq-canary-token"}}]}]}]}]}`)
	events, err := NormalizeTraces(payload, time.Now().UTC())
	if err != nil {
		t.Fatalf("NormalizeTraces: %v", err)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	for _, leaked := range []string{"tiq-canary-api-key", "Bearer tiq-canary-token", "synthetic@example.test", "tiq-canary-prompt"} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("sensitive value %q leaked in %s", leaked, encoded)
		}
	}
	spanAttributes := events[0].ProviderExtensions["span_attributes"].(map[string]any)
	if spanAttributes["stop_reason"] != "end_turn" {
		t.Fatalf("safe span attribute stop_reason not preserved: %#v", spanAttributes)
	}
	if _, present := spanAttributes["user.email"]; present {
		t.Fatalf("identity attribute user.email must be dropped: %#v", spanAttributes)
	}
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
// the bounded outcome fields (attempt, success, status_code, error_class) while
// the raw free-text error message is never surfaced (it is prompt/response-adjacent
// content, epic #87).
func TestNormalizeTracesLLMRequestErrorRetry(t *testing.T) {
	payload := spanPayload(`,{"key":"attempt","value":{"intValue":2}},{"key":"success","value":{"boolValue":false}},{"key":"status_code","value":{"intValue":529}},{"key":"error_class","value":{"stringValue":"server_overload"}},{"key":"error","value":{"stringValue":"tiq-canary-error-detail: upstream connection reset"}}`)
	block := llmRequestBlock(t, payload)
	for key, want := range map[string]any{
		"attempt":     int64(2),
		"success":     false,
		"status_code": int64(529),
		"error_class": "server_overload",
	} {
		if block[key] != want {
			t.Fatalf("llm_request.%s = %#v, want %#v", key, block[key], want)
		}
	}
	if _, present := block["error"]; present {
		t.Fatalf("raw free-text error message must never be mapped: %#v", block)
	}
	events, _ := NormalizeTraces([]byte(payload), time.Now().UTC())
	encoded, _ := json.Marshal(events)
	if strings.Contains(string(encoded), "tiq-canary-error-detail") {
		t.Fatalf("raw error message leaked in %s", encoded)
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
