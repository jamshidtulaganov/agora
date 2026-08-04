package handler

import (
	"context"
	"strings"
	"testing"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// The risk-map context must carry the tier rules an agent acts on: highest
// matching tier wins, unknown paths are guarded, critical never self-merges.
func TestRenderRiskMapContext(t *testing.T) {
	out := renderRiskMapContext([]riskMapEntry{
		{Module: "billing", Tier: "critical", Paths: []string{"protected/modules/pay/**", "protected/controllers/Kassa*"}, Owner: "Davron", Notes: "money paths"},
		{Module: "reports", Tier: "safe", Paths: []string{"protected/views/report/**"}},
		{Module: "mystery"}, // no tier → renders as guarded
	})
	for _, want := range []string{
		"PROJECT RISK MAP",
		"HIGHEST matching tier",
		"GUARDED, never safe",
		"[critical] billing",
		"protected/modules/pay/**",
		"(owner: Davron)",
		"— money paths",
		"[safe] reports",
		"[guarded] mystery",
		"critical → do NOT merge",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("risk map context missing %q\ngot: %s", want, out)
		}
	}
	if renderRiskMapContext(nil) != "" {
		t.Error("empty risk map must render nothing")
	}
}

// issueBriefNote must carry the description + numbered criteria and stay empty
// when the issue has neither (title-only issues add no note).
func TestIssueBriefNote(t *testing.T) {
	out := issueBriefNote("Исправить фильтр по сроку годности", []byte(`["фильтр работает","отчёт открывается"]`))
	for _, want := range []string{"ISSUE BRIEF", "Исправить фильтр", "(1) фильтр работает;", "(2) отчёт открывается;"} {
		if !strings.Contains(out, want) {
			t.Errorf("brief missing %q\ngot: %s", want, out)
		}
	}
	if issueBriefNote("", nil) != "" {
		t.Error("no description + no criteria must render nothing")
	}
	// Rune-safe truncation on long Cyrillic descriptions.
	long := strings.Repeat("я", 4100)
	got := issueBriefNote(long, nil)
	if !strings.Contains(got, "…") {
		t.Error("over-cap description must be truncated with an ellipsis")
	}
}

// DB-backed: projectKBSkill resolves the settings.kb_skill override to the real
// workspace skill and returns its content — the claim-path auto-injection this
// whole feature exists for (a Cyrillic bucket project + an English-named KB).
func TestProjectKBSkill_ResolvesOverride(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ctx := t.Context()
	var skillID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO skill (workspace_id, name, description, content)
		 VALUES ($1::uuid, 'kbtest-kb', 'kb', '# tribal knowledge') RETURNING id::text`,
		testWorkspaceID).Scan(&skillID); err != nil {
		t.Fatalf("create skill: %v", err)
	}
	var pid, iid string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title, status, priority, settings)
		 VALUES ($1::uuid, '10 спринт (Тест)', 'planned', 'none', '{"kb_skill":"kbtest-kb"}') RETURNING id::text`,
		testWorkspaceID).Scan(&pid); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := testPool.QueryRow(ctx,
		`INSERT INTO issue (workspace_id, project_id, title, creator_type, creator_id)
		 VALUES ($1::uuid, $2::uuid, 'kb issue', 'member', $3::uuid) RETURNING id::text`,
		testWorkspaceID, pid, testUserID).Scan(&iid); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() {
		// context.Background(), NOT the captured t.Context() — that one is
		// canceled before Cleanup runs, silently skipping the DELETEs and
		// leaking fixtures (the skill name is UNIQUE per workspace).
		cctx := context.Background()
		testPool.Exec(cctx, `DELETE FROM issue WHERE id=$1::uuid`, iid)
		testPool.Exec(cctx, `DELETE FROM project WHERE id=$1::uuid`, pid)
		testPool.Exec(cctx, `DELETE FROM skill WHERE id=$1::uuid`, skillID)
	})
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(iid))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	kb, ok := testHandler.projectKBSkill(ctx, issue)
	if !ok {
		t.Fatal("expected the kb skill to resolve via the settings override")
	}
	if kb.Name != "kbtest-kb" || !strings.Contains(kb.Content, "tribal knowledge") {
		t.Errorf("unexpected skill payload: %+v", kb)
	}
}

// DB-backed: issueRiskTier — an explicit risk:<tier> label wins; a risk-mapped
// project with no label fails CLOSED to guarded; no risk map → "" (no tiering).
func TestIssueRiskTier(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ctx := t.Context()
	mk := func(settings string) (pid, iid string) {
		if err := testPool.QueryRow(ctx,
			`INSERT INTO project (workspace_id, title, status, priority, settings)
			 VALUES ($1::uuid, 'tier-proj-'||gen_random_uuid(), 'planned', 'none', $2::jsonb) RETURNING id::text`,
			testWorkspaceID, settings).Scan(&pid); err != nil {
			t.Fatalf("create project: %v", err)
		}
		if err := testPool.QueryRow(ctx,
			`INSERT INTO issue (workspace_id, project_id, title, creator_type, creator_id, number)
			 VALUES ($1::uuid, $2::uuid, 'tier issue', 'member', $3::uuid,
			         (2000000 + floor(random()*1000000))::int) RETURNING id::text`,
			testWorkspaceID, pid, testUserID).Scan(&iid); err != nil {
			t.Fatalf("create issue: %v", err)
		}
		t.Cleanup(func() {
			cctx := context.Background()
			testPool.Exec(cctx, `DELETE FROM issue WHERE id=$1::uuid`, iid)
			testPool.Exec(cctx, `DELETE FROM project WHERE id=$1::uuid`, pid)
		})
		return pid, iid
	}
	load := func(iid string) db.Issue {
		issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(iid))
		if err != nil {
			t.Fatalf("load issue: %v", err)
		}
		return issue
	}

	mapped := `{"risk_map":[{"module":"billing","tier":"critical","paths":["pay/**"]}]}`

	// (a) risk-mapped, no label → guarded (fail closed).
	_, iid := mk(mapped)
	if got := testHandler.issueRiskTier(ctx, load(iid)); got != "guarded" {
		t.Errorf("mapped+unlabeled: want guarded, got %q", got)
	}

	// (b) explicit risk:safe label wins over the guarded fallback.
	_, iid2 := mk(mapped)
	var labelID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO issue_label (workspace_id, name, color) VALUES ($1::uuid, 'risk:safe', '#0f0')
		 RETURNING id::text`,
		testWorkspaceID).Scan(&labelID); err != nil {
		t.Fatalf("create label: %v", err)
	}
	if _, err := testPool.Exec(ctx,
		`INSERT INTO issue_to_label (issue_id, label_id) VALUES ($1::uuid,$2::uuid)`,
		iid2, labelID); err != nil {
		t.Fatalf("attach label: %v", err)
	}
	t.Cleanup(func() {
		cctx := context.Background()
		testPool.Exec(cctx, `DELETE FROM issue_to_label WHERE label_id=$1::uuid`, labelID)
		testPool.Exec(cctx, `DELETE FROM issue_label WHERE id=$1::uuid`, labelID)
	})
	if got := testHandler.issueRiskTier(ctx, load(iid2)); got != "safe" {
		t.Errorf("explicit risk:safe label must win, got %q", got)
	}

	// (c) no risk map → no tiering.
	_, iid3 := mk(`{}`)
	if got := testHandler.issueRiskTier(ctx, load(iid3)); got != "" {
		t.Errorf("unmapped project: want empty tier, got %q", got)
	}
}

// DB-backed: the risk-tier human-sign-off gate. When AGORA_RISK_TIER_GATE_ENFORCED
// is on, an AGENT cannot close a CRITICAL-tier issue (held at in_review from any
// prior status, even with its own qa:pass); a human is never held; non-critical
// tiers and non-done targets pass through; flag off is a full passthrough.
func TestEnforceQAGateBeforeDone_RiskTierSignoff(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ctx := t.Context()

	var pid, iid, labelID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title, status, priority, settings)
		 VALUES ($1::uuid,'rt-proj-'||gen_random_uuid(),'planned','none',
		         '{"risk_map":[{"module":"billing","tier":"critical","paths":["pay/**"]}]}'::jsonb)
		 RETURNING id::text`, testWorkspaceID).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx,
		`INSERT INTO issue (workspace_id, project_id, title, creator_type, creator_id, number)
		 VALUES ($1::uuid,$2::uuid,'rt issue','member',$3::uuid,(3000000+floor(random()*1000000))::int)
		 RETURNING id::text`, testWorkspaceID, pid, testUserID).Scan(&iid); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx,
		`INSERT INTO issue_label (workspace_id, name, color) VALUES ($1::uuid,'risk:critical','#f00') RETURNING id::text`,
		testWorkspaceID).Scan(&labelID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx,
		`INSERT INTO issue_to_label (issue_id, label_id) VALUES ($1::uuid,$2::uuid)`, iid, labelID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := context.Background()
		testPool.Exec(c, `DELETE FROM issue_to_label WHERE issue_id=$1::uuid`, iid)
		testPool.Exec(c, `DELETE FROM issue_label WHERE id=$1::uuid`, labelID)
		testPool.Exec(c, `DELETE FROM issue WHERE id=$1::uuid`, iid)
		testPool.Exec(c, `DELETE FROM project WHERE id=$1::uuid`, pid)
	})
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(iid))
	if err != nil {
		t.Fatal(err)
	}
	if tier := testHandler.issueRiskTier(ctx, issue); tier != "critical" {
		t.Fatalf("fixture tier = %q, want critical", tier)
	}

	// Flag OFF → an agent can close a critical issue (gate inert).
	t.Setenv("AGORA_RISK_TIER_GATE_ENFORCED", "")
	if got, held := testHandler.enforceQAGateBeforeDone(ctx, issue, "agent", "in_progress", "done"); held || got != "done" {
		t.Errorf("flag off: want (done,false), got (%q,%v)", got, held)
	}

	t.Setenv("AGORA_RISK_TIER_GATE_ENFORCED", "true")
	// Agent closing a critical issue → held at in_review from ANY prior status.
	for _, prev := range []string{"in_progress", "in_review", "todo"} {
		if got, held := testHandler.enforceQAGateBeforeDone(ctx, issue, "agent", prev, "done"); !held || got != "in_review" {
			t.Errorf("agent+critical from %s: want (in_review,true), got (%q,%v)", prev, got, held)
		}
	}
	// A human is NEVER held.
	if got, held := testHandler.enforceQAGateBeforeDone(ctx, issue, "member", "in_progress", "done"); held || got != "done" {
		t.Errorf("human+critical: want (done,false), got (%q,%v)", got, held)
	}
	// A non-done target passes through even for an agent on a critical issue.
	if got, held := testHandler.enforceQAGateBeforeDone(ctx, issue, "agent", "todo", "in_progress"); held || got != "in_progress" {
		t.Errorf("non-done target: want (in_progress,false), got (%q,%v)", got, held)
	}
}

// The intake-triage prompt is a suggest-only contract: it must demand the label
// set and FORBID any routing/status mutation.
func TestBitrixTriagePromptContract(t *testing.T) {
	for _, want := range []string{
		"SUGGEST-ONLY", "type:bug", "module:<name>", "risk:<tier>", "needs:spec",
		"never safe", "do NOT change status, assignee, project",
		"IN THE ISSUE'S LANGUAGE",
	} {
		if !strings.Contains(bitrixTriagePrompt, want) {
			t.Errorf("triage prompt missing %q", want)
		}
	}
}

// The base-suite authoring prompt must route output through the existing
// capture (```test-cases block) and end at the promotion trigger (done).
func TestBaseSuitePromptContract(t *testing.T) {
	for _, want := range []string{"```test-cases", "JSON ARRAY", "status to done", "blocked", "QA MANIFEST", "Do NOT touch product code"} {
		if !strings.Contains(baseSuitePromptTmpl, want) {
			t.Errorf("base-suite prompt missing %q", want)
		}
	}
}
