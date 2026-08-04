package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// TestQAEvidenceFloorTierScoped pins the tier-scoped evidence floor: a UI-case
// "pass" with a real command but NO screenshot / trace is DOWNGRADED to qa:stale
// for a FULL-scope change (the blast radius earns the heavier visual-evidence
// bar), but ACCEPTED for a TRIVIAL / tiny-diff change — where the fast
// deterministic smoke is the proportionate floor and demanding a Playwright
// trace would dead-end every light-QA pass as stale. This locks the fix for the
// deterministic-first vs evidence-floor conflict.
func TestQAEvidenceFloorTierScoped(t *testing.T) {
	pool := knowledgeTestPool(t)
	ctx := context.Background()
	q := db.New(pool)
	wsID := seedKnowledgeWorkspace(t, pool)
	svc := NewTaskService(q, pool, nil, events.New())

	// A pass carrying exactly one command + no screenshot: clears the
	// unconditional zero-commands check, so only the UI-case branch decides.
	passOneCmd := qaResultPayload{
		Verdict:  "pass",
		Commands: []json.RawMessage{json.RawMessage(`{"cmd":"node smoke.js","branch_exit":0}`)},
	}

	// seedIssue makes an in_review issue with one UI-modality test case (which
	// arms the visual-evidence branch) and NO run carrying a trace.
	seedIssue := func(title string) db.Issue {
		t.Helper()
		var userID string
		if err := pool.QueryRow(ctx, `INSERT INTO "user" (name,email) VALUES ('floor',$1) RETURNING id`,
			"floor-"+uuid.NewString()[:8]+"@x.dev").Scan(&userID); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		var issueID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO issue (workspace_id,title,status,creator_type,creator_id,number)
			VALUES ($1,$2,'in_review','member',$3,$4) RETURNING id`,
			util.UUIDToString(wsID), title, userID, int64(uuid.New().ID()%100000)).Scan(&issueID); err != nil {
			t.Fatalf("seed issue: %v", err)
		}
		issue, err := q.GetIssue(ctx, util.MustParseUUID(issueID))
		if err != nil {
			t.Fatalf("GetIssue: %v", err)
		}
		if _, err := q.CreateTestCase(ctx, db.CreateTestCaseParams{
			WorkspaceID: wsID, IssueID: issue.ID, Title: "renders greeting",
			Steps: "open app", Expected: "greeting shows", Kind: "e2e", Source: "gen",
			AuthorType: "agent", Category: "smoke", Script: "", Preconditions: "",
			Priority: "normal", Modality: "ui", CriterionRef: "",
		}); err != nil {
			t.Fatalf("seed UI test case: %v", err)
		}
		return issue
	}

	attachTier := func(issue db.Issue, tier string) {
		t.Helper()
		label, err := q.CreateLabel(ctx, db.CreateLabelParams{WorkspaceID: wsID, Name: tier, Color: "#22c55e"})
		if err != nil {
			t.Fatalf("create %s label: %v", tier, err)
		}
		if err := q.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
			IssueID: issue.ID, LabelID: label.ID, WorkspaceID: wsID,
		}); err != nil {
			t.Fatalf("attach %s: %v", tier, err)
		}
	}

	// UNKNOWN scope (no tier label, no PR — the sprint-mode / no-per-task-PR
	// path that dead-ended EED-48): a UI-case pass with a real command but no
	// trace CLEARS the floor. We must NOT force the heaviest bar on a change
	// whose blast radius we can't even measure.
	unknown := seedIssue("unknown scope ui change")
	if gap := svc.qaEvidenceFloorGap(ctx, unknown, passOneCmd); gap != "" {
		t.Errorf("unknown-scope UI pass must clear the floor (deterministic commands suffice), got gap: %q", gap)
	}

	// TRIVIAL scope (tier:trivial label): same — light QA is not forced to
	// produce a trace.
	triv := seedIssue("trivial scope ui change")
	attachTier(triv, "tier:trivial")
	if gap := svc.qaEvidenceFloorGap(ctx, triv, passOneCmd); gap != "" {
		t.Errorf("trivial-scope UI pass must clear the floor (no trace needed), got gap: %q", gap)
	}

	// risk:guarded is POSITIVELY high-blast-radius → it DOES earn the visual bar,
	// even overriding a tier:light label. A UI-case pass with no trace gaps.
	guarded := seedIssue("guarded ui change")
	attachTier(guarded, "tier:light")
	attachTier(guarded, "risk:guarded")
	if gap := svc.qaEvidenceFloorGap(ctx, guarded, passOneCmd); gap == "" {
		t.Error("risk:guarded must force full evidence (→ gap) despite tier:light, got no gap")
	}

	// The unconditional zero-commands check still fires regardless of scope: a
	// pass that ran NOTHING is hollow no matter the blast radius.
	if gap := svc.qaEvidenceFloorGap(ctx, triv, qaResultPayload{Verdict: "pass"}); gap == "" {
		t.Error("zero-commands pass must always gap, even trivial scope")
	}
	if gap := svc.qaEvidenceFloorGap(ctx, unknown, qaResultPayload{Verdict: "pass"}); gap == "" {
		t.Error("zero-commands pass must always gap, even unknown scope")
	}

	// ZERO-STAT PR: a linked PR whose diff stats never synced (changed_files=0,
	// the PR-open webhook carries no counts) is UNKNOWN, not "confirmed large" —
	// it must NOT arm the visual bar (the EED-58 qa:stale regression: every
	// freshly-opened PR staled its honest light pass).
	zeroStat := seedIssue("zero-stat pr ui change")
	var prID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO github_pull_request (workspace_id, installation_id, repo_owner, repo_name, pr_number,
			title, state, html_url, pr_created_at, pr_updated_at, head_sha, additions, deletions, changed_files, provider)
		VALUES ($1::uuid, 1, 'o', 'r', (1000000+floor(random()*1000000))::int, 'zero-stat', 'open', 'https://x',
			now(), now(), 'abc123', 0, 0, 0, 'github') RETURNING id::text`,
		util.UUIDToString(wsID)).Scan(&prID); err != nil {
		t.Fatalf("seed zero-stat PR: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO issue_pull_request (issue_id, pull_request_id) VALUES ($1::uuid, $2::uuid)`,
		util.UUIDToString(zeroStat.ID), prID); err != nil {
		t.Fatalf("link zero-stat PR: %v", err)
	}
	if gap := svc.qaEvidenceFloorGap(ctx, zeroStat, passOneCmd); gap != "" {
		t.Errorf("zero-stat-PR UI pass must clear the floor (stats unsynced = unknown), got gap: %q", gap)
	}
}
