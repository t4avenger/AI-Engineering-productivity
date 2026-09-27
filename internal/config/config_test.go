package config

import (
	"os"
	"path/filepath"
	"reflect"
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
	if !reflect.DeepEqual(cfg, Default()) {
		t.Fatalf("expected documented defaults, got %#v", cfg)
	}
}

func TestLoadAcceptsGovernanceMCPAllowlist(t *testing.T) {
	path := writeConfig(t, validConfiguration+`governance:
  mcp_allowlist:
    - filesystem
    - git
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}
	if !reflect.DeepEqual(cfg.Governance.MCPAllowlist, []string{"filesystem", "git"}) {
		t.Fatalf("expected parsed allowlist, got %#v", cfg.Governance.MCPAllowlist)
	}
}

func samplePromptKeyword(id string) PromptKeyword {
	return PromptKeyword{ID: id, Label: "Synthetic label", Group: "custom", Enabled: true, Kind: "literal", Value: "synthetic"}
}

func TestLoadAcceptsPromptKeywords(t *testing.T) {
	path := writeConfig(t, validConfiguration+`governance:
  prompt_keywords:
    - id: aws-key
      label: AWS access key
      group: credentials
      enabled: true
      kind: regex
      value: AKIA[0-9A-Z]{16}
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}
	want := []PromptKeyword{{ID: "aws-key", Label: "AWS access key", Group: "credentials", Enabled: true, Kind: "regex", Value: "AKIA[0-9A-Z]{16}"}}
	if !reflect.DeepEqual(cfg.Governance.PromptKeywords, want) {
		t.Fatalf("prompt keywords = %#v", cfg.Governance.PromptKeywords)
	}
}

func TestLoadRejectsNonBooleanPromptEnabled(t *testing.T) {
	path := writeConfig(t, validConfiguration+`governance:
  prompt_keywords:
    - id: aws-key
      label: AWS access key
      group: credentials
      enabled: maybe
      kind: literal
      value: AKIA
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected non-boolean enabled state to fail")
	}
}

func TestLoadAcceptsExactSkillsAllowlist(t *testing.T) {
	path := writeConfig(t, validConfiguration+`governance:
  skills_allowlist:
    - Deploy
    - review
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}
	want := []string{"Deploy", "review"}
	if !reflect.DeepEqual(cfg.Governance.SkillsAllowlist, want) {
		t.Fatalf("skills allowlist = %#v, want %#v", cfg.Governance.SkillsAllowlist, want)
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
		{"destination", func(c *Config) { c.Storage.Destination = "cloud" }, "storage.destination"},
		{"retention", func(c *Config) { c.Storage.RetentionDays = 0 }, "storage.retention_days"},
		{"diagnostics", func(c *Config) { c.Sharing.Diagnostics = true }, "sharing.diagnostics"},
		{"analytics", func(c *Config) { c.Sharing.AnonymousAnalytics = true }, "sharing.anonymous_analytics"},
		{"research", func(c *Config) { c.Sharing.ResearchSessions = "always" }, "sharing.research_sessions"},
		{"context waste cached ratio threshold", func(c *Config) { c.Insights.ContextWaste.CachedContextRatioThreshold = 1.1 }, "insights.context_waste.cached_context_ratio_threshold"},
		{"context waste input growth threshold", func(c *Config) { c.Insights.ContextWaste.InputTokenGrowthThreshold = 0.9 }, "insights.context_waste.input_token_growth_threshold"},
		{"governance blank allowlist entry", func(c *Config) { c.Governance.MCPAllowlist = []string{"filesystem", "  "} }, "governance.mcp_allowlist[1]"},
		{"governance blank skill entry", func(c *Config) { c.Governance.SkillsAllowlist = []string{"deploy", "  "} }, "governance.skills_allowlist[1]"},
		{"governance too many skills", func(c *Config) { c.Governance.SkillsAllowlist = make([]string, 101) }, "governance.skills_allowlist must contain at most 100"},
		{"governance oversized skill", func(c *Config) { c.Governance.SkillsAllowlist = []string{strings.Repeat("x", 1025)} }, "governance.skills_allowlist[0]"},
		{"governance oversized Unicode skill", func(c *Config) { c.Governance.SkillsAllowlist = []string{strings.Repeat("界", 1025)} }, "governance.skills_allowlist[0]"},
		{"prompt keywords over limit", func(c *Config) { c.Governance.PromptKeywords = make([]PromptKeyword, 101) }, "governance.prompt_keywords must contain at most 100"},
		{"prompt keyword duplicate id", func(c *Config) {
			c.Governance.PromptKeywords = []PromptKeyword{samplePromptKeyword("same"), samplePromptKeyword("same")}
		}, "governance.prompt_keywords[1]: duplicate id"},
		{"prompt keyword blank value", func(c *Config) {
			rule := samplePromptKeyword("blank")
			rule.Value = "  "
			c.Governance.PromptKeywords = []PromptKeyword{rule}
		}, "governance.prompt_keywords[0]: value must not be blank"},
		{"prompt keyword invalid regex", func(c *Config) {
			rule := samplePromptKeyword("bad-re")
			rule.Kind = "regex"
			rule.Value = "("
			c.Governance.PromptKeywords = []PromptKeyword{rule}
		}, "governance.prompt_keywords[0]: value is not a valid RE2 pattern"},
		{"prompt keyword oversized value", func(c *Config) {
			rule := samplePromptKeyword("long")
			rule.Value = strings.Repeat("界", 1025)
			c.Governance.PromptKeywords = []PromptKeyword{rule}
		}, "governance.prompt_keywords[0]: value must be at most 1024"},
		{"prompt keyword oversized label", func(c *Config) {
			rule := samplePromptKeyword("label")
			rule.Label = strings.Repeat("x", 121)
			c.Governance.PromptKeywords = []PromptKeyword{rule}
		}, "governance.prompt_keywords[0]: label must be at most 120 bytes"},
		{"prompt keyword bad id", func(c *Config) {
			rule := samplePromptKeyword("bad id")
			c.Governance.PromptKeywords = []PromptKeyword{rule}
		}, "governance.prompt_keywords[0]: id must be 1-64"},
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

func TestValidateAcceptsSkillIdentityAtUnicodeCharacterLimit(t *testing.T) {
	cfg := Default()
	cfg.Governance.SkillsAllowlist = []string{strings.Repeat("界", 1024)}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("skill identity at Unicode character limit rejected: %v", err)
	}
}

func TestLoadFromEnvDefaultsToLoopback(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TELEMETRYIQ_CONFIG", "")
	t.Setenv("TELEMETRYIQ_HOST", "")
	t.Setenv("TELEMETRYIQ_PORT", "")
	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}
	if cfg.Addr() != "localhost:8080" {
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
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TELEMETRYIQ_CONFIG", "")
	t.Setenv("TELEMETRYIQ_HOST", "0.0.0.0")
	_, err := LoadFromEnv()
	if err == nil || !strings.Contains(err.Error(), "loopback IP") {
		t.Fatalf("expected loopback error, got %v", err)
	}
}

func TestLoadFromEnvAcceptsLocalhost(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
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
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
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
