package handler

import (
	"encoding/json"
	"net/http"
	"time"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// Department setup (docs/workspace-knowledge-plan.md §9): an owner/admin sets
// a workspace up for their team once — knowledge, what the team's sidebar
// shows, a few ready-made agents — or skips to the default. Two small
// workspace.settings keys carry the result; both are written key-scoped so
// they never clobber sibling settings.
//
//	settings.team_sidebar      = {"hidden": [nav keys], "updated_by", "updated_at"}
//	settings.department_setup  = {"status": "done"|"skipped", "by", "at"}
//
// The team sidebar applies to members who never customized their own sidebar
// (user.hidden_nav_customized_at); owners and admins keep the full sidebar.

type teamSidebarRequest struct {
	Hidden []string `json:"hidden"`
}

// PutTeamSidebar — PUT /api/workspaces/{id}/team-sidebar (owner/admin).
func (h *Handler) PutTeamSidebar(w http.ResponseWriter, r *http.Request) {
	if IsMachineActor(r) {
		writeError(w, http.StatusForbidden, "agents can't change the team sidebar")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	var req teamSidebarRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	keys, errMsg := normalizeHiddenNav(req.Hidden)
	if errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return
	}
	value, _ := json.Marshal(map[string]any{
		"hidden":     keys,
		"updated_by": requestUserID(r),
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	})
	ws, err := h.Queries.SetWorkspaceSettingKey(r.Context(), db.SetWorkspaceSettingKeyParams{ID: wsUUID, Key: "team_sidebar", Value: value})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save the team sidebar")
		return
	}
	h.publish(protocol.EventWorkspaceUpdated, uuidToString(wsUUID), "member", requestUserID(r), map[string]any{"workspace": workspaceToResponse(ws)})
	writeJSON(w, http.StatusOK, workspaceToResponse(ws))
}

type departmentSetupRequest struct {
	Status string `json:"status"`
}

// PostDepartmentSetup — POST /api/workspaces/{id}/department-setup
// (owner/admin): records that the setup was finished or skipped, so nobody
// is prompted again.
func (h *Handler) PostDepartmentSetup(w http.ResponseWriter, r *http.Request) {
	if IsMachineActor(r) {
		writeError(w, http.StatusForbidden, "agents can't finish the department setup")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceIDFromURL(r, "id"), "workspace id")
	if !ok {
		return
	}
	var req departmentSetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Status != "done" && req.Status != "skipped" {
		writeError(w, http.StatusBadRequest, `status must be "done" or "skipped"`)
		return
	}
	value, _ := json.Marshal(map[string]any{
		"status": req.Status,
		"by":     requestUserID(r),
		"at":     time.Now().UTC().Format(time.RFC3339),
	})
	ws, err := h.Queries.SetWorkspaceSettingKey(r.Context(), db.SetWorkspaceSettingKeyParams{ID: wsUUID, Key: "department_setup", Value: value})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save the setup")
		return
	}
	h.publish(protocol.EventWorkspaceUpdated, uuidToString(wsUUID), "member", requestUserID(r), map[string]any{"workspace": workspaceToResponse(ws)})
	writeJSON(w, http.StatusOK, workspaceToResponse(ws))
}
