package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/bitrix"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Authenticated Bitrix import-browser endpoints. These power a UI that lets an
// operator inspect Bitrix workgroups + tasks and trigger a bulk import, on top
// of the per-task webhook sync. They operate ACROSS workspaces — the destination
// workspace for each task is decided by the routing config the webhook uses
// (BITRIX_GROUP_MAP / BITRIX_SYNC_WORKSPACE_SLUG / tag slugs), not by request
// headers. Because there is no caller-supplied workspace to scope to, and no
// platform-admin role in this repo, access is gated by requireBitrixOperator:
// the caller must be an owner/admin of EVERY workspace the current config can
// route into. A plain member (or a Telegram Mini-App signup) that merely holds
// an account must NOT be able to inject/mutate issues — or provision members —
// into a tenant it doesn't administer.
//
// Every endpoint 503s when the integration is unconfigured (no
// BITRIX_WEBHOOK_URL) so a self-hosted deployment without Bitrix gets a clear
// error instead of a confusing empty result.

// bitrixEndpointsEnabled reports whether the Bitrix REST integration is
// configured enough to serve the import-browser endpoints (a webhook/REST base
// URL is present). Routing is NOT required here — listing groups/tasks is useful
// before any group mapping exists.
func bitrixEndpointsEnabled() bool { return bitrixWebhookURL() != "" }

// requireBitrixOperator authorizes the cross-workspace Bitrix import-browser
// endpoints. Unlike the per-request Zoho importers (scoped to the caller's
// X-Workspace-Slug), Bitrix routes tasks into workspaces chosen by the server's
// env config, so there is no caller-supplied workspace to gate on. Instead,
// require the caller to be an owner/admin of EVERY workspace the current routing
// config can write into (cfg.DefaultSlug + each cfg.GroupMap value) — the full
// set of tenants Bitrix data can land in. Fails closed: writes a 403 and returns
// false when no route is configured or the caller doesn't administer all targets.
func (h *Handler) requireBitrixOperator(w http.ResponseWriter, r *http.Request) bool {
	userID, ok := requireUserID(w, r)
	if !ok {
		return false
	}
	cfg := bitrixRouteConfig()
	targets := map[string]bool{}
	if s := strings.ToLower(strings.TrimSpace(cfg.DefaultSlug)); s != "" {
		targets[s] = true
	}
	for _, s := range cfg.GroupMap {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			targets[s] = true
		}
	}
	if len(targets) == 0 {
		writeError(w, http.StatusForbidden, "bitrix routing is not configured for any workspace")
		return false
	}
	for slug := range targets {
		ws, err := h.Queries.GetWorkspaceBySlug(r.Context(), slug)
		if err != nil {
			writeError(w, http.StatusForbidden, "you must administer the Bitrix target workspace")
			return false
		}
		m, err := h.getWorkspaceMember(r.Context(), userID, uuidToString(ws.ID))
		if err != nil || (m.Role != "owner" && m.Role != "admin") {
			writeError(w, http.StatusForbidden, "you must be an owner or admin of the Bitrix target workspace")
			return false
		}
	}
	return true
}

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
	if !h.requireBitrixOperator(w, r) {
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
	if !h.requireBitrixOperator(w, r) {
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
		// When a personal-sync allowlist is configured, keep the operator UI on
		// that same fail-closed boundary. Showing every portal user suggests they
		// are importable even though syncBitrixTaskWithState will reject them.
		if !bitrixUserAllowedEmail(&u) {
			continue
		}
		resp = append(resp, BitrixUserResponse{
			ID:       u.ID,
			Name:     u.FullName(),
			Email:    u.Email,
			Position: u.Position,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- POST /api/bitrix/register-webhook --------------------------------------

// RegisterBitrixWebhook binds the task event handlers (ONTASKADD/UPDATE +
// comment events) on the portal so inbound sync fires in REAL TIME. The handler
// URL is <BITRIX_WEBHOOK_PUBLIC_URL>/bitrix/webhook(+?secret=). Requires a
// public URL — Bitrix calls out to it, so this can't be done against localhost.
func (h *Handler) RegisterBitrixWebhook(w http.ResponseWriter, r *http.Request) {
	if !h.requireBitrixOperator(w, r) {
		return
	}
	if !bitrixEndpointsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "bitrix integration not configured")
		return
	}
	base := bitrixWebhookPublicURL()
	if base == "" {
		writeError(w, http.StatusBadRequest,
			"set BITRIX_WEBHOOK_PUBLIC_URL to a public https base — Bitrix must be able to reach this backend's /bitrix/webhook")
		return
	}
	handlerURL := base + "/bitrix/webhook"
	if secret := bitrixInboundSecret(); secret != "" {
		handlerURL += "?secret=" + url.QueryEscape(secret)
	}

	client := bitrix.NewClient(bitrixWebhookURL())
	events := []string{"ONTASKADD", "ONTASKUPDATE", "ONTASKCOMMENTADD", "ONTASKCOMMENTUPDATE"}
	bound := make([]string, 0, len(events))
	errs := make([]string, 0)
	for _, ev := range events {
		if err := client.BindEvent(r.Context(), ev, handlerURL); err != nil {
			errs = append(errs, ev+": "+err.Error())
			continue
		}
		bound = append(bound, ev)
	}
	all, _ := client.ListBoundEvents(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"handler_url":  handlerURL,
		"bound":        bound,
		"errors":       errs,
		"all_bindings": all,
	})
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
	if !h.requireBitrixOperator(w, r) {
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
	if !h.requireBitrixOperator(w, r) {
		return
	}
	// Cross-user bulk import is OFF by default: even an operator must not pull
	// tasks other users are responsible for. Members self-serve via
	// POST /api/bitrix/import/mine; this arbitrary-selector path is a gated
	// backfill tool (AGORA_BITRIX_BULK_IMPORT=1) only.
	if !bitrixBulkImportEnabled() {
		writeError(w, http.StatusForbidden,
			"operator bulk import is disabled — use POST /api/bitrix/import/mine to import your own tasks")
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
	selectorTasks := map[string]map[string]bool{}
	selectorOrder := make([]bitrixImportProgressSelector, 0, len(req.GroupIDs)+len(req.UserIDs))
	ensureSelector := func(kind, id string) string {
		key := kind + ":" + id
		if _, ok := selectorTasks[key]; !ok {
			selectorTasks[key] = map[string]bool{}
			selectorOrder = append(selectorOrder, bitrixImportProgressSelector{Kind: kind, ID: id})
		}
		return key
	}

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
			selectorKey := ensureSelector("group", gid)
			tasks, err := st.client.ListTasks(ctx, gid, st.tag)
			if err != nil {
				resp.Errors = append(resp.Errors, "list group "+gid+": "+err.Error())
				continue
			}
			for i := range tasks {
				id := strings.TrimSpace(tasks[i].ID)
				if !addID(id) {
					if id != "" && seen[id] {
						selectorTasks[selectorKey][id] = true
					}
					withinCap = false
					break
				}
				if id != "" && seen[id] {
					selectorTasks[selectorKey][id] = true
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
			selectorKey := ensureSelector("user", uid)
			tasks, err := st.client.ListTasksByUser(ctx, uid, st.tag)
			if err != nil {
				resp.Errors = append(resp.Errors, "list user "+uid+": "+err.Error())
				continue
			}
			for i := range tasks {
				id := strings.TrimSpace(tasks[i].ID)
				if !addID(id) {
					if id != "" && seen[id] {
						selectorTasks[selectorKey][id] = true
					}
					withinCap = false
					break
				}
				if id != "" && seen[id] {
					selectorTasks[selectorKey][id] = true
				}
			}
			if !withinCap {
				break
			}
		}
	}
	for i := range selectorOrder {
		key := selectorOrder[i].Kind + ":" + selectorOrder[i].ID
		selectorOrder[i].TaskIDs = selectorTasks[key]
	}

	// Per-task sync (downloads + enrichment) runs in the background so a big or
	// video-heavy group doesn't blow the request budget. The board updates live
	// over the websocket as each issue is created/reconciled. A lightweight
	// in-memory progress tracker lets the import UI show a live "synced X/N".
	h.startBitrixTaskSync(ctx, cancel, taskIDs, cfg, st, selectorOrder)

	resp.Accepted = len(taskIDs)
	writeJSON(w, http.StatusAccepted, resp)
}

// ImportMyBitrixTasks imports ONLY the caller's own Bitrix tasks — every task the
// caller is RESPONSIBLE for in Bitrix (filtered by BITRIX_TASK_TAG), routed and
// reconciled through the same per-task sync the webhook uses. Self-scoped by
// construction: the task set comes solely from ListTasksByUser(caller's linked
// Bitrix id), so no member — not even a workspace admin — can pull another user's
// task. The caller must have linked their Bitrix account first
// (POST /api/me/links/bitrix); an unlinked caller gets 412 with an actionable
// message, since without an identity we can't scope the import to them and
// importing anything else would leak other users' tasks. Auth = any logged-in
// member (NOT requireBitrixOperator).
func (h *Handler) ImportMyBitrixTasks(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if !bitrixEndpointsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "bitrix integration not configured")
		return
	}

	bitrixID := h.bitrixIDByUserID(r.Context(), userID)
	if bitrixID == "" {
		writeJSON(w, http.StatusPreconditionFailed, map[string]string{
			"reason": "bitrix_not_linked",
			"error":  "Link your Bitrix account first, then import — your tasks are matched by your Bitrix responsible id.",
		})
		return
	}

	cfg := bitrixRouteConfig()
	st := h.newBitrixSyncState()

	// Detach the per-task sync (REST round-trips, file downloads, frame
	// extraction) from the request context — it outlives the ~30s client budget;
	// issues stream onto the board over the websocket as each lands. cancel() is
	// invoked by the background goroutine started inside startBitrixTaskSync.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)

	resp := BitrixImportResponse{Errors: []string{}}
	tasks, err := st.client.ListTasksByUser(ctx, bitrixID, st.tag)
	if err != nil {
		cancel()
		resp.Errors = append(resp.Errors, "list my tasks: "+err.Error())
		writeJSON(w, http.StatusBadGateway, resp)
		return
	}

	seen := map[string]bool{}
	taskIDs := make([]string, 0, len(tasks))
	for i := range tasks {
		id := strings.TrimSpace(tasks[i].ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		taskIDs = append(taskIDs, id)
		if len(taskIDs) >= bitrixImportMaxTasks {
			break
		}
	}

	h.startBitrixTaskSync(ctx, cancel, taskIDs, cfg, st, []bitrixImportProgressSelector{{
		Kind:    "user",
		ID:      bitrixID,
		TaskIDs: seen,
	}})
	resp.Accepted = len(taskIDs)
	writeJSON(w, http.StatusAccepted, resp)
}

// SyncBitrixProject re-syncs a single Bitrix-linked project on demand: it pulls
// the project's workgroup tasks (new + changed) through the same per-task sync
// the bulk import + webhook use, then stamps project.settings.bitrix_synced_at so
// the UI can show "last synced". POST /api/projects/{id}/bitrix/sync. Returns 202
// (the per-task sync streams onto the board over the websocket) + the timestamp.
func (h *Handler) SyncBitrixProject(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	// A whole-workgroup re-sync pulls every user's tasks in the group, so it is a
	// cross-user bulk path — OFF by default (AGORA_BITRIX_BULK_IMPORT). Per-user
	// refresh is covered by the background auto-pool; members import their own via
	// POST /api/bitrix/import/mine.
	if !bitrixBulkImportEnabled() {
		writeError(w, http.StatusForbidden,
			"project bulk sync is disabled — members import their own tasks via POST /api/bitrix/import/mine")
		return
	}
	if !bitrixEndpointsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "bitrix integration not configured")
		return
	}
	wsID := h.resolveWorkspaceID(r)
	if wsID == "" {
		writeError(w, http.StatusBadRequest, "workspace required")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace_id")
	if !ok {
		return
	}
	// Re-syncing auto-provisions Bitrix users as members and imports comments/
	// attachments — a tenant-provisioning action, so require owner/admin, not a
	// plain member (mirrors requireBitrixOperator on the sibling import routes).
	if _, ok := h.requireWorkspaceRole(w, r, wsID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	projectUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return
	}
	project, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
		ID:          projectUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	groupID := bitrixGroupIDFromDescription(project.Description.String)
	if groupID == "" {
		writeError(w, http.StatusBadRequest, "this project is not linked to a Bitrix workgroup")
		return
	}

	cfg := bitrixRouteConfig()
	st := h.newBitrixSyncState()
	// Detached context so the per-task sync runs to completion even if the client
	// disconnects (cancel() fires in the background goroutine).
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)

	tasks, err := st.client.ListTasks(ctx, groupID, st.tag)
	if err != nil {
		cancel()
		writeError(w, http.StatusBadGateway, "failed to list Bitrix tasks: "+err.Error())
		return
	}
	// Dedup + cap, mirroring the bulk import.
	seen := map[string]bool{}
	taskIDs := make([]string, 0, len(tasks))
	for i := range tasks {
		id := strings.TrimSpace(tasks[i].ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		taskIDs = append(taskIDs, id)
		if len(taskIDs) >= bitrixImportMaxTasks {
			break
		}
	}

	groupTasks := make(map[string]bool, len(taskIDs))
	for _, id := range taskIDs {
		groupTasks[id] = true
	}
	h.startBitrixTaskSync(ctx, cancel, taskIDs, cfg, st, []bitrixImportProgressSelector{{
		Kind:    "group",
		ID:      groupID,
		TaskIDs: groupTasks,
	}})

	// Stamp the last-sync time now (the sync is enqueued + streams in over the
	// websocket). Mirrors the Zoho zoho_synced_at settings merge. A fresh short
	// context — the detached ctx above is cancelled when the goroutine ends.
	syncedAt := time.Now().UTC().Format(time.RFC3339)
	stampCtx, stampCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stampCancel()
	if _, err := h.DB.Exec(stampCtx,
		`UPDATE project
		    SET settings = COALESCE(settings, '{}'::jsonb) || jsonb_build_object('bitrix_synced_at', $2::text),
		        updated_at = now()
		  WHERE id = $1`,
		projectUUID, syncedAt); err != nil {
		slog.Warn("bitrix sync: failed to stamp bitrix_synced_at",
			"project_id", uuidToString(projectUUID), "error", err)
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted":         len(taskIDs),
		"bitrix_synced_at": syncedAt,
	})
}

// bitrixImportProgressState is a process-wide snapshot of the most recent import
// run, so the UI can poll a live "synced X/N" while the background sync streams
// issues onto the board. Single-run (imports are operator-driven + serial in
// practice); a new run overwrites the previous snapshot.
var bitrixImportProgressState struct {
	sync.Mutex
	RunID   uint64
	Total   int
	Synced  int
	Running bool
	Items   []BitrixImportProgressItem
	// TaskItems maps a Bitrix task id to the indexes of selector rows that
	// should advance when that task completes.
	TaskItems map[string][]int
}

type bitrixImportProgressSelector struct {
	Kind    string
	ID      string
	TaskIDs map[string]bool
}

// BitrixImportProgressItem is one selected workgroup/user inside a live run.
// It lets the UI show truthful per-selector progress instead of repeating the
// global X/N value beside every selected row.
type BitrixImportProgressItem struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Total   int    `json:"total"`
	Synced  int    `json:"synced"`
	Running bool   `json:"running"`
}

func bitrixImportProgressStart(total int, selectors []bitrixImportProgressSelector) uint64 {
	bitrixImportProgressState.Lock()
	bitrixImportProgressState.RunID++
	runID := bitrixImportProgressState.RunID
	bitrixImportProgressState.Total = total
	bitrixImportProgressState.Synced = 0
	bitrixImportProgressState.Running = total > 0
	bitrixImportProgressState.Items = make([]BitrixImportProgressItem, 0, len(selectors))
	bitrixImportProgressState.TaskItems = map[string][]int{}
	for _, selector := range selectors {
		itemIndex := len(bitrixImportProgressState.Items)
		item := BitrixImportProgressItem{
			Kind:    selector.Kind,
			ID:      selector.ID,
			Total:   len(selector.TaskIDs),
			Running: len(selector.TaskIDs) > 0,
		}
		bitrixImportProgressState.Items = append(bitrixImportProgressState.Items, item)
		for taskID := range selector.TaskIDs {
			bitrixImportProgressState.TaskItems[taskID] = append(bitrixImportProgressState.TaskItems[taskID], itemIndex)
		}
	}
	bitrixImportProgressState.Unlock()
	return runID
}

func bitrixImportProgressInc(runID uint64, taskID string) {
	bitrixImportProgressState.Lock()
	if bitrixImportProgressState.RunID != runID {
		bitrixImportProgressState.Unlock()
		return
	}
	bitrixImportProgressState.Synced++
	for _, itemIndex := range bitrixImportProgressState.TaskItems[taskID] {
		item := &bitrixImportProgressState.Items[itemIndex]
		item.Synced++
		if item.Synced >= item.Total {
			item.Running = false
		}
	}
	bitrixImportProgressState.Unlock()
}

func bitrixImportProgressFinish(runID uint64) {
	bitrixImportProgressState.Lock()
	if bitrixImportProgressState.RunID != runID {
		bitrixImportProgressState.Unlock()
		return
	}
	bitrixImportProgressState.Running = false
	for i := range bitrixImportProgressState.Items {
		bitrixImportProgressState.Items[i].Running = false
	}
	bitrixImportProgressState.Unlock()
}

// BitrixImportProgressResponse mirrors the import progress for the UI poll.
type BitrixImportProgressResponse struct {
	Total   int                        `json:"total"`
	Synced  int                        `json:"synced"`
	Running bool                       `json:"running"`
	Items   []BitrixImportProgressItem `json:"items"`
}

// GetBitrixImportProgress returns the live progress of the most recent import.
// The progress state is a process-wide global tied to a cross-workspace import,
// so it is gated the same as every other cross-workspace Bitrix browser endpoint
// (requireBitrixOperator) — a plain logged-in account must not read another
// tenant's import volume/timing.
func (h *Handler) GetBitrixImportProgress(w http.ResponseWriter, r *http.Request) {
	if !h.requireBitrixOperator(w, r) {
		return
	}
	bitrixImportProgressState.Lock()
	resp := BitrixImportProgressResponse{
		Total:   bitrixImportProgressState.Total,
		Synced:  bitrixImportProgressState.Synced,
		Running: bitrixImportProgressState.Running,
		Items:   append([]BitrixImportProgressItem(nil), bitrixImportProgressState.Items...),
	}
	bitrixImportProgressState.Unlock()
	writeJSON(w, http.StatusOK, resp)
}

// --- POST /api/bitrix/cleanup-done ----------------------------------------

// BitrixCleanupDoneResponse reports how many closed Bitrix-linked issues were
// hard-deleted from each routed workspace.
type BitrixCleanupDoneResponse struct {
	Deleted int `json:"deleted"`
}

// CleanupBitrixDoneIssues deletes Agora issues that were mirrored from Bitrix
// (metadata.bitrix_task_id set) and are already in a terminal status
// (done/cancelled). One-shot operator cleanup after a historical import flooded
// the board; ongoing sync skips/removes closed tasks itself.
func (h *Handler) CleanupBitrixDoneIssues(w http.ResponseWriter, r *http.Request) {
	if !h.requireBitrixOperator(w, r) {
		return
	}
	if !bitrixEndpointsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "bitrix integration not configured")
		return
	}
	cfg := bitrixRouteConfig()
	slugs := map[string]bool{}
	if s := strings.ToLower(strings.TrimSpace(cfg.DefaultSlug)); s != "" {
		slugs[s] = true
	}
	for _, s := range cfg.GroupMap {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			slugs[s] = true
		}
	}
	deleted := 0
	for slug := range slugs {
		ws, err := h.Queries.GetWorkspaceBySlug(r.Context(), slug)
		if err != nil {
			continue
		}
		n, err := h.cleanupClosedBitrixIssues(r.Context(), ws.ID)
		if err != nil {
			slog.Warn("bitrix cleanup-done failed", "workspace", slug, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to cleanup closed bitrix issues: "+err.Error())
			return
		}
		deleted += n
	}
	writeJSON(w, http.StatusOK, BitrixCleanupDoneResponse{Deleted: deleted})
}

func (h *Handler) cleanupClosedBitrixIssues(ctx context.Context, wsID pgtype.UUID) (int, error) {
	rows, err := h.DB.Query(ctx,
		`SELECT id, workspace_id, title, description, status, priority,
		        assignee_type, assignee_id, creator_type, creator_id,
		        parent_issue_id, acceptance_criteria, context_refs, position,
		        due_date, created_at, updated_at, number, project_id,
		        origin_type, origin_id, first_executed_at, start_date, metadata
		   FROM issue
		  WHERE workspace_id = $1
		    AND metadata ? $2
		    AND status IN ('done', 'cancelled')`,
		wsID, bitrixTaskIDMetaKey)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var issues []db.Issue
	for rows.Next() {
		var i db.Issue
		if err := rows.Scan(
			&i.ID, &i.WorkspaceID, &i.Title, &i.Description, &i.Status, &i.Priority,
			&i.AssigneeType, &i.AssigneeID, &i.CreatorType, &i.CreatorID,
			&i.ParentIssueID, &i.AcceptanceCriteria, &i.ContextRefs, &i.Position,
			&i.DueDate, &i.CreatedAt, &i.UpdatedAt, &i.Number, &i.ProjectID,
			&i.OriginType, &i.OriginID, &i.FirstExecutedAt, &i.StartDate, &i.Metadata,
		); err != nil {
			return 0, err
		}
		issues = append(issues, i)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	deleted := 0
	for _, issue := range issues {
		if err := h.deleteBitrixSyncedIssue(ctx, issue); err != nil {
			slog.Warn("bitrix cleanup-done: delete failed",
				"issue_id", util.UUIDToString(issue.ID), "error", err)
			continue
		}
		deleted++
	}
	return deleted, nil
}
