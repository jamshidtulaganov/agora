package handler

import (
	"context"
	"strings"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// issueQALabelSet reads the issue's label names as a lowercase-normalized
// set, for feeding into service.ReconcileQAState (which reads exact
// "qa:pass" / "qa:fail" / "qa:blocked" / "qa:stale" keys). Best-effort — a
// query error reads as "no labels" rather than failing the caller.
func (h *Handler) issueQALabelSet(ctx context.Context, issue db.Issue) map[string]bool {
	rows, err := h.Queries.ListLabelsByIssue(ctx, db.ListLabelsByIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(rows))
	for _, l := range rows {
		out[strings.ToLower(strings.TrimSpace(l.Name))] = true
	}
	return out
}

// issueHasRunningQATask reports whether a QA-squad agent task is executing on
// this issue RIGHT NOW — the server-side mirror of the frontend's
// useQaRunningTasks (qa-live-progress.tsx): the same "leader + agent members
// of any squad named like qa" resolution (qaSquadAgents) and the same
// "running" status filter, so the two surfaces can never disagree about "is
// QA running". Empty QA squad → no filter (any running task on the issue
// counts), mirroring the frontend hook's own fallback.
func (h *Handler) issueHasRunningQATask(ctx context.Context, issue db.Issue) bool {
	tasks, err := h.Queries.ListActiveTasksByIssue(ctx, issue.ID)
	if err != nil {
		return false
	}
	agents := h.qaSquadAgents(ctx, issue.WorkspaceID)
	if len(agents) == 0 {
		for _, t := range tasks {
			if t.Status == "running" {
				return true
			}
		}
		return false
	}
	qaAgentIDs := make(map[string]bool, len(agents))
	for _, a := range agents {
		qaAgentIDs[uuidToString(a.ID)] = true
	}
	for _, t := range tasks {
		if t.Status == "running" && qaAgentIDs[uuidToString(t.AgentID)] {
			return true
		}
	}
	return false
}

// issueLatestCaseRunStatuses resolves the latest run per test case for this
// issue (the issue's own cases plus project base-suite runs recorded against
// it — mirrors ListLatestRunsForIssueCases' own doc comment), reduced to the
// .Status field service.ReconcileQAState reads.
func (h *Handler) issueLatestCaseRunStatuses(ctx context.Context, issue db.Issue) []service.QACaseRunStatus {
	runs, err := h.Queries.ListLatestRunsForIssueCases(ctx, db.ListLatestRunsForIssueCasesParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return nil
	}
	out := make([]service.QACaseRunStatus, 0, len(runs))
	for _, r := range runs {
		out = append(out, service.QACaseRunStatus{Status: r.Status})
	}
	return out
}

// reconciledQAState gathers every signal service.ReconcileQAState needs for
// one issue and folds them into the single reconciled state — the ONE truth
// the qa-evidence response, the merge-readiness qa gate, and (via the API)
// the qa-lens chip and qa-lane lanes all read.
//
// hasEvidence is passed in by the caller rather than re-queried here: most
// callers already resolved a qa_evidence read for their own response and
// re-querying it here would be a redundant round trip.
func (h *Handler) reconciledQAState(ctx context.Context, issue db.Issue, hasEvidence bool) service.QAState {
	labels := h.issueQALabelSet(ctx, issue)
	runs := h.issueLatestCaseRunStatuses(ctx, issue)
	running := h.issueHasRunningQATask(ctx, issue)
	return service.ReconcileQAState(labels, runs, running, hasEvidence)
}
