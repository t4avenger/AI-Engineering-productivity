package fixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestValidateRejectsLikelySecretsAndProhibitedFields(t *testing.T) {
	for _, test := range []struct{ name, payload, want, secret string }{
		{name: "prompt with secret value", payload: `{"prompt":"Bearer token-value-that-must-never-be-committed"}`, want: "payload.prompt", secret: "token-value-that-must-never-be-committed"},
		{name: "response with entropy value", payload: `{"response":"q1w2E3r4T5y6U7i8O9p0AaBbCcDdEeFf"}`, want: "payload.response", secret: "q1w2E3r4T5y6U7i8O9p0AaBbCcDdEeFf"},
		{name: "bearer", payload: `{"note":"Bearer token-value-that-must-never-be-committed"}`, want: "payload.note", secret: "token-value-that-must-never-be-committed"},
		{name: "dash key", payload: `{"note":"` + "sk-" + strings.Repeat("x", 20) + `"}`, want: "payload.note", secret: "sk-" + strings.Repeat("x", 20)},
		{name: "entropy", payload: `{"note":"q1w2E3r4T5y6U7i8O9p0AaBbCcDdEeFf"}`, want: "payload.note", secret: "q1w2E3r4T5y6U7i8O9p0AaBbCcDdEeFf"},
		{name: "secret value under file_path", payload: `{"file_path":"sk-` + strings.Repeat("x", 20) + `"}`, want: "payload.file_path", secret: "sk-" + strings.Repeat("x", 20)},
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

// TestValidateAcceptsPromptAndResponseFieldNames proves the epic #87 / #94
// relaxation: fields literally named prompt/prompts/response/responses are no
// longer prohibited by name, so synthetic content-present fixtures can be
// committed — while the value-based likelySecret scan still guards every string
// (proven above), so no real credential can ride under a prompt/response key.
func TestValidateAcceptsPromptAndResponseFieldNames(t *testing.T) {
	for _, payload := range []string{
		`{"prompt":"synthetic probe prompt for E7 content capture"}`,
		`{"response":"synthetic assistant response body for E7 capture"}`,
		`{"prompts":["synthetic one","synthetic two"]}`,
		`{"responses":["synthetic one","synthetic two"]}`,
	} {
		if err := Validate([]byte(fixtureWithPayload("claude-code", payload))); err != nil {
			t.Fatalf("prompt/response field name must be accepted, got %v", err)
		}
	}
}

// TestValidateAcceptsPathAndCommandFieldNames proves the epic #87 raw-capture
// direction (#101): fields named file_path/full_command/command are no longer
// prohibited by name — TelemetryIQ captures raw paths and command lines as
// first-class governance data — while the value-based likelySecret scan still
// guards every string, so no real credential can ride under them.
func TestValidateAcceptsPathAndCommandFieldNames(t *testing.T) {
	for _, payload := range []string{
		`{"file_path":"internal/foo.go"}`,
		`{"full_command":"go test ./..."}`,
		`{"command":"npm run build"}`,
	} {
		if err := Validate([]byte(fixtureWithPayload("claude-code", payload))); err != nil {
			t.Fatalf("path/command field name must be accepted, got %v", err)
		}
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
	if err == nil || !strings.Contains(err.Error(), "codex, claude-code, cursor-agent, or cursor") {
		t.Fatalf("expected unsupported tool error, got %v", err)
	}
}

func providerFixturePaths(t *testing.T) []string {
	t.Helper()
	root := repositoryRoot(t)
	roots := []string{
		filepath.Join(root, "fixtures", "codex", "synthetic"),
		filepath.Join(root, "fixtures", "codex", "observed-sanitised"),
		filepath.Join(root, "fixtures", "claude", "synthetic"),
		filepath.Join(root, "fixtures", "claude", "observed-sanitised"),
		filepath.Join(root, "fixtures", "cursor", "observed-sanitised"),
	}
	var paths []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || filepath.Ext(path) != ".json" {
				return nil
			}
			paths = append(paths, path)
			return nil
		})
		if err != nil {
			t.Fatalf("walk fixtures: %v", err)
		}
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
