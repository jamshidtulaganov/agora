package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Settings → Labs: workspace-level experimental flags, stored under the
// `labs` key of workspace.settings (key-scoped write — never replaces the
// whole blob). First resident: QA-environment routing — "QA runs against the
// working developer's own box (shahzod.sdteam.uz), falling back to a
// designated shared box (sandbox.sdteam.uz) when no per-dev box matches."

// WorkspaceLabs is the wire shape for GET/PUT /api/workspace-labs.
type WorkspaceLabs struct {
	// QADevBoxes routes per-task QA to the assignee-developer's own connected
	// box (connected_box.owner_id match) before the project-bound box. Default
	// ON — matches the resolver's historical behavior.
	QADevBoxes bool `json:"qa_dev_boxes"`
	// QAFallbackBoxID names the shared box QA lands on when neither a per-dev
	// nor a project/repo match resolves (e.g. sandbox.sdteam.uz). Empty = no
	// fallback (resolution can still end empty → qa_smoke_url).
	QAFallbackBoxID string `json:"qa_fallback_box_id"`
}

func defaultWorkspaceLabs() WorkspaceLabs {
	return WorkspaceLabs{QADevBoxes: true, QAFallbackBoxID: ""}
}

// workspaceLabs reads the labs block off a workspace settings blob, defaulting
// every absent field. Never errors — a malformed blob degrades to defaults.
func workspaceLabs(settings []byte) WorkspaceLabs {
	labs := defaultWorkspaceLabs()
	if len(settings) == 0 {
		return labs
	}
	var s struct {
		Labs *struct {
			QADevBoxes      *bool  `json:"qa_dev_boxes"`
			QAFallbackBoxID string `json:"qa_fallback_box_id"`
		} `json:"labs"`
	}
	if json.Unmarshal(settings, &s) != nil || s.Labs == nil {
		return labs
	}
	if s.Labs.QADevBoxes != nil {
		labs.QADevBoxes = *s.Labs.QADevBoxes
	}
	labs.QAFallbackBoxID = strings.TrimSpace(s.Labs.QAFallbackBoxID)
	return labs
}

// GetWorkspaceLabs returns the workspace's labs flags. GET /api/workspace-labs.
func (h *Handler) GetWorkspaceLabs(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace_id")
	if !ok {
		return
	}
	if _, err := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
		UserID: parseUUID(userID), WorkspaceID: wsUUID,
	}); err != nil {
		writeError(w, http.StatusForbidden, "not a member of this workspace")
		return
	}
	ws, err := h.Queries.GetWorkspace(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	writeJSON(w, http.StatusOK, workspaceLabs(ws.Settings))
}

// UpdateWorkspaceLabs merges the labs block. PUT /api/workspace-labs.
// Admin-only: labs flags change QA routing for the whole workspace.
func (h *Handler) UpdateWorkspaceLabs(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, h.resolveWorkspaceID(r), "workspace_id")
	if !ok {
		return
	}
	member, err := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
		UserID: parseUUID(userID), WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusForbidden, "not a member of this workspace")
		return
	}
	if member.Role != "admin" && member.Role != "owner" {
		writeError(w, http.StatusForbidden, "only a workspace admin can change labs flags")
		return
	}

	var req WorkspaceLabs
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.QAFallbackBoxID = strings.TrimSpace(req.QAFallbackBoxID)
	if req.QAFallbackBoxID != "" {
		boxUUID, ok := parseUUIDOrBadRequest(w, req.QAFallbackBoxID, "qa_fallback_box_id")
		if !ok {
			return
		}
		// The fallback must be one of THIS workspace's boxes.
		if _, berr := h.Queries.GetConnectedBox(r.Context(), db.GetConnectedBoxParams{
			ID: boxUUID, WorkspaceID: wsUUID,
		}); berr != nil {
			writeError(w, http.StatusBadRequest, "qa_fallback_box_id does not name a box in this workspace")
			return
		}
	}

	value, _ := json.Marshal(req)
	if _, err := h.Queries.SetWorkspaceSettingKey(r.Context(), db.SetWorkspaceSettingKeyParams{
		ID: wsUUID, Key: "labs", Value: value,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save labs settings")
		return
	}
	writeJSON(w, http.StatusOK, req)
}
