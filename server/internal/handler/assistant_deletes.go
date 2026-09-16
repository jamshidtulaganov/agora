package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// assistantConfirmCardPreviewChars bounds the comment text quoted on a
// confirmation card. Long enough to recognise the comment, short enough that the
// card the user reads stays one glance.
const assistantConfirmCardPreviewChars = 80

// The assistant's DELETE tools.
//
// These exist because of the MAIN RULE (docs/agora-assistant-plan.md §0): the
// assistant does what the user can do, and a user can delete an issue. The
// earlier catalog refused outright and told people to go and click it
// themselves, which is not a safety property — it is a worse product with the
// same blast radius, since the user deletes the thing thirty seconds later
// anyway, unassisted and unrecorded.
//
// What makes that safe is not absence, it is the same two-gesture shape the UI
// has:
//
//  1. Every tool below resolves the target under the read gate FIRST, then
//     hands assistantAwaitConfirmation (assistant_operations.go) a summary
//     built from that resolved row. The first call therefore DELETES NOTHING:
//     it persists a pending operation and answers needs_confirmation, and the
//     row only goes when the user presses Confirm in the transcript. The
//     earlier design took the model's word for it through a `confirm: true`
//     argument, which is not a human gesture at all — the model writes that
//     boolean itself.
//  2. The delete then runs through the real HTTP handler with the RESOLVED
//     UUID. A non-owner who cannot SEE an issue gets "issue not found", never
//     a delete; and the raw model-supplied string never reaches a DELETE query
//     (the handler UUID-parsing convention, whose whole origin is #1661 — a
//     204 on a delete that matched zero rows).
//
// Reversible alternatives stay one call away and the tool descriptions point at
// them: archive_issue, remove_issue_label, set_automation_enabled(false).

// The argument structs no longer carry a Confirm field, and the schemas no
// longer offer one: a boolean the model fills in was never evidence of a human
// decision, and leaving it in place would let a future reader mistake it for
// the gate. The gate is the pending operation.
type assistantDeleteIssueArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Ref         string `json:"ref"`
}

func (h *Handler) assistantDeleteIssue(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantDeleteIssueArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, role, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	issue, err := h.assistantResolveIssue(ctx, caller, ws, role, args.Ref)
	if err != nil {
		return nil, err
	}
	// Read the identifier BEFORE the row goes, so the answer can name what was
	// deleted. After the DELETE there is nothing left to derive it from.
	identifier := h.getIssuePrefix(ctx, ws.ID) + "-" + strconv.Itoa(int(issue.Number))
	title := issue.Title

	out, err := h.assistantAwaitConfirmation(ctx, caller, raw, assistantOperationPlan{
		Tool: assistant.ToolDeleteIssue,
		Summary: "Permanently delete issue " + identifier + " “" + title + "” in " + ws.Name +
			", with its comments and attachments. This cannot be undone.",
		Workspace: ws,
		Target:    assistantOperationTarget{Type: "issue", Identifier: identifier, Title: title},
	})
	if out != nil || err != nil {
		return out, err
	}

	issueID := uuidToString(issue.ID)
	status, respBody := h.assistantInvoke(ctx, h.DeleteIssue, http.MethodDelete, "/api/issues/"+issueID,
		caller.ID, uuidToString(ws.ID), "", map[string]string{"id": issueID})
	if status != http.StatusNoContent && status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not delete the issue")
	}
	return json.Marshal(map[string]any{
		"deleted":          true,
		"issue_identifier": identifier,
		"title":            title,
	})
}

type assistantDeleteProjectArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Project     string `json:"project"`
}

func (h *Handler) assistantDeleteProject(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantDeleteProjectArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	project, err := h.assistantResolveProject(ctx, ws, args.Project)
	if err != nil {
		return nil, err
	}

	projectID := uuidToString(project.ID)
	out, err := h.assistantAwaitConfirmation(ctx, caller, raw, assistantOperationPlan{
		Tool: assistant.ToolDeleteProject,
		Summary: "Permanently delete project “" + project.Title + "” in " + ws.Name +
			". Its sprints and knowledge items go with it, and its issues are detached from it. This cannot be undone.",
		Workspace: ws,
		Target:    assistantOperationTarget{Type: "project", Identifier: projectID, Title: project.Title},
	})
	if out != nil || err != nil {
		return out, err
	}

	status, respBody := h.assistantInvoke(ctx, h.DeleteProject, http.MethodDelete, "/api/projects/"+projectID,
		caller.ID, uuidToString(ws.ID), "", map[string]string{"id": projectID})
	if status != http.StatusNoContent && status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not delete the project")
	}
	return json.Marshal(map[string]any{
		"deleted": true,
		"project": project.Title,
	})
}

type assistantDeleteSprintArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Sprint      string `json:"sprint"`
}

func (h *Handler) assistantDeleteSprint(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantDeleteSprintArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	sprint, err := h.assistantResolveSprint(ctx, ws, args.Sprint)
	if err != nil {
		return nil, err
	}

	sprintID := uuidToString(sprint.ID)
	out, err := h.assistantAwaitConfirmation(ctx, caller, raw, assistantOperationPlan{
		Tool: assistant.ToolDeleteSprint,
		Summary: "Permanently delete sprint “" + sprint.Name + "” in " + ws.Name +
			". Its issues survive but lose this sprint and its history. This cannot be undone.",
		Workspace: ws,
		Target:    assistantOperationTarget{Type: "sprint", Identifier: sprintID, Title: sprint.Name},
	})
	if out != nil || err != nil {
		return out, err
	}

	status, respBody := h.assistantInvoke(ctx, h.DeleteSprint, http.MethodDelete, "/api/sprints/"+sprintID,
		caller.ID, uuidToString(ws.ID), "", map[string]string{"id": sprintID})
	if status != http.StatusNoContent && status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not delete the sprint")
	}
	return json.Marshal(map[string]any{
		"deleted": true,
		"sprint":  sprint.Name,
	})
}

type assistantDeleteLabelArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
}

func (h *Handler) assistantDeleteLabel(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantDeleteLabelArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	label, err := h.assistantResolveLabel(ctx, ws, args.Label)
	if err != nil {
		return nil, err
	}

	labelID := uuidToString(label.ID)
	out, err := h.assistantAwaitConfirmation(ctx, caller, raw, assistantOperationPlan{
		Tool: assistant.ToolDeleteLabel,
		Summary: "Permanently delete label “" + label.Name + "” in " + ws.Name +
			", taking it off every issue that carries it. This cannot be undone.",
		Workspace: ws,
		Target:    assistantOperationTarget{Type: "label", Identifier: labelID, Title: label.Name},
	})
	if out != nil || err != nil {
		return out, err
	}

	status, respBody := h.assistantInvoke(ctx, h.DeleteLabel, http.MethodDelete, "/api/labels/"+labelID,
		caller.ID, uuidToString(ws.ID), "", map[string]string{"id": labelID})
	if status != http.StatusNoContent && status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not delete the label")
	}
	return json.Marshal(map[string]any{
		"deleted": true,
		"label":   label.Name,
	})
}

type assistantCommentRefArgs struct {
	WorkspaceID string `json:"workspace_id"`
	CommentID   string `json:"comment_id"`
}

// assistantLoadComment turns a comment id into the row, scoped to the
// workspace AND to an issue the caller may see.
//
// The second half is what the HTTP path gets for free and this one does not:
// GetCommentInWorkspace scopes to the workspace but knows nothing about the
// non-owner issue-visibility gate, so without the issue lookup below a plain
// member could name any comment UUID in the workspace and read or edit it.
// Resolving the parent issue through assistantResolveIssue applies the same gate
// every other tool here runs under.
func (h *Handler) assistantLoadComment(ctx context.Context, caller assistantCaller, ws db.Workspace, role, ref string) (db.Comment, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.Comment{}, errors.New("comment_id is required — list_comments returns it for every comment in a thread")
	}
	commentUUID, err := util.ParseUUID(ref)
	if err != nil {
		return db.Comment{}, errors.New("comment_id must be a comment UUID from list_comments")
	}
	comment, err := h.Queries.GetCommentInWorkspace(ctx, db.GetCommentInWorkspaceParams{
		ID:          commentUUID,
		WorkspaceID: ws.ID,
	})
	if err != nil {
		return db.Comment{}, errors.New("comment not found in that workspace")
	}
	if _, ierr := h.assistantResolveIssue(ctx, caller, ws, role, uuidToString(comment.IssueID)); ierr != nil {
		// Same not-found semantics as everywhere else: never an oracle for a
		// comment on an issue the caller may not see.
		return db.Comment{}, errors.New("comment not found in that workspace")
	}
	return comment, nil
}

func (h *Handler) assistantDeleteComment(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantCommentRefArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, role, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	comment, err := h.assistantLoadComment(ctx, caller, ws, role, args.CommentID)
	if err != nil {
		return nil, err
	}

	commentID := uuidToString(comment.ID)
	// Name the thread, not just the UUID: a card saying "delete a comment" tells
	// the user nothing they can check. assistantLoadComment has already proven
	// the caller may see the parent issue, so this resolve cannot leak one.
	where := ws.Name
	if issue, ierr := h.assistantResolveIssue(ctx, caller, ws, role, uuidToString(comment.IssueID)); ierr == nil {
		where = h.getIssuePrefix(ctx, ws.ID) + "-" + strconv.Itoa(int(issue.Number)) + " in " + ws.Name
	}
	preview := truncateRunes(strings.TrimSpace(comment.Content), assistantConfirmCardPreviewChars)
	out, err := h.assistantAwaitConfirmation(ctx, caller, raw, assistantOperationPlan{
		Tool: assistant.ToolDeleteComment,
		Summary: "Permanently delete a comment on " + where + ": “" + preview +
			"”. This cannot be undone.",
		Workspace: ws,
		Target:    assistantOperationTarget{Type: "comment", Identifier: commentID, Title: preview},
	})
	if out != nil || err != nil {
		return out, err
	}

	status, respBody := h.assistantInvoke(ctx, h.DeleteComment, http.MethodDelete, "/api/comments/"+commentID,
		caller.ID, uuidToString(ws.ID), "", map[string]string{"commentId": commentID})
	if status != http.StatusNoContent && status != http.StatusOK {
		// The handler's own "only comment author or admin can delete" lands
		// here verbatim, which is the answer the user needs.
		return nil, assistantHandlerError(status, respBody, "could not delete the comment")
	}
	return json.Marshal(map[string]any{
		"deleted":    true,
		"comment_id": commentID,
	})
}
