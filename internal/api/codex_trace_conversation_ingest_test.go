package api

import (
	"bytes"
	"net/http"
	"testing"
)

func TestCodexTraceWithObservedConversationIDJoinsLogSession(t *testing.T) {
	server, _ := newPersistentTestServer(t)
	logResponse := postOTLPToPath(t, server.URL, "/v1/logs", []byte(rawCodexOTLPLogs), otlpContentTypeJSON)
	if logResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("log ingest status = %d", logResponse.StatusCode)
	}
	closeBody(t, logResponse)
	tracePayload := bytes.Replace([]byte(rawCodexOTLPTraces), []byte(`{"key":"env","value":{"stringValue":"telemetryiq-synthetic"}}`), []byte(`{"key":"env","value":{"stringValue":"telemetryiq-synthetic"}},{"key":"conversation.id","value":{"stringValue":"synthetic-conversation"}}`), 1)
	traceResponse := postOTLPToPath(t, server.URL, "/v1/traces", tracePayload, otlpContentTypeJSON)
	if traceResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("trace ingest status = %d", traceResponse.StatusCode)
	}
	closeBody(t, traceResponse)
	sessions := fetchSessionList(t, server.URL+"/api/v1/sessions?limit=10")
	if len(sessions.Data) != 1 || sessions.Data[0].SessionID != "codex:synthetic-conversation" || sessions.Data[0].IdentitySource != "conversation.id" {
		t.Fatalf("joined sessions = %#v", sessions.Data)
	}
	spans := getSpanPage(t, server.URL+"/api/v1/sessions/codex:synthetic-conversation/spans?limit=10")
	if len(spans.Data) != 2 || spans.Data[0].TraceID != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("joined spans = %#v", spans.Data)
	}
}
