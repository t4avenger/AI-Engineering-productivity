package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"

	"github.com/wayne/telemetryiq/internal/storage"
	"github.com/wayne/telemetryiq/internal/storage/sqlite"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestOTLPHTTPIngestProof(t *testing.T) {
	server := httptest.NewServer(NewHandler(slog.Default()))
	t.Cleanup(server.Close)

	// Traces are now persisted, resolving the /v1/traces 501 (issue #50). A
	// well-formed spans payload with no claude-code resource is accepted (so
	// exporters flush) but produces no persisted events.
	traces := postOTLP(t, server.URL, []byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"0123456789abcdef0123456789abcdef","spanId":"0123456789abcdef","name":"synthetic"}]}]}]}`))
	if traces.StatusCode != http.StatusAccepted {
		t.Fatalf("expected OTLP traces status 202, got %d", traces.StatusCode)
	}
	closeBody(t, traces)

	// Metrics without Codex skill datapoints are accepted (so exporters flush)
	// but produce no persisted events.
	metrics := postOTLPToPath(t, server.URL, "/v1/metrics", []byte(`{"resourceMetrics":[{"scopeMetrics":[{"metrics":[{"name":"synthetic"}]}]}]}`), "application/json")
	if metrics.StatusCode != http.StatusAccepted {
		t.Fatalf("expected OTLP metrics status 202, got %d", metrics.StatusCode)
	}
	closeBody(t, metrics)

	logs := postOTLPToPath(t, server.URL, "/v1/logs", []byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"synthetic"}}]}]}]}`), "application/json")
	if logs.StatusCode != http.StatusAccepted {
		t.Fatalf("expected OTLP logs status 202, got %d", logs.StatusCode)
	}
	closeBody(t, logs)

	malformed := postOTLPToPath(t, server.URL, "/v1/logs", []byte(`{"resourceLogs":`), "application/json")
	assertIngestError(t, malformed, http.StatusBadRequest, "malformed_payload")

	invalid := postOTLPToPath(t, server.URL, "/v1/logs", []byte(`{"resourceLogs":[]}`), "application/json")
	assertIngestError(t, invalid, http.StatusBadRequest, "invalid_payload")

	oversized := postOTLPToPath(t, server.URL, "/v1/logs", bytes.Repeat([]byte("x"), int(maxOTLPPayloadBytes)+1), "application/json")
	assertIngestError(t, oversized, http.StatusRequestEntityTooLarge, "payload_too_large")

	unsupportedMediaType := postOTLPToPath(t, server.URL, "/v1/logs", []byte(`{"resourceLogs":[{}]}`), "text/plain")
	assertIngestError(t, unsupportedMediaType, http.StatusUnsupportedMediaType, "unsupported_media_type")

	// JSON body with protobuf Content-Type is malformed protobuf (not a media-type reject).
	malformedProtobuf := postOTLPToPath(t, server.URL, "/v1/logs", []byte(`{"resourceLogs":[{}]}`), otlpContentTypeProtobuf)
	assertIngestError(t, malformedProtobuf, http.StatusBadRequest, "malformed_payload")

	protobufLogs := postOTLPToPath(t, server.URL, "/v1/logs", mustMarshalOTLPLogsProtobuf(t), otlpContentTypeProtobuf)
	if protobufLogs.StatusCode != http.StatusAccepted {
		t.Fatalf("expected protobuf logs status 202, got %d", protobufLogs.StatusCode)
	}
	closeBody(t, protobufLogs)

	protobufMetrics := postOTLPToPath(t, server.URL, "/v1/metrics", mustMarshalOTLPMetricsProtobuf(t), otlpContentTypeProtobuf)
	if protobufMetrics.StatusCode != http.StatusAccepted {
		t.Fatalf("expected protobuf metrics status 202, got %d", protobufMetrics.StatusCode)
	}
	closeBody(t, protobufMetrics)

	protobufTraces := postOTLPToPath(t, server.URL, "/v1/traces", mustJSONOTLPToProtobuf(t, rawCodexOTLPTraces, "resourceSpans"), otlpContentTypeProtobuf)
	if protobufTraces.StatusCode != http.StatusAccepted {
		t.Fatalf("expected protobuf traces status 202, got %d", protobufTraces.StatusCode)
	}
	closeBody(t, protobufTraces)

	extraField := postOTLPToPath(t, server.URL, "/v1/logs", []byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"x"}}]}]}],"unexpected":true}`), "application/json")
	assertIngestError(t, extraField, http.StatusBadRequest, "invalid_payload")

	gzipProtobuf := mustGzip(t, mustMarshalOTLPLogsProtobuf(t))
	gzipLogs := postOTLPToPathWithEncoding(t, server.URL, "/v1/logs", gzipProtobuf, otlpContentTypeProtobuf, "gzip")
	if gzipLogs.StatusCode != http.StatusAccepted {
		t.Fatalf("expected gzip protobuf logs status 202, got %d", gzipLogs.StatusCode)
	}
	closeBody(t, gzipLogs)

	resp, err := http.Get(server.URL + "/api/v1/ingest/counters")
	if err != nil {
		t.Fatalf("GET ingest counters: %v", err)
	}
	defer closeBody(t, resp)
	var counters ingestCountersResponse
	if err := json.NewDecoder(resp.Body).Decode(&counters); err != nil {
		t.Fatalf("decode counters: %v", err)
	}
	// 1 JSON log + 1 JSON metrics + 1 JSON traces + 1 protobuf log + 1 protobuf metrics + 1 protobuf trace + 1 gzip protobuf log
	// + 6 validation rejects (malformed JSON, invalid, oversized, text/plain, malformed protobuf, extra field)
	if counters.AcceptedPayloads != 7 || counters.RejectedPayloads != 6 {
		t.Fatalf("unexpected counters: %+v", counters)
	}
}

// TestOTLPTracesRejectMalformedRecognizedCodex proves a recognized Codex
// resource cannot receive a silent 202 when one of its spans is malformed.
func TestOTLPTracesRejectMalformedRecognizedCodex(t *testing.T) {
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	server := httptest.NewServer(NewPersistentHandler(slog.Default(), repository))
	t.Cleanup(server.Close)

	invalid := postOTLPToPath(t, server.URL, "/v1/traces", []byte(`{"ignored":true}`), "application/json")
	assertIngestError(t, invalid, http.StatusBadRequest, "invalid_payload")

	traces := postOTLPToPath(t, server.URL, "/v1/traces", []byte(`{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}}]},"scopeSpans":[{"spans":[{"traceId":"0123456789abcdef0123456789abcdef","spanId":"0123456789abcdef","name":"codex.turn"}]}]}]}`), "application/json")
	assertIngestError(t, traces, http.StatusUnprocessableEntity, "normalization_failed")

	sessions := fetchSessionList(t, server.URL+"/api/v1/sessions?limit=10")
	if len(sessions.Data) != 0 {
		t.Fatalf("malformed Codex traces must not persist sessions, got %#v", sessions.Data)
	}
}

func TestMixedTraceBatchMalformedSupportedResourceIsAtomic(t *testing.T) {
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	server := httptest.NewServer(NewPersistentHandler(slog.Default(), repository))
	t.Cleanup(server.Close)

	var payload map[string]any
	if err := json.Unmarshal([]byte(rawCodexOTLPTraces), &payload); err != nil {
		t.Fatal(err)
	}
	resources := payload["resourceSpans"].([]any)
	resources = append(resources, map[string]any{
		"resource": map[string]any{
			"attributes": []any{
				map[string]any{"key": "service.name", "value": map[string]any{"stringValue": "claude-code"}},
			},
		},
		"scopeSpans": []any{
			map[string]any{
				"spans": []any{
					map[string]any{
						"traceId": "cccccccccccccccccccccccccccccccc", "name": "claude_code.interaction", "startTimeUnixNano": "1789671946326852067",
					},
				},
			},
		},
	})
	payload["resourceSpans"] = resources
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	response := postOTLPToPath(t, server.URL, "/v1/traces", body, otlpContentTypeJSON)
	assertIngestError(t, response, http.StatusUnprocessableEntity, "normalization_failed")

	sessions := fetchSessionList(t, server.URL+"/api/v1/sessions?scope=all&limit=10")
	if len(sessions.Data) != 0 {
		t.Fatalf("mixed malformed trace batch partially persisted: %#v", sessions.Data)
	}
}

// TestOTLPProtobufPersistsRecognizedPayloads proves protobuf decode feeds the
// same normalise/persist path as JSON (#129 Copilot review): a Codex logs
// envelope and a Codex skill metric both survive ingest→read.
func TestOTLPProtobufPersistsRecognizedPayloads(t *testing.T) {
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	server := httptest.NewServer(NewPersistentHandler(slog.Default(), repository))
	t.Cleanup(server.Close)

	logsBody := mustJSONOTLPToProtobuf(t, rawCodexOTLPLogs, "resourceLogs")
	logsResponse := postOTLPToPath(t, server.URL, "/v1/logs", logsBody, otlpContentTypeProtobuf)
	if logsResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("protobuf logs status = %d", logsResponse.StatusCode)
	}
	closeBody(t, logsResponse)

	sessions := fetchSessionList(t, server.URL+"/api/v1/sessions?limit=10")
	if len(sessions.Data) != 1 {
		t.Fatalf("expected 1 Codex session from protobuf logs, got %d: %#v", len(sessions.Data), sessions.Data)
	}
	if sessions.Data[0].SessionID != "codex:synthetic-conversation" {
		t.Fatalf("session id = %q", sessions.Data[0].SessionID)
	}
	model, _ := sessions.Data[0].Attributes["model"].(string)
	if model != "tiq-live-codex-model" {
		t.Fatalf("session model = %#v", sessions.Data[0].Attributes["model"])
	}

	metricsBody := mustJSONOTLPToProtobuf(t, rawCodexSkillMetrics, "resourceMetrics")
	metricsResponse := postOTLPToPath(t, server.URL, "/v1/metrics", metricsBody, otlpContentTypeProtobuf)
	if metricsResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("protobuf metrics status = %d", metricsResponse.StatusCode)
	}
	closeBody(t, metricsResponse)

	usage := getInsightJSON[skillUsageResponse](t, server.URL+"/api/v1/insights/skill-usage")
	if usage.Data.Totals.ExplicitDetection < 1 || usage.Data.Totals.ObservedSkills < 1 {
		t.Fatalf("expected explicit Codex skill from protobuf metrics, got %#v", usage.Data.Totals)
	}
	foundProbe := false
	for _, skill := range usage.Data.Skills {
		if skill.SkillName == "tiq-probe" {
			foundProbe = true
			break
		}
	}
	if !foundProbe {
		t.Fatalf("expected tiq-probe skill from protobuf metrics, got %#v", usage.Data.Skills)
	}
}

// TestCodexLogsPersistWithRawConversationIdentity proves the #88 invariant: the
// raw provider-native conversation identity reaches storage verbatim, with no
// HMAC fingerprint. Prompt/response/PII content-field gating (the api_key,
// output, user.email attributes) has no owning mechanism after the storage-side
// sanitizer was removed and is tracked separately in #94; #88 only de-hides
// identifiers, paths, and commands.
func TestCodexLogsPersistWithRawConversationIdentity(t *testing.T) {
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	server := httptest.NewServer(NewPersistentHandler(slog.Default(), repository))
	t.Cleanup(server.Close)
	payload := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_cli_rs"}},{"key":"service.version","value":{"stringValue":"0.145.0"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.sse_event"}},{"key":"model","value":{"stringValue":"synthetic-model"}},{"key":"arguments","value":{"stringValue":"--token=tiq-canary-argument-token"}},{"key":"output","value":{"stringValue":"tiq-canary-output"}},{"key":"custom_metadata","value":{"stringValue":"token=tiq-canary-provider-extension"}},{"key":"api_key","value":{"stringValue":"tiq-canary-api-key"}},{"key":"user.email","value":{"stringValue":"synthetic@example.test"}},{"key":"conversation.id","value":{"stringValue":"synthetic-conversation"}}],"body":{"stringValue":"synthetic body"}}]}]}]}`)
	response := postOTLPToPath(t, server.URL, "/v1/logs", payload, "application/json")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d", response.StatusCode)
	}
	closeBody(t, response)
	sessions, err := repository.ListSessions(context.Background(), storage.SessionFilter{Limit: 10})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %#v, %v", sessions, err)
	}
	events, err := repository.ListEvents(context.Background(), storage.EventFilter{SessionID: sessions[0].SessionID, Limit: 10})
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	eventData, err := json.Marshal(events[0])
	if err != nil {
		t.Fatal(err)
	}
	// The raw conversation identity is stored verbatim — no HMAC fingerprint.
	if !strings.Contains(string(eventData), "synthetic-conversation") {
		t.Fatalf("raw conversation identity must survive to storage, got %s", eventData)
	}
	if sessions[0].SessionID != "codex:synthetic-conversation" {
		t.Fatalf("session id = %q, want raw provider-native conversation ID", sessions[0].SessionID)
	}
}

func TestCodexTokenUsageMetricsPersistThroughMetricsReceiver(t *testing.T) {
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	server := httptest.NewServer(NewPersistentHandler(slog.Default(), repository))
	t.Cleanup(server.Close)

	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "codex", "observed-sanitised", "codex-0.153.4-turn-token-usage-metrics.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	response := postOTLPToPath(t, server.URL, "/v1/metrics", fixture.Payload, "application/json")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("token metrics status = %d", response.StatusCode)
	}
	closeBody(t, response)

	sessions, err := repository.ListSessions(context.Background(), storage.SessionFilter{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	events := tokenUsageMetricEvents(t, repository, sessions)
	assertPersistedInputTokenMetric(t, events)
}

func tokenUsageMetricEvents(t *testing.T, repository storage.Repository, sessions []canonical.Session) []canonical.Event {
	t.Helper()
	if len(sessions) != 6 {
		t.Fatalf("sessions = %d, want one per token category event", len(sessions))
	}
	var events []canonical.Event
	for _, session := range sessions {
		events = append(events, tokenUsageMetricEvent(t, repository, session.SessionID))
	}
	return events
}

func tokenUsageMetricEvent(t *testing.T, repository storage.Repository, sessionID string) canonical.Event {
	t.Helper()
	events, err := repository.ListEvents(context.Background(), storage.EventFilter{SessionID: sessionID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events for %s = %d, want 1", sessionID, len(events))
	}
	if events[0].EventType != "codex.turn.token_usage" {
		t.Fatalf("event type = %q", events[0].EventType)
	}
	encoded, err := json.Marshal(events[0])
	if err != nil {
		t.Fatal(err)
	}
	assertNoRawIdentifiers(t, []string{"tiq-canary-api-key", "synthetic@example.test"}, encoded)
	return events[0]
}

func assertPersistedInputTokenMetric(t *testing.T, events []canonical.Event) {
	t.Helper()
	for _, event := range events {
		if event.Attributes["input_token_count"] == float64(1200) || event.Attributes["input_token_count"] == int64(1200) {
			return
		}
	}
	t.Fatalf("expected one persisted input token event, got %#v", events)
}

func TestClaudeTokenUsageMetricsPersistThroughMetricsReceiver(t *testing.T) {
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	server := httptest.NewServer(NewPersistentHandler(slog.Default(), repository))
	t.Cleanup(server.Close)

	response := postOTLPToPath(t, server.URL, "/v1/metrics", metricsFixturePayloadBytes(t, "claude-code-2.1.268-token-usage-metrics.json"), "application/json")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("claude token metrics status = %d", response.StatusCode)
	}
	closeBody(t, response)

	// Claude Code stamps a real per-datapoint session.id, so every token category
	// shares one session (unlike Codex, which keys the session on the event ID).
	sessions, err := repository.ListSessions(context.Background(), storage.SessionFilter{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "claude-code:00000000-0000-4000-8000-000000000001" {
		t.Fatalf("sessions = %#v, want one raw provider-native Claude session", sessions)
	}
	events, err := repository.ListEvents(context.Background(), storage.EventFilter{SessionID: sessions[0].SessionID, Limit: 20})
	if err != nil || len(events) != 4 {
		t.Fatalf("events = %#v, %v, want 4 token categories", events, err)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	assertNoRawIdentifiers(t, []string{"microrutter2514@gmail.com", "synthetic@example.test", "user_synthetic"}, encoded)
	var sawInput bool
	for _, event := range events {
		if event.EventType != "claude_code.token.usage" {
			t.Fatalf("event type = %q", event.EventType)
		}
		if event.Attributes["input_token_count"] == float64(10) || event.Attributes["input_token_count"] == int64(10) {
			sawInput = true
		}
	}
	if !sawInput {
		t.Fatalf("expected a persisted input token count of 10, got %s", encoded)
	}
}

// TestClaudeTraceSpansPersistThroughTracesReceiver proves the F3 route end to
// end: the enhanced-telemetry beta span fixture POSTs to /v1/traces, persists,
// and reads back correlated to the same raw provider-native session id as the
// metrics/logs fixtures — with the interaction → llm_request span tree intact
// and no raw identifiers leaked (#50 resolved; #88/#87 raw-identity invariant).
func TestClaudeTraceSpansPersistThroughTracesReceiver(t *testing.T) {
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	server := httptest.NewServer(NewPersistentHandler(slog.Default(), repository))
	t.Cleanup(server.Close)

	response := postOTLPToPath(t, server.URL, "/v1/traces", metricsFixturePayloadBytes(t, "claude-code-2.1.268-trace-spans-otlp.json"), "application/json")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("claude trace spans status = %d", response.StatusCode)
	}
	closeBody(t, response)

	// Both spans carry the same session.id, so they share one raw session — the
	// same identity the token-usage metrics fixture uses, proving cross-signal
	// correlation.
	sessions, err := repository.ListSessions(context.Background(), storage.SessionFilter{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "claude-code:00000000-0000-4000-8000-000000000001" {
		t.Fatalf("sessions = %#v, want one raw provider-native Claude session", sessions)
	}
	events, err := repository.ListEvents(context.Background(), storage.EventFilter{SessionID: sessions[0].SessionID, Limit: 20})
	if err != nil || len(events) != 2 {
		t.Fatalf("events = %#v, %v, want 2 spans (interaction + llm_request)", events, err)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	assertNoRawIdentifiers(t, []string{"microrutter2514@gmail.com", "synthetic@example.test", "user_synthetic", "8d699259db92da74599fd7d5007f335c54e8b16495fdb36464dfbf688d4145bc"}, encoded)

	var sawInteraction, sawLLMRequest bool
	for _, event := range events {
		switch event.EventType {
		case "claude_code.interaction":
			sawInteraction = true
		case "claude_code.llm_request":
			sawLLMRequest = true
		default:
			t.Fatalf("unexpected span event type %q", event.EventType)
		}
	}
	if !sawInteraction || !sawLLMRequest {
		t.Fatalf("expected both spans persisted: interaction=%v llm_request=%v (%s)", sawInteraction, sawLLMRequest, encoded)
	}
}

// TestMixedToolMetricsBatchPersistsBothTools proves the dual-adapter accumulator:
// a single /v1/metrics payload carrying both a Codex and a Claude resource
// persists both tools' token events (the negative sentinel tests alone do not
// exercise accumulation).
func TestMixedToolMetricsBatchPersistsBothTools(t *testing.T) {
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	server := httptest.NewServer(NewPersistentHandler(slog.Default(), repository))
	t.Cleanup(server.Close)

	payload := mergeMetricsPayloads(t,
		metricsFixturePayloadBytes(t, "codex-0.153.4-turn-token-usage-metrics.json"),
		metricsFixturePayloadBytes(t, "claude-code-2.1.268-token-usage-metrics.json"),
	)
	response := postOTLPToPath(t, server.URL, "/v1/metrics", payload, "application/json")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("mixed metrics status = %d", response.StatusCode)
	}
	closeBody(t, response)

	sessions, err := repository.ListSessions(context.Background(), storage.SessionFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var sawCodex, sawClaude bool
	for _, session := range sessions {
		events, err := repository.ListEvents(context.Background(), storage.EventFilter{SessionID: session.SessionID, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			switch event.Tool {
			case "codex":
				sawCodex = true
			case "claude-code":
				sawClaude = true
			}
		}
	}
	if !sawCodex || !sawClaude {
		t.Fatalf("mixed batch must persist both tools: codex=%v claude=%v", sawCodex, sawClaude)
	}
}

// TestMixedMetricsBatchMalformedClaudeRejectsWholeBatch proves the
// no-partial-persistence contract: a payload with a valid Codex resource and a
// malformed Claude token.usage resource is rejected whole, persisting nothing.
func TestMixedMetricsBatchMalformedClaudeRejectsWholeBatch(t *testing.T) {
	repository, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	server := httptest.NewServer(NewPersistentHandler(slog.Default(), repository))
	t.Cleanup(server.Close)

	malformedClaude := []byte(`{"resourceMetrics":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"claude-code"}}]},"scopeMetrics":[{"metrics":[{"name":"claude_code.token.usage","sum":{"dataPoints":[{"attributes":[{"key":"type","value":{"stringValue":"input"}}],"asDouble":-1,"timeUnixNano":"1789042160000000000"}]}}]}]}]}`)
	payload := mergeMetricsPayloads(t,
		metricsFixturePayloadBytes(t, "codex-0.153.4-turn-token-usage-metrics.json"),
		malformedClaude,
	)
	response := postOTLPToPath(t, server.URL, "/v1/metrics", payload, "application/json")
	assertIngestError(t, response, http.StatusUnprocessableEntity, "normalization_failed")

	sessions, err := repository.ListSessions(context.Background(), storage.SessionFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("a rejected batch must persist nothing, got %#v", sessions)
	}
}

// metricsFixturePayloadBytes reads a fixture wrapper and returns its OTLP payload.
func metricsFixturePayloadBytes(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "claude", "observed-sanitised", name))
	if os.IsNotExist(err) {
		data, err = os.ReadFile(filepath.Join("..", "..", "fixtures", "codex", "observed-sanitised", name))
	}
	if os.IsNotExist(err) {
		data, err = os.ReadFile(filepath.Join("..", "..", "fixtures", "cursor", "observed-sanitised", name))
	}
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture.Payload
}

// mergeMetricsPayloads concatenates the resourceMetrics arrays of two OTLP
// payloads into a single batch, so one POST carries multiple tools' resources.
func mergeMetricsPayloads(t *testing.T, payloads ...[]byte) []byte {
	t.Helper()
	var merged []json.RawMessage
	for _, payload := range payloads {
		var decoded struct {
			ResourceMetrics []json.RawMessage `json:"resourceMetrics"`
		}
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatal(err)
		}
		merged = append(merged, decoded.ResourceMetrics...)
	}
	out, err := json.Marshal(struct {
		ResourceMetrics []json.RawMessage `json:"resourceMetrics"`
	}{ResourceMetrics: merged})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestObservedSanitisedFixtureReplaysToLogsReceiver(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "codex", "observed-sanitised", "codex-0.145.0-logs.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(slog.Default()))
	t.Cleanup(server.Close)
	response := postOTLPToPath(t, server.URL, "/v1/logs", fixture.Payload, "application/json")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("fixture replay status = %d", response.StatusCode)
	}
	closeBody(t, response)
}

// TestDevelopmentInspectorRecordsLastIngest exercises the dev-only debug echo.
// After issue #88 removed the storage-side sanitizer, the inspector reflects the
// raw last-seen payload for local troubleshooting (dev mode is opt-in and
// local-only); prompt/response/PII content-field gating is tracked in #94.
func TestDevelopmentInspectorRecordsLastIngest(t *testing.T) {
	server := httptest.NewServer(NewDevelopmentHandler(slog.Default()))
	t.Cleanup(server.Close)
	payload := []byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"attributes":[{"key":"model","value":{"stringValue":"synthetic-model"}}],"body":{"stringValue":"synthetic body"}}]}]}]}`)
	response := postOTLPToPath(t, server.URL, "/v1/logs", payload, "application/json")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("inspector ingest status = %d", response.StatusCode)
	}
	closeBody(t, response)
	inspected, err := http.Get(server.URL + "/api/v1/development/last-ingest")
	if err != nil {
		t.Fatal(err)
	}
	defer closeBody(t, inspected)
	var value any
	if err := json.NewDecoder(inspected.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	serialized, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(serialized), "synthetic-model") {
		t.Fatalf("development inspector did not record the last ingest: %s", serialized)
	}
}

func postOTLP(t *testing.T, serverURL string, body []byte) *http.Response {
	t.Helper()
	return postOTLPWithContentType(t, serverURL, body, "application/json")
}

func mustMarshalOTLPLogsProtobuf(t *testing.T) []byte {
	t.Helper()
	req := &logspb.LogsData{
		ResourceLogs: []*logspb.ResourceLogs{{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{{
					Key: "service.name",
					Value: &commonpb.AnyValue{
						Value: &commonpb.AnyValue_StringValue{StringValue: "synthetic-protobuf"},
					},
				}},
			},
			ScopeLogs: []*logspb.ScopeLogs{{
				LogRecords: []*logspb.LogRecord{{
					Body: &commonpb.AnyValue{
						Value: &commonpb.AnyValue_StringValue{StringValue: "synthetic"},
					},
				}},
			}},
		}},
	}
	body, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal logs protobuf: %v", err)
	}
	return body
}

func mustMarshalOTLPMetricsProtobuf(t *testing.T) []byte {
	t.Helper()
	req := &metricspb.MetricsData{
		ResourceMetrics: []*metricspb.ResourceMetrics{{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{{
					Key: "service.name",
					Value: &commonpb.AnyValue{
						Value: &commonpb.AnyValue_StringValue{StringValue: "synthetic-protobuf"},
					},
				}},
			},
			ScopeMetrics: []*metricspb.ScopeMetrics{{
				Metrics: []*metricspb.Metric{{
					Name: "synthetic",
				}},
			}},
		}},
	}
	body, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal metrics protobuf: %v", err)
	}
	return body
}

func mustJSONOTLPToProtobuf(t *testing.T, jsonPayload, resourceField string) []byte {
	t.Helper()
	opts := protojson.UnmarshalOptions{DiscardUnknown: true}
	var message proto.Message
	switch resourceField {
	case "resourceLogs":
		message = &logspb.LogsData{}
	case "resourceMetrics":
		message = &metricspb.MetricsData{}
	case "resourceSpans":
		message = &tracepb.TracesData{}
	default:
		t.Fatalf("unsupported resource field %q", resourceField)
	}
	if err := opts.Unmarshal([]byte(jsonPayload), message); err != nil {
		t.Fatalf("protojson unmarshal: %v", err)
	}
	body, err := proto.Marshal(message)
	if err != nil {
		t.Fatalf("protobuf marshal: %v", err)
	}
	return body
}

func mustGzip(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func postOTLPWithContentType(t *testing.T, serverURL string, body []byte, contentType string) *http.Response {
	t.Helper()
	return postOTLPToPath(t, serverURL, "/v1/traces", body, contentType)
}

func postOTLPToPath(t *testing.T, serverURL, path string, body []byte, contentType string) *http.Response {
	t.Helper()
	return postOTLPToPathWithEncoding(t, serverURL, path, body, contentType, "")
}

func postOTLPToPathWithEncoding(t *testing.T, serverURL, path string, body []byte, contentType, contentEncoding string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, serverURL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	if contentEncoding != "" {
		req.Header.Set("Content-Encoding", contentEncoding)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST OTLP payload: %v", err)
	}
	return resp
}

func assertIngestError(t *testing.T, resp *http.Response, wantStatus int, wantCode string) {
	t.Helper()
	defer closeBody(t, resp)
	if resp.StatusCode != wantStatus {
		t.Fatalf("expected status %d, got %d", wantStatus, resp.StatusCode)
	}
	var body ingestErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body.Error.Code != wantCode {
		t.Fatalf("expected error code %q, got %q", wantCode, body.Error.Code)
	}
	if strings.Contains(body.Error.Message, "resourceSpans") && wantCode == "malformed_payload" {
		t.Fatalf("malformed response must not expose payload detail: %q", body.Error.Message)
	}
}

func closeBody(t *testing.T, resp *http.Response) {
	t.Helper()
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close response body: %v", err)
	}
}
