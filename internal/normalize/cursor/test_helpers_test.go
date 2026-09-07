package cursor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func stubFingerprint([]byte) string { return "fixture" }

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
