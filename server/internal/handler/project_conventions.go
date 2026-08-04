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
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Project conventions — human-authored coding rules (lint, code style, design
// patterns) an existing codebase already follows. Stored as Markdown text in
// project.settings.conventions, with an optional workspace-level base in
// workspace.settings.conventions that every project inherits (mirrors the
// design-manifest workspace/project split — the SalesDoctor workspace's sd-main
// / sd-cs / sd-billing share a stack). Injected into EVERY agent run on the
// project via the claim path so dev / QA / design agents match the house style
// instead of re-inventing it. Distinct from designManifest.Conventions, which is
// the design system's own convention list.

// conventionsMaxChars caps how much is injected so a sprawling doc can't blow
// the context budget. Conventions should be tight rules, not a manual — but a
// legacy ERP needs per-stack sections (Yii1/tenancy, api contracts, several
// frontend stacks), which 4000 chars cannot hold; 12000 fits a disciplined
// multi-stack rule set while still bounding the prompt.
const conventionsMaxChars = 12000

// projectConventions reads the trimmed project-level conventions text.
// ok=false when the issue has no project, or the project has no conventions set.
func (h *Handler) projectConventions(ctx context.Context, issue db.Issue) (string, bool) {
	if !issue.ProjectID.Valid {
		return "", false
	}
	project, err := h.Queries.GetProject(ctx, issue.ProjectID)
	if err != nil || len(project.Settings) == 0 {
		return "", false
	}
	return conventionsFromSettings(project.Settings)
}

// workspaceConventions reads the trimmed workspace-level conventions base.
func (h *Handler) workspaceConventions(ctx context.Context, wsID pgtype.UUID) (string, bool) {
	ws, err := h.Queries.GetWorkspace(ctx, wsID)
	if err != nil || len(ws.Settings) == 0 {
		return "", false
	}
	return conventionsFromSettings(ws.Settings)
}

// conventionsFromSettings pulls a trimmed, non-empty conventions string out of a
// settings jsonb blob. ok=false on unparseable JSON or an empty value.
func conventionsFromSettings(raw []byte) (string, bool) {
	var s struct {
		Conventions string `json:"conventions"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	text := strings.TrimSpace(s.Conventions)
	if text == "" {
		return "", false
	}
	return text, true
}

// sliceActionProjectConventionsContext injects the project's coding conventions
// into an agent's instructions. Renders the WORKSPACE base first (shared across
// the workspace's projects), then the PROJECT override — so projects converge on
// shared rules while each keeps its own. Returns "" when neither is set. Mirrors
// sliceActionDesignManifestContext.
func (h *Handler) sliceActionProjectConventionsContext(ctx context.Context, issue db.Issue) string {
	var b strings.Builder
	if ws, ok := h.workspaceConventions(ctx, issue.WorkspaceID); ok {
		b.WriteString(renderConventionsContext(ws, "WORKSPACE CONVENTIONS (shared coding rules across this workspace's projects)"))
	}
	if pc, ok := h.projectConventions(ctx, issue); ok {
		b.WriteString(renderConventionsContext(pc, "PROJECT CONVENTIONS (this project's coding rules — take precedence over the workspace base)"))
	}
	return b.String()
}

// renderConventionsContext is the pure renderer — separated so the prompt
// wording is unit-tested without a database.
func renderConventionsContext(text, label string) string {
	text = strings.TrimSpace(text)
	// Cap on RUNES, not bytes — conventions are often Cyrillic (2 bytes/char);
	// a byte cap would halve the effective budget and could split a rune.
	if r := []rune(text); len(r) > conventionsMaxChars {
		text = strings.TrimSpace(string(r[:conventionsMaxChars])) + " …(truncated)"
	}
	return "\n\n" + label + " — you MUST follow these; match the house style, do NOT re-invent it:\n" + text
}

// learnConventionsPromptTmpl tells the project's lead agent to study the repo's
// tooling config + real code and emit ONE fenced ```conventions Markdown block.
// CaptureProjectConventions parses that block back into project.settings.
const learnConventionsPromptTmpl = `Extract the coding conventions of the project "%s" from its connected repositories (attached to this task — check them out; use the agora repo checkout commands in your context). Work AUTONOMOUSLY — do NOT ask the user any questions; make reasonable assumptions and finish end to end.

Read the tooling config (.eslintrc / eslint.config, .prettierrc, tsconfig, .editorconfig, .golangci.yml, pyproject, etc.), any AGENTS.md / CLAUDE.md / CONTRIBUTING, AND the ACTUAL patterns in the existing code: component style, naming, state management, error handling, directory layout, test conventions — anything an engineer must follow to match the house style. If several repositories are connected, cover each.

Then OUTPUT exactly ONE fenced code block, opened with three backticks followed by the word conventions, containing the project's coding conventions as a TIGHT, imperative Markdown bullet list — the rules an agent MUST follow here. Rules only: no prose, no preamble, no closing summary. Be specific to THIS codebase (real library names, real paths), not generic advice. Keep it under ~50 bullets.

Emitting that block IS the deliverable — a human reviews it and it is saved to the project's conventions. Do not do anything else.`

// buildLearnConventionsPrompt fills the study prompt for a project title.
func buildLearnConventionsPrompt(title string) string {
	return fmt.Sprintf(learnConventionsPromptTmpl, title) + soloAutomationDirective
}

// LearnProjectConventions triggers the project lead agent to study the connected
// repos and propose coding conventions. POST /api/projects/{id}/conventions/learn.
// Mirrors BuildProjectKnowledge — 400 with an actionable message when there is no
// agent lead or no repo yet.
func (h *Handler) LearnProjectConventions(w http.ResponseWriter, r *http.Request) {
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
	prompt := buildLearnConventionsPrompt(project.Title)
	if _, err := h.TaskService.EnqueueQuickCreateTask(
		r.Context(), project.WorkspaceID, requester, h.pickAutomationRunner(r.Context(), project), pgtype.UUID{},
		prompt, project.ID, pgtype.UUID{}, nil,
	); err != nil {
		writeError(w, http.StatusBadGateway, "failed to start conventions study: "+err.Error())
		return
	}
	slog.Info("project conventions study enqueued",
		"project_id", uuidToString(project.ID), "lead_agent_id", uuidToString(project.LeadID))
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "queued"})
}
