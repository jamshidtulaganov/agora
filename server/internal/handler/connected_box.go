package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/multica-ai/multica/server/internal/config"
	"io"
	"log/slog"
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

// Remote Boxes (opt-in, additive). A connected_box is a developer's own remote
// dev server that Agora onboards over SSH and runs a NORMAL native self-host
// daemon on. This handler is the CRUD surface for that new, parallel layer; it
// changes nothing about the agent/task/runtime model. The bootstrapper (SSH
// install) and editor tunnel-manager are layered on top in later phases.

// remoteBoxesEnabled gates the entire Remote Boxes feature. Default OFF. The
// routes are not even mounted when disabled (see router) — this in-handler check
// is defense-in-depth so a directly-dispatched call still fails closed.
func remoteBoxesEnabled() bool {
	return config.Bool("AGORA_REMOTE_BOXES_ENABLED")
}

// ConnectedBoxResponse is the API shape. UUIDs are strings; nullable columns are
// pointers so an absent owner/daemon serializes as null (consumers must
// optional-chain — see the API Response Compatibility rules).
type ConnectedBoxResponse struct {
	ID           string  `json:"id"`
	WorkspaceID  string  `json:"workspace_id"`
	OwnerID      *string `json:"owner_id"`
	Label        string  `json:"label"`
	SSHHost      string  `json:"ssh_host"`
	SSHUser      string  `json:"ssh_user"`
	SSHPort      int32   `json:"ssh_port"`
	DeployPubkey string  `json:"deploy_pubkey"`
	DaemonID     *string `json:"daemon_id"`
	Status       string  `json:"status"`
	LastError    string  `json:"last_error"`
	RepoURL      string  `json:"repo_url"`
	WorkDir      string  `json:"work_dir"`
	LastBranch   string  `json:"last_branch"`
	ProjectID    *string `json:"project_id"`
	CreatedAt    string  `json:"created_at"`
}

func connectedBoxToResponse(b db.ConnectedBox) ConnectedBoxResponse {
	resp := ConnectedBoxResponse{
		ID:           uuidToString(b.ID),
		WorkspaceID:  uuidToString(b.WorkspaceID),
		OwnerID:      uuidToPtr(b.OwnerID),
		Label:        b.Label,
		SSHHost:      b.SshHost,
		SSHUser:      b.SshUser,
		SSHPort:      b.SshPort,
		DeployPubkey: b.DeployPubkey,
		DaemonID:     uuidToPtr(b.DaemonID),
		Status:       b.Status,
		LastError:    b.LastError,
		RepoURL:      b.RepoUrl,
		WorkDir:      b.WorkDir,
		LastBranch:   b.LastBranch,
		ProjectID:    uuidToPtr(b.ProjectID),
	}
	if b.CreatedAt.Valid {
		resp.CreatedAt = b.CreatedAt.Time.Format(time.RFC3339)
	}
	return resp
}

type CreateConnectedBoxRequest struct {
	Label     string `json:"label"`
	SSHHost   string `json:"ssh_host"`
	SSHUser   string `json:"ssh_user"`
	SSHPort   int32  `json:"ssh_port"`
	RepoURL   string `json:"repo_url"`
	WorkDir   string `json:"work_dir"`
	ProjectID string `json:"project_id"`
}

// ListConnectedBoxes returns the workspace's remote boxes (tenancy-scoped).
func (h *Handler) ListConnectedBoxes(w http.ResponseWriter, r *http.Request) {
	if !remoteBoxesEnabled() {
		writeError(w, http.StatusNotFound, "remote boxes are not enabled")
		return
	}
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	wsID := h.resolveWorkspaceID(r)
	if wsID == "" {
		writeError(w, http.StatusBadRequest, "workspace required")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace_id")
	if !ok {
		return
	}
	boxes, err := h.Queries.ListConnectedBoxesByWorkspace(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list remote boxes")
		return
	}
	out := make([]ConnectedBoxResponse, 0, len(boxes))
	for _, b := range boxes {
		out = append(out, connectedBoxToResponse(b))
	}
	writeJSON(w, http.StatusOK, map[string]any{"boxes": out})
}

// CreateConnectedBox registers a remote box (status=pending). Owned by the
// calling user; bootstrap/keypair generation happen in a later step.
func (h *Handler) CreateConnectedBox(w http.ResponseWriter, r *http.Request) {
	if !remoteBoxesEnabled() {
		writeError(w, http.StatusNotFound, "remote boxes are not enabled")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	wsID := h.resolveWorkspaceID(r)
	if wsID == "" {
		writeError(w, http.StatusBadRequest, "workspace required")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace_id")
	if !ok {
		return
	}
	var req CreateConnectedBoxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	req.SSHHost = strings.TrimSpace(req.SSHHost)
	req.SSHUser = strings.TrimSpace(req.SSHUser)
	if req.Label == "" || req.SSHHost == "" || req.SSHUser == "" {
		writeError(w, http.StatusBadRequest, "label, ssh_host and ssh_user are required")
		return
	}
	if req.SSHPort <= 0 {
		req.SSHPort = 22
	}
	var projectID pgtype.UUID
	if pid := strings.TrimSpace(req.ProjectID); pid != "" {
		var ok bool
		if projectID, ok = parseUUIDOrBadRequest(w, pid, "project_id"); !ok {
			return
		}
	}
	box, err := h.Queries.CreateConnectedBox(r.Context(), db.CreateConnectedBoxParams{
		WorkspaceID:  wsUUID,
		OwnerID:      parseUUID(userID),
		Label:        req.Label,
		SshHost:      req.SSHHost,
		SshUser:      req.SSHUser,
		SshPort:      req.SSHPort,
		DeployPubkey: "",
		RepoUrl:      strings.TrimSpace(req.RepoURL),
		WorkDir:      strings.TrimSpace(req.WorkDir),
		ProjectID:    projectID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create remote box")
		return
	}
	writeJSON(w, http.StatusCreated, connectedBoxToResponse(box))
}

// ProvisionConnectedBoxRequest provisions a per-developer QA box for a workspace
// member. handle is optional — it defaults to a slug of the member's email local
// part. dry_run returns the exact runbook + computed placement WITHOUT touching
// the host, so the operator reviews it before the real run (the host is a real
// prod server).
type ProvisionConnectedBoxRequest struct {
	MemberID string `json:"member_id"`
	Handle   string `json:"handle"`
	DryRun   bool   `json:"dry_run"`
}

// ProvisionConnectedBoxResponse reports the computed placement + the (token-
// redacted) runbook, and — on a real run — the created box row and the redacted
// host output. On a dry run Box is nil and Ran is false.
type ProvisionConnectedBoxResponse struct {
	Handle    string                `json:"handle"`
	Subdomain string                `json:"subdomain"`
	WorkDir   string                `json:"work_dir"`
	Database  string                `json:"database"`
	Script    string                `json:"script"`
	DryRun    bool                  `json:"dry_run"`
	Ran       bool                  `json:"ran"`
	OK        bool                  `json:"ok"`
	Output    string                `json:"output"`
	Box       *ConnectedBoxResponse `json:"box"`
}

// ProvisionConnectedBoxForMember carves a per-developer QA box out of the shared
// QA host (POST /api/remote-boxes/provision): it resolves the member's user,
// derives a safe handle, and either returns the runbook for review (dry_run) or
// runs it over SSH and registers the resulting connected_box owned by that user.
func (h *Handler) ProvisionConnectedBoxForMember(w http.ResponseWriter, r *http.Request) {
	if !remoteBoxesEnabled() {
		writeError(w, http.StatusNotFound, "remote boxes are not enabled")
		return
	}
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	wsID := h.resolveWorkspaceID(r)
	if wsID == "" {
		writeError(w, http.StatusBadRequest, "workspace required")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace_id")
	if !ok {
		return
	}
	if !qaHostConfigured() {
		writeError(w, http.StatusServiceUnavailable, "the QA host is not configured (set AGORA_QA_HOST_*)")
		return
	}
	var req ProvisionConnectedBoxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	memberUUID, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(req.MemberID), "member_id")
	if !ok {
		return
	}
	member, err := h.Queries.GetMember(r.Context(), memberUUID)
	if err != nil || member.WorkspaceID.Bytes != wsUUID.Bytes {
		writeError(w, http.StatusNotFound, "member not found in this workspace")
		return
	}

	// Derive the handle: explicit wins; otherwise slug the member's email local
	// part. Either way it must survive sanitizeHandle (the subdomain/path/DB-name
	// security boundary).
	rawHandle := strings.TrimSpace(req.Handle)
	if rawHandle == "" {
		if user, uerr := h.Queries.GetUser(r.Context(), member.UserID); uerr == nil {
			rawHandle = user.Email
		}
	}
	handle := sanitizeHandle(rawHandle)
	if handle == "" {
		writeError(w, http.StatusBadRequest, "could not derive a valid handle (allowed: a-z, 0-9, hyphen); pass an explicit handle")
		return
	}

	p := provisionParams{
		Handle:     handle,
		BaseDomain: qaHostBaseDomain(),
		WebRoot:    qaHostWebRoot(),
		RepoURL:    qaHostRepoURL(),
		SeedDir:    qaHostSeedDir(),
	}

	// Opt-in, DEFAULT-OFF safety rails — both are no-ops unless a deployment
	// explicitly sets the corresponding env var, so the real sd-main flow
	// (agora.sdteam.uz / dbt_agora) is unaffected. They run BEFORE any
	// SSH/mutation, ahead of dry_run too, so a misconfigured non-prod
	// deployment can't even preview a script targeting the real host.
	if err := qaHostCheckTarget(qaHostSSHHost(), p); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err := qaHostCheckDBPrefix(qaHostSeedDB()); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	resp := ProvisionConnectedBoxResponse{
		Handle:    handle,
		Subdomain: boxSubdomain(p),
		WorkDir:   boxWorkDir(p),
		// The box inherits the seed's DB config verbatim ("keep each box's
		// existing DB"); report which DB that is, for the operator's review.
		Database: qaHostSeedDB(),
		Script:   redactGitToken(buildProvisionScript(p, remoteBoxesGitToken())),
		DryRun:   req.DryRun,
	}
	// Dry run is the review gate: return the exact runbook + placement, touch
	// nothing on the host, register no row.
	if req.DryRun {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	keyPath := remoteBoxesSSHKeyPath()
	if keyPath == "" {
		writeError(w, http.StatusServiceUnavailable, "remote box SSH key is not configured on the server")
		return
	}

	// Register the box first (owner = the member's user) so a row exists even if
	// the runbook fails partway — its status then carries the error for the operator.
	box, err := h.Queries.CreateConnectedBox(r.Context(), db.CreateConnectedBoxParams{
		WorkspaceID:  wsUUID,
		OwnerID:      member.UserID,
		Label:        handle,
		SshHost:      qaHostSSHHost(),
		SshUser:      qaHostSSHUser(),
		SshPort:      int32(qaHostSSHPort()),
		DeployPubkey: "",
		RepoUrl:      qaHostRepoURL(),
		WorkDir:      boxWorkDir(p),
		ProjectID:    pgtype.UUID{},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to register the box")
		return
	}

	out, runErr := sshRunner{}.Run(r.Context(), qaHostSSHHost(), qaHostSSHUser(), qaHostSSHPort(), keyPath,
		buildProvisionScript(p, remoteBoxesGitToken()))
	status := "online"
	lastErr := ""
	if runErr != nil {
		status = "error"
		lastErr = redactGitToken(runErr.Error())
	}
	updated, uerr := h.Queries.UpdateConnectedBoxStatus(r.Context(), db.UpdateConnectedBoxStatusParams{
		ID:              box.ID,
		WorkspaceID:     wsUUID,
		Status:          status,
		LastError:       lastErr,
		LastBootstrapAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if uerr != nil {
		updated = box
	}
	boxResp := connectedBoxToResponse(updated)
	resp.Box = &boxResp
	resp.Ran = true
	resp.OK = runErr == nil
	resp.Output = redactGitToken(out)
	code := http.StatusCreated
	if runErr != nil {
		code = http.StatusBadGateway
	}
	writeJSON(w, code, resp)
}

// DeleteConnectedBox removes a remote box (tenancy-scoped). Deprovisioning the
// box's daemon/key is a control-plane step layered on later.
func (h *Handler) DeleteConnectedBox(w http.ResponseWriter, r *http.Request) {
	if !remoteBoxesEnabled() {
		writeError(w, http.StatusNotFound, "remote boxes are not enabled")
		return
	}
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	wsID := h.resolveWorkspaceID(r)
	if wsID == "" {
		writeError(w, http.StatusBadRequest, "workspace required")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace_id")
	if !ok {
		return
	}
	boxUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "box id")
	if !ok {
		return
	}
	if err := h.Queries.DeleteConnectedBox(r.Context(), db.DeleteConnectedBoxParams{
		ID:          boxUUID,
		WorkspaceID: wsUUID,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete remote box")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// repoBasename returns the bare repo name from a git URL (last path segment,
// without .git), lowercased — so a project's bound repo matches a box's repo
// regardless of fork/upstream owner or https/ssh form (e.g. both
// github.com/azizkh/sd.git and github.com/jamshidtulaganov/sd-main.git differ in
// owner, but a project bound to the fork matches the box bound to the fork).
func repoBasename(u string) string {
	u = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(u)), ".git")
	if i := strings.LastIndexAny(u, "/:"); i >= 0 {
		u = u[i+1:]
	}
	return u
}

// developerUserForIssue resolves the human developer (a user id) behind an
// issue's work, for per-developer QA box routing. The work is done by an AGENT,
// so an agent assignee maps to its owner user (`agent.owner_id`); that user id is
// what a per-dev `connected_box.owner_id` matches. Member/squad assignees fall
// through (they route to the project box). ok=false when no developer resolves.
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
	// A human assignee IS the developer — their issues route to their own box
	// (Labs: "QA env = developer env", e.g. Shahzod's issue → shahzod.sdteam.uz).
	if issue.AssigneeType.String == "member" {
		member, err := h.Queries.GetMember(ctx, issue.AssigneeID)
		if err == nil && member.WorkspaceID.Bytes == issue.WorkspaceID.Bytes {
			return member.UserID, true
		}
	}
	return pgtype.UUID{}, false
}

// connectedBoxForIssue resolves the QA box for an issue. Primary match is the
// EXPLICIT project binding (connected_box.project_id == issue.project_id) — the
// box's repo (a fork) legitimately differs from the project's repo (upstream),
// and a renamed fork breaks any name-based match, so the link must be explicit.
// Fallback (for boxes not yet bound to a project) is a repo-name match against
// the project's github_repo resource. ok=false when nothing resolves.
func (h *Handler) connectedBoxForIssue(ctx context.Context, issue db.Issue) (db.ConnectedBox, bool) {
	if !issue.ProjectID.Valid {
		return db.ConnectedBox{}, false
	}
	boxes, err := h.Queries.ListConnectedBoxesByWorkspace(ctx, issue.WorkspaceID)
	if err != nil {
		return db.ConnectedBox{}, false
	}
	// Labs flags steer this resolver: qa_dev_boxes gates step 0, and
	// qa_fallback_box_id adds a last-resort shared box (step 3).
	labs := defaultWorkspaceLabs()
	if ws, werr := h.Queries.GetWorkspace(ctx, issue.WorkspaceID); werr == nil {
		labs = workspaceLabs(ws.Settings)
	}
	// 0. Per-developer box (highest priority): the developer behind this issue's
	//    work gets their OWN box (owner_id match), so their branch deploys to their
	//    own isolated environment instead of colliding with other devs on the
	//    shared project box. Only the per-task deploy-qa path uses this resolver;
	//    the sprint-end regression keeps its own project-only box resolution.
	if labs.QADevBoxes {
		if devUser, ok := h.developerUserForIssue(ctx, issue); ok {
			for _, b := range boxes {
				// The box must be EXPLICITLY scoped to the issue's project — a
				// developer's sd-main box must never swallow their sd-cs issue
				// (each project's boxes serve a different app). No project
				// binding = no per-dev match; nothing is a cross-project
				// default.
				if b.OwnerID.Valid && b.OwnerID.Bytes == devUser.Bytes &&
					b.ProjectID.Valid && b.ProjectID.Bytes == issue.ProjectID.Bytes {
					return b, true
				}
			}
		}
	}
	// 1. Explicit project binding — two passes: an OWNERLESS (shared) project
	//    box wins over someone's personal box for the same project, so another
	//    developer's issue never lands on a colleague's environment; a solely
	//    per-dev-provisioned project (only owned boxes) still resolves.
	for _, b := range boxes {
		if !b.OwnerID.Valid && b.ProjectID.Valid && b.ProjectID.Bytes == issue.ProjectID.Bytes {
			return b, true
		}
	}
	for _, b := range boxes {
		if b.ProjectID.Valid && b.ProjectID.Bytes == issue.ProjectID.Bytes {
			return b, true
		}
	}
	// 2. Fallback: repo-name match against the project's bound repo.
	var projRepo string
	for _, row := range h.listProjectResourcesForProject(ctx, issue.ProjectID) {
		if row.ResourceType != "github_repo" {
			continue
		}
		var ref struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(row.ResourceRef, &ref) == nil && strings.TrimSpace(ref.URL) != "" {
			projRepo = ref.URL
			break
		}
	}
	if projRepo != "" {
		want := repoBasename(projRepo)
		for _, b := range boxes {
			if b.RepoUrl != "" && repoBasename(b.RepoUrl) == want {
				return b, true
			}
		}
	}
	// 3. Labs fallback: the workspace-designated shared box (e.g.
	//    sandbox.sdteam.uz). Project-scoped like everything above: a fallback
	//    bound to a project serves ONLY that project's issues; only a
	//    deliberately unbound fallback is workspace-generic. Different projects
	//    run different apps — a fallback must never become a cross-project
	//    default by accident.
	if labs.QAFallbackBoxID != "" {
		if fbID, ferr := parseUUIDErr(labs.QAFallbackBoxID); ferr == nil {
			for _, b := range boxes {
				if b.ID.Bytes != fbID.Bytes {
					continue
				}
				if !b.ProjectID.Valid || b.ProjectID.Bytes == issue.ProjectID.Bytes {
					return b, true
				}
			}
		}
	}
	return db.ConnectedBox{}, false
}

// devBoxSmokeURL returns the https URL the issue's resolved QA box serves
// (https://<subdomain>, derived from the box's work_dir /var/www/<subdomain>),
// so run_qa can smoke the ASSIGNEE DEVELOPER'S own box instead of a single
// project-wide URL. "" when remote boxes are off, no box resolves, or the box
// has no work_dir — the run_qa smoke then falls back to the project qa_smoke_url.
func (h *Handler) devBoxSmokeURL(ctx context.Context, issue db.Issue) string {
	// Step 0 (daemon-per-dev): the developer's own ONLINE daemon declaring a
	// local app for this project beats every deployed target — the QA task is
	// pinned to that runtime (service.maybePinTaskToDevRuntime), so its
	// 127.0.0.1 URL is meaningful to the agent that will run it. Opt-in via
	// labs.qa_dev_runtimes; project-scoped by construction (dev_apps is keyed
	// by project id) — never a cross-project default.
	if url := h.devLocalAppURL(ctx, issue); url != "" {
		return url
	}
	if !remoteBoxesEnabled() {
		return ""
	}
	box, ok := h.connectedBoxForIssue(ctx, issue)
	if !ok {
		return ""
	}
	return boxSmokeURL(box)
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
// therefore runs QA locally regardless of the toggle, taking precedence over
// the connected sdteam boxes. Projects with no local_directory are unaffected.
// Returns ok=false on any miss so resolution falls through to connected_box /
// project qa_smoke_url unchanged.
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

// boxSmokeURL derives the https URL a box serves from its work_dir
// (/var/www/<subdomain> → https://<subdomain>). "" when the box has no work_dir.
func boxSmokeURL(box db.ConnectedBox) string {
	wd := strings.TrimRight(strings.TrimSpace(box.WorkDir), "/")
	if wd == "" {
		return ""
	}
	sub := wd[strings.LastIndex(wd, "/")+1:]
	if sub == "" {
		return ""
	}
	return "https://" + sub
}

// performBoxSync runs a git-sync for a box and records the result, returning the
// updated row, success, and token-redacted output. Shared by the box-id sync
// endpoint and the issue deploy-qa endpoint.
func (h *Handler) performBoxSync(ctx context.Context, box db.ConnectedBox, branch, keyPath string) (db.ConnectedBox, bool, string) {
	// Serialize git-sync per box so concurrent fetch+checkout into the box's one
	// served work_dir can't interleave (one session's fetch updating FETCH_HEAD
	// while another checks it out → a half-checked-out tree). The lock is
	// BLOCKING (audit P1): the old try-lock returned ok=true on contention and
	// skipped the sync, which was only sound when both callers wanted the SAME
	// branch — when a feature-branch deploy raced a sprint-branch regression on
	// the same box, the loser reported success while the box served the OTHER
	// branch, and QA recorded a verdict against the wrong code. Blocking waits
	// for the in-flight sync, then this sync checks out the branch IT was asked
	// for — both callers end verified on their own request (last write wins on
	// the box, but neither is lied to). Skip only when the box already serves
	// this exact branch after the wait. Best-effort on lock-infra errors.
	if lockTx, err := h.TxStarter.Begin(ctx); err == nil {
		defer func() { _ = lockTx.Rollback(ctx) }()
		if _, qerr := lockTx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtext($1))`,
			"connected_box:"+uuidToString(box.ID)); qerr == nil {
			// We now hold the lock. Re-read the box: if the winner just synced
			// the SAME branch we want, the redundant fetch+checkout can be
			// skipped — but never for a different branch.
			if cur, rerr := h.Queries.GetConnectedBox(ctx, db.GetConnectedBoxParams{
				ID: box.ID, WorkspaceID: box.WorkspaceID,
			}); rerr == nil && cur.Status == "online" &&
				strings.TrimSpace(cur.LastBranch) == branch && branch != "" &&
				cur.LastBootstrapAt.Valid && time.Since(cur.LastBootstrapAt.Time) < 2*time.Minute {
				return cur, true, "(box just synced this same branch; skipped redundant sync)"
			}
		}
	}
	out, syncErr := syncBoxBranch(ctx, box, branch, remoteBoxesGitToken(), keyPath, sshRunner{})
	status := "online"
	lastErr := ""
	if syncErr != nil {
		status = "error"
		lastErr = redactGitToken(syncErr.Error())
	}
	updated, uerr := h.Queries.UpdateConnectedBoxSync(ctx, db.UpdateConnectedBoxSyncParams{
		ID:          box.ID,
		WorkspaceID: box.WorkspaceID,
		Status:      status,
		LastError:   lastErr,
		LastBranch:  pgtype.Text{String: branch, Valid: true},
	})
	if uerr != nil {
		updated = box
	}
	return updated, syncErr == nil, redactGitToken(out)
}

type SyncConnectedBoxRequest struct {
	Branch string `json:"branch"`
}

type BindConnectedBoxRequest struct {
	ProjectID string `json:"project_id"`
	// OwnerID (a MEMBER id) maps the box to its developer for Labs per-dev QA
	// routing. Pointer semantics: absent = leave the owner untouched; present
	// but "" = clear the mapping.
	OwnerID *string `json:"owner_id,omitempty"`
}

// BindConnectedBox binds (or, with an empty project_id, unbinds) a box to a
// project so an issue in that project resolves to this box for deploy-qa.
func (h *Handler) BindConnectedBox(w http.ResponseWriter, r *http.Request) {
	if !remoteBoxesEnabled() {
		writeError(w, http.StatusNotFound, "remote boxes are not enabled")
		return
	}
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	wsID := h.resolveWorkspaceID(r)
	if wsID == "" {
		writeError(w, http.StatusBadRequest, "workspace required")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace_id")
	if !ok {
		return
	}
	boxUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "box id")
	if !ok {
		return
	}
	var req BindConnectedBoxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Owner mapping (Labs per-dev QA routing): "owner_id" present in the body
	// binds/clears the box's developer. Explicit presence check so binding a
	// project alone never clears an existing owner.
	if req.OwnerID != nil {
		var ownerID pgtype.UUID
		if oid := strings.TrimSpace(*req.OwnerID); oid != "" {
			memberUUID, mok := parseUUIDOrBadRequest(w, oid, "owner_id")
			if !mok {
				return
			}
			// The value is a MEMBER id from the members list; a box owner is a
			// USER id (what agent.owner_id and session identity carry).
			member, merr := h.Queries.GetMember(r.Context(), memberUUID)
			if merr != nil || member.WorkspaceID.Bytes != wsUUID.Bytes {
				writeError(w, http.StatusBadRequest, "owner_id does not name a member of this workspace")
				return
			}
			ownerID = member.UserID
		}
		if _, oerr := h.Queries.BindConnectedBoxOwner(r.Context(), db.BindConnectedBoxOwnerParams{
			ID: boxUUID, WorkspaceID: wsUUID, OwnerID: ownerID,
		}); oerr != nil {
			writeError(w, http.StatusNotFound, "remote box not found")
			return
		}
	}
	var projectID pgtype.UUID
	if pid := strings.TrimSpace(req.ProjectID); pid != "" {
		if projectID, ok = parseUUIDOrBadRequest(w, pid, "project_id"); !ok {
			return
		}
	}
	box, err := h.Queries.BindConnectedBoxProject(r.Context(), db.BindConnectedBoxProjectParams{
		ID:          boxUUID,
		WorkspaceID: wsUUID,
		ProjectID:   projectID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "remote box not found")
		return
	}
	writeJSON(w, http.StatusOK, connectedBoxToResponse(box))
}

// DeployIssueQA resolves the QA box bound to an issue's project and checks the
// given branch out onto it (git-sync), so the box serves the branch under
// review and run_qa (with the project's qa_smoke_url pointed at the box) can
// test it. The box is auto-resolved from the issue — the caller need only supply
// the branch.
func (h *Handler) DeployIssueQA(w http.ResponseWriter, r *http.Request) {
	if !remoteBoxesEnabled() {
		writeError(w, http.StatusNotFound, "remote boxes are not enabled")
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	_ = userID
	var req SyncConnectedBoxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	branch := strings.TrimSpace(req.Branch)
	if branch == "" {
		writeError(w, http.StatusBadRequest, "branch is required")
		return
	}
	box, found := h.connectedBoxForIssue(r.Context(), issue)
	if !found {
		writeError(w, http.StatusNotFound, "no QA box is bound to this issue's project (no connected_box repo matches the project repo)")
		return
	}
	if strings.TrimSpace(box.RepoUrl) == "" || strings.TrimSpace(box.WorkDir) == "" {
		writeError(w, http.StatusBadRequest, "the resolved box has no repo_url / work_dir configured")
		return
	}
	keyPath := remoteBoxesSSHKeyPath()
	if keyPath == "" {
		writeError(w, http.StatusServiceUnavailable, "remote box SSH key is not configured on the server")
		return
	}
	updated, okSync, output := h.performBoxSync(r.Context(), box, branch, keyPath)
	h.recordDeployEvent(r.Context(), issue.WorkspaceID, issue.ID, branch, box.Label, okSync, output)
	code := http.StatusOK
	if !okSync {
		code = http.StatusBadGateway
	}
	writeJSON(w, code, map[string]any{
		"box":    connectedBoxToResponse(updated),
		"branch": branch,
		"ok":     okSync,
		"output": output,
	})
}

// recordDeployEvent persists a deploy_event row for a Tier-1 (QA-box git-sync)
// deploy — the durable, append-only signal the SDLC stepper's Deploy stage
// reads (GetLatestDeployEventForIssue) instead of the previous client-side
// derivation off connected_box.last_branch (deploy-stage-research.md P0).
// Best-effort: a write failure here must never fail the deploy response the
// caller already computed — deploy_event is a read-side convenience, not a
// consistency boundary the sync itself depends on.
func (h *Handler) recordDeployEvent(ctx context.Context, workspaceID, issueID pgtype.UUID, ref, target string, ok bool, output string) {
	status := "success"
	if !ok {
		status = "failed"
	}
	summary := strings.TrimSpace(output)
	if len(summary) > 500 {
		summary = summary[:500]
	}
	if _, err := h.Queries.InsertDeployEvent(ctx, db.InsertDeployEventParams{
		WorkspaceID: workspaceID,
		IssueID:     issueID,
		Ref:         ref,
		Target:      target,
		Status:      status,
		Summary:     summary,
	}); err != nil {
		slog.Warn("record deploy event failed", "error", err, "issue_id", uuidToString(issueID))
	}
}

// sprintBranchName is the FALLBACK git branch convention for a sprint
// (`sprint/<sprintId>`) when no explicit branch is set. Single source of truth
// for the convention.
func sprintBranchName(sprintID pgtype.UUID) string {
	return "sprint/" + uuidToString(sprintID)
}

// SprintBranchFor returns the real git branch a sprint's work lives on: the
// branch the team set on the sprint (e.g. "billing" or "sprint-9"), or the
// sprint/<id> convention when unset. The QA tiers (per-task / daily / sprint-end)
// + deploy all resolve the branch through this so they agree.
func SprintBranchFor(sprint db.Sprint) string {
	if b := strings.TrimSpace(sprint.Branch); b != "" {
		return b
	}
	return sprintBranchName(sprint.ID)
}

// DeploySprintBranch resolves a sprint's project → its EXPLICITLY bound QA box
// and git-syncs the sprint branch (`sprint/<sprintId>`) onto that box, so the
// box serves the whole sprint's accumulated change for the sprint-end
// regression. Unlike connectedBoxForIssue this does ONLY the explicit
// project_id match — a sprint's project binding is authoritative, no repo-name
// fallback. Callable from the autopilot sprint-end dispatch (no http.Request),
// which is why it is exported. Returns the updated box, whether the sync
// succeeded, and any resolution error (a resolution failure is distinct from a
// sync that ran but failed).
func (h *Handler) DeploySprintBranch(ctx context.Context, sprintID, wsID pgtype.UUID) (db.ConnectedBox, bool, error) {
	sprint, err := h.Queries.GetSprint(ctx, db.GetSprintParams{
		ID:          sprintID,
		WorkspaceID: wsID,
	})
	if err != nil {
		return db.ConnectedBox{}, false, fmt.Errorf("sprint not found: %w", err)
	}
	if !sprint.ProjectID.Valid {
		return db.ConnectedBox{}, false, fmt.Errorf("sprint has no project")
	}

	boxes, err := h.Queries.ListConnectedBoxesByWorkspace(ctx, wsID)
	if err != nil {
		return db.ConnectedBox{}, false, fmt.Errorf("list connected boxes: %w", err)
	}
	var box db.ConnectedBox
	found := false
	// Prefer the OWNERLESS (shared) project box: a sprint branch deploy onto a
	// developer's personal box would clobber whatever they're testing.
	for _, b := range boxes {
		if !b.OwnerID.Valid && b.ProjectID.Valid && b.ProjectID.Bytes == sprint.ProjectID.Bytes {
			box, found = b, true
			break
		}
	}
	if !found {
		for _, b := range boxes {
			if b.ProjectID.Valid && b.ProjectID.Bytes == sprint.ProjectID.Bytes {
				box, found = b, true
				break
			}
		}
	}
	if !found {
		return db.ConnectedBox{}, false, fmt.Errorf("no QA box is bound to the sprint's project")
	}
	if strings.TrimSpace(box.RepoUrl) == "" || strings.TrimSpace(box.WorkDir) == "" {
		return db.ConnectedBox{}, false, fmt.Errorf("the resolved box has no repo_url / work_dir configured")
	}
	keyPath := remoteBoxesSSHKeyPath()
	if keyPath == "" {
		return db.ConnectedBox{}, false, fmt.Errorf("remote box SSH key is not configured on the server")
	}

	// No deploy_event write here (deploy P0 scope): this syncs the WHOLE sprint
	// branch onto a shared box with no single issue in hand — the issue set a
	// sprint covers is a separate lookup (issue_to_sprint) this function
	// doesn't do today. Writing one deploy_event per covered issue belongs in
	// P1 alongside that lookup; see docs/deploy-stage-research.md P0 row.
	updated, okSync, _ := h.performBoxSync(ctx, box, SprintBranchFor(sprint), keyPath)
	return updated, okSync, nil
}

// sprintRegressionPayload is the trigger payload a sprint regression run's
// agent receives — the QA directive: a whole-branch regression of the sprint
// branch against sprint-root. Same shape whether the scheduler or a human
// fired it; only AutopilotRun.Source differs ("schedule" vs "manual").
type sprintRegressionPayload struct {
	Scope    string `json:"scope"`
	Branch   string `json:"branch"`
	Baseline string `json:"baseline"`
	SprintID string `json:"sprint_id"`
	// Tasks the sprint explicitly covers (issue_to_sprint members). The agent
	// scopes regression to these — running each task's promoted test cases plus
	// the project base suite — instead of inferring scope from the branch diff
	// alone. Empty = fall back to whole-branch regression.
	Tasks []sprintRegressionTask `json:"tasks,omitempty"`
	// Directive carries the scope-keyed baseline guidance (the same text issue
	// slices get) so the whole-branch fallback isn't a bare JSON blob the agent
	// must interpret unaided (audit P1: the rich contract never reached the
	// autopilot run).
	Directive string `json:"directive,omitempty"`
	// QATarget is the deployed app the regression drives (the project's bound
	// QA box, else its qa_smoke_url). Without it the agent has no address for
	// the browser-level suite and silently degrades to code-only checks.
	QATarget string `json:"qa_target,omitempty"`
	// RepoURL names the project's primary repo so an issue-less run (no task
	// worktree, no issue→project resource injection) still knows which code the
	// sprint branch lives in.
	RepoURL string `json:"repo_url,omitempty"`
	// ResultsIssue is the issue KEY the agent posts its ```test-runs``` block
	// on — CaptureTestRuns needs an issue in the cases' project to accept the
	// rows. The project's base-suite tracking issue when it still exists, else
	// the sprint's first attached task.
	ResultsIssue string `json:"results_issue,omitempty"`
	// Cases is the project's standing base suite (compiled automated cases).
	// Embedded verbatim because an issue-less autopilot run gets none of the
	// run_test_cases slice injection — without these the whole-branch
	// regression ran zero scripted cases (found live 2026-07-07).
	Cases []sprintRegressionCase `json:"cases,omitempty"`
}

type sprintRegressionTask struct {
	Key   string `json:"key"`
	Title string `json:"title"`
}

type sprintRegressionCase struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Script string `json:"script,omitempty"`
}

// DispatchSprintRegression deploys the sprint branch to its project's bound QA
// box, then dispatches the project's sprint-end (run-only) autopilot with a
// scope=regression / baseline=sprint-root payload — a whole-branch regression,
// distinct from the per-task scope=task QA that fires on each issue's
// in_review transition. Shared by the sprint-end scheduler (source="schedule",
// cmd/server/sprint_end_scheduler.go) and a human-triggered re-run from the QA
// review page (source="manual", RunIssueSprintRegression below) — same
// deploy+dispatch logic, different provenance on the resulting run. Best-effort
// on the deploy (a sync failure is logged, not fatal: the regression still
// runs, just possibly against a stale box) but returns an error when no
// run-only autopilot is bound to the project — the caller can't dispatch
// nothing, unlike the scheduler's tick which just skips and logs.
func (h *Handler) DispatchSprintRegression(ctx context.Context, sprintID, wsID pgtype.UUID, source string) (db.AutopilotRun, error) {
	sprint, err := h.Queries.GetSprint(ctx, db.GetSprintParams{ID: sprintID, WorkspaceID: wsID})
	if err != nil {
		return db.AutopilotRun{}, fmt.Errorf("sprint not found: %w", err)
	}
	branch := SprintBranchFor(sprint)

	if _, ok, derr := h.DeploySprintBranch(ctx, sprint.ID, sprint.WorkspaceID); derr != nil {
		slog.Warn("sprint regression: deploy sprint branch failed", "sprint_id", uuidToString(sprint.ID), "error", derr)
	} else if !ok {
		slog.Warn("sprint regression: sprint branch sync reported failure", "sprint_id", uuidToString(sprint.ID), "branch", branch)
	}

	autopilots, err := h.Queries.ListActiveRunOnlyAutopilotsForProject(ctx, db.ListActiveRunOnlyAutopilotsForProjectParams{
		WorkspaceID: sprint.WorkspaceID,
		ProjectID:   sprint.ProjectID,
	})
	if err != nil {
		return db.AutopilotRun{}, fmt.Errorf("list sprint-end autopilots: %w", err)
	}
	if len(autopilots) == 0 {
		return db.AutopilotRun{}, fmt.Errorf("no sprint-end (run-only) autopilot is bound to this sprint's project")
	}

	// Scope the regression to the sprint's attached tasks (issue_to_sprint) so a
	// user curating the sprint in the QA cockpit controls what gets tested.
	// Best-effort: on lookup failure fall back to whole-branch regression.
	var tasks []sprintRegressionTask
	if issues, ierr := h.Queries.ListIssuesBySprint(ctx, db.ListIssuesBySprintParams{SprintID: sprint.ID}); ierr == nil {
		prefix := h.getIssuePrefix(ctx, sprint.WorkspaceID)
		for _, iss := range issues {
			tasks = append(tasks, sprintRegressionTask{
				Key:   fmt.Sprintf("%s-%d", prefix, iss.Number),
				Title: iss.Title,
			})
		}
	}

	// The deployed target + primary repo + base suite ride in the payload:
	// an issue-less run gets none of the per-issue slice injection, so this
	// payload IS the whole QA contract for the run.
	var qaTarget, repoURL, resultsIssue string
	var cases []sprintRegressionCase
	if project, perr := h.Queries.GetProject(ctx, sprint.ProjectID); perr == nil {
		if boxes, berr := h.Queries.ListConnectedBoxesByWorkspace(ctx, sprint.WorkspaceID); berr == nil {
			// Same shared-box preference as DeploySprintBranch: regression
			// drives the project's shared environment, not a developer's own.
			for _, b := range boxes {
				if !b.OwnerID.Valid && b.ProjectID.Valid && b.ProjectID.Bytes == sprint.ProjectID.Bytes {
					qaTarget = boxSmokeURL(b)
					break
				}
			}
			if qaTarget == "" {
				for _, b := range boxes {
					if b.ProjectID.Valid && b.ProjectID.Bytes == sprint.ProjectID.Bytes {
						qaTarget = boxSmokeURL(b)
						break
					}
				}
			}
		}
		var ps struct {
			QASmokeURL string `json:"qa_smoke_url"`
			BaseSuite  string `json:"base_suite_issue_id"`
		}
		if len(project.Settings) > 0 {
			_ = json.Unmarshal(project.Settings, &ps)
		}
		if qaTarget == "" {
			qaTarget = strings.TrimSpace(ps.QASmokeURL)
		}
		if strings.TrimSpace(ps.BaseSuite) != "" {
			if bid, berr := parseUUIDErr(strings.TrimSpace(ps.BaseSuite)); berr == nil {
				if bi, ierr := h.Queries.GetIssue(ctx, bid); ierr == nil {
					resultsIssue = fmt.Sprintf("%s-%d", h.getIssuePrefix(ctx, sprint.WorkspaceID), bi.Number)
				}
			}
		}
	}
	for _, r := range h.listProjectResourcesForProject(ctx, sprint.ProjectID) {
		if r.ResourceType != "github_repo" {
			continue
		}
		var ref struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(r.ResourceRef, &ref) == nil && strings.TrimSpace(ref.URL) != "" {
			repoURL = strings.TrimSpace(ref.URL)
			break
		}
	}
	if baseCases, cerr := h.Queries.ListAutomatedTestCasesForProject(ctx, db.ListAutomatedTestCasesForProjectParams{
		ProjectID: sprint.ProjectID, WorkspaceID: sprint.WorkspaceID,
	}); cerr == nil {
		for _, c := range baseCases {
			cases = append(cases, sprintRegressionCase{
				ID: uuidToString(c.ID), Title: c.Title, Script: c.Script,
			})
		}
	}
	if resultsIssue == "" && len(tasks) > 0 {
		resultsIssue = tasks[0].Key
	}

	payload, err := json.Marshal(sprintRegressionPayload{
		Scope: "regression", Branch: branch, Baseline: "sprint-root", SprintID: uuidToString(sprint.ID),
		Tasks:        tasks,
		Directive:    strings.TrimSpace(qaBaselineGuidanceFor("regression")),
		QATarget:     qaTarget,
		RepoURL:      repoURL,
		ResultsIssue: resultsIssue,
		Cases:        cases,
	})
	if err != nil {
		return db.AutopilotRun{}, fmt.Errorf("marshal payload: %w", err)
	}

	// Pick the QA-regression autopilot, not just autopilots[0]. A project may
	// have several run-only autopilots (e.g. a "Weekly docs sweep" alongside a
	// regression one); dispatching a whole-branch QA regression to a docs
	// autopilot runs the wrong agent. Prefer a regression/QA-titled autopilot,
	// then any non-docs one, and only fall back to [0] when that's all there is.
	ap := pickRegressionAutopilot(autopilots)
	run, err := h.AutopilotService.DispatchAutopilot(ctx, ap, pgtype.UUID{}, source, payload)
	if err != nil {
		return db.AutopilotRun{}, fmt.Errorf("dispatch regression: %w", err)
	}
	return *run, nil
}

// pickRegressionAutopilot chooses the best run-only autopilot to carry a sprint
// regression: a title signalling regression/QA wins; otherwise the first one
// whose title doesn't look like a docs job; otherwise the first (preserving the
// original single-autopilot behavior). Purely title-based — autopilots carry no
// explicit purpose field — so name a project's regression autopilot with "QA"
// or "regression" for it to be chosen over siblings.
func pickRegressionAutopilot(aps []db.Autopilot) db.Autopilot {
	for _, ap := range aps {
		t := strings.ToLower(ap.Title)
		if strings.Contains(t, "regression") || strings.Contains(t, "qa") {
			return ap
		}
	}
	for _, ap := range aps {
		if !strings.Contains(strings.ToLower(ap.Title), "docs") {
			return ap
		}
	}
	return aps[0]
}

// RunIssueSprintRegression lets a human fire the SAME whole-branch regression
// the sprint-end scheduler runs automatically, from wherever they're already
// looking at one of the sprint's issues (the QA review page) — without
// requiring them to know the sprint id or navigate to a separate sprint admin
// surface. Resolves the issue's sprint via GetSprintForIssue; 404 when the
// issue isn't on a sprint (the regression concept doesn't apply) or no
// sprint-end autopilot is configured. POST /api/issues/{id}/run-regression.
func (h *Handler) RunIssueSprintRegression(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	sprint, err := h.Queries.GetSprintForIssue(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "this issue is not part of a sprint")
		return
	}

	run, err := h.DispatchSprintRegression(r.Context(), sprint.ID, issue.WorkspaceID, "manual")
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, runToResponse(run))
}

// DeploySprintQA is the sprint-level counterpart to DeployIssueQA: it resolves
// the sprint's project-bound QA box and git-syncs the sprint branch onto it.
// POST /api/sprints/{id}/deploy-qa. The box is auto-resolved from the sprint's
// project — the caller supplies nothing but the sprint id in the path.
func (h *Handler) DeploySprintQA(w http.ResponseWriter, r *http.Request) {
	if !remoteBoxesEnabled() {
		writeError(w, http.StatusNotFound, "remote boxes are not enabled")
		return
	}
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	wsID := h.resolveWorkspaceID(r)
	if wsID == "" {
		writeError(w, http.StatusBadRequest, "workspace required")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace_id")
	if !ok {
		return
	}
	sprintUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "sprint id")
	if !ok {
		return
	}

	updated, okSync, err := h.DeploySprintBranch(r.Context(), sprintUUID, wsUUID)
	if err != nil {
		// Resolution failures (sprint/box/config missing) are a 404 — there is
		// nothing to sync, mirroring DeployIssueQA's "no box bound" path.
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	code := http.StatusOK
	if !okSync {
		code = http.StatusBadGateway
	}
	writeJSON(w, code, map[string]any{
		"box":    connectedBoxToResponse(updated),
		"branch": sprintBranchName(sprintUUID),
		"ok":     okSync,
	})
}

// SyncConnectedBox checks out a branch of the box's repo into its work_dir over
// SSH (git-sync), so the box serves that branch and QA can test it. The SSH key
// + git token come from operator config (env); per-box secret storage is later.
func (h *Handler) SyncConnectedBox(w http.ResponseWriter, r *http.Request) {
	if !remoteBoxesEnabled() {
		writeError(w, http.StatusNotFound, "remote boxes are not enabled")
		return
	}
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	wsID := h.resolveWorkspaceID(r)
	if wsID == "" {
		writeError(w, http.StatusBadRequest, "workspace required")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace_id")
	if !ok {
		return
	}
	boxUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "box id")
	if !ok {
		return
	}
	var req SyncConnectedBoxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	branch := strings.TrimSpace(req.Branch)
	if branch == "" {
		writeError(w, http.StatusBadRequest, "branch is required")
		return
	}
	box, err := h.Queries.GetConnectedBox(r.Context(), db.GetConnectedBoxParams{
		ID:          boxUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "remote box not found")
		return
	}
	if strings.TrimSpace(box.RepoUrl) == "" || strings.TrimSpace(box.WorkDir) == "" {
		writeError(w, http.StatusBadRequest, "box has no repo_url / work_dir configured")
		return
	}
	keyPath := remoteBoxesSSHKeyPath()
	if keyPath == "" {
		writeError(w, http.StatusServiceUnavailable, "remote box SSH key is not configured on the server")
		return
	}

	// The box fetches + checks out the branch (glue-preserving); the token lives
	// only in the fetch argv, redacted before it is logged or stored.
	updated, okSync, output := h.performBoxSync(r.Context(), box, branch, keyPath)
	code := http.StatusOK
	if !okSync {
		code = http.StatusBadGateway
	}
	writeJSON(w, code, map[string]any{
		"box":    connectedBoxToResponse(updated),
		"branch": branch,
		"ok":     okSync,
		"output": output,
	})
}

// resolveQAPreviewURL is the same resolution devBoxSmokeURL feeds into the
// run_qa instruction, just returned as a plain URL instead of prose: the
// issue's resolved connected box (per-developer, else project-bound), else
// the project's configured qa_smoke_url. "" when neither resolves — e.g. an
// Agora-self-repo issue with only a per-task daemon worktree and no deployed
// box, which is what GetIssueEditor's CDP-driven browser covers instead.
func (h *Handler) resolveQAPreviewURL(ctx context.Context, issue db.Issue) string {
	// A concrete declared dev_apps URL (the dev's own running app) wins.
	if url := h.devLocalAppURL(ctx, issue); url != "" {
		return url
	}
	// "Local config enabled": the project runs in a local_directory on an
	// online daemon, so the QA app lives on THAT daemon (its worktree /
	// in-place checkout), reached over the daemon's localhost — not a deployed
	// sdteam box. Return "" so the Live pane drives the local daemon preview
	// (agentBrowse against the worktree work_dir) instead of embedding a box.
	if _, _, ok := h.localDirectoryQATarget(ctx, issue); ok {
		return ""
	}
	// Otherwise fall back to the deployed QA target (connected box, e.g.
	// agora.sdteam.uz), then the project's static qa_smoke_url.
	if url := h.devBoxSmokeURL(ctx, issue); url != "" {
		return url
	}
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
// (e.g. a connected box like agora.sdteam.uz) DIRECTLY — no daemon, no CDP
// screencast, no per-task worktree required. This is the fallback for
// workspaces whose QA target is a standing deployed environment rather than
// an Agora-managed per-issue worktree (a PHP monolith QA'd by deploying a
// branch to a box, not by an agent driving its own Chromium). Always 200;
// "url": "" means nothing resolves and the frontend shows its own empty state.
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
// every QA review page load for the same project resolves the same box URL,
// so without caching each one would pay the full outbound HEAD round trip.
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
// "*" — a real bug caught during live testing against sd-main's box: its
// CSP is `frame-ancestors 'self' https://web.telegram.org
// https://*.telegram.org`, a SCOPED subdomain wildcard inside one source
// value, not the CSP special token `*` (any origin). A naive
// strings.Contains(directive, "*") matches that substring and wrongly
// concludes the policy is wide open — exactly backwards, since this policy
// explicitly does NOT permit Agora's origin. Only an exact `*` TOKEN in the
// source list means "any origin may frame this."
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
