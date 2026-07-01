package handler

import "testing"

// The guard exists because a sprint whose integration branch is the prod branch
// (SalesDoctor's "billing") makes the sprint machinery — worktree checkout,
// DeploySprintBranch, auto-QA — target production directly. Sprints must use
// their OWN dedicated branch cut off prod; a human merges it back at sprint end.
func TestSprintBranchRejected(t *testing.T) {
	t.Setenv("AGORA_PROTECTED_SPRINT_BRANCHES", "billing, Release ")

	cases := []struct {
		branch string
		want   bool
	}{
		// Empty is allowed — falls back to the sprint/<id> convention.
		{"", false},
		{"   ", false},
		// Built-in prod backstops (case-insensitive).
		{"master", true},
		{"Master", true},
		{"main", true},
		{"production", true},
		{"prod", true},
		// Env-configured protected names, trimmed + case-insensitive.
		{"billing", true},
		{"BILLING", true},
		{" billing ", true},
		{"release", true},
		// A real dedicated sprint branch is allowed.
		{"sprint-10", false},
		{"sprint/2026-07", false},
		{"feature/x", false},
	}
	for _, c := range cases {
		if got := sprintBranchRejected(c.branch); got != c.want {
			t.Errorf("sprintBranchRejected(%q) = %v, want %v", c.branch, got, c.want)
		}
	}
}

// Without any env override, the built-in prod names are still rejected and a
// generic dedicated branch is still allowed — the backstop works out of the box.
func TestSprintBranchRejected_DefaultBackstop(t *testing.T) {
	t.Setenv("AGORA_PROTECTED_SPRINT_BRANCHES", "")
	if !sprintBranchRejected("main") {
		t.Error("expected 'main' rejected by the default backstop")
	}
	if sprintBranchRejected("sprint-10") {
		t.Error("expected 'sprint-10' allowed")
	}
	// billing is only protected when configured — without the env it is NOT a
	// built-in, so this documents that prod must set AGORA_PROTECTED_SPRINT_BRANCHES.
	if sprintBranchRejected("billing") {
		t.Error("expected 'billing' allowed when not in the env list (must be configured)")
	}
}
