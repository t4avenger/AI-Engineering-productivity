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

// eventWithAttributes builds a canonical event whose raw file paths and command
// lines live under attributes, mirroring what the ingest pipeline now persists
// (issue #88 removed ingest-time hiding — governance classifies raw values).
func eventWithAttributes(attributes map[string]any) canonical.Event {
	return canonical.Event{
		SchemaVersion:      "0.1.0",
		EventID:            "synthetic",
		SessionID:          "session-synthetic-001",
		OccurredAt:         time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC),
		Attributes:         attributes,
		ProviderExtensions: map[string]any{},
	}
}

func TestRiskyAccessFlagsDotenvReadViaTool(t *testing.T) {
	report := RiskyAccessFromEvents([]canonical.Event{eventWithAttributes(map[string]any{"file_path": ".env"})})

	if report.Outcome != OutcomeViolation {
		t.Fatalf("expected violation, got %q", report.Outcome)
	}
	if len(report.Findings) != 1 || report.Findings[0].AccessMethod != AccessFilesystemRead {
		t.Fatalf("expected one filesystem_read finding, got %#v", report.Findings)
	}
	if report.Findings[0].Class != string(privacy.PathDotenv) {
		t.Fatalf("expected dotenv class, got %q", report.Findings[0].Class)
	}
	if report.Findings[0].SessionID != "session-synthetic-001" {
		t.Fatalf("expected session id retained on finding, got %q", report.Findings[0].SessionID)
	}
}

func TestRiskyAccessFlagsDotenvReadViaShell(t *testing.T) {
	report := RiskyAccessFromEvents([]canonical.Event{eventWithAttributes(map[string]any{"command": "cat .env"})})

	if report.Outcome != OutcomeViolation {
		t.Fatalf("expected violation, got %q", report.Outcome)
	}
	if len(report.Findings) != 1 || report.Findings[0].AccessMethod != AccessShellCommand {
		t.Fatalf("expected one shell_command finding, got %#v", report.Findings)
	}
}

func TestRiskyAccessAllowlistsEnvExample(t *testing.T) {
	report := RiskyAccessFromEvents([]canonical.Event{eventWithAttributes(map[string]any{
		"file_path": ".env.example",
		"command":   "cat .env.example",
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

// TestRiskyAccessFlagsToolSpanFilePathAndFullCommand proves the #101 tool-span
// mapping is reachable by governance: a claude_code.tool span nests its raw
// file_path/full_command inside the typed "tool" block, and full_command is the
// Bash tool's native command key (newly registered in commandFieldKeys). A
// credential file_path read and a `cat .env` full_command must each surface a
// finding, classified over the raw value the normaliser stored.
func TestRiskyAccessFlagsToolSpanFilePathAndFullCommand(t *testing.T) {
	report := RiskyAccessFromEvents([]canonical.Event{
		eventWithAttributes(map[string]any{"tool": map[string]any{
			"tool_name": "Read",
			"file_path": "/home/dev/app/.env",
		}}),
		eventWithAttributes(map[string]any{"tool": map[string]any{
			"tool_name":    "Bash",
			"full_command": "cat .env",
		}}),
	})

	if report.Outcome != OutcomeViolation {
		t.Fatalf("expected violation, got %q", report.Outcome)
	}
	if len(report.Findings) != 2 {
		t.Fatalf("expected two findings (filesystem_read + shell_command), got %#v", report.Findings)
	}
	classByMethod := map[AccessMethod]string{}
	for _, finding := range report.Findings {
		classByMethod[finding.AccessMethod] = finding.Class
	}
	if classByMethod[AccessFilesystemRead] != string(privacy.PathDotenv) {
		t.Fatalf("nested file_path finding class = %q, want dotenv", classByMethod[AccessFilesystemRead])
	}
	if _, ok := classByMethod[AccessShellCommand]; !ok {
		t.Fatalf("full_command must be classified as a shell-command access: %#v", report.Findings)
	}
}

func TestRiskyAccessFindingsCarryRawEvidence(t *testing.T) {
	// Issue #88 removed ingest-time hiding: governance classifies the raw stored
	// path/command, and the finding reference carries that raw value so an
	// operator sees exactly which file/command tripped the policy.
	report := RiskyAccessFromEvents([]canonical.Event{eventWithAttributes(map[string]any{
		"file_path": "/home/dev/app/.env",
		"command":   "cat /home/dev/app/.env --password s3cr3t-value",
	})})

	if report.Outcome != OutcomeViolation {
		t.Fatalf("expected violation from raw path/command, got %q", report.Outcome)
	}

	serialized, err := json.Marshal(struct {
		Report   RiskyAccess    `json:"report"`
		Decision PolicyDecision `json:"decision"`
	}{report, report.Decision()})
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if !strings.Contains(string(serialized), "/home/dev/app/.env") {
		t.Fatalf("raw path evidence must be retained in the finding, got %s", serialized)
	}
	if !strings.Contains(string(serialized), `"session_id":"session-synthetic-001"`) {
		t.Fatalf("session id must be retained on findings for UI links, got %s", serialized)
	}
}

func TestRiskyAccessFindingOmitsSessionIDWhenAbsent(t *testing.T) {
	event := eventWithAttributes(map[string]any{"file_path": ".env"})
	event.SessionID = ""
	report := RiskyAccessFromEvents([]canonical.Event{event})
	if len(report.Findings) != 1 {
		t.Fatalf("expected one finding, got %#v", report.Findings)
	}
	if report.Findings[0].SessionID != "" {
		t.Fatalf("empty source session must not invent a session id, got %q", report.Findings[0].SessionID)
	}
}

func TestRiskyAccessDecisionValidatesAgainstPolicySchema(t *testing.T) {
	schema := compilePolicySchema(t)
	for name, report := range map[string]RiskyAccess{
		"violation":     RiskyAccessFromEvents([]canonical.Event{eventWithAttributes(map[string]any{"file_path": ".env"})}),
		"not_violation": RiskyAccessFromEvents([]canonical.Event{eventWithAttributes(map[string]any{"file_path": "main.go"})}),
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
