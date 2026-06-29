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
	"net/url"
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

// bitrixCommentsImportedMetaKey marks an issue whose Bitrix comments have been
// mirrored at least once (legacy/first-sync flag, kept for back-compat).
const bitrixCommentsImportedMetaKey = "bitrix_comments_imported"

// bitrixSyncedCommentIDsKey / bitrixSyncedFileIDsKey hold the arrays of Bitrix
// comment / file ids already mirrored onto an issue, so each sync (webhook
// ONTASKUPDATE / ONTASKCOMMENTADD or a periodic poll) imports only the NEW ones
// — keeping the issue's discussion + files LIVE as the dev team keeps working
// the task in Bitrix, instead of a one-shot import frozen at creation time.
const (
	bitrixSyncedCommentIDsKey = "bitrix_synced_comment_ids"
	bitrixSyncedFileIDsKey    = "bitrix_synced_file_ids"
)

// bitrixFilesImportedMetaKey marks an issue whose Bitrix attachments (and any
// extracted video frames) have already been imported, so a re-sync doesn't
// re-download + re-upload them.
const bitrixFilesImportedMetaKey = "bitrix_files_imported"

// QA verdict labels the agora-sddev-qa skill attaches to an issue after a run.
const (
	qaPassLabel = "qa:pass"
	qaFailLabel = "qa:fail"
)

// bitrixQAMirroredMetaKey records the last QA verdict mirrored to the Bitrix
// task ("pass"/"fail"), so a later label edit doesn't re-post the same verdict.
const bitrixQAMirroredMetaKey = "bitrix_qa_mirrored"

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

// bitrixWebhookPublicURL is the PUBLIC base URL at which Bitrix can reach this
// backend's /bitrix/webhook (e.g. https://api.example.com). Required to register
// event handlers on the portal — Bitrix calls out to it, so localhost won't do.
func bitrixWebhookPublicURL() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("BITRIX_WEBHOOK_PUBLIC_URL")), "/")
}

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
	// sprintCache maps "<workspaceID>:<groupID>" -> resolved sprint id, so a
	// batch creates/looks-up each sprint-named group's Agora sprint exactly once
	// (the sprint lives under the sd-main project, keyed off the same group id).
	sprintCache map[string]pgtype.UUID
	// sdMainProjectID memoizes the "sd-main" project id once per run (nil until
	// first resolved) so a batch doesn't re-query it per task. The inner pointer
	// being nil while the outer field is set is impossible here — we only assign
	// a resolved id; a missing sd-main leaves the field nil so it is re-attempted.
	sdMainProjectID *pgtype.UUID
	// groupNames maps Bitrix group id -> name, lazily filled from GetGroup so a
	// batch doesn't re-query the same workgroup name.
	groupNames map[string]string
	// stagesByGroup maps a Bitrix group id -> (stage id -> stage name), lazily
	// filled from task.stages.get so a batch resolves each kanban's stages once.
	// A nil inner map caches a failed/absent lookup so it isn't retried per task.
	stagesByGroup map[string]map[string]string
	// projectByTitle maps "<workspaceID>:<title>" -> project id (zero/Invalid
	// when no such project), so title-prefix routing resolves each named product
	// project (sd-main / sd-cs / sd-billing) once per batch.
	projectByTitle map[string]pgtype.UUID
	// routing maps "<workspaceID>" -> the project-routing config loaded from
	// workspace.settings (title-prefix rules + default project). A present key
	// means "loaded" (even when both fields are empty); absent triggers a load.
	// Splits one combined Bitrix workgroup across the workspace's product
	// projects instead of auto-creating a project per group.
	routing map[string]bitrixRoutingConfig
	// userCache maps Bitrix user id -> portal user (the task responsible),
	// lazily filled from user.get so a batch resolves each assignee once. A nil
	// value caches a failed/unknown lookup so it isn't retried per task.
	userCache map[string]*bitrix.User

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
		projectCache:   map[string]pgtype.UUID{},
		sprintCache:    map[string]pgtype.UUID{},
		groupNames:     map[string]string{},
		stagesByGroup:  map[string]map[string]string{},
		userCache:      map[string]*bitrix.User{},
		projectByTitle: map[string]pgtype.UUID{},
		routing:        map[string]bitrixRoutingConfig{},
		importContent:  true,
	}
}

// syncBitrixTask pulls a Bitrix task and reconciles it into the routed Agora
// workspace. It is a no-op (nil error) when the task is filtered out by
// BITRIX_TASK_TAG, or when routing resolves to no workspace. This is the
// single-task entry point (webhook); it allocates a fresh per-call state.
func (h *Handler) syncBitrixTask(ctx context.Context, taskID string, cfg bitrix.RouteConfig) error {
	return h.syncBitrixTaskWithState(ctx, taskID, cfg, h.newBitrixSyncState())
}

// bitrixPollBatchSize bounds how many tracked tasks a single poll tick re-syncs,
// so the safety-net poll never floods the Bitrix REST API.
const bitrixPollBatchSize = 200

// PollBitrixActiveTasks re-syncs the Bitrix tasks behind ACTIVE tracked issues
// (status not done/cancelled), stalest-first and bounded — the periodic
// "always sync" safety net that keeps status, stage, comments and attachments
// fresh even when a webhook was missed or never registered. No-op when Bitrix
// is not configured. A single shared sync state resolves each workgroup once.
func (h *Handler) PollBitrixActiveTasks(ctx context.Context) {
	cfg := bitrixRouteConfig()
	if !bitrixInboundEnabled(cfg) {
		return
	}
	rows, err := h.DB.Query(ctx,
		`SELECT metadata->>'bitrix_task_id'
		   FROM issue
		  WHERE metadata ? 'bitrix_task_id'
		    AND status NOT IN ('done', 'cancelled')
		  ORDER BY updated_at ASC
		  LIMIT $1`, bitrixPollBatchSize)
	if err != nil {
		slog.Warn("bitrix poll: list tracked tasks failed", "error", err)
		return
	}
	defer rows.Close()
	var taskIDs []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil && strings.TrimSpace(id) != "" {
			taskIDs = append(taskIDs, strings.TrimSpace(id))
		}
	}
	if len(taskIDs) == 0 {
		return
	}
	st := h.newBitrixSyncState()
	synced := 0
	for _, tid := range taskIDs {
		if ctx.Err() != nil {
			return
		}
		if err := h.syncBitrixTaskWithState(ctx, tid, cfg, st); err != nil {
			slog.Debug("bitrix poll: sync task failed", "task_id", tid, "error", err)
			continue
		}
		synced++
	}
	slog.Info("bitrix poll: tick complete", "candidates", len(taskIDs), "synced", synced)
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

	// Status: prefer the live kanban STAGE (Стадия — the column the dev team
	// drags the task through) over the coarse Bitrix STATUS, with the workspace's
	// optional stage→status override applied. Falls back to STATUS when the task
	// has no resolvable stage.
	stageName := h.bitrixStageName(ctx, st, task.GroupID, task.StageID)
	mappedStatus := resolveBitrixIssueStatus(stageName, task.Status, h.bitrixRoutingForWorkspace(ctx, ws.ID, st).StageMap)

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
		responsible := h.bitrixResponsible(ctx, st, task.ResponsibleID)
		assigneeType, assigneeID := h.bitrixResolveOrProvisionAssignee(ctx, ws.ID, task.ResponsibleID, responsible, st)
		if !sameIssueAssignee(existing, assigneeType, assigneeID) {
			if err := h.bitrixSetIssueAssignee(ctx, existing.ID, ws.ID, assigneeType, assigneeID); err != nil {
				return fmt.Errorf("update issue assignee: %w", err)
			}
			slog.Info("bitrix sync: updated issue assignee in place",
				"issue_id", util.UUIDToString(existing.ID),
				"task_id", task.ID, "responsible_id", task.ResponsibleID)
		}
		// Always refresh the responsible's display metadata (name/email/position)
		// so the assignee is visible even when they aren't an Agora member, and so
		// older issues synced before this existed get backfilled on re-import.
		h.setBitrixIssueMetadata(ctx, existing.ID, ws.ID, task.ResponsibleID, responsible, stageName, task.ID)

		// LIVE content sync: on every update, mirror any Bitrix comments +
		// attachments ADDED since the last sync (importBitrix* dedups by item id),
		// so an issue's discussion and files stay current while the dev team keeps
		// working the task in Bitrix — not frozen at import time.
		if st.importContent {
			if ownerID, oerr := h.bitrixWorkspaceOwner(ctx, ws.ID); oerr == nil {
				h.importBitrixComments(ctx, ws.ID, existing.ID, ownerID, task.ID, task.ChatID, st)
				h.importBitrixAttachments(ctx, ws.ID, existing.ID, ownerID, task.ID, st)
			}
		}

		// Resolve the task's group to its Agora target (project + optional sprint).
		// For a group already mapped to a project this returns that project with no
		// sprint (case (1) of resolveBitrixTarget), so re-syncing an issue that
		// predates sprint-mapping never spuriously mints a sprint — the new-syncs-
		// only rule holds on the update path too.
		targetProject, targetSprint := h.resolveBitrixTarget(ctx, ws.ID, task, st)

		// Backfill the project for issues created before the group→project mapping
		// existed (the create path sets ProjectID; older synced issues have none).
		// Only fill when missing — never reassign a project a user may have moved.
		// Raw, bus-free update — no EventIssueUpdated publish, to avoid an echo.
		if !existing.ProjectID.Valid && targetProject.Valid {
			if _, err := h.DB.Exec(ctx,
				`UPDATE issue SET project_id = $3, updated_at = now()
				   WHERE id = $1 AND workspace_id = $2`,
				existing.ID, ws.ID, targetProject); err != nil {
				slog.Warn("bitrix sync: backfill project failed",
					"issue_id", util.UUIDToString(existing.ID), "task_id", task.ID, "error", err)
			} else {
				slog.Info("bitrix sync: backfilled project on existing issue",
					"issue_id", util.UUIDToString(existing.ID),
					"task_id", task.ID, "project_id", util.UUIDToString(targetProject))
			}
		}
		// Link the issue to its Bitrix-derived sprint on re-sync too, so issues
		// synced before sprint-mapping existed get backfilled. SetIssueSprint is an
		// idempotent upsert keyed on issue_id, so a no-op when already linked.
		// Best-effort: a failure is logged, never fatal.
		if targetSprint.Valid {
			if err := h.Queries.SetIssueSprint(ctx, db.SetIssueSprintParams{
				IssueID:  existing.ID,
				SprintID: targetSprint,
			}); err != nil {
				slog.Warn("bitrix sync: link issue to sprint failed",
					"issue_id", util.UUIDToString(existing.ID),
					"task_id", task.ID, "sprint_id", util.UUIDToString(targetSprint), "error", err)
			}
		}
		// Resolve inline [DISK FILE ID=N] images in the description on re-sync too,
		// so issues imported before inline-image embedding get fixed. Idempotent:
		// a no-op once the refs are resolved.
		if oid, oerr := h.bitrixWorkspaceOwner(ctx, ws.ID); oerr == nil {
			h.embedInlineDiskImages(ctx, ws.ID, existing.ID, oid, st)
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

	// Resolve the task's Bitrix workgroup to an Agora target: a project, and
	// (for a sprint-named group not already mapped to a project) a sprint under
	// sd-main. A failure here is non-fatal: the issue is still created, just
	// unfiled, so a missing "sonet" scope or a transient error never blocks task
	// import.
	projectID, sprintID := h.resolveBitrixTarget(ctx, ws.ID, task, st)

	draft := bitrix.MapTaskToIssue(task)

	responsible := h.bitrixResponsible(ctx, st, task.ResponsibleID)
	assigneeType, assigneeID := h.bitrixResolveOrProvisionAssignee(ctx, ws.ID, task.ResponsibleID, responsible, st)

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

	// Link the issue to its Bitrix-derived sprint (sprint-named group under
	// sd-main). Best-effort: a failure leaves the issue filed in sd-main but
	// outside the sprint, which is recoverable on re-sync, so never fatal.
	if sprintID.Valid {
		if err := h.Queries.SetIssueSprint(ctx, db.SetIssueSprintParams{
			IssueID:  res.Issue.ID,
			SprintID: sprintID,
		}); err != nil {
			slog.Warn("bitrix sync: link issue to sprint failed",
				"issue_id", util.UUIDToString(res.Issue.ID),
				"task_id", task.ID, "sprint_id", util.UUIDToString(sprintID), "error", err)
		}
	}

	// Record the Bitrix responsible (name/email/position) so the assignee shows
	// in the issue's Metadata panel even when they aren't an Agora member.
	h.setBitrixIssueMetadata(ctx, res.Issue.ID, ws.ID, task.ResponsibleID, responsible, stageName, task.ID)

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
		h.importBitrixComments(ctx, ws.ID, res.Issue.ID, ownerID, task.ID, task.ChatID, st)
		h.embedInlineDiskImages(ctx, ws.ID, res.Issue.ID, ownerID, st)
		h.importBitrixAttachments(ctx, ws.ID, res.Issue.ID, ownerID, task.ID, st)
	}
	return nil
}

// resolveBitrixProject maps a task's Bitrix GROUP_ID to the Agora project id its
// issue should live in, creating the project on first sight. Returns an invalid
// (unset) UUID when the task has no group or project resolution fails — the
// caller then creates an unfiled issue rather than failing the whole sync. It is
// now a thin wrapper over resolveBitrixTarget that drops the sprint half, kept so
// any caller wanting only the project keeps a single-return helper.
func (h *Handler) resolveBitrixProject(ctx context.Context, wsID pgtype.UUID, task *bitrix.Task, st *bitrixSyncState) pgtype.UUID {
	projectID, _ := h.resolveBitrixTarget(ctx, wsID, task, st)
	return projectID
}

// bitrixGroupIsSprint reports whether a Bitrix workgroup name denotes a sprint
// (e.g. "Sprint 7", "Iyun Sprint", "Спринт 12"). Case-insensitive, and matches
// either the Latin "sprint" or the Cyrillic "спринт" anywhere in the name. An
// empty name is not a sprint.
func bitrixGroupIsSprint(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	return strings.Contains(n, "sprint") || strings.Contains(n, "спринт")
}

// resolveBitrixTarget maps a task's Bitrix GROUP_ID to where its Agora issue
// should live: a project, and optionally a sprint under sd-main. Order matters
// and encodes the "new syncs only" rule for sprints:
//
//  1. If the group ALREADY has an Agora project (marker lookup hits), the issue
//     stays in that project — no sprint. This is what keeps groups synced before
//     sprint-mapping existed as projects.
//  2. Else if the group's name denotes a sprint AND an "sd-main" project exists,
//     the issue goes into sd-main and is linked to the group's sprint (created on
//     first sight).
//  3. Else the group becomes its own project (the original behavior), no sprint.
//
// A failure in any resolution degrades to an unfiled issue (zero project) rather
// than failing the sync — matching resolveBitrixProject. The returned sprintID is
// the zero UUID (Valid=false) unless case (2) applies.
func (h *Handler) resolveBitrixTarget(ctx context.Context, wsID pgtype.UUID, task *bitrix.Task, st *bitrixSyncState) (projectID pgtype.UUID, sprintID pgtype.UUID) {
	// (0) Named-project routing. When the workspace configures any title-prefix
	// rule or a default project, the importer routes EVERY task to a named
	// product project (sd-main / sd-cs / sd-billing) and NEVER auto-creates a
	// project per Bitrix group: a matched prefix wins, otherwise the default
	// project catches it. This is what splits one combined workgroup ("Sprint 9")
	// across the real projects instead of dumping it into an auto-made group
	// project. Only an unconfigured workspace falls through to the legacy path.
	if cfg := h.bitrixRoutingForWorkspace(ctx, wsID, st); cfg.configured() {
		if projTitle := matchBitrixPrefixRule(task.Title, cfg.Prefixes); projTitle != "" {
			if pid, ok := h.resolveProjectByTitle(ctx, wsID, projTitle, st); ok {
				return pid, pgtype.UUID{}
			}
			slog.Warn("bitrix sync: title prefix matched a project that does not exist, trying default",
				"prefix_project", projTitle, "task_id", task.ID, "workspace_id", util.UUIDToString(wsID))
		}
		if def := strings.TrimSpace(cfg.Default); def != "" {
			if pid, ok := h.resolveProjectByTitle(ctx, wsID, def, st); ok {
				return pid, pgtype.UUID{}
			}
			slog.Warn("bitrix sync: default project does not exist, leaving task unfiled",
				"default_project", def, "task_id", task.ID, "workspace_id", util.UUIDToString(wsID))
		}
		// Configured but nothing resolved (default missing / prefix-only with no
		// match) → leave unfiled rather than auto-creating a group project, which
		// is exactly the behavior the named-routing mode exists to avoid.
		return pgtype.UUID{}, pgtype.UUID{}
	}

	groupID := strings.TrimSpace(task.GroupID)
	if groupID == "" {
		return pgtype.UUID{}, pgtype.UUID{}
	}

	// (1) Existing project for this group wins — found first, so groups synced
	// before sprint-mapping stay projects.
	if pid, ok := h.findBitrixProjectForGroup(ctx, wsID, groupID, st); ok {
		return pid, pgtype.UUID{}
	}

	name := strings.TrimSpace(task.GroupName)
	if name == "" {
		name = h.bitrixGroupName(ctx, groupID, st)
	}

	// (2) Sprint-named group with an sd-main project to host it → sprint under
	// sd-main.
	if bitrixGroupIsSprint(name) {
		if sdMain, ok := h.resolveSdMainProject(ctx, wsID, st); ok {
			sid, err := h.getOrCreateBitrixSprint(ctx, wsID, sdMain, groupID, name, st)
			if err != nil {
				slog.Warn("bitrix sync: could not resolve sprint for group, filing in sd-main without sprint",
					"group_id", groupID, "workspace_id", util.UUIDToString(wsID), "error", err)
				return sdMain, pgtype.UUID{}
			}
			return sdMain, sid
		}
	}

	// (3) Fall back to the original group-as-project behavior.
	pid, err := h.getOrCreateBitrixProject(ctx, wsID, groupID, name, st)
	if err != nil {
		slog.Warn("bitrix sync: could not resolve project for group, leaving issue unfiled",
			"group_id", groupID, "workspace_id", util.UUIDToString(wsID), "error", err)
		return pgtype.UUID{}, pgtype.UUID{}
	}
	return pid, pgtype.UUID{}
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

// bitrixStageName resolves (and caches per group) a Bitrix STAGE_ID to its
// kanban stage name via task.stages.get. Returns "" when the task is not on a
// kanban, the lookup fails, or the id isn't in the group's stage set — the
// caller then falls back to the coarse STATUS mapping.
func (h *Handler) bitrixStageName(ctx context.Context, st *bitrixSyncState, groupID, stageID string) string {
	groupID = strings.TrimSpace(groupID)
	stageID = strings.TrimSpace(stageID)
	if groupID == "" || stageID == "" {
		return ""
	}
	stages, ok := st.stagesByGroup[groupID]
	if !ok {
		fetched, err := st.client.GetTaskStages(ctx, groupID)
		if err != nil {
			slog.Debug("bitrix sync: task.stages.get failed", "group_id", groupID, "error", err)
			st.stagesByGroup[groupID] = nil // cache the miss
			return ""
		}
		stages = fetched
		st.stagesByGroup[groupID] = stages
	}
	return stages[stageID]
}

// resolveBitrixIssueStatus maps a Bitrix task to a Agora status, preferring the
// live kanban STAGE (the column the dev team drags it through) over the coarse
// STATUS. A workspace may override the stage→status mapping in
// settings.bitrix_stage_map (exact stage name, case-insensitive); otherwise the
// keyword default (bitrix.MapStage) applies. Falls back to MapStatus when the
// task has no resolvable stage.
func resolveBitrixIssueStatus(stageName, bitrixStatus string, stageMap map[string]string) string {
	if name := strings.TrimSpace(stageName); name != "" {
		if stageMap != nil {
			if mapped, ok := stageMap[strings.ToLower(name)]; ok && strings.TrimSpace(mapped) != "" {
				return mapped
			}
		}
		if mapped := bitrix.MapStage(name); mapped != "" {
			return mapped
		}
	}
	return bitrix.MapStatus(bitrixStatus)
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
func (h *Handler) bitrixResolveAssignee(ctx context.Context, wsID pgtype.UUID, responsibleID string, u *bitrix.User) (pgtype.Text, pgtype.UUID) {
	none := pgtype.Text{}
	// (1) Explicit Bitrix→Agora external-identity link.
	if id := strings.TrimSpace(responsibleID); id != "" {
		userID, err := h.userIDByExternalIdentity(ctx, providerBitrix, id)
		if err != nil {
			slog.Warn("bitrix sync: external identity lookup failed", "responsible_id", id, "error", err)
		} else if t, uid := h.assigneeIfMember(ctx, wsID, userID); t.Valid {
			return t, uid
		}
	}
	// (2) Email match — most SD staff aren't explicitly linked but share a
	// salesdoc.io email between Bitrix and Agora.
	if u != nil {
		if email := strings.TrimSpace(u.Email); email != "" {
			if agoraUser, err := h.Queries.GetUserByEmail(ctx, email); err == nil {
				if t, uid := h.assigneeIfMember(ctx, wsID, util.UUIDToString(agoraUser.ID)); t.Valid {
					return t, uid
				}
			}
		}
	}
	return none, pgtype.UUID{}
}

// bitrixResolveOrProvisionAssignee resolves the Bitrix responsible to an Agora
// member assignee, and — when the workspace enables it
// (settings.bitrix_provision_assignees) — PROVISIONS one if none exists yet:
// creates a shadow Agora user + workspace member for the responsible so the
// imported task gets a real assignee instead of only a metadata chip. Provision
// is best-effort; on any failure it degrades to the unassigned pair (the
// responsible name still lands in metadata via setBitrixResponsibleMetadata).
func (h *Handler) bitrixResolveOrProvisionAssignee(ctx context.Context, wsID pgtype.UUID, responsibleID string, u *bitrix.User, st *bitrixSyncState) (pgtype.Text, pgtype.UUID) {
	if t, uid := h.bitrixResolveAssignee(ctx, wsID, responsibleID, u); t.Valid {
		return t, uid
	}
	if h.bitrixRoutingForWorkspace(ctx, wsID, st).ProvisionAssignees {
		return h.provisionBitrixAssignee(ctx, wsID, responsibleID, u)
	}
	return pgtype.Text{}, pgtype.UUID{}
}

// provisionBitrixAssignee creates (or reuses) an Agora user + workspace member
// for a Bitrix responsible, then links the Bitrix identity so later syncs
// resolve directly. Idempotent: reuses an existing user-by-email and an existing
// membership. Returns the unset pair when the responsible has no email (Agora
// users are keyed by email, so a shadow account can't be made without one) or on
// any error — the caller then leaves the issue unassigned.
func (h *Handler) provisionBitrixAssignee(ctx context.Context, wsID pgtype.UUID, responsibleID string, u *bitrix.User) (pgtype.Text, pgtype.UUID) {
	none := pgtype.Text{}
	if u == nil {
		return none, pgtype.UUID{}
	}
	email := strings.TrimSpace(u.Email)
	if email == "" {
		return none, pgtype.UUID{}
	}

	// Find or create the Agora user by email.
	var userID pgtype.UUID
	if existing, err := h.Queries.GetUserByEmail(ctx, email); err == nil {
		userID = existing.ID
	} else {
		name := strings.TrimSpace(u.FullName())
		if name == "" {
			name = email
		}
		created, cerr := h.Queries.CreateUser(ctx, db.CreateUserParams{
			Name:  name,
			Email: email,
		})
		if cerr != nil {
			slog.Warn("bitrix sync: provision assignee user failed", "email", email, "error", cerr)
			return none, pgtype.UUID{}
		}
		userID = created.ID
		slog.Info("bitrix sync: provisioned Agora user for Bitrix responsible",
			"email", email, "user_id", util.UUIDToString(userID), "responsible_id", strings.TrimSpace(responsibleID))
	}

	// Ensure workspace membership (skip when already a member).
	if _, err := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID:      userID,
		WorkspaceID: wsID,
	}); err != nil {
		if _, cerr := h.Queries.CreateMember(ctx, db.CreateMemberParams{
			WorkspaceID: wsID,
			UserID:      userID,
			Role:        "member",
		}); cerr != nil {
			slog.Warn("bitrix sync: provision assignee member failed",
				"user_id", util.UUIDToString(userID), "workspace_id", util.UUIDToString(wsID), "error", cerr)
			return none, pgtype.UUID{}
		}
	}

	// Link the Bitrix identity so future syncs resolve directly (and dedup).
	if id := strings.TrimSpace(responsibleID); id != "" {
		if err := h.linkExternalIdentity(ctx, providerBitrix, id, util.UUIDToString(userID)); err != nil {
			slog.Debug("bitrix sync: link bitrix identity for provisioned user (non-fatal)",
				"responsible_id", id, "error", err)
		}
	}

	return pgtype.Text{String: "member", Valid: true}, userID
}

// assigneeIfMember returns ("member", uuid) when userID is a member of wsID,
// else an unset pair. Shared by the external-identity and email assignee paths;
// a linked user in another workspace must never become the assignee here.
func (h *Handler) assigneeIfMember(ctx context.Context, wsID pgtype.UUID, userID string) (pgtype.Text, pgtype.UUID) {
	none := pgtype.Text{}
	if strings.TrimSpace(userID) == "" {
		return none, pgtype.UUID{}
	}
	userUUID, err := util.ParseUUID(userID)
	if err != nil {
		return none, pgtype.UUID{}
	}
	if _, err := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID:      userUUID,
		WorkspaceID: wsID,
	}); err != nil {
		return none, pgtype.UUID{}
	}
	return pgtype.Text{String: "member", Valid: true}, userUUID
}

// bitrixResponsible resolves a Bitrix RESPONSIBLE_ID to its portal user via a
// per-sync cache (user.get). Returns nil when the id is empty or the lookup
// fails — callers treat nil as "no assignee info". The nil result is cached so
// a failed/unknown id isn't re-queried for every task in the batch.
func (h *Handler) bitrixResponsible(ctx context.Context, st *bitrixSyncState, responsibleID string) *bitrix.User {
	id := strings.TrimSpace(responsibleID)
	if id == "" {
		return nil
	}
	if u, ok := st.userCache[id]; ok {
		return u
	}
	u, err := st.client.GetUser(ctx, id)
	if err != nil || strings.TrimSpace(u.ID) == "" {
		if err != nil {
			slog.Warn("bitrix sync: user.get failed", "responsible_id", id, "error", err)
		}
		st.userCache[id] = nil
		return nil
	}
	cached := u
	st.userCache[id] = &cached
	return &cached
}

// bitrixTaskURL builds the deep link back to a task in the Bitrix portal, e.g.
// https://salesdoc.bitrix24.kz/company/personal/user/0/tasks/task/view/53733/.
// The portal origin is parsed from BITRIX_WEBHOOK_URL; returns "" when that is
// unset or unparseable, so the caller simply skips the link metadata.
func bitrixTaskURL(taskID string) string {
	id := strings.TrimSpace(taskID)
	if id == "" {
		return ""
	}
	raw := bitrixWebhookURL()
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host + "/company/personal/user/0/tasks/task/view/" + id + "/"
}

// setBitrixIssueMetadata records the Bitrix provenance on the issue metadata:
// the responsible person (id/name/email/position — so the assignee is visible
// even when they're not an Agora member), the live kanban STAGE name, and a
// deep link back to the original Bitrix task. Best-effort per key; runs on
// create + re-sync.
func (h *Handler) setBitrixIssueMetadata(ctx context.Context, issueID, wsID pgtype.UUID, responsibleID string, u *bitrix.User, stageName, taskID string) {
	kv := [][2]string{{"bitrix_responsible_id", strings.TrimSpace(responsibleID)}}
	if u != nil {
		kv = append(kv,
			[2]string{"bitrix_responsible_name", u.FullName()},
			[2]string{"bitrix_responsible_email", strings.TrimSpace(u.Email)},
			[2]string{"bitrix_responsible_position", strings.TrimSpace(u.Position)},
		)
	}
	if s := strings.TrimSpace(stageName); s != "" {
		kv = append(kv, [2]string{"bitrix_stage", s})
	}
	if link := bitrixTaskURL(taskID); link != "" {
		kv = append(kv, [2]string{"bitrix_task_url", link})
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
			slog.Warn("bitrix sync: set responsible metadata failed",
				"issue_id", util.UUIDToString(issueID), "key", p[0], "error", err)
		}
	}
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

	// QA verdict mirror: when an issue gains a qa:pass / qa:fail label, post a
	// courtesy verdict comment to the linked Bitrix task. The payload carries the
	// full label set (no delta), so the handler re-reads + dedups.
	h.Bus.Subscribe(protocol.EventIssueLabelsChanged, func(e events.Event) {
		m, ok := e.Payload.(map[string]any)
		if !ok {
			return
		}
		issueID, _ := m["issue_id"].(string)
		if issueID == "" {
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), bitrixOutboundTimeout)
			defer cancel()
			if err := h.mirrorQAVerdictToBitrix(ctx, issueID); err != nil {
				slog.Warn("bitrix outbound: qa verdict mirror failed", "issue_id", issueID, "error", err)
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

// mirrorQAVerdictToBitrix posts a courtesy QA verdict comment to the linked
// Bitrix task when an issue carries a qa:pass / qa:fail label. It re-reads the
// issue's labels (authoritative, not the event payload), bails when the issue is
// not Bitrix-linked or has no verdict label, and dedups on the
// bitrix_qa_mirrored metadata key so repeated label edits don't re-post.
func (h *Handler) mirrorQAVerdictToBitrix(ctx context.Context, issueID string) error {
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
		return nil // not a Bitrix-originated issue
	}

	verdict := qaVerdictFromLabels(h.issueLabelNames(ctx, issue.ID))
	if verdict == "" {
		return nil // no QA verdict label present
	}
	if metaString(issue.Metadata, bitrixQAMirroredMetaKey) == verdict {
		return nil // already mirrored this verdict
	}

	prefix := h.getIssuePrefix(ctx, issue.WorkspaceID)
	line := fmt.Sprintf("🤖 Agora: QA passed ✅ for issue %s-%d", prefix, issue.Number)
	if verdict == "fail" {
		line = fmt.Sprintf("🤖 Agora: QA failed ❌ for issue %s-%d", prefix, issue.Number)
	}
	if err := bitrix.NewClient(bitrixWebhookURL()).AddTaskComment(ctx, taskID, line); err != nil {
		return fmt.Errorf("add qa verdict comment: %w", err)
	}

	val, _ := json.Marshal(verdict)
	if _, err := h.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
		ID:          issue.ID,
		WorkspaceID: issue.WorkspaceID,
		Key:         bitrixQAMirroredMetaKey,
		Value:       val,
	}); err != nil {
		slog.Warn("bitrix outbound: stamp qa verdict failed", "issue_id", issueID, "error", err)
	}
	return nil
}

// qaVerdictFromLabels reduces a label set to a QA verdict: "fail" wins over
// "pass" (a fail means do not ship); "" when neither label is present.
func qaVerdictFromLabels(names []string) string {
	pass := false
	for _, n := range names {
		switch strings.ToLower(strings.TrimSpace(n)) {
		case qaFailLabel:
			return "fail"
		case qaPassLabel:
			pass = true
		}
	}
	if pass {
		return "pass"
	}
	return ""
}

// issueLabelNames returns the names of the labels attached to an issue. Raw pgx
// (no new sqlc method) keeps the change surgical; errors degrade to nil.
func (h *Handler) issueLabelNames(ctx context.Context, issueID pgtype.UUID) []string {
	rows, err := h.DB.Query(ctx,
		`SELECT l.name FROM issue_label l
		   JOIN issue_to_label il ON il.label_id = l.id
		  WHERE il.issue_id = $1`, issueID)
	if err != nil {
		slog.Warn("bitrix outbound: list issue labels failed", "error", err)
		return nil
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err == nil {
			names = append(names, n)
		}
	}
	return names
}

// metaString reads a string-valued key from an issue's JSONB metadata, "" when
// absent or non-string.
func metaString(raw []byte, key string) string {
	if v, ok := parseIssueMetadata(raw)[key].(string); ok {
		return v
	}
	return ""
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
