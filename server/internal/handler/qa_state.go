package handler

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/service"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
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
	return h.issueHasRunningQATaskWithSet(ctx, issue, h.qaSquadAgentIDSet(ctx, issue.WorkspaceID))
}

// qaSquadAgentIDSet resolves the QA squad's agent ids once — batch callers
// (ListQAVerdicts) hoist this out of their per-issue loop instead of paying
// the squad lookups N times.
func (h *Handler) qaSquadAgentIDSet(ctx context.Context, wsID pgtype.UUID) map[string]bool {
	agents := h.qaSquadAgents(ctx, wsID)
	if len(agents) == 0 {
		return nil // no QA squad → no filter (any running task counts)
	}
	set := make(map[string]bool, len(agents))
	for _, a := range agents {
		set[uuidToString(a.ID)] = true
	}
	return set
}

// issueHasRunningQATaskWithSet is issueHasRunningQATask with the QA-agent id
// set supplied by the caller (nil = no QA squad = no filter).
func (h *Handler) issueHasRunningQATaskWithSet(ctx context.Context, issue db.Issue, qaAgentIDs map[string]bool) bool {
	tasks, err := h.Queries.ListActiveTasksByIssue(ctx, issue.ID)
	if err != nil {
		return false
	}
	for _, t := range tasks {
		if t.Status != "running" {
			continue
		}
		if qaAgentIDs == nil || qaAgentIDs[uuidToString(t.AgentID)] {
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

// issueKnownHeadSha resolves the issue's CURRENT head commit, when knowable:
// the head sha of its OPEN, unmerged pull request (the branch under QA —
// synced by the PR machinery the backend already runs). Merged/closed PRs
// don't count (their head is history, not the issue's current state), and an
// issue with no open PR — sprint-branch mode, local-worktree mode, no linked
// repo — returns "" (unknowable → the caller makes NO staleness claim;
// fail-open by design). First open PR wins, mirroring
// enforceSprintPRMergedBeforeDone's selection.
func (h *Handler) issueKnownHeadSha(ctx context.Context, issue db.Issue) string {
	prs, err := h.Queries.ListPullRequestsByIssue(ctx, issue.ID)
	if err != nil {
		return ""
	}
	for i := range prs {
		pr := &prs[i]
		if pr.MergedAt.Valid {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(pr.State), "open") {
			return strings.ToLower(strings.TrimSpace(pr.HeadSha))
		}
	}
	return ""
}

// reconciledQAState gathers every signal service.ReconcileQAState needs for
// one issue and folds them into the single reconciled state — the ONE truth
// the qa-evidence response, the merge-readiness qa gate, and (via the API)
// the qa-lens chip and qa-lane lanes all read.
//
// hasEvidence is passed in by the caller rather than re-queried here: most
// callers already resolved a qa_evidence read for their own response and
// re-querying it here would be a redundant round trip. The evidence COMMIT
// SHA (Phase 3 — stale-green invalidation) is re-read here when evidence
// exists: a one-row indexed lookup, paid only to compare against the issue's
// known head (an open PR's head sha) so a verdict on an outdated commit
// reconciles to stale.
func (h *Handler) reconciledQAState(ctx context.Context, issue db.Issue, hasEvidence bool) service.QAState {
	labels := h.issueQALabelSet(ctx, issue)
	runs := h.issueLatestCaseRunStatuses(ctx, issue)
	running := h.issueHasRunningQATask(ctx, issue)
	evidenceSha, headSha := "", ""
	if hasEvidence {
		if ev, err := h.Queries.GetLatestQAEvidenceForIssue(ctx, db.GetLatestQAEvidenceForIssueParams{
			IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
		}); err == nil {
			evidenceSha = ev.CommitSha
		}
		// Only resolve the head when there is an evidence sha to compare —
		// the PR lookup is pointless otherwise (fail-open either way).
		if evidenceSha != "" {
			headSha = h.issueKnownHeadSha(ctx, issue)
		}
	}
	return service.ReconcileQAState(labels, runs, running, hasEvidence, evidenceSha, headSha)
}
