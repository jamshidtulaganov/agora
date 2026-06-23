package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/integrations/zohoprojects"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Zoho Projects → Agora one-way import (Phase 1). A Zoho project becomes a
// Agora project, its tasks become issues (one issue per task), a sprint-named
// task list becomes a Agora sprint under the same project, and each task's
// comment feed becomes issue comments. The whole thing is idempotent: re-running
// reconciles by durable markers and never duplicates.
//
// This mirrors the Bitrix importer (bitrix_sync.go + bitrix_import.go) closely:
// the same sqlc helpers create projects/sprints/issues/comments, the same
// metadata-marker dedup keys a task to its issue, and the same advisory-lock
// critical section guards the find/create/stamp sequence against a concurrent
// re-sync.
//
// Phase 2 (NOT built here) adds a periodic-sync cursor (modified-since) and a
// bidirectional status mirror (an Agora issue status change pushed back to the
// Zoho task). The hook points are marked with // TODO(zoho-phase2).

// zohoTaskIDMetaKey is the metadata key linking an issue to its Zoho task. Dedup
// keys on the task id (not the title), so re-importing updates the same issue
// instead of spawning duplicates. Mirrors bitrixTaskIDMetaKey.
const zohoTaskIDMetaKey = "zoho_task_id"

// zohoCommentsImportedMetaKey marks an issue whose Zoho comments have already
// been mirrored, so a re-import doesn't duplicate them. Mirrors
// bitrixCommentsImportedMetaKey.
const zohoCommentsImportedMetaKey = "zoho_comments_imported"

// zohoProjectIDMetaKey stores the issue's originating Zoho PROJECT id alongside
// its task id (zohoTaskIDMetaKey). Stamped on every reconcile (Phase 2) so the
// outbound status mirror is self-contained — it can build the Zoho task URL
// (which needs portal + project + task) straight from issue metadata without
// walking back to the Agora project's marker.
const zohoProjectIDMetaKey = "zoho_project_id"

// zohoSyncedAtSettingsKey is the per-project modified-since cursor key inside the
// Agora project's settings jsonb. Stores an RFC3339 UTC timestamp; the periodic
// sync reads it to fetch only tasks modified since the last run. Lives in
// settings (merged, never overwritten) so the sprint-mode toggle and any other
// settings survive a cursor advance.
const zohoSyncedAtSettingsKey = "zoho_synced_at"

// zohoOwnerFilterSettingsKey is the per-project "only this owner's tasks" filter
// (a Zoho zpuid / accounts user id) inside the Agora project's settings jsonb.
// Persisted by an owner-scoped import so the periodic poller re-applies the same
// filter rather than pulling every assignee's tasks back into a scoped project.
const zohoOwnerFilterSettingsKey = "zoho_owner_filter"

// zohoProjectMarkerPrefix is the durable marker embedded in a Agora project's
// description to link it back to its Zoho project id, used for dedup when the
// same project is imported again. Format: "zoho_project:<id>". A deterministic
// marker (rather than a new column) keeps the change surgical + upstream-
// mergeable. Mirrors bitrixProjectMarkerPrefix; the sprint marker reuses the
// SAME prefix in the sprint's goal (Zoho task-list id), exactly as Bitrix reuses
// bitrix_group: in both project.description and sprint.goal.
const zohoProjectMarkerPrefix = "zoho_project:"

// zohoSprintMarkerPrefix is the durable marker embedded in a Agora sprint's goal
// linking it back to its Zoho task-list id. Distinct from the project marker so
// a task-list id can never collide with a project id in the LIKE dedup.
const zohoSprintMarkerPrefix = "zoho_tasklist:"

// zohoSprintTaskMarkerPrefix marks a Agora sprint derived from a sprint-named
// parent TASK (not a task list). The Octane portal has no Zoho Sprints API
// access and models a sprint as a parent task like "Foo [Sprint 3]" whose
// subtasks are the work items; such a task becomes a sprint (marker in
// sprint.goal) and its subtasks become issues filed under it. Distinct prefix so
// a task id can't collide with the task-list marker in the LIKE dedup.
const zohoSprintTaskMarkerPrefix = "zoho_sprint:"

// zohoSyncTimeout bounds a single project import so a slow Zoho portal can't hold
// a goroutine open indefinitely. The endpoint runs the import in the background
// against a longer-lived context; this is the per-call ceiling used by the
// single-project entry point.
const zohoSyncTimeout = 20 * time.Minute

// zohoOutboundTimeout bounds a single outbound status mirror (resolve portal +
// list custom statuses + push) so the issue:updated listener can never wedge.
const zohoOutboundTimeout = 30 * time.Second

// zohoDefaultSyncInterval is the periodic modified-since sync cadence used when
// ZOHO_PROJECTS_SYNC_INTERVAL is unset. Set the env to "0" (or any non-positive
// duration) to disable the poller entirely.
const zohoDefaultSyncInterval = 15 * time.Minute

// --- env config -------------------------------------------------------------

func zohoClientID() string     { return strings.TrimSpace(os.Getenv("ZOHO_PROJECTS_CLIENT_ID")) }
func zohoClientSecret() string { return strings.TrimSpace(os.Getenv("ZOHO_PROJECTS_CLIENT_SECRET")) }
func zohoRefreshToken() string { return strings.TrimSpace(os.Getenv("ZOHO_PROJECTS_REFRESH_TOKEN")) }
func zohoPortal() string       { return strings.TrimSpace(os.Getenv("ZOHO_PROJECTS_PORTAL")) }
func zohoAccountsHost() string { return strings.TrimSpace(os.Getenv("ZOHO_PROJECTS_ACCOUNTS_HOST")) }
func zohoAPIHost() string      { return strings.TrimSpace(os.Getenv("ZOHO_PROJECTS_API_HOST")) }

// zohoPushStatus reports whether the outbound status mirror is enabled: an
// Agora issue (origin Zoho) status change is pushed back to its Zoho task only
// when ZOHO_PROJECTS_PUSH_STATUS is truthy. Off by default — writing to the live
// portal is opt-in, mirroring BITRIX_PUSH_STATUS.
func zohoPushStatus() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ZOHO_PROJECTS_PUSH_STATUS"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// zohoSyncInterval is the periodic modified-since sync cadence. Defaults to
// zohoDefaultSyncInterval; a non-positive duration (e.g. "0") disables the
// poller. An unparseable value falls back to the default.
func zohoSyncInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv("ZOHO_PROJECTS_SYNC_INTERVAL"))
	if raw == "" {
		return zohoDefaultSyncInterval
	}
	if d, err := time.ParseDuration(raw); err == nil {
		return d
	}
	return zohoDefaultSyncInterval
}

// zohoConfigured reports whether the Zoho Projects integration has the minimum
// env to operate: an OAuth client id/secret and a refresh token. Portal is
// optional (resolved via ListPortals when blank). When false, the endpoints
// return 400 "Zoho Projects not configured" rather than attempting a token
// fetch with empty credentials.
func zohoConfigured() bool {
	return zohoClientID() != "" && zohoClientSecret() != "" && zohoRefreshToken() != ""
}

// zohoConfigFromEnv snapshots the OAuth + host config from the environment.
func zohoConfigFromEnv() zohoprojects.Config {
	return zohoprojects.Config{
		ClientID:     zohoClientID(),
		ClientSecret: zohoClientSecret(),
		RefreshToken: zohoRefreshToken(),
		PortalID:     zohoPortal(),
		AccountsHost: zohoAccountsHost(),
		APIHost:      zohoAPIHost(),
	}
}

// --- sync state -------------------------------------------------------------

// zohoSyncState carries per-run caches and a tally shared across a batch of
// task syncs within one project import. Keeping the tasklist→sprint map here
// means a many-task import resolves each task list once instead of per task.
// Mirrors bitrixSyncState.
type zohoSyncState struct {
	client   *zohoprojects.Client
	portalID string // resolved portal id for this run

	// agoraProjectID is the Agora project all tasks in this run file under
	// (resolved once per Zoho project via getOrCreateZohoProject).
	agoraProjectID pgtype.UUID
	// sprintCache maps a Zoho task-list id -> resolved Agora sprint id, so a
	// batch creates/looks-up each sprint exactly once.
	sprintCache map[string]pgtype.UUID
	// userCache maps a Zoho owner email -> Agora user id (string), lazily
	// filled so a batch resolves each assignee once. An empty value caches a
	// failed/unknown lookup so it isn't retried per task.
	userCache map[string]string

	// importComments toggles comment import. The import path enables it; it
	// exists so a future caller can sync metadata-only.
	importComments bool

	// ownerZpuid, when non-empty, restricts the import to a single Zoho user's
	// tasks (the "only my tasks" scope). It is the Zoho owner filter value (a
	// zpuid / accounts user id) passed straight to ListTasks. Set per import
	// request and persisted on the project (settings.zoho_owner_filter) so the
	// periodic poller re-applies the SAME filter instead of pulling everyone's
	// tasks back in.
	ownerZpuid string

	// commentsThrottled is tripped the first time Zoho rate-limits the comments
	// endpoint (URL_ROLLING_THROTTLES_LIMIT_EXCEEDED). Once set, the run stops
	// attempting comment fetches — the throttle is per-endpoint and lasts minutes,
	// so every remaining call would hit the same wall. Comments for the skipped
	// tasks can be backfilled by a later, throttle-paced pass.
	commentsThrottled bool

	// subtasksThrottled is the same circuit-breaker for the per-parent subtasks
	// endpoint (also rate-limited). Once tripped, the run stops fetching subtasks.
	subtasksThrottled bool

	// incremental selects the Phase-2 modified-since semantics: when true,
	// syncZohoProject reads the per-project cursor (settings.zoho_synced_at) and
	// passes it as the ListTasks last_modified_time filter, so only changed tasks
	// are pulled. The full-import endpoint leaves this false (full walk); the
	// /sync endpoint and the periodic poller set it true. Either way the cursor is
	// advanced after a successful walk so subsequent incremental runs have a fresh
	// baseline.
	incremental bool

	// Tally for the import endpoint.
	created int
	updated int
	skipped int
}

func (h *Handler) newZohoSyncState() *zohoSyncState {
	return &zohoSyncState{
		client:         zohoprojects.NewClient(zohoConfigFromEnv()),
		sprintCache:    map[string]pgtype.UUID{},
		userCache:      map[string]string{},
		importComments: true,
	}
}

// resolvePortalID returns the portal id to operate against: the configured
// ZOHO_PROJECTS_PORTAL when set, else the first portal from ListPortals. The
// result is memoized on st for the run.
func (h *Handler) resolveZohoPortalID(ctx context.Context, st *zohoSyncState) (string, error) {
	if st.portalID != "" {
		return st.portalID, nil
	}
	if p := strings.TrimSpace(st.client.Portal()); p != "" {
		st.portalID = p
		return p, nil
	}
	portals, err := st.client.ListPortals(ctx)
	if err != nil {
		return "", fmt.Errorf("list zoho portals: %w", err)
	}
	if len(portals) == 0 {
		return "", errors.New("no zoho portals accessible for this token")
	}
	st.portalID = portals[0].ID
	return st.portalID, nil
}

// --- project import ---------------------------------------------------------

// syncZohoProject imports a single Zoho project's tasks into the given Agora
// workspace, reconciling each task into an issue. It resolves (or creates) the
// Agora project for the Zoho project, then walks every task. Per-task errors are
// logged, never fatal, so one bad task doesn't abort the batch. Returns the
// tally accumulated on st.
func (h *Handler) syncZohoProject(ctx context.Context, wsID pgtype.UUID, zohoProjectID string, st *zohoSyncState) error {
	zohoProjectID = strings.TrimSpace(zohoProjectID)
	if zohoProjectID == "" {
		return errors.New("empty zoho project id")
	}
	portalID, err := h.resolveZohoPortalID(ctx, st)
	if err != nil {
		return err
	}

	// Resolve the Zoho project's display name (best-effort) so the Agora project
	// is well-named. A failure degrades to a placeholder name.
	projectName := h.zohoProjectName(ctx, portalID, zohoProjectID, st)

	agoraProjectID, err := h.getOrCreateZohoProject(ctx, wsID, zohoProjectID, projectName)
	if err != nil {
		return fmt.Errorf("resolve agora project for zoho project %s: %w", zohoProjectID, err)
	}
	st.agoraProjectID = agoraProjectID

	// Phase 2 modified-since cursor. The full-import path leaves st.incremental
	// false (nil filter = full walk, idempotent via the zoho_task_id marker); the
	// /sync endpoint and the periodic poller set it true, so only tasks modified
	// since the persisted per-project cursor are pulled. runStart is captured
	// BEFORE the fetch so a task modified during the walk isn't missed by the next
	// incremental run (re-pulling it is harmless — dedup collapses it).
	// Resolve the owner filter ("only my tasks"): a per-request value wins and is
	// persisted on the project; otherwise fall back to the project's persisted
	// filter so the poller re-applies the SAME scope instead of pulling every
	// assignee's tasks back into a scoped project.
	ownerFilter := strings.TrimSpace(st.ownerZpuid)
	if ownerFilter != "" {
		h.saveZohoOwnerFilter(ctx, agoraProjectID, ownerFilter)
	} else {
		ownerFilter = h.loadZohoOwnerFilter(ctx, agoraProjectID)
	}

	runStart := time.Now()
	var modifiedSince *time.Time
	if st.incremental {
		if cur, ok := h.loadZohoCursor(ctx, agoraProjectID); ok {
			modifiedSince = &cur
		}
	}
	tasks, err := st.client.ListTasks(ctx, portalID, zohoProjectID, modifiedSince, ownerFilter)
	if err != nil {
		return fmt.Errorf("list zoho tasks for project %s: %w", zohoProjectID, err)
	}

	for i := range tasks {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := h.syncZohoTask(ctx, wsID, zohoProjectID, &tasks[i], st, pgtype.UUID{}, pgtype.UUID{}, 0); err != nil {
			slog.Warn("zoho import: task sync failed",
				"task_id", tasks[i].ID, "project_id", zohoProjectID, "error", err)
		}
	}

	// Advance the per-project cursor (best-effort) so the next incremental sync
	// only pulls deltas. Done for full imports too, to seed the baseline.
	h.saveZohoCursor(ctx, agoraProjectID, runStart)
	return nil
}

// zohoProjectName resolves a Zoho project id to its display name via
// ListProjects. Best-effort: on any failure it returns "" so the caller falls
// back to a placeholder. (Zoho has no cheap single-project name endpoint that
// adds value over the list call the importer already makes elsewhere.)
func (h *Handler) zohoProjectName(ctx context.Context, portalID, zohoProjectID string, st *zohoSyncState) string {
	projects, err := st.client.ListProjects(ctx, portalID)
	if err != nil {
		slog.Debug("zoho import: project name lookup failed", "project_id", zohoProjectID, "error", err)
		return ""
	}
	for _, p := range projects {
		if p.ID == zohoProjectID {
			return strings.TrimSpace(p.Name)
		}
	}
	return ""
}

// --- task → issue -----------------------------------------------------------

// maxSubtaskDepth bounds the Zoho subtask recursion so a pathological parent
// chain (or an unexpected cycle) can't spin. Zoho task nesting is shallow in
// practice; 5 levels is well beyond any real tree.
const maxSubtaskDepth = 5

// syncZohoTask reconciles a Zoho task into a Agora issue and then imports its
// subtasks (one level deeper) as Agora sub-issues, recursively. parentIssueID is
// the Agora issue this task hangs under (zero/invalid for a top-level task);
// depth bounds the recursion. The locked reconcile runs first and its lock is
// released before the subtask fetch, so a deep tree never holds an ancestor's
// lock-tx open across child API calls.
func (h *Handler) syncZohoTask(ctx context.Context, wsID pgtype.UUID, zohoProjectID string, task *zohoprojects.Task, st *zohoSyncState, parentIssueID, sprintID pgtype.UUID, depth int) error {
	// A TOP-LEVEL task whose name denotes a sprint becomes a Agora sprint (not an
	// issue); its subtasks are filed as issues under that sprint. Detection is
	// top-level only, so a subtask that merely mentions "sprint" stays a normal
	// issue.
	if depth == 0 && zohoprojects.TaskIsSprint(task.Name) {
		sid := h.getOrCreateZohoSprintFromTask(ctx, wsID, st.agoraProjectID, task, st)
		if sid.Valid && task.HasSubtasks && depth < maxSubtaskDepth && !st.subtasksThrottled {
			// Subtasks of a sprint-task have no parent issue — they are the sprint's
			// work items, filed directly under the sprint.
			h.importZohoSubtasks(ctx, wsID, zohoProjectID, task.ID, pgtype.UUID{}, sid, st, depth)
		}
		return nil
	}

	issueID, err := h.reconcileZohoTask(ctx, wsID, zohoProjectID, task, st, parentIssueID, sprintID)
	if err != nil {
		return err
	}
	// Import subtasks as sub-issues, carrying the same sprint membership. Gated on
	// Zoho's HasSubtasks flag so childless tasks cost no extra (rate-limited) API
	// call, bounded by depth, and stopped once Zoho throttles the subtasks endpoint.
	if issueID.Valid && task.HasSubtasks && depth < maxSubtaskDepth && !st.subtasksThrottled {
		h.importZohoSubtasks(ctx, wsID, zohoProjectID, task.ID, issueID, sprintID, st, depth)
	}
	return nil
}

// importZohoSubtasks fetches a parent task's direct children and reconciles each
// into a Agora sub-issue (parent_issue_id = parentIssueID), recursing for deeper
// levels. Best-effort: a fetch failure is logged and skipped; a Zoho throttle
// trips a per-run breaker so the rest of the run stops hitting the subtasks
// endpoint.
func (h *Handler) importZohoSubtasks(ctx context.Context, wsID pgtype.UUID, zohoProjectID, parentTaskID string, parentIssueID, sprintID pgtype.UUID, st *zohoSyncState, depth int) {
	subs, err := st.client.ListSubtasks(ctx, st.portalID, zohoProjectID, parentTaskID)
	if err != nil {
		if zohoprojects.IsThrottle(err) {
			st.subtasksThrottled = true
			slog.Warn("zoho import: subtasks rate-limited by Zoho; skipping subtask import for the rest of this run",
				"parent_task_id", parentTaskID)
			return
		}
		slog.Warn("zoho import: fetch subtasks failed", "parent_task_id", parentTaskID, "error", err)
		return
	}
	for i := range subs {
		if ctx.Err() != nil {
			return
		}
		// depth+1, so a subtask is never itself treated as a sprint container.
		if err := h.syncZohoTask(ctx, wsID, zohoProjectID, &subs[i], st, parentIssueID, sprintID, depth+1); err != nil {
			slog.Warn("zoho import: subtask sync failed",
				"subtask_id", subs[i].ID, "parent_task_id", parentTaskID, "error", err)
		}
	}
}

// reconcileZohoTask reconciles a single Zoho task into a Agora issue: it dedups
// on the zoho_task_id marker, creating the issue on first sight and updating
// status/assignee/sprint in place on re-import. When parentIssueID is valid the
// issue is linked under it (Zoho subtask → Agora sub-issue). Returns the resolved
// Agora issue id so the caller can thread it as the parent of any subtasks.
// Mirrors syncBitrixTaskWithState (minus the inbound-webhook/tag-filter concerns,
// which don't apply to a pull import).
func (h *Handler) reconcileZohoTask(ctx context.Context, wsID pgtype.UUID, zohoProjectID string, task *zohoprojects.Task, st *zohoSyncState, parentIssueID, sprintID pgtype.UUID) (pgtype.UUID, error) {
	if strings.TrimSpace(task.ID) == "" {
		st.skipped++
		return pgtype.UUID{}, errors.New("empty task id")
	}

	// Serialize the find/create/stamp sequence per (workspace, task) with a
	// Postgres transaction-scoped advisory lock so two interleaved imports of the
	// SAME task can't both pass the dedup lookup and double-create the issue.
	// Identical mechanism to the Bitrix sync: hold a short tx with
	// pg_advisory_xact_lock(hashtext(<key>)) for the whole critical section, then
	// commit to release. Key namespaced with ":zoho:" so it can't collide with a
	// Bitrix lock on the same numeric id.
	lockKey := util.UUIDToString(wsID) + ":zoho:" + task.ID
	lockTx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("begin sync lock tx: %w", err)
	}
	defer func() { _ = lockTx.Rollback(ctx) }()
	if _, err := lockTx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, lockKey); err != nil {
		return pgtype.UUID{}, fmt.Errorf("acquire sync lock: %w", err)
	}
	releaseLock := func() {
		if cerr := lockTx.Commit(ctx); cerr != nil {
			slog.Warn("zoho import: lock tx commit failed", "task_id", task.ID, "error", cerr)
		}
	}
	defer releaseLock()

	existing, found, err := h.findIssueByZohoTaskID(ctx, wsID, task.ID)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("dedup lookup: %w", err)
	}

	mappedStatus := mapZohoStatusToAgora(task)

	// Sprint membership: prefer the sprint threaded down from a sprint-task
	// ancestor; otherwise fall back to a sprint-named task LIST (resolveZohoSprint).
	// Non-fatal: a failure leaves the issue filed in the project without a sprint.
	effectiveSprint := sprintID
	if !effectiveSprint.Valid {
		effectiveSprint = h.resolveZohoSprint(ctx, wsID, st.agoraProjectID, task, st)
	}

	if found {
		// Already imported. Reconcile status + assignee with RAW bus-free updates
		// (no EventIssueUpdated publish). This is the echo-break for the Phase-2
		// bidirectional status mirror: the outbound push lives in the event-driven
		// mirrorIssueStatusToZoho (registerZohoOutbound), which fires on
		// EventIssueUpdated. Because these inbound reconciles never publish that
		// event, an Agora status that originated FROM Zoho is never bounced back to
		// Zoho. Only a genuine Agora-side status change (published over the bus)
		// reaches mirrorIssueStatusToZoho.
		if existing.Status != mappedStatus {
			if _, err := h.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
				ID:          existing.ID,
				Status:      mappedStatus,
				WorkspaceID: wsID,
			}); err != nil {
				return pgtype.UUID{}, fmt.Errorf("update issue status: %w", err)
			}
			slog.Info("zoho import: updated issue status in place",
				"issue_id", util.UUIDToString(existing.ID), "task_id", task.ID, "status", mappedStatus)
		}

		// Only (re)assign when the Zoho owner maps to a real workspace member.
		// NEVER clear an assignee just because the owner isn't an Agora member —
		// that would wipe a manual assignment on every re-sync (the owner email
		// often differs from the member's Agora email).
		assigneeType, assigneeID := h.zohoResolveAssignee(ctx, wsID, &task.Owner, st)
		if assigneeType.Valid && assigneeID.Valid && !sameIssueAssignee(existing, assigneeType, assigneeID) {
			if err := h.zohoSetIssueAssignee(ctx, existing.ID, wsID, assigneeType, assigneeID); err != nil {
				return pgtype.UUID{}, fmt.Errorf("update issue assignee: %w", err)
			}
		}
		// Always refresh the Zoho owner display metadata so the assignee is
		// visible even when they aren't a Agora member, and so older imported
		// issues get backfilled.
		h.setZohoOwnerMetadata(ctx, existing.ID, wsID, &task.Owner)
		// Backfill the originating Zoho project id (Phase 2) so pre-Phase-2 issues
		// gain the metadata the outbound status mirror needs.
		h.stampZohoProjectID(ctx, existing.ID, wsID, zohoProjectID)

		// Backfill the project for issues created before a project link existed
		// (only when missing — never reassign a project a user may have moved).
		if !existing.ProjectID.Valid && st.agoraProjectID.Valid {
			if _, err := h.DB.Exec(ctx,
				`UPDATE issue SET project_id = $3, updated_at = now()
				   WHERE id = $1 AND workspace_id = $2`,
				existing.ID, wsID, st.agoraProjectID); err != nil {
				slog.Warn("zoho import: backfill project failed",
					"issue_id", util.UUIDToString(existing.ID), "task_id", task.ID, "error", err)
			}
		}
		// Backfill the parent link for a subtask imported before its parent issue
		// existed (only when missing — never re-parent an issue a user may have
		// moved).
		if parentIssueID.Valid && !existing.ParentIssueID.Valid {
			if _, err := h.DB.Exec(ctx,
				`UPDATE issue SET parent_issue_id = $3, updated_at = now()
				   WHERE id = $1 AND workspace_id = $2 AND parent_issue_id IS NULL`,
				existing.ID, wsID, parentIssueID); err != nil {
				slog.Warn("zoho import: backfill parent failed",
					"issue_id", util.UUIDToString(existing.ID), "task_id", task.ID, "error", err)
			}
		}
		// Link to the sprint on re-import too (idempotent upsert keyed on issue id).
		if effectiveSprint.Valid {
			if err := h.Queries.SetIssueSprint(ctx, db.SetIssueSprintParams{
				IssueID:  existing.ID,
				SprintID: effectiveSprint,
			}); err != nil {
				slog.Warn("zoho import: link issue to sprint failed",
					"issue_id", util.UUIDToString(existing.ID), "task_id", task.ID, "error", err)
			}
		}
		st.updated++
		return existing.ID, nil
	}

	// New issue. Creator is the workspace owner (the integration is a system
	// actor with no member of its own).
	ownerID, err := h.zohoWorkspaceOwner(ctx, wsID)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("resolve workspace owner: %w", err)
	}

	draft := zohoprojects.MapTaskToIssue(task)
	assigneeType, assigneeID := h.zohoResolveAssignee(ctx, wsID, &task.Owner, st)

	res, err := h.IssueService.Create(ctx, service.IssueCreateParams{
		WorkspaceID:  wsID,
		Title:        draft.Title,
		Description:  strToText(draft.Description),
		Status:       draft.Status,
		Priority:     "none",
		AssigneeType: assigneeType,
		AssigneeID:   assigneeID,
		CreatorType:  "member",
		CreatorID:    ownerID,
		ProjectID:    st.agoraProjectID,
		// Link a Zoho subtask under its parent's Agora issue (zero/invalid for a
		// top-level task).
		ParentIssueID: parentIssueID,
		// Dedup is on the task id (set as metadata post-create), not the title.
		AllowDuplicate: true,
	}, service.IssueCreateOpts{
		ActorID: util.UUIDToString(ownerID),
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("create issue: %w", err)
	}

	// Stamp the link so the next import dedups onto this issue.
	idValue, err := json.Marshal(task.ID)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("encode task id: %w", err)
	}
	if _, err := h.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
		ID:          res.Issue.ID,
		WorkspaceID: wsID,
		Key:         zohoTaskIDMetaKey,
		Value:       idValue,
	}); err != nil {
		return pgtype.UUID{}, fmt.Errorf("set zoho_task_id metadata: %w", err)
	}
	// Also stamp the originating Zoho project id (Phase 2) so the outbound status
	// mirror can address the task (portal + project + task) from metadata alone.
	h.stampZohoProjectID(ctx, res.Issue.ID, wsID, zohoProjectID)

	if effectiveSprint.Valid {
		if err := h.Queries.SetIssueSprint(ctx, db.SetIssueSprintParams{
			IssueID:  res.Issue.ID,
			SprintID: effectiveSprint,
		}); err != nil {
			slog.Warn("zoho import: link issue to sprint failed",
				"issue_id", util.UUIDToString(res.Issue.ID), "task_id", task.ID, "error", err)
		}
	}

	h.setZohoOwnerMetadata(ctx, res.Issue.ID, wsID, &task.Owner)

	slog.Info("zoho import: created issue from task",
		"issue_id", util.UUIDToString(res.Issue.ID), "task_id", task.ID,
		"status", draft.Status, "project_id", util.UUIDToString(st.agoraProjectID))
	st.created++

	// Enrich with the task's comments, once (create path only, so a re-import
	// doesn't duplicate them). Best-effort. Skipped once the run has been
	// throttled by Zoho's per-endpoint comment rate limit.
	if st.importComments && !st.commentsThrottled {
		h.importZohoComments(ctx, wsID, res.Issue.ID, ownerID, zohoProjectID, task.ID, st)
	}
	return res.Issue.ID, nil
}

// --- project marker dedup ---------------------------------------------------

// getOrCreateZohoProject returns the Agora project id for a Zoho project in the
// given workspace, creating it on first sight. Dedup is durable via a
// "zoho_project:<id>" marker in the project description (raw pgx LIKE, no new
// sqlc method). Mirrors getOrCreateBitrixProject exactly.
func (h *Handler) getOrCreateZohoProject(ctx context.Context, workspaceID pgtype.UUID, zohoProjectID, projectName string) (pgtype.UUID, error) {
	zohoProjectID = strings.TrimSpace(zohoProjectID)
	if zohoProjectID == "" {
		return pgtype.UUID{}, fmt.Errorf("empty zoho project id")
	}
	marker := zohoProjectMarkerPrefix + zohoProjectID

	var existingID pgtype.UUID
	err := h.DB.QueryRow(ctx,
		`SELECT id FROM project
		  WHERE workspace_id = $1 AND description LIKE '%' || $2 || '%'
		  ORDER BY created_at ASC
		  LIMIT 1`,
		workspaceID, marker).Scan(&existingID)
	if err == nil {
		// Backfill a real title over a placeholder left by a prior failed name
		// lookup (e.g. a transient Zoho timeout). Only touches rows still showing
		// the "Zoho project <id>" placeholder — never a title the user edited.
		if name := strings.TrimSpace(projectName); name != "" {
			if _, uerr := h.DB.Exec(ctx,
				`UPDATE project SET title = $2, updated_at = now()
				   WHERE id = $1 AND title LIKE 'Zoho project %'`,
				existingID, name); uerr != nil {
				slog.Debug("zoho import: project name backfill failed",
					"project_id", util.UUIDToString(existingID), "error", uerr)
			}
		}
		return existingID, nil
	}
	if err != pgx.ErrNoRows {
		return pgtype.UUID{}, fmt.Errorf("lookup zoho project: %w", err)
	}

	title := strings.TrimSpace(projectName)
	if title == "" {
		title = "Zoho project " + zohoProjectID
	}
	description := "Imported from Zoho Projects.\n" + marker

	project, err := h.Queries.CreateProject(ctx, db.CreateProjectParams{
		WorkspaceID: workspaceID,
		Title:       title,
		Description: strToText(description),
		Status:      "planned",
		Priority:    "none",
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("create zoho project: %w", err)
	}
	slog.Info("zoho import: created project for zoho project",
		"project_id", util.UUIDToString(project.ID), "zoho_project_id", zohoProjectID,
		"title", title, "workspace_id", util.UUIDToString(workspaceID))
	return project.ID, nil
}

// --- sprint marker dedup ----------------------------------------------------

// resolveZohoSprint maps a task's Zoho task list to a Agora sprint, but ONLY for
// sprint-named lists (TasklistIsSprint). Returns the zero UUID (Valid=false)
// when the task has no task list or the list isn't sprint-named, so a regular
// task list never spuriously mints a sprint — matching the Bitrix
// sprint-named-group rule. Non-fatal: any failure returns the zero UUID.
func (h *Handler) resolveZohoSprint(ctx context.Context, wsID, agoraProjectID pgtype.UUID, task *zohoprojects.Task, st *zohoSyncState) pgtype.UUID {
	if !agoraProjectID.Valid {
		return pgtype.UUID{}
	}
	tasklistID := strings.TrimSpace(task.TasklistID)
	name := strings.TrimSpace(task.TasklistName)
	if tasklistID == "" || !zohoprojects.TasklistIsSprint(name) {
		return pgtype.UUID{}
	}
	sid, err := h.getOrCreateZohoSprint(ctx, wsID, agoraProjectID, tasklistID, name, st)
	if err != nil {
		slog.Warn("zoho import: could not resolve sprint for tasklist, filing without sprint",
			"tasklist_id", tasklistID, "workspace_id", util.UUIDToString(wsID), "error", err)
		return pgtype.UUID{}
	}
	return sid
}

// getOrCreateZohoSprint returns the Agora sprint id for a sprint-named Zoho task
// list, creating it under the given project on first sight. It mirrors
// getOrCreateBitrixSprint: the durable "zoho_tasklist:<id>" marker lives in the
// sprint's GOAL (sprint has no description column), dedup is scoped to the parent
// project, and resolutions are cached on st for the batch.
func (h *Handler) getOrCreateZohoSprint(ctx context.Context, workspaceID, projectID pgtype.UUID, tasklistID, tasklistName string, st *zohoSyncState) (pgtype.UUID, error) {
	tasklistID = strings.TrimSpace(tasklistID)
	if tasklistID == "" {
		return pgtype.UUID{}, fmt.Errorf("empty tasklist id")
	}
	if id, ok := st.sprintCache[tasklistID]; ok {
		return id, nil
	}

	marker := zohoSprintMarkerPrefix + tasklistID

	var existingID pgtype.UUID
	err := h.DB.QueryRow(ctx,
		`SELECT id FROM sprint
		  WHERE project_id = $1 AND goal LIKE '%' || $2 || '%'
		  ORDER BY created_at ASC
		  LIMIT 1`,
		projectID, marker).Scan(&existingID)
	if err == nil {
		st.sprintCache[tasklistID] = existingID
		return existingID, nil
	}
	if err != pgx.ErrNoRows {
		return pgtype.UUID{}, fmt.Errorf("lookup zoho sprint: %w", err)
	}

	name := strings.TrimSpace(tasklistName)
	if name == "" {
		name = "Zoho tasklist " + tasklistID
	}

	sprint, err := h.Queries.CreateSprint(ctx, db.CreateSprintParams{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		Name:        name,
		Goal:        marker,
		Status:      "active",
		StartDate:   pgtype.Timestamptz{},
		EndDate:     pgtype.Timestamptz{},
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("create zoho sprint: %w", err)
	}
	st.sprintCache[tasklistID] = sprint.ID
	slog.Info("zoho import: created sprint for tasklist",
		"sprint_id", util.UUIDToString(sprint.ID), "tasklist_id", tasklistID,
		"name", name, "project_id", util.UUIDToString(projectID),
		"workspace_id", util.UUIDToString(workspaceID))
	return sprint.ID, nil
}

// getOrCreateZohoSprintFromTask resolves the Agora sprint for a sprint-named
// parent TASK (e.g. "Foo [Sprint 3]"), creating it under the project on first
// sight. Mirrors getOrCreateZohoSprint but keys on the TASK id with the distinct
// "zoho_sprint:" marker, and caches under a "task:" namespace so a task id can't
// collide with a task-list id in st.sprintCache. Returns the zero UUID on any
// failure (the caller then files the subtasks without a sprint).
func (h *Handler) getOrCreateZohoSprintFromTask(ctx context.Context, workspaceID, projectID pgtype.UUID, task *zohoprojects.Task, st *zohoSyncState) pgtype.UUID {
	if !projectID.Valid {
		return pgtype.UUID{}
	}
	taskID := strings.TrimSpace(task.ID)
	if taskID == "" {
		return pgtype.UUID{}
	}
	cacheKey := "task:" + taskID
	if id, ok := st.sprintCache[cacheKey]; ok {
		return id
	}

	marker := zohoSprintTaskMarkerPrefix + taskID
	var existingID pgtype.UUID
	err := h.DB.QueryRow(ctx,
		`SELECT id FROM sprint
		  WHERE project_id = $1 AND goal LIKE '%' || $2 || '%'
		  ORDER BY created_at ASC
		  LIMIT 1`,
		projectID, marker).Scan(&existingID)
	if err == nil {
		st.sprintCache[cacheKey] = existingID
		return existingID
	}
	if err != pgx.ErrNoRows {
		slog.Warn("zoho import: lookup sprint-from-task failed", "task_id", taskID, "error", err)
		return pgtype.UUID{}
	}

	name := strings.TrimSpace(task.Name)
	if name == "" {
		name = "Zoho sprint " + taskID
	}
	sprint, err := h.Queries.CreateSprint(ctx, db.CreateSprintParams{
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		Name:        name,
		Goal:        marker,
		Status:      "active",
		StartDate:   pgtype.Timestamptz{},
		EndDate:     pgtype.Timestamptz{},
	})
	if err != nil {
		slog.Warn("zoho import: create sprint-from-task failed", "task_id", taskID, "error", err)
		return pgtype.UUID{}
	}
	st.sprintCache[cacheKey] = sprint.ID
	slog.Info("zoho import: created sprint from task",
		"sprint_id", util.UUIDToString(sprint.ID), "task_id", taskID,
		"name", name, "project_id", util.UUIDToString(projectID))
	return sprint.ID
}

// --- comments ---------------------------------------------------------------

// importZohoComments mirrors a task's Zoho comment feed onto the freshly created
// issue as issue comments, once. Each Zoho comment becomes one member-authored
// issue comment (author = workspace owner) with a provenance header. Idempotent
// via the zoho_comments_imported metadata flag. Mirrors importBitrixComments.
func (h *Handler) importZohoComments(ctx context.Context, wsID, issueID, ownerID pgtype.UUID, zohoProjectID, taskID string, st *zohoSyncState) {
	comments, err := st.client.GetTaskComments(ctx, st.portalID, zohoProjectID, taskID)
	if err != nil {
		if zohoprojects.IsThrottle(err) {
			// Zoho throttled the comments endpoint (100 req / 2 min). Trip the
			// breaker so the rest of this run skips comments instead of hammering
			// the same wall on every remaining task.
			st.commentsThrottled = true
			slog.Warn("zoho import: comments rate-limited by Zoho; skipping comment import for the rest of this run",
				"task_id", taskID)
			return
		}
		slog.Warn("zoho import: fetch comments failed", "task_id", taskID, "error", err)
		return
	}
	if len(comments) == 0 {
		return
	}

	imported := 0
	for _, c := range comments {
		content := formatZohoComment(c)
		if strings.TrimSpace(content) == "" {
			continue
		}
		if _, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
			IssueID:     issueID,
			WorkspaceID: wsID,
			AuthorType:  "member",
			AuthorID:    ownerID,
			Content:     content,
			Type:        "comment",
		}); err != nil {
			slog.Warn("zoho import: create comment failed",
				"task_id", taskID, "issue_id", util.UUIDToString(issueID), "error", err)
			continue
		}
		imported++
	}

	h.setZohoImportFlag(ctx, wsID, issueID, zohoCommentsImportedMetaKey)
	slog.Info("zoho import: imported comments",
		"task_id", taskID, "issue_id", util.UUIDToString(issueID), "count", imported)
}

// formatZohoComment renders a Zoho comment as an Agora issue-comment body with a
// provenance header: "**Zoho — <author> (<date>)**:\n<text>". A missing
// author/date degrades gracefully. Mirrors formatBitrixComment.
func formatZohoComment(c zohoprojects.Comment) string {
	author := strings.TrimSpace(c.Author)
	if author == "" {
		author = "unknown"
	}
	header := "**Zoho — " + author
	if d := strings.TrimSpace(c.Date); d != "" {
		header += " (" + d + ")"
	}
	header += "**:"
	return header + "\n" + strings.TrimSpace(c.Content)
}

// --- assignee + owner metadata ----------------------------------------------

// zohoResolveAssignee maps a Zoho task owner to a Agora member assignee. It
// returns an unset pair (leaving the issue unassigned) when the owner has no
// email, the email matches no Agora user, or that user isn't a member of this
// workspace. SD staff share a salesdoc.io email between Zoho and Agora, so email
// is the primary (and only, in Phase 1) link — there is no Zoho external-identity
// provider yet. Results are cached on st per email. Mirrors the email half of
// bitrixResolveAssignee.
func (h *Handler) zohoResolveAssignee(ctx context.Context, wsID pgtype.UUID, owner *zohoprojects.User, st *zohoSyncState) (pgtype.Text, pgtype.UUID) {
	none := pgtype.Text{}
	if owner == nil {
		return none, pgtype.UUID{}
	}
	email := strings.ToLower(strings.TrimSpace(owner.Email))
	if email == "" {
		return none, pgtype.UUID{}
	}
	if cached, ok := st.userCache[email]; ok {
		if cached == "" {
			return none, pgtype.UUID{}
		}
		return h.assigneeIfMember(ctx, wsID, cached)
	}
	agoraUser, err := h.Queries.GetUserByEmail(ctx, owner.Email)
	if err != nil {
		st.userCache[email] = ""
		return none, pgtype.UUID{}
	}
	userID := util.UUIDToString(agoraUser.ID)
	st.userCache[email] = userID
	return h.assigneeIfMember(ctx, wsID, userID)
}

// zohoSetIssueAssignee applies an assignee change with a RAW pgx UPDATE — no
// EventIssueUpdated publish — mirroring bitrixSetIssueAssignee. A null
// type/id pair clears the assignment.
func (h *Handler) zohoSetIssueAssignee(ctx context.Context, issueID, wsID pgtype.UUID, assigneeType pgtype.Text, assigneeID pgtype.UUID) error {
	var typeArg any
	if assigneeType.Valid {
		typeArg = assigneeType.String
	}
	var idArg any
	if assigneeID.Valid {
		idArg = util.UUIDToString(assigneeID)
	}
	_, err := h.DB.Exec(ctx,
		`UPDATE issue
		    SET assignee_type = $3, assignee_id = $4::uuid, updated_at = now()
		  WHERE id = $1 AND workspace_id = $2`,
		issueID, wsID, typeArg, idArg)
	return err
}

// setZohoOwnerMetadata records the Zoho owner's id, name and email on the issue
// metadata so the assignee is visible in the issue's Metadata panel even when
// the person isn't a Agora member. Best-effort per key; runs on create +
// re-import. Mirrors setBitrixResponsibleMetadata.
func (h *Handler) setZohoOwnerMetadata(ctx context.Context, issueID, wsID pgtype.UUID, owner *zohoprojects.User) {
	if owner == nil {
		return
	}
	kv := [][2]string{
		{"zoho_owner_id", strings.TrimSpace(owner.ID)},
		{"zoho_owner_name", strings.TrimSpace(owner.Name)},
		{"zoho_owner_email", strings.TrimSpace(owner.Email)},
	}
	for _, p := range kv {
		if p[1] == "" {
			continue
		}
		val, err := json.Marshal(p[1])
		if err != nil {
			continue
		}
		if _, err := h.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
			ID:          issueID,
			WorkspaceID: wsID,
			Key:         p[0],
			Value:       val,
		}); err != nil {
			slog.Warn("zoho import: set owner metadata failed",
				"issue_id", util.UUIDToString(issueID), "key", p[0], "error", err)
		}
	}
}

// --- shared helpers ---------------------------------------------------------

// zohoWorkspaceOwner returns the user_id of the first member with role 'owner'
// in the workspace. Mirrors bitrixWorkspaceOwner.
func (h *Handler) zohoWorkspaceOwner(ctx context.Context, wsID pgtype.UUID) (pgtype.UUID, error) {
	members, err := h.Queries.ListMembers(ctx, wsID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	for _, m := range members {
		if m.Role == "owner" {
			return m.UserID, nil
		}
	}
	return pgtype.UUID{}, fmt.Errorf("workspace %s has no owner member", util.UUIDToString(wsID))
}

// findIssueByZohoTaskID returns the issue in the workspace whose metadata matches
// {"zoho_task_id": <taskID>}, using the same JSONB @> containment the Bitrix
// dedup uses. found=false (nil error) means no match. Mirrors
// findIssueByBitrixTaskID.
func (h *Handler) findIssueByZohoTaskID(ctx context.Context, wsID pgtype.UUID, taskID string) (db.Issue, bool, error) {
	filter, err := json.Marshal(map[string]string{zohoTaskIDMetaKey: taskID})
	if err != nil {
		return db.Issue{}, false, err
	}
	row := h.DB.QueryRow(ctx,
		`SELECT id, workspace_id, title, description, status, priority,
		        assignee_type, assignee_id, creator_type, creator_id,
		        parent_issue_id, acceptance_criteria, context_refs, position,
		        due_date, created_at, updated_at, number, project_id,
		        origin_type, origin_id, first_executed_at, start_date, metadata
		   FROM issue
		  WHERE workspace_id = $1 AND metadata @> $2::jsonb
		  ORDER BY created_at ASC
		  LIMIT 1`,
		wsID, string(filter))

	var i db.Issue
	err = row.Scan(
		&i.ID, &i.WorkspaceID, &i.Title, &i.Description, &i.Status, &i.Priority,
		&i.AssigneeType, &i.AssigneeID, &i.CreatorType, &i.CreatorID,
		&i.ParentIssueID, &i.AcceptanceCriteria, &i.ContextRefs, &i.Position,
		&i.DueDate, &i.CreatedAt, &i.UpdatedAt, &i.Number, &i.ProjectID,
		&i.OriginType, &i.OriginID, &i.FirstExecutedAt, &i.StartDate, &i.Metadata,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Issue{}, false, nil
		}
		return db.Issue{}, false, err
	}
	return i, true, nil
}

// setZohoImportFlag stamps a boolean true under the given metadata key on the
// issue, marking a one-time import (comments) as done. Mirrors
// setBitrixImportFlag.
func (h *Handler) setZohoImportFlag(ctx context.Context, wsID, issueID pgtype.UUID, key string) {
	val, _ := json.Marshal(true)
	if _, err := h.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
		ID:          issueID,
		WorkspaceID: wsID,
		Key:         key,
		Value:       val,
	}); err != nil {
		slog.Debug("zoho import: set import flag failed", "key", key, "error", err)
	}
}

// mapZohoStatusToAgora maps a Zoho task's status to a Agora issue status — the
// single handler-level status entry point (spec-named). It prefers the human
// status name and falls back to Zoho's status "type" (open/closed) for custom
// statuses the name-matcher doesn't recognize; see zohoprojects.MapStatus for
// the full table. Phase-2's reverse mapping (Agora status -> Zoho) would live
// alongside this.
func mapZohoStatusToAgora(task *zohoprojects.Task) string {
	if task == nil {
		return zohoprojects.StatusTodo
	}
	return zohoprojects.MapStatusWithType(task.Status, task.StatusType)
}

// stampZohoProjectID records the originating Zoho project id on the issue so the
// outbound status mirror can build the Zoho task URL (portal + project + task)
// from metadata alone. Best-effort; called on create and on re-import backfill.
func (h *Handler) stampZohoProjectID(ctx context.Context, issueID, wsID pgtype.UUID, zohoProjectID string) {
	zohoProjectID = strings.TrimSpace(zohoProjectID)
	if zohoProjectID == "" {
		return
	}
	val, err := json.Marshal(zohoProjectID)
	if err != nil {
		return
	}
	if _, err := h.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
		ID:          issueID,
		WorkspaceID: wsID,
		Key:         zohoProjectIDMetaKey,
		Value:       val,
	}); err != nil {
		slog.Warn("zoho import: set zoho_project_id metadata failed",
			"issue_id", util.UUIDToString(issueID), "error", err)
	}
}

// --- modified-since cursor (Phase 2) ----------------------------------------

// loadZohoCursor returns the persisted per-project modified-since cursor (stored
// as settings->>'zoho_synced_at', an RFC3339 timestamp). ok=false when unset or
// unparseable, in which case the caller does a full walk.
func (h *Handler) loadZohoCursor(ctx context.Context, agoraProjectID pgtype.UUID) (time.Time, bool) {
	if !agoraProjectID.Valid {
		return time.Time{}, false
	}
	var raw pgtype.Text
	err := h.DB.QueryRow(ctx,
		`SELECT settings->>'`+zohoSyncedAtSettingsKey+`' FROM project WHERE id = $1`,
		agoraProjectID).Scan(&raw)
	if err != nil || !raw.Valid {
		return time.Time{}, false
	}
	s := strings.TrimSpace(raw.String)
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// saveZohoCursor advances the per-project modified-since cursor, MERGING into
// the settings jsonb (`||`) so the sprint-mode toggle and any other settings key
// survive. Best-effort: a failure only means the next sync re-walks more tasks
// (idempotent), never a data error.
func (h *Handler) saveZohoCursor(ctx context.Context, agoraProjectID pgtype.UUID, at time.Time) {
	if !agoraProjectID.Valid {
		return
	}
	if _, err := h.DB.Exec(ctx,
		`UPDATE project
		    SET settings = COALESCE(settings, '{}'::jsonb) || jsonb_build_object($2::text, $3::text),
		        updated_at = now()
		  WHERE id = $1`,
		agoraProjectID, zohoSyncedAtSettingsKey, at.UTC().Format(time.RFC3339)); err != nil {
		slog.Warn("zoho sync: persist cursor failed",
			"project_id", util.UUIDToString(agoraProjectID), "error", err)
	}
}

// loadZohoOwnerFilter returns the per-project persisted owner filter (a Zoho
// zpuid), or "" when none. The poller uses it to re-apply an owner-scoped
// import's "only my tasks" filter instead of pulling everyone's tasks.
func (h *Handler) loadZohoOwnerFilter(ctx context.Context, agoraProjectID pgtype.UUID) string {
	if !agoraProjectID.Valid {
		return ""
	}
	var raw pgtype.Text
	if err := h.DB.QueryRow(ctx,
		`SELECT settings->>'`+zohoOwnerFilterSettingsKey+`' FROM project WHERE id = $1`,
		agoraProjectID).Scan(&raw); err != nil || !raw.Valid {
		return ""
	}
	return strings.TrimSpace(raw.String)
}

// saveZohoOwnerFilter persists the owner filter on the project, MERGING into the
// settings jsonb so the cursor / sprint-mode toggle survive. Best-effort.
func (h *Handler) saveZohoOwnerFilter(ctx context.Context, agoraProjectID pgtype.UUID, ownerZpuid string) {
	if !agoraProjectID.Valid || strings.TrimSpace(ownerZpuid) == "" {
		return
	}
	if _, err := h.DB.Exec(ctx,
		`UPDATE project
		    SET settings = COALESCE(settings, '{}'::jsonb) || jsonb_build_object($2::text, $3::text),
		        updated_at = now()
		  WHERE id = $1`,
		agoraProjectID, zohoOwnerFilterSettingsKey, strings.TrimSpace(ownerZpuid)); err != nil {
		slog.Warn("zoho sync: persist owner filter failed",
			"project_id", util.UUIDToString(agoraProjectID), "error", err)
	}
}

// extractZohoProjectID pulls the Zoho project id out of a Agora project's
// description marker ("zoho_project:<id>"). Returns "" when the marker is absent.
// The id runs to the first whitespace/newline (the marker is written on its own
// line by getOrCreateZohoProject).
func extractZohoProjectID(description string) string {
	idx := strings.Index(description, zohoProjectMarkerPrefix)
	if idx < 0 {
		return ""
	}
	rest := description[idx+len(zohoProjectMarkerPrefix):]
	fields := strings.FieldsFunc(rest, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ' ' || r == '\t'
	})
	if len(fields) == 0 {
		return ""
	}
	return strings.TrimSpace(fields[0])
}

// --- outbound status mirror (Phase 2, bidirectional) ------------------------

// registerZohoOutbound subscribes to issue:updated and pushes status changes of
// Zoho-originated issues back to the portal task. No-op entirely unless the
// integration is configured AND ZOHO_PROJECTS_PUSH_STATUS is on, so deployments
// that only want the one-way import pay nothing. Wired from handler.New().
//
// Echo-safety: the inbound reconcile (syncZohoTask) updates status with RAW
// bus-free UPDATEs that never publish EventIssueUpdated, so a status that
// originated from Zoho is never mirrored back to Zoho. Only a genuine Agora-side
// change reaches this listener.
func (h *Handler) registerZohoOutbound() {
	if h.Bus == nil {
		return
	}
	if !zohoConfigured() || !zohoPushStatus() {
		return
	}
	h.Bus.Subscribe(protocol.EventIssueUpdated, func(e events.Event) {
		if !zohoShouldMirror(e.Payload) {
			return
		}
		issueID := zohoIssueIDFromPayload(e.Payload)
		if issueID == "" {
			return
		}
		// Detached + bounded so the publishing HTTP path is never blocked.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), zohoOutboundTimeout)
			defer cancel()
			if err := h.mirrorIssueStatusToZoho(ctx, issueID); err != nil {
				slog.Warn("zoho outbound: mirror failed", "issue_id", issueID, "error", err)
			}
		}()
	})
}

// zohoShouldMirror decides whether an issue:updated payload is a real status
// change worth mirroring. Proceeds ONLY when status_changed is present AND true:
// an absent key (title-only edit) and a present-and-false flag both skip. Same
// gate as bitrixShouldMirror.
func zohoShouldMirror(payload any) bool {
	m, ok := payload.(map[string]any)
	if !ok {
		return false
	}
	changed, ok := m["status_changed"].(bool)
	return ok && changed
}

// zohoIssueIDFromPayload digs the issue id out of an issue:updated payload,
// tolerating both the handler-path (IssueResponse) and service-path
// (map[string]any) shapes. Mirrors bitrixIssueIDFromPayload.
func zohoIssueIDFromPayload(payload any) string {
	m, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	issue, ok := m["issue"]
	if !ok {
		return ""
	}
	switch v := issue.(type) {
	case IssueResponse:
		return v.ID
	case *IssueResponse:
		if v != nil {
			return v.ID
		}
	case map[string]any:
		if id, ok := v["id"].(string); ok {
			return id
		}
	}
	return ""
}

// mirrorIssueStatusToZoho is the testable core of the outbound listener. It
// re-reads the issue (authoritative status + metadata), bails when the issue is
// not Zoho-linked, resolves the project's matching custom-status id, and pushes
// it to the Zoho task. Re-reading rather than trusting the event payload keeps
// the listener robust to the two payload shapes and to stale data.
func (h *Handler) mirrorIssueStatusToZoho(ctx context.Context, issueID string) error {
	if !zohoPushStatus() {
		return nil
	}
	issueUUID, err := util.ParseUUID(issueID)
	if err != nil {
		return fmt.Errorf("parse issue id: %w", err)
	}
	issue, err := h.Queries.GetIssue(ctx, issueUUID)
	if err != nil {
		return fmt.Errorf("get issue: %w", err)
	}

	taskID := metaString(issue.Metadata, zohoTaskIDMetaKey)
	zohoProjectID := metaString(issue.Metadata, zohoProjectIDMetaKey)
	if taskID == "" || zohoProjectID == "" {
		// Not a Zoho-originated issue (or imported before Phase 2 stamped the
		// project id and not yet re-synced) — nothing to mirror.
		return nil
	}

	st := h.newZohoSyncState()
	portalID, err := h.resolveZohoPortalID(ctx, st)
	if err != nil {
		return fmt.Errorf("resolve zoho portal: %w", err)
	}

	statuses, err := st.client.ListTaskCustomStatuses(ctx, portalID, zohoProjectID)
	if err != nil {
		return fmt.Errorf("list custom statuses: %w", err)
	}
	customStatusID, ok := zohoprojects.ResolveCustomStatusID(statuses, issue.Status)
	if !ok {
		slog.Info("zoho outbound: no matching custom status, skipping push",
			"issue_id", issueID, "status", issue.Status, "zoho_project_id", zohoProjectID,
			"hint", zohoprojects.ZohoStatusNameFromIssue(issue.Status))
		return nil
	}
	if err := st.client.UpdateTaskStatus(ctx, portalID, zohoProjectID, taskID, customStatusID); err != nil {
		return fmt.Errorf("update zoho task status: %w", err)
	}
	slog.Info("zoho outbound: pushed status to zoho task",
		"issue_id", issueID, "task_id", taskID, "status", issue.Status, "custom_status_id", customStatusID)
	return nil
}

// --- periodic modified-since poller (Phase 2) -------------------------------

// RunZohoSyncPoller periodically re-syncs every Zoho-imported project with a
// modified-since cursor, so changes made in Zoho after the initial import flow
// into Agora without a manual /sync. No-op unless the integration is configured
// and ZOHO_PROJECTS_SYNC_INTERVAL is a positive duration (default 15m; "0"
// disables). Bound to ctx; returns on cancellation. Mirrors the telegram login
// poller's lifecycle (wired from cmd/server/main.go onto sweepCtx).
func (h *Handler) RunZohoSyncPoller(ctx context.Context) {
	if !zohoConfigured() {
		return
	}
	interval := zohoSyncInterval()
	if interval <= 0 {
		slog.Info("zoho sync poller disabled (ZOHO_PROJECTS_SYNC_INTERVAL<=0)")
		return
	}
	slog.Info("zoho sync poller started", "interval", interval.String())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.runZohoIncrementalSweep(ctx)
		}
	}
}

// zohoSweepTarget is one (workspace, Zoho project id) pair the poller re-syncs.
type zohoSweepTarget struct {
	workspaceID   pgtype.UUID
	zohoProjectID string
}

// runZohoIncrementalSweep finds every Agora project carrying a Zoho marker
// (across all workspaces) and incrementally re-syncs each. Per-project failures
// are logged, never fatal, so one bad portal call doesn't stop the sweep. A
// fresh per-project sync state is used so a workspace's owner/sprint caches don't
// leak across projects.
func (h *Handler) runZohoIncrementalSweep(ctx context.Context) {
	rows, err := h.DB.Query(ctx,
		`SELECT workspace_id, description FROM project
		  WHERE description LIKE '%' || $1 || '%'`,
		zohoProjectMarkerPrefix)
	if err != nil {
		slog.Warn("zoho sync poller: list imported projects failed", "error", err)
		return
	}
	var targets []zohoSweepTarget
	for rows.Next() {
		var ws pgtype.UUID
		var desc pgtype.Text
		if err := rows.Scan(&ws, &desc); err != nil {
			slog.Warn("zoho sync poller: scan project failed", "error", err)
			continue
		}
		zid := extractZohoProjectID(desc.String)
		if zid == "" {
			continue
		}
		targets = append(targets, zohoSweepTarget{workspaceID: ws, zohoProjectID: zid})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		slog.Warn("zoho sync poller: iterate projects failed", "error", err)
	}
	if len(targets) == 0 {
		return
	}

	synced := 0
	for _, t := range targets {
		if ctx.Err() != nil {
			return
		}
		st := h.newZohoSyncState()
		st.incremental = true
		tctx, cancel := context.WithTimeout(ctx, zohoSyncTimeout)
		err := h.syncZohoProject(tctx, t.workspaceID, t.zohoProjectID, st)
		cancel()
		if err != nil {
			slog.Warn("zoho sync poller: project sync failed",
				"zoho_project_id", t.zohoProjectID,
				"workspace_id", util.UUIDToString(t.workspaceID), "error", err)
			continue
		}
		synced++
	}
	slog.Info("zoho sync poller: sweep finished", "projects", synced)
}

// zohoUnavailable writes the standard 400 used when the integration env is
// unset, so every Zoho endpoint returns an identical, clear error.
func zohoUnavailable(w http.ResponseWriter) {
	writeError(w, http.StatusBadRequest, "Zoho Projects not configured")
}
