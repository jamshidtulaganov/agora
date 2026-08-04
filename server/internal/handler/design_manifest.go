package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/logger"
	"github.com/jamshidtulaganov/agora/server/internal/service"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// The project design manifest — the design counterpart to the QA manifest. It
// is agent-generated (gen_design_manifest, captured in the service layer) AND
// human-editable via this endpoint. Both write paths are KEY-SCOPED (jsonb_set)
// so a human save and a concurrent agent capture can never clobber each other's
// other settings keys — unlike PUT /api/projects/{id}, which replaces the whole
// settings blob from a client-side snapshot. A human save stamps source="manual",
// which then makes agent captures propose-via-comment instead of overwriting.

type putDesignManifestRequest struct {
	Manifest    json.RawMessage `json:"manifest"`     // full manifest object; source stamped "manual"
	DesignAgent *string         `json:"design_agent"` // agent UUID (scalar settings key)
	DesignAuto  *string         `json:"design_auto"`  // off | epics | all (scalar settings key)
}

// PutProjectDesignManifest handles PUT /api/projects/{id}/design-manifest. Each
// provided field is written via its own key-scoped jsonb_set; omitted fields are
// untouched.
func (h *Handler) PutProjectDesignManifest(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return
	}
	project, err := h.Queries.GetProject(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	if _, merr := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      parseUUID(userID),
		WorkspaceID: project.WorkspaceID,
	}); merr != nil {
		writeError(w, http.StatusForbidden, "not a member of this workspace")
		return
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	var req putDesignManifestRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	wrote := false
	updated := project

	if len(req.Manifest) > 0 {
		var obj map[string]any
		if err := json.Unmarshal(req.Manifest, &obj); err != nil || obj == nil {
			writeError(w, http.StatusBadRequest, "manifest must be a JSON object")
			return
		}
		// Stamp human ownership + bump the revision so the injector renders the
		// new one and agent captures propose-via-comment instead of overwriting.
		_, rev := h.currentDesignManifestMeta(project)
		obj["source"] = "manual"
		obj["revision"] = rev + 1
		obj["updated_at"] = nowRFC3339()
		manifestJSON, mErr := json.Marshal(obj)
		if mErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to encode manifest")
			return
		}
		row, wErr := h.Queries.SetProjectDesignManifest(r.Context(), db.SetProjectDesignManifestParams{
			ID:          project.ID,
			WorkspaceID: project.WorkspaceID,
			Manifest:    manifestJSON,
		})
		if wErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to save design manifest")
			return
		}
		updated = row
		wrote = true
	}

	if req.DesignAgent != nil {
		if row, ok := h.setProjectDesignScalar(w, r, project, "design_agent", strings.TrimSpace(*req.DesignAgent)); ok {
			updated = row
			wrote = true
		} else {
			return
		}
	}

	if req.DesignAuto != nil {
		v := strings.TrimSpace(*req.DesignAuto)
		if v != "" && v != "off" && v != "epics" && v != "all" {
			writeError(w, http.StatusBadRequest, "design_auto must be off, epics, or all")
			return
		}
		if row, ok := h.setProjectDesignScalar(w, r, project, "design_auto", v); ok {
			updated = row
			wrote = true
		} else {
			return
		}
	}

	if !wrote {
		writeError(w, http.StatusBadRequest, "no fields to update (provide manifest, design_agent, or design_auto)")
		return
	}

	h.broadcastProjectUpdated(r, updated, userID)
	writeJSON(w, http.StatusOK, projectToResponse(updated))
}

// setProjectDesignScalar writes one scalar settings key via jsonb_set and
// returns the updated row. Writes the 4xx/5xx response and returns ok=false on
// failure.
func (h *Handler) setProjectDesignScalar(w http.ResponseWriter, r *http.Request, project db.Project, key, value string) (db.Project, bool) {
	valueJSON, err := json.Marshal(value)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode "+key)
		return db.Project{}, false
	}
	row, err := h.Queries.SetProjectSettingKey(r.Context(), db.SetProjectSettingKeyParams{
		ID:          project.ID,
		WorkspaceID: project.WorkspaceID,
		Key:         key,
		Value:       valueJSON,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save "+key)
		return db.Project{}, false
	}
	return row, true
}

// currentDesignManifestMeta reads the project's current manifest source +
// revision. ("", 0) when there is no manifest.
func (h *Handler) currentDesignManifestMeta(project db.Project) (source string, revision int) {
	return h.currentDesignManifestMetaFromSettings(project.Settings)
}

// PutWorkspaceDesignManifest handles PUT /api/workspaces/{id}/design-manifest —
// the WORKSPACE-level shared design system every project inherits (admin-gated
// via the router group). Key-scoped jsonb_set (never clobbers other workspace
// settings); stamps source="manual" + revision+1.
func (h *Handler) PutWorkspaceDesignManifest(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	var body struct {
		Manifest json.RawMessage `json:"manifest"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || len(body.Manifest) == 0 {
		writeError(w, http.StatusBadRequest, "manifest is required")
		return
	}
	var obj map[string]any
	if err := json.Unmarshal(body.Manifest, &obj); err != nil || obj == nil {
		writeError(w, http.StatusBadRequest, "manifest must be a JSON object")
		return
	}

	// Bump revision from the current workspace manifest.
	rev := 0
	if ws, gerr := h.Queries.GetWorkspace(r.Context(), wsUUID); gerr == nil {
		_, rev = h.currentDesignManifestMetaFromSettings(ws.Settings)
	}
	obj["source"] = "manual"
	obj["revision"] = rev + 1
	obj["updated_at"] = nowRFC3339()
	manifestJSON, err := json.Marshal(obj)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode manifest")
		return
	}
	ws, err := h.Queries.SetWorkspaceSettingKey(r.Context(), db.SetWorkspaceSettingKeyParams{
		ID:    wsUUID,
		Key:   "design_manifest",
		Value: manifestJSON,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save workspace design manifest")
		return
	}
	// Broadcast workspace:updated so every client's cached workspace list —
	// which backs the settings editor and the inherited-manifest injector —
	// refreshes, mirroring UpdateWorkspace and the project-manifest broadcast.
	h.publish(protocol.EventWorkspaceUpdated, uuidToString(ws.ID), "member", userID, map[string]any{"workspace": workspaceToResponse(ws)})
	slog.Info("workspace design manifest saved", "workspace_id", uuidToString(wsUUID), "by", userID, "revision", rev+1)
	writeJSON(w, http.StatusOK, map[string]any{"status": "saved", "revision": rev + 1})
}

// currentDesignManifestMetaFromSettings reads source+revision from a raw
// settings blob (workspace or project). ("", 0) when absent.
func (h *Handler) currentDesignManifestMetaFromSettings(settings []byte) (source string, revision int) {
	if len(settings) == 0 {
		return "", 0
	}
	var s struct {
		Manifest *struct {
			Source   string `json:"source"`
			Revision int    `json:"revision"`
		} `json:"design_manifest"`
	}
	if json.Unmarshal(settings, &s) != nil || s.Manifest == nil {
		return "", 0
	}
	return s.Manifest.Source, s.Manifest.Revision
}

// broadcastProjectUpdated publishes project:updated with the full project
// response (counts included), mirroring the canonical UpdateProject handler.
func (h *Handler) broadcastProjectUpdated(r *http.Request, project db.Project, userID string) {
	resp := projectToResponse(project)
	resp.IssueCount, resp.DoneCount = h.loadProjectIssueStats(r.Context(), project.ID)
	resp.ResourceCount = h.loadProjectResourceCount(r.Context(), project.ID)
	h.publish(protocol.EventProjectUpdated, uuidToString(project.WorkspaceID), "member", userID, map[string]any{"project": resp})
}

// SyncProjectDesignManifest handles POST /api/projects/{id}/design-manifest/sync.
// It creates a chore issue in the project and fires gen_design_manifest on it
// targeting the project's designer agent, so the agent's ```design-manifest```
// comment is captured onto the project. The chore issue makes the sync visible
// and auditable; the duplicate guard blocks a second sync while one is open.
func (h *Handler) SyncProjectDesignManifest(w http.ResponseWriter, r *http.Request) {
	h.fireProjectDesignChore(w, r, sliceActionGenDesignManifest, "Design manifest sync — ",
		"Auto-created chore: refresh the project design manifest. The designer agent scans the repo (and Figma library if configured) and posts an updated ```design-manifest``` block, which the platform captures onto the project.")
}

// SyncProjectDesignAudit fires a design-system audit on the project: the
// designer agent scans the repo against the manifest and reports off-token
// values, duplicated markup, unmanaged components, and proposed tokens.
func (h *Handler) SyncProjectDesignAudit(w http.ResponseWriter, r *http.Request) {
	h.fireProjectDesignChore(w, r, sliceActionDesignAudit, "Design system audit — ",
		"Auto-created chore: audit the project's design-system health. The designer agent scans the repo against the design manifest and posts a ```design-audit``` block (off-token values, duplicated markup, proposed tokens).")
}

// fireProjectDesignChore is the shared trigger for the project-level design
// actions (manifest sync, audit): it resolves the designer (private-agent
// gated), creates a chore issue in the project (409 on an already-open one),
// and fires the slice action on it via a mention comment. Both actions post a
// fenced block that the platform captures/renders; the chore issue makes the
// run visible + auditable.
func (h *Handler) fireProjectDesignChore(w http.ResponseWriter, r *http.Request, kind, titlePrefix, description string) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	projectID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "project id")
	if !ok {
		return
	}
	project, err := h.Queries.GetProject(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	if _, merr := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      parseUUID(userID),
		WorkspaceID: project.WorkspaceID,
	}); merr != nil {
		writeError(w, http.StatusForbidden, "not a member of this workspace")
		return
	}

	// Resolve the designer via a synthetic project-bound issue. Gate a private
	// designer the caller can't access (never write its name/UUID into a
	// caller-readable comment) — treat inaccessible as "none".
	seed := db.Issue{ProjectID: pgtype.UUID{Bytes: project.ID.Bytes, Valid: true}, WorkspaceID: project.WorkspaceID}
	designer, ok := h.resolveDesignerAgent(r.Context(), seed)
	if !ok || !h.canAccessPrivateAgent(r.Context(), designer, "member", userID, uuidToString(project.WorkspaceID)) {
		writeError(w, http.StatusConflict, "no_designer_available: configure a design agent (project.settings.design_agent) or a 'design' squad leader")
		return
	}

	res, err := h.IssueService.Create(r.Context(), service.IssueCreateParams{
		WorkspaceID:  project.WorkspaceID,
		Title:        titlePrefix + project.Title,
		Description:  pgtype.Text{String: description, Valid: true},
		Status:       "todo",
		Priority:     "none",
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
		AssigneeID:   designer.ID,
		CreatorType:  "member",
		CreatorID:    parseUUID(userID),
		ProjectID:    pgtype.UUID{Bytes: project.ID.Bytes, Valid: true},
	}, service.IssueCreateOpts{ActorID: userID})
	if errors.Is(err, service.ErrActiveDuplicate) {
		writeError(w, http.StatusConflict, "already_running: a "+kind+" chore is already open for this project")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create chore: "+err.Error())
		return
	}
	issue := res.Issue

	instruction := buildSliceInstruction(kind, "")
	if note := h.sliceActionDesignManifestContext(r.Context(), issue); note != "" {
		instruction += note
	}
	content := fmt.Sprintf("[@%s](mention://agent/%s) ", sanitizeMentionLabel(designer.Name), uuidToString(designer.ID)) + instruction
	comment, err := h.Queries.CreateComment(r.Context(), db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "member",
		AuthorID:    parseUUID(userID),
		Content:     content,
		Type:        "comment",
		ParentID:    pgtype.UUID{Valid: false},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fire "+kind)
		return
	}
	h.triggerTasksForComment(r.Context(), issue, comment, nil, "member", userID, nil)
	slog.Info("project design chore fired", append(logger.RequestAttrs(r),
		"kind", kind, "project_id", uuidToString(project.ID), "issue_id", uuidToString(issue.ID), "agent_id", uuidToString(designer.ID))...)
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "queued", "issue_id": uuidToString(issue.ID)})
}
