package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/middleware"
	"github.com/jamshidtulaganov/agora/server/internal/util"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// The assistant's WRITE tools.
//
// Every one of them runs the REAL HTTP handler in-process rather than talking
// to Queries directly. That is deliberate and it is the whole design of this
// file: a create or an update is not one INSERT — it is a duplicate guard, a
// project/squad binding check, a private-agent gate, four status gates, an
// event publish, task-queue reconciliation, inbox fan-out, automations, and
// tracker mirroring. Re-implementing any subset would produce an assistant
// whose writes silently behave differently from the same click in the UI, and
// would drift further with every future change to those handlers.
//
// The synthetic request carries ONLY X-User-ID and X-Workspace-ID. It never
// sets X-Agent-ID, X-Task-ID or X-Actor-Source, so every gate downstream reads
// the caller as a plain human member — the most restrictive interpretation
// available. There is no way for this path to be granted more than the user
// has: membership is checked here first, and the handlers re-derive everything
// else from the same headers a browser would send.
//
// The request is ALSO run through the workspace middleware the router mounts in
// front of the same route. That is not belt-and-braces — it is load-bearing
// twice over:
//
//   - Role gates live in the MIDDLEWARE, not in the handler, for most
//     admin-only routes (invites, member roles, workspace settings). A
//     synthetic request that skipped it would hand an ordinary member the
//     owner's buttons. assistantInvokeAs takes the same role list the router
//     passes so the refusal is the product's own, not a second implementation
//     of it.
//   - Handlers read the workspace from ctxWorkspaceID(), which the middleware
//     populates. MarkAllInboxRead is the one that made this visible: it reads
//     ONLY from context, so before the middleware ran the synthetic request it
//     could not see a workspace at all.

// assistantRecorder captures an in-process handler response. It is the
// http.ResponseWriter half of the invocation; net/http/httptest is a testing
// package and has no business in a production path.
type assistantRecorder struct {
	headers http.Header
	status  int
	body    bytes.Buffer
}

func newAssistantRecorder() *assistantRecorder {
	return &assistantRecorder{headers: http.Header{}, status: http.StatusOK}
}

func (rec *assistantRecorder) Header() http.Header { return rec.headers }

func (rec *assistantRecorder) Write(b []byte) (int, error) { return rec.body.Write(b) }

func (rec *assistantRecorder) WriteHeader(status int) { rec.status = status }

// assistantInvoke calls one of the real HTTP handlers as the given user and
// returns its status and body, behind the member-level workspace middleware.
//
// Pass workspaceID == "" for the handful of routes that are genuinely
// user-scoped rather than workspace-scoped (creating a workspace); the
// middleware is then skipped, exactly as the router skips it there.
func (h *Handler) assistantInvoke(
	ctx context.Context,
	handler http.HandlerFunc,
	method, path, userID, workspaceID, body string,
	urlParams map[string]string,
) (int, []byte) {
	return h.assistantInvokeAs(ctx, handler, method, path, userID, workspaceID, body, urlParams)
}

// assistantInvokeAs is assistantInvoke with the role gate the router puts in
// front of admin-only routes. `roles` is the same list the route passes to
// middleware.RequireWorkspaceRoleFromURL — the header-resolving twin is used
// here because the synthetic request names the workspace in X-Workspace-ID, and
// both variants end in the identical member lookup and role comparison.
func (h *Handler) assistantInvokeAs(
	ctx context.Context,
	handler http.HandlerFunc,
	method, path, userID, workspaceID, body string,
	urlParams map[string]string,
	roles ...string,
) (int, []byte) {
	// An absolute URL keeps the request well-formed; the host is never read by
	// any handler on this path (workspace resolution is header-based).
	req, err := http.NewRequestWithContext(ctx, method, "http://assistant.internal"+path, strings.NewReader(body))
	if err != nil {
		return http.StatusInternalServerError, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", userID)
	if workspaceID != "" {
		req.Header.Set("X-Workspace-ID", workspaceID)
	}

	// THE BACKSTOP for confirmation binding.
	//
	// Every destructive tool is supposed to park a pending operation through
	// assistantAwaitConfirmation before it gets here. This check is what makes
	// that a guarantee rather than a convention: a future delete tool whose
	// author forgets the seam physically cannot reach a real handler, because
	// the one door all of them go through is this function. It fails closed on
	// the tool's CATALOG membership (assistant.DestructiveTools), so the gate is
	// a property of the catalog and not of whoever wrote the handler.
	if exec := assistantExecutionFrom(ctx); exec != nil && exec.destructive && !exec.authorized {
		slog.Error("assistant: destructive tool reached a handler without a bound confirmation — missing assistantAwaitConfirmation call",
			"tool", exec.tool, "path", path)
		return http.StatusForbidden, []byte(`{"error":"this action needs the user's confirmation before it can run"}`)
	}

	if len(urlParams) > 0 {
		rctx := chi.NewRouteContext()
		for k, v := range urlParams {
			rctx.URLParams.Add(k, v)
		}
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	}

	var entry http.Handler = handler
	switch {
	case workspaceID == "":
		// User-scoped route; the router mounts no workspace middleware either.
	case len(roles) > 0:
		entry = middleware.RequireWorkspaceRole(h.Queries, roles...)(entry)
	default:
		entry = middleware.RequireWorkspaceMember(h.Queries)(entry)
	}

	rec := newAssistantRecorder()
	// Whether the context was ALREADY dead matters: a request dispatched into a
	// dead context never reaches a query, while one whose deadline lands
	// mid-flight may have committed and then lost the answer. Only the second
	// is uncertain; calling the first uncertain too would teach the user to
	// inspect after every cancelled run.
	live := ctx.Err() == nil
	entry.ServeHTTP(rec, req)
	if live && ctx.Err() != nil {
		tool := ""
		if exec := assistantExecutionFrom(ctx); exec != nil {
			tool = exec.tool
		}
		assistantMarkUncertain(ctx, tool, ctx.Err().Error())
	}
	return rec.status, rec.body.Bytes()
}

// assistantAuthorize asks the router's OWN role middleware whether this caller
// would get through a route, without dispatching anything.
//
// It exists for confirmation binding. A destructive tool parks a pending
// operation before it mutates, which means the role check that normally happens
// inside assistantInvokeAs would not run until the user had already been shown
// a confirmation card — so a plain member asking to delete a workspace would
// read "press Confirm to permanently delete Acme" and then be told no. Refusing
// at ask time is the product's behaviour (the UI does not render a button the
// role cannot press), and this is how that refusal stays the product's own:
// the same middleware, the same message, run against a terminal handler that
// does nothing at all.
//
// It deliberately does NOT go through assistantInvokeAs: that function's
// confirmation backstop would (correctly) refuse a destructive tool's probe.
func (h *Handler) assistantAuthorize(ctx context.Context, userID, workspaceID string, roles ...string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://assistant.internal/api/assistant/authorize", nil)
	if err != nil {
		return errors.New("could not check your permissions")
	}
	req.Header.Set("X-User-ID", userID)
	req.Header.Set("X-Workspace-ID", workspaceID)

	allowed := false
	terminal := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		allowed = true
		w.WriteHeader(http.StatusNoContent)
	})
	rec := newAssistantRecorder()
	middleware.RequireWorkspaceRole(h.Queries, roles...)(terminal).ServeHTTP(rec, req)
	if allowed {
		return nil
	}
	return assistantHandlerError(rec.status, rec.body.Bytes(), "you do not have permission to do that")
}

// assistantHandlerError turns a non-2xx handler response into the error the
// model sees. The handler's own message is relayed verbatim — it was written
// for a human and is exactly the correction the model needs ("parent issue not
// found in this workspace", "agent is archived").
func assistantHandlerError(status int, body []byte, fallback string) error {
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && strings.TrimSpace(payload.Error) != "" {
		return errors.New(payload.Error)
	}
	return errors.New(fallback + " (status " + strconv.Itoa(status) + ")")
}

// ---------------------------------------------------------------------------
// Attribution
// ---------------------------------------------------------------------------

// assistantViaKey marks an issue the Agora Assistant acted on for the user.
//
// issue.metadata is the smallest mechanism available: the column already
// exists, SetIssueMetadataKey is a single atomic write, and the value is
// already serialized to every client through IssueResponse.Metadata — so the
// UI can render "via Agora Assistant" with no new enum, no new table, and no
// new event. The semantic is deliberately "the assistant has acted on this
// issue", which is true for a create, an update and a comment alike; there is
// no per-change activity slot to hang a finer marker on today (activity_log
// rows are written ad hoc by a handful of handlers, and nothing writes one for
// an ordinary issue update).
const assistantViaKey = "via_assistant"

// stampAssistantAttribution records that this mutation came through the
// assistant. Best-effort by design: attribution must never fail the write the
// user actually asked for.
func (h *Handler) stampAssistantAttribution(ctx context.Context, issueID, workspaceID pgtype.UUID) {
	if _, err := h.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
		ID:          issueID,
		WorkspaceID: workspaceID,
		Key:         assistantViaKey,
		Value:       []byte("true"),
	}); err != nil {
		slog.Warn("assistant: attribution stamp failed",
			"issue_id", uuidToString(issueID), "error", err)
	}
}

// ---------------------------------------------------------------------------
// Shared argument validation
// ---------------------------------------------------------------------------

// assistantAssigneePair validates that assignee_type and assignee_id were sent
// together and that the type is one the platform models. The identity of the
// assignee itself is validated downstream by validateAssigneePair, which also
// runs the private-agent gate — this is only the shape check that lets a clear
// message reach the model instead of a confusing handler error.
func assistantAssigneePair(assigneeType, assigneeID string) (string, string, error) {
	assigneeType = strings.TrimSpace(assigneeType)
	assigneeID = strings.TrimSpace(assigneeID)
	if assigneeType == "" && assigneeID == "" {
		return "", "", nil
	}
	if assigneeType == "" || assigneeID == "" {
		return "", "", errors.New("assignee_type and assignee_id must be sent together")
	}
	switch assigneeType {
	case "member", "agent", "squad":
	default:
		return "", "", errors.New("assignee_type must be one of: member, agent, squad")
	}
	return assigneeType, assigneeID, nil
}

// assistantValidateEnums rejects a status or priority the board cannot render,
// BEFORE the write reaches the database CHECK constraint. Without it a typo
// surfaces to the model as an opaque 500 instead of the one-line correction it
// can act on.
func assistantValidateEnums(status, priority string) error {
	if s := strings.TrimSpace(status); s != "" && !isKnownIssueStatus(s) {
		return errors.New("status must be one of: backlog, todo, in_progress, in_review, done, blocked, cancelled")
	}
	if p := strings.TrimSpace(priority); p != "" && !isKnownIssuePriority(p) {
		return errors.New("priority must be one of: urgent, high, medium, low, none")
	}
	return nil
}

// ---------------------------------------------------------------------------
// create_issue
// ---------------------------------------------------------------------------

type assistantCreateIssueArgs struct {
	WorkspaceID  string `json:"workspace_id"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Status       string `json:"status"`
	Priority     string `json:"priority"`
	ProjectID    string `json:"project_id"`
	AssigneeType string `json:"assignee_type"`
	AssigneeID   string `json:"assignee_id"`
	DueDate      string `json:"due_date"`
}

func (h *Handler) assistantCreateIssue(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantCreateIssueArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	title := strings.TrimSpace(args.Title)
	if title == "" {
		return nil, errors.New("title is required")
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	if err := assistantValidateEnums(args.Status, args.Priority); err != nil {
		return nil, err
	}
	assigneeType, assigneeID, err := assistantAssigneePair(args.AssigneeType, args.AssigneeID)
	if err != nil {
		return nil, err
	}

	body := map[string]any{"title": title}
	if d := strings.TrimSpace(args.Description); d != "" {
		body["description"] = d
	}
	if s := strings.TrimSpace(args.Status); s != "" {
		body["status"] = s
	}
	if p := strings.TrimSpace(args.Priority); p != "" {
		body["priority"] = p
	}
	if p := strings.TrimSpace(args.ProjectID); p != "" {
		// Resolved here rather than passed through, so a project TITLE works as
		// well as a UUID and a name that matches nothing is a clear refusal
		// instead of a 400 about UUID syntax.
		project, perr := h.assistantResolveProject(ctx, ws, p)
		if perr != nil {
			return nil, perr
		}
		body["project_id"] = uuidToString(project.ID)
	}
	if assigneeType != "" {
		body["assignee_type"] = assigneeType
		body["assignee_id"] = assigneeID
	}
	if d := strings.TrimSpace(args.DueDate); d != "" {
		body["due_date"] = d
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("could not build the create request")
	}

	status, respBody := h.assistantInvoke(ctx, h.CreateIssue, http.MethodPost, "/api/issues",
		caller.ID, uuidToString(ws.ID), string(encoded), nil)
	if status != http.StatusCreated {
		return nil, assistantHandlerError(status, respBody, "could not create the issue")
	}

	var created IssueResponse
	if err := json.Unmarshal(respBody, &created); err != nil {
		return nil, errors.New("the issue was created but its details could not be read back")
	}
	if issueUUID, perr := util.ParseUUID(created.ID); perr == nil {
		h.stampAssistantAttribution(ctx, issueUUID, ws.ID)
	}

	return json.Marshal(map[string]any{
		"created": true,
		"issue": assistantIssueResult{
			Identifier:    created.Identifier,
			Title:         created.Title,
			Status:        created.Status,
			Priority:      created.Priority,
			WorkspaceSlug: ws.Slug,
			URLPath:       assistantIssueURLPath(ws.Slug, created.Identifier),
		},
	})
}

// ---------------------------------------------------------------------------
// update_issue
// ---------------------------------------------------------------------------

type assistantUpdateIssueArgs struct {
	WorkspaceID   string  `json:"workspace_id"`
	Ref           string  `json:"ref"`
	Title         string  `json:"title"`
	Description   *string `json:"description"`
	Status        string  `json:"status"`
	Priority      string  `json:"priority"`
	AssigneeType  string  `json:"assignee_type"`
	AssigneeID    string  `json:"assignee_id"`
	ProjectID     *string `json:"project_id"`
	ParentIssueID *string `json:"parent_issue_id"`
	DueDate       *string `json:"due_date"`
}

func (h *Handler) assistantUpdateIssue(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantUpdateIssueArgs
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
	assigneeType, assigneeID, err := assistantAssigneePair(args.AssigneeType, args.AssigneeID)
	if err != nil {
		return nil, err
	}

	// Resolve the ref to a real issue FIRST, under the read gate, so a
	// non-owner cannot blind-write an issue they are not allowed to see. Every
	// write below addresses the RESOLVED issue.ID — the raw ref never reaches a
	// query (handler UUID-parsing convention).
	issue, err := h.assistantResolveIssue(ctx, caller, ws, role, args.Ref)
	if err != nil {
		return nil, err
	}

	// Only the keys the model actually sent: UpdateIssue distinguishes "field
	// absent" from "field null", and an absent key is what leaves a field
	// alone.
	body := map[string]any{}
	if t := strings.TrimSpace(args.Title); t != "" {
		body["title"] = t
	}
	if args.Description != nil {
		// Whole-replace, exactly like the editor: the tool description tells
		// the model to read the body first when the user asked to append.
		body["description"] = *args.Description
	}
	if s := strings.TrimSpace(args.Status); s != "" {
		body["status"] = s
	}
	if p := strings.TrimSpace(args.Priority); p != "" {
		body["priority"] = p
	}
	if assigneeType != "" {
		body["assignee_type"] = assigneeType
		body["assignee_id"] = assigneeID
	}
	if args.ProjectID != nil {
		// UpdateIssue distinguishes absent / null / value: null detaches the
		// issue from its project, which is what an empty string means here.
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
	if args.ParentIssueID != nil {
		if ref := strings.TrimSpace(*args.ParentIssueID); ref != "" {
			// Resolved under the same read gate as the issue being edited, so
			// a non-owner cannot reparent onto an issue they cannot see.
			parent, perr := h.assistantResolveIssue(ctx, caller, ws, role, ref)
			if perr != nil {
				return nil, errors.New("parent issue: " + perr.Error())
			}
			if parent.ID == issue.ID {
				return nil, errors.New("an issue cannot be its own parent")
			}
			body["parent_issue_id"] = uuidToString(parent.ID)
		} else {
			body["parent_issue_id"] = nil
		}
	}
	if args.DueDate != nil {
		// An empty string is the documented "clear the date" form.
		body["due_date"] = strings.TrimSpace(*args.DueDate)
	}
	if len(body) == 0 {
		return nil, errors.New("nothing to change: pass at least one of title, description, status, priority, assignee_type+assignee_id, project_id, parent_issue_id, due_date")
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("could not build the update request")
	}

	issueID := uuidToString(issue.ID)
	status, respBody := h.assistantInvoke(ctx, h.UpdateIssue, http.MethodPatch, "/api/issues/"+issueID,
		caller.ID, uuidToString(ws.ID), string(encoded), map[string]string{"id": issueID})
	if status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not update the issue")
	}

	var updated IssueResponse
	if err := json.Unmarshal(respBody, &updated); err != nil {
		return nil, errors.New("the issue was updated but its details could not be read back")
	}
	h.stampAssistantAttribution(ctx, issue.ID, ws.ID)

	return json.Marshal(map[string]any{
		"updated": true,
		"issue": assistantIssueResult{
			Identifier:    updated.Identifier,
			Title:         updated.Title,
			Status:        updated.Status,
			Priority:      updated.Priority,
			WorkspaceSlug: ws.Slug,
			URLPath:       assistantIssueURLPath(ws.Slug, updated.Identifier),
		},
	})
}

// ---------------------------------------------------------------------------
// comment_issue
// ---------------------------------------------------------------------------

type assistantCommentIssueArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Ref         string `json:"ref"`
	Body        string `json:"body"`
}

// assistantCommentMaxLen bounds one posted comment. The model is capable of
// writing an essay; a comment is a message to teammates.
const assistantCommentMaxLen = 8000

func (h *Handler) assistantCommentIssue(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantCommentIssueArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	content := strings.TrimSpace(args.Body)
	if content == "" {
		return nil, errors.New("body is required")
	}
	if len([]rune(content)) > assistantCommentMaxLen {
		return nil, errors.New("comment is too long")
	}
	ws, role, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	issue, err := h.assistantResolveIssue(ctx, caller, ws, role, args.Ref)
	if err != nil {
		return nil, err
	}

	encoded, err := json.Marshal(map[string]any{"content": content})
	if err != nil {
		return nil, errors.New("could not build the comment request")
	}

	issueID := uuidToString(issue.ID)
	status, respBody := h.assistantInvoke(ctx, h.CreateComment, http.MethodPost, "/api/issues/"+issueID+"/comments",
		caller.ID, uuidToString(ws.ID), string(encoded), map[string]string{"id": issueID})
	if status != http.StatusCreated && status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not post the comment")
	}
	h.stampAssistantAttribution(ctx, issue.ID, ws.ID)

	identifier := h.getIssuePrefix(ctx, ws.ID) + "-" + strconv.Itoa(int(issue.Number))
	return json.Marshal(map[string]any{
		"commented":        true,
		"issue_identifier": identifier,
		"url_path":         assistantIssueURLPath(ws.Slug, identifier),
	})
}

// ---------------------------------------------------------------------------
// archive_issue
// ---------------------------------------------------------------------------

type assistantArchiveIssueArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Ref         string `json:"ref"`
	Archived    *bool  `json:"archived"`
}

// assistantArchiveIssue is the reversible half of "make it go away". The
// assistant has no delete tool at all, so this is the answer to "close this
// out / get rid of it" that does not destroy anything.
func (h *Handler) assistantArchiveIssue(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantArchiveIssueArgs
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
	archived := true
	if args.Archived != nil {
		archived = *args.Archived
	}
	encoded, err := json.Marshal(map[string]any{"archived": archived})
	if err != nil {
		return nil, errors.New("could not build the archive request")
	}

	issueID := uuidToString(issue.ID)
	status, respBody := h.assistantInvoke(ctx, h.ArchiveIssue, http.MethodPost, "/api/issues/"+issueID+"/archive",
		caller.ID, uuidToString(ws.ID), string(encoded), map[string]string{"id": issueID})
	if status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not archive the issue")
	}
	h.stampAssistantAttribution(ctx, issue.ID, ws.ID)

	identifier := h.getIssuePrefix(ctx, ws.ID) + "-" + strconv.Itoa(int(issue.Number))
	return json.Marshal(map[string]any{
		"archived":         archived,
		"issue_identifier": identifier,
		"url_path":         assistantIssueURLPath(ws.Slug, identifier),
	})
}

// ---------------------------------------------------------------------------
// add_issue_label / remove_issue_label
// ---------------------------------------------------------------------------

type assistantIssueLabelArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Ref         string `json:"ref"`
	Label       string `json:"label"`
}

// assistantIssueLabel attaches or detaches one label. Both directions share a
// body because they share everything except the HTTP verb: same issue gate,
// same label resolution, same failure modes.
func (h *Handler) assistantIssueLabel(ctx context.Context, caller assistantCaller, raw json.RawMessage, attach bool) (json.RawMessage, error) {
	var args assistantIssueLabelArgs
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
	label, err := h.assistantResolveLabel(ctx, ws, args.Label)
	if err != nil {
		return nil, err
	}

	issueID := uuidToString(issue.ID)
	labelID := uuidToString(label.ID)
	var status int
	var respBody []byte
	if attach {
		encoded, merr := json.Marshal(map[string]any{"label_id": labelID})
		if merr != nil {
			return nil, errors.New("could not build the label request")
		}
		status, respBody = h.assistantInvoke(ctx, h.AttachLabel, http.MethodPost, "/api/issues/"+issueID+"/labels",
			caller.ID, uuidToString(ws.ID), string(encoded), map[string]string{"id": issueID})
	} else {
		status, respBody = h.assistantInvoke(ctx, h.DetachLabel, http.MethodDelete,
			"/api/issues/"+issueID+"/labels/"+labelID,
			caller.ID, uuidToString(ws.ID), "", map[string]string{"id": issueID, "labelId": labelID})
	}
	// Attach answers 200/201, detach 204 — anything else is the handler saying
	// no (e.g. the human-gate on merge:approved), which the model must relay.
	if status < 200 || status > 299 {
		verb := "attach"
		if !attach {
			verb = "detach"
		}
		return nil, assistantHandlerError(status, respBody, "could not "+verb+" the label")
	}
	h.stampAssistantAttribution(ctx, issue.ID, ws.ID)

	identifier := h.getIssuePrefix(ctx, ws.ID) + "-" + strconv.Itoa(int(issue.Number))
	out := map[string]any{
		"label":            label.Name,
		"issue_identifier": identifier,
		"url_path":         assistantIssueURLPath(ws.Slug, identifier),
	}
	// Distinct keys per direction: a `"labelled": false` would read to the
	// model as "the write did not happen".
	if attach {
		out["label_added"] = true
	} else {
		out["label_removed"] = true
	}
	return json.Marshal(out)
}

// ---------------------------------------------------------------------------
// move_issue_to_sprint
// ---------------------------------------------------------------------------

type assistantMoveSprintArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Ref         string `json:"ref"`
	Sprint      string `json:"sprint"`
}

func (h *Handler) assistantMoveIssueToSprint(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantMoveSprintArgs
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
	issueID := uuidToString(issue.ID)
	identifier := h.getIssuePrefix(ctx, ws.ID) + "-" + strconv.Itoa(int(issue.Number))

	// Empty sprint = take it out of whatever sprint it is in. Removal is its
	// own endpoint rather than a null sprint_id, so the branch is here.
	if strings.TrimSpace(args.Sprint) == "" {
		status, respBody := h.assistantInvoke(ctx, h.RemoveIssueSprint, http.MethodDelete, "/api/issues/"+issueID+"/sprint",
			caller.ID, uuidToString(ws.ID), "", map[string]string{"id": issueID})
		if status != http.StatusNoContent && status != http.StatusOK {
			return nil, assistantHandlerError(status, respBody, "could not remove the issue from its sprint")
		}
		h.stampAssistantAttribution(ctx, issue.ID, ws.ID)
		return json.Marshal(map[string]any{
			"removed_from_sprint": true,
			"issue_identifier":    identifier,
			"url_path":            assistantIssueURLPath(ws.Slug, identifier),
		})
	}

	sprint, err := h.assistantResolveSprint(ctx, ws, args.Sprint)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(map[string]any{"sprint_id": uuidToString(sprint.ID)})
	if err != nil {
		return nil, errors.New("could not build the sprint request")
	}
	status, respBody := h.assistantInvoke(ctx, h.SetIssueSprint, http.MethodPut, "/api/issues/"+issueID+"/sprint",
		caller.ID, uuidToString(ws.ID), string(encoded), map[string]string{"id": issueID})
	if status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not move the issue to that sprint")
	}
	h.stampAssistantAttribution(ctx, issue.ID, ws.ID)

	return json.Marshal(map[string]any{
		"moved":            true,
		"sprint":           sprint.Name,
		"sprint_id":        uuidToString(sprint.ID),
		"issue_identifier": identifier,
		"url_path":         assistantIssueURLPath(ws.Slug, identifier),
	})
}

// ---------------------------------------------------------------------------
// create_project / update_project
// ---------------------------------------------------------------------------

type assistantCreateProjectArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Priority    string `json:"priority"`
	LeadType    string `json:"lead_type"`
	LeadID      string `json:"lead_id"`
	SquadID     string `json:"squad_id"`
}

// assistantProjectLead validates the lead pair. lead_type/lead_id are NOT
// validated by the project handlers beyond UUID syntax (unlike an issue
// assignee, which runs the full private-agent gate), so a hallucinated id would
// otherwise persist as a lead that resolves to nothing in the UI. Checking
// existence here is the cheapest place to stop that.
func (h *Handler) assistantProjectLead(ctx context.Context, ws db.Workspace, leadType, leadID string) (string, string, error) {
	leadType = strings.TrimSpace(leadType)
	leadID = strings.TrimSpace(leadID)
	if leadType == "" && leadID == "" {
		return "", "", nil
	}
	if leadType == "" || leadID == "" {
		return "", "", errors.New("lead_type and lead_id must be sent together")
	}
	leadUUID, err := util.ParseUUID(leadID)
	if err != nil {
		return "", "", errors.New("lead_id must be a UUID from list_members or list_agents")
	}
	switch leadType {
	case "member":
		if _, merr := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
			UserID:      leadUUID,
			WorkspaceID: ws.ID,
		}); merr != nil {
			return "", "", errors.New("lead_id is not a member of that workspace — use a user_id from list_members")
		}
	case "agent":
		if _, aerr := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID:          leadUUID,
			WorkspaceID: ws.ID,
		}); aerr != nil {
			return "", "", errors.New("lead_id is not an agent in that workspace — use an id from list_agents")
		}
	default:
		return "", "", errors.New("lead_type must be one of: member, agent")
	}
	return leadType, leadID, nil
}

func (h *Handler) assistantCreateProject(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantCreateProjectArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	title := strings.TrimSpace(args.Title)
	if title == "" {
		return nil, errors.New("title is required")
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	leadType, leadID, err := h.assistantProjectLead(ctx, ws, args.LeadType, args.LeadID)
	if err != nil {
		return nil, err
	}

	body := map[string]any{"title": title}
	if d := strings.TrimSpace(args.Description); d != "" {
		body["description"] = d
	}
	// status and priority are validated by the handler against the project
	// CHECK constraints (validProjectStatuses / validProjectPriorities) and its
	// 400 message names the allowed values, so there is nothing to duplicate.
	if s := strings.TrimSpace(args.Status); s != "" {
		body["status"] = s
	}
	if p := strings.TrimSpace(args.Priority); p != "" {
		body["priority"] = p
	}
	if leadType != "" {
		body["lead_type"] = leadType
		body["lead_id"] = leadID
	}
	if sq := strings.TrimSpace(args.SquadID); sq != "" {
		// resolveProjectSquadID (in the handler) checks same-workspace and
		// not-archived, so only the syntax matters here.
		body["squad_id"] = sq
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("could not build the create request")
	}

	status, respBody := h.assistantInvoke(ctx, h.CreateProject, http.MethodPost, "/api/projects",
		caller.ID, uuidToString(ws.ID), string(encoded), nil)
	if status != http.StatusCreated {
		return nil, assistantHandlerError(status, respBody, "could not create the project")
	}
	var created ProjectResponse
	if err := json.Unmarshal(respBody, &created); err != nil {
		return nil, errors.New("the project was created but its details could not be read back")
	}
	return json.Marshal(map[string]any{
		"created": true,
		"project": map[string]any{
			"id":       created.ID,
			"title":    created.Title,
			"status":   created.Status,
			"priority": created.Priority,
			"url_path": assistantProjectURLPath(ws.Slug, created.ID),
		},
	})
}

type assistantUpdateProjectArgs struct {
	WorkspaceID string  `json:"workspace_id"`
	Project     string  `json:"project"`
	Title       string  `json:"title"`
	Description *string `json:"description"`
	Status      string  `json:"status"`
	Priority    string  `json:"priority"`
	LeadType    string  `json:"lead_type"`
	LeadID      string  `json:"lead_id"`
	SquadID     *string `json:"squad_id"`
}

// assistantUpdateProject edits a project through PUT /api/projects/{id}.
//
// Going through the handler is not a style choice here, it is the fix for a
// live footgun: Queries.UpdateProject COALESCEs only title/status/priority/
// settings and assigns description, icon, lead_type, lead_id and squad_id
// unconditionally — so a partial params struct NULLs five columns. The handler
// seeds those five from the row it just loaded and only overrides the keys the
// request actually carried, which is why "set status=completed" here must not
// wipe the project's description or lead. TestAssistantUpdateProjectPreserves...
// pins exactly that.
func (h *Handler) assistantUpdateProject(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantUpdateProjectArgs
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
	leadType, leadID, err := h.assistantProjectLead(ctx, ws, args.LeadType, args.LeadID)
	if err != nil {
		return nil, err
	}

	// Only the keys the model actually sent. Every key omitted here is a column
	// the handler re-seeds from the loaded row.
	body := map[string]any{}
	if t := strings.TrimSpace(args.Title); t != "" {
		body["title"] = t
	}
	if args.Description != nil {
		if d := *args.Description; strings.TrimSpace(d) != "" {
			body["description"] = d
		} else {
			body["description"] = nil
		}
	}
	if s := strings.TrimSpace(args.Status); s != "" {
		body["status"] = s
	}
	if p := strings.TrimSpace(args.Priority); p != "" {
		body["priority"] = p
	}
	if leadType != "" {
		body["lead_type"] = leadType
		body["lead_id"] = leadID
	}
	if args.SquadID != nil {
		if sq := strings.TrimSpace(*args.SquadID); sq != "" {
			body["squad_id"] = sq
		} else {
			body["squad_id"] = nil
		}
	}
	if len(body) == 0 {
		return nil, errors.New("nothing to change: pass at least one of title, description, status, priority, lead_type+lead_id, squad_id")
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("could not build the update request")
	}

	projectID := uuidToString(project.ID)
	status, respBody := h.assistantInvoke(ctx, h.UpdateProject, http.MethodPut, "/api/projects/"+projectID,
		caller.ID, uuidToString(ws.ID), string(encoded), map[string]string{"id": projectID})
	if status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not update the project")
	}
	var updated ProjectResponse
	if err := json.Unmarshal(respBody, &updated); err != nil {
		return nil, errors.New("the project was updated but its details could not be read back")
	}
	return json.Marshal(map[string]any{
		"updated": true,
		"project": map[string]any{
			"id":       updated.ID,
			"title":    updated.Title,
			"status":   updated.Status,
			"priority": updated.Priority,
			"url_path": assistantProjectURLPath(ws.Slug, updated.ID),
		},
	})
}

// ---------------------------------------------------------------------------
// create_sprint
// ---------------------------------------------------------------------------

type assistantCreateSprintArgs struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
	Name        string `json:"name"`
	Goal        string `json:"goal"`
	Status      string `json:"status"`
	StartDate   string `json:"start_date"`
	EndDate     string `json:"end_date"`
}

func (h *Handler) assistantCreateSprint(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantCreateSprintArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return nil, errors.New("name is required")
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	project, err := h.assistantResolveProject(ctx, ws, args.ProjectID)
	if err != nil {
		return nil, err
	}

	body := map[string]any{"name": name}
	if g := strings.TrimSpace(args.Goal); g != "" {
		body["goal"] = g
	}
	if s := strings.TrimSpace(args.Status); s != "" {
		body["status"] = s
	}
	// The handler accepts YYYY-MM-DD as well as RFC3339 (parseSprintDate), so
	// the dates go through as sent.
	if d := strings.TrimSpace(args.StartDate); d != "" {
		body["start_date"] = d
	}
	if d := strings.TrimSpace(args.EndDate); d != "" {
		body["end_date"] = d
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("could not build the create request")
	}

	projectID := uuidToString(project.ID)
	status, respBody := h.assistantInvoke(ctx, h.CreateSprint, http.MethodPost, "/api/projects/"+projectID+"/sprints",
		caller.ID, uuidToString(ws.ID), string(encoded), map[string]string{"id": projectID})
	if status != http.StatusCreated {
		return nil, assistantHandlerError(status, respBody, "could not create the sprint")
	}
	var created SprintResponse
	if err := json.Unmarshal(respBody, &created); err != nil {
		return nil, errors.New("the sprint was created but its details could not be read back")
	}
	return json.Marshal(map[string]any{
		"created": true,
		"sprint": map[string]any{
			"id":            created.ID,
			"name":          created.Name,
			"status":        created.Status,
			"project_id":    created.ProjectID,
			"project_title": project.Title,
		},
	})
}

// ---------------------------------------------------------------------------
// create_label
// ---------------------------------------------------------------------------

type assistantCreateLabelArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
}

// assistantDefaultLabelColor is the neutral the workspace already uses for
// machine-created labels (bitrixLabelColor's default). CreateLabel REQUIRES a
// valid hex, so "the model did not pick a color" has to resolve to something
// rather than 400.
const assistantDefaultLabelColor = "#64748b"

func (h *Handler) assistantCreateLabel(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantCreateLabelArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return nil, errors.New("name is required")
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	color := strings.TrimSpace(args.Color)
	if color == "" {
		color = assistantDefaultLabelColor
	}
	encoded, err := json.Marshal(map[string]any{"name": name, "color": color})
	if err != nil {
		return nil, errors.New("could not build the create request")
	}

	status, respBody := h.assistantInvoke(ctx, h.CreateLabel, http.MethodPost, "/api/labels",
		caller.ID, uuidToString(ws.ID), string(encoded), nil)
	if status != http.StatusCreated {
		return nil, assistantHandlerError(status, respBody, "could not create the label")
	}
	var created LabelResponse
	if err := json.Unmarshal(respBody, &created); err != nil {
		return nil, errors.New("the label was created but its details could not be read back")
	}
	return json.Marshal(map[string]any{
		"created": true,
		"label":   assistantLabelResult{ID: created.ID, Name: created.Name, Color: created.Color},
	})
}

// ---------------------------------------------------------------------------
// Setting Agora up: agents and skills
// ---------------------------------------------------------------------------
//
// These are the SETUP half of the catalog. A workspace with no agent cannot do
// anything the product exists for, and "add an agent, give it a skill" is the
// most common thing a new user needs help with — so it is exactly the wrong
// thing for the assistant to answer with directions to a settings page.
//
// The boundary that still holds: nothing here touches a CREDENTIAL. Creating
// an agent binds it to a runtime that already exists; it never pairs a machine,
// never sets an API key, never writes custom_env (which has its own
// owner/admin-gated, audit-logged endpoint), and never configures MCP. Those
// stay in ExcludedCapabilities.

type assistantCreateAgentArgs struct {
	WorkspaceID  string `json:"workspace_id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
	RuntimeID    string `json:"runtime_id"`
	Model        string `json:"model"`
}

// assistantCreateAgent creates an agent on an EXISTING runtime.
//
// runtime_id is mandatory and unforgiving by design: CreateAgent rejects a
// missing or unusable runtime itself, and that refusal ("invalid runtime_id",
// "this runtime is private") is relayed verbatim so the model can explain the
// prerequisite instead of guessing. When the workspace has no runtime at all,
// list_runtimes returns an empty list plus the note that says why — that is the
// grounding step, and it is what turns "I can't" into "nobody has connected a
// runtime yet; that part has to be done in Settings → Runtimes".
//
// visibility, max_concurrent_tasks, thinking_level, custom_env, custom_args and
// mcp_config are deliberately NOT exposed: the first three are unvalidated or
// provider-coupled free strings, and the last three are the credential/config
// surface. Omitting them lands the handler's own defaults (private, 6), exactly
// as a UI create that leaves those fields alone.
func (h *Handler) assistantCreateAgent(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantCreateAgentArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return nil, errors.New("name is required")
	}
	runtimeID := strings.TrimSpace(args.RuntimeID)
	if runtimeID == "" {
		return nil, errors.New("runtime_id is required — call list_runtimes and use one of the ids it returns")
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}

	body := map[string]any{"name": name, "runtime_id": runtimeID}
	if d := strings.TrimSpace(args.Description); d != "" {
		body["description"] = d
	}
	if i := strings.TrimSpace(args.Instructions); i != "" {
		body["instructions"] = i
	}
	if m := strings.TrimSpace(args.Model); m != "" {
		body["model"] = m
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("could not build the create request")
	}

	status, respBody := h.assistantInvoke(ctx, h.CreateAgent, http.MethodPost, "/api/agents",
		caller.ID, uuidToString(ws.ID), string(encoded), nil)
	if status != http.StatusCreated {
		return nil, assistantHandlerError(status, respBody, "could not create the agent")
	}
	var created AgentResponse
	if err := json.Unmarshal(respBody, &created); err != nil {
		return nil, errors.New("the agent was created but its details could not be read back")
	}
	return json.Marshal(map[string]any{
		"created": true,
		"agent": map[string]any{
			"id":         created.ID,
			"name":       created.Name,
			"status":     created.Status,
			"runtime_id": created.RuntimeID,
		},
	})
}

type assistantUpdateAgentArgs struct {
	WorkspaceID        string  `json:"workspace_id"`
	AgentID            string  `json:"agent_id"`
	Name               string  `json:"name"`
	Description        *string `json:"description"`
	Instructions       *string `json:"instructions"`
	Model              string  `json:"model"`
	Visibility         string  `json:"visibility"`
	MaxConcurrentTasks *int32  `json:"max_concurrent_tasks"`
	Archived           *bool   `json:"archived"`
}

// assistantUpdateAgent configures an agent: what it IS (name, blurb, standing
// instructions, model) and, since the MAIN RULE, how it is SET UP — who can see
// it, how many tasks it runs at once, and whether it is archived.
//
// Three fields stay out, and only one kind of reason keeps them out:
//
//   - custom_env and mcp_config carry SECRET VALUES. They are the assistant's
//     one permanent carve-out (see assistant.ExcludedCapabilities), and
//     UpdateAgent rejects custom_env on this endpoint anyway — env has its own
//     owner/admin-gated, audit-logged route.
//   - runtime_id / fallback_runtime_id re-bind the agent to a different
//     machine. That is not withheld on principle; it is withheld because the
//     assistant has no way to tell whether the target runtime has the repo,
//     the credentials and the tooling this agent's tasks assume, so a
//     conversational re-bind breaks work silently. create_agent already picks
//     a runtime at the one moment the choice is visible.
//
// archived is routed to the dedicated archive/restore endpoints rather than to
// PUT: archiving also cancels the agent's pending tasks, which a status field
// on an update would not do.
//
// UpdateAgent (and ArchiveAgent / RestoreAgent) enforce canManageAgent — the
// agent's owner, or a workspace owner/admin — so an ordinary member editing
// someone else's agent is refused by the handler with its own message.
func (h *Handler) assistantUpdateAgent(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantUpdateAgentArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	agent, err := h.assistantResolveAgent(ctx, caller, ws, args.AgentID)
	if err != nil {
		return nil, err
	}

	body := map[string]any{}
	if n := strings.TrimSpace(args.Name); n != "" {
		body["name"] = n
	}
	if args.Description != nil {
		body["description"] = *args.Description
	}
	if args.Instructions != nil {
		body["instructions"] = *args.Instructions
	}
	if m := strings.TrimSpace(args.Model); m != "" {
		body["model"] = m
	}
	if v := strings.TrimSpace(args.Visibility); v != "" {
		// Checked here rather than left to the CHECK constraint: the column
		// rejects anything else with a 500, and "private"/"workspace" is not a
		// pair the model can infer from the words a user says.
		if v != "workspace" && v != "private" {
			return nil, errors.New("visibility must be one of: workspace, private")
		}
		body["visibility"] = v
	}
	if args.MaxConcurrentTasks != nil {
		if *args.MaxConcurrentTasks < 1 {
			return nil, errors.New("max_concurrent_tasks must be at least 1")
		}
		body["max_concurrent_tasks"] = *args.MaxConcurrentTasks
	}
	if len(body) == 0 && args.Archived == nil {
		return nil, errors.New("nothing to change: pass at least one of name, description, instructions, model, visibility, max_concurrent_tasks, archived")
	}

	agentID := uuidToString(agent.ID)
	workspaceID := uuidToString(ws.ID)
	out := map[string]any{"updated": true, "agent": map[string]any{"id": agentID, "name": agent.Name}}

	if len(body) > 0 {
		encoded, merr := json.Marshal(body)
		if merr != nil {
			return nil, errors.New("could not build the update request")
		}
		status, respBody := h.assistantInvoke(ctx, h.UpdateAgent, http.MethodPut, "/api/agents/"+agentID,
			caller.ID, workspaceID, string(encoded), map[string]string{"id": agentID})
		if status != http.StatusOK {
			return nil, assistantHandlerError(status, respBody, "could not update the agent")
		}
		var updated AgentResponse
		if uerr := json.Unmarshal(respBody, &updated); uerr != nil {
			return nil, errors.New("the agent was updated but its details could not be read back")
		}
		out["agent"] = map[string]any{"id": updated.ID, "name": updated.Name}
	}

	if args.Archived != nil {
		if aerr := h.assistantSetAgentArchived(ctx, caller, workspaceID, agentID, agent.ArchivedAt.Valid, *args.Archived); aerr != nil {
			return nil, aerr
		}
		out["archived"] = *args.Archived
	}
	return json.Marshal(out)
}

// assistantSetAgentArchived archives or restores an agent, and treats "already
// in that state" as success.
//
// ArchiveAgent answers 409 when the agent is already archived. Through a
// conversation that is not an error the user should ever hear — they asked for
// the agent to be archived and it is archived — so the no-op case is absorbed
// here rather than relayed as a failure the model then apologises for.
func (h *Handler) assistantSetAgentArchived(ctx context.Context, caller assistantCaller, workspaceID, agentID string, currentlyArchived, want bool) error {
	if currentlyArchived == want {
		return nil
	}
	path := "/api/agents/" + agentID + "/archive"
	handler := h.ArchiveAgent
	if !want {
		path = "/api/agents/" + agentID + "/restore"
		handler = h.RestoreAgent
	}
	status, respBody := h.assistantInvoke(ctx, handler, http.MethodPost, path,
		caller.ID, workspaceID, "", map[string]string{"id": agentID})
	if status < 200 || status > 299 {
		verb := "archive"
		if !want {
			verb = "restore"
		}
		return assistantHandlerError(status, respBody, "could not "+verb+" the agent")
	}
	return nil
}

type assistantAddSkillArgs struct {
	WorkspaceID string `json:"workspace_id"`
	URL         string `json:"url"`
	OnConflict  string `json:"on_conflict"`
}

// assistantAddSkill fetches a skill from a public source into the workspace
// library — the "fetching" half of skill management, which is how skills
// actually arrive (nobody authors SKILL.md in a chat window).
//
// ImportSkill owns source detection (ClawHub / skills.sh / GitHub), the fetch,
// path validation and the provenance stamp, and returns a structured result
// when on_conflict is set. The default here is "skip" rather than the handler's
// "fail": a conversational retry re-importing an existing skill should report
// "already there", not raise a 409 the model then narrates as a failure.
func (h *Handler) assistantAddSkill(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantAddSkillArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	url := strings.TrimSpace(args.URL)
	if url == "" {
		return nil, errors.New("url is required — a GitHub, ClawHub or skills.sh link to the skill")
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	onConflict := strings.TrimSpace(args.OnConflict)
	if onConflict == "" {
		onConflict = importOnConflictSkip
	}
	encoded, err := json.Marshal(map[string]any{"url": url, "on_conflict": onConflict})
	if err != nil {
		return nil, errors.New("could not build the import request")
	}

	status, respBody := h.assistantInvoke(ctx, h.ImportSkill, http.MethodPost, "/api/skills/import",
		caller.ID, uuidToString(ws.ID), string(encoded), nil)
	if status != http.StatusCreated && status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not add the skill")
	}
	var result SkillImportResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, errors.New("the skill was imported but its details could not be read back")
	}
	out := map[string]any{"status": result.Status}
	if result.Reason != "" {
		out["reason"] = result.Reason
	}
	if result.Skill != nil {
		out["skill"] = assistantSkillResult{
			ID:          result.Skill.ID,
			Name:        result.Skill.Name,
			Description: truncateRunes(result.Skill.Description, assistantInboxPreviewChars),
		}
	}
	return json.Marshal(out)
}

type assistantAttachSkillArgs struct {
	WorkspaceID string `json:"workspace_id"`
	AgentID     string `json:"agent_id"`
	SkillID     string `json:"skill_id"`
}

// assistantAttachSkillToAgent wires a library skill onto one agent, through the
// additive POST /api/agents/{id}/skills/add — never the whole-replace PUT,
// which would silently drop every other skill the agent has.
func (h *Handler) assistantAttachSkillToAgent(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantAttachSkillArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	agent, err := h.assistantResolveAgent(ctx, caller, ws, args.AgentID)
	if err != nil {
		return nil, err
	}
	skill, err := h.assistantResolveSkill(ctx, ws, args.SkillID)
	if err != nil {
		return nil, err
	}

	encoded, err := json.Marshal(map[string]any{"skill_ids": []string{uuidToString(skill.ID)}})
	if err != nil {
		return nil, errors.New("could not build the request")
	}
	agentID := uuidToString(agent.ID)
	status, respBody := h.assistantInvoke(ctx, h.AddAgentSkills, http.MethodPost, "/api/agents/"+agentID+"/skills/add",
		caller.ID, uuidToString(ws.ID), string(encoded), map[string]string{"id": agentID})
	if status != http.StatusOK && status != http.StatusCreated {
		return nil, assistantHandlerError(status, respBody, "could not give the agent that skill")
	}
	return json.Marshal(map[string]any{
		"attached": true,
		"agent":    agent.Name,
		"skill":    skill.Name,
		"agent_id": agentID,
		"skill_id": uuidToString(skill.ID),
	})
}

// assistantResolveSkill accepts a skill UUID or its exact name in the
// workspace. Skill names are unique per workspace (the CreateSkill conflict is
// a unique violation), so an exact match is unambiguous.
func (h *Handler) assistantResolveSkill(ctx context.Context, ws db.Workspace, ref string) (db.Skill, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.Skill{}, errors.New("skill_id is required (a skill name or a skill UUID)")
	}
	if skillUUID, err := util.ParseUUID(ref); err == nil {
		skill, serr := h.Queries.GetSkillInWorkspace(ctx, db.GetSkillInWorkspaceParams{
			ID:          skillUUID,
			WorkspaceID: ws.ID,
		})
		if serr != nil {
			return db.Skill{}, errors.New("skill not found in that workspace")
		}
		return skill, nil
	}
	rows, err := h.Queries.ListSkillSummariesByWorkspace(ctx, ws.ID)
	if err != nil {
		slog.Warn("assistant: list skills failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return db.Skill{}, errors.New("could not load skills")
	}
	needle := strings.ToLower(ref)
	for _, s := range rows {
		if strings.ToLower(s.Name) != needle {
			continue
		}
		skill, serr := h.Queries.GetSkillInWorkspace(ctx, db.GetSkillInWorkspaceParams{
			ID:          s.ID,
			WorkspaceID: ws.ID,
		})
		if serr != nil {
			return db.Skill{}, errors.New("skill not found in that workspace")
		}
		return skill, nil
	}
	return db.Skill{}, fmt.Errorf("no skill called %q in that workspace — list_skills shows what exists, add_skill fetches a new one", ref)
}

// ---------------------------------------------------------------------------
// The one-click parity items
// ---------------------------------------------------------------------------
//
// Each of these is a single click in the UI that the assistant previously had
// to describe instead of doing: marking the inbox read, editing or resolving a
// comment, pinning something to the sidebar, following an issue. None of them
// is risky and all of them are reversible; their absence was simply the
// original catalog stopping at "issues and projects".
//
// mark_inbox_read is the one that needed real work rather than a wrapper: see
// the note on assistantInvoke about ctxWorkspaceID.

type assistantUpdateCommentArgs struct {
	WorkspaceID string `json:"workspace_id"`
	CommentID   string `json:"comment_id"`
	Body        string `json:"body"`
}

// assistantUpdateComment edits a comment through the real PUT, so the
// author/admin gate, the edit event and the re-render all behave identically to
// an edit made in the UI.
func (h *Handler) assistantUpdateComment(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantUpdateCommentArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	content := strings.TrimSpace(args.Body)
	if content == "" {
		return nil, errors.New("body is required")
	}
	if len([]rune(content)) > assistantCommentMaxLen {
		return nil, errors.New("comment is too long")
	}
	ws, role, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	comment, err := h.assistantLoadComment(ctx, caller, ws, role, args.CommentID)
	if err != nil {
		return nil, err
	}

	encoded, err := json.Marshal(map[string]any{"content": content})
	if err != nil {
		return nil, errors.New("could not build the update request")
	}
	commentID := uuidToString(comment.ID)
	status, respBody := h.assistantInvoke(ctx, h.UpdateComment, http.MethodPut, "/api/comments/"+commentID,
		caller.ID, uuidToString(ws.ID), string(encoded), map[string]string{"commentId": commentID})
	if status != http.StatusOK {
		// "only comment author or admin can edit" arrives verbatim.
		return nil, assistantHandlerError(status, respBody, "could not edit the comment")
	}
	h.stampAssistantAttribution(ctx, comment.IssueID, ws.ID)
	return json.Marshal(map[string]any{"updated": true, "comment_id": commentID})
}

type assistantResolveCommentArgs struct {
	WorkspaceID string `json:"workspace_id"`
	CommentID   string `json:"comment_id"`
	Resolved    *bool  `json:"resolved"`
}

// assistantResolveComment resolves or reopens one comment thread. Both
// directions are separate endpoints (POST vs DELETE on /resolve), which is why
// the branch is here rather than in a request body.
func (h *Handler) assistantResolveComment(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantResolveCommentArgs
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
	resolved := true
	if args.Resolved != nil {
		resolved = *args.Resolved
	}

	commentID := uuidToString(comment.ID)
	method := http.MethodPost
	handler := h.ResolveComment
	if !resolved {
		method = http.MethodDelete
		handler = h.UnresolveComment
	}
	status, respBody := h.assistantInvoke(ctx, handler, method, "/api/comments/"+commentID+"/resolve",
		caller.ID, uuidToString(ws.ID), "", map[string]string{"commentId": commentID})
	if status < 200 || status > 299 {
		verb := "resolve"
		if !resolved {
			verb = "reopen"
		}
		return nil, assistantHandlerError(status, respBody, "could not "+verb+" the comment")
	}
	return json.Marshal(map[string]any{"resolved": resolved, "comment_id": commentID})
}

type assistantMarkInboxReadArgs struct {
	WorkspaceID string `json:"workspace_id"`
	ItemID      string `json:"item_id"`
}

// assistantMarkInboxRead clears the caller's unread inbox in one workspace, or
// one item of it.
//
// This tool is the reason assistantInvoke runs the workspace middleware.
// MarkAllInboxRead reads its workspace from ctxWorkspaceID() ONLY — no header
// fallback, unlike most handlers — so a synthetic request that skipped the
// middleware saw no workspace at all and answered 400 "workspace id". The fix
// was not to special-case this tool but to make every synthetic request carry
// the same context a routed one does; see assistantInvokeAs.
func (h *Handler) assistantMarkInboxRead(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantMarkInboxReadArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	workspaceID := uuidToString(ws.ID)

	if itemRef := strings.TrimSpace(args.ItemID); itemRef != "" {
		itemUUID, perr := util.ParseUUID(itemRef)
		if perr != nil {
			return nil, errors.New("item_id must be an inbox item UUID")
		}
		itemID := uuidToString(itemUUID)
		status, respBody := h.assistantInvoke(ctx, h.MarkInboxRead, http.MethodPost, "/api/inbox/"+itemID+"/read",
			caller.ID, workspaceID, "", map[string]string{"id": itemID})
		if status != http.StatusOK {
			// loadInboxItemForUser answers 404 for another person's item, so
			// this can never mark somebody else's notification read.
			return nil, assistantHandlerError(status, respBody, "could not mark that inbox item read")
		}
		return json.Marshal(map[string]any{"marked_read": true, "item_id": itemID})
	}

	status, respBody := h.assistantInvoke(ctx, h.MarkAllInboxRead, http.MethodPost, "/api/inbox/mark-all-read",
		caller.ID, workspaceID, "", nil)
	if status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not mark the inbox read")
	}
	var result struct {
		Count int64 `json:"count"`
	}
	_ = json.Unmarshal(respBody, &result)
	return json.Marshal(map[string]any{
		"marked_read":    true,
		"items_marked":   result.Count,
		"workspace_slug": ws.Slug,
	})
}

type assistantPinItemArgs struct {
	WorkspaceID string `json:"workspace_id"`
	ItemType    string `json:"item_type"`
	Item        string `json:"item"`
	Pinned      *bool  `json:"pinned"`
}

// assistantPinItem pins or unpins an issue or project in the caller's own
// sidebar. Private to this user by construction — CreatePin/DeletePin key on
// the requesting user id and there is no argument that could name another.
func (h *Handler) assistantPinItem(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantPinItemArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	itemType := strings.TrimSpace(args.ItemType)
	if itemType != "issue" && itemType != "project" {
		return nil, errors.New("item_type must be one of: issue, project")
	}
	ws, role, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}

	// Resolved to a UUID here so the tool can take a MUL-123 or a project
	// title, and so the issue gate applies before anything is pinned.
	var itemID, label string
	if itemType == "issue" {
		issue, ierr := h.assistantResolveIssue(ctx, caller, ws, role, args.Item)
		if ierr != nil {
			return nil, ierr
		}
		itemID = uuidToString(issue.ID)
		label = h.getIssuePrefix(ctx, ws.ID) + "-" + strconv.Itoa(int(issue.Number))
	} else {
		project, perr := h.assistantResolveProject(ctx, ws, args.Item)
		if perr != nil {
			return nil, perr
		}
		itemID = uuidToString(project.ID)
		label = project.Title
	}

	pinned := true
	if args.Pinned != nil {
		pinned = *args.Pinned
	}
	workspaceID := uuidToString(ws.ID)
	var status int
	var respBody []byte
	if pinned {
		encoded, merr := json.Marshal(map[string]any{"item_type": itemType, "item_id": itemID})
		if merr != nil {
			return nil, errors.New("could not build the pin request")
		}
		status, respBody = h.assistantInvoke(ctx, h.CreatePin, http.MethodPost, "/api/pins",
			caller.ID, workspaceID, string(encoded), nil)
	} else {
		status, respBody = h.assistantInvoke(ctx, h.DeletePin, http.MethodDelete,
			"/api/pins/"+itemType+"/"+itemID,
			caller.ID, workspaceID, "", map[string]string{"itemType": itemType, "itemId": itemID})
	}
	if status < 200 || status > 299 {
		verb := "pin"
		if !pinned {
			verb = "unpin"
		}
		return nil, assistantHandlerError(status, respBody, "could not "+verb+" that")
	}
	return json.Marshal(map[string]any{
		"pinned":    pinned,
		"item_type": itemType,
		"item":      label,
	})
}

type assistantSubscribeIssueArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Ref         string `json:"ref"`
	Subscribed  *bool  `json:"subscribed"`
}

// assistantSubscribeIssue follows or unfollows an issue for the CALLER.
//
// SubscribeToIssue accepts an optional user_id/user_type body to subscribe
// somebody else; the tool deliberately never sends it. Signing a teammate up
// for someone else's notifications is not something the user asked for when
// they said "keep me posted on MUL-12", and there is no UI affordance for it
// either.
func (h *Handler) assistantSubscribeIssue(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantSubscribeIssueArgs
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
	subscribed := true
	if args.Subscribed != nil {
		subscribed = *args.Subscribed
	}

	issueID := uuidToString(issue.ID)
	path := "/api/issues/" + issueID + "/subscribe"
	handler := h.SubscribeToIssue
	if !subscribed {
		path = "/api/issues/" + issueID + "/unsubscribe"
		handler = h.UnsubscribeFromIssue
	}
	status, respBody := h.assistantInvoke(ctx, handler, http.MethodPost, path,
		caller.ID, uuidToString(ws.ID), "{}", map[string]string{"id": issueID})
	if status < 200 || status > 299 {
		verb := "subscribe to"
		if !subscribed {
			verb = "unsubscribe from"
		}
		return nil, assistantHandlerError(status, respBody, "could not "+verb+" the issue")
	}
	identifier := h.getIssuePrefix(ctx, ws.ID) + "-" + strconv.Itoa(int(issue.Number))
	return json.Marshal(map[string]any{
		"subscribed":       subscribed,
		"issue_identifier": identifier,
		"url_path":         assistantIssueURLPath(ws.Slug, identifier),
	})
}
