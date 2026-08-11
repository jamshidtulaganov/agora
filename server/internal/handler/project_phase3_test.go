package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// projectModuleKBName composes "<base>-<module-slug>", and returns "" when the
// base is unresolvable (Cyrillic title, no override) or the module is empty.
func TestProjectModuleKBName(t *testing.T) {
	p := db.Project{Title: "sd-main", Settings: []byte(`{}`)}
	if got := projectModuleKBName(p, "billing/payments"); got != "sd-main-kb-billing-payments" {
		t.Errorf("want sd-main-kb-billing-payments, got %q", got)
	}
	// override base + spaced module
	pov := db.Project{Title: "10 спринт", Settings: []byte(`{"kb_skill":"sd-main-kb"}`)}
	if got := projectModuleKBName(pov, "Stock Warehouse"); got != "sd-main-kb-stock-warehouse" {
		t.Errorf("want sd-main-kb-stock-warehouse, got %q", got)
	}
	// unresolvable base
	if got := projectModuleKBName(db.Project{Title: "спринт", Settings: nil}, "billing"); got != "" {
		t.Errorf("cyrillic base without override must yield empty, got %q", got)
	}
	// empty module
	if got := projectModuleKBName(p, "  "); got != "" {
		t.Errorf("empty module must yield empty, got %q", got)
	}
}

// buildModuleStudyPrompt (pure inputs via projectRiskMapForProject) resolves the
// module's paths from the risk map and names the target skill; rejects unknown.
func TestBuildModuleStudyPrompt(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ctx := t.Context()
	proj := db.Project{
		Title:       "sd-main",
		WorkspaceID: parseUUID(testWorkspaceID),
		Settings:    []byte(`{"kb_skill":"sd-main-kb","risk_map":[{"module":"billing","tier":"critical","paths":["protected/modules/pay/**","protected/modules/finans/**"]}]}`),
	}
	prompt, kbName, reason := testHandler.buildModuleStudyPrompt(ctx, proj, "billing")
	if reason != "" {
		t.Fatalf("unexpected reason: %s", reason)
	}
	if kbName != "sd-main-kb-billing" {
		t.Errorf("want kb sd-main-kb-billing, got %q", kbName)
	}
	for _, want := range []string{"MODULE knowledge base", "billing", "protected/modules/pay/**", "protected/modules/finans/**", "sd-main-kb-billing"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	// unknown module → reason, no prompt
	if _, _, r := testHandler.buildModuleStudyPrompt(ctx, proj, "nonsense"); r == "" {
		t.Error("unknown module must return a 400 reason")
	}
	// no risk map → reason
	if _, _, r := testHandler.buildModuleStudyPrompt(ctx, db.Project{Title: "x", Settings: []byte(`{}`)}, "billing"); r == "" {
		t.Error("no risk map must return a reason")
	}
}

// The autonomy aggregation math: pass rate = pass/(pass+fail), untested
// excluded; multi-module issues count under each module. (Pure aggregation is
// exercised through a small hand-rolled reduction mirroring the handler.)
func TestAutonomyAggregationMath(t *testing.T) {
	rows := []db.ProjectAutonomyRowsRow{
		{QaVerdict: "pass", Modules: []string{"module:billing"}},
		{QaVerdict: "pass", Modules: []string{"module:billing", "module:orders"}},
		{QaVerdict: "fail", Modules: []string{"module:billing"}},
		{QaVerdict: "", Modules: []string{"module:billing"}}, // untested
		{QaVerdict: "pass", Modules: []string{"module:orders"}},
	}
	type acc struct{ pass, fail, untested int }
	m := map[string]*acc{}
	for _, r := range rows {
		v := strings.ToLower(r.QaVerdict)
		for _, l := range r.Modules {
			mod := strings.TrimPrefix(l, "module:")
			a := m[mod]
			if a == nil {
				a = &acc{}
				m[mod] = a
			}
			switch v {
			case "pass":
				a.pass++
			case "fail":
				a.fail++
			default:
				a.untested++
			}
		}
	}
	if m["billing"].pass != 2 || m["billing"].fail != 1 || m["billing"].untested != 1 {
		t.Errorf("billing agg wrong: %+v", *m["billing"])
	}
	rate := float64(m["billing"].pass) / float64(m["billing"].pass+m["billing"].fail)
	if rate < 0.66 || rate > 0.67 {
		t.Errorf("billing pass rate want ~0.667, got %v", rate)
	}
	if m["orders"].pass != 2 || m["orders"].fail != 0 {
		t.Errorf("orders agg wrong: %+v", *m["orders"])
	}
}

// Every focused automation prompt must forbid delegation/fan-out.
func TestAutomationPromptsForbidDelegation(t *testing.T) {
	prompts := map[string]string{
		"base-suite":  baseSuitePromptTmpl + soloAutomationDirective,
		"triage":      bitrixTriagePrompt + soloAutomationDirective,
		"kb-study":    buildProjectStudyPrompt("p"),
	}
	for name, p := range prompts {
		if !strings.Contains(p, "do NOT delegate") || !strings.Contains(p, "do NOT @mention") {
			t.Errorf("%s prompt missing the solo/no-delegate directive", name)
		}
	}
}

// filterQAAgentsForScope drops specialist reviewers on a trivial change but
// never empties the roster.
func TestFilterQAAgentsForScope(t *testing.T) {
	roster := []db.Agent{
		{Name: "Lead QA Engineer"}, {Name: "QA Tester"},
		{Name: "Security Reviewer"}, {Name: "Designer"},
	}
	// non-trivial: unchanged
	if got := filterQAAgentsForScope(roster, false); len(got) != 4 {
		t.Errorf("non-trivial must keep all, got %d", len(got))
	}
	// trivial: drop Security + Designer, keep QA
	got := filterQAAgentsForScope(roster, true)
	names := map[string]bool{}
	for _, a := range got {
		names[a.Name] = true
	}
	if names["Security Reviewer"] || names["Designer"] {
		t.Errorf("trivial must drop specialists, got %v", names)
	}
	if !names["Lead QA Engineer"] || !names["QA Tester"] {
		t.Errorf("trivial must keep QA roles, got %v", names)
	}
	// never empty: a roster of only specialists falls back to the original
	if got := filterQAAgentsForScope([]db.Agent{{Name: "Security Reviewer"}}, true); len(got) != 1 {
		t.Errorf("must never return empty, got %d", len(got))
	}
}

// issueQAScopeTrivial: risk:critical vetoes; type:docs downgrades; DB-backed.
func TestIssueQAScopeTrivial(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ctx := t.Context()
	mkIssue := func() string {
		var pid, iid string
		if err := testPool.QueryRow(ctx,
			`INSERT INTO project (workspace_id, title, status, priority) VALUES ($1::uuid,'scope-p-'||gen_random_uuid(),'planned','none') RETURNING id::text`,
			testWorkspaceID).Scan(&pid); err != nil {
			t.Fatal(err)
		}
		if err := testPool.QueryRow(ctx,
			`INSERT INTO issue (workspace_id, project_id, title, creator_type, creator_id, number) VALUES ($1::uuid,$2::uuid,'scope issue','member',$3::uuid,(3000000+floor(random()*1000000))::int) RETURNING id::text`,
			testWorkspaceID, pid, testUserID).Scan(&iid); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			c := context.Background()
			testPool.Exec(c, `DELETE FROM issue WHERE id=$1::uuid`, iid)
			testPool.Exec(c, `DELETE FROM project WHERE id=$1::uuid`, pid)
		})
		return iid
	}
	label := func(iid, name string) {
		var lid string
		testPool.QueryRow(ctx, `INSERT INTO issue_label (workspace_id,name,color) VALUES ($1::uuid,$2,'#000') RETURNING id::text`, testWorkspaceID, name).Scan(&lid)
		testPool.Exec(ctx, `INSERT INTO issue_to_label (issue_id,label_id) VALUES ($1::uuid,$2::uuid)`, iid, lid)
		t.Cleanup(func() {
			c := context.Background()
			testPool.Exec(c, `DELETE FROM issue_to_label WHERE label_id=$1::uuid`, lid)
			testPool.Exec(c, `DELETE FROM issue_label WHERE id=$1::uuid`, lid)
		})
	}
	load := func(iid string) db.Issue {
		i, err := testHandler.Queries.GetIssue(ctx, parseUUID(iid))
		if err != nil {
			t.Fatal(err)
		}
		return i
	}

	// type:docs → trivial
	d := mkIssue()
	label(d, "type:docs")
	if !testHandler.issueQAScopeTrivial(ctx, load(d)) {
		t.Error("type:docs must scope trivial")
	}
	// risk:critical vetoes even with type:docs
	c := mkIssue()
	label(c, "type:docs")
	label(c, "risk:critical")
	if testHandler.issueQAScopeTrivial(ctx, load(c)) {
		t.Error("risk:critical must veto trivial")
	}
	// no labels, no PR → NOT trivial (roster shedding stays full-safe)
	n := mkIssue()
	if testHandler.issueQAScopeTrivial(ctx, load(n)) {
		t.Error("no signal must not scope trivial")
	}

	// Direct 3-way issueQAScope: type:docs → light, risk:critical → full, and
	// the no-signal case → SELF (the agent sizes the diff from git itself — the
	// sprint-mode no-PR path that must NOT be forced onto the full lead-delegated
	// gate just because there is no PR to measure).
	if got := testHandler.issueQAScope(ctx, load(d)); got != qaScopeLight {
		t.Errorf("type:docs issueQAScope = %d, want qaScopeLight(%d)", got, qaScopeLight)
	}
	if got := testHandler.issueQAScope(ctx, load(c)); got != qaScopeFull {
		t.Errorf("risk:critical issueQAScope = %d, want qaScopeFull(%d)", got, qaScopeFull)
	}
	if got := testHandler.issueQAScope(ctx, load(n)); got != qaScopeSelf {
		t.Errorf("no-signal issueQAScope = %d, want qaScopeSelf(%d)", got, qaScopeSelf)
	}

	// ZERO-STAT PR: a linked PR row whose diff stats never synced
	// (changed_files=0 — the PR-open webhook carries no file counts) is
	// UNKNOWN, not "confirmed large". It must scope SELF, not FULL — treating
	// it as large routed every freshly-opened PR to the lead-delegated full
	// gate and armed the visual-evidence floor (the EED-58 qa:stale regression).
	z := mkIssue()
	var prID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO github_pull_request (workspace_id, installation_id, repo_owner, repo_name, pr_number,
			title, state, html_url, pr_created_at, pr_updated_at, head_sha, additions, deletions, changed_files, provider)
		VALUES ($1::uuid, 1, 'o', 'r', (1000000+floor(random()*1000000))::int, 'zero-stat', 'open', 'https://x',
			now(), now(), 'abc123', 0, 0, 0, 'github') RETURNING id::text`,
		testWorkspaceID).Scan(&prID); err != nil {
		t.Fatalf("seed zero-stat PR: %v", err)
	}
	if _, err := testPool.Exec(ctx,
		`INSERT INTO issue_pull_request (issue_id, pull_request_id) VALUES ($1::uuid, $2::uuid)`, z, prID); err != nil {
		t.Fatalf("link zero-stat PR: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		testPool.Exec(c, `DELETE FROM issue_pull_request WHERE pull_request_id=$1::uuid`, prID)
		testPool.Exec(c, `DELETE FROM github_pull_request WHERE id=$1::uuid`, prID)
	})
	if got := testHandler.issueQAScope(ctx, load(z)); got != qaScopeSelf {
		t.Errorf("zero-stat PR issueQAScope = %d, want qaScopeSelf(%d) — stats-unsynced is unknown, not large", got, qaScopeSelf)
	}
}

// pickAutomationRunner returns the project lead when the lead has no running
// tasks (the default / single-task path). Spread-under-load is verified live.
func TestPickAutomationRunner_LeadFree(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ctx := t.Context()
	// A lead with no running tasks: CountRunningTasks returns 0 (no rows), so the
	// lead-free path returns the lead without consulting readiness — a fresh UUID
	// stands in for "a lead that has nothing in flight".
	var aid string
	testPool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&aid)
	proj := db.Project{
		WorkspaceID: parseUUID(testWorkspaceID),
		LeadType:    pgtype.Text{String: "agent", Valid: true},
		LeadID:      parseUUID(aid),
	}
	got := testHandler.pickAutomationRunner(ctx, proj)
	if uuidToString(got) != aid {
		t.Errorf("lead-free must return the lead %s, got %s", aid, uuidToString(got))
	}
}
