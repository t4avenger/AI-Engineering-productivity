package codex

import (
	"encoding/json"
	"strings"

	"github.com/wayne/telemetryiq/internal/normalize"
	"github.com/wayne/telemetryiq/internal/normalize/canonical"
)

var codexEnvironmentKeys = map[string]string{
	"user.id":              "user_id",
	"user.email":           "user_email",
	"user.account_id":      "user_account_id",
	"organization.id":      "organization_id",
	"terminal.type":        "terminal_type",
	"app.entrypoint":       "app_entrypoint",
	"app.version":          "app_version",
	"os.type":              "os_type",
	"os.version":           "os_version",
	"host.name":            "host_name",
	"host.arch":            "host_arch",
	"cwd":                  "cwd",
	"workspace.host_paths": "workspace_host_paths",
}

func rawCodexAttributes(fields map[string]any) map[string]any {
	raw := make(map[string]any, len(fields))
	for key, value := range fields {
		raw[key] = value
	}
	return raw
}

func attachCodexLogBody(extensions map[string]any, body json.RawMessage) {
	if len(body) == 0 || string(body) == "null" {
		return
	}
	extensions["body"] = append(json.RawMessage(nil), body...)
}

func applyCodexEnvironment(event *canonical.Event, attributeSets ...map[string]any) {
	environment := codexEnvironment(attributeSets...)
	if environment == nil {
		return
	}
	event.ProviderExtensions["environment"] = environment
	if event.ActorID == unavailable {
		for _, key := range []string{"user_id", "user_account_id", "user_email"} {
			if value, ok := normalize.ObservedString(environment[key]); ok {
				event.ActorID = normalize.ProviderNativeSessionID(codexSessionPrefix, value)
				break
			}
		}
	}
}

func codexEnvironment(attributeSets ...map[string]any) map[string]any {
	environment := map[string]any{}
	for _, fields := range attributeSets {
		for rawKey, canonicalKey := range codexEnvironmentKeys {
			if _, exists := environment[canonicalKey]; exists {
				continue
			}
			if value, ok := nonBlankCodexValue(fields[rawKey]); ok {
				environment[canonicalKey] = value
			}
		}
	}
	if len(environment) == 0 {
		return nil
	}
	return environment
}

func nonBlankCodexValue(value any) (any, bool) {
	if value == nil {
		return nil, false
	}
	if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
		return nil, false
	}
	return value, true
}
