package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// ProjectKBSkillName: explicit settings.kb_skill override wins; else the slug;
// a Cyrillic-only title (slug "") with no override yields "" (no lookup).
// Ported from the handler package when the function moved here for the compile
// pipeline (the override governs the compile target too).
func TestProjectKBSkillNameOverride(t *testing.T) {
	withOverride := db.Project{Title: "10 спринт (Июль)", Settings: []byte(`{"kb_skill":"sd-main-kb"}`)}
	if got := ProjectKBSkillName(withOverride); got != "sd-main-kb" {
		t.Errorf("override: want sd-main-kb, got %q", got)
	}
	bySlug := db.Project{Title: "sd-cs", Settings: []byte(`{}`)}
	if got := ProjectKBSkillName(bySlug); got != "sd-cs-kb" {
		t.Errorf("slug: want sd-cs-kb, got %q", got)
	}
	cyrillicNoOverride := db.Project{Title: "спринт", Settings: nil}
	if got := ProjectKBSkillName(cyrillicNoOverride); got != "" {
		t.Errorf("cyrillic-only title without override must yield empty, got %q", got)
	}
}

// kbItem is a compact fixture builder for the pure compile tests. Only the
// columns compileKBItemsRegion reads are populated.
func kbItem(kind, module, title, body string, hits int32, issueHex string) db.KnowledgeItem {
	it := db.KnowledgeItem{
		Kind:   kind,
		Module: module,
		Title:  title,
		Body:   body,
		Hits:   hits,
	}
	if issueHex != "" {
		it.SourceIssueID = util.MustParseUUID(issueHex)
	}
	return it
}

// TestCompileKBItemsRegionDeterministic: the same ranked slice compiles to a
// byte-identical string across two calls; sections appear in the fixed order
// (Architecture, Conventions, Gotchas, Navigation, Decisions) regardless of
// input order; module subsections sort lexically under their section; and the
// caller-supplied rank order is preserved within a section.
func TestCompileKBItemsRegionDeterministic(t *testing.T) {
	// Deliberately shuffled kinds on input; the compile must re-group them into
	// the fixed section order. Within-section order follows slice (rank) order.
	items := []db.KnowledgeItem{
		kbItem("gotcha", "", "Gotcha one high rank", "body g1", 5, "11111111-1111-1111-1111-111111111111"),
		kbItem("decision", "", "Decision fact", "body d1", 4, "22222222-2222-2222-2222-222222222222"),
		kbItem("architecture", "", "Arch fact top", "body a1", 3, "33333333-3333-3333-3333-333333333333"),
		kbItem("gotcha", "billing", "Gotcha in billing module", "body g2", 2, "44444444-4444-4444-4444-444444444444"),
		kbItem("gotcha", "auth", "Gotcha in auth module", "body g3", 1, "55555555-5555-5555-5555-555555555555"),
		kbItem("convention", "", "Convention fact", "body c1", 0, "66666666-6666-6666-6666-666666666666"),
		kbItem("nav", "", "Nav hint here", "body n1", 0, ""),
	}

	first := compileKBItemsRegion(items, kbCompileBudgetMax)
	second := compileKBItemsRegion(items, kbCompileBudgetMax)
	if first != second {
		t.Fatalf("compile not deterministic:\nfirst=\n%s\nsecond=\n%s", first, second)
	}

	// Fixed section order.
	sectionOrder := []string{
		"### Architecture", "### Conventions", "### Gotchas", "### Navigation", "### Decisions",
	}
	prev := -1
	for _, sec := range sectionOrder {
		idx := strings.Index(first, sec)
		if idx < 0 {
			t.Fatalf("section %q missing from region:\n%s", sec, first)
		}
		if idx <= prev {
			t.Fatalf("section %q out of order (idx %d <= prev %d):\n%s", sec, idx, prev, first)
		}
		prev = idx
	}

	// Module subsections sort lexically: auth before billing, both under Gotchas.
	authIdx := strings.Index(first, "#### Module: auth")
	billingIdx := strings.Index(first, "#### Module: billing")
	if authIdx < 0 || billingIdx < 0 {
		t.Fatalf("module subsections missing:\n%s", first)
	}
	if authIdx > billingIdx {
		t.Fatalf("module subsections not sorted lexically (auth %d after billing %d)", authIdx, billingIdx)
	}
	// Project-wide gotcha renders before the module subsections within Gotchas.
	plainGotchaIdx := strings.Index(first, "Gotcha one high rank")
	if plainGotchaIdx < 0 || plainGotchaIdx > authIdx {
		t.Fatalf("project-wide gotcha must precede module subsections")
	}

	// Header + confirmation multiplier (hits+1) rendered.
	if !strings.Contains(first, kbItemsRegionHeader) {
		t.Error("region missing security header")
	}
	if !strings.Contains(first, "×6)") { // hits 5 -> ×6
		t.Errorf("expected confirmation multiplier ×6 for hits=5:\n%s", first)
	}
}

// TestCompileKBItemsRegionBudgetCutoff: over-budget items are cut in rank
// order (the flat slice order) with an omission footer, and the dynamic budget
// shrinks as legacy content grows, respecting the floor.
func TestCompileKBItemsRegionBudgetCutoff(t *testing.T) {
	// Each body is ~500 runes, so a tight budget admits only the first few in
	// rank order. Same kind so all land in one section (cut order == slice order).
	body := strings.Repeat("x", 500)
	var items []db.KnowledgeItem
	for i := 0; i < 10; i++ {
		items = append(items, kbItem("gotcha", "", fmt.Sprintf("Item rank %02d", i), body, int32(100-i),
			fmt.Sprintf("%08d-0000-0000-0000-000000000000", i)))
	}

	region := compileKBItemsRegion(items, 1500)
	// Highest-rank items are included; lowest are dropped.
	if !strings.Contains(region, "Item rank 00") {
		t.Errorf("top-ranked item must be included:\n%s", region)
	}
	if strings.Contains(region, "Item rank 09") {
		t.Errorf("lowest-ranked item must be dropped under a tight budget:\n%s", region)
	}
	if !strings.Contains(region, "omitted from this compile") {
		t.Errorf("omission footer must be present when items are cut:\n%s", region)
	}
	// Cut is a suffix of the rank order: the last included index and the first
	// omitted index must not straddle (no gaps). Verify inclusion is a prefix.
	included := 0
	for i := 0; i < 10; i++ {
		if strings.Contains(region, fmt.Sprintf("Item rank %02d", i)) {
			if included != i {
				t.Fatalf("cut is not a rank-order suffix: rank %02d included but %d were expected before it", i, included)
			}
			included++
		}
	}
	if included == 0 || included == 10 {
		t.Fatalf("budget cutoff test degenerate: %d of 10 included", included)
	}

	// Dynamic budget: kbRegionBudget shrinks as outside content grows and floors
	// at kbCompileBudgetMin. Large legacy blob -> floor.
	bigOutside := strings.Repeat("y", kbSkillTotalTarget) // >> target, forces the floor
	content := bigOutside + "\n\n" + kbItemsBeginMarker + "\n" + kbItemsEndMarker
	if got := kbRegionBudget(content); got != kbCompileBudgetMin {
		t.Errorf("large legacy content must clamp budget to floor %d, got %d", kbCompileBudgetMin, got)
	}
	// Tiny outside content -> ceiling.
	if got := kbRegionBudget(""); got != kbCompileBudgetMax {
		t.Errorf("empty content must clamp budget to ceiling %d, got %d", kbCompileBudgetMax, got)
	}
	// Mid-range outside content -> target minus outside, between floor and ceiling.
	midOutside := strings.Repeat("z", 12000)
	wantMid := kbSkillTotalTarget - 12000 // 8000, within [4000,12000]
	if got := kbRegionBudget(midOutside); got != wantMid {
		t.Errorf("mid-range budget: want %d, got %d", wantMid, got)
	}
}

// TestSpliceManagedRegionPreservesOutsideContent: content outside the markers
// is preserved byte-for-byte; missing markers append the region; a begin
// marker without an end marker self-heals (the stale tail is replaced).
func TestSpliceManagedRegionPreservesOutsideContent(t *testing.T) {
	region := "REGION_BODY"

	// 1. No markers -> region appended, legacy content intact.
	legacy := "# My KB\n\nLegacy human-written notes.\nMore notes."
	out := spliceManagedRegion(legacy, region)
	if !strings.HasPrefix(out, legacy) {
		t.Errorf("legacy content not preserved as prefix:\n%s", out)
	}
	if !strings.Contains(out, kbItemsBeginMarker) || !strings.Contains(out, kbItemsEndMarker) {
		t.Errorf("markers not appended:\n%s", out)
	}
	if !strings.Contains(out, region) {
		t.Errorf("region body not appended:\n%s", out)
	}

	// 2. Both markers present -> replace only between them, outside untouched.
	before := "BEFORE-CONTENT\n\n"
	after := "\n\nAFTER-CONTENT"
	existing := before + kbItemsBeginMarker + "\nSTALE_OLD_REGION\n" + kbItemsEndMarker + after
	out2 := spliceManagedRegion(existing, region)
	if !strings.HasPrefix(out2, before) {
		t.Errorf("content before markers not preserved:\n%s", out2)
	}
	if !strings.HasSuffix(out2, after) {
		t.Errorf("content after markers not preserved:\n%s", out2)
	}
	if strings.Contains(out2, "STALE_OLD_REGION") {
		t.Errorf("stale region body not replaced:\n%s", out2)
	}
	if !strings.Contains(out2, region) {
		t.Errorf("new region body not spliced in:\n%s", out2)
	}

	// 3. Begin without end -> self-heal: everything from begin to EOF is the
	// stale region and gets replaced; content before begin is preserved.
	broken := "PRELUDE\n\n" + kbItemsBeginMarker + "\nORPHANED_TAIL_WITH_NO_END_MARKER"
	out3 := spliceManagedRegion(broken, region)
	if !strings.HasPrefix(out3, "PRELUDE") {
		t.Errorf("self-heal must preserve content before begin marker:\n%s", out3)
	}
	if strings.Contains(out3, "ORPHANED_TAIL_WITH_NO_END_MARKER") {
		t.Errorf("self-heal must drop the orphaned tail:\n%s", out3)
	}
	if !strings.Contains(out3, kbItemsEndMarker) {
		t.Errorf("self-heal must re-add the end marker:\n%s", out3)
	}
	if !strings.Contains(out3, region) {
		t.Errorf("self-heal must splice the new region:\n%s", out3)
	}

	// 4. Empty region on markerless content still appends an empty marker block.
	out4 := spliceManagedRegion(legacy, "")
	if !strings.Contains(out4, kbItemsBeginMarker) || !strings.Contains(out4, kbItemsEndMarker) {
		t.Errorf("empty region must still emit the marker block:\n%s", out4)
	}
}

// ---- DB-backed test (shared-KB topology) ----

// knowledgeTestPool connects to DATABASE_URL (or the local default) and skips
// the test when Postgres is unreachable — the same convention as
// handler_test.go / scheduler stale_steal_test.go.
func knowledgeTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://agora:agora@localhost:5432/agora?sslmode=disable"
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("knowledge service integration tests require Postgres: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("knowledge service integration tests require Postgres: %v", err)
	}
	// The migration must be applied; skip cleanly if the table is missing so a
	// stale DB doesn't fail the whole package.
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.knowledge_item') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		pool.Close()
		t.Skip("knowledge service integration tests require migration 146 (knowledge_item)")
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedKnowledgeWorkspace creates an isolated workspace and returns its id. The
// caller registers cleanup; workspace CASCADE removes projects + items.
func seedKnowledgeWorkspace(t *testing.T, pool *pgxpool.Pool) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	slug := "kb-svc-" + uuid.NewString()[:8]
	var wsID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, '', 'KBS') RETURNING id
	`, "KB Svc Test", slug).Scan(&wsID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, wsID)
	})
	return util.MustParseUUID(wsID)
}

func seedKnowledgeProject(t *testing.T, pool *pgxpool.Pool, q *db.Queries, wsID pgtype.UUID, title, kbSkill string) db.Project {
	t.Helper()
	proj, err := q.CreateProject(context.Background(), db.CreateProjectParams{
		WorkspaceID: wsID,
		Title:       title,
		Status:      "planned",
		Priority:    "none",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	// CreateProjectParams has no Settings column; stamp the kb_skill override
	// directly so RecompileKBForProject's GetProject reads it.
	if kbSkill != "" {
		settings := []byte(fmt.Sprintf(`{"kb_skill":%q}`, kbSkill))
		if _, err := pool.Exec(context.Background(),
			`UPDATE project SET settings = $2 WHERE id = $1`, util.UUIDToString(proj.ID), settings); err != nil {
			t.Fatalf("stamp project settings: %v", err)
		}
		proj.Settings = settings
	}
	return proj
}

// TestRecompileKBSharedAcrossProjects exercises the live sd-main topology: two
// projects whose settings.kb_skill point at ONE shared skill. Items from both
// projects compile into a single merged region; a recompile driven by project
// B does not erase project A's items; and the same normalized fact captured
// from both buckets is a single row whose hit count grows (confirm, not
// duplicate).
func TestRecompileKBSharedAcrossProjects(t *testing.T) {
	pool := knowledgeTestPool(t)
	q := db.New(pool)
	svc := NewTaskService(q, pool, nil, events.New())
	ctx := context.Background()

	wsID := seedKnowledgeWorkspace(t, pool)
	const sharedKB = "shared-sprint-kb"
	projA := seedKnowledgeProject(t, pool, q, wsID, "Sprint A", sharedKB)
	projB := seedKnowledgeProject(t, pool, q, wsID, "Sprint B", sharedKB)

	if got := ProjectKBSkillName(projA); got != sharedKB {
		t.Fatalf("projA kb name: want %q got %q", sharedKB, got)
	}
	if got := ProjectKBSkillName(projB); got != sharedKB {
		t.Fatalf("projB kb name: want %q got %q", sharedKB, got)
	}

	insertActive := func(proj db.Project, title, body string) db.UpsertKnowledgeItemRow {
		t.Helper()
		row, err := q.UpsertKnowledgeItem(ctx, db.UpsertKnowledgeItemParams{
			WorkspaceID:   wsID,
			ProjectID:     proj.ID,
			KbName:        sharedKB,
			Module:        "",
			Kind:          "gotcha",
			Title:         title,
			Body:          body,
			NormTitle:     normalizeKnowledgeTitle(title),
			SourceIssueID: pgtype.UUID{},
			CreatedByType: "member",
			CreatedByID:   pgtype.UUID{},
			Status:        "active",
		})
		if err != nil {
			t.Fatalf("insert item: %v", err)
		}
		return row
	}

	itemA := insertActive(projA, "Fact only in project A", "detail A")
	insertActive(projB, "Fact only in project B", "detail B")

	// Recompile driven by project B: it must NOT erase A's contribution — the
	// key is the shared KB name, not the project id.
	svc.RecompileKBForProject(ctx, wsID, projB.ID)

	skill, err := q.GetSkillByWorkspaceAndName(ctx, db.GetSkillByWorkspaceAndNameParams{
		WorkspaceID: wsID, Name: sharedKB,
	})
	if err != nil {
		t.Fatalf("load compiled skill: %v", err)
	}
	if !strings.Contains(skill.Content, "Fact only in project A") {
		t.Errorf("project A's item erased by a project-B-driven recompile:\n%s", skill.Content)
	}
	if !strings.Contains(skill.Content, "Fact only in project B") {
		t.Errorf("project B's item missing from the shared region:\n%s", skill.Content)
	}
	if !strings.Contains(skill.Content, kbItemsBeginMarker) {
		t.Errorf("compiled skill missing managed-region markers:\n%s", skill.Content)
	}

	// Same normalized fact captured from both buckets -> one row, hits+1
	// (UpsertKnowledgeItem confirms on the partial unique index across the KB,
	// which is NOT per-project). Insert the same normalized title again citing
	// project B; the row count for that norm_title stays 1 and hits increments.
	sameTitle := "Fact only in project A" // identical -> same norm_title
	row2, err := q.UpsertKnowledgeItem(ctx, db.UpsertKnowledgeItemParams{
		WorkspaceID:   wsID,
		ProjectID:     projB.ID, // sibling bucket
		KbName:        sharedKB,
		Kind:          "gotcha",
		Title:         sameTitle,
		Body:          "detail from B",
		NormTitle:     normalizeKnowledgeTitle(sameTitle),
		CreatedByType: "member",
		Status:        "active",
	})
	if err != nil {
		t.Fatalf("re-upsert shared fact: %v", err)
	}
	if row2.Inserted {
		t.Errorf("a fact re-learned in a sibling bucket must confirm, not insert a new row")
	}
	if row2.ID != itemA.ID {
		t.Errorf("confirm must target the existing row (%s), got %s",
			util.UUIDToString(itemA.ID), util.UUIDToString(row2.ID))
	}
	if row2.Hits != itemA.Hits+1 {
		t.Errorf("confirm must bump hits: want %d, got %d", itemA.Hits+1, row2.Hits)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge_item WHERE workspace_id = $1 AND kb_name = $2 AND norm_title = $3 AND status <> 'archived'`,
		util.UUIDToString(wsID), sharedKB, normalizeKnowledgeTitle(sameTitle)).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Errorf("shared fact must be a single live row across sibling projects, got %d", count)
	}
}
