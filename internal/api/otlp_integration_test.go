package api

import (
	"bytes"
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
)

func TestOTLPHTTPIngestProof(t *testing.T) {
	server := httptest.NewServer(NewHandler(slog.Default()))
	t.Cleanup(server.Close)

	// Traces must fail honestly — never 202-then-drop (issue #50).
	traces := postOTLP(t, server.URL, []byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"0123456789abcdef0123456789abcdef","spanId":"0123456789abcdef","name":"synthetic"}]}]}]}`))
	assertIngestError(t, traces, http.StatusNotImplemented, "not_implemented")

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

	unsupportedMediaType := postOTLPToPath(t, server.URL, "/v1/logs", []byte(`{"resourceLogs":[{}]}`), "application/x-protobuf")
	assertIngestError(t, unsupportedMediaType, http.StatusUnsupportedMediaType, "unsupported_media_type")

	resp, err := http.Get(server.URL + "/api/v1/ingest/counters")
	if err != nil {
		t.Fatalf("GET ingest counters: %v", err)
	}
	defer closeBody(t, resp)
	var counters ingestCountersResponse
	if err := json.NewDecoder(resp.Body).Decode(&counters); err != nil {
		t.Fatalf("decode counters: %v", err)
	}
	// 1 accepted log + 1 accepted metrics + 1 unsupported traces + 4 validation rejects
	if counters.AcceptedPayloads != 2 || counters.RejectedPayloads != 5 {
		t.Fatalf("unexpected counters: %+v", counters)
	}
}

func TestOTLPTracesStillRefusedMetricsAccepted(t *testing.T) {
	server := httptest.NewServer(NewHandler(slog.Default()))
	t.Cleanup(server.Close)

	traces := postOTLPToPath(t, server.URL, "/v1/traces", []byte(`{"ignored":true}`), "application/json")
	assertIngestError(t, traces, http.StatusNotImplemented, "not_implemented")

	metrics := postOTLPToPath(t, server.URL, "/v1/metrics", []byte(`{"resourceMetrics":[{"scopeMetrics":[{"metrics":[{"name":"synthetic"}]}]}]}`), "application/json")
	if metrics.StatusCode != http.StatusAccepted {
		t.Fatalf("expected metrics status 202, got %d", metrics.StatusCode)
	}
	closeBody(t, metrics)
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

func postOTLPWithContentType(t *testing.T, serverURL string, body []byte, contentType string) *http.Response {
	t.Helper()
	return postOTLPToPath(t, serverURL, "/v1/traces", body, contentType)
}

func postOTLPToPath(t *testing.T, serverURL, path string, body []byte, contentType string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, serverURL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
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
