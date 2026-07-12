package service

import (
	"strings"
	"testing"
)

func TestReconcileQAState(t *testing.T) {
	fail := []QACaseRunStatus{{Status: "fail"}}
	passOnly := []QACaseRunStatus{{Status: "pass"}}
	mixed := []QACaseRunStatus{{Status: "pass"}, {Status: "fail"}}
	noCases := []QACaseRunStatus{}

	tests := []struct {
		name              string
		labels            map[string]bool
		latestRunsPerCase []QACaseRunStatus
		hasRunningQATask  bool
		hasEvidence       bool
		want              QAState
	}{
		// running always wins, regardless of every other signal.
		{"running beats pass label", map[string]bool{"qa:pass": true}, noCases, true, true, QAStateRunning},
		{"running beats fail label", map[string]bool{"qa:fail": true}, fail, true, true, QAStateRunning},
		{"running beats blocked label", map[string]bool{"qa:blocked": true}, noCases, true, false, QAStateRunning},
		{"running with nothing else", map[string]bool{}, noCases, true, false, QAStateRunning},

		// qa:stale wins over pass/fail/blocked (matches qa-lane.tsx's
		// established, fuzz-tested precedence).
		{"stale label alone", map[string]bool{"qa:stale": true}, noCases, false, false, QAStateStale},
		{"stale beats pass", map[string]bool{"qa:stale": true, "qa:pass": true}, noCases, false, true, QAStateStale},
		{"stale beats fail", map[string]bool{"qa:stale": true, "qa:fail": true}, fail, false, true, QAStateStale},
		{"stale beats blocked", map[string]bool{"qa:stale": true, "qa:blocked": true}, noCases, false, false, QAStateStale},

		// Legacy sticky fail+pass pair (no stale label) — freshest verdict
		// unknowable, folds to stale same as an explicit qa:stale.
		{"sticky fail+pass pair", map[string]bool{"qa:fail": true, "qa:pass": true}, noCases, false, true, QAStateStale},
		{"sticky pair with failing case", map[string]bool{"qa:fail": true, "qa:pass": true}, fail, false, true, QAStateStale},

		// blocked (no stale, no sticky pair).
		{"blocked alone", map[string]bool{"qa:blocked": true}, noCases, false, false, QAStateBlocked},
		{"blocked with a failing case", map[string]bool{"qa:blocked": true}, fail, false, false, QAStateBlocked},

		// fail: explicit label.
		{"fail label alone", map[string]bool{"qa:fail": true}, noCases, false, true, QAStateFail},
		{"fail label with passing cases", map[string]bool{"qa:fail": true}, passOnly, false, true, QAStateFail},

		// fail: no gate label at all, but a case is known failing (regression
		// signal with no verdict yet still blocks — mirrors sprint_readiness).
		{"no label, a case failing", map[string]bool{}, fail, false, false, QAStateFail},
		{"no label, mixed cases failing", map[string]bool{}, mixed, false, false, QAStateFail},

		// pass_with_failing_cases: qa:pass set, but a case's latest run is
		// failing — NOT a clean pass.
		{"pass label with a failing case", map[string]bool{"qa:pass": true}, fail, false, true, QAStatePassWithFailingCases},
		{"pass label with mixed cases", map[string]bool{"qa:pass": true}, mixed, false, true, QAStatePassWithFailingCases},

		// pass: clean.
		{"pass label, all cases passing", map[string]bool{"qa:pass": true}, passOnly, false, true, QAStatePass},
		{"pass label, no cases at all", map[string]bool{"qa:pass": true}, noCases, false, true, QAStatePass},

		// stale (evidence exists, no usable gate label, no case signal).
		{"evidence but no label", map[string]bool{}, noCases, false, true, QAStateStale},
		{"evidence, no label, passing cases only", map[string]bool{}, passOnly, false, true, QAStateStale},

		// never_ran: truly nothing.
		{"nothing at all", map[string]bool{}, noCases, false, false, QAStateNeverRan},
		{"nil maps and slices", nil, nil, false, false, QAStateNeverRan},

		// Case-run statuses other than "fail" never count as failing.
		{"blocked/skip case runs don't count as failing", map[string]bool{"qa:pass": true},
			[]QACaseRunStatus{{Status: "blocked"}, {Status: "skip"}, {Status: ""}}, false, true, QAStatePass},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ReconcileQAState(tt.labels, tt.latestRunsPerCase, tt.hasRunningQATask, tt.hasEvidence, "", "")
			if got != tt.want {
				t.Errorf("ReconcileQAState(%v, %v, running=%v, evidence=%v) = %q, want %q",
					tt.labels, tt.latestRunsPerCase, tt.hasRunningQATask, tt.hasEvidence, got, tt.want)
			}
		})
	}
}

// TestQAStateBlocksMerge locks the fail-closed contract: ONLY a clean Pass
// clears the merge gate. pass_with_failing_cases is the audit's explicit
// requirement — a qa:pass label is not enough on its own if a case is known
// failing.
func TestQAStateBlocksMerge(t *testing.T) {
	blocking := []QAState{
		QAStateRunning, QAStateFail, QAStateBlocked, QAStateStale, QAStateNeverRan, QAStatePassWithFailingCases,
	}
	for _, s := range blocking {
		if !QAStateBlocksMerge(s) {
			t.Errorf("QAStateBlocksMerge(%q) = false, want true (must block merge)", s)
		}
	}
	if QAStateBlocksMerge(QAStatePass) {
		t.Error("QAStateBlocksMerge(pass) = true, want false (a clean pass must clear the gate)")
	}
}

// TestReconcileQAStateMatchesQARowStatePrecedence pins the SAME precedence
// contract qa-lane.tsx's fuzz-tested qaRowState() encodes (running > stale >
// sticky-pair-as-pending > fail > pass > pending), at the label-combination
// level, so the Go and TS implementations cannot silently diverge on the
// cases both handle. (qa-lane's 5-state "pending" bucket has no case-run
// signal and folds sticky-pair into "pending" rather than a literal "stale" —
// this test only pins the RELATIVE ordering of label checks, not qa-lane's
// coarser bucketing.)
func TestReconcileQAStateMatchesQARowStatePrecedence(t *testing.T) {
	// stale beats a plain single fail (not just the sticky pair).
	got := ReconcileQAState(map[string]bool{"qa:stale": true, "qa:fail": true}, nil, false, true, "", "")
	if got != QAStateStale {
		t.Fatalf("qa:stale must win over a plain qa:fail, got %q", got)
	}
	// stale beats a plain single pass.
	got = ReconcileQAState(map[string]bool{"qa:stale": true, "qa:pass": true}, nil, false, true, "", "")
	if got != QAStateStale {
		t.Fatalf("qa:stale must win over a plain qa:pass, got %q", got)
	}
}

// TestCommitShasDiffer pins the fail-open, prefix-tolerant sha comparison
// behind stale-green invalidation.
func TestCommitShasDiffer(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"", "", false},                 // both unknown — no claim
		{"deadbeef", "", false},         // head unknown — no claim
		{"", "deadbeef", false},         // evidence sha unknown — no claim
		{"deadbeef", "deadbeef", false}, // identical
		{"deadbeef", "DEADBEEF", false}, // case-insensitive
		{"deadbef12", "deadbeef1234deadbeef1234deadbeef12341234", true},     // genuinely different (diverge at char 6)
		{"deadbeef1234", "deadbeef1234deadbeef1234deadbeef12341234", false}, // short-vs-full prefix match
		{"deadbeef1234deadbeef1234deadbeef12341234", "deadbeef1234", false}, // full-vs-short prefix match
		{"aaaaaaa", "bbbbbbb", true},                                        // plainly different
	}
	for _, tt := range tests {
		if got := CommitShasDiffer(tt.a, tt.b); got != tt.want {
			t.Errorf("CommitShasDiffer(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

// TestReconcileQAStateStaleGreen pins the Phase 3 stale-green invalidation:
// evidence whose commit_sha no longer matches the issue's known head
// reconciles to stale — a green (or red) verdict on outdated code is not a
// current verdict — while an unknown head or unreported sha makes NO
// staleness claim (fail-open).
func TestReconcileQAStateStaleGreen(t *testing.T) {
	passLabels := map[string]bool{"qa:pass": true}

	// Head moved past the evidence sha → stale, even with a clean qa:pass.
	if got := ReconcileQAState(passLabels, nil, false, true, "deadbeef", "cafebabe1234"); got != QAStateStale {
		t.Errorf("pass with sha mismatch = %q, want stale", got)
	}
	// Same for a fail verdict — the red is equally outdated.
	if got := ReconcileQAState(map[string]bool{"qa:fail": true}, nil, false, true, "deadbeef", "cafebabe1234"); got != QAStateStale {
		t.Errorf("fail with sha mismatch = %q, want stale", got)
	}
	// Matching head (short evidence sha vs full head) → the verdict stands.
	if got := ReconcileQAState(passLabels, nil, false, true, "deadbee1", "deadbee1"+strings.Repeat("0", 32)); got != QAStatePass {
		t.Errorf("pass with matching prefix sha = %q, want pass", got)
	}
	// Unknown head (no open PR / sprint-branch / local-worktree) → fail-open.
	if got := ReconcileQAState(passLabels, nil, false, true, "deadbeef", ""); got != QAStatePass {
		t.Errorf("pass with unknown head = %q, want pass (no staleness claim)", got)
	}
	// Unreported evidence sha (legacy verdict) → fail-open.
	if got := ReconcileQAState(passLabels, nil, false, true, "", "cafebabe1234"); got != QAStatePass {
		t.Errorf("pass with unreported evidence sha = %q, want pass (no staleness claim)", got)
	}
	// running still outranks the mismatch.
	if got := ReconcileQAState(passLabels, nil, true, true, "deadbeef", "cafebabe1234"); got != QAStateRunning {
		t.Errorf("running with sha mismatch = %q, want running", got)
	}
	// No evidence at all → the sha params are ignored entirely.
	if got := ReconcileQAState(map[string]bool{}, nil, false, false, "deadbeef", "cafebabe1234"); got != QAStateNeverRan {
		t.Errorf("no evidence with sha params = %q, want never_ran", got)
	}
}
