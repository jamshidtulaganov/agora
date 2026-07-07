package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
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

// qaVerdictSummary is one issue's freshest QA verdict for the cockpit rows —
// enough to answer "why is this here" without opening the issue.
type qaVerdictSummary struct {
	Verdict    string `json:"verdict"`
	Source     string `json:"source"`
	Summary    string `json:"summary"`
	CapturedAt string `json:"captured_at"`
}

// ListQAVerdicts returns the freshest qa_evidence verdict per in_review issue
// (optionally scoped to one project) keyed by issue id — the QA cockpit's
// row-level "reason + provenance + age" data. GET /api/qa/verdicts.
// This finally wires the previously-unused ListQAEvidenceSummariesForIssues
// (the audit found the cockpit trusting sticky labels while this sat idle).
func (h *Handler) ListQAVerdicts(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	wsID := h.resolveWorkspaceID(r)
	if wsID == "" {
		writeError(w, http.StatusBadRequest, "workspace required")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace_id")
	if !ok {
		return
	}
	var projID pgtype.UUID
	if raw := strings.TrimSpace(r.URL.Query().Get("project_id")); raw != "" {
		if id, perr := util.ParseUUID(raw); perr == nil {
			projID = id
		}
	}

	rows, err := h.DB.Query(r.Context(),
		`SELECT id FROM issue
		 WHERE workspace_id = $1 AND status = 'in_review'
		   AND ($2::uuid IS NULL OR project_id = $2)
		 LIMIT 500`, wsUUID, projID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"verdicts": map[string]any{}})
		return
	}
	var ids []pgtype.UUID
	for rows.Next() {
		var id pgtype.UUID
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	out := map[string]qaVerdictSummary{}
	if len(ids) > 0 {
		sums, serr := h.Queries.ListQAEvidenceSummariesForIssues(r.Context(), db.ListQAEvidenceSummariesForIssuesParams{
			WorkspaceID: wsUUID, Column2: ids,
		})
		if serr == nil {
			for _, s := range sums {
				item := qaVerdictSummary{Verdict: s.Verdict, Source: s.Source, Summary: s.Summary}
				if s.CapturedAt.Valid {
					item.CapturedAt = s.CapturedAt.Time.Format(time.RFC3339)
				}
				out[uuidToString(s.IssueID)] = item
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"verdicts": out})
}
