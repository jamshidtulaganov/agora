package handler

import (
	"strings"
	"testing"
)

func TestTaskModeInstructionFor(t *testing.T) {
	bug := taskModeInstructionFor("bug")
	for _, want := range []string{"BUG", "REPRODUCE", "ROOT CAUSE", "installed version", "PASSES"} {
		if !strings.Contains(bug, want) {
			t.Errorf("bug mode missing %q: %s", want, bug)
		}
	}

	feat := taskModeInstructionFor("feature")
	for _, want := range []string{"FEATURE", "DESIGN VARIANTS", "designer", "verify"} {
		if !strings.Contains(feat, want) {
			t.Errorf("feature mode missing %q: %s", want, feat)
		}
	}

	// chore + untyped get no mode clause (only the universal verify gate applies).
	if got := taskModeInstructionFor("chore"); got != "" {
		t.Errorf("chore mode should be empty, got: %s", got)
	}
	if got := taskModeInstructionFor(""); got != "" {
		t.Errorf("untyped mode should be empty, got: %s", got)
	}
}

func TestVerifyGateInstruction(t *testing.T) {
	v := verifyGateInstruction()
	for _, want := range []string{"VERIFY", "test", "build", "inspection"} {
		if !strings.Contains(v, want) {
			t.Errorf("verify gate missing %q: %s", want, v)
		}
	}
}
