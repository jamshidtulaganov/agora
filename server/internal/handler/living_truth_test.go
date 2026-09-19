package handler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/middleware"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// LIVING TRUTH tests (docs/living-truth-plan.md).
//
// Tier 2 (staleness) is the bulk of the file. Every rule gets a POSITIVE case
// and the negative that would otherwise pass it — an idle rule with no "fresh
// issue stays out" assertion proves nothing, because a query that returned the
// whole workspace would satisfy the positive half on its own.
//
// The other invariant asserted here is the one that cannot be recovered from if
// it breaks: staleness NEVER writes. It is inference, and inference that edits
// the tracker is worse than no signal at all.

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// staleFixture is one isolated workspace with its own owner, so the thresholds
// a test sets and the issues it seeds cannot be disturbed by (or disturb) the
// shared handler fixture.
type staleFixture struct {
	ws      string
	owner   string
	project string
}

func newStaleFixture(t *testing.T, name, prefix string) *staleFixture {
	t.Helper()
	owner := newAssistantTestUser(t, "stale-"+name+"@agora.dev")
	ws := newAssistantTestWorkspace(t, "stale-"+name+"-ws", prefix)
	addAssistantTestMember(t, ws, owner, "owner")
	return &staleFixture{ws: ws, owner: owner}
}

// seedStaleIssue inserts an issue whose updated_at (and created_at) sit
// `ageDays` in the past. Age is set directly rather than by waiting, which is
// the only way to test a day-scale rule in a unit test.
func (fx *staleFixture) seedStaleIssue(t *testing.T, title, status string, ageDays int, projectID string) string {
	t.Helper()
	var project any
	if projectID != "" {
		project = projectID
	}
	var issueID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id,
		                   project_id, number, created_at, updated_at)
		VALUES ($1, $2, $3, 'medium', 'member', $4, $5,
		        COALESCE((SELECT MAX(number) FROM issue WHERE workspace_id = $1), 0) + 1,
		        now() - ($6 || ' days')::interval, now() - ($6 || ' days')::interval)
		RETURNING id
	`, fx.ws, title, status, fx.owner, project, fmt.Sprintf("%d", ageDays)).Scan(&issueID); err != nil {
		t.Fatalf("seed issue %q: %v", title, err)
	}
	return issueID
}

// seedLinkedPR mirrors one pull request and links it to an issue, exactly as
// the webhook would. `resolvedDaysAgo` < 0 leaves merged_at/closed_at NULL (a
// PR still in flight).
func (fx *staleFixture) seedLinkedPR(t *testing.T, issueID, state string, prNumber int, openedDaysAgo, resolvedDaysAgo int) {
	t.Helper()
	ctx := context.Background()
	var resolved any
	if resolvedDaysAgo >= 0 {
		resolved = time.Now().AddDate(0, 0, -resolvedDaysAgo)
	}
	var prID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO github_pull_request (workspace_id, installation_id, repo_owner, repo_name,
		    pr_number, title, state, html_url, head_sha,
		    merged_at, closed_at, pr_created_at, pr_updated_at)
		VALUES ($1, 0, 'acme', 'widget', $2, 'a pull request', $3,
		        $6, 'deadbeef',
		        CASE WHEN $3 = 'merged' THEN $4::timestamptz ELSE NULL END,
		        CASE WHEN $3 IN ('merged','closed') THEN $4::timestamptz ELSE NULL END,
		        now() - ($5 || ' days')::interval, now())
		RETURNING id
	`, fx.ws, prNumber, state, resolved, fmt.Sprintf("%d", openedDaysAgo),
		fmt.Sprintf("https://github.com/acme/widget/pull/%d", prNumber)).Scan(&prID); err != nil {
		t.Fatalf("seed pull request #%d: %v", prNumber, err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO issue_pull_request (issue_id, pull_request_id, linked_by_type, close_intent)
		VALUES ($1, $2, 'system', true)
	`, issueID, prID); err != nil {
		t.Fatalf("link pull request #%d: %v", prNumber, err)
	}
}

// seedTask puts one task of the given status on an issue. `running` is the
// "an agent is on it right now" signal; a terminal status is activity without
// being active work.
func (fx *staleFixture) seedTask(t *testing.T, issueID, status string) {
	t.Helper()
	runtimeID := newAssistantTestRuntime(t, fx.ws)
	agentID := newAssistantTestAgent(t, fx.ws, runtimeID, "stale agent "+issueID[:8], "workspace", fx.owner)
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id, started_at, completed_at)
		VALUES ($1, $2, $3, 0, $4, now(),
		        CASE WHEN $3 IN ('completed','failed','cancelled') THEN now() ELSE NULL END)
	`, agentID, runtimeID, status, issueID); err != nil {
		t.Fatalf("seed %s task: %v", status, err)
	}
}

// staleReasons calls the endpoint as the given user and returns identifier →
// reason, which is what every assertion below is phrased in.
func (fx *staleFixture) staleReasons(t *testing.T, userID, projectID string) map[string]string {
	t.Helper()
	path := "/api/issues/staleness"
	if projectID != "" {
		path += "?project_id=" + projectID
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-User-ID", userID)
	req.Header.Set("X-Workspace-ID", fx.ws)
	rec := httptest.NewRecorder()
	testHandler.ListStaleIssues(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
	}
	var body struct {
		Stale []StaleIssueResponse `json:"stale"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode staleness: %v (%s)", err, rec.Body.String())
	}
	out := map[string]string{}
	for _, row := range body.Stale {
		if row.IssueID == "" || row.Title == "" || row.Status == "" || row.Since == "" {
			t.Fatalf("incomplete stale row: %+v — the contract is {issue_id, identifier, title, status, reason, since}", row)
		}
		out[row.Identifier] = row.Reason
	}
	return out
}

// ---------------------------------------------------------------------------
// idle
// ---------------------------------------------------------------------------

// The idle rule and, just as importantly, the three things that must NOT trip
// it: a recently-touched issue, an issue with an agent working on it, and an
// issue whose PR is still open. Each of those is a real reason an in_progress
// issue is quiet, and flagging any of them would make the signal noise.
func TestStalenessIdleRuleAndItsNegatives(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	fx := newStaleFixture(t, "idle", "SID")

	fx.seedStaleIssue(t, "quietly stuck", "in_progress", 9, "")
	fx.seedStaleIssue(t, "touched today", "in_progress", 0, "")
	withTask := fx.seedStaleIssue(t, "agent is on it", "in_progress", 9, "")
	fx.seedTask(t, withTask, "running")
	withPR := fx.seedStaleIssue(t, "pr is open", "in_progress", 9, "")
	fx.seedLinkedPR(t, withPR, "open", 4101, 9, -1)

	got := fx.staleReasons(t, fx.owner, "")
	if got["SID-1"] != staleReasonIdle {
		t.Fatalf("SID-1 (in_progress, 9 days quiet, no task, no PR) = %q, want %q", got["SID-1"], staleReasonIdle)
	}
	for _, identifier := range []string{"SID-2", "SID-3", "SID-4"} {
		if reason, found := got[identifier]; found {
			t.Fatalf("%s was flagged %q — fresh work, an active task and an open PR each mean the issue is fine", identifier, reason)
		}
	}
}

// A comment or a task resolves the issue's real activity clock even when the
// issue ROW has not been updated: someone talking about an issue is activity,
// and `updated_at` alone would call that silence.
func TestStalenessCountsCommentsAndTasksAsActivity(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	fx := newStaleFixture(t, "activity", "SAC")

	spokenOn := fx.seedStaleIssue(t, "old row, recent comment", "in_progress", 30, "")
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO comment (issue_id, workspace_id, author_type, author_id, content, type)
		VALUES ($1, $2, 'member', $3, 'still working on this', 'comment')
	`, spokenOn, fx.ws, fx.owner); err != nil {
		t.Fatalf("seed comment: %v", err)
	}

	workedOn := fx.seedStaleIssue(t, "old row, recent finished task", "in_progress", 30, "")
	fx.seedTask(t, workedOn, "completed")

	got := fx.staleReasons(t, fx.owner, "")
	for _, identifier := range []string{"SAC-1", "SAC-2"} {
		if reason, found := got[identifier]; found {
			t.Fatalf("%s was flagged %q — a fresh comment and a fresh task are both activity, whatever issue.updated_at says",
				identifier, reason)
		}
	}
}

// ---------------------------------------------------------------------------
// review_done
// ---------------------------------------------------------------------------

// The most expensive silent wrongness in the whole tracker: the code landed,
// the review finished, and the issue is still sitting in review. The rule needs
// linked PRs, all of them resolved, and enough time since the last one to rule
// out "merged five minutes ago and about to be moved".
func TestStalenessReviewDoneRuleAndItsNegatives(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	fx := newStaleFixture(t, "review", "SRV")

	landed := fx.seedStaleIssue(t, "merged days ago, still in review", "in_review", 6, "")
	fx.seedLinkedPR(t, landed, "merged", 5001, 8, 6)

	stillOpen := fx.seedStaleIssue(t, "review genuinely in flight", "in_review", 6, "")
	fx.seedLinkedPR(t, stillOpen, "merged", 5002, 8, 6)
	fx.seedLinkedPR(t, stillOpen, "open", 5003, 2, -1)

	justMerged := fx.seedStaleIssue(t, "merged an hour ago", "in_review", 6, "")
	fx.seedLinkedPR(t, justMerged, "merged", 5004, 3, 0)

	noPRs := fx.seedStaleIssue(t, "review with no pull request at all", "in_review", 60, "")
	_ = noPRs

	got := fx.staleReasons(t, fx.owner, "")
	if got["SRV-1"] != staleReasonReviewDone {
		t.Fatalf("SRV-1 (in_review, only PR merged 6 days ago) = %q, want %q", got["SRV-1"], staleReasonReviewDone)
	}
	for identifier, why := range map[string]string{
		"SRV-2": "a sibling PR is still open, so the review is genuinely in flight",
		"SRV-3": "the PR merged today — nobody has had a chance to move it yet",
		"SRV-4": "no linked PR at all, so there is no landing to have missed",
	} {
		if reason, found := got[identifier]; found {
			t.Fatalf("%s was flagged %q, but %s", identifier, reason, why)
		}
	}
}

// ---------------------------------------------------------------------------
// reopened_work
// ---------------------------------------------------------------------------

// done with a pull request open again. This is the one rule with no clock: the
// contradiction is visible the instant the PR opens, and waiting two days to
// mention it would mean two days of a board claiming work is finished while a
// diff for it is in review.
func TestStalenessReopenedWorkRuleAndItsNegative(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	fx := newStaleFixture(t, "reopened", "SRW")

	reopened := fx.seedStaleIssue(t, "done, but a PR is open again", "done", 1, "")
	fx.seedLinkedPR(t, reopened, "open", 6001, 1, -1)

	finished := fx.seedStaleIssue(t, "done and stayed done", "done", 40, "")
	fx.seedLinkedPR(t, finished, "merged", 6002, 45, 40)

	got := fx.staleReasons(t, fx.owner, "")
	if got["SRW-1"] != staleReasonReopenedWork {
		t.Fatalf("SRW-1 (done with an open linked PR) = %q, want %q", got["SRW-1"], staleReasonReopenedWork)
	}
	if reason, found := got["SRW-2"]; found {
		t.Fatalf("SRW-2 was flagged %q — a done issue whose PRs all merged is simply done, however old", reason)
	}
}

// ---------------------------------------------------------------------------
// blocked_quiet
// ---------------------------------------------------------------------------

// blocked is a human judgement the platform never sets and never clears. All
// this rule does is surface the ones nobody has revisited — which is why the
// assertion below also checks that the status is still blocked afterwards.
func TestStalenessBlockedQuietRuleAndItsNegative(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	fx := newStaleFixture(t, "blocked", "SBQ")

	forgotten := fx.seedStaleIssue(t, "blocked and forgotten", "blocked", 11, "")
	fx.seedStaleIssue(t, "blocked yesterday", "blocked", 1, "")

	got := fx.staleReasons(t, fx.owner, "")
	if got["SBQ-1"] != staleReasonBlockedQuiet {
		t.Fatalf("SBQ-1 (blocked, untouched 11 days) = %q, want %q", got["SBQ-1"], staleReasonBlockedQuiet)
	}
	if reason, found := got["SBQ-2"]; found {
		t.Fatalf("SBQ-2 was flagged %q — blocked yesterday is not blocked-and-forgotten", reason)
	}

	var status string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id = $1`, forgotten).Scan(&status); err != nil {
		t.Fatalf("re-read issue: %v", err)
	}
	if status != "blocked" {
		t.Fatalf("status = %q after a staleness read — staleness must NEVER write", status)
	}
}

// ---------------------------------------------------------------------------
// Scope: project filter, membership, thresholds
// ---------------------------------------------------------------------------

// ?project_id= narrows the answer to one project — what the board asks for when
// the user is looking at a project rather than the whole workspace.
func TestStalenessProjectFilter(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	fx := newStaleFixture(t, "project", "SPF")
	fx.project = newAssistantTestProject(t, fx.ws, "Platform")
	other := newAssistantTestProject(t, fx.ws, "Marketing")

	fx.seedStaleIssue(t, "platform, stuck", "in_progress", 9, fx.project)
	fx.seedStaleIssue(t, "marketing, stuck", "in_progress", 9, other)

	all := fx.staleReasons(t, fx.owner, "")
	if len(all) != 2 {
		t.Fatalf("unfiltered = %v, want both stale issues", all)
	}

	filtered := fx.staleReasons(t, fx.owner, fx.project)
	if len(filtered) != 1 || filtered["SPF-1"] != staleReasonIdle {
		t.Fatalf("filtered by project = %v, want only SPF-1", filtered)
	}

	// A malformed project_id is a 400 from the boundary, not a silently
	// unfiltered workspace-wide answer.
	req := httptest.NewRequest(http.MethodGet, "/api/issues/staleness?project_id=not-a-uuid", nil)
	req.Header.Set("X-User-ID", fx.owner)
	req.Header.Set("X-Workspace-ID", fx.ws)
	rec := httptest.NewRecorder()
	testHandler.ListStaleIssues(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed project_id = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
}

// The endpoint sits in the workspace-member group, so an outsider never learns
// anything — including whether the workspace exists. Mounted with the REAL
// middleware, because the gate is the routing, not the handler.
func TestStalenessRefusesNonMembers(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	fx := newStaleFixture(t, "gate", "SGT")
	fx.seedStaleIssue(t, "members only", "in_progress", 9, "")
	outsider := newAssistantTestUser(t, "stale-outsider@agora.dev")

	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(middleware.RequireWorkspaceMember(testHandler.Queries))
		r.Get("/api/issues/staleness", testHandler.ListStaleIssues)
	})

	exercise := func(userID string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/issues/staleness", nil)
		req.Header.Set("X-User-ID", userID)
		req.Header.Set("X-Workspace-ID", fx.ws)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := exercise(fx.owner); code != http.StatusOK {
		t.Fatalf("member GET staleness = %d, want 200", code)
	}
	if code := exercise(outsider); code != http.StatusNotFound {
		t.Fatalf("non-member GET staleness = %d, want 404", code)
	}
}

// The thresholds are instance config, read on every call — so raising one makes
// a flagged issue stop being flagged with no restart and no cache to bust.
func TestStalenessHonorsConfiguredThresholds(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	fx := newStaleFixture(t, "thresholds", "STH")
	fx.seedStaleIssue(t, "quiet for ten days", "in_progress", 10, "")
	fx.seedStaleIssue(t, "blocked for ten days", "blocked", 10, "")

	// Defaults (5 / 2 / 7): both are stale.
	base := fx.staleReasons(t, fx.owner, "")
	if base["STH-1"] != staleReasonIdle || base["STH-2"] != staleReasonBlockedQuiet {
		t.Fatalf("at default thresholds = %v, want both flagged", base)
	}

	// Raised past the age of both: the same rows, the same data, no signal.
	t.Setenv("AGORA_STALE_IDLE_DAYS", "30")
	t.Setenv("AGORA_STALE_BLOCKED_DAYS", "30")
	raised := fx.staleReasons(t, fx.owner, "")
	if len(raised) != 0 {
		t.Fatalf("at a 30-day threshold = %v, want nothing flagged", raised)
	}

	// A nonsense value falls back to the default rather than flagging the
	// workspace or silently disabling the signal.
	t.Setenv("AGORA_STALE_IDLE_DAYS", "not a number")
	if fallback := fx.staleReasons(t, fx.owner, ""); fallback["STH-1"] != staleReasonIdle {
		t.Fatalf("with an unparseable threshold = %v, want the 5-day default back", fallback)
	}
}

// ---------------------------------------------------------------------------
// Tier 1 — derived moves, and the provenance that explains them
// ---------------------------------------------------------------------------

// postPRWebhook fires a signed pull_request event at the webhook, exactly as
// GitHub would.
func (fx *staleFixture) postPRWebhook(t *testing.T, secret string, installationID int64, payload map[string]any) {
	t.Helper()
	payload["repository"] = map[string]any{"name": "widget", "owner": map[string]any{"login": "acme"}}
	payload["installation"] = map[string]any{"id": installationID}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal webhook payload: %v", err)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(raw)

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/github", bytes.NewReader(raw))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	rec := httptest.NewRecorder()
	testHandler.HandleGitHubWebhook(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("webhook: %d %s", rec.Code, rec.Body.String())
	}
}

// installWebhook wires an installation row so the webhook can attribute events
// to this fixture's workspace.
func (fx *staleFixture) installWebhook(t *testing.T, secret string, installationID int64) {
	t.Helper()
	t.Setenv("GITHUB_WEBHOOK_SECRET", secret)
	if _, err := testHandler.Queries.CreateGitHubInstallation(context.Background(), db.CreateGitHubInstallationParams{
		WorkspaceID:    parseUUID(fx.ws),
		InstallationID: installationID,
		AccountLogin:   "living-truth-acct",
		AccountType:    "User",
	}); err != nil {
		t.Fatalf("CreateGitHubInstallation: %v", err)
	}
}

func issueStatus(t *testing.T, issueID string) string {
	t.Helper()
	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM issue WHERE id = $1`, issueID).Scan(&status); err != nil {
		t.Fatalf("read issue status: %v", err)
	}
	return status
}

// systemComments returns the bodies of the system-authored comments on an
// issue, newest last — the provenance trail.
func systemComments(t *testing.T, issueID string) []string {
	t.Helper()
	rows, err := testPool.Query(context.Background(), `
		SELECT content FROM comment
		WHERE issue_id = $1 AND author_type = 'system'
		ORDER BY created_at ASC
	`, issueID)
	if err != nil {
		t.Fatalf("read system comments: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var content string
		if serr := rows.Scan(&content); serr != nil {
			t.Fatalf("scan comment: %v", serr)
		}
		out = append(out, content)
	}
	return out
}

func hasCommentContaining(comments []string, needle string) bool {
	for _, c := range comments {
		if strings.Contains(c, needle) {
			return true
		}
	}
	return false
}

// TIER 1b: a close-intent PR opening is the start of the loop. Before this,
// Agora moved an issue to done on merge but never moved it to in_progress on
// open — so a board could show "todo" over work that already had a branch, a
// diff and a reviewer.
//
// Four issues, one webhook each, because the rule is as much about what it
// REFUSES to touch as about what it moves.
func TestWebhook_OpenPRWithCloseIntent_StartsTheIssue(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	fx := newStaleFixture(t, "prstart", "PRS")
	fx.installWebhook(t, "living-truth-open-secret", 77001)

	todoIssue := fx.seedStaleIssue(t, "work about to start", "todo", 1, "")
	backlogIssue := fx.seedStaleIssue(t, "still in the backlog", "backlog", 1, "")
	reviewIssue := fx.seedStaleIssue(t, "already further along", "in_review", 1, "")
	mentionedIssue := fx.seedStaleIssue(t, "merely mentioned", "todo", 1, "")
	draftIssue := fx.seedStaleIssue(t, "draft PR only", "todo", 1, "")

	openPR := func(prNumber int, body string, draft bool) {
		fx.postPRWebhook(t, "living-truth-open-secret", 77001, map[string]any{
			"action": "opened",
			"pull_request": map[string]any{
				"number":     prNumber,
				"html_url":   fmt.Sprintf("https://github.com/acme/widget/pull/%d", prNumber),
				"title":      "Some work",
				"body":       body,
				"state":      "open",
				"draft":      draft,
				"merged":     false,
				"created_at": "2026-09-01T00:00:00Z",
				"updated_at": "2026-09-01T00:00:00Z",
				"head":       map[string]any{"ref": "feature/work"},
				"user":       map[string]any{"login": "octocat"},
			},
		})
	}

	openPR(7101, "Closes PRS-1", false)
	openPR(7102, "Closes PRS-2", false)
	openPR(7103, "Closes PRS-3", false)
	openPR(7104, "Follow-up work planned in PRS-4", false)
	openPR(7105, "Closes PRS-5", true)

	// Moved: the two statuses that are simply not true once a close-intent PR
	// is open.
	for _, tc := range []struct{ id, name string }{
		{todoIssue, "PRS-1 (todo)"},
		{backlogIssue, "PRS-2 (backlog)"},
	} {
		if status := issueStatus(t, tc.id); status != "in_progress" {
			t.Fatalf("%s = %q after its close-intent PR opened, want in_progress", tc.name, status)
		}
		if !hasCommentContaining(systemComments(t, tc.id), "Status changed to in_progress") {
			t.Fatalf("%s moved with no provenance comment — a silent auto-move is the failure this feature exists to fix; got %v",
				tc.name, systemComments(t, tc.id))
		}
	}

	// Untouched, each for a different reason.
	for _, tc := range []struct{ id, want, why string }{
		{reviewIssue, "in_review", "in_review is FURTHER along — forward-only means never dragging it back"},
		{mentionedIssue, "todo", "a bare mention is not close intent, so it links but never starts"},
		{draftIssue, "todo", "a draft is not work anybody is claiming to be doing yet"},
	} {
		if status := issueStatus(t, tc.id); status != tc.want {
			t.Fatalf("status = %q, want %q — %s", status, tc.want, tc.why)
		}
		if comments := systemComments(t, tc.id); len(comments) != 0 {
			t.Fatalf("an untouched issue got provenance %v — %s", comments, tc.why)
		}
	}
}

// Re-firing the same open webhook must not produce a second move or a second
// comment: the forward-only guard is what makes the whole thing idempotent, and
// GitHub redelivers.
func TestWebhook_OpenPRProvenanceIsWrittenOnce(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	fx := newStaleFixture(t, "prsonce", "PRO")
	fx.installWebhook(t, "living-truth-once-secret", 77002)
	issueID := fx.seedStaleIssue(t, "redelivered", "todo", 1, "")

	payload := func() map[string]any {
		return map[string]any{
			"action": "opened",
			"pull_request": map[string]any{
				"number":     7201,
				"html_url":   "https://github.com/acme/widget/pull/7201",
				"title":      "Some work",
				"body":       "Closes PRO-1",
				"state":      "open",
				"draft":      false,
				"merged":     false,
				"created_at": "2026-09-01T00:00:00Z",
				"updated_at": "2026-09-01T00:00:00Z",
				"head":       map[string]any{"ref": "feature/work"},
				"user":       map[string]any{"login": "octocat"},
			},
		}
	}
	fx.postPRWebhook(t, "living-truth-once-secret", 77002, payload())
	fx.postPRWebhook(t, "living-truth-once-secret", 77002, payload())

	if status := issueStatus(t, issueID); status != "in_progress" {
		t.Fatalf("status = %q, want in_progress", status)
	}
	if got := systemComments(t, issueID); len(got) != 1 {
		t.Fatalf("provenance comments = %d (%v), want exactly 1 — a redelivered webhook must not re-announce the move",
			len(got), got)
	}
}

// TIER 1a on the path that already existed: the merged→done move keeps its
// three-rule gate byte-for-byte and now also says why it happened.
func TestWebhook_MergedPR_LeavesProvenance(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	fx := newStaleFixture(t, "prdone", "PRD")
	fx.installWebhook(t, "living-truth-merge-secret", 77003)
	issueID := fx.seedStaleIssue(t, "about to land", "in_review", 1, "")

	fx.postPRWebhook(t, "living-truth-merge-secret", 77003, map[string]any{
		"action": "closed",
		"pull_request": map[string]any{
			"number":     7301,
			"html_url":   "https://github.com/acme/widget/pull/7301",
			"title":      "Ship it",
			"body":       "Closes PRD-1",
			"state":      "closed",
			"draft":      false,
			"merged":     true,
			"merged_at":  "2026-09-02T00:00:00Z",
			"closed_at":  "2026-09-02T00:00:00Z",
			"created_at": "2026-09-01T00:00:00Z",
			"updated_at": "2026-09-02T00:00:00Z",
			"head":       map[string]any{"ref": "feature/ship"},
			"user":       map[string]any{"login": "octocat"},
		},
	})

	if status := issueStatus(t, issueID); status != "done" {
		t.Fatalf("status = %q, want done — the three-rule gate must be unchanged", status)
	}
	comments := systemComments(t, issueID)
	if !hasCommentContaining(comments, "Status changed to done") || !hasCommentContaining(comments, "#7301") {
		t.Fatalf("provenance = %v, want a line naming the merged PR", comments)
	}
}

// TIER 1a on the one BACKWARD move the platform makes: a task dies, no PR is in
// flight, and the issue drops from in_progress back to todo. That move is
// correct and it is also the most alarming thing a tracker can do unannounced —
// the issue someone was watching quietly un-starts itself.
//
// This drives TaskService.HandleFailedTasks directly, which is the same entry
// point the stale-task sweeper uses.
func TestFailedTaskResetLeavesProvenance(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	fx := newStaleFixture(t, "failreset", "FTR")
	ctx := context.Background()

	issueID := fx.seedStaleIssue(t, "agent died mid-flight", "in_progress", 0, "")
	runtimeID := newAssistantTestRuntime(t, fx.ws)
	agentID := newAssistantTestAgent(t, fx.ws, runtimeID, "failing agent", "workspace", fx.owner)

	// failure_reason is deliberately NOT one of the retryable reasons, so the
	// sweeper falls through to the reset branch instead of cloning the task.
	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, issue_id,
		                              started_at, completed_at, failure_reason, error)
		VALUES ($1, $2, 'failed', 0, $3, now(), now(), 'agent_error', 'the agent gave up')
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&taskID); err != nil {
		t.Fatalf("seed failed task: %v", err)
	}
	failed, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatalf("load failed task: %v", err)
	}

	testHandler.TaskService.HandleFailedTasks(ctx, []db.AgentTaskQueue{failed})

	if status := issueStatus(t, issueID); status != "todo" {
		t.Fatalf("status = %q after the task failed with no PR in flight, want todo", status)
	}
	if !hasCommentContaining(systemComments(t, issueID), "Status reset to todo") {
		t.Fatalf("the reset left no provenance: %v", systemComments(t, issueID))
	}
}

// ---------------------------------------------------------------------------
// list_stale_issues — the assistant's read of the same signal
// ---------------------------------------------------------------------------

// One definition of stale, two surfaces. The tool must answer exactly what the
// endpoint answers, carry the exact scope envelope (the query has no LIMIT, so
// the row count IS the total and "you have 2 stale issues" is sayable), and
// honour the project filter by title the way every other project argument does.
func TestAssistantListStaleIssuesMatchesTheEndpoint(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	fx := newStaleFixture(t, "assistant", "SAS")
	fx.project = newAssistantTestProject(t, fx.ws, "Platform")

	fx.seedStaleIssue(t, "platform, quiet", "in_progress", 9, fx.project)
	fx.seedStaleIssue(t, "unfiled, blocked", "blocked", 12, "")
	fx.seedStaleIssue(t, "healthy", "in_progress", 0, "")

	result, err := executeAssistantTool(t, fx.owner, assistant.ToolListStaleIssues,
		`{"workspace_id":"`+fx.ws+`"}`)
	if err != nil {
		t.Fatalf("list_stale_issues: %v", err)
	}
	rows, ok := result["stale"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("stale = %v, want the two flagged issues", result["stale"])
	}
	first, _ := rows[0].(map[string]any)
	for _, field := range []string{"issue_id", "identifier", "title", "status", "reason", "since", "url_path"} {
		if value, _ := first[field].(string); value == "" {
			t.Fatalf("row is missing %q: %v", field, first)
		}
	}

	// The envelope is EXACT here, which is the whole point: an unlimited query
	// returned every row, so scope.total is a real number and not a page length.
	scope := assistantScopeOf(t, result)
	if total, _ := scope["total"].(float64); total != 2 {
		t.Fatalf("scope.total = %v, want the real total 2", scope["total"])
	}
	if truncated, _ := scope["truncated"].(bool); truncated {
		t.Fatalf("scope.truncated = true, but the staleness query has no limit to hit")
	}

	// Project scope, resolved from a title like every other project argument.
	filtered, err := executeAssistantTool(t, fx.owner, assistant.ToolListStaleIssues,
		`{"workspace_id":"`+fx.ws+`","project_id":"Platform"}`)
	if err != nil {
		t.Fatalf("list_stale_issues (project): %v", err)
	}
	if got, _ := filtered["stale"].([]any); len(got) != 1 {
		t.Fatalf("project-scoped stale = %v, want only the platform issue", filtered["stale"])
	}

	// And it is a READ: nothing the tool touched changed status.
	var moved int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue WHERE workspace_id = $1 AND status NOT IN ('in_progress','blocked')`,
		fx.ws).Scan(&moved); err != nil {
		t.Fatalf("re-read statuses: %v", err)
	}
	if moved != 0 {
		t.Fatalf("%d issues changed status during a staleness read — staleness must NEVER write", moved)
	}
}
