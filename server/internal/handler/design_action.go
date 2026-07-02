package handler

import (
	"context"
	"encoding/json"
	"fmt"
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

// designManifest is the project's KNOWN design system (stored in
// project.settings.design_manifest). Dual-kind: "tokens" for modern
// token-based repos, "inventory" for legacy monoliths (sd-main PHP/Yii+Vue)
// whose de-facto system is derived from existing markup. Injected into the
// designer + implementation prompts so agents build against the known system
// instead of re-discovering it each run. Authored ONCE per project (agent-
// generated + human-editable) and reused by every run — the design counterpart
// to qaManifest.
type designManifest struct {
	Kind      string `json:"kind"`   // tokens | inventory
	Source    string `json:"source"` // agent | manual | mixed
	Revision  int    `json:"revision"`
	UpdatedAt string `json:"updated_at"`
	Figma     struct {
		LibraryFileKey string `json:"library_file_key"`
		Notes          string `json:"notes"`
	} `json:"figma"`
	Tokens struct {
		Colors     map[string]string `json:"colors"`
		Typography map[string]string `json:"typography"`
		Spacing    map[string]string `json:"spacing"`
	} `json:"tokens"`
	Components []struct {
		Name        string `json:"name"`
		CodeRef     string `json:"code_ref"`
		FigmaNodeID string `json:"figma_node_id"`
		Usage       string `json:"usage"`
	} `json:"components"`
	Conventions      []string `json:"conventions"`
	AntiPatterns     []string `json:"anti_patterns"`
	LegacyNotes      string   `json:"legacy_notes"`
	ScreensReference string   `json:"screens_reference"`
}

// designManifestMaxComponents caps how many components are rendered into the
// prompt so a large inventory can't blow the context budget.
const designManifestMaxComponents = 40

// projectDesignManifest reads + unmarshals the project's design manifest.
// ok=false when the issue has no project, the project has no manifest, or it is
// unparseable.
func (h *Handler) projectDesignManifest(ctx context.Context, issue db.Issue) (designManifest, bool) {
	if !issue.ProjectID.Valid {
		return designManifest{}, false
	}
	project, err := h.Queries.GetProject(ctx, issue.ProjectID)
	if err != nil || len(project.Settings) == 0 {
		return designManifest{}, false
	}
	var settings struct {
		Manifest *designManifest `json:"design_manifest"`
	}
	if json.Unmarshal(project.Settings, &settings) != nil || settings.Manifest == nil {
		return designManifest{}, false
	}
	return *settings.Manifest, true
}

// sliceActionDesignManifestContext injects the project's design system
// (project.settings.design_manifest) into the design_proposal / implementation
// prompts so the designer maps against a KNOWN component inventory instead of
// re-discovering it every run. Returns "" when the project configures none.
// Mirrors sliceActionQAManifestContext.
func (h *Handler) sliceActionDesignManifestContext(ctx context.Context, issue db.Issue) string {
	m, ok := h.projectDesignManifest(ctx, issue)
	if !ok {
		return ""
	}
	return renderDesignManifestContext(m)
}

// renderDesignManifestContext is the pure renderer — separated so the prompt
// wording is unit-tested without a database.
func renderDesignManifestContext(m designManifest) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(" PROJECT DESIGN SYSTEM (rev %d, kind=%s) — build against THIS, do not re-invent it.", m.Revision, m.Kind))
	if len(m.Tokens.Colors) > 0 || len(m.Tokens.Typography) > 0 || len(m.Tokens.Spacing) > 0 {
		b.WriteString(" TOKENS:")
		for name, v := range m.Tokens.Colors {
			b.WriteString(" " + name + "=" + v + ";")
		}
		for name, v := range m.Tokens.Typography {
			b.WriteString(" " + name + "=" + v + ";")
		}
		for name, v := range m.Tokens.Spacing {
			b.WriteString(" " + name + "=" + v + ";")
		}
	}
	if len(m.Components) > 0 {
		b.WriteString(" COMPONENTS (reuse these):")
		for i, c := range m.Components {
			if i >= designManifestMaxComponents {
				b.WriteString(fmt.Sprintf(" …(+%d more)", len(m.Components)-designManifestMaxComponents))
				break
			}
			b.WriteString(" " + c.Name)
			if c.CodeRef != "" {
				b.WriteString(" (" + c.CodeRef + ")")
			}
			if c.Usage != "" {
				b.WriteString(" — " + c.Usage)
			}
			b.WriteString(";")
		}
	}
	if len(m.Conventions) > 0 {
		b.WriteString(" CONVENTIONS: " + strings.Join(m.Conventions, "; ") + ".")
	}
	if len(m.AntiPatterns) > 0 {
		b.WriteString(" ANTI-PATTERNS (never do): " + strings.Join(m.AntiPatterns, "; ") + ".")
	}
	if m.LegacyNotes != "" {
		b.WriteString(" LEGACY NOTES: " + m.LegacyNotes)
	}
	return b.String()
}

// designManifestSource returns the manifest's source ("agent"/"manual"/"mixed")
// or "" when there is no manifest — the guard that stops an agent capture from
// overwriting a human-curated ("manual") manifest.
func (h *Handler) designManifestSource(ctx context.Context, issue db.Issue) string {
	m, ok := h.projectDesignManifest(ctx, issue)
	if !ok {
		return ""
	}
	return m.Source
}
