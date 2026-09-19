package fixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wayne/telemetryiq/internal/capabilities"
)

func TestCapabilityMatrixClaimsReferenceCommittedFixtureEvidence(t *testing.T) {
	root := repositoryRoot(t)
	matrixPath := filepath.Join(root, "docs", "integrations", "capability-matrix.md")
	data, err := os.ReadFile(matrixPath)
	if err != nil {
		t.Fatalf("read capability matrix: %v", err)
	}
	document := string(data)

	matrix, err := capabilities.Parse(document)
	if err != nil {
		t.Fatalf("parse capability matrix: %v", err)
	}

	for _, row := range matrix.Rows {
		for _, state := range []capabilities.State{row.Codex, row.Claude, row.Cursor} {
			if state == capabilities.StateUnknown {
				continue
			}
			note := capabilities.NotesForCapability(document, row.Name)
			if !strings.Contains(note, "fixtures/") {
				t.Fatalf("capability %q state %q must reference committed fixture evidence: %q", row.Name, state, note)
			}
			for _, evidencePath := range fixtureEvidencePaths(note) {
				if _, err := os.Stat(filepath.Join(root, evidencePath)); err != nil {
					t.Fatalf("capability %q references missing evidence %q: %v", row.Name, evidencePath, err)
				}
			}
		}
	}
}

func fixtureEvidencePaths(note string) []string {
	fields := strings.FieldsFunc(note, func(r rune) bool {
		return r == ' ' || r == ',' || r == ';' || r == ')' || r == '('
	})
	var paths []string
	for _, field := range fields {
		field = strings.Trim(field, ".`")
		if strings.HasPrefix(field, "fixtures/") {
			paths = append(paths, field)
		}
	}
	return paths
}
