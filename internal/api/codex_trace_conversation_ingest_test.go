package api

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/wayne/telemetryiq/internal/breakdown"
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

func TestCodexThreadTurnTraceProjectsObservedModelGeneration(t *testing.T) {
	server, _ := newPersistentTestServer(t)
	for _, ingest := range []struct {
		path, fixture string
	}{
		{path: "/v1/logs", fixture: "codex-0.155.1-log-thread-turn-otlp.json"},
		{path: "/v1/traces", fixture: "codex-0.155.1-trace-thread-turn-otlp.json"},
	} {
		response := postOTLPToPath(t, server.URL, ingest.path, metricsFixturePayloadBytes(t, ingest.fixture), otlpContentTypeJSON)
		if response.StatusCode != http.StatusAccepted {
			t.Fatalf("%s ingest status = %d", ingest.path, response.StatusCode)
		}
		closeBody(t, response)
	}

	result := getBreakdown(t, server.URL+"/api/v1/sessions/codex:tiq-thread-218/breakdown")
	if result.Availability != breakdown.AvailabilityAvailable || len(result.Categories) != 1 {
		t.Fatalf("breakdown = %#v", result)
	}
	category := result.Categories[0]
	if category.ID != breakdown.CategoryModelGeneration || category.DurationMs != 2_000 || len(category.SourceEventIDs) != 1 {
		t.Fatalf("category = %#v", category)
	}
}
