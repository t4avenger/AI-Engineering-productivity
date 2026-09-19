package capabilities_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wayne/telemetryiq/internal/capabilities"
)

func TestHeadlineMatchesDocument(t *testing.T) {
	matrix := loadMatrix(t)
	got := capabilities.HeadlineMatrix()
	want := matrix.Headline()
	if len(got) != len(want) {
		t.Fatalf("headline length = %d, want %d from document", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("headline[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseRejectsInvalidState(t *testing.T) {
	_, err := capabilities.Parse(`## Capability matrix

| Capability | Codex | Claude Code | Cursor | Notes |
| --- | --- | --- | --- | --- |
| Model identity | invent | supported | partial | note |
`)
	if err == nil {
		t.Fatal("expected error for invalid state")
	}
}

func TestValidState(t *testing.T) {
	for _, state := range []capabilities.State{
		capabilities.StateSupported,
		capabilities.StatePartial,
		capabilities.StateUnsupported,
		capabilities.StateUnknown,
		capabilities.StateVersionDependent,
	} {
		if !capabilities.ValidState(state) {
			t.Fatalf("%q should be valid", state)
		}
	}
	if capabilities.ValidState("invent") {
		t.Fatal("invent should be invalid")
	}
}

func loadMatrix(t *testing.T) capabilities.Matrix {
	t.Helper()
	root := repositoryRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "docs", "integrations", "capability-matrix.md"))
	if err != nil {
		t.Fatalf("read capability matrix: %v", err)
	}
	matrix, err := capabilities.Parse(string(data))
	if err != nil {
		t.Fatalf("parse capability matrix: %v", err)
	}
	return matrix
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	return filepath.Clean(filepath.Join(workingDirectory, "..", ".."))
}
