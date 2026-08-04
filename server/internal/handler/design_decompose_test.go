package handler

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/service"
)

func sptrDesign(s string) *string { return &s }

func TestBuildEffectiveDesignPlan(t *testing.T) {
	proposal := service.DesignProposal{
		SubIssues: []service.DesignProposalSubIssue{
			{Title: "A", Description: "da", DependsOn: []int{}},
			{Title: "B", Description: "db", DependsOn: []int{0}},
			{Title: "C", Description: "dc", DependsOn: []int{0, 1}},
		},
	}

	t.Run("no overrides keeps all + deps", func(t *testing.T) {
		plan := buildEffectiveDesignPlan(proposal, nil)
		if len(plan) != 3 {
			t.Fatalf("got %d, want 3", len(plan))
		}
		if !reflect.DeepEqual(plan[2].dependsOn, []int{0, 1}) {
			t.Errorf("C deps = %v, want [0 1]", plan[2].dependsOn)
		}
	})

	t.Run("excluding index prunes it from sibling deps", func(t *testing.T) {
		// Exclude index 1 (B) → C's depends_on [0,1] becomes [0].
		plan := buildEffectiveDesignPlan(proposal, []designSubIssueOverride{
			{Index: 1, Include: false},
		})
		if len(plan) != 2 {
			t.Fatalf("got %d, want 2 (B excluded)", len(plan))
		}
		// The surviving C keeps original index 2 and deps pruned of 1.
		var c *effectiveDesignPlan
		for i := range plan {
			if plan[i].index == 2 {
				c = &plan[i]
			}
		}
		if c == nil {
			t.Fatal("C missing")
		}
		if !reflect.DeepEqual(c.dependsOn, []int{0}) {
			t.Errorf("C deps after excluding B = %v, want [0]", c.dependsOn)
		}
	})

	t.Run("invalid deps are dropped so the child is not stranded", func(t *testing.T) {
		// C.depends_on = [1, 5, 2] — 5 is out of range, 2 is self. Both dropped;
		// only the valid [1] survives, so C can be promoted on its real prereq.
		p := service.DesignProposal{
			SubIssues: []service.DesignProposalSubIssue{
				{Title: "A", DependsOn: []int{}},
				{Title: "B", DependsOn: []int{}},
				{Title: "C", DependsOn: []int{1, 5, 2}},
			},
		}
		plan := buildEffectiveDesignPlan(p, nil)
		var c *effectiveDesignPlan
		for i := range plan {
			if plan[i].index == 2 {
				c = &plan[i]
			}
		}
		if c == nil || !reflect.DeepEqual(c.dependsOn, []int{1}) {
			t.Fatalf("C deps = %v, want [1] (out-of-range 5 + self 2 dropped)", c.dependsOn)
		}
	})

	t.Run("title/description overrides applied", func(t *testing.T) {
		plan := buildEffectiveDesignPlan(proposal, []designSubIssueOverride{
			{Index: 0, Include: true, Title: sptrDesign("A-edited"), Description: sptrDesign("new desc")},
		})
		if plan[0].title != "A-edited" || plan[0].description != "new desc" {
			t.Errorf("override not applied: %+v", plan[0])
		}
	})
}

func TestParseDependsOnAndSatisfied(t *testing.T) {
	if got := parseDependsOn(""); got != nil {
		t.Errorf("empty → nil, got %v", got)
	}
	if got := parseDependsOn("0,2"); !reflect.DeepEqual(got, []int{0, 2}) {
		t.Errorf("got %v, want [0 2]", got)
	}
	if got := parseDependsOn(" 1 , 3 "); !reflect.DeepEqual(got, []int{1, 3}) {
		t.Errorf("whitespace: got %v, want [1 3]", got)
	}

	done := map[int]bool{0: true, 2: true}
	if !depsSatisfied([]int{0, 2}, done) {
		t.Error("[0,2] should be satisfied by {0,2}")
	}
	if depsSatisfied([]int{0, 1}, done) {
		t.Error("[0,1] should NOT be satisfied (1 not done)")
	}
	if !depsSatisfied(nil, done) {
		t.Error("no deps → always satisfied")
	}
}

func TestJoinInts(t *testing.T) {
	if joinInts([]int{0, 2, 5}) != "0,2,5" {
		t.Errorf("got %q", joinInts([]int{0, 2, 5}))
	}
	if joinInts(nil) != "" {
		t.Errorf("nil → empty, got %q", joinInts(nil))
	}
}

func TestComposeDesignChildDescription(t *testing.T) {
	proposal := service.DesignProposal{
		Components: []service.DesignProposalComponent{
			{Name: "DataTable", Verdict: "reuse", CodeRef: "src/DataTable.vue", FigmaNodeID: "208:5147"},
		},
	}
	p := effectiveDesignPlan{
		index:       0,
		title:       "List view",
		description: "Build the list.",
		sub:         service.DesignProposalSubIssue{Screens: []string{"208:5147"}, NodeIDs: []string{"208:5147"}},
	}
	got := composeDesignChildDescription(p, proposal, "MUL-348")
	for _, want := range []string{"Build the list.", "## Design context", "parent MUL-348", "reuse", "DataTable", "Do not restyle shared components"} {
		if !strings.Contains(got, want) {
			t.Errorf("description missing %q:\n%s", want, got)
		}
	}
}
