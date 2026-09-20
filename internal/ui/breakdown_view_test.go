package ui_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestSessionRightRailShowsBreakdownGovernanceAndLegend(t *testing.T) {
	base := time.Date(2026, 9, 20, 15, 0, 0, 0, time.UTC)
	repo := &fullStub{
		sessions: []canonical.Session{syntheticSession("breakdown-session", base)},
		events: map[string][]canonical.Event{
			"breakdown-session": {
				uiTypedSpan("llm", "breakdown-session", "trace", "llm", "llm_request", base, base.Add(4*time.Second)),
				uiTypedSpan("tool", "breakdown-session", "trace", "tool", "tool", base.Add(2*time.Second), base.Add(6*time.Second)),
			},
		},
	}
	body := renderSessionDetail(t, repo, nil, "breakdown-session")
	assertContainsAll(t, body, []string{
		`aria-label="Session summary"`,
		`id="session-breakdown-heading"`,
		"Session Breakdown",
		"Model generation",
		"Tool calls",
		"Overlap",
		`id="session-governance-heading"`,
		"Risky access",
		"Unapproved MCP",
		"Indeterminate",
		`id="session-legend-heading"`,
		"Event Legend",
		"Evidence",
		"conic-gradient(",
		"breakdown-donut",
	})
	if strings.Contains(body, "Zgotmplz") {
		t.Fatalf("html/template sanitised the donut CSS: %q", body)
	}
	if strings.Count(body, `id="session-governance-heading"`) != 1 {
		t.Fatalf("governance heading duplicated: %q", body)
	}
	mainStart := strings.Index(body, `id="main-content"`)
	railStart := strings.Index(body, `aria-label="Session summary"`)
	if mainStart == -1 || railStart == -1 || railStart < mainStart {
		t.Fatalf("unexpected layout markers main=%d rail=%d", mainStart, railStart)
	}
	main := body[mainStart:railStart]
	if strings.Contains(main, `id="session-governance-heading"`) {
		t.Fatalf("governance checklist remained in main column: %q", main)
	}
}

func TestSessionRightRailUnavailableWithoutClassifiedIntervals(t *testing.T) {
	base := time.Date(2026, 9, 20, 15, 0, 0, 0, time.UTC)
	repo := &fullStub{
		sessions: []canonical.Session{syntheticSession("empty-breakdown-session", base)},
		events:   map[string][]canonical.Event{"empty-breakdown-session": {}},
	}
	body := renderSessionDetail(t, repo, nil, "empty-breakdown-session")
	assertContainsAll(t, body, []string{
		"Session Breakdown",
		"Duration breakdown unavailable",
		"Governance",
		"Indeterminate",
	})
	if strings.Contains(body, "breakdown-donut") {
		t.Fatalf("unavailable breakdown rendered a donut: %q", body)
	}
}

func uiTypedSpan(eventID, sessionID, traceID, spanID, spanType string, start, end time.Time) canonical.Event {
	envelope := map[string]any{
		"trace_id": traceID, "span_id": spanID, "name": "claude_code." + spanType,
		"start_unix_nano": strconv.FormatInt(start.UnixNano(), 10),
		"end_unix_nano":   strconv.FormatInt(end.UnixNano(), 10),
		"status_code":     int64(0),
	}
	return canonical.Event{
		SchemaVersion: "0.1.0", EventID: eventID, EventType: "claude_code." + spanType,
		OccurredAt: start, ReceivedAt: start, Provider: "anthropic", Tool: "claude-code",
		SourceSchema: "otel", SourceVersion: "2.1.268", ActorID: "unavailable", DeviceID: "unavailable",
		SessionID: sessionID, PrivacyLevel: "operational",
		Attributes:         map[string]any{"span_type": spanType},
		ProviderExtensions: map[string]any{"span": envelope},
	}
}
