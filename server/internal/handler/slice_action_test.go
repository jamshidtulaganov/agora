package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestBuildSliceInstruction is a pure unit test of the single source of truth
// for slice-action wording. It runs without a database. For every supported
// kind it asserts the template asks the human to REVIEW and explicitly tells
// the agent NOT to merge, that the optional scope is appended as a "Focus on:"
// clause, and that an unknown kind renders nothing.
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
	for _, banned := range []string{"sd-qa-process", "btx-", "dev test box"} {
		if strings.Contains(lower, strings.ToLower(banned)) {
			t.Errorf("run_qa instruction must stay product-neutral; found %q, got: %s", banned, got)
		}
	}
	if sliceActionOpensPR(sliceActionRunQA) {
		t.Error("run_qa opens no PR; must be excluded from the branch-hint set")
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
