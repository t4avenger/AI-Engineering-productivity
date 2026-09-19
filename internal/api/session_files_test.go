package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestSessionFilesAPIContract(t *testing.T) {
	repo := sessionTestRepository(t)
	occurred := time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC)
	events := []canonical.Event{
		{
			SchemaVersion: "0.1.0", EventID: "file-a", EventType: "claude_code.tool",
			OccurredAt: occurred, ReceivedAt: occurred, Provider: "anthropic", Tool: "claude-code",
			SourceSchema: "otel", SourceVersion: "2.1.268", ActorID: "unavailable", DeviceID: "unavailable",
			SessionID: "session-newest", PrivacyLevel: "operational",
			Attributes: map[string]any{
				"tool": map[string]any{
					"file_path": "/workspace/a.go", "tool_name": "Read", "duration_ms": int64(10),
				},
			},
			ProviderExtensions: map[string]any{},
		},
		{
			SchemaVersion: "0.1.0", EventID: "file-b", EventType: "claude_code.tool",
			OccurredAt: occurred.Add(time.Second), ReceivedAt: occurred, Provider: "anthropic", Tool: "claude-code",
			SourceSchema: "otel", SourceVersion: "2.1.268", ActorID: "unavailable", DeviceID: "unavailable",
			SessionID: "session-newest", PrivacyLevel: "operational",
			Attributes: map[string]any{
				"tool": map[string]any{
					"file_path": "/workspace/b.go", "tool_name": "Write", "duration_ms": int64(20),
				},
			},
			ProviderExtensions: map[string]any{},
		},
		{
			SchemaVersion: "0.1.0", EventID: "other-session-file", EventType: "claude_code.tool",
			OccurredAt: occurred, ReceivedAt: occurred, Provider: "anthropic", Tool: "claude-code",
			SourceSchema: "otel", SourceVersion: "2.1.268", ActorID: "unavailable", DeviceID: "unavailable",
			SessionID: "session-middle", PrivacyLevel: "operational",
			Attributes: map[string]any{
				"tool": map[string]any{"file_path": "/other/session.go", "tool_name": "Read"},
			},
			ProviderExtensions: map[string]any{},
		},
	}
	if err := repo.SaveEvents(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(slog.Default(), repo))
	t.Cleanup(server.Close)

	first := getFileList(t, server.URL+"/api/v1/sessions/session-newest/files?limit=1")
	if len(first.Data) != 1 || first.Data[0].EventID != "file-a" || first.Pagination.NextCursor == nil {
		t.Fatalf("first page = %#v", first)
	}
	if first.Data[0].Path == nil || *first.Data[0].Path != "/workspace/a.go" {
		t.Fatalf("path = %#v", first.Data[0].Path)
	}
	second := getFileList(t, server.URL+"/api/v1/sessions/session-newest/files?limit=1&cursor="+url.QueryEscape(*first.Pagination.NextCursor))
	if len(second.Data) != 1 || second.Data[0].EventID != "file-b" || second.Pagination.NextCursor != nil {
		t.Fatalf("second page = %#v", second)
	}

	empty := getFileList(t, server.URL+"/api/v1/sessions/session-oldest/files")
	if len(empty.Data) != 0 {
		t.Fatalf("empty session files = %#v", empty.Data)
	}

	missing, err := http.Get(server.URL + "/api/v1/sessions/missing/files")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = missing.Body.Close() }()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("missing status = %d", missing.StatusCode)
	}

	bad, err := http.Get(server.URL + "/api/v1/sessions/session-newest/files?cursor=not-a-cursor")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bad.Body.Close() }()
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad cursor status = %d", bad.StatusCode)
	}
}

func getFileList(t *testing.T, address string) fileListResponse {
	t.Helper()
	response, err := http.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("files status = %d", response.StatusCode)
	}
	var page fileListResponse
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	return page
}
