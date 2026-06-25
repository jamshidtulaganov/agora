package runtrace

import "testing"

func TestDeriveOutcome(t *testing.T) {
	cases := []struct {
		name     string
		in       Signals
		want     string
		reopened bool
	}{
		{"reopened: done -> in_progress", Signals{StatusAtRun: "done", CurrentStatus: "in_progress"}, LabelRejected, true},
		{"reopened: in_review -> todo", Signals{StatusAtRun: "in_review", CurrentStatus: "todo"}, LabelRejected, true},
		{"done -> blocked counts as reopened", Signals{StatusAtRun: "done", CurrentStatus: "blocked"}, LabelRejected, true},
		{"cancelled now", Signals{StatusAtRun: "in_progress", CurrentStatus: "cancelled"}, LabelRejected, false},
		{"negative reactions", Signals{StatusAtRun: "todo", CurrentStatus: "done", ReactionScore: -2}, LabelRejected, false},
		{"human follow-up = corrected", Signals{StatusAtRun: "todo", CurrentStatus: "in_review", HumanFollowUp: true}, LabelCorrected, false},
		{"advanced + positive = accepted", Signals{StatusAtRun: "todo", CurrentStatus: "done", ReactionScore: 2}, LabelAccepted, false},
		{"untouched settled = accepted", Signals{StatusAtRun: "todo", CurrentStatus: "todo"}, LabelAccepted, false},
		{"stayed advanced (in_review -> done)", Signals{StatusAtRun: "in_review", CurrentStatus: "done"}, LabelAccepted, false},
		{"reopen beats positive reactions", Signals{StatusAtRun: "done", CurrentStatus: "backlog", ReactionScore: 5}, LabelRejected, true},
		{"reopen beats human follow-up", Signals{StatusAtRun: "done", CurrentStatus: "todo", HumanFollowUp: true}, LabelRejected, true},
		{"negative reactions beat human follow-up", Signals{StatusAtRun: "todo", CurrentStatus: "done", HumanFollowUp: true, ReactionScore: -1}, LabelRejected, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveOutcome(tc.in)
			if got.Label != tc.want {
				t.Errorf("label = %q, want %q", got.Label, tc.want)
			}
			if got.Reopened != tc.reopened {
				t.Errorf("reopened = %v, want %v", got.Reopened, tc.reopened)
			}
			if got.ReactionScore != tc.in.ReactionScore {
				t.Errorf("reaction score passthrough = %d, want %d", got.ReactionScore, tc.in.ReactionScore)
			}
		})
	}
}

func TestReactionDelta(t *testing.T) {
	for _, tc := range []struct {
		emoji string
		want  int
	}{
		{"👍", 1}, {"+1", 1}, {"✅", 1}, {" 👍 ", 1},
		{"👎", -1}, {"-1", -1}, {"❌", -1},
		{"🤔", 0}, {"", 0}, {"random", 0},
	} {
		if got := ReactionDelta(tc.emoji); got != tc.want {
			t.Errorf("ReactionDelta(%q) = %d, want %d", tc.emoji, got, tc.want)
		}
	}
}
