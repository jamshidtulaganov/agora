package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// TestBuildSliceInstruction is a pure unit test of the single source of truth
// for slice-action wording. It runs without a database. For every supported
// kind it asserts the template asks the human to REVIEW and explicitly tells
// the agent NOT to merge, that the optional scope is appended as a "Focus on:"
// clause, and that an unknown kind renders nothing.
//
// TestDocsRepoInstruction is a pure unit test of the auto_docs target wording.
// It asserts the docs repo is named, and that a GitLab docs repo (which has no
// `gh`/pull-request flow) gets the merge-request push-option recipe while a
// GitHub docs repo does not — keyed on the DOCS repo's host, independent of the
// project's code repo.
func TestDocsRepoInstruction(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if got := docsRepoInstruction(""); got != "" {
			t.Errorf("empty docs repo must yield empty instruction, got: %s", got)
		}
		if got := docsRepoInstruction("   "); got != "" {
			t.Errorf("whitespace docs repo must yield empty instruction, got: %s", got)
		}
	})

	t.Run("github_no_mr_flow", func(t *testing.T) {
		got := docsRepoInstruction("https://github.com/org/docs.git")
		if !strings.Contains(got, "https://github.com/org/docs.git") {
			t.Errorf("must name the docs repo, got: %s", got)
		}
		if strings.Contains(got, "merge_request.create") {
			t.Errorf("GitHub docs repo must NOT get the GitLab MR flow, got: %s", got)
		}
	})

	t.Run("gitlab_gets_mr_flow", func(t *testing.T) {
		got := docsRepoInstruction("https://gitlab.sdteam.uz/j.tulaganov/sales-doctor-docs.git")
		if !strings.Contains(got, "sales-doctor-docs") {
			t.Errorf("must name the docs repo, got: %s", got)
		}
		if !strings.Contains(got, "merge_request.create") {
			t.Errorf("GitLab docs repo must get the merge-request push-option flow, got: %s", got)
		}
		if !strings.Contains(strings.ToLower(got), "gitlab") {
			t.Errorf("GitLab docs repo instruction must mention GitLab, got: %s", got)
		}
	})
}

func TestBuildSliceInstruction(t *testing.T) {
	kinds := []string{
		sliceActionDraftCode,
		sliceActionWriteDocs,
		sliceActionWriteTests,
		sliceActionReviewPart,
	}

	for _, kind := range kinds {
		t.Run(kind+"/no_scope", func(t *testing.T) {
			got := buildSliceInstruction(kind, "")
			if got == "" {
				t.Fatalf("buildSliceInstruction(%q, \"\") returned empty", kind)
			}
			lower := strings.ToLower(got)
			if !strings.Contains(lower, "review") {
				t.Errorf("instruction for %q must ask for review, got: %s", kind, got)
			}
			// Must explicitly tell the agent not to merge. The review_part
			// template forbids making/merging changes; the others forbid
			// merging the PR. Both contain "not" + "merge".
			if !strings.Contains(lower, "merge") {
				t.Errorf("instruction for %q must mention merge prohibition, got: %s", kind, got)
			}
			if !strings.Contains(lower, "do not") && !strings.Contains(lower, "not to merge") {
				t.Errorf("instruction for %q must contain an explicit prohibition, got: %s", kind, got)
			}
			if strings.Contains(got, "Focus on:") {
				t.Errorf("instruction for %q with empty scope must not contain a Focus clause, got: %s", kind, got)
			}
		})

		t.Run(kind+"/with_scope", func(t *testing.T) {
			got := buildSliceInstruction(kind, "the JSON parser")
			if !strings.Contains(got, "Focus on: the JSON parser") {
				t.Errorf("instruction for %q must append the scope as a Focus clause, got: %s", kind, got)
			}
		})

		t.Run(kind+"/scope_trimmed", func(t *testing.T) {
			// Whitespace-only scope is treated as no scope.
			got := buildSliceInstruction(kind, "   ")
			if strings.Contains(got, "Focus on:") {
				t.Errorf("instruction for %q with whitespace scope must not append a Focus clause, got: %s", kind, got)
			}
		})
	}

	t.Run("unknown_kind", func(t *testing.T) {
		if got := buildSliceInstruction("do_everything", "anything"); got != "" {
			t.Errorf("unknown kind must render empty instruction, got: %s", got)
		}
		if isKnownSliceActionKind("do_everything") {
			t.Error("isKnownSliceActionKind must reject an unknown kind")
		}
	})
}

// TestBuildSliceInstructionRunQA covers the run_qa kind: a GENERIC, deterministic
// QA gate (no new PR). It must run the project's checks by exit code, smoke the
// running app, set qa:pass/qa:fail, and carry the no-merge guardrail — and it
// must NOT be a PR-opening action. The banned list is a regression guard: run_qa
// must stay product-neutral (no SalesDoc "sd-qa-process" skill, no Bitrix "btx-"
// branch, no "dev test box"); those once made the gate unusable off-SD.
func TestBuildSliceInstructionRunQA(t *testing.T) {
	if !isKnownSliceActionKind(sliceActionRunQA) {
		t.Fatal("run_qa must be a known slice-action kind")
	}
	got := buildSliceInstruction(sliceActionRunQA, "")
	if got == "" {
		t.Fatal("run_qa instruction must not be empty")
	}
	lower := strings.ToLower(got)
	for _, want := range []string{"qa:pass", "qa:fail", "exit code", "deterministic", "do not"} {
		if !strings.Contains(lower, strings.ToLower(want)) {
			t.Errorf("run_qa instruction must mention %q, got: %s", want, got)
		}
	}
	// Baseline diffing (P0): the gate must judge the CHANGE, not the repo — a
	// check already red on the base branch is pre-existing and must not fail the
	// gate; only a NEW failure does. Regression guard so the recipe never reverts
	// to "qa:pass ONLY if ALL checks passed", which mis-fires on a dirty repo
	// (the SD-170 case: pre-existing build/lint/test red → no verdict at all).
	for _, want := range []string{"baseline", "pre-existing", "new failure"} {
		if !strings.Contains(lower, strings.ToLower(want)) {
			t.Errorf("run_qa instruction must encode baseline diffing (%q), got: %s", want, got)
		}
	}
	// Deterministic-first smoke (speed): the gate must decide pass/fail from
	// deterministic signals (status / console / network / DOM text), take a
	// configured smoke command's EXIT CODE as the verdict, and capture a
	// screenshot ONLY to document a failure — never vision-analyze a screenshot
	// to decide. Regression guard so the smoke step never reverts to the slow
	// per-step screenshot+vision loop (the QA-time-cost complaint).
	for _, want := range []string{"deterministic-first", "vision", "screenshot only to document", "accessibility", "exit code"} {
		if !strings.Contains(lower, strings.ToLower(want)) {
			t.Errorf("run_qa smoke must be deterministic-first / vision-last (%q), got: %s", want, got)
		}
	}
	// Shift-left model: the QA test cases are authored FROM THE PLAN by the
	// parallel gen_test_cases step (in parallel with dev), so step 4 must PREFER
	// running that PRE-AUTHORED suite and only BACKFILL what's missing — not
	// re-author cold (QA's dominant time cost). It must still enforce
	// plan-alignment (assert the acceptance criteria, not ECHO the code) and
	// surface any criterion left as a COVERAGE GAP.
	for _, want := range []string{"pre-authored", "backfill", "acceptance criteri", "coverage gap"} {
		if !strings.Contains(lower, strings.ToLower(want)) {
			t.Errorf("run_qa step 4 must prefer the pre-authored suite + backfill (%q), got: %s", want, got)
		}
	}
	// A self-referential test (passes even when the code is wrong) must be
	// rejected — the anti-circular guard survives the model change.
	if !strings.Contains(lower, "echoes the implementation") {
		t.Errorf("run_qa step 4 must reject cases that echo the implementation; got: %s", got)
	}
	// Regression guard: the recipe must no longer derive tests FROM THE DIFF (the
	// circular pre-Feature-2 wording).
	if strings.Contains(lower, "from this issue's diff") {
		t.Errorf("run_qa step 4 must not derive tests from the diff (circular); got: %s", got)
	}
	for _, banned := range []string{"sd-qa-process", "btx-", "dev test box"} {
		if strings.Contains(lower, strings.ToLower(banned)) {
			t.Errorf("run_qa instruction must stay product-neutral; found %q, got: %s", banned, got)
		}
	}
	if sliceActionOpensPR(sliceActionRunQA) {
		t.Error("run_qa opens no PR; must be excluded from the branch-hint set")
	}

	// Default (empty) scope must NOT append any sprint-branch baseline guidance —
	// the original merge-base wording is the backward-compatible path. The phrases
	// below only appear when scope is task/regression.
	defaultLower := strings.ToLower(buildSliceInstruction(sliceActionRunQA, ""))
	for _, mustNotAppear := range []string{"last-green", "sprint-root", "scope=task", "scope=regression"} {
		if strings.Contains(defaultLower, strings.ToLower(mustNotAppear)) {
			t.Errorf("default-scope run_qa must NOT carry sprint-branch guidance %q, got: %s", mustNotAppear, defaultLower)
		}
	}

	// scope=task → the MOVING last-green ref baseline (per-task attribution on a
	// shared sprint branch) plus the git update-ref advance after a green run.
	taskScope := strings.ToLower(buildSliceInstruction(sliceActionRunQA, "task"))
	for _, want := range []string{"last-green", "update-ref", "scope=task"} {
		if !strings.Contains(taskScope, strings.ToLower(want)) {
			t.Errorf("run_qa scope=task must encode the last-green baseline (%q), got: %s", want, taskScope)
		}
	}

	// scope=regression → the FIXED sprint-root merge-base baseline (whole-branch,
	// cross-task drift) used by the daily backstop + sprint-end regression.
	regScope := strings.ToLower(buildSliceInstruction(sliceActionRunQA, "regression"))
	for _, want := range []string{"sprint-root", "merge-base", "scope=regression"} {
		if !strings.Contains(regScope, strings.ToLower(want)) {
			t.Errorf("run_qa scope=regression must encode the sprint-root baseline (%q), got: %s", want, regScope)
		}
	}

	// Product neutrality must survive the new guidance: no product/box/branch-prefix
	// names leak into either scope's baseline text.
	for _, banned := range []string{"sd-qa-process", "btx-", "dev test box", "salesdoc", "bitrix"} {
		if strings.Contains(taskScope, strings.ToLower(banned)) || strings.Contains(regScope, strings.ToLower(banned)) {
			t.Errorf("sprint-branch baseline guidance must stay product-neutral; found %q", banned)
		}
	}
}

// TestQAPlanContext covers the pure plan-context helper appended to a run_qa
// instruction: it must render the issue's description + acceptance criteria so
// the QA agent tests INTENDED behavior, parse the criteria JSONB defensively
// (string array, object array, empty), stay silent when there is no plan, and
// be rune-safe on an oversized non-ASCII description.
func TestQAPlanContext(t *testing.T) {
	// No-plan inputs → empty (the recipe's description-fallback then applies).
	for _, raw := range [][]byte{nil, []byte(""), []byte("[]"), []byte("null"), []byte("   ")} {
		if got := qaPlanContext("", raw); got != "" {
			t.Errorf("empty plan must yield no context, got: %q", got)
		}
	}

	// Description-only → renders the Plan line, omits the criteria section.
	descOnly := qaPlanContext("Add a logout button to the header", []byte("[]"))
	if !strings.Contains(descOnly, "TASK PLAN") || !strings.Contains(descOnly, "Add a logout button") {
		t.Errorf("description-only must render the plan line, got: %q", descOnly)
	}
	if strings.Contains(descOnly, "Acceptance criteria:") {
		t.Errorf("no criteria must omit the criteria section, got: %q", descOnly)
	}

	// []string criteria → enumerated.
	strCrit := qaPlanContext("", []byte(`["totals are correct","receipt prints"]`))
	for _, want := range []string{"Acceptance criteria:", "(1) totals are correct", "(2) receipt prints"} {
		if !strings.Contains(strCrit, want) {
			t.Errorf("string criteria must enumerate (%q), got: %q", want, strCrit)
		}
	}

	// []object criteria → pull the text-ish field (text / title / …).
	objCrit := qaPlanContext("", []byte(`[{"text":"order appears in list"},{"title":"balance updates"}]`))
	for _, want := range []string{"(1) order appears in list", "(2) balance updates"} {
		if !strings.Contains(objCrit, want) {
			t.Errorf("object criteria must pull the text field (%q), got: %q", want, objCrit)
		}
	}

	// The block always carries the anti-circular directive.
	if !strings.Contains(strCrit, "never rewrite the test to match the code") {
		t.Errorf("plan context must carry the anti-circular directive, got: %q", strCrit)
	}

	// Oversized non-ASCII description → truncated with an ellipsis, rune-safe.
	long := strings.Repeat("ы", 5000) // Cyrillic, 2 bytes each → exercises the rune cut
	big := qaPlanContext(long, nil)
	if !strings.Contains(big, "…") {
		t.Errorf("oversized description must be truncated with an ellipsis")
	}
	if n := len([]rune(big)); n > 4400 {
		t.Errorf("oversized description must be capped near 4000 runes, got %d", n)
	}
}

// TestQASquadLeader covers resolving the QA squad's leader agent (the runner for
// an auto-fired in_review run_qa): a squad whose name contains "qa" resolves to
// its leader; the leader must have a runtime + not be archived.
func TestQASquadLeader(t *testing.T) {
	ctx := context.Background()
	agentID, _, _ := privateAgentTestFixture(t)

	var squadID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
		 VALUES ($1, 'QA Squad', '', $2, $2) RETURNING id`,
		testWorkspaceID, agentID).Scan(&squadID); err != nil {
		t.Fatalf("create QA squad: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM squad WHERE id=$1`, squadID) })

	leader, ok := testHandler.qaSquadLeader(ctx, testUUID(testWorkspaceID))
	if !ok || uuidToString(leader.ID) != agentID {
		t.Errorf("qaSquadLeader = (%s, %v), want (%s, true)", uuidToString(leader.ID), ok, agentID)
	}
}

// TestQASquadAgents: the roster resolver returns the QA squad's leader + agent
// members (deduped, ready-filtered) so auto-QA can fan across them.
func TestQASquadAgents(t *testing.T) {
	ctx := context.Background()
	agentID, _, _ := privateAgentTestFixture(t)

	var squadID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
		 VALUES ($1, 'QA Squad', '', $2, $2) RETURNING id`,
		testWorkspaceID, agentID).Scan(&squadID); err != nil {
		t.Fatalf("create QA squad: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM squad WHERE id=$1`, squadID) })
	// Also list the same agent as a member — exercises leader+member collection + dedup.
	testPool.Exec(ctx,
		`INSERT INTO squad_member (squad_id, member_type, member_id, role) VALUES ($1,'agent',$2,'')
		 ON CONFLICT DO NOTHING`, squadID, agentID)

	agents := testHandler.qaSquadAgents(ctx, testUUID(testWorkspaceID))
	if len(agents) != 1 || uuidToString(agents[0].ID) != agentID {
		t.Errorf("qaSquadAgents = %d agents, want exactly [%s]", len(agents), agentID)
	}
}

// TestMaybeRunQAOnInReviewDisabled: with AGORA_AUTO_QA_ENABLED unset the auto-QA
// trigger is inert (posts no comment), so the behavior is strictly opt-in.
func TestMaybeRunQAOnInReviewDisabled(t *testing.T) {
	t.Setenv("AGORA_AUTO_QA_ENABLED", "")
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	var before int
	testPool.QueryRow(ctx, `SELECT count(*) FROM comment WHERE issue_id=$1`, issue.ID).Scan(&before)
	testHandler.maybeRunQAOnInReview(ctx, issue, "member", testUserID)
	var after int
	testPool.QueryRow(ctx, `SELECT count(*) FROM comment WHERE issue_id=$1`, issue.ID).Scan(&after)
	if after != before {
		t.Errorf("disabled auto-QA must not post a comment: %d -> %d", before, after)
	}
}

// A running orchestration already owns QA/review as persisted DAG steps. The
// legacy in_review reflex must not create an unrelated run_qa task that races
// the integration gate or holds the same local_directory lock.
func TestMaybeRunQAOnInReviewSuppressedByActiveOrchestration(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv("AGORA_AUTO_QA_ENABLED", "true")
	ctx := context.Background()
	_, _, devMemberID := leadOrchestratorTestFixture(t, ctx, "orchestration-owns-pipeline")
	issueID := sliceActionTestIssue(t, "agent", devMemberID)
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	var runID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO orchestration_run (
			workspace_id, issue_id, status, mode, policy, created_by,
			execution_strategy, progression_policy, owner_type
		)
		VALUES ($1, $2, 'running', 'auto', '{}'::jsonb, $3, 'squad', 'automatic', 'unassigned')
		RETURNING id
	`, testWorkspaceID, issueID, testUserID).Scan(&runID); err != nil {
		t.Fatalf("create active orchestration: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM orchestration_run WHERE id = $1`, runID) })

	var before int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM comment WHERE issue_id = $1`, issueID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	testHandler.maybeRunQAOnInReview(ctx, issue, "member", testUserID)
	var after int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM comment WHERE issue_id = $1`, issueID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("active orchestration allowed legacy auto-QA comment: %d -> %d", before, after)
	}
}

// leadOrchestratorTestFixture creates a runtime + a QA squad (leader
// qaLeaderID) + a dev squad (leader devLeaderID, one member devMemberID) —
// the minimal orchestrator-pair setup for the leader-to-leader routing tests.
func leadOrchestratorTestFixture(t *testing.T, ctx context.Context, name string) (qaLeaderID, devLeaderID, devMemberID string) {
	t.Helper()
	runtimeID := createClaimReclaimRuntime(t, ctx, name+" runtime")

	insertAgent := func(label string) string {
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent (workspace_id, name, description, runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id)
			VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'private', 1, $4)
			RETURNING id
		`, testWorkspaceID, name+" "+label+" "+time.Now().Format(time.RFC3339Nano), runtimeID, testUserID).Scan(&id); err != nil {
			t.Fatalf("setup: create agent %s: %v", label, err)
		}
		t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, id) })
		return id
	}
	qaLeaderID = insertAgent("qa-lead")
	devLeaderID = insertAgent("dev-lead")
	devMemberID = insertAgent("dev-member")

	var qaSquadID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
		VALUES ($1, $2, '', $3, $4) RETURNING id
	`, testWorkspaceID, name+" QA Squad", qaLeaderID, testUserID).Scan(&qaSquadID); err != nil {
		t.Fatalf("setup: create QA squad: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM squad WHERE id = $1`, qaSquadID) })

	var devSquadID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
		VALUES ($1, $2, '', $3, $4) RETURNING id
	`, testWorkspaceID, name+" Dev Squad", devLeaderID, testUserID).Scan(&devSquadID); err != nil {
		t.Fatalf("setup: create dev squad: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM squad WHERE id = $1`, devSquadID) })
	if _, err := testPool.Exec(ctx, `
		INSERT INTO squad_member (squad_id, member_type, member_id, role) VALUES ($1, 'agent', $2, 'member')
	`, devSquadID, devMemberID); err != nil {
		t.Fatalf("setup: add dev member: %v", err)
	}
	return qaLeaderID, devLeaderID, devMemberID
}

// TestMaybeRunQAOnInReview_RoutesToQALeadWhenDevOrchestrated is the
// leader-to-leader regression guard: when the dev side is squad-managed
// (assigned to a squad member, or directly to a squad), auto-QA must go
// straight to the QA squad's LEADER — not a load-balanced roster pick — so
// the two orchestrators are always the ones talking to each other.
func TestMaybeRunQAOnInReview_RoutesToQALeadWhenDevOrchestrated(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	t.Setenv("AGORA_AUTO_QA_ENABLED", "true")

	qaLeaderID, _, devMemberID := leadOrchestratorTestFixture(t, ctx, "lead-routing-member")
	issueID := sliceActionTestIssue(t, "agent", devMemberID)
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}

	testHandler.maybeRunQAOnInReview(ctx, issue, "member", testUserID)

	var commentContent string
	if err := testPool.QueryRow(ctx, `
		SELECT content FROM comment WHERE issue_id = $1 ORDER BY created_at DESC LIMIT 1
	`, issueID).Scan(&commentContent); err != nil {
		t.Fatalf("load auto-QA comment: %v", err)
	}
	if !strings.Contains(commentContent, "mention://agent/"+qaLeaderID) {
		t.Errorf("auto-QA comment does not @-mention the QA lead %s (dev side is squad-managed): %q", qaLeaderID, commentContent)
	}
}

// TestMaybeRunQAOnInReview_SquadAssigneeRoutesToQALead covers the other
// devOrchestrated path: the issue is assigned directly to a squad (not one of
// its members) — must also route to the QA lead.
func TestMaybeRunQAOnInReview_SquadAssigneeRoutesToQALead(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	t.Setenv("AGORA_AUTO_QA_ENABLED", "true")

	qaLeaderID, devLeaderID, _ := leadOrchestratorTestFixture(t, ctx, "lead-routing-squad")
	var devSquadID string
	if err := testPool.QueryRow(ctx, `SELECT id FROM squad WHERE leader_id = $1`, devLeaderID).Scan(&devSquadID); err != nil {
		t.Fatalf("find dev squad: %v", err)
	}
	issueID := sliceActionTestIssue(t, "squad", devSquadID)
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}

	testHandler.maybeRunQAOnInReview(ctx, issue, "member", testUserID)

	var commentContent string
	if err := testPool.QueryRow(ctx, `
		SELECT content FROM comment WHERE issue_id = $1 ORDER BY created_at DESC LIMIT 1
	`, issueID).Scan(&commentContent); err != nil {
		t.Fatalf("load auto-QA comment: %v", err)
	}
	if !strings.Contains(commentContent, "mention://agent/"+qaLeaderID) {
		t.Errorf("auto-QA comment does not @-mention the QA lead %s (issue assigned to a squad): %q", qaLeaderID, commentContent)
	}
}

// TestMaybeRunQAOnInReview_SoloAgentKeepsLoadBalancedRoster is the regression
// guard for the OTHER direction: a plain solo-agent assignment (no squad on
// either side) must keep today's roster load-balance — the precedence reorder
// must not accidentally force EVERY auto-QA through a leader. The QA squad's
// LEADER is deliberately made busier (one running task) than a non-leader
// member (zero) — if the reorder incorrectly treated this issue as
// dev-orchestrated, the comment would mention the leader regardless of load;
// picking the genuinely-idle non-leader member proves pickLeastBusyQAAgent's
// existing logic actually ran, not just that "a QA agent" got mentioned.
func TestMaybeRunQAOnInReview_SoloAgentKeepsLoadBalancedRoster(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	t.Setenv("AGORA_AUTO_QA_ENABLED", "true")

	runtimeID := createClaimReclaimRuntime(t, ctx, "solo-roster runtime")
	insertAgent := func(label string) string {
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent (workspace_id, name, description, runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id)
			VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'private', 1, $4)
			RETURNING id
		`, testWorkspaceID, "solo-roster "+label+" "+time.Now().Format(time.RFC3339Nano), runtimeID, testUserID).Scan(&id); err != nil {
			t.Fatalf("setup: create agent %s: %v", label, err)
		}
		t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, id) })
		return id
	}
	leaderID := insertAgent("qa-leader")
	idleMemberID := insertAgent("qa-idle-member")

	var qaSquadID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
		VALUES ($1, 'Solo Roster QA Squad', '', $2, $3) RETURNING id
	`, testWorkspaceID, leaderID, testUserID).Scan(&qaSquadID); err != nil {
		t.Fatalf("create QA squad: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM squad WHERE id=$1`, qaSquadID) })
	if _, err := testPool.Exec(ctx, `
		INSERT INTO squad_member (squad_id, member_type, member_id, role) VALUES ($1, 'agent', $2, 'member')
	`, qaSquadID, idleMemberID); err != nil {
		t.Fatalf("add idle member: %v", err)
	}

	// Bias the leader busier than the idle member: one running task for the
	// leader, none for the member.
	busyIssueID := sliceActionTestIssue(t, "", "")
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, dispatched_at, started_at)
		VALUES ($1, $2, $3, 'running', 0, now(), now())
	`, leaderID, runtimeID, busyIssueID); err != nil {
		t.Fatalf("bias leader running task: %v", err)
	}

	// A plain solo dev agent — no squad membership anywhere.
	solo, _, _ := privateAgentTestFixture(t)
	issueID := sliceActionTestIssue(t, "agent", solo)
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}

	testHandler.maybeRunQAOnInReview(ctx, issue, "member", testUserID)

	var commentContent string
	if err := testPool.QueryRow(ctx, `
		SELECT content FROM comment WHERE issue_id = $1 ORDER BY created_at DESC LIMIT 1
	`, issueID).Scan(&commentContent); err != nil {
		t.Fatalf("load auto-QA comment: %v", err)
	}
	if !strings.Contains(commentContent, "mention://agent/"+idleMemberID) {
		t.Errorf("auto-QA comment should mention the idle non-leader member %s (existing load-balance), got: %q", idleMemberID, commentContent)
	}
	if strings.Contains(commentContent, "mention://agent/"+leaderID) {
		t.Errorf("auto-QA comment must NOT mention the busier leader %s when dev side is a solo agent: %q", leaderID, commentContent)
	}
}

// attachLabelDirect inserts a workspace label (idempotent by name) and attaches
// it to an issue, bypassing the API — for setting up qa:pass state in tests.
func attachLabelDirect(t *testing.T, ctx context.Context, issueID, name string) {
	t.Helper()
	var labelID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue_label (workspace_id, name, color)
		VALUES ($1, $2, '#22c55e')
		ON CONFLICT (workspace_id, lower(name)) DO UPDATE SET color = EXCLUDED.color
		RETURNING id
	`, testWorkspaceID, name).Scan(&labelID); err != nil {
		t.Fatalf("setup: upsert label %q: %v", name, err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO issue_to_label (issue_id, label_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, issueID, labelID); err != nil {
		t.Fatalf("setup: attach label %q: %v", name, err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue_to_label WHERE issue_id = $1 AND label_id = $2`, issueID, labelID)
	})
}

// TestEnforceQAGateBeforeDone is the truth table for the structural QA gate:
// a squad-orchestrated issue must be redirected from a direct →done to
// →in_review (so the QA lead runs first) unless it already carries qa:pass,
// is already in in_review, or the target isn't done — and nothing is
// redirected at all when the gate env is off or the dev side is a solo agent.
func TestEnforceQAGateBeforeDone(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	// A squad-orchestrated issue: assigned directly to a dev squad.
	_, devLeaderID, _ := leadOrchestratorTestFixture(t, ctx, "qa-gate")
	var devSquadID string
	if err := testPool.QueryRow(ctx, `SELECT id FROM squad WHERE leader_id = $1`, devLeaderID).Scan(&devSquadID); err != nil {
		t.Fatalf("find dev squad: %v", err)
	}
	squadIssueID := sliceActionTestIssue(t, "squad", devSquadID)
	squadIssue, err := testHandler.Queries.GetIssue(ctx, testUUID(squadIssueID))
	if err != nil {
		t.Fatalf("load squad issue: %v", err)
	}

	// A solo-agent issue: no squad on either side.
	solo, _, _ := privateAgentTestFixture(t)
	soloIssueID := sliceActionTestIssue(t, "agent", solo)
	soloIssue, err := testHandler.Queries.GetIssue(ctx, testUUID(soloIssueID))
	if err != nil {
		t.Fatalf("load solo issue: %v", err)
	}

	t.Run("gate off → passthrough even for squad done", func(t *testing.T) {
		t.Setenv("AGORA_QA_GATE_ENFORCED", "")
		got, redirected := testHandler.enforceQAGateBeforeDone(ctx, squadIssue, "agent", "in_progress", "done")
		if redirected || got != "done" {
			t.Errorf("gate off must passthrough, got (%q, %v)", got, redirected)
		}
	})

	t.Run("gate on + squad + direct done → redirect to in_review", func(t *testing.T) {
		t.Setenv("AGORA_QA_GATE_ENFORCED", "true")
		got, redirected := testHandler.enforceQAGateBeforeDone(ctx, squadIssue, "agent", "in_progress", "done")
		if !redirected || got != "in_review" {
			t.Errorf("expected redirect to in_review, got (%q, %v)", got, redirected)
		}
	})

	t.Run("gate on + in_review without a verdict → held (audit: in_review→done was ungated)", func(t *testing.T) {
		t.Setenv("AGORA_QA_GATE_ENFORCED", "true")
		got, redirected := testHandler.enforceQAGateBeforeDone(ctx, squadIssue, "agent", "in_review", "done")
		if !redirected || got != "in_review" {
			t.Errorf("done from in_review without qa:pass must hold, got (%q, %v)", got, redirected)
		}
	})

	t.Run("gate on + target not done → passthrough", func(t *testing.T) {
		t.Setenv("AGORA_QA_GATE_ENFORCED", "true")
		got, redirected := testHandler.enforceQAGateBeforeDone(ctx, squadIssue, "agent", "todo", "in_progress")
		if redirected || got != "in_progress" {
			t.Errorf("non-done target must passthrough, got (%q, %v)", got, redirected)
		}
	})

	t.Run("gate on + solo agent → passthrough", func(t *testing.T) {
		t.Setenv("AGORA_QA_GATE_ENFORCED", "true")
		got, redirected := testHandler.enforceQAGateBeforeDone(ctx, soloIssue, "agent", "in_progress", "done")
		if redirected || got != "done" {
			t.Errorf("solo-agent issue must passthrough, got (%q, %v)", got, redirected)
		}
	})

	t.Run("gate on + squad + qa:pass present → passthrough", func(t *testing.T) {
		t.Setenv("AGORA_QA_GATE_ENFORCED", "true")
		attachLabelDirect(t, ctx, squadIssueID, "qa:pass")
		got, redirected := testHandler.enforceQAGateBeforeDone(ctx, squadIssue, "agent", "in_progress", "done")
		if redirected || got != "done" {
			t.Errorf("qa:pass present must passthrough, got (%q, %v)", got, redirected)
		}
	})
}

// TestUpdateIssue_QAGateRedirectsSquadDoneToInReview is the end-to-end guard:
// a real PUT /api/issues/{id} that tries to move a squad-orchestrated issue
// straight to done must LAND on in_review (the QA-lead handoff) when the gate
// env is on, and must go straight to done for a solo agent. Exercises the full
// UpdateIssue handler, not just the helper.
func TestUpdateIssue_QAGateRedirectsSquadDoneToInReview(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	t.Setenv("AGORA_QA_GATE_ENFORCED", "true")
	t.Setenv("AGORA_AUTO_QA_ENABLED", "") // isolate the gate; don't fire the async QA task

	_, devLeaderID, _ := leadOrchestratorTestFixture(t, ctx, "qa-gate-e2e")
	var devSquadID string
	if err := testPool.QueryRow(ctx, `SELECT id FROM squad WHERE leader_id = $1`, devLeaderID).Scan(&devSquadID); err != nil {
		t.Fatalf("find dev squad: %v", err)
	}

	drive := func(issueID, status string) string {
		w := httptest.NewRecorder()
		req := newRequest("PUT", "/api/issues/"+issueID, map[string]any{"status": status})
		req = withURLParam(req, "id", issueID)
		testHandler.UpdateIssue(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("UpdateIssue status=%q: expected 200, got %d: %s", status, w.Code, w.Body.String())
		}
		var got string
		if err := testPool.QueryRow(ctx, `SELECT status FROM issue WHERE id = $1`, issueID).Scan(&got); err != nil {
			t.Fatalf("read back status: %v", err)
		}
		return got
	}

	// Squad-orchestrated issue: done request must be redirected to in_review.
	squadIssueID := sliceActionTestIssue(t, "squad", devSquadID)
	if got := drive(squadIssueID, "done"); got != "in_review" {
		t.Errorf("squad-orchestrated done must redirect to in_review, got %q", got)
	}
	// Second hop: from in_review WITHOUT a verdict, done is still held — the
	// audit found this edge ungated (an issue the cockpit showed as failing
	// could be closed anyway). A qa:pass is the exit.
	if got := drive(squadIssueID, "done"); got != "in_review" {
		t.Errorf("done from in_review without qa:pass must stay held, got %q", got)
	}
	attachTestLabel(t, squadIssueID, "qa:pass")
	if got := drive(squadIssueID, "done"); got != "done" {
		t.Errorf("done from in_review WITH qa:pass must pass through, got %q", got)
	}

	// Solo-agent issue: done is never gated.
	solo, _, _ := privateAgentTestFixture(t)
	soloIssueID := sliceActionTestIssue(t, "agent", solo)
	if got := drive(soloIssueID, "done"); got != "done" {
		t.Errorf("solo-agent done must pass through, got %q", got)
	}
}

// attachTestLabel ensures a label exists in the fixture workspace and attaches
// it to the issue — direct DB, mirroring what CaptureQAEvidence does.
func attachTestLabel(t *testing.T, issueID, name string) {
	t.Helper()
	ctx := context.Background()
	var labelID string
	err := testPool.QueryRow(ctx,
		`WITH ins AS (
		    INSERT INTO issue_label (workspace_id, name, color)
		    SELECT $1, $2, '#22c55e'
		    WHERE NOT EXISTS (SELECT 1 FROM issue_label WHERE workspace_id = $1 AND name = $2)
		    RETURNING id
		 )
		 SELECT id FROM ins
		 UNION ALL
		 SELECT id FROM issue_label WHERE workspace_id = $1 AND name = $2
		 LIMIT 1`, testWorkspaceID, name).Scan(&labelID)
	if err != nil {
		t.Fatalf("ensure label %s: %v", name, err)
	}
	if _, err := testPool.Exec(ctx,
		`INSERT INTO issue_to_label (issue_id, label_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		issueID, labelID); err != nil {
		t.Fatalf("attach label %s: %v", name, err)
	}
}

// TestBuildSliceInstructionRunCI covers the run_ci kind: a deterministic gate on
// an existing PR branch (no new PR). It must run the checks and report by exit
// code, set the ci:pass/ci:fail label, reference branch resolution + the no-merge
// guardrail, and must NOT be a PR-opening action.
func TestBuildSliceInstructionRunCI(t *testing.T) {
	if !isKnownSliceActionKind(sliceActionRunCI) {
		t.Fatal("run_ci must be a known slice-action kind")
	}
	got := buildSliceInstruction(sliceActionRunCI, "")
	if got == "" {
		t.Fatal("run_ci instruction must not be empty")
	}
	lower := strings.ToLower(got)
	for _, want := range []string{"ci:pass", "ci:fail", "exit", "do not"} {
		if !strings.Contains(lower, strings.ToLower(want)) {
			t.Errorf("run_ci instruction must mention %q, got: %s", want, got)
		}
	}
	if strings.Contains(lower, "btx-") {
		t.Errorf("run_ci instruction must stay product-neutral (no Bitrix btx- branch), got: %s", got)
	}
	if sliceActionOpensPR(sliceActionRunCI) {
		t.Error("run_ci opens no PR; must be excluded from the branch-hint set")
	}
}

// TestSanitizeSliceScope is a pure unit test of the scope sanitizer that defends
// against mention injection. It runs without a database. The contract: after
// sanitizing, the scope embedded as a "Focus on:" clause can NEVER be parsed by
// util.ParseMentions as a mention — so a caller cannot smuggle a second mention
// (and a second queued task) through the scope. Benign scopes pass through
// (minus the mention-forming delimiters) and stay human-readable.
func TestSanitizeSliceScope(t *testing.T) {
	otherUUID := "99999999-9999-9999-9999-999999999999"

	t.Run("injected_mention_is_neutralized", func(t *testing.T) {
		raw := "the parser [@evil](mention://agent/" + otherUUID + ")"
		got := sanitizeSliceScope(raw)
		// The sanitized scope, embedded verbatim in the comment body, must not
		// parse as any mention.
		instruction := buildSliceInstruction(sliceActionDraftCode, got)
		if ms := util.ParseMentions(instruction); len(ms) != 0 {
			t.Fatalf("sanitized scope must not embed a parsable mention; got %d mentions from instruction %q (scope %q)", len(ms), instruction, got)
		}
		// The defended substrings must be gone.
		for _, banned := range []string{"mention://", "]", "(", ")"} {
			if strings.Contains(got, banned) {
				t.Errorf("sanitized scope must not contain %q, got: %q", banned, got)
			}
		}
		// The injected target id must not survive in a form that re-parses; the
		// bare uuid text remaining as prose is harmless (no mention anchors).
		if util.MentionRe.MatchString(got) {
			t.Errorf("sanitized scope must not match MentionRe, got: %q", got)
		}
	})

	t.Run("benign_scope_preserved", func(t *testing.T) {
		got := sanitizeSliceScope("the JSON parser in auth.go")
		if got != "the JSON parser in auth.go" {
			t.Errorf("benign scope must pass through unchanged, got: %q", got)
		}
	})

	t.Run("whitespace_only_trimmed", func(t *testing.T) {
		if got := sanitizeSliceScope("   "); got != "" {
			t.Errorf("whitespace-only scope must sanitize to empty, got: %q", got)
		}
	})

	t.Run("partial_mention_tokens_stripped", func(t *testing.T) {
		// Even fragments that could combine with the template wording must be
		// neutralized: any of the anchor delimiters is removed.
		got := sanitizeSliceScope("a)(b]c mention://x")
		if util.MentionRe.MatchString(got) || strings.Contains(got, "mention://") {
			t.Errorf("partial tokens must be stripped, got: %q", got)
		}
	})
}

// TestAutoDocsEnabled pins the opt-in default: the qa:pass → auto_docs trigger
// is off unless explicitly enabled.
func TestAutoDocsEnabled(t *testing.T) {
	// A no-project issue resolves to the instance value (nil project overrides),
	// so this still pins the env-driven default without a database.
	h := &Handler{}
	noProject := db.Issue{}
	t.Setenv("AGORA_AUTO_DOCS_ENABLED", "")
	if h.autoDocsEnabled(context.Background(), noProject) {
		t.Error("auto_docs must default to OFF")
	}
	t.Setenv("AGORA_AUTO_DOCS_ENABLED", "true")
	if !h.autoDocsEnabled(context.Background(), noProject) {
		t.Error("auto_docs must be ON when AGORA_AUTO_DOCS_ENABLED=true")
	}
}

// TestMaybeAutoDocsOnLabelGating covers the safety gates: the trigger no-ops when
// disabled, and (even enabled) when the attached label is not qa:pass — so a
// label attach never queues a docs run it shouldn't.
func TestMaybeAutoDocsOnLabelGating(t *testing.T) {
	ctx := context.Background()
	countComments := func(issueID string) int {
		var n int
		testPool.QueryRow(ctx, `SELECT count(*) FROM comment WHERE issue_id = $1`, issueID).Scan(&n)
		return n
	}
	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}

	t.Run("disabled_noop_even_on_qa_pass", func(t *testing.T) {
		t.Setenv("AGORA_AUTO_DOCS_ENABLED", "")
		before := countComments(issueID)
		testHandler.maybeAutoDocsOnLabel(ctx, issue, "qa:pass", testUserID)
		if got := countComments(issueID); got != before {
			t.Errorf("disabled must not fire: comments %d → %d", before, got)
		}
	})

	t.Run("wrong_label_noop_when_enabled", func(t *testing.T) {
		t.Setenv("AGORA_AUTO_DOCS_ENABLED", "true")
		before := countComments(issueID)
		testHandler.maybeAutoDocsOnLabel(ctx, issue, "type:bug", testUserID)
		if got := countComments(issueID); got != before {
			t.Errorf("non-qa:pass label must not fire: comments %d → %d", before, got)
		}
	})
}

// qaFailAutorouteFixture creates a runtime + two agents (a failing dev and a
// squad leader). withSquad puts the dev in a squad led by the leader agent —
// the minimal setup maybeRouteToDevLeadOnQAFail needs to find a lead.
func qaFailAutorouteFixture(t *testing.T, ctx context.Context, withSquad bool) (devAgentID, leaderAgentID string) {
	t.Helper()
	runtimeID := createClaimReclaimRuntime(t, ctx, "qa-fail-autoroute runtime")

	insertAgent := func(name string) string {
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent (workspace_id, name, description, runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id)
			VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'private', 1, $4)
			RETURNING id
		`, testWorkspaceID, name, runtimeID, testUserID).Scan(&id); err != nil {
			t.Fatalf("setup: create agent %s: %v", name, err)
		}
		t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, id) })
		return id
	}
	devAgentID = insertAgent("qa-fail-autoroute dev " + time.Now().Format(time.RFC3339Nano))
	leaderAgentID = insertAgent("qa-fail-autoroute lead " + time.Now().Format(time.RFC3339Nano))

	if !withSquad {
		return devAgentID, leaderAgentID
	}

	var squadID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
		VALUES ($1, 'qa-fail-autoroute squad', '', $2, $3)
		RETURNING id
	`, testWorkspaceID, leaderAgentID, testUserID).Scan(&squadID); err != nil {
		t.Fatalf("setup: create squad: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM squad WHERE id = $1`, squadID) })

	if _, err := testPool.Exec(ctx, `
		INSERT INTO squad_member (squad_id, member_type, member_id, role)
		VALUES ($1, 'agent', $2, 'member')
	`, squadID, devAgentID); err != nil {
		t.Fatalf("setup: add dev to squad: %v", err)
	}
	return devAgentID, leaderAgentID
}

// TestMaybeRouteToDevLeadOnQAFail_ReassignsToSquadLeader is the happy path:
// a squad-member dev's issue gains qa:fail -> the issue moves to the squad
// LEADER (not the dev, not a random member), status resets to "todo" so the
// leader's claim fires the normal dispatch path, and a comment mentioning
// the leader carries the QA verdict — the actual QA<->dev communication.
func TestMaybeRouteToDevLeadOnQAFail_ReassignsToSquadLeader(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	t.Setenv("AGORA_QA_FAIL_AUTOROUTE_ENABLED", "true")

	devAgentID, leaderAgentID := qaFailAutorouteFixture(t, ctx, true)
	issueID := sliceActionTestIssue(t, "agent", devAgentID)

	// A captured verdict so the routing comment has something to quote —
	// mirrors what a real run_qa failure leaves behind.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO qa_evidence (workspace_id, issue_id, baseline_ref, branch_sha, verdict, summary, result_json)
		VALUES ($1, $2, '', '', 'fail', 'the new endpoint returns 500', '{}'::jsonb)
	`, testWorkspaceID, issueID); err != nil {
		t.Fatalf("setup: insert qa_evidence: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM qa_evidence WHERE issue_id = $1`, issueID) })

	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}

	testHandler.maybeRouteToDevLeadOnQAFail(ctx, issue, "qa:fail", testUserID)

	updated, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("reload issue: %v", err)
	}
	if !updated.AssigneeID.Valid || uuidToString(updated.AssigneeID) != leaderAgentID {
		t.Errorf("assignee_id = %v, want leader %s (not the failing dev %s)", updated.AssigneeID, leaderAgentID, devAgentID)
	}
	if updated.AssigneeType.String != "agent" {
		t.Errorf("assignee_type = %q, want agent", updated.AssigneeType.String)
	}
	if updated.Status != "todo" {
		t.Errorf("status = %q, want todo (back on the dev board for the leader's claim to fire)", updated.Status)
	}

	var commentContent string
	if err := testPool.QueryRow(ctx, `
		SELECT content FROM comment WHERE issue_id = $1 ORDER BY created_at DESC LIMIT 1
	`, issueID).Scan(&commentContent); err != nil {
		t.Fatalf("load routing comment: %v", err)
	}
	if !strings.Contains(commentContent, "mention://agent/"+leaderAgentID) {
		t.Errorf("routing comment does not @-mention the leader: %q", commentContent)
	}
	if !strings.Contains(commentContent, "the new endpoint returns 500") {
		t.Errorf("routing comment does not carry the QA verdict summary: %q", commentContent)
	}
}

// TestMaybeRouteToDevLeadOnQAFail_Gating covers the gate paths: disabled and
// wrong-label no-op; an unassigned issue (no agent orchestrator) no-op; and the
// mandatory-orchestrator routes — a solo agent and a directly-assigned lead both
// self-route with the QA-fail feedback (bounded by the attempt cap).
func TestMaybeRouteToDevLeadOnQAFail_Gating(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	reassigned := func(issueID string) bool {
		issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
		if err != nil {
			t.Fatalf("load issue: %v", err)
		}
		return issue.Status == "todo"
	}

	t.Run("explicitly_disabled_noop", func(t *testing.T) {
		// Default is ON now, so disabling takes an explicit false.
		t.Setenv("AGORA_QA_FAIL_AUTOROUTE_ENABLED", "false")
		devAgentID, _ := qaFailAutorouteFixture(t, ctx, true)
		issueID := sliceActionTestIssue(t, "agent", devAgentID)
		issue, _ := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
		testHandler.maybeRouteToDevLeadOnQAFail(ctx, issue, "qa:fail", testUserID)
		if reassigned(issueID) {
			t.Error("explicitly disabled must not reassign")
		}
	})

	t.Run("default_on_routes", func(t *testing.T) {
		// Unset env → falls to the registry default, which is now "true".
		t.Setenv("AGORA_QA_FAIL_AUTOROUTE_ENABLED", "")
		devAgentID, _ := qaFailAutorouteFixture(t, ctx, true)
		issueID := sliceActionTestIssue(t, "agent", devAgentID)
		issue, _ := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
		testHandler.maybeRouteToDevLeadOnQAFail(ctx, issue, "qa:fail", testUserID)
		if !reassigned(issueID) {
			t.Error("default-on must route qa:fail back to the dev lead")
		}
	})

	t.Run("attempt_cap_noop", func(t *testing.T) {
		t.Setenv("AGORA_QA_FAIL_AUTOROUTE_ENABLED", "true")
		devAgentID, _ := qaFailAutorouteFixture(t, ctx, true)
		issueID := sliceActionTestIssue(t, "agent", devAgentID)
		// Pre-spend the whole loop budget.
		if _, err := testHandler.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
			ID: testUUID(issueID), WorkspaceID: testUUID(testWorkspaceID),
			Key: "qa_fail_autoroute_count", Value: []byte(strconv.Itoa(qaFailAutorouteMaxAttempts)),
		}); err != nil {
			t.Fatalf("seed count: %v", err)
		}
		issue, _ := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
		testHandler.maybeRouteToDevLeadOnQAFail(ctx, issue, "qa:fail", testUserID)
		if reassigned(issueID) {
			t.Error("an issue at the attempt cap must not auto-route again")
		}
	})

	t.Run("wrong_label_noop", func(t *testing.T) {
		t.Setenv("AGORA_QA_FAIL_AUTOROUTE_ENABLED", "true")
		devAgentID, _ := qaFailAutorouteFixture(t, ctx, true)
		issueID := sliceActionTestIssue(t, "agent", devAgentID)
		issue, _ := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
		testHandler.maybeRouteToDevLeadOnQAFail(ctx, issue, "qa:pass", testUserID)
		if reassigned(issueID) {
			t.Error("non-qa:fail label must not reassign")
		}
	})

	t.Run("unassigned_noop", func(t *testing.T) {
		t.Setenv("AGORA_QA_FAIL_AUTOROUTE_ENABLED", "true")
		issueID := sliceActionTestIssue(t, "", "") // no assignee -> no orchestrator
		issue, _ := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
		testHandler.maybeRouteToDevLeadOnQAFail(ctx, issue, "qa:fail", testUserID)
		if reassigned(issueID) {
			t.Error("an unassigned issue has no agent orchestrator and must not route")
		}
	})

	t.Run("solo_agent_self_routes", func(t *testing.T) {
		// Mandatory orchestrator: a solo agent (no squad) owns its own task, so a
		// qa:fail routes back to IT with the feedback comment — no silent wedge.
		t.Setenv("AGORA_QA_FAIL_AUTOROUTE_ENABLED", "true")
		devAgentID, _ := qaFailAutorouteFixture(t, ctx, false) // no squad membership
		issueID := sliceActionTestIssue(t, "agent", devAgentID)
		issue, _ := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
		testHandler.maybeRouteToDevLeadOnQAFail(ctx, issue, "qa:fail", testUserID)
		if !reassigned(issueID) {
			t.Error("a solo agent must self-route on qa:fail (it orchestrates its own task)")
		}
		updated, _ := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
		if !updated.AssigneeID.Valid || uuidToString(updated.AssigneeID) != devAgentID {
			t.Errorf("assignee_id = %v, want the solo agent itself %s", updated.AssigneeID, devAgentID)
		}
	})

	t.Run("failing_agent_is_leader_self_retries", func(t *testing.T) {
		// The orchestrator absorbs its OWN qa:fail: a directly-assigned lead
		// re-fires with the feedback comment (the attempt cap bounds the retry).
		t.Setenv("AGORA_QA_FAIL_AUTOROUTE_ENABLED", "true")
		_, leaderAgentID := qaFailAutorouteFixture(t, ctx, true)
		issueID := sliceActionTestIssue(t, "agent", leaderAgentID)
		issue, _ := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
		testHandler.maybeRouteToDevLeadOnQAFail(ctx, issue, "qa:fail", testUserID)
		if !reassigned(issueID) {
			t.Error("the orchestrator must absorb its own qa:fail and re-fire with feedback")
		}
	})
}

// TestCastAgentForStage covers the per-issue stage-cast resolver: an unset cast
// yields no agent (caller falls back), a valid pinned agent resolves, and a
// malformed / non-workspace id degrades to no-cast rather than wedging.
func TestCastAgentForStage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	devAgentID, castAgentID := qaFailAutorouteFixture(t, ctx, false)
	issueID := sliceActionTestIssue(t, "agent", devAgentID)

	setCast := func(val string) db.Issue {
		if _, err := testHandler.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
			ID: testUUID(issueID), WorkspaceID: testUUID(testWorkspaceID),
			Key: metaCastQAAgent, Value: []byte(strconv.Quote(val)),
		}); err != nil {
			t.Fatalf("set cast metadata: %v", err)
		}
		issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
		if err != nil {
			t.Fatalf("reload issue: %v", err)
		}
		return issue
	}

	// Unset -> no cast.
	issue, _ := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if _, ok := testHandler.castAgentForStage(ctx, issue, metaCastQAAgent); ok {
		t.Error("no cast metadata must resolve ok=false")
	}

	// Valid, ready agent in the workspace -> resolves it.
	got, ok := testHandler.castAgentForStage(ctx, setCast(castAgentID), metaCastQAAgent)
	if !ok || uuidToString(got.ID) != castAgentID {
		t.Errorf("cast must resolve the pinned agent %s, got ok=%v id=%s", castAgentID, ok, uuidToString(got.ID))
	}

	// Malformed id -> no cast (degrade, do not wedge).
	if _, ok := testHandler.castAgentForStage(ctx, setCast("not-a-uuid"), metaCastQAAgent); ok {
		t.Error("a malformed cast id must resolve ok=false")
	}

	// Well-formed but unknown agent id (a workspace id, not an agent) -> no cast.
	if _, ok := testHandler.castAgentForStage(ctx, setCast(testWorkspaceID), metaCastQAAgent); ok {
		t.Error("a cast id that is not an agent in the workspace must resolve ok=false")
	}
}

// TestResolveReviewerAgent_HonorsCast asserts the review cast wins over the
// default resolution order, but never breaks the author-exclusion invariant: a
// cast reviewer that IS the diff's author is ignored.
func TestResolveReviewerAgent_HonorsCast(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	devAgentID, reviewerAgentID := qaFailAutorouteFixture(t, ctx, false)
	issueID := sliceActionTestIssue(t, "agent", devAgentID)

	setReviewCast := func(val string) db.Issue {
		if _, err := testHandler.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
			ID: testUUID(issueID), WorkspaceID: testUUID(testWorkspaceID),
			Key: metaCastReviewAgent, Value: []byte(strconv.Quote(val)),
		}); err != nil {
			t.Fatalf("set review cast: %v", err)
		}
		issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
		if err != nil {
			t.Fatalf("reload issue: %v", err)
		}
		return issue
	}

	// A distinct cast reviewer wins.
	got, ok := testHandler.resolveReviewerAgent(ctx, setReviewCast(reviewerAgentID))
	if !ok || uuidToString(got.ID) != reviewerAgentID {
		t.Errorf("review cast must pick the pinned reviewer %s, got ok=%v id=%s", reviewerAgentID, ok, uuidToString(got.ID))
	}

	// A cast reviewer that IS the author is ignored (never self-review).
	if got, ok := testHandler.resolveReviewerAgent(ctx, setReviewCast(devAgentID)); ok && uuidToString(got.ID) == devAgentID {
		t.Error("a cast reviewer equal to the author must be ignored")
	}
}

// TestPipelineManual is the pure resolver: only an explicit "manual" (any case)
// flips the switch; unset / "auto" / anything else stays on autopilot.
func TestPipelineManual(t *testing.T) {
	mk := func(v string) db.Issue {
		if v == "" {
			return db.Issue{}
		}
		return db.Issue{Metadata: []byte(`{"pipeline_mode":` + strconv.Quote(v) + `}`)}
	}
	if pipelineManual(mk("")) {
		t.Error("unset pipeline_mode must be auto (false)")
	}
	if pipelineManual(mk("auto")) {
		t.Error(`"auto" must be false`)
	}
	if !pipelineManual(mk("manual")) {
		t.Error(`"manual" must be true`)
	}
	if !pipelineManual(mk("MANUAL")) {
		t.Error("manual must be case-insensitive")
	}
}

// TestPipelineManual_WakesOrchestratorNotAutoQA: in manual mode the in_review
// auto-QA reflex steps back and instead wakes the orchestrator (an @mention that
// triggers its run) to dispatch QA itself — the pipeline stays in its hands.
func TestPipelineManual_WakesOrchestratorNotAutoQA(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	t.Setenv("AGORA_AUTO_QA_ENABLED", "true")
	devAgentID, leaderAgentID := qaFailAutorouteFixture(t, ctx, true) // dev in a squad led by leader
	issueID := sliceActionTestIssue(t, "agent", devAgentID)
	if _, err := testHandler.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
		ID: testUUID(issueID), WorkspaceID: testUUID(testWorkspaceID),
		Key: metaPipelineMode, Value: []byte(strconv.Quote("manual")),
	}); err != nil {
		t.Fatalf("set manual mode: %v", err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}

	testHandler.maybeRunQAOnInReview(ctx, issue, "member", testUserID)

	var content string
	if err := testPool.QueryRow(ctx, `
		SELECT content FROM comment WHERE issue_id = $1 ORDER BY created_at DESC LIMIT 1
	`, issueID).Scan(&content); err != nil {
		t.Fatalf("manual mode must post a wake comment: %v", err)
	}
	if !strings.Contains(content, "mention://agent/"+leaderAgentID) {
		t.Errorf("manual mode must @mention the orchestrator (leader), got: %q", content)
	}
	if !strings.Contains(content, "manual pipeline mode") {
		t.Errorf("wake comment should name manual mode, got: %q", content)
	}
}

// TestMaybeRecoverSquadTaskFailure_ReTriggersLeader is the BUG-2 happy path: a
// squad-member dev task dies (idle_watchdog) on a still-in-progress issue with
// nothing else queued → the squad LEADER is re-woken with an @-mention carrying
// the failure reason, so the wedged issue gets re-triaged instead of stalling.
func TestMaybeRecoverSquadTaskFailure_ReTriggersLeader(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	t.Setenv("AGORA_SQUAD_FAILURE_RECOVERY_ENABLED", "true")

	devAgentID, leaderAgentID := qaFailAutorouteFixture(t, ctx, true)
	issueID := sliceActionTestIssue(t, "", "") // in_progress, no assignee/pending task
	task := db.AgentTaskQueue{IssueID: testUUID(issueID), AgentID: testUUID(devAgentID)}

	testHandler.maybeRecoverSquadTaskFailure(ctx, task, "idle_watchdog")

	var content string
	if err := testPool.QueryRow(ctx, `
		SELECT content FROM comment WHERE issue_id = $1 ORDER BY created_at DESC LIMIT 1
	`, issueID).Scan(&content); err != nil {
		t.Fatalf("load recovery comment: %v", err)
	}
	if !strings.Contains(content, "mention://agent/"+leaderAgentID) {
		t.Errorf("recovery comment does not @-mention the leader: %q", content)
	}
	if !strings.Contains(content, "idle_watchdog") {
		t.Errorf("recovery comment does not carry the failure reason: %q", content)
	}
	if !strings.Contains(content, squadFailureRecoveryMarker) {
		t.Errorf("recovery comment missing the retry-cap marker: %q", content)
	}
}

// TestMaybeRecoverSquadTaskFailure_Gating covers every no-op path: disabled,
// clean cancel, solo agent (no squad), the failing agent IS the leader, the
// issue already progressed past dev, and the retry cap reached.
func TestMaybeRecoverSquadTaskFailure_Gating(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	commentCount := func(issueID string) int {
		var n int
		if err := testPool.QueryRow(ctx, `SELECT count(*) FROM comment WHERE issue_id = $1`, issueID).Scan(&n); err != nil {
			t.Fatalf("count comments: %v", err)
		}
		return n
	}

	t.Run("disabled noop", func(t *testing.T) {
		t.Setenv("AGORA_SQUAD_FAILURE_RECOVERY_ENABLED", "")
		devAgentID, _ := qaFailAutorouteFixture(t, ctx, true)
		issueID := sliceActionTestIssue(t, "", "")
		task := db.AgentTaskQueue{IssueID: testUUID(issueID), AgentID: testUUID(devAgentID)}
		testHandler.maybeRecoverSquadTaskFailure(ctx, task, "idle_watchdog")
		if commentCount(issueID) != 0 {
			t.Error("disabled gate must not post a comment")
		}
	})

	t.Run("clean cancel noop", func(t *testing.T) {
		t.Setenv("AGORA_SQUAD_FAILURE_RECOVERY_ENABLED", "true")
		devAgentID, _ := qaFailAutorouteFixture(t, ctx, true)
		issueID := sliceActionTestIssue(t, "", "")
		task := db.AgentTaskQueue{IssueID: testUUID(issueID), AgentID: testUUID(devAgentID)}
		testHandler.maybeRecoverSquadTaskFailure(ctx, task, "cancelled")
		if commentCount(issueID) != 0 {
			t.Error("a clean cancellation must not trigger recovery")
		}
	})

	t.Run("solo agent noop", func(t *testing.T) {
		t.Setenv("AGORA_SQUAD_FAILURE_RECOVERY_ENABLED", "true")
		devAgentID, _ := qaFailAutorouteFixture(t, ctx, false) // no squad
		issueID := sliceActionTestIssue(t, "", "")
		task := db.AgentTaskQueue{IssueID: testUUID(issueID), AgentID: testUUID(devAgentID)}
		testHandler.maybeRecoverSquadTaskFailure(ctx, task, "idle_watchdog")
		if commentCount(issueID) != 0 {
			t.Error("a solo agent (no squad) must not trigger recovery")
		}
	})

	t.Run("failing agent is leader noop", func(t *testing.T) {
		t.Setenv("AGORA_SQUAD_FAILURE_RECOVERY_ENABLED", "true")
		_, leaderAgentID := qaFailAutorouteFixture(t, ctx, true)
		issueID := sliceActionTestIssue(t, "", "")
		task := db.AgentTaskQueue{IssueID: testUUID(issueID), AgentID: testUUID(leaderAgentID)}
		testHandler.maybeRecoverSquadTaskFailure(ctx, task, "idle_watchdog")
		if commentCount(issueID) != 0 {
			t.Error("the leader failing its own task must not self-loop")
		}
	})

	t.Run("issue past dev noop", func(t *testing.T) {
		t.Setenv("AGORA_SQUAD_FAILURE_RECOVERY_ENABLED", "true")
		devAgentID, _ := qaFailAutorouteFixture(t, ctx, true)
		issueID := sliceActionTestIssue(t, "", "")
		if _, err := testPool.Exec(ctx, `UPDATE issue SET status='in_review' WHERE id=$1`, issueID); err != nil {
			t.Fatalf("set in_review: %v", err)
		}
		task := db.AgentTaskQueue{IssueID: testUUID(issueID), AgentID: testUUID(devAgentID)}
		testHandler.maybeRecoverSquadTaskFailure(ctx, task, "idle_watchdog")
		if commentCount(issueID) != 0 {
			t.Error("an issue already in_review must not trigger recovery (work landed elsewhere)")
		}
	})

	t.Run("retry cap reached noop", func(t *testing.T) {
		t.Setenv("AGORA_SQUAD_FAILURE_RECOVERY_ENABLED", "true")
		devAgentID, _ := qaFailAutorouteFixture(t, ctx, true)
		issueID := sliceActionTestIssue(t, "", "")
		// Pre-seed maxSquadFailureRecoveries recovery markers → cap hit.
		for i := 0; i < maxSquadFailureRecoveries; i++ {
			if _, err := testPool.Exec(ctx, `
				INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type)
				VALUES ($1, $2, 'member', $3, $4, 'comment')
			`, issueID, testWorkspaceID, testUserID, "prior recovery "+squadFailureRecoveryMarker); err != nil {
				t.Fatalf("seed marker: %v", err)
			}
		}
		before := commentCount(issueID)
		task := db.AgentTaskQueue{IssueID: testUUID(issueID), AgentID: testUUID(devAgentID)}
		testHandler.maybeRecoverSquadTaskFailure(ctx, task, "idle_watchdog")
		if commentCount(issueID) != before {
			t.Error("recovery must stop once the retry cap is reached")
		}
	})
}

// TestBuildSliceInstructionAutoDocs covers the auto_docs kind: document a
// change into a SEPARATE docs repo, open a PR there, never touch product code,
// never merge — and skip if the change has no doc-worthy surface. Product-neutral.
func TestBuildSliceInstructionAutoDocs(t *testing.T) {
	if !isKnownSliceActionKind(sliceActionAutoDocs) {
		t.Fatal("auto_docs must be a known slice-action kind")
	}
	got := buildSliceInstruction(sliceActionAutoDocs, "")
	if got == "" {
		t.Fatal("auto_docs instruction must not be empty")
	}
	lower := strings.ToLower(got)
	for _, want := range []string{"document", "documentation", "pull request", "do not", "separate", "code"} {
		if !strings.Contains(lower, want) {
			t.Errorf("auto_docs instruction must mention %q, got: %s", want, got)
		}
	}
	// Docs-only: must say not to change product code, and must not merge.
	if !strings.Contains(lower, "not touch product code") && !strings.Contains(lower, "do not touch product code") {
		t.Errorf("auto_docs must forbid touching product code, got: %s", got)
	}
	// It opens a PR against the DOCS repo, so it must NOT get the code-repo branch
	// hint (that targets the project's code repo / billing base).
	if sliceActionOpensPR(sliceActionAutoDocs) {
		t.Error("auto_docs opens its PR against the docs repo; must be excluded from the code-repo branch-hint set")
	}
}

// TestSliceActionDocsRepoContext covers the project-configurable docs target for
// auto_docs (the docs_repo project setting), mirroring the QA smoke seam.
func TestSliceActionDocsRepoContext(t *testing.T) {
	ctx := context.Background()
	newProject := func(settings string) pgtype.UUID {
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
			"title": "docs-repo project " + time.Now().Format(time.RFC3339Nano),
		})
		testHandler.CreateProject(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create project: %d", w.Code)
		}
		var p ProjectResponse
		json.NewDecoder(w.Body).Decode(&p)
		t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM project WHERE id = $1`, p.ID) })
		if settings != "" {
			testPool.Exec(ctx, `UPDATE project SET settings = $1 WHERE id = $2`, []byte(settings), p.ID)
		}
		return testUUID(p.ID)
	}
	issueIn := func(pid pgtype.UUID) db.Issue {
		return db.Issue{ProjectID: pid, WorkspaceID: testUUID(testWorkspaceID)}
	}

	t.Run("repo_rendered_when_set", func(t *testing.T) {
		pid := newProject(`{"docs_repo":"https://github.com/jamshidtulaganov/sd-doc.git"}`)
		got := testHandler.sliceActionDocsRepoContext(ctx, issueIn(pid))
		if !strings.Contains(got, "https://github.com/jamshidtulaganov/sd-doc.git") || !strings.Contains(got, "open the review request against it") {
			t.Errorf("docs repo context wrong: %q", got)
		}
	})
	t.Run("empty_when_unset", func(t *testing.T) {
		pid := newProject(`{}`)
		if got := testHandler.sliceActionDocsRepoContext(ctx, issueIn(pid)); got != "" {
			t.Errorf("no docs_repo must yield \"\", got: %q", got)
		}
	})
	t.Run("no_project_empty", func(t *testing.T) {
		if got := testHandler.sliceActionDocsRepoContext(ctx, db.Issue{}); got != "" {
			t.Errorf("no project must yield \"\", got: %q", got)
		}
	})
}

// TestSliceActionQADocsContext covers the READ-side counterpart to
// sliceActionDocsRepoContext: run_qa / run_test_cases / gen_test_cases must be
// told to consult the project's docs repo as real context, not just write to
// it (which only auto_docs does). Same project-settings seam, same empty/
// no-project degrade — the QA-flavored wording differs (judging, not writing).
func TestSliceActionQADocsContext(t *testing.T) {
	ctx := context.Background()
	newProject := func(settings string) pgtype.UUID {
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
			"title": "qa-docs project " + time.Now().Format(time.RFC3339Nano),
		})
		testHandler.CreateProject(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create project: %d", w.Code)
		}
		var p ProjectResponse
		json.NewDecoder(w.Body).Decode(&p)
		t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM project WHERE id = $1`, p.ID) })
		if settings != "" {
			testPool.Exec(ctx, `UPDATE project SET settings = $1 WHERE id = $2`, []byte(settings), p.ID)
		}
		return testUUID(p.ID)
	}
	issueIn := func(pid pgtype.UUID) db.Issue {
		return db.Issue{ProjectID: pid, WorkspaceID: testUUID(testWorkspaceID)}
	}

	t.Run("repo_rendered_when_set", func(t *testing.T) {
		pid := newProject(`{"docs_repo":"https://github.com/jamshidtulaganov/sd-doc.git"}`)
		got := testHandler.sliceActionQADocsContext(ctx, issueIn(pid))
		if !strings.Contains(got, "https://github.com/jamshidtulaganov/sd-doc.git") {
			t.Errorf("docs repo URL missing: %q", got)
		}
		if !strings.Contains(got, "REAL source of") || !strings.Contains(got, "disagree") {
			t.Errorf("expected judging-context wording (REAL source / disagree), got: %q", got)
		}
		// This is the READ seam — must never carry the write-side ("open the
		// review request") instruction, or a QA run would think it's supposed
		// to edit the docs repo.
		if strings.Contains(got, "open the review request against it") {
			t.Errorf("QA docs context must not carry the write-side auto_docs instruction: %q", got)
		}
	})
	t.Run("empty_when_unset", func(t *testing.T) {
		pid := newProject(`{}`)
		if got := testHandler.sliceActionQADocsContext(ctx, issueIn(pid)); got != "" {
			t.Errorf("no docs_repo must yield \"\", got: %q", got)
		}
	})
	t.Run("no_project_empty", func(t *testing.T) {
		if got := testHandler.sliceActionQADocsContext(ctx, db.Issue{}); got != "" {
			t.Errorf("no project must yield \"\", got: %q", got)
		}
	})
}

// TestSliceActionQASmokeContext covers the generic QA gate's only
// product-specific seam: the project-configurable smoke override
// (qa_smoke_cmd / qa_smoke_url in project.settings) appended to a run_qa
// instruction. It is a regression guard for the SD-box-hardcoding removal —
// the gate must stay generic, reading the smoke flow from project settings
// instead of any one product's branch/skill. Asserts cmd-only, url-only, both,
// empty settings, and no-project paths, plus that the override actually
// concatenates onto the run_qa base instruction the agent receives.
func TestSliceActionQASmokeContext(t *testing.T) {
	ctx := context.Background()

	// newProject creates a real project via the handler, optionally sets its
	// settings JSON directly, registers cleanup, and returns its UUID.
	newProject := func(settings string) pgtype.UUID {
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
			"title": "qa-smoke project " + time.Now().Format(time.RFC3339Nano),
		})
		testHandler.CreateProject(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create project: %d: %s", w.Code, w.Body.String())
		}
		var p ProjectResponse
		if err := json.NewDecoder(w.Body).Decode(&p); err != nil {
			t.Fatalf("decode project: %v", err)
		}
		t.Cleanup(func() {
			testPool.Exec(ctx, `DELETE FROM project WHERE id = $1`, p.ID)
		})
		if settings != "" {
			if _, err := testPool.Exec(ctx, `UPDATE project SET settings = $1 WHERE id = $2`, []byte(settings), p.ID); err != nil {
				t.Fatalf("set settings: %v", err)
			}
		}
		return testUUID(p.ID)
	}

	issueIn := func(projectID pgtype.UUID) db.Issue {
		return db.Issue{ProjectID: projectID, WorkspaceID: testUUID(testWorkspaceID)}
	}

	t.Run("cmd_and_url_both_rendered", func(t *testing.T) {
		pid := newProject(`{"qa_smoke_cmd":"pnpm dev","qa_smoke_url":"http://localhost:5173"}`)
		got := testHandler.sliceActionQASmokeContext(ctx, issueIn(pid))
		for _, want := range []string{"pnpm dev", "http://localhost:5173", "instead of auto-detecting"} {
			if !strings.Contains(got, want) {
				t.Errorf("want %q in smoke context, got: %q", want, got)
			}
		}
	})

	t.Run("cmd_only_no_url_clause", func(t *testing.T) {
		pid := newProject(`{"qa_smoke_cmd":"make run"}`)
		got := testHandler.sliceActionQASmokeContext(ctx, issueIn(pid))
		if !strings.Contains(got, "make run") || strings.Contains(got, "smoke it at") {
			t.Errorf("cmd-only render wrong: %q", got)
		}
	})

	t.Run("url_only_no_cmd_clause", func(t *testing.T) {
		pid := newProject(`{"qa_smoke_url":"http://localhost:3000"}`)
		got := testHandler.sliceActionQASmokeContext(ctx, issueIn(pid))
		if !strings.Contains(got, "http://localhost:3000") || strings.Contains(got, "start the app") {
			t.Errorf("url-only render wrong: %q", got)
		}
	})

	t.Run("empty_settings_yields_nothing", func(t *testing.T) {
		pid := newProject(`{}`)
		if got := testHandler.sliceActionQASmokeContext(ctx, issueIn(pid)); got != "" {
			t.Errorf("empty settings must yield \"\", got: %q", got)
		}
	})

	t.Run("whitespace_only_values_yield_nothing", func(t *testing.T) {
		pid := newProject(`{"qa_smoke_cmd":"   ","qa_smoke_url":"  "}`)
		if got := testHandler.sliceActionQASmokeContext(ctx, issueIn(pid)); got != "" {
			t.Errorf("whitespace-only settings must yield \"\", got: %q", got)
		}
	})

	t.Run("no_project_yields_nothing", func(t *testing.T) {
		if got := testHandler.sliceActionQASmokeContext(ctx, db.Issue{}); got != "" {
			t.Errorf("no project must yield \"\", got: %q", got)
		}
	})

	// Integration seam: the agent receives buildSliceInstruction(run_qa) with the
	// smoke override concatenated. Verify both the deterministic gate text and the
	// project override survive into the final instruction.
	t.Run("appended_to_run_qa_instruction", func(t *testing.T) {
		pid := newProject(`{"qa_smoke_cmd":"pnpm dev"}`)
		full := buildSliceInstruction(sliceActionRunQA, "") + testHandler.sliceActionQASmokeContext(ctx, issueIn(pid))
		for _, want := range []string{"qa:pass", "exit code", "pnpm dev"} {
			if !strings.Contains(full, want) {
				t.Errorf("run_qa + smoke must contain %q, got: %q", want, full)
			}
		}
	})
}

// sliceActionTestIssue creates a workspace issue (via the real handler) and
// returns its UUID, registering cleanup. Optionally seeds an assignee directly
// in SQL (pass assigneeType=="" to leave the issue unassigned).
func sliceActionTestIssue(t *testing.T, assigneeType, assigneeID string) string {
	t.Helper()

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  "slice-action issue " + time.Now().Format(time.RFC3339Nano),
		"status": "in_progress",
	})
	testHandler.CreateIssue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create issue: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var issue IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&issue); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issue.ID)
		testPool.Exec(context.Background(), `DELETE FROM comment WHERE issue_id = $1`, issue.ID)
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issue.ID)
	})

	if assigneeType != "" {
		setIssueAssigneeDirect(t, issue.ID, assigneeType, assigneeID)
	}
	return issue.ID
}

// postSliceAction fires POST /api/issues/{id}/slice-actions against the real
// handler with member auth and returns the recorder.
func postSliceAction(t *testing.T, issueID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/issues/"+issueID+"/slice-actions", body)
	req = withURLParam(req, "id", issueID)
	testHandler.CreateSliceAction(w, req)
	return w
}

// TestCreateSliceActionAgentAssignee verifies the happy path for an agent
// assignee: firing a slice action on an issue assigned to an agent posts an
// @mention comment that targets that agent AND queues exactly one agent task,
// driven by the mention (the assignee is not double-triggered).
func TestCreateSliceActionAgentAssignee(t *testing.T) {
	if testHandler == nil {
		t.Skip("no DATABASE_URL; handler integration tests skipped")
	}
	agentID := createHandlerTestAgent(t, "Slice Assignee "+time.Now().Format("150405.000000"), nil)
	issueID := sliceActionTestIssue(t, "agent", agentID)

	w := postSliceAction(t, issueID, map[string]any{
		"kind":  sliceActionDraftCode,
		"scope": "the URL parser",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateSliceAction: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp CreateSliceActionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.AgentID != agentID {
		t.Errorf("expected resolved agent %s, got %s", agentID, resp.AgentID)
	}
	if !strings.Contains(resp.Instruction, "Focus on: the URL parser") {
		t.Errorf("expected scoped instruction, got: %s", resp.Instruction)
	}
	if !strings.Contains(resp.Comment.Content, "mention://agent/"+agentID) {
		t.Errorf("expected mention link to %s in comment, got: %s", agentID, resp.Comment.Content)
	}

	if got := countPendingTasksForAgent(t, issueID, agentID); got != 1 {
		t.Errorf("expected exactly 1 queued task for the assignee agent, got %d", got)
	}
}

// TestCreateSliceActionBranchHint verifies that PR-producing actions on a
// Bitrix-synced issue pin the working branch to btx-<bitrixTaskId>, while
// review_part (which opens no PR) does not.
func TestCreateSliceActionBranchHint(t *testing.T) {
	if testHandler == nil {
		t.Skip("no DATABASE_URL; handler integration tests skipped")
	}
	agentID := createHandlerTestAgent(t, "Slice Branch "+time.Now().Format("150405.000000"), nil)
	issueID := sliceActionTestIssue(t, "agent", agentID)

	// Stamp the Bitrix task id the branch hint keys off.
	if _, err := testPool.Exec(context.Background(),
		`UPDATE issue SET metadata = '{"bitrix_task_id":"77123"}'::jsonb WHERE id = $1`, issueID); err != nil {
		t.Fatalf("seed metadata: %v", err)
	}

	// draft_code opens a PR → deterministic branch hint present.
	w := postSliceAction(t, issueID, map[string]any{"kind": sliceActionDraftCode})
	if w.Code != http.StatusCreated {
		t.Fatalf("draft_code: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp CreateSliceActionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(resp.Instruction, "btx-77123") {
		t.Errorf("draft_code should pin branch btx-77123, got: %s", resp.Instruction)
	}

	// review_part posts an advisory comment, opens no PR → no branch hint.
	w2 := postSliceAction(t, issueID, map[string]any{"kind": sliceActionReviewPart})
	if w2.Code != http.StatusCreated {
		t.Fatalf("review_part: expected 201, got %d: %s", w2.Code, w2.Body.String())
	}
	var resp2 CreateSliceActionResponse
	if err := json.NewDecoder(w2.Body).Decode(&resp2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if strings.Contains(resp2.Instruction, "btx-") {
		t.Errorf("review_part must not pin a branch, got: %s", resp2.Instruction)
	}
}

// TestCreateSliceActionFallbackToOwnAgent verifies fallback (c): on a
// member-assigned issue with no explicit agent_id, the action resolves to the
// caller's own ready agent and queues exactly one task driven by the mention.
func TestCreateSliceActionFallbackToOwnAgent(t *testing.T) {
	if testHandler == nil {
		t.Skip("no DATABASE_URL; handler integration tests skipped")
	}
	// Guarantee the caller owns at least one ready agent so fallback (c) can
	// succeed. resolveOwnAgent returns the caller's FIRST ready owned agent by
	// creation order, and testUserID may already own the shared fixture agent
	// created in TestMain — so we assert the RESOLVED agent is caller-owned and
	// receives the single queued task, not that it is this specific new one
	// (which would make the test depend on agent creation order).
	_ = createHandlerTestAgent(t, "Slice Own "+time.Now().Format("150405.000000"), nil)

	// Issue assigned to the calling member (a human-owned issue), so the agent
	// assignee path (b) does not apply and the handler must fall back to (c).
	issueID := sliceActionTestIssue(t, "member", testUserID)

	w := postSliceAction(t, issueID, map[string]any{
		"kind": sliceActionWriteTests,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateSliceAction: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp CreateSliceActionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.AgentID == "" {
		t.Fatal("expected a resolved own agent, got empty agent_id")
	}
	// The essence of fallback (c): the resolved agent must be owned by the
	// calling user.
	var ownerID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT owner_id::text FROM agent WHERE id = $1`, resp.AgentID).Scan(&ownerID); err != nil {
		t.Fatalf("lookup resolved agent owner: %v", err)
	}
	if ownerID != testUserID {
		t.Errorf("expected fallback to a caller-owned agent (owner %s), got agent %s owned by %s", testUserID, resp.AgentID, ownerID)
	}
	if !strings.Contains(resp.Comment.Content, "mention://agent/"+resp.AgentID) {
		t.Errorf("expected mention link to the resolved own agent in comment, got: %s", resp.Comment.Content)
	}
	if got := countPendingTasksForAgent(t, issueID, resp.AgentID); got != 1 {
		t.Errorf("expected exactly 1 queued task for the resolved own agent, got %d", got)
	}
}

// TestCreateSliceActionUnknownAgentID verifies that an explicit agent_id that
// does not refer to a usable agent in the workspace is a 400.
func TestCreateSliceActionUnknownAgentID(t *testing.T) {
	if testHandler == nil {
		t.Skip("no DATABASE_URL; handler integration tests skipped")
	}
	issueID := sliceActionTestIssue(t, "", "")

	w := postSliceAction(t, issueID, map[string]any{
		"kind":     sliceActionDraftCode,
		"agent_id": "00000000-0000-0000-0000-000000000000",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown agent_id, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCreateSliceActionUnknownKind verifies that an unknown kind is a 400 and
// that no comment / task is created as a side effect.
func TestCreateSliceActionUnknownKind(t *testing.T) {
	if testHandler == nil {
		t.Skip("no DATABASE_URL; handler integration tests skipped")
	}
	agentID := createHandlerTestAgent(t, "Slice Kind "+time.Now().Format("150405.000000"), nil)
	issueID := sliceActionTestIssue(t, "agent", agentID)

	w := postSliceAction(t, issueID, map[string]any{
		"kind": "ship_it",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown kind, got %d: %s", w.Code, w.Body.String())
	}
	if got := countPendingTasksForAgent(t, issueID, agentID); got != 0 {
		t.Errorf("unknown kind must not queue a task, got %d", got)
	}
}

// makePublicAgent flips an agent's visibility to public so it is reachable by
// any workspace member (used as an injection target / explicit accessible
// agent in the tests below).
func makePublicAgent(t *testing.T, agentID string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent SET visibility = 'workspace' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("make agent workspace-visible: %v", err)
	}
}

// TestCreateSliceActionScopeMentionInjection is the regression for the mention
// INJECTION finding (1). A caller-supplied scope that embeds a second mention
// link must NOT smuggle a second queued task: after sanitizing, the resolved
// agent is the ONLY mention in the comment and exactly ONE task is queued (for
// the resolved agent), with ZERO tasks for the injected target — even though
// that target is a real, ready, accessible agent that would otherwise be
// triggered by the parsed mention.
func TestCreateSliceActionScopeMentionInjection(t *testing.T) {
	if testHandler == nil {
		t.Skip("no DATABASE_URL; handler integration tests skipped")
	}
	stamp := time.Now().Format("150405.000000")
	resolvedID := createHandlerTestAgent(t, "Slice Resolved "+stamp, nil)
	// The injected target is a real, ready, PUBLIC agent owned by the caller —
	// so the only thing preventing a second task is the scope sanitizer, not an
	// access gate or readiness check.
	injectedID := createHandlerTestAgent(t, "Slice Injected "+stamp, nil)
	makePublicAgent(t, injectedID)

	issueID := sliceActionTestIssue(t, "agent", resolvedID)

	w := postSliceAction(t, issueID, map[string]any{
		"kind":  sliceActionDraftCode,
		"scope": "the parser [@evil](mention://agent/" + injectedID + ")",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateSliceAction: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp CreateSliceActionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.AgentID != resolvedID {
		t.Errorf("expected resolved agent %s, got %s", resolvedID, resp.AgentID)
	}

	// The posted comment must contain exactly one mention — the resolved agent.
	mentions := util.ParseMentions(resp.Comment.Content)
	if len(mentions) != 1 {
		t.Fatalf("expected exactly 1 mention in comment, got %d: %q", len(mentions), resp.Comment.Content)
	}
	if mentions[0].Type != "agent" || mentions[0].ID != resolvedID {
		t.Errorf("expected the only mention to be resolved agent %s, got %s/%s", resolvedID, mentions[0].Type, mentions[0].ID)
	}
	if strings.Contains(resp.Comment.Content, "mention://agent/"+injectedID) {
		t.Errorf("injected mention link must not survive in comment, got: %q", resp.Comment.Content)
	}

	// Exactly one task for the resolved agent; zero for the injected target.
	if got := countPendingTasksForAgent(t, issueID, resolvedID); got != 1 {
		t.Errorf("expected exactly 1 queued task for the resolved agent, got %d", got)
	}
	if got := countPendingTasksForAgent(t, issueID, injectedID); got != 0 {
		t.Errorf("expected 0 queued tasks for the INJECTED target agent, got %d", got)
	}
}

// TestCreateSliceActionExplicitPrivateAgentForbidden is the regression for
// finding (2a): an explicit agent_id naming another user's PRIVATE agent must
// be rejected with the SAME 400 as a nonexistent agent (no existence oracle, no
// private-agent disclosure), and must NOT 201-with-0-tasks.
func TestCreateSliceActionExplicitPrivateAgentForbidden(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("no DATABASE_URL; handler integration tests skipped")
	}
	// A ready, private agent owned by a DIFFERENT user; the caller (testUserID,
	// workspace owner) is admin — but the gate for slice actions resolves the
	// caller as a "member" actor, and admin DOES pass canAccessPrivateAgent, so
	// to test the FORBIDDEN path we must call as the unrelated plain member.
	privateAgentID, _, plainMemberID := privateAgentTestFixture(t)

	issueID := sliceActionTestIssue(t, "", "")

	w := httptest.NewRecorder()
	req := newRequestAs(plainMemberID, "POST", "/api/issues/"+issueID+"/slice-actions", map[string]any{
		"kind":     sliceActionDraftCode,
		"agent_id": privateAgentID,
	})
	req = withURLParam(req, "id", issueID)
	testHandler.CreateSliceAction(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for inaccessible private agent_id, got %d: %s", w.Code, w.Body.String())
	}
	// The error must be indistinguishable from a nonexistent agent — same
	// message, no name/id leak.
	if strings.Contains(w.Body.String(), "private-access-test-agent") {
		t.Errorf("error body must not leak the private agent name, got: %s", w.Body.String())
	}
	// And no task may have been queued for the private agent.
	if got := countPendingTasksForAgent(t, issueID, privateAgentID); got != 0 {
		t.Errorf("forbidden explicit agent_id must not queue a task, got %d", got)
	}
}

// TestCreateSliceActionPrivateAssigneeFallsBack is the regression for finding
// (2b): when the issue is assigned to another user's PRIVATE agent that the
// caller cannot access, the assignee path must be treated as "not resolved" and
// fall through to the caller's own-agent path — never 201-with-0-tasks and
// never leaking the private assignee's identity. Here the caller owns a ready
// agent, so resolution must land on THAT agent.
func TestCreateSliceActionPrivateAssigneeFallsBack(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("no DATABASE_URL; handler integration tests skipped")
	}
	privateAgentID, _, plainMemberID := privateAgentTestFixture(t)

	// Give the plain member their own ready agent so fallback (c) can succeed.
	// createHandlerTestAgent seeds an agent owned by testUserID; we re-own it to
	// the plain member here so resolveOwnAgent finds it for that caller.
	ownAgentID := createHandlerTestAgent(t, "Slice Member Own "+time.Now().Format("150405.000000"), nil)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent SET owner_id = $2 WHERE id = $1`, ownAgentID, plainMemberID); err != nil {
		t.Fatalf("re-own agent to plain member: %v", err)
	}

	// Issue assigned to the private agent the caller cannot access.
	issueID := sliceActionTestIssue(t, "agent", privateAgentID)

	w := httptest.NewRecorder()
	req := newRequestAs(plainMemberID, "POST", "/api/issues/"+issueID+"/slice-actions", map[string]any{
		"kind": sliceActionWriteTests,
	})
	req = withURLParam(req, "id", issueID)
	testHandler.CreateSliceAction(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 (fall back to own agent), got %d: %s", w.Code, w.Body.String())
	}
	var resp CreateSliceActionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// Must resolve to the caller's own agent, NOT the inaccessible private
	// assignee.
	if resp.AgentID == privateAgentID {
		t.Fatalf("must not resolve to the inaccessible private assignee %s", privateAgentID)
	}
	if resp.AgentID != ownAgentID {
		t.Errorf("expected fallback to caller's own agent %s, got %s", ownAgentID, resp.AgentID)
	}
	// The private assignee's id/name must never appear in the posted comment.
	if strings.Contains(resp.Comment.Content, privateAgentID) ||
		strings.Contains(resp.Comment.Content, "private-access-test-agent") {
		t.Errorf("comment must not leak the private assignee identity, got: %q", resp.Comment.Content)
	}
	// Exactly one task — for the own agent; zero for the private assignee.
	if got := countPendingTasksForAgent(t, issueID, resp.AgentID); got != 1 {
		t.Errorf("expected exactly 1 queued task for the own agent, got %d", got)
	}
	if got := countPendingTasksForAgent(t, issueID, privateAgentID); got != 0 {
		t.Errorf("expected 0 queued tasks for the inaccessible private assignee, got %d", got)
	}
}

// TestCreateSliceActionPrivateAssigneeNoOwnAgent verifies the second half of
// finding (2b): when the assignee is an inaccessible private agent AND the
// caller has no own ready agent, the handler returns 400 (no agent available) —
// it must never 201 with zero tasks.
func TestCreateSliceActionPrivateAssigneeNoOwnAgent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("no DATABASE_URL; handler integration tests skipped")
	}
	privateAgentID, _, plainMemberID := privateAgentTestFixture(t)

	issueID := sliceActionTestIssue(t, "agent", privateAgentID)

	w := httptest.NewRecorder()
	req := newRequestAs(plainMemberID, "POST", "/api/issues/"+issueID+"/slice-actions", map[string]any{
		"kind": sliceActionDraftCode,
	})
	req = withURLParam(req, "id", issueID)
	testHandler.CreateSliceAction(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (no accessible agent), got %d: %s", w.Code, w.Body.String())
	}
	if got := countPendingTasksForAgent(t, issueID, privateAgentID); got != 0 {
		t.Errorf("inaccessible private assignee must not be queued, got %d", got)
	}
}

// TestCreateSliceActionExplicitAgentOverridesAssignee is the missing
// agent!=assignee regression for finding (3): an issue assigned to agent X,
// fired with an explicit accessible agent_id=Y, must target Y — the comment
// mentions Y, exactly one task is queued for Y, and ZERO for X (the assignee
// trigger is suppressed because the comment mentions Y, not X).
func TestCreateSliceActionExplicitAgentOverridesAssignee(t *testing.T) {
	if testHandler == nil {
		t.Skip("no DATABASE_URL; handler integration tests skipped")
	}
	stamp := time.Now().Format("150405.000000")
	assigneeX := createHandlerTestAgent(t, "Slice Assignee X "+stamp, nil)
	explicitY := createHandlerTestAgent(t, "Slice Explicit Y "+stamp, nil)

	// Issue assigned to X; caller (testUserID, owner) owns both agents so Y is
	// accessible.
	issueID := sliceActionTestIssue(t, "agent", assigneeX)

	w := postSliceAction(t, issueID, map[string]any{
		"kind":     sliceActionDraftCode,
		"agent_id": explicitY,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateSliceAction: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp CreateSliceActionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.AgentID != explicitY {
		t.Errorf("explicit agent_id must win over the assignee: expected %s, got %s", explicitY, resp.AgentID)
	}
	if !strings.Contains(resp.Comment.Content, "mention://agent/"+explicitY) {
		t.Errorf("comment must mention explicit agent Y %s, got: %q", explicitY, resp.Comment.Content)
	}
	if strings.Contains(resp.Comment.Content, "mention://agent/"+assigneeX) {
		t.Errorf("comment must NOT mention assignee X %s, got: %q", assigneeX, resp.Comment.Content)
	}
	if got := countPendingTasksForAgent(t, issueID, explicitY); got != 1 {
		t.Errorf("expected exactly 1 queued task for explicit agent Y, got %d", got)
	}
	if got := countPendingTasksForAgent(t, issueID, assigneeX); got != 0 {
		t.Errorf("expected 0 queued tasks for assignee X (suppressed), got %d", got)
	}
}

func TestQATierScopeClause(t *testing.T) {
	// Full-scope change → no extra clause (the whole gate runs).
	if got := qaTierScopeClause(qaScopeFull); got != "" {
		t.Errorf("full scope should add no clause, got: %q", got)
	}
	// Trivial change → a LIGHT-scope directive: fast smoke, skip baseline, no
	// broad e2e authoring — but the gate still runs (verification never skipped).
	c := qaTierScopeClause(qaScopeLight)
	// Sheds ceremony...
	for _, want := range []string{"LIGHT", "DETERMINISTIC SMOKE", "Do NOT check out the merge-base", "do NOT author a broad new e2e suite"} {
		if !strings.Contains(c, want) {
			t.Errorf("trivial clause missing %q, got: %s", want, c)
		}
	}
	// ...but KEEPS the real check: it must assert the SPECIFIC EXPECTED value
	// from the plan and explicitly forbid a hollow "renders" pass (the accuracy
	// gap that green-lit a wrong fix). This is the fast+accurate contract.
	for _, want := range []string{"SPECIFIC EXPECTED OUTCOME", "EXACT expected value", "NEVER pass on", "COVERAGE GAP"} {
		if !strings.Contains(c, want) {
			t.Errorf("trivial clause must keep the ground-truth assertion (%q), got: %s", want, c)
		}
	}
	// It must NOT tell the agent to skip verification entirely — the gate scales
	// depth, it does not disappear.
	if strings.Contains(strings.ToLower(c), "skip qa") || strings.Contains(strings.ToLower(c), "no gate") {
		t.Errorf("trivial clause must scale the gate, not skip it; got: %s", c)
	}

	// SELF-SIZED (unknown: no label, no PR — the sprint-mode gap): the agent must
	// be told to measure the diff from git FIRST, then run the SAME light body if
	// it is genuinely tiny — so a one-line sprint-branch change isn't stuck on the
	// full gate just because there is no PR to read.
	s := qaTierScopeClause(qaScopeSelf)
	for _, want := range []string{"SELF-SIZED", "git diff --numstat", "≤3 files", "≤15 changed lines", "DETERMINISTIC SMOKE", "FULL gate"} {
		if !strings.Contains(s, want) {
			t.Errorf("self-sized clause missing %q, got: %s", want, s)
		}
	}
}

// TestGenTestCountClause pins the authoring-side count scaling: full scope adds
// nothing (full matrix), light caps to a minimal positive+negative on one
// layer, and self-sized tells the agent to match the count to the change's
// actual size — the twin lever to qaTierScopeClause that stops a one-line
// change from generating a 4-case matrix the light gate then runs one by one.
func TestGenTestCountClause(t *testing.T) {
	// Full scope: no COUNT cap, but the LAYER rule is UNIVERSAL — a button-text
	// issue must not spawn [api]/[e2e] cases regardless of scope.
	full := genTestCountClause(qaScopeFull, false)
	if strings.Contains(full, "CASE COUNT") {
		t.Errorf("full scope must not cap the count, got: %q", full)
	}
	for _, want := range []string{"LAYER FOLLOWS THE CHANGE", "ONE [smoke]", "endpoint/response changed → ONE [api]", "Do NOT author cases for surfaces the issue did not touch"} {
		if !strings.Contains(full, want) {
			t.Errorf("universal layer clause missing %q", want)
		}
	}
	if strings.Contains(full, "SPRINT") {
		t.Errorf("non-sprint clause must not mention sprint, got: %q", full)
	}
	light := genTestCountClause(qaScopeLight, false)
	for _, want := range []string{"MINIMAL", "ONE positive", "ONE negative", "SINGLE layer"} {
		if !strings.Contains(light, want) {
			t.Errorf("light count clause missing %q, got: %s", want, light)
		}
	}
	self := genTestCountClause(qaScopeSelf, false)
	for _, want := range []string{"SIZE-AWARE", "git diff", "full positive+negative matrix"} {
		if !strings.Contains(self, want) {
			t.Errorf("self-sized count clause missing %q, got: %s", want, self)
		}
	}
	// Neither scaling clause may tell the agent to DROP negative cases wholesale
	// — the positive/negative discipline survives; only the fan-out shrinks.
	for _, c := range []string{light, self} {
		if strings.Contains(strings.ToLower(c), "skip negative") || strings.Contains(strings.ToLower(c), "no negative") {
			t.Errorf("count clause must keep positive+negative discipline, got: %s", c)
		}
	}

	// SPRINT MODE adds the deferral on top of the universal layer rule: the
	// broad cross-layer matrix belongs to the sprint-end regression.
	sprint := genTestCountClause(qaScopeFull, true)
	for _, want := range []string{"LAYER FOLLOWS THE CHANGE", "SPRINT MODE", "SPRINT-END regression"} {
		if !strings.Contains(sprint, want) {
			t.Errorf("sprint clause missing %q, got: %s", want, sprint)
		}
	}
	if got := genTestCountClause(qaScopeLight, true); !strings.Contains(got, "MINIMAL") ||
		!strings.Contains(got, "LAYER FOLLOWS THE CHANGE") || !strings.Contains(got, "SPRINT MODE") {
		t.Errorf("sprint+light must carry count + layer + sprint clauses, got: %s", got)
	}
}

// TestSliceActionStageGuard pins the stage "brain": a human dispatch that
// contradicts the pipeline state is refused with the reason + next step —
// QA on a finished/backlog task, QA re-run while the fix is still in dev,
// review on a failing or stale-QA task. force=true bypasses (checked at the
// handler layer; the guard itself only reports).
func TestSliceActionStageGuard(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ctx := context.Background()

	mk := func(status string, labels ...string) db.Issue {
		id := createTestIssue(t, "guard "+status+strings.Join(labels, ""), status, "medium")
		t.Cleanup(func() { deleteTestIssue(t, id) })
		for _, name := range labels {
			attachLabelToTestIssue(t, id, name)
		}
		issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(id))
		if err != nil {
			t.Fatalf("GetIssue: %v", err)
		}
		return issue
	}

	// run_qa refusals
	for _, tc := range []struct {
		issue db.Issue
		want  string
	}{
		{mk("done"), "already finished"},
		{mk("backlog"), "not been started"},
		{mk("in_progress"), "dev is not finished"},
		{mk("todo", "qa:fail"), "fix the failing cases first"},
	} {
		if got := testHandler.sliceActionStageGuard(ctx, tc.issue, sliceActionRunQA); !strings.Contains(got, tc.want) {
			t.Errorf("run_qa guard on %s = %q, want contains %q", tc.issue.Status, got, tc.want)
		}
	}
	// run_qa allowed in review
	if got := testHandler.sliceActionStageGuard(ctx, mk("in_review"), sliceActionRunQA); got != "" {
		t.Errorf("run_qa in_review must be allowed, got %q", got)
	}

	// run_review refusals
	if got := testHandler.sliceActionStageGuard(ctx, mk("in_review", "qa:fail"), sliceActionRunReview); !strings.Contains(got, "review is disabled") {
		t.Errorf("run_review with qa:fail = %q, want disabled", got)
	}
	if got := testHandler.sliceActionStageGuard(ctx, mk("in_review", "qa:stale"), sliceActionRunReview); !strings.Contains(got, "stale") {
		t.Errorf("run_review with qa:stale = %q, want stale warning", got)
	}
	if got := testHandler.sliceActionStageGuard(ctx, mk("done"), sliceActionRunReview); !strings.Contains(got, "already finished") {
		t.Errorf("run_review on done = %q", got)
	}
	// run_review allowed on green
	if got := testHandler.sliceActionStageGuard(ctx, mk("in_review", "qa:pass"), sliceActionRunReview); got != "" {
		t.Errorf("run_review with qa:pass must be allowed, got %q", got)
	}
}
