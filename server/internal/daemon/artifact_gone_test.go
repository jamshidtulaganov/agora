package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitInitDir makes dir a real git work tree (isGitWorkTree shells out to git, so
// a fake .git folder would not do).
func gitInitDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if out, err := exec.Command("git", "-C", dir, "init", "--quiet").CombinedOutput(); err != nil {
		t.Fatalf("git init %s: %v: %s", dir, err, out)
	}
}

// TestArtifactRepoPathResolution covers every shape a finished run leaves behind.
// The last case is the SD-25 report: the checkout is gone, and the surface must
// say "gone" (so the UI explains it) instead of returning a bare error that
// renders as a red load failure with a Refresh button that cannot help.
func TestArtifactRepoPathResolution(t *testing.T) {
	ctx := context.Background()

	t.Run("single-repo grant whose work dir IS the checkout", func(t *testing.T) {
		root := t.TempDir()
		gitInitDir(t, root)
		grant := ArtifactCapabilityGrant{SourceRoot: root, Repos: []ArtifactRepoRef{{Repo: "app"}}}
		got, err := artifactRepoPath(ctx, grant, ArtifactRepoRef{Repo: "app"})
		if err != nil || got != root {
			t.Fatalf("got (%q, %v), want (%q, nil)", got, err, root)
		}
	})

	t.Run("multi-repo grant resolves the named subdirectory", func(t *testing.T) {
		root := t.TempDir()
		repo := filepath.Join(root, "web")
		gitInitDir(t, repo)
		grant := ArtifactCapabilityGrant{SourceRoot: root, Repos: []ArtifactRepoRef{{Repo: "web"}, {Repo: "api"}}}
		got, err := artifactRepoPath(ctx, grant, ArtifactRepoRef{Repo: "web"})
		if err != nil || got != repo {
			t.Fatalf("got (%q, %v), want (%q, nil)", got, err, repo)
		}
	})

	t.Run("multi-repo grant falls back to the work dir when it is the checkout", func(t *testing.T) {
		// A run recorded two repos but actually checked one out directly into the
		// work dir. The diff is right there, so dead-ending would be a bug.
		root := t.TempDir()
		gitInitDir(t, root)
		grant := ArtifactCapabilityGrant{SourceRoot: root, Repos: []ArtifactRepoRef{{Repo: "web"}, {Repo: "api"}}}
		got, err := artifactRepoPath(ctx, grant, ArtifactRepoRef{Repo: "api"})
		if err != nil || got != root {
			t.Fatalf("got (%q, %v), want (%q, nil)", got, err, root)
		}
	})

	t.Run("traversal in the repo name is rejected, not resolved", func(t *testing.T) {
		root := t.TempDir()
		grant := ArtifactCapabilityGrant{SourceRoot: root, Repos: []ArtifactRepoRef{{Repo: "a"}, {Repo: "b"}}}
		if _, err := artifactRepoPath(ctx, grant, ArtifactRepoRef{Repo: "../etc"}); err == nil {
			t.Fatal("a traversing repo name must be rejected")
		} else if artifactIsGone(err) {
			t.Fatal("an invalid repo name is a bad request, not a gone runtime")
		}
	})

	t.Run("cleaned-up checkout reports gone", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "torn-down")
		grant := ArtifactCapabilityGrant{SourceRoot: root, Repos: []ArtifactRepoRef{{Repo: "web"}, {Repo: "api"}}}
		_, err := artifactRepoPath(ctx, grant, ArtifactRepoRef{Repo: "web"})
		if err == nil {
			t.Fatal("a missing checkout must be an error")
		}
		if !artifactIsGone(err) {
			t.Fatalf("a missing checkout must be reported as gone, got %v", err)
		}
	})

	t.Run("empty source root reports gone", func(t *testing.T) {
		grant := ArtifactCapabilityGrant{SourceRoot: "  ", Repos: []ArtifactRepoRef{{Repo: "web"}}}
		_, err := artifactRepoPath(ctx, grant, ArtifactRepoRef{Repo: "web"})
		if !artifactIsGone(err) {
			t.Fatalf("an empty source root must be reported as gone, got %v", err)
		}
	})
}

// TestWriteArtifactError pins the wire contract the UI branches on: a gone error
// is a 410 with a JSON reason; anything else keeps the plain-text shape older
// clients already handle.
func TestWriteArtifactError(t *testing.T) {
	t.Run("gone becomes a structured 410", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writeArtifactError(rec, errArtifactRuntimeGone("artifact repository is unavailable"), http.StatusGone)
		if rec.Code != http.StatusGone {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusGone)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q, want application/json", ct)
		}
		var body struct {
			Reason string `json:"reason"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("body is not JSON: %v (%s)", err, rec.Body.String())
		}
		if body.Reason != artifactRuntimeGoneReason {
			t.Errorf("reason = %q, want %q", body.Reason, artifactRuntimeGoneReason)
		}
		if body.Error == "" {
			t.Error("the human-readable error must survive alongside the reason")
		}
	})

	t.Run("other failures keep the plain-text shape", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writeArtifactError(rec, errNotJSON, http.StatusNotFound)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
		}
		if got := rec.Body.String(); got == "" || got[0] == '{' {
			t.Errorf("body = %q, want plain text", got)
		}
	})
}

// errNotJSON is a plain error used to assert the non-gone branch.
var errNotJSON = &plainError{"invalid repository name"}

type plainError struct{ msg string }

func (e *plainError) Error() string { return e.msg }
