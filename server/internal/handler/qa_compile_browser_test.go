package handler

import (
	"context"
	"strings"
	"testing"
)

// TestCompileInstructionRequiresBrowserDrivenUICases asserts the compile_tests
// directive forces a UI / [e2e] case to actually DRIVE the browser (connectOverCDP
// to the shared review browser + real page interactions) rather than shortcutting
// it with a raw fetch of the HTML. That shortcut is exactly why the first live
// runs authored HTTP/filesystem assertions — nothing ever opened in the review
// page's live pane. Pure API/[api] cases may still use HTTP. Pure (no DB).
func TestCompileInstructionRequiresBrowserDrivenUICases(t *testing.T) {
	s := buildSliceInstruction(sliceActionCompileTests, "")
	for _, want := range []string{
		"connectOverCDP",         // attach to the SHARED review browser (watchable live)
		"BROWSER-DRIVE UI CASES", // the explicit requirement
		"page.goto",              // real navigation, not a raw fetch
		"[e2e]",                  // UI cases MUST browser-drive
		"[api]",                  // pure API/data cases may stay HTTP
	} {
		if !strings.Contains(s, want) {
			t.Errorf("compile_tests instruction missing %q", want)
		}
	}
}

// TestQAManifestContext_InjectsRoleAccounts covers the role-account fix for the
// "QA_AGENT_LOGIN not set / ROLE=4 required" block: a manifest account is injected
// into the QA instruction (role + creds + when-to-use) so the agent logs in with
// the right account for auth'd flows the default admin login can't reach.
func TestQAManifestContext_InjectsRoleAccounts(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ctx := context.Background()
	manifest := `{"qa_manifest":{"base_url":"https://x","auth":{"login_path":"/login","user_field":"login","pass_field":"password","username":"demo","password":"123456"},"accounts":[{"role":"agent (ROLE=4)","username":"agent1","password":"s3cret","note":"for /api3/stock/*"}]}}`
	var pid, iid string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title, status, priority, settings)
		 VALUES ($1::uuid, 'acct-proj', 'planned', 'none', $2::jsonb) RETURNING id::text`,
		testWorkspaceID, manifest).Scan(&pid); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := testPool.QueryRow(ctx,
		`INSERT INTO issue (workspace_id, project_id, title, creator_type, creator_id)
		 VALUES ($1::uuid, $2::uuid, 'acct-issue', 'member', $3::uuid) RETURNING id::text`,
		testWorkspaceID, pid, testUserID).Scan(&iid); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1::uuid`, iid)
		testPool.Exec(ctx, `DELETE FROM project WHERE id = $1::uuid`, pid)
	})

	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(iid))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	got := testHandler.sliceActionQAManifestContext(ctx, issue)
	for _, want := range []string{"ACCOUNT [agent (ROLE=4)]", "agent1", "s3cret", "for /api3/stock"} {
		if !strings.Contains(got, want) {
			t.Errorf("manifest context missing %q\ngot: %s", want, got)
		}
	}
}
