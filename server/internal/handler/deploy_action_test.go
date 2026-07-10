package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestDeploySliceActionKind: deploy is a member of the closed slice-action
// kind set and renders a non-empty instruction carrying the write-back
// contract (the fenced deploy-result block the server parses).
func TestDeploySliceActionKind(t *testing.T) {
	if !isKnownSliceActionKind(sliceActionDeploy) {
		t.Fatal("deploy must be a known slice action kind")
	}
	got := buildSliceInstruction(sliceActionDeploy, "")
	if got == "" {
		t.Fatal("deploy buildSliceInstruction empty — template not wired")
	}
	for _, want := range []string{
		"```deploy-result```",
		`"status":"success"|"failed"|"timeout"`,
		"Do NOT merge",
		"DEPLOY OPERATOR",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("deploy instruction missing %q", want)
		}
	}
	// The environment key rides as the scope ("Focus on: <key>").
	if got := buildSliceInstruction(sliceActionDeploy, "staging"); !strings.Contains(got, "Focus on: staging") {
		t.Errorf("deploy instruction with scope should carry the focus clause, got tail %q", got[len(got)-80:])
	}
}

// TestParseDeployEnvironments covers the defensive JSONB parse: valid lists
// parse, malformed blobs/entries degrade instead of erroring, keyless entries
// are dropped, and the env-level kind wins over target.kind.
func TestParseDeployEnvironments(t *testing.T) {
	t.Run("valid two-environment list", func(t *testing.T) {
		raw := []byte(`{"deploy_environments":[
			{"key":"staging","label":"Staging","kind":"gitlab_pipeline",
			 "target":{"project_path":"salesdoctor/sd-main","ref":"staging","environment":"staging"}},
			{"key":"production","label":"Production","kind":"gitlab_pipeline",
			 "target":{"project_path":"salesdoctor/sd-main","ref":"main"},"requires_human":true}
		]}`)
		envs := parseDeployEnvironments(raw)
		if len(envs) != 2 {
			t.Fatalf("expected 2 environments, got %d", len(envs))
		}
		if envs[0].Key != "staging" || envs[0].Target.ProjectPath != "salesdoctor/sd-main" || envs[0].Target.Ref != "staging" {
			t.Errorf("unexpected staging env: %+v", envs[0])
		}
		if !envs[1].RequiresHuman {
			t.Error("production env should carry requires_human")
		}
	})

	t.Run("malformed settings blob", func(t *testing.T) {
		if envs := parseDeployEnvironments([]byte(`{not json`)); envs != nil {
			t.Errorf("expected nil for malformed settings, got %+v", envs)
		}
	})

	t.Run("deploy_environments is not an array", func(t *testing.T) {
		if envs := parseDeployEnvironments([]byte(`{"deploy_environments":"staging"}`)); envs != nil {
			t.Errorf("expected nil for non-array value, got %+v", envs)
		}
	})

	t.Run("one malformed entry does not hide its siblings", func(t *testing.T) {
		raw := []byte(`{"deploy_environments":[
			{"key":"staging","target":{"command":"make deploy"}},
			{"key":"broken","requires_human":"yes"},
			{"label":"keyless entry is dropped"}
		]}`)
		envs := parseDeployEnvironments(raw)
		if len(envs) != 1 || envs[0].Key != "staging" {
			t.Errorf("expected only the valid staging entry, got %+v", envs)
		}
	})

	t.Run("kind tolerated inside target", func(t *testing.T) {
		raw := []byte(`{"deploy_environments":[
			{"key":"staging","target":{"kind":"gitlab_pipeline","project_path":"g/p","ref":"main"}}
		]}`)
		envs := parseDeployEnvironments(raw)
		if len(envs) != 1 || envs[0].targetKind() != "gitlab_pipeline" {
			t.Errorf("expected target.kind fallback, got %+v", envs)
		}
	})

	t.Run("empty settings", func(t *testing.T) {
		if envs := parseDeployEnvironments(nil); envs != nil {
			t.Errorf("expected nil for empty settings, got %+v", envs)
		}
	})
}

// TestDeployEnvironmentRequiresHuman: the explicit flag gates, and a
// production-named key gates even without the flag (defense in depth).
func TestDeployEnvironmentRequiresHuman(t *testing.T) {
	cases := []struct {
		env  deployEnvironment
		want bool
	}{
		{deployEnvironment{Key: "staging"}, false},
		{deployEnvironment{Key: "staging", RequiresHuman: true}, true},
		{deployEnvironment{Key: "production"}, true},
		{deployEnvironment{Key: "PROD"}, true},
		{deployEnvironment{Key: " Production "}, true},
		{deployEnvironment{Key: "qa"}, false},
	}
	for _, c := range cases {
		if got := deployEnvironmentRequiresHuman(c.env); got != c.want {
			t.Errorf("requiresHuman(%q, flag=%v) = %v, want %v", c.env.Key, c.env.RequiresHuman, got, c.want)
		}
	}
}

// TestDeployTargetClause: the gitlab_pipeline target renders the MCP
// pipeline contract (tool names, server-computed ref, write-back values),
// the command target renders the exit-code contract, and an environment
// with no usable target reports not-ok so the handler 400s.
func TestDeployTargetClause(t *testing.T) {
	t.Run("gitlab pipeline target", func(t *testing.T) {
		clause, ok := deployTargetClause(deployEnvironment{
			Key: "staging", Label: "Staging", Kind: "gitlab_pipeline",
			Target: deployEnvironmentTarget{ProjectPath: "salesdoctor/sd-main", Ref: "staging", Environment: "staging"},
		})
		if !ok {
			t.Fatal("expected a usable clause")
		}
		for _, want := range []string{
			"DEPLOY ENVIRONMENT: `staging`",
			"salesdoctor/sd-main",
			"`create_pipeline`",
			"`get_pipeline`",
			"status=\"timeout\"",
			"NEVER call `retry_pipeline`",
			`environment="staging"`,
			`ref="staging"`,
		} {
			if !strings.Contains(clause, want) {
				t.Errorf("gitlab clause missing %q", want)
			}
		}
	})

	t.Run("command target", func(t *testing.T) {
		clause, ok := deployTargetClause(deployEnvironment{
			Key:    "staging",
			Target: deployEnvironmentTarget{Command: "make deploy", Ref: "main"},
		})
		if !ok {
			t.Fatal("expected a usable clause")
		}
		for _, want := range []string{"DEPLOY TARGET = COMMAND", "`make deploy`", "EXIT CODE", `environment="staging"`} {
			if !strings.Contains(clause, want) {
				t.Errorf("command clause missing %q", want)
			}
		}
	})

	t.Run("gitlab target with command fallback", func(t *testing.T) {
		clause, ok := deployTargetClause(deployEnvironment{
			Key: "staging", Kind: "gitlab_pipeline",
			Target: deployEnvironmentTarget{ProjectPath: "g/p", Ref: "main", Command: "make deploy"},
		})
		if !ok || !strings.Contains(clause, "FALLBACK") || !strings.Contains(clause, "`make deploy`") {
			t.Errorf("expected the MCP-unavailable command fallback, got ok=%v clause=%q", ok, clause)
		}
	})

	t.Run("no usable target", func(t *testing.T) {
		if _, ok := deployTargetClause(deployEnvironment{Key: "staging", Kind: "gitlab_pipeline"}); ok {
			t.Error("an environment without project_path/ref or command must not be usable")
		}
	})
}

// deployTestProject inserts a project with the given settings and links the
// issue to it. Returns nothing — cleanup cascades with the workspace, and the
// issue row is deleted by the caller's own cleanup.
func deployTestProject(t *testing.T, ctx context.Context, issueID, settings string) {
	t.Helper()
	var projectID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title, status, settings) VALUES ($1, $2, 'in_progress', $3::jsonb) RETURNING id`,
		testWorkspaceID, "deploy-action-test", settings,
	).Scan(&projectID); err != nil {
		t.Fatalf("insert test project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID)
	})
	if _, err := testPool.Exec(ctx, `UPDATE issue SET project_id = $1 WHERE id = $2`, projectID, issueID); err != nil {
		t.Fatalf("link issue to project: %v", err)
	}
}

const deployTestEnvSettings = `{"deploy_environments":[
	{"key":"staging","label":"Staging","kind":"gitlab_pipeline",
	 "target":{"project_path":"salesdoctor/sd-main","ref":"staging","environment":"staging"}},
	{"key":"production","label":"Production","kind":"gitlab_pipeline",
	 "target":{"project_path":"salesdoctor/sd-main","ref":"main"},"requires_human":true}
]}`

// TestCreateSliceAction_Deploy covers the HTTP surface: environment
// resolution from the scope, the rendered pipeline contract, the 4xx paths,
// and — the safety-critical piece — the server-side production human gate
// (a machine actor is 403'd for requires_human environments regardless of
// what any UI shows).
func TestCreateSliceAction_Deploy(t *testing.T) {
	ctx := context.Background()

	fire := func(t *testing.T, issueID, scope string, machine bool) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/"+issueID+"/slice-actions", map[string]any{
			"kind":  "deploy",
			"scope": scope,
		})
		if machine {
			// The auth middleware stamps this for mat_/mcn_ credentials; in a
			// handler-level test we stamp it directly (server-set semantics).
			req.Header.Set("X-Actor-Source", "task_token")
		}
		req = withURLParam(req, "id", issueID)
		testHandler.CreateSliceAction(w, req)
		return w
	}

	t.Run("human fires a staging deploy", func(t *testing.T) {
		issueID := createTestIssue(t, "deploy slice staging", "in_review", "medium")
		t.Cleanup(func() { deleteTestIssue(t, issueID) })
		deployTestProject(t, ctx, issueID, deployTestEnvSettings)

		w := fire(t, issueID, "staging", false)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}
		var resp CreateSliceActionResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		for _, want := range []string{
			"DEPLOY TARGET = GitLab CI/CD pipeline (MCP)",
			"salesdoctor/sd-main",
			"`staging`",
			"```deploy-result```",
		} {
			if !strings.Contains(resp.Instruction, want) {
				t.Errorf("instruction missing %q", want)
			}
		}
		if resp.Scope != "staging" {
			t.Errorf("scope = %q, want staging", resp.Scope)
		}
	})

	t.Run("machine actor may fire a non-production deploy", func(t *testing.T) {
		issueID := createTestIssue(t, "deploy slice machine staging", "in_review", "medium")
		t.Cleanup(func() { deleteTestIssue(t, issueID) })
		deployTestProject(t, ctx, issueID, deployTestEnvSettings)

		if w := fire(t, issueID, "staging", true); w.Code != http.StatusCreated {
			t.Fatalf("expected 201 for machine actor on staging, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("machine actor is rejected for production", func(t *testing.T) {
		issueID := createTestIssue(t, "deploy slice machine prod", "in_review", "medium")
		t.Cleanup(func() { deleteTestIssue(t, issueID) })
		deployTestProject(t, ctx, issueID, deployTestEnvSettings)

		w := fire(t, issueID, "production", true)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for a machine actor firing production, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("human may fire production", func(t *testing.T) {
		issueID := createTestIssue(t, "deploy slice human prod", "in_review", "medium")
		t.Cleanup(func() { deleteTestIssue(t, issueID) })
		deployTestProject(t, ctx, issueID, deployTestEnvSettings)

		if w := fire(t, issueID, "production", false); w.Code != http.StatusCreated {
			t.Fatalf("expected 201 for a human firing production, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("unknown environment is a 400", func(t *testing.T) {
		issueID := createTestIssue(t, "deploy slice unknown env", "in_review", "medium")
		t.Cleanup(func() { deleteTestIssue(t, issueID) })
		deployTestProject(t, ctx, issueID, deployTestEnvSettings)

		if w := fire(t, issueID, "nosuch", false); w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for an unknown environment, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("no configured environments is a 400", func(t *testing.T) {
		issueID := createTestIssue(t, "deploy slice no envs", "in_review", "medium")
		t.Cleanup(func() { deleteTestIssue(t, issueID) })
		deployTestProject(t, ctx, issueID, `{}`)

		if w := fire(t, issueID, "staging", false); w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 when the project configures no environments, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("unusable target is a 400", func(t *testing.T) {
		issueID := createTestIssue(t, "deploy slice unusable target", "in_review", "medium")
		t.Cleanup(func() { deleteTestIssue(t, issueID) })
		deployTestProject(t, ctx, issueID, `{"deploy_environments":[{"key":"staging","kind":"gitlab_pipeline"}]}`)

		if w := fire(t, issueID, "staging", false); w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for an environment with no usable target, got %d: %s", w.Code, w.Body.String())
		}
	})
}

// TestSanitizeDeployRef: the ref-override allowlist accepts git-ref-shaped
// values and rejects anything that could break out of the instruction's
// code span or smuggle a mention.
func TestSanitizeDeployRef(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"sprint-9", "sprint-9"},
		{"sprint/9f2c1a", "sprint/9f2c1a"},
		{"  billing  ", "billing"},
		{"release-2.4", "release-2.4"},
		{"", ""},
		{"has space", ""},
		{"evil`ref", ""},
		{"a]b(c)", ""},
		{"-leading-dash", ""},
		{"line\nbreak", ""},
		{strings.Repeat("a", 201), ""},
	}
	for _, c := range cases {
		if got := sanitizeDeployRef(c.in); got != c.want {
			t.Errorf("sanitizeDeployRef(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestCreateSliceAction_DeployRefOverride: the sprint panel's ref threading —
// a valid ref override replaces the environment's configured target.ref in
// the rendered contract; an invalid one falls back to the configured ref;
// and the production human gate is unaffected by the override.
func TestCreateSliceAction_DeployRefOverride(t *testing.T) {
	ctx := context.Background()

	fire := func(t *testing.T, issueID, scope, ref string, machine bool) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues/"+issueID+"/slice-actions", map[string]any{
			"kind":  "deploy",
			"scope": scope,
			"ref":   ref,
		})
		if machine {
			req.Header.Set("X-Actor-Source", "task_token")
		}
		req = withURLParam(req, "id", issueID)
		testHandler.CreateSliceAction(w, req)
		return w
	}

	t.Run("a valid ref override replaces the configured target ref", func(t *testing.T) {
		issueID := createTestIssue(t, "deploy ref override", "in_review", "medium")
		t.Cleanup(func() { deleteTestIssue(t, issueID) })
		deployTestProject(t, ctx, issueID, deployTestEnvSettings)

		w := fire(t, issueID, "staging", "sprint/test-sprint-1", false)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}
		var resp CreateSliceActionResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		for _, want := range []string{
			"on ref `sprint/test-sprint-1`",
			`ref="sprint/test-sprint-1"`,
		} {
			if !strings.Contains(resp.Instruction, want) {
				t.Errorf("instruction missing %q — the ref override did not thread through", want)
			}
		}
		if strings.Contains(resp.Instruction, "ref `staging`") {
			t.Error("instruction still names the environment's static ref despite the override")
		}
	})

	t.Run("an invalid ref falls back to the configured target ref", func(t *testing.T) {
		issueID := createTestIssue(t, "deploy ref invalid", "in_review", "medium")
		t.Cleanup(func() { deleteTestIssue(t, issueID) })
		deployTestProject(t, ctx, issueID, deployTestEnvSettings)

		w := fire(t, issueID, "staging", "evil`ref with spaces", false)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}
		var resp CreateSliceActionResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !strings.Contains(resp.Instruction, "on ref `staging`") {
			t.Error("instruction should keep the configured ref when the override is invalid")
		}
		if strings.Contains(resp.Instruction, "evil") {
			t.Error("an invalid ref must never reach the instruction")
		}
	})

	t.Run("the production human gate is unaffected by a ref override", func(t *testing.T) {
		issueID := createTestIssue(t, "deploy ref prod gate", "in_review", "medium")
		t.Cleanup(func() { deleteTestIssue(t, issueID) })
		deployTestProject(t, ctx, issueID, deployTestEnvSettings)

		if w := fire(t, issueID, "production", "sprint/test-sprint-1", true); w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for a machine actor firing production with a ref, got %d: %s", w.Code, w.Body.String())
		}
	})
}

// TestTaskTriggerIsDeploy: the claim path attaches GitLab MCP tools ONLY to
// tasks whose triggering comment is a deploy slice-action dispatch.
func TestTaskTriggerIsDeploy(t *testing.T) {
	ctx := context.Background()
	issueID := createTestIssue(t, "deploy trigger detection", "in_review", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	mkComment := func(t *testing.T, content string) db.Comment {
		t.Helper()
		c, err := testHandler.Queries.CreateComment(ctx, db.CreateCommentParams{
			IssueID:     testUUID(issueID),
			WorkspaceID: testUUID(testWorkspaceID),
			AuthorType:  "member",
			AuthorID:    testUUID(testUserID),
			Content:     content,
			Type:        "comment",
		})
		if err != nil {
			t.Fatalf("create comment: %v", err)
		}
		return c
	}

	deployComment := mkComment(t, agentProtocolMarker(sliceActionDeploy)+"[@Agent](mention://agent/x) deploy staging")
	if !testHandler.taskTriggerIsDeploy(ctx, deployComment.ID) {
		t.Error("a deploy-marked trigger comment must be detected")
	}

	qaComment := mkComment(t, agentProtocolMarker(sliceActionRunQA)+"[@Agent](mention://agent/x) run the gate")
	if testHandler.taskTriggerIsDeploy(ctx, qaComment.ID) {
		t.Error("a run_qa trigger comment must NOT read as deploy")
	}

	plain := mkComment(t, "just a comment")
	if testHandler.taskTriggerIsDeploy(ctx, plain.ID) {
		t.Error("an ordinary comment must NOT read as deploy")
	}
}

// TestCaptureDeployEvent: the fenced deploy-result block becomes a durable
// deploy_event row; re-capturing the same terminal content is idempotent; a
// malformed block or unknown status is logged and skipped, never crashing the
// comment path.
func TestCaptureDeployEvent(t *testing.T) {
	ctx := context.Background()

	countEvents := func(t *testing.T, issueID string) int {
		t.Helper()
		rows, err := testHandler.Queries.ListDeployEventsForIssue(ctx, db.ListDeployEventsForIssueParams{
			IssueID:     testUUID(issueID),
			WorkspaceID: testUUID(testWorkspaceID),
			Limit:       50,
		})
		if err != nil {
			t.Fatalf("list deploy events: %v", err)
		}
		return len(rows)
	}

	issueFor := func(t *testing.T, title string) (db.Issue, string) {
		t.Helper()
		issueID := createTestIssue(t, title, "in_review", "medium")
		t.Cleanup(func() { deleteTestIssue(t, issueID) })
		issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
		if err != nil {
			t.Fatalf("get issue: %v", err)
		}
		return issue, issueID
	}

	t.Run("valid block persists and re-capture is idempotent", func(t *testing.T) {
		issue, issueID := issueFor(t, "deploy capture happy path")
		content := "Deployed staging.\n\n```deploy-result\n" +
			`{"environment":"staging","ref":"main","status":"success","summary":"pipeline green","pipeline_url":"https://gitlab.example/p/-/pipelines/1","duration_s":184}` +
			"\n```\n"

		testHandler.TaskService.CaptureDeployEvent(ctx, issue, content)
		if got := countEvents(t, issueID); got != 1 {
			t.Fatalf("expected 1 deploy event, got %d", got)
		}
		latest, err := testHandler.Queries.GetLatestDeployEventForIssue(ctx, db.GetLatestDeployEventForIssueParams{
			IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
		})
		if err != nil {
			t.Fatalf("get latest: %v", err)
		}
		if latest.Target != "staging" || latest.Ref != "main" || latest.Status != "success" || latest.Summary != "pipeline green" {
			t.Errorf("unexpected row: %+v", latest)
		}

		// The same terminal content can flow through both the comment path and
		// the final-result capture path — the second capture must not duplicate.
		testHandler.TaskService.CaptureDeployEvent(ctx, issue, content)
		if got := countEvents(t, issueID); got != 1 {
			t.Errorf("expected idempotent re-capture (1 row), got %d", got)
		}
	})

	t.Run("a failed result with a fresh outcome writes a new row", func(t *testing.T) {
		issue, issueID := issueFor(t, "deploy capture new outcome")
		ok := "```deploy-result\n" + `{"environment":"staging","ref":"main","status":"success","summary":"green"}` + "\n```"
		bad := "```deploy-result\n" + `{"environment":"staging","ref":"main","status":"failed","summary":"job build failed"}` + "\n```"
		testHandler.TaskService.CaptureDeployEvent(ctx, issue, ok)
		testHandler.TaskService.CaptureDeployEvent(ctx, issue, bad)
		if got := countEvents(t, issueID); got != 2 {
			t.Fatalf("expected 2 rows for two distinct outcomes, got %d", got)
		}
	})

	t.Run("malformed JSON is skipped", func(t *testing.T) {
		issue, issueID := issueFor(t, "deploy capture malformed")
		testHandler.TaskService.CaptureDeployEvent(ctx, issue, "```deploy-result\n{not json}\n```")
		if got := countEvents(t, issueID); got != 0 {
			t.Errorf("expected no rows for malformed JSON, got %d", got)
		}
	})

	t.Run("unknown status is skipped", func(t *testing.T) {
		issue, issueID := issueFor(t, "deploy capture bad status")
		testHandler.TaskService.CaptureDeployEvent(ctx, issue, "```deploy-result\n"+`{"environment":"staging","status":"maybe"}`+"\n```")
		if got := countEvents(t, issueID); got != 0 {
			t.Errorf("expected no rows for an unknown status, got %d", got)
		}
	})

	t.Run("no block is a no-op", func(t *testing.T) {
		issue, issueID := issueFor(t, "deploy capture no block")
		testHandler.TaskService.CaptureDeployEvent(ctx, issue, "an ordinary progress comment")
		if got := countEvents(t, issueID); got != 0 {
			t.Errorf("expected no rows without a block, got %d", got)
		}
	})

	t.Run("summary falls back to the pipeline url", func(t *testing.T) {
		issue, _ := issueFor(t, "deploy capture summary fallback")
		testHandler.TaskService.CaptureDeployEvent(ctx, issue,
			"```deploy-result\n"+`{"environment":"staging","ref":"main","status":"timeout","pipeline_url":"https://gitlab.example/p/-/pipelines/2"}`+"\n```")
		latest, err := testHandler.Queries.GetLatestDeployEventForIssue(ctx, db.GetLatestDeployEventForIssueParams{
			IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
		})
		if err != nil {
			t.Fatalf("get latest: %v", err)
		}
		if latest.Status != "timeout" || latest.Summary != "https://gitlab.example/p/-/pipelines/2" {
			t.Errorf("unexpected row: %+v", latest)
		}
	})
}
