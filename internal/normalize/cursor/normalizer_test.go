package cursor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func stubFingerprint([]byte) string { return "fixture" }

func TestNormalizeGolden_PrintJSON(t *testing.T) {
	input := readFixture(t, "cursor-agent-2026.05.16-0338208-print-result.json")
	first, err := Normalize(input, stubFingerprint)
	if err != nil {
		t.Fatalf("first normalisation: %v", err)
	}
	second, err := Normalize(input, stubFingerprint)
	if err != nil {
		t.Fatalf("second normalisation: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("normalisation must be deterministic")
	}

	if updateGolden() {
		writeGolden(t, "cursor-agent-2026.05.16-0338208-print-result.events.json", first)
	}
	assertMatchesGolden(t, "cursor-agent-2026.05.16-0338208-print-result.events.json", first)
}

func TestNormalizeGolden_StreamJSON(t *testing.T) {
	input := readFixture(t, "cursor-agent-2026.09.02-c22c1a3-stream-result-with-model.json")
	first, err := Normalize(input, stubFingerprint)
	if err != nil {
		t.Fatalf("first normalisation: %v", err)
	}
	second, err := Normalize(input, stubFingerprint)
	if err != nil {
		t.Fatalf("second normalisation: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("normalisation must be deterministic")
	}

	if updateGolden() {
		writeGolden(t, "cursor-agent-2026.09.02-c22c1a3-stream-result-with-model.events.json", first)
	}
	assertMatchesGolden(t, "cursor-agent-2026.09.02-c22c1a3-stream-result-with-model.events.json", first)
}

func TestNormalizeCapabilityProbeYieldsNoEvents(t *testing.T) {
	events, err := Normalize(readFixture(t, "cursor-agent-2026.05.16-0338208-capability-probe.json"), stubFingerprint)
	if err != nil {
		t.Fatalf("normalise probe: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("capability probe must not fabricate events, got %d", len(events))
	}
}

func TestNormalizeFingerprintsIDsAndDoesNotLeakRawIDs(t *testing.T) {
	events, err := Normalize(readFixture(t, "cursor-agent-2026.05.16-0338208-print-result.json"), stubFingerprint)
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	event := events[0]
	if event.SessionID != "cursor-agent:fixture" {
		t.Fatalf("session id = %q, want fingerprint", event.SessionID)
	}
	serialized, _ := json.Marshal(events)
	for _, leaked := range []string{"synthetic-session-id", "synthetic-request-id"} {
		if strings.Contains(string(serialized), leaked) {
			t.Fatalf("raw identifier leaked into canonical output: %q", leaked)
		}
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

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	return readFile(t, filepath.Join(repositoryRoot(t), "fixtures", "cursor", "observed-sanitised", name))
}

func expectedDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(repositoryRoot(t), "fixtures", "cursor", "expected")
}

func readGolden(t *testing.T, name string) []byte {
	t.Helper()
	return readFile(t, filepath.Join(expectedDir(t), name))
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
