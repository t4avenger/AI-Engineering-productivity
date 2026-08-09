package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateStoresStablePrivateToken(t *testing.T) {
	directory := t.TempDir()
	first, err := LoadOrCreate(directory, "")
	if err != nil || len(first) < 40 {
		t.Fatalf("LoadOrCreate() = %q, %v", first, err)
	}
	second, err := LoadOrCreate(directory, "")
	if err != nil || second != first {
		t.Fatalf("second LoadOrCreate() = %q, %v", second, err)
	}
	info, err := os.Stat(filepath.Join(directory, tokenFileName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode = %v, %v", info.Mode().Perm(), err)
	}
}

func TestLoadOrCreateUsesRuntimeOverrideWithoutPersistingIt(t *testing.T) {
	directory := t.TempDir()
	token, err := LoadOrCreate(directory, "test-token")
	if err != nil || token != "test-token" {
		t.Fatalf("LoadOrCreate() = %q, %v", token, err)
	}
	if _, err := os.Stat(filepath.Join(directory, tokenFileName)); !os.IsNotExist(err) {
		t.Fatalf("override unexpectedly persisted: %v", err)
	}
}

func TestReadRequiresExistingToken(t *testing.T) {
	if _, err := Read(t.TempDir()); err == nil {
		t.Fatal("Read() unexpectedly succeeded")
	}
}
