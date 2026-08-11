package handler

import (
	"strings"
	"testing"
)

func TestTaskModeInstructionFor(t *testing.T) {
	bug := taskModeInstructionFor("bug")
	for _, want := range []string{"debugging", "REPRODUCE", "ROOT CAUSE", "installed version", "PASSES"} {
		if !strings.Contains(bug, want) {
			t.Errorf("bug mode missing %q: %s", want, bug)
		}
	}

	feat := taskModeInstructionFor("feature")
	for _, want := range []string{"planning", "DESIGN VARIANTS", "designer", "acceptance"} {
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

func TestResolveWorkMode(t *testing.T) {
	cases := []struct {
		labels []string
		want   string
	}{
		{[]string{"mode:debugging"}, "debugging"},
		{[]string{"mode:planning"}, "planning"},
		{[]string{"mode:debugging", "mode:planning"}, "debugging"},
		{[]string{"type:bug"}, "debugging"},
		{[]string{"type:feature"}, "planning"},
		{[]string{"type:question"}, "planning"},
		{[]string{"mode:planning", "type:bug"}, "planning"}, // explicit mode wins
		{[]string{"urgent"}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := resolveWorkMode(c.labels); got != c.want {
			t.Errorf("resolveWorkMode(%v) = %q, want %q", c.labels, got, c.want)
		}
	}
}

func TestDraftCodeModeInstructionPrefersExplicitMode(t *testing.T) {
	got := draftCodeModeInstruction([]string{"type:bug", "mode:planning"})
	if !strings.Contains(got, "mode:planning") || strings.Contains(got, "mode:debugging") {
		t.Fatalf("explicit planning mode should win over type:bug; got %s", got)
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
