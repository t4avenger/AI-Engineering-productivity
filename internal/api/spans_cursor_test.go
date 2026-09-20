package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestSessionSpansAPIRejectsStaleCursor(t *testing.T) {
	repo := sessionTestRepository(t)
	if err := repo.SaveEvents(context.Background(), []canonical.Event{apiSpanEvent("span", "session", "trace", "span", "", "1000000000", "2000000000")}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(slog.Default(), repo))
	t.Cleanup(server.Close)
	cursor := encodeEventCursor("1970-01-01T00:00:00Z", "trace\x00deleted")
	response, err := http.Get(server.URL + "/api/v1/sessions/session/spans?cursor=" + url.QueryEscape(*cursor))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
}
