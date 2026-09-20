package api

import (
	"net/http"
	"net/url"
	"testing"
)

func TestClaudeTraceIngestProjectsSpanEvidence(t *testing.T) {
	server, _ := newPersistentTestServer(t)
	response := postOTLPToPath(t, server.URL, "/v1/traces", metricsFixturePayloadBytes(t, "claude-code-2.1.268-trace-spans-otlp.json"), otlpContentTypeJSON)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("trace ingest status = %d", response.StatusCode)
	}
	closeBody(t, response)

	const sessionID = "claude-code:00000000-0000-4000-8000-000000000001"
	first := getSpanPage(t, server.URL+"/api/v1/sessions/"+url.PathEscape(sessionID)+"/spans?limit=1")
	assertFirstClaudeSpanPage(t, first)
	second := getSpanPage(t, server.URL+"/api/v1/sessions/"+url.PathEscape(sessionID)+"/spans?limit=1&cursor="+url.QueryEscape(*first.Pagination.NextCursor))
	assertSecondClaudeSpanPage(t, second)
}

func assertFirstClaudeSpanPage(t *testing.T, page spanListResponse) {
	t.Helper()
	if len(page.Data) != 1 || page.Data[0].TraceID != "00000000000000000000000000000001" || page.Data[0].SpanID != "0000000000000002" || page.Data[0].ParentAvailability != "root" || page.Data[0].DurationMs == nil || page.Data[0].StatusCode == nil || *page.Data[0].StatusCode != 0 || page.Pagination.NextCursor == nil {
		t.Fatalf("unexpected first Claude span page")
	}
}

func assertSecondClaudeSpanPage(t *testing.T, page spanListResponse) {
	t.Helper()
	if len(page.Data) != 1 || page.Data[0].SpanID != "0000000000000001" || page.Data[0].ParentSpanID == nil || *page.Data[0].ParentSpanID != "0000000000000002" || page.Data[0].ParentAvailability != "not_loaded" || page.Data[0].DurationMs == nil || page.Data[0].StatusCode == nil || *page.Data[0].StatusCode != 0 {
		t.Fatalf("unexpected second Claude span page")
	}
}
