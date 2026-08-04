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
	"github.com/jamshidtulaganov/agora/server/internal/service"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// QAEvidenceResponse is the durable, evidence-first QA verdict for an issue. The
// QA section reads this single indexed row instead of re-parsing the timeline,
// so opening any of N in-review tasks is a cheap read. result is the verbatim
// ```qa-result``` payload (verdict / summary / commands / screenshots).
//
// ReconciledState is the server-computed single source of truth (Phase 2 of
// the QA-stage review — see service.ReconcileQAState): the SAME enum the
// merge-readiness qa gate uses, folding in labels, per-case run results, and
// whether a QA task is running right now — not just this row's own verdict
// field. Only populated when an evidence row exists (this endpoint still
// returns a bare `null` body when none has been captured — see
// GetIssueQAEvidence — so existing `!evidence` consumers are unaffected); a
// plain string (not a strict enum) so an old frontend simply ignores it and a
// new frontend on an old server that omits it falls back to its own
// label-derived computation (parseWithFallback + the schema default).
type QAEvidenceResponse struct {
	ID              string          `json:"id"`
	IssueID         string          `json:"issue_id"`
	BaselineRef     string          `json:"baseline_ref"`
	BranchSha       string          `json:"branch_sha"`
	Verdict         string          `json:"verdict"`
	Source          string          `json:"source"`
	Summary         string          `json:"summary"`
	Result          json.RawMessage `json:"result"`
	CapturedAt      string          `json:"captured_at"`
	ReconciledState string          `json:"reconciled_state"`
	// Run identity (Phase 3, migration 157): which commit the verdict judged
	// ("" = unreported/legacy), who fired the gate (agent|human|auto, "" =
	// legacy), and the gate's timing (RFC3339, "" = unknown).
	CommitSha   string `json:"commit_sha"`
	TriggeredBy string `json:"triggered_by"`
	StartedAt   string `json:"started_at"`
	FinishedAt  string `json:"finished_at"`
}

func qaEvidenceToResponse(e db.QaEvidence, reconciledState service.QAState) QAEvidenceResponse {
	result := json.RawMessage(e.ResultJson)
	if len(result) == 0 {
		result = json.RawMessage("{}")
	}
	resp := QAEvidenceResponse{
		ID:              uuidToString(e.ID),
		IssueID:         uuidToString(e.IssueID),
		BaselineRef:     e.BaselineRef,
		BranchSha:       e.BranchSha,
		Verdict:         e.Verdict,
		Source:          e.Source,
		Summary:         e.Summary,
		Result:          result,
		CapturedAt:      e.CapturedAt.Time.Format(time.RFC3339),
		ReconciledState: string(reconciledState),
		CommitSha:       e.CommitSha,
		TriggeredBy:     e.TriggeredBy,
	}
	if e.StartedAt.Valid {
		resp.StartedAt = e.StartedAt.Time.Format(time.RFC3339)
	}
	if e.FinishedAt.Valid {
		resp.FinishedAt = e.FinishedAt.Time.Format(time.RFC3339)
	}
	return resp
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
	reconciled := h.reconciledQAState(r.Context(), issue, true)
	writeJSON(w, http.StatusOK, qaEvidenceToResponse(evidence, reconciled))
}

// qaVerdictSummary is one issue's freshest QA verdict for the cockpit rows —
// enough to answer "why is this here" without opening the issue.
type qaVerdictSummary struct {
	Verdict    string `json:"verdict"`
	Source     string `json:"source"`
	Summary    string `json:"summary"`
	CapturedAt string `json:"captured_at"`
	// ReconciledState (Phase 3): the SAME server-computed enum the qa-evidence
	// endpoint returns, batch-computed per in_review issue so the cockpit's
	// Stale / Needs-human filters read one truth instead of re-deriving it
	// client-side. "" for issues the batch couldn't reconcile (fail-open — the
	// client falls back to its label-derived state).
	ReconciledState string `json:"reconciled_state"`
	// TriggeredBy: who fired the gate that produced this verdict
	// (agent|human|auto, "" = legacy).
	TriggeredBy string `json:"triggered_by"`
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
			// Reconcile per evidence-bearing issue with the QA-agent set hoisted
			// out of the loop (the per-issue reads — labels, latest runs, active
			// tasks, PR head — stay, bounded by the 500-issue cap + the client's
			// 15s staleTime).
			qaAgentIDs := h.qaSquadAgentIDSet(r.Context(), wsUUID)
			for _, s := range sums {
				item := qaVerdictSummary{Verdict: s.Verdict, Source: s.Source, Summary: s.Summary, TriggeredBy: s.TriggeredBy}
				if s.CapturedAt.Valid {
					item.CapturedAt = s.CapturedAt.Time.Format(time.RFC3339)
				}
				stub := db.Issue{ID: s.IssueID, WorkspaceID: wsUUID}
				labels := h.issueQALabelSet(r.Context(), stub)
				runs := h.issueLatestCaseRunStatuses(r.Context(), stub)
				running := h.issueHasRunningQATaskWithSet(r.Context(), stub, qaAgentIDs)
				headSha := ""
				if s.CommitSha != "" {
					headSha = h.issueKnownHeadSha(r.Context(), stub)
				}
				item.ReconciledState = string(service.ReconcileQAState(labels, runs, running, true, s.CommitSha, headSha))
				out[uuidToString(s.IssueID)] = item
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"verdicts": out})
}
