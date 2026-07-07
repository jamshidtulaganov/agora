package handler

import (
	"context"
	"strings"
	"testing"
)

// A project with no manifest of its own inherits the workspace-default
// project's manifest (labs.qa_default_manifest_project) so its QA runs still
// get a navigation map. Own manifest wins; self-reference never loops.
func TestQAManifestInheritance(t *testing.T) {
	ctx := context.Background()

	// Default (parent) project WITH a manifest.
	var parentID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title, status, priority, settings)
		 VALUES ($1::uuid,'inh-parent','planned','none',
		 '{"qa_manifest":{"base_url":"https://sandbox.x","auth":{"login_path":"/site/login","username":"demo","password":"123456"},"routes":{"dash":"/"}}}'::jsonb)
		 RETURNING id::text`, testWorkspaceID).Scan(&parentID); err != nil {
		t.Fatalf("parent: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM project WHERE id=$1::uuid`, parentID) })

	// Point the workspace default at the parent.
	if _, err := testPool.Exec(ctx,
		`UPDATE workspace SET settings = jsonb_set(COALESCE(settings,'{}'::jsonb),'{labs}',
		 COALESCE(settings->'labs','{}'::jsonb) || jsonb_build_object('qa_default_manifest_project',$2::text), true)
		 WHERE id=$1::uuid`, testWorkspaceID, parentID); err != nil {
		t.Fatalf("set default: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `UPDATE workspace SET settings = settings #- '{labs,qa_default_manifest_project}' WHERE id=$1::uuid`, testWorkspaceID)
	})

	// Child project with NO manifest + an issue.
	var childID, iid string
	testPool.QueryRow(ctx, `INSERT INTO project (workspace_id,title,status,priority) VALUES ($1::uuid,'inh-child','planned','none') RETURNING id::text`, testWorkspaceID).Scan(&childID)
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM project WHERE id=$1::uuid`, childID) })
	testPool.QueryRow(ctx, `INSERT INTO issue (workspace_id,project_id,title,creator_type,creator_id) VALUES ($1::uuid,$2::uuid,'inh-issue','member',$3::uuid) RETURNING id::text`, testWorkspaceID, childID, testUserID).Scan(&iid)
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id=$1::uuid`, iid) })

	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(iid))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	got := testHandler.sliceActionQAManifestContext(ctx, issue)
	if !strings.Contains(got, "INHERITED") || !strings.Contains(got, "/site/login") {
		t.Fatalf("child did not inherit parent manifest\ngot: %s", got)
	}
}
