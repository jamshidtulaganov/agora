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
// Reviewer findings (the Gemini code-review + Security agents) are advisory
// comments the human reads; they are not yet label-backed, so they are not part
// of the deterministic verdict. When those agents start setting review:pass /
// sec:pass labels they can be added to `required` with no other change.

type gateStatus struct {
	Name   string `json:"name"`   // "ci" | "qa"
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

// MergeReadiness handles GET /api/issues/{id}/merge-readiness. Read-only and
// deterministic — it computes the gate verdict from labels and returns it for
// the human reviewer; it does not mutate the issue or merge anything.
func (h *Handler) MergeReadiness(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}

	labelRows, _ := h.listLabelsForIssueSafe(r, issue.ID, issue.WorkspaceID)
	labels := make(map[string]bool, len(labelRows))
	for _, l := range labelRows {
		labels[strings.ToLower(strings.TrimSpace(l.Name))] = true
	}

	t := reviewTierForLabels(labels)
	required := t.required

	gates := make([]gateStatus, 0, len(required))
	blocked := make([]string, 0)
	ready := true
	for _, g := range required {
		var st, reason string
		if g == "qa" {
			// The qa gate is RECONCILED (labels + per-case run results + live
			// task), not a bare label check — see qaGateFromReconciledState.
			st, reason = h.qaGateFromReconciledState(r.Context(), issue)
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

	writeJSON(w, http.StatusOK, MergeReadinessResponse{
		Ready:   ready,
		Tier:    t.name,
		Gates:   gates,
		Blocked: blocked,
		Reviews: t.reviews,
	})
}
