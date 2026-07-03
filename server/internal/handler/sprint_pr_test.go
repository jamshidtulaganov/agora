package handler

import (
	"context"
	"strings"
	"testing"
)

// TestSprintPRInstruction asserts the sprint-PR-mode dev directive carries the
// invariants that keep the flow correct: a PR INTO the sprint branch (never a
// push onto it, never a PR into main), and the agent must not self-merge — the
// squad lead owns the merge. Pure (no DB).
func TestSprintPRInstruction(t *testing.T) {
	s := sprintPRInstruction("sprint10")
	for _, want := range []string{
		"sprint10",                 // the sprint branch is named
		"--base sprint10",          // PR targets the sprint branch
		"do NOT push onto",         // never push straight onto the shared branch
		"do NOT merge it yourself", // the lead merges, not the dev
		"main/default branch",      // never target main
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
