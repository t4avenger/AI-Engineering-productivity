package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultMatchesLocalOnlyPrivacySettings(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default configuration must validate: %v", err)
	}
	if cfg.SchemaVersion != SchemaVersion || cfg.Mode != ModeLocalOnly {
		t.Fatalf("expected versioned local-only default, got %#v", cfg)
	}
	if cfg.Collection.Prompts || cfg.Collection.Responses || cfg.Collection.SourceCode {
		t.Fatalf("content collection must be disabled by default, got %#v", cfg.Collection)
	}
	if cfg.Collection.FilePaths != "hash" || cfg.Collection.CommandArguments != "redact" {
		t.Fatalf("expected protected operational fields, got %#v", cfg.Collection)
	}
	if cfg.Sharing.Diagnostics || cfg.Sharing.AnonymousAnalytics || cfg.Sharing.ResearchSessions != "explicit-only" {
		t.Fatalf("expected sharing to remain disabled or explicit-only, got %#v", cfg.Sharing)
	}
}

func TestLoadAcceptsDocumentedConfiguration(t *testing.T) {
	path := writeConfig(t, validConfiguration)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}
	if cfg != Default() {
		t.Fatalf("expected documented defaults, got %#v", cfg)
	}
}

func TestLoadRejectsInvalidConfigurationWithActionableError(t *testing.T) {
	path := writeConfig(t, strings.Replace(validConfiguration, "prompts: false", "prompts: true", 1))
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "collection.prompts must be false") {
		t.Fatalf("expected actionable prompt error, got %v", err)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := writeConfig(t, strings.Replace(validConfiguration, "  model_usage: true", "  model_usage: true\n  unexpected: value", 1))
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "field unexpected not found") {
		t.Fatalf("expected unknown-field error, got %v", err)
	}
}

func TestLoadReportsReadAndYAMLErrors(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil || !strings.Contains(err.Error(), "read configuration") {
		t.Fatalf("expected read error, got %v", err)
	}
	path := writeConfig(t, "schema_version: [")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "parse configuration") {
		t.Fatalf("expected YAML parse error, got %v", err)
	}
}

func TestValidateRejectsUnsafeOrUnsupportedSettings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"schema version", func(c *Config) { c.SchemaVersion = "9.9.9" }, "schema_version"},
		{"mode", func(c *Config) { c.Mode = "personal-cloud" }, "mode"},
		{"collection level", func(c *Config) { c.Collection.Level = "forensic" }, "collection.level"},
		{"responses", func(c *Config) { c.Collection.Responses = true }, "collection.responses"},
		{"source code", func(c *Config) { c.Collection.SourceCode = true }, "collection.source_code"},
		{"file paths", func(c *Config) { c.Collection.FilePaths = "store" }, "collection.file_paths"},
		{"command arguments", func(c *Config) { c.Collection.CommandArguments = "store" }, "collection.command_arguments"},
		{"destination", func(c *Config) { c.Storage.Destination = "cloud" }, "storage.destination"},
		{"retention", func(c *Config) { c.Storage.RetentionDays = 0 }, "storage.retention_days"},
		{"diagnostics", func(c *Config) { c.Sharing.Diagnostics = true }, "sharing.diagnostics"},
		{"analytics", func(c *Config) { c.Sharing.AnonymousAnalytics = true }, "sharing.anonymous_analytics"},
		{"research", func(c *Config) { c.Sharing.ResearchSessions = "always" }, "sharing.research_sessions"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q error, got %v", test.want, err)
			}
		})
	}
}

func TestLoadFromEnvDefaultsToLoopback(t *testing.T) {
	t.Setenv("TELEMETRYIQ_CONFIG", "")
	t.Setenv("TELEMETRYIQ_HOST", "")
	t.Setenv("TELEMETRYIQ_PORT", "")
	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	if cfg.Addr() != "127.0.0.1:8080" {
		t.Fatalf("expected loopback address, got %q", cfg.Addr())
	}
}

func TestFromEnvUsesServerOverrides(t *testing.T) {
	t.Setenv("TELEMETRYIQ_HOST", "127.0.0.1")
	t.Setenv("TELEMETRYIQ_PORT", "9090")
	if got := FromEnv().Addr(); got != "127.0.0.1:9090" {
		t.Fatalf("expected overridden address, got %q", got)
	}
}

func TestLoadFromEnvRejectsNonLoopbackHost(t *testing.T) {
	t.Setenv("TELEMETRYIQ_CONFIG", "")
	t.Setenv("TELEMETRYIQ_HOST", "0.0.0.0")
	_, err := LoadFromEnv()
	if err == nil || !strings.Contains(err.Error(), "loopback IP") {
		t.Fatalf("expected loopback error, got %v", err)
	}
}

func TestLoadFromEnvAcceptsLocalhost(t *testing.T) {
	t.Setenv("TELEMETRYIQ_CONFIG", "")
	t.Setenv("TELEMETRYIQ_HOST", "localhost")
	t.Setenv("TELEMETRYIQ_PORT", "")
	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("load localhost configuration: %v", err)
	}
	if cfg.Addr() != "localhost:8080" {
		t.Fatalf("expected localhost address, got %q", cfg.Addr())
	}
}

func TestLoadFromEnvRejectsInvalidPort(t *testing.T) {
	t.Setenv("TELEMETRYIQ_CONFIG", "")
	t.Setenv("TELEMETRYIQ_PORT", "70000")
	_, err := LoadFromEnv()
	if err == nil || !strings.Contains(err.Error(), "number from 1 to 65535") {
		t.Fatalf("expected invalid port error, got %v", err)
	}
}

func FuzzLoad(f *testing.F) {
	f.Add([]byte(validConfiguration))
	f.Add([]byte("schema_version: ["))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, contents []byte) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatalf("write fuzz config: %v", err)
		}
		_, _ = Load(path)
	})
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	return path
}

const validConfiguration = `schema_version: "0.1.0"
mode: local-only
collection:
  level: operational
  prompts: false
  responses: false
  source_code: false
  file_paths: hash
  command_arguments: redact
  tool_calls: true
  model_usage: true
storage:
  destination: local
  retention_days: 30
sharing:
  diagnostics: false
  anonymous_analytics: false
  research_sessions: explicit-only
`
