package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// Tests for the MAIN-RULE parity pass (docs/agora-assistant-plan.md §0): the
// assistant now does everything a user can do by hand — deletes, invites, role
// changes, workspace settings, automations, autopilots, agent configuration.
//
// The three things these tests are actually protecting:
//
//  1. The CONFIRM PROTOCOL. A destructive tool called without confirm:true must
//     refuse AND leave the row standing. Both halves matter: a refusal that
//     still deleted would be worse than no gate at all.
//  2. The ROLE GATE being the product's. A plain member calling invite_member
//     must hit the router's own middleware refusal, not a second policy written
//     for the assistant.
//  3. list_issues counting the WORKSPACE. The bug that motivated it is silent:
//     list_my_issues answers workspace-wide questions with one person's issues.
//
// Non-member refusals for every tool here live in the meta-test
// TestEveryWorkspaceScopedToolRefusesNonMembers, which fails outright when a new
// workspace-scoped tool ships without an entry.

// ---------------------------------------------------------------------------
// list_issues
// ---------------------------------------------------------------------------

func TestAssistantListIssuesCoversTheWholeWorkspace(t *testing.T) {
	owner := newAssistantTestUser(t, "assistant-listissues@agora.dev")
	other := newAssistantTestUser(t, "assistant-listissues-other@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-listissues-ws", "LIS")
	addAssistantTestMember(t, ws, owner, "owner")
	addAssistantTestMember(t, ws, other, "member")
	newAssistantTestIssue(t, ws, "mine", owner, owner)
	newAssistantTestIssue(t, ws, "theirs", other, other)

	// THE regression: list_my_issues sees one issue, list_issues sees both.
	// A chart grounded on the first under-counts the workspace by half.
	mine, err := executeAssistantTool(t, owner, assistant.ToolListMyIssues, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("list_my_issues: %v", err)
	}
	if got := assistantIssueTitles(t, mine); len(got) != 1 || got[0] != "mine" {
		t.Fatalf("list_my_issues = %v, want just the caller's own", got)
	}

	all, err := executeAssistantTool(t, owner, assistant.ToolListIssues, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("list_issues: %v", err)
	}
	titles := assistantIssueTitles(t, all)
	if len(titles) != 2 {
		t.Fatalf("list_issues = %v, want both issues", titles)
	}
	if all["returned_count"] != float64(2) {
		t.Fatalf("returned_count = %v, want 2", all["returned_count"])
	}

	// Filters are the handler's own enums, rejected before the query.
	assistantToolRejects(t, owner, assistant.ToolListIssues,
		`{"workspace_id":"`+ws+`","status":"shipped"}`, "status must be one of")
	assistantToolRejects(t, owner, assistant.ToolListIssues,
		`{"workspace_id":"`+ws+`","project_id":"Ghost"}`, "no project called")

	filtered, err := executeAssistantTool(t, owner, assistant.ToolListIssues,
		`{"workspace_id":"`+ws+`","status":"todo","priority":"medium"}`)
	if err != nil {
		t.Fatalf("list_issues filtered: %v", err)
	}
	if len(assistantIssueTitles(t, filtered)) != 2 {
		t.Fatalf("filtered = %v", filtered)
	}
}

// Archived issues are hidden by default and reachable on request — the same
// two states the Issues page has.
func TestAssistantListIssuesIncludeArchived(t *testing.T) {
	owner := newAssistantTestUser(t, "assistant-listarch@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-listarch-ws", "LAR")
	addAssistantTestMember(t, ws, owner, "owner")
	newAssistantTestIssue(t, ws, "live one", owner, owner)
	archivedID := newAssistantTestIssue(t, ws, "archived one", owner, owner)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE issue SET archived_at = now() WHERE id = $1`, archivedID); err != nil {
		t.Fatalf("archive issue: %v", err)
	}

	visible, err := executeAssistantTool(t, owner, assistant.ToolListIssues, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("list_issues: %v", err)
	}
	if got := assistantIssueTitles(t, visible); len(got) != 1 || got[0] != "live one" {
		t.Fatalf("default listing = %v, want only the live issue", got)
	}

	withArchived, err := executeAssistantTool(t, owner, assistant.ToolListIssues,
		`{"workspace_id":"`+ws+`","include_archived":true}`)
	if err != nil {
		t.Fatalf("list_issues(include_archived): %v", err)
	}
	if got := assistantIssueTitles(t, withArchived); len(got) != 2 {
		t.Fatalf("include_archived = %v, want both", got)
	}
}

// The non-owner gate applies to the workspace-wide list exactly as it does to
// search and to list_my_issues — otherwise list_issues would be a way around it.
func TestAssistantListIssuesHonorsVisibilityGate(t *testing.T) {
	owner := newAssistantTestUser(t, "assistant-listgate-owner@agora.dev")
	member := newAssistantTestUser(t, "assistant-listgate-member@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-listgate-ws", "LGT")
	addAssistantTestMember(t, ws, owner, "owner")
	addAssistantTestMember(t, ws, member, "member")
	newAssistantTestIssue(t, ws, "owner's secret", owner, owner)
	newAssistantTestIssue(t, ws, "member's own", member, member)

	result, err := executeAssistantTool(t, member, assistant.ToolListIssues, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("list_issues: %v", err)
	}
	got := assistantIssueTitles(t, result)
	if len(got) != 1 || got[0] != "member's own" {
		t.Fatalf("a non-owner saw %v — the visibility gate did not apply", got)
	}
}

// ---------------------------------------------------------------------------
// The confirmation protocol
// ---------------------------------------------------------------------------
//
// This section used to hold TestAssistantDestructiveToolsRefuseWithoutConfirm
// and TestAssistantUnconfirmedDeleteLeavesTheRowStanding, which drove every
// destructive tool through `confirm` absent / false / "true" and pinned that
// each one refused. That contract is gone: the argument no longer exists,
// because a boolean the MODEL writes was never evidence that a HUMAN agreed.
//
// The same catalog-driven pins live in assistant_operations_test.go, one step
// stronger — every tool in assistant.DestructiveTools, called with arguments
// that resolve to a real row, must park a pending operation, answer
// needs_confirmation, and leave the row exactly where it was.

// ---------------------------------------------------------------------------
// Deletes — happy paths
// ---------------------------------------------------------------------------

func TestAssistantDeleteIssueAfterConfirmation(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-delissue@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-delissue-ws", "DIS")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	issueID := newAssistantTestIssue(t, ws, "doomed", user, user)
	deleted := recordBusEvents(t, protocol.EventIssueDeleted)

	result := assistantAskAndConfirm(t, user, session, assistant.ToolDeleteIssue,
		`{"workspace_id":"`+ws+`","ref":"DIS-1"}`)
	// The answer names what went — after the row is gone there is nothing left
	// to derive the identifier from, so the tool has to capture it first.
	if result["deleted"] != true || result["issue_identifier"] != "DIS-1" || result["title"] != "doomed" {
		t.Fatalf("delete_issue = %v", result)
	}

	var count int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue WHERE id = $1`, issueID).Scan(&count); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	if count != 0 {
		t.Fatal("the issue survived a confirmed delete")
	}
	// The real delete path ran, so the rest of the platform heard about it.
	if len(deleted()) == 0 {
		t.Fatal("no issue:deleted event — the delete did not go through the handler")
	}

	// A ref nobody has never reaches the confirmation seam: resolution refuses
	// first, so the user is never shown a card for a thing that is not there.
	assistantToolRejects(t, user, assistant.ToolDeleteIssue,
		`{"workspace_id":"`+ws+`","ref":"DIS-404"}`, "issue not found")
}

func TestAssistantDeleteProjectSprintAndLabel(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-delrest@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-delrest-ws", "DRS")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	projectID := newAssistantTestProject(t, ws, "Doomed project")
	labelID := newAssistantTestLabel(t, ws, "doomed", "#64748b")

	created, err := executeAssistantTool(t, user, assistant.ToolCreateSprint,
		`{"workspace_id":"`+ws+`","project_id":"Doomed project","name":"Sprint X"}`)
	if err != nil {
		t.Fatalf("create_sprint: %v", err)
	}
	sprint, _ := created["sprint"].(map[string]any)
	sprintID, _ := sprint["id"].(string)

	// Sprint first: deleting the project would cascade it away.
	if result := assistantAskAndConfirm(t, user, session, assistant.ToolDeleteSprint,
		`{"workspace_id":"`+ws+`","sprint":"Sprint X"}`); result["deleted"] != true || result["sprint"] != "Sprint X" {
		t.Fatalf("delete_sprint = %v", result)
	}
	assertAssistantRowGone(t, "sprint", sprintID)

	if result := assistantAskAndConfirm(t, user, session, assistant.ToolDeleteLabel,
		`{"workspace_id":"`+ws+`","label":"doomed"}`); result["deleted"] != true {
		t.Fatalf("delete_label = %v", result)
	}
	assertAssistantRowGone(t, "issue_label", labelID)

	if result := assistantAskAndConfirm(t, user, session, assistant.ToolDeleteProject,
		`{"workspace_id":"`+ws+`","project":"Doomed project"}`); result["deleted"] != true || result["project"] != "Doomed project" {
		t.Fatalf("delete_project = %v", result)
	}
	assertAssistantRowGone(t, "project", projectID)

	// A name nobody has is an answer, not a 500 — and not a confirmation card.
	assistantToolRejects(t, user, assistant.ToolDeleteProject,
		`{"workspace_id":"`+ws+`","project":"Ghost"}`, "no project called")
	assistantToolRejects(t, user, assistant.ToolDeleteLabel,
		`{"workspace_id":"`+ws+`","label":"ghost"}`, "no label called")
	assistantToolRejects(t, user, assistant.ToolDeleteSprint,
		`{"workspace_id":"`+ws+`","sprint":"Ghost"}`, "no sprint called")
}

func assertAssistantRowGone(t *testing.T, table, id string) {
	t.Helper()
	var count int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM `+table+` WHERE id = $1`, id).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if count != 0 {
		t.Fatalf("%s row %s survived a confirmed delete", table, id)
	}
}

// ---------------------------------------------------------------------------
// Comments: edit, resolve, delete
// ---------------------------------------------------------------------------

func TestAssistantCommentEditResolveAndDelete(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-commentedit@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-commentedit-ws", "CED")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	newAssistantTestIssue(t, ws, "discussed", user, user)

	if _, err := executeAssistantTool(t, user, assistant.ToolCommentIssue,
		`{"workspace_id":"`+ws+`","ref":"CED-1","body":"first draft"}`); err != nil {
		t.Fatalf("comment_issue: %v", err)
	}

	// list_comments now carries the id the other three tools address — without
	// it the model has nothing to pass but the text.
	thread, err := executeAssistantTool(t, user, assistant.ToolListComments,
		`{"workspace_id":"`+ws+`","ref":"CED-1"}`)
	if err != nil {
		t.Fatalf("list_comments: %v", err)
	}
	rows, _ := thread["comments"].([]any)
	if len(rows) != 1 {
		t.Fatalf("comments = %v", thread)
	}
	row, _ := rows[0].(map[string]any)
	commentID, _ := row["comment_id"].(string)
	if commentID == "" {
		t.Fatalf("list_comments returned no comment_id: %v", row)
	}

	if _, err := executeAssistantTool(t, user, assistant.ToolUpdateComment,
		`{"workspace_id":"`+ws+`","comment_id":"`+commentID+`","body":"second draft"}`); err != nil {
		t.Fatalf("update_comment: %v", err)
	}
	var content string
	if err := testPool.QueryRow(context.Background(),
		`SELECT content FROM comment WHERE id = $1`, commentID).Scan(&content); err != nil {
		t.Fatalf("read comment: %v", err)
	}
	if content != "second draft" {
		t.Fatalf("content = %q", content)
	}

	if result, err := executeAssistantTool(t, user, assistant.ToolResolveComment,
		`{"workspace_id":"`+ws+`","comment_id":"`+commentID+`"}`); err != nil {
		t.Fatalf("resolve_comment: %v", err)
	} else if result["resolved"] != true {
		t.Fatalf("resolve = %v", result)
	}
	var resolvedAt *string
	if err := testPool.QueryRow(context.Background(),
		`SELECT resolved_at::text FROM comment WHERE id = $1`, commentID).Scan(&resolvedAt); err != nil {
		t.Fatalf("read comment: %v", err)
	}
	if resolvedAt == nil {
		t.Fatal("comment was not resolved")
	}

	// Reversible, like every other resolve in the product.
	if _, err := executeAssistantTool(t, user, assistant.ToolResolveComment,
		`{"workspace_id":"`+ws+`","comment_id":"`+commentID+`","resolved":false}`); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := testPool.QueryRow(context.Background(),
		`SELECT resolved_at::text FROM comment WHERE id = $1`, commentID).Scan(&resolvedAt); err != nil {
		t.Fatalf("read comment: %v", err)
	}
	if resolvedAt != nil {
		t.Fatalf("comment was not reopened: %v", *resolvedAt)
	}

	assistantAskAndConfirm(t, user, session, assistant.ToolDeleteComment,
		`{"workspace_id":"`+ws+`","comment_id":"`+commentID+`"}`)
	assertAssistantRowGone(t, "comment", commentID)
}

// A comment on an issue the caller may not see is not reachable by id. Without
// the parent-issue lookup in assistantLoadComment, GetCommentInWorkspace alone
// would hand a plain member any comment in the workspace.
func TestAssistantCommentToolsHonorTheIssueVisibilityGate(t *testing.T) {
	owner := newAssistantTestUser(t, "assistant-cmtgate-owner@agora.dev")
	member := newAssistantTestUser(t, "assistant-cmtgate-member@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-cmtgate-ws", "CGT")
	addAssistantTestMember(t, ws, owner, "owner")
	addAssistantTestMember(t, ws, member, "member")
	issueID := newAssistantTestIssue(t, ws, "owner's issue", owner, owner)

	var commentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type)
		VALUES ($1, $2, 'member', $3, 'private discussion', 'comment')
		RETURNING id
	`, issueID, ws, owner).Scan(&commentID); err != nil {
		t.Fatalf("insert comment: %v", err)
	}

	for _, tc := range []struct{ tool, args string }{
		{assistant.ToolUpdateComment, `{"workspace_id":"` + ws + `","comment_id":"` + commentID + `","body":"x"}`},
		{assistant.ToolResolveComment, `{"workspace_id":"` + ws + `","comment_id":"` + commentID + `"}`},
		{assistant.ToolDeleteComment, `{"workspace_id":"` + ws + `","comment_id":"` + commentID + `"}`},
	} {
		if _, err := executeAssistantTool(t, member, tc.tool, tc.args); err == nil {
			t.Fatalf("%s reached a comment on an issue the caller cannot see", tc.tool)
		}
	}
	assertAssistantRowCount(t, "comment", commentID, 1)
}

func assertAssistantRowCount(t *testing.T, table, id string, want int) {
	t.Helper()
	var count int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM `+table+` WHERE id = $1`, id).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if count != want {
		t.Fatalf("%s rows for %s = %d, want %d", table, id, count, want)
	}
}

// ---------------------------------------------------------------------------
// Members
// ---------------------------------------------------------------------------

func TestAssistantInviteMemberCreatesARealInvitation(t *testing.T) {
	owner := newAssistantTestUser(t, "assistant-invite-owner@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-invite-ws", "INV")
	addAssistantTestMember(t, ws, owner, "owner")
	invited := recordBusEvents(t, protocol.EventInvitationCreated)

	result, err := executeAssistantTool(t, owner, assistant.ToolInviteMember,
		`{"workspace_id":"`+ws+`","email":"NewPerson@Example.com","role":"admin"}`)
	if err != nil {
		t.Fatalf("invite_member: %v", err)
	}
	if result["invited"] != true || result["role"] != "admin" {
		t.Fatalf("invite_member = %v", result)
	}
	// Normalised by the real handler, not by the tool.
	if result["email"] != "newperson@example.com" {
		t.Fatalf("email = %v, want it lowercased by the handler", result["email"])
	}

	var storedEmail, storedRole, status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT invitee_email, role, status FROM workspace_invitation WHERE workspace_id = $1`, ws,
	).Scan(&storedEmail, &storedRole, &status); err != nil {
		t.Fatalf("invitation row was not created: %v", err)
	}
	if storedEmail != "newperson@example.com" || storedRole != "admin" || status != "pending" {
		t.Fatalf("stored invitation = %s / %s / %s", storedEmail, storedRole, status)
	}
	if len(invited()) == 0 {
		t.Fatal("no invitation:created event — the invite did not go through the handler")
	}

	// The handler's own rules reach the model verbatim.
	assistantToolRejects(t, owner, assistant.ToolInviteMember,
		`{"workspace_id":"`+ws+`","email":"newperson@example.com"}`, "already pending")
	assistantToolRejects(t, owner, assistant.ToolInviteMember,
		`{"workspace_id":"`+ws+`","email":"someone@example.com","role":"owner"}`, "cannot invite as owner")
	// And the tool's own shape check, which turns "invite Anna" into a question
	// rather than an invitation to the address "anna".
	assistantToolRejects(t, owner, assistant.ToolInviteMember,
		`{"workspace_id":"`+ws+`","email":"Anna"}`, "full email address")
}

// THE role test: the gate lives in the router's middleware, not in the handler,
// so an assistant path that skipped it would hand every member the owner's
// buttons. The refusal must be the product's own.
func TestAssistantMemberWritesEnforceTheRouterRoleGate(t *testing.T) {
	owner := newAssistantTestUser(t, "assistant-role-owner@agora.dev")
	member := newAssistantTestUser(t, "assistant-role-member@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-role-ws", "ROL")
	addAssistantTestMember(t, ws, owner, "owner")
	addAssistantTestMember(t, ws, member, "member")

	for _, tc := range []struct{ tool, args string }{
		{assistant.ToolInviteMember, `{"workspace_id":"` + ws + `","email":"nobody@example.com"}`},
		{assistant.ToolUpdateMemberRole, `{"workspace_id":"` + ws + `","user_id":"` + owner + `","role":"member"}`},
		{assistant.ToolRemoveMember, `{"workspace_id":"` + ws + `","user_id":"` + owner + `"}`},
		{assistant.ToolUpdateWorkspace, `{"workspace_id":"` + ws + `","name":"Renamed by a member"}`},
		{assistant.ToolDeleteWorkspace, `{"workspace_id":"` + ws + `"}`},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			_, err := executeAssistantTool(t, member, tc.tool, tc.args)
			if err == nil {
				t.Fatalf("%s let a plain member through", tc.tool)
			}
			if !strings.Contains(err.Error(), "insufficient permissions") {
				t.Fatalf("%s refusal = %q, want the middleware's own message", tc.tool, err.Error())
			}
		})
	}

	// Nothing moved.
	var role, name string
	if err := testPool.QueryRow(context.Background(),
		`SELECT m.role, w.name FROM member m JOIN workspace w ON w.id = m.workspace_id
		  WHERE m.workspace_id = $1 AND m.user_id = $2`, ws, owner).Scan(&role, &name); err != nil {
		t.Fatalf("read member: %v", err)
	}
	if role != "owner" || name == "Renamed by a member" {
		t.Fatalf("a refused call still changed something: role=%s name=%s", role, name)
	}
}

func TestAssistantUpdateMemberRoleAndRemoveMember(t *testing.T) {
	owner := newAssistantTestUser(t, "assistant-members-owner@agora.dev")
	other := newAssistantTestUser(t, "assistant-members-other@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-members-ws", "MEM")
	addAssistantTestMember(t, ws, owner, "owner")
	addAssistantTestMember(t, ws, other, "member")
	session := newAssistantTestSession(t, owner)

	// user_id, not member_id: list_members returns the user id, so that is the
	// only id the model ever handles for a person.
	result, err := executeAssistantTool(t, owner, assistant.ToolUpdateMemberRole,
		`{"workspace_id":"`+ws+`","user_id":"`+other+`","role":"admin"}`)
	if err != nil {
		t.Fatalf("update_member_role: %v", err)
	}
	if result["updated"] != true || result["role"] != "admin" || result["previous_role"] != "member" {
		t.Fatalf("update_member_role = %v", result)
	}
	var role string
	if err := testPool.QueryRow(context.Background(),
		`SELECT role FROM member WHERE workspace_id = $1 AND user_id = $2`, ws, other).Scan(&role); err != nil {
		t.Fatalf("read member: %v", err)
	}
	if role != "admin" {
		t.Fatalf("stored role = %q", role)
	}

	// A no-op role change is a correction, not a write.
	assistantToolRejects(t, owner, assistant.ToolUpdateMemberRole,
		`{"workspace_id":"`+ws+`","user_id":"`+other+`","role":"admin"}`, "already admin")
	// Somebody who is not in the workspace at all.
	assistantToolRejects(t, owner, assistant.ToolUpdateMemberRole,
		`{"workspace_id":"`+ws+`","user_id":"`+newAssistantTestUser(t, "assistant-members-stranger@agora.dev")+`","role":"admin"}`,
		"not a member of this workspace")
	// Removing yourself has its own tool with its own last-owner rule.
	assistantToolRejects(t, owner, assistant.ToolRemoveMember,
		`{"workspace_id":"`+ws+`","user_id":"`+owner+`"}`, "use leave_workspace")

	removed := recordBusEvents(t, protocol.EventMemberRemoved)
	if result := assistantAskAndConfirm(t, owner, session, assistant.ToolRemoveMember,
		`{"workspace_id":"`+ws+`","user_id":"`+other+`"}`); result["removed"] != true {
		t.Fatalf("remove_member = %v", result)
	}
	var stillThere int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM member WHERE workspace_id = $1 AND user_id = $2`, ws, other).Scan(&stillThere); err != nil {
		t.Fatalf("count members: %v", err)
	}
	if stillThere != 0 {
		t.Fatal("the member was not removed")
	}
	if len(removed()) == 0 {
		t.Fatal("no member:removed event — the removal did not go through the handler")
	}
}

// ---------------------------------------------------------------------------
// Workspaces
// ---------------------------------------------------------------------------

func TestAssistantCreateAndUpdateWorkspace(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-createws@agora.dev")

	result, err := executeAssistantTool(t, user, assistant.ToolCreateWorkspace,
		`{"name":"Assistant Q4 Planning","description":"the quarter"}`)
	if err != nil {
		t.Fatalf("create_workspace: %v", err)
	}
	workspace, _ := result["workspace"].(map[string]any)
	wsID, _ := workspace["id"].(string)
	if result["created"] != true || wsID == "" {
		t.Fatalf("create_workspace = %v", result)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, wsID) })

	// The slug was derived from the name — a user who says "call it Q4
	// Planning" has not thought about URLs, and making the model invent one
	// produces a slug nobody wanted.
	if workspace["slug"] != "assistant-q4-planning" {
		t.Fatalf("slug = %v, want it derived from the name", workspace["slug"])
	}
	if workspace["url_path"] != "/assistant-q4-planning" {
		t.Fatalf("url_path = %v", workspace["url_path"])
	}

	// The caller is the owner, so the very next tool call works.
	var role string
	if err := testPool.QueryRow(context.Background(),
		`SELECT role FROM member WHERE workspace_id = $1 AND user_id = $2`, wsID, user).Scan(&role); err != nil {
		t.Fatalf("creator is not a member: %v", err)
	}
	if role != "owner" {
		t.Fatalf("creator role = %q, want owner", role)
	}

	if _, err := executeAssistantTool(t, user, assistant.ToolUpdateWorkspace,
		`{"workspace_id":"`+wsID+`","name":"Q4 Planning (renamed)","context":"we ship on Fridays"}`); err != nil {
		t.Fatalf("update_workspace: %v", err)
	}
	var name, description, wsContext string
	if err := testPool.QueryRow(context.Background(),
		`SELECT name, coalesce(description,''), coalesce(context,'') FROM workspace WHERE id = $1`, wsID,
	).Scan(&name, &description, &wsContext); err != nil {
		t.Fatalf("read workspace: %v", err)
	}
	if name != "Q4 Planning (renamed)" || wsContext != "we ship on Fridays" {
		t.Fatalf("stored %q / %q", name, wsContext)
	}
	// Untouched fields survive — UpdateWorkspace COALESCEs, and the tool must
	// only send what the model actually passed.
	if description != "the quarter" {
		t.Fatalf("description was lost: %q", description)
	}

	assistantToolRejects(t, user, assistant.ToolUpdateWorkspace,
		`{"workspace_id":"`+wsID+`"}`, "nothing to change")
	assistantToolRejects(t, user, assistant.ToolCreateWorkspace, `{"name":"  "}`, "name is required")
	// A reserved slug is the product's refusal, relayed.
	assistantToolRejects(t, user, assistant.ToolCreateWorkspace,
		`{"name":"Login","slug":"login"}`, "reserved")
}

func TestAssistantLeaveAndDeleteWorkspace(t *testing.T) {
	owner := newAssistantTestUser(t, "assistant-leave-owner@agora.dev")
	other := newAssistantTestUser(t, "assistant-leave-other@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-leave-ws", "LVE")
	addAssistantTestMember(t, ws, owner, "owner")
	addAssistantTestMember(t, ws, other, "member")
	ownerSession := newAssistantTestSession(t, owner)
	otherSession := newAssistantTestSession(t, other)

	result := assistantAskAndConfirm(t, other, otherSession, assistant.ToolLeaveWorkspace,
		`{"workspace_id":"`+ws+`"}`)
	if result["left"] != true || result["workspace_slug"] != "assistant-leave-ws" {
		t.Fatalf("leave_workspace = %v", result)
	}
	var stillThere int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM member WHERE workspace_id = $1 AND user_id = $2`, ws, other).Scan(&stillThere); err != nil {
		t.Fatalf("count members: %v", err)
	}
	if stillThere != 0 {
		t.Fatal("the member did not leave")
	}
	// And they are out: the next tool call is a non-member refusal.
	if _, err := executeAssistantTool(t, other, assistant.ToolListIssues,
		`{"workspace_id":"`+ws+`"}`); err == nil {
		t.Fatal("a member who left still reads the workspace")
	}

	// The last owner cannot leave — the handler's own invariant. It only shows
	// up at execution time, so the card appears and the CONFIRM is what fails:
	// the refusal reaches the user, and the workspace still has its owner.
	lastOwnerAsk := assistantAsk(t, owner, ownerSession, assistant.ToolLeaveWorkspace,
		`{"workspace_id":"`+ws+`"}`)
	lastOwnerConfirm := postAssistantOperation(t, owner, assistantOperationID(t, lastOwnerAsk), "confirm")
	if lastOwnerConfirm.Code != http.StatusConflict ||
		!strings.Contains(lastOwnerConfirm.Body.String(), "at least one owner") {
		t.Fatalf("last-owner leave = %d %s", lastOwnerConfirm.Code, lastOwnerConfirm.Body.String())
	}

	deleted := recordBusEvents(t, protocol.EventWorkspaceDeleted)
	if result := assistantAskAndConfirm(t, owner, ownerSession, assistant.ToolDeleteWorkspace,
		`{"workspace_id":"`+ws+`"}`); result["deleted"] != true || result["workspace_name"] == "" {
		t.Fatalf("delete_workspace = %v", result)
	}
	assertAssistantRowGone(t, "workspace", ws)
	if len(deleted()) == 0 {
		t.Fatal("no workspace:deleted event")
	}
}

// Deleting a workspace is owner-only, and an admin is not an owner.
func TestAssistantDeleteWorkspaceIsOwnerOnly(t *testing.T) {
	owner := newAssistantTestUser(t, "assistant-delws-owner@agora.dev")
	admin := newAssistantTestUser(t, "assistant-delws-admin@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-delws-ws", "DWS")
	addAssistantTestMember(t, ws, owner, "owner")
	addAssistantTestMember(t, ws, admin, "admin")

	// Refused at ASK time: an admin is never shown a card for something their
	// role cannot authorize.
	assistantToolRejects(t, admin, assistant.ToolDeleteWorkspace,
		`{"workspace_id":"`+ws+`"}`, "insufficient permissions")
	assertAssistantRowCount(t, "workspace", ws, 1)

	// An admin CAN edit it, which is the parity half of the same check.
	if _, err := executeAssistantTool(t, admin, assistant.ToolUpdateWorkspace,
		`{"workspace_id":"`+ws+`","name":"Edited by an admin"}`); err != nil {
		t.Fatalf("an admin must be able to edit workspace settings: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Autopilots
// ---------------------------------------------------------------------------

func TestAssistantAutopilotTools(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-autopilot@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-autopilot-ws", "APT")
	addAssistantTestMember(t, ws, user, "owner")
	runtimeID := newAssistantTestRuntime(t, ws)
	agentID := newAssistantTestAgent(t, ws, runtimeID, "Nightly bot", "workspace", user)

	created, err := executeAssistantTool(t, user, assistant.ToolCreateAutopilot,
		`{"workspace_id":"`+ws+`","title":"Nightly digest","prompt":"summarise yesterday",`+
			`"assignee_id":"`+agentID+`","cron_expression":"0 9 * * 1-5","timezone":"Asia/Tashkent"}`)
	if err != nil {
		t.Fatalf("create_autopilot: %v", err)
	}
	if created["created"] != true || created["cron_expression"] != "0 9 * * 1-5" {
		t.Fatalf("create_autopilot = %v", created)
	}
	if created["schedule_error"] != nil {
		t.Fatalf("the schedule did not attach: %v", created["schedule_error"])
	}

	// The schedule is a second resource; the tool folds it into one call so
	// "every weekday at 9" does not need a round the model will forget.
	listed, err := executeAssistantTool(t, user, assistant.ToolListAutopilots, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("list_autopilots: %v", err)
	}
	rows, _ := listed["autopilots"].([]any)
	if len(rows) != 1 {
		t.Fatalf("autopilots = %v", listed)
	}
	row, _ := rows[0].(map[string]any)
	if row["title"] != "Nightly digest" || row["status"] != "active" {
		t.Fatalf("autopilot row = %v", row)
	}
	if row["cron_expression"] != "0 9 * * 1-5" || row["timezone"] != "Asia/Tashkent" {
		t.Fatalf("schedule not reported: %v", row)
	}
	if row["assignee_name"] != "Nightly bot" {
		t.Fatalf("assignee not named: %v", row)
	}

	// Re-schedule + rewrite the prompt in one update.
	if _, err := executeAssistantTool(t, user, assistant.ToolUpdateAutopilot,
		`{"workspace_id":"`+ws+`","autopilot":"Nightly digest","prompt":"summarise the week","cron_expression":"0 18 * * 5"}`); err != nil {
		t.Fatalf("update_autopilot: %v", err)
	}
	var description, cron string
	if err := testPool.QueryRow(context.Background(), `
		SELECT a.description, t.cron_expression FROM autopilot a
		JOIN autopilot_trigger t ON t.autopilot_id = a.id AND t.kind = 'schedule'
		WHERE a.workspace_id = $1
	`, ws).Scan(&description, &cron); err != nil {
		t.Fatalf("read autopilot: %v", err)
	}
	if description != "summarise the week" || cron != "0 18 * * 5" {
		t.Fatalf("stored %q / %q", description, cron)
	}

	// Pausing is how you turn one off without losing it — and a paused
	// autopilot refuses to run, which is the handler's own rule.
	if _, err := executeAssistantTool(t, user, assistant.ToolUpdateAutopilot,
		`{"workspace_id":"`+ws+`","autopilot":"Nightly digest","status":"paused"}`); err != nil {
		t.Fatalf("pause: %v", err)
	}
	assistantToolRejects(t, user, assistant.ToolRunAutopilotNow,
		`{"workspace_id":"`+ws+`","autopilot":"Nightly digest"}`, "not active")

	assistantToolRejects(t, user, assistant.ToolUpdateAutopilot,
		`{"workspace_id":"`+ws+`","autopilot":"Nightly digest","status":"retired"}`, "status must be one of")
	assistantToolRejects(t, user, assistant.ToolUpdateAutopilot,
		`{"workspace_id":"`+ws+`","autopilot":"Nightly digest"}`, "nothing to change")
	assistantToolRejects(t, user, assistant.ToolUpdateAutopilot,
		`{"workspace_id":"`+ws+`","autopilot":"Ghost","status":"paused"}`, "no autopilot called")
	assistantToolRejects(t, user, assistant.ToolCreateAutopilot,
		`{"workspace_id":"`+ws+`","title":"Headless"}`, "assignee_id is required")
	assistantToolRejects(t, user, assistant.ToolCreateAutopilot,
		`{"workspace_id":"`+ws+`","title":" ","assignee_id":"`+agentID+`"}`, "title is required")
}

// An autopilot with no cron is legal — it just has to say so, or the user walks
// away believing something is scheduled when nothing is.
func TestAssistantCreateAutopilotWithoutAScheduleSaysSo(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-autopilot-nocron@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-autopilot-nocron-ws", "APN")
	addAssistantTestMember(t, ws, user, "owner")
	runtimeID := newAssistantTestRuntime(t, ws)
	agentID := newAssistantTestAgent(t, ws, runtimeID, "Manual bot", "workspace", user)

	created, err := executeAssistantTool(t, user, assistant.ToolCreateAutopilot,
		`{"workspace_id":"`+ws+`","title":"On demand","assignee_id":"`+agentID+`"}`)
	if err != nil {
		t.Fatalf("create_autopilot: %v", err)
	}
	note, _ := created["note"].(string)
	if !strings.Contains(note, "no schedule") {
		t.Fatalf("note = %q, want it to say the autopilot is manual-only", note)
	}
}

// ---------------------------------------------------------------------------
// Automations
// ---------------------------------------------------------------------------

func TestAssistantAutomationTools(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-automation@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-automation-ws", "ATM")
	addAssistantTestMember(t, ws, user, "owner")
	newAssistantTestLabel(t, ws, "needs-qa", "#64748b")

	// The catalog rides along with the listing: create_automation's arguments
	// are a grammar, and a model guessing "status_changed" gets a 400 it then
	// narrates as a broken feature.
	empty, err := executeAssistantTool(t, user, assistant.ToolListAutomations, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("list_automations: %v", err)
	}
	catalog, _ := empty["catalog"].(map[string]any)
	triggers, _ := catalog["trigger_types"].([]any)
	steps, _ := catalog["step_types"].([]any)
	if len(triggers) == 0 || len(steps) == 0 {
		t.Fatalf("catalog = %v", catalog)
	}

	created, err := executeAssistantTool(t, user, assistant.ToolCreateAutomation,
		`{"workspace_id":"`+ws+`","name":"Tag review","trigger_type":"issue.status_changed",`+
			`"conditions":"[{\"field\":\"status\",\"op\":\"eq\",\"value\":\"in_review\"}]",`+
			`"actions":"[{\"type\":\"add_label\",\"config\":{\"name\":\"needs-qa\"}}]"}`)
	if err != nil {
		t.Fatalf("create_automation: %v", err)
	}
	automation, _ := created["automation"].(map[string]any)
	automationID, _ := automation["id"].(string)
	if created["created"] != true || automationID == "" || automation["enabled"] != true {
		t.Fatalf("create_automation = %v", created)
	}

	// The flow persisted as the editor would have written it.
	var name, trigger string
	var conditions, actions []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT name, trigger_type, conditions, actions FROM automation WHERE id = $1`, automationID,
	).Scan(&name, &trigger, &conditions, &actions); err != nil {
		t.Fatalf("read automation: %v", err)
	}
	if name != "Tag review" || trigger != "issue.status_changed" {
		t.Fatalf("stored %q / %q", name, trigger)
	}
	if !strings.Contains(string(actions), "add_label") || !strings.Contains(string(conditions), "in_review") {
		t.Fatalf("stored flow = %s / %s", conditions, actions)
	}

	// Switching off is the reversible answer to "stop that rule".
	if result, err := executeAssistantTool(t, user, assistant.ToolSetAutomationEnabled,
		`{"workspace_id":"`+ws+`","automation":"Tag review","enabled":false}`); err != nil {
		t.Fatalf("set_automation_enabled: %v", err)
	} else if result["enabled"] != false {
		t.Fatalf("set_automation_enabled = %v", result)
	}
	var enabled bool
	if err := testPool.QueryRow(context.Background(),
		`SELECT enabled FROM automation WHERE id = $1`, automationID).Scan(&enabled); err != nil {
		t.Fatalf("read automation: %v", err)
	}
	if enabled {
		t.Fatal("the automation was not switched off")
	}

	// Bad JSON and the engine's own validation both reach the model as
	// corrections rather than as failures.
	assistantToolRejects(t, user, assistant.ToolCreateAutomation,
		`{"workspace_id":"`+ws+`","name":"Broken","trigger_type":"issue.created","actions":"not json"}`,
		"actions must be valid JSON")
	assistantToolRejects(t, user, assistant.ToolCreateAutomation,
		`{"workspace_id":"`+ws+`","name":"Broken","trigger_type":"issue.created","actions":"[]"}`,
		"actions is required")
	assistantToolRejects(t, user, assistant.ToolCreateAutomation,
		`{"workspace_id":"`+ws+`","name":"Broken","trigger_type":"issue.exploded",`+
			`"actions":"[{\"type\":\"add_label\",\"config\":{\"name\":\"x\"}}]"}`,
		"unknown trigger")
	assistantToolRejects(t, user, assistant.ToolSetAutomationEnabled,
		`{"workspace_id":"`+ws+`","automation":"Tag review"}`, "enabled is required")
	assistantToolRejects(t, user, assistant.ToolDeleteAutomation,
		`{"workspace_id":"`+ws+`","automation":"Ghost"}`, "no automation called")

	if result := assistantAskAndConfirm(t, user, newAssistantTestSession(t, user), assistant.ToolDeleteAutomation,
		`{"workspace_id":"`+ws+`","automation":"Tag review"}`); result["deleted"] != true {
		t.Fatalf("delete_automation = %v", result)
	}
	assertAssistantRowGone(t, "automation", automationID)
}

// ---------------------------------------------------------------------------
// Agent configuration parity
// ---------------------------------------------------------------------------

func TestAssistantUpdateAgentConfigParity(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-agentcfg@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-agentcfg-ws", "ACF")
	addAssistantTestMember(t, ws, user, "owner")
	runtimeID := newAssistantTestRuntime(t, ws)
	agentID := newAssistantTestAgent(t, ws, runtimeID, "Configurable", "workspace", user)

	if _, err := executeAssistantTool(t, user, assistant.ToolUpdateAgent,
		`{"workspace_id":"`+ws+`","agent_id":"Configurable","visibility":"private","max_concurrent_tasks":4}`); err != nil {
		t.Fatalf("update_agent: %v", err)
	}
	var visibility string
	var maxTasks int32
	if err := testPool.QueryRow(context.Background(),
		`SELECT visibility, max_concurrent_tasks FROM agent WHERE id = $1`, agentID,
	).Scan(&visibility, &maxTasks); err != nil {
		t.Fatalf("read agent: %v", err)
	}
	if visibility != "private" || maxTasks != 4 {
		t.Fatalf("stored %s / %d", visibility, maxTasks)
	}

	// Archiving routes to the dedicated endpoint, not to a status field —
	// archiving also cancels the agent's pending tasks.
	if result, err := executeAssistantTool(t, user, assistant.ToolUpdateAgent,
		`{"workspace_id":"`+ws+`","agent_id":"Configurable","archived":true}`); err != nil {
		t.Fatalf("archive agent: %v", err)
	} else if result["archived"] != true {
		t.Fatalf("archive = %v", result)
	}
	var archivedAt *string
	if err := testPool.QueryRow(context.Background(),
		`SELECT archived_at::text FROM agent WHERE id = $1`, agentID).Scan(&archivedAt); err != nil {
		t.Fatalf("read agent: %v", err)
	}
	if archivedAt == nil {
		t.Fatal("agent was not archived")
	}

	// Archiving something already archived is a no-op, not the handler's 409:
	// through a conversation the user asked for a state, and they have it.
	if _, err := executeAssistantTool(t, user, assistant.ToolUpdateAgent,
		`{"workspace_id":"`+ws+`","agent_id":"Configurable","archived":true}`); err != nil {
		t.Fatalf("re-archiving must be a no-op, got %v", err)
	}

	if _, err := executeAssistantTool(t, user, assistant.ToolUpdateAgent,
		`{"workspace_id":"`+ws+`","agent_id":"Configurable","archived":false}`); err != nil {
		t.Fatalf("restore agent: %v", err)
	}
	if err := testPool.QueryRow(context.Background(),
		`SELECT archived_at::text FROM agent WHERE id = $1`, agentID).Scan(&archivedAt); err != nil {
		t.Fatalf("read agent: %v", err)
	}
	if archivedAt != nil {
		t.Fatalf("agent was not restored: %v", *archivedAt)
	}

	assistantToolRejects(t, user, assistant.ToolUpdateAgent,
		`{"workspace_id":"`+ws+`","agent_id":"Configurable","visibility":"everyone"}`, "visibility must be one of")
	assistantToolRejects(t, user, assistant.ToolUpdateAgent,
		`{"workspace_id":"`+ws+`","agent_id":"Configurable","max_concurrent_tasks":0}`, "at least 1")
	assistantToolRejects(t, user, assistant.ToolUpdateAgent,
		`{"workspace_id":"`+ws+`","agent_id":"Configurable"}`, "nothing to change")
}

// ---------------------------------------------------------------------------
// Inbox, pins, subscriptions
// ---------------------------------------------------------------------------

// mark_inbox_read is the tool the ctxWorkspaceID fix was for: MarkAllInboxRead
// reads its workspace from context ONLY, so before the synthetic request ran
// through the workspace middleware it answered 400 with no workspace at all.
func TestAssistantMarkInboxRead(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-inboxread@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-inboxread-ws", "IBR")
	addAssistantTestMember(t, ws, user, "owner")
	issueID := newAssistantTestIssue(t, ws, "noisy issue", user, user)

	var firstItem string
	for i, title := range []string{"one", "two"} {
		var id string
		if err := testPool.QueryRow(context.Background(), `
			INSERT INTO inbox_item (workspace_id, recipient_type, recipient_id, type, severity, issue_id, title, body)
			VALUES ($1, 'member', $2, 'mention', 'info', $3, $4, 'body')
			RETURNING id
		`, ws, user, issueID, title).Scan(&id); err != nil {
			t.Fatalf("insert inbox item: %v", err)
		}
		if i == 0 {
			firstItem = id
		}
	}

	// One item.
	if result, err := executeAssistantTool(t, user, assistant.ToolMarkInboxRead,
		`{"workspace_id":"`+ws+`","item_id":"`+firstItem+`"}`); err != nil {
		t.Fatalf("mark_inbox_read(item): %v", err)
	} else if result["marked_read"] != true {
		t.Fatalf("mark_inbox_read = %v", result)
	}
	var unread int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM inbox_item WHERE workspace_id = $1 AND read = false`, ws).Scan(&unread); err != nil {
		t.Fatalf("count unread: %v", err)
	}
	if unread != 1 {
		t.Fatalf("unread after one = %d, want 1", unread)
	}

	// The rest.
	result, err := executeAssistantTool(t, user, assistant.ToolMarkInboxRead, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("mark_inbox_read(all): %v", err)
	}
	if result["marked_read"] != true || result["items_marked"] != float64(1) {
		t.Fatalf("mark_inbox_read(all) = %v", result)
	}
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM inbox_item WHERE workspace_id = $1 AND read = false`, ws).Scan(&unread); err != nil {
		t.Fatalf("count unread: %v", err)
	}
	if unread != 0 {
		t.Fatalf("unread after all = %d, want 0", unread)
	}

	assistantToolRejects(t, user, assistant.ToolMarkInboxRead,
		`{"workspace_id":"`+ws+`","item_id":"not-a-uuid"}`, "item_id must be")
}

// Somebody else's notification is not reachable by id: loadInboxItemForUser
// answers 404 for a recipient that is not the caller.
func TestAssistantMarkInboxReadCannotTouchSomeoneElsesItem(t *testing.T) {
	owner := newAssistantTestUser(t, "assistant-inbox-owner@agora.dev")
	other := newAssistantTestUser(t, "assistant-inbox-other@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-inbox-other-ws", "IBO")
	addAssistantTestMember(t, ws, owner, "owner")
	addAssistantTestMember(t, ws, other, "member")

	var itemID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO inbox_item (workspace_id, recipient_type, recipient_id, type, severity, title, body)
		VALUES ($1, 'member', $2, 'mention', 'info', 'owner only', 'body')
		RETURNING id
	`, ws, owner).Scan(&itemID); err != nil {
		t.Fatalf("insert inbox item: %v", err)
	}

	if _, err := executeAssistantTool(t, other, assistant.ToolMarkInboxRead,
		`{"workspace_id":"`+ws+`","item_id":"`+itemID+`"}`); err == nil {
		t.Fatal("a member marked somebody else's inbox item read")
	}
	var read bool
	if err := testPool.QueryRow(context.Background(),
		`SELECT read FROM inbox_item WHERE id = $1`, itemID).Scan(&read); err != nil {
		t.Fatalf("read inbox item: %v", err)
	}
	if read {
		t.Fatal("somebody else's item was marked read")
	}
}

func TestAssistantPinAndSubscribe(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-pinsub@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-pinsub-ws", "PNS")
	addAssistantTestMember(t, ws, user, "owner")
	issueID := newAssistantTestIssue(t, ws, "worth watching", user, user)
	projectID := newAssistantTestProject(t, ws, "Pinned project")

	// By identifier and by project title — the model never has to carry a UUID.
	if result, err := executeAssistantTool(t, user, assistant.ToolPinItem,
		`{"workspace_id":"`+ws+`","item_type":"issue","item":"PNS-1"}`); err != nil {
		t.Fatalf("pin_item(issue): %v", err)
	} else if result["pinned"] != true || result["item"] != "PNS-1" {
		t.Fatalf("pin_item = %v", result)
	}
	if _, err := executeAssistantTool(t, user, assistant.ToolPinItem,
		`{"workspace_id":"`+ws+`","item_type":"project","item":"Pinned project"}`); err != nil {
		t.Fatalf("pin_item(project): %v", err)
	}
	var pins int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM pinned_item WHERE workspace_id = $1 AND user_id = $2`, ws, user).Scan(&pins); err != nil {
		t.Fatalf("count pins: %v", err)
	}
	if pins != 2 {
		t.Fatalf("pins = %d, want 2", pins)
	}

	if result, err := executeAssistantTool(t, user, assistant.ToolPinItem,
		`{"workspace_id":"`+ws+`","item_type":"issue","item":"PNS-1","pinned":false}`); err != nil {
		t.Fatalf("unpin: %v", err)
	} else if result["pinned"] != false {
		t.Fatalf("unpin = %v", result)
	}
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM pinned_item WHERE workspace_id = $1 AND user_id = $2 AND item_type = 'issue'`,
		ws, user).Scan(&pins); err != nil {
		t.Fatalf("count pins: %v", err)
	}
	if pins != 0 {
		t.Fatal("the issue was not unpinned")
	}
	_ = projectID

	// Subscriptions are the caller's own, both directions.
	if result, err := executeAssistantTool(t, user, assistant.ToolSubscribeIssue,
		`{"workspace_id":"`+ws+`","ref":"PNS-1"}`); err != nil {
		t.Fatalf("subscribe_issue: %v", err)
	} else if result["subscribed"] != true || result["issue_identifier"] != "PNS-1" {
		t.Fatalf("subscribe_issue = %v", result)
	}
	var subs int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue_subscriber WHERE issue_id = $1 AND user_id = $2`, issueID, user).Scan(&subs); err != nil {
		t.Fatalf("count subscribers: %v", err)
	}
	if subs != 1 {
		t.Fatalf("subscribers = %d, want 1", subs)
	}

	if _, err := executeAssistantTool(t, user, assistant.ToolSubscribeIssue,
		`{"workspace_id":"`+ws+`","ref":"PNS-1","subscribed":false}`); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue_subscriber WHERE issue_id = $1 AND user_id = $2`, issueID, user).Scan(&subs); err != nil {
		t.Fatalf("count subscribers: %v", err)
	}
	if subs != 0 {
		t.Fatalf("subscribers after unsubscribe = %d, want 0", subs)
	}

	assistantToolRejects(t, user, assistant.ToolPinItem,
		`{"workspace_id":"`+ws+`","item_type":"sprint","item":"x"}`, "item_type must be one of")
	assistantToolRejects(t, user, assistant.ToolPinItem,
		`{"workspace_id":"`+ws+`","item_type":"issue","item":"PNS-404"}`, "issue not found")
	assistantToolRejects(t, user, assistant.ToolSubscribeIssue,
		`{"workspace_id":"`+ws+`","ref":"PNS-404"}`, "issue not found")
}
