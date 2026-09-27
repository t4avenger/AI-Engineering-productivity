package insights

import (
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestSourceEventsFromSessionKeepsMCPIdentityWithTokenFields(t *testing.T) {
	now := time.Now().UTC()
	events := []canonical.Event{{
		EventID: "mcp-connection", EventType: "mcp_server_connection", SessionID: "s1",
		OccurredAt: now, ReceivedAt: now, Provider: "anthropic", Tool: "claude-code",
		ProviderExtensions: map[string]any{
			"event": map[string]any{
				"server_name":       "tiq-mcp",
				"status":            "connected",
				"input_tokens":      int64(1200),
				"output_tokens":     int64(50),
				"cache_read_tokens": int64(900),
			},
		},
	}}
	thin := SourceEventsFromSession(events)
	inventory := MCPInventoryFromEvents(thin)
	if inventory.Totals.ConnectedServers != 1 || len(inventory.Servers) != 1 {
		t.Fatalf("inventory = %#v", inventory)
	}
	if inventory.Servers[0].ServerName != "tiq-mcp" {
		t.Fatalf("server name = %q, want tiq-mcp", inventory.Servers[0].ServerName)
	}
	if inventory.Servers[0].IdentityState != "provider_reported" {
		t.Fatalf("identity state = %q, want provider_reported", inventory.Servers[0].IdentityState)
	}
}

func TestSourceEventsFromSessionRetainsIncompleteSkillPolicyEvidence(t *testing.T) {
	now := time.Now().UTC()
	events := []canonical.Event{
		{
			EventID: "inferred", EventType: "skill_invocation", SessionID: "s1",
			OccurredAt: now, ReceivedAt: now, Provider: "anthropic", Tool: "claude-code",
			ProviderExtensions: map[string]any{"skill_detection": "inferred"},
		},
		{
			EventID: "unnamed-explicit", EventType: "skill_invocation", SessionID: "s1",
			OccurredAt: now, ReceivedAt: now, Provider: "anthropic", Tool: "claude-code",
			ProviderExtensions: map[string]any{"skill_detection": "explicit", "skill": map[string]any{"name": ""}},
		},
	}

	thin := SourceEventsFromSession(events)
	if len(thin) != 3 {
		t.Fatalf("retained events = %#v", thin)
	}
	for _, event := range thin[:2] {
		if event.ProviderExtensions["skill_detection"] == nil {
			t.Fatalf("incomplete skill signal was dropped: %#v", event)
		}
	}
	if thin[1].ProviderExtensions["skill"].(map[string]any)["name"] != "" {
		t.Fatalf("explicit skill evidence = %#v", thin[1].ProviderExtensions)
	}
}

func TestSourceEventsFromSessionRetainsUserPromptAndDropsAssistantText(t *testing.T) {
	now := time.Now().UTC()
	events := []canonical.Event{
		{
			EventID: "prompt", EventType: "user_prompt", SessionID: "s1",
			OccurredAt: now, ReceivedAt: now, Provider: "anthropic", Tool: "claude-code",
			ProviderExtensions: map[string]any{"event": map[string]any{"prompt": "synthetic retained user", "unrelated": "drop-me"}},
		},
		{
			EventID: "reply", EventType: "assistant_response", SessionID: "s1",
			OccurredAt: now, ReceivedAt: now, Provider: "anthropic", Tool: "claude-code",
			ProviderExtensions: map[string]any{"event": map[string]any{"response": "synthetic assistant"}},
		},
	}
	thin := SourceEventsFromSession(events)
	var prompt *canonical.Event
	for i := range thin {
		if thin[i].EventType == "user_prompt" {
			prompt = &thin[i]
		}
		if thin[i].EventType == "assistant_response" {
			t.Fatalf("assistant text was copied into insight signals: %#v", thin[i])
		}
	}
	if prompt == nil {
		t.Fatal("user prompt signal missing")
	}
	echo := prompt.ProviderExtensions["event"].(map[string]any)
	if echo["prompt"] != "synthetic retained user" {
		t.Fatalf("prompt signal = %#v", echo)
	}
	if _, present := echo["unrelated"]; present {
		t.Fatalf("unrelated prompt field retained: %#v", echo)
	}
}
