package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/zohoprojects"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Zoho Projects → Agora WORKSPACE migration. Where /api/zoho-projects/import
// files a Zoho project as an Agora project inside the caller's workspace, this
// moves a whole portal the other way round: every active Zoho project becomes
// its own Agora workspace, its people become that workspace's members, and its
// tasks become issues through the same idempotent importer (syncZohoProject).
//
// POST /api/zoho-projects/migrate-workspaces
//
//	{"dry_run": true, "allowed_email_domains": ["tsst.ai", "octanefuel.com"]}
//
// dry_run returns the full plan (workspaces, people + roles, task counts) and
// writes nothing. A real run creates/reuses the workspaces and members inline,
// answers 202 with the same plan, and imports the tasks in the background.
// Re-running is safe: a workspace is found again by settings.zoho_project_id,
// people by email, issues by the zoho_task_id marker.
//
// Membership: with the ZohoProjects.users.READ scope the project's own member
// list (and each member's Zoho role) is used; without it the migration falls
// back to the people it can see — the project owner plus every task owner and
// creator — all as members. Roles map through zohoprojects.MapRole (admin /
// manager → admin, everyone else → member). The Zoho project owner and the
// operator running the migration are the workspace's owners — the only role
// that sees every issue.
//
// Gated by AGORA_ZOHO_MIGRATE (default off): it creates users and workspaces
// instance-wide from another system's data, so it is switched on for the
// migration window only.

// zohoWorkspaceProjectKey links a migrated workspace to its Zoho project id
// (workspace.settings), the dedup key for re-runs.
const zohoWorkspaceProjectKey = "zoho_project_id"

// zohoWorkspaceAliasesKey stores the migration's email aliases on the
// workspace, for the same reason.
const zohoWorkspaceAliasesKey = "zoho_migrate_aliases"

// zohoWorkspaceDomainsKey stores the migration's allowed email domains on the
// workspace so the poller provisions late-arriving people under the same rule.
const zohoWorkspaceDomainsKey = "zoho_migrate_domains"

// zohoMigrateMu serializes the workspace/member apply step: two concurrent runs
// would both miss findZohoWorkspace and create the same workspace twice.
var zohoMigrateMu sync.Mutex

func zohoMigrateEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AGORA_ZOHO_MIGRATE"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// freemailDomains are never provisioned unless explicitly allowed: a migrated
// workspace is a company space and its accounts must be company addresses.
var freemailDomains = map[string]bool{
	"gmail.com": true, "googlemail.com": true, "yahoo.com": true, "outlook.com": true,
	"hotmail.com": true, "live.com": true, "icloud.com": true, "mail.ru": true,
	"yandex.ru": true, "yandex.com": true, "proton.me": true, "protonmail.com": true,
}

// zohoEmailAllowed applies the migration's email policy: freemail is refused
// unless listed, and a non-empty allowlist restricts to exactly those domains.
// The returned reason is empty when allowed.
func zohoEmailAllowed(email string, domains []string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return "invalid email"
	}
	domain := email[at+1:]
	for _, d := range domains {
		if strings.EqualFold(strings.TrimSpace(d), domain) {
			return ""
		}
	}
	if len(domains) > 0 {
		return "domain not allowed"
	}
	if freemailDomains[domain] {
		return "personal email domain"
	}
	return ""
}

// zohoProjectIsActive reports whether a Zoho project is part of the migration:
// not archived, and its custom status is not a cancelled/closed one.
func zohoProjectIsActive(p zohoprojects.Project) bool {
	if s := strings.ToLower(strings.TrimSpace(p.Status)); s != "" && s != "active" {
		return false
	}
	cs := strings.ToLower(strings.TrimSpace(p.CustomStatus))
	for _, bad := range []string{"cancel", "closed", "complete", "archiv", "on hold"} {
		if strings.Contains(cs, bad) {
			return false
		}
	}
	return true
}

var slugInvalidRe = regexp.MustCompile(`[^a-z0-9]+`)

// zohoWorkspaceSlug derives a workspace slug from a project name
// ("DWH (Analytics Department)" → "dwh-analytics-department").
func zohoWorkspaceSlug(name string) string {
	s := slugInvalidRe.ReplaceAllString(strings.ToLower(name), "-")
	s = strings.Trim(s, "-")
	if len(s) > 48 {
		s = strings.TrimRight(s[:48], "-")
	}
	if s == "" {
		s = "zoho-project"
	}
	return s
}

// roleRank orders workspace roles so a re-run only ever raises a role.
func roleRank(role string) int {
	switch role {
	case "owner":
		return 3
	case "admin":
		return 2
	case "member":
		return 1
	default:
		return 0
	}
}

// --- request / response -----------------------------------------------------

type ZohoMigrateRequest struct {
	DryRun bool `json:"dry_run"`
	// ProjectIDs narrows the run to these Zoho project ids (still only active
	// ones). Empty = every active project in the portal.
	ProjectIDs []string `json:"project_ids"`
	// AllowedEmailDomains restricts provisioning to company domains. Empty =
	// any domain except personal mail (gmail.com, …).
	AllowedEmailDomains []string `json:"allowed_email_domains"`
	// EmailAliases folds a person's second address into their main one
	// ({"jamshidjon.t@octanefuel.com": "jamshidjon.t1@tsst.ai"}): the alias gets
	// no account of its own and its tasks are assigned to the main account.
	EmailAliases map[string]string `json:"email_aliases"`
}

// zohoMigratePolicy is who may be provisioned and under which address.
type zohoMigratePolicy struct {
	Domains []string          `json:"domains"`
	Aliases map[string]string `json:"aliases"`
}

func newZohoMigratePolicy(domains []string, aliases map[string]string) zohoMigratePolicy {
	p := zohoMigratePolicy{Domains: []string{}, Aliases: map[string]string{}}
	for _, d := range domains {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			p.Domains = append(p.Domains, d)
		}
	}
	for from, to := range aliases {
		from = strings.ToLower(strings.TrimSpace(from))
		to = strings.ToLower(strings.TrimSpace(to))
		if from != "" && to != "" && from != to {
			p.Aliases[from] = to
		}
	}
	return p
}

// canonical returns the address a person is provisioned under.
func (p zohoMigratePolicy) canonical(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	if to, ok := p.Aliases[email]; ok {
		return to
	}
	return email
}

type ZohoMigratePerson struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Source   string `json:"source"` // project_owner | project_user | task_owner | task_creator | operator
	ZohoRole string `json:"zoho_role,omitempty"`
	Exists   bool   `json:"exists"` // an Agora account with this email already exists
	Skipped  string `json:"skipped,omitempty"`
}

type ZohoMigrateProject struct {
	ZohoProjectID string              `json:"zoho_project_id"`
	Key           string              `json:"key"`
	Name          string              `json:"name"`
	Status        string              `json:"status"`
	Action        string              `json:"action"` // create | reuse
	WorkspaceID   string              `json:"workspace_id,omitempty"`
	WorkspaceSlug string              `json:"workspace_slug"`
	IssuePrefix   string              `json:"issue_prefix"`
	Tasks         int                 `json:"tasks"` // top-level tasks; subtasks come on import
	OpenTasks     int                 `json:"open_tasks"`
	People        []ZohoMigratePerson `json:"people"`
	Error         string              `json:"error,omitempty"`

	description string
	ownerEmail  string
}

type ZohoMigrateSkip struct {
	ZohoProjectID string `json:"zoho_project_id"`
	Key           string `json:"key"`
	Name          string `json:"name"`
	Status        string `json:"status"`
	Reason        string `json:"reason"`
}

type ZohoMigrateResponse struct {
	DryRun           bool                 `json:"dry_run"`
	MembershipSource string               `json:"membership_source"`
	Projects         []ZohoMigrateProject `json:"projects"`
	Skipped          []ZohoMigrateSkip    `json:"skipped"`
	Totals           struct {
		Workspaces    int `json:"workspaces"`
		NewWorkspaces int `json:"new_workspaces"`
		Tasks         int `json:"tasks"`
		People        int `json:"people"`
		NewUsers      int `json:"new_users"`
	} `json:"totals"`
}

// --- handler ----------------------------------------------------------------

// MigrateZohoWorkspaces handles POST /api/zoho-projects/migrate-workspaces.
func (h *Handler) MigrateZohoWorkspaces(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	if !zohoMigrateEnabled() {
		writeError(w, http.StatusForbidden, "zoho workspace migration is disabled (set AGORA_ZOHO_MIGRATE=1)")
		return
	}
	if !zohoConfigured() {
		zohoUnavailable(w)
		return
	}
	var req ZohoMigrateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	operator, err := h.Queries.GetUser(r.Context(), parseUUID(userID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load caller")
		return
	}
	policy := newZohoMigratePolicy(req.AllowedEmailDomains, req.EmailAliases)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	st := h.newZohoSyncState()
	portalID, err := h.resolveZohoPortalID(ctx, st)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to resolve zoho portal: "+err.Error())
		return
	}
	resp, err := h.planZohoMigration(ctx, st, portalID, req.ProjectIDs, operator, policy)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	resp.DryRun = req.DryRun
	if req.DryRun {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Workspaces + members inline (fast, and the response can name them);
	// tasks in the background (slow, rate-limited Zoho walk). The plan above was
	// read before taking the lock, so a run that lost the race re-plans.
	if !zohoMigrateMu.TryLock() {
		writeError(w, http.StatusConflict, "a zoho workspace migration is already running")
		return
	}
	defer zohoMigrateMu.Unlock()
	type job struct {
		wsID          pgtype.UUID
		zohoProjectID string
	}
	var jobs []job
	for i := range resp.Projects {
		p := &resp.Projects[i]
		wsID, err := h.applyZohoWorkspace(ctx, p, policy)
		if err != nil {
			p.Error = err.Error()
			slog.Warn("zoho migrate: workspace apply failed", "zoho_project_id", p.ZohoProjectID, "error", err)
			continue
		}
		p.WorkspaceID = util.UUIDToString(wsID)
		jobs = append(jobs, job{wsID: wsID, zohoProjectID: p.ZohoProjectID})
	}

	go func() {
		for _, j := range jobs {
			jst := h.newZohoSyncState()
			jst.backfillComments = true
			jst.provisionMember = h.zohoMemberProvisioner(policy)
			jctx, jcancel := context.WithTimeout(context.Background(), zohoSyncTimeout)
			err := h.syncZohoProject(jctx, j.wsID, j.zohoProjectID, jst)
			jcancel()
			slog.Info("zoho migrate: project imported",
				"zoho_project_id", j.zohoProjectID, "workspace_id", util.UUIDToString(j.wsID),
				"created", jst.created, "updated", jst.updated, "skipped", jst.skipped,
				"comments_throttled", jst.commentsThrottled, "error", err)
		}
		slog.Info("zoho migrate: background import finished", "workspaces", len(jobs))
	}()

	writeJSON(w, http.StatusAccepted, resp)
}

// --- plan -------------------------------------------------------------------

// planZohoMigration reads the portal and builds the per-project plan. Read-only
// on both sides: it lists Zoho projects/tasks/users and looks Agora users and
// workspaces up, but creates nothing.
func (h *Handler) planZohoMigration(ctx context.Context, st *zohoSyncState, portalID string, onlyIDs []string, operator db.User, policy zohoMigratePolicy) (*ZohoMigrateResponse, error) {
	projects, err := st.client.ListProjects(ctx, portalID)
	if err != nil {
		return nil, fmt.Errorf("failed to list zoho projects: %w", err)
	}
	want := map[string]bool{}
	for _, id := range onlyIDs {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}

	resp := &ZohoMigrateResponse{
		Projects:         []ZohoMigrateProject{},
		Skipped:          []ZohoMigrateSkip{},
		MembershipSource: "project_users",
	}
	usersReadable := true
	slugsTaken := map[string]bool{}
	people := map[string]bool{}

	for _, zp := range projects {
		if len(want) > 0 && !want[zp.ID] {
			continue
		}
		if !zohoProjectIsActive(zp) {
			resp.Skipped = append(resp.Skipped, ZohoMigrateSkip{
				ZohoProjectID: zp.ID, Key: zp.Key, Name: zp.Name,
				Status: zp.CustomStatus, Reason: "not active",
			})
			continue
		}
		plan := ZohoMigrateProject{
			ZohoProjectID: zp.ID,
			Key:           zp.Key,
			Name:          strings.TrimSpace(zp.Name),
			Status:        zp.CustomStatus,
			Action:        "create",
			People:        []ZohoMigratePerson{},
			description:   zohoprojects.HTMLToText(zp.Description),
			ownerEmail:    policy.canonical(zp.Owner.Email),
		}

		if ws, found, err := h.findZohoWorkspace(ctx, zp.ID); err != nil {
			return nil, fmt.Errorf("lookup workspace for %s: %w", zp.Key, err)
		} else if found {
			plan.Action = "reuse"
			plan.WorkspaceID = util.UUIDToString(ws.ID)
			plan.WorkspaceSlug = ws.Slug
			plan.IssuePrefix = ws.IssuePrefix
		} else {
			slug, err := h.freeWorkspaceSlug(ctx, zohoWorkspaceSlug(plan.Name), slugsTaken)
			if err != nil {
				return nil, err
			}
			slugsTaken[slug] = true
			plan.WorkspaceSlug = slug
			plan.IssuePrefix = generateIssuePrefix(plan.Name)
		}

		// People, highest role first so a duplicate keeps the stronger role.
		seen := map[string]int{}
		add := func(u zohoprojects.User, role, source, zohoRole string) {
			if strings.TrimSpace(u.Email) == "" {
				return
			}
			email := policy.canonical(u.Email)
			if i, ok := seen[email]; ok {
				if roleRank(role) > roleRank(plan.People[i].Role) {
					plan.People[i].Role = role
				}
				return
			}
			p := ZohoMigratePerson{
				Email: email, Name: strings.TrimSpace(u.Name),
				Role: role, Source: source, ZohoRole: zohoRole,
			}
			if _, err := h.Queries.GetUserByEmail(ctx, email); err == nil {
				p.Exists = true
			}
			if email != strings.ToLower(operator.Email) {
				p.Skipped = zohoEmailAllowed(email, policy.Domains)
			}
			seen[email] = len(plan.People)
			plan.People = append(plan.People, p)
		}

		add(zp.Owner, "owner", "project_owner", "")
		// The operator co-owns every migrated workspace: only owners see all
		// issues (issueVisibilityRestriction), and they run the migration.
		add(zohoprojects.User{Email: operator.Email, Name: operator.Name}, "owner", "operator", "")

		if usersReadable {
			users, err := st.client.ListProjectUsers(ctx, portalID, zp.ID)
			if err != nil {
				usersReadable = false
				resp.MembershipSource = "tasks (project users unreadable: " + err.Error() + ")"
			} else {
				for _, u := range users {
					if !u.Active {
						continue
					}
					add(zohoprojects.User{ID: u.ID, Name: u.Name, Email: u.Email}, zohoprojects.MapRole(u.Role), "project_user", u.Role)
				}
			}
		}

		tasks, err := st.client.ListTasks(ctx, portalID, zp.ID, nil, "")
		if err != nil {
			plan.Error = "list tasks: " + err.Error()
		}
		plan.Tasks = len(tasks)
		for _, t := range tasks {
			if st := zohoprojects.MapStatusWithType(t.Status, t.StatusType); st != zohoprojects.StatusDone && st != zohoprojects.StatusCancelled {
				plan.OpenTasks++
			}
			for _, o := range t.Owners {
				add(o, "member", "task_owner", "")
			}
			add(t.Creator, "member", "task_creator", "")
		}

		resp.Projects = append(resp.Projects, plan)
		resp.Totals.Workspaces++
		if plan.Action == "create" {
			resp.Totals.NewWorkspaces++
		}
		resp.Totals.Tasks += plan.Tasks
		for _, p := range plan.People {
			if p.Skipped != "" || people[p.Email] {
				continue
			}
			people[p.Email] = true
			resp.Totals.People++
			if !p.Exists {
				resp.Totals.NewUsers++
			}
		}
	}
	return resp, nil
}

// findZohoWorkspace returns the workspace an earlier migration created for a
// Zoho project, keyed by settings.zoho_project_id.
func (h *Handler) findZohoWorkspace(ctx context.Context, zohoProjectID string) (db.Workspace, bool, error) {
	var id pgtype.UUID
	err := h.DB.QueryRow(ctx,
		`SELECT id FROM workspace WHERE settings->>$1 = $2 ORDER BY created_at LIMIT 1`,
		zohoWorkspaceProjectKey, zohoProjectID,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.Workspace{}, false, nil
	}
	if err != nil {
		return db.Workspace{}, false, err
	}
	ws, err := h.Queries.GetWorkspace(ctx, id)
	if err != nil {
		return db.Workspace{}, false, err
	}
	return ws, true, nil
}

// freeWorkspaceSlug returns base, or base-2, base-3, … — the first slug that is
// not reserved, not an existing workspace, and not claimed earlier in this plan.
func (h *Handler) freeWorkspaceSlug(ctx context.Context, base string, taken map[string]bool) (string, error) {
	for n := 1; n <= 50; n++ {
		slug := base
		if n > 1 {
			slug = fmt.Sprintf("%s-%d", base, n)
		}
		if taken[slug] || isReservedSlug(slug) || !workspaceSlugPattern.MatchString(slug) {
			continue
		}
		if _, err := h.Queries.GetWorkspaceBySlug(ctx, slug); err == nil {
			continue
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("check slug %q: %w", slug, err)
		}
		return slug, nil
	}
	return "", fmt.Errorf("no free workspace slug for %q", base)
}

// --- apply ------------------------------------------------------------------

// applyZohoWorkspace creates (or reuses) the plan's workspace and makes every
// allowed person a member with the planned role.
func (h *Handler) applyZohoWorkspace(ctx context.Context, p *ZohoMigrateProject, policy zohoMigratePolicy) (pgtype.UUID, error) {
	// Resolve the owner's account first: the workspace is created with its
	// owner in one transaction, as CreateWorkspace does.
	var owner *ZohoMigratePerson
	for i := range p.People {
		if p.People[i].Role == "owner" && p.People[i].Skipped == "" {
			owner = &p.People[i]
			break
		}
	}
	if owner == nil {
		return pgtype.UUID{}, errors.New("no eligible workspace owner")
	}

	var wsID pgtype.UUID
	if p.Action == "reuse" {
		id, err := util.ParseUUID(p.WorkspaceID)
		if err != nil {
			return pgtype.UUID{}, err
		}
		wsID = id
	} else {
		ownerID, _, err := h.zohoEnsureUser(ctx, owner.Email, owner.Name)
		if err != nil {
			return pgtype.UUID{}, fmt.Errorf("owner account %s: %w", owner.Email, err)
		}
		wsID, err = h.createZohoWorkspace(ctx, p, ownerID, policy)
		if err != nil {
			return pgtype.UUID{}, err
		}
	}

	for i := range p.People {
		person := &p.People[i]
		if person.Skipped != "" {
			continue
		}
		userID, _, err := h.zohoEnsureUser(ctx, person.Email, person.Name)
		if err != nil {
			person.Skipped = "create user: " + err.Error()
			continue
		}
		if err := h.zohoEnsureMembership(ctx, wsID, userID, person.Role); err != nil {
			person.Skipped = "add member: " + err.Error()
		}
	}
	return wsID, nil
}

func (h *Handler) createZohoWorkspace(ctx context.Context, p *ZohoMigrateProject, ownerID pgtype.UUID, policy zohoMigratePolicy) (pgtype.UUID, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return pgtype.UUID{}, err
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)

	ws, err := qtx.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name:        p.Name,
		Slug:        p.WorkspaceSlug,
		Description: strToText(p.description),
		IssuePrefix: p.IssuePrefix,
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("create workspace %q: %w", p.WorkspaceSlug, err)
	}
	if _, err := qtx.CreateMember(ctx, db.CreateMemberParams{
		WorkspaceID: ws.ID, UserID: ownerID, Role: "owner",
	}); err != nil {
		return pgtype.UUID{}, fmt.Errorf("add owner: %w", err)
	}
	marker, err := json.Marshal(map[string]any{
		zohoWorkspaceProjectKey: p.ZohoProjectID,
		"zoho_project_key":      p.Key,
		zohoWorkspaceDomainsKey: policy.Domains,
		zohoWorkspaceAliasesKey: policy.Aliases,
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE workspace SET settings = COALESCE(settings, '{}'::jsonb) || $2::jsonb WHERE id = $1`,
		ws.ID, marker,
	); err != nil {
		return pgtype.UUID{}, fmt.Errorf("stamp zoho marker: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return pgtype.UUID{}, err
	}
	slog.Info("zoho migrate: workspace created",
		"workspace_id", util.UUIDToString(ws.ID), "slug", ws.Slug, "zoho_project_id", p.ZohoProjectID)
	return ws.ID, nil
}

// zohoEnsureUser finds the Agora account for email or creates it. A created
// account is left NOT onboarded on purpose: its first sign-in lands in the
// member setup (photo, notifications, a tour of their workspaces) instead of
// straight in a workspace nobody introduced — see the welcome-emails send.
func (h *Handler) zohoEnsureUser(ctx context.Context, email, name string) (pgtype.UUID, bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if u, err := h.Queries.GetUserByEmail(ctx, email); err == nil {
		return u.ID, false, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, false, err
	}
	if strings.TrimSpace(name) == "" {
		name = email
	}
	u, err := h.Queries.CreateUser(ctx, db.CreateUserParams{Name: strings.TrimSpace(name), Email: email})
	if err != nil {
		return pgtype.UUID{}, false, err
	}
	slog.Info("zoho migrate: user created", "email", email, "user_id", util.UUIDToString(u.ID))
	return u.ID, true, nil
}

// zohoEnsureMembership adds the user with role, or raises an existing lower
// role. It never lowers a role.
func (h *Handler) zohoEnsureMembership(ctx context.Context, wsID, userID pgtype.UUID, role string) error {
	m, err := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID: userID, WorkspaceID: wsID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = h.Queries.CreateMember(ctx, db.CreateMemberParams{WorkspaceID: wsID, UserID: userID, Role: role})
		return err
	}
	if err != nil {
		return err
	}
	if roleRank(role) > roleRank(m.Role) {
		_, err = h.Queries.UpdateMemberRole(ctx, db.UpdateMemberRoleParams{ID: m.ID, Role: role})
	}
	return err
}

// zohoMemberProvisioner returns the syncZohoTask hook that makes a task's
// owner/creator a member (role member) of the migrated workspace, under the
// migration's email policy. Returns ok=false for a refused or failed person;
// the importer then leaves them as a metadata chip, as before.
func (h *Handler) zohoMemberProvisioner(policy zohoMigratePolicy) func(context.Context, pgtype.UUID, zohoprojects.User) (pgtype.UUID, bool) {
	return func(ctx context.Context, wsID pgtype.UUID, u zohoprojects.User) (pgtype.UUID, bool) {
		email := policy.canonical(u.Email)
		if zohoEmailAllowed(email, policy.Domains) != "" {
			return pgtype.UUID{}, false
		}
		userID, _, err := h.zohoEnsureUser(ctx, email, u.Name)
		if err != nil {
			slog.Warn("zoho migrate: provision user failed", "email", u.Email, "error", err)
			return pgtype.UUID{}, false
		}
		if err := h.zohoEnsureMembership(ctx, wsID, userID, "member"); err != nil {
			slog.Warn("zoho migrate: provision member failed", "email", u.Email, "error", err)
			return pgtype.UUID{}, false
		}
		return userID, true
	}
}

// zohoMigratedProvisioner returns the provisioning hook for a workspace the
// migration created (so the poller keeps adding new Zoho people), or nil for
// any other workspace.
func (h *Handler) zohoMigratedProvisioner(ctx context.Context, wsID pgtype.UUID) func(context.Context, pgtype.UUID, zohoprojects.User) (pgtype.UUID, bool) {
	var raw []byte
	if err := h.DB.QueryRow(ctx,
		`SELECT settings FROM workspace WHERE id = $1 AND settings ? $2`, wsID, zohoWorkspaceProjectKey,
	).Scan(&raw); err != nil {
		return nil
	}
	var settings struct {
		Domains []string          `json:"zoho_migrate_domains"`
		Aliases map[string]string `json:"zoho_migrate_aliases"`
	}
	_ = json.Unmarshal(raw, &settings)
	return h.zohoMemberProvisioner(newZohoMigratePolicy(settings.Domains, settings.Aliases))
}
