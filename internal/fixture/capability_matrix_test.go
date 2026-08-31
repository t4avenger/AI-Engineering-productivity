package fixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCapabilityMatrixClaimsReferenceCommittedFixtureEvidence(t *testing.T) {
	root := repositoryRoot(t)
	matrixPath := filepath.Join(root, "docs", "integrations", "capability-matrix.md")
	data, err := os.ReadFile(matrixPath)
	if err != nil {
		t.Fatalf("read capability matrix: %v", err)
	}

	for _, row := range capabilityRows(string(data)) {
		cells := markdownCells(row)
		if len(cells) != 5 || cells[0] == "Capability" {
			continue
		}
		for _, cellIndex := range []int{1, 2, 3} {
			state := cells[cellIndex]
			if !validCapabilityState(state) {
				t.Fatalf("invalid capability state %q in row %q", state, row)
			}
			if state == "unknown" {
				continue
			}
			if !strings.Contains(cells[4], "fixtures/") {
				t.Fatalf("capability %q state %q must reference committed fixture evidence: %q", cells[0], state, cells[4])
			}
			for _, evidencePath := range fixtureEvidencePaths(cells[4]) {
				if _, err := os.Stat(filepath.Join(root, evidencePath)); err != nil {
					t.Fatalf("capability %q references missing evidence %q: %v", cells[0], evidencePath, err)
				}
			}
		}
	}
}

func capabilityRows(document string) []string {
	marker := "## Capability matrix"
	start := strings.Index(document, marker)
	if start == -1 {
		return nil
	}
	section := document[start+len(marker):]
	if next := strings.Index(section, "\n## "); next >= 0 {
		section = section[:next]
	}
	return strings.Split(section, "\n")
}

func markdownCells(row string) []string {
	row = strings.TrimSpace(row)
	if !strings.HasPrefix(row, "|") || strings.Contains(row, "---") {
		return nil
	}
	parts := strings.Split(strings.Trim(row, "|"), "|")
	cells := make([]string, 0, len(parts))
	for _, part := range parts {
		cells = append(cells, strings.TrimSpace(part))
	}
	return cells
}

func validCapabilityState(state string) bool {
	switch state {
	case "supported", "partial", "unsupported", "unknown", "version-dependent":
		return true
	default:
		return false
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
