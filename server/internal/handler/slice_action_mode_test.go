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
	if strings.Contains(bug, "mode:debugging") {
		t.Error("bug instruction must not reference mode:* labels")
	}

	feat := taskModeInstructionFor("feature")
	for _, want := range []string{"FEATURE", "DESIGN VARIANTS", "designer", "acceptance"} {
		if !strings.Contains(feat, want) {
			t.Errorf("feature mode missing %q: %s", want, feat)
		}
	}
	if strings.Contains(feat, "mode:planning") {
		t.Error("feature instruction must not reference mode:* labels")
	}

	// chore + untyped get no mode clause (only the universal verify gate applies).
	if got := taskModeInstructionFor("chore"); got != "" {
		t.Errorf("chore mode should be empty, got: %s", got)
	}
	if got := taskModeInstructionFor(""); got != "" {
		t.Errorf("untyped mode should be empty, got: %s", got)
	}
}

func TestResolveTaskType(t *testing.T) {
	cases := []struct {
		labels []string
		want   string
	}{
		{[]string{"type:bug"}, "bug"},
		{[]string{"type:feature"}, "feature"},
		{[]string{"type:question"}, "feature"}, // planning path via feature instructions
		{[]string{"type:chore"}, "chore"},
		{[]string{"urgent"}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := resolveTaskType(c.labels); got != c.want {
			t.Errorf("resolveTaskType(%v) = %q, want %q", c.labels, got, c.want)
		}
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
