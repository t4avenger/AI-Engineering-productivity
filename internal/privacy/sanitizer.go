// Package privacy classifies file paths and shell commands into coarse
// credential/secret categories for governance detection (PRODUCT_MAP.md §14).
//
// It no longer hides anything: TelemetryIQ captures raw provider-native
// identifiers, file paths, and commands (epic #87). These classifiers run over
// those raw values so the risky-access policy can flag credential access while
// the underlying evidence stays raw and visible.
package privacy

import (
	"strings"
)

// PathClass is a coarse classification of a file path: the sensitive category
// or project location it belongs to. It is derived from the raw path for
// governance detection and is not a substitute for the raw path, which is
// retained alongside it.
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

// ClassifyPath maps a raw path to a coarse class and project boundary.
// Classification is OS-independent and case-insensitive: both separators are
// normalised and Windows drive-letter and tilde-prefixed paths are recognised.
// The sensitive category dominates location (a .env inside or outside the
// project is still dotenv). When the location cannot be determined it returns
// indeterminate rather than a fabricated answer.
func ClassifyPath(raw string) (PathClass, PathBoundary) {
	cleaned := strings.TrimSpace(raw)
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

// CommandAccessClass is a coarse category for a shell command. Only the
// credential-access category is detected here; everything else is
// CommandAccessNone.
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
// file access. It reuses ClassifyPath over each candidate token, so the
// .env.example allowlist and the sensitive-path classes are honoured identically
// to filesystem-read detection. When a secret-file token is present it returns
// CommandAccessCredential with the matched token's boundary; otherwise
// CommandAccessNone. An empty command yields (none, indeterminate) so absent
// visibility is never reported as a clean read.
func ClassifyCommandAccess(raw string) (CommandAccessClass, PathBoundary) {
	cleaned := strings.TrimSpace(raw)
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
