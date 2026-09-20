package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// THE RANKED DECISION QUEUE (docs/orchestration-upgrade-plan.md §A2).
//
// The properties asserted here are the ones the product claim rests on:
//
//   - every kind of pending human decision appears, and nothing else does;
//   - the ranking is blast radius FIRST — a critical merge outranks an
//     escalation, which is the entire reason for ranking by risk rather than by
//     arrival;
//   - the tier on each row is SERVER-DERIVED where a diff exists, and says so;
//   - the total is EXACT (the query has no LIMIT), so "you have N decisions" is
//     a true sentence;
//   - the workspace fence holds.

// ── the pure ranking function ───────────────────────────────────────────────

func TestDecisionScore(t *testing.T) {
	// Blast radius dominates: a critical item outranks a same-age escalation on
	// tier alone. This is the ordering §A2 asks for, stated as a test so a
	// future weight change has to face it.
	criticalMerge := decisionScore(riskTierCritical, decisionKindMergeReady, "", 0)
	guardedEscalation := decisionScore(riskTierGuarded, decisionKindEscalation, "", 0)
	if criticalMerge <= guardedEscalation {
		t.Errorf("a critical merge (%v) must outrank a guarded escalation (%v)", criticalMerge, guardedEscalation)
	}
	// Unclassified sits between safe and guarded — "nobody tiered this" is more
	// urgent than known-safe and less urgent than known-fragile (§A1.3).
	safe := decisionRiskWeight(riskTierSafe)
	unclassified := decisionRiskWeight(riskTierUnclassified)
	guarded := decisionRiskWeight(riskTierGuarded)
	if !(safe < unclassified && unclassified < guarded) {
		t.Errorf("unclassified must rank between safe and guarded: %v / %v / %v", safe, unclassified, guarded)
	}
	// An unknown future tier degrades to the unclassified weight rather than
	// scoring zero and sinking out of sight (CLAUDE.md enum drift).
	if decisionRiskWeight("catastrophic") != unclassified {
		t.Error("an unknown tier must degrade to the unclassified weight")
	}
	// Age is capped, so a forgotten safe item never buries a fresh critical one.
	if got := decisionAgeWeight(10_000); got != decisionAgeWeightMax {
		t.Errorf("age weight must cap at %v, got %v", decisionAgeWeightMax, got)
	}
	if got := decisionAgeWeight(-5); got != 0 {
		t.Errorf("a negative age (clock skew) must score 0, got %v", got)
	}
	// Only the two "waiting on a person" staleness reasons earn the bonus.
	if decisionStaleBonusFor(staleReasonReviewDone) != decisionStaleBonus ||
		decisionStaleBonusFor(staleReasonIdle) != decisionStaleBonus {
		t.Error("review_done and idle must earn the stale bonus")
	}
	if decisionStaleBonusFor(staleReasonReopenedWork) != 0 || decisionStaleBonusFor("") != 0 {
		t.Error("only review_done / idle earn the stale bonus")
	}
}

// One row per issue: the highest-priority pending decision wins, and a red gate
// is never reported as "ready to merge".
func TestDecisionKindForRow(t *testing.T) {
	escalated := decisionRow("in_review", 1)
	escalated.EscalationID = parseUUID("11111111-1111-1111-1111-111111111111")
	if kind, _ := decisionKindForRow(escalated, map[string]bool{"qa:fail": true}); kind != decisionKindEscalation {
		t.Errorf("a parked agent outranks every other signal, got %q", kind)
	}

	cases := []struct {
		name     string
		status   string
		openPRs  int64
		labels   map[string]bool
		wantKind string
		wantNeed string
	}{
		{"in_review with nothing red", "in_review", 1, nil, decisionKindMergeReady, decisionNeedReviewAndApprove},
		{"approved but unmerged", "in_progress", 1, map[string]bool{"merge:approved": true}, decisionKindMergeReady, decisionNeedMergeApprovedPR},
		{"approved and already merged is not a decision", "in_progress", 0, map[string]bool{"merge:approved": true}, "", ""},
		{"qa:fail beats in_review", "in_review", 1, map[string]bool{"qa:fail": true}, decisionKindQAFailed, decisionNeedActOnQAFail},
		{"qa:blocked is a QA decision too", "in_review", 1, map[string]bool{"qa:blocked": true}, decisionKindQAFailed, decisionNeedActOnQABlocked},
		{"review:fail", "in_progress", 1, map[string]bool{"review:fail": true}, decisionKindReviewFailed, decisionNeedActOnReviewFail},
		{"qa:fail wins over review:fail (QA is upstream)", "in_review", 1, map[string]bool{"qa:fail": true, "review:fail": true}, decisionKindQAFailed, decisionNeedActOnQAFail},
		{"nothing pending", "in_progress", 0, nil, "", ""},
	}
	for _, c := range cases {
		row := decisionRow(c.status, c.openPRs)
		kind, need := decisionKindForRow(row, c.labels)
		if kind != c.wantKind || need != c.wantNeed {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.name, kind, need, c.wantKind, c.wantNeed)
		}
	}
}

// Every needed code renders a sentence — a client that does not know a code
// still shows something, and the escalation's own question is what it shows.
func TestDecisionNeededSentence(t *testing.T) {
	if got := decisionNeededSentence(decisionNeedAnswerEscalation, "Which tariff applies to returns?"); got == "" ||
		!jsonErrorContains([]byte(got), "Which tariff") {
		t.Errorf("an escalation's sentence must carry the question, got %q", got)
	}
	if decisionNeededSentence(decisionNeedAnswerEscalation, "  ") == "" {
		t.Error("a blank prompt must still render a sentence")
	}
	if decisionNeededSentence("a_code_from_a_newer_server", "") == "" {
		t.Error("an unknown needed code must degrade to a generic sentence, not an empty cell")
	}
}

// ── the endpoint, against a real database ───────────────────────────────────

type decisionQueueFixture struct {
	riskProjectID  string
	plainProjectID string
	escalationID   string
	escalatedIssue string
	mergeIssue     string
	qaIssue        string
	reviewIssue    string
	approvedIssue  string
	doneIssue      string
	plainIssue     string
}

func newDecisionQueueFixture(t *testing.T) decisionQueueFixture {
	t.Helper()
	ctx := t.Context()
	f := decisionQueueFixture{}

	riskMap := `[{"module":"billing","tier":"critical","paths":["pay/**"]},
	             {"module":"docs","tier":"safe","paths":["docs/**"]}]`
	if err := testPool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title, settings)
		 VALUES ($1::uuid, 'Decision Queue Risk', jsonb_build_object('risk_map', $2::jsonb))
		 RETURNING id::text`, testWorkspaceID, riskMap).Scan(&f.riskProjectID); err != nil {
		t.Fatalf("create risk project: %v", err)
	}
	if err := testPool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title) VALUES ($1::uuid, 'Decision Queue Plain') RETURNING id::text`,
		testWorkspaceID).Scan(&f.plainProjectID); err != nil {
		t.Fatalf("create plain project: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		testPool.Exec(c, `DELETE FROM project WHERE id = ANY($1::uuid[])`, []string{f.riskProjectID, f.plainProjectID})
	})

	newIssue := func(title, status, projectID string) string {
		t.Helper()
		var id string
		if err := testPool.QueryRow(ctx,
			`INSERT INTO issue (workspace_id, project_id, title, status, creator_type, creator_id, number)
			 VALUES ($1::uuid, $2::uuid, $3, $4, 'member', $5::uuid,
			         (4000000 + floor(random()*900000))::int)
			 RETURNING id::text`,
			testWorkspaceID, projectID, title, status, testUserID).Scan(&id); err != nil {
			t.Fatalf("create issue %s: %v", title, err)
		}
		t.Cleanup(func() {
			testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1::uuid`, id)
		})
		return id
	}

	f.escalatedIssue = newIssue("parked on a human", "in_progress", f.riskProjectID)
	f.mergeIssue = newIssue("money path awaiting approval", "in_review", f.riskProjectID)
	f.qaIssue = newIssue("qa came back red", "in_progress", f.riskProjectID)
	f.reviewIssue = newIssue("review found blockers", "in_progress", f.riskProjectID)
	f.approvedIssue = newIssue("approved, still unmerged", "in_progress", f.riskProjectID)
	f.doneIssue = newIssue("shipped last week", "done", f.riskProjectID)
	f.plainIssue = newIssue("untiered project", "in_review", f.plainProjectID)

	// The escalation is inserted directly so raised_at is controlled (the
	// service path would also post a comment and move the activity clock).
	if err := testPool.QueryRow(ctx,
		`INSERT INTO task_escalation (workspace_id, issue_id, kind, prompt, detail, options, risk_tier, raised_at)
		 VALUES ($1::uuid, $2::uuid, 'question', 'Which tariff applies to returns?', 'checked the spec',
		         ARRAY['Standard','Reduced'], 'guarded', now() - interval '10 hours')
		 RETURNING id::text`, testWorkspaceID, f.escalatedIssue).Scan(&f.escalationID); err != nil {
		t.Fatalf("raise escalation: %v", err)
	}

	attachLabel(t, f.qaIssue, "qa:fail")
	attachLabel(t, f.reviewIssue, "review:fail")
	attachLabel(t, f.approvedIssue, "merge:approved")
	attachLabel(t, f.doneIssue, "qa:pass")

	// A money-path PR on the merge issue (derived CRITICAL) and a docs PR on
	// the approved issue (derived SAFE) — the two ends of the derivation.
	linkPR(t, f.mergeIssue, "open", []string{"pay/Invoice.php", "docs/readme.md"})
	linkPR(t, f.approvedIssue, "open", []string{"docs/readme.md"})
	return f
}

func attachLabel(t *testing.T, issueID, name string) {
	t.Helper()
	ctx := t.Context()
	var labelID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO issue_label (workspace_id, name, color) VALUES ($1::uuid, $2, '#000000')
		 RETURNING id::text`, testWorkspaceID, name).Scan(&labelID); err != nil {
		t.Fatalf("create label %s: %v", name, err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue_label WHERE id = $1::uuid`, labelID)
	})
	if _, err := testPool.Exec(ctx,
		`INSERT INTO issue_to_label (issue_id, label_id) VALUES ($1::uuid, $2::uuid)`, issueID, labelID); err != nil {
		t.Fatalf("attach label %s: %v", name, err)
	}
}

func linkPR(t *testing.T, issueID, state string, paths []string) {
	t.Helper()
	ctx := t.Context()
	var prID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO github_pull_request
		   (workspace_id, installation_id, repo_owner, repo_name, pr_number, title, state, html_url,
		    pr_created_at, pr_updated_at, head_sha, changed_paths)
		 VALUES ($1::uuid, 42, 'acme', 'app', (100000 + floor(random()*800000))::int,
		         'test pr', $2, 'https://example.test/pr', now(), now(), 'deadbeef', $3::text[])
		 RETURNING id::text`, testWorkspaceID, state, paths).Scan(&prID); err != nil {
		t.Fatalf("create pr: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM github_pull_request WHERE id = $1::uuid`, prID)
	})
	if _, err := testPool.Exec(ctx,
		`INSERT INTO issue_pull_request (issue_id, pull_request_id) VALUES ($1::uuid, $2::uuid)`,
		issueID, prID); err != nil {
		t.Fatalf("link pr: %v", err)
	}
}

func fetchDecisionQueue(t *testing.T, projectID string) DecisionQueueResponse {
	t.Helper()
	path := "/api/issues/decision-queue"
	if projectID != "" {
		path += "?project_id=" + projectID
	}
	w := httptest.NewRecorder()
	testHandler.ListDecisionQueue(w, newRequest(http.MethodGet, path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("decision queue: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp DecisionQueueResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestDecisionQueueReturnsEveryKindAndRanksThem(t *testing.T) {
	f := newDecisionQueueFixture(t)
	resp := fetchDecisionQueue(t, f.riskProjectID)

	// EXACT TOTALS: the query has no LIMIT, so the count is the real count and
	// the per-kind breakdown adds up to it.
	if resp.Total != len(resp.Items) {
		t.Errorf("total (%d) must equal the number of items returned (%d)", resp.Total, len(resp.Items))
	}
	summed := 0
	for _, n := range resp.Counts {
		summed += n
	}
	if summed != resp.Total {
		t.Errorf("per-kind counts (%d) must sum to the total (%d): %v", summed, resp.Total, resp.Counts)
	}
	if resp.Total != 5 {
		t.Fatalf("expected exactly the 5 pending decisions, got %d: %s", resp.Total, decisionDump(resp.Items))
	}

	byIssue := map[string]DecisionQueueItem{}
	for _, it := range resp.Items {
		byIssue[it.IssueID] = it
	}
	if _, found := byIssue[f.doneIssue]; found {
		t.Error("a finished issue with a green verdict is not a decision")
	}

	for _, c := range []struct {
		issue, kind, tier, source string
	}{
		{f.escalatedIssue, decisionKindEscalation, riskTierGuarded, riskSourceMapDefault},
		{f.mergeIssue, decisionKindMergeReady, riskTierCritical, riskSourceDerived},
		{f.qaIssue, decisionKindQAFailed, riskTierGuarded, riskSourceMapDefault},
		{f.reviewIssue, decisionKindReviewFailed, riskTierGuarded, riskSourceMapDefault},
		{f.approvedIssue, decisionKindMergeReady, riskTierSafe, riskSourceDerived},
	} {
		item, found := byIssue[c.issue]
		if !found {
			t.Errorf("issue %s is waiting on a human and did not appear in the queue", c.issue)
			continue
		}
		if item.Kind != c.kind {
			t.Errorf("issue %s: kind = %q, want %q", c.issue, item.Kind, c.kind)
		}
		if item.RiskTier != c.tier || item.RiskTierSource != c.source {
			t.Errorf("issue %s: risk = (%q, %q), want (%q, %q)", c.issue, item.RiskTier, item.RiskTierSource, c.tier, c.source)
		}
		if item.Identifier == "" || item.Needed == "" || item.NeededCode == "" {
			t.Errorf("issue %s: every row must name itself and what it needs: %+v", c.issue, item)
		}
	}

	// The escalation carries its question and its options inline, so the queue
	// (and a Telegram keyboard) can answer without a second round trip.
	esc := byIssue[f.escalatedIssue].Escalation
	if esc == nil || esc.ID != f.escalationID || len(esc.Options) != 2 || esc.Prompt == "" {
		t.Fatalf("the escalation row must carry its own detail, got %+v", esc)
	}
	if byIssue[f.escalatedIssue].AgeHours < 9 {
		t.Errorf("an escalation's age measures from raised_at, got %v", byIssue[f.escalatedIssue].AgeHours)
	}

	// BLAST RADIUS FIRST. The critical merge outranks the older escalation, and
	// the safe merge sinks below both red verdicts.
	if resp.Items[0].IssueID != f.mergeIssue {
		t.Errorf("a critical-tier merge must lead the queue, got %s (%+v)", resp.Items[0].IssueID, resp.Items[0])
	}
	if resp.Items[1].IssueID != f.escalatedIssue {
		t.Errorf("the escalation must rank second, got %s", resp.Items[1].IssueID)
	}
	if resp.Items[len(resp.Items)-1].IssueID != f.approvedIssue {
		t.Errorf("the safe-tier item must rank last, got %s", resp.Items[len(resp.Items)-1].IssueID)
	}
	// The ordering the server sent is the ordering its own scores imply.
	for i := 1; i < len(resp.Items); i++ {
		if resp.Items[i-1].Score < resp.Items[i].Score {
			t.Fatalf("items are not sorted by score: %v", decisionDump(resp.Items))
		}
	}
}

// A project with no risk map yields `unclassified`, explicitly — never "" and
// never "safe" (§A1.3).
func TestDecisionQueueUnclassifiedProject(t *testing.T) {
	f := newDecisionQueueFixture(t)
	resp := fetchDecisionQueue(t, f.plainProjectID)
	if resp.Total != 1 {
		t.Fatalf("expected the one untiered decision, got %d", resp.Total)
	}
	item := resp.Items[0]
	if item.RiskTier != riskTierUnclassified || item.RiskTierSource != riskSourceNone {
		t.Errorf("an untiered project must report unclassified, got (%q, %q)", item.RiskTier, item.RiskTierSource)
	}
}

// The workspace fence: another tenant's pending decisions are not ours.
func TestDecisionQueueWorkspaceFence(t *testing.T) {
	ctx := t.Context()
	var otherWS, otherUser, otherIssue string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO workspace (name, slug, description, issue_prefix)
		 VALUES ('Decision Fence', 'decision-fence-'||substr(gen_random_uuid()::text,1,8), '', 'DEC')
		 RETURNING id::text`).Scan(&otherWS); err != nil {
		t.Fatalf("create other workspace: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, otherWS) })
	if err := testPool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ('Fence User', 'fence-'||substr(gen_random_uuid()::text,1,8)||'@test.local')
		 RETURNING id::text`).Scan(&otherUser); err != nil {
		t.Fatalf("create other user: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, otherUser) })
	if err := testPool.QueryRow(ctx,
		`INSERT INTO issue (workspace_id, title, status, creator_type, creator_id, number)
		 VALUES ($1::uuid, 'foreign decision', 'in_review', 'member', $2::uuid, 77)
		 RETURNING id::text`, otherWS, otherUser).Scan(&otherIssue); err != nil {
		t.Fatalf("create foreign issue: %v", err)
	}

	items, err := testHandler.buildDecisionQueue(ctx, parseUUID(testWorkspaceID), pgUUIDZero(), pgUUIDZero())
	if err != nil {
		t.Fatalf("build queue: %v", err)
	}
	for _, it := range items {
		if it.IssueID == otherIssue {
			t.Fatal("a foreign workspace's decision crossed the fence")
		}
	}
}

// ── small helpers ───────────────────────────────────────────────────────────

// decisionRow builds the query row shape for the pure kind-selection tests, so
// those can run without a database.
func decisionRow(status string, openPRs int64) db.ListDecisionQueueIssuesRow {
	return db.ListDecisionQueueIssuesRow{Status: status, OpenPrCount: openPRs}
}

func pgUUIDZero() pgtype.UUID { return pgtype.UUID{} }

// decisionDump renders the queue for a failure message. Named distinctly from
// the suite's existing mustJSON helper.
func decisionDump(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}
