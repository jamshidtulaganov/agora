package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// The design-review endpoint is the human approval gate for a design proposal.
// It is the canonical, atomic entrance: one call swaps the design-state label,
// records the reviewer's note + per-sub-issue overrides, posts a comment, and
// (from Phase 4) drives decomposition. Labels remain the human-visible state so
// the issue list can filter on design:proposed / design:approved /
// design:changes_requested.

// designSubIssueOverride lets a reviewer trim or edit the proposed sub-issues at
// approval time. index points into the proposal's sub_issues array; include=false
// drops that slice; title/description override the agent's text. Phase 2 accepts
// and echoes these; Phase 4 applies them during decomposition.
type designSubIssueOverride struct {
	Index       int     `json:"index"`
	Include     bool    `json:"include"`
	Title       *string `json:"title"`
	Description *string `json:"description"`
}

type createDesignReviewRequest struct {
	Action            string                   `json:"action"` // approve | request_changes
	Note              string                   `json:"note"`
	SubIssueOverrides []designSubIssueOverride `json:"sub_issue_overrides"`
}

// CreateDesignReview handles POST /api/issues/{id}/design-review.
func (h *Handler) CreateDesignReview(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req createDesignReviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Action = strings.TrimSpace(req.Action)
	if req.Action != "approve" && req.Action != "request_changes" {
		writeError(w, http.StatusBadRequest, "action must be 'approve' or 'request_changes'")
		return
	}

	proposal, sourceCommentID, state, err := h.TaskService.LatestDesignProposalForIssue(r.Context(), issue)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load design proposal")
		return
	}
	switch state {
	case service.DesignProposalStateNone:
		writeError(w, http.StatusNotFound, "no_design_proposal: this issue has no design proposal to review")
		return
	case service.DesignProposalStateInvalid:
		writeError(w, http.StatusConflict, "design_proposal_unparseable: the latest proposal could not be parsed — re-run the design proposal")
		return
	}

	if req.Action == "approve" {
		h.approveDesignProposal(w, r, issue, userID, proposal, sourceCommentID, state, req)
		return
	}
	h.requestDesignChanges(w, r, issue, userID, req)
}

// approveDesignProposal swaps the state label to design:approved, records the
// approval (note + overrides) as an activity + system comment, and invokes the
// decomposition seam (a no-op until Phase 4). A blocked proposal cannot be
// approved — there is nothing to build from.
func (h *Handler) approveDesignProposal(w http.ResponseWriter, r *http.Request, issue db.Issue, userID string, proposal service.DesignProposal, sourceCommentID pgtype.UUID, state service.DesignProposalState, req createDesignReviewRequest) {
	if state == service.DesignProposalStateBlocked {
		writeError(w, http.StatusConflict, "design_proposal_blocked: the proposal is blocked (the design was unreadable) — resolve the blocker and re-run before approving")
		return
	}

	if err := h.TaskService.SetDesignStateLabel(r.Context(), issue, service.DesignLabelApproved); err != nil {
		slog.Warn("design review: set approved label failed", append(logger.RequestAttrs(r), "error", err, "issue_id", uuidToString(issue.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to update design state")
		return
	}

	overrides, _ := json.Marshal(req.SubIssueOverrides)
	h.TaskService.RecordDesignReviewActivity(r.Context(), issue, parseUUID(userID), "design_approved", map[string]any{
		"source_comment_id": uuidToString(sourceCommentID),
		"note":              req.Note,
		"overrides":         json.RawMessage(overrides),
	})

	note := strings.TrimSpace(req.Note)
	sysBody := "✅ Design proposal approved."
	if note != "" {
		sysBody += " Reviewer note: " + note
	}
	h.postDesignSystemComment(r, issue, sysBody)

	// Decomposition seam: Phase 4 turns the approved (override-filtered) proposal
	// into real sub-issues here. No-op until then.
	h.decomposeApprovedProposal(r, issue, userID, proposal, sourceCommentID, req.SubIssueOverrides)

	writeJSON(w, http.StatusOK, map[string]any{"action": "approve", "state": "design:approved"})
}

// requestDesignChanges swaps the state label to design:changes_requested and
// @mentions the designer agent to revise, routed through the canonical
// comment-trigger path so exactly one task is queued.
func (h *Handler) requestDesignChanges(w http.ResponseWriter, r *http.Request, issue db.Issue, userID string, req createDesignReviewRequest) {
	designer, ok := h.resolveDesignerAgent(r.Context(), issue)
	// Gate a private designer the caller can't access exactly like the
	// slice-action path does: never write its name/UUID into a comment the
	// caller can read, and never (silently) fail to dispatch. Treat it as
	// "no designer available" — indistinguishable from truly having none.
	if !ok || !h.canAccessPrivateAgent(r.Context(), designer, "member", userID, uuidToString(issue.WorkspaceID)) {
		writeError(w, http.StatusConflict, "no_designer_available: no design agent or design squad leader is configured to revise the proposal")
		return
	}

	if err := h.TaskService.SetDesignStateLabel(r.Context(), issue, service.DesignLabelChangesRequested); err != nil {
		slog.Warn("design review: set changes_requested label failed", append(logger.RequestAttrs(r), "error", err, "issue_id", uuidToString(issue.ID))...)
		writeError(w, http.StatusInternalServerError, "failed to update design state")
		return
	}

	note := strings.TrimSpace(req.Note)
	content := fmt.Sprintf("[@%s](mention://agent/%s) ", sanitizeMentionLabel(designer.Name), uuidToString(designer.ID)) +
		"Changes were requested on your design proposal. Read the latest human comments, revise your analysis, and " +
		"post a NEW ```design-proposal``` block."
	if note != "" {
		content += " Reviewer note: " + note
	}
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
		writeError(w, http.StatusInternalServerError, "failed to create changes-requested comment")
		return
	}
	resp := commentToResponse(comment, nil, nil)
	h.publish(protocol.EventCommentCreated, uuidToString(issue.WorkspaceID), "member", userID, map[string]any{
		"comment":      resp,
		"issue_title":  issue.Title,
		"issue_status": issue.Status,
	})
	h.triggerTasksForComment(r.Context(), issue, comment, nil, "member", userID, nil)

	writeJSON(w, http.StatusOK, map[string]any{"action": "request_changes", "state": "design:changes_requested"})
}

// postDesignSystemComment writes a system comment and broadcasts it (mirrors the
// QA watchdog's system-comment pattern).
func (h *Handler) postDesignSystemComment(r *http.Request, issue db.Issue, body string) {
	comment, err := h.Queries.CreateComment(r.Context(), db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "system",
		AuthorID:    pgtype.UUID{Valid: true},
		Content:     body,
		Type:        "system",
		ParentID:    pgtype.UUID{Valid: false},
	})
	if err != nil {
		slog.Warn("design review: system comment failed", append(logger.RequestAttrs(r), "error", err, "issue_id", uuidToString(issue.ID))...)
		return
	}
	h.publish(protocol.EventCommentCreated, uuidToString(issue.WorkspaceID), "system", "", map[string]any{
		"comment": commentToResponse(comment, nil, nil),
	})
}

// decomposeApprovedProposal is the seam Phase 4 fills to create sub-issues from
// the approved, override-filtered proposal. No-op in Phase 2 so the approval
// flow ships end-to-end (label + notification + audit) before decomposition.
func (h *Handler) decomposeApprovedProposal(r *http.Request, issue db.Issue, userID string, proposal service.DesignProposal, sourceCommentID pgtype.UUID, overrides []designSubIssueOverride) {
	// Intentionally empty until Phase 4.
}
