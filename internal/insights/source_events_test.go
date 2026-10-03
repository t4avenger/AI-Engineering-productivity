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

func TestSourceEventsFromSessionRetainsOnlyGovernanceStateEvidence(t *testing.T) {
	now := time.Now().UTC()
	event := canonical.Event{
		EventID: "governance", EventType: "codex.auth_recovery", SessionID: "s1",
		OccurredAt: now, ReceivedAt: now, Provider: "openai", Tool: "codex",
		Attributes: map[string]any{
			"auth_mode": "managed", "lifecycle_kind": "auth_recovery", "lifecycle_phase": "reload",
			"lifecycle_status": "recovery_not_run", "unrelated": "drop-me",
		},
		ProviderExtensions: map[string]any{
			"session_lifecycle": map[string]any{"kind": "auth_recovery", "provenance": "observed"},
			"raw_log_record":    map[string]any{"body": "drop-me"},
		},
	}
	unrelatedLifecycle := event
	unrelatedLifecycle.EventID = "session-active"
	unrelatedLifecycle.EventType = "session.active"
	unrelatedLifecycle.Attributes = map[string]any{"lifecycle_kind": "session_start", "lifecycle_status": "active"}
	unrelatedLifecycle.ProviderExtensions = map[string]any{"session_lifecycle": map[string]any{"kind": "session_start"}}
	thin := SourceEventsFromSession([]canonical.Event{event, unrelatedLifecycle})
	if len(thin) != 2 {
		t.Fatalf("retained events = %#v", thin)
	}
	if thin[0].Attributes["auth_mode"] != "managed" || thin[0].Attributes["lifecycle_status"] != "recovery_not_run" {
		t.Fatalf("governance attributes = %#v", thin[0].Attributes)
	}
	if _, present := thin[0].Attributes["unrelated"]; present || thin[0].ProviderExtensions["raw_log_record"] != nil {
		t.Fatalf("unrelated evidence retained = %#v", thin[0])
	}
	if thin[0].ProviderExtensions["session_lifecycle"].(map[string]any)["provenance"] != "observed" {
		t.Fatalf("lifecycle provenance = %#v", thin[0].ProviderExtensions)
	}
}
