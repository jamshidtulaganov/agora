package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// QA-target resolution + the Live-pane embeddability probe. The QA target for an
// issue resolves through a ladder: the developer's own running app (dev_apps) →
// a local_directory folder on an online daemon (the app lives on the developer's
// own machine) → the project's configured qa_smoke_url (the team's BYO staging).
// When nothing resolves the QA run degrades to a graceful "no target" hint
// (slice_action.go) instead of fabricating a localhost.

// developerUserForIssue resolves the human developer (a user id) behind an
// issue's work, for per-developer QA routing. The work is done by an AGENT,
// so an agent assignee maps to its owner user (`agent.owner_id`). Member
// assignees ARE the developer (their issues route to their own machine).
// ok=false when no developer resolves.
func (h *Handler) developerUserForIssue(ctx context.Context, issue db.Issue) (pgtype.UUID, bool) {
	if !issue.AssigneeType.Valid || !issue.AssigneeID.Valid {
		return pgtype.UUID{}, false
	}
	if issue.AssigneeType.String == "agent" {
		agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID:          issue.AssigneeID,
			WorkspaceID: issue.WorkspaceID,
		})
		if err == nil && agent.OwnerID.Valid {
			return agent.OwnerID, true
		}
	}
	// A human assignee IS the developer — their issues route to their own
	// machine (Labs: "QA env = developer env").
	if issue.AssigneeType.String == "member" {
		member, err := h.Queries.GetMember(ctx, issue.AssigneeID)
		if err == nil && member.WorkspaceID.Bytes == issue.WorkspaceID.Bytes {
			return member.UserID, true
		}
	}
	return pgtype.UUID{}, false
}

// devLocalAppURL resolves the issue-developer's declared local app for the
// issue's project (agent_runtime.metadata.dev_apps), gated by
// labs.qa_dev_runtimes. "" on any miss.
func (h *Handler) devLocalAppURL(ctx context.Context, issue db.Issue) string {
	if !issue.ProjectID.Valid {
		return ""
	}
	ws, err := h.Queries.GetWorkspace(ctx, issue.WorkspaceID)
	if err != nil || !util.ParseWorkspaceLabs(ws.Settings).QADevRuntimes {
		return ""
	}
	devUser, ok := h.developerUserForIssue(ctx, issue)
	if !ok {
		return ""
	}
	runtime, err := h.Queries.GetDevRuntimeForProject(ctx, db.GetDevRuntimeForProjectParams{
		WorkspaceID: issue.WorkspaceID,
		OwnerID:     devUser,
		ProjectID:   uuidToString(issue.ProjectID),
	})
	if err != nil {
		return ""
	}
	return util.DevAppURL(runtime.Metadata, uuidToString(issue.ProjectID))
}

// localDirectoryQATarget resolves the daemon + path of a local_directory
// resource on the issue's project whose daemon is currently ONLINE. This is
// the "the app lives on the developer's own machine, in their own folder" QA
// tier. Unlike dev_apps it carries no ready-to-smoke URL — it only tells us
// WHERE — so the caller uses it to (a) pin the QA task to that daemon and
// (b) instruct the agent to start the app via /editor/preview and smoke the
// resulting 127.0.0.1 URL.
//
// A local_directory is a DELIBERATE per-project resource — attaching it IS the
// opt-in ("local config enabled"), so this is NOT gated on the labs
// qa_dev_runtimes toggle (which stays the opt-in for the dev_apps /
// daemon-per-dev flow). A project with a local_directory on an online daemon
// therefore runs QA locally regardless of the toggle. Projects with no
// local_directory are unaffected. Returns ok=false on any miss so resolution
// falls through to the project qa_smoke_url unchanged.
func (h *Handler) localDirectoryQATarget(ctx context.Context, issue db.Issue) (daemonID, localPath string, ok bool) {
	if !issue.ProjectID.Valid {
		return "", "", false
	}
	for _, res := range h.listProjectResourcesForProject(ctx, issue.ProjectID) {
		if res.ResourceType != "local_directory" {
			continue
		}
		var ref struct {
			LocalPath string `json:"local_path"`
			DaemonID  string `json:"daemon_id"`
		}
		if err := json.Unmarshal(res.ResourceRef, &ref); err != nil || ref.DaemonID == "" || ref.LocalPath == "" {
			continue
		}
		// The daemon must be online for its 127.0.0.1 preview to be reachable
		// by the pinned QA task; an offline daemon means fall through.
		if _, err := h.Queries.GetOnlineRuntimeForDaemon(ctx, db.GetOnlineRuntimeForDaemonParams{
			WorkspaceID: issue.WorkspaceID,
			DaemonID:    pgtype.Text{String: ref.DaemonID, Valid: true},
		}); err != nil {
			continue
		}
		return ref.DaemonID, ref.LocalPath, true
	}
	return "", "", false
}

// qaLocalDirectoryClause instructs a QA agent running ON the developer's own
// machine (the task was pinned to the local_directory's daemon) to start the
// app itself and smoke localhost, and to never mutate the user's working tree
// for the baseline. It targets the agent's CURRENT WORKING DIRECTORY (`pwd`),
// not the source localPath: in worktree-isolation mode the agent runs in the
// issue's worktree (its changes), so previewing the source folder would smoke
// the WRONG code. localPath is passed only as the in-place fallback hint.
func qaLocalDirectoryClause(localPath string) string {
	return " QA ENVIRONMENT = LOCAL (this daemon's folder, NOT a deployed box): this project runs on THIS machine — you are on the developer's own daemon. The code under test is in your CURRENT WORKING DIRECTORY: run `pwd` to get its absolute path (call it $QADIR — in worktree-isolation mode this is the issue's own worktree with its changes; in in-place mode it is the project folder " + localPath + "). Bring the app up on localhost via the daemon: POST http://127.0.0.1:$AGORA_DAEMON_PORT/editor/preview/status with body {\"workdir\":\"$QADIR\"}; if it is not running, POST http://127.0.0.1:$AGORA_DAEMON_PORT/editor/preview with the same body (it auto-detects the dev command, installs deps, and returns {\"url\":\"http://127.0.0.1:<port>/\"} — add \"command\" from the project QA smoke command below if one is set) and smoke that http://127.0.0.1:<port>/ URL. That URL is ALSO your `qa-target:<url>` key for the shared review browser. TREE SAFETY: if $QADIR is the developer's real in-place working tree, NEVER run `git checkout`/`switch`/`reset`/`stash` there or edit files outside your task; for the step-1 baseline create a throwaway scratch worktree instead: `git -C $QADIR worktree add <tmpdir> <merge-base>`, run baseline commands there, then `git -C $QADIR worktree remove <tmpdir>` and `git -C $QADIR worktree prune`."
}

// resolveQAPreviewURL resolves the URL the QA review page's Live testing bay
// embeds for an issue: the developer's own running app (dev_apps), else the
// project's configured qa_smoke_url (the team's BYO staging). "" when neither
// resolves — e.g. a local_directory project (the Live pane drives the local
// daemon preview instead) or an Agora-self-repo issue with only a per-task
// worktree (the exact artifact Product view covers that).
func (h *Handler) resolveQAPreviewURL(ctx context.Context, issue db.Issue) string {
	// A concrete declared dev_apps URL (the dev's own running app) wins.
	if url := h.devLocalAppURL(ctx, issue); url != "" {
		return url
	}
	// "Local config enabled": the project runs in a local_directory on an
	// online daemon, so the QA app lives on THAT daemon (its worktree /
	// in-place checkout), reached over the daemon's localhost. Return "" so the
	// Live pane drives the local daemon preview (agentBrowse against the
	// worktree work_dir) instead of embedding a URL.
	if _, _, ok := h.localDirectoryQATarget(ctx, issue); ok {
		return ""
	}
	// Otherwise fall back to the project's configured qa_smoke_url (the team's
	// staging / BYO QA target).
	if !issue.ProjectID.Valid {
		return ""
	}
	project, err := h.Queries.GetProject(ctx, issue.ProjectID)
	if err != nil || len(project.Settings) == 0 {
		return ""
	}
	var settings struct {
		QASmokeURL string `json:"qa_smoke_url"`
	}
	if json.Unmarshal(project.Settings, &settings) != nil {
		return ""
	}
	return strings.TrimSpace(settings.QASmokeURL)
}

// GetIssueQAPreviewURL exposes resolveQAPreviewURL to the frontend so the QA
// review page's Live testing bay can embed a project's deployed QA target
// (the team's qa_smoke_url) DIRECTLY — no daemon, no CDP screencast, no
// per-task worktree required. This is the fallback for workspaces whose QA
// target is a standing deployed environment rather than an Agora-managed
// per-issue worktree. Always 200; "url": "" means nothing resolves and the
// frontend shows its own empty state.
func (h *Handler) GetIssueQAPreviewURL(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	issueUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "issue id")
	if !ok {
		return
	}
	issue, err := h.Queries.GetIssue(r.Context(), issueUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	if _, merr := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      parseUUID(userID),
		WorkspaceID: issue.WorkspaceID,
	}); merr != nil {
		writeError(w, http.StatusForbidden, "not a member of this workspace")
		return
	}
	url := h.resolveQAPreviewURL(r.Context(), issue)
	writeJSON(w, http.StatusOK, map[string]any{
		"url":        url,
		"embeddable": url != "" && cachedURLAllowsFraming(r.Context(), url),
	})
}

// frameCheckTTL bounds how long a cached embeddability verdict is trusted.
// CSP/X-Frame-Options headers change on infra changes, not per-request — an
// hour avoids re-probing the target on every QA-page load (the real network
// hop took ~1.2s even when healthy) while still catching a config change
// within a working session.
const frameCheckTTL = time.Hour

type frameCheckResult struct {
	embeddable bool
	expires    time.Time
}

var (
	frameCheckMu    sync.Mutex
	frameCheckCache = map[string]frameCheckResult{}
)

// cachedURLAllowsFraming wraps urlAllowsFraming with a per-URL TTL cache —
// every QA review page load for the same project resolves the same target
// URL, so without caching each one would pay the full outbound HEAD round trip.
func cachedURLAllowsFraming(ctx context.Context, target string) bool {
	frameCheckMu.Lock()
	if cached, ok := frameCheckCache[target]; ok && time.Now().Before(cached.expires) {
		frameCheckMu.Unlock()
		return cached.embeddable
	}
	frameCheckMu.Unlock()

	result := urlAllowsFraming(ctx, target)

	frameCheckMu.Lock()
	frameCheckCache[target] = frameCheckResult{embeddable: result, expires: time.Now().Add(frameCheckTTL)}
	frameCheckMu.Unlock()
	return result
}

// responseBlocksFraming reports whether a single response's headers forbid
// cross-origin framing: X-Frame-Options deny/sameorigin, or a CSP
// frame-ancestors directive that isn't the bare wildcard source `*`.
//
// Tokenizes the directive's source list rather than substring-matching for
// "*" — a real bug caught during live testing: a CSP like `frame-ancestors
// 'self' https://web.telegram.org https://*.telegram.org` carries a SCOPED
// subdomain wildcard inside one source value, not the CSP special token `*`
// (any origin). A naive strings.Contains(directive, "*") matches that
// substring and wrongly concludes the policy is wide open — exactly backwards.
// Only an exact `*` TOKEN in the source list means "any origin may frame this."
func responseBlocksFraming(h http.Header) bool {
	if xfo := strings.ToLower(strings.TrimSpace(h.Get("X-Frame-Options"))); xfo == "deny" || xfo == "sameorigin" {
		return true
	}
	for _, directive := range strings.Split(h.Get("Content-Security-Policy"), ";") {
		directive = strings.TrimSpace(directive)
		fields := strings.Fields(directive)
		if len(fields) == 0 || strings.ToLower(fields[0]) != "frame-ancestors" {
			continue
		}
		openToAll := false
		for _, source := range fields[1:] {
			if source == "*" {
				openToAll = true
				break
			}
		}
		if !openToAll {
			return true
		}
	}
	return false
}

// urlAllowsFraming reports whether url's response headers permit embedding it
// in a cross-origin iframe — the Live testing bay iframe would otherwise
// render silently blank (a CSP frame-ancestors or X-Frame-Options block fires
// no JS error event, so the frontend has no way to detect it after the fact).
// Checked server-side because the outbound request needs no CORS exemption
// here, unlike a browser fetch.
//
// Uses GET (not HEAD) and walks redirects MANUALLY, checking headers at
// EVERY hop, instead of trusting Go's default client to auto-follow and
// report only the final response — a real iframe navigation is a GET, and
// an intermediate redirect can legitimately carry its own restrictive
// headers even when the final destination's happen to look permissive.
// Bounded to 5 hops. Any signal we can't positively clear — request
// failure, timeout, too many redirects, or a block seen at ANY hop —
// returns false: the caller shows an "Open" link instead of a gambled
// iframe. False positives (looks embeddable, isn't) are far worse for a
// first impression than false negatives (embeddable, we just offer a link
// instead).
func urlAllowsFraming(ctx context.Context, target string) bool {
	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	next := target
	for hop := 0; hop < 5; hop++ {
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, next, nil)
		if err != nil {
			return false
		}
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		blocked := responseBlocksFraming(resp.Header)
		location := resp.Header.Get("Location")
		_, _ = io.CopyN(io.Discard, resp.Body, 4096) // drain a little so keep-alive can reuse the conn
		resp.Body.Close()

		if blocked {
			return false
		}
		if resp.StatusCode < 300 || resp.StatusCode >= 400 || location == "" {
			return true // terminal response, no block seen at this or any prior hop
		}
		redirectURL, err := url.Parse(location)
		if err != nil {
			return false
		}
		base, err := url.Parse(next)
		if err != nil {
			return false
		}
		next = base.ResolveReference(redirectURL).String()
	}
	return false // too many redirects — can't confirm, fail closed
}
