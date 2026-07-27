package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Merge-readiness signal. Deterministic: aggregates the pipeline's gate
// verdicts (qa:pass/qa:fail from run_qa, review:pass/review:fail from
// run_review) into a single ready/blocked answer for the HUMAN who owns the
// merge. It NEVER merges anything and runs no agent reasoning, so a confident
// paragraph cannot talk it out of its verdict.
//
// A GATE THAT NEVER RAN NEVER BLOCKS. Review and merge are human work here, so
// nothing machine-run is a precondition for a human looking at a diff. Only an
// explicitly RED gate blocks — merging over a known-failing verdict is the one
// thing this endpoint exists to stop. A gate with no signal at all is simply
// not reported.
//
// This is the fix for the class of bug that made the old version useless: the
// default tier used to `require` a `ci` gate that nothing in the product ever
// emitted (ci:pass is written only by a manually-fired run_ci slice action, and
// nothing auto-dispatches it), so `Ready` was structurally false on every
// default-tier issue and every merge degenerated into a hand-merge whenever a
// human happened to notice. The same shape had already been found and fixed for
// the reviewer gate (see reviewGateRequired) and never for ci. Rather than
// patch one gate, no PENDING gate blocks now.
//
// Tier survives only as ADVISORY guidance (`reviews`): which reviewer fleet a
// change of this blast radius deserves. It gates nothing.

type gateStatus struct {
	Name   string `json:"name"`   // "qa" | "review"
	Status string `json:"status"` // "pass" | "fail" | "pending"
}

// blockingGates are the gates whose RED verdict blocks a merge. Same set for
// every tier: a landed qa:fail or review:fail is a real signal regardless of
// how big the change is, and no gate is a precondition (see the package
// comment). `ci` is deliberately absent — nothing in the product emits ci:pass
// without a human manually firing run_ci.
var blockingGates = []string{"qa", "review"}

// MergeReadinessResponse is the deterministic gate verdict for an issue's PR.
type MergeReadinessResponse struct {
	Ready   bool         `json:"ready"`
	Tier    string       `json:"tier"` // "trivial" | "light" | "full"
	Gates   []gateStatus `json:"gates"`
	Blocked []string     `json:"blocked,omitempty"` // human-readable reasons it is not ready
	Reviews []string     `json:"reviews"`           // recommended reviewer fleet for this tier (advisory)
}

// reviewTier maps an issue's blast radius to its review effort. `reviews` is
// the recommended reviewer fleet for the tier: ADVISORY guidance so a human
// does not fire a full QA + Security + code-review pass on a one-line change.
// The tier gates nothing — see blockingGates and the package comment. It used
// to carry a `required` set, and requiring a gate no reporter emitted is
// exactly what deadlocked the merge.
type reviewTier struct {
	name    string
	reviews []string
}

// reviewTierForLabels resolves the review tier from the issue's label set. The
// default (no tier label) recommends the full fleet; tier:light and
// tier:trivial recommend a spot-check. The trivial/light split also drives the
// MODEL (see applyIssueCostTier) — that is where it earns its keep.
func reviewTierForLabels(labels map[string]bool) reviewTier {
	switch {
	case labels["tier:trivial"]:
		return reviewTier{name: "trivial", reviews: []string{"code-review"}}
	case labels["tier:light"]:
		return reviewTier{name: "light", reviews: []string{"code-review"}}
	default:
		return reviewTier{name: "full", reviews: []string{"qa", "security", "code-review"}}
	}
}

// reviewGateRequired decides whether the reviewer gate must be ENFORCED for an
// issue — the single predicate both the merge chain (reviewGateApplies) and the
// merge-readiness computation share, so they can never disagree.
//
// The gate is required only when ALL hold:
//   - FULL review tier (trivial/light changes never wait on a code review);
//   - there is a diff to review — a known PR OR a review verdict label already
//     landed (a present verdict proves a review happened even when PR detection
//     is flaky/absent);
//   - the review is ACTIVE — auto-review is enabled (so a verdict WILL be
//     produced and auto-dispatched) OR a manual review verdict already landed.
//
// The active-review condition is the key coherence fix: when auto-review is OFF
// and no manual review has run, nothing produces review:pass, so requiring the
// gate would stall the merge chain forever. In that state the review is
// advisory only — never a silent blocker. Flag on ⇒ review required +
// auto-dispatched; flag off ⇒ review is manual/advisory.
// autoReview is the resolved (project-scoped) AGORA_AUTO_REVIEW_ENABLED value —
// passed in so this stays a pure function (the caller, computeMergeReadiness,
// has the issue and resolves the per-project override).
func reviewGateRequired(t reviewTier, hasPR bool, labels map[string]bool, autoReview bool) bool {
	if t.name != "full" {
		return false
	}
	hasVerdict := labels["review:pass"] || labels["review:fail"]
	if !hasPR && !hasVerdict {
		return false
	}
	return autoReview || hasVerdict
}

// gateFromLabels resolves one gate's status from the issue's label set: a
// `<g>:fail` label blocks, a `<g>:pass` label passes, neither is pending.
// Used for every gate EXCEPT "qa" — the qa gate is reconciled (see
// qaGateFromReconciledState) so it also fails closed on a case regression a
// bare label check can't see.
func gateFromLabels(labels map[string]bool, g string) string {
	// Both labels present = a legacy sticky pair from before verdicts became
	// replace-on-write; the freshest verdict is unknowable from the label set,
	// so the gate needs a re-verdict rather than a hard block.
	if labels[g+":fail"] && labels[g+":pass"] {
		return "pending"
	}
	if labels[g+":fail"] {
		return "fail"
	}
	if labels[g+":pass"] {
		return "pass"
	}
	return "pending"
}

// qaGateFromReconciledState resolves the "qa" gate from the SAME reconciled
// state the qa-evidence endpoint and the QA lens/cockpit read (Phase 2 of the
// QA-stage review — service.ReconcileQAState), instead of a bare label check.
// This is what makes pass_with_failing_cases fail-closed here too: a qa:pass
// label sitting on a known-failing case must NOT clear the merge gate just
// because gateFromLabels alone can't see the case-run signal. reason is a
// human-readable blocked-reason string for the non-pass states.
func (h *Handler) qaGateFromReconciledState(ctx context.Context, issue db.Issue) (status, reason string) {
	hasEvidence := true
	if _, err := h.Queries.GetLatestQAEvidenceForIssue(ctx, db.GetLatestQAEvidenceForIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		hasEvidence = false
	}
	state := h.reconciledQAState(ctx, issue, hasEvidence)
	switch state {
	case service.QAStatePass:
		return "pass", ""
	case service.QAStatePassWithFailingCases:
		return "fail", "qa passed (qa:pass) but at least one defined test case's latest run is failing — not a clean pass"
	case service.QAStateFail:
		return "fail", "qa failed (qa:fail)"
	case service.QAStateBlocked:
		return "fail", "qa gate is blocked (qa:blocked)"
	case service.QAStateRunning:
		return "pending", "qa gate is running"
	case service.QAStateStale:
		return "pending", "qa gate went stale — re-run QA"
	default: // QAStateNeverRan
		return "pending", "qa has not passed yet"
	}
}

// computeMergeReadiness is the deterministic gate-readiness SPINE shared by the
// GET /merge-readiness endpoint and the human Approve flow (review_decision.go)
// — extracted so the two can never disagree about what "ready to merge" means.
// Reads labels + the reconciled QA state; never mutates. On a label read error
// it degrades gracefully to an empty label set, which yields "not ready"
// (pending gates) — safe for the read endpoint and fail-closed for approve.
func (h *Handler) computeMergeReadiness(ctx context.Context, issue db.Issue) MergeReadinessResponse {
	labelRows, err := h.Queries.ListLabelsByIssue(ctx, db.ListLabelsByIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		labelRows = nil // degrade to empty set → gates read pending → not ready
	}
	labels := make(map[string]bool, len(labelRows))
	for _, l := range labelRows {
		labels[strings.ToLower(strings.TrimSpace(l.Name))] = true
	}

	t := reviewTierForLabels(labels)

	// Report every gate that has actually SPOKEN, and block only on a red one.
	// A gate that never ran is not reported at all — it is not a wait, and
	// rendering a permanent "pending" chip for a QA run nobody asked for is the
	// noise this endpoint used to be made of.
	gates := make([]gateStatus, 0, len(blockingGates))
	blocked := make([]string, 0)
	ready := true
	for _, g := range blockingGates {
		var st, reason string
		if g == "qa" {
			// The qa gate is RECONCILED (labels + per-case run results + live
			// task), not a bare label check — see qaGateFromReconciledState.
			st, reason = h.qaGateFromReconciledState(ctx, issue)
		} else {
			st = gateFromLabels(labels, g)
			if st == "fail" {
				reason = g + " failed (" + g + ":fail)"
			}
		}
		if st == "pending" {
			continue // never ran, or still running — not a blocker either way
		}
		gates = append(gates, gateStatus{Name: g, Status: st})
		if st == "fail" {
			ready = false
			blocked = append(blocked, reason)
		}
	}

	return MergeReadinessResponse{
		Ready:   ready,
		Tier:    t.name,
		Gates:   gates,
		Blocked: blocked,
		Reviews: t.reviews,
	}
}

// MergeReadiness handles GET /api/issues/{id}/merge-readiness. Read-only and
// deterministic — it computes the gate verdict from labels and returns it for
// the human reviewer; it does not mutate the issue or merge anything.
func (h *Handler) MergeReadiness(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, h.computeMergeReadiness(r.Context(), issue))
}
