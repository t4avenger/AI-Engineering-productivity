package config

import (
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestManagerPersistsValidatedConfigurationAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	cfg := Default()
	cfg.Storage.RetentionDays = 42
	manager, err := NewManager(path, cfg)
	if err != nil {
		t.Fatal(err)
	}

	if err := manager.SaveMCPAllowlist([]string{" Rogue ", "filesystem", "FILESYSTEM"}); err != nil {
		t.Fatalf("save allowlist: %v", err)
	}
	want := []string{"filesystem", "Rogue"}
	if got := manager.MCPAllowlist(); !reflect.DeepEqual(got, want) {
		t.Fatalf("allowlist = %#v, want %#v", got, want)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload saved config: %v", err)
	}
	if loaded.Storage.RetentionDays != 42 || !reflect.DeepEqual(loaded.Governance.MCPAllowlist, want) {
		t.Fatalf("saved config did not preserve settings: %#v", loaded)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config permissions = %o, want 600", got)
	}

	snapshot := manager.Current()
	snapshot.Governance.MCPAllowlist[0] = "mutated"
	if got := manager.MCPAllowlist(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Current returned aliased state: %#v", got)
	}
}

func TestLoadManagerFromEnvUsesManagedDefaultAndReloadsIt(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("TELEMETRYIQ_CONFIG", "")

	manager, cfg, err := LoadManagerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Fatalf("initial config = %#v, want defaults", cfg)
	}
	if err := manager.SaveMCPAllowlist([]string{"filesystem"}); err != nil {
		t.Fatal(err)
	}
	path, explicit, err := PathFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if explicit || path != filepath.Join(configHome, "telemetryiq", "config.yaml") {
		t.Fatalf("managed path = %q explicit=%v", path, explicit)
	}
	reloaded, err := LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reloaded.Governance.MCPAllowlist, []string{"filesystem"}) {
		t.Fatalf("reloaded allowlist = %#v", reloaded.Governance.MCPAllowlist)
	}
}

func TestLoadManagerFromEnvHonorsExplicitPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "explicit.yaml")
	seed, err := NewManager(path, Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.SaveMCPAllowlist([]string{"git"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TELEMETRYIQ_CONFIG", path)

	manager, _, err := LoadManagerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(manager.MCPAllowlist(), []string{"git"}) {
		t.Fatalf("explicit allowlist = %#v", manager.MCPAllowlist())
	}
}

func TestManagerDoesNotPublishFailedSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	manager, err := NewManager(path, Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SaveMCPAllowlist([]string{"filesystem"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SaveMCPAllowlist([]string{"filesystem", " "}); err == nil {
		t.Fatal("expected blank entry validation error")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(manager.MCPAllowlist(), []string{"filesystem"}) {
		t.Fatal("failed validation changed disk or active configuration")
	}
}

func TestManagerPersistsExactSkillsAllowlist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	manager, err := NewManager(path, Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SaveSkillsAllowlist([]string{"Deploy", "deploy"}); err != nil {
		t.Fatalf("save skills allowlist: %v", err)
	}
	want := []string{"Deploy", "deploy"}
	if got := manager.SkillsAllowlist(); !reflect.DeepEqual(got, want) {
		t.Fatalf("skills allowlist = %#v, want %#v", got, want)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Governance.SkillsAllowlist, want) {
		t.Fatalf("reloaded skills allowlist = %#v", loaded.Governance.SkillsAllowlist)
	}
	if err := manager.SaveSkillsAllowlist([]string{" "}); err == nil {
		t.Fatal("expected invalid skills allowlist error")
	}
	if got := manager.SkillsAllowlist(); !reflect.DeepEqual(got, want) {
		t.Fatalf("failed skill save changed active policy: %#v", got)
	}
}

func TestManagerDoesNotPublishWriteFailure(t *testing.T) {
	root := t.TempDir()
	blockedParent := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blockedParent, []byte("block"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(filepath.Join(blockedParent, "config.yaml"), Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SaveMCPAllowlist([]string{"filesystem"}); err == nil {
		t.Fatal("expected write failure")
	}
	if got := manager.MCPAllowlist(); len(got) != 0 {
		t.Fatalf("failed write published allowlist: %#v", got)
	}
}

func TestManagerConcurrentReadsAndSaves(t *testing.T) {
	manager, err := NewManager(filepath.Join(t.TempDir(), "config.yaml"), Default())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			for iteration := 0; iteration < 10; iteration++ {
				if index%2 == 0 {
					_ = manager.SaveMCPAllowlist([]string{"filesystem", "git"})
				} else {
					_ = manager.MCPAllowlist()
				}
			}
		}(worker)
	}
	wg.Wait()
	if _, err := Load(filepath.Join(filepath.Dir(manager.path), "config.yaml")); err != nil {
		t.Fatalf("concurrent save produced invalid config: %v", err)
	}
}
