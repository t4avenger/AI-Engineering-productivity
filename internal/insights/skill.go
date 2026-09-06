package insights

import (
	"fmt"
	"sort"
	"strings"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

const SkillUsageSchemaVersion = "0.1.0"

// SkillUsage reports which skills were invoked, how often, and their outcome —
// only where a provider explicitly stamps skill identity. Detection coverage is
// reported per (provider, tool) surface so a provider that does not expose skill
// identity is listed with its honest detection state instead of a silent zero.
type SkillUsage struct {
	SchemaVersion string          `json:"schema_version"`
	Totals        SkillTotals     `json:"totals"`
	Skills        []SkillRecord   `json:"skills"`
	Coverage      []SkillCoverage `json:"coverage"`
	Notes         []string        `json:"notes"`
}

// SkillTotals summarises the insight. ObservedSkills counts distinct explicit
// skills with at least one invocation and Invocations counts explicit skill
// invocations. The detection counts each count one entry per (provider, tool)
// surface, classified by the strongest detection state observed on that surface.
type SkillTotals struct {
	ObservedSkills       int `json:"observed_skills"`
	Invocations          int `json:"invocations"`
	ExplicitDetection    int `json:"explicit_detection"`
	InferredDetection    int `json:"inferred_detection"`
	UnavailableDetection int `json:"unavailable_detection"`
	UnknownDetection     int `json:"unknown_detection"`
}

// SkillRecord is one explicitly-identified skill on one (provider, tool) surface.
// Records are produced only from provider-stamped explicit skill identity; the
// outcome breakdown is populated only from the skill payload's own outcome field,
// never borrowed from session- or task-level outcome attributes.
type SkillRecord struct {
	SkillName       string         `json:"skill_name"`
	Provider        string         `json:"provider"`
	Tool            string         `json:"tool"`
	DetectionState  string         `json:"detection_state"`
	InvocationCount int            `json:"invocation_count"`
	Outcomes        map[string]int `json:"outcomes"`
	OutcomeState    string         `json:"outcome_state"`
}

// SkillCoverage records the strongest skill-detection state observed for one
// (provider, tool) surface: explicit and inferred are provider-stamped signals,
// unavailable is a provider that explicitly stamps no skill signal, and unknown
// is a surface whose events carry no detection metadata at all.
type SkillCoverage struct {
	Provider       string `json:"provider"`
	Tool           string `json:"tool"`
	DetectionState string `json:"detection_state"`
}

// detectionRank orders detection states from strongest evidence to weakest so a
// surface is classified by the best signal any of its events carried.
var detectionRank = map[string]int{
	"explicit":    4,
	"inferred":    3,
	"unavailable": 2,
	"unknown":     1,
}

type skillKey struct {
	provider string
	tool     string
	name     string
}

type skillAccumulator struct {
	count    int
	outcomes map[string]int
}

// SkillUsageFromEvents derives the skill usage insight from sanitised canonical
// events. Skill records are emitted only for provider-stamped explicit skill
// identity; every (provider, tool) surface is reported in coverage with its
// honest detection state so unavailable and unknown providers are visible rather
// than silently absent.
func SkillUsageFromEvents(events []canonical.Event) SkillUsage {
	coverage := map[string]SkillCoverage{}
	skills := map[skillKey]*skillAccumulator{}

	for _, event := range events {
		state := detectionState(event)
		surfaceKey := event.Provider + "\x00" + event.Tool
		if existing, ok := coverage[surfaceKey]; !ok || detectionRank[state] > detectionRank[existing.DetectionState] {
			coverage[surfaceKey] = SkillCoverage{Provider: event.Provider, Tool: event.Tool, DetectionState: state}
		}
		if state != "explicit" {
			continue
		}
		name, outcome, ok := explicitSkill(event)
		if !ok {
			continue
		}
		key := skillKey{provider: event.Provider, tool: event.Tool, name: name}
		acc, exists := skills[key]
		if !exists {
			acc = &skillAccumulator{outcomes: map[string]int{}}
			skills[key] = acc
		}
		acc.count++
		if outcome != "" {
			acc.outcomes[outcome]++
		}
	}

	result := SkillUsage{
		SchemaVersion: SkillUsageSchemaVersion,
		Skills:        skillRecords(skills),
		Coverage:      sortedCoverage(coverage),
		Notes: []string{
			"Skill identity is reported only where the provider explicitly stamps it; inferred and unavailable states are shown, never guessed.",
			"Skill outcomes are counted only from the skill's own reported outcome, never borrowed from session- or task-level outcomes.",
		},
	}
	result.Totals = skillTotals(result.Skills, result.Coverage)
	return result
}

func skillRecords(skills map[skillKey]*skillAccumulator) []SkillRecord {
	records := make([]SkillRecord, 0, len(skills))
	for key, acc := range skills {
		outcomeState := "unavailable"
		if len(acc.outcomes) > 0 {
			outcomeState = "observed"
		}
		records = append(records, SkillRecord{
			SkillName:       key.name,
			Provider:        key.provider,
			Tool:            key.tool,
			DetectionState:  "explicit",
			InvocationCount: acc.count,
			Outcomes:        acc.outcomes,
			OutcomeState:    outcomeState,
		})
	}
	sort.Slice(records, func(i, j int) bool {
		return recordSortKey(records[i]) < recordSortKey(records[j])
	})
	return records
}

func recordSortKey(record SkillRecord) string {
	return fmt.Sprintf("%s\x00%s\x00%s", record.Provider, record.Tool, record.SkillName)
}

func sortedCoverage(coverage map[string]SkillCoverage) []SkillCoverage {
	rows := make([]SkillCoverage, 0, len(coverage))
	for _, row := range coverage {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		left, right := rows[i], rows[j]
		return left.Provider+"\x00"+left.Tool < right.Provider+"\x00"+right.Tool
	})
	return rows
}

func skillTotals(records []SkillRecord, coverage []SkillCoverage) SkillTotals {
	totals := SkillTotals{ObservedSkills: len(records)}
	for _, record := range records {
		totals.Invocations += record.InvocationCount
	}
	for _, row := range coverage {
		switch row.DetectionState {
		case "explicit":
			totals.ExplicitDetection++
		case "inferred":
			totals.InferredDetection++
		case "unavailable":
			totals.UnavailableDetection++
		default:
			totals.UnknownDetection++
		}
	}
	return totals
}

// detectionState reads the provider-stamped skill detection state. Absent or
// unrecognised metadata is reported as unknown, which is distinct from a provider
// explicitly stamping unavailable.
func detectionState(event canonical.Event) string {
	if value, ok := event.ProviderExtensions["skill_detection"].(string); ok {
		switch trimmed := strings.TrimSpace(value); trimmed {
		case "explicit", "inferred", "unavailable":
			return trimmed
		}
	}
	return "unknown"
}

// explicitSkill returns the skill name and its own reported outcome from a
// provider-stamped explicit skill payload. It returns false when no named skill
// payload is present, so an explicit surface with no skill name yields coverage
// but no fabricated record.
func explicitSkill(event canonical.Event) (name, outcome string, ok bool) {
	payload, isMap := event.ProviderExtensions["skill"].(map[string]any)
	if !isMap {
		return "", "", false
	}
	name = stringValue(payload, "name", "")
	if name == "" {
		return "", "", false
	}
	return name, stringValue(payload, "outcome", ""), true
}
