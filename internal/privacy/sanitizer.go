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
	ActionRetained          Action = "retained"
	ActionRemoved           Action = "removed"
	ActionHashed            Action = "hashed"
	ActionRedacted          Action = "redacted"
	ActionClassified        Action = "classified"
	ActionCommandClassified Action = "command_classified"
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
//
// Fingerprint is the unscoped identifier used for cross-record correlation
// (e.g. deriving a stable event/session id). New persisted identifiers that
// must not be linkable across contexts should prefer FingerprintScoped so a
// value in one scope cannot be matched against the same value in another.
func (s *Sanitizer) Fingerprint(value any) string { return s.hash(value) }

// FingerprintScoped returns a non-reversible identifier that is domain-separated
// by scope: the same value under different scopes produces unrelated
// fingerprints, so a leaked fingerprint in one context cannot be matched against
// the same value in another. Fingerprints remain stable within an installation
// and differ across installations and across key rotations (see RotateSalt).
func (s *Sanitizer) FingerprintScoped(scope string, value any) string {
	subkey := hmac.New(sha256.New, s.salt)
	_, _ = fmt.Fprintf(subkey, "privacy-hmac-v1|scope|%s", scope)
	mac := hmac.New(sha256.New, subkey.Sum(nil))
	_, _ = fmt.Fprint(mac, value)
	return "hmac-sha256:" + hex.EncodeToString(mac.Sum(nil))
}

// PathClass is a coarse, non-reversible classification of a file path. It
// records the sensitive category or project location a path belongs to without
// persisting the raw path, which — for the small universe of interesting secret
// paths — a bare keyed hash would leave dictionary-recoverable if the local key
// ever leaked.
type PathClass string

const (
	PathDotenv          PathClass = "dotenv"
	PathSSHKey          PathClass = "ssh_key"
	PathCert            PathClass = "cert"
	PathCredentialsFile PathClass = "credentials_file"
	PathProjectRelative PathClass = "project_relative"
	PathNonProject      PathClass = "non_project"
)

// PathBoundary records where a path sits relative to the observed project. It is
// derived syntactically (no symlink resolution), so project means
// "syntactically project-relative", not a filesystem-verified location.
type PathBoundary string

const (
	BoundaryProject       PathBoundary = "project"
	BoundaryExternal      PathBoundary = "external"
	BoundaryIndeterminate PathBoundary = "indeterminate"
)

// homeConfigSegments name home-directory locations that hold credentials or
// keys; a path passing through one is treated as external to the project.
var homeConfigSegments = map[string]struct{}{
	".ssh": {}, ".aws": {}, ".gnupg": {}, ".config": {}, ".kube": {}, ".docker": {}, "gcloud": {},
}

// certExtensions name certificate/key-material file extensions. ".key" is
// deliberately excluded: it collides with too many non-secret files
// (localization keys, license keys, keyboard maps) to classify reliably.
var certExtensions = map[string]struct{}{
	".pem": {}, ".crt": {}, ".cer": {}, ".der": {}, ".p12": {}, ".pfx": {},
}

// envTemplateSuffixes name the conventional non-secret dotenv template files.
// A .env.example (or .sample/.template/.dist) is committed documentation of the
// variables an app expects, never real credentials, so it is deliberately
// excluded from the dotenv secret class and from risky-access detection.
var envTemplateSuffixes = map[string]struct{}{
	"example": {}, "sample": {}, "template": {}, "dist": {},
}

// isDotenvSecret reports whether a base file name is a real dotenv secret file
// (.env or a .env.<environment> variant) rather than a committed template. Only
// the exact allowlisted template suffixes are excused; anything else (including
// compound suffixes like .env.local.example) stays classified as a secret so the
// allowlist can never be used to smuggle a real .env past detection.
func isDotenvSecret(base string) bool {
	if base == ".env" {
		return true
	}
	const prefix = ".env."
	if !strings.HasPrefix(base, prefix) {
		return false
	}
	suffix := base[len(prefix):]
	_, allowlisted := envTemplateSuffixes[suffix]
	return !allowlisted
}

// ClassifyPath maps a raw path to a coarse class and project boundary without
// returning or persisting the raw path. Classification is OS-independent and
// case-insensitive: both separators are normalised and Windows drive-letter and
// tilde-prefixed paths are recognised. The sensitive category dominates location
// (a .env inside or outside the project is still dotenv). When the location
// cannot be determined it returns indeterminate rather than a fabricated answer.
func ClassifyPath(raw string) (PathClass, PathBoundary) {
	cleaned := strings.TrimSpace(raw)
	// Idempotency: the sanitiser runs twice (once at ingest, once at storage as
	// defence in depth). A value that is already a path token must survive the
	// second pass unchanged, or its class/boundary would be corrupted.
	if class, boundary, ok := ParsePathToken(cleaned); ok {
		return class, boundary
	}
	if cleaned == "" {
		// An empty or whitespace-only path carries no location or category
		// signal; classifying it as project-relative would fabricate one.
		return PathNonProject, BoundaryIndeterminate
	}
	normalized := strings.ToLower(strings.ReplaceAll(cleaned, "\\", "/"))
	segments := make([]string, 0)
	for _, segment := range strings.Split(normalized, "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	base := ""
	if len(segments) > 0 {
		base = segments[len(segments)-1]
	}

	boundary := classifyBoundary(normalized, segments)

	switch {
	case hasSegment(segments, ".ssh") || isSSHKeyName(base):
		return PathSSHKey, boundary
	case hasCertExtension(base):
		return PathCert, boundary
	case isCredentialsName(base) || hasSegment(segments, ".aws"):
		return PathCredentialsFile, boundary
	case isDotenvSecret(base):
		return PathDotenv, boundary
	case boundary == BoundaryProject:
		return PathProjectRelative, boundary
	default:
		return PathNonProject, boundary
	}
}

// PathToken renders a class + boundary as the compact, non-reversible string
// that is safe to persist in place of a raw path.
func PathToken(class PathClass, boundary PathBoundary) string {
	return fmt.Sprintf("path-class:%s;boundary:%s", class, boundary)
}

// CommandAccessClass is a coarse category for a shell command, derived without
// retaining the raw arguments (PRODUCT_MAP.md §14.5). Only the credential-access
// category is detected here; everything else is CommandAccessNone.
type CommandAccessClass string

const (
	CommandAccessCredential CommandAccessClass = "credential_access"
	CommandAccessNone       CommandAccessClass = "none"
)

// commandSplit separates a raw command line into candidate tokens. It splits on
// whitespace and the shell/interpreter metacharacters that commonly wrap a file
// path (quotes, parentheses, redirections, pipes, separators, and the `=` and
// `,` used by interpreter one-liners such as python -c "open('.env')"), so a
// path embedded in a quoted argument is still surfaced for classification.
func commandSplit(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', '"', '\'', '(', ')', '[', ']', '{', '}',
			'<', '>', '|', '&', ';', ',', '=', '`':
			return true
		}
		return false
	})
}

// ClassifyCommandAccess categorises a raw command line for credential/secret
// file access without returning or persisting any part of the command. It reuses
// ClassifyPath over each candidate token, so the .env.example allowlist and the
// sensitive-path classes are honoured identically to filesystem-read detection.
// When a secret-file token is present it returns CommandAccessCredential with the
// matched token's boundary; otherwise CommandAccessNone. An empty command yields
// (none, indeterminate) so absent visibility is never reported as a clean read.
func ClassifyCommandAccess(raw string) (CommandAccessClass, PathBoundary) {
	cleaned := strings.TrimSpace(raw)
	// Idempotency: a value that is already a command-access token must survive the
	// storage-time re-sanitisation pass unchanged (see ClassifyPath).
	if class, boundary, ok := ParseCommandAccessToken(cleaned); ok {
		return class, boundary
	}
	if cleaned == "" {
		return CommandAccessNone, BoundaryIndeterminate
	}
	for _, token := range commandSplit(raw) {
		class, boundary := ClassifyPath(token)
		switch class {
		case PathDotenv, PathSSHKey, PathCert, PathCredentialsFile:
			return CommandAccessCredential, boundary
		}
	}
	return CommandAccessNone, BoundaryIndeterminate
}

// CommandAccessToken renders a command-access class + boundary as the compact,
// non-reversible string that is safe to persist in place of a raw command.
func CommandAccessToken(class CommandAccessClass, boundary PathBoundary) string {
	return fmt.Sprintf("command-access:%s;boundary:%s", class, boundary)
}

// knownPathClasses and knownBoundaries enumerate the values a genuine token can
// carry. Parsing validates against them so a crafted attribute that merely
// matches the token grammar (e.g. "path-class:/home/dev/app/.env;boundary:project")
// is not mistaken for an already-sanitised token — which would let its raw
// payload survive the idempotency fast-path across the privacy boundary.
var knownPathClasses = map[PathClass]struct{}{
	PathDotenv: {}, PathSSHKey: {}, PathCert: {}, PathCredentialsFile: {},
	PathProjectRelative: {}, PathNonProject: {},
}

var knownBoundaries = map[PathBoundary]struct{}{
	BoundaryProject: {}, BoundaryExternal: {}, BoundaryIndeterminate: {},
}

var knownCommandAccessClasses = map[CommandAccessClass]struct{}{
	CommandAccessCredential: {}, CommandAccessNone: {},
}

// ParsePathToken decodes a PathToken back into its class and boundary. ok is
// false for any string that is not a path-class token, so callers can scan mixed
// attribute values without misreading unrelated strings. Both fields are
// validated against the known enumerations, so an untrusted value that only
// mimics the grammar falls back to re-classification rather than being trusted.
func ParsePathToken(token string) (PathClass, PathBoundary, bool) {
	rawClass, rawBoundary, ok := parseToken(token, "path-class:")
	if !ok {
		return "", "", false
	}
	class, boundary := PathClass(rawClass), PathBoundary(rawBoundary)
	if _, known := knownPathClasses[class]; !known {
		return "", "", false
	}
	if _, known := knownBoundaries[boundary]; !known {
		return "", "", false
	}
	return class, boundary, true
}

// ParseCommandAccessToken decodes a CommandAccessToken back into its class and
// boundary. ok is false for any string that is not a command-access token, or
// whose class/boundary is outside the known enumerations (see ParsePathToken).
func ParseCommandAccessToken(token string) (CommandAccessClass, PathBoundary, bool) {
	rawClass, rawBoundary, ok := parseToken(token, "command-access:")
	if !ok {
		return "", "", false
	}
	class, boundary := CommandAccessClass(rawClass), PathBoundary(rawBoundary)
	if _, known := knownCommandAccessClasses[class]; !known {
		return "", "", false
	}
	if _, known := knownBoundaries[boundary]; !known {
		return "", "", false
	}
	return class, boundary, true
}

// parseToken decodes the shared "<prefix><class>;boundary:<boundary>" grammar.
func parseToken(token, prefix string) (class, boundary string, ok bool) {
	if !strings.HasPrefix(token, prefix) {
		return "", "", false
	}
	body := token[len(prefix):]
	separator := strings.Index(body, ";boundary:")
	if separator < 0 {
		return "", "", false
	}
	class = body[:separator]
	boundary = body[separator+len(";boundary:"):]
	if class == "" || boundary == "" {
		return "", "", false
	}
	return class, boundary, true
}

func classifyBoundary(normalized string, segments []string) PathBoundary {
	// A relative path is syntactically project-relative regardless of the
	// segments it passes through: an in-repo path like internal/.ssh/id_rsa is
	// still inside the project, so the home-config heuristic must not apply.
	if !strings.HasPrefix(normalized, "~") && !isAbsolutePath(normalized) {
		return BoundaryProject
	}
	// Absolute or home-anchored: project membership is unknown. A home-directory
	// config segment marks it external; otherwise it stays indeterminate.
	if strings.HasPrefix(normalized, "~") {
		return BoundaryExternal
	}
	for _, segment := range segments {
		if _, ok := homeConfigSegments[segment]; ok {
			return BoundaryExternal
		}
	}
	return BoundaryIndeterminate
}

// isAbsolutePath recognises POSIX, UNC, and Windows drive-letter absolute paths
// regardless of the host OS (input is already lowercased and slash-normalised).
func isAbsolutePath(normalized string) bool {
	if strings.HasPrefix(normalized, "/") {
		return true
	}
	if len(normalized) >= 2 && normalized[1] == ':' && normalized[0] >= 'a' && normalized[0] <= 'z' {
		return true
	}
	return false
}

func isSSHKeyName(base string) bool {
	for _, name := range []string{"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519"} {
		if base == name || strings.HasPrefix(base, name+".") {
			return true
		}
	}
	return base == "known_hosts" || base == "authorized_keys"
}

func isCredentialsName(base string) bool {
	if strings.Contains(base, "credential") {
		return true
	}
	switch base {
	case ".netrc", "_netrc", ".pgpass", ".npmrc", ".pypirc":
		return true
	}
	return false
}

func hasCertExtension(base string) bool {
	dot := strings.LastIndex(base, ".")
	if dot < 0 {
		return false
	}
	_, ok := certExtensions[base[dot:]]
	return ok
}

func hasSegment(segments []string, target string) bool {
	for _, segment := range segments {
		if segment == target {
			return true
		}
	}
	return false
}

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

// RotateSalt replaces the installation salt with a freshly generated key and
// returns it. Every fingerprint produced from the new salt is unrelated to those
// produced before rotation, so a leaked fingerprint cannot be matched against
// values collected after the key is rotated. Rotation is forward-only: it does
// not re-key identifiers already persisted — remove local data (the salt itself
// is enough) to sever their linkage. The write is atomic (temp file, fsync,
// rename, directory fsync) so a crash cannot leave a truncated key.
func RotateSalt(dataDir string) ([]byte, error) {
	if dataDir == "" {
		return nil, errors.New("privacy salt data directory is required")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create privacy data directory: %w", err)
	}
	if err := os.Chmod(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("secure privacy data directory: %w", err)
	}

	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate privacy salt: %w", err)
	}

	temp, err := os.CreateTemp(dataDir, saltFileName+".rotate-*")
	if err != nil {
		return nil, fmt.Errorf("create privacy salt temp file: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return nil, fmt.Errorf("secure privacy salt temp file: %w", err)
	}
	if _, err := temp.Write(salt); err != nil {
		_ = temp.Close()
		return nil, fmt.Errorf("write privacy salt: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return nil, fmt.Errorf("sync privacy salt: %w", err)
	}
	if err := temp.Close(); err != nil {
		return nil, fmt.Errorf("close privacy salt temp file: %w", err)
	}
	if err := os.Rename(tempPath, filepath.Join(dataDir, saltFileName)); err != nil {
		return nil, fmt.Errorf("replace privacy salt: %w", err)
	}
	if dir, err := os.Open(dataDir); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
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
		case ActionClassified:
			token := PathToken(ClassifyPath(scalarString(value)))
			if attributeName != "" {
				result[key] = map[string]any{"stringValue": token}
			} else {
				result[key] = token
			}
			provenance = append(provenance, Provenance{Path: path, Action: ActionClassified, Reason: "file_path"})
		case ActionCommandClassified:
			token := CommandAccessToken(ClassifyCommandAccess(scalarString(value)))
			if attributeName != "" {
				result[key] = map[string]any{"stringValue": token}
			} else {
				result[key] = token
			}
			provenance = append(provenance, Provenance{Path: path, Action: ActionCommandClassified, Reason: "command_access"})
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

// scalarString returns the underlying string a value carries. OTLP attribute
// values arrive wrapped as {"stringValue": "..."}; classification must read that
// inner string rather than the map's Go representation, or a path/command would
// be classified from "map[stringValue:...]" and always mis-bucketed.
func scalarString(value any) string {
	if wrapped, ok := value.(map[string]any); ok {
		if text, ok := wrapped["stringValue"].(string); ok {
			return text
		}
	}
	return fmt.Sprint(value)
}

func classify(key string) Action {
	normalized := strings.NewReplacer("_", "", "-", "", " ", "", ".", "").Replace(strings.ToLower(key))
	for _, suffix := range []string{"email", "accountid", "conversationid", "hostname"} {
		if strings.HasSuffix(normalized, suffix) {
			return ActionRemoved
		}
	}
	switch normalized {
	case "command", "commandline":
		return ActionCommandClassified
	case "commandarguments", "commandargs", "arguments":
		return ActionRedacted
	case "prompt", "prompts", "response", "responses", "sourcecode", "output", "body", "email", "accountid", "conversationid", "hostname":
		return ActionRemoved
	case "filepath", "filepaths", "filename", "filenames":
		return ActionClassified
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
