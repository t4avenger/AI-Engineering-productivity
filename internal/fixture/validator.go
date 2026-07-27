// Package fixture validates sanitised provider fixtures before replay.
package fixture

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[a-z0-9._~+/=-]{12,}\b`),
	regexp.MustCompile(`\b(?:sk|ghp|github[_-]pat)[_-][a-zA-Z0-9_-]{16,}\b`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`-----BEGIN (?:[A-Z ]+ )?PRIVATE KEY-----`),
}

// Validate checks required metadata and rejects likely sensitive content.
// Errors name field paths but never include fixture values.
func Validate(data []byte) error {
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return errors.New("fixture must be valid JSON")
	}
	if err := validateMetadata(document); err != nil {
		return err
	}
	return scan(document, "")
}

func validateMetadata(document map[string]any) error {
	if err := requireMetadata(document); err != nil {
		return err
	}
	if err := validateFixtureVersion(document); err != nil {
		return err
	}
	if err := validateFixtureOrigin(document); err != nil {
		return err
	}
	if err := validateTextMetadata(document); err != nil {
		return err
	}
	if err := validateCapturedAt(document); err != nil {
		return err
	}
	if err := validateReview(document); err != nil {
		return err
	}
	return validatePayload(document)
}

func requireMetadata(document map[string]any) error {
	for _, field := range []string{"fixture_version", "fixture_origin", "provider", "tool", "tool_version", "captured_at", "sanitisation_reviewed", "payload"} {
		if _, found := document[field]; !found {
			return fmt.Errorf("fixture metadata is missing %q", field)
		}
	}
	return nil
}

func validateFixtureVersion(document map[string]any) error {
	if version, ok := document["fixture_version"].(float64); !ok || version != 1 {
		return errors.New("fixture_version must be 1")
	}
	return nil
}

func validateFixtureOrigin(document map[string]any) error {
	origin, ok := document["fixture_origin"].(string)
	if !ok || (origin != "synthetic" && origin != "observed-sanitised") {
		return errors.New("fixture_origin must be synthetic or observed-sanitised")
	}
	return nil
}

func validateTextMetadata(document map[string]any) error {
	for _, field := range []string{"provider", "tool", "tool_version"} {
		if value, ok := document[field].(string); !ok || strings.TrimSpace(value) == "" {
			return fmt.Errorf("fixture metadata %q must be a non-empty string", field)
		}
	}
	if document["tool"] != "codex" {
		return errors.New("fixture metadata tool must be codex")
	}
	return nil
}

func validateCapturedAt(document map[string]any) error {
	capturedAt, ok := document["captured_at"].(string)
	if !ok {
		return errors.New("fixture metadata captured_at must be RFC3339")
	}
	if _, err := time.Parse(time.RFC3339, capturedAt); err != nil {
		return errors.New("fixture metadata captured_at must be RFC3339")
	}
	return nil
}

func validateReview(document map[string]any) error {
	if reviewed, ok := document["sanitisation_reviewed"].(bool); !ok || !reviewed {
		return errors.New("fixture metadata sanitisation_reviewed must be true")
	}
	return nil
}

func validatePayload(document map[string]any) error {
	if _, ok := document["payload"].(map[string]any); !ok {
		return errors.New("fixture metadata payload must be an object")
	}
	return nil
}

func scan(value any, path string) error {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fieldPath := joinPath(path, key)
			if prohibitedField(key) {
				return fmt.Errorf("fixture contains prohibited field %q", fieldPath)
			}
			if err := scan(typed[key], fieldPath); err != nil {
				return err
			}
		}
	case []any:
		for index, item := range typed {
			if err := scan(item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	case string:
		if likelySecret(typed) {
			return fmt.Errorf("fixture contains likely secret at %q", path)
		}
	}
	return nil
}

func prohibitedField(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(key))
	switch normalized {
	case "prompt", "prompts", "response", "responses", "sourcecode", "command", "commandline", "commandarguments", "commandargs", "filepath", "filepaths", "filename", "filenames", "password", "token", "accesstoken", "apikey", "authorization", "secret":
		return true
	default:
		return false
	}
}

func likelySecret(value string) bool {
	for _, pattern := range secretPatterns {
		if pattern.MatchString(value) {
			return true
		}
	}
	return len(value) >= 32 && isSecretAlphabet(value) && shannonEntropy(value) >= 4.3
}

func isSecretAlphabet(value string) bool {
	for _, runeValue := range value {
		if runeValue >= 'a' && runeValue <= 'z' || runeValue >= 'A' && runeValue <= 'Z' || runeValue >= '0' && runeValue <= '9' || strings.ContainsRune(".+/=_-", runeValue) {
			continue
		}
		return false
	}
	return true
}

func shannonEntropy(value string) float64 {
	counts := make(map[rune]int)
	for _, runeValue := range value {
		counts[runeValue]++
	}
	length := float64(len(value))
	var entropy float64
	for _, count := range counts {
		probability := float64(count) / length
		entropy -= probability * math.Log2(probability)
	}
	return entropy
}

func joinPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}
