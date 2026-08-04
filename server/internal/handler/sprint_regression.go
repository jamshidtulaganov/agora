package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Sprint-end regression dispatch. A whole-branch regression of the sprint
// branch against sprint-root, run by the project's sprint-end (run-only)
// autopilot. The QA target rides in the payload (the project's qa_smoke_url —
// the team's BYO staging); there is no Agora-managed deploy step (the team's
// own CI puts the sprint branch on staging).

// sprintBranchName is the FALLBACK git branch convention for a sprint
// (`sprint/<sprintId>`) when no explicit branch is set. Single source of truth
// for the convention.
func sprintBranchName(sprintID pgtype.UUID) string {
	return "sprint/" + uuidToString(sprintID)
}

// SprintBranchFor returns the real git branch a sprint's work lives on: the
// branch the team set on the sprint (e.g. "billing" or "sprint-9"), or the
// sprint/<id> convention when unset. The QA tiers (per-task / daily / sprint-end)
// all resolve the branch through this so they agree.
func SprintBranchFor(sprint db.Sprint) string {
	if b := strings.TrimSpace(sprint.Branch); b != "" {
		return b
	}
	return sprintBranchName(sprint.ID)
}

// sprintRegressionPayload is the trigger payload a sprint regression run's
// agent receives — the QA directive: a whole-branch regression of the sprint
// branch against sprint-root. Same shape whether the scheduler or a human
// fired it; only AutopilotRun.Source differs ("schedule" vs "manual").
type sprintRegressionPayload struct {
	Scope    string `json:"scope"`
	Branch   string `json:"branch"`
	Baseline string `json:"baseline"`
	SprintID string `json:"sprint_id"`
	// Tasks the sprint explicitly covers (issue_to_sprint members). The agent
	// scopes regression to these — running each task's promoted test cases plus
	// the project base suite — instead of inferring scope from the branch diff
	// alone. Empty = fall back to whole-branch regression.
	Tasks []sprintRegressionTask `json:"tasks,omitempty"`
	// Directive carries the scope-keyed baseline guidance (the same text issue
	// slices get) so the whole-branch fallback isn't a bare JSON blob the agent
	// must interpret unaided.
	Directive string `json:"directive,omitempty"`
	// QATarget is the deployed app the regression drives (the project's
	// qa_smoke_url). Without it the agent has no address for the browser-level
	// suite and silently degrades to code-only checks.
	QATarget string `json:"qa_target,omitempty"`
	// RepoURL names the project's primary repo so an issue-less run (no task
	// worktree, no issue→project resource injection) still knows which code the
	// sprint branch lives in.
	RepoURL string `json:"repo_url,omitempty"`
	// ResultsIssue is the issue KEY the agent posts its ```test-runs``` block
	// on — CaptureTestRuns needs an issue in the cases' project to accept the
	// rows. The project's base-suite tracking issue when it still exists, else
	// the sprint's first attached task.
	ResultsIssue string `json:"results_issue,omitempty"`
	// Cases is the project's standing base suite (compiled automated cases).
	// Embedded verbatim because an issue-less autopilot run gets none of the
	// run_test_cases slice injection.
	Cases []sprintRegressionCase `json:"cases,omitempty"`
}

type sprintRegressionTask struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

type sprintRegressionCase struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Script string `json:"script,omitempty"`
}

// DispatchSprintRegression dispatches the project's sprint-end (run-only)
// autopilot with a scope=regression / baseline=sprint-root payload — a
// whole-branch regression of the sprint branch, distinct from the per-task
// scope=task QA that fires on each issue's in_review transition. Shared by the
// sprint-end scheduler (source="schedule", cmd/server/sprint_end_scheduler.go)
// and a human-triggered re-run from the QA review page (source="manual",
// RunIssueSprintRegression below). The QA target rides in the payload (the
// project's qa_smoke_url — the team's BYO staging); Agora runs no deploy step.
// Returns an error when no run-only autopilot is bound to the project.
func (h *Handler) DispatchSprintRegression(ctx context.Context, sprintID, wsID pgtype.UUID, source string) (db.AutopilotRun, error) {
	sprint, err := h.Queries.GetSprint(ctx, db.GetSprintParams{ID: sprintID, WorkspaceID: wsID})
	if err != nil {
		return db.AutopilotRun{}, fmt.Errorf("sprint not found: %w", err)
	}
	branch := SprintBranchFor(sprint)

	autopilots, err := h.Queries.ListActiveRunOnlyAutopilotsForProject(ctx, db.ListActiveRunOnlyAutopilotsForProjectParams{
		WorkspaceID: sprint.WorkspaceID,
		ProjectID:   sprint.ProjectID,
	})
	if err != nil {
		return db.AutopilotRun{}, fmt.Errorf("list sprint-end autopilots: %w", err)
	}
	if len(autopilots) == 0 {
		return db.AutopilotRun{}, fmt.Errorf("no sprint-end (run-only) autopilot is bound to this sprint's project")
	}

	// Scope the regression to the sprint's attached tasks (issue_to_sprint) so a
	// user curating the sprint in the QA cockpit controls what gets tested.
	// Best-effort: on lookup failure fall back to whole-branch regression.
	var tasks []sprintRegressionTask
	if issues, ierr := h.Queries.ListIssuesBySprint(ctx, db.ListIssuesBySprintParams{SprintID: sprint.ID}); ierr == nil {
		prefix := h.getIssuePrefix(ctx, sprint.WorkspaceID)
		for _, iss := range issues {
			tasks = append(tasks, sprintRegressionTask{
				Key:   fmt.Sprintf("%s-%d", prefix, iss.Number),
				Title: iss.Title,
			})
		}
	}

	// The QA target + primary repo + base suite ride in the payload: an
	// issue-less run gets none of the per-issue slice injection, so this payload
	// IS the whole QA contract for the run. Assembled by the shared builder,
	// which the generic deploy-triggered project regression reuses verbatim.
	payload, err := h.assembleRegressionPayload(ctx, sprint.ProjectID, sprint.WorkspaceID, branch, "sprint-root", uuidToString(sprint.ID), "", tasks)
	if err != nil {
		return db.AutopilotRun{}, fmt.Errorf("assemble regression payload: %w", err)
	}

	// Pick the QA-regression autopilot, not just autopilots[0]. A project may
	// have several run-only autopilots (e.g. a "Weekly docs sweep" alongside a
	// regression one); dispatching a whole-branch QA regression to a docs
	// autopilot runs the wrong agent.
	ap := pickRegressionAutopilot(autopilots)
	run, err := h.AutopilotService.DispatchAutopilot(ctx, ap, pgtype.UUID{}, source, payload)
	if err != nil {
		return db.AutopilotRun{}, fmt.Errorf("dispatch regression: %w", err)
	}
	return *run, nil
}

// pickRegressionAutopilot chooses the best run-only autopilot to carry a sprint
// regression: a title signalling regression/QA wins; otherwise the first one
// whose title doesn't look like a docs job; otherwise the first (preserving the
// original single-autopilot behavior). Purely title-based — autopilots carry no
// explicit purpose field — so name a project's regression autopilot with "QA"
// or "regression" for it to be chosen over siblings.
func pickRegressionAutopilot(aps []db.Autopilot) db.Autopilot {
	for _, ap := range aps {
		t := strings.ToLower(ap.Title)
		if strings.Contains(t, "regression") || strings.Contains(t, "qa") {
			return ap
		}
	}
	for _, ap := range aps {
		if !strings.Contains(strings.ToLower(ap.Title), "docs") {
			return ap
		}
	}
	return aps[0]
}

// RunIssueSprintRegression lets a human fire the SAME whole-branch regression
// the sprint-end scheduler runs automatically, from wherever they're already
// looking at one of the sprint's issues (the QA review page) — without
// requiring them to know the sprint id or navigate to a separate sprint admin
// surface. Resolves the issue's sprint via GetSprintForIssue; 404 when the
// issue isn't on a sprint (the regression concept doesn't apply) or no
// sprint-end autopilot is configured. POST /api/issues/{id}/run-regression.
func (h *Handler) RunIssueSprintRegression(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	sprint, err := h.Queries.GetSprintForIssue(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "this issue is not part of a sprint")
		return
	}

	run, err := h.DispatchSprintRegression(r.Context(), sprint.ID, issue.WorkspaceID, "manual")
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, runToResponse(run))
}
