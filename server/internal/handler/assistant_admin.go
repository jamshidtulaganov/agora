package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// The assistant's ADMINISTRATIVE tools: people, workspaces, autopilots and
// automations — Settings, in other words.
//
// The MAIN RULE (docs/agora-assistant-plan.md §0) is why these exist at all:
// full UI parity, bounded by the caller's own role rather than by a second,
// stricter policy invented for the assistant. "Invite Anna as an admin" is one
// sentence and three clicks; there was never a reason for the chat to answer it
// with directions.
//
// Two things carry the safety here, and neither is new code:
//
//   - The ROLE GATE is the router's. Every write below goes through
//     assistantInvokeAs with the same role list the route passes to
//     middleware.RequireWorkspaceRoleFromURL, so an ordinary member asking for
//     an invite is refused by the product's own middleware with the product's
//     own message ("insufficient permissions"). Re-implementing that check here
//     would be a second source of truth that drifts the first time someone
//     changes who may invite.
//   - The remaining handler-side invariants (last owner cannot be removed, an
//     owner may only be demoted by another owner, an invite cannot grant
//     ownership) live inside UpdateMember / DeleteMember / CreateInvitation and
//     are reached unchanged.
//
// Destructive entries here (remove_member, leave_workspace, delete_workspace,
// delete_automation) are additionally behind the confirm gate in
// assistant_tools.go.

// ---------------------------------------------------------------------------
// Members
// ---------------------------------------------------------------------------

// assistantMemberRoles is the role list the router requires for the member
// routes. Named once so invite / update / remove cannot drift apart.
var assistantMemberRoles = []string{"owner", "admin"}

type assistantInviteMemberArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Email       string `json:"email"`
	Role        string `json:"role"`
}

// assistantInviteMember sends a real invitation through CreateInvitation: an
// invitation row, the invitation:created event, the analytics record, and the
// email. Nothing about the flow is assistant-specific — a member who accepts
// cannot tell how they were invited, which is the point.
func (h *Handler) assistantInviteMember(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantInviteMemberArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	email := strings.ToLower(strings.TrimSpace(args.Email))
	if email == "" {
		return nil, errors.New("email is required")
	}
	// A cheap shape check so an obviously-wrong address (a name, a username)
	// becomes a question to the user instead of a pending invitation nobody
	// will ever accept. The handler's own validation is otherwise the authority.
	if !strings.Contains(email, "@") || strings.HasPrefix(email, "@") || strings.HasSuffix(email, "@") {
		return nil, errors.New("email must be a full email address — ask the user for it rather than guessing one from a name")
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}

	body := map[string]any{"email": email}
	if role := strings.TrimSpace(args.Role); role != "" {
		body["role"] = role
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("could not build the invite request")
	}

	workspaceID := uuidToString(ws.ID)
	status, respBody := h.assistantInvokeAs(ctx, h.CreateInvitation, http.MethodPost,
		"/api/workspaces/"+workspaceID+"/members",
		caller.ID, workspaceID, string(encoded), map[string]string{"id": workspaceID},
		assistantMemberRoles...)
	if status != http.StatusCreated {
		return nil, assistantHandlerError(status, respBody, "could not invite that person")
	}
	var invitation InvitationResponse
	if err := json.Unmarshal(respBody, &invitation); err != nil {
		return nil, errors.New("the invitation was created but its details could not be read back")
	}
	return json.Marshal(map[string]any{
		"invited":        true,
		"email":          invitation.InviteeEmail,
		"role":           invitation.Role,
		"invitation_id":  invitation.ID,
		"workspace_name": ws.Name,
		"note":           "An invitation email has been sent. They join the workspace when they accept it.",
	})
}

// assistantResolveMember turns a user_id (what list_members returns) into the
// MEMBER row the member routes address.
//
// The two ids are different — member.id is the membership, user.id is the
// person — and list_members deliberately returns the user id, because that is
// also what an assignee is. Doing the translation here means the model only
// ever handles one id per person.
func (h *Handler) assistantResolveMember(ctx context.Context, ws db.Workspace, ref string) (db.Member, string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.Member{}, "", errors.New("user_id is required (a user_id from list_members)")
	}
	userUUID, err := util.ParseUUID(ref)
	if err != nil {
		return db.Member{}, "", errors.New("user_id must be a UUID from list_members")
	}
	member, err := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID:      userUUID,
		WorkspaceID: ws.ID,
	})
	if err != nil {
		return db.Member{}, "", errors.New("that user is not a member of this workspace — list_members shows who is")
	}
	name := ""
	if user, uerr := h.Queries.GetUser(ctx, userUUID); uerr == nil {
		name = user.Name
	}
	return member, name, nil
}

type assistantMemberRoleArgs struct {
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id"`
	Role        string `json:"role"`
}

func (h *Handler) assistantUpdateMemberRole(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantMemberRoleArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	role := strings.TrimSpace(args.Role)
	if role == "" {
		return nil, errors.New("role is required: one of owner, admin, member")
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	member, name, err := h.assistantResolveMember(ctx, ws, args.UserID)
	if err != nil {
		return nil, err
	}
	if member.Role == role {
		return nil, fmt.Errorf("%s is already %s in that workspace — nothing to change", assistantPersonLabel(name, args.UserID), role)
	}

	encoded, err := json.Marshal(map[string]any{"role": role})
	if err != nil {
		return nil, errors.New("could not build the role request")
	}
	workspaceID := uuidToString(ws.ID)
	memberID := uuidToString(member.ID)
	status, respBody := h.assistantInvokeAs(ctx, h.UpdateMember, http.MethodPatch,
		"/api/workspaces/"+workspaceID+"/members/"+memberID,
		caller.ID, workspaceID, string(encoded),
		map[string]string{"id": workspaceID, "memberId": memberID},
		assistantMemberRoles...)
	if status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not change that role")
	}
	return json.Marshal(map[string]any{
		"updated":       true,
		"user_id":       args.UserID,
		"name":          name,
		"role":          role,
		"previous_role": member.Role,
	})
}

type assistantRemoveMemberArgs struct {
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id"`
}

func (h *Handler) assistantRemoveMember(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantRemoveMemberArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	member, name, err := h.assistantResolveMember(ctx, ws, args.UserID)
	if err != nil {
		return nil, err
	}
	// Removing yourself is leave_workspace, which has its own handler and its
	// own last-owner rule. Routing it here would produce a confusing refusal.
	if uuidToString(member.UserID) == caller.ID {
		return nil, errors.New("to remove yourself from a workspace use leave_workspace")
	}

	// Ask-time role refusal: never show somebody a confirmation card for
	// something their role will not let them do (see assistantAuthorize).
	if err := h.assistantAuthorize(ctx, caller.ID, uuidToString(ws.ID), assistantMemberRoles...); err != nil {
		return nil, err
	}
	out, err := h.assistantAwaitConfirmation(ctx, caller, raw, assistantOperationPlan{
		Tool: assistant.ToolRemoveMember,
		Summary: "Remove " + assistantPersonLabel(name, args.UserID) + " from " + ws.Name +
			". They lose access immediately, and their runtimes and tokens in this workspace are revoked.",
		Workspace: ws,
		Target: assistantOperationTarget{
			Type:       "member",
			Identifier: uuidToString(member.UserID),
			Title:      assistantPersonLabel(name, args.UserID),
		},
	})
	if out != nil || err != nil {
		return out, err
	}

	workspaceID := uuidToString(ws.ID)
	memberID := uuidToString(member.ID)
	status, respBody := h.assistantInvokeAs(ctx, h.DeleteMember, http.MethodDelete,
		"/api/workspaces/"+workspaceID+"/members/"+memberID,
		caller.ID, workspaceID, "",
		map[string]string{"id": workspaceID, "memberId": memberID},
		assistantMemberRoles...)
	if status != http.StatusNoContent && status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not remove that person")
	}
	return json.Marshal(map[string]any{
		"removed": true,
		"user_id": args.UserID,
		"name":    name,
	})
}

// assistantPersonLabel prefers a name over a UUID in a message the user reads.
func assistantPersonLabel(name, id string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	return id
}

// ---------------------------------------------------------------------------
// Workspaces
// ---------------------------------------------------------------------------

type assistantCreateWorkspaceArgs struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	IssuePrefix string `json:"issue_prefix"`
}

// assistantWorkspaceSlugFromName derives a URL slug from a workspace name, the
// way the create form's live preview does.
//
// The tool takes slug as OPTIONAL because a user saying "make me a workspace
// called Q4 Planning" has not thought about URLs, and forcing the model to
// invent one produces either a rejected request or a slug nobody wanted. This
// keeps letters, digits and hyphens, folds everything else to a hyphen, and
// collapses runs — the same shape workspaceSlugPattern accepts.
func assistantWorkspaceSlugFromName(name string) string {
	var b strings.Builder
	lastHyphen := true // leading hyphens are dropped
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen {
				b.WriteRune('-')
				lastHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// assistantCreateWorkspace creates a workspace with the caller as its owner.
//
// This is the one write in the catalog with no workspace to be a member of, so
// it invokes the handler with an empty workspace id — the router mounts no
// workspace middleware on POST /api/workspaces either.
func (h *Handler) assistantCreateWorkspace(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantCreateWorkspaceArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return nil, errors.New("name is required")
	}
	slug := strings.ToLower(strings.TrimSpace(args.Slug))
	if slug == "" {
		slug = assistantWorkspaceSlugFromName(name)
	}
	if slug == "" {
		return nil, errors.New("could not derive a URL slug from that name — ask the user for one (lowercase letters, digits and hyphens)")
	}

	body := map[string]any{"name": name, "slug": slug}
	if d := strings.TrimSpace(args.Description); d != "" {
		body["description"] = d
	}
	if p := strings.TrimSpace(args.IssuePrefix); p != "" {
		body["issue_prefix"] = p
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("could not build the create request")
	}

	status, respBody := h.assistantInvoke(ctx, h.CreateWorkspace, http.MethodPost, "/api/workspaces",
		caller.ID, "", string(encoded), nil)
	if status != http.StatusCreated {
		// "slug is reserved" and "workspace slug already exists" both land here
		// verbatim, which tells the model to pick another one.
		return nil, assistantHandlerError(status, respBody, "could not create the workspace")
	}
	var created WorkspaceResponse
	if err := json.Unmarshal(respBody, &created); err != nil {
		return nil, errors.New("the workspace was created but its details could not be read back")
	}
	return json.Marshal(map[string]any{
		"created": true,
		"workspace": map[string]any{
			"id":       created.ID,
			"name":     created.Name,
			"slug":     created.Slug,
			"url_path": "/" + created.Slug,
		},
	})
}

type assistantUpdateWorkspaceArgs struct {
	WorkspaceID string  `json:"workspace_id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Context     *string `json:"context"`
}

func (h *Handler) assistantUpdateWorkspace(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantUpdateWorkspaceArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}

	// Only the keys the model sent: UpdateWorkspace COALESCEs, so an absent key
	// leaves the column alone.
	body := map[string]any{}
	if n := strings.TrimSpace(args.Name); n != "" {
		body["name"] = n
	}
	if args.Description != nil {
		body["description"] = *args.Description
	}
	if args.Context != nil {
		body["context"] = *args.Context
	}
	if len(body) == 0 {
		return nil, errors.New("nothing to change: pass at least one of name, description, context")
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("could not build the update request")
	}

	workspaceID := uuidToString(ws.ID)
	status, respBody := h.assistantInvokeAs(ctx, h.UpdateWorkspace, http.MethodPatch,
		"/api/workspaces/"+workspaceID,
		caller.ID, workspaceID, string(encoded), map[string]string{"id": workspaceID},
		assistantMemberRoles...)
	if status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not update the workspace")
	}
	var updated WorkspaceResponse
	if err := json.Unmarshal(respBody, &updated); err != nil {
		return nil, errors.New("the workspace was updated but its details could not be read back")
	}
	return json.Marshal(map[string]any{
		"updated": true,
		"workspace": map[string]any{
			"id":   updated.ID,
			"name": updated.Name,
			"slug": updated.Slug,
		},
	})
}

type assistantWorkspaceConfirmArgs struct {
	WorkspaceID string `json:"workspace_id"`
}

func (h *Handler) assistantLeaveWorkspace(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantWorkspaceConfirmArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}

	out, err := h.assistantAwaitConfirmation(ctx, caller, raw, assistantOperationPlan{
		Tool: assistant.ToolLeaveWorkspace,
		Summary: "Leave the workspace " + ws.Name + " (" + ws.Slug + "). You lose access to everything in it, " +
			"and you cannot let yourself back in.",
		Workspace: ws,
		Target:    assistantOperationTarget{Type: "workspace", Identifier: ws.Slug, Title: ws.Name},
	})
	if out != nil || err != nil {
		return out, err
	}

	workspaceID := uuidToString(ws.ID)
	status, respBody := h.assistantInvoke(ctx, h.LeaveWorkspace, http.MethodPost,
		"/api/workspaces/"+workspaceID+"/leave",
		caller.ID, workspaceID, "", map[string]string{"id": workspaceID})
	if status < 200 || status > 299 {
		// "workspace must have at least one owner" reaches the model here.
		return nil, assistantHandlerError(status, respBody, "could not leave the workspace")
	}
	return json.Marshal(map[string]any{
		"left":           true,
		"workspace_name": ws.Name,
		"workspace_slug": ws.Slug,
	})
}

func (h *Handler) assistantDeleteWorkspace(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantWorkspaceConfirmArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	name, slug := ws.Name, ws.Slug

	// Owner-only, refused before the card rather than after it.
	if err := h.assistantAuthorize(ctx, caller.ID, uuidToString(ws.ID), "owner"); err != nil {
		return nil, err
	}
	out, err := h.assistantAwaitConfirmation(ctx, caller, raw, assistantOperationPlan{
		Tool: assistant.ToolDeleteWorkspace,
		Summary: "Permanently delete the whole workspace " + name + " (" + slug + ") — every issue, project, " +
			"sprint, agent and member in it. This is the most destructive action in Agora and it cannot be undone.",
		Workspace: ws,
		Target:    assistantOperationTarget{Type: "workspace", Identifier: slug, Title: name},
	})
	if out != nil || err != nil {
		return out, err
	}

	workspaceID := uuidToString(ws.ID)
	status, respBody := h.assistantInvokeAs(ctx, h.DeleteWorkspace, http.MethodDelete,
		"/api/workspaces/"+workspaceID,
		caller.ID, workspaceID, "", map[string]string{"id": workspaceID},
		"owner")
	if status != http.StatusNoContent && status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not delete the workspace")
	}
	return json.Marshal(map[string]any{
		"deleted":        true,
		"workspace_name": name,
		"workspace_slug": slug,
	})
}

// ---------------------------------------------------------------------------
// Autopilots
// ---------------------------------------------------------------------------

type assistantAutopilotResult struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Prompt         string `json:"prompt,omitempty"`
	Status         string `json:"status"`
	ExecutionMode  string `json:"execution_mode"`
	AssigneeType   string `json:"assignee_type,omitempty"`
	AssigneeID     string `json:"assignee_id,omitempty"`
	AssigneeName   string `json:"assignee_name,omitempty"`
	ProjectID      string `json:"project_id,omitempty"`
	CronExpression string `json:"cron_expression,omitempty"`
	Timezone       string `json:"timezone,omitempty"`
	ScheduleOn     bool   `json:"schedule_enabled,omitempty"`
	LastRunAt      string `json:"last_run_at,omitempty"`
}

// assistantAutopilotRow flattens an autopilot and its SCHEDULE trigger into one
// row.
//
// The two are separate resources in the API (an autopilot may carry several
// triggers, including webhooks), but the question a user asks is always one
// question — "what runs, and when" — so the read tool answers it in one row and
// the write tools take cron_expression as a field rather than making the model
// orchestrate a second call it will forget.
func (h *Handler) assistantAutopilotRow(ctx context.Context, ap db.Autopilot, names map[string]string) assistantAutopilotResult {
	row := assistantAutopilotResult{
		ID:            uuidToString(ap.ID),
		Title:         ap.Title,
		Status:        ap.Status,
		ExecutionMode: ap.ExecutionMode,
		AssigneeType:  ap.AssigneeType,
		AssigneeID:    uuidToString(ap.AssigneeID),
	}
	if ap.Description.Valid {
		row.Prompt = truncateRunes(ap.Description.String, issueBodyQueryMaxRunes)
	}
	if ap.ProjectID.Valid {
		row.ProjectID = uuidToString(ap.ProjectID)
	}
	if ap.LastRunAt.Valid {
		row.LastRunAt = timestampToString(ap.LastRunAt)
	}
	row.AssigneeName = names[row.AssigneeID]
	if trigger, ok := h.assistantScheduleTrigger(ctx, ap.ID); ok {
		row.ScheduleOn = trigger.Enabled
		if trigger.CronExpression.Valid {
			row.CronExpression = trigger.CronExpression.String
		}
		if trigger.Timezone.Valid {
			row.Timezone = trigger.Timezone.String
		}
	}
	return row
}

// assistantScheduleTrigger returns the autopilot's schedule trigger, if it has
// one. Best-effort: a trigger lookup failure means a row without a schedule, not
// a failed listing.
func (h *Handler) assistantScheduleTrigger(ctx context.Context, autopilotID pgtype.UUID) (db.AutopilotTrigger, bool) {
	triggers, err := h.Queries.ListAutopilotTriggers(ctx, autopilotID)
	if err != nil {
		return db.AutopilotTrigger{}, false
	}
	for _, t := range triggers {
		if t.Kind == "schedule" {
			return t, true
		}
	}
	return db.AutopilotTrigger{}, false
}

func (h *Handler) assistantListAutopilots(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	ws, _, err := h.assistantWorkspaceScope(ctx, caller, raw)
	if err != nil {
		return nil, err
	}
	rows, err := h.Queries.ListAutopilots(ctx, db.ListAutopilotsParams{WorkspaceID: ws.ID})
	if err != nil {
		slog.Warn("assistant: list autopilots failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return nil, errors.New("could not load autopilots")
	}
	names := h.assistantActorNames(ctx, ws.ID)
	out := make([]assistantAutopilotResult, 0, len(rows))
	for _, ap := range rows {
		out = append(out, h.assistantAutopilotRow(ctx, ap, names))
	}
	return json.Marshal(map[string]any{"autopilots": out})
}

// assistantResolveAutopilot accepts an autopilot UUID or its title.
func (h *Handler) assistantResolveAutopilot(ctx context.Context, ws db.Workspace, ref string) (db.Autopilot, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.Autopilot{}, errors.New("autopilot is required (a title from list_autopilots, or an autopilot UUID)")
	}
	rows, err := h.Queries.ListAutopilots(ctx, db.ListAutopilotsParams{WorkspaceID: ws.ID})
	if err != nil {
		slog.Warn("assistant: list autopilots failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return db.Autopilot{}, errors.New("could not load autopilots")
	}
	byID, _ := util.ParseUUID(ref)
	needle := strings.ToLower(ref)
	var matches []db.Autopilot
	for _, ap := range rows {
		if byID.Valid && ap.ID == byID {
			return ap, nil
		}
		if !byID.Valid && strings.ToLower(ap.Title) == needle {
			matches = append(matches, ap)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return db.Autopilot{}, fmt.Errorf("no autopilot called %q in that workspace — list_autopilots shows what exists", ref)
	default:
		titles := make([]string, 0, len(matches))
		for _, ap := range matches {
			titles = append(titles, ap.Title+" ("+uuidToString(ap.ID)+")")
		}
		return db.Autopilot{}, assistantAmbiguous("autopilot", ref, titles)
	}
}

type assistantAutopilotWriteArgs struct {
	WorkspaceID        string  `json:"workspace_id"`
	Autopilot          string  `json:"autopilot"`
	Title              string  `json:"title"`
	Prompt             *string `json:"prompt"`
	Status             string  `json:"status"`
	AssigneeType       string  `json:"assignee_type"`
	AssigneeID         string  `json:"assignee_id"`
	ProjectID          *string `json:"project_id"`
	ExecutionMode      string  `json:"execution_mode"`
	IssueTitleTemplate *string `json:"issue_title_template"`
	CronExpression     string  `json:"cron_expression"`
	Timezone           string  `json:"timezone"`
}

func (h *Handler) assistantCreateAutopilot(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantAutopilotWriteArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	title := strings.TrimSpace(args.Title)
	if title == "" {
		return nil, errors.New("title is required")
	}
	if strings.TrimSpace(args.AssigneeID) == "" {
		return nil, errors.New("assignee_id is required — call list_agents or list_squads and use one of the ids it returns")
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}

	body := map[string]any{"title": title, "assignee_id": strings.TrimSpace(args.AssigneeID)}
	if t := strings.TrimSpace(args.AssigneeType); t != "" {
		body["assignee_type"] = t
	}
	if args.Prompt != nil {
		body["description"] = *args.Prompt
	}
	// execution_mode is required by CreateAutopilot; create_issue is the
	// default the create form ships with, so a model that did not think about
	// it lands where a human clicking through would.
	mode := strings.TrimSpace(args.ExecutionMode)
	if mode == "" {
		mode = "create_issue"
	}
	body["execution_mode"] = mode
	if args.IssueTitleTemplate != nil {
		body["issue_title_template"] = *args.IssueTitleTemplate
	}
	if args.ProjectID != nil {
		if ref := strings.TrimSpace(*args.ProjectID); ref != "" {
			project, perr := h.assistantResolveProject(ctx, ws, ref)
			if perr != nil {
				return nil, perr
			}
			body["project_id"] = uuidToString(project.ID)
		}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("could not build the create request")
	}

	workspaceID := uuidToString(ws.ID)
	status, respBody := h.assistantInvoke(ctx, h.CreateAutopilot, http.MethodPost, "/api/autopilots",
		caller.ID, workspaceID, string(encoded), nil)
	if status != http.StatusCreated {
		return nil, assistantHandlerError(status, respBody, "could not create the autopilot")
	}
	var created AutopilotResponse
	if err := json.Unmarshal(respBody, &created); err != nil {
		return nil, errors.New("the autopilot was created but its details could not be read back")
	}

	out := map[string]any{
		"created": true,
		"autopilot": map[string]any{
			"id":             created.ID,
			"title":          created.Title,
			"status":         created.Status,
			"execution_mode": created.ExecutionMode,
		},
	}
	// A schedule is a second resource. Attaching it here rather than making it
	// a separate tool is what lets "every weekday at 9" be one call — and a
	// schedule that fails to attach must NOT read as a failed create, since the
	// autopilot itself exists and can still be triggered.
	if cron := strings.TrimSpace(args.CronExpression); cron != "" {
		if serr := h.assistantSetAutopilotSchedule(ctx, caller, workspaceID, created.ID, cron, args.Timezone); serr != nil {
			out["schedule_error"] = serr.Error()
		} else {
			out["cron_expression"] = cron
		}
	} else {
		out["note"] = "This autopilot has no schedule, so it only runs when somebody triggers it. Pass cron_expression to give it one."
	}
	return json.Marshal(out)
}

// assistantSetAutopilotSchedule creates or re-points the autopilot's schedule
// trigger. Both directions go through the real trigger handlers, so cron
// validation, timezone validation and next_run_at computation are theirs.
func (h *Handler) assistantSetAutopilotSchedule(ctx context.Context, caller assistantCaller, workspaceID, autopilotID, cron, timezone string) error {
	autopilotUUID, err := util.ParseUUID(autopilotID)
	if err != nil {
		return errors.New("could not identify the autopilot")
	}
	body := map[string]any{"cron_expression": cron}
	if tz := strings.TrimSpace(timezone); tz != "" {
		body["timezone"] = tz
	}

	if existing, ok := h.assistantScheduleTrigger(ctx, autopilotUUID); ok {
		encoded, merr := json.Marshal(body)
		if merr != nil {
			return errors.New("could not build the schedule request")
		}
		triggerID := uuidToString(existing.ID)
		status, respBody := h.assistantInvoke(ctx, h.UpdateAutopilotTrigger, http.MethodPatch,
			"/api/autopilots/"+autopilotID+"/triggers/"+triggerID,
			caller.ID, workspaceID, string(encoded),
			map[string]string{"id": autopilotID, "triggerId": triggerID})
		if status != http.StatusOK {
			return assistantHandlerError(status, respBody, "could not change the schedule")
		}
		return nil
	}

	body["kind"] = "schedule"
	encoded, merr := json.Marshal(body)
	if merr != nil {
		return errors.New("could not build the schedule request")
	}
	status, respBody := h.assistantInvoke(ctx, h.CreateAutopilotTrigger, http.MethodPost,
		"/api/autopilots/"+autopilotID+"/triggers",
		caller.ID, workspaceID, string(encoded), map[string]string{"id": autopilotID})
	if status != http.StatusCreated && status != http.StatusOK {
		return assistantHandlerError(status, respBody, "could not set the schedule")
	}
	return nil
}

func (h *Handler) assistantUpdateAutopilot(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantAutopilotWriteArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	autopilot, err := h.assistantResolveAutopilot(ctx, ws, args.Autopilot)
	if err != nil {
		return nil, err
	}

	body := map[string]any{}
	if t := strings.TrimSpace(args.Title); t != "" {
		body["title"] = t
	}
	if args.Prompt != nil {
		body["description"] = *args.Prompt
	}
	if s := strings.TrimSpace(args.Status); s != "" {
		switch s {
		case "active", "paused", "archived":
		default:
			return nil, errors.New("status must be one of: active, paused, archived")
		}
		body["status"] = s
	}
	if m := strings.TrimSpace(args.ExecutionMode); m != "" {
		body["execution_mode"] = m
	}
	if args.IssueTitleTemplate != nil {
		body["issue_title_template"] = *args.IssueTitleTemplate
	}
	assigneeType, assigneeID, err := assistantAssigneePair(args.AssigneeType, args.AssigneeID)
	if err != nil {
		return nil, err
	}
	if assigneeType != "" {
		body["assignee_type"] = assigneeType
		body["assignee_id"] = assigneeID
	}
	if args.ProjectID != nil {
		if ref := strings.TrimSpace(*args.ProjectID); ref != "" {
			project, perr := h.assistantResolveProject(ctx, ws, ref)
			if perr != nil {
				return nil, perr
			}
			body["project_id"] = uuidToString(project.ID)
		} else {
			body["project_id"] = nil
		}
	}
	cron := strings.TrimSpace(args.CronExpression)
	if len(body) == 0 && cron == "" {
		return nil, errors.New("nothing to change: pass at least one of title, prompt, status, execution_mode, issue_title_template, assignee_type+assignee_id, project_id, cron_expression")
	}

	workspaceID := uuidToString(ws.ID)
	autopilotID := uuidToString(autopilot.ID)
	out := map[string]any{"updated": true, "autopilot": autopilot.Title}
	if len(body) > 0 {
		encoded, merr := json.Marshal(body)
		if merr != nil {
			return nil, errors.New("could not build the update request")
		}
		status, respBody := h.assistantInvoke(ctx, h.UpdateAutopilot, http.MethodPatch,
			"/api/autopilots/"+autopilotID,
			caller.ID, workspaceID, string(encoded), map[string]string{"id": autopilotID})
		if status != http.StatusOK {
			return nil, assistantHandlerError(status, respBody, "could not update the autopilot")
		}
		var updated AutopilotResponse
		if uerr := json.Unmarshal(respBody, &updated); uerr == nil {
			out["autopilot"] = updated.Title
			out["status"] = updated.Status
		}
	}
	if cron != "" {
		if serr := h.assistantSetAutopilotSchedule(ctx, caller, workspaceID, autopilotID, cron, args.Timezone); serr != nil {
			return nil, serr
		}
		out["cron_expression"] = cron
	}
	return json.Marshal(out)
}

type assistantAutopilotRefArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Autopilot   string `json:"autopilot"`
}

func (h *Handler) assistantRunAutopilotNow(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantAutopilotRefArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	autopilot, err := h.assistantResolveAutopilot(ctx, ws, args.Autopilot)
	if err != nil {
		return nil, err
	}

	autopilotID := uuidToString(autopilot.ID)
	status, respBody := h.assistantInvoke(ctx, h.TriggerAutopilot, http.MethodPost,
		"/api/autopilots/"+autopilotID+"/trigger",
		caller.ID, uuidToString(ws.ID), "", map[string]string{"id": autopilotID})
	if status != http.StatusOK {
		// "autopilot is not active" is the common one, and it is exactly what
		// the user needs to hear.
		return nil, assistantHandlerError(status, respBody, "could not run the autopilot")
	}
	out := map[string]any{"triggered": true, "autopilot": autopilot.Title}
	var run AutopilotRunResponse
	if rerr := json.Unmarshal(respBody, &run); rerr == nil {
		out["run_id"] = run.ID
		out["run_status"] = run.Status
	}
	return json.Marshal(out)
}

// ---------------------------------------------------------------------------
// Automations
// ---------------------------------------------------------------------------

type assistantAutomationResult struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Enabled     bool            `json:"enabled"`
	TriggerType string          `json:"trigger_type"`
	Conditions  json.RawMessage `json:"conditions"`
	Actions     json.RawMessage `json:"actions"`
	ProjectID   string          `json:"project_id,omitempty"`
	RunCount    int32           `json:"run_count"`
	LastRunAt   string          `json:"last_run_at,omitempty"`
}

// assistantListAutomations returns the workspace's rules AND the catalog of
// valid trigger types, step types and operators.
//
// The catalog is bundled into the read rather than given its own tool for one
// reason: create_automation's arguments are not free text, they are a small
// grammar, and a model that has to guess "issue.status_changed" from prose will
// guess "status_changed" and get a 400 it then narrates as a broken feature.
// One call gives it both the existing rules (to copy the shape from) and the
// vocabulary (to build a new one correctly), which is what the editor's own
// sidebar does for a human.
func (h *Handler) assistantListAutomations(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	ws, _, err := h.assistantWorkspaceScope(ctx, caller, raw)
	if err != nil {
		return nil, err
	}
	rows, err := h.Queries.ListAutomationsForWorkspace(ctx, ws.ID)
	if err != nil {
		slog.Warn("assistant: list automations failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return nil, errors.New("could not load automations")
	}
	out := make([]assistantAutomationResult, 0, len(rows))
	for _, a := range rows {
		row := assistantAutomationResult{
			ID:          uuidToString(a.ID),
			Name:        a.Name,
			Description: a.Description,
			Enabled:     a.Enabled,
			TriggerType: a.TriggerType,
			Conditions:  rawOrEmptyArray(a.Conditions),
			Actions:     rawOrEmptyArray(a.Actions),
			RunCount:    a.RunCount,
		}
		if a.ProjectID.Valid {
			row.ProjectID = uuidToString(a.ProjectID)
		}
		if a.LastRunAt.Valid {
			row.LastRunAt = timestampString(a.LastRunAt)
		}
		out = append(out, row)
	}
	return json.Marshal(map[string]any{
		"automations": out,
		"catalog": map[string]any{
			"trigger_types": automationTriggers,
			"step_types":    automationActions,
			"operators": []string{
				automationOpEq, automationOpNeq, automationOpIn, automationOpNotIn,
				automationOpContains, automationOpExists, automationOpHasLabel, automationOpNotHasLabel,
			},
			"statuses": []string{"backlog", "todo", "in_progress", "in_review", "done", "blocked", "cancelled"},
		},
		"note": "conditions and actions use exactly this JSON shape: conditions [{\"field\":..,\"op\":..,\"value\":..}], actions [{\"type\":..,\"config\":{..}}].",
	})
}

type assistantCreateAutomationArgs struct {
	WorkspaceID   string `json:"workspace_id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	TriggerType   string `json:"trigger_type"`
	TriggerConfig string `json:"trigger_config"`
	Conditions    string `json:"conditions"`
	Actions       string `json:"actions"`
	ProjectID     string `json:"project_id"`
	Enabled       *bool  `json:"enabled"`
}

// assistantAutomationJSON decodes one of the JSON-blob arguments.
//
// conditions/actions/trigger_config are declared as STRINGS in the tool schema
// rather than as nested objects, deliberately: the free-tier model is a poor
// nested-schema filler, and a malformed nested object reaches the executor as
// an unusable partial structure, whereas a malformed string reaches it as a
// string we can reject with a message naming the shape we wanted. The handler
// still does the real validation — this only turns the text into JSON.
func assistantAutomationJSON(field, raw string, into any) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), into); err != nil {
		return fmt.Errorf("%s must be valid JSON — %s", field, err.Error())
	}
	return nil
}

func (h *Handler) assistantCreateAutomation(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantCreateAutomationArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return nil, errors.New("name is required")
	}
	trigger := strings.TrimSpace(args.TriggerType)
	if trigger == "" {
		return nil, errors.New("trigger_type is required — list_automations returns the valid trigger types")
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}

	var triggerConfig map[string]any
	if err := assistantAutomationJSON("trigger_config", args.TriggerConfig, &triggerConfig); err != nil {
		return nil, err
	}
	var conditions []any
	if err := assistantAutomationJSON("conditions", args.Conditions, &conditions); err != nil {
		return nil, err
	}
	var actions []any
	if err := assistantAutomationJSON("actions", args.Actions, &actions); err != nil {
		return nil, err
	}
	if len(actions) == 0 {
		return nil, errors.New("actions is required: a JSON array with at least one step, e.g. [{\"type\":\"add_label\",\"config\":{\"label\":\"needs-qa\"}}]")
	}

	body := map[string]any{
		"name":         name,
		"trigger_type": trigger,
		"actions":      actions,
	}
	if d := strings.TrimSpace(args.Description); d != "" {
		body["description"] = d
	}
	if triggerConfig != nil {
		body["trigger_config"] = triggerConfig
	}
	if conditions != nil {
		body["conditions"] = conditions
	}
	if args.Enabled != nil {
		body["enabled"] = *args.Enabled
	}
	if ref := strings.TrimSpace(args.ProjectID); ref != "" {
		project, perr := h.assistantResolveProject(ctx, ws, ref)
		if perr != nil {
			return nil, perr
		}
		body["project_id"] = uuidToString(project.ID)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("could not build the create request")
	}

	status, respBody := h.assistantInvoke(ctx, h.CreateAutomation, http.MethodPost, "/api/automations",
		caller.ID, uuidToString(ws.ID), string(encoded), nil)
	if status != http.StatusCreated {
		// validateAutomationRule's messages ("unknown trigger", "set_status
		// needs a valid status") are precise enough for the model to fix the
		// rule and retry.
		return nil, assistantHandlerError(status, respBody, "could not create the automation")
	}
	var created AutomationResponse
	if err := json.Unmarshal(respBody, &created); err != nil {
		return nil, errors.New("the automation was created but its details could not be read back")
	}
	return json.Marshal(map[string]any{
		"created": true,
		"automation": map[string]any{
			"id":           created.ID,
			"name":         created.Name,
			"enabled":      created.Enabled,
			"trigger_type": created.TriggerType,
		},
		"note": "This rule now fires on its own. Tell the user what triggers it and what it does.",
	})
}

// assistantResolveAutomation accepts an automation UUID or its exact name.
func (h *Handler) assistantResolveAutomation(ctx context.Context, ws db.Workspace, ref string) (db.Automation, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.Automation{}, errors.New("automation is required (a name from list_automations, or an automation UUID)")
	}
	rows, err := h.Queries.ListAutomationsForWorkspace(ctx, ws.ID)
	if err != nil {
		slog.Warn("assistant: list automations failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return db.Automation{}, errors.New("could not load automations")
	}
	byID, _ := util.ParseUUID(ref)
	needle := strings.ToLower(ref)
	var matches []db.Automation
	for _, a := range rows {
		if byID.Valid && a.ID == byID {
			return a, nil
		}
		if !byID.Valid && strings.ToLower(a.Name) == needle {
			matches = append(matches, a)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return db.Automation{}, fmt.Errorf("no automation called %q in that workspace — list_automations shows what exists", ref)
	default:
		names := make([]string, 0, len(matches))
		for _, a := range matches {
			names = append(names, a.Name+" ("+uuidToString(a.ID)+")")
		}
		return db.Automation{}, assistantAmbiguous("automation", ref, names)
	}
}

type assistantAutomationEnabledArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Automation  string `json:"automation"`
	Enabled     *bool  `json:"enabled"`
}

func (h *Handler) assistantSetAutomationEnabled(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantAutomationEnabledArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	if args.Enabled == nil {
		return nil, errors.New("enabled is required: true switches the automation on, false switches it off")
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	automation, err := h.assistantResolveAutomation(ctx, ws, args.Automation)
	if err != nil {
		return nil, err
	}

	encoded, err := json.Marshal(map[string]any{"enabled": *args.Enabled})
	if err != nil {
		return nil, errors.New("could not build the request")
	}
	automationID := uuidToString(automation.ID)
	status, respBody := h.assistantInvoke(ctx, h.SetAutomationEnabled, http.MethodPost,
		"/api/automations/"+automationID+"/enabled",
		caller.ID, uuidToString(ws.ID), string(encoded), map[string]string{"id": automationID})
	if status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not change the automation")
	}
	return json.Marshal(map[string]any{
		"updated":    true,
		"automation": automation.Name,
		"enabled":    *args.Enabled,
	})
}

type assistantAutomationRefArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Automation  string `json:"automation"`
}

func (h *Handler) assistantDeleteAutomation(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantAutomationRefArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	automation, err := h.assistantResolveAutomation(ctx, ws, args.Automation)
	if err != nil {
		return nil, err
	}

	automationID := uuidToString(automation.ID)
	out, err := h.assistantAwaitConfirmation(ctx, caller, raw, assistantOperationPlan{
		Tool: assistant.ToolDeleteAutomation,
		Summary: "Permanently delete the automation “" + automation.Name + "” in " + ws.Name +
			", with its run history. This cannot be undone — set_automation_enabled(false) only stops it.",
		Workspace: ws,
		Target:    assistantOperationTarget{Type: "automation", Identifier: automationID, Title: automation.Name},
	})
	if out != nil || err != nil {
		return out, err
	}

	status, respBody := h.assistantInvoke(ctx, h.DeleteAutomation, http.MethodDelete,
		"/api/automations/"+automationID,
		caller.ID, uuidToString(ws.ID), "", map[string]string{"id": automationID})
	if status != http.StatusNoContent && status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not delete the automation")
	}
	return json.Marshal(map[string]any{
		"deleted":    true,
		"automation": automation.Name,
	})
}
