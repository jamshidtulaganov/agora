package handler

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// The design stage: an issue that references a Figma design gets a
// designer-analyst pass (the design_proposal slice action) that reads the
// design, maps it against the project's design system, and proposes an
// implementation decomposition for a human to approve. This file holds the
// handler-side helpers — agent resolution and the project design-manifest
// context injected into the recipe. Capture + labels live in the service layer
// (service/design_proposal.go) because the agent-comment ingest points are
// there.

// resolveDesignerAgent resolves the agent a design_proposal targets when the
// caller did not pin one explicitly. Order (mirrors projectDocsAgentID /
// qaSquadLeader):
//  1. project.settings.design_agent — a dedicated designer-analyst agent.
//  2. the leader of a squad whose name contains "design".
//
// Returns ok=false when neither resolves (the slice-action resolver then falls
// through to the issue assignee / caller's own agent). Every candidate must be
// ready (has a runtime, not archived).
func (h *Handler) resolveDesignerAgent(ctx context.Context, issue db.Issue) (db.Agent, bool) {
	if id := h.projectDesignAgentID(ctx, issue); id != "" {
		if agentUUID, err := util.ParseUUID(id); err == nil {
			agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
				ID:          agentUUID,
				WorkspaceID: issue.WorkspaceID,
			})
			if err == nil && sliceAgentReady(agent) {
				return agent, true
			}
		}
	}
	if leader, ok := h.designSquadLeader(ctx, issue.WorkspaceID); ok {
		return leader, true
	}
	return db.Agent{}, false
}

// projectDesignAgentID reads the project's configured design agent (an agent
// UUID in project.settings.design_agent). Empty when unset. Mirrors
// projectDocsAgentID.
func (h *Handler) projectDesignAgentID(ctx context.Context, issue db.Issue) string {
	if !issue.ProjectID.Valid {
		return ""
	}
	project, err := h.Queries.GetProject(ctx, issue.ProjectID)
	if err != nil || len(project.Settings) == 0 {
		return ""
	}
	var s struct {
		DesignAgent string `json:"design_agent"`
	}
	if json.Unmarshal(project.Settings, &s) != nil {
		return ""
	}
	return strings.TrimSpace(s.DesignAgent)
}

// designSquadLeader resolves the leader of a squad whose name contains
// "design" (case-insensitive). Mirrors qaSquadLeader. ok=false when there is no
// design squad, it has no leader, or the leader is archived / not ready.
func (h *Handler) designSquadLeader(ctx context.Context, wsID pgtype.UUID) (db.Agent, bool) {
	squads, err := h.Queries.ListSquads(ctx, wsID)
	if err != nil {
		return db.Agent{}, false
	}
	for _, s := range squads {
		if !strings.Contains(strings.ToLower(s.Name), "design") || !s.LeaderID.Valid {
			continue
		}
		leader, err := h.Queries.GetAgent(ctx, s.LeaderID)
		if err == nil && !leader.ArchivedAt.Valid && sliceAgentReady(leader) {
			return leader, true
		}
	}
	return db.Agent{}, false
}

// sliceActionDesignManifestContext injects the project's design system
// (project.settings.design_manifest) into the design_proposal / implementation
// prompts so the designer maps against a KNOWN component inventory instead of
// re-discovering it every run. Phase 3 authors and renders the manifest; this
// stub returns "" so the call sites (design_proposal recipe assembly) are wired
// now and light up when Phase 3 lands. Mirrors sliceActionQAManifestContext.
func (h *Handler) sliceActionDesignManifestContext(ctx context.Context, issue db.Issue) string {
	return ""
}
