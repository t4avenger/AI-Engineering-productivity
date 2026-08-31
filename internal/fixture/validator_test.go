package fixture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wayne/telemetryiq/internal/privacy"
)

func TestValidateAcceptsAllReviewedProviderFixtures(t *testing.T) {
	for _, path := range providerFixturePaths(t) {
		t.Run(path, func(t *testing.T) {
			data := readFixtureFile(t, path)
			if err := Validate(data); err != nil {
				t.Fatalf("validate fixture: %v", err)
			}
		})
	}
}

func TestProviderFixturesPassSanitizerWithoutSensitiveSurvivors(t *testing.T) {
	sanitizer, err := privacy.New(make([]byte, 32))
	if err != nil {
		t.Fatalf("create sanitizer: %v", err)
	}

	for _, path := range providerFixturePaths(t) {
		t.Run(path, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(readFixtureFile(t, path), &document); err != nil {
				t.Fatalf("decode fixture: %v", err)
			}
			result := sanitizer.Sanitize(document)
			encoded, err := json.Marshal(result.Value)
			if err != nil {
				t.Fatalf("marshal sanitized fixture: %v", err)
			}
			assertNoSensitiveSurvivors(t, string(encoded))
		})
	}
}

func TestValidateRejectsLikelySecretsAndProhibitedFields(t *testing.T) {
	for _, test := range []struct{ name, payload, want, secret string }{
		{name: "prompt", payload: `{"prompt":"synthetic"}`, want: "payload.prompt", secret: "synthetic"},
		{name: "bearer", payload: `{"note":"Bearer token-value-that-must-never-be-committed"}`, want: "payload.note", secret: "token-value-that-must-never-be-committed"},
		{name: "dash key", payload: `{"note":"` + "sk-" + strings.Repeat("x", 20) + `"}`, want: "payload.note", secret: "sk-" + strings.Repeat("x", 20)},
		{name: "entropy", payload: `{"note":"q1w2E3r4T5y6U7i8O9p0AaBbCcDdEeFf"}`, want: "payload.note", secret: "q1w2E3r4T5y6U7i8O9p0AaBbCcDdEeFf"},
		{name: "path", payload: `{"file_path":"/tmp/project/.env"}`, want: "payload.file_path", secret: "/tmp/project/.env"},
		{name: "command", payload: `{"command_arguments":"cat .env"}`, want: "payload.command_arguments", secret: "cat .env"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := Validate([]byte(fixtureWithPayload("codex", test.payload)))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected error for %q, got %v", test.want, err)
			}
			if strings.Contains(err.Error(), test.secret) {
				t.Fatalf("validation error must not expose rejected value: %v", err)
			}
		})
	}
}

func TestValidateRequiresOriginAndToolVersion(t *testing.T) {
	err := Validate([]byte(`{"fixture_version":1,"fixture_origin":"synthetic","provider":"openai","tool":"codex","captured_at":"2026-07-26T10:00:00Z","sanitisation_reviewed":true,"payload":{}}`))
	if err == nil || !strings.Contains(err.Error(), "tool_version") {
		t.Fatalf("expected missing tool version error, got %v", err)
	}
}

func TestValidateRejectsUnsupportedTools(t *testing.T) {
	err := Validate([]byte(fixtureWithPayload("unknown-tool", `{}`)))
	if err == nil || !strings.Contains(err.Error(), "codex, claude-code, or cursor-agent") {
		t.Fatalf("expected unsupported tool error, got %v", err)
	}
}

func providerFixturePaths(t *testing.T) []string {
	t.Helper()
	root := repositoryRoot(t)
	patterns := []string{
		filepath.Join(root, "fixtures", "codex", "synthetic", "*.json"),
		filepath.Join(root, "fixtures", "codex", "observed-sanitised", "*.json"),
		filepath.Join(root, "fixtures", "claude", "observed-sanitised", "*.json"),
		filepath.Join(root, "fixtures", "cursor", "observed-sanitised", "*.json"),
	}
	var paths []string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatalf("glob fixtures: %v", err)
		}
		paths = append(paths, matches...)
	}
	if len(paths) == 0 {
		t.Fatal("expected provider fixtures")
	}
	return paths
}

func readFixtureFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func assertNoSensitiveSurvivors(t *testing.T, encoded string) {
	t.Helper()
	for _, forbidden := range []string{
		"token-value-that-must-never-be-committed",
		"q1w2E3r4T5y6U7i8O9p0AaBbCcDdEeFf",
		"/tmp/project/.env",
		"cat .env",
		"prompt",
		"response",
		"source_code",
		"file_path",
		"command_arguments",
	} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("sanitized fixture contains forbidden value or field %q", forbidden)
		}
	}
}

func fixtureWithPayload(tool, payload string) string {
	return `{"fixture_version":1,"fixture_origin":"synthetic","provider":"openai","tool":"` + tool + `","tool_version":"synthetic-0.0.0","captured_at":"2026-07-26T10:00:00Z","sanitisation_reviewed":true,"payload":` + payload + `}`
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	return filepath.Clean(filepath.Join(workingDirectory, "..", ".."))
}
