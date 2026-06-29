package handler

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// SteerIssueRequest is the body of POST /api/issues/{id}/steer.
type SteerIssueRequest struct {
	// AgentID is the agent to steer — the one currently running on the issue.
	AgentID string `json:"agent_id"`
	// CommentID links the steering comment the client just posted. The agent
	// reads it as its next message when the session resumes.
	CommentID string `json:"comment_id,omitempty"`
}

// SteerIssue queues a "steer" — a follow-up run for an agent that is mid-task on
// the issue. A plain @mention posted while a task is in-flight is deduped (the
// HasPendingTaskForIssueAndAgent guard) precisely to prevent runaway loops, so
// it would be dropped; this endpoint is the explicit-user-intent escape hatch:
// it force-enqueues a resuming task. When the current turn ends, the agent picks
// it up, resumes its session via the proven completed→GetLastTaskSession path
// (keeping full context + the worktree), and reads the steering comment as its
// next instruction. The running turn is NOT interrupted — this is "queued
// steer", applied the moment the current turn finishes.
func (h *Handler) SteerIssue(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, id)
	if !ok {
		return
	}

	var req SteerIssueRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
	}

	agentID, ok := parseUUIDOrBadRequest(w, req.AgentID, "agent_id")
	if !ok {
		return
	}
	// Tenant guard: the agent must belong to the issue's workspace.
	if _, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
		ID:          agentID,
		WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	var commentID pgtype.UUID
	if req.CommentID != "" {
		parsed, parsedOK := parseUUIDOrBadRequest(w, req.CommentID, "comment_id")
		if !parsedOK {
			return
		}
		commentID = parsed
	}

	task, err := h.TaskService.EnqueueTaskForMention(r.Context(), issue, agentID, commentID)
	if err != nil {
		slog.Warn("issue steer failed", "issue_id", id, "agent_id", req.AgentID, "error", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, taskToResponse(task, uuidToString(issue.WorkspaceID)))
}
