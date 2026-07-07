package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// QAEvidenceResponse is the durable, evidence-first QA verdict for an issue. The
// QA section reads this single indexed row instead of re-parsing the timeline,
// so opening any of N in-review tasks is a cheap read. result is the verbatim
// ```qa-result``` payload (verdict / summary / commands / screenshots).
type QAEvidenceResponse struct {
	ID          string          `json:"id"`
	IssueID     string          `json:"issue_id"`
	BaselineRef string          `json:"baseline_ref"`
	BranchSha   string          `json:"branch_sha"`
	Verdict     string          `json:"verdict"`
	Source      string          `json:"source"`
	Summary     string          `json:"summary"`
	Result      json.RawMessage `json:"result"`
	CapturedAt  string          `json:"captured_at"`
}

func qaEvidenceToResponse(e db.QaEvidence) QAEvidenceResponse {
	result := json.RawMessage(e.ResultJson)
	if len(result) == 0 {
		result = json.RawMessage("{}")
	}
	return QAEvidenceResponse{
		ID:          uuidToString(e.ID),
		IssueID:     uuidToString(e.IssueID),
		BaselineRef: e.BaselineRef,
		BranchSha:   e.BranchSha,
		Verdict:     e.Verdict,
		Source:      e.Source,
		Summary:     e.Summary,
		Result:      result,
		CapturedAt:  e.CapturedAt.Time.Format(time.RFC3339),
	}
}

// GetIssueQAEvidence returns the freshest QA evidence row for an issue, or null
// when none has been captured yet (the QA section then shows an empty state +
// "Re-run evidence"). A null body is a normal, expected response — not an error.
func (h *Handler) GetIssueQAEvidence(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	evidence, err := h.Queries.GetLatestQAEvidenceForIssue(r.Context(), db.GetLatestQAEvidenceForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusOK, nil)
			return
		}
		slog.Warn("get qa evidence failed", "error", err, "issue_id", uuidToString(issue.ID))
		writeError(w, http.StatusInternalServerError, "failed to load qa evidence")
		return
	}
	writeJSON(w, http.StatusOK, qaEvidenceToResponse(evidence))
}
