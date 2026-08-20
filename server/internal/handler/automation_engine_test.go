package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// seedAutomation inserts a rule and returns it. Conditions/actions are marshalled
// here so the tests read as flows, not as JSON strings.
func seedAutomation(
	t *testing.T, ctx context.Context, name, trigger string,
	conditions []automationCondition, actions []automationAction,
	projectID pgtype.UUID, triggerConfig string,
) db.Automation {
	t.Helper()
	conds, err := json.Marshal(conditions)
	if err != nil {
		t.Fatalf("marshal conditions: %v", err)
	}
	acts, err := json.Marshal(actions)
	if err != nil {
		t.Fatalf("marshal actions: %v", err)
	}
	if triggerConfig == "" {
		triggerConfig = `{}`
	}
	row, err := testHandler.Queries.CreateAutomation(ctx, db.CreateAutomationParams{
		WorkspaceID:   testUUID(testWorkspaceID),
		ProjectID:     projectID,
		Name:          name,
		Description:   "",
		Enabled:       true,
		TriggerType:   trigger,
		TriggerConfig: []byte(triggerConfig),
		Conditions:    conds,
		Actions:       acts,
		RecipeKey:     "",
		CreatedByType: "member",
		CreatedByID:   testUUID(testUserID),
	})
	if err != nil {
		t.Fatalf("create automation: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM automation WHERE id = $1`, row.ID)
	})
	return row
}

func automationRunsFor(t *testing.T, ctx context.Context, rule db.Automation) []db.AutomationRun {
	t.Helper()
	runs, err := testHandler.Queries.ListAutomationRuns(ctx, db.ListAutomationRunsParams{
		AutomationID: rule.ID, WorkspaceID: rule.WorkspaceID, Limit: 20,
	})
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	return runs
}

// TestAutomationAppliesActions is the happy path: the conditions hold, so both
// steps run and the audit row says so.
func TestAutomationAppliesActions(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}

	rule := seedAutomation(t, ctx, "returned work goes to todo", automationTriggerLabelAttached,
		[]automationCondition{{Field: "label", Op: automationOpEq, Value: "review:fail"}},
		[]automationAction{
			{Type: automationActionSetStatus, Config: map[string]string{"status": "todo"}},
			{Type: automationActionAddLabel, Config: map[string]string{"name": "needs-fix"}},
		}, pgtype.UUID{}, "")

	testHandler.runAutomationsForEvent(ctx, AutomationEvent{
		Trigger: automationTriggerLabelAttached, Issue: issue, Label: "review:fail",
		ActorType: "agent", ActorID: "",
	})

	reloaded, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Status != "todo" {
		t.Errorf("status = %q, want todo", reloaded.Status)
	}
	if !testHandler.issueHasLabelNameHandler(ctx, reloaded, "needs-fix") {
		t.Error("the add_label step did not attach its label")
	}
	runs := automationRunsFor(t, ctx, rule)
	if len(runs) != 1 {
		t.Fatalf("run rows = %d, want 1", len(runs))
	}
	if runs[0].Status != "applied" || runs[0].ActionsApplied != 2 {
		t.Errorf("run = %s with %d actions, want applied with 2", runs[0].Status, runs[0].ActionsApplied)
	}
	fired, err := testHandler.Queries.GetAutomation(ctx, db.GetAutomationParams{ID: rule.ID, WorkspaceID: rule.WorkspaceID})
	if err != nil {
		t.Fatalf("reload rule: %v", err)
	}
	if fired.RunCount != 1 || !fired.LastRunAt.Valid {
		t.Errorf("counters not bumped: run_count=%d last_run_at valid=%v", fired.RunCount, fired.LastRunAt.Valid)
	}
}

func TestAutomationPartialProgressStillReportsFailure(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	rule := seedAutomation(t, ctx, "show partial failures", automationTriggerLabelAttached, nil,
		[]automationAction{
			{Type: automationActionAddLabel, Config: map[string]string{"name": "first-step-worked"}},
			{Type: "unknown_step", Config: map[string]string{}},
		}, pgtype.UUID{}, "")

	testHandler.runAutomationsForEvent(ctx, AutomationEvent{
		Trigger: automationTriggerLabelAttached, Issue: issue, Label: "x", ActorType: "member",
	})
	runs := automationRunsFor(t, ctx, rule)
	if len(runs) != 1 || runs[0].Status != "failed" || runs[0].ActionsApplied != 1 {
		t.Fatalf("run = %+v, want failed with one successful step", runs)
	}
}

// TestAutomationRecordsWhyItSkipped: a rule whose conditions fail must leave an
// explanation. "My automation does nothing" is the question the run list answers.
func TestAutomationRecordsWhyItSkipped(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	rule := seedAutomation(t, ctx, "only on review:fail", automationTriggerLabelAttached,
		[]automationCondition{{Field: "label", Op: automationOpEq, Value: "review:fail"}},
		[]automationAction{{Type: automationActionSetStatus, Config: map[string]string{"status": "todo"}}},
		pgtype.UUID{}, "")

	testHandler.runAutomationsForEvent(ctx, AutomationEvent{
		Trigger: automationTriggerLabelAttached, Issue: issue, Label: "qa:pass", ActorType: "agent",
	})

	runs := automationRunsFor(t, ctx, rule)
	if len(runs) != 1 || runs[0].Status != "skipped" {
		t.Fatalf("runs = %+v, want one skipped row", runs)
	}
	var detail struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(runs[0].Detail, &detail); err != nil {
		t.Fatalf("detail is not JSON: %v", err)
	}
	if detail.Reason == "" {
		t.Error("a skipped run must record a reason")
	}
	reloaded, _ := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if reloaded.Status == "todo" {
		t.Error("a skipped rule must not have changed the issue")
	}
}

// TestAutomationIgnoresItsOwnWrites is loop guard #1. Without it, a rule whose
// action changes a status would see its own change and fire forever.
func TestAutomationIgnoresItsOwnWrites(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	rule := seedAutomation(t, ctx, "status ping-pong", automationTriggerStatusChanged, nil,
		[]automationAction{{Type: automationActionSetStatus, Config: map[string]string{"status": "todo"}}},
		pgtype.UUID{}, "")

	// emitAutomationEvent (not the internal body) is where the actor guard lives.
	testHandler.emitAutomationEvent(ctx, AutomationEvent{
		Trigger: automationTriggerStatusChanged, Issue: issue,
		FromStatus: "in_review", ToStatus: "todo",
		ActorType: automationActorType, ActorID: uuidToString(rule.ID),
	})

	if runs := automationRunsFor(t, ctx, rule); len(runs) != 0 {
		t.Fatalf("an automation's own write must not re-enter the engine, got %d runs", len(runs))
	}
}

// TestAutomationCooldownGuard is loop guard #2: a second application to the SAME
// issue inside the cooldown is skipped, and the reason says so.
func TestAutomationCooldownGuard(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	rule := seedAutomation(t, ctx, "labels twice", automationTriggerLabelAttached, nil,
		[]automationAction{{Type: automationActionAddLabel, Config: map[string]string{"name": "touched"}}},
		pgtype.UUID{}, `{"min_interval_seconds": 300}`)

	ev := AutomationEvent{Trigger: automationTriggerLabelAttached, Issue: issue, Label: "x", ActorType: "member"}
	testHandler.runAutomationsForEvent(ctx, ev)
	testHandler.runAutomationsForEvent(ctx, ev)

	runs := automationRunsFor(t, ctx, rule)
	if len(runs) != 2 {
		t.Fatalf("run rows = %d, want 2 (one applied, one skipped)", len(runs))
	}
	applied, skipped := 0, 0
	for _, run := range runs {
		switch run.Status {
		case "applied":
			applied++
		case "skipped":
			skipped++
		}
	}
	if applied != 1 || skipped != 1 {
		t.Errorf("applied=%d skipped=%d, want 1 and 1 (the cooldown must block the second)", applied, skipped)
	}
}

// TestAutomationProjectScope: a project-scoped rule must not touch another
// project's issues, and must not spam the audit trail with rows for traffic it was
// never meant to see.
func TestAutomationProjectScope(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	var projectID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title) VALUES ($1::uuid, 'Automation Scope') RETURNING id::text`,
		testWorkspaceID).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1::uuid`, projectID) })

	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	rule := seedAutomation(t, ctx, "scoped to another project", automationTriggerLabelAttached, nil,
		[]automationAction{{Type: automationActionAddLabel, Config: map[string]string{"name": "scoped"}}},
		testUUID(projectID), "")

	testHandler.runAutomationsForEvent(ctx, AutomationEvent{
		Trigger: automationTriggerLabelAttached, Issue: issue, Label: "x", ActorType: "member",
	})
	if runs := automationRunsFor(t, ctx, rule); len(runs) != 0 {
		t.Errorf("a project-scoped rule must ignore other projects silently, got %d runs", len(runs))
	}
	reloaded, _ := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if testHandler.issueHasLabelNameHandler(ctx, reloaded, "scoped") {
		t.Error("a project-scoped rule applied to an issue outside its project")
	}
}

// TestAutomationFilterStepStopsFlow: the filter node is what makes a flow a flow.
// Steps before it run, steps after it do not, and the run row explains the stop.
func TestAutomationFilterStepStopsFlow(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	rule := seedAutomation(t, ctx, "filtered flow", automationTriggerLabelAttached, nil,
		[]automationAction{
			{Type: automationActionAddLabel, Config: map[string]string{"name": "before-filter"}},
			{Type: automationStepFilter, Conditions: []automationCondition{
				{Field: "labels", Op: automationOpHasLabel, Value: "never-attached"},
			}},
			{Type: automationActionAddLabel, Config: map[string]string{"name": "after-filter"}},
		}, pgtype.UUID{}, "")

	testHandler.runAutomationsForEvent(ctx, AutomationEvent{
		Trigger: automationTriggerLabelAttached, Issue: issue, Label: "x", ActorType: "member",
	})

	reloaded, _ := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if !testHandler.issueHasLabelNameHandler(ctx, reloaded, "before-filter") {
		t.Error("steps before the filter must run")
	}
	if testHandler.issueHasLabelNameHandler(ctx, reloaded, "after-filter") {
		t.Error("steps after a failing filter must NOT run")
	}
	runs := automationRunsFor(t, ctx, rule)
	if len(runs) != 1 {
		t.Fatalf("run rows = %d, want 1", len(runs))
	}
	if runs[0].ActionsApplied != 1 {
		t.Errorf("actions_applied = %d, want 1 (only the step before the filter)", runs[0].ActionsApplied)
	}
	var detail struct {
		Actions []struct {
			Type   string `json:"type"`
			Detail string `json:"detail"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(runs[0].Detail, &detail); err != nil {
		t.Fatalf("detail is not JSON: %v", err)
	}
	foundStop := false
	for _, a := range detail.Actions {
		if a.Type == automationStepFilter && a.Detail != "" && a.Detail != "passed" {
			foundStop = true
		}
	}
	if !foundStop {
		t.Errorf("the audit trail must record that the filter stopped the flow: %s", runs[0].Detail)
	}
}

// TestAutomationDisabledRuleNeverRuns: the list toggle must be authoritative.
func TestAutomationDisabledRuleNeverRuns(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	rule := seedAutomation(t, ctx, "switched off", automationTriggerLabelAttached, nil,
		[]automationAction{{Type: automationActionAddLabel, Config: map[string]string{"name": "should-not-appear"}}},
		pgtype.UUID{}, "")
	if _, err := testHandler.Queries.SetAutomationEnabled(ctx, db.SetAutomationEnabledParams{
		ID: rule.ID, WorkspaceID: rule.WorkspaceID, Enabled: false,
	}); err != nil {
		t.Fatalf("disable: %v", err)
	}

	testHandler.runAutomationsForEvent(ctx, AutomationEvent{
		Trigger: automationTriggerLabelAttached, Issue: issue, Label: "x", ActorType: "member",
	})
	if runs := automationRunsFor(t, ctx, rule); len(runs) != 0 {
		t.Errorf("a disabled rule must not run, got %d runs", len(runs))
	}
	reloaded, _ := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if testHandler.issueHasLabelNameHandler(ctx, reloaded, "should-not-appear") {
		t.Error("a disabled rule applied its action")
	}
}

// TestInstallAutomationRecipeRefusesDuplicate: a second install of the same
// recipe must 409, not stack duplicate flows — a doubled notify rule posts twice
// per event, and this exact stacking was observed live (three installs, eleven
// flows). Deleting the installed flows re-opens the door.
func TestInstallAutomationRecipeRefusesDuplicate(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	install := func() *httptest.ResponseRecorder {
		req := newRequest("POST", "/api/automations/recipes/"+automationRecipeStaleNudge+"/install?workspace_id="+testWorkspaceID,
			map[string]any{"enabled": false})
		chiCtx := chi.NewRouteContext()
		chiCtx.URLParams.Add("key", automationRecipeStaleNudge)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx))
		w := httptest.NewRecorder()
		testHandler.InstallAutomationRecipe(w, req)
		return w
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM automation WHERE workspace_id = $1::uuid AND recipe_key = $2`,
			testWorkspaceID, automationRecipeStaleNudge)
	})

	if w := install(); w.Code != http.StatusCreated {
		t.Fatalf("first install: %d %s", w.Code, w.Body.String())
	}
	if w := install(); w.Code != http.StatusConflict {
		t.Fatalf("second install must 409, got %d %s", w.Code, w.Body.String())
	}
	var n int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM automation WHERE workspace_id = $1::uuid AND recipe_key = $2`,
		testWorkspaceID, automationRecipeStaleNudge).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("installed flows = %d, want 1 (the refused install must write nothing)", n)
	}

	// Deleting the installed flow re-opens the door.
	if _, err := testPool.Exec(ctx,
		`DELETE FROM automation WHERE workspace_id = $1::uuid AND recipe_key = $2`,
		testWorkspaceID, automationRecipeStaleNudge); err != nil {
		t.Fatal(err)
	}
	if w := install(); w.Code != http.StatusCreated {
		t.Errorf("re-install after delete: %d %s", w.Code, w.Body.String())
	}
}

func TestRerunAutomationRunRetriesOnlyFailedSteps(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	rule := seedAutomation(t, ctx, "retry only the failed step", automationTriggerLabelAttached, nil,
		[]automationAction{
			{Type: automationActionAddLabel, Config: map[string]string{"name": "must-not-repeat"}},
			{Type: automationActionAddLabel, Config: map[string]string{"name": "retried-step"}},
		}, pgtype.UUID{}, "")
	source, err := testHandler.Queries.CreateAutomationRun(ctx, db.CreateAutomationRunParams{
		AutomationID: rule.ID, WorkspaceID: rule.WorkspaceID, IssueID: issue.ID,
		TriggerType: automationTriggerLabelAttached, Status: "failed", ActionsApplied: 1,
		Detail: []byte(`{"actions":[{"type":"add_label","ok":true,"detail":"done"},{"type":"add_label","ok":false,"detail":"temporary failure"}]}`),
		Error:  "temporary failure",
	})
	if err != nil {
		t.Fatalf("seed failed run: %v", err)
	}

	req := newRequest("POST", "/api/automations/"+uuidToString(rule.ID)+"/runs/"+uuidToString(source.ID)+"/rerun?workspace_id="+testWorkspaceID, nil)
	req = withURLParams(req, "id", uuidToString(rule.ID), "runId", uuidToString(source.ID))
	w := httptest.NewRecorder()
	testHandler.RerunAutomationRun(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("rerun: %d %s", w.Code, w.Body.String())
	}
	reloaded, err := testHandler.Queries.GetIssue(ctx, issue.ID)
	if err != nil {
		t.Fatalf("reload issue: %v", err)
	}
	if testHandler.issueHasLabelNameHandler(ctx, reloaded, "must-not-repeat") {
		t.Error("a step that already succeeded was executed again")
	}
	if !testHandler.issueHasLabelNameHandler(ctx, reloaded, "retried-step") {
		t.Error("the failed step was not retried")
	}
	runs := automationRunsFor(t, ctx, rule)
	if len(runs) != 2 || runs[0].Status != "applied" {
		t.Fatalf("runs = %+v, want a new applied retry row", runs)
	}
	var detail struct {
		RetryOf string `json:"retry_of"`
	}
	if err := json.Unmarshal(runs[0].Detail, &detail); err != nil {
		t.Fatalf("decode retry detail: %v", err)
	}
	if detail.RetryOf != uuidToString(source.ID) {
		t.Errorf("retry_of = %q, want %s", detail.RetryOf, uuidToString(source.ID))
	}
}

func TestAutomationExpandTemplateIncludesOwnership(t *testing.T) {
	issue := db.Issue{
		Title:    "Fix Telegram delivery",
		Status:   "in_review",
		Metadata: []byte(`{"bitrix_task_url":"https://salesdoc.bitrix24.kz/company/personal/user/0/tasks/task/view/25/","bitrix_responsible_name":"Jamshid Tulaganov"}`),
	}
	got := automationExpandTemplate(
		"{{issue}} · {{title}} · {{status}} · {{automation}} · {{assignee}} · {{actor}} · {{source_url}} · {{source_assignee}}",
		issue, "ISSUE-69", "Review passed", "Octane Principal", "Code Reviewer",
	)
	want := "ISSUE-69 · Fix Telegram delivery · in_review · Review passed · Octane Principal · Code Reviewer · " +
		"https://salesdoc.bitrix24.kz/company/personal/user/0/tasks/task/view/25/ · Jamshid Tulaganov"
	if got != want {
		t.Fatalf("expanded template = %q, want %q", got, want)
	}
}
