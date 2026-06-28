package handler

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Remote Boxes (opt-in, additive). A connected_box is a developer's own remote
// dev server that Agora onboards over SSH and runs a NORMAL native self-host
// daemon on. This handler is the CRUD surface for that new, parallel layer; it
// changes nothing about the agent/task/runtime model. The bootstrapper (SSH
// install) and editor tunnel-manager are layered on top in later phases.

// remoteBoxesEnabled gates the entire Remote Boxes feature. Default OFF. The
// routes are not even mounted when disabled (see router) — this in-handler check
// is defense-in-depth so a directly-dispatched call still fails closed.
func remoteBoxesEnabled() bool {
	return strings.TrimSpace(os.Getenv("AGORA_REMOTE_BOXES_ENABLED")) == "true"
}

// ConnectedBoxResponse is the API shape. UUIDs are strings; nullable columns are
// pointers so an absent owner/daemon serializes as null (consumers must
// optional-chain — see the API Response Compatibility rules).
type ConnectedBoxResponse struct {
	ID           string  `json:"id"`
	WorkspaceID  string  `json:"workspace_id"`
	OwnerID      *string `json:"owner_id"`
	Label        string  `json:"label"`
	SSHHost      string  `json:"ssh_host"`
	SSHUser      string  `json:"ssh_user"`
	SSHPort      int32   `json:"ssh_port"`
	DeployPubkey string  `json:"deploy_pubkey"`
	DaemonID     *string `json:"daemon_id"`
	Status       string  `json:"status"`
	LastError    string  `json:"last_error"`
	CreatedAt    string  `json:"created_at"`
}

func connectedBoxToResponse(b db.ConnectedBox) ConnectedBoxResponse {
	resp := ConnectedBoxResponse{
		ID:           uuidToString(b.ID),
		WorkspaceID:  uuidToString(b.WorkspaceID),
		OwnerID:      uuidToPtr(b.OwnerID),
		Label:        b.Label,
		SSHHost:      b.SshHost,
		SSHUser:      b.SshUser,
		SSHPort:      b.SshPort,
		DeployPubkey: b.DeployPubkey,
		DaemonID:     uuidToPtr(b.DaemonID),
		Status:       b.Status,
		LastError:    b.LastError,
	}
	if b.CreatedAt.Valid {
		resp.CreatedAt = b.CreatedAt.Time.Format(time.RFC3339)
	}
	return resp
}

type CreateConnectedBoxRequest struct {
	Label   string `json:"label"`
	SSHHost string `json:"ssh_host"`
	SSHUser string `json:"ssh_user"`
	SSHPort int32  `json:"ssh_port"`
}

// ListConnectedBoxes returns the workspace's remote boxes (tenancy-scoped).
func (h *Handler) ListConnectedBoxes(w http.ResponseWriter, r *http.Request) {
	if !remoteBoxesEnabled() {
		writeError(w, http.StatusNotFound, "remote boxes are not enabled")
		return
	}
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	wsID := h.resolveWorkspaceID(r)
	if wsID == "" {
		writeError(w, http.StatusBadRequest, "workspace required")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace_id")
	if !ok {
		return
	}
	boxes, err := h.Queries.ListConnectedBoxesByWorkspace(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list remote boxes")
		return
	}
	out := make([]ConnectedBoxResponse, 0, len(boxes))
	for _, b := range boxes {
		out = append(out, connectedBoxToResponse(b))
	}
	writeJSON(w, http.StatusOK, map[string]any{"boxes": out})
}

// CreateConnectedBox registers a remote box (status=pending). Owned by the
// calling user; bootstrap/keypair generation happen in a later step.
func (h *Handler) CreateConnectedBox(w http.ResponseWriter, r *http.Request) {
	if !remoteBoxesEnabled() {
		writeError(w, http.StatusNotFound, "remote boxes are not enabled")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	wsID := h.resolveWorkspaceID(r)
	if wsID == "" {
		writeError(w, http.StatusBadRequest, "workspace required")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace_id")
	if !ok {
		return
	}
	var req CreateConnectedBoxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	req.SSHHost = strings.TrimSpace(req.SSHHost)
	req.SSHUser = strings.TrimSpace(req.SSHUser)
	if req.Label == "" || req.SSHHost == "" || req.SSHUser == "" {
		writeError(w, http.StatusBadRequest, "label, ssh_host and ssh_user are required")
		return
	}
	if req.SSHPort <= 0 {
		req.SSHPort = 22
	}
	box, err := h.Queries.CreateConnectedBox(r.Context(), db.CreateConnectedBoxParams{
		WorkspaceID:  wsUUID,
		OwnerID:      parseUUID(userID),
		Label:        req.Label,
		SshHost:      req.SSHHost,
		SshUser:      req.SSHUser,
		SshPort:      req.SSHPort,
		DeployPubkey: "",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create remote box")
		return
	}
	writeJSON(w, http.StatusCreated, connectedBoxToResponse(box))
}

// DeleteConnectedBox removes a remote box (tenancy-scoped). Deprovisioning the
// box's daemon/key is a control-plane step layered on later.
func (h *Handler) DeleteConnectedBox(w http.ResponseWriter, r *http.Request) {
	if !remoteBoxesEnabled() {
		writeError(w, http.StatusNotFound, "remote boxes are not enabled")
		return
	}
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	wsID := h.resolveWorkspaceID(r)
	if wsID == "" {
		writeError(w, http.StatusBadRequest, "workspace required")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace_id")
	if !ok {
		return
	}
	boxUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "box id")
	if !ok {
		return
	}
	if err := h.Queries.DeleteConnectedBox(r.Context(), db.DeleteConnectedBoxParams{
		ID:          boxUUID,
		WorkspaceID: wsUUID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete remote box")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
