// Package privacy removes or transforms sensitive telemetry fields before they
// can cross a persistence, diagnostics, or logging boundary.
package privacy

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	saltFileName  = "privacy-hmac-salt"
	saltSize      = 32
	redactedValue = "[REDACTED]"
)

// Action documents how a field was treated at the privacy boundary.
type Action string

const (
	ActionRetained Action = "retained"
	ActionRemoved  Action = "removed"
	ActionHashed   Action = "hashed"
	ActionRedacted Action = "redacted"
)

// Provenance provides the reason a field was retained or transformed without
// exposing its original value.
type Provenance struct {
	Path   string `json:"path"`
	Action Action `json:"action"`
	Reason string `json:"reason"`
}

// Result contains only safe values and their field-level provenance.
type Result struct {
	Value      map[string]any `json:"value"`
	Provenance []Provenance   `json:"provenance"`
}

// Sanitizer applies the fixed local-only collection rules.
type Sanitizer struct {
	salt []byte
}

// Fingerprint returns an installation-specific, non-reversible identifier for
// transient telemetry. It is safe to persist, unlike the input value.
func (s *Sanitizer) Fingerprint(value any) string { return s.hash(value) }

// New creates a Sanitizer from an installation-specific secret salt.
func New(salt []byte) (*Sanitizer, error) {
	if len(salt) != saltSize {
		return nil, fmt.Errorf("privacy salt must be %d bytes", saltSize)
	}
	return &Sanitizer{salt: append([]byte(nil), salt...)}, nil
}

// LoadOrCreateSalt returns the stable salt for one local installation. The
// caller controls the local data directory; the salt is created with 0600
// permissions and is never included in telemetry or diagnostics.
func LoadOrCreateSalt(dataDir string) ([]byte, error) {
	if dataDir == "" {
		return nil, errors.New("privacy salt data directory is required")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create privacy data directory: %w", err)
	}
	if err := os.Chmod(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("secure privacy data directory: %w", err)
	}

	path := filepath.Join(dataDir, saltFileName)
	salt, err := os.ReadFile(path)
	if err == nil {
		validated, validationErr := validateSalt(salt)
		if validationErr != nil {
			return nil, fmt.Errorf("validate privacy salt %q: %w", path, validationErr)
		}
		return validated, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read privacy salt: %w", err)
	}

	salt = make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate privacy salt: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return LoadOrCreateSalt(dataDir)
	}
	if err != nil {
		return nil, fmt.Errorf("create privacy salt: %w", err)
	}
	if _, err := file.Write(salt); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write privacy salt: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close privacy salt: %w", err)
	}
	return append([]byte(nil), salt...), nil
}

func validateSalt(salt []byte) ([]byte, error) {
	if len(salt) != saltSize {
		return nil, fmt.Errorf("privacy salt must be %d bytes", saltSize)
	}
	return append([]byte(nil), salt...), nil
}

// Sanitize recursively applies privacy transformations and records why every
// field was retained or changed. Its result is safe to pass to storage or
// diagnostic code; callers must never pass the input across those boundaries.
func (s *Sanitizer) Sanitize(input map[string]any) Result {
	value, provenance := s.sanitizeMap(input, "")
	return Result{Value: value, Provenance: provenance}
}

func (s *Sanitizer) sanitizeMap(input map[string]any, parentPath string) (map[string]any, []Provenance) {
	result := make(map[string]any, len(input))
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var provenance []Provenance
	for _, key := range keys {
		path := joinPath(parentPath, key)
		value := input[key]
		action := classify(key)
		attributeName := ""
		if key == "value" {
			if name, ok := input["key"].(string); ok {
				attributeName = name
				action = classify(attributeName)
			}
		}
		switch action {
		case ActionRemoved:
			if attributeName != "" {
				result[key] = map[string]any{}
				provenance = append(provenance, Provenance{Path: path + ".attribute_value", Action: ActionRemoved, Reason: removalReason(attributeName)})
				continue
			}
			provenance = append(provenance, Provenance{Path: path, Action: ActionRemoved, Reason: removalReason(key)})
		case ActionHashed:
			if attributeName != "" {
				result[key] = map[string]any{"stringValue": s.hash(value)}
			} else {
				result[key] = s.hash(value)
			}
			provenance = append(provenance, Provenance{Path: path, Action: ActionHashed, Reason: "file_path"})
		case ActionRedacted:
			if attributeName != "" {
				result[key] = map[string]any{"stringValue": redactedValue}
			} else {
				result[key] = redactedValue
			}
			provenance = append(provenance, Provenance{Path: path, Action: ActionRedacted, Reason: "command_arguments"})
		default:
			sanitized, nestedProvenance := s.sanitizeValue(value, path)
			result[key] = sanitized
			if !hasTransformProvenance(nestedProvenance, path) {
				provenance = append(provenance, Provenance{Path: path, Action: ActionRetained, Reason: "operational_metadata"})
			}
			provenance = append(provenance, nestedProvenance...)
		}
	}
	return result, provenance
}

func (s *Sanitizer) sanitizeValue(value any, path string) (any, []Provenance) {
	switch typed := value.(type) {
	case map[string]any:
		return s.sanitizeMap(typed, path)
	case []any:
		result := make([]any, len(typed))
		var provenance []Provenance
		for index, item := range typed {
			sanitized, nestedProvenance := s.sanitizeValue(item, fmt.Sprintf("%s[%d]", path, index))
			result[index] = sanitized
			provenance = append(provenance, nestedProvenance...)
		}
		return result, provenance
	case string:
		if looksSecretLike(typed) {
			return redactedValue, []Provenance{{Path: path, Action: ActionRedacted, Reason: "secret_pattern"}}
		}
		return typed, nil
	default:
		return value, nil
	}
}

func hasTransformProvenance(provenance []Provenance, path string) bool {
	for _, entry := range provenance {
		if entry.Path == path && entry.Action != ActionRetained {
			return true
		}
	}
	return false
}

func (s *Sanitizer) hash(value any) string {
	mac := hmac.New(sha256.New, s.salt)
	_, _ = fmt.Fprint(mac, value)
	return "hmac-sha256:" + hex.EncodeToString(mac.Sum(nil))
}

func classify(key string) Action {
	normalized := strings.NewReplacer("_", "", "-", "", " ", "", ".", "").Replace(strings.ToLower(key))
	for _, suffix := range []string{"email", "accountid", "conversationid", "hostname"} {
		if strings.HasSuffix(normalized, suffix) {
			return ActionRemoved
		}
	}
	switch normalized {
	case "command", "commandline", "commandarguments", "commandargs", "arguments":
		return ActionRedacted
	case "prompt", "prompts", "response", "responses", "sourcecode", "output", "body", "email", "accountid", "conversationid", "hostname":
		return ActionRemoved
	case "filepath", "filepaths", "filename", "filenames":
		return ActionHashed
	case "password", "token", "accesstoken", "apikey", "authorization", "secret":
		return ActionRemoved
	default:
		return ActionRetained
	}
}

func looksSecretLike(value string) bool {
	normalized := strings.ToLower(value)
	for _, marker := range []string{
		"api_key=",
		"apikey=",
		"authorization:",
		"bearer ",
		"password=",
		"secret=",
		"token=",
		"-----begin private key-----",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return strings.HasPrefix(normalized, "sk-")
}

func removalReason(key string) string {
	normalized := strings.ToLower(key)
	if strings.Contains(normalized, "prompt") || strings.Contains(normalized, "response") || strings.Contains(normalized, "source") {
		return "prohibited_content"
	}
	return "sensitive_value"
}

func joinPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}
