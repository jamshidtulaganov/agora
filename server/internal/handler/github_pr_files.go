package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// THE EVIDENCE BEHIND THE DERIVED RISK TIER (plan §A1.1).
//
// risk_tier.go can classify a diff against the project risk map, but only if it
// knows which files the diff touches — and the `pull_request` webhook payload
// does not carry one. It ships counts (additions / deletions / changed_files)
// and nothing else. So this file fetches the list from GitHub's PR Files API,
// once per state-changing PR event, and caches it on the PR row (migration 210).
//
// Why the server fetches instead of asking the agent: the tier gates auto-merge
// and the done gate. An agent that reports its own file list is reporting its
// own blast radius, which is the self-report §A1.1 exists to remove. GitHub is
// the only party to this that did not write the diff.
//
// EVERYTHING HERE IS BEST-EFFORT AND DETACHED. No App credentials, a rate
// limit, a network blip, a GitLab-provider row — all of them leave changed_paths
// empty, which the resolver reads as UNKNOWN and answers with the risk map's
// fail-closed default. A failure to classify must never fail a webhook, and it
// must never silently downgrade a tier.

const (
	// prFilesPerPage is GitHub's maximum page size for the Files API.
	prFilesPerPage = 100
	// prFilesMaxPages bounds the fetch. 300 files is far past the point where
	// any risk map returns anything but its strictest matching tier, so more
	// pages buy nothing and cost rate limit.
	prFilesMaxPages = 3
	// prFilesTimeout bounds the whole detached sync.
	prFilesTimeout = 25 * time.Second
)

// Risk label colors. Red / amber / green, matching how the tiers read: critical
// stops a merge, guarded slows it, safe is the normal flow.
const (
	riskLabelCriticalColor = "#dc2626"
	riskLabelGuardedColor  = "#f59e0b"
	riskLabelSafeColor     = "#16a34a"
)

// riskLabelNames is every label this stamp owns. Listed once so attaching one
// and detaching the others can never fall out of sync.
var riskLabelNames = map[string]string{
	riskTierCritical: riskLabelCriticalColor,
	riskTierGuarded:  riskLabelGuardedColor,
	riskTierSafe:     riskLabelSafeColor,
}

// prDiffChangingActions are the `pull_request` actions that can change which
// files a PR touches. Metadata events (labeled, assigned, review_requested…)
// cannot, so they must not spend a GitHub API call or overwrite a good list.
var prDiffChangingActions = map[string]bool{
	"opened":             true,
	"reopened":           true,
	"synchronize":        true,
	"ready_for_review":   true,
	"converted_to_draft": true,
}

// ── installation token ──────────────────────────────────────────────────────

// installationTokenCacheEntry is one minted token and when it stops being
// usable. GitHub issues installation tokens for an hour; the cache expires them
// early so a request never starts with a token that dies mid-flight.
type installationTokenCacheEntry struct {
	token     string
	expiresAt time.Time
}

var (
	installationTokenMu    sync.Mutex
	installationTokenCache = map[int64]installationTokenCacheEntry{}
)

// installationTokenSkew retires a cached token this long before GitHub does.
const installationTokenSkew = 5 * time.Minute

// githubInstallationToken mints (or reuses) an installation access token for a
// GitHub App installation. Returns ("", nil) — a soft "App auth not available"
// — when the operator has not configured the App identity, mirroring
// signGitHubAppJWT's contract so callers can fall through instead of erroring.
func githubInstallationToken(ctx context.Context, installationID int64) (string, error) {
	if installationID == 0 {
		return "", nil
	}
	installationTokenMu.Lock()
	if cached, ok := installationTokenCache[installationID]; ok && time.Now().Before(cached.expiresAt) {
		installationTokenMu.Unlock()
		return cached.token, nil
	}
	installationTokenMu.Unlock()

	appJWT, err := signGitHubAppJWT(time.Now())
	if err != nil {
		return "", err
	}
	if appJWT == "" {
		return "", nil // App identity not configured — not an error
	}

	endpoint := fmt.Sprintf("%s/app/installations/%d/access_tokens",
		strings.TrimRight(githubAPIBase, "/"), installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+appJWT)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github: installation token %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var parsed struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || strings.TrimSpace(parsed.Token) == "" {
		return "", fmt.Errorf("github: installation token response was not usable")
	}
	expiry := time.Now().Add(30 * time.Minute)
	if parsed.ExpiresAt != "" {
		if t, perr := time.Parse(time.RFC3339, parsed.ExpiresAt); perr == nil {
			expiry = t.Add(-installationTokenSkew)
		}
	}
	installationTokenMu.Lock()
	installationTokenCache[installationID] = installationTokenCacheEntry{token: parsed.Token, expiresAt: expiry}
	installationTokenMu.Unlock()
	return parsed.Token, nil
}

// ── the files fetch ─────────────────────────────────────────────────────────

// prChangedFile is one entry of GitHub's "List pull request files" response.
//
// The same upstream call answers two different questions, so it is fetched
// once and shaped once: the risk resolver wants only the paths, and the in-app
// Changes view wants the per-file status, the +/- counts and (on demand) the
// unified patch. Dropping the richer fields — which is what this file used to
// do — forced a second round trip to GitHub for data already on the wire.
//
// Patch is the per-file unified diff GitHub inlines. It is ABSENT for binary
// files and for files whose diff GitHub considers too large; PatchReason
// records which, so the UI can say so instead of rendering an empty box.
// A patch is NEVER persisted — it lives in the request, or in the bounded
// in-process cache in issue_changes.go, and nowhere else.
type prChangedFile struct {
	Path         string
	PreviousPath string
	Status       string
	Additions    int
	Deletions    int
	Patch        string
	PatchReason  string
}

// prPatchAbsent is the PatchReason for a file GitHub listed without a patch:
// a binary blob, or a diff past the size GitHub inlines. Both read the same
// way to a human ("there is no diff to show here"), so they share one value
// and the UI points at GitHub for either.
const prPatchAbsent = "patch_unavailable"

// fetchPullRequestFiles returns the PR's changed files as GitHub reports them,
// paginated up to prFilesMaxPages. On a partial failure it returns what it
// collected SO FAR together with the error, so a caller that can use a partial
// list (the risk resolver treats short lists as evidence, not as truth) is not
// forced to discard it.
func fetchPullRequestFiles(ctx context.Context, token, owner, repo string, number int32) ([]prChangedFile, error) {
	out := make([]prChangedFile, 0, prFilesPerPage)
	for page := 1; page <= prFilesMaxPages; page++ {
		endpoint := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/files?per_page=%d&page=%d",
			strings.TrimRight(githubAPIBase, "/"), owner, repo, number, prFilesPerPage, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return out, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return out, err
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return out, fmt.Errorf("github: pr files %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var files []struct {
			Filename         string `json:"filename"`
			PreviousFilename string `json:"previous_filename"`
			Status           string `json:"status"`
			Additions        int    `json:"additions"`
			Deletions        int    `json:"deletions"`
			Patch            string `json:"patch"`
		}
		if err := json.Unmarshal(body, &files); err != nil {
			return out, err
		}
		for _, f := range files {
			path := normalizeRiskPath(f.Filename)
			if path == "" {
				continue
			}
			entry := prChangedFile{
				Path:         path,
				PreviousPath: normalizeRiskPath(f.PreviousFilename),
				Status:       strings.TrimSpace(f.Status),
				Additions:    f.Additions,
				Deletions:    f.Deletions,
				Patch:        f.Patch,
			}
			if entry.Patch == "" {
				entry.PatchReason = prPatchAbsent
			}
			out = append(out, entry)
		}
		if len(files) < prFilesPerPage {
			break // last page
		}
	}
	return out, nil
}

// changedPathsFromFiles flattens a file list into the repo-relative paths the
// risk map is matched against.
//
// A RENAME reports its new path in `filename` and its old one in
// `previous_filename`; both are returned, because a file moved OUT of a
// critical module is still a change to that module.
func changedPathsFromFiles(files []prChangedFile) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(files))
	for _, f := range files {
		for _, name := range []string{f.Path, f.PreviousPath} {
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// fetchPullRequestChangedPaths returns the repo-relative paths a PR touches, as
// GitHub reports them. Kept as a named entry point because the risk sync below
// wants nothing but the paths, and a partial list on error is still usable.
func fetchPullRequestChangedPaths(ctx context.Context, token, owner, repo string, number int32) ([]string, error) {
	files, err := fetchPullRequestFiles(ctx, token, owner, repo, number)
	return changedPathsFromFiles(files), err
}

// ── the sync + stamp ────────────────────────────────────────────────────────

// maybeSyncPullRequestRisk is the webhook's entry point. It returns immediately;
// the fetch, the persist and the label stamp all run detached, because a webhook
// must be acknowledged whether or not GitHub's API answers.
func (h *Handler) maybeSyncPullRequestRisk(ctx context.Context, action string, pr db.GithubPullRequest, issueIDs []string) {
	if !prDiffChangingActions[action] {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(pr.Provider), "github") {
		// GitLab-provider rows (migration 124) come in through a different path
		// and have no GitHub Files API behind them.
		return
	}
	if pr.InstallationID == 0 || pr.RepoOwner == "" || pr.RepoName == "" {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("pr risk sync: panic recovered", "recover", r)
			}
		}()
		bgctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), prFilesTimeout)
		defer cancel()
		h.syncPullRequestChangedPaths(bgctx, pr, issueIDs)
	}()
}

// syncPullRequestChangedPaths fetches the file list, stores it, and re-stamps
// the derived risk label on every issue the PR is linked to.
func (h *Handler) syncPullRequestChangedPaths(ctx context.Context, pr db.GithubPullRequest, issueIDs []string) {
	token, err := githubInstallationToken(ctx, pr.InstallationID)
	if err != nil {
		slog.Warn("pr risk sync: installation token failed — the risk tier stays self-reported for this PR",
			"pr", pr.PrNumber, "repo", pr.RepoOwner+"/"+pr.RepoName, "error", err)
		return
	}
	if token == "" {
		return // App identity not configured; nothing to say about it every webhook
	}
	paths, err := fetchPullRequestChangedPaths(ctx, token, pr.RepoOwner, pr.RepoName, pr.PrNumber)
	if err != nil {
		slog.Warn("pr risk sync: file list fetch failed — the risk tier stays self-reported for this PR",
			"pr", pr.PrNumber, "repo", pr.RepoOwner+"/"+pr.RepoName, "error", err)
		return
	}
	if len(paths) == 0 {
		// An EMPTY answer is never written: it is indistinguishable on the wire
		// from "the fetch returned nothing useful", and storing it would turn
		// UNKNOWN into a positive claim that the PR touches nothing.
		return
	}
	if _, err := h.Queries.SetGitHubPullRequestChangedPaths(ctx, db.SetGitHubPullRequestChangedPathsParams{
		ID:           pr.ID,
		WorkspaceID:  pr.WorkspaceID,
		ChangedPaths: paths,
	}); err != nil {
		slog.Warn("pr risk sync: persist changed paths failed",
			"pr", pr.PrNumber, "error", err)
		return
	}
	slog.Info("pr risk sync: changed paths stored",
		"pr", pr.PrNumber, "repo", pr.RepoOwner+"/"+pr.RepoName, "files", len(paths), "issues", len(issueIDs))

	for _, id := range issueIDs {
		// These ids came out of our own webhook bookkeeping, but a slice of
		// strings is a slice of strings: parse with the error-returning variant
		// so a malformed entry cannot panic a detached goroutine.
		issueUUID, perr := util.ParseUUID(id)
		if perr != nil {
			continue
		}
		issue, ierr := h.Queries.GetIssue(ctx, issueUUID)
		if ierr != nil || uuidToString(issue.WorkspaceID) != uuidToString(pr.WorkspaceID) {
			continue
		}
		h.stampDerivedRiskLabel(ctx, issue)
	}
}

// stampDerivedRiskLabel writes the SERVER-DERIVED tier onto the issue as a
// risk:<tier> label, so the five existing consumers of issueRiskTier and every
// read surface that renders labels see it with no further plumbing.
//
// The label is a CACHE of a computed answer, not the answer itself —
// resolveIssueRiskTier recomputes from the map and the file list on every read,
// and the derived answer outranks the label. Stamping exists so a human sees the
// tier on the card and so the brief the next agent gets already carries it.
//
// It is silent when nothing changes and it says so when something does: a tier
// that appears on an issue with no explanation reads as "who moved my issue",
// which is the reaction that gets derivation switched off (living truth, Tier 1a).
func (h *Handler) stampDerivedRiskLabel(ctx context.Context, issue db.Issue) {
	resolution := h.resolveIssueRiskTier(ctx, issue)
	if resolution.Source != riskSourceDerived {
		return // no evidence, or an explicit stricter label already governs
	}
	tier := resolution.Tier
	color, known := riskLabelNames[tier]
	if !known {
		return
	}
	name := "risk:" + tier
	if h.issueHasLabel(ctx, issue, name) {
		return // already stamped; nothing to say
	}
	labelID, err := h.ensureLabel(ctx, issue.WorkspaceID, name, color)
	if err != nil {
		slog.Warn("risk stamp: ensure label failed", "error", err, "issue_id", uuidToString(issue.ID))
		return
	}
	if err := h.Queries.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
		IssueID: issue.ID, LabelID: labelID, WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		slog.Warn("risk stamp: attach label failed", "error", err, "issue_id", uuidToString(issue.ID))
		return
	}
	for other := range riskLabelNames {
		if other == tier {
			continue
		}
		h.TaskService.DetachIssueLabelByName(ctx, issue, "risk:"+other)
	}

	// Publish the FULL label set: the frontend's labels-changed handler REPLACES
	// an issue's labels with the payload, so an issue_id-only event would wipe
	// every client's label cache.
	if labels, lerr := h.Queries.ListLabelsByIssue(ctx, db.ListLabelsByIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	}); lerr == nil {
		h.publish(protocol.EventIssueLabelsChanged, uuidToString(issue.WorkspaceID), "system", "", map[string]any{
			"issue_id": uuidToString(issue.ID),
			"labels":   labelsToResponse(labels),
		})
	}

	h.postDerivedStatusProvenance(ctx, issue,
		"🎚️ Risk tier set to **"+tier+"** from the pull request's changed files, matched against this project's risk map ("+
			strconv.Itoa(resolution.KnownPaths)+" file(s) classified). This tier is derived by Agora, not reported by the agent.")
}
