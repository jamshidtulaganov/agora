package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Generic Zoho CRM reconcile engine (docs/zoho-dynamic-integration.md §1.4,
// phases D2 inbound + D3 outbound). One loop serves every configured module:
// each zoho_sync_config row names a module, a field map, a status map and a
// direction; the engine pulls changed records via COQL and reconciles them
// into issues, and mirrors Agora status changes back through the same map.
//
// The skeleton deliberately mirrors zohoprojects_sync.go: the same advisory-
// lock critical section, the same metadata @> dedup, the same RAW bus-free
// inbound writes (the echo-break for the outbound mirror), and the same
// per-target error isolation in the poller.

// zohoDynRecIDMetaKey links an issue to its CRM record as "<module>:<id>" —
// one module-qualified key so a single GIN containment filter dedups every
// module. The companion keys are display metadata.
const (
	zohoDynRecIDMetaKey      = "zoho_rec_id"
	zohoDynModuleMetaKey     = "zoho_module"
	zohoDynRecordURLMetaKey  = "zoho_record_url"
	zohoDynStatusNameMetaKey = "zoho_status_name"
	zohoDynOwnerEmailMetaKey = "zoho_owner_email"
)

// zohoDynModuleMarkerPrefix is the durable marker embedded in an auto-created
// Agora project's description linking it to its CRM module, mirroring
// zohoProjectMarkerPrefix ("zoho_project:") for the static channel.
const zohoDynModuleMarkerPrefix = "zoho_module:"

// zohoDynSyncTimeout bounds one config's sweep; zohoDynOutboundTimeout bounds
// one outbound status push (mirrors zohoOutboundTimeout).
const (
	zohoDynSyncTimeout     = 5 * time.Minute
	zohoDynOutboundTimeout = 30 * time.Second
)

// zohoDynPageLimit is the COQL page size; zohoDynMaxPages caps one sweep so a
// giant backlog is chipped away across sweeps instead of wedging one.
const (
	zohoDynPageLimit = 200
	zohoDynMaxPages  = 25
)

// zohoDynChannel is the only channel the engine serves in v1. The column
// exists so Desk/Projects could migrate onto the engine later (design doc §2).
const zohoDynChannel = "crm"

// zohoDynIdentifierRe validates every identifier interpolated into COQL or a
// record URL path — module API names, field API names, record ids. Enforced
// at config-write time AND again before building queries (defense in depth
// against COQL injection, design doc §4).
var zohoDynIdentifierRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,100}$`)

// zohoDynAgoraFields is the whitelisted set of Agora-side field_map keys. A
// field map can never target an Agora column outside this set (§4). "status"
// is special: it names the Zoho field the status_map reads/writes, not a
// directly copied column.
var zohoDynAgoraFields = map[string]bool{
	"title":       true,
	"description": true,
	"priority":    true,
	"due_date":    true,
	"status":      true,
}

// zohoDynAgoraStatuses is the issue.status CHECK constraint set (migration
// 001). status_map entries naming anything else are rejected at write time
// and ignored at sync time, so a bad map can never violate the constraint.
var zohoDynAgoraStatuses = map[string]bool{
	"backlog":     true,
	"todo":        true,
	"in_progress": true,
	"in_review":   true,
	"done":        true,
	"blocked":     true,
	"cancelled":   true,
}

// zohoCRMWebHosts maps a connection dc to the CRM web UI host used for
// zoho_record_url metadata. Unknown dc falls back to .com.
var zohoCRMWebHosts = map[string]string{
	"us": "https://crm.zoho.com",
	"eu": "https://crm.zoho.eu",
	"in": "https://crm.zoho.in",
	"au": "https://crm.zoho.com.au",
	"jp": "https://crm.zoho.jp",
	"sa": "https://crm.zoho.sa",
	"ca": "https://crm.zohocloud.ca",
}

// --- env config -------------------------------------------------------------

// zohoDynSyncInterval is the poller cadence. Unset / unparseable / non-positive
// all mean disabled (opt-in, unlike the static channel's 15m default — the
// dynamic engine only runs where an operator deliberately turned it on).
func zohoDynSyncInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv("ZOHO_DYN_SYNC_INTERVAL"))
	if raw == "" {
		return 0
	}
	if d, err := time.ParseDuration(raw); err == nil {
		return d
	}
	return 0
}

// zohoDynPushEnabled gates the outbound mirror. Off by default — writing to a
// live CRM is opt-in, mirroring ZOHO_PROJECTS_PUSH_STATUS.
func zohoDynPushEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ZOHO_DYN_PUSH"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// --- field / status maps ------------------------------------------------------

// zohoDynMaps is the parsed, validated projection of a config row's field_map
// and status_map. Entries failing the whitelist / identifier / status-enum
// checks are dropped rather than crashing (enum-drift rule): a config edited
// out from under the engine degrades to fewer synced fields, never a panic.
type zohoDynMaps struct {
	fields    map[string]string // agora field -> zoho field api name
	statusIn  map[string]string // zoho status value -> agora status
	statusOut map[string]string // agora status -> zoho status value
}

func parseZohoDynMaps(cfg db.ZohoSyncConfig) zohoDynMaps {
	m := zohoDynMaps{
		fields:    map[string]string{},
		statusIn:  map[string]string{},
		statusOut: map[string]string{},
	}
	var fields map[string]string
	if err := json.Unmarshal(cfg.FieldMap, &fields); err == nil {
		for k, v := range fields {
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			if zohoDynAgoraFields[k] && zohoDynIdentifierRe.MatchString(v) {
				m.fields[k] = v
			}
		}
	}
	var status struct {
		In  map[string]string `json:"in"`
		Out map[string]string `json:"out"`
	}
	if err := json.Unmarshal(cfg.StatusMap, &status); err == nil {
		for zoho, agora := range status.In {
			if zohoDynAgoraStatuses[agora] {
				m.statusIn[zoho] = agora
			}
		}
		for agora, zoho := range status.Out {
			if zohoDynAgoraStatuses[agora] && strings.TrimSpace(zoho) != "" {
				m.statusOut[agora] = strings.TrimSpace(zoho)
			}
		}
	}
	return m
}

// --- record value helpers -----------------------------------------------------

// zohoDynRecString extracts a record field as a display string. CRM values
// arrive as strings, numbers, bools, or lookup objects ({name, id}); anything
// missing or unrecognized degrades to "" — never a crash on a null field.
func zohoDynRecString(rec map[string]any, field string) string {
	if field == "" {
		return ""
	}
	switch v := rec[field].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case map[string]any:
		// Lookup fields: prefer the human label over the raw id.
		if s, ok := v["name"].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
		if s, ok := v["id"].(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// zohoDynOwnerEmail pulls Owner.email out of a record, best-effort.
func zohoDynOwnerEmail(rec map[string]any) string {
	owner, ok := rec["Owner"].(map[string]any)
	if !ok {
		return ""
	}
	email, _ := owner["email"].(string)
	return strings.TrimSpace(email)
}

// zohoDynParseDate parses a CRM date ("2006-01-02") or datetime (RFC3339)
// value into a pgtype.Date. ok=false on anything else.
func zohoDynParseDate(s string) (pgtype.Date, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return pgtype.Date{}, false
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return pgtype.Date{Time: t, Valid: true}, true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return pgtype.Date{Time: t, Valid: true}, true
	}
	return pgtype.Date{}, false
}

// zohoDynNormalizePriority buckets a CRM priority picklist value into the
// Agora priority enum. Unknown values degrade to "none".
func zohoDynNormalizePriority(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "high", "highest":
		return "high"
	case "medium", "normal":
		return "medium"
	case "low", "lowest":
		return "low"
	}
	return "none"
}

// zohoDynRecordURL builds the CRM web UI deep link for a record, deriving the
// host from the connection's dc.
func zohoDynRecordURL(dc, module, id string) string {
	host, ok := zohoCRMWebHosts[dc]
	if !ok {
		host = zohoCRMWebHosts["us"]
	}
	return host + "/crm/tab/" + module + "/" + id
}

// --- dedup --------------------------------------------------------------------

// findIssueByZohoDynRecID returns the issue whose metadata matches
// {"zoho_rec_id": "<module>:<id>"} — the same JSONB @> containment dedup as
// findIssueByZohoTaskID, keyed on the module-qualified record ref.
func (h *Handler) findIssueByZohoDynRecID(ctx context.Context, wsID pgtype.UUID, recRef string) (db.Issue, bool, error) {
	filter, err := json.Marshal(map[string]string{zohoDynRecIDMetaKey: recRef})
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

// --- destination project --------------------------------------------------------

// getOrCreateZohoDynProject resolves the Agora project for a module's records,
// creating "<ModuleApiName>" with a durable "zoho_module:<module>" description
// marker on first sight. Mirrors getOrCreateZohoProject.
func (h *Handler) getOrCreateZohoDynProject(ctx context.Context, wsID pgtype.UUID, module string) (pgtype.UUID, error) {
	marker := zohoDynModuleMarkerPrefix + module

	var existingID pgtype.UUID
	err := h.DB.QueryRow(ctx,
		`SELECT id FROM project
		  WHERE workspace_id = $1 AND description LIKE '%' || $2 || '%'
		  ORDER BY created_at ASC
		  LIMIT 1`,
		wsID, marker).Scan(&existingID)
	if err == nil {
		return existingID, nil
	}
	if err != pgx.ErrNoRows {
		return pgtype.UUID{}, fmt.Errorf("lookup zoho module project: %w", err)
	}

	project, err := h.Queries.CreateProject(ctx, db.CreateProjectParams{
		WorkspaceID: wsID,
		Title:       module,
		Description: strToText("Synced from Zoho CRM.\n" + marker),
		Status:      "planned",
		Priority:    "none",
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("create zoho module project: %w", err)
	}
	slog.Info("zoho dyn sync: created project for module",
		"project_id", util.UUIDToString(project.ID), "module", module,
		"workspace_id", util.UUIDToString(wsID))
	return project.ID, nil
}

// --- record → issue reconcile ---------------------------------------------------

// reconcileZohoDynRecord reconciles one CRM record into an Agora issue: dedup
// on the module-qualified zoho_rec_id marker, create on first sight (unless
// the config is outbound-only), RAW bus-free in-place update on re-sync. dc
// names the connection's data center (for the record URL metadata).
//
// Echo-safety: updates here never publish EventIssueUpdated, so a change that
// originated in Zoho is never bounced back by the outbound listener — the
// identical break used by reconcileZohoTask.
func (h *Handler) reconcileZohoDynRecord(ctx context.Context, cfg db.ZohoSyncConfig, wsUUID pgtype.UUID, dc string, rec map[string]any) error {
	recID := zohoDynRecString(rec, "id")
	if recID == "" {
		return errors.New("record missing id")
	}
	if !zohoDynIdentifierRe.MatchString(cfg.ModuleApiName) || !zohoDynIdentifierRe.MatchString(recID) {
		return fmt.Errorf("invalid module/record identifier %q/%q", cfg.ModuleApiName, recID)
	}
	maps := parseZohoDynMaps(cfg)
	recRef := cfg.ModuleApiName + ":" + recID

	// Serialize find/create/stamp per (workspace, module, record) with a
	// tx-scoped advisory lock so two interleaved sweeps can't double-create.
	// Same mechanism as reconcileZohoTask; ":zohodyn:" namespaces the key away
	// from the static channels' locks.
	lockKey := fmt.Sprintf("%s:zohodyn:%s:%s", util.UUIDToString(wsUUID), cfg.ModuleApiName, recID)
	lockTx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin sync lock tx: %w", err)
	}
	defer func() { _ = lockTx.Rollback(ctx) }()
	if _, err := lockTx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, lockKey); err != nil {
		return fmt.Errorf("acquire sync lock: %w", err)
	}
	releaseLock := func() {
		if cerr := lockTx.Commit(ctx); cerr != nil {
			slog.Warn("zoho dyn sync: lock tx commit failed", "record", recRef, "error", cerr)
		}
	}
	defer releaseLock()

	existing, found, err := h.findIssueByZohoDynRecID(ctx, wsUUID, recRef)
	if err != nil {
		return fmt.Errorf("dedup lookup: %w", err)
	}

	rawStatus := zohoDynRecString(rec, maps.fields["status"])
	mappedStatus, statusMapped := maps.statusIn[rawStatus]
	if rawStatus == "" {
		statusMapped = false
	}

	if found {
		// RAW bus-free UPDATE of only the mapped-and-present fields — no
		// EventIssueUpdated publish (echo-guard). An unmapped/unknown inbound
		// status keeps the existing issue status.
		sets := []string{}
		args := []any{existing.ID, wsUUID}
		addSet := func(col string, val any) {
			args = append(args, val)
			sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
		}
		if f := maps.fields["title"]; f != "" {
			if v := zohoDynRecString(rec, f); v != "" && v != existing.Title {
				addSet("title", v)
			}
		}
		if f := maps.fields["description"]; f != "" {
			if v := zohoDynRecString(rec, f); v != "" && v != existing.Description.String {
				addSet("description", v)
			}
		}
		if f := maps.fields["priority"]; f != "" {
			if v := zohoDynRecString(rec, f); v != "" {
				if p := zohoDynNormalizePriority(v); p != existing.Priority {
					addSet("priority", p)
				}
			}
		}
		if f := maps.fields["due_date"]; f != "" {
			if d, ok := zohoDynParseDate(zohoDynRecString(rec, f)); ok && !d.Time.Equal(existing.DueDate.Time) {
				addSet("due_date", d)
			}
		}
		if statusMapped && mappedStatus != existing.Status {
			addSet("status", mappedStatus)
		}
		if len(sets) > 0 {
			q := "UPDATE issue SET " + strings.Join(sets, ", ") + ", updated_at = now() WHERE id = $1 AND workspace_id = $2"
			if _, err := h.DB.Exec(ctx, q, args...); err != nil {
				return fmt.Errorf("update issue: %w", err)
			}
			slog.Info("zoho dyn sync: updated issue in place",
				"issue_id", util.UUIDToString(existing.ID), "record", recRef, "fields", len(sets))
		}
		// Refresh display metadata so the CRM status label stays current.
		h.setZohoDynDisplayMetadata(ctx, existing.ID, wsUUID, rawStatus, zohoDynOwnerEmail(rec))
		return nil
	}

	// Outbound-only configs never create issues from CRM records.
	if cfg.Direction == "out" {
		return nil
	}

	ownerID, err := h.zohoWorkspaceOwner(ctx, wsUUID)
	if err != nil {
		return fmt.Errorf("resolve workspace owner: %w", err)
	}

	projectID := cfg.ProjectID
	if !projectID.Valid {
		pid, perr := h.getOrCreateZohoDynProject(ctx, wsUUID, cfg.ModuleApiName)
		if perr != nil {
			slog.Warn("zoho dyn sync: project resolve failed, filing without project",
				"module", cfg.ModuleApiName, "error", perr)
		} else {
			projectID = pid
			// Persist the destination back onto the config row so later sweeps
			// (and the UI) see it. COALESCE update — only project_id changes.
			if _, uerr := h.Queries.UpdateZohoSyncConfig(ctx, db.UpdateZohoSyncConfigParams{
				ID:        cfg.ID,
				ProjectID: pid,
			}); uerr != nil {
				slog.Warn("zoho dyn sync: persist project on config failed",
					"config_id", util.UUIDToString(cfg.ID), "error", uerr)
			}
		}
	}

	title := zohoDynRecString(rec, maps.fields["title"])
	if title == "" {
		title = cfg.ModuleApiName + " " + recID
	}
	status := "todo"
	if statusMapped {
		status = mappedStatus
	}
	priority := "none"
	if f := maps.fields["priority"]; f != "" {
		priority = zohoDynNormalizePriority(zohoDynRecString(rec, f))
	}
	var dueDate pgtype.Date
	if f := maps.fields["due_date"]; f != "" {
		if d, ok := zohoDynParseDate(zohoDynRecString(rec, f)); ok {
			dueDate = d
		}
	}

	// Metadata is stamped atomically inside the create tx so the record is
	// dedup-visible the instant the issue exists (no crash window between
	// create and stamp).
	meta := map[string]any{
		zohoDynRecIDMetaKey:     recRef,
		zohoDynModuleMetaKey:    cfg.ModuleApiName,
		zohoDynRecordURLMetaKey: zohoDynRecordURL(dc, cfg.ModuleApiName, recID),
	}
	if rawStatus != "" {
		meta[zohoDynStatusNameMetaKey] = rawStatus
	}
	if email := zohoDynOwnerEmail(rec); email != "" {
		meta[zohoDynOwnerEmailMetaKey] = email
	}

	res, err := h.IssueService.Create(ctx, service.IssueCreateParams{
		WorkspaceID: wsUUID,
		Title:       title,
		Description: strToText(zohoDynRecString(rec, maps.fields["description"])),
		Status:      status,
		Priority:    priority,
		CreatorType: "member",
		CreatorID:   ownerID,
		ProjectID:   projectID,
		DueDate:     dueDate,
		// Dedup is on zoho_rec_id, not the title.
		AllowDuplicate: true,
		Metadata:       meta,
	}, service.IssueCreateOpts{
		ActorID: util.UUIDToString(ownerID),
	})
	if err != nil {
		return fmt.Errorf("create issue: %w", err)
	}
	slog.Info("zoho dyn sync: created issue from record",
		"issue_id", util.UUIDToString(res.Issue.ID), "record", recRef,
		"status", status, "project_id", util.UUIDToString(projectID))
	return nil
}

// setZohoDynDisplayMetadata refreshes the CRM status label + owner email on
// re-sync, best-effort per key.
func (h *Handler) setZohoDynDisplayMetadata(ctx context.Context, issueID, wsID pgtype.UUID, statusName, ownerEmail string) {
	kv := [][2]string{
		{zohoDynStatusNameMetaKey, statusName},
		{zohoDynOwnerEmailMetaKey, ownerEmail},
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
			slog.Warn("zoho dyn sync: set metadata failed",
				"issue_id", util.UUIDToString(issueID), "key", p[0], "error", err)
		}
	}
}

// --- COQL sweep -----------------------------------------------------------------

// buildZohoDynCOQL assembles the incremental COQL for one config page. Every
// identifier is regex-validated (module at config-write time and again here;
// field names when the map was parsed) and the cursor literal is formatted
// server-side, so no operator input reaches the query unvalidated. The
// admin-only filter_coql fragment is wrapped in parentheses so it cannot
// widen the WHERE with an OR.
func buildZohoDynCOQL(module string, maps zohoDynMaps, since time.Time, filterCOQL string) (string, error) {
	if !zohoDynIdentifierRe.MatchString(module) {
		return "", fmt.Errorf("invalid module api name %q", module)
	}
	cols := []string{"id", "Modified_Time", "Owner"}
	seen := map[string]bool{"id": true, "Modified_Time": true, "Owner": true}
	// Sorted agora keys keep the generated query deterministic.
	agoraKeys := make([]string, 0, len(maps.fields))
	for k := range maps.fields {
		agoraKeys = append(agoraKeys, k)
	}
	sort.Strings(agoraKeys)
	for _, k := range agoraKeys {
		f := maps.fields[k]
		if !seen[f] {
			seen[f] = true
			cols = append(cols, f)
		}
	}
	where := fmt.Sprintf("Modified_Time > '%s'", since.UTC().Format("2006-01-02T15:04:05-07:00"))
	if f := strings.TrimSpace(filterCOQL); f != "" {
		where += " AND (" + f + ")"
	}
	return fmt.Sprintf("SELECT %s FROM %s WHERE %s ORDER BY Modified_Time ASC LIMIT %d",
		strings.Join(cols, ", "), module, where, zohoDynPageLimit), nil
}

// zohoDynParseModifiedTime parses a record's Modified_Time (RFC3339 with
// offset). Zero time when absent/unparseable.
func zohoDynParseModifiedTime(rec map[string]any) time.Time {
	s, _ := rec["Modified_Time"].(string)
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}
	}
	return t
}

// syncZohoDynConfig runs one inbound sweep for one config: COQL page loop
// from the persisted cursor, per-record reconcile with error isolation, then
// cursor advance to the max Modified_Time seen. Outbound-only configs are a
// no-op — direction 'out' means Zoho is never read into Agora.
func (h *Handler) syncZohoDynConfig(ctx context.Context, cfg db.ZohoSyncConfig) error {
	if !cfg.Enabled || cfg.Direction == "out" {
		return nil
	}
	if !zohoDynIdentifierRe.MatchString(cfg.ModuleApiName) {
		return fmt.Errorf("invalid module api name %q", cfg.ModuleApiName)
	}
	client, ok := h.zohoCRMClientForWorkspace(ctx, cfg.WorkspaceID)
	if !ok {
		return errors.New("zoho connection unavailable for workspace")
	}
	// dc feeds the record URL metadata; the row was just proven readable.
	conn, err := h.Queries.GetZohoConnectionForWorkspace(ctx, cfg.WorkspaceID)
	if err != nil {
		return fmt.Errorf("load zoho connection: %w", err)
	}
	maps := parseZohoDynMaps(cfg)

	cursor := time.Unix(0, 0).UTC()
	if cfg.Cursor.Valid {
		cursor = cfg.Cursor.Time.UTC()
	}
	start := cursor
	maxSeen := cursor
	// Earliest Modified_Time among records whose reconcile FAILED this sweep.
	// The persisted cursor must never advance past it, or a transient DB blip
	// silently drops that record's inbound change forever (strict '>' in the
	// COQL means it is only ever re-queried if it changes again in Zoho).
	var earliestFailed time.Time

	for page := 0; page < zohoDynMaxPages; page++ {
		coql, err := buildZohoDynCOQL(cfg.ModuleApiName, maps, cursor, cfg.FilterCoql)
		if err != nil {
			return err
		}
		recs, more, err := client.Query(ctx, coql)
		if err != nil {
			// Persist progress before surfacing the error so a mid-sweep
			// failure doesn't re-walk reconciled pages next time.
			h.finishZohoDynSweep(ctx, cfg.ID, start, maxSeen, earliestFailed)
			return fmt.Errorf("coql query %s: %w", cfg.ModuleApiName, err)
		}
		for i := range recs {
			if ctx.Err() != nil {
				h.finishZohoDynSweep(ctx, cfg.ID, start, maxSeen, earliestFailed)
				return ctx.Err()
			}
			if err := h.reconcileZohoDynRecord(ctx, cfg, cfg.WorkspaceID, conn.Dc, recs[i]); err != nil {
				slog.Warn("zoho dyn sync: record reconcile failed",
					"module", cfg.ModuleApiName, "error", err)
				if mt := zohoDynParseModifiedTime(recs[i]); !mt.IsZero() &&
					(earliestFailed.IsZero() || mt.Before(earliestFailed)) {
					earliestFailed = mt
				}
			}
			if mt := zohoDynParseModifiedTime(recs[i]); mt.After(maxSeen) {
				maxSeen = mt
			}
		}
		if !more || len(recs) == 0 {
			break
		}
		// Advance the page window on Modified_Time. If the whole page shares
		// one timestamp the strict '>' cannot progress — stop rather than spin;
		// the next sweep resumes past it. Likewise stop once the window would
		// pass a failed record: later pages would be lost behind the capped
		// cursor, so let the next sweep retry from the failure point.
		if !maxSeen.After(cursor) {
			break
		}
		if !earliestFailed.IsZero() && !maxSeen.Before(earliestFailed) {
			break
		}
		cursor = maxSeen
	}

	h.finishZohoDynSweep(ctx, cfg.ID, start, maxSeen, earliestFailed)
	return nil
}

// zohoDynMaxFailStreak is how many consecutive sweeps may end capped on the
// same failure window before the record is abandoned and the cursor advances
// past it — a poison record must not stall a module's sync forever.
const zohoDynMaxFailStreak = 5

// zohoDynFailStreaks counts consecutive capped sweeps per config id.
// In-memory by design: a restart just grants the record a fresh retry cycle.
var zohoDynFailStreaks sync.Map

// zohoDynSweepWatermark decides what cursor to persist after a sweep.
// Pure so the cap/abandon ladder is unit-testable: with no failure the full
// watermark persists and the streak resets; with a failure the cursor is
// capped just below the earliest failed record until the streak exhausts,
// after which the sweep abandons the record and advances.
func zohoDynSweepWatermark(maxSeen, earliestFailed time.Time, streak int) (persist time.Time, nextStreak int, abandoned bool) {
	if earliestFailed.IsZero() || maxSeen.Before(earliestFailed) {
		return maxSeen, 0, false
	}
	if streak+1 >= zohoDynMaxFailStreak {
		return maxSeen, 0, true
	}
	return earliestFailed.Add(-time.Millisecond), streak + 1, false
}

// finishZohoDynSweep applies zohoDynSweepWatermark and persists the result.
func (h *Handler) finishZohoDynSweep(ctx context.Context, cfgID pgtype.UUID, start, maxSeen, earliestFailed time.Time) {
	key := util.UUIDToString(cfgID)
	streak := 0
	if v, ok := zohoDynFailStreaks.Load(key); ok {
		streak, _ = v.(int)
	}
	persist, nextStreak, abandoned := zohoDynSweepWatermark(maxSeen, earliestFailed, streak)
	if abandoned {
		slog.Error("zoho dyn sync: abandoning repeatedly failing record window; cursor advances past it",
			"config_id", key, "failed_at", earliestFailed.UTC().Format(time.RFC3339), "sweeps", streak+1)
	}
	if nextStreak == 0 {
		zohoDynFailStreaks.Delete(key)
	} else {
		zohoDynFailStreaks.Store(key, nextStreak)
	}
	h.saveZohoDynCursor(ctx, cfgID, start, persist)
}

// saveZohoDynCursor persists the sweep watermark when it advanced.
// Best-effort: a failure only means re-walking records (idempotent).
func (h *Handler) saveZohoDynCursor(ctx context.Context, cfgID pgtype.UUID, start, maxSeen time.Time) {
	if !maxSeen.After(start) {
		return
	}
	if err := h.Queries.UpdateZohoSyncConfigCursor(ctx, db.UpdateZohoSyncConfigCursorParams{
		ID:     cfgID,
		Cursor: pgtype.Timestamptz{Time: maxSeen.UTC(), Valid: true},
	}); err != nil {
		slog.Warn("zoho dyn sync: persist cursor failed",
			"config_id", util.UUIDToString(cfgID), "error", err)
	}
}

// --- poller ----------------------------------------------------------------------

// RunZohoDynSyncPoller periodically sweeps every enabled sync config. No-op
// unless ZOHO_DYN_SYNC_INTERVAL is a positive duration (default off — the
// engine is idle until an operator opts in). Bound to ctx; wired from
// cmd/server/main.go next to RunZohoSyncPoller.
func (h *Handler) RunZohoDynSyncPoller(ctx context.Context) {
	interval := zohoDynSyncInterval()
	if interval <= 0 {
		return
	}
	slog.Info("zoho dyn sync poller started", "interval", interval.String())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.runZohoDynSweep(ctx)
		}
	}
}

// runZohoDynSweep syncs each enabled config with per-config error isolation —
// one failing config (bad credentials, revoked module) must not kill the
// sweep for every other workspace.
func (h *Handler) runZohoDynSweep(ctx context.Context) {
	cfgs, err := h.Queries.ListEnabledZohoSyncConfigs(ctx)
	if err != nil {
		slog.Warn("zoho dyn sync poller: list configs failed", "error", err)
		return
	}
	synced := 0
	for i := range cfgs {
		if ctx.Err() != nil {
			return
		}
		tctx, cancel := context.WithTimeout(ctx, zohoDynSyncTimeout)
		err := h.syncZohoDynConfig(tctx, cfgs[i])
		cancel()
		if err != nil {
			slog.Warn("zoho dyn sync poller: config sync failed",
				"config_id", util.UUIDToString(cfgs[i].ID),
				"module", cfgs[i].ModuleApiName,
				"workspace_id", util.UUIDToString(cfgs[i].WorkspaceID), "error", err)
			continue
		}
		synced++
	}
	if len(cfgs) > 0 {
		slog.Info("zoho dyn sync poller: sweep finished", "configs", synced)
	}
}

// --- outbound status mirror (D3) ---------------------------------------------------

// registerZohoDynOutbound subscribes to issue:updated and pushes status
// changes of CRM-linked issues back through each module's status_map. Gated
// on ZOHO_DYN_PUSH (default off). Echo-safe because every inbound write in
// this file is RAW/bus-free. Wired from handler.New() next to
// registerZohoOutbound.
func (h *Handler) registerZohoDynOutbound() {
	if h.Bus == nil {
		return
	}
	if !zohoDynPushEnabled() {
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
			ctx, cancel := context.WithTimeout(context.Background(), zohoDynOutboundTimeout)
			defer cancel()
			if err := h.mirrorIssueStatusToZohoDyn(ctx, issueID); err != nil {
				slog.Warn("zoho dyn outbound: mirror failed", "issue_id", issueID, "error", err)
			}
		}()
	})
}

// mirrorIssueStatusToZohoDyn is the testable core of the outbound listener.
// It re-reads the issue, bails unless it is CRM-linked with an enabled
// out-capable config, maps the status via status_map.out (miss = skip — Zoho
// blueprints may reject arbitrary jumps, never guess), and PUTs the module's
// status field. v1 mirrors status only; title/description push is a later
// phase.
func (h *Handler) mirrorIssueStatusToZohoDyn(ctx context.Context, issueID string) error {
	if !zohoDynPushEnabled() {
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

	recRef := metaString(issue.Metadata, zohoDynRecIDMetaKey)
	if recRef == "" {
		// Not a CRM-linked issue — nothing to mirror.
		return nil
	}
	module, recID, ok := strings.Cut(recRef, ":")
	if !ok || !zohoDynIdentifierRe.MatchString(module) || !zohoDynIdentifierRe.MatchString(recID) {
		slog.Debug("zoho dyn outbound: malformed zoho_rec_id, skipping",
			"issue_id", issueID, "rec_ref", recRef)
		return nil
	}

	cfg, err := h.Queries.GetZohoSyncConfigByModule(ctx, db.GetZohoSyncConfigByModuleParams{
		WorkspaceID:   issue.WorkspaceID,
		Channel:       zohoDynChannel,
		ModuleApiName: module,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("load sync config: %w", err)
	}
	if !cfg.Enabled || (cfg.Direction != "out" && cfg.Direction != "both") {
		return nil
	}

	maps := parseZohoDynMaps(cfg)
	statusField := maps.fields["status"]
	if statusField == "" {
		slog.Debug("zoho dyn outbound: no status field mapped, skipping",
			"issue_id", issueID, "module", module)
		return nil
	}
	mapped, ok := maps.statusOut[issue.Status]
	if !ok {
		slog.Debug("zoho dyn outbound: status not mapped, skipping push",
			"issue_id", issueID, "module", module, "status", issue.Status)
		return nil
	}

	client, okc := h.zohoCRMClientForWorkspace(ctx, issue.WorkspaceID)
	if !okc {
		return errors.New("zoho connection unavailable for workspace")
	}
	if err := client.UpdateRecord(ctx, module, recID, map[string]any{statusField: mapped}); err != nil {
		return fmt.Errorf("update zoho record: %w", err)
	}
	slog.Info("zoho dyn outbound: pushed status to zoho record",
		"issue_id", issueID, "record", recRef, "status", issue.Status, "zoho_status", mapped)
	return nil
}
