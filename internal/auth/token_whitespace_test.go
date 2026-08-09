package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadTrimsWhitespaceFromStoredToken(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, tokenFileName)
	if err := os.WriteFile(path, []byte("  token-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := Read(directory)
	if err != nil || token != "token-value" {
		t.Fatalf("Read() = %q, %v", token, err)
	}
}
