package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// stubFingerprint keeps protected identifier fingerprints deterministic for
// golden comparison. A real caller supplies the installation HMAC fingerprint.
func stubFingerprint([]byte) string { return "fixture" }

func TestNormalizeEventsGolden(t *testing.T) {
	input := readFixture(t, "claude-code-2.1.251-otlp-events.json")
	first, err := NormalizeEvents(input, stubFingerprint)
	if err != nil {
		t.Fatalf("first normalisation: %v", err)
	}
	second, err := NormalizeEvents(input, stubFingerprint)
	if err != nil {
		t.Fatalf("second normalisation: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("normalisation must be deterministic")
	}

	if updateGolden() {
		writeGolden(t, "claude-code-2.1.251-otlp-events.events.json", first)
	}
	assertMatchesGolden(t, "claude-code-2.1.251-otlp-events.events.json", first)
}

func TestNormalizeEventsKeepsNativeSessionAndOmitsFabricatedSkillUnavailable(t *testing.T) {
	events, err := NormalizeEvents(readFixture(t, "claude-code-2.1.251-otlp-events.json"), stubFingerprint)
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2", len(events))
	}
	for _, event := range events {
		if event.SessionID != "claude-code:synthetic-session-id" {
			t.Fatalf("session id = %q, want native provider ID", event.SessionID)
		}
		if _, stamped := event.ProviderExtensions["skill_detection"]; stamped {
			t.Fatalf("non-skill events must not stamp skill_detection, got %v", event.ProviderExtensions["skill_detection"])
		}
		unavailableFields, ok := event.Attributes["unavailable_fields"].([]string)
		if !ok || containsField(unavailableFields, "skill_invocations") || !containsField(unavailableFields, "tool_calls") {
			t.Fatalf("unavailable_fields = %v", event.Attributes["unavailable_fields"])
		}
		preserved := event.ProviderExtensions["event"].(map[string]any)
		if _, duplicated := preserved["session_id"]; duplicated {
			t.Fatal("session_id should be promoted to canonical identity, not duplicated in provider_extensions.event")
		}
	}
	// The connection event additionally lacks model/token identity.
	if got := events[0].EventType; got != "mcp_server_connection" {
		t.Fatalf("first event type = %q, want mcp_server_connection", got)
	}
	connectionUnavailable := events[0].Attributes["unavailable_fields"].([]string)
	if !containsField(connectionUnavailable, "model") || !containsField(connectionUnavailable, "token_usage") {
		t.Fatalf("connection event should mark model/token unavailable, got %v", connectionUnavailable)
	}
}

func TestNormalizeEventsSkillActivatedIsExplicit(t *testing.T) {
	events, err := NormalizeEvents(readFixture(t, "claude-code-2.1.263-skill-activated.json"), stubFingerprint)
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	event := events[0]
	if event.EventType != "skill_activated" {
		t.Fatalf("event_type = %q", event.EventType)
	}
	if event.ProviderExtensions["skill_detection"] != "explicit" {
		t.Fatalf("skill_detection = %v, want explicit", event.ProviderExtensions["skill_detection"])
	}
	skill, ok := event.ProviderExtensions["skill"].(map[string]any)
	if !ok || skill["name"] != "tiq-probe" {
		t.Fatalf("skill payload = %#v", event.ProviderExtensions["skill"])
	}
	if skill["invocation_trigger"] != "user-slash" || skill["source"] != "projectSettings" {
		t.Fatalf("skill extras = %#v", skill)
	}
	if updateGolden() {
		writeGolden(t, "claude-code-2.1.263-skill-activated.events.json", events)
	}
	assertMatchesGolden(t, "claude-code-2.1.263-skill-activated.events.json", events)
}

func TestNormalizeEventsCapabilityProbeYieldsNoEvents(t *testing.T) {
	events, err := NormalizeEvents(readFixture(t, "claude-code-2.1.251-capability-probe.json"), stubFingerprint)
	if err != nil {
		t.Fatalf("normalise probe: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("capability probe must not fabricate events, got %d", len(events))
	}
}

func TestNormalizeEventsRejectsUnsupportedProviderTool(t *testing.T) {
	data := readFixture(t, "claude-code-2.1.251-otlp-events.json")
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode: %v", err)
	}
	document["provider"] = "openai"
	mutated, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := NormalizeEvents(mutated, stubFingerprint); err == nil || !strings.Contains(err.Error(), "anthropic and claude-code") {
		t.Fatalf("expected provider/tool error, got %v", err)
	}
}

func TestNormalizeEventsRejectsUnsupportedSourceType(t *testing.T) {
	data := readFixture(t, "claude-code-2.1.251-otlp-events.json")
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode: %v", err)
	}
	document["payload"].(map[string]any)["source_type"] = "otlp_grpc_unreviewed"
	mutated, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := NormalizeEvents(mutated, stubFingerprint); err == nil || !strings.Contains(err.Error(), "source_type") {
		t.Fatalf("expected source_type error, got %v", err)
	}
}

func TestNormalizeEventsRequiresFingerprint(t *testing.T) {
	if _, err := NormalizeEvents(readFixture(t, "claude-code-2.1.251-otlp-events.json"), nil); err == nil {
		t.Fatal("expected error when fingerprint is nil")
	}
}

func containsField(fields []string, want string) bool {
	for _, field := range fields {
		if field == want {
			return true
		}
	}
	return false
}

// assertMatchesGolden compares the marshalled value byte-for-byte against the
// committed golden file. Comparing marshalled JSON (rather than decode +
// DeepEqual) keeps the check independent of how map[string]any slices decode
// back into Go types.
func assertMatchesGolden(t *testing.T, name string, value any) {
	t.Helper()
	got, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal value: %v", err)
	}
	want := strings.TrimSpace(string(readGolden(t, name)))
	if string(got) != want {
		t.Fatalf("%s mismatch\n got: %s\nwant: %s", name, got, want)
	}
}

func updateGolden() bool { return os.Getenv("UPDATE_GOLDEN") == "1" }

func writeGolden(t *testing.T, name string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden: %v", err)
	}
	if err := os.WriteFile(filepath.Join(expectedDir(t), name), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write golden: %v", err)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	return readFile(t, filepath.Join(repositoryRoot(t), "fixtures", "claude", "observed-sanitised", name))
}

func readGolden(t *testing.T, name string) []byte {
	t.Helper()
	return readFile(t, filepath.Join(expectedDir(t), name))
}

func expectedDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(repositoryRoot(t), "fixtures", "claude", "expected")
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return contents
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}
