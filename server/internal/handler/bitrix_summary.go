package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/bitrix"
	"github.com/jamshidtulaganov/agora/server/internal/logger"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// bitrixSummaryPushedMetaKey records (RFC3339) when a final summary was last
// posted to the linked Bitrix task — the in-app "last posted" hint + audit.
const bitrixSummaryPushedMetaKey = "bitrix_summary_pushed"

type postBitrixSummaryRequest struct {
	Text string `json:"text"`
}

// PostBitrixSummary posts an agent's final summary (branch name, bug causes, …)
// as a comment onto the issue's linked Bitrix task. It is the HUMAN approval gate
// for that outbound write:
//   - the route is wrapped in RequireHumanActor, so an agent can never self-post
//     its own summary — a human must trigger it (the whole point of the gate);
//   - the caller must be a workspace member (loadIssueForUser);
//   - the summary text is supplied by the human — the UI prefills it from the
//     agent's final comment + the integration artifact and human reviews
//     before submitting, so nothing reaches Bitrix without this explicit action.
//
// POST /api/issues/{id}/bitrix-summary.
func (h *Handler) PostBitrixSummary(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	if !bitrixEndpointsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "bitrix integration not configured")
		return
	}

	taskID := bitrixTaskIDFromMetadata(issue.Metadata)
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "this issue is not linked to a Bitrix task")
		return
	}

	var req postBitrixSummaryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		writeError(w, http.StatusBadRequest, "summary text is required")
		return
	}

	// Post to Bitrix. The header marks the comment as an Agora-originated final
	// summary so a Bitrix reader knows its provenance; the body is the
	// human-reviewed text verbatim.
	prefix := h.getIssuePrefix(r.Context(), issue.WorkspaceID)
	body := fmt.Sprintf("🤖 Agora — final summary (%s-%d):\n\n%s", prefix, issue.Number, text)
	if err := bitrix.NewClient(bitrixWebhookURL()).AddTaskComment(r.Context(), taskID, body); err != nil {
		slog.Warn("bitrix summary: add task comment failed",
			append(logger.RequestAttrs(r), "error", err, "issue_id", uuidToString(issue.ID))...)
		writeError(w, http.StatusBadGateway, "failed to post the summary to Bitrix")
		return
	}

	postedAt := time.Now().UTC().Format(time.RFC3339)
	if val, err := json.Marshal(postedAt); err == nil {
		if _, err := h.Queries.SetIssueMetadataKey(r.Context(), db.SetIssueMetadataKeyParams{
			ID:          issue.ID,
			WorkspaceID: issue.WorkspaceID,
			Key:         bitrixSummaryPushedMetaKey,
			Value:       val,
		}); err != nil {
			slog.Warn("bitrix summary: stamp pushed failed", "issue_id", uuidToString(issue.ID), "error", err)
		}
	}

	// In-app audit trail: a system comment recording the human-approved push, so
	// the team sees on the issue that the summary went out to Bitrix.
	h.postBitrixSummarySystemComment(r, issue)

	writeJSON(w, http.StatusOK, map[string]any{"status": "posted", "posted_at": postedAt})
}

// postBitrixSummarySystemComment records the human-approved Bitrix push as a
// system comment on the issue (best-effort; a failure never fails the request).
func (h *Handler) postBitrixSummarySystemComment(r *http.Request, issue db.Issue) {
	comment, err := h.Queries.CreateComment(r.Context(), db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "system",
		AuthorID:    pgtype.UUID{Valid: true},
		Content:     "📤 Final summary posted to the linked Bitrix task.",
		Type:        "system",
		ParentID:    pgtype.UUID{Valid: false},
	})
	if err != nil {
		slog.Warn("bitrix summary: system comment failed",
			append(logger.RequestAttrs(r), "error", err, "issue_id", uuidToString(issue.ID))...)
		return
	}
	h.publish(protocol.EventCommentCreated, uuidToString(issue.WorkspaceID), "system", "", map[string]any{
		"comment": commentToResponse(comment, nil, nil),
	})
}
