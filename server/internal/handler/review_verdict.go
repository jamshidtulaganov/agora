package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/util"
)

// GET /api/issues/{id}/review-verdict — the latest run_review verdict for an
// issue, resolved from the newest agent comment carrying a parsable
// ```review-result``` block (there is deliberately no review table; the
// comment IS the record — see review_evidence.go). Read-only and
// deterministic; registered next to merge-readiness.

// ReviewFindingResponse is one reviewer finding as the frontend consumes it.
type ReviewFindingResponse struct {
	File     string `json:"file"`
	Line     *int   `json:"line"`
	Severity string `json:"severity"` // "blocker" | "major" | "minor"
	Title    string `json:"title"`
	Detail   string `json:"detail"`
}

// ReviewVerdictResponse is the review-verdict endpoint's payload. Verdict is
// "none" when the issue has no captured review verdict yet — every other
// field is then zero-valued, and the frontend renders the "No review yet"
// empty state.
type ReviewVerdictResponse struct {
	Verdict         string                  `json:"verdict"` // "pass" | "fail" | "none"
	Summary         string                  `json:"summary,omitempty"`
	CommitSha       string                  `json:"commit_sha,omitempty"`
	FilesReviewed   int                     `json:"files_reviewed,omitempty"`
	Findings        []ReviewFindingResponse `json:"findings"`
	CommentID       string                  `json:"comment_id,omitempty"`
	ReviewedAt      string                  `json:"reviewed_at,omitempty"`
	ReviewerAgentID string                  `json:"reviewer_agent_id,omitempty"`
}

// GetIssueReviewVerdict handles GET /api/issues/{id}/review-verdict.
func (h *Handler) GetIssueReviewVerdict(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	p, commentID, reviewerID, reviewedAt, found, err := h.TaskService.LatestReviewResultForIssue(r.Context(), issue)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load the review verdict")
		return
	}
	if !found {
		writeJSON(w, http.StatusOK, ReviewVerdictResponse{Verdict: "none", Findings: []ReviewFindingResponse{}})
		return
	}

	findings := make([]ReviewFindingResponse, 0, len(p.Findings))
	for _, f := range p.Findings {
		findings = append(findings, ReviewFindingResponse{
			File: f.File, Line: f.Line, Severity: f.Severity, Title: f.Title, Detail: f.Detail,
		})
	}
	writeJSON(w, http.StatusOK, ReviewVerdictResponse{
		Verdict:         p.Verdict,
		Summary:         p.Summary,
		CommitSha:       p.CommitSha,
		FilesReviewed:   p.FilesReviewed,
		Findings:        findings,
		CommentID:       uuidToString(commentID),
		ReviewedAt:      util.TimestampToString(reviewedAt),
		ReviewerAgentID: uuidToString(reviewerID),
	})
}
