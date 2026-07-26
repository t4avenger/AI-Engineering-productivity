package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
