package handler

import (
	"context"
	"strings"
	"testing"
)

// TestSprintPRInstruction asserts the sprint-PR-mode dev directive carries the
// invariants that keep the flow correct: the PR base is the sprint branch itself
// (never a sprint-wt-* alias, never main), with a self-correcting `gh pr edit`
// guard for the base — the fix for a dev opening the PR against the per-task
// alias — and the agent must not self-merge. Pure (no DB).
func TestSprintPRInstruction(t *testing.T) {
	s := sprintPRInstruction("sprint10")
	for _, want := range []string{
		"sprint10",                     // the sprint branch is named
		"--base sprint10",              // PR targets the sprint branch
		"do NOT push onto",             // never push straight onto the shared branch
		"sprint-wt-*",                  // must NOT target the per-task alias
		"gh pr edit",                   // self-correct the base if it resolved wrong
		"Do NOT merge the PR yourself", // reviewed + merged after QA, not by the dev
		"main/default branch",          // never target main
	} {
		if !strings.Contains(s, want) {
			t.Errorf("sprintPRInstruction missing %q\ngot: %s", want, s)
		}
	}
}

// TestSprintPRModeEnabled covers the env gate: off by default, on for "1"/"true"
// (case-insensitive), so the switch is explicit and reversible. Pure (no DB).
func TestSprintPRModeEnabled(t *testing.T) {
	cases := []struct {
		val  string
		want bool
	}{
		{"", false},
		{"false", false},
		{"0", false},
		{"1", true},
		{"true", true},
		{"TRUE", true},
	}
	for _, c := range cases {
		t.Setenv("AGORA_SPRINT_PR_MODE", c.val)
		if got := sprintPRModeEnabled(); got != c.want {
			t.Errorf("sprintPRModeEnabled(%q) = %v, want %v", c.val, got, c.want)
		}
	}
}

// TestSliceActionLandingInstruction_SprintPRMode is the end-to-end wiring check
// for Phase 1: a real sprint-mode issue (project sprint_mode=on + a sprint on
// branch sprint10 + issue_to_sprint) resolves the sprint branch, and the flag
// then selects the PR-into-sprint directive vs the direct-commit one. Proves the
// DB-dependent selection, not just the pure instruction text.
func TestSliceActionLandingInstruction_SprintPRMode(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ctx := context.Background()
	// sliceActionSprintContext short-circuits unless the worktree model is on.
	t.Setenv("AGORA_SPRINT_WORKTREE_ENABLED", "true")

	var pid, sid, iid string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title, status, priority, settings)
		 VALUES ($1::uuid, 'sprint-pr-proj', 'planned', 'none', '{"sprint_mode":true}'::jsonb)
		 RETURNING id::text`, testWorkspaceID).Scan(&pid); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := testPool.QueryRow(ctx,
		`INSERT INTO sprint (workspace_id, project_id, name, status, branch)
		 VALUES ($1::uuid, $2::uuid, 'Sprint 10', 'active', 'sprint10')
		 RETURNING id::text`, testWorkspaceID, pid).Scan(&sid); err != nil {
		t.Fatalf("create sprint: %v", err)
	}
	if err := testPool.QueryRow(ctx,
		`INSERT INTO issue (workspace_id, project_id, title, status, creator_type, creator_id)
		 VALUES ($1::uuid, $2::uuid, 'sprint pr issue', 'todo', 'member', $3::uuid)
		 RETURNING id::text`, testWorkspaceID, pid, testUserID).Scan(&iid); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	if _, err := testPool.Exec(ctx,
		`INSERT INTO issue_to_sprint (issue_id, sprint_id) VALUES ($1::uuid, $2::uuid)`, iid, sid); err != nil {
		t.Fatalf("link issue to sprint: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM issue_to_sprint WHERE issue_id = $1::uuid`, iid)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1::uuid`, iid)
		testPool.Exec(ctx, `DELETE FROM sprint WHERE id = $1::uuid`, sid)
		testPool.Exec(ctx, `DELETE FROM project WHERE id = $1::uuid`, pid)
	})

	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(iid))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}

	// Flag ON → open a PR into the sprint branch.
	t.Setenv("AGORA_SPRINT_PR_MODE", "true")
	on := testHandler.sliceActionLandingInstruction(ctx, issue)
	if !strings.Contains(on, "SPRINT MODE (PR REVIEW)") || !strings.Contains(on, "--base sprint10") {
		t.Errorf("flag on: expected PR-into-sprint directive, got: %s", on)
	}

	// Flag OFF → commit straight onto the sprint branch (no PR).
	t.Setenv("AGORA_SPRINT_PR_MODE", "")
	off := testHandler.sliceActionLandingInstruction(ctx, issue)
	if !strings.Contains(off, "DO NOT OPEN A PULL REQUEST") {
		t.Errorf("flag off: expected direct-commit sprint directive, got: %s", off)
	}
}

// TestMaybeMergeOnQAPass_RoutesLeadToMerge is the Phase 3 gate: when a squad-
// assigned sprint task's PR passes QA (qa:pass) and AGORA_SPRINT_PR_MODE is on,
// the squad LEAD is routed a review+merge directive (a comment mentioning the
// lead with a `gh pr merge` into the sprint branch). Off / wrong label → no-op.
func TestMaybeMergeOnQAPass_RoutesLeadToMerge(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ctx := context.Background()

	leaderID := createHandlerTestAgent(t, "sprint-merge-lead", []byte("[]"))
	var squadID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO squad (workspace_id, name, leader_id, creator_id)
		 VALUES ($1::uuid, 'merge-squad', $2::uuid, $3::uuid) RETURNING id::text`,
		testWorkspaceID, leaderID, testUserID).Scan(&squadID); err != nil {
		t.Fatalf("create squad: %v", err)
	}
	var pid, sid, iid string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title, status, priority, settings)
		 VALUES ($1::uuid, 'merge-proj', 'planned', 'none', '{"sprint_mode":true}'::jsonb)
		 RETURNING id::text`, testWorkspaceID).Scan(&pid); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := testPool.QueryRow(ctx,
		`INSERT INTO sprint (workspace_id, project_id, name, status, branch)
		 VALUES ($1::uuid, $2::uuid, 'Sprint 10', 'active', 'sprint10') RETURNING id::text`,
		testWorkspaceID, pid).Scan(&sid); err != nil {
		t.Fatalf("create sprint: %v", err)
	}
	if err := testPool.QueryRow(ctx,
		`INSERT INTO issue (workspace_id, project_id, title, status, creator_type, creator_id, assignee_type, assignee_id)
		 VALUES ($1::uuid, $2::uuid, 'merge issue', 'in_review', 'member', $3::uuid, 'squad', $4::uuid)
		 RETURNING id::text`, testWorkspaceID, pid, testUserID, squadID).Scan(&iid); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	if _, err := testPool.Exec(ctx,
		`INSERT INTO issue_to_sprint (issue_id, sprint_id) VALUES ($1::uuid, $2::uuid)`, iid, sid); err != nil {
		t.Fatalf("link issue to sprint: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM comment WHERE issue_id = $1::uuid`, iid)
		testPool.Exec(ctx, `DELETE FROM issue_to_sprint WHERE issue_id = $1::uuid`, iid)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1::uuid`, iid)
		testPool.Exec(ctx, `DELETE FROM sprint WHERE id = $1::uuid`, sid)
		testPool.Exec(ctx, `DELETE FROM squad WHERE id = $1::uuid`, squadID)
		testPool.Exec(ctx, `DELETE FROM project WHERE id = $1::uuid`, pid)
	})

	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(iid))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}

	mergeComments := func() int {
		var n int
		testPool.QueryRow(ctx,
			`SELECT count(*) FROM comment WHERE issue_id = $1::uuid AND content LIKE '%gh pr merge%'`, iid).Scan(&n)
		return n
	}

	humanNotes := func() int {
		var n int
		testPool.QueryRow(ctx,
			`SELECT count(*) FROM comment WHERE issue_id = $1::uuid AND content LIKE '%READY FOR HUMAN%'`, iid).Scan(&n)
		return n
	}

	// PR mode OFF → no routing at all.
	t.Setenv("AGORA_SPRINT_PR_MODE", "")
	testHandler.maybeMergeOnQAPass(ctx, issue, "qa:pass", testUserID)
	if mergeComments()+humanNotes() != 0 {
		t.Fatalf("pr-mode off: expected no routing")
	}

	// PR mode on, wrong label → still no-op.
	t.Setenv("AGORA_SPRINT_PR_MODE", "true")
	testHandler.maybeMergeOnQAPass(ctx, issue, "qa:fail", testUserID)
	if mergeComments()+humanNotes() != 0 {
		t.Fatalf("qa:fail: expected no routing")
	}

	// qa:pass, auto-merge OFF (default) → a HUMAN-facing "ready to merge" note,
	// with NO agent mention (no agent acts) and NO gh pr merge directive.
	testHandler.maybeMergeOnQAPass(ctx, issue, "qa:pass", testUserID)
	if humanNotes() != 1 {
		t.Fatalf("auto-merge off: expected one human-merge note, got %d", humanNotes())
	}
	if mergeComments() != 0 {
		t.Fatalf("auto-merge off: must NOT emit a gh pr merge directive")
	}
	var note string
	testPool.QueryRow(ctx,
		`SELECT content FROM comment WHERE issue_id = $1::uuid AND content LIKE '%READY FOR HUMAN%' ORDER BY created_at DESC LIMIT 1`, iid).Scan(&note)
	if strings.Contains(note, "mention://agent/") {
		t.Errorf("human-merge note must not @mention an agent\ngot: %s", note)
	}

	// qa:pass, auto-merge ON → the lead gets a gh pr merge directive into sprint10.
	t.Setenv("AGORA_SPRINT_AUTO_MERGE", "true")
	testHandler.maybeMergeOnQAPass(ctx, issue, "qa:pass", testUserID)
	var content string
	if err := testPool.QueryRow(ctx,
		`SELECT content FROM comment WHERE issue_id = $1::uuid AND content LIKE '%gh pr merge%' ORDER BY created_at DESC LIMIT 1`,
		iid).Scan(&content); err != nil {
		t.Fatalf("auto-merge on: expected a merge directive comment, none found: %v", err)
	}
	for _, want := range []string{"mention://agent/" + leaderID, "sprint10", "gh pr merge"} {
		if !strings.Contains(content, want) {
			t.Errorf("merge directive missing %q\ngot: %s", want, content)
		}
	}
}
