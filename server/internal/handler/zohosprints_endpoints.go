package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/util"
)

// Authenticated Zoho Sprints import endpoints (one-way). Workspace-scoped +
// role-gated exactly like the Zoho Projects endpoints: the destination workspace
// comes from the X-Workspace-ID/Slug header and the caller must be owner/admin.
// Each 400s "Zoho Sprints not configured" when ZOHO_SPRINTS_* is unset.

const zohoSprintsImportMaxProjects = 50

type ZohoSprintsImportRequest struct {
	ProjectIDs []string `json:"project_ids"`
	All        bool     `json:"all"`
}

type ZohoSprintsImportResponse struct {
	Accepted int      `json:"accepted"`
	Errors   []string `json:"errors"`
}

// ImportZohoSprintsProjects handles POST /api/zoho-sprints/import: import the
// requested Zoho Sprints projects (or all) into the caller's workspace, each into
// its own "<name> (Sprints)" Agora project.
func (h *Handler) ImportZohoSprintsProjects(w http.ResponseWriter, r *http.Request) {
	if !zohoSprintsConfigured() {
		zohoSprintsUnavailable(w)
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return
	}
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}

	var req ZohoSprintsImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	st := h.newZohoSprintsSyncState()
	ctx, cancel := context.WithTimeout(context.Background(), zohoSprintsSyncTimeout)

	teamID, err := h.resolveZohoSprintsTeamID(ctx, st)
	if err != nil {
		cancel()
		writeError(w, http.StatusBadGateway, "failed to resolve zoho sprints team: "+err.Error())
		return
	}

	seen := map[string]bool{}
	projectIDs := make([]string, 0, len(req.ProjectIDs))
	addID := func(id string) bool {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return true
		}
		seen[id] = true
		projectIDs = append(projectIDs, id)
		return len(projectIDs) < zohoSprintsImportMaxProjects
	}
	withinCap := true
	for _, id := range req.ProjectIDs {
		if !addID(id) {
			withinCap = false
			break
		}
	}
	if withinCap && req.All {
		projects, err := st.client.ListProjects(ctx, teamID)
		if err != nil {
			cancel()
			writeError(w, http.StatusBadGateway, "failed to list zoho sprints projects: "+err.Error())
			return
		}
		for _, p := range projects {
			if !addID(p.ID) {
				break
			}
		}
	}
	if len(projectIDs) == 0 {
		cancel()
		writeError(w, http.StatusBadRequest, "no projects to import (set project_ids or all=true)")
		return
	}

	go func() {
		defer cancel()
		for _, id := range projectIDs {
			if err := h.syncZohoSprintsProject(ctx, wsUUID, id, st); err != nil {
				slog.Warn("zoho sprints import: project sync failed", "zoho_sprints_project_id", id, "error", err)
			}
		}
		slog.Info("zoho sprints import: background sync finished",
			"workspace_id", util.UUIDToString(wsUUID), "requested", len(projectIDs),
			"created", st.created, "updated", st.updated, "skipped", st.skipped)
	}()

	writeJSON(w, http.StatusAccepted, ZohoSprintsImportResponse{Accepted: len(projectIDs), Errors: []string{}})
}

// --- GET /api/zoho-sprints/projects -----------------------------------------

type ZohoSprintsProjectResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListZohoSprintsProjects returns the projects in the configured Sprints team so
// an operator can pick which to import. Login-gated; 400 when unconfigured.
func (h *Handler) ListZohoSprintsProjects(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	if !zohoSprintsConfigured() {
		zohoSprintsUnavailable(w)
		return
	}
	st := h.newZohoSprintsSyncState()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	teamID, err := h.resolveZohoSprintsTeamID(ctx, st)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to resolve zoho sprints team: "+err.Error())
		return
	}
	projects, err := st.client.ListProjects(ctx, teamID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list zoho sprints projects: "+err.Error())
		return
	}
	out := make([]ZohoSprintsProjectResponse, 0, len(projects))
	for _, p := range projects {
		out = append(out, ZohoSprintsProjectResponse{ID: p.ID, Name: p.Name})
	}
	writeJSON(w, http.StatusOK, out)
}
