package handler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/integrations/bitrix"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Bitrix24 task sync. Bitrix is the task master: an inbound webhook on
// ONTASKADD / ONTASKUPDATE pulls the task, and (if it is tagged "ai") creates
// or updates a Agora issue in the routed workspace. A complementary outbound
// listener mirrors Agora issue status changes back to Bitrix as a courtesy
// comment (always) and, when BITRIX_PUSH_STATUS is truthy, a real status
// update.
//
// The metadata key linking an issue to its Bitrix origin. Dedup keys on the
// task id (not the title) so re-firing ONTASKUPDATE updates the same issue
// instead of spawning duplicates.
const bitrixTaskIDMetaKey = "bitrix_task_id"

// bitrixCommentsImportedMetaKey marks an issue whose Bitrix comments have
// already been mirrored, so a re-sync (ONTASKUPDATE) doesn't duplicate them.
// Comments are imported once, on first issue creation.
const bitrixCommentsImportedMetaKey = "bitrix_comments_imported"

// bitrixFilesImportedMetaKey marks an issue whose Bitrix attachments (and any
// extracted video frames) have already been imported, so a re-sync doesn't
// re-download + re-upload them.
const bitrixFilesImportedMetaKey = "bitrix_files_imported"

// bitrixProjectMarkerPrefix is the durable marker embedded in a project's
// description to link it back to its Bitrix workgroup id, used for dedup when
// the same group is synced again. Format: "bitrix_group:<id>". A deterministic
// marker (rather than a new column) keeps the change surgical + upstream-mergeable.
const bitrixProjectMarkerPrefix = "bitrix_group:"

// bitrixSyncTimeout bounds a single inbound sync so a slow Bitrix portal can't
// hold a goroutine open indefinitely.
const bitrixSyncTimeout = 20 * time.Second

// bitrixOutboundTimeout bounds an outbound mirror call. Kept tight (~10s) so
// the HTTP request path that published the issue:updated event is never
// blocked by a Bitrix round-trip — the listener runs in its own goroutine.
const bitrixOutboundTimeout = 10 * time.Second

// --- env config -------------------------------------------------------------

// bitrixWebhookURL is the portal inbound-webhook REST base URL. When empty the
// integration is disabled (inbound guard short-circuits, outbound listener
// no-ops).
func bitrixWebhookURL() string { return strings.TrimSpace(os.Getenv("BITRIX_WEBHOOK_URL")) }

// bitrixInboundSecret is an optional shared secret. When set, the inbound
// webhook requires a matching ?secret= or X-Bitrix-Secret header.
func bitrixInboundSecret() string { return strings.TrimSpace(os.Getenv("BITRIX_INBOUND_SECRET")) }

// bitrixTaskTag is the OPTIONAL tag filter for imports. Empty (the default)
// means import ALL tasks; when set, only tasks carrying this tag
// (case-insensitive) are synced. Replaces the old hard "ai"-only gate so the
// integration mirrors a whole Bitrix workgroup, not just AI-flagged tasks.
func bitrixTaskTag() string { return strings.TrimSpace(os.Getenv("BITRIX_TASK_TAG")) }

// bitrixTaskHasTag reports whether the task carries the given tag
// (case-insensitive, whitespace-trimmed). An empty tag always matches (the
// "import everything" default).
func bitrixTaskHasTag(task *bitrix.Task, tag string) bool {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == "" {
		return true
	}
	if task == nil {
		return false
	}
	for _, t := range task.Tags {
		if strings.EqualFold(strings.TrimSpace(t), tag) {
			return true
		}
	}
	return false
}

// bitrixPushStatus reports whether outbound mirroring should also push a real
// status update (tasks.task.update) on top of the courtesy comment.
func bitrixPushStatus() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("BITRIX_PUSH_STATUS"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// bitrixRouteConfig builds the routing config from env:
//
//   - BITRIX_SYNC_WORKSPACE_SLUG : default (catch-all) workspace slug.
//   - BITRIX_GROUP_MAP           : "123:sd-main,456:sd-cs" CSV of GROUP_ID:slug
//     (malformed pairs dropped).
//   - BITRIX_WORKSPACE_SLUGS     : "sd-main,sd-cs" slugs a task may name via a
//     tag to force a workspace.
//
// The final TagSlugs set is the union of BITRIX_WORKSPACE_SLUGS, every mapped
// GroupMap value, and the default slug — so any workspace reachable through
// routing can also be targeted directly by a tag.
func bitrixRouteConfig() bitrix.RouteConfig {
	defaultSlug := strings.TrimSpace(os.Getenv("BITRIX_SYNC_WORKSPACE_SLUG"))

	groupMap := map[string]string{}
	for _, pair := range strings.Split(os.Getenv("BITRIX_GROUP_MAP"), ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		gid, slug, ok := strings.Cut(pair, ":")
		gid = strings.TrimSpace(gid)
		slug = strings.TrimSpace(slug)
		if !ok || gid == "" || slug == "" {
			// Malformed pair — drop it rather than minting a bogus route.
			continue
		}
		groupMap[gid] = slug
	}

	tagSlugs := map[string]bool{}
	addSlug := func(s string) {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			tagSlugs[s] = true
		}
	}
	for _, s := range strings.Split(os.Getenv("BITRIX_WORKSPACE_SLUGS"), ",") {
		addSlug(s)
	}
	for _, slug := range groupMap {
		addSlug(slug)
	}
	addSlug(defaultSlug)

	return bitrix.RouteConfig{
		DefaultSlug: defaultSlug,
		GroupMap:    groupMap,
		TagSlugs:    tagSlugs,
	}
}

// bitrixInboundEnabled reports whether the inbound webhook has anything to do:
// a webhook URL plus at least one routing target (a default slug or a non-empty
// group map). Without a URL there's nothing to fetch; without a route there's
// nowhere to put the issue.
func bitrixInboundEnabled(cfg bitrix.RouteConfig) bool {
	if bitrixWebhookURL() == "" {
		return false
	}
	return cfg.DefaultSlug != "" || len(cfg.GroupMap) > 0
}

// --- inbound webhook --------------------------------------------------------

// BitrixWebhookRateLimit is chi middleware that throttles the public
// /bitrix/webhook endpoint per client IP using the shared
// h.WebhookIPRateLimiter (the same limiter autopilot/stripe webhooks use).
// Because the endpoint contractually ALWAYS responds 200 (so a misconfigured
// Bitrix never retry-storms), a throttled request is dropped with a bare 200
// and no work — denying the timing/brute-force and amplification surface
// without handing an attacker a distinguishable 429. No-op when the limiter is
// unset.
func (h *Handler) BitrixWebhookRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.WebhookIPRateLimiter != nil {
			if ip := h.clientIPForRateLimit(r); ip != "" {
				if !h.WebhookIPRateLimiter.Allow(r.Context(), ip) {
					slog.Warn("bitrix webhook: rate limited", "ip", ip)
					w.WriteHeader(http.StatusOK)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// BitrixWebhook is the public POST /bitrix/webhook endpoint. Bitrix outbound
// webhooks expect a fast 2xx; if they get errors or timeouts they retry
// aggressively, so this handler ALWAYS responds 200 and logs internal failures
// rather than surfacing them. The actual sync runs synchronously within a
// bounded context (the handler already returns 200 before any heavy DB work
// only if we choose to; here we run inline with a timeout so test assertions
// are deterministic, and the work is cheap).
func (h *Handler) BitrixWebhook(w http.ResponseWriter, r *http.Request) {
	// Optional shared-secret gate. Checked first so an unauthenticated caller
	// can't even trigger a Bitrix fetch. Skipped entirely when unset.
	if secret := bitrixInboundSecret(); secret != "" {
		provided := r.URL.Query().Get("secret")
		if provided == "" {
			provided = r.Header.Get("X-Bitrix-Secret")
		}
		// Constant-time compare so a timing side-channel can't be used to
		// recover the secret (matches auth_telegram.go's webhook check).
		if subtle.ConstantTimeCompare([]byte(provided), []byte(secret)) != 1 {
			// Still 200 so a misconfigured Bitrix doesn't retry-storm, but do
			// no work.
			slog.Warn("bitrix webhook: secret mismatch")
			w.WriteHeader(http.StatusOK)
			return
		}
	}

	cfg := bitrixRouteConfig()
	if !bitrixInboundEnabled(cfg) {
		slog.Debug("bitrix webhook: integration disabled (no URL or no route), ignoring")
		w.WriteHeader(http.StatusOK)
		return
	}

	if err := r.ParseForm(); err != nil {
		slog.Warn("bitrix webhook: failed to parse form", "error", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	event, taskID, ok := bitrix.ParseWebhookEvent(r.Form)
	if !ok {
		slog.Debug("bitrix webhook: unhandled or incomplete event", "event", event)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Run the sync against a bounded context detached from the request (Bitrix
	// has already gotten its response by the time slow work matters; we don't
	// want a client disconnect cancelling the sync mid-flight).
	ctx, cancel := context.WithTimeout(context.Background(), bitrixSyncTimeout)
	defer cancel()
	if err := h.syncBitrixTask(ctx, taskID, cfg); err != nil {
		slog.Error("bitrix webhook: sync failed", "event", event, "task_id", taskID, "error", err)
	}

	w.WriteHeader(http.StatusOK)
}

// bitrixSyncState carries per-run caches and a tally shared across a batch of
// syncBitrixTask calls (the /api/bitrix/import endpoint syncs many tasks in one
// request). Keeping the group→project and group-id→name maps here means a 200-
// task import resolves each workgroup once instead of per task. The webhook path
// allocates a fresh single-use state via syncBitrixTask.
type bitrixSyncState struct {
	client *bitrix.Client
	tag    string // BITRIX_TASK_TAG snapshot ("" = import all)

	// projectCache maps "<workspaceID>:<groupID>" -> resolved project id, so a
	// batch creates/looks-up each group's project exactly once.
	projectCache map[string]pgtype.UUID
	// groupNames maps Bitrix group id -> name, lazily filled from GetGroup so a
	// batch doesn't re-query the same workgroup name.
	groupNames map[string]string

	// importContent toggles comment + attachment (and video-frame) import. The
	// webhook + import paths both enable it; it exists so a future caller can
	// sync metadata-only.
	importContent bool

	// Tally for the import endpoint.
	created int
	updated int
	skipped int
}

func (h *Handler) newBitrixSyncState() *bitrixSyncState {
	return &bitrixSyncState{
		client:        bitrix.NewClient(bitrixWebhookURL()),
		tag:           bitrixTaskTag(),
		projectCache:  map[string]pgtype.UUID{},
		groupNames:    map[string]string{},
		importContent: true,
	}
}

// syncBitrixTask pulls a Bitrix task and reconciles it into the routed Agora
// workspace. It is a no-op (nil error) when the task is filtered out by
// BITRIX_TASK_TAG, or when routing resolves to no workspace. This is the
// single-task entry point (webhook); it allocates a fresh per-call state.
func (h *Handler) syncBitrixTask(ctx context.Context, taskID string, cfg bitrix.RouteConfig) error {
	return h.syncBitrixTaskWithState(ctx, taskID, cfg, h.newBitrixSyncState())
}

// syncBitrixTaskWithState is the batched core of the inbound sync. It reuses the
// caches + tally on st so a multi-task import resolves each workgroup once.
func (h *Handler) syncBitrixTaskWithState(ctx context.Context, taskID string, cfg bitrix.RouteConfig, st *bitrixSyncState) error {
	task, err := st.client.GetTask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("get bitrix task %s: %w", taskID, err)
	}

	// Optional tag filter (default empty = import ALL tasks). Only skip when a
	// tag is configured AND the task lacks it — routing still decides the
	// workspace below, so tasks in unmapped groups are dropped there.
	if !bitrixTaskHasTag(task, st.tag) {
		slog.Debug("bitrix sync: task lacks configured tag, skipping",
			"task_id", task.ID, "tag", st.tag)
		st.skipped++
		return nil
	}

	slug := bitrix.ResolveWorkspaceSlug(task, cfg)
	if slug == "" {
		slog.Warn("bitrix sync: no workspace resolved for task, skipping",
			"task_id", task.ID, "group_id", task.GroupID, "tags", task.Tags)
		st.skipped++
		return nil
	}

	ws, err := h.Queries.GetWorkspaceBySlug(ctx, slug)
	if err != nil {
		return fmt.Errorf("workspace by slug %q: %w", slug, err)
	}

	// Serialize the find/create/stamp sequence per (workspace, task) with a
	// Postgres transaction-scoped advisory lock so two interleaved syncs of the
	// SAME task (e.g. ONTASKADD racing ONTASKUPDATE) can't both pass the dedup
	// lookup and double-create the issue. We hold the lock on a dedicated
	// connection via a short transaction for the whole critical section, then
	// commit to release it: the second sync blocks on pg_advisory_xact_lock
	// until the first commits, by which point the first sync's issue + metadata
	// (written on pooled connections that auto-commit before this lock-tx
	// commits) are durable, so the second sees the stamp and takes the update
	// path. The lock key is hashtext("<workspaceID>:bitrix:<taskID>") (hashtext
	// returns int4, a valid advisory-lock key). A transaction-scoped lock is
	// used (not a session lock) because h.DB is a pgxpool — a session-level
	// pg_advisory_lock/unlock could land on different pooled connections and
	// leak the lock; pg_advisory_xact_lock auto-releases with the tx on its own
	// connection. NOTE: single-DB-cluster safe — advisory locks are
	// per-Postgres-cluster, matching our single-primary deployment.
	lockKey := util.UUIDToString(ws.ID) + ":bitrix:" + task.ID
	lockTx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin sync lock tx: %w", err)
	}
	// Rollback is a no-op once we've committed; it guarantees the lock is freed
	// on any early-return error path.
	defer func() { _ = lockTx.Rollback(ctx) }()
	if _, err := lockTx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, lockKey); err != nil {
		return fmt.Errorf("acquire sync lock: %w", err)
	}
	releaseLock := func() {
		// Commit releases the xact-scoped advisory lock. Done explicitly after
		// the critical section so the next sync only proceeds once our writes
		// are durable. A commit failure still leaves the deferred Rollback to
		// free the lock.
		if cerr := lockTx.Commit(ctx); cerr != nil {
			slog.Warn("bitrix sync: lock tx commit failed", "task_id", task.ID, "error", cerr)
		}
	}
	defer releaseLock()

	// Dedup on the bitrix_task_id metadata key. Reuse the same JSONB @> filter
	// path ListIssues exposes, but scoped to this workspace via raw pgx so we
	// don't depend on a sqlc method for the new metadata shape.
	existing, found, err := h.findIssueByBitrixTaskID(ctx, ws.ID, task.ID)
	if err != nil {
		return fmt.Errorf("dedup lookup: %w", err)
	}

	mappedStatus := bitrix.MapStatus(task.Status)

	if found {
		// Already synced. Reconcile status AND assignee, doing both RAW (no bus
		// publish) so we don't trigger our own outbound listener and echo the
		// change straight back to Bitrix.
		if existing.Status != mappedStatus {
			if _, err := h.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
				ID:          existing.ID,
				Status:      mappedStatus,
				WorkspaceID: ws.ID,
			}); err != nil {
				return fmt.Errorf("update issue status: %w", err)
			}
			slog.Info("bitrix sync: updated issue status in place",
				"issue_id", util.UUIDToString(existing.ID),
				"task_id", task.ID, "status", mappedStatus)
		}

		// Re-resolve the assignee from RESPONSIBLE_ID; an ONTASKUPDATE may have
		// reassigned the Bitrix task. Apply only when it actually differs, via a
		// RAW bus-free update (no EventIssueUpdated publish, to avoid an
		// outbound echo).
		assigneeType, assigneeID := h.bitrixResolveAssignee(ctx, ws.ID, task.ResponsibleID)
		if !sameIssueAssignee(existing, assigneeType, assigneeID) {
			if err := h.bitrixSetIssueAssignee(ctx, existing.ID, ws.ID, assigneeType, assigneeID); err != nil {
				return fmt.Errorf("update issue assignee: %w", err)
			}
			slog.Info("bitrix sync: updated issue assignee in place",
				"issue_id", util.UUIDToString(existing.ID),
				"task_id", task.ID, "responsible_id", task.ResponsibleID)
		}

		// Backfill the project for issues created before the group→project mapping
		// existed (the create path sets ProjectID; older synced issues have none).
		// Raw, bus-free update — no EventIssueUpdated publish, to avoid an echo.
		if !existing.ProjectID.Valid {
			if pid := h.resolveBitrixProject(ctx, ws.ID, task, st); pid.Valid {
				if _, err := h.DB.Exec(ctx,
					`UPDATE issue SET project_id = $3, updated_at = now()
					   WHERE id = $1 AND workspace_id = $2`,
					existing.ID, ws.ID, pid); err != nil {
					slog.Warn("bitrix sync: backfill project failed",
						"issue_id", util.UUIDToString(existing.ID), "task_id", task.ID, "error", err)
				} else {
					slog.Info("bitrix sync: backfilled project on existing issue",
						"issue_id", util.UUIDToString(existing.ID),
						"task_id", task.ID, "project_id", util.UUIDToString(pid))
				}
			}
		}
		st.updated++
		return nil
	}

	// New issue. Creator is the workspace owner (the integration is a system
	// actor with no member of its own). Assignee is resolved from the Bitrix
	// RESPONSIBLE_ID via the external-identity map, but only set when that user
	// is actually a member of this workspace.
	ownerID, err := h.bitrixWorkspaceOwner(ctx, ws.ID)
	if err != nil {
		return fmt.Errorf("resolve workspace owner: %w", err)
	}

	// Resolve the task's Bitrix workgroup to an Agora project in this workspace
	// (creating it on first sight). A failure here is non-fatal: the issue is
	// still created, just unfiled, so a missing "sonet" scope or a transient
	// error never blocks task import.
	projectID := h.resolveBitrixProject(ctx, ws.ID, task, st)

	draft := bitrix.MapTaskToIssue(task)

	assigneeType, assigneeID := h.bitrixResolveAssignee(ctx, ws.ID, task.ResponsibleID)

	res, err := h.IssueService.Create(ctx, service.IssueCreateParams{
		WorkspaceID:  ws.ID,
		Title:        draft.Title,
		Description:  strToText(draft.Description),
		Status:       draft.Status,
		Priority:     "none",
		AssigneeType: assigneeType,
		AssigneeID:   assigneeID,
		CreatorType:  "member",
		CreatorID:    ownerID,
		ProjectID:    projectID,
		// Dedup is on the task id (set as metadata post-create), not the
		// title, so allow titles that collide with existing issues.
		AllowDuplicate: true,
	}, service.IssueCreateOpts{
		ActorID: util.UUIDToString(ownerID),
	})
	if err != nil {
		return fmt.Errorf("create issue: %w", err)
	}

	// Stamp the link so the next ONTASKUPDATE dedups onto this issue. JSON-encode
	// the id as a string value to match the metadata KV contract (primitive
	// scalar).
	idValue, err := json.Marshal(task.ID)
	if err != nil {
		return fmt.Errorf("encode task id: %w", err)
	}
	if _, err := h.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
		ID:          res.Issue.ID,
		WorkspaceID: ws.ID,
		Key:         bitrixTaskIDMetaKey,
		Value:       idValue,
	}); err != nil {
		return fmt.Errorf("set bitrix_task_id metadata: %w", err)
	}

	slog.Info("bitrix sync: created issue from task",
		"issue_id", util.UUIDToString(res.Issue.ID),
		"task_id", task.ID, "workspace", slug, "status", draft.Status,
		"project_id", util.UUIDToString(projectID))
	st.created++

	// Enrich the new issue with the task's comments and attachments. Best-effort
	// and bounded; failures are logged, never fatal — the issue already exists.
	// Only on first create (the dedup branch above returns before reaching here),
	// so a re-sync doesn't duplicate comments/files.
	if st.importContent {
		h.importBitrixComments(ctx, ws.ID, res.Issue.ID, ownerID, task.ID, st)
		h.importBitrixAttachments(ctx, ws.ID, res.Issue.ID, ownerID, task.ID, st)
	}
	return nil
}

// resolveBitrixProject maps a task's Bitrix GROUP_ID to an Agora project id in
// the workspace, creating the project on first sight. Returns an invalid
// (unset) UUID when the task has no group or project resolution fails — the
// caller then creates an unfiled issue rather than failing the whole sync.
func (h *Handler) resolveBitrixProject(ctx context.Context, wsID pgtype.UUID, task *bitrix.Task, st *bitrixSyncState) pgtype.UUID {
	groupID := strings.TrimSpace(task.GroupID)
	if groupID == "" {
		return pgtype.UUID{}
	}
	name := strings.TrimSpace(task.GroupName)
	if name == "" {
		name = h.bitrixGroupName(ctx, groupID, st)
	}
	projectID, err := h.getOrCreateBitrixProject(ctx, wsID, groupID, name, st)
	if err != nil {
		slog.Warn("bitrix sync: could not resolve project for group, leaving issue unfiled",
			"group_id", groupID, "workspace_id", util.UUIDToString(wsID), "error", err)
		return pgtype.UUID{}
	}
	return projectID
}

// bitrixGroupName resolves (and caches) a Bitrix group id to its display name
// via sonet_group.get. On any failure it returns "" so the caller falls back to
// a generated placeholder name.
func (h *Handler) bitrixGroupName(ctx context.Context, groupID string, st *bitrixSyncState) string {
	if name, ok := st.groupNames[groupID]; ok {
		return name
	}
	g, err := st.client.GetGroup(ctx, groupID)
	name := ""
	if err != nil {
		slog.Debug("bitrix sync: group name lookup failed", "group_id", groupID, "error", err)
	} else {
		name = strings.TrimSpace(g.Name)
	}
	st.groupNames[groupID] = name
	return name
}

// findIssueByBitrixTaskID returns the issue in the workspace whose metadata
// matches {"bitrix_task_id": <taskID>}. Uses the same JSONB @> containment the
// ListIssues metadata filter uses. found=false (nil error) means no match.
func (h *Handler) findIssueByBitrixTaskID(ctx context.Context, wsID pgtype.UUID, taskID string) (db.Issue, bool, error) {
	filter, err := json.Marshal(map[string]string{bitrixTaskIDMetaKey: taskID})
	if err != nil {
		return db.Issue{}, false, err
	}
	// LIMIT 1 — at most one issue should carry a given task id, but cap defensively.
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

// sameIssueAssignee reports whether the issue's current (assignee_type,
// assignee_id) already equals the resolved pair, so we skip a no-op write.
func sameIssueAssignee(issue db.Issue, assigneeType pgtype.Text, assigneeID pgtype.UUID) bool {
	if issue.AssigneeType.Valid != assigneeType.Valid || issue.AssigneeType.String != assigneeType.String {
		return false
	}
	if issue.AssigneeID.Valid != assigneeID.Valid {
		return false
	}
	if !issue.AssigneeID.Valid {
		return true
	}
	return issue.AssigneeID.Bytes == assigneeID.Bytes
}

// bitrixSetIssueAssignee applies an assignee change with a RAW pgx UPDATE — no
// EventIssueUpdated publish — so the inbound reconcile doesn't re-trigger the
// outbound mirror and echo back to Bitrix. A null assigneeType/assigneeID pair
// clears the assignment.
func (h *Handler) bitrixSetIssueAssignee(ctx context.Context, issueID, wsID pgtype.UUID, assigneeType pgtype.Text, assigneeID pgtype.UUID) error {
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

// bitrixWorkspaceOwner returns the user_id of the first member with role
// 'owner' in the workspace (ordered by creation, matching ListMembers).
func (h *Handler) bitrixWorkspaceOwner(ctx context.Context, wsID pgtype.UUID) (pgtype.UUID, error) {
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

// bitrixResolveAssignee maps a Bitrix RESPONSIBLE_ID to a Agora member
// assignee, returning the (type, id) pair to set on the issue. It returns an
// unset (invalid) pair — leaving the issue unassigned — when the responsible
// id is empty, not linked to any Agora user, or linked to a user who is not
// a member of this workspace.
func (h *Handler) bitrixResolveAssignee(ctx context.Context, wsID pgtype.UUID, responsibleID string) (pgtype.Text, pgtype.UUID) {
	none := pgtype.Text{}
	if strings.TrimSpace(responsibleID) == "" {
		return none, pgtype.UUID{}
	}
	userID, err := h.userIDByExternalIdentity(ctx, providerBitrix, responsibleID)
	if err != nil {
		slog.Warn("bitrix sync: external identity lookup failed", "responsible_id", responsibleID, "error", err)
		return none, pgtype.UUID{}
	}
	if userID == "" {
		return none, pgtype.UUID{}
	}
	userUUID, err := util.ParseUUID(userID)
	if err != nil {
		return none, pgtype.UUID{}
	}
	// Confirm membership before assigning — a linked user in another workspace
	// must not become the assignee here.
	if _, err := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID:      userUUID,
		WorkspaceID: wsID,
	}); err != nil {
		slog.Debug("bitrix sync: responsible user not a member of workspace, leaving unassigned",
			"user_id", userID, "workspace_id", util.UUIDToString(wsID))
		return none, pgtype.UUID{}
	}
	return pgtype.Text{String: "member", Valid: true}, userUUID
}

// --- outbound mirror --------------------------------------------------------

// registerBitrixOutbound subscribes to issue:updated and mirrors status changes
// of Bitrix-linked issues back to the portal. No-op entirely when
// BITRIX_WEBHOOK_URL is unset, so self-hosted deployments without Bitrix pay
// nothing. Wired from handler.New() before the final return.
func (h *Handler) registerBitrixOutbound() {
	if bitrixWebhookURL() == "" {
		return
	}
	if h.Bus == nil {
		return
	}
	h.Bus.Subscribe(protocol.EventIssueUpdated, func(e events.Event) {
		if !bitrixShouldMirror(e.Payload) {
			return
		}
		issueID := bitrixIssueIDFromPayload(e.Payload)
		if issueID == "" {
			return
		}
		// Run detached + bounded so the publishing HTTP path is never blocked.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), bitrixOutboundTimeout)
			defer cancel()
			if err := h.mirrorIssueStatusToBitrix(ctx, issueID); err != nil {
				slog.Warn("bitrix outbound: mirror failed", "issue_id", issueID, "error", err)
			}
		}()
	})
}

// bitrixShouldMirror decides whether an issue:updated payload represents a real
// status change worth mirroring. It proceeds ONLY when the status_changed key
// is present AND true: an ABSENT key is treated as "no status change" (a
// title-only edit that never stamped the flag) and a present-and-false flag is
// an explicit "status unchanged". Both non-changes are skipped so we don't spam
// Bitrix with courtesy comments. A non-map payload is also skipped.
func bitrixShouldMirror(payload any) bool {
	m, ok := payload.(map[string]any)
	if !ok {
		return false
	}
	changed, ok := m["status_changed"].(bool)
	return ok && changed
}

// bitrixIssueIDFromPayload digs the issue id out of an issue:updated payload.
// The payload's "issue" value may be an IssueResponse struct (handler path) or
// a map[string]any (service path); handle both.
func bitrixIssueIDFromPayload(payload any) string {
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

// mirrorIssueStatusToBitrix is the testable core of the outbound listener. It
// re-reads the issue (authoritative status + metadata), bails when the issue is
// not Bitrix-linked, posts a courtesy comment, and optionally pushes a real
// status update. Re-reading rather than trusting the event payload keeps the
// listener robust to the two payload shapes and to stale data.
func (h *Handler) mirrorIssueStatusToBitrix(ctx context.Context, issueID string) error {
	issueUUID, err := util.ParseUUID(issueID)
	if err != nil {
		return fmt.Errorf("parse issue id: %w", err)
	}
	issue, err := h.Queries.GetIssue(ctx, issueUUID)
	if err != nil {
		return fmt.Errorf("get issue: %w", err)
	}

	taskID := bitrixTaskIDFromMetadata(issue.Metadata)
	if taskID == "" {
		// Not a Bitrix-originated issue — nothing to mirror.
		return nil
	}

	client := bitrix.NewClient(bitrixWebhookURL())

	// Spec format: "🤖 Agora: issue <PREFIX>-<n> → <Label>" — the
	// workspace-prefixed identifier (PROJ-12) the rest of the product uses, a
	// bot emoji, and a Unicode arrow.
	prefix := h.getIssuePrefix(ctx, issue.WorkspaceID)
	comment := fmt.Sprintf("🤖 Agora: issue %s-%d → %s", prefix, issue.Number, bitrixStatusLabel(issue.Status))
	if err := client.AddTaskComment(ctx, taskID, comment); err != nil {
		return fmt.Errorf("add task comment: %w", err)
	}

	if bitrixPushStatus() {
		if err := client.UpdateTaskStatus(ctx, taskID, bitrix.BitrixStatusFromIssue(issue.Status)); err != nil {
			return fmt.Errorf("update task status: %w", err)
		}
	}
	return nil
}

// bitrixTaskIDFromMetadata extracts the bitrix_task_id value from an issue's
// JSONB metadata, returning "" when absent. The value is normalized to a string
// whether it was stored as a JSON string or number.
func bitrixTaskIDFromMetadata(raw []byte) string {
	meta := parseIssueMetadata(raw)
	v, ok := meta[bitrixTaskIDMetaKey]
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		// JSON numbers decode to float64. Render integral values as a plain
		// integer (strconv, not %v) so large/round ids like 900000000000 don't
		// collapse to scientific notation ("9e+11"). Non-integral values keep a
		// faithful decimal representation.
		if t == math.Trunc(t) && !math.IsInf(t, 0) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case json.Number:
		return t.String()
	default:
		return ""
	}
}

// bitrixStatusLabel turns a Agora issue status into a human label for the
// Bitrix comment.
func bitrixStatusLabel(status string) string {
	switch status {
	case bitrix.StatusBacklog:
		return "Backlog"
	case bitrix.StatusTodo:
		return "Todo"
	case bitrix.StatusInProgress:
		return "In Progress"
	case bitrix.StatusInReview:
		return "In Review"
	case bitrix.StatusDone:
		return "Done"
	case bitrix.StatusCancelled:
		return "Cancelled"
	default:
		return status
	}
}
