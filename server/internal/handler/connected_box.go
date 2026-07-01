package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
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
	return strings.TrimSpace(os.Getenv("AGORA_REMOTE_BOXES_ENABLED")) == "true"
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
	// 0. Per-developer box (highest priority): the developer behind this issue's
	//    work gets their OWN box (owner_id match), so their branch deploys to their
	//    own isolated environment instead of colliding with other devs on the
	//    shared project box. Only the per-task deploy-qa path uses this resolver;
	//    the sprint-end regression keeps its own project-only box resolution.
	if devUser, ok := h.developerUserForIssue(ctx, issue); ok {
		for _, b := range boxes {
			if b.OwnerID.Valid && b.OwnerID.Bytes == devUser.Bytes {
				return b, true
			}
		}
	}
	// 1. Explicit project binding.
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
	return db.ConnectedBox{}, false
}

// devBoxSmokeURL returns the https URL the issue's resolved QA box serves
// (https://<subdomain>, derived from the box's work_dir /var/www/<subdomain>),
// so run_qa can smoke the ASSIGNEE DEVELOPER'S own box instead of a single
// project-wide URL. "" when remote boxes are off, no box resolves, or the box
// has no work_dir — the run_qa smoke then falls back to the project qa_smoke_url.
func (h *Handler) devBoxSmokeURL(ctx context.Context, issue db.Issue) string {
	if !remoteBoxesEnabled() {
		return ""
	}
	box, ok := h.connectedBoxForIssue(ctx, issue)
	if !ok {
		return ""
	}
	return boxSmokeURL(box)
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
	// while another checks it out → a half-checked-out tree). Use a NON-BLOCKING
	// box-scoped advisory lock: if another sync of this box is already in flight,
	// SKIP the redundant fetch+checkout rather than block (and hold a pooled
	// connection) — in the shared-sprint-branch model every QA task syncs the SAME
	// branch, so the in-flight sync already converges the box on the right tip. The
	// lock is held (tx open) across the SSH sync and released on tx rollback at
	// return. Best-effort: a lock-infra error proceeds unlocked rather than fail.
	if lockTx, err := h.TxStarter.Begin(ctx); err == nil {
		defer func() { _ = lockTx.Rollback(ctx) }()
		var got bool
		if qerr := lockTx.QueryRow(ctx,
			`SELECT pg_try_advisory_xact_lock(hashtext($1))`,
			"connected_box:"+uuidToString(box.ID)).Scan(&got); qerr == nil && !got {
			return box, true, "(box sync already in progress for the same branch; skipped redundant sync)"
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
	for _, b := range boxes {
		if b.ProjectID.Valid && b.ProjectID.Bytes == sprint.ProjectID.Bytes {
			box = b
			found = true
			break
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

	payload, err := json.Marshal(sprintRegressionPayload{
		Scope: "regression", Branch: branch, Baseline: "sprint-root", SprintID: uuidToString(sprint.ID),
	})
	if err != nil {
		return db.AutopilotRun{}, fmt.Errorf("marshal payload: %w", err)
	}

	ap := autopilots[0]
	run, err := h.AutopilotService.DispatchAutopilot(ctx, ap, pgtype.UUID{}, source, payload)
	if err != nil {
		return db.AutopilotRun{}, fmt.Errorf("dispatch regression: %w", err)
	}
	return *run, nil
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
	writeJSON(w, http.StatusOK, map[string]string{"url": h.resolveQAPreviewURL(r.Context(), issue)})
}
