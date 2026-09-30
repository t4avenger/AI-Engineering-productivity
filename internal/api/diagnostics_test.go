package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wayne/telemetryiq/internal/storage"
)

const diagnosticTestToken = "diagnostic-test-token"

func TestDiagnosticAPIContract(t *testing.T) {
	repository := sessionTestRepository(t)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	server := httptest.NewServer(NewAuthenticatedPersistentHandler(logger, repository, diagnosticTestToken, DefaultInsightThresholds()))
	t.Cleanup(server.Close)

	response := postOTLPToPath(t, server.URL, "/v1/logs", []byte(rawCodexOTLPLogs), otlpContentTypeJSON)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("ingest status = %d", response.StatusCode)
	}
	closeBody(t, response)

	preview := authenticatedDiagnosticRequest(t, http.MethodGet, server.URL+"/api/v1/diagnostics/preview")
	if preview.StatusCode != http.StatusOK {
		t.Fatalf("preview status = %d", preview.StatusCode)
	}
	if preview.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("preview cache control = %q", preview.Header.Get("Cache-Control"))
	}
	previewBytes := readAndClose(t, preview)
	assertDiagnosticDocument(t, previewBytes)

	export := authenticatedDiagnosticRequest(t, http.MethodPost, server.URL+"/api/v1/diagnostics/export")
	if export.StatusCode != http.StatusOK {
		t.Fatalf("export status = %d", export.StatusCode)
	}
	if export.Header.Get("Content-Disposition") != `attachment; filename="telemetryiq-diagnostics.json"` {
		t.Fatalf("export disposition = %q", export.Header.Get("Content-Disposition"))
	}
	if export.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("export cache control = %q", export.Header.Get("Cache-Control"))
	}
	exportBytes := readAndClose(t, export)
	assertDiagnosticDocument(t, exportBytes)

	for _, canary := range []string{
		"tiq-canary-argument-token", "tiq-canary-output", "tiq-canary-provider-extension",
		"tiq-canary-api-key", "synthetic@example.test", "synthetic body", "synthetic-conversation",
	} {
		if bytes.Contains(previewBytes, []byte(canary)) || bytes.Contains(exportBytes, []byte(canary)) || strings.Contains(logs.String(), canary) {
			t.Fatalf("diagnostics leaked retained value %q", canary)
		}
	}
}

func TestDiagnosticEndpointsRequireAuthentication(t *testing.T) {
	repository := sessionTestRepository(t)
	server := httptest.NewServer(NewAuthenticatedPersistentHandler(slog.Default(), repository, diagnosticTestToken, DefaultInsightThresholds()))
	t.Cleanup(server.Close)
	for _, request := range []struct {
		method string
		path   string
	}{{http.MethodGet, "/api/v1/diagnostics/preview"}, {http.MethodPost, "/api/v1/diagnostics/export"}} {
		req, err := http.NewRequest(request.method, server.URL+request.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d", request.method, request.path, response.StatusCode)
		}
		closeBody(t, response)
	}
}

func TestDiagnosticPreviewHandlesReaderFailure(t *testing.T) {
	recorder := httptest.NewRecorder()
	diagnosticAPI{reader: failingDiagnosticReader{}}.preview(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics/preview", nil))
	if recorder.Code != http.StatusInternalServerError || bytes.Contains(recorder.Body.Bytes(), []byte("synthetic diagnostic failure")) {
		t.Fatalf("failure response = %d %s", recorder.Code, recorder.Body.String())
	}
}

type failingDiagnosticReader struct{}

func (failingDiagnosticReader) DiagnosticSummary(context.Context) (storage.DiagnosticSummary, error) {
	return storage.DiagnosticSummary{}, errors.New("synthetic diagnostic failure")
}

func authenticatedDiagnosticRequest(t *testing.T, method, url string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+diagnosticTestToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func readAndClose(t *testing.T, response *http.Response) []byte {
	t.Helper()
	defer closeBody(t, response)
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertDiagnosticDocument(t *testing.T, data []byte) {
	t.Helper()
	var response diagnosticResponse
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatalf("decode diagnostics: %v", err)
	}
	if response.Data.SchemaVersion != diagnosticSchemaVersion || response.Data.Service != "telemetryiq-daemon" {
		t.Fatalf("diagnostic metadata = %#v", response.Data)
	}
	if response.Data.Ingest.AcceptedPayloads != 1 || response.Data.Storage.EventCount < 1 || response.Data.Storage.SessionCount < 1 {
		t.Fatalf("diagnostic counts = %#v", response.Data)
	}
	foundCodex := false
	for _, item := range response.Data.Storage.ProviderTools {
		if item.Provider == "openai" && item.Tool == "codex" && item.EventCount > 0 {
			foundCodex = true
		}
	}
	if !foundCodex {
		t.Fatalf("diagnostic provider counts = %#v", response.Data.Storage.ProviderTools)
	}
}
