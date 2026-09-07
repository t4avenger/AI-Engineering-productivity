// Package config loads and validates TelemetryIQ's local-only configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultHost   = "localhost"
	defaultPort   = "8080"
	SchemaVersion = "0.1.0"
	ModeLocalOnly = "local-only"
)

type Config struct {
	SchemaVersion string     `yaml:"schema_version"`
	Mode          string     `yaml:"mode"`
	Collection    Collection `yaml:"collection"`
	Storage       Storage    `yaml:"storage"`
	Sharing       Sharing    `yaml:"sharing"`
	Pricing       Pricing    `yaml:"pricing"`
	Insights      Insights   `yaml:"insights"`
	Governance    Governance `yaml:"governance"`
	Host          string     `yaml:"-"`
	Port          string     `yaml:"-"`
}

type Collection struct {
	Level            string `yaml:"level"`
	Prompts          bool   `yaml:"prompts"`
	Responses        bool   `yaml:"responses"`
	SourceCode       bool   `yaml:"source_code"`
	FilePaths        string `yaml:"file_paths"`
	CommandArguments string `yaml:"command_arguments"`
	ToolCalls        bool   `yaml:"tool_calls"`
	ModelUsage       bool   `yaml:"model_usage"`
}

type Storage struct {
	Destination   string `yaml:"destination"`
	RetentionDays int    `yaml:"retention_days"`
}

type Sharing struct {
	Diagnostics        bool   `yaml:"diagnostics"`
	AnonymousAnalytics bool   `yaml:"anonymous_analytics"`
	ResearchSessions   string `yaml:"research_sessions"`
}

// Pricing only selects a local override file; it never enables network pricing.
type Pricing struct {
	OverridePath string `yaml:"override_path"`
}

type Insights struct {
	ContextWaste ContextWaste `yaml:"context_waste"`
}

// Governance configures the §14 detect-and-report policies. All fields are
// optional; an unset policy input yields an indeterminate decision rather than a
// fabricated clean result.
type Governance struct {
	// MCPAllowlist is the set of approved MCP server names for the §14.4
	// unapproved-MCP policy. When empty, the policy reports indeterminate because
	// approval cannot be judged without a declared allowlist.
	MCPAllowlist []string `yaml:"mcp_allowlist"`
}

// ContextWaste configures the §13.10 context-waste insight thresholds.
// Values are compared with >= and must remain in safe, meaningful ranges.
type ContextWaste struct {
	// CachedContextRatioThreshold triggers when cached_input/input >= threshold.
	// Must be within [0,1].
	CachedContextRatioThreshold float64 `yaml:"cached_context_ratio_threshold"`
	// InputTokenGrowthThreshold triggers when max_input/first_input >= threshold.
	// Must be >= 1.
	InputTokenGrowthThreshold float64 `yaml:"input_token_growth_threshold"`
}

// Default returns the safe local-only configuration specified in PRODUCT_MAP.md.
func Default() Config {
	return Config{SchemaVersion: SchemaVersion, Mode: ModeLocalOnly,
		Collection: Collection{Level: "operational", FilePaths: "hash", CommandArguments: "redact", ToolCalls: true, ModelUsage: true},
		Storage:    Storage{Destination: "local", RetentionDays: 30},
		Sharing:    Sharing{ResearchSessions: "explicit-only"},
		Pricing:    Pricing{},
		Insights: Insights{ContextWaste: ContextWaste{
			CachedContextRatioThreshold: 0.75,
			InputTokenGrowthThreshold:   2.0,
		}},
		Host: defaultHost, Port: defaultPort}
}

// FromEnv remains for backwards compatibility with Task 001 callers.
func FromEnv() Config {
	cfg := Default()
	cfg.Host = getenv("TELEMETRYIQ_HOST", defaultHost)
	cfg.Port = getenv("TELEMETRYIQ_PORT", defaultPort)
	return cfg
}

// Load reads a YAML configuration file. An empty path uses the safe defaults.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read configuration: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse configuration: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LoadFromEnv loads TELEMETRYIQ_CONFIG and applies loopback server overrides.
func LoadFromEnv() (Config, error) {
	cfg, err := Load(os.Getenv("TELEMETRYIQ_CONFIG"))
	if err != nil {
		return Config{}, err
	}
	cfg.Host = getenv("TELEMETRYIQ_HOST", defaultHost)
	cfg.Port = getenv("TELEMETRYIQ_PORT", defaultPort)
	if err := cfg.validateServer(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate preserves local-only privacy invariants and supported meanings.
func (c Config) Validate() error {
	if err := c.validateVersionAndMode(); err != nil {
		return err
	}
	if err := c.validateCollection(); err != nil {
		return err
	}
	if err := c.validateStorage(); err != nil {
		return err
	}
	if err := c.validateSharing(); err != nil {
		return err
	}
	if err := c.validateInsights(); err != nil {
		return err
	}
	if err := c.validateGovernance(); err != nil {
		return err
	}
	return c.validatePricing()
}

func (c Config) validateVersionAndMode() error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version must be %q, got %q", SchemaVersion, c.SchemaVersion)
	}
	if c.Mode != ModeLocalOnly {
		return fmt.Errorf("mode must be %q, got %q", ModeLocalOnly, c.Mode)
	}
	return nil
}

func (c Config) validateCollection() error {
	if c.Collection.Level != "aggregate" && c.Collection.Level != "operational" {
		return fmt.Errorf("collection.level must be \"aggregate\" or \"operational\", got %q", c.Collection.Level)
	}
	if c.Collection.Prompts {
		return errors.New("collection.prompts must be false in local-only mode")
	}
	if c.Collection.Responses {
		return errors.New("collection.responses must be false in local-only mode")
	}
	if c.Collection.SourceCode {
		return errors.New("collection.source_code must be false in local-only mode")
	}
	if c.Collection.FilePaths != "hash" {
		return fmt.Errorf("collection.file_paths must be \"hash\", got %q", c.Collection.FilePaths)
	}
	if c.Collection.CommandArguments != "redact" {
		return fmt.Errorf("collection.command_arguments must be \"redact\", got %q", c.Collection.CommandArguments)
	}
	return nil
}

func (c Config) validateStorage() error {
	if c.Storage.Destination != "local" {
		return fmt.Errorf("storage.destination must be \"local\", got %q", c.Storage.Destination)
	}
	if c.Storage.RetentionDays < 1 {
		return fmt.Errorf("storage.retention_days must be at least 1, got %d", c.Storage.RetentionDays)
	}
	return nil
}

func (c Config) validateSharing() error {
	if c.Sharing.Diagnostics {
		return errors.New("sharing.diagnostics must be false by default")
	}
	if c.Sharing.AnonymousAnalytics {
		return errors.New("sharing.anonymous_analytics must be false in local-only mode")
	}
	if c.Sharing.ResearchSessions != "explicit-only" {
		return fmt.Errorf("sharing.research_sessions must be \"explicit-only\", got %q", c.Sharing.ResearchSessions)
	}
	return nil
}

func (c Config) validateInsights() error {
	if c.Insights.ContextWaste.CachedContextRatioThreshold < 0 || c.Insights.ContextWaste.CachedContextRatioThreshold > 1 {
		return fmt.Errorf("insights.context_waste.cached_context_ratio_threshold must be within [0,1], got %v", c.Insights.ContextWaste.CachedContextRatioThreshold)
	}
	if c.Insights.ContextWaste.InputTokenGrowthThreshold < 1 {
		return fmt.Errorf("insights.context_waste.input_token_growth_threshold must be >= 1, got %v", c.Insights.ContextWaste.InputTokenGrowthThreshold)
	}
	return nil
}

func (c Config) validateGovernance() error {
	for i, name := range c.Governance.MCPAllowlist {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("governance.mcp_allowlist[%d] must not be blank", i)
		}
	}
	return nil
}

func (c Config) validatePricing() error {
	if c.Pricing.OverridePath != "" && !filepath.IsAbs(c.Pricing.OverridePath) {
		return errors.New("pricing.override_path must be an absolute local path")
	}
	return nil
}

func (c Config) validateServer() error {
	ip := net.ParseIP(c.Host)
	if c.Host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("TELEMETRYIQ_HOST must be a loopback IP address, got %q", c.Host)
	}
	port, err := strconv.Atoi(c.Port)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("TELEMETRYIQ_PORT must be a number from 1 to 65535, got %q", c.Port)
	}
	return nil
}

func (c Config) Addr() string { return net.JoinHostPort(c.Host, c.Port) }

func getenv(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
