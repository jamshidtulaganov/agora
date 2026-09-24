package handler

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgtype"
)

// An agent task may reach Zoho only as the person it is working for, so that
// Zoho applies that person's own role, profile and sharing rules. There is
// deliberately no fallback — not the runtime owner, not the workspace's
// org-level connection: a task we can't attribute to a person gets no Zoho
// data at all.
//
// The person is found from what the task already records, most specific
// first:
//
//  1. initiator_user_id — whoever started the chat (or Telegram message);
//  2. the trigger comment's author, when a member @mentioned the agent;
//  3. the autopilot's creator, for autopilot runs;
//  4. the quick-create requester;
//  5. for issue work: the member who last assigned the issue to an agent or
//     squad, else the member who created it, else the same question asked of
//     the parent issue (agent-created sub-issues);
//  6. for retries and failovers: the same question asked of the parent task.

// zohoIdentityMaxDepth bounds the parent-issue / parent-task walk.
const zohoIdentityMaxDepth = 3

// zohoActingUserForTask returns the member a task acts for and a short
// reason ("assigned the issue") shown by zoho_whoami. ok=false means the task
// can't be attributed to a person.
func (h *Handler) zohoActingUserForTask(ctx context.Context, taskID pgtype.UUID) (pgtype.UUID, string, bool) {
	return h.zohoActingUserForTaskDepth(ctx, taskID, 0)
}

func (h *Handler) zohoActingUserForTaskDepth(ctx context.Context, taskID pgtype.UUID, depth int) (pgtype.UUID, string, bool) {
	task, err := h.Queries.GetAgentTask(ctx, taskID)
	if err != nil {
		return pgtype.UUID{}, "", false
	}
	if task.InitiatorUserID.Valid {
		return task.InitiatorUserID, "started this conversation", true
	}
	if task.TriggerCommentID.Valid {
		if c, err := h.Queries.GetComment(ctx, task.TriggerCommentID); err == nil && c.AuthorType == "member" && c.AuthorID.Valid {
			return c.AuthorID, "mentioned the agent", true
		}
	}
	if task.AutopilotRunID.Valid {
		if run, err := h.Queries.GetAutopilotRun(ctx, task.AutopilotRunID); err == nil {
			if ap, err := h.Queries.GetAutopilot(ctx, run.AutopilotID); err == nil && ap.CreatedByType == "member" && ap.CreatedByID.Valid {
				return ap.CreatedByID, "set up the autopilot", true
			}
		}
	}
	if requester, ok := quickCreateRequester(task.Context); ok {
		return requester, "asked for the issue", true
	}
	if task.IssueID.Valid {
		if user, reason, ok := h.zohoActingUserForIssue(ctx, task.IssueID, depth); ok {
			return user, reason, true
		}
	}
	if task.ParentTaskID.Valid && depth < zohoIdentityMaxDepth {
		return h.zohoActingUserForTaskDepth(ctx, task.ParentTaskID, depth+1)
	}
	return pgtype.UUID{}, "", false
}

func (h *Handler) zohoActingUserForIssue(ctx context.Context, issueID pgtype.UUID, depth int) (pgtype.UUID, string, bool) {
	if assigner, err := h.Queries.LatestMemberAssignerOfIssue(ctx, issueID); err == nil && assigner.Valid {
		return assigner, "assigned the issue", true
	}
	issue, err := h.Queries.GetIssue(ctx, issueID)
	if err != nil {
		return pgtype.UUID{}, "", false
	}
	if issue.CreatorType == "member" && issue.CreatorID.Valid {
		return issue.CreatorID, "created the issue", true
	}
	if issue.ParentIssueID.Valid && depth < zohoIdentityMaxDepth {
		return h.zohoActingUserForIssue(ctx, issue.ParentIssueID, depth+1)
	}
	return pgtype.UUID{}, "", false
}

// quickCreateRequester reads the requester of a quick-create task, whose
// whole job description lives in the context JSON.
func quickCreateRequester(raw []byte) (pgtype.UUID, bool) {
	if len(raw) == 0 {
		return pgtype.UUID{}, false
	}
	var qc struct {
		Type        string `json:"type"`
		RequesterID string `json:"requester_id"`
	}
	if err := json.Unmarshal(raw, &qc); err != nil || qc.Type != "quick_create" {
		return pgtype.UUID{}, false
	}
	var id pgtype.UUID
	if err := id.Scan(qc.RequesterID); err != nil || !id.Valid {
		return pgtype.UUID{}, false
	}
	return id, true
}

// zohoTaskIdentityForRequest resolves the acting member for a task-token
// request (X-Task-ID is written by the auth middleware from the token, never
// taken from the client).
func (h *Handler) zohoTaskIdentityForRequest(ctx context.Context, taskIDHeader string) (pgtype.UUID, string, bool) {
	var taskID pgtype.UUID
	if err := taskID.Scan(taskIDHeader); err != nil || !taskID.Valid {
		return pgtype.UUID{}, "", false
	}
	return h.zohoActingUserForTask(ctx, taskID)
}
