package handler

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/util"
)

func assistantIssueCreator(ctx context.Context, userID string) pgtype.UUID {
	exec := assistantExecutionFrom(ctx)
	if exec == nil || exec.tool != assistant.ToolCreateIssue { return pgtype.UUID{} }
	id, err := util.ParseUUID(userID)
	if err != nil { return pgtype.UUID{} }
	return id
}

// Called both before offering a confirmation and, under a row lock, before
// deleting. Includes archived/hidden children: visibility cannot weaken safety.
func checkAssistantIssueDeletion(ctx context.Context, store dbExecutor, userID string, issueID, workspaceID pgtype.UUID) error {
	var created, children bool
	if err := store.QueryRow(ctx, `SELECT
        EXISTS(SELECT 1 FROM assistant_issue_creation c JOIN issue i ON i.id=c.issue_id
               WHERE c.issue_id=$1 AND c.user_id=$2 AND i.workspace_id=$3
                 AND i.creator_type='member' AND i.creator_id=c.user_id),
        EXISTS(SELECT 1 FROM issue WHERE parent_issue_id=$1)`, issueID, userID, workspaceID).Scan(&created, &children); err != nil {
		return errors.New("could not verify issue deletion safety; no deletion is allowed")
	}
	if !created { return errors.New("the assistant may only delete issues it created for you; existing or other people's issues are protected") }
	if children { return errors.New("the assistant cannot delete an issue with subtasks, including archived subtasks") }
	return nil
}
