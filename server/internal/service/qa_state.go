package service

// QAState is the single reconciled QA state for an issue — the ONE truth the
// verdict chip (qa-lens.tsx), the cockpit lanes (qa-lane.tsx), and the merge
// gate (merge_readiness.go) all read, instead of each separately combining
// labels + live signals and risking disagreement. The 44-agent adversarial
// review's five correctness bugs were exactly that kind of drift (label vs
// evidence, panel vs cockpit) — this is the reconciliation phase.
type QAState string

const (
	// QAStateRunning: a QA-squad task is executing on this issue right now.
	// Highest precedence — a live run supersedes any stale prior verdict.
	QAStateRunning QAState = "running"
	// QAStatePass: qa:pass is set and no known case's latest run is failing.
	QAStatePass QAState = "pass"
	// QAStatePassWithFailingCases: qa:pass is set, but at least one defined
	// case's LATEST run is "fail" — a green gate label sitting on top of a
	// known-red case. Distinct from Fail so the UI can render "pass, but…";
	// the merge gate treats it as NOT pass (fail-closed).
	QAStatePassWithFailingCases QAState = "pass_with_failing_cases"
	// QAStateFail: qa:fail is set, OR no qa:pass is set and a case's latest
	// run is failing (a regression signal blocks even with no gate verdict).
	QAStateFail QAState = "fail"
	// QAStateBlocked: qa:blocked is set — a DELIBERATE infra-blocked state
	// the gate itself reported (e.g. an undeployable sprint branch), not a
	// test failure.
	QAStateBlocked QAState = "blocked"
	// QAStateStale: the gate ran (or already-attached labels are untrustworthy)
	// but there is nothing solid to show — the watchdog's qa:stale escalation,
	// a legacy fail+pass sticky pair (freshest verdict unknowable), or a
	// captured qa_evidence row that produced no pass/fail gate label at all.
	QAStateStale QAState = "stale"
	// QAStateNeverRan: no label, no evidence, no running task, no case run —
	// QA has not touched this issue.
	QAStateNeverRan QAState = "never_ran"
)

// QACaseRunStatus is the minimal per-case signal ReconcileQAState needs: one
// test case's LATEST run status ("pass" | "fail" | "blocked" | "skip"). Kept
// independent of the generated db package so this file stays a pure,
// dependency-free function that's trivial to exhaustively unit test.
type QACaseRunStatus struct {
	Status string
}

// ReconcileQAState folds every QA signal for one issue into ONE state.
//
//   - labels: the issue's label NAMES currently attached (only "qa:pass",
//     "qa:fail", "qa:blocked", "qa:stale" are read; anything else is ignored).
//   - latestRunsPerCase: the LATEST test_run per test_case for this issue —
//     the issue's own cases plus any project base-suite runs recorded against
//     it (mirrors ListLatestRunsForIssueCases). Only .Status is read.
//   - hasRunningQATask: a QA-squad agent task is executing on this issue right
//     now (a live gate always wins over a stale prior verdict).
//   - hasEvidence: a qa_evidence row exists for this issue — a run_qa verdict
//     was captured at some point, even if no gate label ended up attached.
//
// Precedence (highest first): running > (qa:stale OR a legacy qa:fail+qa:pass
// sticky pair) > blocked > fail > pass_with_failing_cases > pass > (evidence
// with no usable label) stale > never_ran.
//
// The PRECEDENCE ORDER — an explicit qa:stale check ranking above
// blocked/fail/pass — mirrors qa-lane.tsx's pre-existing, fuzz-tested
// qaRowState contract (packages/views/qa/components/qa-lane.tsx). The
// sticky-pair MAPPING TARGET differs by design: qa-lane's narrower 5/6-value
// vocabulary folds an untrustworthy fail+pass pair into its own "pending"
// bucket (that exact rule is fuzz-tested there — do not change it), while
// this richer 7-value enum has no "pending" and folds the same pair into
// Stale (an untrustworthy verdict needing a re-run is a stale one, in this
// vocabulary). Both agree an EXPLICIT qa:stale label always wins outright.
func ReconcileQAState(labels map[string]bool, latestRunsPerCase []QACaseRunStatus, hasRunningQATask, hasEvidence bool) QAState {
	if hasRunningQATask {
		return QAStateRunning
	}

	hasPass := labels["qa:pass"]
	hasFail := labels["qa:fail"]

	// qa:stale (watchdog-escalated) and a legacy fail+pass sticky pair both
	// mean "the freshest verdict here is not trustworthy — needs a re-run,"
	// so both fold to Stale ahead of any other label check.
	if labels["qa:stale"] || (hasFail && hasPass) {
		return QAStateStale
	}
	if labels["qa:blocked"] {
		return QAStateBlocked
	}
	if hasFail {
		return QAStateFail
	}

	anyCaseFailing := false
	for _, r := range latestRunsPerCase {
		if r.Status == "fail" {
			anyCaseFailing = true
			break
		}
	}

	switch {
	case hasPass && anyCaseFailing:
		return QAStatePassWithFailingCases
	case hasPass:
		return QAStatePass
	case anyCaseFailing:
		// No gate label at all, but a defined case is known failing — a
		// regression signal that blocks on its own (mirrors
		// sprint_readiness.go's "RunsFail > 0" rule).
		return QAStateFail
	case hasEvidence:
		// A run_qa verdict was captured but produced no pass/fail gate label
		// (e.g. a non-actionable verdict like "blocked"/"maybe", or a label
		// attach that failed — see CaptureQAEvidence's label-first ordering).
		return QAStateStale
	default:
		return QAStateNeverRan
	}
}

// QAStateBlocksMerge reports whether state must NOT be treated as "passed"
// by a fail-closed consumer (the merge-readiness qa gate). Only a clean Pass
// clears it — Running/Fail/Blocked/Stale/NeverRan/PassWithFailingCases all
// block, including PassWithFailingCases: a qa:pass label sitting on a known-
// failing case is not a clean pass, by design (audit requirement).
func QAStateBlocksMerge(state QAState) bool {
	return state != QAStatePass
}
