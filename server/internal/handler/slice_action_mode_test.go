package handler

import (
	"context"
	"strings"
	"testing"
)

func TestTaskModeInstructionFor(t *testing.T) {
	bug := taskModeInstructionFor("bug")
	for _, want := range []string{"DEBUGGING", "REPRODUCE", "ROOT CAUSE", "installed version", "failing-before / passing-after"} {
		if !strings.Contains(bug, want) {
			t.Errorf("bug mode missing %q: %s", want, bug)
		}
	}
	if strings.Contains(bug, "mode:debugging") {
		t.Error("bug instruction must not reference mode:* labels")
	}

	feat := taskModeInstructionFor("feature")
	for _, want := range []string{"PLAN THEN BUILD", "2-3 viable variants", "acceptance", "blocking question", "implement"} {
		if !strings.Contains(feat, want) {
			t.Errorf("feature mode missing %q: %s", want, feat)
		}
	}
	if strings.Contains(feat, "mode:planning") {
		t.Error("feature instruction must not reference mode:* labels")
	}

	// chore + untyped get no mode clause (only the universal verify gate applies).
	if got := taskModeInstructionFor("chore"); got != "" {
		t.Errorf("chore mode should be empty, got: %s", got)
	}
	if got := taskModeInstructionFor(""); got != "" {
		t.Errorf("untyped mode should be empty, got: %s", got)
	}
}

func TestResolveTaskType(t *testing.T) {
	cases := []struct {
		labels []string
		want   string
	}{
		{[]string{"type:bug"}, "bug"},
		{[]string{"type:feature"}, "feature"},
		{[]string{"type:question"}, "question"},
		{[]string{"type:chore"}, "chore"},
		{[]string{"type:feature", "type:bug"}, "bug"},
		{[]string{"urgent"}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := resolveTaskType(c.labels); got != c.want {
			t.Errorf("resolveTaskType(%v) = %q, want %q", c.labels, got, c.want)
		}
	}
}

func TestQuestionWorkInstructionDoesNotAssumeCode(t *testing.T) {
	got := taskModeInstructionFor("question")
	for _, want := range []string{"INVESTIGATE AND PLAN", "Do not assume", "Only modify code", "next decision"} {
		if !strings.Contains(got, want) {
			t.Errorf("question workflow missing %q: %s", want, got)
		}
	}
}

func TestPlanningStageInstructionDoesNotImplement(t *testing.T) {
	for _, taskType := range []string{"bug", "feature", "question"} {
		got := taskModeInstructionForClaim(taskType, "plan")
		if !strings.Contains(got, "Do not edit implementation code in the plan stage") {
			t.Errorf("%s planning contract may compete with dev workers: %s", taskType, got)
		}
	}
	if got := taskModeInstructionForClaim("feature", "dev"); !strings.Contains(got, "PLAN THEN BUILD") {
		t.Errorf("dev stage should receive the build contract, got: %s", got)
	}
}

func TestExplicitTaskRunModesOverrideIssueType(t *testing.T) {
	debug := taskRunModeInstructionForClaim("debug", "feature", "")
	for _, want := range []string{"RUN MODE — DEBUG", "Reproduce", "root cause", "smallest causal fix"} {
		if !strings.Contains(debug, want) {
			t.Errorf("debug override missing %q: %s", want, debug)
		}
	}
	if strings.Contains(debug, "PLAN THEN BUILD") {
		t.Fatalf("debug override leaked feature auto behavior: %s", debug)
	}

	plan := taskRunModeInstructionForClaim("plan", "bug", "")
	for _, want := range []string{"RUN MODE — PLAN", "read-only planning", "Do not edit files", "implementation-ready plan"} {
		if !strings.Contains(plan, want) {
			t.Errorf("plan override missing %q: %s", want, plan)
		}
	}

	build := taskRunModeInstructionForClaim("build", "question", "")
	for _, want := range []string{"RUN MODE — BUILD", "Implement the accepted request now", "acceptance criteria", "verification"} {
		if !strings.Contains(build, want) {
			t.Errorf("build override missing %q: %s", want, build)
		}
	}

	auto := taskRunModeInstructionForClaim("auto", "bug", "")
	if !strings.Contains(auto, "ISSUE WORKFLOW — DEBUGGING") {
		t.Fatalf("auto mode did not preserve type:bug behavior: %s", auto)
	}
}

func TestClaimNeedsIssueWorkMode(t *testing.T) {
	cases := []struct {
		name          string
		orchestration bool
		stage         string
		stepKind      string
		protocolKind  string
		want          bool
	}{
		{name: "direct assignment", want: true},
		{name: "ordinary issue comment", want: true},
		{name: "orchestrated dev", orchestration: true, stage: "dev", stepKind: "task", want: true},
		{name: "orchestrated plan", orchestration: true, stage: "plan", stepKind: "task", want: true},
		{name: "orchestrated integration", orchestration: true, stage: "dev", stepKind: "integration", want: false},
		{name: "orchestrated qa", orchestration: true, stage: "qa", stepKind: "task", want: false},
		{name: "draft already embeds workflow", protocolKind: sliceActionDraftCode, want: false},
		{name: "qa slice", protocolKind: sliceActionRunQA, want: false},
		{name: "review slice", protocolKind: sliceActionRunReview, want: false},
		{name: "future unknown protocol fails closed", protocolKind: "future_action", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := claimNeedsIssueWorkMode(tc.orchestration, tc.stage, tc.stepKind, tc.protocolKind); got != tc.want {
				t.Fatalf("claimNeedsIssueWorkMode() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSliceActionKindFromComment(t *testing.T) {
	cases := []struct {
		content string
		want    string
	}{
		{agentProtocolMarker(sliceActionDraftCode) + "[@Dev](mention://agent/id) draft", sliceActionDraftCode},
		{"  " + agentProtocolMarker(sliceActionRunQA) + "run", sliceActionRunQA},
		{"ordinary human comment", ""},
		{"<!--agent-protocol:broken", unknownAgentProtocolKind},
	}
	for _, tc := range cases {
		if got := sliceActionKindFromComment(tc.content); got != tc.want {
			t.Errorf("sliceActionKindFromComment(%q) = %q, want %q", tc.content, got, tc.want)
		}
	}
}

func TestClaimTaskInjectsTypeBugWorkflow(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	leaderID, _ := seededLeaderAgent(t)
	var runtimeID string
	if err := testPool.QueryRow(context.Background(), `SELECT runtime_id FROM agent WHERE id = $1`, leaderID).Scan(&runtimeID); err != nil {
		t.Fatalf("get leader runtime: %v", err)
	}
	squad := seedSquadForBriefing(t, leaderID, "Type Bug Workflow Claim", "")
	issueID, _ := queueSquadIssueTaskFor(t, uuidToString(squad.ID), leaderID, runtimeID, 95011)
	attachLabelToTestIssue(t, issueID, "type:bug")

	agent := claimAndDecodeAgent(t, runtimeID)
	for _, want := range []string{"ISSUE WORKFLOW — DEBUGGING", "REPRODUCE", "ROOT CAUSE", "failing-before / passing-after"} {
		if !strings.Contains(agent.Instructions, want) {
			t.Errorf("claimed type:bug task missing %q\n--- instructions ---\n%s", want, agent.Instructions)
		}
	}
}

func TestVerifyGateInstruction(t *testing.T) {
	v := verifyGateInstruction()
	for _, want := range []string{"VERIFY", "test", "build", "inspection"} {
		if !strings.Contains(v, want) {
			t.Errorf("verify gate missing %q: %s", want, v)
		}
	}
}
