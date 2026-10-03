package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

type rolloutGoldenProjection struct {
	EventCount    int      `json:"event_count"`
	SessionID     string   `json:"session_id"`
	SourceVersion string   `json:"source_version"`
	EventTypes    []string `json:"event_types"`
}

type rolloutLifecycleFixture struct {
	name, fixture, eventType, kind, status string
	duration                               int64
	completedAt                            time.Time
}

func TestNormalizeRolloutGolden(t *testing.T) {
	input := rolloutFixtureNDJSON(t)
	receivedAt := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	events, err := NormalizeRollout(input, receivedAt)
	if err != nil {
		t.Fatalf("NormalizeRollout() error = %v", err)
	}
	projection := projectRolloutGolden(events)
	actual, err := json.MarshalIndent(projection, "", "  ")
	if err != nil {
		t.Fatalf("marshal projection: %v", err)
	}
	expected, err := os.ReadFile(filepath.Join(codexFixturesDir(t), "expected", "codex-0.157.1-rollout.projection.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(actual), bytes.TrimSpace(expected)) {
		t.Fatalf("projection mismatch\nactual:\n%s\nexpected:\n%s", actual, expected)
	}

	assertRolloutContentAndUnknownFields(t, events)
	replayed, err := NormalizeRollout(input, receivedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("replay NormalizeRollout() error = %v", err)
	}
	if got, want := eventIDs(replayed), eventIDs(events); !reflect.DeepEqual(got, want) {
		t.Fatalf("replay event IDs = %#v, want %#v", got, want)
	}
}

func TestNormalizeRolloutLifecycleFixtures(t *testing.T) {
	for _, test := range []rolloutLifecycleFixture{
		{name: "completed", fixture: "codex-0.160.0-lifecycle-completed.jsonl", eventType: "session.completed", kind: "task_complete", status: "completed", duration: 4562, completedAt: time.Unix(1791024631, 0).UTC()},
		{name: "failed", fixture: "codex-0.160.0-lifecycle-failed.jsonl", eventType: "session.failed", kind: "task_complete", status: "failed", duration: 2476, completedAt: time.Unix(1791024774, 0).UTC()},
		{name: "cancelled", fixture: "codex-0.160.0-lifecycle-cancelled.jsonl", eventType: "session.cancelled", kind: "turn_aborted", status: "cancelled", duration: 4842, completedAt: time.Unix(1791024742, 0).UTC()},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertRolloutLifecycleFixture(t, test)
		})
	}
}

func TestRolloutLifecycleProjectsOnlyReviewedTerminalEvidence(t *testing.T) {
	for _, test := range []struct {
		name       string
		payload    map[string]any
		wantType   string
		wantStatus string
	}{
		{name: "empty error object fails", payload: map[string]any{"type": "task_complete", "error": map[string]any{}}, wantType: "session.failed", wantStatus: "failed"},
		{name: "explicit null completes", payload: map[string]any{"type": "task_complete", "error": nil}, wantType: "session.completed", wantStatus: "completed"},
		{name: "reviewed interruption cancels", payload: map[string]any{"type": "turn_aborted", "reason": "interrupted"}, wantType: "session.cancelled", wantStatus: "cancelled"},
		{name: "unknown abort stays raw", payload: map[string]any{"type": "turn_aborted", "reason": "model_error"}, wantType: "codex.rollout.event_msg"},
		{name: "missing abort reason stays raw", payload: map[string]any{"type": "turn_aborted"}, wantType: "codex.rollout.event_msg"},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := canonical.Event{EventType: "codex.rollout.event_msg", Attributes: map[string]any{}, ProviderExtensions: map[string]any{}}
			record := rolloutRecord{recordType: "event_msg", decoded: map[string]any{"payload": test.payload}}
			applyRolloutLifecycleAndGovernance(&event, record)
			status, _ := event.Attributes["lifecycle_status"].(string)
			if event.EventType != test.wantType || status != test.wantStatus {
				t.Fatalf("terminal projection = %q, %#v; want %q, %q", event.EventType, event.Attributes, test.wantType, test.wantStatus)
			}
		})
	}
}

func assertRolloutLifecycleFixture(t *testing.T, test rolloutLifecycleFixture) {
	t.Helper()
	events, err := NormalizeRollout(readRolloutFixture(t, test.fixture), time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	active := rolloutEventByType(events, "session.active")
	terminal := rolloutEventByType(events, test.eventType)
	if active == nil || terminal == nil {
		t.Fatalf("lifecycle events missing: %#v", projectRolloutGolden(events).EventTypes)
	}
	if terminal.Attributes["lifecycle_kind"] != test.kind || terminal.Attributes["lifecycle_status"] != test.status || terminal.Attributes["duration_ms"] != test.duration {
		t.Fatalf("terminal lifecycle = %#v", terminal.Attributes)
	}
	if !terminal.OccurredAt.Equal(test.completedAt) {
		t.Fatalf("terminal boundary = %s, want %s", terminal.OccurredAt, test.completedAt)
	}
	lifecycle := terminal.ProviderExtensions["session_lifecycle"].(map[string]any)
	if lifecycle["provenance"] != "observed" || lifecycle["turn_id"] == nil {
		t.Fatalf("lifecycle evidence = %#v", lifecycle)
	}
	golden := "codex-0.160.0-lifecycle-" + test.name + ".events.json"
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		writeCodexGolden(t, golden, events)
	}
	assertCodexGolden(t, golden, events)
}

func TestNormalizeRolloutProjectsGovernanceWithoutFlatteningRawPolicy(t *testing.T) {
	events, err := NormalizeRollout(readRolloutFixture(t, "codex-0.160.0-lifecycle-completed.jsonl"), time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	governance := rolloutEventByType(events, "codex.rollout.turn_context")
	if governance == nil || governance.Attributes["approval_policy"] != "never" || governance.Attributes["sandbox_policy"] != "workspace-write" {
		t.Fatalf("governance projection = %#v", governance)
	}
	state := governance.ProviderExtensions["governance_state"].(map[string]any)
	sandbox := state["sandbox_policy"].(map[string]any)
	if sandbox["network_access"] != false || state["permission_profile"] == nil || state["file_system_sandbox_policy"] == nil {
		t.Fatalf("raw governance evidence = %#v", state)
	}
}

func TestNormalizeRolloutProjectsCurrentReadOnlyGovernance(t *testing.T) {
	events, err := NormalizeRollout(readRolloutFixture(t, "codex-0.160.0-governance-read-only.jsonl"), time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	governance := rolloutEventByType(events, "codex.rollout.turn_context")
	if governance == nil || governance.Attributes["approval_policy"] != "never" || governance.Attributes["sandbox_policy"] != "read-only" {
		t.Fatalf("read-only governance projection = %#v", governance)
	}
	state := governance.ProviderExtensions["governance_state"].(map[string]any)
	if state["approvals_reviewer"] != nil || state["permission_profile"] == nil {
		t.Fatalf("selected raw governance evidence = %#v", state)
	}
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		writeCodexGolden(t, "codex-0.160.0-governance-read-only.events.json", events)
	}
	assertCodexGolden(t, "codex-0.160.0-governance-read-only.events.json", events)
}

func TestNormalizeRolloutKeepsDisabledPluginDistinctFromUse(t *testing.T) {
	data := []byte(`{"timestamp":"2026-10-03T12:00:00Z","type":"session_meta","payload":{"id":"plugin-state","cli_version":"0.160.0"}}
{"timestamp":"2026-10-03T12:00:01Z","type":"turn_context","payload":{"disabled_plugin_ids":["tiq-synthetic-plugin"]}}`)
	events, err := NormalizeRollout(data, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	state := rolloutEventByType(events, codexIntegrationStateEvent)
	if state == nil || state.Attributes["integration_kind"] != "plugin" || state.Attributes["integration_name"] != "tiq-synthetic-plugin" || state.Attributes["integration_state"] != "disabled" {
		t.Fatalf("plugin integration state = %#v", state)
	}
}

func TestNormalizeRolloutRejectsInvalidInput(t *testing.T) {
	validMeta := `{"timestamp":"2026-09-29T07:01:40Z","type":"session_meta","payload":{"id":"session","cli_version":"0.157.1"}}`
	for _, test := range []struct {
		name string
		data string
		want error
	}{
		{name: "empty", want: ErrMalformedRollout},
		{name: "malformed", data: `{`, want: ErrMalformedRollout},
		{name: "two values", data: validMeta + `{}`, want: ErrMalformedRollout},
		{name: "missing type", data: `{"timestamp":"2026-09-29T07:01:40Z","payload":{}}`, want: ErrInvalidRollout},
		{name: "missing timestamp", data: `{"type":"session_meta","payload":{"id":"session"}}`, want: ErrInvalidRollout},
		{name: "missing session meta", data: `{"timestamp":"2026-09-29T07:01:40Z","type":"event_msg","payload":{}}`, want: ErrInvalidRollout},
		{name: "missing session id", data: `{"timestamp":"2026-09-29T07:01:40Z","type":"session_meta","payload":{}}`, want: ErrInvalidRollout},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NormalizeRollout([]byte(test.data), time.Now())
			if !errors.Is(err, test.want) {
				t.Fatalf("NormalizeRollout() error = %v, want %v", err, test.want)
			}
		})
	}
}

func FuzzNormalizeRollout(f *testing.F) {
	f.Add(rolloutFixtureNDJSON(f))
	f.Add([]byte("{}\n"))
	f.Add(bytes.Repeat([]byte("\n"), 4096))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = NormalizeRollout(data, time.Unix(0, 0).UTC())
	})
}

func rolloutFixtureNDJSON(t testing.TB) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(codexFixturesDir(t), "observed-sanitised", "codex-0.157.1-rollout-synchronised.jsonl"))
	if err != nil {
		t.Fatalf("read rollout fixture: %v", err)
	}
	return raw
}

func readRolloutFixture(t testing.TB, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(codexFixturesDir(t), "observed-sanitised", name))
	if err != nil {
		t.Fatalf("read rollout fixture: %v", err)
	}
	return raw
}

func projectRolloutGolden(events []canonical.Event) rolloutGoldenProjection {
	projection := rolloutGoldenProjection{EventCount: len(events)}
	if len(events) == 0 {
		return projection
	}
	projection.SessionID = events[0].SessionID
	projection.SourceVersion = events[0].SourceVersion
	for _, event := range events {
		projection.EventTypes = append(projection.EventTypes, event.EventType)
	}
	return projection
}

func assertRolloutContentAndUnknownFields(t *testing.T, events []canonical.Event) {
	t.Helper()
	var user, assistant, future *canonical.Event
	for index := range events {
		assertRolloutEnvelope(t, events[index])
		switch events[index].EventType {
		case "user_prompt":
			user = &events[index]
		case "assistant_response":
			assistant = &events[index]
		case "codex.rollout.future_provider_record":
			future = &events[index]
		}
	}
	if user == nil || assistant == nil || future == nil {
		t.Fatalf("missing projected events: user=%v assistant=%v future=%v", user != nil, assistant != nil, future != nil)
	}
	if got := user.ProviderExtensions["event"].(map[string]any)["prompt"]; got != "append synthetic rollout evidence" {
		t.Fatalf("user prompt = %#v", got)
	}
	if got := assistant.ProviderExtensions["event"].(map[string]any)["response"]; got != "I will update the synthetic file." {
		t.Fatalf("assistant response = %#v", got)
	}
	record := future.ProviderExtensions["rollout"].(map[string]any)["record"].(map[string]any)
	payload := record["payload"].(map[string]any)
	if payload["unknown_flag"] != true || payload["future_shape"] == nil {
		t.Fatalf("unknown provider fields were not retained: %#v", payload)
	}
	tokenUsage := rolloutEventByType(events, "codex.rollout.token_usage_record")
	if tokenUsage == nil {
		t.Fatal("missing token usage rollout record")
	}
	usageRecord := tokenUsage.ProviderExtensions["rollout"].(map[string]any)["record"].(map[string]any)
	usage := usageRecord["payload"].(map[string]any)
	if got := usage["input_tokens"].(json.Number).String(); got != "9007199254740993" {
		t.Fatalf("large number = %q", got)
	}
}

func assertRolloutEnvelope(t *testing.T, event canonical.Event) {
	t.Helper()
	if event.ActorID != "codex:synthetic-user" {
		t.Fatalf("actor id = %q, want namespaced provider identity", event.ActorID)
	}
	if event.PrivacyLevel != "governed-content" {
		t.Fatalf("privacy level = %q, want governed-content", event.PrivacyLevel)
	}
}

func rolloutEventByType(events []canonical.Event, eventType string) *canonical.Event {
	for index := range events {
		if events[index].EventType == eventType {
			return &events[index]
		}
	}
	return nil
}

func eventIDs(events []canonical.Event) []string {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.EventID)
	}
	return ids
}
