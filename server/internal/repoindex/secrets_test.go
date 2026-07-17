package repoindex

import "testing"

// TestExclusionFloorDenies pins the default-deny floor. Every case here is a
// file that would leak a credential into an agent prompt (and from there to a
// closed model provider) if the floor regressed.
func TestExclusionFloorDenies(t *testing.T) {
	denied := []string{
		".env",
		".env.local",
		"config/.env.production",
		"app/settings.env",
		"deploy/server.pem",
		"certs/private.key",
		"keys/id_rsa",
		"keys/id_ed25519.pub",
		"src/aws_credentials.json",
		"config/secret_values.yaml",
		"app/api_key.txt",
		"db/passwords.sql",
		".ssh/config",
		".git/config",
		".aws/credentials",
		"nested/.hidden/file.go",
		"web/.npmrc",
		"conf/.htpasswd",
		"store/keystore.jks",
	}
	for _, path := range denied {
		if !isDeniedPath(path) {
			t.Errorf("isDeniedPath(%q) = false, want true — floor leak", path)
		}
	}
}

// TestExclusionFloorAllows guards the other direction: an over-broad floor
// silently empties the index and the pack degrades to useless.
func TestExclusionFloorAllows(t *testing.T) {
	allowed := []string{
		"server/internal/handler/daemon.go",
		"packages/views/issues/components/board-view.tsx",
		"app/Models/User.php",
		"server/migrations/151_issue_archived_at.up.sql",
		"README.md",
		"src/environment.ts",
		"internal/keyboard/handler.go",
	}
	for _, path := range allowed {
		if isDeniedPath(path) {
			t.Errorf("isDeniedPath(%q) = true, want false — floor too broad", path)
		}
	}
}

func TestIsDeniedDir(t *testing.T) {
	for _, name := range []string{".git", ".ssh", ".vscode", "node_modules", "vendor", "__pycache__"} {
		if !isDeniedDir(name) {
			t.Errorf("isDeniedDir(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"server", "internal", "src", "components"} {
		if isDeniedDir(name) {
			t.Errorf("isDeniedDir(%q) = true, want false", name)
		}
	}
}
