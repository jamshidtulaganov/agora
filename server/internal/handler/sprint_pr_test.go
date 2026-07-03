package handler

import (
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
