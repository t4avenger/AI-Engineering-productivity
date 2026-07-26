package fixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateAcceptsReviewedSyntheticFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "codex", "synthetic", "fixture-001.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := Validate(data); err != nil {
		t.Fatalf("validate fixture: %v", err)
	}
}

func TestValidateRejectsLikelySecretsAndProhibitedFields(t *testing.T) {
	for _, test := range []struct{ name, payload, want, secret string }{
		{name: "prompt", payload: `{"prompt":"synthetic"}`, want: "payload.prompt", secret: "synthetic"},
		{name: "bearer", payload: `{"note":"Bearer token-value-that-must-never-be-committed"}`, want: "payload.note", secret: "token-value-that-must-never-be-committed"},
		{name: "dash key", payload: `{"note":"` + "sk-" + strings.Repeat("x", 20) + `"}`, want: "payload.note", secret: "sk-" + strings.Repeat("x", 20)},
		{name: "entropy", payload: `{"note":"q1w2E3r4T5y6U7i8O9p0AaBbCcDdEeFf"}`, want: "payload.note", secret: "q1w2E3r4T5y6U7i8O9p0AaBbCcDdEeFf"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := Validate([]byte(fixtureWithPayload(test.payload)))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected error for %q, got %v", test.want, err)
			}
			if strings.Contains(err.Error(), test.secret) {
				t.Fatalf("validation error must not expose rejected value: %v", err)
			}
		})
	}
}

func TestValidateRequiresOriginAndToolVersion(t *testing.T) {
	err := Validate([]byte(`{"fixture_version":1,"fixture_origin":"synthetic","provider":"openai","tool":"codex","captured_at":"2026-07-26T10:00:00Z","sanitisation_reviewed":true,"payload":{}}`))
	if err == nil || !strings.Contains(err.Error(), "tool_version") {
		t.Fatalf("expected missing tool version error, got %v", err)
	}
}

func fixtureWithPayload(payload string) string {
	return `{"fixture_version":1,"fixture_origin":"synthetic","provider":"openai","tool":"codex","tool_version":"synthetic-0.0.0","captured_at":"2026-07-26T10:00:00Z","sanitisation_reviewed":true,"payload":` + payload + `}`
}
