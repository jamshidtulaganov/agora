package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// captureContent wraps a JSON array literal in a knowledge-items fence.
func captureContent(jsonArray string) string {
	return "some prose\n\n```knowledge-items\n" + jsonArray + "\n```\n\ntrailing prose"
}

// TestParseKnowledgeItemsBlock exercises the block extraction + JSON parse in
// isolation, mirroring what CaptureKnowledgeItems does before touching the DB:
// the fence regex + json.Unmarshal + the 10-item hard cap. (CaptureKnowledgeItems
// itself needs a DB; the parse contract is unit-testable on its own.)
func TestParseKnowledgeItemsBlock(t *testing.T) {
	// parse mirrors CaptureKnowledgeItems' extraction: matched reports whether
	// the fence was found; parseOK reports whether the JSON inside unmarshalled.
	parse := func(content string) (items []knowledgeItemProposal, matched, parseOK bool) {
		m := knowledgeItemsBlockRe.FindStringSubmatch(content)
		if m == nil {
			return nil, false, false
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(m[1])), &items); err != nil {
			return nil, true, false // matched a block but the JSON was malformed
		}
		return items, true, true
	}

	// Valid block.
	if items, matched, parseOK := parse(captureContent(`[{"kind":"gotcha","title":"A fact","body":"detail"}]`)); !matched || !parseOK || len(items) != 1 {
		t.Fatalf("valid block: matched=%v parseOK=%v len=%d", matched, parseOK, len(items))
	}

	// Malformed JSON inside a well-formed fence -> fence matched, parse fails
	// (the capture path logs a warning and no-ops on this).
	if _, matched, parseOK := parse(captureContent(`[{"kind": "gotcha", "title": ]`)); !matched || parseOK {
		t.Errorf("malformed JSON must match the fence but fail to parse: matched=%v parseOK=%v", matched, parseOK)
	}

	// No block at all -> not found.
	if _, matched, _ := parse("just a normal comment with no fence"); matched {
		t.Error("content without a knowledge-items fence must not match")
	}

	// >10 items: the regex/parse yields all of them; the cap is applied by
	// CaptureKnowledgeItems. Assert the parse sees them, then apply the cap.
	var sb strings.Builder
	sb.WriteString("[")
	for i := 0; i < 15; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"kind":"gotcha","title":"Item %d","body":"b"}`, i)
	}
	sb.WriteString("]")
	items, matched, parseOK := parse(captureContent(sb.String()))
	if !matched || !parseOK || len(items) != 15 {
		t.Fatalf("15-item block: matched=%v parseOK=%v len=%d", matched, parseOK, len(items))
	}
	if len(items) > kbCaptureMaxItems {
		items = items[:kbCaptureMaxItems]
	}
	if len(items) != kbCaptureMaxItems {
		t.Errorf("cap must trim to %d, got %d", kbCaptureMaxItems, len(items))
	}

	// Indented fence tolerated (leading-whitespace regex). An indented fence
	// escapes mention-expansion's line-anchored skip, so it must still parse.
	indented := "prose\n  ```knowledge-items\n  [{\"kind\":\"nav\",\"title\":\"T\",\"body\":\"b\"}]\n  ```"
	if _, matched, parseOK := parse(indented); !matched || !parseOK {
		t.Errorf("indented fence must still be extracted+parsed (matched=%v parseOK=%v):\n%s", matched, parseOK, indented)
	}
}

// TestSanitizeKnowledgeText covers the per-field hygiene: backtick fences and
// HTML-comment tokens are stripped; NUL bytes removed; a body over 1200 runes
// is truncated at a rune boundary; an empty title drops the item; and an
// embedded newline in a title collapses to a single-line bullet-safe string.
func TestSanitizeKnowledgeText(t *testing.T) {
	// Backtick + HTML-comment strip in title and body.
	p, ok := sanitizeKnowledgeText(knowledgeItemProposal{
		Kind:  "gotcha",
		Title: "Title with ``` fence and <!-- comment --> tokens",
		Body:  "Body with ```\ncode\n``` and <!-- x --> markers",
	})
	if !ok {
		t.Fatal("item with a non-empty title must survive")
	}
	if strings.Contains(p.Title, "```") || strings.Contains(p.Title, "<!--") || strings.Contains(p.Title, "-->") {
		t.Errorf("fence/comment tokens must be stripped from title: %q", p.Title)
	}
	if strings.Contains(p.Body, "```") || strings.Contains(p.Body, "<!--") || strings.Contains(p.Body, "-->") {
		t.Errorf("fence/comment tokens must be stripped from body: %q", p.Body)
	}

	// NUL strip.
	pn, ok := sanitizeKnowledgeText(knowledgeItemProposal{Kind: "gotcha", Title: "before\x00after", Body: "b\x00c"})
	if !ok || strings.ContainsRune(pn.Title, 0) || strings.ContainsRune(pn.Body, 0) {
		t.Errorf("NUL bytes must be removed: title=%q body=%q", pn.Title, pn.Body)
	}

	// Body truncated at a rune boundary at 1200 with an ellipsis; title capped
	// at 160. Use multibyte runes to confirm boundary correctness (no split).
	longBody := strings.Repeat("é", 2000)
	pt, ok := sanitizeKnowledgeText(knowledgeItemProposal{Kind: "gotcha", Title: "T", Body: longBody})
	if !ok {
		t.Fatal("valid item dropped")
	}
	if r := []rune(pt.Body); len(r) != kbBodyMaxRunes {
		t.Errorf("body must be capped to %d runes, got %d", kbBodyMaxRunes, len(r))
	}
	if !strings.HasSuffix(pt.Body, "…") {
		t.Errorf("truncated body must end with an ellipsis: ...%q", pt.Body[len(pt.Body)-6:])
	}
	if strings.ContainsRune(pt.Body, '�') {
		t.Error("truncation split a multibyte rune (replacement char present)")
	}

	// Empty title (only whitespace/strippable content) -> item dropped.
	if _, ok := sanitizeKnowledgeText(knowledgeItemProposal{Kind: "gotcha", Title: "   ```   ", Body: "b"}); ok {
		t.Error("an item whose title is empty after cleaning must be dropped")
	}

	// Embedded newline in a title collapses to a single line (no bullet break).
	pnl, ok := sanitizeKnowledgeText(knowledgeItemProposal{Kind: "gotcha", Title: "line one\nline two\n### fake header", Body: "b"})
	if !ok {
		t.Fatal("newline title dropped")
	}
	if strings.ContainsAny(pnl.Title, "\n\r") {
		t.Errorf("title must collapse to a single line: %q", pnl.Title)
	}
	if pnl.Title != "line one line two ### fake header" {
		t.Errorf("title whitespace not collapsed to single spaces: %q", pnl.Title)
	}

	// Unknown kind defaults to gotcha.
	pk, _ := sanitizeKnowledgeText(knowledgeItemProposal{Kind: "nonsense", Title: "T", Body: "b"})
	if pk.Kind != "gotcha" {
		t.Errorf("unknown kind must default to gotcha, got %q", pk.Kind)
	}
}

// TestNormalizeKnowledgeTitle: issue-key tokens (MUL-123) are stripped; case is
// folded; punctuation collapses to spaces; whitespace is collapsed; Cyrillic is
// preserved; and lowercase tech tokens like react-18 vs react-19 stay DISTINCT
// (the stripper is uppercase-anchored so it never eats them).
func TestNormalizeKnowledgeTitle(t *testing.T) {
	// Issue key stripped, case folded, punctuation -> space, collapsed.
	if got := normalizeKnowledgeTitle("MUL-123: UpdateProject COALESCEs only 4/9 columns!"); got != "updateproject coalesces only 4 9 columns" {
		t.Errorf("normalize: got %q", got)
	}

	// react-18 vs react-19 must NOT normalize equal (distinct learnings).
	n18 := normalizeKnowledgeTitle("react-18 concurrent mode gotcha")
	n19 := normalizeKnowledgeTitle("react-19 concurrent mode gotcha")
	if n18 == n19 {
		t.Errorf("react-18 and react-19 must stay distinct: both %q", n18)
	}
	if !strings.Contains(n18, "18") || !strings.Contains(n19, "19") {
		t.Errorf("version numbers must survive: n18=%q n19=%q", n18, n19)
	}

	// A lowercase token that LOOKS like an issue key (glm-4) survives because
	// the stripper only matches uppercase prefixes.
	if got := normalizeKnowledgeTitle("glm-4 is the free tier"); !strings.Contains(got, "4") {
		t.Errorf("lowercase tech token must survive the uppercase-anchored stripper: %q", got)
	}

	// Cyrillic preserved (IsLetter is Unicode-aware).
	if got := normalizeKnowledgeTitle("Спринт июль gotcha"); !strings.Contains(got, "спринт") {
		t.Errorf("Cyrillic must be preserved: %q", got)
	}

	// Whitespace collapse across tabs/newlines.
	if got := normalizeKnowledgeTitle("a\t\t b\n\nc"); got != "a b c" {
		t.Errorf("whitespace collapse: got %q", got)
	}
}

// ---- DB-backed tests ----

// knowledgeCaptureFixture bundles the rows CaptureKnowledgeItems reads/writes.
type knowledgeCaptureFixture struct {
	pool    *pgxpool.Pool
	q       *db.Queries
	svc     *TaskService
	wsID    pgtype.UUID
	project db.Project
	issue   db.Issue
	synthID pgtype.UUID // stamped synthesizer agent
	otherID pgtype.UUID // a non-synthesizer agent
}

// seedCaptureFixture builds an isolated workspace with a member, a project, an
// issue, a stamped synthesizer agent and a second (untrusted) agent.
func seedCaptureFixture(t *testing.T, pool *pgxpool.Pool, q *db.Queries, kbSkill string) knowledgeCaptureFixture {
	t.Helper()
	ctx := context.Background()
	wsID := seedKnowledgeWorkspace(t, pool)

	// A member so knowledgeReviewRecipients has a target and the notification
	// path runs end-to-end.
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		"KB Cap User", "kb-cap-"+uuid.NewString()[:8]+"@agora.dev").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID) })
	if _, err := pool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`,
		util.UUIDToString(wsID), userID); err != nil {
		t.Fatalf("seed member: %v", err)
	}

	project := seedKnowledgeProject(t, pool, q, wsID, "Capture Proj", kbSkill)

	var issueID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, creator_type, creator_id, project_id, number)
		VALUES ($1, 'Done issue', 'done', 'member', $2, $3, 1) RETURNING id
	`, util.UUIDToString(wsID), userID, util.UUIDToString(project.ID)).Scan(&issueID); err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issue := db.Issue{
		ID:          util.MustParseUUID(issueID),
		WorkspaceID: wsID,
		ProjectID:   project.ID,
		Title:       "Done issue",
		Status:      "done",
		Number:      1,
	}

	// agent.runtime_id is NOT NULL (a later migration), so seed a runtime first.
	var runtimeID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, metadata, last_seen_at)
		VALUES ($1, 'KB Cap Runtime', 'cloud', 'claude', 'online', '{}'::jsonb, now()) RETURNING id
	`, util.UUIDToString(wsID)).Scan(&runtimeID); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}

	mkAgent := func(name string) pgtype.UUID {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO agent (workspace_id, name, runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks)
			VALUES ($1, $2, 'cloud', '{}'::jsonb, $3, 'workspace', 3) RETURNING id
		`, util.UUIDToString(wsID), name, runtimeID).Scan(&id); err != nil {
			t.Fatalf("seed agent %q: %v", name, err)
		}
		return util.MustParseUUID(id)
	}
	synthID := mkAgent("KB Synthesizer")
	otherID := mkAgent("Some Other Agent")

	// Stamp the synthesizer UUID into workspace.settings — findKBSynthesizer
	// trusts ONLY this persisted value (never a name match).
	if _, err := pool.Exec(ctx,
		`UPDATE workspace SET settings = jsonb_build_object('kb_synthesizer_agent_id', $2::text) WHERE id = $1`,
		util.UUIDToString(wsID), util.UUIDToString(synthID)); err != nil {
		t.Fatalf("stamp synthesizer: %v", err)
	}

	return knowledgeCaptureFixture{
		pool: pool, q: q, svc: NewTaskService(q, pool, nil, events.New()),
		wsID: wsID, project: project, issue: issue, synthID: synthID, otherID: otherID,
	}
}

func (f knowledgeCaptureFixture) countRows(t *testing.T, where string, args ...any) int {
	t.Helper()
	q := `SELECT count(*) FROM knowledge_item WHERE workspace_id = $1` + where
	full := append([]any{util.UUIDToString(f.wsID)}, args...)
	var n int
	if err := f.pool.QueryRow(context.Background(), q, full...).Scan(&n); err != nil {
		t.Fatalf("count(%s): %v", where, err)
	}
	return n
}

func (f knowledgeCaptureFixture) getItemByTitle(t *testing.T, title string) db.KnowledgeItem {
	t.Helper()
	var it db.KnowledgeItem
	err := f.pool.QueryRow(context.Background(), `
		SELECT id, kind, title, status, hits FROM knowledge_item
		WHERE workspace_id = $1 AND title = $2 LIMIT 1
	`, util.UUIDToString(f.wsID), title).Scan(&it.ID, &it.Kind, &it.Title, &it.Status, &it.Hits)
	if err != nil {
		t.Fatalf("get item %q: %v", title, err)
	}
	return it
}

// TestCaptureKnowledgeItemsStatusPolicy verifies the trust + status gate:
//   - synthesizer (matched by the STAMPED UUID) gotcha/nav -> active;
//   - synthesizer convention (instruction-bearing) -> proposed;
//   - a non-synthesizer agent's gotcha -> proposed;
//   - an agent merely NAMED "KB Synthesizer" but not the stamped UUID -> untrusted;
//   - the >=100-proposed spam cutoff refuses new proposals.
func TestCaptureKnowledgeItemsStatusPolicy(t *testing.T) {
	pool := knowledgeTestPool(t)
	q := db.New(pool)

	t.Run("synthesizer gotcha and nav are auto-active", func(t *testing.T) {
		f := seedCaptureFixture(t, pool, q, "")
		content := captureContent(`[
			{"kind":"gotcha","title":"Synth gotcha fact","body":"b"},
			{"kind":"nav","title":"Synth nav hint","body":"b"}
		]`)
		f.svc.CaptureKnowledgeItems(context.Background(), f.issue, content, f.synthID)
		if got := f.getItemByTitle(t, "Synth gotcha fact"); got.Status != "active" {
			t.Errorf("synth gotcha: want active, got %q", got.Status)
		}
		if got := f.getItemByTitle(t, "Synth nav hint"); got.Status != "active" {
			t.Errorf("synth nav: want active, got %q", got.Status)
		}
	})

	t.Run("synthesizer convention is proposed (review gate)", func(t *testing.T) {
		f := seedCaptureFixture(t, pool, q, "")
		content := captureContent(`[{"kind":"convention","title":"Synth convention rule","body":"b"}]`)
		f.svc.CaptureKnowledgeItems(context.Background(), f.issue, content, f.synthID)
		if got := f.getItemByTitle(t, "Synth convention rule"); got.Status != "proposed" {
			t.Errorf("synth convention: want proposed, got %q", got.Status)
		}
	})

	t.Run("non-synthesizer gotcha is proposed", func(t *testing.T) {
		f := seedCaptureFixture(t, pool, q, "")
		content := captureContent(`[{"kind":"gotcha","title":"Untrusted gotcha fact","body":"b"}]`)
		f.svc.CaptureKnowledgeItems(context.Background(), f.issue, content, f.otherID)
		if got := f.getItemByTitle(t, "Untrusted gotcha fact"); got.Status != "proposed" {
			t.Errorf("non-synth gotcha: want proposed, got %q", got.Status)
		}
	})

	t.Run("agent named like the synth but not the stamped UUID is untrusted", func(t *testing.T) {
		f := seedCaptureFixture(t, pool, q, "")
		// f.synthID is stamped in workspace.settings; the seed also created an
		// agent literally NAMED "KB Synthesizer" (that is f.synthID itself), so
		// re-stamp settings to a DIFFERENT live agent (otherID) and post as the
		// name-matching agent. Trust is by stamped UUID only: the name-matching
		// agent is now untrusted and its gotcha must land proposed.
		if _, err := f.pool.Exec(context.Background(),
			`UPDATE workspace SET settings = jsonb_build_object('kb_synthesizer_agent_id', $2::text) WHERE id = $1`,
			util.UUIDToString(f.wsID), util.UUIDToString(f.otherID)); err != nil {
			t.Fatalf("re-stamp synthesizer: %v", err)
		}
		// f.synthID's agent is named "KB Synthesizer" but is no longer the
		// stamped UUID -> must be treated as untrusted.
		content := captureContent(`[{"kind":"gotcha","title":"Impostor gotcha fact","body":"b"}]`)
		f.svc.CaptureKnowledgeItems(context.Background(), f.issue, content, f.synthID)
		if got := f.getItemByTitle(t, "Impostor gotcha fact"); got.Status != "proposed" {
			t.Errorf("an agent NAMED like the synth but not the stamped UUID must be untrusted (proposed), got %q", got.Status)
		}
	})

	t.Run("spam cutoff refuses new proposals at the ceiling", func(t *testing.T) {
		f := seedCaptureFixture(t, pool, q, "")
		ctx := context.Background()
		// Pre-seed the proposed backlog to the ceiling.
		for i := 0; i < kbCaptureProposedCeiling; i++ {
			title := fmt.Sprintf("backlog item %d", i)
			if _, err := q.InsertKnowledgeItemIgnoreDup(ctx, db.InsertKnowledgeItemIgnoreDupParams{
				WorkspaceID: f.wsID, ProjectID: f.project.ID, KbName: ProjectKBSkillName(f.project),
				Kind: "gotcha", Title: title, Body: "b", NormTitle: normalizeKnowledgeTitle(title),
				CreatedByType: "agent", CreatedByID: f.otherID, Status: "proposed",
			}); err != nil {
				t.Fatalf("seed backlog: %v", err)
			}
		}
		before := f.countRows(t, ` AND status = 'proposed'`)
		content := captureContent(`[{"kind":"gotcha","title":"one more over the ceiling","body":"b"}]`)
		f.svc.CaptureKnowledgeItems(ctx, f.issue, content, f.otherID)
		after := f.countRows(t, ` AND status = 'proposed'`)
		if after != before {
			t.Errorf("saturated review queue must refuse new proposals: before=%d after=%d", before, after)
		}
	})
}

// TestKnowledgeDedupeJaccard verifies the near-duplicate policy through the full
// capture path:
//   - an exact norm-title restatement by the synthesizer confirms (hits+1, no new row);
//   - a >=0.6 same-kind near match confirms;
//   - a >=0.6 cross-kind near match inserts (threshold is 0.8 across kinds);
//   - a >=0.8 cross-kind near match confirms;
//   - same-batch near-duplicates merge (in-memory accumulation);
//   - a NON-synthesizer restating an active item does NOT bump hits (rank-pump guard).
func TestKnowledgeDedupeJaccard(t *testing.T) {
	pool := knowledgeTestPool(t)
	q := db.New(pool)
	ctx := context.Background()

	t.Run("exact restatement by synth confirms, no new row", func(t *testing.T) {
		f := seedCaptureFixture(t, pool, q, "")
		base := captureContent(`[{"kind":"gotcha","title":"Cache invalidation is hard here","body":"b"}]`)
		f.svc.CaptureKnowledgeItems(ctx, f.issue, base, f.synthID)
		item := f.getItemByTitle(t, "Cache invalidation is hard here")
		if item.Hits != 0 {
			t.Fatalf("fresh item should have 0 hits, got %d", item.Hits)
		}
		countBefore := f.countRows(t, "")
		// Re-capture the identical fact (different issue text irrelevant).
		f.svc.CaptureKnowledgeItems(ctx, f.issue, base, f.synthID)
		if got := f.countRows(t, ""); got != countBefore {
			t.Errorf("exact restatement must not add a row: before=%d after=%d", countBefore, got)
		}
		if reloaded := f.getItemByTitle(t, "Cache invalidation is hard here"); reloaded.Hits != item.Hits+1 {
			t.Errorf("exact restatement must bump hits: want %d, got %d", item.Hits+1, reloaded.Hits)
		}
	})

	t.Run("0.6 same-kind near match confirms", func(t *testing.T) {
		f := seedCaptureFixture(t, pool, q, "")
		f.svc.CaptureKnowledgeItems(ctx, f.issue,
			captureContent(`[{"kind":"gotcha","title":"alpha beta gamma delta","body":"b"}]`), f.synthID)
		item := f.getItemByTitle(t, "alpha beta gamma delta")
		countBefore := f.countRows(t, "")
		// 3 shared of union 5 -> jaccard 0.6 exactly (meets the 0.6 same-kind
		// threshold but is below the 0.8 cross-kind threshold). Same kind gotcha.
		f.svc.CaptureKnowledgeItems(ctx, f.issue,
			captureContent(`[{"kind":"gotcha","title":"alpha beta gamma epsilon","body":"b"}]`), f.synthID)
		if got := f.countRows(t, ""); got != countBefore {
			t.Errorf("same-kind near match must confirm, not insert: before=%d after=%d", countBefore, got)
		}
		if reloaded := f.getItemByTitle(t, "alpha beta gamma delta"); reloaded.Hits != item.Hits+1 {
			t.Errorf("same-kind near match must bump hits: want %d got %d", item.Hits+1, reloaded.Hits)
		}
	})

	t.Run("0.6 cross-kind inserts (needs 0.8 across kinds)", func(t *testing.T) {
		f := seedCaptureFixture(t, pool, q, "")
		f.svc.CaptureKnowledgeItems(ctx, f.issue,
			captureContent(`[{"kind":"gotcha","title":"one two three four","body":"b"}]`), f.synthID)
		countBefore := f.countRows(t, "")
		// jaccard 3/5 = 0.6 -> below the 0.8 cross-kind threshold -> inserts.
		f.svc.CaptureKnowledgeItems(ctx, f.issue,
			captureContent(`[{"kind":"nav","title":"one two three five","body":"b"}]`), f.synthID)
		if got := f.countRows(t, ""); got != countBefore+1 {
			t.Errorf("0.6 cross-kind must insert a new row: before=%d after=%d", countBefore, got)
		}
	})

	t.Run("0.8 cross-kind confirms", func(t *testing.T) {
		f := seedCaptureFixture(t, pool, q, "")
		f.svc.CaptureKnowledgeItems(ctx, f.issue,
			captureContent(`[{"kind":"gotcha","title":"one two three four five","body":"b"}]`), f.synthID)
		item := f.getItemByTitle(t, "one two three four five")
		countBefore := f.countRows(t, "")
		// {one,two,three,four} shared, union {one..five} -> jaccard 4/5 = 0.8,
		// meeting the cross-kind threshold -> confirms even across kinds.
		f.svc.CaptureKnowledgeItems(ctx, f.issue,
			captureContent(`[{"kind":"nav","title":"one two three four","body":"b"}]`), f.synthID)
		if got := f.countRows(t, ""); got != countBefore {
			t.Errorf("0.8 cross-kind must confirm, not insert: before=%d after=%d", countBefore, got)
		}
		if reloaded := f.getItemByTitle(t, "one two three four five"); reloaded.Hits != item.Hits+1 {
			t.Errorf("0.8 cross-kind must bump hits: want %d got %d", item.Hits+1, reloaded.Hits)
		}
	})

	t.Run("same-batch near-duplicates merge", func(t *testing.T) {
		f := seedCaptureFixture(t, pool, q, "")
		countBefore := f.countRows(t, "")
		// Two near-identical items in ONE comment: the second must be caught by
		// the in-memory accumulation of the first and confirm, not insert.
		content := captureContent(`[
			{"kind":"gotcha","title":"batch alpha beta gamma delta","body":"b"},
			{"kind":"gotcha","title":"batch alpha beta gamma delta epsilon","body":"b"}
		]`)
		f.svc.CaptureKnowledgeItems(ctx, f.issue, content, f.synthID)
		if got := f.countRows(t, ""); got != countBefore+1 {
			t.Errorf("same-batch near-dups must collapse to one row: before=%d after=%d (want +1)", countBefore, got)
		}
	})

	t.Run("non-synth restating active does NOT bump hits", func(t *testing.T) {
		f := seedCaptureFixture(t, pool, q, "")
		// Synth records an ACTIVE gotcha.
		f.svc.CaptureKnowledgeItems(ctx, f.issue,
			captureContent(`[{"kind":"gotcha","title":"trusted active learning here","body":"b"}]`), f.synthID)
		item := f.getItemByTitle(t, "trusted active learning here")
		if item.Status != "active" {
			t.Fatalf("precondition: item should be active, got %q", item.Status)
		}
		countBefore := f.countRows(t, "")
		// A non-synthesizer restates the SAME fact: rank-pump guard means no hit
		// bump and no new row (exact collision -> ignore-dup no-op).
		f.svc.CaptureKnowledgeItems(ctx, f.issue,
			captureContent(`[{"kind":"gotcha","title":"trusted active learning here","body":"b"}]`), f.otherID)
		if got := f.countRows(t, ""); got != countBefore {
			t.Errorf("untrusted restatement must not add a row: before=%d after=%d", countBefore, got)
		}
		if reloaded := f.getItemByTitle(t, "trusted active learning here"); reloaded.Hits != item.Hits {
			t.Errorf("untrusted restatement must NOT bump hits (rank-pump guard): before=%d after=%d", item.Hits, reloaded.Hits)
		}
	})
}
