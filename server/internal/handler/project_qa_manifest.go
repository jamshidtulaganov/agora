package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Project QA manifest — the QA counterpart to the project knowledge base
// (project_knowledge.go). The lead agent studies the project's connected repos
// (router/controller files, the login form, the nav menu) and, when a deployed
// QA target is reachable, VERIFIES each candidate route live, then persists the
// result into project.settings.qa_manifest — the navigation map every QA run
// (run_qa / gen_test_cases / run_test_cases and every daemon claim) is briefed
// with via sliceActionQAManifestContext.
//
// Entry points mirror the knowledge build:
//   - automatic, in the background, when a project is created with a repo AND
//     when the first repo is attached later (maybeEnqueueQAManifestBuild) —
//     never when a manifest already exists (a curated manifest is not clobbered)
//   - manual, on demand, via POST /api/projects/{id}/qa-manifest/build
//   - the agent persists via PUT /api/projects/{id}/qa-manifest
//     (CLI: agora project qa-manifest set <project> --file manifest.json)

const qaManifestBuildPromptTmpl = `Build the QA manifest for the project "%s". Work AUTONOMOUSLY — do NOT ask the user any questions; make reasonable assumptions and finish end to end.

The QA manifest is the app's KNOWN navigation map for QA agents: where to log in, which routes exist, and the golden-path flows. It is stored on the project and injected into every QA run so agents navigate by map instead of exploring.

Steps:
1. Check out the connected repositories (attached to this task) and find: the route table (router/controllers/menu definitions), the login form (its action path and input field names), and the main user-facing pages.
2. If the project has a deployed QA target (a connected box URL or qa_smoke_url in your context), VERIFY the candidate routes against it live — log in with the QA credentials from the project context and probe each route. Only verified-alive routes go into "routes"; dead or role-gated paths go into "known_issues" so QA never wastes a run on them. If no live target is reachable, derive routes from code only and say so in "notes".
3. Write the manifest as JSON with exactly this shape:
   {"base_url": "...", "auth": {"login_path","user_field","pass_field","username","password","success_contains"}, "routes": {"name": "/path", ...}, "flows": [{"name","path","steps":[...],"assert"}], "known_issues": ["..."], "notes": "..."}
   Include 4-10 golden-path flows for the app's daily-critical operations (login always first) with concrete selectors where forms are involved.
4. You MUST persist it by running: agora project qa-manifest set %s --file <your-manifest.json>. Writing a file in the worktree does NOT complete this task — the worktree is discarded; ONLY the saved project manifest is read by QA agents. Do not stop until the command succeeds.

NEVER invent routes you did not see in code or verify live. An honest small manifest beats a fabricated large one.`

func buildQAManifestPrompt(title string, projectID pgtype.UUID) string {
	return fmt.Sprintf(qaManifestBuildPromptTmpl, title, uuidToString(projectID))
}

// projectHasQAManifest reports whether project.settings already carries a
// qa_manifest key — the guard that keeps the background build from clobbering
// a curated manifest.
func projectHasQAManifest(settings []byte) bool {
	if len(settings) == 0 {
		return false
	}
	var s map[string]json.RawMessage
	if json.Unmarshal(settings, &s) != nil {
		return false
	}
	_, ok := s["qa_manifest"]
	return ok
}

// maybeEnqueueQAManifestBuild fires a one-off background QA-manifest build for
// the project's lead agent. No-op unless the lead is an agent, the project has
// a github repo to study, and no qa_manifest exists yet. Best-effort: failures
// are logged and swallowed — this must never block project create or repo
// attach. Runs alongside the knowledge build (same quick-create task path).
func (h *Handler) maybeEnqueueQAManifestBuild(ctx context.Context, project db.Project, requesterUserID string) {
	if !project.LeadType.Valid || project.LeadType.String != "agent" || !project.LeadID.Valid {
		return // manifest build is driven by an agent lead
	}
	if !h.projectHasGithubRepo(ctx, project.ID) {
		return // nothing to derive routes from yet
	}
	if projectHasQAManifest(project.Settings) {
		return // a curated manifest exists — never clobber it in the background
	}
	requester, _ := h.parseUserUUIDOrZero(requesterUserID)
	prompt := buildQAManifestPrompt(project.Title, project.ID)
	if _, err := h.TaskService.EnqueueQuickCreateTask(
		ctx, project.WorkspaceID, requester, project.LeadID, pgtype.UUID{},
		prompt, project.ID, pgtype.UUID{}, nil,
	); err != nil {
		slog.Warn("project qa-manifest build enqueue failed",
			"project_id", uuidToString(project.ID), "error", err)
		return
	}
	slog.Info("project qa-manifest build enqueued",
		"project_id", uuidToString(project.ID), "lead_agent_id", uuidToString(project.LeadID))
}

// BuildProjectQAManifest manually (re-)triggers the lead agent's QA-manifest
// build. POST /api/projects/{id}/qa-manifest/build. Unlike the background
// trigger this fires even when a manifest exists — an explicit human request
// means "re-derive it" (the agent overwrites via the set endpoint).
func (h *Handler) BuildProjectQAManifest(w http.ResponseWriter, r *http.Request) {
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
	prompt := buildQAManifestPrompt(project.Title, project.ID)
	if _, err := h.TaskService.EnqueueQuickCreateTask(
		r.Context(), project.WorkspaceID, requester, project.LeadID, pgtype.UUID{},
		prompt, project.ID, pgtype.UUID{}, nil,
	); err != nil {
		writeError(w, http.StatusBadGateway, "failed to start qa-manifest build: "+err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "queued"})
}

// SetProjectQAManifest writes project.settings.qa_manifest — a MERGE on the
// settings blob (only the qa_manifest key changes; docs_repo, sprint_mode and
// the rest stay untouched), unlike PUT /api/projects/{id} which replaces the
// whole settings blob. PUT /api/projects/{id}/qa-manifest with the manifest
// JSON as the body. This is the persistence endpoint the build prompt points
// the agent at (agora project qa-manifest set).
func (h *Handler) SetProjectQAManifest(w http.ResponseWriter, r *http.Request) {
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

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // manifests are small; 1MB is generous
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	// Validate the shape before persisting: it must parse as a qaManifest and
	// carry at least an auth login path, a route, or a flow — an empty or
	// malformed manifest would silently brief every future QA run with nothing.
	var m qaManifest
	if err := json.Unmarshal(body, &m); err != nil {
		writeError(w, http.StatusBadRequest, "manifest is not valid JSON: "+err.Error())
		return
	}
	if m.Auth.LoginPath == "" && len(m.Routes) == 0 && len(m.Flows) == 0 {
		writeError(w, http.StatusBadRequest, "manifest is empty — provide auth.login_path, routes, or flows")
		return
	}
	if strings.TrimSpace(m.BaseURL) == "" {
		writeError(w, http.StatusBadRequest, "manifest.base_url is required")
		return
	}

	// Merge into the existing settings blob.
	settings := map[string]json.RawMessage{}
	if len(project.Settings) > 0 {
		if err := json.Unmarshal(project.Settings, &settings); err != nil {
			settings = map[string]json.RawMessage{}
		}
	}
	settings["qa_manifest"] = json.RawMessage(body)
	blob, err := json.Marshal(settings)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode settings")
		return
	}
	if _, err := h.Queries.UpdateProject(r.Context(), db.UpdateProjectParams{
		ID:       project.ID,
		Settings: blob,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save qa manifest")
		return
	}
	slog.Info("project qa-manifest saved",
		"project_id", uuidToString(project.ID), "by", userID,
		"routes", len(m.Routes), "flows", len(m.Flows))
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "saved",
		"routes": len(m.Routes),
		"flows":  len(m.Flows),
	})
}
