package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// TestValidCommitSha pins the fail-open sha validation: 7-40 hex accepted
// (lowercased), everything else — prose, branch names, injection — becomes "".
func TestValidCommitSha(t *testing.T) {
	tests := []struct{ in, want string }{
		{"a1b2c3d", "a1b2c3d"}, // short sha
		{"A1B2C3D4E5F6A7B8C9D0A1B2C3D4E5F6A7B8C9D0", strings.ToLower("A1B2C3D4E5F6A7B8C9D0A1B2C3D4E5F6A7B8C9D0")}, // full sha, case-folded
		{"  deadbeef  ", "deadbeef"}, // trimmed
		{"abc123", ""},               // 6 chars — too short
		{"main", ""},                 // branch name
		{"g1234567", ""},             // non-hex char
		{"deadbeef; rm -rf /", ""},   // injection attempt
		{"", ""},                     // absent
	}
	for _, tt := range tests {
		if got := validCommitSha(tt.in); got != tt.want {
			t.Errorf("validCommitSha(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestParseFenceTime pins the fail-open timestamp parse.
func TestParseFenceTime(t *testing.T) {
	if ts := parseFenceTime("2026-07-10T12:00:00Z"); !ts.Valid {
		t.Error("valid RFC3339 must parse")
	}
	for _, bad := range []string{"", "yesterday", "2026-07-10", "not a time"} {
		if ts := parseFenceTime(bad); ts.Valid {
			t.Errorf("parseFenceTime(%q) must be NULL, got valid", bad)
		}
	}
}

// TestCaptureQAEvidenceRunIdentity covers the Phase 3 evidence identity:
// commit_sha + started_at from the fence, triggered_by=auto when the trigger
// comment carries QADispatchAutoMarker, "agent" otherwise, finished_at
// stamped at capture.
func TestCaptureQAEvidenceRunIdentity(t *testing.T) {
	pool := knowledgeTestPool(t)
	ctx := context.Background()
	q := db.New(pool)
	wsID := seedKnowledgeWorkspace(t, pool)

	var userID string
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name,email) VALUES ('rid',$1) RETURNING id`,
		"rid-"+uuid.NewString()[:8]+"@x.dev").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	var issueID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id,title,status,creator_type,creator_id,number)
		VALUES ($1,'run identity issue','in_review','member',$2,1) RETURNING id`,
		util.UUIDToString(wsID), userID).Scan(&issueID); err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issue, err := q.GetIssue(ctx, util.MustParseUUID(issueID))
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	svc := NewTaskService(q, pool, nil, events.New())

	// AUTO-dispatched trigger comment (carries the marker).
	autoTrigger, err := q.CreateComment(ctx, db.CreateCommentParams{
		IssueID: issue.ID, WorkspaceID: wsID, AuthorType: "member", AuthorID: util.MustParseUUID(userID),
		Content: "<!--agent-protocol:run_qa-->" + QADispatchAutoMarker + "\n[@QA](mention://agent/x) gate",
		Type:    "comment",
	})
	if err != nil {
		t.Fatalf("seed auto trigger: %v", err)
	}

	content := "```qa-result\n" +
		`{"verdict":"pass","summary":"green","commit_sha":"DeadBeefCafe1234","started_at":"2026-07-10T11:00:00Z","commands":[{"cmd":"go test ./...","branch_exit":0,"kind":"pass"}]}` +
		"\n```"
	if v, labeled := svc.CaptureQAEvidence(ctx, issue, content, autoTrigger.ID); v != "pass" || !labeled {
		t.Fatalf("capture: verdict=%q labeled=%v", v, labeled)
	}

	ev, err := q.GetLatestQAEvidenceForIssue(ctx, db.GetLatestQAEvidenceForIssueParams{
		IssueID: issue.ID, WorkspaceID: wsID,
	})
	if err != nil {
		t.Fatalf("GetLatestQAEvidenceForIssue: %v", err)
	}
	if ev.CommitSha != "deadbeefcafe1234" {
		t.Errorf("commit_sha = %q, want lowercased deadbeefcafe1234", ev.CommitSha)
	}
	if ev.TriggeredBy != "auto" {
		t.Errorf("triggered_by = %q, want auto (marker present on trigger)", ev.TriggeredBy)
	}
	if !ev.StartedAt.Valid {
		t.Error("started_at must be set from the fence")
	}
	if !ev.FinishedAt.Valid {
		t.Error("finished_at must be stamped at capture")
	}

	// A plain (manual Re-run) trigger — no marker → "agent"; a garbage sha
	// fails open to "".
	plainTrigger, err := q.CreateComment(ctx, db.CreateCommentParams{
		IssueID: issue.ID, WorkspaceID: wsID, AuthorType: "member", AuthorID: util.MustParseUUID(userID),
		Content: "<!--agent-protocol:run_qa-->\n[@QA](mention://agent/x) re-run",
		Type:    "comment",
	})
	if err != nil {
		t.Fatalf("seed plain trigger: %v", err)
	}
	content2 := "```qa-result\n" +
		`{"verdict":"fail","summary":"red","commit_sha":"the-main-branch","commands":[{"cmd":"go test ./...","branch_exit":1,"kind":"new_failure","error":"boom"}]}` +
		"\n```"
	if v, _ := svc.CaptureQAEvidence(ctx, issue, content2, plainTrigger.ID); v != "fail" {
		t.Fatalf("capture 2: verdict=%q", v)
	}
	ev2, err := q.GetLatestQAEvidenceForIssue(ctx, db.GetLatestQAEvidenceForIssueParams{
		IssueID: issue.ID, WorkspaceID: wsID,
	})
	if err != nil {
		t.Fatalf("GetLatestQAEvidenceForIssue 2: %v", err)
	}
	if ev2.TriggeredBy != "agent" {
		t.Errorf("triggered_by = %q, want agent (no auto marker)", ev2.TriggeredBy)
	}
	if ev2.CommitSha != "" {
		t.Errorf("commit_sha = %q, want \"\" (invalid sha shape fails open)", ev2.CommitSha)
	}
	if ev2.StartedAt.Valid {
		t.Error("started_at must be NULL when the fence omits it")
	}
}

// TestCaptureTestRunsSessionAndSha: every run row from ONE capture dispatch
// shares the same minted session_id, carries its validated commit_sha, and
// gets finished_at stamped.
func TestCaptureTestRunsSessionAndSha(t *testing.T) {
	pool := knowledgeTestPool(t)
	ctx := context.Background()
	q := db.New(pool)
	wsID := seedKnowledgeWorkspace(t, pool)

	var projectID, userID string
	if err := pool.QueryRow(ctx, `INSERT INTO project (workspace_id, title, status, priority) VALUES ($1,'sid-proj','planned','none') RETURNING id`,
		util.UUIDToString(wsID)).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name,email) VALUES ('sid',$1) RETURNING id`,
		"sid-"+uuid.NewString()[:8]+"@x.dev").Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	var issueID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id,title,status,creator_type,creator_id,project_id,number)
		VALUES ($1,'session issue','in_review','member',$2,$3,1) RETURNING id`,
		util.UUIDToString(wsID), userID, projectID).Scan(&issueID); err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	issue := db.Issue{ID: util.MustParseUUID(issueID), WorkspaceID: wsID, ProjectID: util.MustParseUUID(projectID), Status: "in_review", Number: 1}

	var runtimeID, agentID string
	pool.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id,name,runtime_mode,provider,status,metadata,last_seen_at) VALUES ($1,'sid-rt','cloud','claude','online','{}'::jsonb,now()) RETURNING id`, util.UUIDToString(wsID)).Scan(&runtimeID)
	pool.QueryRow(ctx, `INSERT INTO agent (workspace_id,name,runtime_mode,runtime_config,runtime_id,visibility,max_concurrent_tasks) VALUES ($1,'QA','cloud','{}'::jsonb,$2,'workspace',3) RETURNING id`, util.UUIDToString(wsID), runtimeID).Scan(&agentID)

	seedCase := func(title string) string {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO test_case (workspace_id,issue_id,project_id,title,steps,expected,kind,source,author_type,author_id,category)
			VALUES ($1,$2,$3,$4,'s','e','automated','agent','agent',$5,'positive') RETURNING id`,
			util.UUIDToString(wsID), issueID, projectID, title, agentID).Scan(&id); err != nil {
			t.Fatalf("seed case: %v", err)
		}
		return id
	}
	caseA, caseB := seedCase("A"), seedCase("B")

	svc := NewTaskService(q, pool, nil, events.New())
	content := fmt.Sprintf(
		"```test-runs\n[{\"test_case_id\":\"%s\",\"status\":\"pass\",\"output\":\"ok\",\"commit_sha\":\"AbCdEf1234\"},{\"test_case_id\":\"%s\",\"status\":\"fail\",\"output\":\"red\",\"commit_sha\":\"not_a_sha\"}]\n```",
		caseA, caseB)
	svc.CaptureTestRuns(ctx, issue, content, util.MustParseUUID(agentID), pgtype.UUID{})

	type row struct {
		sessionID  *string
		commitSha  string
		finishedOK bool
	}
	get := func(caseID string) row {
		var r row
		if err := pool.QueryRow(ctx,
			`SELECT session_id::text, commit_sha, finished_at IS NOT NULL FROM test_run WHERE test_case_id=$1`,
			caseID).Scan(&r.sessionID, &r.commitSha, &r.finishedOK); err != nil {
			t.Fatalf("read run for %s: %v", caseID, err)
		}
		return r
	}
	a, b := get(caseA), get(caseB)
	if a.sessionID == nil || b.sessionID == nil || *a.sessionID != *b.sessionID {
		t.Errorf("both runs from one dispatch must share one session_id, got %v / %v", a.sessionID, b.sessionID)
	}
	if a.commitSha != "abcdef1234" {
		t.Errorf("case A commit_sha = %q, want abcdef1234 (lowercased)", a.commitSha)
	}
	if b.commitSha != "" {
		t.Errorf("case B commit_sha = %q, want \"\" (invalid shape fails open)", b.commitSha)
	}
	if !a.finishedOK || !b.finishedOK {
		t.Error("finished_at must be stamped at capture")
	}

	// A SECOND dispatch mints a DIFFERENT session.
	content2 := fmt.Sprintf("```test-runs\n[{\"test_case_id\":\"%s\",\"status\":\"pass\",\"output\":\"ok again\"}]\n```", caseA)
	svc.CaptureTestRuns(ctx, issue, content2, util.MustParseUUID(agentID), pgtype.UUID{})
	var sessions int
	pool.QueryRow(ctx, `SELECT count(DISTINCT session_id) FROM test_run WHERE test_case_id=$1`, caseA).Scan(&sessions)
	if sessions != 2 {
		t.Errorf("two dispatches must mint two distinct sessions, got %d", sessions)
	}
}
