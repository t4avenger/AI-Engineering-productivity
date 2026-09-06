package insights

import (
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestContextWasteThresholdsCachedRatio(t *testing.T) {
	thresholds := ContextWasteThresholds{CachedContextRatioThreshold: 0.75, InputTokenGrowthThreshold: 2.0}
	tests := []struct {
		name      string
		cached    int64
		wantTrig  bool
		wantState string
	}{
		{"below", 74, false, "observed"},
		{"at", 75, true, "observed"},
		{"above", 90, true, "observed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := []canonical.Event{
				contextWasteEvent("a", "s1", time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC), nil, map[string]any{"event": map[string]any{"input_tokens": int64(100), "cache_read_tokens": test.cached}}),
			}
			insight := ContextWasteFromEvents(events, thresholds)
			if len(insight.Sessions) != 1 {
				t.Fatalf("sessions = %#v", insight.Sessions)
			}
			row := insight.Sessions[0]
			if row.CachedContextRatioState != test.wantState {
				t.Fatalf("cached ratio state = %q", row.CachedContextRatioState)
			}
			if row.Triggered != test.wantTrig {
				t.Fatalf("triggered = %v, reasons=%#v", row.Triggered, row.TriggerReasons)
			}
		})
	}
}

func TestContextWasteThresholdsInputTokenGrowth(t *testing.T) {
	thresholds := ContextWasteThresholds{CachedContextRatioThreshold: 0.75, InputTokenGrowthThreshold: 2.0}
	tests := []struct {
		name     string
		maxInput int64
		wantTrig bool
	}{
		{"below", 199, false},
		{"at", 200, true},
		{"above", 300, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := []canonical.Event{
				contextWasteEvent("a", "s1", time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC), map[string]any{"input_token_count": "100"}, nil),
				contextWasteEvent("b", "s1", time.Date(2026, 1, 2, 9, 0, 1, 0, time.UTC), map[string]any{"input_token_count": test.maxInput}, nil),
			}
			insight := ContextWasteFromEvents(events, thresholds)
			row := insight.Sessions[0]
			if row.InputTokenGrowthState != "observed" {
				t.Fatalf("growth state = %q", row.InputTokenGrowthState)
			}
			if row.Triggered != test.wantTrig {
				t.Fatalf("triggered = %v, reasons=%#v", row.Triggered, row.TriggerReasons)
			}
		})
	}
}

func TestContextWasteUnavailableWhenNoSignals(t *testing.T) {
	thresholds := ContextWasteThresholds{CachedContextRatioThreshold: 0.75, InputTokenGrowthThreshold: 2.0}
	insight := ContextWasteFromEvents([]canonical.Event{
		// One observed input-token sample is enough to include the session, but it
		// is insufficient to compute growth and carries no cached-token data.
		contextWasteEvent("a", "s1", time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC), map[string]any{"input_token_count": "100"}, map[string]any{}),
	}, thresholds)
	if len(insight.Sessions) != 1 {
		t.Fatalf("sessions = %#v", insight.Sessions)
	}
	row := insight.Sessions[0]
	if row.CachedContextRatioState != "unavailable" || row.InputTokenGrowthState != "unavailable" {
		t.Fatalf("states = %q/%q", row.CachedContextRatioState, row.InputTokenGrowthState)
	}
	if row.Triggered {
		t.Fatal("unavailable metrics must not trigger")
	}
}

func contextWasteEvent(eventID, sessionID string, occurredAt time.Time, attrs map[string]any, extensions map[string]any) canonical.Event {
	return canonical.Event{
		SchemaVersion:      "0.1.0",
		EventID:            eventID,
		EventType:          "api_request",
		OccurredAt:         occurredAt,
		ReceivedAt:         occurredAt,
		Provider:           "anthropic",
		Tool:               "claude-code",
		SourceSchema:       "otel",
		SourceVersion:      "test",
		ActorID:            "unavailable",
		DeviceID:           "unavailable",
		SessionID:          sessionID,
		PrivacyLevel:       "operational",
		Attributes:         attrs,
		ProviderExtensions: extensions,
	}
}
