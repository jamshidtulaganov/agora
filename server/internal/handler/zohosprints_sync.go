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
	"github.com/multica-ai/multica/server/internal/integrations/zohosprints"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Zoho Sprints → Agora one-way import. Zoho Sprints is a SEPARATE product from
// Zoho Projects (see the zohosprints package doc), so this import is independent
// of the zohoprojects importer and lands in its OWN Agora project ("<name>
// (Sprints)") to avoid colliding with a Projects import of the same workspace.
//
// Mapping: a Sprints project -> a Agora project; its sprints -> Agora sprints
// (with the real start/end dates); its work items (sprint + backlog) -> issues,
// with a Zoho parent item -> a Agora sub-issue. Idempotent via durable markers +
// a per-item metadata key, exactly like the zohoprojects importer.

// zohoSprintsItemIDMetaKey links an issue to its Zoho Sprints item. Dedup keys on
// it so re-importing updates rather than duplicates.
const zohoSprintsItemIDMetaKey = "zoho_sprint_item_id"

// zohoSprintsProjectMarkerPrefix marks a Agora project imported from a Zoho
// Sprints project. Distinct from the zohoprojects "zoho_project:" marker so the
// two importers never resolve onto each other's project.
const zohoSprintsProjectMarkerPrefix = "zoho_sprints_project:"

// zohoSprintsSprintMarkerPrefix marks a Agora sprint derived from a Zoho Sprints
// sprint (in sprint.goal). Distinct prefix so a sprint id can't collide with the
// zohoprojects task-list / sprint-task markers.
const zohoSprintsSprintMarkerPrefix = "zsprint:"

const zohoSprintsSyncTimeout = 20 * time.Minute

// --- env config -------------------------------------------------------------

func zohoSprintsClientID() string { return strings.TrimSpace(os.Getenv("ZOHO_SPRINTS_CLIENT_ID")) }
func zohoSprintsClientSecret() string {
	return strings.TrimSpace(os.Getenv("ZOHO_SPRINTS_CLIENT_SECRET"))
}
func zohoSprintsRefreshToken() string {
	return strings.TrimSpace(os.Getenv("ZOHO_SPRINTS_REFRESH_TOKEN"))
}
func zohoSprintsTeam() string { return strings.TrimSpace(os.Getenv("ZOHO_SPRINTS_TEAM")) }
func zohoSprintsAccountsHost() string {
	return strings.TrimSpace(os.Getenv("ZOHO_SPRINTS_ACCOUNTS_HOST"))
}
func zohoSprintsAPIHost() string { return strings.TrimSpace(os.Getenv("ZOHO_SPRINTS_API_HOST")) }

// zohoSprintsConfigured reports whether the Zoho Sprints integration has the
// minimum env (OAuth client id/secret + refresh token).
func zohoSprintsConfigured() bool {
	return zohoSprintsClientID() != "" && zohoSprintsClientSecret() != "" && zohoSprintsRefreshToken() != ""
}

func zohoSprintsConfigFromEnv() zohosprints.Config {
	return zohosprints.Config{
		ClientID:     zohoSprintsClientID(),
		ClientSecret: zohoSprintsClientSecret(),
		RefreshToken: zohoSprintsRefreshToken(),
		TeamID:       zohoSprintsTeam(),
		AccountsHost: zohoSprintsAccountsHost(),
		APIHost:      zohoSprintsAPIHost(),
	}
}

// --- sync state -------------------------------------------------------------

type zohoSprintsSyncState struct {
	client *zohosprints.Client
	teamID string

	// zohoProjectID is the originating Zoho Sprints project id for this run,
	// stamped on each issue's metadata for provenance.
	zohoProjectID  string
	agoraProjectID pgtype.UUID
	statuses       map[string]zohosprints.ItemStatus
	// sprintCache maps a Zoho Sprints sprint id -> Agora sprint id.
	sprintCache map[string]pgtype.UUID
	// itemIssueCache maps a Zoho Sprints item id -> Agora issue id, for parent
	// linking in the second pass.
	itemIssueCache map[string]pgtype.UUID

	created int
	updated int
	skipped int
}

func (h *Handler) newZohoSprintsSyncState() *zohoSprintsSyncState {
	return &zohoSprintsSyncState{
		client:         zohosprints.NewClient(zohoSprintsConfigFromEnv()),
		statuses:       map[string]zohosprints.ItemStatus{},
		sprintCache:    map[string]pgtype.UUID{},
		itemIssueCache: map[string]pgtype.UUID{},
	}
}

func (h *Handler) resolveZohoSprintsTeamID(ctx context.Context, st *zohoSprintsSyncState) (string, error) {
	if st.teamID != "" {
		return st.teamID, nil
	}
	id, err := st.client.ResolveTeamID(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve zoho sprints team: %w", err)
	}
	st.teamID = id
	return id, nil
}

// --- project import ---------------------------------------------------------

// syncZohoSprintsProject imports a single Zoho Sprints project into the workspace
// as its own Agora project: sprints become Agora sprints (with dates), and every
// work item (sprint + backlog) becomes an issue, with Zoho parent items linked as
// Agora sub-issues.
func (h *Handler) syncZohoSprintsProject(ctx context.Context, wsID pgtype.UUID, sprintsProjectID string, st *zohoSprintsSyncState) error {
	sprintsProjectID = strings.TrimSpace(sprintsProjectID)
	if sprintsProjectID == "" {
		return errors.New("empty zoho sprints project id")
	}
	teamID, err := h.resolveZohoSprintsTeamID(ctx, st)
	if err != nil {
		return err
	}

	st.zohoProjectID = sprintsProjectID
	projectName := h.zohoSprintsProjectName(ctx, teamID, sprintsProjectID, st)
	agoraProjectID, err := h.getOrCreateZohoSprintsProject(ctx, wsID, sprintsProjectID, projectName)
	if err != nil {
		return fmt.Errorf("resolve agora project for zoho sprints project %s: %w", sprintsProjectID, err)
	}
	st.agoraProjectID = agoraProjectID

	// Status map (best-effort: an empty map degrades every item to its bucket /
	// the "todo" default).
	if statuses, err := st.client.ListItemStatuses(ctx, teamID, sprintsProjectID); err != nil {
		slog.Warn("zoho sprints import: list item statuses failed", "project_id", sprintsProjectID, "error", err)
	} else {
		st.statuses = statuses
	}

	// Sprints (with real dates) -> Agora sprints.
	sprints, err := st.client.ListSprints(ctx, teamID, sprintsProjectID)
	if err != nil {
		return fmt.Errorf("list zoho sprints: %w", err)
	}
	containerIDs := make([]string, 0, len(sprints)+1)
	for i := range sprints {
		sid := h.getOrCreateZohoSprintsSprint(ctx, wsID, agoraProjectID, &sprints[i], st)
		if sid.Valid {
			st.sprintCache[sprints[i].ID] = sid
		}
		containerIDs = append(containerIDs, sprints[i].ID)
	}
	// The backlog holds the bulk of items in a Sprints project that isn't actively
	// running sprints. Include it as a container so those items import too.
	if backlogID, err := st.client.BacklogID(ctx, teamID, sprintsProjectID); err != nil {
		slog.Warn("zoho sprints import: backlog lookup failed", "project_id", sprintsProjectID, "error", err)
	} else if backlogID != "" {
		containerIDs = append(containerIDs, backlogID)
	}

	// Gather all items across containers, deduped by item id (an item belongs to
	// exactly one container, but guard anyway).
	seen := map[string]bool{}
	var items []zohosprints.Item
	for _, cid := range containerIDs {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch, err := st.client.ListItems(ctx, teamID, sprintsProjectID, cid)
		if err != nil {
			slog.Warn("zoho sprints import: list items failed", "container_id", cid, "error", err)
			continue
		}
		for i := range batch {
			if batch[i].ID == "" || seen[batch[i].ID] {
				continue
			}
			seen[batch[i].ID] = true
			items = append(items, batch[i])
		}
	}

	// Pass 1: reconcile every item into an issue (records itemIssueCache).
	for i := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := h.reconcileZohoSprintsItem(ctx, wsID, sprintsProjectID, &items[i], st); err != nil {
			slog.Warn("zoho sprints import: item sync failed", "item_id", items[i].ID, "error", err)
		}
	}
	// Pass 2: link parent items now that every child + parent issue exists.
	for i := range items {
		if !zohosprints.IsParentRef(items[i].ParentID) {
			continue
		}
		child, ok := st.itemIssueCache[items[i].ID]
		parent, ok2 := st.itemIssueCache[items[i].ParentID]
		if !ok || !ok2 || !child.Valid || !parent.Valid {
			continue
		}
		if _, err := h.DB.Exec(ctx,
			`UPDATE issue SET parent_issue_id = $3, updated_at = now()
			   WHERE id = $1 AND workspace_id = $2 AND parent_issue_id IS NULL`,
			child, wsID, parent); err != nil {
			slog.Warn("zoho sprints import: link parent failed", "item_id", items[i].ID, "error", err)
		}
	}
	return nil
}

// zohoSprintsProjectName resolves a Sprints project id to its display name.
// Best-effort: "" on failure.
func (h *Handler) zohoSprintsProjectName(ctx context.Context, teamID, sprintsProjectID string, st *zohoSprintsSyncState) string {
	projects, err := st.client.ListProjects(ctx, teamID)
	if err != nil {
		return ""
	}
	for _, p := range projects {
		if p.ID == sprintsProjectID {
			return strings.TrimSpace(p.Name)
		}
	}
	return ""
}

// --- item → issue -----------------------------------------------------------

func (h *Handler) reconcileZohoSprintsItem(ctx context.Context, wsID pgtype.UUID, sprintsProjectID string, item *zohosprints.Item, st *zohoSprintsSyncState) (pgtype.UUID, error) {
	if strings.TrimSpace(item.ID) == "" {
		st.skipped++
		return pgtype.UUID{}, errors.New("empty item id")
	}

	// Per-(workspace,item) advisory lock, mirroring the zohoprojects importer.
	lockKey := util.UUIDToString(wsID) + ":zsprints:" + item.ID
	lockTx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("begin sync lock tx: %w", err)
	}
	defer func() { _ = lockTx.Rollback(ctx) }()
	if _, err := lockTx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, lockKey); err != nil {
		return pgtype.UUID{}, fmt.Errorf("acquire sync lock: %w", err)
	}
	defer func() {
		if cerr := lockTx.Commit(ctx); cerr != nil {
			slog.Warn("zoho sprints import: lock tx commit failed", "item_id", item.ID, "error", cerr)
		}
	}()

	existing, found, err := h.findIssueByZohoSprintsItemID(ctx, wsID, item.ID)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("dedup lookup: %w", err)
	}

	draft := zohosprints.MapItemToIssue(item, st.statuses)
	// Sprint membership: only when the item's container is a real Agora sprint
	// (backlog items map to no sprint).
	sprintID := st.sprintCache[item.SprintID]

	if found {
		if existing.Status != draft.Status {
			if _, err := h.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
				ID: existing.ID, Status: draft.Status, WorkspaceID: wsID,
			}); err != nil {
				return pgtype.UUID{}, fmt.Errorf("update issue status: %w", err)
			}
		}
		h.setZohoSprintsItemMetadata(ctx, existing.ID, wsID, item, st)
		if !existing.ProjectID.Valid && st.agoraProjectID.Valid {
			_, _ = h.DB.Exec(ctx, `UPDATE issue SET project_id=$3, updated_at=now() WHERE id=$1 AND workspace_id=$2`,
				existing.ID, wsID, st.agoraProjectID)
		}
		if sprintID.Valid {
			if err := h.Queries.SetIssueSprint(ctx, db.SetIssueSprintParams{IssueID: existing.ID, SprintID: sprintID}); err != nil {
				slog.Warn("zoho sprints import: link sprint failed", "item_id", item.ID, "error", err)
			}
		}
		st.itemIssueCache[item.ID] = existing.ID
		st.updated++
		return existing.ID, nil
	}

	ownerID, err := h.zohoSprintsWorkspaceOwner(ctx, wsID)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("resolve workspace owner: %w", err)
	}
	res, err := h.IssueService.Create(ctx, service.IssueCreateParams{
		WorkspaceID:    wsID,
		Title:          draft.Title,
		Description:    strToText(draft.Description),
		Status:         draft.Status,
		Priority:       "none",
		CreatorType:    "member",
		CreatorID:      ownerID,
		ProjectID:      st.agoraProjectID,
		AllowDuplicate: true,
	}, service.IssueCreateOpts{ActorID: util.UUIDToString(ownerID)})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("create issue: %w", err)
	}

	idValue, _ := json.Marshal(item.ID)
	if _, err := h.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
		ID: res.Issue.ID, WorkspaceID: wsID, Key: zohoSprintsItemIDMetaKey, Value: idValue,
	}); err != nil {
		return pgtype.UUID{}, fmt.Errorf("set item id metadata: %w", err)
	}
	h.setZohoSprintsItemMetadata(ctx, res.Issue.ID, wsID, item, st)

	if sprintID.Valid {
		if err := h.Queries.SetIssueSprint(ctx, db.SetIssueSprintParams{IssueID: res.Issue.ID, SprintID: sprintID}); err != nil {
			slog.Warn("zoho sprints import: link sprint failed", "item_id", item.ID, "error", err)
		}
	}
	st.itemIssueCache[item.ID] = res.Issue.ID
	st.created++
	slog.Info("zoho sprints import: created issue from item",
		"issue_id", util.UUIDToString(res.Issue.ID), "item_id", item.ID, "status", draft.Status)
	return res.Issue.ID, nil
}

// setZohoSprintsItemMetadata stamps the item's Zoho provenance on the issue:
// project id, owner ids (no public email lookup exists, so the raw Sprints user
// ids are recorded for later mapping), item number, points, status name.
func (h *Handler) setZohoSprintsItemMetadata(ctx context.Context, issueID, wsID pgtype.UUID, item *zohosprints.Item, st *zohoSprintsSyncState) {
	set := func(key string, val any) {
		b, err := json.Marshal(val)
		if err != nil {
			return
		}
		if _, err := h.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
			ID: issueID, WorkspaceID: wsID, Key: key, Value: b,
		}); err != nil {
			slog.Debug("zoho sprints import: set metadata failed", "key", key, "error", err)
		}
	}
	if st.zohoProjectID != "" {
		set("zoho_sprints_project_id", st.zohoProjectID)
	}
	if len(item.OwnerIDs) > 0 {
		set("zoho_sprints_owner_ids", item.OwnerIDs)
	}
	if n := strings.TrimSpace(item.No); n != "" {
		set("zoho_sprints_item_no", n)
	}
	if p := strings.TrimSpace(item.Points); p != "" && p != "0" {
		set("zoho_sprints_points", p)
	}
	if s, ok := st.statuses[item.StatusID]; ok && s.Name != "" {
		set("zoho_sprints_status", s.Name)
	}
}

// --- project marker dedup ---------------------------------------------------

func (h *Handler) getOrCreateZohoSprintsProject(ctx context.Context, workspaceID pgtype.UUID, sprintsProjectID, projectName string) (pgtype.UUID, error) {
	sprintsProjectID = strings.TrimSpace(sprintsProjectID)
	if sprintsProjectID == "" {
		return pgtype.UUID{}, fmt.Errorf("empty zoho sprints project id")
	}
	marker := zohoSprintsProjectMarkerPrefix + sprintsProjectID

	var existingID pgtype.UUID
	err := h.DB.QueryRow(ctx,
		`SELECT id FROM project WHERE workspace_id = $1 AND description LIKE '%' || $2 || '%'
		  ORDER BY created_at ASC LIMIT 1`,
		workspaceID, marker).Scan(&existingID)
	if err == nil {
		return existingID, nil
	}
	if err != pgx.ErrNoRows {
		return pgtype.UUID{}, fmt.Errorf("lookup zoho sprints project: %w", err)
	}

	title := strings.TrimSpace(projectName)
	if title == "" {
		title = "Zoho Sprints project " + sprintsProjectID
	}
	// Suffix so it never visually collides with a Projects import of the same name.
	title += " (Sprints)"
	description := "Imported from Zoho Sprints.\n" + marker

	project, err := h.Queries.CreateProject(ctx, db.CreateProjectParams{
		WorkspaceID: workspaceID, Title: title, Description: strToText(description),
		Status: "planned", Priority: "none",
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("create zoho sprints project: %w", err)
	}
	slog.Info("zoho sprints import: created project",
		"project_id", util.UUIDToString(project.ID), "zoho_sprints_project_id", sprintsProjectID, "title", title)
	return project.ID, nil
}

// --- sprint marker dedup ----------------------------------------------------

func (h *Handler) getOrCreateZohoSprintsSprint(ctx context.Context, workspaceID, projectID pgtype.UUID, sprint *zohosprints.Sprint, st *zohoSprintsSyncState) pgtype.UUID {
	if !projectID.Valid {
		return pgtype.UUID{}
	}
	sid := strings.TrimSpace(sprint.ID)
	if sid == "" {
		return pgtype.UUID{}
	}
	if id, ok := st.sprintCache[sid]; ok {
		return id
	}
	marker := zohoSprintsSprintMarkerPrefix + sid

	var existingID pgtype.UUID
	err := h.DB.QueryRow(ctx,
		`SELECT id FROM sprint WHERE project_id = $1 AND goal LIKE '%' || $2 || '%'
		  ORDER BY created_at ASC LIMIT 1`,
		projectID, marker).Scan(&existingID)
	if err == nil {
		st.sprintCache[sid] = existingID
		return existingID
	}
	if err != pgx.ErrNoRows {
		slog.Warn("zoho sprints import: lookup sprint failed", "sprint_id", sid, "error", err)
		return pgtype.UUID{}
	}

	name := strings.TrimSpace(sprint.Name)
	if name == "" {
		if n := strings.TrimSpace(sprint.No); n != "" {
			name = "Sprint " + n
		} else {
			name = "Zoho Sprint " + sid
		}
	}
	created, err := h.Queries.CreateSprint(ctx, db.CreateSprintParams{
		WorkspaceID: workspaceID, ProjectID: projectID, Name: name, Goal: marker,
		Status:    "active",
		StartDate: zohoSprintsTimestamp(sprint.StartDate),
		EndDate:   zohoSprintsTimestamp(sprint.EndDate),
	})
	if err != nil {
		slog.Warn("zoho sprints import: create sprint failed", "sprint_id", sid, "error", err)
		return pgtype.UUID{}
	}
	st.sprintCache[sid] = created.ID
	slog.Info("zoho sprints import: created sprint",
		"sprint_id", util.UUIDToString(created.ID), "zoho_sprint_id", sid, "name", name)
	return created.ID
}

// zohoSprintsTimestamp parses a Zoho ISO date into a pgtype.Timestamptz, leaving
// it null (Valid=false) when unset/unparseable.
func zohoSprintsTimestamp(s string) pgtype.Timestamptz {
	t, ok := zohosprints.ParseZohoDate(s)
	if !ok {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// --- shared helpers ---------------------------------------------------------

func (h *Handler) zohoSprintsWorkspaceOwner(ctx context.Context, wsID pgtype.UUID) (pgtype.UUID, error) {
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

func (h *Handler) findIssueByZohoSprintsItemID(ctx context.Context, wsID pgtype.UUID, itemID string) (db.Issue, bool, error) {
	filter, err := json.Marshal(map[string]string{zohoSprintsItemIDMetaKey: itemID})
	if err != nil {
		return db.Issue{}, false, err
	}
	row := h.DB.QueryRow(ctx,
		`SELECT id, workspace_id, title, description, status, priority,
		        assignee_type, assignee_id, creator_type, creator_id,
		        parent_issue_id, acceptance_criteria, context_refs, position,
		        due_date, created_at, updated_at, number, project_id,
		        origin_type, origin_id, first_executed_at, start_date, metadata
		   FROM issue WHERE workspace_id = $1 AND metadata @> $2::jsonb
		  ORDER BY created_at ASC LIMIT 1`,
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

// zohoSprintsUnavailable writes the standard 400 used when the integration env is
// unset.
func zohoSprintsUnavailable(w http.ResponseWriter) {
	writeError(w, http.StatusBadRequest, "Zoho Sprints not configured")
}
