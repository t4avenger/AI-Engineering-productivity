package api

import (
	"net/http"
	"testing"
)

// rawClaudeSkillOTLPLogs is a sanitised Claude Code OTLP log payload carrying a
// skill_activated event observed on Claude Code 2.1.263 (synthetic identifiers).
const rawClaudeSkillOTLPLogs = `{"resourceLogs":[{"resource":{"attributes":[
  {"key":"service.name","value":{"stringValue":"claude-code"}},
  {"key":"service.version","value":{"stringValue":"2.1.263"}}]},
 "scopeLogs":[{"logRecords":[
   {"attributes":[
     {"key":"event.name","value":{"stringValue":"skill_activated"}},
     {"key":"event.timestamp","value":{"stringValue":"2026-09-06T14:50:00.962Z"}},
     {"key":"event.sequence","value":{"intValue":"9"}},
     {"key":"session.id","value":{"stringValue":"tiq-canary-session"}},
     {"key":"skill.name","value":{"stringValue":"tiq-probe"}},
     {"key":"invocation_trigger","value":{"stringValue":"user-slash"}},
     {"key":"skill.source","value":{"stringValue":"projectSettings"}},
     {"key":"user.email","value":{"stringValue":"tiq-canary@example.test"}},
     {"key":"api_key","value":{"stringValue":"tiq-canary-api-key"}}]}]}]}]}`

const rawCodexSkillMetrics = `{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}},{"key":"service.version","value":{"stringValue":"0.153.4"}}]},"scopeMetrics":[{"metrics":[{"name":"codex.skill.injected","sum":{"dataPoints":[{"attributes":[{"key":"skill","value":{"stringValue":"tiq-probe"}},{"key":"status","value":{"stringValue":"ok"}},{"key":"invoke_type","value":{"stringValue":"explicit"}}],"asInt":1,"timeUnixNano":"1788706421601372612"}]}}]}]}]}`

const rawCodexSkillTurnMetrics = `{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}},{"key":"service.version","value":{"stringValue":"0.153.4"}}]},"scopeMetrics":[{"metrics":[{"name":"codex.skill.turn.duration_seconds","histogram":{"dataPoints":[{"attributes":[{"key":"status","value":{"stringValue":"completed"}},{"key":"plugin_id","value":{"stringValue":"unattributed"}}],"count":"1","timeUnixNano":"1788709355426961808"}]}}]}]}]}`

// TestSkillUsageInsightIngestEndToEnd is the live-data DoD gate for the skill
// usage insight: it POSTs Claude skill_activated logs and Codex skill.injected
// metrics to the live receivers and reads explicit skill records back through
// the real HTTP read API.
func TestSkillUsageInsightIngestEndToEnd(t *testing.T) {
	server, _ := newPersistentTestServer(t)
	postAcceptedOTLP(t, server.URL, "/v1/logs", rawClaudeSkillOTLPLogs)
	postAcceptedOTLP(t, server.URL, "/v1/metrics", rawCodexSkillMetrics)

	usage := getInsightJSON[skillUsageResponse](t, server.URL+"/api/v1/insights/skill-usage")
	assertExplicitSkillUsage(t, usage)

	assertNoRawIdentifiers(t,
		[]string{"tiq-canary@example.test", "tiq-canary-api-key"},
		marshalJSON(t, usage))
}

func TestCodexSkillTurnMetricReportsInferredCoverage(t *testing.T) {
	server, _ := newPersistentTestServer(t)
	postAcceptedOTLP(t, server.URL, "/v1/metrics", rawCodexSkillTurnMetrics)

	usage := getInsightJSON[skillUsageResponse](t, server.URL+"/api/v1/insights/skill-usage")
	if usage.Data.Totals.ObservedSkills != 0 || usage.Data.Totals.Invocations != 0 {
		t.Fatalf("inferred skill turn fabricated named skill usage")
	}
	if usage.Data.Totals.InferredDetection != 1 {
		t.Fatalf("inferred detection count mismatch")
	}
	assertCoverageState(t, usage, "openai", "codex", "inferred")
}

func postAcceptedOTLP(t *testing.T, base, path, body string) {
	t.Helper()
	resp := postOTLPToPath(t, base, path, []byte(body), "application/json")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("%s status = %d", path, resp.StatusCode)
	}
	closeBody(t, resp)
}

func assertExplicitSkillUsage(t *testing.T, usage skillUsageResponse) {
	t.Helper()
	if usage.Data.Totals.ObservedSkills != 2 || usage.Data.Totals.Invocations < 2 {
		t.Fatalf("expected explicit skill usage, got totals %#v skills %#v", usage.Data.Totals, usage.Data.Skills)
	}
	if usage.Data.Totals.ExplicitDetection < 2 {
		t.Fatalf("expected explicit detection on Claude and Codex, got %#v", usage.Data.Totals)
	}
	assertCoverageState(t, usage, "anthropic", "claude-code", "explicit")
	assertCoverageState(t, usage, "openai", "codex", "explicit")
	for _, skill := range usage.Data.Skills {
		if skill.SkillName != "tiq-probe" {
			t.Fatalf("unexpected skill %#v", skill)
		}
	}
	if len(usage.Data.Skills) < 2 {
		t.Fatalf("skills = %#v", usage.Data.Skills)
	}
}

func assertCoverageState(t *testing.T, usage skillUsageResponse, provider, tool, want string) {
	t.Helper()
	for _, row := range usage.Data.Coverage {
		if row.Provider == provider && row.Tool == tool {
			if row.DetectionState != want {
				t.Fatalf("%s/%s skill detection = %q, want %s", provider, tool, row.DetectionState, want)
			}
			return
		}
	}
	t.Fatalf("coverage missing %s/%s: %#v", provider, tool, usage.Data.Coverage)
}
