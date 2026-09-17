package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// The assistant's tool executor: dispatch, every READ tool, and the resolvers
// (issue / project / label / sprint) that turn a name the user said into the
// row a write addresses. The write tools live in assistant_writes.go, which
// reuses the access helpers defined here.
//
// This is the security boundary of the whole feature: the model proposes, this
// file disposes. Every tool re-derives the caller's access from scratch —
// membership per workspace, then the same non-owner visibility gate the HTTP
// list/search/detail endpoints apply — so the assistant can never surface a row
// the user could not have fetched themselves.
//
// Three rules this file must never break:
//  1. The assistant is NOT an exempt actor. `issueVisibilityRestriction` lets
//     agent/daemon actors (X-Actor-Source) see the whole workspace because they
//     run work across it. The assistant runs on behalf of a HUMAN and gets the
//     human's narrow view — hence the role-only restriction below rather than a
//     reuse of the request-scoped helper.
//  2. A non-member workspace returns an error, never data, and never a partial
//     list that leaks the workspace's existence beyond what the user asked.
//  3. Every tool the model can name has a branch in Execute and a gate before
//     its first query. A new tool without both is a hole, not a feature — the
//     handler test TestEveryWorkspaceScopedToolRefusesNonMembers fails when one
//     ships ungated.

// defaultAssistantIssueLimit is what a tool returns when the model names no
// limit. assistant.MaxToolResultIssues is the hard ceiling for the
// cross-workspace fan-out.
const defaultAssistantIssueLimit = 20

// errAssistantNoAccess is the caller-visible refusal. It is returned to the
// MODEL as a tool error, so it must read as an answer, not a stack trace.
var errAssistantNoAccess = errors.New("you are not a member of that workspace")

// errAssistantBadArgs is returned when the model's argument blob is not even
// JSON. Like every executor error it reaches the model as a tool result, which
// is exactly the feedback it needs to retry with a well-formed call.
var errAssistantBadArgs = errors.New("could not read the tool arguments as JSON")

// errAssistantIssueNotFound is deliberately identical whether the issue does
// not exist or the caller simply may not see it — the same not-found semantics
// the HTTP issue reads use, so the tool is never an existence oracle.
var errAssistantIssueNotFound = errors.New("issue not found in that workspace")

// assistantCaller is the resolved human on whose behalf every tool runs. Both
// forms of the id are carried because the read tools query with the UUID while
// the write tools hand the string to the real HTTP handlers.
type assistantCaller struct {
	ID   string
	UUID pgtype.UUID
}

// Execute runs one allowlisted assistant tool as userID, inside sessionID. It
// implements assistant.ToolExecutor.
//
// sessionID is the run's conversation. Only the artifact tools read it — they
// are session-scoped rather than workspace-scoped — but it is a parameter
// rather than a ctx value so the compiler, not a code review, is what stops a
// future caller from dropping it.
func (h *Handler) Execute(ctx context.Context, userID, sessionID, name string, args json.RawMessage) (json.RawMessage, error) {
	userUUID, err := util.ParseUUID(userID)
	if err != nil {
		return nil, errors.New("could not identify the requesting user")
	}
	caller := assistantCaller{ID: userID, UUID: userUUID}

	// One mutable record per execution, installed before dispatch. It carries
	// the human confirmation (present only on the confirm endpoint's path) down
	// to the tool that needs it, and carries an uncertain outcome back up from
	// the invoke helper. See assistant_operations.go.
	exec := assistantExecutionFrom(ctx)
	if exec == nil {
		exec = &assistantExecution{}
	}
	exec.tool = name
	exec.sessionID = sessionID
	exec.destructive = assistant.RequiresConfirmation(name)
	exec.confirmed = assistantConfirmationFrom(ctx)
	ctx = withAssistantExecution(ctx, exec)

	result, err := h.assistantDispatch(ctx, caller, sessionID, name, args)

	// A dispatched write whose outcome is unknown outranks both the result and
	// the error: neither of them is true, and the one thing the model must not
	// do is retry.
	if exec.uncertain != "" {
		return assistantUncertainResult(exec.uncertain), nil
	}
	if err != nil {
		return nil, err
	}
	if assistant.IsMutating(name) {
		result = assistantAttachReceipt(name, result)
	}
	return result, nil
}

// assistantDispatch is the allowlist switch. Split out of Execute so the seams
// around it — confirmation binding, receipts, uncertain outcomes — are one
// place each rather than repeated on every return path.
func (h *Handler) assistantDispatch(ctx context.Context, caller assistantCaller, sessionID, name string, args json.RawMessage) (json.RawMessage, error) {
	switch name {
	// --- grounding / discovery -------------------------------------------
	case assistant.ToolListWorkspaces:
		return h.assistantListWorkspaces(ctx, caller)
	case assistant.ToolListMyIssues:
		return h.assistantListMyIssues(ctx, caller, args)
	case assistant.ToolListIssues:
		return h.assistantListIssues(ctx, caller, args)
	case assistant.ToolSearchIssues:
		return h.assistantSearchIssues(ctx, caller, args)
	case assistant.ToolGetIssue:
		return h.assistantGetIssue(ctx, caller, args)
	case assistant.ToolListComments:
		return h.assistantListComments(ctx, caller, args)
	case assistant.ToolListProjects:
		return h.assistantListProjects(ctx, caller, args)
	case assistant.ToolGetProject:
		return h.assistantGetProject(ctx, caller, args)
	case assistant.ToolListSprints:
		return h.assistantListSprints(ctx, caller, args)
	case assistant.ToolListLabels:
		return h.assistantListLabels(ctx, caller, args)
	case assistant.ToolListAgents:
		return h.assistantListAgents(ctx, caller, args)
	case assistant.ToolListSquads:
		return h.assistantListSquads(ctx, caller, args)
	case assistant.ToolListMembers:
		return h.assistantListMembers(ctx, caller, args)
	case assistant.ToolListRuntimes:
		return h.assistantListRuntimes(ctx, caller, args)
	case assistant.ToolListSkills:
		return h.assistantListSkills(ctx, caller, args)
	case assistant.ToolListAutopilots:
		return h.assistantListAutopilots(ctx, caller, args)
	case assistant.ToolListAutomations:
		return h.assistantListAutomations(ctx, caller, args)

	// --- mutations --------------------------------------------------------
	case assistant.ToolCreateIssue:
		return h.assistantCreateIssue(ctx, caller, args)
	case assistant.ToolUpdateIssue:
		return h.assistantUpdateIssue(ctx, caller, args)
	case assistant.ToolCommentIssue:
		return h.assistantCommentIssue(ctx, caller, args)
	case assistant.ToolArchiveIssue:
		return h.assistantArchiveIssue(ctx, caller, args)
	case assistant.ToolAddIssueLabel:
		return h.assistantIssueLabel(ctx, caller, args, true)
	case assistant.ToolRemoveIssueLabel:
		return h.assistantIssueLabel(ctx, caller, args, false)
	case assistant.ToolMoveIssueToSprint:
		return h.assistantMoveIssueToSprint(ctx, caller, args)
	case assistant.ToolCreateProject:
		return h.assistantCreateProject(ctx, caller, args)
	case assistant.ToolUpdateProject:
		return h.assistantUpdateProject(ctx, caller, args)
	case assistant.ToolCreateSprint:
		return h.assistantCreateSprint(ctx, caller, args)
	case assistant.ToolCreateLabel:
		return h.assistantCreateLabel(ctx, caller, args)
	case assistant.ToolCreateAgent:
		return h.assistantCreateAgent(ctx, caller, args)
	case assistant.ToolUpdateAgent:
		return h.assistantUpdateAgent(ctx, caller, args)
	case assistant.ToolAddSkill:
		return h.assistantAddSkill(ctx, caller, args)
	case assistant.ToolAttachSkillToAgent:
		return h.assistantAttachSkillToAgent(ctx, caller, args)
	case assistant.ToolUpdateComment:
		return h.assistantUpdateComment(ctx, caller, args)
	case assistant.ToolResolveComment:
		return h.assistantResolveComment(ctx, caller, args)
	case assistant.ToolMarkInboxRead:
		return h.assistantMarkInboxRead(ctx, caller, args)
	case assistant.ToolPinItem:
		return h.assistantPinItem(ctx, caller, args)
	case assistant.ToolSubscribeIssue:
		return h.assistantSubscribeIssue(ctx, caller, args)

	// --- deletes ----------------------------------------------------------
	// Every one of these resolves its target, then parks a pending operation
	// and waits for the user's Confirm click — see assistant_operations.go.
	case assistant.ToolDeleteIssue:
		return h.assistantDeleteIssue(ctx, caller, args)
	case assistant.ToolDeleteProject:
		return h.assistantDeleteProject(ctx, caller, args)
	case assistant.ToolDeleteSprint:
		return h.assistantDeleteSprint(ctx, caller, args)
	case assistant.ToolDeleteLabel:
		return h.assistantDeleteLabel(ctx, caller, args)
	case assistant.ToolDeleteComment:
		return h.assistantDeleteComment(ctx, caller, args)

	// --- people and workspaces --------------------------------------------
	case assistant.ToolInviteMember:
		return h.assistantInviteMember(ctx, caller, args)
	case assistant.ToolUpdateMemberRole:
		return h.assistantUpdateMemberRole(ctx, caller, args)
	case assistant.ToolRemoveMember:
		return h.assistantRemoveMember(ctx, caller, args)
	case assistant.ToolCreateWorkspace:
		return h.assistantCreateWorkspace(ctx, caller, args)
	case assistant.ToolUpdateWorkspace:
		return h.assistantUpdateWorkspace(ctx, caller, args)
	case assistant.ToolLeaveWorkspace:
		return h.assistantLeaveWorkspace(ctx, caller, args)
	case assistant.ToolDeleteWorkspace:
		return h.assistantDeleteWorkspace(ctx, caller, args)

	// --- autopilots and automations ---------------------------------------
	case assistant.ToolCreateAutopilot:
		return h.assistantCreateAutopilot(ctx, caller, args)
	case assistant.ToolUpdateAutopilot:
		return h.assistantUpdateAutopilot(ctx, caller, args)
	case assistant.ToolRunAutopilotNow:
		return h.assistantRunAutopilotNow(ctx, caller, args)
	case assistant.ToolCreateAutomation:
		return h.assistantCreateAutomation(ctx, caller, args)
	case assistant.ToolSetAutomationEnabled:
		return h.assistantSetAutomationEnabled(ctx, caller, args)
	case assistant.ToolDeleteAutomation:
		return h.assistantDeleteAutomation(ctx, caller, args)

	// --- analytics --------------------------------------------------------
	case assistant.ToolUsageSummary:
		return h.assistantUsageSummary(ctx, caller, args)
	case assistant.ToolActivityDigest:
		return h.assistantActivityDigest(ctx, caller, args)
	case assistant.ToolInboxSummary:
		return h.assistantInboxSummary(ctx, caller)
	case assistant.ToolQAStatus:
		return h.assistantQAStatus(ctx, caller, args)

	// --- artifacts --------------------------------------------------------
	// Session-scoped, not workspace-scoped: see assistant_artifacts.go.
	case assistant.ToolCreateArtifact:
		return h.assistantCreateArtifact(ctx, caller, sessionID, args)
	case assistant.ToolUpdateArtifact:
		return h.assistantUpdateArtifact(ctx, caller, sessionID, args)

	default:
		// Hard allowlist: an unknown name is either model hallucination or a
		// stale catalog. Both are the model's problem to correct, not ours.
		return nil, fmt.Errorf("unknown tool %q", name)
	}
}

// assistantWorkspaceArgs is the argument shape shared by every tool whose only
// input is a workspace id.
type assistantWorkspaceArgs struct {
	WorkspaceID string `json:"workspace_id"`
}

// assistantWorkspaceScope decodes a workspace-only argument blob and resolves
// the caller's membership in one step — the opening move of most tools.
func (h *Handler) assistantWorkspaceScope(ctx context.Context, caller assistantCaller, raw json.RawMessage) (db.Workspace, string, error) {
	var args assistantWorkspaceArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return db.Workspace{}, "", errAssistantBadArgs
	}
	return h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
}

// ---------------------------------------------------------------------------
// Result shapes
// ---------------------------------------------------------------------------

// assistantWorkspaceResult is one membership. The id is what the other tools
// take; the slug is what URLs are built from.
type assistantWorkspaceResult struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// assistantIssueResult is deliberately narrow: identifier + title + state + a
// link. Everything else would spend context the model does not need to answer
// "what is on my plate".
type assistantIssueResult struct {
	Identifier    string `json:"identifier"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	Priority      string `json:"priority"`
	WorkspaceSlug string `json:"workspace_slug"`
	URLPath       string `json:"url_path"`
}

// assistantIssueURLPath mirrors the frontend route (packages/core/paths:
// `${ws}/issues/${id}`). The identifier is used rather than the UUID because
// the issue loader accepts both and a human-readable link is what the chip
// renders.
func assistantIssueURLPath(slug, identifier string) string {
	if slug == "" || identifier == "" {
		return ""
	}
	return "/" + slug + "/issues/" + identifier
}

// ---------------------------------------------------------------------------
// Access helpers
// ---------------------------------------------------------------------------

// assistantVisibilityRestriction is the pure-role twin of
// issueVisibilityRestriction: owners see the whole workspace, everyone else
// sees only their own issues. It takes no *http.Request precisely so it CANNOT
// accidentally inherit the X-Actor-Source exemption — the assistant never gets
// the agent-actor wide view.
//
// Fails CLOSED: any role that is not exactly "owner" is restricted.
func assistantVisibilityRestriction(role string, userUUID pgtype.UUID) pgtype.UUID {
	if role == "owner" {
		return pgtype.UUID{}
	}
	return userUUID
}

// assistantMembership resolves the caller's role in one workspace, refusing
// non-members. Returns the workspace too, since every caller needs its slug.
func (h *Handler) assistantMembership(ctx context.Context, userUUID pgtype.UUID, workspaceID string) (db.Workspace, string, error) {
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return db.Workspace{}, "", errors.New("workspace_id must be a workspace UUID from list_workspaces")
	}
	member, err := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID:      userUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		// Both "no such workspace" and "not a member" collapse to the same
		// refusal on purpose: distinguishing them would turn the tool into a
		// workspace-existence oracle.
		return db.Workspace{}, "", errAssistantNoAccess
	}
	ws, err := h.Queries.GetWorkspace(ctx, wsUUID)
	if err != nil {
		return db.Workspace{}, "", errAssistantNoAccess
	}
	return ws, member.Role, nil
}

// assistantLimit clamps a model-supplied limit into the allowed band.
func assistantLimit(requested *int) int32 {
	limit := defaultAssistantIssueLimit
	if requested != nil && *requested > 0 {
		limit = *requested
	}
	if limit > 50 {
		limit = 50
	}
	return int32(limit)
}

// ---------------------------------------------------------------------------
// list_workspaces
// ---------------------------------------------------------------------------

func (h *Handler) assistantListWorkspaces(ctx context.Context, caller assistantCaller) (json.RawMessage, error) {
	userUUID := caller.UUID
	// ListWorkspaces joins member, so it is already membership-scoped.
	workspaces, err := h.Queries.ListWorkspaces(ctx, userUUID)
	if err != nil {
		slog.Warn("assistant: list workspaces failed", "error", err)
		return nil, errors.New("could not load your workspaces")
	}

	out := make([]assistantWorkspaceResult, 0, len(workspaces))
	for _, ws := range workspaces {
		role := ""
		if member, merr := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
			UserID:      userUUID,
			WorkspaceID: ws.ID,
		}); merr == nil {
			role = member.Role
		}
		out = append(out, assistantWorkspaceResult{
			ID:   uuidToString(ws.ID),
			Slug: ws.Slug,
			Name: ws.Name,
			Role: role,
		})
	}
	// ListWorkspaces is exhaustive (it joins member, it has no LIMIT), so every
	// workspace is trivially checked and the roster length IS the total.
	slugs := make([]string, 0, len(out))
	for _, ws := range out {
		slugs = append(slugs, ws.Slug)
	}
	return assistantScopedResult(map[string]any{"workspaces": out}, assistantRosterScope(slugs))
}

// ---------------------------------------------------------------------------
// list_my_issues
// ---------------------------------------------------------------------------

type assistantListMyIssuesArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Status      string `json:"status"`
	Limit       *int   `json:"limit"`
}

func (h *Handler) assistantListMyIssues(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	userUUID := caller.UUID
	var args assistantListMyIssuesArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	limit := assistantLimit(args.Limit)

	if wsID := strings.TrimSpace(args.WorkspaceID); wsID != "" {
		ws, role, err := h.assistantMembership(ctx, userUUID, wsID)
		if err != nil {
			return nil, err
		}
		issues, err := h.assistantMyIssuesIn(ctx, userUUID, ws, role, args.Status, limit)
		if err != nil {
			return nil, err
		}
		total := h.assistantCountIssues(ctx, db.CountIssuesParams{
			WorkspaceID:    ws.ID,
			AssigneeID:     userUUID,
			RestrictToUser: assistantVisibilityRestriction(role, userUUID),
			Status:         assistantOptionalText(args.Status),
		})
		return assistantScopedResult(map[string]any{"issues": issues},
			assistantCappedScope(ws.Slug, len(issues), int(limit), total))
	}

	// Unscoped: fan out across every membership. The per-workspace cap is what
	// keeps a user in a dozen workspaces from blowing the context window.
	workspaces, err := h.Queries.ListWorkspaces(ctx, userUUID)
	if err != nil {
		slog.Warn("assistant: list workspaces failed", "error", err)
		return nil, errors.New("could not load your workspaces")
	}
	if limit > assistant.MaxToolResultIssues {
		limit = assistant.MaxToolResultIssues
	}

	all := []assistantIssueResult{}
	scope := newAssistantScopeBuilder()
	for _, ws := range workspaces {
		role := ""
		if member, merr := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
			UserID:      userUUID,
			WorkspaceID: ws.ID,
		}); merr == nil {
			role = member.Role
		}
		issues, err := h.assistantMyIssuesIn(ctx, userUUID, ws, role, args.Status, limit)
		if err != nil {
			// One unreadable workspace must not sink the whole answer — but it
			// is NAMED, so the model can say which one it could not read.
			slog.Warn("assistant: list issues failed", "workspace_id", uuidToString(ws.ID), "error", err)
			scope.failedWorkspace(ws.Slug)
			continue
		}
		total := h.assistantCountIssues(ctx, db.CountIssuesParams{
			WorkspaceID:    ws.ID,
			AssigneeID:     userUUID,
			RestrictToUser: assistantVisibilityRestriction(role, userUUID),
			Status:         assistantOptionalText(args.Status),
		})
		truncated := len(issues) >= int(limit)
		if total != nil {
			truncated = *total > int64(len(issues))
		}
		scope.checkedWorkspace(ws.Slug, total, truncated)
		all = append(all, issues...)
	}
	return assistantScopedResult(map[string]any{"issues": all}, scope.scope())
}

func (h *Handler) assistantMyIssuesIn(ctx context.Context, userUUID pgtype.UUID, ws db.Workspace, role, status string, limit int32) ([]assistantIssueResult, error) {
	params := db.ListIssuesParams{
		WorkspaceID:    ws.ID,
		Limit:          limit,
		Offset:         0,
		AssigneeID:     userUUID,
		RestrictToUser: assistantVisibilityRestriction(role, userUUID),
	}
	if s := strings.TrimSpace(status); s != "" {
		params.Status = strToText(s)
	}

	rows, err := h.Queries.ListIssues(ctx, params)
	if err != nil {
		return nil, errors.New("could not load issues")
	}

	prefix := h.getIssuePrefix(ctx, ws.ID)
	out := make([]assistantIssueResult, 0, len(rows))
	for _, row := range rows {
		identifier := prefix + "-" + strconv.Itoa(int(row.Number))
		out = append(out, assistantIssueResult{
			Identifier:    identifier,
			Title:         row.Title,
			Status:        row.Status,
			Priority:      row.Priority,
			WorkspaceSlug: ws.Slug,
			URLPath:       assistantIssueURLPath(ws.Slug, identifier),
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// list_issues
// ---------------------------------------------------------------------------

type assistantListIssuesArgs struct {
	WorkspaceID     string `json:"workspace_id"`
	Status          string `json:"status"`
	Priority        string `json:"priority"`
	ProjectID       string `json:"project_id"`
	AssigneeID      string `json:"assignee_id"`
	IncludeArchived bool   `json:"include_archived"`
	Limit           *int   `json:"limit"`
}

// assistantListIssues is the workspace issue list — everybody's issues, not just
// the caller's.
//
// It exists because list_my_issues was being used to answer workspace-wide
// questions ("how many bugs are open"), and it silently under-counts: it filters
// on assignee = the caller. A chart grounded on it is wrong in a way nobody can
// see. This is the same query the Issues page runs, with the same non-owner
// visibility gate applied — so a plain member still sees only their own issues,
// and an owner sees the workspace.
func (h *Handler) assistantListIssues(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantListIssuesArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, role, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	if err := assistantValidateEnums(args.Status, args.Priority); err != nil {
		return nil, err
	}

	params := db.ListIssuesParams{
		WorkspaceID:    ws.ID,
		Limit:          assistantLimit(args.Limit),
		Offset:         0,
		RestrictToUser: assistantVisibilityRestriction(role, caller.UUID),
	}
	if s := strings.TrimSpace(args.Status); s != "" {
		params.Status = strToText(s)
	}
	if p := strings.TrimSpace(args.Priority); p != "" {
		params.Priority = strToText(p)
	}
	if args.IncludeArchived {
		params.IncludeArchived = pgtype.Bool{Bool: true, Valid: true}
	}
	if ref := strings.TrimSpace(args.ProjectID); ref != "" {
		project, perr := h.assistantResolveProject(ctx, ws, ref)
		if perr != nil {
			return nil, perr
		}
		params.ProjectID = project.ID
	}
	if ref := strings.TrimSpace(args.AssigneeID); ref != "" {
		assigneeUUID, aerr := util.ParseUUID(ref)
		if aerr != nil {
			return nil, errors.New("assignee_id must be a UUID from list_members, list_agents or list_squads")
		}
		params.AssigneeID = assigneeUUID
	}

	rows, err := h.Queries.ListIssues(ctx, params)
	if err != nil {
		slog.Warn("assistant: list issues failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return nil, errors.New("could not load issues")
	}

	prefix := h.getIssuePrefix(ctx, ws.ID)
	out := make([]assistantIssueResult, 0, len(rows))
	for _, row := range rows {
		identifier := prefix + "-" + strconv.Itoa(int(row.Number))
		out = append(out, assistantIssueResult{
			Identifier:    identifier,
			Title:         row.Title,
			Status:        row.Status,
			Priority:      row.Priority,
			WorkspaceSlug: ws.Slug,
			URLPath:       assistantIssueURLPath(ws.Slug, identifier),
		})
	}
	// returned_count is what came back; scope.total is how many there ARE, from
	// CountIssues run under the identical predicates (archive filter and
	// non-owner gate included). A model that reads a truncated list as "all of
	// them" is the exact failure this tool exists to stop.
	total := h.assistantCountIssues(ctx, db.CountIssuesParams{
		WorkspaceID:     params.WorkspaceID,
		IncludeArchived: params.IncludeArchived,
		Status:          params.Status,
		Priority:        params.Priority,
		AssigneeID:      params.AssigneeID,
		ProjectID:       params.ProjectID,
		RestrictToUser:  params.RestrictToUser,
	})
	result := map[string]any{"issues": out, "returned_count": len(out)}
	scope := assistantCappedScope(ws.Slug, len(out), int(params.Limit), total)
	if scope.Truncated {
		result["note"] = "This list hit the limit — quote scope.total for the real number, and say how many of it you are showing. Raise limit (max 50) or narrow the filters to see more rows."
	}
	return assistantScopedResult(result, scope)
}

// ---------------------------------------------------------------------------
// search_issues
// ---------------------------------------------------------------------------

type assistantSearchIssuesArgs struct {
	WorkspaceID   string `json:"workspace_id"`
	Query         string `json:"query"`
	IncludeClosed bool   `json:"include_closed"`
	Limit         *int   `json:"limit"`
}

func (h *Handler) assistantSearchIssues(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	userUUID := caller.UUID
	var args assistantSearchIssuesArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, role, err := h.assistantMembership(ctx, userUUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	q := strings.TrimSpace(args.Query)
	if q == "" {
		return nil, errors.New("query is required")
	}

	terms := splitSearchTerms(q)
	queryNum, hasNum := parseQueryNumber(q)
	sqlQuery, sqlArgs := buildSearchQuery(q, terms, queryNum, hasNum, args.IncludeClosed,
		assistantVisibilityRestriction(role, userUUID))
	// Same placeholder contract as the SearchIssues handler: $4 is the
	// workspace, the last two are limit and offset.
	sqlArgs[3] = ws.ID
	sqlArgs[len(sqlArgs)-2] = int(assistantLimit(args.Limit))
	sqlArgs[len(sqlArgs)-1] = 0

	rows, err := h.DB.Query(ctx, sqlQuery, sqlArgs...)
	if err != nil {
		slog.Warn("assistant: search issues failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return nil, errors.New("could not search issues")
	}
	defer rows.Close()

	prefix := h.getIssuePrefix(ctx, ws.ID)
	out := []assistantIssueResult{}
	// The search query carries its own window-function total on every row, so
	// the exact number of matches costs nothing extra.
	var matchTotal *int64
	for rows.Next() {
		var sr searchResult
		if err := rows.Scan(
			&sr.issue.ID,
			&sr.issue.WorkspaceID,
			&sr.issue.Title,
			&sr.issue.Description,
			&sr.issue.Status,
			&sr.issue.Priority,
			&sr.issue.AssigneeType,
			&sr.issue.AssigneeID,
			&sr.issue.CreatorType,
			&sr.issue.CreatorID,
			&sr.issue.ParentIssueID,
			&sr.issue.AcceptanceCriteria,
			&sr.issue.ContextRefs,
			&sr.issue.Position,
			&sr.issue.StartDate,
			&sr.issue.DueDate,
			&sr.issue.CreatedAt,
			&sr.issue.UpdatedAt,
			&sr.issue.Number,
			&sr.issue.ProjectID,
			&sr.totalCount,
			&sr.matchSource,
			&sr.matchedCommentContent,
		); err != nil {
			slog.Warn("assistant: search scan failed", "error", err)
			return nil, errors.New("could not search issues")
		}
		identifier := prefix + "-" + strconv.Itoa(int(sr.issue.Number))
		matched := sr.totalCount
		matchTotal = &matched
		out = append(out, assistantIssueResult{
			Identifier:    identifier,
			Title:         sr.issue.Title,
			Status:        sr.issue.Status,
			Priority:      sr.issue.Priority,
			WorkspaceSlug: ws.Slug,
			URLPath:       assistantIssueURLPath(ws.Slug, identifier),
		})
	}
	if err := rows.Err(); err != nil {
		slog.Warn("assistant: search rows error", "error", err)
		return nil, errors.New("could not search issues")
	}
	if len(out) == 0 {
		// No rows means no window-function value came back; zero matches is an
		// exact total, not an unknown one.
		zero := int64(0)
		matchTotal = &zero
	}
	return assistantScopedResult(map[string]any{"issues": out},
		assistantCappedScope(ws.Slug, len(out), int(assistantLimit(args.Limit)), matchTotal))
}

// ---------------------------------------------------------------------------
// get_issue
// ---------------------------------------------------------------------------

// assistantResolveIssue is the executor's twin of loadIssueForUser: it accepts
// either a "MUL-123" identifier or a UUID, scopes the lookup to the workspace,
// and applies the non-owner visibility gate.
//
// It exists separately from loadIssueForUser only because that one is bound to
// an *http.Request (and to the X-Actor-Source exemption the assistant must not
// inherit). The resolution order and the 404-not-403 semantics are identical,
// and the RESOLVED issue is what every caller writes with — the raw `ref` never
// reaches a query, per the handler UUID-parsing convention.
func (h *Handler) assistantResolveIssue(ctx context.Context, caller assistantCaller, ws db.Workspace, role, ref string) (db.Issue, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.Issue{}, errors.New("ref is required (an issue identifier like MUL-123, or an issue UUID)")
	}

	// Identifier first; resolveIssueByIdentifier reports false for anything
	// that isn't PREFIX-NUMBER shaped, so a UUID falls through cleanly.
	issue, ok := h.resolveIssueByIdentifier(ctx, ref, uuidToString(ws.ID))
	if !ok {
		issueUUID, err := util.ParseUUID(ref)
		if err != nil {
			return db.Issue{}, errAssistantIssueNotFound
		}
		issue, err = h.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
			ID:          issueUUID,
			WorkspaceID: ws.ID,
		})
		if err != nil {
			return db.Issue{}, errAssistantIssueNotFound
		}
	}

	// Non-owner gate, mirroring issueAccessDenied. Fails CLOSED: a lookup
	// error denies rather than leaking.
	restrict := assistantVisibilityRestriction(role, caller.UUID)
	if restrict.Valid {
		owned, err := h.Queries.IssueBelongsToUser(ctx, db.IssueBelongsToUserParams{
			IssueID:     issue.ID,
			WorkspaceID: issue.WorkspaceID,
			UserID:      restrict,
		})
		if err != nil || !owned {
			return db.Issue{}, errAssistantIssueNotFound
		}
	}
	return issue, nil
}

type assistantIssueRefArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Ref         string `json:"ref"`
}

// assistantIssueDetail is the fuller shape get_issue returns — still bounded,
// but with the body and dates the model needs to actually answer about one
// issue rather than list it.
type assistantIssueDetail struct {
	assistantIssueResult
	Description   string `json:"description,omitempty"`
	AssigneeType  string `json:"assignee_type,omitempty"`
	AssigneeID    string `json:"assignee_id,omitempty"`
	ProjectID     string `json:"project_id,omitempty"`
	ParentIssueID string `json:"parent_issue_id,omitempty"`
	DueDate       string `json:"due_date,omitempty"`
	Archived      bool   `json:"archived,omitempty"`
	// Labels and SprintID are what remove_issue_label and move_issue_to_sprint
	// need to know before they act: "take the bug label off" is unanswerable
	// from a list row, and re-reading the issue is the cheap way to get it.
	Labels     []assistantLabelResult `json:"labels,omitempty"`
	SprintID   string                 `json:"sprint_id,omitempty"`
	SprintName string                 `json:"sprint_name,omitempty"`
	CreatedAt  string                 `json:"created_at"`
	UpdatedAt  string                 `json:"updated_at"`
}

func (h *Handler) assistantGetIssue(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantIssueRefArgs
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

	identifier := h.getIssuePrefix(ctx, ws.ID) + "-" + strconv.Itoa(int(issue.Number))
	detail := assistantIssueDetail{
		assistantIssueResult: assistantIssueResult{
			Identifier:    identifier,
			Title:         issue.Title,
			Status:        issue.Status,
			Priority:      issue.Priority,
			WorkspaceSlug: ws.Slug,
			URLPath:       assistantIssueURLPath(ws.Slug, identifier),
		},
		CreatedAt: timestampToString(issue.CreatedAt),
		UpdatedAt: timestampToString(issue.UpdatedAt),
	}
	if issue.Description.Valid {
		detail.Description = truncateRunes(issue.Description.String, issueBodyQueryMaxRunes)
	}
	if issue.AssigneeType.Valid {
		detail.AssigneeType = issue.AssigneeType.String
	}
	if issue.AssigneeID.Valid {
		detail.AssigneeID = uuidToString(issue.AssigneeID)
	}
	if issue.ProjectID.Valid {
		detail.ProjectID = uuidToString(issue.ProjectID)
	}
	if issue.ParentIssueID.Valid {
		detail.ParentIssueID = uuidToString(issue.ParentIssueID)
	}
	if issue.DueDate.Valid {
		detail.DueDate = issue.DueDate.Time.Format("2006-01-02")
	}
	detail.Archived = issue.ArchivedAt.Valid

	// Both of these are best-effort: a detail read that loses its labels is a
	// worse answer, not a failed one.
	if labels, lerr := h.Queries.ListLabelsByIssue(ctx, db.ListLabelsByIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: ws.ID,
	}); lerr == nil {
		for _, l := range labels {
			detail.Labels = append(detail.Labels, assistantLabelResult{
				ID: uuidToString(l.ID), Name: l.Name, Color: l.Color,
			})
		}
	}
	if sprint, serr := h.Queries.GetSprintForIssue(ctx, issue.ID); serr == nil {
		detail.SprintID = uuidToString(sprint.ID)
		detail.SprintName = sprint.Name
	}
	return json.Marshal(detail)
}

// ---------------------------------------------------------------------------
// list_projects / list_agents / list_members
// ---------------------------------------------------------------------------

type assistantProjectResult struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

func (h *Handler) assistantListProjects(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	ws, _, err := h.assistantWorkspaceScope(ctx, caller, raw)
	if err != nil {
		return nil, err
	}
	projects, err := h.Queries.ListProjects(ctx, db.ListProjectsParams{WorkspaceID: ws.ID})
	if err != nil {
		slog.Warn("assistant: list projects failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return nil, errors.New("could not load projects")
	}
	out := make([]assistantProjectResult, 0, len(projects))
	for _, p := range projects {
		out = append(out, assistantProjectResult{
			ID:     uuidToString(p.ID),
			Title:  p.Title,
			Status: p.Status,
		})
	}
	return assistantScopedResult(map[string]any{"projects": out}, assistantExactScope(ws.Slug, len(out)))
}

type assistantAgentResult struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (h *Handler) assistantListAgents(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	ws, role, err := h.assistantWorkspaceScope(ctx, caller, raw)
	if err != nil {
		return nil, err
	}
	workspaceID := uuidToString(ws.ID)

	// accessibleAgentIDs is the same private-agent predicate the chat and
	// assignment surfaces use. The actor is "member" — the assistant is never
	// an agent actor, so it never gets the agent-to-agent bypass.
	allowed, ok := h.accessibleAgentIDs(ctx, workspaceID, "member", caller.ID, role)
	if !ok {
		return nil, errors.New("could not resolve agent access")
	}
	agents, err := h.Queries.ListAllAgents(ctx, ws.ID)
	if err != nil {
		slog.Warn("assistant: list agents failed", "workspace_id", workspaceID, "error", err)
		return nil, errors.New("could not load agents")
	}
	out := make([]assistantAgentResult, 0, len(agents))
	for _, a := range agents {
		if a.ArchivedAt.Valid {
			continue
		}
		if _, permitted := allowed[uuidToString(a.ID)]; !permitted {
			continue
		}
		out = append(out, assistantAgentResult{ID: uuidToString(a.ID), Name: a.Name})
	}
	// The total is what THIS caller may see: the private-agent gate above is
	// part of the answer, not a page boundary.
	return assistantScopedResult(map[string]any{"agents": out}, assistantExactScope(ws.Slug, len(out)))
}

type assistantMemberResult struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Role   string `json:"role"`
}

func (h *Handler) assistantListMembers(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	ws, _, err := h.assistantWorkspaceScope(ctx, caller, raw)
	if err != nil {
		return nil, err
	}
	members, err := h.Queries.ListMembersWithUser(ctx, ws.ID)
	if err != nil {
		slog.Warn("assistant: list members failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return nil, errors.New("could not load members")
	}
	out := make([]assistantMemberResult, 0, len(members))
	for _, m := range members {
		out = append(out, assistantMemberResult{
			UserID: uuidToString(m.UserID),
			Name:   m.UserName,
			Role:   m.Role,
		})
	}
	return assistantScopedResult(map[string]any{"members": out}, assistantExactScope(ws.Slug, len(out)))
}

// ---------------------------------------------------------------------------
// usage_summary
// ---------------------------------------------------------------------------

type assistantUsageArgs struct {
	WorkspaceID string `json:"workspace_id"`
	RangeDays   *int   `json:"range_days"`
}

type assistantUsageDayResult struct {
	Date        string `json:"date"`
	Model       string `json:"model"`
	TotalTokens int64  `json:"total_tokens"`
	TaskCount   int32  `json:"task_count"`
}

type assistantUsageAgentResult struct {
	AgentID     string `json:"agent_id"`
	AgentName   string `json:"agent_name,omitempty"`
	Model       string `json:"model"`
	TotalTokens int64  `json:"total_tokens"`
	TaskCount   int32  `json:"task_count"`
}

// assistantWindowDays clamps an analytics window into its allowed band.
func assistantWindowDays(requested *int, def, max int) int {
	days := def
	if requested != nil && *requested > 0 {
		days = *requested
	}
	if days > max {
		days = max
	}
	return days
}

// assistantUsageSummary wraps the dashboard usage rollups.
//
// Token counts only, no cost: this server deliberately keeps the model
// dimension on the wire and prices it client-side from a pricing table, so
// there is no server-side cost number to report. Inventing one here would be
// a number the dashboard disagrees with.
func (h *Handler) assistantUsageSummary(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantUsageArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	days := assistantWindowDays(args.RangeDays, 30, assistant.MaxUsageRangeDays)
	// The caller's calendar, not the server's: "the last 30 days" ends with the
	// day they are having, and the daily buckets are cut on their midnights so
	// the rollup the model quotes matches the dashboard they would open.
	from, _, window := h.assistantDayWindow(ctx, caller, days)
	since := pgtype.Timestamptz{Time: from, Valid: true}

	daily, err := h.listDashboardUsageDaily(ctx, ws.ID, window.Timezone, since, pgtype.UUID{})
	if err != nil {
		slog.Warn("assistant: usage daily failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return nil, errors.New("could not load usage")
	}
	byAgentRows, err := h.listDashboardUsageByAgent(ctx, ws.ID, since, pgtype.UUID{})
	if err != nil {
		slog.Warn("assistant: usage by agent failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return nil, errors.New("could not load usage")
	}

	// Agent names, so the model can say "Dev squad lead" instead of a UUID.
	// One query, and only when there is something to name.
	names := map[string]string{}
	if len(byAgentRows) > 0 {
		if agents, aerr := h.Queries.ListAllAgents(ctx, ws.ID); aerr == nil {
			for _, a := range agents {
				names[uuidToString(a.ID)] = a.Name
			}
		}
	}

	var totalTokens int64
	var totalTasks int64
	byDay := make([]assistantUsageDayResult, 0, len(daily))
	for _, row := range daily {
		tokens := row.InputTokens + row.OutputTokens + row.CacheReadTokens + row.CacheWriteTokens
		totalTokens += tokens
		totalTasks += int64(row.TaskCount)
		byDay = append(byDay, assistantUsageDayResult{
			Date:        row.Date,
			Model:       row.Model,
			TotalTokens: tokens,
			TaskCount:   row.TaskCount,
		})
	}
	byAgent := make([]assistantUsageAgentResult, 0, len(byAgentRows))
	for _, row := range byAgentRows {
		byAgent = append(byAgent, assistantUsageAgentResult{
			AgentID:     row.AgentID,
			AgentName:   names[row.AgentID],
			Model:       row.Model,
			TotalTokens: row.InputTokens + row.OutputTokens + row.CacheReadTokens + row.CacheWriteTokens,
			TaskCount:   row.TaskCount,
		})
	}

	scope := newAssistantScopeBuilder().withWindow(window)
	scope.checkedWorkspace(ws.Slug, nil, false)
	return assistantScopedResult(map[string]any{
		"workspace_slug": ws.Slug,
		"range_days":     days,
		"total_tokens":   totalTokens,
		"total_tasks":    totalTasks,
		"by_day":         byDay,
		"by_agent":       byAgent,
		"note":           "Token counts only. This instance prices tokens in the client, so no cost figure is available server-side.",
	}, scope.scope())
}

// ---------------------------------------------------------------------------
// activity_digest
// ---------------------------------------------------------------------------

type assistantDigestArgs struct {
	WorkspaceID string `json:"workspace_id"`
	SinceDays   *int   `json:"since_days"`
}

type assistantActivityResult struct {
	WorkspaceSlug   string `json:"workspace_slug"`
	IssueIdentifier string `json:"issue_identifier,omitempty"`
	IssueTitle      string `json:"issue_title,omitempty"`
	Type            string `json:"type"`
	ActorType       string `json:"actor_type,omitempty"`
	Actor           string `json:"actor,omitempty"`
	CreatedAt       string `json:"created_at"`
}

func (h *Handler) assistantActivityDigest(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantDigestArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	days := assistantWindowDays(args.SinceDays, 7, assistant.MaxDigestDays)
	// Calendar days in the caller's zone. days=1 is "today", and something
	// filed at 23:50 local is in it even when UTC has already turned over —
	// the digest that dropped a user's whole evening is this boundary.
	from, _, window := h.assistantDayWindow(ctx, caller, days)
	since := pgtype.Timestamptz{Time: from, Valid: true}

	var scope []db.Workspace
	if wsID := strings.TrimSpace(args.WorkspaceID); wsID != "" {
		ws, _, err := h.assistantMembership(ctx, caller.UUID, wsID)
		if err != nil {
			return nil, err
		}
		scope = []db.Workspace{ws}
	} else {
		workspaces, err := h.Queries.ListWorkspaces(ctx, caller.UUID)
		if err != nil {
			slog.Warn("assistant: list workspaces failed", "error", err)
			return nil, errors.New("could not load your workspaces")
		}
		scope = workspaces
	}

	out := []assistantActivityResult{}
	coverage := newAssistantScopeBuilder().withWindow(window)
	for _, ws := range scope {
		rows, truncated, err := h.assistantWorkspaceActivity(ctx, caller, ws, since)
		if err != nil {
			// One unreadable workspace must not sink the whole digest — and it
			// is named, so the digest cannot quietly omit it.
			slog.Warn("assistant: activity digest failed", "workspace_id", uuidToString(ws.ID), "error", err)
			coverage.failedWorkspace(ws.Slug)
			continue
		}
		// No count query: the rows are visibility-filtered in Go after the
		// read, so any aggregate would describe a different set. Unknown is the
		// honest answer.
		coverage.checkedWorkspace(ws.Slug, nil, truncated)
		out = append(out, rows...)
	}
	return assistantScopedResult(map[string]any{"since_days": days, "activity": out}, coverage.scope())
}

// assistantWorkspaceActivity returns one workspace's rows plus whether the
// per-workspace cap cut the window short.
func (h *Handler) assistantWorkspaceActivity(ctx context.Context, caller assistantCaller, ws db.Workspace, since pgtype.Timestamptz) ([]assistantActivityResult, bool, error) {
	role := ""
	if member, err := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID:      caller.UUID,
		WorkspaceID: ws.ID,
	}); err == nil {
		role = member.Role
	}
	restrict := assistantVisibilityRestriction(role, caller.UUID)

	rows, err := h.Queries.ListRecentActivities(ctx, db.ListRecentActivitiesParams{
		WorkspaceID: ws.ID,
		Since:       since,
		Limit:       assistant.MaxActivityRows,
	})
	if err != nil {
		return nil, false, err
	}
	// Truncation is decided on the ROWS THE QUERY RETURNED, before the
	// visibility filter below thins them: a digest that shows three of fifty
	// rows is still a digest that stopped at the cap.
	truncated := len(rows) >= assistant.MaxActivityRows
	if len(rows) == 0 {
		return nil, false, nil
	}

	prefix := h.getIssuePrefix(ctx, ws.ID)
	actors := h.assistantActorNames(ctx, ws.ID)
	// Memoized per issue: a busy issue produces many rows and the ownership
	// predicate is the same answer for all of them.
	visible := map[string]bool{}

	out := make([]assistantActivityResult, 0, len(rows))
	for _, row := range rows {
		if restrict.Valid && row.IssueID.Valid {
			key := uuidToString(row.IssueID)
			allowed, known := visible[key]
			if !known {
				owned, oerr := h.Queries.IssueBelongsToUser(ctx, db.IssueBelongsToUserParams{
					IssueID:     row.IssueID,
					WorkspaceID: ws.ID,
					UserID:      restrict,
				})
				allowed = oerr == nil && owned // fail closed
				visible[key] = allowed
			}
			if !allowed {
				continue
			}
		}
		entry := assistantActivityResult{
			WorkspaceSlug: ws.Slug,
			Type:          row.Action,
			CreatedAt:     timestampToString(row.CreatedAt),
		}
		if row.IssueNumber.Valid {
			entry.IssueIdentifier = prefix + "-" + strconv.Itoa(int(row.IssueNumber.Int32))
		}
		if row.IssueTitle.Valid {
			entry.IssueTitle = row.IssueTitle.String
		}
		if row.ActorType.Valid {
			entry.ActorType = row.ActorType.String
		}
		if row.ActorID.Valid {
			entry.Actor = actors[uuidToString(row.ActorID)]
		}
		out = append(out, entry)
	}
	return out, truncated, nil
}

// assistantActorNames maps member and agent UUIDs to display names for one
// workspace. Two queries, so a digest can say who did something instead of
// printing raw ids. Best-effort: a lookup failure just yields fewer names.
func (h *Handler) assistantActorNames(ctx context.Context, workspaceID pgtype.UUID) map[string]string {
	names := map[string]string{}
	if members, err := h.Queries.ListMembersWithUser(ctx, workspaceID); err == nil {
		for _, m := range members {
			names[uuidToString(m.UserID)] = m.UserName
		}
	}
	if agents, err := h.Queries.ListAllAgents(ctx, workspaceID); err == nil {
		for _, a := range agents {
			names[uuidToString(a.ID)] = a.Name
		}
	}
	return names
}

// ---------------------------------------------------------------------------
// inbox_summary
// ---------------------------------------------------------------------------

type assistantInboxResult struct {
	WorkspaceSlug string `json:"workspace_slug"`
	Title         string `json:"title"`
	Preview       string `json:"preview,omitempty"`
	Type          string `json:"type"`
	CreatedAt     string `json:"created_at"`
}

// assistantInboxPreviewChars keeps one notification body to a line or two.
const assistantInboxPreviewChars = 240

// assistantInboxSummary rolls up the caller's UNREAD inbox across every
// workspace they belong to. The inbox is recipient-scoped per workspace, so
// "my inbox" is a fan-out, sorted back into one newest-first list.
func (h *Handler) assistantInboxSummary(ctx context.Context, caller assistantCaller) (json.RawMessage, error) {
	workspaces, err := h.Queries.ListWorkspaces(ctx, caller.UUID)
	if err != nil {
		slog.Warn("assistant: list workspaces failed", "error", err)
		return nil, errors.New("could not load your workspaces")
	}

	type dated struct {
		at   time.Time
		item assistantInboxResult
	}
	var all []dated
	coverage := newAssistantScopeBuilder()
	for _, ws := range workspaces {
		items, ierr := h.Queries.ListInboxItems(ctx, db.ListInboxItemsParams{
			WorkspaceID:   ws.ID,
			RecipientType: "member",
			RecipientID:   caller.UUID,
		})
		if ierr != nil {
			slog.Warn("assistant: list inbox failed", "workspace_id", uuidToString(ws.ID), "error", ierr)
			coverage.failedWorkspace(ws.Slug)
			continue
		}
		unread := int64(0)
		for _, item := range items {
			if !item.Read {
				unread++
			}
		}
		// ListInboxItems has no LIMIT, so this is an exact per-workspace count
		// even though the merged list below is capped.
		coverage.checkedWorkspace(ws.Slug, &unread, false)
		for _, item := range items {
			if item.Read {
				continue
			}
			entry := assistantInboxResult{
				WorkspaceSlug: ws.Slug,
				Title:         item.Title,
				Type:          item.Type,
				CreatedAt:     timestampToString(item.CreatedAt),
			}
			if item.Body.Valid {
				entry.Preview = truncateRunes(item.Body.String, assistantInboxPreviewChars)
			}
			all = append(all, dated{at: item.CreatedAt.Time, item: entry})
		}
	}

	// One list the user reads top-down, so it is sorted globally and then
	// capped — not capped per workspace, which would bury a fresh item behind
	// a noisy workspace's backlog.
	sort.Slice(all, func(i, j int) bool { return all[i].at.After(all[j].at) })
	// unread_count is the number of unread items that EXIST, not the number
	// that survived the cap — it used to be the latter, which made a busy inbox
	// report exactly 30 unread forever.
	unreadTotal := len(all)
	if len(all) > assistant.MaxInboxRows {
		all = all[:assistant.MaxInboxRows]
	}
	out := make([]assistantInboxResult, 0, len(all))
	for _, d := range all {
		out = append(out, d.item)
	}
	envelope := coverage.scope()
	envelope.Truncated = unreadTotal > len(out)
	return assistantScopedResult(map[string]any{
		"unread_count":   unreadTotal,
		"returned_count": len(out),
		"items":          out,
	}, envelope)
}

// ---------------------------------------------------------------------------
// qa_status
// ---------------------------------------------------------------------------

func (h *Handler) assistantQAStatus(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	ws, _, err := h.assistantWorkspaceScope(ctx, caller, raw)
	if err != nil {
		return nil, err
	}
	// Both queries are workspace-wide (project_id NULL) and carry their own
	// windows — 30 days for runs. No new aggregate SQL was written for this.
	totals, err := h.Queries.QAMetricsRunTotals(ctx, db.QAMetricsRunTotalsParams{WorkspaceID: ws.ID})
	if err != nil {
		slog.Warn("assistant: qa totals failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return nil, errors.New("could not load QA status")
	}
	coverage, cerr := h.Queries.QAMetricsScriptCoverage(ctx, db.QAMetricsScriptCoverageParams{WorkspaceID: ws.ID})
	if cerr != nil {
		slog.Warn("assistant: qa coverage failed", "workspace_id", uuidToString(ws.ID), "error", cerr)
	}

	// QAMetricsRunTotals carries its own `now() - interval '30 days'`, so the
	// boundary is an absolute instant rather than a local midnight. The window
	// is therefore RENDERED in the caller's zone, not recomputed in it —
	// claiming a calendar window here would be an invented number.
	_, _, window := h.assistantRollingWindow(ctx, caller, 30*24*time.Hour)
	scope := newAssistantScopeBuilder().withWindow(window)
	scope.checkedWorkspace(ws.Slug, nil, false)
	return assistantScopedResult(map[string]any{
		"workspace_slug":  ws.Slug,
		"window_days":     30,
		"runs_total":      totals.Total,
		"runs_passed":     totals.Passed,
		"runs_failed":     totals.Failed,
		"runs_skipped":    totals.Skipped,
		"cases_automated": coverage.Automated,
		"cases_scripted":  coverage.Scripted,
	}, scope.scope())
}

// ---------------------------------------------------------------------------
// Resolvers — name → row
// ---------------------------------------------------------------------------
//
// The assistant's users talk in names ("the TEST project", "the bug label",
// "Sprint 12"), and the free-tier model is a poor id-carrier. Each resolver
// below accepts a UUID or a human name so a single tool call can succeed from
// what the user actually said, while every WRITE still addresses the resolved
// row's ID — the raw string never reaches a query (handler UUID convention).
//
// Ambiguity is an ERROR, not a coin flip: two projects called "Web" produce a
// refusal naming both, because silently picking one is how an assistant edits
// the wrong thing.

// assistantAmbiguous builds the refusal a resolver returns when a name matches
// more than one row. Listing the candidates is what lets the model come back
// with a question instead of a guess.
func assistantAmbiguous(kind, ref string, candidates []string) error {
	sort.Strings(candidates)
	if len(candidates) > 8 {
		candidates = candidates[:8]
	}
	return fmt.Errorf("%q matches more than one %s (%s) — ask which one, or pass its id",
		ref, kind, strings.Join(candidates, ", "))
}

// assistantResolveProject accepts a project UUID or a title. Exact
// case-insensitive title match wins; a unique substring match is the fallback
// so "TEST" finds "TEST project".
func (h *Handler) assistantResolveProject(ctx context.Context, ws db.Workspace, ref string) (db.Project, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.Project{}, errors.New("project is required (a project title or a project UUID)")
	}
	if projectUUID, err := util.ParseUUID(ref); err == nil {
		project, perr := h.Queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
			ID:          projectUUID,
			WorkspaceID: ws.ID,
		})
		if perr != nil {
			return db.Project{}, errors.New("project not found in that workspace")
		}
		return project, nil
	}

	projects, err := h.Queries.ListProjects(ctx, db.ListProjectsParams{WorkspaceID: ws.ID})
	if err != nil {
		slog.Warn("assistant: list projects failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return db.Project{}, errors.New("could not load projects")
	}
	needle := strings.ToLower(ref)
	var exact, partial []db.Project
	for _, p := range projects {
		title := strings.ToLower(p.Title)
		switch {
		case title == needle:
			exact = append(exact, p)
		case strings.Contains(title, needle):
			partial = append(partial, p)
		}
	}
	matches := exact
	if len(matches) == 0 {
		matches = partial
	}
	switch len(matches) {
	case 0:
		return db.Project{}, fmt.Errorf("no project called %q in that workspace — list_projects shows what exists", ref)
	case 1:
		return matches[0], nil
	default:
		titles := make([]string, 0, len(matches))
		for _, p := range matches {
			titles = append(titles, p.Title)
		}
		return db.Project{}, assistantAmbiguous("project", ref, titles)
	}
}

// assistantResolveLabel accepts a label UUID or an exact (case-insensitive)
// label name. Labels are short and unique per workspace, so there is no
// substring fallback — "bug" must not silently resolve to "type:bug".
func (h *Handler) assistantResolveLabel(ctx context.Context, ws db.Workspace, ref string) (db.IssueLabel, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.IssueLabel{}, errors.New("label is required (a label name or a label UUID)")
	}
	if labelUUID, err := util.ParseUUID(ref); err == nil {
		label, lerr := h.Queries.GetLabel(ctx, db.GetLabelParams{ID: labelUUID, WorkspaceID: ws.ID})
		if lerr != nil {
			return db.IssueLabel{}, errors.New("label not found in that workspace")
		}
		return label, nil
	}
	labels, err := h.Queries.ListLabels(ctx, ws.ID)
	if err != nil {
		slog.Warn("assistant: list labels failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return db.IssueLabel{}, errors.New("could not load labels")
	}
	needle := strings.ToLower(ref)
	for _, l := range labels {
		if strings.ToLower(l.Name) == needle {
			return l, nil
		}
	}
	return db.IssueLabel{}, fmt.Errorf("no label called %q in that workspace — list_labels shows what exists, create_label makes a new one", ref)
}

// assistantResolveSprint accepts a sprint UUID or a sprint name, scoped to the
// workspace. Sprint names repeat across projects ("Sprint 1" in three of them),
// so a duplicate name is refused with the project titles attached.
func (h *Handler) assistantResolveSprint(ctx context.Context, ws db.Workspace, ref string) (db.Sprint, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.Sprint{}, errors.New("sprint is required (a sprint name or a sprint UUID)")
	}
	if sprintUUID, err := util.ParseUUID(ref); err == nil {
		sprint, serr := h.Queries.GetSprint(ctx, db.GetSprintParams{ID: sprintUUID, WorkspaceID: ws.ID})
		if serr != nil {
			return db.Sprint{}, errors.New("sprint not found in that workspace")
		}
		return sprint, nil
	}
	rows, err := h.Queries.ListSprintsForWorkspace(ctx, ws.ID)
	if err != nil {
		slog.Warn("assistant: list sprints failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return db.Sprint{}, errors.New("could not load sprints")
	}
	needle := strings.ToLower(ref)
	var matched []string
	var found pgtype.UUID
	for _, row := range rows {
		if strings.ToLower(row.Name) != needle {
			continue
		}
		matched = append(matched, row.Name+" ("+row.ProjectTitle+")")
		found = row.ID
	}
	switch len(matched) {
	case 0:
		return db.Sprint{}, fmt.Errorf("no sprint called %q in that workspace — list_sprints shows what exists", ref)
	case 1:
		sprint, serr := h.Queries.GetSprint(ctx, db.GetSprintParams{ID: found, WorkspaceID: ws.ID})
		if serr != nil {
			return db.Sprint{}, errors.New("sprint not found in that workspace")
		}
		return sprint, nil
	default:
		return db.Sprint{}, assistantAmbiguous("sprint", ref, matched)
	}
}

// ---------------------------------------------------------------------------
// get_project
// ---------------------------------------------------------------------------

type assistantProjectRefArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Project     string `json:"project"`
}

// assistantProjectDetail is what get_project returns. Wider than the list row
// because this is the shape the model reads before deciding what to change.
type assistantProjectDetail struct {
	assistantProjectResult
	Description string `json:"description,omitempty"`
	Priority    string `json:"priority"`
	LeadType    string `json:"lead_type,omitempty"`
	LeadID      string `json:"lead_id,omitempty"`
	SquadID     string `json:"squad_id,omitempty"`
	IssueCount  int64  `json:"issue_count"`
	DoneCount   int64  `json:"done_count"`
	URLPath     string `json:"url_path"`
}

// assistantProjectURLPath mirrors the frontend route (packages/core/paths:
// `${ws}/projects/${id}`).
func assistantProjectURLPath(slug, projectID string) string {
	if slug == "" || projectID == "" {
		return ""
	}
	return "/" + slug + "/projects/" + projectID
}

func (h *Handler) assistantProjectDetailResult(ctx context.Context, ws db.Workspace, p db.Project) assistantProjectDetail {
	detail := assistantProjectDetail{
		assistantProjectResult: assistantProjectResult{
			ID:     uuidToString(p.ID),
			Title:  p.Title,
			Status: p.Status,
		},
		Priority: p.Priority,
		URLPath:  assistantProjectURLPath(ws.Slug, uuidToString(p.ID)),
	}
	if p.Description.Valid {
		detail.Description = truncateRunes(p.Description.String, issueBodyQueryMaxRunes)
	}
	if p.LeadType.Valid {
		detail.LeadType = p.LeadType.String
	}
	if p.LeadID.Valid {
		detail.LeadID = uuidToString(p.LeadID)
	}
	if p.SquadID.Valid {
		detail.SquadID = uuidToString(p.SquadID)
	}
	detail.IssueCount, detail.DoneCount = h.loadProjectIssueStats(ctx, p.ID)
	return detail
}

func (h *Handler) assistantGetProject(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantProjectRefArgs
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
	return json.Marshal(h.assistantProjectDetailResult(ctx, ws, project))
}

// ---------------------------------------------------------------------------
// list_sprints
// ---------------------------------------------------------------------------

type assistantListSprintsArgs struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
}

type assistantSprintResult struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	ProjectID    string `json:"project_id"`
	ProjectTitle string `json:"project_title,omitempty"`
	StartDate    string `json:"start_date,omitempty"`
	EndDate      string `json:"end_date,omitempty"`
}

func assistantSprintRow(s db.Sprint, projectTitle string) assistantSprintResult {
	out := assistantSprintResult{
		ID:           uuidToString(s.ID),
		Name:         s.Name,
		Status:       s.Status,
		ProjectID:    uuidToString(s.ProjectID),
		ProjectTitle: projectTitle,
	}
	if s.StartDate.Valid {
		out.StartDate = s.StartDate.Time.Format("2006-01-02")
	}
	if s.EndDate.Valid {
		out.EndDate = s.EndDate.Time.Format("2006-01-02")
	}
	return out
}

func (h *Handler) assistantListSprints(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantListSprintsArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}

	if projectRef := strings.TrimSpace(args.ProjectID); projectRef != "" {
		project, perr := h.assistantResolveProject(ctx, ws, projectRef)
		if perr != nil {
			return nil, perr
		}
		sprints, serr := h.Queries.ListSprintsByProject(ctx, project.ID)
		if serr != nil {
			slog.Warn("assistant: list sprints by project failed", "project_id", uuidToString(project.ID), "error", serr)
			return nil, errors.New("could not load sprints")
		}
		out := make([]assistantSprintResult, 0, len(sprints))
		for _, s := range sprints {
			out = append(out, assistantSprintRow(s, project.Title))
		}
		return assistantScopedResult(map[string]any{"sprints": out}, assistantExactScope(ws.Slug, len(out)))
	}

	// Workspace-wide: the same rollup the bulk "move to sprint" picker uses.
	rows, err := h.Queries.ListSprintsForWorkspace(ctx, ws.ID)
	if err != nil {
		slog.Warn("assistant: list sprints failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return nil, errors.New("could not load sprints")
	}
	out := make([]assistantSprintResult, 0, len(rows))
	for _, row := range rows {
		out = append(out, assistantSprintResult{
			ID:           uuidToString(row.ID),
			Name:         row.Name,
			Status:       row.Status,
			ProjectID:    uuidToString(row.ProjectID),
			ProjectTitle: row.ProjectTitle,
		})
	}
	return assistantScopedResult(map[string]any{"sprints": out}, assistantExactScope(ws.Slug, len(out)))
}

// ---------------------------------------------------------------------------
// list_labels / list_squads
// ---------------------------------------------------------------------------

type assistantLabelResult struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

func (h *Handler) assistantListLabels(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	ws, _, err := h.assistantWorkspaceScope(ctx, caller, raw)
	if err != nil {
		return nil, err
	}
	labels, err := h.Queries.ListLabels(ctx, ws.ID)
	if err != nil {
		slog.Warn("assistant: list labels failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return nil, errors.New("could not load labels")
	}
	out := make([]assistantLabelResult, 0, len(labels))
	for _, l := range labels {
		out = append(out, assistantLabelResult{ID: uuidToString(l.ID), Name: l.Name, Color: l.Color})
	}
	return assistantScopedResult(map[string]any{"labels": out}, assistantExactScope(ws.Slug, len(out)))
}

type assistantSquadResult struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	LeaderID    string `json:"leader_id,omitempty"`
}

// assistantListSquads lists the workspace's squads. ListSquads already excludes
// archived rows, and squad membership is not privacy-gated the way a private
// agent is — a squad is a team, visible to the workspace.
func (h *Handler) assistantListSquads(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	ws, _, err := h.assistantWorkspaceScope(ctx, caller, raw)
	if err != nil {
		return nil, err
	}
	squads, err := h.Queries.ListSquads(ctx, ws.ID)
	if err != nil {
		slog.Warn("assistant: list squads failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return nil, errors.New("could not load squads")
	}
	out := make([]assistantSquadResult, 0, len(squads))
	for _, s := range squads {
		row := assistantSquadResult{
			ID:          uuidToString(s.ID),
			Name:        s.Name,
			Description: truncateRunes(s.Description, assistantInboxPreviewChars),
		}
		if s.LeaderID.Valid {
			row.LeaderID = uuidToString(s.LeaderID)
		}
		out = append(out, row)
	}
	return assistantScopedResult(map[string]any{"squads": out}, assistantExactScope(ws.Slug, len(out)))
}

// ---------------------------------------------------------------------------
// list_comments
// ---------------------------------------------------------------------------

type assistantListCommentsArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Ref         string `json:"ref"`
	Limit       *int   `json:"limit"`
}

type assistantCommentResult struct {
	// ID is what update_comment / resolve_comment / delete_comment address. A
	// thread read that cannot name its comments makes those three unusable —
	// the model would have nothing to pass but the text.
	ID        string `json:"comment_id"`
	Author    string `json:"author,omitempty"`
	Type      string `json:"author_type"`
	Body      string `json:"body"`
	Resolved  bool   `json:"resolved,omitempty"`
	CreatedAt string `json:"created_at"`
}

// assistantCommentPreviewChars bounds one comment in a thread read. Agent
// comments carry whole QA reports; the model needs the gist of each, not the
// transcript, and 20 of them must still fit the context window.
const assistantCommentPreviewChars = 1200

func (h *Handler) assistantListComments(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantListCommentsArgs
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

	// Newest-first from the DB (a long issue's tail is what matters), then
	// flipped back to reading order for the model.
	rows, err := h.Queries.ListRecentCommentsForIssue(ctx, db.ListRecentCommentsForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: ws.ID,
		Limit:       assistantLimit(args.Limit),
	})
	if err != nil {
		slog.Warn("assistant: list comments failed", "issue_id", uuidToString(issue.ID), "error", err)
		return nil, errors.New("could not load comments")
	}
	actors := h.assistantActorNames(ctx, ws.ID)
	out := make([]assistantCommentResult, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		out = append(out, assistantCommentResult{
			ID:        uuidToString(row.ID),
			Author:    actors[uuidToString(row.AuthorID)],
			Type:      row.AuthorType,
			Body:      truncateRunes(row.Content, assistantCommentPreviewChars),
			Resolved:  row.ResolvedAt.Valid,
			CreatedAt: timestampToString(row.CreatedAt),
		})
	}
	identifier := h.getIssuePrefix(ctx, ws.ID) + "-" + strconv.Itoa(int(issue.Number))
	// The thread is read from the NEWEST end, so a truncated read is missing
	// the start of the conversation — exactly the half a summary would need.
	var total *int64
	if n, cerr := h.Queries.CountComments(ctx, db.CountCommentsParams{
		IssueID:     issue.ID,
		WorkspaceID: ws.ID,
	}); cerr == nil {
		total = &n
	}
	return assistantScopedResult(map[string]any{
		"issue_identifier": identifier,
		"comments":         out,
	}, assistantCappedScope(ws.Slug, len(out), int(assistantLimit(args.Limit)), total))
}

// ---------------------------------------------------------------------------
// list_runtimes
// ---------------------------------------------------------------------------

// assistantRuntimeResult is the grounding shape for create_agent: an agent
// MUST be bound to a runtime, so the model needs the ids and — more usefully —
// which of them are actually online. A runtime this user may not build on is
// filtered out here rather than rejected later by the handler.
type assistantRuntimeResult struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	RuntimeMode string `json:"runtime_mode"`
	Status      string `json:"status"`
}

func (h *Handler) assistantListRuntimes(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	ws, role, err := h.assistantWorkspaceScope(ctx, caller, raw)
	if err != nil {
		return nil, err
	}
	runtimes, err := h.Queries.ListAgentRuntimes(ctx, ws.ID)
	if err != nil {
		slog.Warn("assistant: list runtimes failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return nil, errors.New("could not load runtimes")
	}
	// canUseRuntimeForAgent is the same predicate CreateAgent applies; the
	// member is rebuilt from what membership already resolved rather than
	// re-queried.
	member := db.Member{UserID: caller.UUID, WorkspaceID: ws.ID, Role: role}
	out := make([]assistantRuntimeResult, 0, len(runtimes))
	for _, rt := range runtimes {
		if !canUseRuntimeForAgent(member, rt) {
			continue
		}
		out = append(out, assistantRuntimeResult{
			ID:          uuidToString(rt.ID),
			Name:        rt.Name,
			Provider:    rt.Provider,
			RuntimeMode: rt.RuntimeMode,
			Status:      rt.Status,
		})
	}
	return assistantScopedResult(map[string]any{
		"runtimes": out,
		"note":     "An agent must be created on one of these runtime ids. If this list is empty the workspace has no runtime connected yet, and no agent can be created until someone connects one in Settings → Runtimes.",
	}, assistantExactScope(ws.Slug, len(out)))
}

// ---------------------------------------------------------------------------
// list_skills
// ---------------------------------------------------------------------------

type assistantListSkillsArgs struct {
	WorkspaceID string `json:"workspace_id"`
	AgentID     string `json:"agent_id"`
}

type assistantSkillResult struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// assistantListSkills lists the workspace's skill library, or — when agent_id
// is given — the skills attached to one agent. Two listings behind one tool
// because the question is always the same shape ("what can it do"), and the
// model picking between two near-identical tool names is a reliable way to
// lose a round.
func (h *Handler) assistantListSkills(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantListSkillsArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}

	if agentRef := strings.TrimSpace(args.AgentID); agentRef != "" {
		agent, aerr := h.assistantResolveAgent(ctx, caller, ws, agentRef)
		if aerr != nil {
			return nil, aerr
		}
		rows, lerr := h.Queries.ListAgentSkillSummaries(ctx, agent.ID)
		if lerr != nil {
			slog.Warn("assistant: list agent skills failed", "agent_id", uuidToString(agent.ID), "error", lerr)
			return nil, errors.New("could not load the agent's skills")
		}
		out := make([]assistantSkillResult, 0, len(rows))
		for _, s := range rows {
			out = append(out, assistantSkillResult{
				ID:          uuidToString(s.ID),
				Name:        s.Name,
				Description: truncateRunes(s.Description, assistantInboxPreviewChars),
			})
		}
		return assistantScopedResult(map[string]any{
			"agent_id":   uuidToString(agent.ID),
			"agent_name": agent.Name,
			"skills":     out,
		}, assistantExactScope(ws.Slug, len(out)))
	}

	rows, err := h.Queries.ListSkillSummariesByWorkspace(ctx, ws.ID)
	if err != nil {
		slog.Warn("assistant: list skills failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return nil, errors.New("could not load skills")
	}
	out := make([]assistantSkillResult, 0, len(rows))
	for _, s := range rows {
		out = append(out, assistantSkillResult{
			ID:          uuidToString(s.ID),
			Name:        s.Name,
			Description: truncateRunes(s.Description, assistantInboxPreviewChars),
		})
	}
	return assistantScopedResult(map[string]any{"skills": out}, assistantExactScope(ws.Slug, len(out)))
}

// assistantResolveAgent accepts an agent UUID or an agent name, and refuses an
// agent this caller may not see — the same private-agent predicate list_agents
// applies, so the resolver can never be used to reach around that gate.
func (h *Handler) assistantResolveAgent(ctx context.Context, caller assistantCaller, ws db.Workspace, ref string) (db.Agent, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.Agent{}, errors.New("agent_id is required (an agent name or an agent UUID)")
	}
	role := ""
	if member, merr := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID:      caller.UUID,
		WorkspaceID: ws.ID,
	}); merr == nil {
		role = member.Role
	}
	allowed, ok := h.accessibleAgentIDs(ctx, uuidToString(ws.ID), "member", caller.ID, role)
	if !ok {
		return db.Agent{}, errors.New("could not resolve agent access")
	}
	agents, err := h.Queries.ListAllAgents(ctx, ws.ID)
	if err != nil {
		slog.Warn("assistant: list agents failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return db.Agent{}, errors.New("could not load agents")
	}

	byID, _ := util.ParseUUID(ref)
	needle := strings.ToLower(ref)
	var matches []db.Agent
	for _, a := range agents {
		if _, permitted := allowed[uuidToString(a.ID)]; !permitted {
			continue
		}
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
		return db.Agent{}, fmt.Errorf("no agent called %q in that workspace — list_agents shows what exists", ref)
	default:
		names := make([]string, 0, len(matches))
		for _, a := range matches {
			names = append(names, a.Name+" ("+uuidToString(a.ID)+")")
		}
		return db.Agent{}, assistantAmbiguous("agent", ref, names)
	}
}
