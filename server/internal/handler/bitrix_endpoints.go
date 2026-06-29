package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/integrations/bitrix"
)

// Authenticated Bitrix import-browser endpoints. These power a UI that lets an
// operator inspect Bitrix workgroups + tasks and trigger a bulk import, on top
// of the per-task webhook sync. They are login-gated (any authenticated user;
// the repo has no platform-admin role) and operate ACROSS workspaces — the
// destination workspace for each task is decided by the same routing config the
// webhook uses (BITRIX_GROUP_MAP / BITRIX_SYNC_WORKSPACE_SLUG / tag slugs), not
// by request headers.
//
// Every endpoint 503s when the integration is unconfigured (no
// BITRIX_WEBHOOK_URL) so a self-hosted deployment without Bitrix gets a clear
// error instead of a confusing empty result.

// bitrixEndpointsEnabled reports whether the Bitrix REST integration is
// configured enough to serve the import-browser endpoints (a webhook/REST base
// URL is present). Routing is NOT required here — listing groups/tasks is useful
// before any group mapping exists.
func bitrixEndpointsEnabled() bool { return bitrixWebhookURL() != "" }

// --- GET /api/bitrix/groups -------------------------------------------------

// BitrixGroupResponse is one workgroup plus the Agora workspace slug it routes
// to (empty when no route resolves — such a group's tasks would be skipped on
// import).
type BitrixGroupResponse struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	WorkspaceSlug string `json:"workspace_slug"`
}

// ListBitrixGroups returns the active Bitrix workgroups, each annotated with the
// workspace slug it would route to under the current config.
func (h *Handler) ListBitrixGroups(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	if !bitrixEndpointsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "bitrix integration not configured")
		return
	}

	cfg := bitrixRouteConfig()
	client := bitrix.NewClient(bitrixWebhookURL())
	groups, err := client.ListGroups(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list bitrix groups: "+err.Error())
		return
	}

	resp := make([]BitrixGroupResponse, 0, len(groups))
	for _, g := range groups {
		// Route a synthetic task carrying only this group id to see where the
		// group lands (GroupMap → DefaultSlug). Tag-based routing is per-task, so
		// it's intentionally not reflected at the group level.
		slug := bitrix.ResolveWorkspaceSlug(&bitrix.Task{GroupID: g.ID}, cfg)
		resp = append(resp, BitrixGroupResponse{
			ID:            g.ID,
			Name:          g.Name,
			WorkspaceSlug: slug,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- GET /api/bitrix/users --------------------------------------------------

// BitrixUserResponse is one portal user for the "import by responsible" picker.
type BitrixUserResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Position string `json:"position"`
}

// ListBitrixUsers returns the portal's active users so the import UI can offer
// importing a specific responsible's tasks (alongside importing by group).
func (h *Handler) ListBitrixUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	if !bitrixEndpointsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "bitrix integration not configured")
		return
	}
	client := bitrix.NewClient(bitrixWebhookURL())
	users, err := client.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list bitrix users: "+err.Error())
		return
	}
	resp := make([]BitrixUserResponse, 0, len(users))
	for _, u := range users {
		resp = append(resp, BitrixUserResponse{
			ID:       u.ID,
			Name:     u.FullName(),
			Email:    u.Email,
			Position: u.Position,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- GET /api/bitrix/tasks?group_id=<id> ------------------------------------

// BitrixTaskResponse is one task in a group, with a resolved status, the
// destination workspace slug, and whether it has already been synced there.
type BitrixTaskResponse struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Status        string   `json:"status"`        // Bitrix status code
	MappedStatus  string   `json:"mapped_status"` // Agora issue status
	Tags          []string `json:"tags"`
	WorkspaceSlug string   `json:"workspace_slug"`
	AlreadySynced bool     `json:"already_synced"`
}

// ListBitrixTasks returns the tasks in a Bitrix workgroup (optionally filtered
// by BITRIX_TASK_TAG), each annotated with its routed workspace and whether an
// issue already exists for it there.
func (h *Handler) ListBitrixTasks(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	if !bitrixEndpointsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "bitrix integration not configured")
		return
	}
	groupID := strings.TrimSpace(r.URL.Query().Get("group_id"))
	if groupID == "" {
		writeError(w, http.StatusBadRequest, "group_id is required")
		return
	}

	cfg := bitrixRouteConfig()
	client := bitrix.NewClient(bitrixWebhookURL())
	tasks, err := client.ListTasks(r.Context(), groupID, bitrixTaskTag())
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list bitrix tasks: "+err.Error())
		return
	}

	// Cache workspace-slug → workspace UUID so the already_synced lookup doesn't
	// re-resolve the same workspace for every task in the group.
	wsIDBySlug := map[string]string{}

	resp := make([]BitrixTaskResponse, 0, len(tasks))
	for i := range tasks {
		t := &tasks[i]
		slug := bitrix.ResolveWorkspaceSlug(t, cfg)
		synced := false
		if slug != "" {
			wsID, ok := wsIDBySlug[slug]
			if !ok {
				if ws, err := h.Queries.GetWorkspaceBySlug(r.Context(), slug); err == nil {
					wsID = uuidToString(ws.ID)
				}
				wsIDBySlug[slug] = wsID
			}
			if wsID != "" {
				if _, found, err := h.findIssueByBitrixTaskID(r.Context(), parseUUID(wsID), t.ID); err == nil && found {
					synced = true
				}
			}
		}
		resp = append(resp, BitrixTaskResponse{
			ID:            t.ID,
			Title:         t.Title,
			Status:        t.Status,
			MappedStatus:  bitrix.MapStatus(t.Status),
			Tags:          t.Tags,
			WorkspaceSlug: slug,
			AlreadySynced: synced,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- POST /api/bitrix/import ------------------------------------------------

// bitrixImportMaxTasks caps a single import request so a runaway call can't sync
// thousands of tasks (each sync is several Bitrix round-trips + DB writes).
const bitrixImportMaxTasks = 200

// BitrixImportRequest selects what to import: explicit task ids and/or every
// task in the given groups. The union (deduped) is synced, capped at
// bitrixImportMaxTasks.
type BitrixImportRequest struct {
	GroupIDs []string `json:"group_ids"`
	TaskIDs  []string `json:"task_ids"`
	// UserIDs imports every task a given Bitrix user is RESPONSIBLE for — the
	// "import by user" flow, alongside group + explicit task selection.
	UserIDs []string `json:"user_ids"`
}

// BitrixImportResponse tallies the run. The import is asynchronous: the request
// returns 202 with Accepted (how many task ids were enqueued) once the task set
// is resolved; Created/Updated/Skipped stay 0 because the per-task sync runs in
// the background (issues then stream onto the board over the websocket). Errors
// only carries up-front failures (e.g. a group listing that failed).
type BitrixImportResponse struct {
	Created  int      `json:"created"`
	Updated  int      `json:"updated"`
	Skipped  int      `json:"skipped"`
	Accepted int      `json:"accepted"`
	Errors   []string `json:"errors"`
}

// ImportBitrixTasks bulk-syncs tasks: the explicit task_ids plus every task in
// the requested group_ids (deduped), each routed + reconciled through the same
// syncBitrixTask path the webhook uses. Capped at bitrixImportMaxTasks. Per-task
// errors are collected (not fatal) so one bad task doesn't abort the batch.
func (h *Handler) ImportBitrixTasks(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	if !bitrixEndpointsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "bitrix integration not configured")
		return
	}

	var req BitrixImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	cfg := bitrixRouteConfig()
	st := h.newBitrixSyncState()

	// Detach the heavy import work (Bitrix REST round-trips, file downloads,
	// enrichment) from the request context. A video-heavy group can take longer
	// than the client/proxy is willing to wait (~30s); without this, a client
	// disconnect cancels r.Context() mid-task and leaves partial imports. With a
	// detached context the import runs to completion server-side regardless, and
	// new issues stream into the board live over the websocket.
	// cancel() is invoked by the background goroutine below, NOT here.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)

	// Resolve the task id set: explicit ids first, then expand each group into
	// its task ids (filtered by BITRIX_TASK_TAG), deduping as we go and stopping
	// at the cap.
	seen := map[string]bool{}
	taskIDs := make([]string, 0, len(req.TaskIDs))
	resp := BitrixImportResponse{Errors: []string{}}

	addID := func(id string) bool {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return true // skip blanks/dupes, keep going
		}
		seen[id] = true
		taskIDs = append(taskIDs, id)
		return len(taskIDs) < bitrixImportMaxTasks
	}

	withinCap := true
	for _, id := range req.TaskIDs {
		if !addID(id) {
			withinCap = false
			break
		}
	}
	if withinCap {
		for _, gid := range req.GroupIDs {
			gid = strings.TrimSpace(gid)
			if gid == "" {
				continue
			}
			tasks, err := st.client.ListTasks(ctx, gid, st.tag)
			if err != nil {
				resp.Errors = append(resp.Errors, "list group "+gid+": "+err.Error())
				continue
			}
			for i := range tasks {
				if !addID(tasks[i].ID) {
					withinCap = false
					break
				}
			}
			if !withinCap {
				break
			}
		}
	}
	// Expand each selected responsible into the tasks they own.
	if withinCap {
		for _, uid := range req.UserIDs {
			uid = strings.TrimSpace(uid)
			if uid == "" {
				continue
			}
			tasks, err := st.client.ListTasksByUser(ctx, uid, st.tag)
			if err != nil {
				resp.Errors = append(resp.Errors, "list user "+uid+": "+err.Error())
				continue
			}
			for i := range tasks {
				if !addID(tasks[i].ID) {
					withinCap = false
					break
				}
			}
			if !withinCap {
				break
			}
		}
	}

	// Per-task sync (downloads + enrichment) runs in the background so a big or
	// video-heavy group doesn't blow the request budget. The board updates live
	// over the websocket as each issue is created/reconciled.
	go func() {
		defer cancel()
		for _, id := range taskIDs {
			if err := h.syncBitrixTaskWithState(ctx, id, cfg, st); err != nil {
				slog.Warn("bitrix import: task sync failed", "task_id", id, "error", err)
			}
		}
		slog.Info("bitrix import: background sync finished",
			"requested", len(taskIDs), "created", st.created, "updated", st.updated, "skipped", st.skipped)
	}()

	resp.Accepted = len(taskIDs)
	writeJSON(w, http.StatusAccepted, resp)
}
