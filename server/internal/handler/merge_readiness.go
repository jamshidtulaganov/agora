package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Merge-readiness gate (Phase D of the review-system build). Deterministic:
// aggregates the pipeline's gate LABELS (ci:pass/ci:fail from run_ci, qa:pass/
// qa:fail from run_qa) into a single ready/blocked signal, tiered by blast
// radius. It is a SIGNAL for the human who owns the merge — it NEVER merges
// anything and runs no agent reasoning, so a confident paragraph cannot talk it
// out of its verdict.
//
// `billing` is PROD and every agent PR targets it, so the default ("full") tier
// requires the full stack of deterministic gates. A `tier:light` label downgrades
// a genuinely low-risk change (docs/config) to the CI-only gate — match the
// review effort to the cost of being wrong, not to the author.
//
// The reviewer gate (Review stage v2) IS label-backed now: run_review's
// captured ```review-result``` verdict attaches review:pass / review:fail
// (service.CaptureReviewEvidence), so "review" joins `required` for the full
// tier — but ONLY when the gate is actually required (see reviewGateRequired):
// there is a diff to review (a known PR or a landed verdict) AND the review is
// active (auto-review enabled or a manual verdict landed). When auto-review is
// off and no manual review ran, the gate is omitted entirely — advisory, never
// a dangling "pending" that stalls the merge. Security findings remain advisory
// (no sec:pass label yet).

type gateStatus struct {
	Name   string `json:"name"`   // "ci" | "qa" | "review"
	Status string `json:"status"` // "pass" | "fail" | "pending"
}

// MergeReadinessResponse is the deterministic gate verdict for an issue's PR.
type MergeReadinessResponse struct {
	Ready   bool         `json:"ready"`
	Tier    string       `json:"tier"` // "trivial" | "light" | "full"
	Gates   []gateStatus `json:"gates"`
	Blocked []string     `json:"blocked,omitempty"` // human-readable reasons it is not ready
	Reviews []string     `json:"reviews"`           // recommended reviewer fleet for this tier (advisory)
}

// reviewTier maps an issue's blast radius to its review effort. `required` is the
// set of deterministic, label-backed gates that must pass before merge — only
// gates the pipeline actually emits (ci via run_ci, qa via run_qa). `reviews` is
// the recommended reviewer fleet for the tier: advisory guidance so a human does
// not fire a full QA + Security + code-review pass on a one-line change. reviews
// is intentionally kept OUT of required — listing a reviewer that does not yet
// emit a pass label there would deadlock the gate forever.
type reviewTier struct {
	name     string
	required []string
	reviews  []string
}

// reviewTierForLabels resolves the review tier from the issue's label set.
// `billing` is PROD and every agent PR targets it, so the default (no tier
// label) is the full fleet. tier:light and tier:trivial both gate on CI alone —
// the trivial/light split drives the MODEL (see applyIssueCostTier), not the
// merge gate — and both skip the heavy QA + Security + code-review fleet that a
// full-blast change warrants.
func reviewTierForLabels(labels map[string]bool) reviewTier {
	switch {
	case labels["tier:trivial"]:
		return reviewTier{name: "trivial", required: []string{"ci"}, reviews: []string{"ci"}}
	case labels["tier:light"]:
		return reviewTier{name: "light", required: []string{"ci"}, reviews: []string{"ci"}}
	default:
		return reviewTier{name: "full", required: []string{"ci", "qa"}, reviews: []string{"ci", "qa", "security", "code-review"}}
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
func reviewGateRequired(t reviewTier, hasPR bool, labels map[string]bool) bool {
	if t.name != "full" {
		return false
	}
	hasVerdict := labels["review:pass"] || labels["review:fail"]
	if !hasPR && !hasVerdict {
		return false
	}
	return autoReviewEnabled() || hasVerdict
}

// requiredGatesWithReview appends the "review" gate to a tier's required set
// when the reviewer gate is required (see reviewGateRequired). PURE
// (unit-tested without a DB): the caller computes reviewRequired. When the gate
// is not required the review gate is omitted entirely — never left dangling as
// "pending".
func requiredGatesWithReview(t reviewTier, reviewRequired bool) []string {
	if !reviewRequired {
		return t.required
	}
	out := make([]string, 0, len(t.required)+1)
	out = append(out, t.required...)
	return append(out, "review")
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
	required := requiredGatesWithReview(t, reviewGateRequired(t, h.issueHasKnownPR(ctx, issue), labels))

	gates := make([]gateStatus, 0, len(required))
	blocked := make([]string, 0)
	ready := true
	for _, g := range required {
		var st, reason string
		if g == "qa" {
			// The qa gate is RECONCILED (labels + per-case run results + live
			// task), not a bare label check — see qaGateFromReconciledState.
			st, reason = h.qaGateFromReconciledState(ctx, issue)
		} else {
			st = gateFromLabels(labels, g)
			if st == "fail" {
				reason = g + " failed (" + g + ":fail)"
			} else if st == "pending" {
				reason = g + " has not passed yet"
			}
		}
		gates = append(gates, gateStatus{Name: g, Status: st})
		if st != "pass" {
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
