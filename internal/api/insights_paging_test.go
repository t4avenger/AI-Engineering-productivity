package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestMCPInventoryInsightPaginatesSessionEvents(t *testing.T) {
	repo := sessionTestRepository(t)
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	events := make([]canonical.Event, 0, insightEventLimit+3)
	for index := range insightEventLimit + 1 {
		events = append(events, canonical.Event{
			SchemaVersion:      "0.1.0",
			EventID:            fmt.Sprintf("mcp-filler-%04d", index),
			EventType:          "api_request",
			OccurredAt:         start.Add(time.Duration(index) * time.Second),
			ReceivedAt:         start,
			Provider:           "anthropic",
			Tool:               "claude-code",
			SourceSchema:       "otel",
			SourceVersion:      "test",
			ActorID:            "unavailable",
			DeviceID:           "unavailable",
			SessionID:          "paged-mcp-session",
			PrivacyLevel:       "operational",
			Attributes:         map[string]any{},
			ProviderExtensions: map[string]any{},
		})
	}
	connection := sessionTestEvent(t, "paged-mcp-connected", "paged-mcp-session", "claude-code", "active", "2026-01-05T09:20:00Z", "")
	connection.Provider = "anthropic"
	connection.EventType = "mcp_server_connection"
	connection.ProviderExtensions = map[string]any{"event": map[string]any{"server_name": "paged", "status": "connected"}}
	use := sessionTestEvent(t, "paged-mcp-used", "paged-mcp-session", "claude-code", "active", "2026-01-05T09:20:01Z", "")
	use.Provider = "anthropic"
	use.EventType = "mcp_call"
	use.ProviderExtensions = map[string]any{"mcp_call": map[string]any{"server_name": "paged"}}
	events = append(events, connection, use)
	if err := repo.SaveEvents(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(slog.Default(), repo))
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/api/v1/insights/mcp-inventory")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var body mcpInventoryResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Totals.ConnectedServers != 1 || body.Data.Totals.UsedServers != 1 {
		t.Fatalf("paged mcp insight totals = %#v", body.Data.Totals)
	}
}
