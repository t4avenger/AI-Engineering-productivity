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

type rolloutFixture struct {
	Payload struct {
		Records []json.RawMessage `json:"rollout_records"`
	} `json:"payload"`
}

type rolloutGoldenProjection struct {
	EventCount    int      `json:"event_count"`
	SessionID     string   `json:"session_id"`
	SourceVersion string   `json:"source_version"`
	EventTypes    []string `json:"event_types"`
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
	var fixture rolloutFixture
	raw, err := os.ReadFile(filepath.Join(codexFixturesDir(t), "observed-sanitised", "codex-0.157.1-rollout-synchronised.json"))
	if err != nil {
		t.Fatalf("read rollout fixture: %v", err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode rollout fixture: %v", err)
	}
	var data bytes.Buffer
	for _, record := range fixture.Payload.Records {
		if err := json.Compact(&data, record); err != nil {
			t.Fatalf("compact rollout record: %v", err)
		}
		data.WriteByte('\n')
	}
	return data.Bytes()
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
	usageRecord := events[8].ProviderExtensions["rollout"].(map[string]any)["record"].(map[string]any)
	usage := usageRecord["payload"].(map[string]any)
	if got := usage["input_tokens"].(json.Number).String(); got != "9007199254740993" {
		t.Fatalf("large number = %q", got)
	}
}

func eventIDs(events []canonical.Event) []string {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.EventID)
	}
	return ids
}
