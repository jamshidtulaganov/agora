package handler

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/bitrix"
)

// Read-only Bitrix task surface for agents.
//
// The analytics endpoint answers "how many"; these answer "what is task 4711
// and what was said on it" — the questions a group actually asks an agent. They
// complete the read side and stop there: there is no route here that writes to
// Bitrix, because a Bitrix task is a company-wide, outward-facing record and an
// agent posting into one is not an action that can be quietly undone.
//
// Same principle as bitrix_analytics.go: the webhook URL never leaves the
// server. It is a full REST key — an agent holding it could do anything the
// portal allows, and it would travel into every comment, log line and Telegram
// message that agent writes. Instead the server holds the credential, the agent
// authenticates as itself, and the response carries data only.

// bitrixTaskResponse is the wire shape for one task. Deliberately flat and
// small: an agent summarising a task does not need the portal's full payload,
// and a large one crowds out the reasoning it is supposed to be doing.
type bitrixTaskResponse struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Description   string   `json:"description,omitempty"`
	Status        string   `json:"status"`
	StatusLabel   string   `json:"status_label"`
	ResponsibleID string   `json:"responsible_id,omitempty"`
	GroupID       string   `json:"group_id,omitempty"`
	GroupName     string   `json:"group_name,omitempty"`
	StageID       string   `json:"stage_id,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	CreatedAt     string   `json:"created_at,omitempty"`
	ClosedAt      string   `json:"closed_at,omitempty"`
}

// bitrixCommentResponse is one comment on a task.
type bitrixCommentResponse struct {
	ID       string `json:"id"`
	Author   string `json:"author,omitempty"`
	AuthorID string `json:"author_id,omitempty"`
	Date     string `json:"date,omitempty"`
	Text     string `json:"text"`
}

// GetBitrixTask handles GET /api/bitrix/tasks/{id}.
func (h *Handler) GetBitrixTask(w http.ResponseWriter, r *http.Request) {
	if !h.requireBitrixOperator(w, r) {
		return
	}
	if !bitrixEndpointsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "bitrix integration not configured")
		return
	}
	taskID := strings.TrimSpace(chi.URLParam(r, "id"))
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "task id is required")
		return
	}

	client := bitrix.NewClient(bitrixWebhookURL())
	task, err := client.GetTask(r.Context(), taskID)
	if err != nil {
		// The portal's own error text can carry the webhook URL back in some
		// failure modes, so it is logged rather than returned.
		writeError(w, http.StatusBadGateway, "failed to read the task from Bitrix")
		return
	}

	resp := bitrixTaskResponse{
		ID:            task.ID,
		Title:         task.Title,
		Description:   task.Description,
		Status:        task.Status,
		StatusLabel:   bitrixStatusBucket(task.Status),
		ResponsibleID: task.ResponsibleID,
		GroupID:       task.GroupID,
		GroupName:     task.GroupName,
		StageID:       task.StageID,
		Tags:          task.Tags,
		CreatedAt:     task.CreatedAt,
		ClosedAt:      task.ClosedAt,
	}
	writeJSON(w, http.StatusOK, resp)
}

// ListBitrixTaskComments handles GET /api/bitrix/tasks/{id}/comments.
//
// Newer tasks keep their discussion in a chat, older ones in the legacy comment
// feed. GetTaskComments already resolves which; the agent should not have to
// know a task's vintage to read what was said on it.
func (h *Handler) ListBitrixTaskComments(w http.ResponseWriter, r *http.Request) {
	if !h.requireBitrixOperator(w, r) {
		return
	}
	if !bitrixEndpointsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "bitrix integration not configured")
		return
	}
	taskID := strings.TrimSpace(chi.URLParam(r, "id"))
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "task id is required")
		return
	}

	client := bitrix.NewClient(bitrixWebhookURL())
	comments, err := client.GetTaskComments(r.Context(), taskID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to read comments from Bitrix")
		return
	}

	out := make([]bitrixCommentResponse, 0, len(comments))
	for _, c := range comments {
		out = append(out, bitrixCommentResponse{
			ID: c.ID, Author: c.Author, AuthorID: c.AuthorID, Date: c.Date, Text: c.Text,
		})
	}
	// Always an array, never null: a client that treats null as an error would
	// report "failed to read" for a task that simply has no discussion.
	writeJSON(w, http.StatusOK, map[string]any{"comments": out, "count": len(out)})
}
