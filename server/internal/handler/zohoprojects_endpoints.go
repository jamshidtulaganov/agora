package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/util"
)

// Authenticated Zoho Projects import endpoints (Phase 1, one-way). Unlike the
// Bitrix import browser (which is login-gated and routes each task to a
// workspace via env config), these are WORKSPACE-SCOPED and ROLE-GATED: the
// caller names the destination workspace via the usual X-Workspace-ID /
// X-Workspace-Slug header, must be an owner or admin of it, and every imported
// Zoho project/task/sprint lands in that one workspace. This is the cleaner
// Phase-1 model — there is no Zoho equivalent of the Bitrix per-task routing
// config, and a portal maps naturally onto a single workspace.
//
// Both endpoints 400 "Zoho Projects not configured" when the OAuth env is unset
// so a deployment without Zoho gets a clear error.
//
// POST /api/zoho-projects/sync   — reconcile the requested projects (Phase 1:
//                                  identical to import; Phase 2 will use a
//                                  modified-since cursor here).
// POST /api/zoho-projects/import — full import of the requested projects (or
//                                  every project in the portal when all=true).

// zohoImportMaxProjects caps a single request so a runaway call can't walk an
// entire portal of hundreds of projects (each project is many Zoho round-trips +
// DB writes). The per-project task pagination is separately bounded in the
// client.
const zohoImportMaxProjects = 50

// ZohoImportRequest selects what to import. ProjectIDs are explicit Zoho project
// ids; All=true expands to every project in the portal (still capped at
// zohoImportMaxProjects). The union is deduped.
type ZohoImportRequest struct {
	ProjectIDs []string `json:"project_ids"`
	All        bool     `json:"all"`
	// OwnerZpuid, when set, restricts the import to a single Zoho user's tasks
	// (the "only my tasks" scope) — a Zoho zpuid / accounts user id passed to the
	// Zoho owner filter. Persisted per project so the poller keeps the same scope.
	OwnerZpuid string `json:"owner_zpuid"`
}

// ZohoImportResponse tallies the run. Like the Bitrix import it is asynchronous:
// the request returns 202 with Accepted (how many projects were enqueued) once
// the project set is resolved; Created/Updated/Skipped stay 0 because the
// per-task reconcile runs in the background and issues stream onto the board over
// the websocket. Errors carries only up-front failures (e.g. listing projects).
type ZohoImportResponse struct {
	Created  int      `json:"created"`
	Updated  int      `json:"updated"`
	Skipped  int      `json:"skipped"`
	Accepted int      `json:"accepted"`
	Errors   []string `json:"errors"`
}

// SyncZohoProjects handles POST /api/zoho-projects/sync. Phase 2: cursor-driven
// delta sync — only tasks modified since each project's persisted
// settings.zoho_synced_at cursor are pulled (incremental=true). The reconcile is
// otherwise identical to import and equally idempotent; the cursor just trims the
// work to changed tasks.
func (h *Handler) SyncZohoProjects(w http.ResponseWriter, r *http.Request) {
	h.runZohoImport(w, r, true)
}

// ImportZohoProjects handles POST /api/zoho-projects/import: a FULL import of the
// requested Zoho projects (or all of them) into the caller's workspace. Always a
// full walk (incremental=false); it still advances the cursor so a later /sync or
// the poller continues from the import baseline.
func (h *Handler) ImportZohoProjects(w http.ResponseWriter, r *http.Request) {
	h.runZohoImport(w, r, false)
}

// runZohoImport is the shared body of the sync + import endpoints: validate
// config + workspace role, resolve the project id set, and kick off the
// background reconcile. incremental selects the modified-since cursor semantics
// (sync=true, import=false).
func (h *Handler) runZohoImport(w http.ResponseWriter, r *http.Request, incremental bool) {
	if !zohoConfigured() {
		zohoUnavailable(w)
		return
	}

	// Workspace-scoped + role-gated: the destination workspace comes from the
	// request headers (X-Workspace-ID / X-Workspace-Slug) and the caller must be
	// an owner or admin of it.
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

	var req ZohoImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	st := h.newZohoSyncState()
	st.incremental = incremental
	st.ownerZpuid = strings.TrimSpace(req.OwnerZpuid)

	// Detach the heavy import work (Zoho REST round-trips) from the request
	// context so a client disconnect doesn't cancel a long import mid-flight;
	// issues stream onto the board live over the websocket. cancel() is invoked
	// by the background goroutine below.
	ctx, cancel := context.WithTimeout(context.Background(), zohoSyncTimeout)

	portalID, err := h.resolveZohoPortalID(ctx, st)
	if err != nil {
		cancel()
		writeError(w, http.StatusBadGateway, "failed to resolve zoho portal: "+err.Error())
		return
	}

	resp := ZohoImportResponse{Errors: []string{}}

	// Resolve the project id set: explicit ids, plus every portal project when
	// all=true, deduped and capped.
	seen := map[string]bool{}
	projectIDs := make([]string, 0, len(req.ProjectIDs))
	addID := func(id string) bool {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return true
		}
		seen[id] = true
		projectIDs = append(projectIDs, id)
		return len(projectIDs) < zohoImportMaxProjects
	}

	withinCap := true
	for _, id := range req.ProjectIDs {
		if !addID(id) {
			withinCap = false
			break
		}
	}
	if withinCap && req.All {
		projects, err := st.client.ListProjects(ctx, portalID)
		if err != nil {
			cancel()
			writeError(w, http.StatusBadGateway, "failed to list zoho projects: "+err.Error())
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

	// Per-project reconcile runs in the background so a large portal doesn't blow
	// the request budget. The board updates live over the websocket.
	go func() {
		defer cancel()
		for _, id := range projectIDs {
			if err := h.syncZohoProject(ctx, wsUUID, id, st); err != nil {
				slog.Warn("zoho import: project sync failed", "zoho_project_id", id, "error", err)
			}
		}
		slog.Info("zoho import: background sync finished",
			"workspace_id", util.UUIDToString(wsUUID), "requested", len(projectIDs),
			"created", st.created, "updated", st.updated, "skipped", st.skipped)
	}()

	resp.Accepted = len(projectIDs)
	writeJSON(w, http.StatusAccepted, resp)
}

// --- GET /api/zoho-projects/projects ----------------------------------------

// ZohoProjectResponse is one Zoho project surfaced to the import UI.
type ZohoProjectResponse struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// ListZohoProjects returns the projects in the configured portal so an operator
// can pick which to import. Login-gated (any authenticated user); the import
// itself is the role-gated, workspace-scoped action. 400 when unconfigured.
func (h *Handler) ListZohoProjects(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	if !zohoConfigured() {
		zohoUnavailable(w)
		return
	}
	st := h.newZohoSyncState()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	portalID, err := h.resolveZohoPortalID(ctx, st)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to resolve zoho portal: "+err.Error())
		return
	}
	projects, err := st.client.ListProjects(ctx, portalID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list zoho projects: "+err.Error())
		return
	}
	out := make([]ZohoProjectResponse, 0, len(projects))
	for _, p := range projects {
		out = append(out, ZohoProjectResponse{ID: p.ID, Name: p.Name, Status: p.Status})
	}
	writeJSON(w, http.StatusOK, out)
}
