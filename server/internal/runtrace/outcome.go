// Package runtrace derives and backfills the OUTCOME of completed agent runs
// (accepted / corrected / rejected) from live workspace signals — the
// preference data that turns captured runs into a fine-tuning dataset. The run
// anchor itself is written at completion by the task service (persistRunTrace);
// this package fills the outcome columns afterwards, once the work has had time
// to settle so humans can react, correct, or reopen first.
package runtrace

import "strings"

// Issue status buckets used to read a trajectory. Agora's statuses are:
// backlog, todo, in_progress, in_review, done, blocked, cancelled.
var (
	advancedStatuses = map[string]bool{"in_review": true, "done": true}
	openStatuses     = map[string]bool{"backlog": true, "todo": true, "in_progress": true, "blocked": true}
)

// Outcome label values stored in agent_run_trace.outcome_label.
const (
	LabelPending   = "pending"
	LabelAccepted  = "accepted"
	LabelCorrected = "corrected"
	LabelRejected  = "rejected"
)

// Signals are the live facts gathered for one run's issue after it settled.
// Plain values only, so DeriveOutcome stays a pure function.
type Signals struct {
	StatusAtRun   string // issue.status snapshot when the run closed
	CurrentStatus string // issue.status now
	HumanFollowUp bool   // a member (human) commented on the issue after the run
	ReactionScore int    // net member reactions on the agent's comments (+ good / - bad)
}

// Outcome is the derived label plus the persisted signal columns.
type Outcome struct {
	Label         string
	Reopened      bool
	HumanRevised  bool
	ReactionScore int
}

// DeriveOutcome maps settled signals to a terminal label. It is only called on
// traces past the settle window, so it never returns LabelPending. Precedence,
// strongest negative first:
//
//  1. reopened (advanced → open regression) or cancelled  → rejected
//  2. negative net reactions                              → rejected
//  3. a human stepped in after the run (comment)          → corrected
//  4. otherwise (advanced, positive, or simply untouched) → accepted
func DeriveOutcome(s Signals) Outcome {
	reopened := advancedStatuses[s.StatusAtRun] && openStatuses[s.CurrentStatus]
	out := Outcome{Reopened: reopened, HumanRevised: s.HumanFollowUp, ReactionScore: s.ReactionScore}

	switch {
	case reopened || s.CurrentStatus == "cancelled":
		out.Label = LabelRejected
	case s.ReactionScore < 0:
		out.Label = LabelRejected
	case s.HumanFollowUp:
		out.Label = LabelCorrected
	default:
		out.Label = LabelAccepted
	}
	return out
}

// Reaction emoji classification. Emoji are free-text, so we match the common
// acceptance/rejection signals and treat everything else as neutral.
var (
	positiveEmoji = map[string]bool{"👍": true, "+1": true, "✅": true, "🎉": true, "❤️": true, "🙏": true, "💯": true}
	negativeEmoji = map[string]bool{"👎": true, "-1": true, "❌": true, "😕": true}
)

// ReactionDelta scores one reaction emoji: +1 positive, -1 negative, 0 neutral.
func ReactionDelta(emoji string) int {
	switch e := strings.TrimSpace(emoji); {
	case positiveEmoji[e]:
		return 1
	case negativeEmoji[e]:
		return -1
	default:
		return 0
	}
}
