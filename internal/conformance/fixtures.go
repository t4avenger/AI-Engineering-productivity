package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Reviewed, committed fixtures the suite replays. Codex adapters consume the
// inner OTLP logs payload, so its fixture is unwrapped; the Claude adapter
// consumes the full reviewed wrapper.
const (
	codexLogsFixture   = "codex/observed-sanitised/codex-0.145.0-logs.json"
	claudeEventFixture = "claude/observed-sanitised/claude-code-2.1.251-otlp-events.json"
	cursorEventFixture = "cursor/observed-sanitised/cursor-agent-2026.09.02-c22c1a3-stream-result-with-model.json"
)

// fixturePath resolves a path under the repository fixtures/ directory. The suite
// package lives at internal/conformance, two levels below the repository root.
func fixturePath(t *testing.T, relative string) string {
	t.Helper()
	return filepath.Join("..", "..", "fixtures", relative)
}

func readFixture(t *testing.T, relative string) []byte {
	t.Helper()
	data, err := os.ReadFile(fixturePath(t, relative))
	if err != nil {
		t.Fatalf("read fixture %s: %v", relative, err)
	}
	return data
}

// codexReviewedInput returns the inner OTLP logs payload of the reviewed Codex
// fixture, the byte shape the Codex log adapters consume.
func codexReviewedInput(t *testing.T) []byte {
	t.Helper()
	var wrapper struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(readFixture(t, codexLogsFixture), &wrapper); err != nil {
		t.Fatalf("unwrap codex payload: %v", err)
	}
	return wrapper.Payload
}

// claudeReviewedInput returns the full reviewed Claude Code fixture wrapper, the
// byte shape the Claude adapters consume (they validate the wrapper themselves).
func claudeReviewedInput(t *testing.T) []byte {
	t.Helper()
	return readFixture(t, claudeEventFixture)
}

// cursorReviewedInput returns the full reviewed Cursor Agent fixture wrapper,
// the byte shape the Cursor adapters consume (they validate the wrapper
// themselves).
func cursorReviewedInput(t *testing.T) []byte {
	t.Helper()
	return readFixture(t, cursorEventFixture)
}
