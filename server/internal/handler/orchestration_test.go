package handler

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/service"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

func orchestrationTestUUID(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	id, err := parseUUIDValue(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDefaultOrchestrationStepsRouteStageCast(t *testing.T) {
	dev := "11111111-1111-1111-1111-111111111111"
	qa := "22222222-2222-2222-2222-222222222222"
	reviewer := "33333333-3333-3333-3333-333333333333"
	issue := db.Issue{
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
		AssigneeID:   orchestrationTestUUID(t, dev),
		Metadata:     []byte(`{"cast_qa_agent_id":"` + qa + `","cast_review_agent_id":"` + reviewer + `"}`),
	}

	steps := defaultOrchestrationSteps(issue, orchestrationRouting{
		OwnerType: "agent", OwnerID: issue.AssigneeID, ControllerAgent: issue.AssigneeID,
		DevelopmentAgent: issue.AssigneeID, ExecutionMode: "direct",
	}, "squad")
	if len(steps) != 5 {
		t.Fatalf("got %d steps, want 5 (single development branch needs no integration hop)", len(steps))
	}
	if steps[1].AgentID != dev || steps[2].AgentID != qa || steps[3].AgentID != reviewer {
		t.Fatalf("unexpected routing: dev=%s qa=%s review=%s", steps[1].AgentID, steps[2].AgentID, steps[3].AgentID)
	}
	for _, step := range steps {
		if step.Kind == "integration" {
			t.Fatalf("single development branch must not create a redundant integration stage: %#v", step)
		}
	}
	// UNROUTED on purpose: the scheduler parks a step only while
	// `ApprovalRequired && !AgentID`, so a release step carrying an agent_id is
	// dispatched BEFORE the human approves. The agent is bound at approval time
	// from controller_agent_id instead.
	if !steps[4].ApprovalRequired || steps[4].AgentID != "" {
		t.Fatalf("release step must stay unrouted until human approval: %#v", steps[4])
	}
	if steps[4].MaxAttempts < 2 {
		t.Fatalf("release needs both attempts post-approval, got MaxAttempts=%d", steps[4].MaxAttempts)
	}
	if len(steps[2].DependsOnKeys) != 1 || steps[2].DependsOnKeys[0] != "dev" ||
		len(steps[3].DependsOnKeys) != 1 || steps[3].DependsOnKeys[0] != "qa" {
		t.Fatalf("QA must inspect development and review must consume QA evidence: qa=%v review=%v", steps[2].DependsOnKeys, steps[3].DependsOnKeys)
	}
	if !reflect.DeepEqual(steps[4].DependsOnKeys, []string{"review"}) {
		t.Fatalf("release must wait for the completed review chain: %v", steps[4].DependsOnKeys)
	}
}

func TestDefaultOrchestrationStepsKeepControllerAndWorkerDistinct(t *testing.T) {
	worker := orchestrationTestUUID(t, "11111111-1111-1111-1111-111111111111")
	controller := orchestrationTestUUID(t, "44444444-4444-4444-4444-444444444444")
	issue := db.Issue{AssigneeType: pgtype.Text{String: "agent", Valid: true}, AssigneeID: worker, Metadata: []byte(`{}`)}
	steps := defaultOrchestrationSteps(issue, orchestrationRouting{
		OwnerType: "agent", OwnerID: worker, ControllerAgent: controller,
		DevelopmentAgent: worker, ExecutionMode: "orchestrated",
	}, "squad")
	if steps[0].AgentID != uuidToString(controller) || steps[1].AgentID != uuidToString(worker) {
		t.Fatalf("controller should plan and assigned worker should develop: plan=%s dev=%s", steps[0].AgentID, steps[1].AgentID)
	}
	if steps[2].AgentID != uuidToString(controller) || steps[3].AgentID != uuidToString(controller) {
		t.Fatalf("uncast QA/review should return to controller: qa=%s review=%s", steps[2].AgentID, steps[3].AgentID)
	}
}

func TestSoloOrchestrationUsesOneAgentWithoutArtificialBranches(t *testing.T) {
	agent := orchestrationTestUUID(t, "11111111-1111-1111-1111-111111111111")
	issue := db.Issue{AssigneeType: pgtype.Text{String: "agent", Valid: true}, AssigneeID: agent}
	steps := defaultOrchestrationSteps(issue, orchestrationRouting{
		OwnerType: "agent", OwnerID: agent, ControllerAgent: agent,
		DevelopmentAgent: agent, ExecutionMode: "direct",
	}, "solo")
	if len(steps) != 2 {
		t.Fatalf("solo plan has %d steps, want work + release", len(steps))
	}
	if steps[0].AgentID != uuidToString(agent) || steps[0].Key != "work" {
		t.Fatalf("solo work must stay with the assigned agent: %#v", steps[0])
	}
	if steps[1].Key != "release" || !steps[1].ApprovalRequired || steps[1].AgentID != "" || len(steps[1].DependsOnKeys) != 1 {
		t.Fatalf("solo release gate is malformed (it must stay unrouted until approval): %#v", steps[1])
	}
}

func TestSquadOrchestrationBuildsCapabilityAwareParallelBranches(t *testing.T) {
	leader := orchestrationTestUUID(t, "11111111-1111-4111-8111-111111111111")
	backend := orchestrationTestUUID(t, "22222222-2222-4222-8222-222222222222")
	frontend := orchestrationTestUUID(t, "33333333-3333-4333-8333-333333333333")
	qa := orchestrationTestUUID(t, "44444444-4444-4444-8444-444444444444")
	reviewer := orchestrationTestUUID(t, "55555555-5555-4555-8555-555555555555")
	squad := orchestrationTestUUID(t, "66666666-6666-4666-8666-666666666666")
	issue := db.Issue{AssigneeType: pgtype.Text{String: "squad", Valid: true}, AssigneeID: squad, Metadata: []byte(`{}`)}
	members := []orchestrationPlannerMember{
		{AgentID: leader, Name: "Lead", Role: "leader", IsLeader: true, Model: "claude-opus-4", ThinkingLevel: "high", MaxConcurrentTasks: 1},
		{AgentID: backend, Name: "API", Role: "Backend engineer", Model: "claude-sonnet-5", ThinkingLevel: "low", MaxConcurrentTasks: 1},
		{AgentID: frontend, Name: "Web", Role: "Frontend engineer", Model: "gpt-5", ThinkingLevel: "medium", MaxConcurrentTasks: 2},
		{AgentID: qa, Name: "QA", Role: "QA engineer", Model: "claude-haiku", ThinkingLevel: "", MaxConcurrentTasks: 1},
		{AgentID: reviewer, Name: "Review", Role: "Security reviewer", Model: "claude-sonnet-5", ThinkingLevel: "high", MaxConcurrentTasks: 1},
	}
	steps := defaultOrchestrationStepsWithMembers(issue, orchestrationRouting{
		OwnerType: "squad", OwnerID: squad, ControllerAgent: leader,
		DevelopmentAgent: leader, ExecutionMode: "squad",
	}, "squad", members, 3)

	if len(steps) != 7 {
		t.Fatalf("capability-aware squad plan has %d steps, want 7: %#v", len(steps), steps)
	}
	byKey := make(map[string]orchestrationStepRequest, len(steps))
	for _, step := range steps {
		byKey[step.Key] = step
	}
	if byKey["dev-backend"].AgentID != uuidToString(backend) || byKey["dev-frontend"].AgentID != uuidToString(frontend) {
		t.Fatalf("specialist branches were not routed by capability: backend=%#v frontend=%#v", byKey["dev-backend"], byKey["dev-frontend"])
	}
	if byKey["dev-backend"].Model != "claude-sonnet-5" || byKey["dev-frontend"].Model != "gpt-5" {
		t.Fatalf("specialist branches must pin each agent's model: backend=%q frontend=%q", byKey["dev-backend"].Model, byKey["dev-frontend"].Model)
	}
	if byKey["plan"].Model != "claude-opus-4" || byKey["qa"].Model != "claude-haiku" || byKey["review"].Model != "claude-sonnet-5" {
		t.Fatalf("stage agents must pin their models: plan=%q qa=%q review=%q", byKey["plan"].Model, byKey["qa"].Model, byKey["review"].Model)
	}
	if got := byKey["integrate"].DependsOnKeys; len(got) != 2 || got[0] != "dev-backend" || got[1] != "dev-frontend" {
		t.Fatalf("integration must join both independent branches, got %v", got)
	}
	if byKey["qa"].AgentID != uuidToString(qa) || byKey["review"].AgentID != uuidToString(reviewer) {
		t.Fatalf("specialist verification routing is wrong: qa=%s review=%s", byKey["qa"].AgentID, byKey["review"].AgentID)
	}
	if byKey["integrate"].AgentID != uuidToString(leader) {
		t.Fatalf("squad leader must own integration, got %s", byKey["integrate"].AgentID)
	}
	if !strings.Contains(byKey["plan"].Instructions, "non-overlapping outcome") || !strings.Contains(byKey["plan"].Instructions, "recommend the exact reroute") {
		t.Fatalf("controller plan step lacks an actionable worker handoff contract: %q", byKey["plan"].Instructions)
	}
	if !strings.Contains(byKey["plan"].Instructions, "model=claude-sonnet-5") ||
		!strings.Contains(byKey["plan"].Instructions, "thinking=medium") ||
		!strings.Contains(byKey["plan"].Instructions, "max_concurrent_tasks=2") ||
		!strings.Contains(byKey["plan"].Instructions, "Backend engineer") {
		t.Fatalf("controller plan must expose role/model/think/concurrency roster: %q", byKey["plan"].Instructions)
	}
	roster := buildSquadRosterPolicy(members)
	if len(roster) != 5 {
		t.Fatalf("roster has %d entries, want 5", len(roster))
	}
	if roster[2].Model != "gpt-5" || roster[2].ThinkingLevel != "medium" || roster[2].MaxConcurrentTasks != 2 {
		t.Fatalf("roster lost frontend multi-model contract: %#v", roster[2])
	}
	if byKey["release"].AgentID != "" || !byKey["release"].ApprovalRequired {
		t.Fatalf("release must stay unrouted until approval binds the controller: %#v", byKey["release"])
	}
}

func TestSquadOrchestrationUsesLeanPlanForCohesiveIssue(t *testing.T) {
	leader := orchestrationTestUUID(t, "11111111-1111-4111-8111-111111111111")
	backend := orchestrationTestUUID(t, "22222222-2222-4222-8222-222222222222")
	frontend := orchestrationTestUUID(t, "33333333-3333-4333-8333-333333333333")
	qa := orchestrationTestUUID(t, "44444444-4444-4444-8444-444444444444")
	squad := orchestrationTestUUID(t, "66666666-6666-4666-8666-666666666666")
	issue := db.Issue{
		Title:       "Use EFS for the cards API",
		Description: pgtype.Text{String: "Replace the existing DWH source for card automation.", Valid: true},
		Metadata:    []byte(`{}`),
	}
	members := []orchestrationPlannerMember{
		{AgentID: leader, Role: "leader", IsLeader: true},
		{AgentID: frontend, Role: "Frontend engineer"},
		{AgentID: backend, Role: "Backend engineer"},
		{AgentID: qa, Role: "QA engineer"},
	}
	steps := defaultOrchestrationStepsWithMembers(issue, orchestrationRouting{
		OwnerType: "squad", OwnerID: squad, ControllerAgent: leader, DevelopmentAgent: leader,
	}, "squad", members, 3)
	if len(steps) != 4 {
		t.Fatalf("lean squad plan has %d steps, want plan + dev + verify + release: %#v", len(steps), steps)
	}
	if got := []string{steps[0].Key, steps[1].Key, steps[2].Key, steps[3].Key}; !reflect.DeepEqual(got, []string{"plan", "dev-backend", "verify", "release"}) {
		t.Fatalf("lean plan keys = %v", got)
	}
	if steps[1].AgentID != uuidToString(backend) {
		t.Fatalf("API issue should select backend specialist, got %#v", steps[1])
	}
	if steps[2].AgentID != uuidToString(qa) || !reflect.DeepEqual(steps[3].DependsOnKeys, []string{"verify"}) {
		t.Fatalf("lean verification/release route is malformed: verify=%#v release=%#v", steps[2], steps[3])
	}
}

func TestSquadOrchestrationUsesHumanVerificationWhenAutomationIsOff(t *testing.T) {
	leader := orchestrationTestUUID(t, "11111111-1111-4111-8111-111111111111")
	worker := orchestrationTestUUID(t, "22222222-2222-4222-8222-222222222222")
	squad := orchestrationTestUUID(t, "66666666-6666-4666-8666-666666666666")
	issue := db.Issue{Title: "Fix the invoice display", Metadata: []byte(`{}`)}
	steps := defaultOrchestrationStepsWithMembersAndAutomation(issue, orchestrationRouting{
		OwnerType: "squad", OwnerID: squad, ControllerAgent: leader, DevelopmentAgent: leader,
	}, "squad", []orchestrationPlannerMember{
		{AgentID: leader, Role: "leader", IsLeader: true},
		{AgentID: worker, Role: "Implementation engineer"},
	}, 3, false, false)
	if got := len(steps); got != 4 {
		t.Fatalf("manual-verification plan has %d steps, want plan + dev + human review + release: %#v", got, steps)
	}
	manual := steps[2]
	if manual.Key != "manual-review" || manual.Stage != "review" || !manual.HumanOnly || !manual.ApprovalRequired || manual.AgentID != "" {
		t.Fatalf("manual QA/review gate is malformed: %#v", manual)
	}
	if !reflect.DeepEqual(steps[3].DependsOnKeys, []string{"manual-review"}) {
		t.Fatalf("release must wait for manual QA/review: %#v", steps[3])
	}
}

func TestSquadOrchestrationAddsManualQABeforeAutomatedReview(t *testing.T) {
	leader := orchestrationTestUUID(t, "11111111-1111-4111-8111-111111111111")
	worker := orchestrationTestUUID(t, "22222222-2222-4222-8222-222222222222")
	reviewer := orchestrationTestUUID(t, "33333333-3333-4333-8333-333333333333")
	steps := defaultOrchestrationStepsWithMembersAndAutomation(db.Issue{Title: "Small backend fix"}, orchestrationRouting{
		OwnerType: "squad", ControllerAgent: leader, DevelopmentAgent: leader,
	}, "squad", []orchestrationPlannerMember{
		{AgentID: leader, Role: "leader", IsLeader: true},
		{AgentID: worker, Role: "Backend engineer"},
		{AgentID: reviewer, Role: "Reviewer"},
	}, 3, false, true)
	byKey := map[string]orchestrationStepRequest{}
	for _, step := range steps {
		byKey[step.Key] = step
	}
	if !byKey["manual-qa"].HumanOnly || !reflect.DeepEqual(byKey["review"].DependsOnKeys, []string{"manual-qa"}) {
		t.Fatalf("automated review must wait for manual QA: %#v", byKey)
	}
}

func TestSquadOrchestrationShapeOverrideKeepsFullPlan(t *testing.T) {
	issue := db.Issue{
		Title:    "Small API fix",
		Metadata: []byte(`{"orchestration_shape":"full"}`),
	}
	if got := inferSquadPlanShape(issue); got != squadPlanShapeFull {
		t.Fatalf("explicit full shape = %q", got)
	}
	issue.Metadata = []byte(`{"orchestration_shape":"lean"}`)
	issue.Description = pgtype.Text{String: strings.Repeat("large scope ", 200), Valid: true}
	if got := inferSquadPlanShape(issue); got != squadPlanShapeLean {
		t.Fatalf("explicit lean shape = %q", got)
	}
}

func TestSquadOrchestrationKeepsDuplicateCapabilitiesAsDistinctBranches(t *testing.T) {
	leader := orchestrationTestUUID(t, "11111111-1111-4111-8111-111111111111")
	first := orchestrationTestUUID(t, "22222222-2222-4222-8222-222222222222")
	second := orchestrationTestUUID(t, "33333333-3333-4333-8333-333333333333")
	squad := orchestrationTestUUID(t, "66666666-6666-4666-8666-666666666666")
	steps := defaultOrchestrationStepsWithMembers(db.Issue{Metadata: []byte(`{}`)}, orchestrationRouting{
		OwnerType: "squad", OwnerID: squad, ControllerAgent: leader, DevelopmentAgent: leader,
	}, "squad", []orchestrationPlannerMember{
		{AgentID: first, Role: "Backend API"},
		{AgentID: second, Role: "Database backend"},
	}, 3)
	keys := map[string]bool{}
	for _, step := range steps {
		keys[step.Key] = true
	}
	if !keys["dev-backend"] || !keys["dev-backend-2"] {
		t.Fatalf("duplicate capabilities need stable unique branch keys, got %#v", keys)
	}
}

func TestSquadPlannerExcludesInstructionIncompatibleWorkerAndUsesDeveloperLead(t *testing.T) {
	leader := orchestrationTestUUID(t, "11111111-1111-4111-8111-111111111111")
	developer := orchestrationTestUUID(t, "22222222-2222-4222-8222-222222222222")
	knowledge := orchestrationTestUUID(t, "33333333-3333-4333-8333-333333333333")
	squad := orchestrationTestUUID(t, "66666666-6666-4666-8666-666666666666")
	steps := defaultOrchestrationStepsWithMembers(db.Issue{Metadata: []byte(`{}`)}, orchestrationRouting{
		OwnerType: "squad", OwnerID: squad, ControllerAgent: leader, DevelopmentAgent: leader,
	}, "squad", []orchestrationPlannerMember{
		{AgentID: leader, Name: "Dev lead", Role: "leader", Description: "Builds product features", IsLeader: true},
		{AgentID: developer, Name: "Developer", Role: "Implementation engineer"},
		{
			AgentID: knowledge, Name: "KB Synthesizer", Role: "Implementation engineer",
			Instructions: "You are the knowledge synthesizer. Never start work beyond completed-issue knowledge capture.",
		},
	}, 3)

	devAgents := map[string]bool{}
	for _, step := range steps {
		if step.Stage == "dev" && step.Kind != "integration" {
			devAgents[step.AgentID] = true
		}
	}
	if !devAgents[uuidToString(developer)] || !devAgents[uuidToString(leader)] || len(devAgents) != 2 {
		t.Fatalf("planner should use the compatible developer and dev lead, got %#v", devAgents)
	}
	if devAgents[uuidToString(knowledge)] {
		t.Fatal("instruction-incompatible knowledge agent was routed to implementation")
	}
}

func TestOrchestrationCapabilityCompatibleKeepsReroutesInsideWorkerScope(t *testing.T) {
	backend := orchestrationPlannerMember{Role: "Backend engineer"}
	frontend := orchestrationPlannerMember{Role: "Frontend engineer"}
	generalist := orchestrationPlannerMember{Role: "Full-stack generalist"}
	qa := orchestrationPlannerMember{Role: "QA engineer"}
	controller := orchestrationPlannerMember{Role: "leader", IsLeader: true}
	knowledgeOnly := orchestrationPlannerMember{
		Role: "Backend engineer", Instructions: "You are the knowledge synthesizer. Never implement product changes.",
	}

	if !orchestrationCapabilityCompatible(backend, "backend") || orchestrationCapabilityCompatible(backend, "frontend") {
		t.Fatal("backend specialist must stay on backend work")
	}
	if !orchestrationCapabilityCompatible(frontend, "implementation") || !orchestrationCapabilityCompatible(generalist, "backend") {
		t.Fatal("broad implementation work should accept specialists and generalists")
	}
	if !orchestrationCapabilityCompatible(qa, "qa") || orchestrationCapabilityCompatible(qa, "review") {
		t.Fatal("verification specialists must stay inside their verification capability")
	}
	if !orchestrationCapabilityCompatible(controller, "frontend") || !orchestrationCapabilityCompatible(controller, "release") {
		t.Fatal("the controller must remain the recovery route for every capability")
	}
	if orchestrationCapabilityCompatible(knowledgeOnly, "backend") {
		t.Fatal("durable instructions forbidding implementation must override a squad role")
	}
}

func TestModifyingDevGitEvidenceRejectsNoOpBranch(t *testing.T) {
	step := db.OrchestrationStep{Stage: "dev", StepKind: "task"}
	base := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	req := TaskCompleteRequest{
		BranchName: "agent/dev", BaseSHA: base, HeadSHA: base, MergeStatus: "clean",
		GitStates: []RepoGitStateResponse{{Repo: "app", Branch: "agent/dev", BaseSHA: base, HeadSHA: base, MergeStatus: "clean"}},
	}
	if err := modifyingDevGitEvidenceError(step, req); !strings.Contains(err, "identical") {
		t.Fatalf("no-op dev branch should be rejected, got %q", err)
	}
	req.HeadSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	req.GitStates[0].HeadSHA = req.HeadSHA
	if err := modifyingDevGitEvidenceError(step, req); err != "" {
		t.Fatalf("clean committed dev delta should pass, got %q", err)
	}
	step.Stage = "plan"
	req.HeadSHA = base
	req.GitStates[0].HeadSHA = base
	if err := modifyingDevGitEvidenceError(step, req); err != "" {
		t.Fatalf("read-only plan step may keep the base HEAD, got %q", err)
	}
}

func TestOrchestrationHandoffSuccessGatesPauseForClarification(t *testing.T) {
	waiting := "```agora-handoff\n" + `{"schema_version":1,"stage":"dev","outcome":"waiting_input","summary":"Need a decision","question":{"prompt":"Which API?","target":"human","blocking":true}}` + "\n```"
	if orchestrationHandoffRequiresSuccessGates("dev", waiting) {
		t.Fatal("development clarification must be persisted before committed-delta success gates")
	}
	blocked := "```agora-handoff\n" + `{"schema_version":1,"stage":"dev","outcome":"blocked","summary":"Missing commit","blockers":["dependency unavailable"]}` + "\n```"
	if orchestrationHandoffRequiresSuccessGates("dev", blocked) {
		t.Fatal("integration blocker must be persisted before full-integration success gates")
	}
	completed := "```agora-handoff\n" + `{"schema_version":1,"stage":"dev","outcome":"completed","summary":"Done"}` + "\n```"
	if !orchestrationHandoffRequiresSuccessGates("dev", completed) {
		t.Fatal("completed handoff must enforce success gates")
	}
	if !orchestrationHandoffRequiresSuccessGates("dev", "legacy output") {
		t.Fatal("legacy output must fail closed through success gates")
	}
}

func TestQAPassRequiresPassedVerificationEvidence(t *testing.T) {
	for name, verification := range map[string][]service.OrchestrationVerification{
		"empty":       {},
		"all skipped": {{Name: "browser smoke", Status: "skipped"}},
	} {
		t.Run(name, func(t *testing.T) {
			handoff := service.OrchestrationHandoff{
				Outcome: "completed", Verdict: "pass", Summary: "looks good", Verification: verification,
			}
			enforceOrchestrationVerificationGate("qa", &handoff)
			if handoff.Outcome != "blocked" || len(handoff.Blockers) == 0 {
				t.Fatalf("QA pass without executed evidence was accepted: %#v", handoff)
			}
		})
	}
	handoff := service.OrchestrationHandoff{
		Outcome: "completed", Verdict: "pass", Summary: "verified",
		Verification: []service.OrchestrationVerification{{Name: "unit tests", Status: "passed"}},
	}
	enforceOrchestrationVerificationGate("qa", &handoff)
	if handoff.Outcome != "completed" {
		t.Fatalf("QA pass with executed evidence was blocked: %#v", handoff)
	}
}

func TestOrchestrationTerminalStatusDoesNotCompleteCancelledRelease(t *testing.T) {
	steps := []db.OrchestrationStep{
		{Stage: "dev", Status: "completed"},
		{Stage: "release", Status: "cancelled"},
	}
	if status, terminal := orchestrationTerminalRunStatus(steps); !terminal || status != "cancelled" {
		t.Fatalf("cancelled mandatory release = %q, terminal=%v; want cancelled", status, terminal)
	}
	steps[1].Status = "completed"
	if status, terminal := orchestrationTerminalRunStatus(steps); !terminal || status != "completed" {
		t.Fatalf("completed release = %q, terminal=%v; want completed", status, terminal)
	}
}

func TestOrchestrationRunLifecycleStatusPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		steps      []db.OrchestrationStep
		idleStatus string
		wantStatus string
		terminal   bool
	}{
		{
			name: "blocker wins over question and active sibling",
			steps: []db.OrchestrationStep{
				{Status: "blocked"}, {Status: "waiting_input"}, {Status: "running"},
			},
			idleStatus: "running", wantStatus: "blocked",
		},
		{
			name: "question wins over active sibling",
			steps: []db.OrchestrationStep{
				{Status: "waiting_input"}, {Status: "queued"},
			},
			idleStatus: "running", wantStatus: "waiting_input",
		},
		{
			name: "approval waits for active sibling",
			steps: []db.OrchestrationStep{
				{Status: "waiting_approval"}, {Status: "running"},
			},
			idleStatus: "running", wantStatus: "running",
		},
		{
			name: "approval becomes visible when idle",
			steps: []db.OrchestrationStep{
				{Status: "waiting_approval"}, {Status: "completed"},
			},
			idleStatus: "running", wantStatus: "waiting_approval",
		},
		{
			name: "manual idle projection pauses",
			steps: []db.OrchestrationStep{
				{Status: "completed"}, {Status: "pending"},
			},
			idleStatus: "waiting_approval", wantStatus: "waiting_approval",
		},
		{
			name: "completed release is terminal",
			steps: []db.OrchestrationStep{
				{Stage: "dev", Status: "completed"}, {Stage: "release", Status: "completed"},
			},
			idleStatus: "running", wantStatus: "completed", terminal: true,
		},
		{
			name: "exhausted failure wins over active sibling",
			steps: []db.OrchestrationStep{
				{Status: "failed", Attempt: 2, MaxAttempts: 2}, {Status: "running", Attempt: 1, MaxAttempts: 2},
			},
			idleStatus: "running", wantStatus: "failed", terminal: true,
		},
		{
			name: "retryable failure does not hide blocker",
			steps: []db.OrchestrationStep{
				{Status: "failed", Attempt: 1, MaxAttempts: 2}, {Status: "blocked", Attempt: 1, MaxAttempts: 2},
			},
			idleStatus: "running", wantStatus: "blocked",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, terminal := orchestrationRunLifecycleStatus(tt.steps, tt.idleStatus)
			if status != tt.wantStatus || terminal != tt.terminal {
				t.Fatalf("status=%q terminal=%v, want %q/%v", status, terminal, tt.wantStatus, tt.terminal)
			}
		})
	}
}

func TestExecutionStrategyInference(t *testing.T) {
	if got := inferExecutionStrategy(orchestrationRouting{OwnerType: "member"}, false); got != "human" {
		t.Fatalf("member strategy = %q, want human", got)
	}
	if got := inferExecutionStrategy(orchestrationRouting{OwnerType: "agent", ExecutionMode: "direct"}, false); got != "solo" {
		t.Fatalf("direct agent strategy = %q, want solo", got)
	}
	// An agent that belongs to a squad still self-orchestrates when the issue
	// is assigned to that agent — membership must not force a squad run.
	if got := inferExecutionStrategy(orchestrationRouting{OwnerType: "agent", ExecutionMode: "orchestrated"}, false); got != "solo" {
		t.Fatalf("agent-in-squad strategy = %q, want solo", got)
	}
	if got := inferExecutionStrategy(orchestrationRouting{OwnerType: "squad", ExecutionMode: "squad"}, false); got != "squad" {
		t.Fatalf("squad strategy = %q, want squad", got)
	}
	if got := inferExecutionStrategy(orchestrationRouting{OwnerType: "agent", ExecutionMode: "direct"}, true); got != "custom" {
		t.Fatalf("explicit plan strategy = %q, want custom", got)
	}
}

func TestResolveExecutionStrategyPrefersAssignee(t *testing.T) {
	agent := orchestrationRouting{OwnerType: "agent", ExecutionMode: "orchestrated"}
	if got := resolveExecutionStrategy(agent, "squad", false); got != "solo" {
		t.Fatalf("agent assignee must stay solo despite project squad pin: %q", got)
	}
	squad := orchestrationRouting{OwnerType: "squad", ExecutionMode: "squad"}
	if got := resolveExecutionStrategy(squad, "solo", false); got != "squad" {
		t.Fatalf("squad assignee must stay squad despite project solo pin: %q", got)
	}
	if got := resolveExecutionStrategy(orchestrationRouting{OwnerType: "member"}, "solo", false); got != "human" {
		t.Fatalf("human-owned issue must remain human despite project pin: %q", got)
	}
	if got := resolveExecutionStrategy(orchestrationRouting{OwnerType: "unassigned"}, "automatic", false); got != "human" {
		t.Fatalf("automatic on unassigned = %q, want human", got)
	}
}

func TestApplySoloAgentRoutingKeepsAssigneeAsController(t *testing.T) {
	agent := orchestrationTestUUID(t, "11111111-1111-1111-1111-111111111111")
	leader := orchestrationTestUUID(t, "22222222-2222-2222-2222-222222222222")
	routing := applySoloAgentRouting(orchestrationRouting{
		OwnerType: "agent", OwnerID: agent, ControllerAgent: leader,
		DevelopmentAgent: agent, ExecutionMode: "orchestrated",
	})
	if routing.ControllerAgent != agent || routing.DevelopmentAgent != agent || routing.ExecutionMode != "direct" {
		t.Fatalf("solo routing must keep the assignee as controller: %#v", routing)
	}
}

func TestApplySoloAgentRoutingPreservesExplicitController(t *testing.T) {
	agent := orchestrationTestUUID(t, "11111111-1111-1111-1111-111111111111")
	controller := orchestrationTestUUID(t, "22222222-2222-2222-2222-222222222222")
	routing := applySoloAgentRouting(orchestrationRouting{
		OwnerType: "agent", OwnerID: agent, ControllerAgent: controller,
		DevelopmentAgent: agent, ExecutionMode: "orchestrated", ExplicitController: true,
	})
	if routing.ControllerAgent != controller || routing.DevelopmentAgent != agent || routing.ExecutionMode != "orchestrated" {
		t.Fatalf("explicit controller must survive solo normalization: %#v", routing)
	}
}

func TestSquadParallelBudgetCountsDistinctSameIssueAgents(t *testing.T) {
	workerA := orchestrationTestUUID(t, "11111111-1111-1111-1111-111111111111")
	workerB := orchestrationTestUUID(t, "22222222-2222-2222-2222-222222222222")
	qa := orchestrationTestUUID(t, "33333333-3333-3333-3333-333333333333")
	review := orchestrationTestUUID(t, "44444444-4444-4444-4444-444444444444")
	workers := []orchestrationPlannerMember{
		{AgentID: workerA, MaxConcurrentTasks: 1},
		{AgentID: workerB, MaxConcurrentTasks: 5},
	}
	if got := squadParallelBudget(workers, qa, review, 10); got != 2 {
		t.Fatalf("budget = %d, want two distinct agents", got)
	}
	if got := squadParallelBudget(workers[:1], qa, qa, 10); got != 1 {
		t.Fatalf("one worker and one verification agent at separate stages has width 1, got %d", got)
	}
	if got := squadParallelBudget(workers, qa, review, 1); got != 1 {
		t.Fatalf("configured cap must bound the budget, got %d", got)
	}
}

func TestProgressionPolicyCompatibility(t *testing.T) {
	issue := db.Issue{Metadata: []byte(`{"pipeline_mode":"manual"}`)}
	if got := progressionPolicyForIssue(issue, "", "", "gated"); got != "manual" {
		t.Fatalf("legacy pipeline mode = %q, want manual", got)
	}
	issue.Metadata = []byte(`{"pipeline_mode":"manual","progression_policy":"gated"}`)
	if got := progressionPolicyForIssue(issue, "", "", "manual"); got != "gated" {
		t.Fatalf("new metadata must win, got %q", got)
	}
	if got := progressionPolicyForIssue(issue, "automatic", "manual", "gated"); got != "automatic" {
		t.Fatalf("request must win, got %q", got)
	}
	issue.Metadata = []byte(`{}`)
	if got := progressionPolicyForIssue(issue, "", "", "gated"); got != "gated" {
		t.Fatalf("project default = %q, want gated", got)
	}
}

func orchestrationSchedulingStep(t *testing.T, id, agent, status string, position int32) db.OrchestrationStep {
	t.Helper()
	return db.OrchestrationStep{
		ID:       orchestrationTestUUID(t, id),
		AgentID:  orchestrationTestUUID(t, agent),
		Status:   status,
		Position: position,
	}
}

func TestSelectDispatchableOrchestrationStepsSerializesSameAgent(t *testing.T) {
	agentA := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	agentB := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	activeA := orchestrationSchedulingStep(t, "10000000-0000-4000-8000-000000000001", agentA, "running", 0)
	readyA := orchestrationSchedulingStep(t, "10000000-0000-4000-8000-000000000002", agentA, "pending", 1)
	readyB := orchestrationSchedulingStep(t, "10000000-0000-4000-8000-000000000003", agentB, "pending", 2)

	selected := selectDispatchableOrchestrationSteps(
		[]db.OrchestrationStep{activeA, readyA, readyB},
		[]db.OrchestrationStep{readyA, readyB},
		3,
	)
	if len(selected) != 1 || selected[0].ID != readyB.ID {
		t.Fatalf("busy agent branch must wait while independent agent dispatches: %#v", selected)
	}
}

func TestSelectDispatchableOrchestrationStepsFansOutIndependentAgents(t *testing.T) {
	agentA := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	agentB := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	firstA := orchestrationSchedulingStep(t, "20000000-0000-4000-8000-000000000001", agentA, "pending", 0)
	secondA := orchestrationSchedulingStep(t, "20000000-0000-4000-8000-000000000002", agentA, "pending", 1)
	readyB := orchestrationSchedulingStep(t, "20000000-0000-4000-8000-000000000003", agentB, "pending", 2)

	selected := selectDispatchableOrchestrationSteps(nil, []db.OrchestrationStep{firstA, secondA, readyB}, 3)
	if len(selected) != 2 || selected[0].ID != firstA.ID || selected[1].ID != readyB.ID {
		t.Fatalf("one branch per agent should fan out in position order: %#v", selected)
	}
}

func TestSelectDispatchableOrchestrationStepsHonorsRunCapacity(t *testing.T) {
	agentA := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	agentB := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	agentC := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	activeA := orchestrationSchedulingStep(t, "30000000-0000-4000-8000-000000000001", agentA, "queued", 0)
	readyB := orchestrationSchedulingStep(t, "30000000-0000-4000-8000-000000000002", agentB, "pending", 1)
	readyC := orchestrationSchedulingStep(t, "30000000-0000-4000-8000-000000000003", agentC, "pending", 2)

	selected := selectDispatchableOrchestrationSteps(
		[]db.OrchestrationStep{activeA, readyB, readyC},
		[]db.OrchestrationStep{readyB, readyC},
		2,
	)
	if len(selected) != 1 || selected[0].ID != readyB.ID {
		t.Fatalf("only one remaining run slot should dispatch: %#v", selected)
	}
}

func TestValidOrchestrationStageRejectsUnknown(t *testing.T) {
	for _, stage := range []string{"plan", "dev", "qa", "review", "release"} {
		if !validOrchestrationStage(stage) {
			t.Fatalf("expected %q to be valid", stage)
		}
	}
	if validOrchestrationStage("deploy") {
		t.Fatal("unexpected deploy stage acceptance")
	}
}

func TestReadOnlyVerificationMatchesExactIntegrationHeads(t *testing.T) {
	expected := []OrchestrationGitHeadResponse{
		{Repo: "api", HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{Repo: "web", HeadSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
	}
	reported := []RepoGitStateResponse{
		{Repo: "api", HeadSHA: expected[0].HeadSHA, MergeStatus: "clean"},
		{Repo: "web", HeadSHA: expected[1].HeadSHA, MergeStatus: "clean"},
	}
	if !readOnlyVerificationMatches(expected, reported, "clean") {
		t.Fatal("exact clean integration handoff should pass")
	}
	reported[1].HeadSHA = "cccccccccccccccccccccccccccccccccccccccc"
	if readOnlyVerificationMatches(expected, reported, "clean") {
		t.Fatal("moved verification HEAD should fail")
	}
	reported[1].HeadSHA = expected[1].HeadSHA
	if readOnlyVerificationMatches(expected, reported, "uncommitted") {
		t.Fatal("dirty verification worktree should fail")
	}
}

func TestOrchestrationDependencyArtifactBaseUsesExactIntegration(t *testing.T) {
	heads := []OrchestrationGitHeadResponse{{Repo: "app", HeadSHA: strings.Repeat("a", 40)}}
	integration := db.OrchestrationStep{StepKind: "integration", IntegrationStatus: "complete"}

	base, readOnly, ok := orchestrationDependencyArtifactBase(
		db.OrchestrationStep{Stage: "dev", StepKind: "task"}, integration, heads,
	)
	if !ok || readOnly || !reflect.DeepEqual(base, heads) {
		t.Fatalf("correction base = %#v readOnly=%v ok=%v; want exact writable integration", base, readOnly, ok)
	}

	base, readOnly, ok = orchestrationDependencyArtifactBase(
		db.OrchestrationStep{Stage: "qa", StepKind: "task"}, integration, heads,
	)
	if !ok || !readOnly || !reflect.DeepEqual(base, heads) {
		t.Fatalf("QA base = %#v readOnly=%v ok=%v; want exact read-only integration", base, readOnly, ok)
	}

	base, readOnly, ok = orchestrationDependencyArtifactBase(
		db.OrchestrationStep{Stage: "review", StepKind: "task"},
		db.OrchestrationStep{Stage: "dev", Status: "completed", StepKind: "task"},
		heads,
	)
	if !ok || !readOnly || !reflect.DeepEqual(base, heads) {
		t.Fatalf("sequential review base = %#v readOnly=%v ok=%v; want exact read-only development artifact", base, readOnly, ok)
	}
	if _, _, ok = orchestrationDependencyArtifactBase(
		db.OrchestrationStep{Stage: "qa", StepKind: "task"},
		db.OrchestrationStep{Stage: "dev", Status: "failed", StepKind: "task"},
		heads,
	); ok {
		t.Fatal("incomplete development must not become the QA artifact")
	}

	base, readOnly, ok = orchestrationDependencyArtifactBase(
		db.OrchestrationStep{Stage: "release", StepKind: "task"},
		db.OrchestrationStep{Stage: "review", Status: "completed", StepKind: "task"},
		heads,
	)
	if !ok || !readOnly || !reflect.DeepEqual(base, heads) {
		t.Fatalf("release base = %#v readOnly=%v ok=%v; want exact read-only reviewed artifact", base, readOnly, ok)
	}

	integration.IntegrationStatus = "conflicts"
	if _, _, ok = orchestrationDependencyArtifactBase(
		db.OrchestrationStep{Stage: "dev", StepKind: "task"}, integration, heads,
	); ok {
		t.Fatal("conflicted integration must not override the immutable run base")
	}
}

func TestPrepareOrchestrationPlanAcceptsCapabilityAwareJoin(t *testing.T) {
	steps := []orchestrationStepRequest{
		{Key: "plan", Title: "Plan", Stage: "plan"},
		{Key: "api", Title: "API", Stage: "dev", Capability: "backend", DependsOnKeys: []string{"plan"}},
		{Key: "web", Title: "Web", Stage: "dev", Capability: "frontend", DependsOnKeys: []string{"plan"}},
		{Key: "integrate", Title: "Integrate", Stage: "dev", Kind: "integration", DependsOnKeys: []string{"api", "web"}},
		{Key: "qa", Title: "QA", Stage: "qa", DependsOnKeys: []string{"integrate"}},
		{Key: "review", Title: "Review", Stage: "review", DependsOnKeys: []string{"integrate"}},
	}
	if err := prepareOrchestrationPlan(steps); err != nil {
		t.Fatalf("valid capability-aware DAG rejected: %v", err)
	}
	if steps[0].Capability != "coordination" || steps[3].Capability != "integration" || steps[4].Capability != "qa" {
		t.Fatalf("capabilities were not inferred: %#v", steps)
	}
}

func TestPrepareOrchestrationPlanRejectsParallelBranchesWithoutJoin(t *testing.T) {
	steps := []orchestrationStepRequest{
		{Key: "api", Title: "API", Stage: "dev", Capability: "backend"},
		{Key: "web", Title: "Web", Stage: "dev", Capability: "frontend"},
		{Key: "qa", Title: "QA", Stage: "qa", DependsOnKeys: []string{"api", "web"}},
	}
	if err := prepareOrchestrationPlan(steps); err == nil || !strings.Contains(err.Error(), "integration join") {
		t.Fatalf("parallel branches without integration should fail, got %v", err)
	}
}

func TestPrepareOrchestrationPlanRejectsVerificationBypassingJoin(t *testing.T) {
	steps := []orchestrationStepRequest{
		{Key: "api", Title: "API", Stage: "dev", Capability: "backend"},
		{Key: "web", Title: "Web", Stage: "dev", Capability: "frontend"},
		{Key: "integrate", Title: "Integrate", Stage: "dev", Kind: "integration", DependsOnKeys: []string{"api", "web"}},
		{Key: "qa", Title: "QA", Stage: "qa", DependsOnKeys: []string{"api"}},
	}
	if err := prepareOrchestrationPlan(steps); err == nil || !strings.Contains(err.Error(), "must depend on the integration join") {
		t.Fatalf("QA bypassing integration should fail, got %v", err)
	}
}

func TestPrepareOrchestrationPlanAllowsSequentialDevelopment(t *testing.T) {
	steps := []orchestrationStepRequest{
		{Key: "api", Title: "API", Stage: "dev", Capability: "backend"},
		{Key: "web", Title: "Web", Stage: "dev", Capability: "frontend", DependsOnKeys: []string{"api"}},
		{Key: "qa", Title: "QA", Stage: "qa", DependsOnKeys: []string{"web"}},
	}
	if err := prepareOrchestrationPlan(steps); err != nil {
		t.Fatalf("sequential development should not require an artificial join: %v", err)
	}
}

func TestPrepareOrchestrationPlanRejectsMixedIntegratedAndDirectArtifacts(t *testing.T) {
	steps := []orchestrationStepRequest{
		{Key: "api", Title: "API", Stage: "dev", Capability: "backend"},
		{Key: "web", Title: "Web", Stage: "dev", Capability: "frontend"},
		{Key: "integrate", Title: "Integrate", Stage: "dev", Kind: "integration", DependsOnKeys: []string{"api", "web"}},
		{Key: "qa", Title: "QA", Stage: "qa", DependsOnKeys: []string{"integrate", "api"}},
	}
	if err := prepareOrchestrationPlan(steps); err == nil || !strings.Contains(err.Error(), "cannot mix") {
		t.Fatalf("verification must consume one canonical artifact, got %v", err)
	}
}

func TestPrepareOrchestrationPlanRejectsForwardDependency(t *testing.T) {
	steps := []orchestrationStepRequest{
		{Key: "web", Title: "Web", Stage: "dev", DependsOnKeys: []string{"api"}},
		{Key: "api", Title: "API", Stage: "dev"},
	}
	if err := prepareOrchestrationPlan(steps); err == nil || !strings.Contains(err.Error(), "earlier step") {
		t.Fatalf("forward dependency should fail DAG validation, got %v", err)
	}
}

func TestMissingIntegrationDependencyHeadsRequiresRepoScopedEvidence(t *testing.T) {
	dependencies := []db.ListOrchestrationStepGitDependenciesRow{
		{
			StepKey:   "multi-repo-dev",
			GitStates: []byte(`[{"repo":"api","head_sha":"same-sha"},{"repo":"web","head_sha":"web-sha"}]`),
		},
	}
	missing := missingIntegrationDependencyHeads(
		dependencies,
		[]string{"same-sha", "web-sha"},
		[]OrchestrationGitHeadResponse{{Repo: "web", HeadSHA: "same-sha"}, {Repo: "web", HeadSHA: "web-sha"}},
		nil,
	)
	if !reflect.DeepEqual(missing, []string{"api@same-sha"}) {
		t.Fatalf("cross-repository SHA satisfied named dependency: %v", missing)
	}

	missing = missingIntegrationDependencyHeads(
		dependencies,
		nil,
		[]OrchestrationGitHeadResponse{{Repo: "api", HeadSHA: "same-sha"}, {Repo: "web", HeadSHA: "web-sha"}},
		nil,
	)
	if len(missing) != 0 {
		t.Fatalf("complete repo-scoped evidence was rejected: %v", missing)
	}

	legacy := []db.ListOrchestrationStepGitDependenciesRow{{
		StepKey: "legacy", HeadSha: pgtype.Text{String: "legacy-sha", Valid: true},
	}}
	if missing = missingIntegrationDependencyHeads(legacy, []string{"legacy-sha"}, nil, nil); len(missing) != 0 {
		t.Fatalf("legacy scalar evidence was rejected: %v", missing)
	}
}
