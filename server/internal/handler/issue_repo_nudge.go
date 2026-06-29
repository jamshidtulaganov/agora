package handler

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// noRepoBoundNudgeKey marks (in issue metadata) that the one-time "no
// repository connected" courtesy comment has already been posted for this
// issue, so a re-claimed / retried task does not repost it.
const noRepoBoundNudgeKey = "no_repo_bound_nudge_posted"

// projectResourcesHaveCode reports whether any resolved project resource gives
// the agent real code to work on — a github repo or a bound local directory.
func projectResourcesHaveCode(resources []ProjectResourceData) bool {
	for _, r := range resources {
		if r.ResourceType == "github_repo" || r.ResourceType == "local_directory" {
			return true
		}
	}
	return false
}

// postNoRepoBoundNudge posts a one-time, non-blocking system comment on an
// issue whose project has no repository bound. Such an issue's agent runs in a
// slim worktree with no project code, and nothing tells the human why — this
// nudges them to connect a repo (the "Repository" section in the issue
// sidebar, or project settings). Mirrors to Telegram/Bitrix via the normal
// comment path. Idempotent via issue metadata; every error is logged and
// swallowed so it never affects the task claim that triggered it.
func (h *Handler) postNoRepoBoundNudge(ctx context.Context, issue db.Issue) {
	if metaString(issue.Metadata, noRepoBoundNudgeKey) == "true" {
		return
	}

	content := "⚠️ This project has no repository connected, so I have no code to work on. " +
		"Connect a GitHub/GitLab repo in the **Repository** section of this issue " +
		"(or in the project settings) so agents can clone, edit and open PRs."

	// author_type='system', author_id=zero UUID — same convention as the
	// child-done notification; frontend branches on author_type, not the id.
	comment, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "system",
		AuthorID:    pgtype.UUID{Valid: true},
		Content:     content,
		Type:        "system",
		ParentID:    pgtype.UUID{Valid: false},
	})
	if err != nil {
		slog.Warn("no-repo nudge: create system comment failed",
			"error", err, "issue_id", uuidToString(issue.ID))
		return
	}

	h.publish(protocol.EventCommentCreated, uuidToString(issue.WorkspaceID), "system", "", map[string]any{
		"comment":             commentToResponse(comment, nil, nil),
		"issue_title":         issue.Title,
		"issue_assignee_type": textToPtr(issue.AssigneeType),
		"issue_assignee_id":   uuidToPtr(issue.AssigneeID),
		"issue_status":        issue.Status,
	})

	// Stamp the idempotency flag so retries / re-claims don't repost.
	val, _ := json.Marshal("true")
	if _, err := h.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
		Key:         noRepoBoundNudgeKey,
		Value:       val,
		ID:          issue.ID,
		WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		slog.Warn("no-repo nudge: stamp metadata failed",
			"error", err, "issue_id", uuidToString(issue.ID))
	}
}
