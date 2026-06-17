package handler

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
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
	Tier    string       `json:"tier"` // "full" | "light"
	Gates   []gateStatus `json:"gates"`
	Blocked []string     `json:"blocked,omitempty"` // human-readable reasons it is not ready
}

// gateFromLabels resolves one gate's status from the issue's label set: a
// `<g>:fail` label blocks, a `<g>:pass` label passes, neither is pending.
func gateFromLabels(labels map[string]bool, g string) string {
	if labels[g+":fail"] {
		return "fail"
	}
	if labels[g+":pass"] {
		return "pass"
	}
	return "pending"
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

	tier := "full"
	required := []string{"ci", "qa"}
	if labels["tier:light"] {
		tier = "light"
		required = []string{"ci"}
	}

	gates := make([]gateStatus, 0, len(required))
	blocked := make([]string, 0)
	ready := true
	for _, g := range required {
		st := gateFromLabels(labels, g)
		gates = append(gates, gateStatus{Name: g, Status: st})
		switch st {
		case "fail":
			ready = false
			blocked = append(blocked, g+" failed ("+g+":fail)")
		case "pending":
			ready = false
			blocked = append(blocked, g+" has not passed yet")
		}
	}

	writeJSON(w, http.StatusOK, MergeReadinessResponse{
		Ready:   ready,
		Tier:    tier,
		Gates:   gates,
		Blocked: blocked,
	})
}
