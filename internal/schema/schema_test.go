package schema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestSchemasValidateSyntheticFixtures(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	for _, test := range []struct {
		name   string
		schema string
	}{
		{name: "canonical event", schema: "canonical-event.schema.json"},
		{name: "session", schema: "session.schema.json"},
		{name: "policy", schema: "policy.schema.json"},
		{name: "config", schema: "config.schema.json"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			schema, err := jsonschema.NewCompiler().Compile(filepath.Join(root, "schemas", test.schema))
			if err != nil {
				t.Fatalf("compile schema: %v", err)
			}

			valid := readJSON(t, filepath.Join(root, "fixtures", "schemas", "valid", schemaFixtureName(test.schema)))
			if err := schema.Validate(valid); err != nil {
				t.Fatalf("valid fixture must validate: %v", err)
			}

			invalid := readJSON(t, filepath.Join(root, "fixtures", "schemas", "invalid", schemaFixtureName(test.schema)))
			if err := schema.Validate(invalid); err == nil {
				t.Fatal("invalid fixture must fail validation")
			}
		})
	}
}

func TestCanonicalEventAcceptsProviderExtensions(t *testing.T) {
	root := repositoryRoot(t)
	schema, err := jsonschema.NewCompiler().Compile(filepath.Join(root, "schemas", "canonical-event.schema.json"))
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	if err := schema.Validate(readJSON(t, filepath.Join(root, "fixtures", "schemas", "valid", "canonical-event.json"))); err != nil {
		t.Fatalf("provider extensions fixture must validate: %v", err)
	}
}

func TestCanonicalEventAcceptsCodexNormaliserGoldenFixture(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	schema, err := jsonschema.NewCompiler().Compile(filepath.Join(root, "schemas", "canonical-event.schema.json"))
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	events, ok := readJSON(t, filepath.Join(root, "fixtures", "codex", "expected", "fixture-001.canonical.json")).([]any)
	if !ok {
		t.Fatal("Codex golden fixture must be an array")
	}
	for index, event := range events {
		if err := schema.Validate(event); err != nil {
			t.Fatalf("Codex golden event %d must validate: %v", index, err)
		}
	}
}

func schemaFixtureName(schema string) string {
	return schema[:len(schema)-len(".schema.json")] + ".json"
}

func readJSON(t *testing.T, path string) any {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var value any
	if err := json.Unmarshal(contents, &value); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return value
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
