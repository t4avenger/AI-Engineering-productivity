package insights

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

func TestSkillUsageFromEventsAggregatesExplicitSkills(t *testing.T) {
	events := []canonical.Event{
		testSkillEvent("a", map[string]any{"skill_detection": "explicit", "skill": map[string]any{"name": "pdf", "outcome": "success"}}, nil),
		testSkillEvent("b", map[string]any{"skill_detection": "explicit", "skill": map[string]any{"name": "pdf", "outcome": "failed"}}, nil),
		testSkillEvent("c", map[string]any{"skill_detection": "explicit", "skill": map[string]any{"name": "pdf", "outcome": "success"}}, nil),
	}

	usage := SkillUsageFromEvents(events)

	if usage.Totals.ObservedSkills != 1 || usage.Totals.Invocations != 3 || usage.Totals.ExplicitDetection != 1 {
		t.Fatalf("totals = %#v", usage.Totals)
	}
	if len(usage.Skills) != 1 {
		t.Fatalf("skills = %#v", usage.Skills)
	}
	skill := usage.Skills[0]
	if skill.SkillName != "pdf" || skill.InvocationCount != 3 || skill.DetectionState != "explicit" {
		t.Fatalf("skill = %#v", skill)
	}
	if skill.OutcomeState != "observed" || skill.Outcomes["success"] != 2 || skill.Outcomes["failed"] != 1 {
		t.Fatalf("outcomes = %#v (state %q)", skill.Outcomes, skill.OutcomeState)
	}
}

func TestSkillUsageMarksUnavailableProviderInsteadOfEmptyZero(t *testing.T) {
	// Claude Code stamps skill_detection=unavailable on every event today.
	usage := SkillUsageFromEvents([]canonical.Event{
		testSkillEvent("a", map[string]any{"skill_detection": "unavailable"}, nil),
		testSkillEvent("b", map[string]any{"skill_detection": "unavailable"}, nil),
	})

	if usage.Totals.ObservedSkills != 0 || usage.Totals.UnavailableDetection != 1 {
		t.Fatalf("totals = %#v", usage.Totals)
	}
	if len(usage.Skills) != 0 {
		t.Fatalf("skills should be empty, got %#v", usage.Skills)
	}
	if len(usage.Coverage) != 1 || usage.Coverage[0].DetectionState != "unavailable" {
		t.Fatalf("coverage = %#v", usage.Coverage)
	}
	if usage.Coverage[0].Provider != "anthropic" || usage.Coverage[0].Tool != "claude-code" {
		t.Fatalf("coverage surface = %#v", usage.Coverage[0])
	}
}

func TestSkillUsageTreatsMissingDetectionAsUnknownNotExplicit(t *testing.T) {
	usage := SkillUsageFromEvents([]canonical.Event{
		testSkillEvent("a", map[string]any{"skill": map[string]any{"name": "pdf"}}, nil),
	})

	if usage.Totals.UnknownDetection != 1 || usage.Totals.ExplicitDetection != 0 || usage.Totals.UnavailableDetection != 0 {
		t.Fatalf("totals = %#v", usage.Totals)
	}
	if len(usage.Skills) != 0 {
		t.Fatalf("missing detection must not fabricate a skill record: %#v", usage.Skills)
	}
	if usage.Coverage[0].DetectionState != "unknown" {
		t.Fatalf("coverage = %#v", usage.Coverage)
	}
}

func TestSkillUsageExplicitWithoutNameYieldsCoverageOnly(t *testing.T) {
	usage := SkillUsageFromEvents([]canonical.Event{
		testSkillEvent("a", map[string]any{"skill_detection": "explicit", "skill": map[string]any{"name": ""}}, nil),
	})

	if usage.Totals.ExplicitDetection != 1 || usage.Totals.ObservedSkills != 0 {
		t.Fatalf("totals = %#v", usage.Totals)
	}
	if len(usage.Skills) != 0 {
		t.Fatalf("explicit event without a skill name must not produce a record: %#v", usage.Skills)
	}
}

func TestSkillUsageDoesNotBorrowSessionOutcomeForSkill(t *testing.T) {
	// A task/session-level outcome attribute must never be attributed to the skill.
	usage := SkillUsageFromEvents([]canonical.Event{
		testSkillEvent("a", map[string]any{"skill_detection": "explicit", "skill": map[string]any{"name": "pdf"}},
			map[string]any{"task_outcome": "success", "outcome": "success"}),
	})

	skill := usage.Skills[0]
	if skill.OutcomeState != "unavailable" || len(skill.Outcomes) != 0 {
		t.Fatalf("skill outcome must stay unavailable: %#v (state %q)", skill.Outcomes, skill.OutcomeState)
	}
}

func TestSkillUsageResolvesStrongestDetectionPerSurface(t *testing.T) {
	usage := SkillUsageFromEvents([]canonical.Event{
		testSkillEvent("a", map[string]any{"skill_detection": "unavailable"}, nil),
		testSkillEvent("b", map[string]any{"skill_detection": "explicit", "skill": map[string]any{"name": "pdf"}}, nil),
	})

	if len(usage.Coverage) != 1 || usage.Coverage[0].DetectionState != "explicit" {
		t.Fatalf("coverage = %#v", usage.Coverage)
	}
	if usage.Totals.ExplicitDetection != 1 || usage.Totals.UnavailableDetection != 0 {
		t.Fatalf("totals = %#v", usage.Totals)
	}
}

func TestSkillUsageEmptyCollectionsEncodeAsArrays(t *testing.T) {
	payload, err := json.Marshal(SkillUsageFromEvents(nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"skills":[]`, `"coverage":[]`} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("empty %s missing: %s", want, payload)
		}
	}
	if strings.Contains(string(payload), `"skills":null`) || strings.Contains(string(payload), `"coverage":null`) {
		t.Fatalf("empty collections must not encode as null: %s", payload)
	}
}

func testSkillEvent(id string, extensions, attributes map[string]any) canonical.Event {
	at := time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)
	if attributes == nil {
		attributes = map[string]any{}
	}
	return canonical.Event{
		SchemaVersion:      "0.1.0",
		EventID:            id,
		EventType:          "skill_invocation",
		OccurredAt:         at,
		ReceivedAt:         at,
		Provider:           "anthropic",
		Tool:               "claude-code",
		SourceSchema:       "otel",
		SourceVersion:      "test",
		ActorID:            "unavailable",
		DeviceID:           "unavailable",
		SessionID:          "session",
		PrivacyLevel:       "operational",
		Attributes:         attributes,
		ProviderExtensions: extensions,
	}
}
