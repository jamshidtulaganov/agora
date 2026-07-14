package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// createTestProject inserts a bare project in the test workspace and returns its
// UUID, cleaning it up when the test ends. Generic project fixture, previously
// shared from the (now removed) connected-box test suite.
func createTestProject(t *testing.T, ctx context.Context, title string) pgtype.UUID {
	t.Helper()
	var id string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title, status) VALUES ($1, $2, 'planned') RETURNING id`,
		testWorkspaceID, title).Scan(&id); err != nil {
		t.Fatalf("create test project: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM project WHERE id=$1`, id) })
	return testUUID(id)
}

func TestQALocalDirectoryClause(t *testing.T) {
	got := qaLocalDirectoryClause("/Users/dev/code/app")
	for _, needle := range []string{
		"/Users/dev/code/app",
		"AGORA_DAEMON_PORT",
		"/editor/preview",
		"qa-target:",
		"TREE SAFETY",
		"worktree add",
	} {
		if !strings.Contains(got, needle) {
			t.Errorf("clause missing %q:\n%s", needle, got)
		}
	}
	// Must forbid mutating the user's tree for the baseline.
	if !strings.Contains(got, "NEVER") {
		t.Errorf("clause should forbid git mutation of the user's tree:\n%s", got)
	}
}

// setWorkspaceLabsQADevRuntimes flips the workspace labs flag for the duration
// of a test and restores the prior settings on cleanup so parallel/subsequent
// tests sharing testWorkspaceID are unaffected.
func setWorkspaceLabsQADevRuntimes(t *testing.T, on bool) {
	t.Helper()
	var prior []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT settings FROM workspace WHERE id = $1`, testWorkspaceID).Scan(&prior); err != nil {
		t.Fatalf("read workspace settings: %v", err)
	}
	blob := `{"labs":{"qa_dev_runtimes":false}}`
	if on {
		blob = `{"labs":{"qa_dev_runtimes":true}}`
	}
	if _, err := testPool.Exec(context.Background(),
		`UPDATE workspace SET settings = $2::jsonb WHERE id = $1`, testWorkspaceID, blob); err != nil {
		t.Fatalf("set workspace labs: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`UPDATE workspace SET settings = $2 WHERE id = $1`, testWorkspaceID, prior)
	})
}

// seedLocalDirectoryResource inserts a local_directory project_resource row
// directly (bypassing the human-gated handler — this is fixture setup).
func seedLocalDirectoryResource(t *testing.T, projectID pgtype.UUID, localPath, daemonID string) {
	t.Helper()
	ref := `{"local_path":"` + localPath + `","daemon_id":"` + daemonID + `","label":"test"}`
	var id string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO project_resource (workspace_id, project_id, resource_type, resource_ref)
		VALUES ($1, $2, 'local_directory', $3::jsonb) RETURNING id
	`, testWorkspaceID, projectID, ref).Scan(&id); err != nil {
		t.Fatalf("seed local_directory resource: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM project_resource WHERE id = $1`, id)
	})
}

func TestLocalDirectoryQATarget(t *testing.T) {
	ctx := context.Background()
	projectID := createTestProject(t, ctx, "local-dir-qa-target")
	issue := db.Issue{
		WorkspaceID: testUUID(testWorkspaceID),
		ProjectID:   projectID,
	}

	t.Run("resolves when labs on and daemon online", func(t *testing.T) {
		setWorkspaceLabsQADevRuntimes(t, true)
		createHandlerTestDaemonRuntime(t, "ld-daemon-online", "claude", testUserID, "private")
		seedLocalDirectoryResource(t, projectID, "/Users/dev/app", "ld-daemon-online")

		did, lp, ok := testHandler.localDirectoryQATarget(ctx, issue)
		if !ok {
			t.Fatal("expected a resolved local_directory QA target")
		}
		if did != "ld-daemon-online" {
			t.Errorf("daemonID = %q", did)
		}
		if lp != "/Users/dev/app" {
			t.Errorf("localPath = %q", lp)
		}
	})

	t.Run("resolves even when labs off (local_directory is the opt-in)", func(t *testing.T) {
		// A local_directory is a deliberate per-project resource; its presence
		// on an online daemon is itself the signal to QA locally, regardless of
		// the labs qa_dev_runtimes toggle.
		setWorkspaceLabsQADevRuntimes(t, false)
		createHandlerTestDaemonRuntime(t, "ld-daemon-labsoff", "claude", testUserID, "private")
		seedLocalDirectoryResource(t, projectID, "/Users/dev/app", "ld-daemon-labsoff")

		did, _, ok := testHandler.localDirectoryQATarget(ctx, issue)
		if !ok {
			t.Fatal("local_directory on an online daemon must resolve even with labs off")
		}
		if did != "ld-daemon-labsoff" {
			t.Errorf("daemonID = %q", did)
		}
	})

	t.Run("misses when daemon offline", func(t *testing.T) {
		setWorkspaceLabsQADevRuntimes(t, true)
		// Resource points at a daemon_id with no online runtime row.
		seedLocalDirectoryResource(t, projectID, "/Users/dev/app", "ld-daemon-never-registered")

		if _, _, ok := testHandler.localDirectoryQATarget(ctx, issue); ok {
			t.Error("offline/unknown daemon must not resolve")
		}
	})
}
