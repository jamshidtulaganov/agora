package handler

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Sprint QA-readiness — "is this sprint mergeable?" for the QA cockpit's Sprint
// tab. One call returns every active sprint in the workspace with its per-issue
// QA rows + a green/blocked rollup, so the page renders without per-sprint
// round trips (a workspace rarely has more than a couple active sprints).

type sprintReadinessIssue struct {
	ID       string `json:"id"`
	Number   int32  `json:"number"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	QAPass   bool   `json:"qa_pass"`
	QAFail   bool   `json:"qa_fail"`
	RunsPass int64  `json:"runs_pass"`
	RunsFail int64  `json:"runs_fail"`
	RunsTotal int64 `json:"runs_total"`
	// verdict: the rolled-up state used for the row chip + the rollup counts.
	// fail if qa:fail or any failing run; pass if qa:pass and no failing run;
	// else pending.
	Verdict string `json:"verdict"`
}

// sprintRegressionGate is the sprint's latest whole-branch regression run (the
// daily backstop / sprint-end gate). Empty Status = never run.
type sprintRegressionGate struct {
	Status      string `json:"status"`
	Source      string `json:"source"`
	TriggeredAt string `json:"triggered_at"`
	CompletedAt string `json:"completed_at"`
	Reason      string `json:"reason"`
}

type sprintReadiness struct {
	SprintID     string                 `json:"sprint_id"`
	Name         string                 `json:"name"`
	Branch       string                 `json:"branch"`
	ProjectID    string                 `json:"project_id"`
	ProjectTitle string                 `json:"project_title"`
	Total        int                    `json:"total"`
	Passed       int                    `json:"passed"`
	Failed       int                    `json:"failed"`
	Pending      int                    `json:"pending"`
	NoQA         int                    `json:"no_qa"`
	Mergeable    bool                   `json:"mergeable"`
	Regression   *sprintRegressionGate  `json:"regression"`
	Issues       []sprintReadinessIssue `json:"issues"`
}

type sprintReadinessResponse struct {
	Sprints []sprintReadiness `json:"sprints"`
}

// GetSprintReadiness returns per-sprint QA readiness for the workspace's active
// sprints. GET /api/qa/sprint-readiness.
func (h *Handler) GetSprintReadiness(w http.ResponseWriter, r *http.Request) {
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
	ctx := r.Context()

	resp := sprintReadinessResponse{Sprints: []sprintReadiness{}}
	sprints, err := h.Queries.ListActiveSprintsForWorkspace(ctx, wsUUID)
	if err != nil {
		writeJSON(w, http.StatusOK, resp) // fail soft — empty rather than 500
		return
	}

	for _, s := range sprints {
		sr := sprintReadiness{
			SprintID:     uuidToString(s.ID),
			Name:         s.Name,
			Branch:       s.Branch,
			ProjectID:    uuidToString(s.ProjectID),
			ProjectTitle: s.ProjectTitle,
			Issues:       []sprintReadinessIssue{},
		}
		rows, err := h.Queries.SprintReadinessRows(ctx, db.SprintReadinessRowsParams{
			SprintID:    s.ID,
			WorkspaceID: wsUUID,
		})
		if err == nil {
			for _, row := range rows {
				verdict := "pending"
				switch {
				case row.QaFail || row.RunsFail > 0:
					verdict = "fail"
				case row.QaPass && row.RunsFail == 0:
					verdict = "pass"
				}
				switch verdict {
				case "pass":
					sr.Passed++
				case "fail":
					sr.Failed++
				default:
					sr.Pending++
					// never QA'd at all (no label, no run) — the backlog QA hasn't reached.
					if !row.QaPass && !row.QaFail && row.RunsTotal == 0 {
						sr.NoQA++
					}
				}
				sr.Issues = append(sr.Issues, sprintReadinessIssue{
					ID:        uuidToString(row.ID),
					Number:    row.Number,
					Title:     row.Title,
					Status:    row.Status,
					QAPass:    row.QaPass,
					QAFail:    row.QaFail,
					RunsPass:  row.RunsPass,
					RunsFail:  row.RunsFail,
					RunsTotal: row.RunsTotal,
					Verdict:   verdict,
				})
			}
		}
		sr.Total = len(sr.Issues)
		// Mergeable = every issue passed and nothing failed (an empty sprint is
		// not "mergeable" — nothing has been verified).
		sr.Mergeable = sr.Total > 0 && sr.Failed == 0 && sr.Pending == 0

		// Regression gate: the sprint's latest whole-branch regression run.
		if run, err := h.Queries.LatestSprintRegressionRun(ctx, []byte(uuidToString(s.ID))); err == nil {
			g := &sprintRegressionGate{Status: run.Status, Source: run.Source}
			if run.TriggeredAt.Valid {
				g.TriggeredAt = run.TriggeredAt.Time.Format(time.RFC3339)
			}
			if run.CompletedAt.Valid {
				g.CompletedAt = run.CompletedAt.Time.Format(time.RFC3339)
			}
			if run.FailureReason.Valid {
				g.Reason = run.FailureReason.String
			}
			sr.Regression = g
		}
		resp.Sprints = append(resp.Sprints, sr)
	}

	writeJSON(w, http.StatusOK, resp)
}

// RunSprintRegression fires the whole-branch regression for a sprint directly
// (the sprint-level counterpart to RunIssueSprintRegression) — the Sprint tab's
// "Run regression" action. POST /api/sprints/{id}/run-regression.
func (h *Handler) RunSprintRegression(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	wsID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace_id")
	if !ok {
		return
	}
	sprintUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "sprint id")
	if !ok {
		return
	}
	run, err := h.DispatchSprintRegression(r.Context(), sprintUUID, wsUUID, "manual")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, runToResponse(run))
}
