package privacy

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSanitizeRemovesSensitiveContentBeforeStorageBoundary(t *testing.T) {
	sanitizer := testSanitizer(t, 1)
	result := sanitizer.Sanitize(map[string]any{
		"prompt":            "synthetic prompt secret",
		"response":          "synthetic response secret",
		"source_code":       "const syntheticSecret = true",
		"file_path":         "/private/project/main.go",
		"command_arguments": "--token synthetic-command-secret",
		"provider_extensions": map[string]any{
			"api_key": "synthetic-api-key",
			"model":   "gpt-test",
		},
	})

	persisted, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal sanitized result: %v", err)
	}
	for _, prohibited := range []string{"synthetic prompt secret", "synthetic response secret", "syntheticSecret", "/private/project/main.go", "synthetic-command-secret", "synthetic-api-key"} {
		if strings.Contains(string(persisted), prohibited) {
			t.Fatalf("prohibited value reached storage boundary: %q", prohibited)
		}
	}
	if got := result.Value["command_arguments"]; got != "[REDACTED]" {
		t.Fatalf("expected command arguments redacted, got %#v", got)
	}
	if got, ok := result.Value["file_path"].(string); !ok || !strings.HasPrefix(got, "hmac-sha256:") {
		t.Fatalf("expected hashed file path, got %#v", got)
	}
	if _, found := result.Value["prompt"]; found {
		t.Fatal("prompt must be removed")
	}
	if !hasProvenance(result.Provenance, "file_path", ActionHashed) || !hasProvenance(result.Provenance, "provider_extensions.api_key", ActionRemoved) {
		t.Fatalf("expected transformation provenance, got %#v", result.Provenance)
	}
}

func TestSanitizeRemovesSensitiveOTLPAttributeValues(t *testing.T) {
	result := testSanitizer(t, 1).Sanitize(map[string]any{
		"resourceLogs": []any{map[string]any{"attributes": []any{
			map[string]any{"key": "user.email", "value": map[string]any{"stringValue": "synthetic@example.test"}},
			map[string]any{"key": "user.account_id", "value": map[string]any{"stringValue": "synthetic-account"}},
			map[string]any{"key": "host.name", "value": map[string]any{"stringValue": "synthetic-host"}},
			map[string]any{"key": "conversation.id", "value": map[string]any{"stringValue": "synthetic-conversation"}},
		}, "body": map[string]any{"stringValue": "synthetic body"}}},
	})
	persisted, err := json.Marshal(result.Value)
	if err != nil {
		t.Fatal(err)
	}
	for _, prohibited := range []string{"synthetic@example.test", "synthetic-account", "synthetic-host", "synthetic-conversation", "synthetic body"} {
		if strings.Contains(string(persisted), prohibited) {
			t.Fatalf("prohibited OTLP value retained: %q", prohibited)
		}
	}
}

func TestHashIsStablePerInstallationAndDifferentAcrossInstallations(t *testing.T) {
	first := testSanitizer(t, 1)
	second := testSanitizer(t, 2)
	input := map[string]any{"file_path": "/private/project/main.go"}
	if first.Sanitize(input).Value["file_path"] != first.Sanitize(input).Value["file_path"] {
		t.Fatal("expected stable hash within one installation")
	}
	if first.Sanitize(input).Value["file_path"] == second.Sanitize(input).Value["file_path"] {
		t.Fatal("expected different hashes across installations")
	}
}

func TestLoadOrCreateSaltIsStableAndPrivate(t *testing.T) {
	dir := t.TempDir()
	first, err := LoadOrCreateSalt(dir)
	if err != nil {
		t.Fatalf("create salt: %v", err)
	}
	second, err := LoadOrCreateSalt(dir)
	if err != nil {
		t.Fatalf("load salt: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("expected stable installation salt")
	}
	info, err := os.Stat(filepath.Join(dir, saltFileName))
	if err != nil {
		t.Fatalf("stat salt: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected salt permissions 0600, got %o", got)
	}
	if runtime.GOOS != "windows" {
		if got := mustStat(t, dir).Mode().Perm(); got != 0o700 {
			t.Fatalf("expected salt directory permissions 0700, got %o", got)
		}
	}
}

func TestLoadOrCreateSaltReportsInvalidSaltPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, saltFileName)
	if err := os.WriteFile(path, []byte("not-a-valid-salt"), 0o600); err != nil {
		t.Fatalf("write invalid salt: %v", err)
	}
	_, err := LoadOrCreateSalt(dir)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("expected invalid salt error with path %q, got %v", path, err)
	}
}

func FuzzSanitize(f *testing.F) {
	f.Add("prompt", "synthetic secret")
	f.Add("file_path", "/private/file.go")
	f.Fuzz(func(t *testing.T, key, value string) {
		sanitizer := testSanitizer(t, 1)
		result := sanitizer.Sanitize(map[string]any{key: value})
		if _, err := json.Marshal(result); err != nil {
			t.Fatalf("marshal sanitized value: %v", err)
		}
	})
}

func testSanitizer(t *testing.T, fill byte) *Sanitizer {
	t.Helper()
	sanitizer, err := New(bytesOf(fill))
	if err != nil {
		t.Fatalf("create sanitizer: %v", err)
	}
	return sanitizer
}

func bytesOf(fill byte) []byte {
	return bytes.Repeat([]byte{fill}, saltSize)
}

func hasProvenance(provenance []Provenance, path string, action Action) bool {
	for _, entry := range provenance {
		if entry.Path == path && entry.Action == action {
			return true
		}
	}
	return false
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	return info
}
