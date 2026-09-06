package governance

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/wayne/telemetryiq/internal/normalize/canonical"
	"github.com/wayne/telemetryiq/internal/privacy"
)

// eventWithAttributes builds a canonical event whose sanitiser-produced tokens
// live under attributes, mirroring what the ingest pipeline persists.
func eventWithAttributes(attributes map[string]any) canonical.Event {
	return canonical.Event{
		SchemaVersion:      "0.1.0",
		EventID:            "synthetic",
		OccurredAt:         time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC),
		Attributes:         attributes,
		ProviderExtensions: map[string]any{},
	}
}

func TestRiskyAccessFlagsDotenvReadViaTool(t *testing.T) {
	token := privacy.PathToken(privacy.ClassifyPath(".env"))
	report := RiskyAccessFromEvents([]canonical.Event{eventWithAttributes(map[string]any{"file_path": token})})

	if report.Outcome != OutcomeViolation {
		t.Fatalf("expected violation, got %q", report.Outcome)
	}
	if len(report.Findings) != 1 || report.Findings[0].AccessMethod != AccessFilesystemRead {
		t.Fatalf("expected one filesystem_read finding, got %#v", report.Findings)
	}
	if report.Findings[0].Class != string(privacy.PathDotenv) {
		t.Fatalf("expected dotenv class, got %q", report.Findings[0].Class)
	}
}

func TestRiskyAccessFlagsDotenvReadViaShell(t *testing.T) {
	token := privacy.CommandAccessToken(privacy.ClassifyCommandAccess("cat .env"))
	report := RiskyAccessFromEvents([]canonical.Event{eventWithAttributes(map[string]any{"command": token})})

	if report.Outcome != OutcomeViolation {
		t.Fatalf("expected violation, got %q", report.Outcome)
	}
	if len(report.Findings) != 1 || report.Findings[0].AccessMethod != AccessShellCommand {
		t.Fatalf("expected one shell_command finding, got %#v", report.Findings)
	}
}

func TestRiskyAccessAllowlistsEnvExample(t *testing.T) {
	pathToken := privacy.PathToken(privacy.ClassifyPath(".env.example"))
	commandToken := privacy.CommandAccessToken(privacy.ClassifyCommandAccess("cat .env.example"))
	report := RiskyAccessFromEvents([]canonical.Event{eventWithAttributes(map[string]any{
		"file_path": pathToken,
		"command":   commandToken,
	})})

	if report.Outcome != OutcomeNotViolation {
		t.Fatalf("expected not_violation for allowlisted template, got %q", report.Outcome)
	}
	if len(report.Findings) != 0 {
		t.Fatalf("expected no findings, got %#v", report.Findings)
	}
	if report.Visibility != "observed" {
		t.Fatalf("expected observed visibility, got %q", report.Visibility)
	}
}

func TestRiskyAccessIndeterminateWhenVisibilityAbsent(t *testing.T) {
	// An api_request-shaped event with no path/command tokens: file and command
	// access is simply not visible, so the policy must not claim a clean read.
	report := RiskyAccessFromEvents([]canonical.Event{eventWithAttributes(map[string]any{
		"model":              "synthetic-model",
		"unavailable_fields": []any{"file_operations", "command_execution"},
	})})

	if report.Outcome != OutcomeIndeterminate {
		t.Fatalf("expected indeterminate, got %q", report.Outcome)
	}
	if report.Visibility != "unavailable" {
		t.Fatalf("expected unavailable visibility, got %q", report.Visibility)
	}
	if len(report.Findings) != 0 {
		t.Fatalf("expected no findings, got %#v", report.Findings)
	}
}

func TestRiskyAccessFindingsCarryNoRawEvidence(t *testing.T) {
	// Feed a raw payload through the real sanitiser, then detect on the tokens it
	// emits, and assert no raw path or secret survives into the report or its
	// policy decision.
	sanitizer, err := privacy.New(make([]byte, 32))
	if err != nil {
		t.Fatalf("build sanitizer: %v", err)
	}
	safe := sanitizer.Sanitize(map[string]any{
		"file_path": "/home/dev/app/.env",
		"command":   "cat /home/dev/app/.env --password s3cr3t-value",
	})
	report := RiskyAccessFromEvents([]canonical.Event{eventWithAttributes(safe.Value)})

	serialized, err := json.Marshal(struct {
		Report   RiskyAccess    `json:"report"`
		Decision PolicyDecision `json:"decision"`
	}{report, report.Decision()})
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	for _, prohibited := range []string{"/home/dev/app", "s3cr3t-value", "--password", "cat "} {
		if strings.Contains(string(serialized), prohibited) {
			t.Fatalf("raw evidence %q reached the persisted finding", prohibited)
		}
	}
	if report.Outcome != OutcomeViolation {
		t.Fatalf("expected violation from sanitised tokens, got %q", report.Outcome)
	}
}

func TestRiskyAccessDecisionValidatesAgainstPolicySchema(t *testing.T) {
	schema := compilePolicySchema(t)
	for name, report := range map[string]RiskyAccess{
		"violation":     RiskyAccessFromEvents([]canonical.Event{eventWithAttributes(map[string]any{"file_path": privacy.PathToken(privacy.ClassifyPath(".env"))})}),
		"not_violation": RiskyAccessFromEvents([]canonical.Event{eventWithAttributes(map[string]any{"file_path": privacy.PathToken(privacy.ClassifyPath("main.go"))})}),
		"indeterminate": RiskyAccessFromEvents([]canonical.Event{eventWithAttributes(map[string]any{"model": "x"})}),
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(report.Decision())
			if err != nil {
				t.Fatalf("marshal decision: %v", err)
			}
			var decoded any
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("decode decision: %v", err)
			}
			if err := schema.Validate(decoded); err != nil {
				t.Fatalf("decision must satisfy policy schema: %v", err)
			}
		})
	}
}

func compilePolicySchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test file path")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	schema, err := jsonschema.NewCompiler().Compile(filepath.Join(root, "schemas", "policy.schema.json"))
	if err != nil {
		t.Fatalf("compile policy schema: %v", err)
	}
	return schema
}
