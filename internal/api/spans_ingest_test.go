package api

import (
	"net/http"
	"net/url"
	"testing"
)

func TestCodexTraceIngestProjectsSpanEvidence(t *testing.T) {
	server, _ := newPersistentTestServer(t)
	response := postOTLPToPath(t, server.URL, "/v1/traces", []byte(rawCodexOTLPTraces), otlpContentTypeJSON)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("trace ingest status = %d", response.StatusCode)
	}
	closeBody(t, response)

	session := assertCodexTraceObservation(t, server)
	page := getSpanPage(t, server.URL+"/api/v1/sessions/"+url.PathEscape(session.SessionID)+"/spans?limit=1")
	if len(page.Data) != 1 || page.Data[0].TraceID != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" || page.Data[0].SpanID != "1111111111111111" || page.Data[0].ParentAvailability != "root" || page.Pagination.NextCursor == nil {
		t.Fatalf("first span page = %#v", page)
	}
	page = getSpanPage(t, server.URL+"/api/v1/sessions/"+url.PathEscape(session.SessionID)+"/spans?limit=1&cursor="+url.QueryEscape(*page.Pagination.NextCursor))
	if len(page.Data) != 1 || page.Data[0].SpanID != "2222222222222222" || page.Data[0].ParentAvailability != "not_loaded" || page.Data[0].DurationMs == nil || *page.Data[0].DurationMs <= 0 {
		t.Fatalf("second span page = %#v", page)
	}
}
