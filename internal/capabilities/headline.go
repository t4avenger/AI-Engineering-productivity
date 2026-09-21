package capabilities

// HeadlineMatrix is the Integrations UI subset of
// docs/integrations/capability-matrix.md. TestHeadlineMatchesDocument fails if
// these cells drift from the committed markdown.
func HeadlineMatrix() []Row {
	return []Row{
		{Name: "Model identity", Codex: StateSupported, Claude: StateSupported, Cursor: StatePartial},
		{Name: "Token: input/output", Codex: StateSupported, Claude: StateSupported, Cursor: StateSupported},
		{Name: "Tool calls (generic)", Codex: StatePartial, Claude: StatePartial, Cursor: StateUnknown},
		{Name: "MCP calls", Codex: StateUnknown, Claude: StateSupported, Cursor: StateUnknown},
		{Name: "Skill invocations", Codex: StateVersionDependent, Claude: StateSupported, Cursor: StateUnknown},
		{Name: "Session boundaries", Codex: StatePartial, Claude: StatePartial, Cursor: StatePartial},
	}
}
