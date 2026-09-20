package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// WHAT THE AGENT CHANGED, WITHOUT LEAVING AGORA.
//
// An issue's pull requests already render as rows that link out to GitHub. That
// link is the end of the story inside Agora: to answer "what did the agent
// actually do" a reviewer had to leave the product, log into GitHub, and come
// back. This file answers the question in place — the file list first, then a
// single file's unified diff on demand.
//
// TWO SOURCES, ONE SHAPE. The file list is cheap and always available from
// github_pull_request.changed_paths (migration 210), which the webhook fills at
// PR open / synchronize. The richer answer — per-file status and +/- counts, and
// the patch text itself — only exists on GitHub's "List pull request files"
// response, so it is fetched live through a short-lived in-process cache. When
// that fetch is impossible (no App credentials, a non-GitHub provider row, a
// rate limit, a network blip) the endpoint DEGRADES to the stored paths and
// says which source it used. It never fails the request: a Changes view that
// 500s is worse than a Changes view that lists paths without counts.
//
// PATCHES ARE NEVER PERSISTED. They live in this process's bounded cache for a
// few minutes and nowhere else. A diff is derivable from the repository at any
// time; storing it would put source code in Agora's database with none of the
// retention, access or deletion guarantees that implies.

const (
	// prFilesCacheTTL keeps a PR's file list warm long enough that opening
	// several files in a row costs ONE upstream call, and short enough that a
	// push lands in the view without a manual refresh.
	prFilesCacheTTL = 5 * time.Minute
	// prFilesCacheMaxEntries bounds the cache by PR count. Paired with the
	// per-entry patch budget below, this is what keeps an in-memory diff cache
	// from becoming an unbounded heap.
	prFilesCacheMaxEntries = 24
	// prFilePatchMaxBytes drops a single oversized file's patch. A diff this
	// long is not read in a browser pane anyway; the UI points at GitHub.
	prFilePatchMaxBytes = 128 << 10
	// prFilesPatchBudgetBytes caps the patch text held for ONE pull request.
	prFilesPatchBudgetBytes = 2 << 20
	// prChangesListTimeout bounds the live fetch behind the list endpoint. It is
	// short on purpose: the stored path list is the fallback, and a page that
	// waits on GitHub is a page that looks broken.
	prChangesListTimeout = 8 * time.Second
	// prChangesPatchTimeout bounds the on-demand single-file fetch, which the
	// user asked for explicitly and is willing to wait a moment for.
	prChangesPatchTimeout = 15 * time.Second
	// prChangesMaxLiveFetches caps how many of an issue's PRs get a live fetch
	// in one list request. Beyond it the stored paths answer — an issue with
	// six linked PRs must not cost six serial GitHub round trips.
	prChangesMaxLiveFetches = 5
)

// Reasons the UI renders verbatim-by-key when there is no patch to show. Every
// one of them is a STATEMENT, not an empty result: "we could not get this" and
// "there is nothing here" look identical in a blank pane, and the difference is
// exactly what tells a reviewer whether to trust the view.
const (
	changesReasonNoApp      = "github_app_not_configured"
	changesReasonFileAbsent = "file_not_found"
	changesReasonFetchFail  = "fetch_failed"
)

// errGitHubAppNotConfigured separates "this deployment has no GitHub App
// identity" from "the call failed". The first is an operator configuration
// state the UI should name; the second is an incident.
var errGitHubAppNotConfigured = errors.New("github app not configured")

// ── response shapes ─────────────────────────────────────────────────────────

type IssueChangeFileResponse struct {
	Path string `json:"path"`
	// Status is GitHub's per-file verb (added / modified / removed / renamed /
	// copied / changed / unchanged), or "" when the list came from stored paths
	// and the verb is genuinely unknown. The UI must have a default branch:
	// GitHub can add a value at any time.
	Status string `json:"status"`
	// PreviousPath is set on a rename, so the row can show where the file came
	// from instead of reading as an unrelated add.
	PreviousPath string `json:"previous_path,omitempty"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
}

type IssueChangeResponse struct {
	PRNumber     int32  `json:"pr_number"`
	Title        string `json:"title"`
	State        string `json:"state"`
	HtmlURL      string `json:"html_url"`
	RepoOwner    string `json:"repo_owner"`
	RepoName     string `json:"repo_name"`
	Additions    int32  `json:"additions"`
	Deletions    int32  `json:"deletions"`
	ChangedFiles int32  `json:"changed_files"`
	// FilesSource is "github" (live, with stats), "stored" (changed_paths from
	// the webhook sync, paths only) or "none" (nothing known). The UI uses it to
	// decide whether "no +/- counts" means zero or means unknown.
	FilesSource string                    `json:"files_source"`
	Files       []IssueChangeFileResponse `json:"files"`
}

type IssueChangesResponse struct {
	Changes []IssueChangeResponse `json:"changes"`
}

type IssueChangePatchResponse struct {
	// Patch is the file's unified diff as GitHub produced it (hunk headers, no
	// `diff --git` preamble), or null when there is none to show.
	Patch *string `json:"patch"`
	// Reason names why Patch is null. Empty when Patch is present.
	Reason string `json:"reason"`
}

// ── the bounded file-list cache ─────────────────────────────────────────────

type prFilesCacheEntry struct {
	files     []prChangedFile
	storedAt  time.Time
	expiresAt time.Time
}

var (
	prFilesCacheMu sync.Mutex
	prFilesCache   = map[string]prFilesCacheEntry{}
)

func prFilesCacheKey(owner, repo string, number int32) string {
	return owner + "/" + repo + "#" + strconv.FormatInt(int64(number), 10)
}

// trimPatchesToBudget keeps the cache's memory cost bounded and SAYS SO on the
// entries it empties. A file whose patch is dropped carries prPatchAbsent, so
// the patch endpoint answers "no diff to show, open it on GitHub" instead of
// silently returning an empty string that renders as a file with no changes.
func trimPatchesToBudget(files []prChangedFile) []prChangedFile {
	budget := prFilesPatchBudgetBytes
	for i := range files {
		size := len(files[i].Patch)
		if size == 0 {
			continue
		}
		if size > prFilePatchMaxBytes || size > budget {
			files[i].Patch = ""
			files[i].PatchReason = prPatchAbsent
			continue
		}
		budget -= size
	}
	return files
}

// storePullRequestFiles caches one PR's files, evicting expired entries first
// and then the oldest one if the cache is still at its bound.
func storePullRequestFiles(key string, files []prChangedFile) {
	now := time.Now()
	prFilesCacheMu.Lock()
	defer prFilesCacheMu.Unlock()
	for k, entry := range prFilesCache {
		if now.After(entry.expiresAt) {
			delete(prFilesCache, k)
		}
	}
	if _, replacing := prFilesCache[key]; !replacing {
		for len(prFilesCache) >= prFilesCacheMaxEntries {
			oldestKey, oldestAt := "", time.Time{}
			for k, entry := range prFilesCache {
				if oldestKey == "" || entry.storedAt.Before(oldestAt) {
					oldestKey, oldestAt = k, entry.storedAt
				}
			}
			if oldestKey == "" {
				break
			}
			delete(prFilesCache, oldestKey)
		}
	}
	prFilesCache[key] = prFilesCacheEntry{
		files:     files,
		storedAt:  now,
		expiresAt: now.Add(prFilesCacheTTL),
	}
}

func lookupPullRequestFiles(key string) ([]prChangedFile, bool) {
	prFilesCacheMu.Lock()
	defer prFilesCacheMu.Unlock()
	entry, ok := prFilesCache[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.files, true
}

// resetPullRequestFilesCache exists for tests, which must not inherit a warm
// cache from a sibling test and call it a cache hit.
func resetPullRequestFilesCache() {
	prFilesCacheMu.Lock()
	defer prFilesCacheMu.Unlock()
	prFilesCache = map[string]prFilesCacheEntry{}
}

// pullRequestFilesCached is the single upstream door for both endpoints below.
// One warm entry serves the file list AND every file's patch, which is the
// whole point: opening six files in the Changes view costs zero extra calls.
func pullRequestFilesCached(ctx context.Context, pr db.ListPullRequestsByIssueRow) ([]prChangedFile, error) {
	key := prFilesCacheKey(pr.RepoOwner, pr.RepoName, pr.PrNumber)
	if files, ok := lookupPullRequestFiles(key); ok {
		return files, nil
	}
	if pr.InstallationID == 0 || pr.RepoOwner == "" || pr.RepoName == "" {
		// No installation to mint a token for — a GitLab-provider row
		// (migration 124) or a PR recorded before the App was connected.
		return nil, errGitHubAppNotConfigured
	}
	token, err := githubInstallationToken(ctx, pr.InstallationID)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, errGitHubAppNotConfigured
	}
	files, err := fetchPullRequestFiles(ctx, token, pr.RepoOwner, pr.RepoName, pr.PrNumber)
	if err != nil {
		return nil, err
	}
	files = trimPatchesToBudget(files)
	storePullRequestFiles(key, files)
	return files, nil
}

// ── GET /api/issues/{id}/changes ────────────────────────────────────────────

// GetIssueChanges returns, per pull request linked to the issue, the files it
// touches. Gated by loadIssueForUser, which is the choke point that applies the
// workspace scope AND the non-owner visibility rule to every single-issue read.
//
// An issue with no linked PR answers {"changes": []} — the frontend renders
// nothing at all rather than an empty "no changes" card, because "this task had
// no code" and "this task's code is not visible here" must not look the same.
func (h *Handler) GetIssueChanges(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	rows, err := h.Queries.ListPullRequestsByIssue(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list pull requests")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), prChangesListTimeout)
	defer cancel()

	out := make([]IssueChangeResponse, 0, len(rows))
	liveFetches := 0
	for _, row := range rows {
		// Belt and braces on top of the issue loader: the link table is keyed by
		// issue, and the issue is workspace-scoped, but a PR row that somehow
		// belongs to another workspace must never be rendered through here.
		if uuidToString(row.WorkspaceID) != uuidToString(issue.WorkspaceID) {
			continue
		}
		change := IssueChangeResponse{
			PRNumber:     row.PrNumber,
			Title:        row.Title,
			State:        row.State,
			HtmlURL:      row.HtmlUrl,
			RepoOwner:    row.RepoOwner,
			RepoName:     row.RepoName,
			Additions:    row.Additions,
			Deletions:    row.Deletions,
			ChangedFiles: row.ChangedFiles,
			FilesSource:  "none",
			Files:        []IssueChangeFileResponse{},
		}

		var live []prChangedFile
		if liveFetches < prChangesMaxLiveFetches {
			files, ferr := pullRequestFilesCached(ctx, row)
			if ferr != nil {
				if !errors.Is(ferr, errGitHubAppNotConfigured) {
					// Not an error the user can act on, and not a reason to fail
					// the page: log it and fall through to the stored paths.
					slog.Warn("issue changes: live file list unavailable — falling back to stored paths",
						"pr", row.PrNumber, "repo", row.RepoOwner+"/"+row.RepoName, "error", ferr)
				}
			} else {
				live = files
			}
			liveFetches++
		}

		if len(live) > 0 {
			change.FilesSource = "github"
			for _, f := range live {
				change.Files = append(change.Files, IssueChangeFileResponse{
					Path:         f.Path,
					Status:       f.Status,
					PreviousPath: f.PreviousPath,
					Additions:    f.Additions,
					Deletions:    f.Deletions,
				})
			}
		} else if len(row.ChangedPaths) > 0 {
			// Stored paths carry no verb and no counts. Leaving Status empty and
			// the counts at zero is the honest shape — and files_source tells the
			// UI to render them as "unknown", not as "zero lines changed".
			change.FilesSource = "stored"
			for _, path := range row.ChangedPaths {
				change.Files = append(change.Files, IssueChangeFileResponse{Path: path})
			}
		}
		out = append(out, change)
	}

	writeJSON(w, http.StatusOK, IssueChangesResponse{Changes: out})
}

// ── GET /api/issues/{id}/changes/patch ──────────────────────────────────────

// GetIssueChangePatch returns ONE file's unified diff, fetched live and served
// from the same cache the list endpoint warms.
//
// It answers 200 with an explicit reason far more often than it errors: no App
// configured, a file GitHub does not list, a binary blob, a diff GitHub refused
// to inline — all of those are states a reviewer should be told about in the
// pane they are looking at, not HTTP failures the client has to interpret.
func (h *Handler) GetIssueChangePatch(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	prNumber, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("pr")))
	if err != nil || prNumber <= 0 {
		writeError(w, http.StatusBadRequest, "pr must be a positive pull request number")
		return
	}
	path := normalizeRiskPath(r.URL.Query().Get("path"))
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	rows, err := h.Queries.ListPullRequestsByIssue(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list pull requests")
		return
	}
	var pr db.ListPullRequestsByIssueRow
	found := false
	for _, row := range rows {
		if row.PrNumber == int32(prNumber) && uuidToString(row.WorkspaceID) == uuidToString(issue.WorkspaceID) {
			pr, found = row, true
			break
		}
	}
	if !found {
		// A PR not linked to this issue is not this issue's diff to serve. 404
		// (not 403) so an unlinked PR number reveals nothing.
		writeError(w, http.StatusNotFound, "pull request not linked to this issue")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), prChangesPatchTimeout)
	defer cancel()

	files, ferr := pullRequestFilesCached(ctx, pr)
	if ferr != nil {
		reason := changesReasonFetchFail
		if errors.Is(ferr, errGitHubAppNotConfigured) {
			reason = changesReasonNoApp
		} else {
			slog.Warn("issue changes: patch fetch failed",
				"pr", pr.PrNumber, "repo", pr.RepoOwner+"/"+pr.RepoName, "error", ferr)
		}
		writeJSON(w, http.StatusOK, IssueChangePatchResponse{Patch: nil, Reason: reason})
		return
	}
	for _, f := range files {
		if f.Path != path && f.PreviousPath != path {
			continue
		}
		if f.Patch == "" {
			reason := f.PatchReason
			if reason == "" {
				reason = prPatchAbsent
			}
			writeJSON(w, http.StatusOK, IssueChangePatchResponse{Patch: nil, Reason: reason})
			return
		}
		patch := f.Patch
		writeJSON(w, http.StatusOK, IssueChangePatchResponse{Patch: &patch, Reason: ""})
		return
	}
	writeJSON(w, http.StatusOK, IssueChangePatchResponse{Patch: nil, Reason: changesReasonFileAbsent})
}
