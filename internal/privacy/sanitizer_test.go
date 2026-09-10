package privacy

import (
	"testing"
)

func TestClassifyPathBucketsSecretAndProjectPaths(t *testing.T) {
	cases := map[string]struct {
		class    PathClass
		boundary PathBoundary
	}{
		".env":                    {PathDotenv, BoundaryProject},
		".env.production":         {PathDotenv, BoundaryProject},
		"~/.ssh/id_rsa":           {PathSSHKey, BoundaryExternal},
		"~/.aws/credentials":      {PathCredentialsFile, BoundaryExternal},
		"/etc/ssl/server.pem":     {PathCert, BoundaryIndeterminate},
		`C:\Users\me\.ssh\id_rsa`: {PathSSHKey, BoundaryExternal},
		"internal/app/handler.go": {PathProjectRelative, BoundaryProject},
		// A home-config-like segment inside a relative path is still in-repo: the
		// boundary stays project, not external.
		"internal/.ssh/id_rsa": {PathSSHKey, BoundaryProject},
	}
	for raw, want := range cases {
		class, boundary := ClassifyPath(raw)
		if class != want.class || boundary != want.boundary {
			t.Fatalf("classify %q: got (%s,%s) want (%s,%s)", raw, class, boundary, want.class, want.boundary)
		}
	}

	// An empty or whitespace-only path must not fabricate a project-relative
	// signal; it carries no location or category information.
	for _, blank := range []string{"", "   ", "\t"} {
		class, boundary := ClassifyPath(blank)
		if class != PathNonProject || boundary != BoundaryIndeterminate {
			t.Fatalf("blank path %q: got (%s,%s) want non_project/indeterminate", blank, class, boundary)
		}
	}
}

func TestClassifyPathAllowlistsEnvTemplates(t *testing.T) {
	// Committed dotenv templates carry no secrets and must not classify as the
	// dotenv secret class, so risky-access detection can allowlist them.
	for _, template := range []string{".env.example", ".env.sample", ".env.template", ".env.dist"} {
		class, _ := ClassifyPath(template)
		if class == PathDotenv {
			t.Fatalf("template %q must not classify as dotenv secret", template)
		}
	}
	// Real dotenv files stay classified as secrets, including compound suffixes
	// that merely end in an allowlisted word.
	for _, secret := range []string{".env", ".env.production", ".env.local", ".env.local.example"} {
		if class, _ := ClassifyPath(secret); class != PathDotenv {
			t.Fatalf("secret %q must classify as dotenv, got %q", secret, class)
		}
	}
}

func TestClassifyCommandAccessDetectsCredentialReads(t *testing.T) {
	credential := []string{
		"cat .env",
		"grep API_KEY .env.production",
		`python -c "print(open('.env').read())"`,
		"cp ~/.aws/credentials /tmp/x",
		"base64 ~/.ssh/id_rsa",
	}
	for _, command := range credential {
		if class, _ := ClassifyCommandAccess(command); class != CommandAccessCredential {
			t.Fatalf("command %q must flag credential access, got %q", command, class)
		}
	}
	benign := []string{
		"",
		"go test ./...",
		"cat README.md",
		"cat .env.example", // allowlisted template
		"git status",
	}
	for _, command := range benign {
		if class, _ := ClassifyCommandAccess(command); class != CommandAccessNone {
			t.Fatalf("command %q must not flag credential access, got %q", command, class)
		}
	}
}
