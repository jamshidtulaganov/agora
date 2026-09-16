package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jamshidtulaganov/agora/server/internal/logger"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// ArchiveIssueRequest is the body of POST /api/issues/{id}/archive. The body is
// optional: no body at all means "archive it", which is what a bare POST from a
// menu item sends.
type ArchiveIssueRequest struct {
	// Archived is a pointer so an absent field defaults to true (archive)
	// while an explicit `false` un-archives.
	Archived *bool `json:"archived"`
}

// ArchiveIssue hides an issue from lists and boards WITHOUT deleting it, and
// brings it back with {"archived": false}.
//
// The column and the list filters already existed (migration 151, the
// `include_archived` narg on ListIssues) — only the Bitrix sync wrote it, via
// Queries.SetIssueArchived. This endpoint is the human-facing half: the
// reversible alternative to DELETE, which is what makes "get this off my board"
// answerable without destroying the row.
//
// Idempotent, because SetIssueArchived only writes when the state actually
// flips: archiving an already-archived issue is a no-op, not an error.
func (h *Handler) ArchiveIssue(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	// The workspace gate and the non-owner visibility gate, in one call. The
	// resolved issue.ID is what the write below addresses — the raw URL string
	// never reaches a query.
	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}

	archived := true
	var req ArchiveIssueRequest
	// A malformed or empty body is not an error here: the default action is
	// unambiguous and a menu click carries no body.
	if err := json.NewDecoder(r.Body).Decode(&req); err == nil && req.Archived != nil {
		archived = *req.Archived
	}

	if err := h.Queries.SetIssueArchived(r.Context(), db.SetIssueArchivedParams{
		ID:       issue.ID,
		Archived: archived,
	}); err != nil {
		slog.Warn("ArchiveIssue failed", append(logger.RequestAttrs(r), "error", err, "issue_id", uuidToString(issue.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to archive issue")
		return
	}

	workspaceID := uuidToString(issue.WorkspaceID)
	updated, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
		ID:          issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		// The write landed; only the read-back failed. Report success with what
		// we already have rather than telling the caller the archive failed.
		updated = issue
	}
	resp := issueToResponse(updated, h.getIssuePrefix(r.Context(), issue.WorkspaceID))

	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	// issue:updated rather than issue:deleted — the row still exists, and a
	// client that treated this as a delete would drop it from caches it will
	// need again the moment the user un-archives.
	h.publish(protocol.EventIssueUpdated, workspaceID, actorType, actorID, map[string]any{
		"issue":            resp,
		"archived":         archived,
		"archived_changed": true,
	})
	slog.Info("issue archived", append(logger.RequestAttrs(r),
		"issue_id", uuidToString(issue.ID), "archived", archived, "workspace_id", workspaceID)...)

	writeJSON(w, http.StatusOK, map[string]any{"archived": archived, "issue": resp})
}
