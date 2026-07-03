package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Project knowledge base — the lead agent studies the project's connected repos
// and writes a per-project `<slug>-kb` knowledgebase skill, which every agent
// working on the project's tasks then reads as context (the same `<slug>-kb`
// skill the on-done KB synthesizer uses; see maybeEnqueueKnowledgeCapture).
//
// Two entry points, both best-effort and both routed through the lead agent's
// quick-create task (project-scoped → the claim path surfaces the project's
// github_repo resources as the task's repos, so the agent can check them out):
//   - automatic, once, on project create (maybeEnqueueProjectStudy)
//   - manual, on demand, via POST /api/projects/{id}/knowledge/build

const projectStudyPromptTmpl = `Build the knowledge base for the project "%s". Work AUTONOMOUSLY — do NOT ask the user any questions; make reasonable assumptions and finish the task end to end.

Its connected repositories are attached to this task — check them out (use the agora repo checkout commands surfaced in your context) and study them. Cover: the architecture and main components, the tech stack and frameworks, the directory layout and where key things live, how to build / test / run it, the coding conventions, and anything an engineer (human or agent) must know to work here effectively. If several repositories are connected, cover each and how they relate.

Then you MUST persist the knowledge base as a workspace SKILL named "%s-kb" by running the agora skill CLI to create it (or update it if it already exists). Writing a file in the worktree (CLAUDE.md, README, notes, etc.) does NOT complete this task — the worktree is temporary and is discarded; ONLY the saved "%s-kb" skill is read by other agents. Keep the skill concise, accurate, current, and practical, with no fluff — focus on what helps someone act correctly in this codebase. Do not stop until the "%s-kb" skill exists.`

// slugifyProjectName lowercases a project title into a skill-name-safe slug:
// runs of non-alphanumeric become single hyphens, trimmed at the ends.
func slugifyProjectName(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		case !prevDash:
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func buildProjectStudyPrompt(title string) string {
	slug := slugifyProjectName(title)
	if slug == "" {
		slug = "project"
	}
	return fmt.Sprintf(projectStudyPromptTmpl, title, slug, slug, slug)
}

// projectKBSkillName resolves the name of a project's knowledge-base skill:
// the explicit project.settings.kb_skill override when set, else the derived
// "<slug>-kb". The override exists because slugifyProjectName is ASCII-only —
// a Cyrillic-titled Bitrix sprint bucket ("10 спринт (Июль)") slugifies to
// "10", never matching the real "sd-main-kb" skill.
func projectKBSkillName(project db.Project) string {
	if len(project.Settings) > 0 {
		var s struct {
			KBSkill string `json:"kb_skill"`
		}
		if json.Unmarshal(project.Settings, &s) == nil {
			if name := strings.TrimSpace(s.KBSkill); name != "" {
				return name
			}
		}
	}
	slug := slugifyProjectName(project.Title)
	if slug == "" {
		return ""
	}
	return slug + "-kb"
}

// projectKBSkill loads the issue's project KB skill so the claim path can
// auto-inject it into every run on the project — without this, the KB reaches
// an agent only via manual agent_skill binding, and in practice it reaches
// nobody. ok=false when the issue has no project or no such skill exists.
func (h *Handler) projectKBSkill(ctx context.Context, issue db.Issue) (service.AgentSkillData, bool) {
	if !issue.ProjectID.Valid {
		return service.AgentSkillData{}, false
	}
	project, err := h.Queries.GetProject(ctx, issue.ProjectID)
	if err != nil {
		return service.AgentSkillData{}, false
	}
	name := projectKBSkillName(project)
	if name == "" {
		return service.AgentSkillData{}, false
	}
	skill, err := h.Queries.GetSkillByWorkspaceAndName(ctx, db.GetSkillByWorkspaceAndNameParams{
		WorkspaceID: issue.WorkspaceID,
		Name:        name,
	})
	if err != nil {
		return service.AgentSkillData{}, false
	}
	data := service.AgentSkillData{
		ID:          uuidToString(skill.ID),
		Name:        skill.Name,
		Description: skill.Description,
		Content:     skill.Content,
	}
	files, _ := h.Queries.ListSkillFiles(ctx, skill.ID)
	for _, f := range files {
		data.Files = append(data.Files, service.AgentSkillFileData{Path: f.Path, Content: f.Content})
	}
	return data, true
}

// projectHasGithubRepo reports whether the project has at least one github_repo
// resource to study.
func (h *Handler) projectHasGithubRepo(ctx context.Context, projectID pgtype.UUID) bool {
	for _, row := range h.listProjectResourcesForProject(ctx, projectID) {
		if row.ResourceType == "github_repo" {
			return true
		}
	}
	return false
}

// maybeEnqueueProjectStudy fires a one-off knowledge-build run for the project's
// lead agent. No-op unless the lead is an agent AND the project has a github
// repo to study. Best-effort: any failure (e.g. the lead agent has no runtime
// yet) is logged and swallowed so it never blocks project creation. The task is
// queued and runs once a runtime for the lead agent comes online.
func (h *Handler) maybeEnqueueProjectStudy(ctx context.Context, project db.Project, requesterUserID string) {
	if !project.LeadType.Valid || project.LeadType.String != "agent" || !project.LeadID.Valid {
		return // KB build is driven by an agent lead
	}
	if !h.projectHasGithubRepo(ctx, project.ID) {
		return // nothing to study yet
	}
	requester, _ := h.parseUserUUIDOrZero(requesterUserID)
	prompt := buildProjectStudyPrompt(project.Title)
	if _, err := h.TaskService.EnqueueQuickCreateTask(
		ctx, project.WorkspaceID, requester, project.LeadID, pgtype.UUID{},
		prompt, project.ID, pgtype.UUID{}, nil,
	); err != nil {
		slog.Warn("project knowledge build enqueue failed",
			"project_id", uuidToString(project.ID), "error", err)
		return
	}
	slog.Info("project knowledge build enqueued",
		"project_id", uuidToString(project.ID), "lead_agent_id", uuidToString(project.LeadID))
}

// BuildProjectKnowledge manually (re-)triggers the lead agent's knowledge build
// for a project. POST /api/projects/{id}/knowledge/build. Returns 400 with an
// actionable message when the project has no agent lead or no repo yet.
func (h *Handler) BuildProjectKnowledge(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return
	}
	project, err := h.Queries.GetProject(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	if _, merr := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      parseUUID(userID),
		WorkspaceID: project.WorkspaceID,
	}); merr != nil {
		writeError(w, http.StatusForbidden, "not a member of this workspace")
		return
	}
	if !project.LeadType.Valid || project.LeadType.String != "agent" || !project.LeadID.Valid {
		writeError(w, http.StatusBadRequest, "set an agent as the project lead first")
		return
	}
	if !h.projectHasGithubRepo(r.Context(), project.ID) {
		writeError(w, http.StatusBadRequest, "connect a repository to this project first")
		return
	}
	requester, _ := h.parseUserUUIDOrZero(userID)
	prompt := buildProjectStudyPrompt(project.Title)
	if _, err := h.TaskService.EnqueueQuickCreateTask(
		r.Context(), project.WorkspaceID, requester, project.LeadID, pgtype.UUID{},
		prompt, project.ID, pgtype.UUID{}, nil,
	); err != nil {
		writeError(w, http.StatusBadGateway, "failed to start knowledge build: "+err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "queued"})
}
