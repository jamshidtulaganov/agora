package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// The in-app Changes view is the answer to "what did the agent actually
// change" without leaving Agora, so these tests pin the two properties that
// decide whether a reviewer can trust it:
//
//  1. It NEVER fails the page. No App credentials, a dead upstream, a PR
//     recorded before the App was connected — each degrades to the stored path
//     list and says which source answered.
//  2. It NEVER widens what a user can see. Every read goes through
//     loadIssueForUser, so the workspace scope and the non-owner visibility
//     rule apply here exactly as they do on the issue detail itself.

// newChangesTestIssue creates an issue owned by the fixture user and registers
// full cleanup (link rows, PR rows, the issue).
func newChangesTestIssue(t *testing.T, title string) IssueResponse {
	t.Helper()
	w := httptest.NewRecorder()
	testHandler.CreateIssue(w, newRequest(http.MethodPost, "/api/issues?workspace_id="+testWorkspaceID, map[string]any{
		"title":  title,
		"status": "in_review",
	}))
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateIssue: %d %s", w.Code, w.Body.String())
	}
	var created IssueResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created issue: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		testPool.Exec(ctx, `DELETE FROM issue_pull_request WHERE issue_id = $1`, created.ID)
		testPool.Exec(ctx, `DELETE FROM activity_log WHERE issue_id = $1`, created.ID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, created.ID)
	})
	return created
}

// linkChangesTestPR inserts a PR row with an explicit number / repo / stored
// path list and links it to the issue.
func linkChangesTestPR(t *testing.T, issueID string, installationID int64, owner, repo string, number int32, paths []string) {
	t.Helper()
	ctx := t.Context()
	var prID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO github_pull_request
		  (workspace_id, installation_id, repo_owner, repo_name, pr_number, title, state, html_url,
		   pr_created_at, pr_updated_at, head_sha, additions, deletions, changed_files, changed_paths)
		VALUES ($1::uuid, $2, $3, $4, $5, 'agent work', 'open', $6,
		        now(), now(), 'deadbeef', 12, 4, 2, $7::text[])
		RETURNING id::text`,
		testWorkspaceID, installationID, owner, repo, number,
		"https://github.com/"+owner+"/"+repo+"/pull/"+strconv.FormatInt(int64(number), 10), paths,
	).Scan(&prID); err != nil {
		t.Fatalf("insert pr: %v", err)
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

// stubGitHubFilesAPI stands in for GitHub: it mints an installation token and
// serves one page of the PR Files API. The returned counter records how many
// times the FILES endpoint was hit, which is how the cache is observed.
func stubGitHubFilesAPI(t *testing.T, files []map[string]any) *int32 {
	t.Helper()
	pemBytes, _ := generateTestRSAKeyPEM(t)
	t.Setenv("GITHUB_APP_ID", "1")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", string(pemBytes))

	var fileCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/access_tokens"):
			writeJSON(w, http.StatusCreated, map[string]any{
				"token":      "ghs_stub_token",
				"expires_at": "",
			})
		case strings.HasSuffix(r.URL.Path, "/files"):
			atomic.AddInt32(&fileCalls, 1)
			writeJSON(w, http.StatusOK, files)
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	oldBase := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() { githubAPIBase = oldBase })
	resetPullRequestFilesCache()
	t.Cleanup(resetPullRequestFilesCache)
	return &fileCalls
}

func fetchIssueChanges(t *testing.T, issueID string) IssueChangesResponse {
	t.Helper()
	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodGet, "/api/issues/"+issueID+"/changes", nil), "id", issueID)
	testHandler.GetIssueChanges(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetIssueChanges: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp IssueChangesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode changes: %v", err)
	}
	return resp
}

func fetchIssueChangePatch(t *testing.T, issueID string, pr int, path string) (int, IssueChangePatchResponse) {
	t.Helper()
	url := "/api/issues/" + issueID + "/changes/patch?pr=" + strconv.Itoa(pr) + "&path=" + path
	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodGet, url, nil), "id", issueID)
	testHandler.GetIssueChangePatch(w, req)
	var resp IssueChangePatchResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode patch: %v", err)
		}
	}
	return w.Code, resp
}

// An issue with no pull request has nothing to say. The frontend renders the
// section only when `changes` is non-empty, so an empty array — not a
// placeholder entry — is the contract.
func TestIssueChanges_NoPullRequestsReturnsEmpty(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	issue := newChangesTestIssue(t, "changes: no PR")
	resp := fetchIssueChanges(t, issue.ID)
	if len(resp.Changes) != 0 {
		t.Fatalf("expected no changes for an issue with no PR, got %d", len(resp.Changes))
	}
}

// The degraded path, and the common one on a deployment without a GitHub App:
// the webhook already stored the path list, so the file list still renders —
// with files_source="stored" telling the UI that zero counts mean UNKNOWN, not
// "zero lines changed".
func TestIssueChanges_FallsBackToStoredPathsWithoutApp(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	t.Setenv("GITHUB_APP_ID", "")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", "")
	resetPullRequestFilesCache()
	t.Cleanup(resetPullRequestFilesCache)

	issue := newChangesTestIssue(t, "changes: stored paths")
	linkChangesTestPR(t, issue.ID, 0, "acme", "stored-repo", 4101,
		[]string{"server/internal/handler/issue.go", "packages/views/issues/x.tsx"})

	resp := fetchIssueChanges(t, issue.ID)
	if len(resp.Changes) != 1 {
		t.Fatalf("expected 1 change entry, got %d", len(resp.Changes))
	}
	change := resp.Changes[0]
	if change.FilesSource != "stored" {
		t.Errorf("files_source = %q, want stored — the UI needs to know the counts are unknown", change.FilesSource)
	}
	if change.PRNumber != 4101 || change.State != "open" {
		t.Errorf("pr header = #%d/%s, want #4101/open", change.PRNumber, change.State)
	}
	if change.HtmlURL == "" {
		t.Error("html_url must survive: the GitHub link is the secondary affordance beside the in-app view")
	}
	if len(change.Files) != 2 {
		t.Fatalf("expected 2 stored paths, got %d", len(change.Files))
	}
	if change.Files[0].Status != "" {
		t.Errorf("stored paths carry no verb; status = %q, want empty", change.Files[0].Status)
	}
}

// The upgraded path: with an App configured, the same Files API response that
// feeds the risk tier also carries status and per-file counts, and they land on
// the wire instead of being dropped.
func TestIssueChanges_LiveFilesCarryStatusAndCounts(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	calls := stubGitHubFilesAPI(t, []map[string]any{
		{"filename": "server/internal/handler/issue.go", "status": "modified", "additions": 10, "deletions": 2, "patch": "@@ -1 +1 @@\n-a\n+b"},
		{"filename": "docs/new.md", "status": "added", "additions": 7, "deletions": 0, "patch": "@@ -0,0 +1 @@\n+hi"},
	})

	issue := newChangesTestIssue(t, "changes: live files")
	linkChangesTestPR(t, issue.ID, 55110001, "acme", "live-repo", 4202, []string{"stale/path.go"})

	resp := fetchIssueChanges(t, issue.ID)
	if len(resp.Changes) != 1 {
		t.Fatalf("expected 1 change entry, got %d", len(resp.Changes))
	}
	change := resp.Changes[0]
	if change.FilesSource != "github" {
		t.Fatalf("files_source = %q, want github", change.FilesSource)
	}
	if len(change.Files) != 2 {
		t.Fatalf("expected 2 live files, got %d", len(change.Files))
	}
	if change.Files[0].Status != "modified" || change.Files[0].Additions != 10 || change.Files[0].Deletions != 2 {
		t.Errorf("first file = %+v, want modified +10 -2", change.Files[0])
	}
	if change.Files[1].Status != "added" {
		t.Errorf("second file status = %q, want added", change.Files[1].Status)
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Errorf("files endpoint hit %d times for one list render, want 1", got)
	}
}

// The visibility gate is not re-implemented here — it is INHERITED from
// loadIssueForUser. This test is the proof: a plain member who does not own the
// issue gets 404 (not an empty list, which would confirm the issue exists).
func TestIssueChanges_NonOwnerMemberCannotSeeAnotherUsersChanges(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	t.Setenv("GITHUB_APP_ID", "")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", "")

	issue := newChangesTestIssue(t, "changes: visibility fence")
	linkChangesTestPR(t, issue.ID, 0, "acme", "fence-repo", 4303, []string{"secret/file.go"})

	outsider := newAssistantTestUser(t, "changes-fence-member@agora.dev")
	addAssistantTestMember(t, testWorkspaceID, outsider, "member")

	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodGet, "/api/issues/"+issue.ID+"/changes", nil), "id", issue.ID)
	req.Header.Set("X-User-ID", outsider)
	testHandler.GetIssueChanges(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("non-owner member got %d, want 404: %s", w.Code, w.Body.String())
	}

	pw := httptest.NewRecorder()
	preq := withURLParam(newRequest(http.MethodGet, "/api/issues/"+issue.ID+"/changes/patch?pr=4303&path=secret/file.go", nil), "id", issue.ID)
	preq.Header.Set("X-User-ID", outsider)
	testHandler.GetIssueChangePatch(pw, preq)
	if pw.Code != http.StatusNotFound {
		t.Fatalf("non-owner member reached the patch endpoint with %d, want 404: %s", pw.Code, pw.Body.String())
	}
}

// Workspace fencing: an issue that exists, but in another workspace, is a 404
// under this workspace's header — same answer as an issue that does not exist.
func TestIssueChanges_ForeignWorkspaceIssueIs404(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	otherWS := newAssistantTestWorkspace(t, "changes-fence-ws", "CFW")
	addAssistantTestMember(t, otherWS, testUserID, "owner")
	foreignIssue := newAssistantTestIssue(t, otherWS, "foreign changes", testUserID, testUserID)

	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodGet, "/api/issues/"+foreignIssue+"/changes", nil), "id", foreignIssue)
	testHandler.GetIssueChanges(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace read returned %d, want 404: %s", w.Code, w.Body.String())
	}
}

// The patch endpoint's happy path, and the reason the cache exists: opening a
// second file must not cost a second upstream call. Without this, a reviewer
// clicking through eight files spends eight GitHub requests and the rate limit
// decides how much of the diff they get to read.
func TestIssueChangePatch_ServesDiffAndReusesTheCachedFetch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	calls := stubGitHubFilesAPI(t, []map[string]any{
		{"filename": "server/a.go", "status": "modified", "additions": 1, "deletions": 1, "patch": "@@ -1 +1 @@\n-old\n+new"},
		{"filename": "server/b.go", "status": "added", "additions": 3, "deletions": 0, "patch": "@@ -0,0 +1,3 @@\n+x\n+y\n+z"},
	})

	issue := newChangesTestIssue(t, "changes: patch happy path")
	linkChangesTestPR(t, issue.ID, 55110002, "acme", "patch-repo", 4404, []string{})

	code, first := fetchIssueChangePatch(t, issue.ID, 4404, "server/a.go")
	if code != http.StatusOK {
		t.Fatalf("patch: expected 200, got %d", code)
	}
	if first.Patch == nil || !strings.Contains(*first.Patch, "+new") {
		t.Fatalf("patch = %v, want the unified diff for server/a.go", first.Patch)
	}
	if first.Reason != "" {
		t.Errorf("reason = %q, want empty when a patch is returned", first.Reason)
	}

	code, second := fetchIssueChangePatch(t, issue.ID, 4404, "server/b.go")
	if code != http.StatusOK || second.Patch == nil || !strings.Contains(*second.Patch, "+z") {
		t.Fatalf("second file patch = %d / %v", code, second.Patch)
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Errorf("files endpoint hit %d times for two file opens, want 1 (the cache is the point)", got)
	}
}

// GitHub omits `patch` for binary files and for diffs it will not inline. That
// is a STATEMENT the UI repeats ("Diff too large — view on GitHub"), so it must
// arrive as an explicit reason and never as an empty string that renders like
// an unchanged file.
func TestIssueChangePatch_AbsentPatchSaysSo(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	stubGitHubFilesAPI(t, []map[string]any{
		{"filename": "assets/logo.png", "status": "modified", "additions": 0, "deletions": 0},
	})

	issue := newChangesTestIssue(t, "changes: binary file")
	linkChangesTestPR(t, issue.ID, 55110003, "acme", "binary-repo", 4505, []string{})

	code, resp := fetchIssueChangePatch(t, issue.ID, 4505, "assets/logo.png")
	if code != http.StatusOK {
		t.Fatalf("expected 200 with a reason, got %d", code)
	}
	if resp.Patch != nil {
		t.Errorf("patch = %q, want null for a file GitHub sent no diff for", *resp.Patch)
	}
	if resp.Reason != prPatchAbsent {
		t.Errorf("reason = %q, want %q", resp.Reason, prPatchAbsent)
	}
}

// A file the PR does not touch is a different answer from a file whose diff we
// could not fetch. Both are 200; only the reason distinguishes them.
func TestIssueChangePatch_UnknownFileSaysFileNotFound(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	stubGitHubFilesAPI(t, []map[string]any{
		{"filename": "server/a.go", "status": "modified", "additions": 1, "deletions": 0, "patch": "@@ -1 +1 @@\n+x"},
	})

	issue := newChangesTestIssue(t, "changes: unknown file")
	linkChangesTestPR(t, issue.ID, 55110004, "acme", "unknown-repo", 4606, []string{})

	code, resp := fetchIssueChangePatch(t, issue.ID, 4606, "server/never-touched.go")
	if code != http.StatusOK {
		t.Fatalf("expected 200 with a reason, got %d", code)
	}
	if resp.Patch != nil || resp.Reason != changesReasonFileAbsent {
		t.Errorf("got patch=%v reason=%q, want null / %q", resp.Patch, resp.Reason, changesReasonFileAbsent)
	}
}

// No App identity configured is an OPERATOR state, not a failure: the endpoint
// names it so the UI can say "connect GitHub to see diffs here" instead of
// showing a spinner that never resolves.
func TestIssueChangePatch_NoAppConfiguredIsNamed(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	t.Setenv("GITHUB_APP_ID", "")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", "")
	resetPullRequestFilesCache()
	t.Cleanup(resetPullRequestFilesCache)

	issue := newChangesTestIssue(t, "changes: no app")
	linkChangesTestPR(t, issue.ID, 0, "acme", "noapp-repo", 4707, []string{"server/a.go"})

	code, resp := fetchIssueChangePatch(t, issue.ID, 4707, "server/a.go")
	if code != http.StatusOK {
		t.Fatalf("expected 200 with a reason, got %d", code)
	}
	if resp.Patch != nil || resp.Reason != changesReasonNoApp {
		t.Errorf("got patch=%v reason=%q, want null / %q", resp.Patch, resp.Reason, changesReasonNoApp)
	}
}

// A PR number that is not linked to this issue is not this issue's diff to
// serve. 404 rather than a reason payload: this is an addressing error, and
// answering 200 would let the endpoint be walked for PR numbers.
func TestIssueChangePatch_UnlinkedPullRequestIs404(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test fixture not initialized (no DB?)")
	}
	issue := newChangesTestIssue(t, "changes: unlinked pr")
	linkChangesTestPR(t, issue.ID, 0, "acme", "linked-repo", 4808, []string{"server/a.go"})

	if code, _ := fetchIssueChangePatch(t, issue.ID, 9999, "server/a.go"); code != http.StatusNotFound {
		t.Fatalf("unlinked PR returned %d, want 404", code)
	}
	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodGet, "/api/issues/"+issue.ID+"/changes/patch?pr=notanumber&path=a.go", nil), "id", issue.ID)
	testHandler.GetIssueChangePatch(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("non-numeric pr returned %d, want 400", w.Code)
	}
}

// The cache is bounded by design. This pins the budget behaviour directly:
// an oversized patch is dropped and REPLACED BY A REASON, never by "".
func TestTrimPatchesToBudget_DropsOversizedPatchesWithAReason(t *testing.T) {
	huge := strings.Repeat("x", prFilePatchMaxBytes+1)
	files := trimPatchesToBudget([]prChangedFile{
		{Path: "small.go", Patch: "@@ -1 +1 @@\n+ok"},
		{Path: "huge.go", Patch: huge},
	})
	if files[0].Patch == "" || files[0].PatchReason != "" {
		t.Errorf("a small patch must survive untouched, got %+v", files[0])
	}
	if files[1].Patch != "" {
		t.Error("an oversized patch must be dropped from the cache")
	}
	if files[1].PatchReason != prPatchAbsent {
		t.Errorf("dropped patch reason = %q, want %q", files[1].PatchReason, prPatchAbsent)
	}
}

// changedPathsFromFiles is what the risk sync consumes. A rename must yield
// BOTH paths: a file moved out of a critical module is still a change to it.
func TestChangedPathsFromFiles_KeepsBothSidesOfARename(t *testing.T) {
	paths := changedPathsFromFiles([]prChangedFile{
		{Path: "billing/new.php", PreviousPath: "billing/old.php", Status: "renamed"},
		{Path: "billing/new.php"},
	})
	if len(paths) != 2 || paths[0] != "billing/new.php" || paths[1] != "billing/old.php" {
		t.Fatalf("paths = %v, want both sides of the rename, deduped", paths)
	}
}
