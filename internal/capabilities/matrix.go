// Package capabilities loads the multi-provider capability matrix from
// docs/integrations/capability-matrix.md so the Integrations UI and sync tests
// share one parser. Display cells stay honest: never invent parity.
package capabilities

import (
	"fmt"
	"strings"
)

// State is a capability-matrix cell. Values match the matrix vocabulary.
type State string

const (
	StateSupported        State = "supported"
	StatePartial          State = "partial"
	StateUnsupported      State = "unsupported"
	StateUnknown          State = "unknown"
	StateVersionDependent State = "version-dependent"
)

// Provider columns in the committed matrix.
const (
	ProviderCodex      = "Codex"
	ProviderClaudeCode = "Claude Code"
	ProviderCursor     = "Cursor"
)

// Providers is the column order used by the matrix and Integrations UI.
var Providers = []string{ProviderCodex, ProviderClaudeCode, ProviderCursor}

// headlineNames are the compact capability rows surfaced on Integrations.
// Full matrix remains in the markdown; this subset earns the nav slot without
// dumping every research row into the local dashboard.
var headlineNames = []string{
	"Model identity",
	"Token: input/output",
	"Tool calls (generic)",
	"MCP calls",
	"Skill invocations",
	"Session boundaries",
}

// Row is one capability across the three first-class providers.
type Row struct {
	Name   string
	Codex  State
	Claude State
	Cursor State
}

// Matrix is the parsed capability table.
type Matrix struct {
	Rows []Row
}

// Parse extracts the "## Capability matrix" table from the markdown document.
func Parse(document string) (Matrix, error) {
	marker := "## Capability matrix"
	start := strings.Index(document, marker)
	if start == -1 {
		return Matrix{}, fmt.Errorf("capability matrix section not found")
	}
	section := document[start+len(marker):]
	if next := strings.Index(section, "\n## "); next >= 0 {
		section = section[:next]
	}

	var rows []Row
	for _, line := range strings.Split(section, "\n") {
		cells := markdownCells(line)
		if len(cells) != 5 || cells[0] == "Capability" {
			continue
		}
		row := Row{
			Name:   cells[0],
			Codex:  State(cells[1]),
			Claude: State(cells[2]),
			Cursor: State(cells[3]),
		}
		for _, state := range []State{row.Codex, row.Claude, row.Cursor} {
			if !ValidState(state) {
				return Matrix{}, fmt.Errorf("invalid capability state %q for %q", state, row.Name)
			}
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return Matrix{}, fmt.Errorf("capability matrix has no data rows")
	}
	return Matrix{Rows: rows}, nil
}

// Headline returns the Integrations-facing subset of matrix rows, preserving
// matrix order among the selected names. Missing names are omitted (never invented).
func (m Matrix) Headline() []Row {
	wanted := make(map[string]struct{}, len(headlineNames))
	for _, name := range headlineNames {
		wanted[name] = struct{}{}
	}
	out := make([]Row, 0, len(headlineNames))
	for _, row := range m.Rows {
		if _, ok := wanted[row.Name]; ok {
			out = append(out, row)
		}
	}
	return out
}

// StateFor returns the cell for a provider column name.
func (r Row) StateFor(provider string) State {
	switch provider {
	case ProviderCodex:
		return r.Codex
	case ProviderClaudeCode:
		return r.Claude
	case ProviderCursor:
		return r.Cursor
	default:
		return StateUnknown
	}
}

// ValidState reports whether s is a documented matrix cell value.
func ValidState(s State) bool {
	switch s {
	case StateSupported, StatePartial, StateUnsupported, StateUnknown, StateVersionDependent:
		return true
	default:
		return false
	}
}

func markdownCells(row string) []string {
	row = strings.TrimSpace(row)
	if !strings.HasPrefix(row, "|") || strings.Contains(row, "---") {
		return nil
	}
	parts := strings.Split(strings.Trim(row, "|"), "|")
	if len(parts) < 5 {
		return nil
	}
	cells := make([]string, 5)
	for i := 0; i < 4; i++ {
		cells[i] = strings.TrimSpace(parts[i])
	}
	// Notes may contain "|" (e.g. "explicit | inferred"); keep the remainder intact.
	cells[4] = strings.TrimSpace(strings.Join(parts[4:], "|"))
	return cells
}
