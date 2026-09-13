package claude

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestNormalizeEventsToolDecisionGolden(t *testing.T) {
	input := readFixture(t, "claude-code-2.1.270-tool-decision.json")
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
		writeGolden(t, "claude-code-2.1.270-tool-decision.events.json", first)
	}
	assertMatchesGolden(t, "claude-code-2.1.270-tool-decision.events.json", first)
}

// TestNormalizeEventsToolDecisionApprovalMapping proves the canonical approval
// signal: accept→approved, reject→denied, the reason class carries the wire
// source, tool_name/tool_source are surfaced, and the untouched wire decision is
// preserved under provider_extensions (epic #87 — capture raw, hide nothing).
func TestNormalizeEventsToolDecisionApprovalMapping(t *testing.T) {
	events, err := NormalizeEvents(readFixture(t, "claude-code-2.1.270-tool-decision.json"))
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 tool_decision events, got %d", len(events))
	}
	wants := map[string]toolDecisionWant{
		"claude-code:synthetic-decision-session:approval:toolu_synthetic_read": {"approved", "accept", "config", "Read", "builtin"},
		"claude-code:synthetic-decision-session:approval:toolu_synthetic_mcp":  {"denied", "reject", "hook", "mcp_tool", "mcp"},
		"claude-code:synthetic-decision-session:approval:toolu_synthetic_bash": {"denied", "reject", "hook", "Bash", "builtin"},
	}
	for _, event := range events {
		if event.EventType != "tool_decision" {
			t.Fatalf("event type = %q, want tool_decision", event.EventType)
		}
		approvalID, _ := event.Attributes["approval_id"].(string)
		expected, ok := wants[approvalID]
		if !ok {
			t.Fatalf("unexpected approval_id %q", approvalID)
		}
		assertToolDecisionEvent(t, event, approvalID, expected)
	}
}

// toolDecisionWant is the expected canonical shape of one tool_decision event.
type toolDecisionWant struct {
	decision, rawDecision, reasonClass, toolName, toolSource string
}

// assertToolDecisionEvent checks one normalised tool_decision event against its
// expected canonical attributes, raw-preservation, no double-echo, and
// unavailable-field accounting.
func assertToolDecisionEvent(t *testing.T, event canonical.Event, approvalID string, expected toolDecisionWant) {
	t.Helper()
	for key, got := range map[string]any{
		"approval_decision":     event.Attributes["approval_decision"],
		"approval_reason_class": event.Attributes["approval_reason_class"],
		"tool_name":             event.Attributes["tool_name"],
		"tool_source":           event.Attributes["tool_source"],
	} {
		want := map[string]string{
			"approval_decision":     expected.decision,
			"approval_reason_class": expected.reasonClass,
			"tool_name":             expected.toolName,
			"tool_source":           expected.toolSource,
		}[key]
		if got != want {
			t.Fatalf("%s for %q = %v, want %q", key, approvalID, got, want)
		}
	}
	decisionExt, _ := event.ProviderExtensions["tool_decision"].(map[string]any)
	if decisionExt == nil {
		t.Fatalf("provider_extensions.tool_decision missing for %q", approvalID)
	}
	if got := decisionExt["raw_decision"]; got != expected.rawDecision {
		t.Fatalf("raw_decision for %q = %v, want %q", approvalID, got, expected.rawDecision)
	}
	if got := decisionExt["provenance"]; got != "observed" {
		t.Fatalf("provenance for %q = %v, want observed", approvalID, got)
	}
	// Decision fields are promoted into tool_decision, not double-echoed under event.
	if echo, _ := event.ProviderExtensions["event"].(map[string]any); echo["decision"] != nil {
		t.Fatalf("decision must not double-echo under provider_extensions.event: %#v", echo)
	}
	// approvals is available on this event; tool_calls remains unavailable.
	fields := event.Attributes["unavailable_fields"].([]string)
	if containsField(fields, "approvals") {
		t.Fatalf("approvals must not be unavailable on a tool_decision event: %v", fields)
	}
	if !containsField(fields, "tool_calls") {
		t.Fatalf("tool_calls should remain unavailable on a tool_decision event: %v", fields)
	}
}

// TestNormalizeEventsMarksApprovalsUnavailableElsewhere proves approvals is
// reported as an unavailable field on events that carry no permission decision,
// so the capability is explicit per-event rather than globally assumed.
func TestNormalizeEventsMarksApprovalsUnavailableElsewhere(t *testing.T) {
	events, err := NormalizeEvents(readFixture(t, "claude-code-2.1.251-otlp-events.json"))
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	for _, event := range events {
		fields := event.Attributes["unavailable_fields"].([]string)
		if !containsField(fields, "approvals") {
			t.Fatalf("approvals should be unavailable on %q: %v", event.EventType, fields)
		}
	}
}

// TestNormalizeLogsToolDecisionUnknownIsNeverInferred proves a decision with no
// wire value is reported as unknown, never inferred from any other signal.
func TestNormalizeLogsToolDecisionUnknownIsNeverInferred(t *testing.T) {
	payload := `{"resourceLogs":[{"resource":{"attributes":[
	  {"key":"service.name","value":{"stringValue":"claude-code"}},
	  {"key":"service.version","value":{"stringValue":"2.1.270"}}]},
	 "scopeLogs":[{"logRecords":[{"attributes":[
	   {"key":"event.name","value":{"stringValue":"tool_decision"}},
	   {"key":"event.timestamp","value":{"stringValue":"2026-09-13T18:53:20.000Z"}},
	   {"key":"event.sequence","value":{"intValue":"30"}},
	   {"key":"session.id","value":{"stringValue":"synthetic-decision-session"}},
	   {"key":"source","value":{"stringValue":"user_abort"}},
	   {"key":"tool_name","value":{"stringValue":"Edit"}},
	   {"key":"tool_source","value":{"stringValue":"builtin"}},
	   {"key":"tool_use_id","value":{"stringValue":"toolu_synthetic_edit"}}]}]}]}]}`
	events, err := NormalizeLogs([]byte(payload), time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if got := events[0].Attributes["approval_decision"]; got != "unknown" {
		t.Fatalf("approval_decision = %v, want unknown", got)
	}
	// A missing decision still records the reason class, so the absence is explicit.
	if got := events[0].Attributes["approval_reason_class"]; got != "user_abort" {
		t.Fatalf("approval_reason_class = %v, want user_abort", got)
	}
}

// TestNormalizeEventsDropsGatedToolParameters proves gated content never
// surfaces on the reviewed-fixture path either: a sample event carrying
// tool_parameters (which the fixture validator does not prohibit) must not echo
// it under provider_extensions.event.
func TestNormalizeEventsDropsGatedToolParameters(t *testing.T) {
	fixture := `{"fixture_version":1,"fixture_origin":"observed-sanitised","provider":"anthropic","tool":"claude-code","tool_version":"2.1.270","captured_at":"2026-09-13T18:53:00Z","sanitisation_reviewed":true,"payload":{"source_type":"otlp_http_json_logs","sample_events":[{"event_name":"tool_decision","session_id":"synthetic-decision-session","event_timestamp":"2026-09-13T18:53:10.354Z","event_sequence":14,"decision":"reject","source":"hook","tool_name":"Bash","tool_source":"builtin","tool_use_id":"toolu_synthetic_bash","tool_parameters":"{\"canary\":\"drop-me\"}"}]}}`
	events, err := NormalizeEvents([]byte(fixture))
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	serialized, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, leaked := range []string{"tool_parameters", "drop-me"} {
		if strings.Contains(string(serialized), leaked) {
			t.Fatalf("gated content leaked through NormalizeEvents: %q", leaked)
		}
	}
	echo, _ := events[0].ProviderExtensions["event"].(map[string]any)
	if _, present := echo["tool_parameters"]; present {
		t.Fatalf("tool_parameters must not echo under provider_extensions.event: %#v", echo)
	}
}

// TestNormalizeLogsToolDecisionFallbackApprovalIDsAreUnique proves that two
// decisions both lacking a tool_use_id do not collapse onto one approval_id: the
// fallback is the per-event ID, unique per session and sequence.
func TestNormalizeLogsToolDecisionFallbackApprovalIDsAreUnique(t *testing.T) {
	record := func(sequence, ts string) string {
		return `{"attributes":[
		   {"key":"event.name","value":{"stringValue":"tool_decision"}},
		   {"key":"event.timestamp","value":{"stringValue":"` + ts + `"}},
		   {"key":"event.sequence","value":{"intValue":"` + sequence + `"}},
		   {"key":"session.id","value":{"stringValue":"synthetic-decision-session"}},
		   {"key":"decision","value":{"stringValue":"reject"}},
		   {"key":"source","value":{"stringValue":"user_abort"}},
		   {"key":"tool_name","value":{"stringValue":"Bash"}},
		   {"key":"tool_source","value":{"stringValue":"builtin"}}]}`
	}
	payload := `{"resourceLogs":[{"resource":{"attributes":[
	  {"key":"service.name","value":{"stringValue":"claude-code"}},
	  {"key":"service.version","value":{"stringValue":"2.1.270"}}]},
	 "scopeLogs":[{"logRecords":[` + record("30", "2026-09-13T18:53:20.000Z") + `,` + record("31", "2026-09-13T18:53:21.000Z") + `]}]}]}`
	events, err := NormalizeLogs([]byte(payload), time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	first := events[0].Attributes["approval_id"].(string)
	second := events[1].Attributes["approval_id"].(string)
	if first == second {
		t.Fatalf("fallback approval_ids collided: %q", first)
	}
	if first != events[0].EventID || second != events[1].EventID {
		t.Fatalf("fallback approval_id must be the per-event ID: %q/%q vs %q/%q", first, second, events[0].EventID, events[1].EventID)
	}
}

// TestNormalizeLogsRecognisesToolDecisionEvent runs the raw OTLP capture through
// the wire adapter and proves gated content is dropped: the tool_parameters
// canary (full command / MCP server+tool names on the wire) and the prompt.id
// identifier never reach canonical output.
func TestNormalizeLogsRecognisesToolDecisionEvent(t *testing.T) {
	var wrapper struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(readFixture(t, "claude-code-2.1.270-tool-decision-otlp.json"), &wrapper); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	events, err := NormalizeLogs([]byte(wrapper.Payload), time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 tool_decision events, got %d", len(events))
	}
	byDecision := map[string]int{}
	for _, event := range events {
		if event.EventType != "tool_decision" {
			t.Fatalf("event type = %q, want tool_decision", event.EventType)
		}
		byDecision[event.Attributes["approval_decision"].(string)]++
	}
	if byDecision["approved"] != 1 || byDecision["denied"] != 2 {
		t.Fatalf("decision counts = %#v, want 1 approved / 2 denied", byDecision)
	}
	serialized, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	for _, leaked := range []string{"tool_parameters", "drop-me", "synthetic-prompt-id"} {
		if strings.Contains(string(serialized), leaked) {
			t.Fatalf("gated content leaked into canonical events: %q", leaked)
		}
	}
}
