package handler

import (
	"strings"
	"testing"
)

func TestDefaultOrchestratorInstructionsSane(t_ *testing.T) {
	d := defaultOrchestratorInstructions
	if strings.TrimSpace(d) == "" {
		t_.Fatal("default orchestrator instructions must not be empty")
	}
	for _, want := range []string{"orchestrator", "Decompose", "Delegate", "@mention", "difficulty"} {
		if !strings.Contains(d, want) {
			t_.Fatalf("default brief missing %q", want)
		}
	}
}

func TestComposeAlwaysStartsWithDefaultPolicy(t_ *testing.T) {
	got := composeTailoredOrchestratorInstructions("Web Team", nil)
	if !strings.HasPrefix(got, defaultOrchestratorInstructions) {
		t_.Fatal("tailored brief must start with the default policy")
	}
	if !strings.Contains(got, "## Your squad") {
		t_.Fatal("tailored brief must add a roster section")
	}
}

func TestComposeSquadNameInHeader(t_ *testing.T) {
	withName := composeTailoredOrchestratorInstructions("Web Team", nil)
	if !strings.Contains(withName, "## Your squad — Web Team") {
		t_.Fatalf("squad name should appear in the header: %q", tail(withName))
	}
	noName := composeTailoredOrchestratorInstructions("   ", nil)
	// Check the HEADER specifically — the default policy legitimately contains
	// em-dashes, so a whole-string scan would false-positive.
	if strings.Contains(noName, "## Your squad —") {
		t_.Fatalf("blank squad name must not leave a dangling dash in the header: %q", tail(noName))
	}
}

func TestComposeNoMembersBesidesLeader(t_ *testing.T) {
	for _, roster := range [][]squadRosterEntry{
		nil,
		{{name: "Lead Bot", role: "leader", isLeader: true}},
	} {
		got := composeTailoredOrchestratorInstructions("Solo", roster)
		if !strings.Contains(got, "no members besides you") {
			t_.Fatalf("empty/leader-only roster should say so: %q", tail(got))
		}
		if strings.Contains(got, "Members you delegate to") {
			t_.Fatalf("no delegate list should appear with no members: %q", tail(got))
		}
	}
}

func TestComposeListsMembersWithRolesAndDescriptions(t_ *testing.T) {
	roster := []squadRosterEntry{
		{name: "Lead Bot", role: "leader", isLeader: true},
		{name: "Backend Bot", role: "engineer", description: "Go + Postgres API work."},
		{name: "QA Bot", role: "member", description: "Playwright test authoring."},
	}
	got := composeTailoredOrchestratorInstructions("SD Web", roster)

	if !strings.Contains(got, "You are the leader (Lead Bot).") {
		t_.Fatalf("leader line missing: %q", tail(got))
	}
	if !strings.Contains(got, "Members you delegate to:") {
		t_.Fatalf("delegate list header missing: %q", tail(got))
	}
	// Non-default role is shown; "member" role is suppressed as noise.
	if !strings.Contains(got, "- Backend Bot (engineer) — Go + Postgres API work.") {
		t_.Fatalf("engineer member line wrong: %q", tail(got))
	}
	if !strings.Contains(got, "- QA Bot — Playwright test authoring.") {
		t_.Fatalf("member-role suffix should be omitted: %q", tail(got))
	}
	if strings.Contains(got, "QA Bot (member)") {
		t_.Fatal("the generic 'member' role must not be rendered")
	}
	// The leader is never listed as a delegate target.
	if strings.Contains(got, "- Lead Bot") {
		t_.Fatal("leader must not appear in the delegate list")
	}
}

func TestOneLineCollapsesAndTruncates(t_ *testing.T) {
	if got := oneLine("  a\n\tb   c  ", 100); got != "a b c" {
		t_.Fatalf("whitespace collapse = %q", got)
	}
	long := strings.Repeat("x", 300)
	got := oneLine(long, 160)
	if len([]rune(got)) > 161 || !strings.HasSuffix(got, "…") {
		t_.Fatalf("truncation failed: len=%d suffix=%q", len([]rune(got)), got[len(got)-3:])
	}
	// A member with a huge description must not blow up the one-line row.
	roster := []squadRosterEntry{{name: "Verbose Bot", role: "x", description: long}}
	line := composeTailoredOrchestratorInstructions("S", roster)
	if strings.Contains(line, strings.Repeat("x", 200)) {
		t_.Fatal("member description was not truncated in the composed brief")
	}
}

// tail returns the roster section for readable failure messages.
func tail(s string) string {
	if i := strings.Index(s, "## Your squad"); i >= 0 {
		return s[i:]
	}
	return s
}
