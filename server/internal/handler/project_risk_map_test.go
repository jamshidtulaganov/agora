package handler

import (
	"context"
	"strings"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
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

// projectKBSkillName: explicit settings.kb_skill override wins; else the slug;
// a Cyrillic-only title (slug "") with no override yields "" (no lookup).
func TestProjectKBSkillName(t *testing.T) {
	withOverride := db.Project{Title: "10 спринт (Июль)", Settings: []byte(`{"kb_skill":"sd-main-kb"}`)}
	if got := projectKBSkillName(withOverride); got != "sd-main-kb" {
		t.Errorf("override: want sd-main-kb, got %q", got)
	}
	bySlug := db.Project{Title: "sd-cs", Settings: []byte(`{}`)}
	if got := projectKBSkillName(bySlug); got != "sd-cs-kb" {
		t.Errorf("slug: want sd-cs-kb, got %q", got)
	}
	cyrillicNoOverride := db.Project{Title: "спринт", Settings: nil}
	if got := projectKBSkillName(cyrillicNoOverride); got != "" {
		t.Errorf("cyrillic-only title without override must yield empty, got %q", got)
	}
}
