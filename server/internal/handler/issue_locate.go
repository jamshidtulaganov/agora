package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/jamshidtulaganov/agora/server/internal/util"
)

// LocateIssueResponse tells a client which workspace an issue lives in, so a
// surface that isn't already scoped to that workspace (e.g. a Telegram deep
// link opened in the wrong/last workspace) can switch before loading it.
type LocateIssueResponse struct {
	IssueID       string `json:"issue_id"`
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceSlug string `json:"workspace_slug"`
}

// LocateIssue resolves an issue UUID to its workspace WITHOUT requiring the
// X-Workspace header to already match — it looks the issue up globally, then
// gates on the caller's membership of that workspace. This is the fix for
// cross-workspace deep links (#cross-ws): the normal issue routes are
// workspace-scoped and 404 when the active workspace differs from the issue's.
//
// Only UUIDs are accepted (deep links carry the UUID); a human identifier like
// "OCT-1244" can't be resolved without already knowing the workspace, so it
// 404s here and the caller falls back to its existing scoped lookup.
func (h *Handler) LocateIssue(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	issueUUID, err := util.ParseUUID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}

	issue, err := h.Queries.GetIssue(r.Context(), issueUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}

	wsID := util.UUIDToString(issue.WorkspaceID)
	// Membership gate: never reveal an issue's workspace to a non-member.
	if _, err := h.getWorkspaceMember(r.Context(), userID, wsID); err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}

	ws, err := h.Queries.GetWorkspace(r.Context(), issue.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}

	writeJSON(w, http.StatusOK, LocateIssueResponse{
		IssueID:       util.UUIDToString(issue.ID),
		WorkspaceID:   wsID,
		WorkspaceSlug: ws.Slug,
	})
}
