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
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
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
	if len(project.Settings) == 0 {
		return "", 0
	}
	var s struct {
		Manifest *struct {
			Source   string `json:"source"`
			Revision int    `json:"revision"`
		} `json:"design_manifest"`
	}
	if json.Unmarshal(project.Settings, &s) != nil || s.Manifest == nil {
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

	// Resolve the designer via a synthetic project-bound issue (resolveDesignerAgent
	// keys off issue.ProjectID + WorkspaceID). Gate a private designer the caller
	// can't access exactly like design_review's request_changes: never write its
	// name/UUID into a caller-readable comment. Treat inaccessible as "none".
	seed := db.Issue{ProjectID: pgtype.UUID{Bytes: project.ID.Bytes, Valid: true}, WorkspaceID: project.WorkspaceID}
	designer, ok := h.resolveDesignerAgent(r.Context(), seed)
	if !ok || !h.canAccessPrivateAgent(r.Context(), designer, "member", userID, uuidToString(project.WorkspaceID)) {
		writeError(w, http.StatusConflict, "no_designer_available: configure a design agent (project.settings.design_agent) or a 'design' squad leader")
		return
	}

	title := "Design manifest sync — " + project.Title
	res, err := h.IssueService.Create(r.Context(), service.IssueCreateParams{
		WorkspaceID:  project.WorkspaceID,
		Title:        title,
		Description:  pgtype.Text{String: "Auto-created chore: refresh the project design manifest. The designer agent scans the repo (and Figma library if configured) and posts an updated ```design-manifest``` block, which the platform captures onto the project.", Valid: true},
		Status:       "todo",
		Priority:     "none",
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
		AssigneeID:   designer.ID,
		CreatorType:  "member",
		CreatorID:    parseUUID(userID),
		ProjectID:    pgtype.UUID{Bytes: project.ID.Bytes, Valid: true},
	}, service.IssueCreateOpts{ActorID: userID})
	if errors.Is(err, service.ErrActiveDuplicate) {
		writeError(w, http.StatusConflict, "sync_already_running: a design manifest sync is already open for this project")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create sync chore: "+err.Error())
		return
	}
	issue := res.Issue

	// Fire gen_design_manifest on the chore issue: post the recipe as a mention
	// comment to the designer + route through the canonical trigger path (mirrors
	// the qa-fail autoroute). The agent's reply block is then captured.
	instruction := buildSliceInstruction(sliceActionGenDesignManifest, "")
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
		writeError(w, http.StatusInternalServerError, "failed to fire manifest build")
		return
	}
	h.triggerTasksForComment(r.Context(), issue, comment, nil, "member", userID, nil)
	slog.Info("design manifest sync fired", append(logger.RequestAttrs(r),
		"project_id", uuidToString(project.ID), "issue_id", uuidToString(issue.ID), "agent_id", uuidToString(designer.ID))...)
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "queued", "issue_id": uuidToString(issue.ID)})
}
