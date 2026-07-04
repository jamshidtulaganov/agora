package handler

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Capture / provisioning tests for the KB flywheel. maybeEnqueueKnowledgeCapture
// and resolveKBSynthesizer are unexported handler methods, so these tests drive
// testHandler directly (the shared handler_test.go fixture) and assert on the
// side effects: the workspace.settings stamp, the presence/absence of a
// "KB Synthesizer" agent, the trigger comment, and the enqueued task row.
//
// The tests mutate workspace.settings and provision agents in the shared test
// workspace, so each snapshots+restores settings and deletes any agent it
// (or the code under test) created. They run serially (no t.Parallel), so
// settings mutation is safe.

// kbSaveWorkspaceSettings snapshots the shared workspace's settings and
// restores them on test end, so a provisioning stamp does not leak into the
// "no setting" branch of a sibling test.
func kbSaveWorkspaceSettings(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	var settings []byte
	if err := testPool.QueryRow(ctx, `SELECT settings FROM workspace WHERE id = $1`, testWorkspaceID).Scan(&settings); err != nil {
		t.Fatalf("snapshot workspace settings: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `UPDATE workspace SET settings = $2 WHERE id = $1`, testWorkspaceID, settings)
	})
}

// kbClearWorkspaceSettings wipes settings to {} for the "no explicit setting"
// branch. Paired with kbSaveWorkspaceSettings for restore.
func kbClearWorkspaceSettings(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `UPDATE workspace SET settings = '{}'::jsonb WHERE id = $1`, testWorkspaceID); err != nil {
		t.Fatalf("clear workspace settings: %v", err)
	}
}

// kbDeleteSynthesizerAgents removes any "KB Synthesizer" agents in the shared
// workspace (auto-provisioned by the code under test or seeded by the test).
func kbDeleteSynthesizerAgents(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM agent WHERE workspace_id = $1 AND name = $2`, testWorkspaceID, kbSynthesizerAgentName)
	})
}

// kbCaptureIssue seeds a project (with a resolvable KB name) plus an issue and
// returns the loaded db.Issue. The issue is assigned to the given agent so
// pickKBSynthRuntime can prefer that agent's runtime.
func kbCaptureIssue(t *testing.T, assigneeAgentID string) db.Issue {
	t.Helper()
	ctx := context.Background()
	projectID := kbTestProject(t, fmt.Sprintf("KB Capture Project %d", time.Now().UnixNano()))
	var assigneeType, assignee any
	if assigneeAgentID != "" {
		assigneeType, assignee = "agent", assigneeAgentID
	}
	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (
			workspace_id, project_id, title, description, status, priority,
			creator_type, creator_id, assignee_type, assignee_id, number
		)
		VALUES ($1, $2, 'Capture issue', 'A short description', 'done', 'medium',
			'member', $3, $4, $5, $6)
		RETURNING id
	`, testWorkspaceID, projectID, testUserID, assigneeType, assignee,
		int32(time.Now().UnixNano()%1_000_000_000)).Scan(&issueID); err != nil {
		t.Fatalf("insert capture issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
		testPool.Exec(ctx, `DELETE FROM comment WHERE issue_id = $1`, issueID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID)
	})
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("load capture issue: %v", err)
	}
	return issue
}

// kbSeedSynthesizerAgent inserts a "KB Synthesizer" agent bound to the shared
// online runtime and returns its id. archived controls archived_at.
func kbSeedSynthesizerAgent(t *testing.T, archived bool) string {
	t.Helper()
	ctx := context.Background()
	archivedExpr := "NULL"
	if archived {
		archivedExpr = "now()"
	}
	var id string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args, archived_at
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'workspace', 3, NULL,
			'', '{}'::jsonb, '[]'::jsonb, `+archivedExpr+`)
		RETURNING id
	`, testWorkspaceID, kbSynthesizerAgentName, testRuntimeID).Scan(&id); err != nil {
		t.Fatalf("seed synthesizer agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent WHERE id = $1`, id)
	})
	return id
}

// kbWorkspaceSynthesizerStamp reads the persisted kb_synthesizer_agent_id, "" if unset.
func kbWorkspaceSynthesizerStamp(t *testing.T) string {
	t.Helper()
	var stamp string
	if err := testPool.QueryRow(context.Background(),
		`SELECT COALESCE(settings->>'kb_synthesizer_agent_id', '') FROM workspace WHERE id = $1`,
		testWorkspaceID,
	).Scan(&stamp); err != nil {
		t.Fatalf("read synthesizer stamp: %v", err)
	}
	return stamp
}

func kbCountEnqueuedTasksForIssue(t *testing.T, issueID pgtype.UUID) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_task_queue WHERE issue_id = $1`, uuidToString(issueID),
	).Scan(&n); err != nil {
		t.Fatalf("count enqueued tasks: %v", err)
	}
	return n
}

func kbCountSynthAgents(t *testing.T) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent WHERE workspace_id = $1 AND name = $2`, testWorkspaceID, kbSynthesizerAgentName,
	).Scan(&n); err != nil {
		t.Fatalf("count synthesizer agents: %v", err)
	}
	return n
}

func TestMaybeEnqueueKnowledgeCaptureResolvesExplicitSetting(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	kbSaveWorkspaceSettings(t)
	kbDeleteSynthesizerAgents(t)

	// An explicitly-stamped (non-"KB Synthesizer"-named) live agent is used
	// verbatim: back-compat with workspaces that opted in by hand.
	agentID := createHandlerTestAgent(t, fmt.Sprintf("explicit-synth-%d", time.Now().UnixNano()), []byte(`{}`))
	if _, err := testPool.Exec(context.Background(),
		`UPDATE workspace SET settings = jsonb_build_object('kb_synthesizer_agent_id', $2::text) WHERE id = $1`,
		testWorkspaceID, agentID,
	); err != nil {
		t.Fatalf("stamp explicit setting: %v", err)
	}

	issue := kbCaptureIssue(t, "")
	testHandler.maybeEnqueueKnowledgeCapture(context.Background(), issue)

	// The stamped agent got the task; no "KB Synthesizer" was provisioned.
	if kbCountSynthAgents(t) != 0 {
		t.Fatalf("explicit stamp must not provision a KB Synthesizer agent")
	}
	var taskAgent string
	if err := testPool.QueryRow(context.Background(),
		`SELECT agent_id FROM agent_task_queue WHERE issue_id = $1 LIMIT 1`, uuidToString(issue.ID),
	).Scan(&taskAgent); err != nil {
		t.Fatalf("expected an enqueued task for the stamped agent: %v", err)
	}
	if taskAgent != agentID {
		t.Fatalf("enqueued task agent = %s, want stamped agent %s", taskAgent, agentID)
	}
}

func TestMaybeEnqueueKnowledgeCaptureArchivedSynthesizerOptsOut(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	kbSaveWorkspaceSettings(t)
	kbClearWorkspaceSettings(t)
	kbDeleteSynthesizerAgents(t)

	// Archived "KB Synthesizer" found by name → opt-out: no new agent, no task,
	// no 409 loop.
	archivedID := kbSeedSynthesizerAgent(t, true)
	issue := kbCaptureIssue(t, "")
	testHandler.maybeEnqueueKnowledgeCapture(context.Background(), issue)

	if kbCountSynthAgents(t) != 1 {
		t.Fatalf("archived opt-out must NOT create another KB Synthesizer (count=%d)", kbCountSynthAgents(t))
	}
	if n := kbCountEnqueuedTasksForIssue(t, issue.ID); n != 0 {
		t.Fatalf("archived opt-out must enqueue no task, got %d", n)
	}
	// Also verify the stamped-archived path opts out identically.
	if _, err := testPool.Exec(context.Background(),
		`UPDATE workspace SET settings = jsonb_build_object('kb_synthesizer_agent_id', $2::text) WHERE id = $1`,
		testWorkspaceID, archivedID,
	); err != nil {
		t.Fatalf("stamp archived agent: %v", err)
	}
	issue2 := kbCaptureIssue(t, "")
	testHandler.maybeEnqueueKnowledgeCapture(context.Background(), issue2)
	if n := kbCountEnqueuedTasksForIssue(t, issue2.ID); n != 0 {
		t.Fatalf("stamped-archived opt-out must enqueue no task, got %d", n)
	}
}

func TestMaybeEnqueueKnowledgeCaptureFindsByNameAndStamps(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	kbSaveWorkspaceSettings(t)
	kbClearWorkspaceSettings(t)
	kbDeleteSynthesizerAgents(t)

	// No stamp, but a live "KB Synthesizer" exists → adopt it AND stamp its UUID.
	namedID := kbSeedSynthesizerAgent(t, false)
	issue := kbCaptureIssue(t, "")
	testHandler.maybeEnqueueKnowledgeCapture(context.Background(), issue)

	if got := kbWorkspaceSynthesizerStamp(t); got != namedID {
		t.Fatalf("find-by-name must stamp the adopted agent UUID: got %q want %q", got, namedID)
	}
	if kbCountSynthAgents(t) != 1 {
		t.Fatalf("find-by-name must reuse the existing agent, not provision a second")
	}
	var taskAgent string
	if err := testPool.QueryRow(context.Background(),
		`SELECT agent_id FROM agent_task_queue WHERE issue_id = $1 LIMIT 1`, uuidToString(issue.ID),
	).Scan(&taskAgent); err != nil {
		t.Fatalf("expected an enqueued task: %v", err)
	}
	if taskAgent != namedID {
		t.Fatalf("task agent = %s, want named synthesizer %s", taskAgent, namedID)
	}

	// Second call now short-circuits on the stamp (step 1) — still the same agent.
	issue2 := kbCaptureIssue(t, "")
	testHandler.maybeEnqueueKnowledgeCapture(context.Background(), issue2)
	if kbCountSynthAgents(t) != 1 {
		t.Fatalf("second call must not provision another agent")
	}
}

func TestMaybeEnqueueKnowledgeCaptureAutoProvisions(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	kbSaveWorkspaceSettings(t)
	kbClearWorkspaceSettings(t)
	kbDeleteSynthesizerAgents(t)

	// No stamp, no named agent, an online runtime exists → auto-provision.
	issue := kbCaptureIssue(t, "")
	testHandler.maybeEnqueueKnowledgeCapture(context.Background(), issue)

	if kbCountSynthAgents(t) != 1 {
		t.Fatalf("auto-provision must create exactly one KB Synthesizer, got %d", kbCountSynthAgents(t))
	}
	var provisionedID, model, runtimeID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT id, COALESCE(model, ''), COALESCE(runtime_id::text, '') FROM agent WHERE workspace_id = $1 AND name = $2`,
		testWorkspaceID, kbSynthesizerAgentName,
	).Scan(&provisionedID, &model, &runtimeID); err != nil {
		t.Fatalf("load provisioned agent: %v", err)
	}
	// pickKBSynthRuntime chooses the most-recently-seen online runtime in the
	// shared workspace, which is not necessarily the fixture runtime (sibling
	// tests leave their own online runtimes behind). Assert the model against the
	// provider of the runtime the agent was ACTUALLY provisioned on, which is the
	// only guarantee the code makes.
	if runtimeID == "" {
		t.Fatalf("provisioned synthesizer must be bound to a runtime")
	}
	var provider string
	if err := testPool.QueryRow(context.Background(),
		`SELECT provider FROM agent_runtime WHERE id = $1`, runtimeID,
	).Scan(&provider); err != nil {
		t.Fatalf("load runtime provider: %v", err)
	}
	if want := kbSynthModelForProvider(provider); model != want {
		t.Fatalf("provisioned model = %q, want %q for provider %q", model, want, provider)
	}
	// UUID stamped.
	if got := kbWorkspaceSynthesizerStamp(t); got != provisionedID {
		t.Fatalf("auto-provision must stamp the new UUID: got %q want %q", got, provisionedID)
	}
	// Trigger comment authored by the synthesizer.
	var commentCount int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM comment WHERE issue_id = $1 AND author_type = 'agent' AND author_id = $2`,
		uuidToString(issue.ID), provisionedID,
	).Scan(&commentCount); err != nil {
		t.Fatalf("count trigger comment: %v", err)
	}
	if commentCount != 1 {
		t.Fatalf("expected 1 trigger comment authored by synthesizer, got %d", commentCount)
	}
	// Task enqueued.
	if n := kbCountEnqueuedTasksForIssue(t, issue.ID); n != 1 {
		t.Fatalf("expected 1 enqueued task, got %d", n)
	}

	// 409 race convergence: a second capture with the stamp cleared but the
	// agent already present resolves via find-by-name and re-stamps — no
	// duplicate agent, no error surfaced.
	kbClearWorkspaceSettings(t)
	issue2 := kbCaptureIssue(t, "")
	testHandler.maybeEnqueueKnowledgeCapture(context.Background(), issue2)
	if kbCountSynthAgents(t) != 1 {
		t.Fatalf("convergence must keep exactly one KB Synthesizer, got %d", kbCountSynthAgents(t))
	}
	if got := kbWorkspaceSynthesizerStamp(t); got != provisionedID {
		t.Fatalf("convergence must re-stamp the surviving agent: got %q want %q", got, provisionedID)
	}
}

func TestMaybeEnqueueKnowledgeCaptureNoRuntimeSkips(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	kbSaveWorkspaceSettings(t)
	kbClearWorkspaceSettings(t)
	kbDeleteSynthesizerAgents(t)

	// Take the shared runtime offline for the duration so no runtime is online.
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `UPDATE agent_runtime SET status = 'offline' WHERE workspace_id = $1`, testWorkspaceID); err != nil {
		t.Fatalf("take runtimes offline: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `UPDATE agent_runtime SET status = 'online' WHERE id = $1`, testRuntimeID)
	})

	issue := kbCaptureIssue(t, "")
	testHandler.maybeEnqueueKnowledgeCapture(ctx, issue)

	if kbCountSynthAgents(t) != 0 {
		t.Fatalf("no online runtime must not provision an agent")
	}
	if n := kbCountEnqueuedTasksForIssue(t, issue.ID); n != 0 {
		t.Fatalf("no online runtime must enqueue no task, got %d", n)
	}
	var commentCount int
	testPool.QueryRow(ctx, `SELECT count(*) FROM comment WHERE issue_id = $1`, uuidToString(issue.ID)).Scan(&commentCount)
	if commentCount != 0 {
		t.Fatalf("no online runtime must author no trigger comment, got %d", commentCount)
	}
}

func TestMaybeEnqueueKnowledgeCaptureBacklogSkips(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	kbSaveWorkspaceSettings(t)
	kbClearWorkspaceSettings(t)
	kbDeleteSynthesizerAgents(t)

	// Stamp a live synthesizer, then load it up with >=10 in-flight tasks.
	synthID := kbSeedSynthesizerAgent(t, false)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE workspace SET settings = jsonb_build_object('kb_synthesizer_agent_id', $2::text) WHERE id = $1`,
		testWorkspaceID, synthID,
	); err != nil {
		t.Fatalf("stamp synthesizer: %v", err)
	}
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if _, err := testPool.Exec(ctx,
			`INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority) VALUES ($1, $2, 'queued', 0)`,
			synthID, testRuntimeID,
		); err != nil {
			t.Fatalf("seed in-flight task %d: %v", i, err)
		}
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE agent_id = $1`, synthID)
	})

	issue := kbCaptureIssue(t, "")
	testHandler.maybeEnqueueKnowledgeCapture(ctx, issue)

	if n := kbCountEnqueuedTasksForIssue(t, issue.ID); n != 0 {
		t.Fatalf("backlog >= 10 must skip enqueue for the issue, got %d tasks", n)
	}
	var commentCount int
	testPool.QueryRow(ctx, `SELECT count(*) FROM comment WHERE issue_id = $1`, uuidToString(issue.ID)).Scan(&commentCount)
	if commentCount != 0 {
		t.Fatalf("backlog skip must author no trigger comment, got %d", commentCount)
	}
}

func TestKBCaptureKillSwitch(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	kbSaveWorkspaceSettings(t)
	kbClearWorkspaceSettings(t)
	kbDeleteSynthesizerAgents(t)

	t.Setenv("AGORA_KB_CAPTURE_DISABLED", "1")

	issue := kbCaptureIssue(t, "")
	testHandler.maybeEnqueueKnowledgeCapture(context.Background(), issue)

	if kbCountSynthAgents(t) != 0 {
		t.Fatalf("kill switch must provision no agent")
	}
	if n := kbCountEnqueuedTasksForIssue(t, issue.ID); n != 0 {
		t.Fatalf("kill switch must enqueue no task, got %d", n)
	}
	var commentCount int
	testPool.QueryRow(context.Background(), `SELECT count(*) FROM comment WHERE issue_id = $1`, uuidToString(issue.ID)).Scan(&commentCount)
	if commentCount != 0 {
		t.Fatalf("kill switch must author no comment, got %d", commentCount)
	}
	// Sanity: the stamp must not have been written either.
	if got := kbWorkspaceSynthesizerStamp(t); got != "" {
		t.Fatalf("kill switch must not stamp settings, got %q", got)
	}
}

func TestKBCaptureModelEscalation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	// kbCaptureModelOverride escalates to sonnet only on a claude-provider
	// runtime when the thread exceeds kbLargeContextRunes. Build one runtime per
	// provider and one synthesizer agent bound to each.
	makeRuntime := func(provider string) string {
		var rid string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at)
			VALUES ($1, NULL, $2, 'cloud', $3, 'online', 'x', '{}'::jsonb, now())
			RETURNING id
		`, testWorkspaceID, fmt.Sprintf("kb-esc-%s-%d", provider, time.Now().UnixNano()), provider).Scan(&rid); err != nil {
			t.Fatalf("insert %s runtime: %v", provider, err)
		}
		t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_runtime WHERE id = $1`, rid) })
		return rid
	}
	makeAgent := func(runtimeID string) string {
		var aid string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent (workspace_id, name, description, runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id, instructions, custom_env, custom_args)
			VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'workspace', 3, NULL, '', '{}'::jsonb, '[]'::jsonb)
			RETURNING id
		`, testWorkspaceID, fmt.Sprintf("kb-esc-agent-%d", time.Now().UnixNano()), runtimeID).Scan(&aid); err != nil {
			t.Fatalf("insert escalation agent: %v", err)
		}
		t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent WHERE id = $1`, aid) })
		return aid
	}

	// A large-thread issue: a >25k-rune description guarantees the cutoff.
	bigDesc := strings.Repeat("x", kbLargeContextRunes+1000)
	projectID := kbTestProject(t, fmt.Sprintf("KB Esc Project %d", time.Now().UnixNano()))
	makeBigIssue := func() db.Issue {
		var issueID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO issue (workspace_id, project_id, title, description, status, priority, creator_type, creator_id, number)
			VALUES ($1, $2, 'Big capture issue', $3, 'done', 'medium', 'member', $4, $5)
			RETURNING id
		`, testWorkspaceID, projectID, bigDesc, testUserID, int32(time.Now().UnixNano()%1_000_000_000)).Scan(&issueID); err != nil {
			t.Fatalf("insert big issue: %v", err)
		}
		t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID) })
		issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
		if err != nil {
			t.Fatalf("load big issue: %v", err)
		}
		return issue
	}

	// claude runtime + large thread → sonnet override.
	claudeAgent := makeAgent(makeRuntime("claude"))
	got := testHandler.kbCaptureModelOverride(ctx, parseUUID(claudeAgent), makeBigIssue())
	if !got.Valid || got.String != kbSynthEscalationModel {
		t.Fatalf("claude large thread: override = %+v, want %q", got, kbSynthEscalationModel)
	}

	// opencode runtime + large thread → no override (owns its own context handling).
	opencodeAgent := makeAgent(makeRuntime("opencode"))
	got = testHandler.kbCaptureModelOverride(ctx, parseUUID(opencodeAgent), makeBigIssue())
	if got.Valid {
		t.Fatalf("opencode large thread: override = %+v, want no override", got)
	}

	// claude runtime + small thread → no override.
	var smallIssueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, project_id, title, description, status, priority, creator_type, creator_id, number)
		VALUES ($1, $2, 'Small issue', 'tiny', 'done', 'medium', 'member', $3, $4)
		RETURNING id
	`, testWorkspaceID, projectID, testUserID, int32(time.Now().UnixNano()%1_000_000_000)).Scan(&smallIssueID); err != nil {
		t.Fatalf("insert small issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, smallIssueID) })
	smallIssue, err := testHandler.Queries.GetIssue(ctx, parseUUID(smallIssueID))
	if err != nil {
		t.Fatalf("load small issue: %v", err)
	}
	got = testHandler.kbCaptureModelOverride(ctx, parseUUID(claudeAgent), smallIssue)
	if got.Valid {
		t.Fatalf("claude small thread: override = %+v, want no override", got)
	}
}
