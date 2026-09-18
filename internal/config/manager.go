package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Manager owns the validated on-disk configuration and exposes a race-safe
// live view to the local API and dashboard.
type Manager struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

// NewManager creates a manager for an already loaded configuration.
func NewManager(path string, cfg Config) (*Manager, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("configuration path must not be blank")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	manager := &Manager{path: path, cfg: cloneConfig(cfg)}
	return manager, nil
}

// LoadManagerFromEnv loads the active configuration and remembers its explicit
// or managed-default path for later saves.
func LoadManagerFromEnv() (*Manager, Config, error) {
	path, _, err := PathFromEnv()
	if err != nil {
		return nil, Config{}, err
	}
	cfg, err := LoadFromEnv()
	if err != nil {
		return nil, Config{}, err
	}
	manager, err := NewManager(path, cfg)
	if err != nil {
		return nil, Config{}, err
	}
	return manager, cfg, nil
}

// Current returns a detached snapshot of the active configuration.
func (m *Manager) Current() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneConfig(m.cfg)
}

// MCPAllowlist returns a detached snapshot of the active MCP policy input.
func (m *Manager) MCPAllowlist() []string {
	return m.Current().Governance.MCPAllowlist
}

// SaveMCPAllowlist validates and atomically persists the complete configuration
// before publishing the new allowlist to in-process readers.
func (m *Manager) SaveMCPAllowlist(names []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	next := cloneConfig(m.cfg)
	next.Governance.MCPAllowlist = normalizedAllowlist(names)
	if err := next.Validate(); err != nil {
		return err
	}
	contents, err := yaml.Marshal(next)
	if err != nil {
		return fmt.Errorf("encode configuration: %w", err)
	}
	if err := atomicWrite(m.path, contents); err != nil {
		return err
	}
	m.cfg = next
	return nil
}

func normalizedAllowlist(names []string) []string {
	result := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, name)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return strings.ToLower(result[i]) < strings.ToLower(result[j])
	})
	return result
}

func cloneConfig(cfg Config) Config {
	cfg.Governance.MCPAllowlist = append([]string(nil), cfg.Governance.MCPAllowlist...)
	return cfg
}

func atomicWrite(path string, contents []byte) (returnErr error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create configuration directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary configuration: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		if returnErr != nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("secure temporary configuration: %w", err)
	}
	if _, err := tmp.Write(contents); err != nil {
		return fmt.Errorf("write temporary configuration: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temporary configuration: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary configuration: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace configuration: %w", err)
	}
	return nil
}
