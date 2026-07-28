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

	"github.com/wayne/telemetryiq/internal/privacy"
	"github.com/wayne/telemetryiq/internal/storage"
	"github.com/wayne/telemetryiq/internal/storage/sqlite"
)

func TestOTLPHTTPIngestProof(t *testing.T) {
	server := httptest.NewServer(NewHandler(slog.Default()))
	t.Cleanup(server.Close)

	accepted := postOTLP(t, server.URL, []byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"0123456789abcdef0123456789abcdef","spanId":"0123456789abcdef","name":"synthetic"}]}]}]}`))
	if accepted.StatusCode != http.StatusAccepted {
		t.Fatalf("expected supported payload status 202, got %d", accepted.StatusCode)
	}
	closeBody(t, accepted)

	logs := postOTLPToPath(t, server.URL, "/v1/logs", []byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"synthetic"}}]}]}]}`), "application/json")
	if logs.StatusCode != http.StatusAccepted {
		t.Fatalf("expected OTLP logs status 202, got %d", logs.StatusCode)
	}
	closeBody(t, logs)

	malformed := postOTLP(t, server.URL, []byte(`{"resourceSpans":`))
	assertIngestError(t, malformed, http.StatusBadRequest, "malformed_payload")

	invalid := postOTLP(t, server.URL, []byte(`{"resourceSpans":[]}`))
	assertIngestError(t, invalid, http.StatusBadRequest, "invalid_payload")

	oversized := postOTLP(t, server.URL, bytes.Repeat([]byte("x"), int(maxOTLPPayloadBytes)+1))
	assertIngestError(t, oversized, http.StatusRequestEntityTooLarge, "payload_too_large")

	unsupportedMediaType := postOTLPWithContentType(t, server.URL, []byte(`{"resourceSpans":[{}]}`), "application/x-protobuf")
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
	if counters.AcceptedPayloads != 2 || counters.RejectedPayloads != 4 {
		t.Fatalf("unexpected counters: %+v", counters)
	}
}

func TestCodexLogsPersistAsSanitizedCanonicalSession(t *testing.T) {
	sanitizer, err := privacy.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repository, err := sqlite.Open(":memory:", sanitizer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	server := httptest.NewServer(NewPersistentHandler(slog.Default(), sanitizer, repository))
	t.Cleanup(server.Close)
	payload := []byte(`{"resourceLogs":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"codex_exec"}},{"key":"service.version","value":{"stringValue":"0.145.0"}}]},"scopeLogs":[{"logRecords":[{"attributes":[{"key":"event.name","value":{"stringValue":"codex.sse_event"}},{"key":"model","value":{"stringValue":"synthetic-model"}},{"key":"user.email","value":{"stringValue":"synthetic@example.test"}},{"key":"conversation.id","value":{"stringValue":"synthetic-conversation"}}],"body":{"stringValue":"synthetic body"}}]}]}]}`)
	response := postOTLPToPath(t, server.URL, "/v1/logs", payload, "application/json")
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d", response.StatusCode)
	}
	closeBody(t, response)
	sessions, err := repository.ListSessions(context.Background(), storage.SessionFilter{Limit: 10})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %#v, %v", sessions, err)
	}
	data, err := json.Marshal(sessions[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "synthetic@example.test") || strings.Contains(string(data), "synthetic-conversation") || strings.Contains(string(data), "synthetic body") {
		t.Fatalf("privacy leak: %s", data)
	}
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

func TestDevelopmentInspectorSanitizesOTLPAttributes(t *testing.T) {
	sanitizer, err := privacy.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewDevelopmentHandler(slog.Default(), sanitizer))
	t.Cleanup(server.Close)
	payload := []byte(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"attributes":[{"key":"user.email","value":{"stringValue":"synthetic@example.test"}},{"key":"model","value":{"stringValue":"synthetic-model"}}],"body":{"stringValue":"synthetic body"}}]}]}]}`)
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
	if strings.Contains(string(serialized), "synthetic@example.test") || strings.Contains(string(serialized), "synthetic body") {
		t.Fatalf("development inspector leaked sensitive content: %s", serialized)
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
