package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportCodexRollout(t *testing.T) {
	const body = `{"timestamp":"2026-09-29T07:01:40Z","type":"session_meta","payload":{"id":"synthetic"}}`
	received := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/codex/rollout" || r.Header.Get("Content-Type") != "application/x-ndjson" {
			t.Errorf("request path/content type = %q/%q", r.URL.Path, r.Header.Get("Content-Type"))
		}
		if got := r.Header.Get("Authorization"); got != "Bearer synthetic-token" {
			t.Errorf("Authorization = %q", got)
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		if string(data) != body {
			t.Errorf("body = %q", data)
		}
		received <- struct{}{}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)
	t.Setenv("TELEMETRYIQ_AUTH_TOKEN", "synthetic-token")
	file := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := importCodexRollout([]string{"--file", file, "--endpoint", server.URL}); err != nil {
		t.Fatalf("importCodexRollout() error = %v", err)
	}
	<-received
}

func TestLocalRolloutURLRejectsNonLoopback(t *testing.T) {
	for _, endpoint := range []string{
		"https://example.com:8080", "http://0.0.0.0:8080", "http://127.0.0.1", "ftp://127.0.0.1:8080", "http://127.0.0.1:8080/path",
	} {
		t.Run(endpoint, func(t *testing.T) {
			if _, err := localRolloutURL(endpoint); err == nil {
				t.Fatal("localRolloutURL() error = nil")
			}
		})
	}
}

func TestImportCodexRolloutDoesNotExposePathOrTokenInErrors(t *testing.T) {
	t.Setenv("TELEMETRYIQ_AUTH_TOKEN", "sensitive-test-token")
	sensitivePath := filepath.Join(t.TempDir(), "private-rollout.jsonl")
	err := importCodexRollout([]string{"--file", sensitivePath})
	if err == nil {
		t.Fatal("importCodexRollout() error = nil")
	}
	if strings.Contains(err.Error(), sensitivePath) || strings.Contains(err.Error(), "sensitive-test-token") {
		t.Fatalf("error exposed sensitive value: %q", err)
	}
}
