package insights

import (
	"strings"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

// SourceEventsFromSession returns the minimal event set required by dashboard
// insight engines for one session. Callers persist these as insight_signals so
// Home/Insights/Models/Governance never scan full event_json corpora.
// Risky-access thinning stays in the storage write path (governance import
// would cycle through insights).
func SourceEventsFromSession(events []canonical.Event) []canonical.Event {
	if len(events) == 0 {
		return nil
	}
	out := make([]canonical.Event, 0, len(events))
	coverage := map[string]string{}
	for _, event := range events {
		surface := event.Provider + "\x00" + event.Tool
		state := detectionState(event)
		if existing, ok := coverage[surface]; !ok || detectionRank[state] > detectionRank[existing] {
			coverage[surface] = state
		}
		if thin, ok := thinInsightEvent(event); ok {
			out = append(out, thin)
		}
	}
	for surface, state := range coverage {
		provider, tool, ok := strings.Cut(surface, "\x00")
		if !ok {
			continue
		}
		out = append(out, canonical.Event{
			SchemaVersion:      events[0].SchemaVersion,
			EventID:            "skill-coverage:" + events[0].SessionID + ":" + provider + ":" + tool,
			EventType:          "skill_coverage",
			OccurredAt:         events[0].OccurredAt,
			ReceivedAt:         events[0].ReceivedAt,
			Provider:           provider,
			Tool:               tool,
			SessionID:          events[0].SessionID,
			PrivacyLevel:       events[0].PrivacyLevel,
			Attributes:         map[string]any{},
			ProviderExtensions: map[string]any{"skill_detection": state},
		})
	}
	return out
}

func thinInsightEvent(event canonical.Event) (canonical.Event, bool) {
	keep := false
	thin := canonical.Event{
		SchemaVersion:      event.SchemaVersion,
		EventID:            event.EventID,
		EventType:          event.EventType,
		OccurredAt:         event.OccurredAt,
		ReceivedAt:         event.ReceivedAt,
		Provider:           event.Provider,
		Tool:               event.Tool,
		SourceSchema:       event.SourceSchema,
		SourceVersion:      event.SourceVersion,
		SessionID:          event.SessionID,
		PrivacyLevel:       event.PrivacyLevel,
		Attributes:         map[string]any{},
		ProviderExtensions: map[string]any{},
	}
	if _, _, ok := mcpConnection(event); ok {
		keep = true
		if raw, ok := event.ProviderExtensions["event"].(map[string]any); ok {
			thin.ProviderExtensions["event"] = cloneStringKeyedMap(raw)
		}
	}
	if _, ok := mcpUseEvent(event); ok {
		keep = true
		if raw, ok := event.ProviderExtensions["mcp_call"].(map[string]any); ok {
			thin.ProviderExtensions["mcp_call"] = cloneStringKeyedMap(raw)
		}
		if category, ok := event.Attributes["category"]; ok {
			thin.Attributes["category"] = category
		}
	}
	if _, ok := outcomeContract(event); ok {
		keep = true
		thin.ProviderExtensions["outcome_contract"] = event.ProviderExtensions["outcome_contract"]
	}
	if copySkillPolicyEvidence(&thin, event) {
		keep = true
	}
	if _, _, ok := tokenSignals(event); ok {
		keep = true
		copyTokenAttributes(&thin, event)
	}
	return thin, keep
}

func copySkillPolicyEvidence(thin *canonical.Event, event canonical.Event) bool {
	detection, present := event.ProviderExtensions["skill_detection"]
	if !present {
		return false
	}
	// Retain every provider-stamped detection signal. Incomplete evidence must
	// remain visible to policy evaluation so it cannot report a clean allowlist
	// decision for inferred, unknown, or unnamed explicit skills.
	thin.ProviderExtensions["skill_detection"] = detection
	if detectionState(event) == "explicit" {
		if skill, ok := event.ProviderExtensions["skill"].(map[string]any); ok {
			thin.ProviderExtensions["skill"] = cloneStringKeyedMap(skill)
		}
	}
	return true
}

func copyTokenAttributes(thin *canonical.Event, event canonical.Event) {
	copySelectedKeys(thin.Attributes, event.Attributes,
		"input_token_count", "output_token_count", "cached_input_tokens", "cached_input_token_count")
	mergeTokenEventExtension(thin, event)
	if contract, ok := event.ProviderExtensions["outcome_contract"].(map[string]any); ok {
		if _, has := thin.ProviderExtensions["outcome_contract"]; !has {
			thin.ProviderExtensions["outcome_contract"] = contract
		}
	}
}

func mergeTokenEventExtension(thin *canonical.Event, event canonical.Event) {
	raw, ok := event.ProviderExtensions["event"].(map[string]any)
	if !ok {
		return
	}
	eventCopy, _ := thin.ProviderExtensions["event"].(map[string]any)
	if eventCopy == nil {
		eventCopy = map[string]any{}
	}
	copySelectedKeys(eventCopy, raw, "input_tokens", "output_tokens", "cache_read_tokens", "cache_creation_tokens")
	if len(eventCopy) > 0 {
		thin.ProviderExtensions["event"] = eventCopy
	}
}

func copySelectedKeys(dst, src map[string]any, keys ...string) {
	for _, key := range keys {
		if value, ok := src[key]; ok {
			dst[key] = value
		}
	}
}

func cloneStringKeyedMap(src map[string]any) map[string]any {
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}
