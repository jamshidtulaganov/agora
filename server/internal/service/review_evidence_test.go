package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

func TestParseReviewResultBlock(t *testing.T) {
	t.Run("valid block amid prose", func(t *testing.T) {
		content := "## Review\n\nOne blocker in the handler.\n\n" +
			"```review-result\n" +
			`{"verdict":"fail","summary":"1 blocker","commit_sha":"AB12cd34ef56","files_reviewed":7,"findings":[{"file":"internal/handler/x.go","line":42,"severity":"blocker","title":"nil deref","detail":"issue.AssigneeID may be invalid"}]}` +
			"\n```\n"
		p, ok := ParseReviewResultBlock(content)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if p.Verdict != "fail" || p.Summary != "1 blocker" || p.FilesReviewed != 7 {
			t.Errorf("payload = %+v", p)
		}
		if p.CommitSha != "ab12cd34ef56" {
			t.Errorf("commit_sha = %q, want lowercased ab12cd34ef56", p.CommitSha)
		}
		if len(p.Findings) != 1 || p.Findings[0].Severity != "blocker" || p.Findings[0].Line == nil || *p.Findings[0].Line != 42 {
			t.Errorf("findings = %+v", p.Findings)
		}
	})

	t.Run("null line survives distinctly", func(t *testing.T) {
		content := "```review-result\n" +
			`{"verdict":"pass","summary":"clean","files_reviewed":2,"findings":[{"file":"a.go","line":null,"severity":"minor","title":"nit","detail":"style"}]}` +
			"\n```"
		p, ok := ParseReviewResultBlock(content)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if p.Findings[0].Line != nil {
			t.Errorf("line = %v, want nil", *p.Findings[0].Line)
		}
	})

	t.Run("no block", func(t *testing.T) {
		if _, ok := ParseReviewResultBlock("just prose, and a ```qa-result\n{}\n``` block"); ok {
			t.Error("expected ok=false when no review-result block")
		}
	})

	t.Run("malformed json", func(t *testing.T) {
		if _, ok := ParseReviewResultBlock("```review-result\n{not valid}\n```"); ok {
			t.Error("expected ok=false on malformed JSON")
		}
	})

	t.Run("invalid verdict", func(t *testing.T) {
		if _, ok := ParseReviewResultBlock("```review-result\n" + `{"verdict":"maybe","summary":"x"}` + "\n```"); ok {
			t.Error("expected ok=false on a verdict that is neither pass nor fail")
		}
	})

	t.Run("garbage commit sha fails open to empty", func(t *testing.T) {
		p, ok := ParseReviewResultBlock("```review-result\n" + `{"verdict":"pass","summary":"x","commit_sha":"not a sha"}` + "\n```")
		if !ok || p.CommitSha != "" {
			t.Errorf("ok=%v commit_sha=%q, want ok=true and empty sha", ok, p.CommitSha)
		}
	})
}

// seedReviewIssue creates an isolated issue in a fresh workspace for the
// capture tests.
func seedReviewIssue(t *testing.T, pool *pgxpool.Pool, q *db.Queries) db.Issue {
	t.Helper()
	ctx := context.Background()
	wsID := seedKnowledgeWorkspace(t, pool)
	var creatorID string
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name,email) VALUES ('rev-creator',$1) RETURNING id`,
		"rev-creator-"+uuid.NewString()[:8]+"@x.dev").Scan(&creatorID); err != nil {
		t.Fatalf("seed creator: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM "user" WHERE id=$1`, creatorID) })
	var issueID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id,title,status,creator_type,creator_id,number)
		VALUES ($1,'review capture issue','in_review','member',$2,1) RETURNING id`,
		util.UUIDToString(wsID), creatorID).Scan(&issueID); err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issue, err := q.GetIssue(ctx, util.MustParseUUID(issueID))
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	return issue
}

// TestCaptureReviewEvidence covers the label-first capture contract: a valid
// verdict attaches its label, the opposite verdict replaces it (replace-on-
// write), a repeated identical verdict is not a NEW attach, and a malformed
// block touches nothing.
func TestCaptureReviewEvidence(t *testing.T) {
	pool := knowledgeTestPool(t)
	ctx := context.Background()
	q := db.New(pool)
	issue := seedReviewIssue(t, pool, q)
	svc := NewTaskService(q, pool, nil, events.New())

	hasLabel := func(name string) bool {
		return svc.issueHasLabelName(ctx, issue, name)
	}

	// 1. Malformed block → nothing happens.
	if v, newly := svc.CaptureReviewEvidence(ctx, issue, "```review-result\n{broken\n```", pgtype.UUID{}); v != "" || newly {
		t.Errorf("malformed: got (%q,%v), want (\"\",false)", v, newly)
	}
	if hasLabel(ReviewLabelPass) || hasLabel(ReviewLabelFail) {
		t.Error("malformed block must not attach any review label")
	}

	// 2. Fail verdict → review:fail attached, newly=true.
	fail := "```review-result\n" + `{"verdict":"fail","summary":"blocker found","files_reviewed":3,"findings":[{"file":"a.go","line":1,"severity":"blocker","title":"t","detail":"d"}]}` + "\n```"
	if v, newly := svc.CaptureReviewEvidence(ctx, issue, fail, pgtype.UUID{}); v != "fail" || !newly {
		t.Errorf("fail capture: got (%q,%v), want (fail,true)", v, newly)
	}
	if !hasLabel(ReviewLabelFail) {
		t.Error("review:fail not attached")
	}

	// 3. Same verdict again → not a NEW attach.
	if v, newly := svc.CaptureReviewEvidence(ctx, issue, fail, pgtype.UUID{}); v != "fail" || newly {
		t.Errorf("repeat fail: got (%q,%v), want (fail,false)", v, newly)
	}

	// 4. Pass verdict REPLACES the fail (replace-on-write).
	pass := "```review-result\n" + `{"verdict":"pass","summary":"clean now","files_reviewed":3,"findings":[]}` + "\n```"
	if v, newly := svc.CaptureReviewEvidence(ctx, issue, pass, pgtype.UUID{}); v != "pass" || !newly {
		t.Errorf("pass capture: got (%q,%v), want (pass,true)", v, newly)
	}
	if !hasLabel(ReviewLabelPass) {
		t.Error("review:pass not attached")
	}
	if hasLabel(ReviewLabelFail) {
		t.Error("review:fail must be detached when review:pass lands (replace-on-write)")
	}
}

// TestCaptureReviewEvidenceSelfReviewRejected covers the reviewer≠author
// invariant enforced at CAPTURE (finding 1): the AUTHOR agent posting a passing
// review-result block for its own diff must NOT mint review:pass; a distinct
// reviewer's verdict is accepted.
func TestCaptureReviewEvidenceSelfReviewRejected(t *testing.T) {
	pool := knowledgeTestPool(t)
	ctx := context.Background()
	q := db.New(pool)
	issue := seedReviewIssue(t, pool, q)
	svc := NewTaskService(q, pool, nil, events.New())

	var runtimeID, agentID string
	if err := pool.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id,name,runtime_mode,provider,status,metadata,last_seen_at) VALUES ($1,'self-rt','cloud','claude','online','{}'::jsonb,now()) RETURNING id`,
		util.UUIDToString(issue.WorkspaceID)).Scan(&runtimeID); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id,name,runtime_mode,runtime_config,runtime_id,visibility,max_concurrent_tasks)
		VALUES ($1,'Author','cloud','{}'::jsonb,$2,'workspace',1) RETURNING id`,
		util.UUIDToString(issue.WorkspaceID), runtimeID).Scan(&agentID); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	// Make the agent the issue's assignee (the AUTHOR).
	if _, err := pool.Exec(ctx, `UPDATE issue SET assignee_type='agent', assignee_id=$1::uuid WHERE id=$2::uuid`,
		agentID, util.UUIDToString(issue.ID)); err != nil {
		t.Fatalf("assign issue: %v", err)
	}
	issue, err := q.GetIssue(ctx, issue.ID)
	if err != nil {
		t.Fatalf("reload issue: %v", err)
	}

	pass := "```review-result\n" + `{"verdict":"pass","summary":"lgtm (by author)","files_reviewed":2,"findings":[]}` + "\n```"
	// The AUTHOR agent reviews its own diff → REJECTED, no label.
	if v, newly := svc.CaptureReviewEvidence(ctx, issue, pass, util.MustParseUUID(agentID)); v != "" || newly {
		t.Errorf("self-review: got (%q,%v), want (\"\",false)", v, newly)
	}
	if svc.issueHasLabelName(ctx, issue, ReviewLabelPass) {
		t.Error("a self-authored review-result must NOT attach review:pass")
	}

	// A DIFFERENT reviewer's verdict is accepted.
	if v, newly := svc.CaptureReviewEvidence(ctx, issue, pass, util.MustParseUUID(uuid.NewString())); v != "pass" || !newly {
		t.Errorf("distinct reviewer: got (%q,%v), want (pass,true)", v, newly)
	}
	if !svc.issueHasLabelName(ctx, issue, ReviewLabelPass) {
		t.Error("a distinct reviewer's review:pass must attach")
	}
}

// TestLatestReviewResultForIssue verifies the newest-first comment resolution:
// the latest AGENT comment with a valid block wins; unparsable blocks are
// skipped; no block → found=false.
func TestLatestReviewResultForIssue(t *testing.T) {
	pool := knowledgeTestPool(t)
	ctx := context.Background()
	q := db.New(pool)
	issue := seedReviewIssue(t, pool, q)
	svc := NewTaskService(q, pool, nil, events.New())

	if _, _, _, _, found, err := svc.LatestReviewResultForIssue(ctx, issue); err != nil || found {
		t.Fatalf("empty issue: found=%v err=%v, want found=false", found, err)
	}

	var runtimeID, agentID string
	if err := pool.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id,name,runtime_mode,provider,status,metadata,last_seen_at) VALUES ($1,'rev-rt','cloud','claude','online','{}'::jsonb,now()) RETURNING id`,
		util.UUIDToString(issue.WorkspaceID)).Scan(&runtimeID); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id,name,runtime_mode,runtime_config,runtime_id,visibility,max_concurrent_tasks)
		VALUES ($1,'Reviewer','cloud','{}'::jsonb,$2,'workspace',1) RETURNING id`,
		util.UUIDToString(issue.WorkspaceID), runtimeID).Scan(&agentID); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	post := func(authorType, authorID, content string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO comment (issue_id,workspace_id,author_type,author_id,content,type)
			VALUES ($1,$2,$3,$4,$5,'comment')`,
			util.UUIDToString(issue.ID), util.UUIDToString(issue.WorkspaceID), authorType, authorID, content); err != nil {
			t.Fatalf("seed comment: %v", err)
		}
	}

	post("agent", agentID, "first verdict\n```review-result\n"+`{"verdict":"fail","summary":"old","files_reviewed":1,"findings":[]}`+"\n```")
	post("agent", agentID, "second verdict\n```review-result\n"+`{"verdict":"pass","summary":"newest","files_reviewed":2,"findings":[]}`+"\n```")
	// An unparsable newer block must be skipped, not resolve as the latest.
	post("agent", agentID, "broken\n```review-result\n{oops\n```")

	p, commentID, reviewerID, reviewedAt, found, err := svc.LatestReviewResultForIssue(ctx, issue)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v, want found=true", found, err)
	}
	if p.Verdict != "pass" || p.Summary != "newest" {
		t.Errorf("resolved %+v, want the newest VALID verdict (pass/newest)", p)
	}
	if !commentID.Valid || !reviewedAt.Valid {
		t.Error("comment_id / reviewed_at must be set")
	}
	if util.UUIDToString(reviewerID) != agentID {
		t.Errorf("reviewer = %s, want %s", util.UUIDToString(reviewerID), agentID)
	}
}
