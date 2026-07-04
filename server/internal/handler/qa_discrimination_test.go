package handler

import (
	"context"
	"testing"
)

// HasDiscriminatingRunForIssue is the core of the test-accuracy gate: an issue
// has real evidence only when a latest-per-case run PASSED on the branch AND
// FAILED on the pre-change baseline. A test green on both proves nothing.
func TestHasDiscriminatingRunForIssue(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ctx := context.Background()

	mkIssue := func() string {
		var pid, iid string
		if err := testPool.QueryRow(ctx,
			`INSERT INTO project (workspace_id, title, status, priority) VALUES ($1::uuid,'disc-p-'||gen_random_uuid(),'planned','none') RETURNING id::text`,
			testWorkspaceID).Scan(&pid); err != nil {
			t.Fatal(err)
		}
		if err := testPool.QueryRow(ctx,
			`INSERT INTO issue (workspace_id, project_id, title, creator_type, creator_id, number) VALUES ($1::uuid,$2::uuid,'disc issue','member',$3::uuid,(4000000+floor(random()*1000000))::int) RETURNING id::text`,
			testWorkspaceID, pid, testUserID).Scan(&iid); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			testPool.Exec(ctx, `DELETE FROM issue WHERE id=$1::uuid`, iid)
			testPool.Exec(ctx, `DELETE FROM project WHERE id=$1::uuid`, pid)
		})
		return iid
	}
	// mkRun creates a test_case + a test_run with the given branch + baseline status.
	mkRun := func(iid, status, baseline string) {
		var cid string
		if err := testPool.QueryRow(ctx,
			`INSERT INTO test_case (workspace_id, issue_id, title, steps, expected, kind, source, author_type, category)
			 VALUES ($1::uuid,$2::uuid,'c','s','e','automated','agent','agent','positive') RETURNING id::text`,
			testWorkspaceID, iid).Scan(&cid); err != nil {
			t.Fatal(err)
		}
		if _, err := testPool.Exec(ctx,
			`INSERT INTO test_run (workspace_id, test_case_id, issue_id, status, output, run_source, run_by_type, baseline_status)
			 VALUES ($1::uuid,$2::uuid,$3::uuid,$4,'',' agent','agent',$5)`,
			testWorkspaceID, cid, iid, status, baseline); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM test_case WHERE id=$1::uuid`, cid) })
	}
	has := func(iid string) bool {
		ok, err := testHandler.Queries.HasDiscriminatingRunForIssue(ctx, parseUUID(iid))
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}

	// (a) pass on branch + FAIL on baseline → discriminating.
	a := mkIssue()
	mkRun(a, "pass", "fail")
	if !has(a) {
		t.Error("pass+baseline:fail must discriminate")
	}
	// (b) pass on BOTH → non-discriminating.
	b := mkIssue()
	mkRun(b, "pass", "pass")
	if has(b) {
		t.Error("pass on both baseline+branch must NOT discriminate")
	}
	// (c) only unknown baseline → not counted.
	c := mkIssue()
	mkRun(c, "pass", "unknown")
	if has(c) {
		t.Error("unknown baseline must not count as discriminating")
	}
	// (d) mixed: one non-discriminating + one discriminating → true.
	d := mkIssue()
	mkRun(d, "pass", "pass")
	mkRun(d, "pass", "fail")
	if !has(d) {
		t.Error("any discriminating case makes the issue discriminating")
	}
}
