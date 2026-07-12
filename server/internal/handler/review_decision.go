package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// POST /api/issues/{id}/review-decision — the HUMAN half of "agent reviews,
// human approves" (Review stage v2). The agent's run_review verdict is
// advisory + label-backed; the merge itself happens only after a human clicks
// one of two buttons this endpoint backs:
//
//   - approve: verify the deterministic gates (qa:pass present, review:fail
//     absent — merge:override bypasses both), attach merge:approved, record
//     the decision as a system comment, and dispatch the ACTUAL merge as a
//     member-authored @mention comment ordering the dev squad leader to
//     `gh pr merge` — the same instruction shape maybeMergeOnQAPass's
//     auto-merge branch uses, but human-gated.
//   - request_changes: a non-empty note is required; the issue drops back to
//     in_progress and the AUTHOR agent (the issue's assignee; fallback the
//     dev squad leader) is @mentioned with the note + a pointer to the latest
//     review findings. review:fail is deliberately KEPT — only a re-review's
//     replace-on-write verdict clears it.
//
// Route-gated by RequireHumanActor (a machine credential can never approve a
// merge or reject a review — modeled on qa-override) and membership-checked
// via loadIssueForUser.

// mergeApprovedLabel marks a human's Approve & merge decision. Distinct from
// merge:override (the escape hatch that FORCES done past an unmerged PR):
// merge:approved asserts "a human reviewed the gates and ordered the merge".
// Brand blue — a human decision, not a pass/fail verdict.
const (
	mergeApprovedLabel      = "merge:approved"
	mergeApprovedLabelColor = "#2563eb"
)

// reviewDecisionNoteMaxRunes caps the note at a comment-sized text.
const reviewDecisionNoteMaxRunes = 2000

type reviewDecisionRequest struct {
	Action string `json:"action"` // "approve" | "request_changes"
	Note   string `json:"note"`
}

// CreateReviewDecision handles POST /api/issues/{id}/review-decision.
func (h *Handler) CreateReviewDecision(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	var req reviewDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	action := strings.TrimSpace(req.Action)
	if action != "approve" && action != "request_changes" {
		writeError(w, http.StatusBadRequest, "action must be 'approve' or 'request_changes'")
		return
	}
	note := strings.TrimSpace(req.Note)
	if runes := []rune(note); len(runes) > reviewDecisionNoteMaxRunes {
		note = string(runes[:reviewDecisionNoteMaxRunes-1]) + "…"
	}

	userUUID := parseUUID(userID)
	userName := "a teammate"
	if u, err := h.Queries.GetUser(r.Context(), userUUID); err == nil && strings.TrimSpace(u.Name) != "" {
		userName = u.Name
	}

	if action == "approve" {
		h.approveReviewDecision(w, r, issue, userID, userName, note)
		return
	}
	h.requestReviewChanges(w, r, issue, userID, userName, note)
}

// approveReviewDecision verifies the gates, stamps merge:approved, and
// dispatches the merge order to the dev squad leader.
func (h *Handler) approveReviewDecision(w http.ResponseWriter, r *http.Request, issue db.Issue, userID, userName, note string) {
	ctx := r.Context()

	// Gate check — merge:override is the human's explicit bypass (they already
	// accepted responsibility for the gates when they attached it).
	if !h.issueHasLabel(ctx, issue, sprintPRMergeOverrideLabel) {
		if h.issueHasLabel(ctx, issue, "qa:fail") {
			writeError(w, http.StatusConflict, "qa_failed: the QA gate is failing (qa:fail) — fix and re-run QA before approving the merge")
			return
		}
		if !h.issueHasLabel(ctx, issue, "qa:pass") {
			writeError(w, http.StatusConflict, "qa_gate_not_passed: no qa:pass verdict yet — run QA before approving the merge")
			return
		}
		if h.issueHasLabel(ctx, issue, service.ReviewLabelFail) {
			writeError(w, http.StatusConflict, "review_failed: the code review found blockers (review:fail) — request changes or re-run the review before approving")
			return
		}
	}

	// Stamp the decision label (idempotent: a repeat approve re-dispatches the
	// merge order but never duplicates the label).
	if !h.issueHasLabel(ctx, issue, mergeApprovedLabel) {
		labelID, err := h.ensureLabel(ctx, issue.WorkspaceID, mergeApprovedLabel, mergeApprovedLabelColor)
		if err != nil {
			slog.Warn("review decision: ensure merge:approved failed", "error", err, "issue_id", uuidToString(issue.ID))
			writeError(w, http.StatusInternalServerError, "failed to record the approval")
			return
		}
		if err := h.Queries.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
			IssueID: issue.ID, LabelID: labelID, WorkspaceID: issue.WorkspaceID,
		}); err != nil {
			slog.Warn("review decision: attach merge:approved failed", "error", err, "issue_id", uuidToString(issue.ID))
			writeError(w, http.StatusInternalServerError, "failed to record the approval")
			return
		}
		h.publish(protocol.EventIssueLabelsChanged, uuidToString(issue.WorkspaceID), "member", userID, map[string]any{
			"issue_id": uuidToString(issue.ID),
		})
	}

	// The decision in prose — a system comment so every surface that renders
	// the timeline carries the record.
	sysBody := "✅ Merge approved by " + userName + "."
	if note != "" {
		sysBody += " Note: " + note
	}
	h.postDesignSystemComment(r, issue, sysBody)

	// Dispatch the actual merge to the dev squad leader as a member-authored
	// @mention (the human's click IS the authorization — same instruction
	// contract as maybeMergeOnQAPass's auto-merge branch). Risk-tier
	// critical/guarded work is NOT refused here — a human decided — but the
	// order carries cautious wording so the lead double-checks the diff scope.
	mergedDispatch := false
	if leader, ok := h.devSquadLeaderForIssue(ctx, issue); ok && sliceAgentReady(leader) {
		branchClause := "the branch this PR targets"
		if sprint, err := h.Queries.GetSprintForIssue(ctx, issue.ID); err == nil {
			if b := SprintBranchFor(sprint); b != "" {
				branchClause = "`" + b + "`"
			}
		}
		content := fmt.Sprintf("[@%s](mention://agent/%s) ", sanitizeMentionLabel(leader.Name), uuidToString(leader.ID)) +
			"A HUMAN (" + userName + ") reviewed the gates and APPROVED the merge of this issue's pull request. " +
			"Find the task's open PR (`gh pr list --state open`, or the PR named in this issue's metadata/context) and MERGE it into " + branchClause +
			" with `gh pr merge <pr> --squash --delete-branch`. Do NOT re-review or second-guess the decision — the human approval is the final gate. " +
			"Never target the repository's main/default branch unless that IS the PR's base."
		if tier := h.issueRiskTier(ctx, issue); tier == "critical" || tier == "guarded" {
			content += " CAUTION: this issue is RISK TIER " + strings.ToUpper(tier) +
				" — before merging, VERIFY you are merging exactly the reviewed PR (number + head SHA) and nothing else; if the PR has changed since the review, STOP and report instead of merging."
		}
		comment, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
			IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
			AuthorType: "member", AuthorID: parseUUID(userID),
			Content: content, Type: "comment", ParentID: pgtype.UUID{Valid: false},
		})
		if err != nil {
			slog.Warn("review decision: merge dispatch comment failed", "error", err, "issue_id", uuidToString(issue.ID))
		} else {
			h.publish(protocol.EventCommentCreated, uuidToString(issue.WorkspaceID), "member", userID, map[string]any{
				"comment":      commentToResponse(comment, nil, nil),
				"issue_title":  issue.Title,
				"issue_status": issue.Status,
			})
			h.triggerTasksForComment(ctx, issue, comment, nil, "member", userID, nil)
			mergedDispatch = true
		}
	}

	slog.Info("review decision: merge approved",
		"issue_id", uuidToString(issue.ID), "user_id", userID, "merge_dispatched", mergedDispatch)
	writeJSON(w, http.StatusOK, map[string]any{"action": "approve", "merged_dispatch": mergedDispatch})
}

// requestReviewChanges routes the review findings back to the author agent
// and drops the issue to in_progress. review:fail (when present) is KEPT —
// only the next review's replace-on-write verdict clears it.
func (h *Handler) requestReviewChanges(w http.ResponseWriter, r *http.Request, issue db.Issue, userID, userName, note string) {
	ctx := r.Context()
	if note == "" {
		writeError(w, http.StatusBadRequest, "note is required for request_changes")
		return
	}

	// The AUTHOR agent gets the work back: the issue's agent assignee,
	// falling back to the dev squad leader (who re-delegates).
	var author db.Agent
	resolved := false
	if issue.AssigneeType.Valid && issue.AssigneeType.String == "agent" && issue.AssigneeID.Valid {
		if a, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID: issue.AssigneeID, WorkspaceID: issue.WorkspaceID,
		}); err == nil && sliceAgentReady(a) {
			author, resolved = a, true
		}
	}
	if !resolved {
		if leader, ok := h.devSquadLeaderForIssue(ctx, issue); ok && sliceAgentReady(leader) {
			author, resolved = leader, true
		}
	}

	if _, err := h.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
		ID: issue.ID, Status: "in_progress", WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		slog.Warn("review decision: status reset failed", "error", err, "issue_id", uuidToString(issue.ID))
		writeError(w, http.StatusInternalServerError, "failed to move the issue back to in_progress")
		return
	}

	body := "Changes were requested on this implementation by " + userName + ". Note: " + note +
		" Read the latest review findings on this issue (the newest comment carrying a ```review-result``` block lists every finding with file, line, and severity), " +
		"address every blocker, then move the issue back to in_review so QA and the review re-run."
	content := body
	if resolved {
		content = fmt.Sprintf("[@%s](mention://agent/%s) ", sanitizeMentionLabel(author.Name), uuidToString(author.ID)) + body
	}
	comment, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
		AuthorType: "member", AuthorID: parseUUID(userID),
		Content: content, Type: "comment", ParentID: pgtype.UUID{Valid: false},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create the request-changes comment")
		return
	}
	h.publish(protocol.EventCommentCreated, uuidToString(issue.WorkspaceID), "member", userID, map[string]any{
		"comment":      commentToResponse(comment, nil, nil),
		"issue_title":  issue.Title,
		"issue_status": "in_progress",
	})
	if resolved {
		h.triggerTasksForComment(ctx, issue, comment, nil, "member", userID, nil)
	}

	slog.Info("review decision: changes requested",
		"issue_id", uuidToString(issue.ID), "user_id", userID, "author_dispatched", resolved)
	writeJSON(w, http.StatusOK, map[string]any{"action": "request_changes", "status": "in_progress", "dispatched": resolved})
}
