package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"mime"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/integrations/bitrix"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Bitrix → Agora enrichment: a Bitrix workgroup becomes an Agora project, a
// task's comment feed becomes issue comments, and its attachments become issue
// attachments (with video recordings decomposed into still frames so the
// Planner's claim brief can "see" the bug). All of this runs in the detached
// sync goroutine the webhook already spawns; every step is bounded and
// best-effort so a slow/missing portal scope never blocks task import.

// --- group → project --------------------------------------------------------

// bitrixGroupIDFromDescription extracts the Bitrix workgroup id from a project's
// durable "bitrix_group:<id>" description marker — the inverse of the marker that
// getOrCreateBitrixProject writes. Returns "" when the project carries no marker
// (i.e. it is not Bitrix-linked). Bitrix group ids are numeric, so the id runs
// from just after the prefix up to the first non-digit.
func bitrixGroupIDFromDescription(description string) string {
	i := strings.Index(description, bitrixProjectMarkerPrefix)
	if i < 0 {
		return ""
	}
	rest := description[i+len(bitrixProjectMarkerPrefix):]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	return rest[:end]
}

// getOrCreateBitrixProject returns the Agora project id for a Bitrix workgroup
// in the given workspace, creating it on first sight. Dedup is durable: the
// project's description carries a "bitrix_group:<id>" marker, and an existing
// project is found by querying that marker (raw pgx — no new sqlc method, so the
// change stays surgical + upstream-mergeable). Resolutions are cached on st for
// the duration of a batch import.
func (h *Handler) getOrCreateBitrixProject(ctx context.Context, workspaceID pgtype.UUID, groupID, groupName string, st *bitrixSyncState) (pgtype.UUID, error) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return pgtype.UUID{}, fmt.Errorf("empty group id")
	}
	cacheKey := util.UUIDToString(workspaceID) + ":" + groupID
	if id, ok := st.projectCache[cacheKey]; ok {
		return id, nil
	}

	marker := bitrixProjectMarkerPrefix + groupID

	// Look up an existing project for this group via the durable description
	// marker. metadata-free: project has no JSONB column, so the marker lives in
	// the description text. Scoped to the workspace (tenant boundary).
	var existingID pgtype.UUID
	err := h.DB.QueryRow(ctx,
		`SELECT id FROM project
		  WHERE workspace_id = $1 AND description LIKE '%' || $2 || '%'
		  ORDER BY created_at ASC
		  LIMIT 1`,
		workspaceID, marker).Scan(&existingID)
	if err == nil {
		st.projectCache[cacheKey] = existingID
		return existingID, nil
	}
	if err != pgx.ErrNoRows {
		return pgtype.UUID{}, fmt.Errorf("lookup bitrix project: %w", err)
	}

	// Create it. Title is the group name (fall back to a stable placeholder so a
	// nameless group still files). Description carries the marker on its own line
	// so the LIKE dedup is reliable and a human reading the project sees the link.
	title := strings.TrimSpace(groupName)
	if title == "" {
		title = "Bitrix group " + groupID
	}
	description := "Imported from Bitrix workgroup.\n" + marker

	project, err := h.Queries.CreateProject(ctx, db.CreateProjectParams{
		WorkspaceID: workspaceID,
		Title:       title,
		Description: strToText(description),
		Status:      "planned", // matches CreateProject handler default
		Priority:    "none",    // matches CreateProject handler default
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("create bitrix project: %w", err)
	}
	st.projectCache[cacheKey] = project.ID
	slog.Info("bitrix sync: created project for workgroup",
		"project_id", util.UUIDToString(project.ID),
		"group_id", groupID, "title", title,
		"workspace_id", util.UUIDToString(workspaceID))
	return project.ID, nil
}

// findBitrixProjectForGroup is the LOOKUP-ONLY half of getOrCreateBitrixProject:
// it resolves an EXISTING Agora project for a Bitrix workgroup (cache, then the
// durable "bitrix_group:<id>" description marker) but never creates one. ok=false
// (with no error surfaced) means no project is mapped to this group yet — the
// caller then decides whether the group should become a sprint instead. This is
// what preserves the "new syncs only" sprint behavior: a group that already has
// a project is found here first and stays a project.
func (h *Handler) findBitrixProjectForGroup(ctx context.Context, workspaceID pgtype.UUID, groupID string, st *bitrixSyncState) (pgtype.UUID, bool) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return pgtype.UUID{}, false
	}
	cacheKey := util.UUIDToString(workspaceID) + ":" + groupID
	if id, ok := st.projectCache[cacheKey]; ok {
		return id, true
	}

	marker := bitrixProjectMarkerPrefix + groupID
	var existingID pgtype.UUID
	err := h.DB.QueryRow(ctx,
		`SELECT id FROM project
		  WHERE workspace_id = $1 AND description LIKE '%' || $2 || '%'
		  ORDER BY created_at ASC
		  LIMIT 1`,
		workspaceID, marker).Scan(&existingID)
	if err == nil {
		st.projectCache[cacheKey] = existingID
		return existingID, true
	}
	if err != pgx.ErrNoRows {
		slog.Warn("bitrix sync: lookup project for group failed",
			"group_id", groupID, "workspace_id", util.UUIDToString(workspaceID), "error", err)
	}
	return pgtype.UUID{}, false
}

// --- group → sprint (sd-main) -----------------------------------------------

// resolveSdMainProject resolves (and memoizes on st) the id of the "sd-main"
// project in the workspace — the parent under which sprint-named Bitrix groups
// become Agora sprints. ok=false (no error surfaced) when the workspace has no
// sd-main project, so the caller falls back to the group-as-project path.
func (h *Handler) resolveSdMainProject(ctx context.Context, workspaceID pgtype.UUID, st *bitrixSyncState) (pgtype.UUID, bool) {
	if st.sdMainProjectID != nil {
		return *st.sdMainProjectID, true
	}
	var id pgtype.UUID
	err := h.DB.QueryRow(ctx,
		`SELECT id FROM project WHERE workspace_id = $1 AND title = 'sd-main' LIMIT 1`,
		workspaceID).Scan(&id)
	if err != nil {
		if err != pgx.ErrNoRows {
			slog.Warn("bitrix sync: resolve sd-main project failed",
				"workspace_id", util.UUIDToString(workspaceID), "error", err)
		}
		return pgtype.UUID{}, false
	}
	memo := id
	st.sdMainProjectID = &memo
	return id, true
}

// --- title-prefix → project routing -----------------------------------------

// bitrixPrefixRule routes a Bitrix task to a specific Agora project by a
// case-insensitive prefix on the task TITLE — how a single combined Bitrix
// workgroup is split across the workspace's product projects (sd-main / sd-cs /
// sd-billing). Configured in workspace.settings.bitrix_project_prefixes as
// [{"prefix":"CRM:","project":"sd-cs"}, ...].
type bitrixPrefixRule struct {
	Prefix  string `json:"prefix"`
	Project string `json:"project"`
}

// bitrixRoutingConfig is the per-workspace project-routing config read from
// workspace.settings. Prefixes split a combined workgroup by title; Default is
// the project unmatched tasks land in (so the importer NEVER auto-creates a
// project per Bitrix group). Either may be empty — empty Default falls back to
// the legacy group-based path.
type bitrixRoutingConfig struct {
	Prefixes []bitrixPrefixRule
	Default  string
	// ProvisionAssignees, when true, makes the importer create an Agora user +
	// workspace member for a Bitrix responsible who has no Agora account yet, so
	// the imported task gets a REAL assignee (not just a metadata chip).
	ProvisionAssignees bool
	// StageMap overrides the keyword stage→status default per workspace, keyed by
	// the LOWERCASED Bitrix stage name (e.g. {"code review":"in_review"}). Empty
	// → the bitrix.MapStage keyword default applies.
	StageMap map[string]string
}

// configured reports whether the workspace opted into named-project routing
// (any prefix rule or a default project). When true, the importer routes to
// named projects only and skips the auto-create-per-group fallback.
func (c bitrixRoutingConfig) configured() bool {
	return len(c.Prefixes) > 0 || strings.TrimSpace(c.Default) != ""
}

// bitrixRoutingForWorkspace loads + caches the project-routing config from
// workspace.settings (bitrix_project_prefixes + bitrix_default_project). Prefix
// rules are sorted longest-prefix-first so the most specific prefix wins.
func (h *Handler) bitrixRoutingForWorkspace(ctx context.Context, wsID pgtype.UUID, st *bitrixSyncState) bitrixRoutingConfig {
	key := util.UUIDToString(wsID)
	if cfg, ok := st.routing[key]; ok {
		return cfg
	}
	var settings []byte
	if err := h.DB.QueryRow(ctx, `SELECT settings FROM workspace WHERE id = $1`, wsID).Scan(&settings); err != nil {
		st.routing[key] = bitrixRoutingConfig{}
		return bitrixRoutingConfig{}
	}
	var parsed struct {
		Rules     []bitrixPrefixRule `json:"bitrix_project_prefixes"`
		Default   string             `json:"bitrix_default_project"`
		Provision bool               `json:"bitrix_provision_assignees"`
		StageMap  map[string]string  `json:"bitrix_stage_map"`
	}
	if len(settings) == 0 || json.Unmarshal(settings, &parsed) != nil {
		st.routing[key] = bitrixRoutingConfig{}
		return bitrixRoutingConfig{}
	}
	var stageMap map[string]string
	if len(parsed.StageMap) > 0 {
		stageMap = make(map[string]string, len(parsed.StageMap))
		for name, status := range parsed.StageMap {
			if n := strings.TrimSpace(strings.ToLower(name)); n != "" {
				stageMap[n] = strings.TrimSpace(status)
			}
		}
	}
	rules := make([]bitrixPrefixRule, 0, len(parsed.Rules))
	for _, r := range parsed.Rules {
		prefix := strings.TrimSpace(r.Prefix)
		project := strings.TrimSpace(r.Project)
		if prefix != "" && project != "" {
			rules = append(rules, bitrixPrefixRule{Prefix: prefix, Project: project})
		}
	}
	sort.SliceStable(rules, func(i, j int) bool { return len(rules[i].Prefix) > len(rules[j].Prefix) })
	cfg := bitrixRoutingConfig{Prefixes: rules, Default: strings.TrimSpace(parsed.Default), ProvisionAssignees: parsed.Provision, StageMap: stageMap}
	st.routing[key] = cfg
	return cfg
}

// matchBitrixPrefixRule returns the project title a task title routes to by
// prefix, or "" when no rule matches. Case-insensitive; a leading "[", "#", "("
// or whitespace on the title is tolerated so "[CRM] ..." matches a "CRM" prefix.
// PURE — unit-tested without a DB.
func matchBitrixPrefixRule(title string, rules []bitrixPrefixRule) string {
	t := strings.ToLower(strings.TrimLeft(strings.TrimSpace(title), "[#( \t"))
	for _, r := range rules {
		if strings.HasPrefix(t, strings.ToLower(r.Prefix)) {
			return r.Project
		}
	}
	return ""
}

// resolveProjectByTitle resolves (and caches) a project id by its exact title in
// the workspace. ok=false (no error surfaced for a plain miss) when no such
// project exists, so the caller falls back to the group-based path.
func (h *Handler) resolveProjectByTitle(ctx context.Context, wsID pgtype.UUID, title string, st *bitrixSyncState) (pgtype.UUID, bool) {
	key := util.UUIDToString(wsID) + ":" + title
	if id, ok := st.projectByTitle[key]; ok {
		return id, id.Valid
	}
	var id pgtype.UUID
	err := h.DB.QueryRow(ctx,
		`SELECT id FROM project WHERE workspace_id = $1 AND title = $2 LIMIT 1`,
		wsID, title).Scan(&id)
	if err != nil {
		st.projectByTitle[key] = pgtype.UUID{}
		if err != pgx.ErrNoRows {
			slog.Warn("bitrix sync: resolve project by title failed",
				"title", title, "workspace_id", util.UUIDToString(wsID), "error", err)
		}
		return pgtype.UUID{}, false
	}
	st.projectByTitle[key] = id
	return id, true
}

// getOrCreateBitrixSprint returns the Agora sprint id for a sprint-named Bitrix
// workgroup, creating it under the given host project on first sight. It mirrors
// getOrCreateBitrixProject exactly, but the durable "bitrix_group:<id>" marker
// lives in the sprint's GOAL (sprint has no description column), and dedup is
// scoped to the host project. Resolutions are cached on st for the batch.
func (h *Handler) getOrCreateBitrixSprint(ctx context.Context, workspaceID, hostProjectID pgtype.UUID, groupID, groupName string, st *bitrixSyncState) (pgtype.UUID, error) {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return pgtype.UUID{}, fmt.Errorf("empty group id")
	}
	// Key by (workspace, host project, group): one Bitrix sprint-group can be
	// hosted under more than one product project in a single batch — a
	// cross-product "Sprint 12" split by title prefix into sd-main + sd-cs — and
	// each must get its OWN sprint. A ws:group key would collide and hand the
	// second product the first product's sprint.
	cacheKey := util.UUIDToString(workspaceID) + ":" + util.UUIDToString(hostProjectID) + ":" + groupID
	if id, ok := st.sprintCache[cacheKey]; ok {
		return id, nil
	}

	marker := bitrixProjectMarkerPrefix + groupID

	// Look up an existing sprint for this group via the durable goal marker,
	// scoped to the host project (the sprint's parent).
	var existingID pgtype.UUID
	err := h.DB.QueryRow(ctx,
		`SELECT id FROM sprint
		  WHERE project_id = $1 AND goal LIKE '%' || $2 || '%'
		  ORDER BY created_at ASC
		  LIMIT 1`,
		hostProjectID, marker).Scan(&existingID)
	if err == nil {
		st.sprintCache[cacheKey] = existingID
		return existingID, nil
	}
	if err != pgx.ErrNoRows {
		return pgtype.UUID{}, fmt.Errorf("lookup bitrix sprint: %w", err)
	}

	// Create it. Name is the group name (fall back to a stable placeholder so a
	// nameless group still files). Goal carries the marker so the LIKE dedup is
	// reliable. Status "active" (a live sprint); no start/end dates.
	name := strings.TrimSpace(groupName)
	if name == "" {
		name = "Bitrix group " + groupID
	}

	sprint, err := h.Queries.CreateSprint(ctx, db.CreateSprintParams{
		WorkspaceID: workspaceID,
		ProjectID:   hostProjectID,
		Name:        name,
		Goal:        marker,
		Status:      "active",
		StartDate:   pgtype.Timestamptz{},
		EndDate:     pgtype.Timestamptz{},
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("create bitrix sprint: %w", err)
	}
	st.sprintCache[cacheKey] = sprint.ID
	slog.Info("bitrix sync: created sprint for workgroup",
		"sprint_id", util.UUIDToString(sprint.ID),
		"group_id", groupID, "name", name,
		"project_id", util.UUIDToString(hostProjectID),
		"workspace_id", util.UUIDToString(workspaceID))
	return sprint.ID, nil
}

// --- comments ---------------------------------------------------------------

// importBitrixComments mirrors a task's Bitrix comment feed onto the freshly
// created issue as issue comments, once. Each Bitrix comment becomes one
// member-authored issue comment (author = workspace owner, since the
// integration has no member of its own) with a clear provenance header.
// Bounded by the client (maxCommentsPerTask) and idempotent via the
// bitrix_comments_imported metadata flag. All failures are logged, never fatal.
func (h *Handler) importBitrixComments(ctx context.Context, wsID, issueID, ownerID pgtype.UUID, taskID, chatID string, st *bitrixSyncState) {
	comments, err := st.client.GetTaskComments(ctx, taskID)
	if err != nil {
		slog.Warn("bitrix sync: fetch comments failed", "task_id", taskID, "error", err)
		comments = nil // chat below may still have the discussion
	}
	// Newer Bitrix tasks keep their discussion in the task CHAT, not the legacy
	// commentitem feed (which returns 0 for them). Pull the chat messages too and
	// merge — the incremental dedup below keys on the (namespaced) message id, so
	// commentitem + chat never double-import.
	if chatID != "" {
		if chatMsgs, cerr := st.client.GetTaskChatMessages(ctx, chatID); cerr != nil {
			slog.Debug("bitrix sync: fetch chat messages failed",
				"task_id", taskID, "chat_id", chatID, "error", cerr)
		} else if len(chatMsgs) > 0 {
			comments = append(comments, chatMsgs...)
		}
	}
	if len(comments) == 0 {
		return
	}

	// Incremental: import only comments not already mirrored (dedup by Bitrix
	// comment id), so a re-sync picks up the discussion ADDED in Bitrix since the
	// last sync instead of either duplicating the feed or skipping it entirely.
	seen := h.bitrixSyncedIDSet(ctx, issueID, bitrixSyncedCommentIDsKey)
	imported := 0
	for _, c := range comments {
		cid := strings.TrimSpace(c.ID)
		if cid != "" && seen[cid] {
			continue // already synced
		}
		content := formatBitrixComment(c)
		if strings.TrimSpace(content) == "" {
			continue
		}
		// Attribute to the REAL Bitrix author when we can resolve them to an
		// Agora member; otherwise to the dedicated "Bitrix" import identity —
		// NOT the workspace owner, which mis-showed every external author as the
		// operator who ran the import (the reported bug). The real name is still
		// in the comment body's "**Bitrix — <author>**" provenance header.
		authorType, authorID := "member", ownerID
		if ref := h.bitrixCommentAuthor(ctx, wsID, c.AuthorID, st); ref.Type.Valid {
			authorType, authorID = ref.Type.String, ref.ID
		} else if bid, ok := h.ensureBitrixAuthorMember(ctx, wsID); ok {
			authorType, authorID = "member", bid
		}
		created, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
			IssueID:     issueID,
			WorkspaceID: wsID,
			AuthorType:  authorType,
			AuthorID:    authorID,
			Content:     content,
			Type:        "comment",
		})
		if err != nil {
			slog.Warn("bitrix sync: create comment failed",
				"task_id", taskID, "issue_id", util.UUIDToString(issueID), "error", err)
			continue
		}
		if cid != "" {
			// Stamp the Bitrix-origin marker so the issue-detail activity tabs can
			// cleanly separate this from in-Agora discussion (the issue-level
			// bitrix_synced_comment_ids array holds Bitrix ids, which can't be
			// matched to an Agora comment row).
			if err := h.Queries.SetCommentBitrixOrigin(ctx, db.SetCommentBitrixOriginParams{
				ID:              created.ID,
				BitrixCommentID: pgtype.Text{String: cid, Valid: true},
			}); err != nil {
				slog.Warn("bitrix sync: mark comment bitrix-origin failed",
					"comment_id", util.UUIDToString(created.ID), "error", err)
			}
			seen[cid] = true
		}
		imported++
	}

	if imported > 0 {
		h.setBitrixSyncedIDSet(ctx, wsID, issueID, bitrixSyncedCommentIDsKey, seen)
	}
	// Keep the legacy first-sync flag for back-compat (UI / older checks).
	h.setBitrixImportFlag(ctx, wsID, issueID, bitrixCommentsImportedMetaKey)
	slog.Info("bitrix sync: imported comments",
		"task_id", taskID, "issue_id", util.UUIDToString(issueID), "new", imported, "total", len(seen))
}

// bitrixAuthorUserEmail / bitrixAuthorUserName identify the single global
// "Bitrix" system user that owns import-attributed comments across workspaces.
// The .local domain is undeliverable and can never log in (email-code login
// rejects synthetic domains), so it is a pure attribution identity.
const (
	bitrixAuthorUserEmail = "bitrix-import@bitrix.local"
	bitrixAuthorUserName  = "Bitrix"
)

// ensureBitrixAuthorMember returns the id of the dedicated "Bitrix" import
// identity (a single global system user, added as a member of wsID) used to
// attribute a Bitrix comment whose real author can't be resolved to an Agora
// member. This keeps external Bitrix authors off the workspace owner — who
// merely ran the import — while the real name stays in the comment's
// "**Bitrix — <author>**" provenance header. Best-effort: ok=false on any error
// so the caller falls back to the owner rather than dropping the comment.
func (h *Handler) ensureBitrixAuthorMember(ctx context.Context, wsID pgtype.UUID) (pgtype.UUID, bool) {
	var userID pgtype.UUID
	if u, err := h.Queries.GetUserByEmail(ctx, bitrixAuthorUserEmail); err == nil {
		userID = u.ID
	} else {
		created, cerr := h.Queries.CreateUser(ctx, db.CreateUserParams{
			Name:  bitrixAuthorUserName,
			Email: bitrixAuthorUserEmail,
		})
		if cerr != nil {
			return pgtype.UUID{}, false
		}
		userID = created.ID
	}
	if _, err := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID:      userID,
		WorkspaceID: wsID,
	}); err != nil {
		if _, cerr := h.Queries.CreateMember(ctx, db.CreateMemberParams{
			WorkspaceID: wsID,
			UserID:      userID,
			Role:        "member",
		}); cerr != nil {
			return pgtype.UUID{}, false
		}
	}
	return userID, true
}

// formatBitrixComment renders a Bitrix comment as an Agora issue-comment body
// with a provenance header: "**Bitrix — <author> (<date>)**:\n<text>". A
// missing author/date degrades gracefully.
func formatBitrixComment(c bitrix.Comment) string {
	author := strings.TrimSpace(c.Author)
	if author == "" {
		author = "unknown"
	}
	header := "**Bitrix — " + author
	if d := strings.TrimSpace(c.Date); d != "" {
		header += " (" + d + ")"
	}
	header += "**:"
	return header + "\n" + bitrix.BBCodeToMarkdown(strings.TrimSpace(c.Text))
}

// --- attachments + video frames ---------------------------------------------

// maxBitrixVideoFrames caps how many extracted frames are uploaded per video.
const maxBitrixVideoFrames = bitrix.MaxVideoFrames

// bitrixFrameExtractTimeout bounds a single ffmpeg/ffprobe invocation so a
// pathological video can't wedge the sync goroutine.
const bitrixFrameExtractTimeout = 60 * time.Second

// importBitrixAttachments downloads a task's attachments and stores them as
// issue attachments, once. Videos are stored as-is (linked in the description);
// ffmpeg frame extraction is deferred to planning time so a video-heavy group
// doesn't blow the import's request budget. Bounded by the client
// (maxFilesPerTask) and idempotent via the bitrix_files_imported metadata flag.
// Requires Storage; a no-op (logged) when storage is unconfigured. All failures
// are logged, never fatal.
func (h *Handler) importBitrixAttachments(ctx context.Context, wsID, issueID, ownerID pgtype.UUID, taskID string, st *bitrixSyncState) {
	if h.Storage == nil {
		slog.Debug("bitrix sync: storage not configured, skipping attachments", "task_id", taskID)
		return
	}
	files, err := st.client.GetTaskFiles(ctx, taskID)
	if err != nil {
		slog.Warn("bitrix sync: fetch files failed", "task_id", taskID, "error", err)
		return
	}
	if len(files) == 0 {
		return
	}

	// Incremental: only download + store files not already mirrored (dedup by
	// Bitrix file id), so a re-sync pulls in files ATTACHED in Bitrix since the
	// last sync without re-downloading the whole set.
	seen := h.bitrixSyncedIDSet(ctx, issueID, bitrixSyncedFileIDsKey)
	stored := 0
	var embeds []bitrixEmbed
	for _, f := range files {
		fid := strings.TrimSpace(f.ID)
		if fid != "" && seen[fid] {
			continue // already synced
		}
		data, ctype, err := st.client.DownloadFile(ctx, f.URL)
		if err != nil {
			slog.Warn("bitrix sync: download attachment failed",
				"task_id", taskID, "name", f.Name, "error", err)
			continue
		}
		contentType := pickAttachmentContentType(ctype, f.Name)
		_, url, err := h.storeBitrixAttachment(ctx, wsID, issueID, ownerID, f.Name, contentType, data)
		if err != nil {
			slog.Warn("bitrix sync: store attachment failed",
				"task_id", taskID, "name", f.Name, "error", err)
			continue
		}
		if fid != "" {
			seen[fid] = true
		}
		stored++
		embeds = append(embeds, bitrixEmbed{url: url, name: f.Name, contentType: contentType})
		// Videos are stored as-is and surfaced as a link; frame extraction
		// (ffmpeg) is intentionally NOT done here — it is the single slowest
		// step and a video-heavy group would blow the import's request budget.
		// Frames are extracted lazily at planning time instead (the agent runs
		// ffmpeg on the stored video via extractAndStoreFrames-equivalent).
	}

	// Surface every newly stored file inline in the issue description.
	h.appendBitrixAttachmentsToDescription(ctx, wsID, issueID, embeds)

	if stored > 0 {
		h.setBitrixSyncedIDSet(ctx, wsID, issueID, bitrixSyncedFileIDsKey, seen)
	}
	h.setBitrixImportFlag(ctx, wsID, issueID, bitrixFilesImportedMetaKey)
	slog.Info("bitrix sync: imported attachments",
		"task_id", taskID, "issue_id", util.UUIDToString(issueID),
		"new", stored, "total", len(seen))
}

// storeBitrixAttachment uploads bytes to Storage and records the attachment row
// linked to the issue, mirroring the /api/upload-file path (storage.Upload +
// CreateAttachment). The uploader is the workspace owner ("member"). Returns the
// created attachment id and its public URL (for embedding in the description).
func (h *Handler) storeBitrixAttachment(ctx context.Context, wsID, issueID, ownerID pgtype.UUID, filename, contentType string, data []byte) (pgtype.UUID, string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return pgtype.UUID{}, "", fmt.Errorf("generate attachment id: %w", err)
	}
	// Same key layout as UploadFile: workspaces/<ws>/<uuid><ext>.
	key := "workspaces/" + util.UUIDToString(wsID) + "/" + id.String() + path.Ext(filename)
	link, err := h.Storage.Upload(ctx, key, data, contentType, filename)
	if err != nil {
		return pgtype.UUID{}, "", fmt.Errorf("upload: %w", err)
	}
	att, err := h.Queries.CreateAttachment(ctx, db.CreateAttachmentParams{
		ID:           pgtype.UUID{Bytes: id, Valid: true},
		WorkspaceID:  wsID,
		UploaderType: "member",
		UploaderID:   ownerID,
		Filename:     filename,
		Url:          link,
		ContentType:  contentType,
		SizeBytes:    int64(len(data)),
		IssueID:      issueID,
	})
	if err != nil {
		return pgtype.UUID{}, "", fmt.Errorf("create attachment row: %w", err)
	}
	return att.ID, link, nil
}

// bitrixEmbed is one stored attachment (file or extracted frame) to surface in
// the issue description as inline markdown.
type bitrixEmbed struct {
	url         string
	name        string
	contentType string
}

// appendBitrixAttachmentsToDescription appends a markdown block linking every
// imported attachment to the issue description, so the issue view AND the
// Planner's claim brief see the screenshots/frames inline instead of as orphan
// attachment rows. Images embed (![]); videos and other files render as links.
// Best-effort: a failure is logged, never fatal (the attachment rows already
// exist). Runs once, on first import (the same create-only path as the rows).
func (h *Handler) appendBitrixAttachmentsToDescription(ctx context.Context, wsID, issueID pgtype.UUID, embeds []bitrixEmbed) {
	block := bitrixAttachmentBlock(embeds)
	if block == "" {
		return
	}
	if _, err := h.DB.Exec(ctx,
		`UPDATE issue SET description = coalesce(description, '') || $3, updated_at = now()
		   WHERE id = $1 AND workspace_id = $2`,
		issueID, wsID, block); err != nil {
		slog.Warn("bitrix sync: append attachments to description failed",
			"issue_id", util.UUIDToString(issueID), "error", err)
	}
}

// bitrixAttachmentBlock renders the markdown block appended to an issue
// description for the imported attachments. Images embed inline (![]); videos
// and other files render as labelled links. Returns "" when there is nothing to
// embed.
func bitrixAttachmentBlock(embeds []bitrixEmbed) string {
	if len(embeds) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n---\n\n**Attachments (from Bitrix):**\n\n")
	for _, e := range embeds {
		name := sanitizeEmbedName(e.name)
		switch {
		case strings.HasPrefix(e.contentType, "image/"):
			fmt.Fprintf(&b, "![%s](%s)\n\n", name, e.url)
		case strings.HasPrefix(e.contentType, "video/"):
			fmt.Fprintf(&b, "🎬 [%s](%s)\n\n", name, e.url)
		default:
			fmt.Fprintf(&b, "📎 [%s](%s)\n\n", name, e.url)
		}
	}
	return b.String()
}

// sanitizeEmbedName makes a filename safe for a markdown link/alt label: brackets
// would terminate the [..](..) syntax and newlines would break the block.
func sanitizeEmbedName(name string) string {
	return strings.TrimSpace(strings.NewReplacer(
		"[", "(", "]", ")", "\n", " ", "\r", " ",
	).Replace(name))
}

// diskFileRefRe matches Bitrix's inline file reference in a description body:
// [DISK FILE ID=123] (the N is a disk attached-object id).
var diskFileRefRe = regexp.MustCompile(`\[DISK FILE ID=(\d+)\]`)

// maxInlineImages caps how many inline [DISK FILE ID=N] refs we resolve per task.
const maxInlineImages = 12

// embedInlineDiskImages resolves [DISK FILE ID=N] refs in the issue's
// description to the real files (disk.attachedObject.get → download → store) and
// rewrites each ref as an inline markdown image (a link for non-images), so the
// screenshots referenced in a Bitrix task body actually render. Best-effort and
// deduped; a ref that fails to resolve is left untouched. Runs once on first
// import (create path).
func (h *Handler) embedInlineDiskImages(ctx context.Context, wsID, issueID, ownerID pgtype.UUID, st *bitrixSyncState) {
	if h.Storage == nil {
		return
	}
	var desc string
	if err := h.DB.QueryRow(ctx,
		`SELECT coalesce(description, '') FROM issue WHERE id = $1 AND workspace_id = $2`,
		issueID, wsID).Scan(&desc); err != nil || !strings.Contains(desc, "[DISK FILE ID=") {
		return
	}

	repl := map[string]string{} // disk id -> markdown replacement
	for _, m := range diskFileRefRe.FindAllStringSubmatch(desc, -1) {
		id := m[1]
		if _, done := repl[id]; done {
			continue
		}
		if len(repl) >= maxInlineImages {
			break
		}
		f, err := st.client.ResolveAttachedObject(ctx, id)
		if err != nil || f.URL == "" {
			continue
		}
		data, ctype, err := st.client.DownloadFile(ctx, f.URL)
		if err != nil {
			continue
		}
		contentType := pickAttachmentContentType(ctype, f.Name)
		_, url, err := h.storeBitrixAttachment(ctx, wsID, issueID, ownerID, f.Name, contentType, data)
		if err != nil {
			continue
		}
		name := sanitizeEmbedName(f.Name)
		if strings.HasPrefix(contentType, "image/") {
			repl[id] = "\n\n![" + name + "](" + url + ")\n\n"
		} else {
			repl[id] = "[" + name + "](" + url + ")"
		}
	}
	if len(repl) == 0 {
		return
	}

	newDesc := diskFileRefRe.ReplaceAllStringFunc(desc, func(ref string) string {
		if r, ok := repl[diskFileRefRe.FindStringSubmatch(ref)[1]]; ok {
			return r
		}
		return ref
	})
	if newDesc == desc {
		return
	}
	if _, err := h.DB.Exec(ctx,
		`UPDATE issue SET description = $3, updated_at = now() WHERE id = $1 AND workspace_id = $2`,
		issueID, wsID, newDesc); err != nil {
		slog.Warn("bitrix sync: embed inline images failed",
			"issue_id", util.UUIDToString(issueID), "error", err)
	}
}

// extractAndStoreFrames writes the video bytes to a temp file, runs ffmpeg to
// pull still frames (scene detection with an interval fallback, mirroring the
// legacy bot), and uploads each frame as an image attachment on the issue.
// Returns the stored frames as embeds (for inline description embedding); its
// length is the count. ffmpeg missing / any failure logs and returns nil — never
// fatal.
func (h *Handler) extractAndStoreFrames(ctx context.Context, wsID, issueID, ownerID pgtype.UUID, filename string, data []byte, st *bitrixSyncState) []bitrixEmbed {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		slog.Warn("bitrix sync: ffmpeg not found, skipping video frames", "filename", filename)
		return nil
	}

	tmpDir, err := os.MkdirTemp("", "bitrix-frames-*")
	if err != nil {
		slog.Warn("bitrix sync: temp dir failed", "error", err)
		return nil
	}
	defer os.RemoveAll(tmpDir)

	ext := path.Ext(filename)
	if ext == "" {
		ext = ".mp4"
	}
	srcPath := filepath.Join(tmpDir, "source"+ext)
	if err := os.WriteFile(srcPath, data, 0o600); err != nil {
		slog.Warn("bitrix sync: write temp video failed", "error", err)
		return nil
	}

	framePaths := extractVideoFrames(ctx, srcPath, tmpDir)
	if len(framePaths) == 0 {
		slog.Debug("bitrix sync: no frames extracted", "filename", filename)
		return nil
	}

	base := strings.TrimSuffix(path.Base(filename), ext)
	var embeds []bitrixEmbed
	for i, fp := range framePaths {
		if len(embeds) >= maxBitrixVideoFrames {
			break
		}
		frameBytes, err := os.ReadFile(fp)
		if err != nil || len(frameBytes) == 0 {
			continue
		}
		frameName := fmt.Sprintf("%s_frame_%03d.jpg", base, i+1)
		_, url, err := h.storeBitrixAttachment(ctx, wsID, issueID, ownerID, frameName, "image/jpeg", frameBytes)
		if err != nil {
			slog.Warn("bitrix sync: store frame failed", "name", frameName, "error", err)
			continue
		}
		embeds = append(embeds, bitrixEmbed{url: url, name: frameName, contentType: "image/jpeg"})
	}
	return embeds
}

// extractVideoFrames shells out to ffmpeg to pull stills from srcPath into
// outDir, returning the sorted frame paths. It runs the scene-detection pass
// first and, when that yields too few frames for a non-trivial video, an
// interval-sampling fallback — the exact two-stage strategy of the legacy bot.
// The ffmpeg argument vectors come from the pure bitrix.*Args builders so the
// command shape is unit-testable without ffmpeg present.
func extractVideoFrames(ctx context.Context, srcPath, outDir string) []string {
	duration := probeVideoDuration(ctx, srcPath)

	// Primary: scene-change detection.
	scenePattern := filepath.Join(outDir, "scene_%03d.jpg")
	runFFmpeg(ctx, bitrix.SceneDetectArgs(srcPath, scenePattern, maxBitrixVideoFrames, bitrix.DefaultSceneThreshold))
	frames := globSorted(outDir, "scene_")

	// Fallback: interval sampling when scene detection found too few cuts.
	if bitrix.NeedsIntervalFallback(len(frames), duration) {
		n := bitrix.IntervalFrameCount(duration, maxBitrixVideoFrames)
		for i, ts := range bitrix.IntervalTimestamps(duration, n) {
			out := filepath.Join(outDir, fmt.Sprintf("interval_%03d.jpg", i))
			runFFmpeg(ctx, bitrix.IntervalFrameArgs(srcPath, out, ts))
		}
		// Re-glob both prefixes so scene + interval frames are all returned.
		frames = append(globSorted(outDir, "scene_"), globSorted(outDir, "interval_")...)
	}
	if len(frames) > maxBitrixVideoFrames {
		frames = frames[:maxBitrixVideoFrames]
	}
	return frames
}

// probeVideoDuration returns a video's duration in seconds via ffprobe, or 0 on
// any failure (ffprobe missing, unparseable output). A 0 duration disables the
// interval fallback's coverage math, which is the safe degrade.
func probeVideoDuration(ctx context.Context, srcPath string) float64 {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return 0
	}
	cctx, cancel := context.WithTimeout(ctx, bitrixFrameExtractTimeout)
	defer cancel()
	out, err := exec.CommandContext(cctx, "ffprobe", bitrix.ProbeDurationArgs(srcPath)...).Output()
	if err != nil {
		return 0
	}
	d, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0
	}
	return d
}

// runFFmpeg runs one bounded ffmpeg invocation, discarding output. Errors are
// swallowed — the caller inspects the produced files, not the exit code, and a
// partial run (some frames written) is still useful.
func runFFmpeg(ctx context.Context, args []string) {
	cctx, cancel := context.WithTimeout(ctx, bitrixFrameExtractTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "ffmpeg", args...)
	if err := cmd.Run(); err != nil {
		slog.Debug("bitrix sync: ffmpeg run returned error (continuing)", "error", err)
	}
}

// globSorted returns the lexically-sorted files in dir whose base name starts
// with prefix. ffmpeg writes zero-padded indices, so lexical order == frame
// order.
func globSorted(dir, prefix string) []string {
	matches, err := filepath.Glob(filepath.Join(dir, prefix+"*"))
	if err != nil {
		return nil
	}
	sort.Strings(matches)
	return matches
}

// --- shared helpers ---------------------------------------------------------

// pickAttachmentContentType resolves a sane content type for a downloaded
// attachment: the server-reported type wins when it is concrete (not the
// generic octet-stream), otherwise it is inferred from the filename extension,
// falling back to application/octet-stream.
func pickAttachmentContentType(reported, filename string) string {
	reported = strings.TrimSpace(reported)
	// Strip any "; charset=..." parameter.
	if i := strings.Index(reported, ";"); i >= 0 {
		reported = strings.TrimSpace(reported[:i])
	}
	if reported != "" && reported != "application/octet-stream" {
		return reported
	}
	if byExt := mime.TypeByExtension(strings.ToLower(path.Ext(filename))); byExt != "" {
		if i := strings.Index(byExt, ";"); i >= 0 {
			byExt = strings.TrimSpace(byExt[:i])
		}
		return byExt
	}
	if reported != "" {
		return reported
	}
	return "application/octet-stream"
}

// setBitrixImportFlag stamps a boolean true under the given metadata key on the
// issue, marking a one-time import (comments / files) as done. Failures are
// logged, not fatal — at worst a re-sync re-imports, which the create-only
// guard already prevents by returning before this path on the dedup branch.
func (h *Handler) setBitrixImportFlag(ctx context.Context, wsID, issueID pgtype.UUID, key string) {
	val, _ := json.Marshal(true)
	if _, err := h.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
		ID:          issueID,
		WorkspaceID: wsID,
		Key:         key,
		Value:       val,
	}); err != nil {
		slog.Debug("bitrix sync: set import flag failed", "key", key, "error", err)
	}
}

// bitrixSyncedIDSet reads the set of already-synced Bitrix item ids (comment or
// file ids) from an issue-metadata array key, so a re-sync mirrors only NEW
// items rather than re-importing the whole feed. Empty set on any miss.
func (h *Handler) bitrixSyncedIDSet(ctx context.Context, issueID pgtype.UUID, key string) map[string]bool {
	set := map[string]bool{}
	var raw []byte
	if err := h.DB.QueryRow(ctx, `SELECT metadata FROM issue WHERE id = $1`, issueID).Scan(&raw); err != nil || len(raw) == 0 {
		return set
	}
	var meta map[string]json.RawMessage
	if json.Unmarshal(raw, &meta) != nil {
		return set
	}
	if arr, ok := meta[key]; ok {
		var ids []string
		if json.Unmarshal(arr, &ids) == nil {
			for _, id := range ids {
				if s := strings.TrimSpace(id); s != "" {
					set[s] = true
				}
			}
		}
	}
	return set
}

// setBitrixSyncedIDSet persists the merged synced-id set back onto the issue
// metadata (sorted for a stable value). Best-effort.
func (h *Handler) setBitrixSyncedIDSet(ctx context.Context, wsID, issueID pgtype.UUID, key string, set map[string]bool) {
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	val, err := json.Marshal(ids)
	if err != nil {
		return
	}
	if _, err := h.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
		ID: issueID, WorkspaceID: wsID, Key: key, Value: val,
	}); err != nil {
		slog.Debug("bitrix sync: set synced-id set failed", "key", key, "error", err)
	}
}
