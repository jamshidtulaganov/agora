package handler

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestBitrixProjectBindingFallsBackAfterHumanResponsible pins import ownership:
// a project squad is used when Bitrix has no resolvable responsible, but a real
// workspace member responsible wins even when the target project is squad-bound.
func TestBitrixProjectBindingFallsBackAfterHumanResponsible(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ctx := context.Background()
	portal := newBitrixRichPortal(t)
	configureBitrixEnvRich(t, portal.srv.URL)

	leaderID := createHandlerTestAgent(t, "Bitrix bound squad leader", []byte("[]"))
	var squadID, projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, leader_id, creator_id)
		VALUES ($1::uuid, $2, $3::uuid, $4::uuid)
		RETURNING id::text
	`, testWorkspaceID, fmt.Sprintf("Bitrix bound squad %d", time.Now().UnixNano()), leaderID, testUserID).Scan(&squadID); err != nil {
		t.Fatalf("create squad: %v", err)
	}
	projectTitle := fmt.Sprintf("Bitrix bound project %d", time.Now().UnixNano())
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title, status, priority, squad_id)
		VALUES ($1::uuid, $2, 'planned', 'none', $3::uuid)
		RETURNING id::text
	`, testWorkspaceID, projectTitle, squadID).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}

	const fallbackTaskID = "bx-compose-bound-squad"
	const humanTaskID = "bx-compose-human-owner"
	const bitrixUserID = "bx-compose-member-100"
	cleanupBitrixIssues(t, fallbackTaskID)
	cleanupBitrixIssues(t, humanTaskID)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1::uuid`, projectID)
		testPool.Exec(context.Background(), `DELETE FROM squad WHERE id = $1::uuid`, squadID)
	})
	setWorkspaceBitrixRouting(t, `{"bitrix_default_project":`+jsonStr(projectTitle)+`}`)
	portal.setTask(fallbackTaskID, `{"id":"`+fallbackTaskID+`","title":"Bound project fallback task","status":2,"tags":["ai"]}`)

	if err := testHandler.syncBitrixTaskWithState(ctx, fallbackTaskID, bitrixRouteConfig(), testHandler.newBitrixSyncState()); err != nil {
		t.Fatalf("sync fallback task: %v", err)
	}
	_, _, assigneeType, assigneeID, count := issueByBitrixTaskID(t, fallbackTaskID)
	if count != 1 {
		t.Fatalf("fallback issue count = %d, want 1", count)
	}
	if assigneeType != "squad" || assigneeID != squadID {
		t.Fatalf("fallback assignee = %s:%s, want bound squad:%s", assigneeType, assigneeID, squadID)
	}

	if err := testHandler.linkExternalIdentity(ctx, providerBitrix, bitrixUserID, testUserID); err != nil {
		t.Fatalf("link responsible identity: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM user_external_identity WHERE provider = $1 AND external_id = $2`,
			providerBitrix, bitrixUserID)
	})
	portal.setTask(humanTaskID, `{"id":"`+humanTaskID+`","title":"Bound project human task","status":2,"responsibleId":"`+bitrixUserID+`","tags":["ai"]}`)

	if err := testHandler.syncBitrixTaskWithState(ctx, humanTaskID, bitrixRouteConfig(), testHandler.newBitrixSyncState()); err != nil {
		t.Fatalf("sync human task: %v", err)
	}
	_, _, humanType, humanID, humanCount := issueByBitrixTaskID(t, humanTaskID)
	if humanCount != 1 {
		t.Fatalf("human issue count = %d, want 1", humanCount)
	}
	if humanType != "member" || humanID != testUserID {
		t.Fatalf("human assignee = %s:%s, want member:%s", humanType, humanID, testUserID)
	}
}

// TestBitrixComposeNamedRoutingWithSprint verifies the fix for the "tasks pile up
// flat on every sync" problem: named-project routing (title prefix / default) and
// Bitrix-sprint grouping now COMPOSE. A task routes to its product project by
// title prefix AND is linked to a sprint — hosted under that same product project
// — derived from its Bitrix sprint-group. It also guards the per-batch sprint
// cache against a (ws:group) key collision: one cross-product sprint-group
// ("Sprint 42" carrying both a CRM and a plain task) must yield TWO distinct
// sprints, one under each product project.
func TestBitrixComposeNamedRoutingWithSprint(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ctx := context.Background()
	portal := newBitrixRichPortal(t)
	configureBitrixEnvRich(t, portal.srv.URL)

	// Two product projects in the fixture workspace, and named routing that sends
	// "CRM:"-prefixed tasks to route-cs and everything else to route-main.
	mainPID := createRoutingProject(t, "route-main")
	csPID := createRoutingProject(t, "route-cs")
	setWorkspaceBitrixRouting(t, `{
		"bitrix_default_project":"route-main",
		"bitrix_project_prefixes":[{"prefix":"CRM:","project":"route-cs"}]
	}`)

	// One Bitrix sprint-group carrying tasks bound for BOTH products. Numeric id:
	// the Bitrix REST group lookup (sonet_group.get) keys on an integer id.
	const groupID = "884242"
	portal.setGroup(groupID, "Sprint 42")

	const crmTask = "bx-compose-crm"
	const mainTask = "bx-compose-main"
	cleanupBitrixIssues(t, crmTask)
	cleanupBitrixIssues(t, mainTask)
	portal.setTask(crmTask, `{"id":"`+crmTask+`","title":"CRM: fix login","status":2,"groupId":"`+groupID+`","tags":["ai"]}`)
	portal.setTask(mainTask, `{"id":"`+mainTask+`","title":"refactor dashboard","status":2,"groupId":"`+groupID+`","tags":["ai"]}`)

	// One shared sync state processes both tasks — exactly the batch condition
	// where a (ws:group) sprint-cache key would collide.
	cfg := bitrixRouteConfig()
	st := testHandler.newBitrixSyncState()
	if err := testHandler.syncBitrixTaskWithState(ctx, crmTask, cfg, st); err != nil {
		t.Fatalf("sync crm task: %v", err)
	}
	if err := testHandler.syncBitrixTaskWithState(ctx, mainTask, cfg, st); err != nil {
		t.Fatalf("sync main task: %v", err)
	}

	// CRM task → route-cs project + a "Sprint 42" sprint UNDER route-cs.
	crmIssue, ok := issueIDByBitrixTaskID(t, crmTask)
	if !ok {
		t.Fatal("crm task did not sync to exactly one issue")
	}
	if pid, _ := projectIDForIssue(t, crmIssue); pid != csPID {
		t.Errorf("crm issue project = %q, want route-cs %q", pid, csPID)
	}
	crmSprint, crmSprintProj, crmSprintName, ok := sprintForIssue(t, crmIssue)
	if !ok {
		t.Fatal("crm issue not linked to any sprint (compose failed)")
	}
	if crmSprintProj != csPID {
		t.Errorf("crm sprint hosted under %q, want route-cs %q", crmSprintProj, csPID)
	}
	if crmSprintName != "Sprint 42" {
		t.Errorf("crm sprint name = %q, want Sprint 42", crmSprintName)
	}

	// Main task → route-main project + a "Sprint 42" sprint UNDER route-main.
	mainIssue, ok := issueIDByBitrixTaskID(t, mainTask)
	if !ok {
		t.Fatal("main task did not sync to exactly one issue")
	}
	if pid, _ := projectIDForIssue(t, mainIssue); pid != mainPID {
		t.Errorf("main issue project = %q, want route-main %q", pid, mainPID)
	}
	mainSprint, mainSprintProj, _, ok := sprintForIssue(t, mainIssue)
	if !ok {
		t.Fatal("main issue not linked to any sprint (compose failed)")
	}
	if mainSprintProj != mainPID {
		t.Errorf("main sprint hosted under %q, want route-main %q", mainSprintProj, mainPID)
	}

	// The cross-product sprint-group must yield TWO distinct sprints — the
	// (ws:group) cache-key collision guard.
	if crmSprint == mainSprint {
		t.Errorf("both products share sprint %q — (ws:group) sprint-cache collision not fixed", crmSprint)
	}
}

// createRoutingProject inserts a product project in the fixture workspace and
// registers cleanup of the project plus any sprints (and their issue links)
// created under it during the test.
func createRoutingProject(t *testing.T, title string) string {
	t.Helper()
	var pid string
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO project (workspace_id, title, status, priority)
		 VALUES ($1::uuid, $2, 'planned', 'none') RETURNING id::text`,
		testWorkspaceID, title).Scan(&pid); err != nil {
		t.Fatalf("create project %q: %v", title, err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM issue_to_sprint WHERE sprint_id IN (SELECT id FROM sprint WHERE project_id = $1::uuid)`, pid)
		testPool.Exec(context.Background(), `DELETE FROM sprint WHERE project_id = $1::uuid`, pid)
		testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1::uuid`, pid)
	})
	return pid
}

// setWorkspaceBitrixRouting overwrites the fixture workspace's settings with the
// given JSON (named-routing config) and restores the previous value on cleanup.
func setWorkspaceBitrixRouting(t *testing.T, settingsJSON string) {
	t.Helper()
	var prev string
	testPool.QueryRow(context.Background(),
		`SELECT COALESCE(settings::text,'{}') FROM workspace WHERE id = $1::uuid`, testWorkspaceID).Scan(&prev)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE workspace SET settings = $2::jsonb WHERE id = $1::uuid`, testWorkspaceID, settingsJSON); err != nil {
		t.Fatalf("set workspace routing: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`UPDATE workspace SET settings = $2::jsonb WHERE id = $1::uuid`, testWorkspaceID, prev)
	})
}

// sprintForIssue reads the sprint an issue is linked to via issue_to_sprint,
// returning the sprint id, its host project id, and its name.
func sprintForIssue(t *testing.T, issueID string) (sprintID, projectID, name string, ok bool) {
	t.Helper()
	err := testPool.QueryRow(context.Background(),
		`SELECT s.id::text, s.project_id::text, s.name
		   FROM issue_to_sprint its JOIN sprint s ON s.id = its.sprint_id
		  WHERE its.issue_id = $1::uuid`, issueID).Scan(&sprintID, &projectID, &name)
	if err != nil {
		return "", "", "", false
	}
	return sprintID, projectID, name, true
}
