package daemon

import (
	"strings"
	"testing"
)

// TestPackArmIsDeterministic pins the property the whole experiment rests on:
// a retried or resumed task must land in the same arm every time. A random
// draw would let one task contribute to both arms and contaminate the result.
func TestPackArmIsDeterministic(t *testing.T) {
	const id = "6f1c2f7a-3b1e-4c2a-9d5e-8a7b6c5d4e3f"
	first := packArm(id)
	for i := 0; i < 100; i++ {
		if got := packArm(id); got != first {
			t.Fatalf("packArm(%q) = %d on call %d, want %d — arm is not stable", id, got, i, first)
		}
	}
}

// TestPackArmSplitsRoughlyEvenly guards against a hash that piles every task
// into one arm, which would look like a working experiment while measuring
// nothing.
func TestPackArmSplitsRoughlyEvenly(t *testing.T) {
	counts := make([]int, packArms)
	const n = 2000
	for i := 0; i < n; i++ {
		// Realistic-shaped distinct IDs.
		counts[packArm("6f1c2f7a-3b1e-4c2a-9d5e-"+pad(i))]++
	}
	for arm, c := range counts {
		if c < n/packArms*8/10 || c > n/packArms*12/10 {
			t.Errorf("arm %d got %d/%d tasks — split is too skewed (counts=%v)", arm, c, n, counts)
		}
	}
}

func pad(i int) string {
	s := ""
	for _, r := range []rune("000000000000") {
		_ = r
		s += "0"
	}
	d := ""
	for i > 0 {
		d = string(rune('0'+i%10)) + d
		i /= 10
	}
	return (s + d)[len(s+d)-12:]
}

// TestTaskWantsRepoPack pins where the pack is allowed to spend tokens. Each
// excluded kind is one where a repository map cannot pay for itself.
func TestTaskWantsRepoPack(t *testing.T) {
	cases := []struct {
		name string
		task Task
		want bool
	}{
		{"issue task", Task{ID: "t1", IssueID: "i1"}, true},
		{"orchestration step on an issue", Task{ID: "t2", IssueID: "i1", OrchestrationStepID: "s1"}, true},
		{"quick create has no issue or repo", Task{ID: "t3", QuickCreatePrompt: "make an issue"}, false},
		{"chat", Task{ID: "t4", ChatSessionID: "c1", IssueID: "i1"}, false},
		{"autopilot", Task{ID: "t5", AutopilotRunID: "a1", IssueID: "i1"}, false},
		{"newly tagged agent gets code context", Task{ID: "t6", IssueID: "i1", TriggerCommentID: "c9"}, true},
		{"comment reply reuses a warm session", Task{ID: "t7", IssueID: "i1", TriggerCommentID: "c9", PriorSessionID: "session-1"}, false},
		{"no issue", Task{ID: "t8"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := taskWantsRepoPack(tc.task); got != tc.want {
				t.Errorf("taskWantsRepoPack = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTaskRequiresRepoPackForAccuracyCriticalTurns(t *testing.T) {
	cases := []struct {
		name string
		task Task
		want bool
	}{
		{"orchestration", Task{IssueID: "i1", OrchestrationStepID: "s1"}, true},
		{"cold tagged turn", Task{IssueID: "i1", TriggerCommentID: "c1"}, true},
		{"warm tagged turn", Task{IssueID: "i1", TriggerCommentID: "c1", PriorSessionID: "session-1"}, false},
		{"ordinary issue task remains in experiment", Task{IssueID: "i1"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := taskRequiresRepoPack(tc.task); got != tc.want {
				t.Fatalf("taskRequiresRepoPack = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPackQueryUsesIssueText: the ranker is only as good as its query. Title
// alone must work (older servers send no body), and the body must be included
// when present.
func TestPackQueryUsesIssueText(t *testing.T) {
	q := packQuery(Task{ThreadName: "Board shows archived issues", IssueBody: "the board should filter archived", ProjectTitle: "sd-main"})
	for _, want := range []string{"Board shows archived issues", "filter archived", "sd-main"} {
		if !strings.Contains(q, want) {
			t.Errorf("packQuery missing %q; got %q", want, q)
		}
	}

	// Old server: no body. The title must still carry the query.
	q = packQuery(Task{ThreadName: "Fix reserved slug validation"})
	if strings.TrimSpace(q) != "Fix reserved slug validation" {
		t.Errorf("packQuery on a title-only task = %q", q)
	}
}
