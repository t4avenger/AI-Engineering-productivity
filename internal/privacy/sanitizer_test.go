package privacy

import (
	"bytes"
	"encoding/json"
	"fmt"
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
		"arguments":         "synthetic raw command arguments",
		"output":            "synthetic command output",
		"provider_extensions": map[string]any{
			"api_key": "synthetic-api-key",
			"model":   "gpt-test",
		},
	})

	persisted, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal sanitized result: %v", err)
	}
	for _, prohibited := range []string{"synthetic prompt secret", "synthetic response secret", "syntheticSecret", "/private/project/main.go", "synthetic-command-secret", "synthetic raw command arguments", "synthetic command output", "synthetic-api-key"} {
		if strings.Contains(string(persisted), prohibited) {
			t.Fatalf("prohibited value reached storage boundary: %q", prohibited)
		}
	}
	if got := result.Value["command_arguments"]; got != "[REDACTED]" {
		t.Fatalf("expected command arguments redacted, got %#v", got)
	}
	if got, ok := result.Value["file_path"].(string); !ok || got != "path-class:non_project;boundary:indeterminate" {
		t.Fatalf("expected classified file path token, got %#v", got)
	}
	if strings.Contains(fmt.Sprint(result.Value["file_path"]), "hmac-sha256:") {
		t.Fatalf("classified file path must not carry a reversible hash, got %#v", result.Value["file_path"])
	}
	if _, found := result.Value["prompt"]; found {
		t.Fatal("prompt must be removed")
	}
	if !hasProvenance(result.Provenance, "file_path", ActionClassified) || !hasProvenance(result.Provenance, "provider_extensions.api_key", ActionRemoved) {
		t.Fatalf("expected transformation provenance, got %#v", result.Provenance)
	}
}

func TestSanitizeSecretLikeScalarHasOnlyRedactedProvenance(t *testing.T) {
	result := testSanitizer(t, 1).Sanitize(map[string]any{
		"provider_extensions": map[string]any{
			"metadata": "SK-synthetic-secret",
		},
	})

	extension, ok := result.Value["provider_extensions"].(map[string]any)
	if !ok {
		t.Fatalf("provider_extensions = %#v", result.Value["provider_extensions"])
	}
	if got := extension["metadata"]; got != redactedValue {
		t.Fatalf("expected redacted metadata, got %#v", got)
	}
	if hasProvenance(result.Provenance, "provider_extensions.metadata", ActionRetained) {
		t.Fatalf("secret-like scalar must not have retained provenance at same path: %#v", result.Provenance)
	}
	if !hasProvenance(result.Provenance, "provider_extensions.metadata", ActionRedacted) {
		t.Fatalf("expected redacted provenance, got %#v", result.Provenance)
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
	for _, prohibited := range []string{"synthetic@example.test", "synthetic-account", "synthetic-host", "synthetic body"} {
		if strings.Contains(string(persisted), prohibited) {
			t.Fatalf("prohibited OTLP value retained: %q", prohibited)
		}
	}
	if !strings.Contains(string(persisted), "synthetic-conversation") {
		t.Fatalf("provider conversation ID should be retained in local-only mode: %s", persisted)
	}
}

func TestClassifiedPathsAreNotDictionaryReversible(t *testing.T) {
	sanitizer := testSanitizer(t, 1)
	// A leaked local key must not let an attacker recover which secret file was
	// touched from the persisted output. Every entry below is a well-known secret
	// path an attacker would hold in a dictionary.
	cases := map[string]string{
		".env":                    "path-class:dotenv;boundary:project",
		".env.production":         "path-class:dotenv;boundary:project",
		"~/.ssh/id_rsa":           "path-class:ssh_key;boundary:external",
		"~/.aws/credentials":      "path-class:credentials_file;boundary:external",
		"/etc/ssl/server.pem":     "path-class:cert;boundary:indeterminate",
		`C:\Users\me\.ssh\id_rsa`: "path-class:ssh_key;boundary:external",
		"internal/app/handler.go": "path-class:project_relative;boundary:project",
		// A home-config-like segment inside a relative path is still in-repo:
		// the boundary stays project, not external.
		"internal/.ssh/id_rsa": "path-class:ssh_key;boundary:project",
	}
	for raw, want := range cases {
		result := sanitizer.Sanitize(map[string]any{"file_path": raw})
		got, _ := result.Value["file_path"].(string)
		if got != want {
			t.Fatalf("classify %q: got %q want %q", raw, got, want)
		}
		persisted, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("marshal %q: %v", raw, err)
		}
		if strings.Contains(string(persisted), raw) {
			t.Fatalf("raw path %q reached storage boundary", raw)
		}
		if strings.Contains(string(persisted), "hmac-sha256:") {
			t.Fatalf("classified path %q must not carry a reversible fingerprint", raw)
		}
	}
	// Two distinct dotenv files collapse to one token: the specific file cannot
	// be recovered even by an attacker who knows the class.
	if sanitizer.Sanitize(map[string]any{"file_path": ".env"}).Value["file_path"] !=
		sanitizer.Sanitize(map[string]any{"file_path": ".env.production"}).Value["file_path"] {
		t.Fatal("distinct dotenv paths must share one class token")
	}

	// An empty or whitespace-only path must not fabricate a project-relative
	// signal; it carries no location or category information.
	for _, blank := range []string{"", "   ", "\t"} {
		got, _ := sanitizer.Sanitize(map[string]any{"file_path": blank}).Value["file_path"].(string)
		if got != "path-class:non_project;boundary:indeterminate" {
			t.Fatalf("blank path %q: got %q want non_project/indeterminate", blank, got)
		}
	}
}

func TestClassifyPathAllowlistsEnvTemplates(t *testing.T) {
	// Committed dotenv templates carry no secrets and must not classify as the
	// dotenv secret class, so risky-access detection can allowlist them.
	for _, template := range []string{".env.example", ".env.sample", ".env.template", ".env.dist"} {
		class, _ := ClassifyPath(template)
		if class == PathDotenv {
			t.Fatalf("template %q must not classify as dotenv secret", template)
		}
	}
	// Real dotenv files stay classified as secrets, including compound suffixes
	// that merely end in an allowlisted word.
	for _, secret := range []string{".env", ".env.production", ".env.local", ".env.local.example"} {
		if class, _ := ClassifyPath(secret); class != PathDotenv {
			t.Fatalf("secret %q must classify as dotenv, got %q", secret, class)
		}
	}
}

func TestParseTokenRejectsCraftedPayloads(t *testing.T) {
	// A crafted attribute that only mimics the token grammar must not be trusted
	// as an already-sanitised token, or its raw payload would survive the
	// idempotency fast-path across the privacy boundary.
	craftedPaths := []string{
		"path-class:/home/dev/app/.env;boundary:project",
		"path-class:dotenv;boundary:/etc/shadow",
		"path-class:secret;boundary:project",
	}
	for _, token := range craftedPaths {
		if _, _, ok := ParsePathToken(token); ok {
			t.Fatalf("crafted path token %q must be rejected", token)
		}
		// Re-classification must strip the raw payload rather than echo it back.
		if class, _ := ClassifyPath(token); strings.Contains(string(PathToken(class, BoundaryProject)), ".env;") {
			t.Fatalf("crafted path token %q leaked raw payload after re-classification", token)
		}
	}
	craftedCommands := []string{
		"command-access:cat /home/dev/.env;boundary:project",
		"command-access:credential_access;boundary:rm -rf /",
	}
	for _, token := range craftedCommands {
		if _, _, ok := ParseCommandAccessToken(token); ok {
			t.Fatalf("crafted command token %q must be rejected", token)
		}
	}
	// Genuine tokens still round-trip.
	if _, _, ok := ParsePathToken(PathToken(PathDotenv, BoundaryIndeterminate)); !ok {
		t.Fatal("genuine path token must parse")
	}
	if _, _, ok := ParseCommandAccessToken(CommandAccessToken(CommandAccessCredential, BoundaryProject)); !ok {
		t.Fatal("genuine command token must parse")
	}
}

func TestClassifyCommandAccessDetectsCredentialReads(t *testing.T) {
	credential := []string{
		"cat .env",
		"grep API_KEY .env.production",
		`python -c "print(open('.env').read())"`,
		"cp ~/.aws/credentials /tmp/x",
		"base64 ~/.ssh/id_rsa",
	}
	for _, command := range credential {
		if class, _ := ClassifyCommandAccess(command); class != CommandAccessCredential {
			t.Fatalf("command %q must flag credential access, got %q", command, class)
		}
	}
	benign := []string{
		"",
		"go test ./...",
		"cat README.md",
		"cat .env.example", // allowlisted template
		"git status",
	}
	for _, command := range benign {
		if class, _ := ClassifyCommandAccess(command); class != CommandAccessNone {
			t.Fatalf("command %q must not flag credential access, got %q", command, class)
		}
	}
}

func TestSanitizeClassifiesCommandWithoutRetainingRawArguments(t *testing.T) {
	result := testSanitizer(t, 1).Sanitize(map[string]any{
		"command": "cat .env --password synthetic-command-secret",
	})
	persisted, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal sanitized result: %v", err)
	}
	for _, prohibited := range []string{"synthetic-command-secret", "--password", "cat .env"} {
		if strings.Contains(string(persisted), prohibited) {
			t.Fatalf("raw command fragment %q reached storage boundary", prohibited)
		}
	}
	if got, _ := result.Value["command"].(string); got != "command-access:credential_access;boundary:project" {
		t.Fatalf("expected classified command token, got %#v", result.Value["command"])
	}
	if !hasProvenance(result.Provenance, "command", ActionCommandClassified) {
		t.Fatalf("expected command-classified provenance, got %#v", result.Provenance)
	}
}

func TestSanitizeIsIdempotentOnClassifiedTokens(t *testing.T) {
	// The pipeline sanitises at ingest and again at storage. A second pass over
	// an already-classified path or command token must preserve its class and
	// boundary, or governance detection that depends on the class would break.
	sanitizer := testSanitizer(t, 1)
	first := sanitizer.Sanitize(map[string]any{
		"file_path": "/home/dev/app/.env",
		"command":   "cat /home/dev/app/.env",
	})
	second := sanitizer.Sanitize(first.Value)

	if got := second.Value["file_path"]; got != first.Value["file_path"] {
		t.Fatalf("path token not idempotent: first %#v second %#v", first.Value["file_path"], got)
	}
	if got := second.Value["command"]; got != first.Value["command"] {
		t.Fatalf("command token not idempotent: first %#v second %#v", first.Value["command"], got)
	}
	if got, _ := second.Value["file_path"].(string); got != "path-class:dotenv;boundary:indeterminate" {
		t.Fatalf("expected preserved dotenv path token, got %#v", second.Value["file_path"])
	}
	if got, _ := second.Value["command"].(string); got != "command-access:credential_access;boundary:indeterminate" {
		t.Fatalf("expected preserved credential command token, got %#v", second.Value["command"])
	}
}

func TestFingerprintScopedIsDomainSeparatedStableAndInstallationSpecific(t *testing.T) {
	first := testSanitizer(t, 1)
	second := testSanitizer(t, 2)
	const value = "session-correlation-key"

	sessionScoped := first.FingerprintScoped("session", value)
	if sessionScoped != first.FingerprintScoped("session", value) {
		t.Fatal("expected scoped fingerprint stable within one installation")
	}
	if sessionScoped == first.FingerprintScoped("event", value) {
		t.Fatal("expected different fingerprints across scopes")
	}
	if sessionScoped == second.FingerprintScoped("session", value) {
		t.Fatal("expected different scoped fingerprints across installations")
	}
	if !strings.HasPrefix(sessionScoped, "hmac-sha256:") {
		t.Fatal("expected hmac-prefixed scoped fingerprint")
	}
}

func TestRotateSaltChangesFingerprints(t *testing.T) {
	dir := t.TempDir()
	original, err := LoadOrCreateSalt(dir)
	if err != nil {
		t.Fatalf("create salt: %v", err)
	}
	before, err := New(original)
	if err != nil {
		t.Fatalf("build sanitizer: %v", err)
	}

	rotated, err := RotateSalt(dir)
	if err != nil {
		t.Fatalf("rotate salt: %v", err)
	}
	if string(rotated) == string(original) {
		t.Fatal("rotation must produce a new key")
	}
	reloaded, err := LoadOrCreateSalt(dir)
	if err != nil {
		t.Fatalf("reload salt: %v", err)
	}
	if string(reloaded) != string(rotated) {
		t.Fatal("rotated salt must be the one persisted")
	}
	after, err := New(rotated)
	if err != nil {
		t.Fatalf("build rotated sanitizer: %v", err)
	}

	const value = "session-correlation-key"
	if before.Fingerprint(value) == after.Fingerprint(value) {
		t.Fatal("rotation must change fingerprints for the same value")
	}
	if before.FingerprintScoped("session", value) == after.FingerprintScoped("session", value) {
		t.Fatal("rotation must change scoped fingerprints for the same value")
	}

	info, err := os.Stat(filepath.Join(dir, saltFileName))
	if err != nil {
		t.Fatalf("stat rotated salt: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected rotated salt permissions 0600, got %o", got)
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
