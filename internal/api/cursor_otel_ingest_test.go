package api

import (
	"testing"
)

// TestCursorEnterpriseOTELIngestEndToEnd is the live ingest→read gate for #130:
// POST synthetic Cursor Enterprise OTEL logs and metrics fixtures to the
// running receiver, then prove the HTTP read API surfaces the Cursor session
// and token fields without retaining account canaries.
func TestCursorEnterpriseOTELIngestEndToEnd(t *testing.T) {
	server, _ := newPersistentTestServer(t)
	postCursorOTLPAccepted(t, server, "/v1/logs", "cursor-otel-0.1.0-api-request-logs.json")
	postCursorOTLPAccepted(t, server, "/v1/metrics", "cursor-otel-0.1.0-token-usage-metrics.json")

	sessions := fetchSessionList(t, server.URL+"/api/v1/sessions?limit=50")
	assertCursorEnterpriseSessionsOnReadAPI(t, sessions)

	canaries := []string{"424242", "434343", "cursor.team.id", "cursor.user.id", "tiq-probe"}
	assertNoRawIdentifiers(t, canaries, marshalJSON(t, sessions))

	events := timelinePage(t, server.URL+"/api/v1/sessions/cursor:00000000-0000-4000-8000-0000000000c1/events?limit=10")
	if len(events.Data) != 1 || events.Data[0].EventType != "cursor.api.request" {
		t.Fatalf("timeline = %#v, want one cursor.api.request", events.Data)
	}
	assertNoRawIdentifiers(t, canaries, marshalJSON(t, events))
}

func assertCursorEnterpriseSessionsOnReadAPI(t *testing.T, sessions sessionListResponse) {
	t.Helper()
	var foundAPIRequest bool
	var foundTokenMetric bool
	for _, session := range sessions.Data {
		if session.Tool != "cursor" {
			continue
		}
		if session.SessionID == "cursor:00000000-0000-4000-8000-0000000000c1" {
			foundAPIRequest = true
			model, _ := session.Attributes["model"].(string)
			if model != "Thinking" {
				t.Fatalf("api.request session model = %#v", session.Attributes["model"])
			}
		}
		if session.Attributes["model"] == "Auto" {
			foundTokenMetric = true
		}
	}
	if !foundAPIRequest {
		t.Fatalf("expected cursor conversation session from api.request logs, got %#v", sessions.Data)
	}
	if !foundTokenMetric {
		t.Fatalf("expected cursor token metric session with model Auto, got %#v", sessions.Data)
	}
}
