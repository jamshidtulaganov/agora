package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
)

// Tests for the tools that took the assistant from "I can't create projects
// here, use the Projects section" to actually doing the work — projects,
// sprints, labels, archiving, and the setup half (agents and skills).
//
// Every write tool below gets the same three: it works, a non-member is refused
// (in TestEveryWorkspaceScopedToolRefusesNonMembers, which fails outright when a
// new tool ships without an entry), and one bad-argument case comes back as a
// correction the model can act on rather than a 500.

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

func newAssistantTestLabel(t *testing.T, workspaceID, name, color string) string {
	t.Helper()
	var labelID string
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO issue_label (workspace_id, name, color) VALUES ($1, $2, $3) RETURNING id`,
		workspaceID, name, color,
	).Scan(&labelID); err != nil {
		t.Fatalf("insert label: %v", err)
	}
	return labelID
}

func newAssistantTestSkill(t *testing.T, workspaceID, creatorID, name string) string {
	t.Helper()
	var skillID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO skill (workspace_id, name, description, content, config, created_by)
		VALUES ($1, $2, 'a test skill', '# Test', '{}'::jsonb, $3)
		RETURNING id
	`, workspaceID, name, creatorID).Scan(&skillID); err != nil {
		t.Fatalf("insert skill: %v", err)
	}
	return skillID
}

// assistantToolRejects asserts that a tool refuses these arguments, and that the
// refusal mentions `wantSubstring` — the point of a tool error is that it tells
// the model what to fix.
func assistantToolRejects(t *testing.T, userID, tool, args, wantSubstring string) {
	t.Helper()
	_, err := executeAssistantTool(t, userID, tool, args)
	if err == nil {
		t.Fatalf("%s accepted %s", tool, args)
	}
	if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(wantSubstring)) {
		t.Fatalf("%s error = %q, want it to mention %q", tool, err.Error(), wantSubstring)
	}
}

// ---------------------------------------------------------------------------
// get_project — the tool that makes "the TEST project" grounded
// ---------------------------------------------------------------------------

func TestAssistantGetProjectResolvesByTitleAndID(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-getproj@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-getproj-ws", "GPJ")
	addAssistantTestMember(t, ws, user, "owner")
	projectID := newAssistantTestProject(t, ws, "TEST")
	if _, err := testPool.Exec(context.Background(),
		`UPDATE project SET description = 'the sandbox project', priority = 'high' WHERE id = $1`,
		projectID); err != nil {
		t.Fatalf("set description: %v", err)
	}

	// UUID, exact title, different case, and a substring all reach the same row.
	for _, ref := range []string{projectID, "TEST", "test", "TES"} {
		result, err := executeAssistantTool(t, user, assistant.ToolGetProject,
			`{"workspace_id":"`+ws+`","project":"`+ref+`"}`)
		if err != nil {
			t.Fatalf("get_project(%s): %v", ref, err)
		}
		if result["id"] != projectID || result["title"] != "TEST" {
			t.Fatalf("get_project(%s) = %v", ref, result)
		}
		if result["description"] != "the sandbox project" || result["priority"] != "high" {
			t.Fatalf("get_project(%s) detail = %v", ref, result)
		}
		if result["url_path"] != "/assistant-getproj-ws/projects/"+projectID {
			t.Fatalf("url_path = %v", result["url_path"])
		}
	}

	// A name nobody has is an answer, not a reason to invent one.
	assistantToolRejects(t, user, assistant.ToolGetProject,
		`{"workspace_id":"`+ws+`","project":"Nope"}`, "no project called")
}

// Two projects whose titles both contain the phrase must NOT be silently
// disambiguated — picking one is how an assistant edits the wrong thing.
func TestAssistantGetProjectRefusesAmbiguousTitle(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-ambig@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-ambig-ws", "AMB")
	addAssistantTestMember(t, ws, user, "owner")
	newAssistantTestProject(t, ws, "Web frontend")
	newAssistantTestProject(t, ws, "Web backend")

	assistantToolRejects(t, user, assistant.ToolGetProject,
		`{"workspace_id":"`+ws+`","project":"Web"}`, "more than one project")

	// An exact title still wins over the substring cloud.
	result, err := executeAssistantTool(t, user, assistant.ToolGetProject,
		`{"workspace_id":"`+ws+`","project":"Web frontend"}`)
	if err != nil {
		t.Fatalf("get_project exact: %v", err)
	}
	if result["title"] != "Web frontend" {
		t.Fatalf("exact title = %v", result["title"])
	}
}

// ---------------------------------------------------------------------------
// create_project / update_project
// ---------------------------------------------------------------------------

func TestAssistantCreateProject(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-cproj@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-cproj-ws", "CPJ")
	addAssistantTestMember(t, ws, user, "owner")

	result, err := executeAssistantTool(t, user, assistant.ToolCreateProject,
		`{"workspace_id":"`+ws+`","title":"TEST","description":"scratch space","status":"in_progress","priority":"high"}`)
	if err != nil {
		t.Fatalf("create_project: %v", err)
	}
	if result["created"] != true {
		t.Fatalf("create_project = %v", result)
	}
	project, _ := result["project"].(map[string]any)
	if project["title"] != "TEST" || project["status"] != "in_progress" || project["priority"] != "high" {
		t.Fatalf("project = %v", project)
	}

	// It is a real row created through the real handler, not a tool-shaped echo.
	var title, description, status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT title, description, status FROM project WHERE workspace_id = $1`, ws,
	).Scan(&title, &description, &status); err != nil {
		t.Fatalf("project was not created: %v", err)
	}
	if title != "TEST" || description != "scratch space" || status != "in_progress" {
		t.Fatalf("stored project = %s / %s / %s", title, description, status)
	}

	// Validation reaches the model as a correction, not a 500.
	assistantToolRejects(t, user, assistant.ToolCreateProject,
		`{"workspace_id":"`+ws+`","title":"  "}`, "title is required")
	assistantToolRejects(t, user, assistant.ToolCreateProject,
		`{"workspace_id":"`+ws+`","title":"Other","status":"active"}`, "invalid status")
	assistantToolRejects(t, user, assistant.ToolCreateProject,
		`{"workspace_id":"`+ws+`","title":"Other","lead_type":"member","lead_id":"`+ws+`"}`, "not a member")
}

// THE regression this whole file exists for.
//
// Queries.UpdateProject COALESCEs only title/status/priority/settings and
// assigns description, icon, lead_type, lead_id and squad_id unconditionally —
// so a partial params struct NULLs five columns. The HTTP handler defends
// against that by seeding those five from the row it loaded, which is exactly
// why the assistant's update goes through the handler rather than the query.
// An update of ONLY status must leave everything else standing.
func TestAssistantUpdateProjectPreservesUnsentColumns(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-uproj@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-uproj-ws", "UPJ")
	addAssistantTestMember(t, ws, user, "owner")
	projectID := newAssistantTestProject(t, ws, "Preserve me")

	runtimeID := newAssistantTestRuntime(t, ws)
	leaderID := newAssistantTestAgent(t, ws, runtimeID, "Squad lead", "workspace", user)
	var squadID string
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
		 VALUES ($1, 'Dev squad', '', $2, $2) RETURNING id`,
		ws, leaderID).Scan(&squadID); err != nil {
		t.Fatalf("insert squad: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE project
		SET description = 'do not lose me', icon = 'rocket',
		    lead_type = 'member', lead_id = $2, squad_id = $3, priority = 'high'
		WHERE id = $1
	`, projectID, user, squadID); err != nil {
		t.Fatalf("seed project columns: %v", err)
	}

	if _, err := executeAssistantTool(t, user, assistant.ToolUpdateProject,
		`{"workspace_id":"`+ws+`","project":"Preserve me","status":"completed"}`); err != nil {
		t.Fatalf("update_project: %v", err)
	}

	var status, priority string
	var description, icon, leadType *string
	var leadID, storedSquadID *string
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, priority, description, icon, lead_type, lead_id::text, squad_id::text
		FROM project WHERE id = $1
	`, projectID).Scan(&status, &priority, &description, &icon, &leadType, &leadID, &storedSquadID); err != nil {
		t.Fatalf("read project back: %v", err)
	}
	if status != "completed" {
		t.Fatalf("status = %q, want completed", status)
	}
	if description == nil || *description != "do not lose me" {
		t.Fatalf("description was NULLed: %v", description)
	}
	if icon == nil || *icon != "rocket" {
		t.Fatalf("icon was NULLed: %v", icon)
	}
	if leadType == nil || *leadType != "member" || leadID == nil || *leadID != user {
		t.Fatalf("lead was NULLed: %v / %v", leadType, leadID)
	}
	if storedSquadID == nil || *storedSquadID != squadID {
		t.Fatalf("squad binding was NULLed: %v", storedSquadID)
	}
	if priority != "high" {
		t.Fatalf("priority = %q, want high", priority)
	}
}

func TestAssistantUpdateProjectRejectsEmptyChange(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-uproj2@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-uproj2-ws", "UP2")
	addAssistantTestMember(t, ws, user, "owner")
	newAssistantTestProject(t, ws, "Idle")

	assistantToolRejects(t, user, assistant.ToolUpdateProject,
		`{"workspace_id":"`+ws+`","project":"Idle"}`, "nothing to change")
	assistantToolRejects(t, user, assistant.ToolUpdateProject,
		`{"workspace_id":"`+ws+`","project":"Idle","status":"active"}`, "invalid status")
}

// ---------------------------------------------------------------------------
// archive_issue
// ---------------------------------------------------------------------------

func TestAssistantArchiveIssueIsReversible(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-arch@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-arch-ws", "ARC")
	addAssistantTestMember(t, ws, user, "owner")
	issueID := newAssistantTestIssue(t, ws, "stale ticket", user, user)

	result, err := executeAssistantTool(t, user, assistant.ToolArchiveIssue,
		`{"workspace_id":"`+ws+`","ref":"ARC-1"}`)
	if err != nil {
		t.Fatalf("archive_issue: %v", err)
	}
	if result["archived"] != true || result["issue_identifier"] != "ARC-1" {
		t.Fatalf("archive_issue = %v", result)
	}
	var archivedAt *string
	if err := testPool.QueryRow(context.Background(),
		`SELECT archived_at::text FROM issue WHERE id = $1`, issueID).Scan(&archivedAt); err != nil {
		t.Fatalf("read issue: %v", err)
	}
	if archivedAt == nil {
		t.Fatal("issue was not archived")
	}
	// The row still exists — this is the whole reason archive is a tool and
	// delete is not.
	if meta := storedIssueMetadata(t, issueID); meta["via_assistant"] != true {
		t.Fatalf("metadata = %v", meta)
	}

	if _, err := executeAssistantTool(t, user, assistant.ToolArchiveIssue,
		`{"workspace_id":"`+ws+`","ref":"ARC-1","archived":false}`); err != nil {
		t.Fatalf("un-archive: %v", err)
	}
	if err := testPool.QueryRow(context.Background(),
		`SELECT archived_at::text FROM issue WHERE id = $1`, issueID).Scan(&archivedAt); err != nil {
		t.Fatalf("read issue: %v", err)
	}
	if archivedAt != nil {
		t.Fatalf("issue was not restored: %v", *archivedAt)
	}

	assistantToolRejects(t, user, assistant.ToolArchiveIssue,
		`{"workspace_id":"`+ws+`","ref":"ARC-404"}`, "issue not found")
}

// ---------------------------------------------------------------------------
// add_issue_label / remove_issue_label / create_label / list_labels
// ---------------------------------------------------------------------------

func TestAssistantLabelTools(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-label@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-label-ws", "LBL")
	addAssistantTestMember(t, ws, user, "owner")
	issueID := newAssistantTestIssue(t, ws, "labelled issue", user, user)

	created, err := executeAssistantTool(t, user, assistant.ToolCreateLabel,
		`{"workspace_id":"`+ws+`","name":"bug"}`)
	if err != nil {
		t.Fatalf("create_label: %v", err)
	}
	if created["created"] != true {
		t.Fatalf("create_label = %v", created)
	}
	label, _ := created["label"].(map[string]any)
	if label["name"] != "bug" || label["color"] != assistantDefaultLabelColor {
		t.Fatalf("label = %v", label)
	}

	listed, err := executeAssistantTool(t, user, assistant.ToolListLabels, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("list_labels: %v", err)
	}
	if rows, _ := listed["labels"].([]any); len(rows) != 1 {
		t.Fatalf("list_labels = %v", listed)
	}

	// By NAME, not id — that is the whole point of the resolver.
	added, err := executeAssistantTool(t, user, assistant.ToolAddIssueLabel,
		`{"workspace_id":"`+ws+`","ref":"LBL-1","label":"bug"}`)
	if err != nil {
		t.Fatalf("add_issue_label: %v", err)
	}
	if added["label_added"] != true || added["label"] != "bug" {
		t.Fatalf("add_issue_label = %v", added)
	}
	var attached int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue_to_label WHERE issue_id = $1`, issueID).Scan(&attached); err != nil {
		t.Fatalf("count labels: %v", err)
	}
	if attached != 1 {
		t.Fatalf("attached = %d, want 1", attached)
	}

	// get_issue reports it, so remove_issue_label has something to aim at.
	detail, err := executeAssistantTool(t, user, assistant.ToolGetIssue,
		`{"workspace_id":"`+ws+`","ref":"LBL-1"}`)
	if err != nil {
		t.Fatalf("get_issue: %v", err)
	}
	labels, _ := detail["labels"].([]any)
	if len(labels) != 1 {
		t.Fatalf("get_issue labels = %v", detail["labels"])
	}

	removed, err := executeAssistantTool(t, user, assistant.ToolRemoveIssueLabel,
		`{"workspace_id":"`+ws+`","ref":"LBL-1","label":"bug"}`)
	if err != nil {
		t.Fatalf("remove_issue_label: %v", err)
	}
	if removed["label_removed"] != true {
		t.Fatalf("remove_issue_label = %v", removed)
	}
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue_to_label WHERE issue_id = $1`, issueID).Scan(&attached); err != nil {
		t.Fatalf("count labels: %v", err)
	}
	if attached != 0 {
		t.Fatalf("attached after remove = %d, want 0", attached)
	}

	// The label row itself survives detaching — detach is not delete.
	var labelRows int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue_label WHERE workspace_id = $1`, ws).Scan(&labelRows); err != nil {
		t.Fatalf("count label rows: %v", err)
	}
	if labelRows != 1 {
		t.Fatalf("label rows = %d, want 1", labelRows)
	}

	assistantToolRejects(t, user, assistant.ToolAddIssueLabel,
		`{"workspace_id":"`+ws+`","ref":"LBL-1","label":"nonexistent"}`, "no label called")
	assistantToolRejects(t, user, assistant.ToolCreateLabel,
		`{"workspace_id":"`+ws+`","name":"  "}`, "name is required")
	// Names are unique per workspace; the handler's 409 becomes the model's
	// correction rather than a silent duplicate.
	assistantToolRejects(t, user, assistant.ToolCreateLabel,
		`{"workspace_id":"`+ws+`","name":"bug"}`, "already exists")
}

// ---------------------------------------------------------------------------
// create_sprint / list_sprints / move_issue_to_sprint
// ---------------------------------------------------------------------------

func TestAssistantSprintTools(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-sprint@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-sprint-ws", "SPR")
	addAssistantTestMember(t, ws, user, "owner")
	projectID := newAssistantTestProject(t, ws, "Platform")
	issueID := newAssistantTestIssue(t, ws, "sprint work", user, user)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE issue SET project_id = $2 WHERE id = $1`, issueID, projectID); err != nil {
		t.Fatalf("bind issue to project: %v", err)
	}

	// project_id accepts a title too, because the model rarely has the UUID.
	created, err := executeAssistantTool(t, user, assistant.ToolCreateSprint,
		`{"workspace_id":"`+ws+`","project_id":"Platform","name":"Sprint 12","goal":"ship it","start_date":"2026-10-01"}`)
	if err != nil {
		t.Fatalf("create_sprint: %v", err)
	}
	sprint, _ := created["sprint"].(map[string]any)
	if sprint["name"] != "Sprint 12" || sprint["project_id"] != projectID {
		t.Fatalf("sprint = %v", sprint)
	}

	listed, err := executeAssistantTool(t, user, assistant.ToolListSprints, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("list_sprints: %v", err)
	}
	rows, _ := listed["sprints"].([]any)
	if len(rows) != 1 {
		t.Fatalf("list_sprints = %v", listed)
	}
	if row, _ := rows[0].(map[string]any); row["project_title"] != "Platform" {
		t.Fatalf("sprint row = %v", rows[0])
	}

	// By sprint NAME.
	moved, err := executeAssistantTool(t, user, assistant.ToolMoveIssueToSprint,
		`{"workspace_id":"`+ws+`","ref":"SPR-1","sprint":"Sprint 12"}`)
	if err != nil {
		t.Fatalf("move_issue_to_sprint: %v", err)
	}
	if moved["moved"] != true || moved["sprint"] != "Sprint 12" {
		t.Fatalf("move = %v", moved)
	}
	var inSprint int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue_to_sprint WHERE issue_id = $1`, issueID).Scan(&inSprint); err != nil {
		t.Fatalf("count sprint membership: %v", err)
	}
	if inSprint != 1 {
		t.Fatalf("sprint membership = %d, want 1", inSprint)
	}

	// An empty sprint takes it back out.
	removed, err := executeAssistantTool(t, user, assistant.ToolMoveIssueToSprint,
		`{"workspace_id":"`+ws+`","ref":"SPR-1","sprint":""}`)
	if err != nil {
		t.Fatalf("remove from sprint: %v", err)
	}
	if removed["removed_from_sprint"] != true {
		t.Fatalf("remove = %v", removed)
	}
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue_to_sprint WHERE issue_id = $1`, issueID).Scan(&inSprint); err != nil {
		t.Fatalf("count sprint membership: %v", err)
	}
	if inSprint != 0 {
		t.Fatalf("sprint membership after remove = %d, want 0", inSprint)
	}

	assistantToolRejects(t, user, assistant.ToolCreateSprint,
		`{"workspace_id":"`+ws+`","project_id":"Platform","name":" "}`, "name is required")
	assistantToolRejects(t, user, assistant.ToolCreateSprint,
		`{"workspace_id":"`+ws+`","project_id":"Ghost","name":"Sprint 13"}`, "no project called")
	assistantToolRejects(t, user, assistant.ToolMoveIssueToSprint,
		`{"workspace_id":"`+ws+`","ref":"SPR-1","sprint":"Sprint 99"}`, "no sprint called")
}

// ---------------------------------------------------------------------------
// update_issue — the fields the UI has that the tool did not
// ---------------------------------------------------------------------------

func TestAssistantUpdateIssueRenamesAndMovesProject(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-uissue@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-uissue-ws", "UIS")
	addAssistantTestMember(t, ws, user, "owner")
	projectID := newAssistantTestProject(t, ws, "Platform")
	issueID := newAssistantTestIssue(t, ws, "old title", user, user)

	if _, err := executeAssistantTool(t, user, assistant.ToolUpdateIssue,
		`{"workspace_id":"`+ws+`","ref":"UIS-1","title":"new title","description":"a body","project_id":"Platform"}`); err != nil {
		t.Fatalf("update_issue: %v", err)
	}
	var title string
	var description, storedProject *string
	if err := testPool.QueryRow(context.Background(),
		`SELECT title, description, project_id::text FROM issue WHERE id = $1`, issueID,
	).Scan(&title, &description, &storedProject); err != nil {
		t.Fatalf("read issue: %v", err)
	}
	if title != "new title" {
		t.Fatalf("title = %q", title)
	}
	if description == nil || *description != "a body" {
		t.Fatalf("description = %v", description)
	}
	if storedProject == nil || *storedProject != projectID {
		t.Fatalf("project_id = %v, want %s", storedProject, projectID)
	}

	// An empty string takes it back out of the project.
	if _, err := executeAssistantTool(t, user, assistant.ToolUpdateIssue,
		`{"workspace_id":"`+ws+`","ref":"UIS-1","project_id":""}`); err != nil {
		t.Fatalf("clear project: %v", err)
	}
	if err := testPool.QueryRow(context.Background(),
		`SELECT project_id::text FROM issue WHERE id = $1`, issueID).Scan(&storedProject); err != nil {
		t.Fatalf("read issue: %v", err)
	}
	if storedProject != nil {
		t.Fatalf("project_id = %v, want NULL", *storedProject)
	}

	assistantToolRejects(t, user, assistant.ToolUpdateIssue,
		`{"workspace_id":"`+ws+`","ref":"UIS-1"}`, "nothing to change")
	assistantToolRejects(t, user, assistant.ToolUpdateIssue,
		`{"workspace_id":"`+ws+`","ref":"UIS-1","project_id":"Ghost"}`, "no project called")
	assistantToolRejects(t, user, assistant.ToolUpdateIssue,
		`{"workspace_id":"`+ws+`","ref":"UIS-1","parent_issue_id":"UIS-1"}`, "own parent")
}

// ---------------------------------------------------------------------------
// list_comments / list_squads
// ---------------------------------------------------------------------------

func TestAssistantListCommentsReadsTheThread(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-comments@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-comments-ws", "CMT")
	addAssistantTestMember(t, ws, user, "owner")
	issueID := newAssistantTestIssue(t, ws, "discussed issue", user, user)
	for _, body := range []string{"first", "second"} {
		if _, err := testPool.Exec(context.Background(), `
			INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type)
			VALUES ($1, $2, 'member', $3, $4, 'comment')
		`, issueID, ws, user, body); err != nil {
			t.Fatalf("insert comment: %v", err)
		}
	}

	result, err := executeAssistantTool(t, user, assistant.ToolListComments,
		`{"workspace_id":"`+ws+`","ref":"CMT-1"}`)
	if err != nil {
		t.Fatalf("list_comments: %v", err)
	}
	rows, _ := result["comments"].([]any)
	if len(rows) != 2 {
		t.Fatalf("comments = %v", result)
	}
	// Reading order, oldest first — a thread read backwards is a different
	// conversation.
	first, _ := rows[0].(map[string]any)
	if first["body"] != "first" {
		t.Fatalf("first comment = %v", first)
	}
	if first["author"] == "" {
		t.Fatalf("comment has no author name: %v", first)
	}
}

func TestAssistantListSquads(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-squads@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-squads-ws", "SQD")
	addAssistantTestMember(t, ws, user, "owner")
	runtimeID := newAssistantTestRuntime(t, ws)
	leaderID := newAssistantTestAgent(t, ws, runtimeID, "Squad lead", "workspace", user)
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
		 VALUES ($1, 'Dev squad', 'builds things', $2, $3)`,
		ws, leaderID, user); err != nil {
		t.Fatalf("insert squad: %v", err)
	}

	result, err := executeAssistantTool(t, user, assistant.ToolListSquads, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("list_squads: %v", err)
	}
	rows, _ := result["squads"].([]any)
	if len(rows) != 1 {
		t.Fatalf("squads = %v", result)
	}
	if row, _ := rows[0].(map[string]any); row["name"] != "Dev squad" {
		t.Fatalf("squad = %v", rows[0])
	}
}

// ---------------------------------------------------------------------------
// Setup: runtimes, agents, skills
// ---------------------------------------------------------------------------

// The empty case is the one that matters: a workspace with no runtime cannot
// have an agent, and the tool has to say so in a way the model can relay
// instead of inventing a runtime id.
func TestAssistantListRuntimesExplainsTheEmptyCase(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-rt@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-rt-ws", "RTS")
	addAssistantTestMember(t, ws, user, "owner")

	empty, err := executeAssistantTool(t, user, assistant.ToolListRuntimes, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("list_runtimes: %v", err)
	}
	if rows, _ := empty["runtimes"].([]any); len(rows) != 0 {
		t.Fatalf("runtimes = %v", empty)
	}
	if note, _ := empty["note"].(string); !strings.Contains(note, "no runtime connected") {
		t.Fatalf("note = %q", note)
	}

	runtimeID := newAssistantTestRuntime(t, ws)
	listed, err := executeAssistantTool(t, user, assistant.ToolListRuntimes, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("list_runtimes: %v", err)
	}
	rows, _ := listed["runtimes"].([]any)
	if len(rows) != 1 {
		t.Fatalf("runtimes = %v", listed)
	}
	row, _ := rows[0].(map[string]any)
	if row["id"] != runtimeID || row["status"] != "online" {
		t.Fatalf("runtime = %v", row)
	}
}

func TestAssistantCreateAndUpdateAgent(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-agent@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-agent-ws", "AGT")
	addAssistantTestMember(t, ws, user, "owner")
	runtimeID := newAssistantTestRuntime(t, ws)

	created, err := executeAssistantTool(t, user, assistant.ToolCreateAgent,
		`{"workspace_id":"`+ws+`","name":"Reviewer","description":"reviews PRs","instructions":"be strict","runtime_id":"`+runtimeID+`"}`)
	if err != nil {
		t.Fatalf("create_agent: %v", err)
	}
	if created["created"] != true {
		t.Fatalf("create_agent = %v", created)
	}
	agent, _ := created["agent"].(map[string]any)
	if agent["name"] != "Reviewer" {
		t.Fatalf("agent = %v", agent)
	}

	var name, description, instructions, ownerID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT name, description, instructions, owner_id::text FROM agent WHERE workspace_id = $1`, ws,
	).Scan(&name, &description, &instructions, &ownerID); err != nil {
		t.Fatalf("agent was not created: %v", err)
	}
	if name != "Reviewer" || description != "reviews PRs" || instructions != "be strict" {
		t.Fatalf("stored agent = %s / %s / %s", name, description, instructions)
	}
	// Created as the human, never as some assistant identity.
	if ownerID != user {
		t.Fatalf("owner = %s, want %s", ownerID, user)
	}

	// Edited by NAME, and only the fields sent.
	if _, err := executeAssistantTool(t, user, assistant.ToolUpdateAgent,
		`{"workspace_id":"`+ws+`","agent_id":"Reviewer","instructions":"be kind"}`); err != nil {
		t.Fatalf("update_agent: %v", err)
	}
	if err := testPool.QueryRow(context.Background(),
		`SELECT name, description, instructions FROM agent WHERE workspace_id = $1`, ws,
	).Scan(&name, &description, &instructions); err != nil {
		t.Fatalf("read agent: %v", err)
	}
	if instructions != "be kind" {
		t.Fatalf("instructions = %q", instructions)
	}
	if name != "Reviewer" || description != "reviews PRs" {
		t.Fatalf("update_agent touched fields it was not given: %s / %s", name, description)
	}

	assistantToolRejects(t, user, assistant.ToolCreateAgent,
		`{"workspace_id":"`+ws+`","name":"Nameless"}`, "runtime_id is required")
	assistantToolRejects(t, user, assistant.ToolCreateAgent,
		`{"workspace_id":"`+ws+`","name":"  ","runtime_id":"`+runtimeID+`"}`, "name is required")
	// A runtime id that is not a runtime is the handler's own refusal, relayed.
	assistantToolRejects(t, user, assistant.ToolCreateAgent,
		`{"workspace_id":"`+ws+`","name":"Ghost","runtime_id":"`+ws+`"}`, "invalid runtime_id")
	assistantToolRejects(t, user, assistant.ToolUpdateAgent,
		`{"workspace_id":"`+ws+`","agent_id":"Reviewer"}`, "nothing to change")
	assistantToolRejects(t, user, assistant.ToolUpdateAgent,
		`{"workspace_id":"`+ws+`","agent_id":"Nobody","name":"x"}`, "no agent called")
}

func TestAssistantSkillTools(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-skill@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-skill-ws", "SKL")
	addAssistantTestMember(t, ws, user, "owner")
	runtimeID := newAssistantTestRuntime(t, ws)
	agentID := newAssistantTestAgent(t, ws, runtimeID, "Coder", "workspace", user)
	skillID := newAssistantTestSkill(t, ws, user, "code-review")

	library, err := executeAssistantTool(t, user, assistant.ToolListSkills, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("list_skills: %v", err)
	}
	rows, _ := library["skills"].([]any)
	if len(rows) != 1 {
		t.Fatalf("skills = %v", library)
	}

	// Attached by NAME on both sides.
	attached, err := executeAssistantTool(t, user, assistant.ToolAttachSkillToAgent,
		`{"workspace_id":"`+ws+`","agent_id":"Coder","skill_id":"code-review"}`)
	if err != nil {
		t.Fatalf("attach_skill_to_agent: %v", err)
	}
	if attached["attached"] != true || attached["skill"] != "code-review" {
		t.Fatalf("attach = %v", attached)
	}
	var links int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_skill WHERE agent_id = $1 AND skill_id = $2`, agentID, skillID).Scan(&links); err != nil {
		t.Fatalf("count agent skills: %v", err)
	}
	if links != 1 {
		t.Fatalf("agent_skill rows = %d, want 1", links)
	}

	// The per-agent listing is the same tool with agent_id.
	perAgent, err := executeAssistantTool(t, user, assistant.ToolListSkills,
		`{"workspace_id":"`+ws+`","agent_id":"Coder"}`)
	if err != nil {
		t.Fatalf("list_skills(agent): %v", err)
	}
	if agentRows, _ := perAgent["skills"].([]any); len(agentRows) != 1 {
		t.Fatalf("agent skills = %v", perAgent)
	}

	assistantToolRejects(t, user, assistant.ToolAttachSkillToAgent,
		`{"workspace_id":"`+ws+`","agent_id":"Coder","skill_id":"no-such-skill"}`, "no skill called")
	assistantToolRejects(t, user, assistant.ToolAttachSkillToAgent,
		`{"workspace_id":"`+ws+`","agent_id":"Nobody","skill_id":"code-review"}`, "no agent called")
}

// add_skill fetches over the network, so the happy path belongs in E2E. What is
// testable here — and what actually protects the model — is that bad input is
// rejected BEFORE any fetch, with the reason attached.
func TestAssistantAddSkillRejectsBadInputWithoutFetching(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-addskill@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-addskill-ws", "ASK")
	addAssistantTestMember(t, ws, user, "owner")

	assistantToolRejects(t, user, assistant.ToolAddSkill,
		`{"workspace_id":"`+ws+`","url":"  "}`, "url is required")
	assistantToolRejects(t, user, assistant.ToolAddSkill,
		`{"workspace_id":"`+ws+`","url":"https://github.com/acme/skill","on_conflict":"clobber"}`, "on_conflict must be one of")
	assistantToolRejects(t, user, assistant.ToolAddSkill,
		`{"workspace_id":"`+ws+`","url":"https://example.com/a/b.md"}`, "unsupported source")
}

// ---------------------------------------------------------------------------
// The boundary, from the executor's side
// ---------------------------------------------------------------------------

// The catalog is an allowlist: a name that is not in it must fail at dispatch,
// not fall through to anything. This is the executor-side twin of the
// assistant-package catalog tests.
//
// The names below used to be "everything the assistant must never do". Under
// the MAIN RULE most of them are shipped tools, so what is left is two much
// narrower groups, and both are here deliberately:
//
//   - The SECRET-BEARING endpoints (agent env, MCP config, provider keys,
//     runtime pairing). These stay out permanently — see
//     assistant.ExcludedCapabilities — and this is the runtime half of that
//     promise: even a model that has read about them in a skill cannot reach
//     one.
//   - Capabilities the product simply does not expose to a conversation at all
//     (shell, repo, billing top-ups). A model reaching for one gets a
//     correction, never a partial execution.
func TestExecutorRefusesToolsOutsideTheCatalog(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-allowlist@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-allowlist-ws", "ALW")
	addAssistantTestMember(t, ws, user, "owner")

	catalog := map[string]bool{}
	for _, spec := range assistant.ToolSpecs() {
		catalog[spec.Name] = true
	}
	for _, name := range []string{
		// Secrets — the one standing carve-out.
		"set_agent_env", "update_agent_env", "get_agent_env",
		"configure_mcp", "set_mcp_config", "set_api_key", "create_ai_account",
		"connect_runtime", "create_runtime", "rotate_webhook_token",
		// Never modelled as a conversational capability.
		"run_shell", "read_file", "write_file", "topup_billing", "delete_agent",
	} {
		if catalog[name] {
			t.Fatalf("%q is in the catalog — ExcludedCapabilities still says it is not available", name)
		}
		_, err := testHandler.Execute(context.Background(), user, "", name,
			json.RawMessage(`{"workspace_id":"`+ws+`"}`))
		if err == nil {
			t.Fatalf("executor ran %q, which is not an allowlisted tool", name)
		}
		if !strings.Contains(err.Error(), "unknown tool") {
			t.Fatalf("executor error for %q = %q, want an unknown-tool refusal", name, err.Error())
		}
	}
}

// ---------------------------------------------------------------------------
// The secrets carve-out — the only exclusion left
// ---------------------------------------------------------------------------

// Under the MAIN RULE (docs/agora-assistant-plan.md §0) the assistant does
// everything the user can do. The single standing exception is that no raw
// SECRET VALUE may travel through the conversation, because the transcript is
// persisted.
//
// The assistant package pins the SCHEMA half of that (no tool declares a
// secret-bearing parameter). This pins the EXECUTOR half, which is the one that
// actually decides what reaches the database: a model that sends `custom_env`
// or `mcp_config` anyway — because it read about them in a skill, or inferred
// them from the agent API — must have those keys dropped on the floor rather
// than forwarded to UpdateAgent.
//
// The guarantee here is structural: the tool decodes into a typed struct and
// rebuilds the request body from named fields, so an unmodelled key cannot pass
// through. This test is what stops someone "simplifying" that into a
// pass-through map.
func TestAssistantAgentToolsDropSecretFieldsTheModelInvents(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-secrets@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-secrets-ws", "SEC")
	addAssistantTestMember(t, ws, user, "owner")
	runtimeID := newAssistantTestRuntime(t, ws)
	agentID := newAssistantTestAgent(t, ws, runtimeID, "Sealed", "workspace", user)

	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent SET custom_env = '{"OPENAI_API_KEY":"sk-real-secret"}'::jsonb WHERE id = $1`,
		agentID); err != nil {
		t.Fatalf("seed custom_env: %v", err)
	}

	// Both writes carry invented secret fields alongside a legitimate change.
	// The legitimate change must land; the secrets must not.
	for _, args := range []string{
		`{"workspace_id":"` + ws + `","agent_id":"Sealed","description":"edited",` +
			`"custom_env":{"OPENAI_API_KEY":"sk-hijacked"},"mcp_config":{"servers":{}}}`,
		`{"workspace_id":"` + ws + `","agent_id":"Sealed","instructions":"be careful",` +
			`"api_key":"sk-hijacked","token":"ghp_hijacked"}`,
	} {
		if _, err := executeAssistantTool(t, user, assistant.ToolUpdateAgent, args); err != nil {
			t.Fatalf("update_agent(%s): %v", args, err)
		}
	}

	var env []byte
	var description, instructions string
	if err := testPool.QueryRow(context.Background(),
		`SELECT custom_env, description, instructions FROM agent WHERE id = $1`, agentID,
	).Scan(&env, &description, &instructions); err != nil {
		t.Fatalf("read agent: %v", err)
	}
	if !strings.Contains(string(env), "sk-real-secret") || strings.Contains(string(env), "sk-hijacked") {
		t.Fatalf("custom_env was written through the assistant: %s", env)
	}
	if description != "edited" || instructions != "be careful" {
		t.Fatalf("the legitimate half of the write was lost: %q / %q", description, instructions)
	}
}

// The refusal the user hears has to name WHERE the secret goes instead. A bare
// "I can't do that" is indistinguishable from a broken feature, and this is the
// only thing in the product the assistant ever declines — so it has to decline
// it well.
func TestAssistantExcludedCapabilityNamesItsSettingsDestination(t *testing.T) {
	if len(assistant.ExcludedCapabilities) != 1 {
		t.Fatalf("ExcludedCapabilities has %d entries — the MAIN RULE leaves exactly one, the secrets carve-out",
			len(assistant.ExcludedCapabilities))
	}
	only := assistant.ExcludedCapabilities[0]
	if !strings.Contains(strings.ToLower(only.Capability), "secret") {
		t.Fatalf("the surviving exclusion is not the secrets carve-out: %q", only.Capability)
	}
	for _, where := range []string{"Settings → AI Accounts", "Settings → MCP", "Environment tab"} {
		if !strings.Contains(only.Where, where) {
			t.Fatalf("the carve-out does not point at %q: %q", where, only.Where)
		}
	}
}
