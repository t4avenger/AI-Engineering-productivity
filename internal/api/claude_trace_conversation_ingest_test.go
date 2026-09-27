package api

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/wayne/telemetryiq/internal/conversation"
)

// TestClaudeTraceLogShareSessionJoinAndConversation proves the #210 contract from
// one version-pinned synthetic run: the OTLP trace spans and the OTLP content
// logs carry the same raw session.id, so ingest→read reconstructs ONE provider
// session that exposes both the trace spans and the projected conversation. The
// join key is session.id alone — no time/model/prompt heuristic.
func TestClaudeTraceLogShareSessionJoinAndConversation(t *testing.T) {
	server, _ := newPersistentTestServer(t)

	logResponse := postOTLPToPath(t, server.URL, "/v1/logs", metricsFixturePayloadBytes(t, "claude-code-2.1.283-log-conversation-otlp.json"), otlpContentTypeJSON)
	if logResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("log ingest status = %d", logResponse.StatusCode)
	}
	closeBody(t, logResponse)

	traceResponse := postOTLPToPath(t, server.URL, "/v1/traces", metricsFixturePayloadBytes(t, "claude-code-2.1.283-trace-conversation-otlp.json"), otlpContentTypeJSON)
	if traceResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("trace ingest status = %d", traceResponse.StatusCode)
	}
	closeBody(t, traceResponse)

	sessions := fetchSessionList(t, server.URL+"/api/v1/sessions?limit=10")
	if len(sessions.Data) != 1 {
		t.Fatalf("trace + log must join into one provider session, got %#v", sessions.Data)
	}
	joined := sessions.Data[0]
	if joined.SessionID != "claude-code:tiq-corr-210" || joined.IdentitySource != "session.id" || joined.IdentityScope != "provider" {
		t.Fatalf("joined identity = %#v", joined)
	}
	if joined.Availability["conversation"] != "observed" {
		t.Fatalf("conversation coverage = %q, want observed: %#v", joined.Availability["conversation"], joined.Availability)
	}

	spans := getSpanPage(t, server.URL+"/api/v1/sessions/claude-code:tiq-corr-210/spans?limit=10")
	if len(spans.Data) != 2 || spans.Data[0].TraceID != "00000000000000000000000000000210" {
		t.Fatalf("joined spans = %#v", spans.Data)
	}

	page := getConversationPage(t, server.URL+"/api/v1/sessions/claude-code:tiq-corr-210/conversation?limit=10")
	if len(page.Data) != 2 {
		t.Fatalf("conversation records = %#v", page.Data)
	}
	assertConversationTurn(t, page.Data[0], conversation.RoleUser, "synthetic correlation prompt for issue 210")
	assertConversationTurn(t, page.Data[1], conversation.RoleAssistant, "synthetic correlation response for issue 210")
}

// TestClaudeTraceOnlyStaysSeparateObservation proves the non-join fallback: a
// trace with no session.id is keyed by trace id, surfaces only under the
// observation scope with identity_source trace.id, is absent from the provider
// scope, and reports conversation coverage as unavailable. This is the
// counter-example to any proximity/heuristic join.
func TestClaudeTraceOnlyStaysSeparateObservation(t *testing.T) {
	server, _ := newPersistentTestServer(t)

	tracePayload := metricsFixturePayloadBytes(t, "claude-code-2.1.283-trace-conversation-otlp.json")
	traceOnly := bytes.ReplaceAll(tracePayload, []byte(`"tiq-corr-210"`), []byte(`""`))
	if bytes.Equal(traceOnly, tracePayload) {
		t.Fatal("fixture no longer carries the shared session.id to strip")
	}
	response := postOTLPToPath(t, server.URL, "/v1/traces", traceOnly, otlpContentTypeJSON)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("trace ingest status = %d", response.StatusCode)
	}
	closeBody(t, response)

	const observationID = "claude-code:trace:00000000000000000000000000000210"

	observations := fetchSessionList(t, server.URL+"/api/v1/sessions?scope=observation&limit=10")
	if len(observations.Data) != 1 {
		t.Fatalf("observation sessions = %#v", observations.Data)
	}
	observation := observations.Data[0]
	if observation.SessionID != observationID || observation.IdentitySource != "trace.id" || observation.IdentityScope != "observation" {
		t.Fatalf("observation identity = %#v", observation)
	}
	if observation.Availability["conversation"] != "unavailable" {
		t.Fatalf("conversation coverage = %q, want unavailable: %#v", observation.Availability["conversation"], observation.Availability)
	}

	provider := fetchSessionList(t, server.URL+"/api/v1/sessions?limit=10")
	for _, session := range provider.Data {
		if session.SessionID == observationID {
			t.Fatalf("trace-only observation leaked into the provider scope: %#v", session)
		}
	}
}

func assertConversationTurn(t *testing.T, record conversationRecord, role, text string) {
	t.Helper()
	if record.Role != role {
		t.Fatalf("role = %q, want %q: %#v", record.Role, role, record)
	}
	if record.ContentAvailability != conversation.AvailabilityAvailable {
		t.Fatalf("content availability = %q, want available: %#v", record.ContentAvailability, record)
	}
	if record.Text == nil || *record.Text != text {
		t.Fatalf("text = %v, want %q", record.Text, text)
	}
}
