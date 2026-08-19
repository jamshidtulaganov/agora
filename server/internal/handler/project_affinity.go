package handler

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Project-affine agent resolution.
//
// The workspace-level pools (the QA squad, the QA leader of last resort) pick
// the least-busy READY agent with no regard for which PROJECT an issue belongs
// to — observed live as an SD Bridge engineer running the QA gate on a Bitrix
// project's issue. A project that has bound a squad (project.squad_id) has
// declared who its work belongs to, so dispatches on its issues prefer those
// agents; the workspace-wide pool remains the fallback for projects that have
// not bound one, and for issues with no project at all.

// projectSquadAgents returns the READY agents of the squad bound to the issue's
// project (leader plus agent members, deduped). Empty when the issue has no
// project, the project no squad, or nobody in it is ready.
func (h *Handler) projectSquadAgents(ctx context.Context, issue db.Issue) []db.Agent {
	if !issue.ProjectID.Valid {
		return nil
	}
	project, err := h.Queries.GetProject(ctx, issue.ProjectID)
	if err != nil || !project.SquadID.Valid {
		return nil
	}
	squad, err := h.Queries.GetSquad(ctx, project.SquadID)
	if err != nil || squad.ArchivedAt.Valid {
		return nil
	}
	seen := map[string]bool{}
	var agents []db.Agent
	add := func(id pgtype.UUID) {
		if !id.Valid {
			return
		}
		k := uuidToString(id)
		if seen[k] {
			return
		}
		seen[k] = true
		a, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: id, WorkspaceID: issue.WorkspaceID})
		if err == nil && !a.ArchivedAt.Valid && sliceAgentReady(a) {
			agents = append(agents, a)
		}
	}
	add(squad.LeaderID)
	if members, err := h.Queries.ListSquadMembers(ctx, squad.ID); err == nil {
		for _, m := range members {
			if m.MemberType == "agent" {
				add(m.MemberID)
			}
		}
	}
	return agents
}

// qaAgentsForIssue is the project-affine QA pool. Preference order:
//
//  1. QA-squad agents that are ALSO in the project's squad — QA stays the
//     cross-cutting discipline it was designed as, scoped to the people who
//     know this project;
//  2. the project's squad agents, minus the issue's author agent (a teammate
//     may run the gate; the author must not pass their own work);
//  3. the workspace QA squad, unchanged — the pool every project without a
//     bound squad already uses.
func (h *Handler) qaAgentsForIssue(ctx context.Context, issue db.Issue) []db.Agent {
	qa := h.qaSquadAgents(ctx, issue.WorkspaceID)
	project := h.projectSquadAgents(ctx, issue)
	if len(project) == 0 {
		return qa
	}
	inProject := make(map[string]bool, len(project))
	for _, a := range project {
		inProject[uuidToString(a.ID)] = true
	}
	var affine []db.Agent
	for _, a := range qa {
		if inProject[uuidToString(a.ID)] {
			affine = append(affine, a)
		}
	}
	if len(affine) > 0 {
		return affine
	}
	authorID := ""
	if issue.AssigneeType.Valid && issue.AssigneeType.String == "agent" && issue.AssigneeID.Valid {
		authorID = uuidToString(issue.AssigneeID)
	}
	var own []db.Agent
	for _, a := range project {
		if uuidToString(a.ID) != authorID {
			own = append(own, a)
		}
	}
	if len(own) > 0 {
		return own
	}
	return qa
}

// projectReviewerAgent picks a reviewer from the project's squad, preferring its
// leader, always excluding the author agent. ok=false when the project binds no
// squad or nobody distinct from the author is ready.
func (h *Handler) projectReviewerAgent(ctx context.Context, issue db.Issue) (db.Agent, bool) {
	agents := h.projectSquadAgents(ctx, issue)
	if len(agents) == 0 {
		return db.Agent{}, false
	}
	authorID := ""
	if issue.AssigneeType.Valid && issue.AssigneeType.String == "agent" && issue.AssigneeID.Valid {
		authorID = uuidToString(issue.AssigneeID)
	}
	var candidates []db.Agent
	for _, a := range agents {
		if uuidToString(a.ID) != authorID {
			candidates = append(candidates, a)
		}
	}
	if len(candidates) == 0 {
		return db.Agent{}, false
	}
	// The squad's leader reviews when it can (the same convention the dev-squad
	// path uses); otherwise the least-busy member.
	if project, err := h.Queries.GetProject(ctx, issue.ProjectID); err == nil && project.SquadID.Valid {
		if squad, err := h.Queries.GetSquad(ctx, project.SquadID); err == nil && squad.LeaderID.Valid {
			leaderID := uuidToString(squad.LeaderID)
			for _, a := range candidates {
				if uuidToString(a.ID) == leaderID {
					return a, true
				}
			}
		}
	}
	return h.pickLeastBusyQAAgent(ctx, candidates), true
}

// issueBelongsToProjectNamed is a tiny debug helper used in logs only.
func issueBelongsToProjectNamed(project db.Project, name string) bool {
	return strings.EqualFold(strings.TrimSpace(project.Title), strings.TrimSpace(name))
}
