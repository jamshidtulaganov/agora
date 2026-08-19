package handler

import (
	"context"
	"encoding/json"
	"testing"

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
