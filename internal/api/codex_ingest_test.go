package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/storage"
	"github.com/wayne/telemetryiq/internal/storage/sqlite"
)

// rawCodexOTLPLogs is a raw Codex OTLP/HTTP log payload in the observed wire
// shape (service.name codex_cli_rs, model + token attributes, operator
// identifiers, and secret canaries). Values are synthetic.
const rawCodexOTLPLogs = `{"resourceLogs":[{"resource":{"attributes":[
  {"key":"service.name","value":{"stringValue":"codex_cli_rs"}},
  {"key":"service.version","value":{"stringValue":"0.145.0"}}]},
 "scopeLogs":[{"logRecords":[
   {"attributes":[
     {"key":"event.name","value":{"stringValue":"codex.sse_event"}},
     {"key":"model","value":{"stringValue":"tiq-live-codex-model"}},
     {"key":"input_token_count","value":{"stringValue":"42"}},
     {"key":"cached_token_count","value":{"stringValue":"21"}},
     {"key":"output_token_count","value":{"stringValue":"7"}},
     {"key":"reasoning_token_count","value":{"stringValue":"3"}},
     {"key":"arguments","value":{"stringValue":"--token=tiq-canary-argument-token"}},
     {"key":"output","value":{"stringValue":"tiq-canary-output"}},
     {"key":"custom_metadata","value":{"stringValue":"token=tiq-canary-provider-extension"}},
     {"key":"api_key","value":{"stringValue":"tiq-canary-api-key"}},
     {"key":"user.email","value":{"stringValue":"synthetic@example.test"}},
     {"key":"conversation.id","value":{"stringValue":"synthetic-conversation"}}],
    "body":{"stringValue":"synthetic body"}}]}]}]}`

// rawCodexOTLPTraces is a wire-shaped subset of the observed Codex CLI 0.154.0
// trace exporter surface. It deliberately contains no prompt body: the capture
// ran with log_user_prompt=false, so the prompt canary never crossed the wire.
const rawCodexOTLPTraces = `{"resourceSpans":[{"resource":{"attributes":[
  {"key":"service.name","value":{"stringValue":"codex_exec"}},
  {"key":"service.version","value":{"stringValue":"0.154.0"}},
  {"key":"env","value":{"stringValue":"telemetryiq-synthetic"}}]},
 "scopeSpans":[{"scope":{"name":"codex_exec"},"spans":[
   {"traceId":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","spanId":"1111111111111111","parentSpanId":"","name":"turn/start","startTimeUnixNano":"1789671946326852067","endTimeUnixNano":"1789671946357351775","attributes":[],"status":{"code":0}},
   {"traceId":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","spanId":"2222222222222222","parentSpanId":"1111111111111111","name":"session_task.turn","startTimeUnixNano":"1789671946355925969","endTimeUnixNano":"1789671953383319471","attributes":[
     {"key":"codex.turn.token_usage.input_tokens","value":{"intValue":"1200"}},
     {"key":"codex.turn.token_usage.cached_input_tokens","value":{"intValue":"800"}},
     {"key":"codex.turn.token_usage.output_tokens","value":{"intValue":"12"}},
     {"key":"codex.turn.token_usage.reasoning_output_tokens","value":{"intValue":"3"}},
     {"key":"codex.unknown.observed","value":{"stringValue":"retained"}}],"status":{"code":0}}
 ]}]}]}`

func TestCodexTracesIngestEndToEndJSONAndProtobuf(t *testing.T) {
	server, repository := newPersistentTestServer(t)
	postCodexTraceTransports(t, server)
	session := assertCodexTraceObservation(t, server)
	assertCodexTraceTimeline(t, server, session.SessionID)
	assertCodexTraceStorage(t, repository, session)
}

func postCodexTraceTransports(t *testing.T, server *httptest.Server) {
	t.Helper()
	for _, test := range []struct {
		name, contentType string
		body              []byte
	}{
		{name: "json", contentType: otlpContentTypeJSON, body: []byte(rawCodexOTLPTraces)},
		{name: "protobuf", contentType: otlpContentTypeProtobuf, body: mustJSONOTLPToProtobuf(t, rawCodexOTLPTraces, "resourceSpans")},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := postOTLPToPath(t, server.URL, "/v1/traces", test.body, test.contentType)
			if response.StatusCode != http.StatusAccepted {
				t.Fatalf("trace ingest status = %d", response.StatusCode)
			}
			closeBody(t, response)
		})
	}
}

func assertCodexTraceObservation(t *testing.T, server *httptest.Server) publicSession {
	t.Helper()
	observations := fetchSessionList(t, server.URL+"/api/v1/sessions?scope=observation&limit=10")
	if len(observations.Data) != 1 {
		t.Fatalf("trace observations = %d, want 1: %#v", len(observations.Data), observations.Data)
	}
	session := observations.Data[0]
	if session.SessionID != "codex:trace:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" || session.IdentityScope != "observation" || session.IdentitySource != "trace.id" {
		t.Fatalf("trace observation identity = %#v", session)
	}
	if session.Attributes["service_name"] != "codex_exec" || session.Attributes["entrypoint"] != "codex exec" || session.Attributes["service_version"] != "0.154.0" {
		t.Fatalf("trace session environment = %#v", session.Attributes)
	}
	resource, ok := session.ProviderExtensions["resource_attributes"].(map[string]any)
	if !ok || resource["service.name"] != "codex_exec" || resource["service.version"] != "0.154.0" {
		t.Fatalf("trace resource metadata = %#v", session.ProviderExtensions)
	}
	if _, hasCorrelation := session.ProviderExtensions["correlation"]; hasCorrelation {
		t.Fatalf("trace-only observation must not fabricate conversation correlation: %#v", session.ProviderExtensions)
	}
	return session
}

func assertCodexTraceTimeline(t *testing.T, server *httptest.Server, sessionID string) {
	t.Helper()
	timeline := timelinePage(t, server.URL+"/api/v1/sessions/"+sessionID+"/events?limit=10")
	if len(timeline.Data) != 2 {
		t.Fatalf("trace timeline events = %d, want 2: %#v", len(timeline.Data), timeline.Data)
	}
	if timeline.Data[1].InputTokenCount == nil || *timeline.Data[1].InputTokenCount != "1200" || timeline.Data[1].CachedInputTokenCount == nil || *timeline.Data[1].CachedInputTokenCount != "800" {
		t.Fatalf("trace token evidence = %#v", timeline.Data[1])
	}
}

func assertCodexTraceStorage(t *testing.T, repository storage.Repository, session publicSession) {
	t.Helper()
	stored, err := repository.ListEvents(t.Context(), storage.EventFilter{SessionID: session.SessionID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 {
		t.Fatalf("stored trace events = %d, want 2", len(stored))
	}
	encoded := marshalJSON(t, struct {
		Session publicSession
		Events  []canonical.Event
	}{session, stored})
	assertNoRawIdentifiers(t, []string{"TRACE_CAPTURE_COMPLETE", "INTERACTIVE_TRACE_CAPTURE_COMPLETE"}, encoded)
}

// TestCodexLogsIngestEndToEnd is the Codex counterpart of the Claude live gate:
// POST a raw OTLP log payload to /v1/logs, then prove the HTTP read API serves
// the resulting session (tool, model) without leaking identity or secrets.
func TestCodexLogsIngestEndToEnd(t *testing.T) {
	server, repository := newPersistentTestServer(t)
	postCodexSessionAndMetrics(t, server)
	sessions := assertCodexPrimarySession(t, server)
	assertCodexSessionTimeline(t, server)
	assertCodexTokenObservations(t, server)

	canaries := []string{
		"tiq-canary-provider-extension",
		"tiq-canary-api-key",
		"synthetic@example.test",
		"synthetic body",
	}
	assertNoRawIdentifiers(t, canaries, marshalJSON(t, sessions))

	stored, err := repository.ListEvents(t.Context(), storage.EventFilter{SessionID: "codex:synthetic-conversation", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	assertNoRawIdentifiers(t, canaries, marshalJSON(t, stored))
	assertRawToolEvidence(t, marshalJSON(t, stored), "tiq-canary-argument-token", "tiq-canary-output")
}

func TestCodexToolEvidenceIngestPromotesObservedPRLink(t *testing.T) {
	server, repository := newPersistentTestServer(t)
	body := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}},{"key":"service.version","value":{"stringValue":"0.155.1"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.tool_result"}},{"key":"conversation.id","value":{"stringValue":"pr-link-live-session"}},{"key":"arguments","value":{"stringValue":"gh pr view https://gitlab.example.test/group/project/-/merge_requests/184"}}]}]}]}]}`)
	response := postOTLPToPath(t, server.URL, "/v1/logs", body, "application/json")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("ingest status = %d", response.StatusCode)
	}
	closeBody(t, response)
	sessions := fetchSessionList(t, server.URL+"/api/v1/sessions?limit=10")
	if len(sessions.Data) != 1 || sessions.Data[0].Attributes["pr_link"] != "https://gitlab.example.test/group/project/-/merge_requests/184" || sessions.Data[0].Availability["pr_link"] != "observed" {
		t.Fatalf("PR-link session = %#v", sessions.Data)
	}
	stored, err := repository.ListEvents(t.Context(), storage.EventFilter{SessionID: "codex:pr-link-live-session", Limit: 10})
	if err != nil || len(stored) != 1 {
		t.Fatalf("stored events = %#v, %v", stored, err)
	}
	assertRawToolEvidence(t, marshalJSON(t, stored), "https://gitlab.example.test/group/project/-/merge_requests/184")
}

func postCodexSessionAndMetrics(t *testing.T, server *httptest.Server) {
	t.Helper()
	payloads := []struct {
		path string
		body []byte
	}{
		{path: "/v1/logs", body: []byte(rawCodexOTLPLogs)},
		{path: "/v1/metrics", body: metricsFixturePayloadBytes(t, "codex-0.153.4-turn-token-usage-metrics.json")},
	}
	for _, payload := range payloads {
		response := postOTLPToPath(t, server.URL, payload.path, payload.body, "application/json")
		if response.StatusCode != http.StatusAccepted {
			t.Fatalf("%s ingest status = %d", payload.path, response.StatusCode)
		}
		closeBody(t, response)
	}
}

func assertCodexPrimarySession(t *testing.T, server *httptest.Server) sessionListResponse {
	t.Helper()
	sessions := fetchSessionList(t, server.URL+"/api/v1/sessions?limit=10")
	if len(sessions.Data) != 1 {
		t.Fatalf("expected 1 Codex session from read API, got %d: %#v", len(sessions.Data), sessions.Data)
	}
	session := sessions.Data[0]
	if session.Provider != "openai" || session.Tool != "codex" {
		t.Fatalf("session provider/tool = %q/%q", session.Provider, session.Tool)
	}
	if session.SessionID != "codex:synthetic-conversation" {
		t.Fatalf("session id = %q, want native provider conversation ID", session.SessionID)
	}
	if session.IdentityScope != "provider" || session.IdentitySource != "conversation.id" {
		t.Fatalf("session identity = %#v", session)
	}
	model, _ := session.Attributes["model"].(string)
	if model != "tiq-live-codex-model" {
		t.Fatalf("session model = %#v, want tiq-live-codex-model", session.Attributes["model"])
	}
	assertAvailabilityObserved(t, session.Availability, "model", "entrypoint", "tool_version")
	assertCodexSessionEnvironment(t, session, "codex_cli_rs", "interactive", "0.145.0", "synthetic-conversation")
	return sessions
}

func assertCodexSessionTimeline(t *testing.T, server *httptest.Server) {
	t.Helper()
	timeline := timelinePage(t, server.URL+"/api/v1/sessions/codex:synthetic-conversation/events?limit=10")
	if len(timeline.Data) != 1 {
		t.Fatalf("timeline events = %d, want 1: %#v", len(timeline.Data), timeline.Data)
	}
	if timeline.Data[0].CachedInputTokenCount == nil || *timeline.Data[0].CachedInputTokenCount != "21" {
		t.Fatalf("cached input token count = %#v", timeline.Data[0].CachedInputTokenCount)
	}
	if timeline.Data[0].ReasoningTokenCount == nil || *timeline.Data[0].ReasoningTokenCount != "3" {
		t.Fatalf("reasoning token count = %#v", timeline.Data[0].ReasoningTokenCount)
	}
}

func assertCodexTokenObservations(t *testing.T, server *httptest.Server) {
	t.Helper()
	observations := fetchSessionList(t, server.URL+"/api/v1/sessions?limit=10&scope=observation")
	if len(observations.Data) != 6 {
		t.Fatalf("expected 6 retained token observations, got %d: %#v", len(observations.Data), observations.Data)
	}
	for _, observation := range observations.Data {
		if observation.IdentityScope != "observation" || observation.IdentitySource != "content-derived" {
			t.Fatalf("token observation identity = %#v", observation)
		}
	}
}

const rawCodexLifecycleOTLPLogs = `{"resourceLogs":[{"resource":{"attributes":[
  {"key":"service.name","value":{"stringValue":"codex_exec"}},
  {"key":"service.version","value":{"stringValue":"0.153.4"}},
  {"key":"host.name","value":{"stringValue":"lifecycle-host.example.test"}},
  {"key":"user.account_id","value":{"stringValue":"lifecycle-account-123"}},
  {"key":"authorization","value":{"stringValue":"Bearer tiq-canary-lifecycle-resource-token"}}]},
 "scopeLogs":[{"logRecords":[
   {"attributes":[
     {"key":"event.name","value":{"stringValue":"codex.conversation_starts"}},
     {"key":"conversation.id","value":{"stringValue":"synthetic-lifecycle-session"}},
     {"key":"model","value":{"stringValue":"gpt-6-astra"}},
     {"key":"approval_policy","value":{"stringValue":"on-request"}},
     {"key":"sandbox_policy","value":{"stringValue":"workspace-write"}},
     {"key":"auth_mode","value":{"stringValue":"api-key"}},
     {"key":"terminal.type","value":{"stringValue":"pty"}},
     {"key":"slug","value":{"stringValue":"tiq-canary-lifecycle-slug"}},
     {"key":"user.email","value":{"stringValue":"lifecycle-user@example.test"}}],
    "body":{"stringValue":"tiq-canary-lifecycle-body"},"timeUnixNano":"1788717763000000000"},
   {"attributes":[
     {"key":"event.name","value":{"stringValue":"codex.startup_phase"}},
     {"key":"conversation.id","value":{"stringValue":"synthetic-lifecycle-session"}},
     {"key":"startup.phase","value":{"stringValue":"init"}},
     {"key":"startup.status","value":{"stringValue":"ok"}},
     {"key":"duration_ms","value":{"stringValue":"17"}}],"timeUnixNano":"1788717763000000001"},
   {"attributes":[
     {"key":"event.name","value":{"stringValue":"codex.websocket_connect"}},
     {"key":"conversation.id","value":{"stringValue":"synthetic-lifecycle-session"}},
     {"key":"success","value":{"boolValue":true}},
     {"key":"duration_ms","value":{"stringValue":"23"}}],"timeUnixNano":"1788717763000000002"}
  ]}]}]}`

func TestCodexLifecycleIngestExposesActiveSession(t *testing.T) {
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	server := httptest.NewServer(NewPersistentHandler(slog.Default(), repository))
	t.Cleanup(server.Close)

	response := postOTLPToPath(t, server.URL, "/v1/logs", []byte(rawCodexLifecycleOTLPLogs), "application/json")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("ingest status = %d", response.StatusCode)
	}
	closeBody(t, response)

	sessions := fetchSessionList(t, server.URL+"/api/v1/sessions?limit=10")
	if len(sessions.Data) != 1 {
		t.Fatalf("expected 1 Codex lifecycle session, got %d: %#v", len(sessions.Data), sessions.Data)
	}
	session := sessions.Data[0]
	if session.SessionID != "codex:synthetic-lifecycle-session" || session.State != "active" || session.CompletedAt != nil {
		t.Fatalf("session lifecycle = %#v", session)
	}
	assertAvailabilityObserved(t, session.Availability, "outcome", "entrypoint", "tool_version")
	if session.Availability["completed_at"] != "unavailable" {
		t.Fatalf("completed availability = %#v", session.Availability)
	}
	assertCodexSessionEnvironment(t, session, "codex_exec", "codex exec", "0.153.4", "synthetic-lifecycle-session")

	events := timelinePage(t, server.URL+"/api/v1/sessions/codex:synthetic-lifecycle-session/events?limit=10")
	if len(events.Data) != 3 {
		t.Fatalf("timeline events = %d, want 3: %#v", len(events.Data), events.Data)
	}
	assertCodexLifecycleTimeline(t, events.Data)

	canaries := codexLifecycleCanaries()
	assertNoRawIdentifiers(t, canaries, marshalJSON(t, sessions))
	assertNoRawIdentifiers(t, canaries, marshalJSON(t, events))

	stored, err := repository.ListEvents(t.Context(), storage.EventFilter{SessionID: "codex:synthetic-lifecycle-session", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	assertNoRawIdentifiers(t, canaries, marshalJSON(t, stored))
}

func assertAvailabilityObserved(t *testing.T, availability map[string]string, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if availability[key] != "observed" {
			t.Fatalf("%s availability = %#v", key, availability)
		}
	}
}

func assertCodexSessionEnvironment(t *testing.T, session publicSession, serviceName, entrypoint, version, providerSessionID string) {
	t.Helper()
	if session.Attributes["service_name"] != serviceName || session.Attributes["entrypoint"] != entrypoint || session.Attributes["service_version"] != version {
		t.Fatalf("session environment attributes = %#v", session.Attributes)
	}
	resource, ok := session.ProviderExtensions["resource_attributes"].(map[string]any)
	if !ok || resource["service.name"] != serviceName || resource["service.version"] != version {
		t.Fatalf("session resource metadata = %#v", session.ProviderExtensions)
	}
	correlation, ok := session.ProviderExtensions["correlation"].(map[string]any)
	if !ok || correlation["session_id_source"] != "conversation.id" || correlation["provider_session_id"] != providerSessionID {
		t.Fatalf("session correlation metadata = %#v", session.ProviderExtensions)
	}
}

func assertCodexLifecycleTimeline(t *testing.T, events []timelineEvent) {
	t.Helper()
	start := findTimelineEvent(events, "session.active")
	if start == nil || start.LifecycleKind == nil || *start.LifecycleKind != "session_start" || start.Entrypoint == nil || *start.Entrypoint != "codex exec" {
		t.Fatalf("start timeline event = %#v", start)
	}
	if slices.Contains(start.UnavailableFields, "session_lifecycle") {
		t.Fatalf("session_lifecycle must be available: %#v", start.UnavailableFields)
	}
	assertCodexStartupTimelineEvent(t, findTimelineEvent(events, "codex.startup_phase"))
	assertCodexWebsocketTimelineEvent(t, findTimelineEvent(events, "codex.websocket_connect"))
}

func assertCodexStartupTimelineEvent(t *testing.T, event *timelineEvent) {
	t.Helper()
	if event == nil || event.LifecyclePhase == nil || *event.LifecyclePhase != "init" || event.LifecycleStatus == nil || *event.LifecycleStatus != "ok" || event.DurationMs == nil || *event.DurationMs != "17" {
		t.Fatalf("startup timeline event = %#v", event)
	}
}

func assertCodexWebsocketTimelineEvent(t *testing.T, event *timelineEvent) {
	t.Helper()
	if event == nil || event.LifecycleStatus == nil || *event.LifecycleStatus != "success" || event.DurationMs == nil || *event.DurationMs != "23" {
		t.Fatalf("websocket timeline event = %#v", event)
	}
}

func findTimelineEvent(events []timelineEvent, eventType string) *timelineEvent {
	for index := range events {
		if events[index].EventType == eventType {
			return &events[index]
		}
	}
	return nil
}

func codexLifecycleCanaries() []string {
	return []string{
		"tiq-canary-lifecycle-resource-token",
		"tiq-canary-lifecycle-slug",
		"tiq-canary-lifecycle-body",
		"lifecycle-host.example.test",
		"lifecycle-account-123",
		"lifecycle-user@example.test",
	}
}

const rawCodexToolResultOTLPLogs = `{"resourceLogs":[{"resource":{"attributes":[
  {"key":"service.name","value":{"stringValue":"codex_exec"}},
  {"key":"service.version","value":{"stringValue":"0.153.4"}},
  {"key":"host.name","value":{"stringValue":"tool-host.example.test"}},
  {"key":"user.account_id","value":{"stringValue":"tool-account-123"}},
  {"key":"authorization","value":{"stringValue":"Bearer tiq-canary-tool-resource-token"}}]},
 "scopeLogs":[{"logRecords":[
   {"attributes":[
     {"key":"event.name","value":{"stringValue":"codex.tool_result"}},
     {"key":"conversation.id","value":{"stringValue":"synthetic-tool-session"}},
     {"key":"tool_name","value":{"stringValue":"exec_command"}},
     {"key":"tool_namespace","value":{"stringValue":"functions"}},
     {"key":"call_id","value":{"stringValue":"synthetic-call-success"}},
     {"key":"duration_ms","value":{"stringValue":"92"}},
     {"key":"success","value":{"stringValue":"true"}},
     {"key":"output_truncated","value":{"boolValue":false}},
     {"key":"arguments","value":{"stringValue":"--token=tiq-canary-tool-argument"}},
     {"key":"output","value":{"stringValue":"tiq-canary-tool-output"}},
     {"key":"api_key","value":{"stringValue":"tiq-canary-tool-api-key"}},
     {"key":"user.email","value":{"stringValue":"tool-user@example.test"}}],
    "body":{"stringValue":"tiq-canary-tool-body"}}]}]}]}`

func TestCodexToolResultIngestExposesToolCallSignal(t *testing.T) {
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	server := httptest.NewServer(NewPersistentHandler(slog.Default(), repository))
	t.Cleanup(server.Close)

	response := postOTLPToPath(t, server.URL, "/v1/logs", []byte(rawCodexToolResultOTLPLogs), "application/json")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("ingest status = %d", response.StatusCode)
	}
	closeBody(t, response)

	events := timelinePage(t, server.URL+"/api/v1/sessions/codex:synthetic-tool-session/events?limit=10")
	if len(events.Data) != 1 {
		t.Fatalf("timeline events = %d, want 1: %#v", len(events.Data), events.Data)
	}
	event := events.Data[0]
	if event.EventType != "codex.tool_result" || event.OperationID == nil || *event.OperationID != "codex:synthetic-tool-session:tool:synthetic-call-success" {
		t.Fatalf("operation identity = %#v", event)
	}
	if event.Category == nil || *event.Category != "shell command" || event.Outcome == nil || *event.Outcome != "success" || event.DurationMs == nil || *event.DurationMs != "92" {
		t.Fatalf("operation fields = %#v", event)
	}
	if slices.Contains(event.UnavailableFields, "tool_calls") {
		t.Fatalf("tool_calls must be available for codex.tool_result: %#v", event.UnavailableFields)
	}

	canaries := []string{
		"tiq-canary-tool-api-key",
		"tiq-canary-tool-resource-token",
		"tool-host.example.test",
		"tool-account-123",
		"tool-user@example.test",
		"tiq-canary-tool-body",
	}
	assertNoRawIdentifiers(t, canaries, marshalJSON(t, events))

	stored, err := repository.ListEvents(t.Context(), storage.EventFilter{SessionID: "codex:synthetic-tool-session", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	assertNoRawIdentifiers(t, canaries, marshalJSON(t, stored))
	assertRawToolEvidence(t, marshalJSON(t, stored), "tiq-canary-tool-argument", "tiq-canary-tool-output")
}

func assertRawToolEvidence(t *testing.T, document []byte, values ...string) {
	t.Helper()
	for _, value := range values {
		if !bytes.Contains(document, []byte(value)) {
			t.Fatalf("raw tool evidence %q missing", value)
		}
	}
}

const rawCodexToolDecisionOTLPLogs = `{"resourceLogs":[{"resource":{"attributes":[
  {"key":"service.name","value":{"stringValue":"codex_exec"}},
  {"key":"service.version","value":{"stringValue":"0.153.4"}},
  {"key":"host.name","value":{"stringValue":"decision-host.example.test"}},
  {"key":"user.account_id","value":{"stringValue":"decision-account-123"}},
  {"key":"authorization","value":{"stringValue":"Bearer tiq-canary-decision-resource-token"}}]},
 "scopeLogs":[{"logRecords":[
   {"attributes":[
     {"key":"event.name","value":{"stringValue":"codex.tool_decision"}},
     {"key":"conversation.id","value":{"stringValue":"synthetic-decision-session"}},
     {"key":"call_id","value":{"stringValue":"synthetic-decision-call"}},
     {"key":"decision","value":{"stringValue":"allow"}},
     {"key":"source","value":{"stringValue":"policy"}},
     {"key":"tool_name","value":{"stringValue":"exec_command"}},
     {"key":"tool_namespace","value":{"stringValue":"functions"}},
     {"key":"model","value":{"stringValue":"gpt-6-astra"}},
     {"key":"slug","value":{"stringValue":"tiq-canary-decision-slug"}},
     {"key":"authorization","value":{"stringValue":"Bearer tiq-canary-decision-token"}},
     {"key":"command","value":{"stringValue":"tiq-canary-decision-command"}},
     {"key":"command_args","value":{"stringValue":"tiq-canary-decision-command-args"}},
     {"key":"command_line","value":{"stringValue":"tiq-canary-decision-command-line"}},
     {"key":"cwd","value":{"stringValue":"/tmp/tiq-canary-decision-cwd"}},
     {"key":"path","value":{"stringValue":"/tmp/tiq-canary-decision-path"}},
     {"key":"file_path","value":{"stringValue":"/tmp/tiq-canary-decision-file-path"}},
     {"key":"arguments","value":{"stringValue":"--token=tiq-canary-decision-argument"}},
     {"key":"output","value":{"stringValue":"tiq-canary-decision-output"}},
     {"key":"api_key","value":{"stringValue":"tiq-canary-decision-api-key"}},
     {"key":"user.email","value":{"stringValue":"decision-user@example.test"}}],
    "body":{"stringValue":"tiq-canary-decision-body"}}]}]}]}`

func TestCodexToolDecisionIngestExposesApprovalSignal(t *testing.T) {
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	server := httptest.NewServer(NewPersistentHandler(slog.Default(), repository))
	t.Cleanup(server.Close)

	response := postOTLPToPath(t, server.URL, "/v1/logs", []byte(rawCodexToolDecisionOTLPLogs), "application/json")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("ingest status = %d", response.StatusCode)
	}
	closeBody(t, response)

	events := timelinePage(t, server.URL+"/api/v1/sessions/codex:synthetic-decision-session/events?limit=10")
	if len(events.Data) != 1 {
		t.Fatalf("timeline events = %d, want 1: %#v", len(events.Data), events.Data)
	}
	assertCodexToolDecisionTimelineEvent(t, events.Data[0])
	assertNoRawIdentifiers(t, codexToolDecisionCanaries(), marshalJSON(t, events))

	stored, err := repository.ListEvents(t.Context(), storage.EventFilter{SessionID: "codex:synthetic-decision-session", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	assertNoRawIdentifiers(t, codexToolDecisionCanaries(), marshalJSON(t, stored))
}

func assertCodexToolDecisionTimelineEvent(t *testing.T, event timelineEvent) {
	t.Helper()
	if event.EventType != "codex.tool_decision" || event.ApprovalID == nil || *event.ApprovalID != "codex:synthetic-decision-session:approval:synthetic-decision-call" {
		t.Fatalf("approval identity = %#v", event)
	}
	if event.ApprovalDecision == nil || *event.ApprovalDecision != "approved" || event.ApprovalReasonClass == nil || *event.ApprovalReasonClass != "policy" {
		t.Fatalf("approval fields = %#v", event)
	}
	if event.ToolName == nil || *event.ToolName != "exec_command" || event.ToolNamespace == nil || *event.ToolNamespace != "functions" {
		t.Fatalf("tool identity = %#v", event)
	}
	if slices.Contains(event.UnavailableFields, "approvals") {
		t.Fatalf("approvals must be available for codex.tool_decision: %#v", event.UnavailableFields)
	}
	if !slices.Contains(event.UnavailableFields, "tool_calls") {
		t.Fatalf("tool_calls must remain unavailable for codex.tool_decision: %#v", event.UnavailableFields)
	}
}

func codexToolDecisionCanaries() []string {
	return []string{
		"tiq-canary-decision-argument",
		"tiq-canary-decision-output",
		"tiq-canary-decision-api-key",
		"decision-user@example.test",
		"tiq-canary-decision-body",
		"decision-host.example.test",
		"decision-account-123",
		"tiq-canary-decision-resource-token",
		"tiq-canary-decision-slug",
		"tiq-canary-decision-token",
		"tiq-canary-decision-command",
		"tiq-canary-decision-command-args",
		"tiq-canary-decision-command-line",
		"tiq-canary-decision-cwd",
		"tiq-canary-decision-path",
		"tiq-canary-decision-file-path",
	}
}

const rawCodexSandboxOutcomeOTLPLogs = `{"resourceLogs":[{"resource":{"attributes":[
  {"key":"service.name","value":{"stringValue":"codex_exec"}},
  {"key":"service.version","value":{"stringValue":"0.153.4"}},
  {"key":"host.name","value":{"stringValue":"sandbox-host.example.test"}},
  {"key":"user.account_id","value":{"stringValue":"sandbox-account-123"}},
  {"key":"authorization","value":{"stringValue":"Bearer tiq-canary-resource-token"}}]},
 "scopeLogs":[{"logRecords":[
   {"attributes":[
     {"key":"event.name","value":{"stringValue":"codex.sandbox_outcome"}},
     {"key":"conversation.id","value":{"stringValue":"synthetic-sandbox-session"}},
     {"key":"call_id","value":{"stringValue":"synthetic-sandbox-call-success"}},
     {"key":"tool_name","value":{"stringValue":"exec_command"}},
     {"key":"initial_duration_ms","value":{"stringValue":"123"}},
     {"key":"outcome","value":{"stringValue":"success"}},
     {"key":"model","value":{"stringValue":"gpt-6-astra"}},
     {"key":"slug","value":{"stringValue":"tiq-canary-sandbox-slug"}},
     {"key":"command","value":{"stringValue":"tiq-canary-sandbox-command"}},
     {"key":"command_args","value":{"stringValue":"tiq-canary-sandbox-command-args"}},
     {"key":"cwd","value":{"stringValue":"/tmp/tiq-canary-sandbox-cwd"}},
     {"key":"path","value":{"stringValue":"/tmp/tiq-canary-sandbox-path"}},
     {"key":"arguments","value":{"stringValue":"--token=tiq-canary-sandbox-argument"}},
     {"key":"output","value":{"stringValue":"tiq-canary-sandbox-output"}},
     {"key":"api_key","value":{"stringValue":"tiq-canary-sandbox-api-key"}},
     {"key":"user.email","value":{"stringValue":"sandbox-user@example.test"}}],
    "body":{"stringValue":"tiq-canary-sandbox-body"}}]}]}]}`

func TestCodexSandboxOutcomeIngestExposesCommandExecutionSignal(t *testing.T) {
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	server := httptest.NewServer(NewPersistentHandler(slog.Default(), repository))
	t.Cleanup(server.Close)

	response := postOTLPToPath(t, server.URL, "/v1/logs", []byte(rawCodexSandboxOutcomeOTLPLogs), "application/json")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("ingest status = %d", response.StatusCode)
	}
	closeBody(t, response)

	events := timelinePage(t, server.URL+"/api/v1/sessions/codex:synthetic-sandbox-session/events?limit=10")
	if len(events.Data) != 1 {
		t.Fatalf("timeline events = %d, want 1: %#v", len(events.Data), events.Data)
	}
	event := events.Data[0]
	assertCodexSandboxTimelineEvent(t, event)

	canaries := []string{
		"tiq-canary-sandbox-argument",
		"tiq-canary-sandbox-output",
		"tiq-canary-sandbox-api-key",
		"sandbox-user@example.test",
		"tiq-canary-sandbox-body",
		"sandbox-host.example.test",
		"sandbox-account-123",
		"tiq-canary-resource-token",
		"tiq-canary-sandbox-slug",
		"tiq-canary-sandbox-command",
		"tiq-canary-sandbox-command-args",
		"tiq-canary-sandbox-cwd",
		"tiq-canary-sandbox-path",
	}
	assertNoRawIdentifiers(t, canaries, marshalJSON(t, events))

	stored, err := repository.ListEvents(t.Context(), storage.EventFilter{SessionID: "codex:synthetic-sandbox-session", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	assertNoRawIdentifiers(t, canaries, marshalJSON(t, stored))
}

func assertCodexSandboxTimelineEvent(t *testing.T, event timelineEvent) {
	t.Helper()
	if event.EventType != "codex.sandbox_outcome" || event.OperationID == nil || *event.OperationID != "codex:synthetic-sandbox-session:sandbox:synthetic-sandbox-call-success" {
		t.Fatalf("operation identity = %#v", event)
	}
	if event.Category == nil || *event.Category != "shell command" || event.Outcome == nil || *event.Outcome != "success" || event.DurationMs == nil || *event.DurationMs != "123" {
		t.Fatalf("operation fields = %#v", event)
	}
	if slices.Contains(event.UnavailableFields, "command_execution") {
		t.Fatalf("command_execution must be available for codex.sandbox_outcome: %#v", event.UnavailableFields)
	}
	if !slices.Contains(event.UnavailableFields, "file_operations") {
		t.Fatalf("file_operations must remain unavailable until evidenced: %#v", event.UnavailableFields)
	}
}

func fetchSessionList(t *testing.T, url string) sessionListResponse {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("sessions status = %d", response.StatusCode)
	}
	var body sessionListResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}
