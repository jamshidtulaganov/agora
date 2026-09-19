// The applier (docs/importers-plan.md §3.5, §3.7).
//
// Everything that writes lives here, and it writes in one fixed,
// dependency-driven order:
//
//	containers → labels → iterations → issues (parents before children)
//	→ comments → attachments → relations
//
// Relations are last, in their own pass, because a blocks-link can point at an
// issue that only exists after the pass that creates it. Parents come before
// children for the same reason one level down.
//
// The contract that makes the whole product work is the upsert: running the
// same import twice produces ZERO duplicates and a receipt of
// "created: 0, updated: N". Issues match on the external_ref blob in
// issue.metadata (migration 207's expression index); comments match on the
// comment.external_source/external_id pair (its partial unique index). Neither
// is a set on a row — the Bitrix design's bitrix_synced_comment_ids ARRAY meant
// every re-sync read and rewrote an unbounded array and a 400-comment issue
// turned that into a hot row.
//
// Source timestamps are preserved throughout: CreateIssueImported and
// UpsertCommentImported take explicit created_at/updated_at, because a two-year
// backlog that all says "created today" is not a migration, it is a paste.
package imports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Store is the applier's view of the database. The method set is exactly the
// generated sqlc signatures, so *db.Queries satisfies it with no adapter — see
// the compile-time assertion below. Tests supply an in-memory implementation,
// which is what lets the whole applier, including the idempotency contract, be
// covered without a database.
type Store interface {
	IncrementIssueCounter(ctx context.Context, workspaceID pgtype.UUID) (int32, error)

	ListProjects(ctx context.Context, arg db.ListProjectsParams) ([]db.Project, error)
	CreateProject(ctx context.Context, arg db.CreateProjectParams) (db.Project, error)
	SetProjectSettingKey(ctx context.Context, arg db.SetProjectSettingKeyParams) (db.Project, error)

	ListSprintsByProject(ctx context.Context, projectID pgtype.UUID) ([]db.Sprint, error)
	CreateSprint(ctx context.Context, arg db.CreateSprintParams) (db.Sprint, error)
	SetIssueSprint(ctx context.Context, arg db.SetIssueSprintParams) error

	ListLabels(ctx context.Context, workspaceID pgtype.UUID) ([]db.IssueLabel, error)
	CreateLabel(ctx context.Context, arg db.CreateLabelParams) (db.IssueLabel, error)
	AttachLabelToIssue(ctx context.Context, arg db.AttachLabelToIssueParams) error

	FindIssueByExternalRef(ctx context.Context, arg db.FindIssueByExternalRefParams) (db.Issue, error)
	ListIssuesByExternalSource(ctx context.Context, arg db.ListIssuesByExternalSourceParams) ([]db.ListIssuesByExternalSourceRow, error)
	CreateIssueImported(ctx context.Context, arg db.CreateIssueImportedParams) (db.Issue, error)
	UpdateIssueImported(ctx context.Context, arg db.UpdateIssueImportedParams) (db.Issue, error)
	UpsertCommentImported(ctx context.Context, arg db.UpsertCommentImportedParams) (db.Comment, error)
	UpsertIssueDependency(ctx context.Context, arg db.UpsertIssueDependencyParams) (int64, error)
}

// The framework must keep working against the generated queries directly; this
// line fails the build the day a query's signature drifts away from the seam.
var _ Store = (*db.Queries)(nil)

// Limits are the explicit budgets §3.6 requires to be surfaced rather than
// enforced silently. Anything over budget is skipped AND LISTED, and the
// original URL stays in the linkage blob so a human can fetch it.
type Limits struct {
	MaxAttachmentBytes      int64
	MaxTotalAttachmentBytes int64
	MaxAttachmentsPerIssue  int
	// MaxFailures bounds import_job.failures so a pathological run cannot
	// write a 200 MB jsonb column.
	MaxFailures int
}

// DefaultLimits are the caps a run gets when the caller sets none. They are
// deliberately conservative: an import that copies 2 GB of screen recordings on
// its first try is a support ticket, and the plan tells the operator exactly
// what was left behind so they can raise the cap and re-run (which upserts).
func DefaultLimits() Limits {
	return Limits{
		MaxAttachmentBytes:      25 << 20, // 25 MB
		MaxTotalAttachmentBytes: 2 << 30,  // 2 GB
		MaxAttachmentsPerIssue:  20,
		MaxFailures:             200,
	}
}

func (l Limits) withDefaults() Limits {
	d := DefaultLimits()
	if l.MaxAttachmentBytes == 0 {
		l.MaxAttachmentBytes = d.MaxAttachmentBytes
	}
	if l.MaxTotalAttachmentBytes == 0 {
		l.MaxTotalAttachmentBytes = d.MaxTotalAttachmentBytes
	}
	if l.MaxAttachmentsPerIssue == 0 {
		l.MaxAttachmentsPerIssue = d.MaxAttachmentsPerIssue
	}
	if l.MaxFailures == 0 {
		l.MaxFailures = d.MaxFailures
	}
	return l
}

// EntityTotals is the per-kind receipt line. It is what import_job.totals holds
// and what the UI renders as "created 240, updated 0, skipped 3, failed 0".
type EntityTotals struct {
	Created int `json:"created"`
	Updated int `json:"updated"`
	Skipped int `json:"skipped"`
	Failed  int `json:"failed"`
}

// Failure is one bounded line of import_job.failures. Reason is operator-facing
// and must never contain a credential.
type Failure struct {
	Kind       string `json:"kind"`
	Identifier string `json:"identifier"`
	Reason     string `json:"reason"`
}

// Result is what one apply produced.
type Result struct {
	Totals   map[string]*EntityTotals `json:"totals"`
	Failures []Failure                `json:"failures"`
	// FailuresTruncated counts failures beyond MaxFailures that were not
	// recorded. A bounded list that does not say it is bounded is a lie.
	FailuresTruncated int `json:"failures_truncated,omitempty"`
}

// Entity kinds, as they appear in totals/failures.
const (
	KindProject    = "projects"
	KindSprint     = "sprints"
	KindLabel      = "labels"
	KindIssue      = "issues"
	KindComment    = "comments"
	KindAttachment = "attachments"
	KindRelation   = "relations"
)

func newResult() *Result {
	return &Result{Totals: map[string]*EntityTotals{}}
}

func (r *Result) totals(kind string) *EntityTotals {
	if r.Totals[kind] == nil {
		r.Totals[kind] = &EntityTotals{}
	}
	return r.Totals[kind]
}

func (r *Result) fail(limit int, kind, identifier string, err error) {
	r.totals(kind).Failed++
	if len(r.Failures) >= limit {
		r.FailuresTruncated++
		return
	}
	r.Failures = append(r.Failures, Failure{Kind: kind, Identifier: identifier, Reason: err.Error()})
}

// Applier executes one plan. It holds no package-level state of any kind: two
// workspaces importing at the same time are two Appliers, which is the whole
// reason the job is a row and not a global (§3.7).
type Applier struct {
	Store    Store
	Resolver *ActorResolver
	Mapping  *Mapping

	WorkspaceID pgtype.UUID
	// ImportID is the import_job id, stamped into every external_ref so the
	// receipt can be recomputed from the rows themselves
	// (CountIssuesByImportJob) rather than from a counter held in memory.
	ImportID string
	Source   string
	Limits   Limits

	// Attachments are copied only when BOTH halves of the seam are present:
	// an adapter that can stream the file and a sink that can store it. When
	// either is missing the files are skipped and listed, never dropped
	// quietly.
	Opener AttachmentOpener
	Sink   AttachmentSink

	// Now is injectable so tests get deterministic imported_at stamps.
	Now func() time.Time

	// resolved maps source ids to what they became, within one run.
	projects   map[string]pgtype.UUID
	sprints    map[string]pgtype.UUID
	labels     map[string]pgtype.UUID
	issues     map[string]pgtype.UUID
	attachment struct {
		bytes int64
	}
}

// Apply walks the bundle in dependency order and returns the receipt. ctx
// cancellation is honoured between entities, so a cancelled job stops promptly
// and everything already written stays written — the upsert makes resuming a
// re-run rather than a repair.
func (a *Applier) Apply(ctx context.Context, b *Bundle, progress ProgressFunc) (*Result, error) {
	if a.Store == nil {
		return nil, errors.New("imports: applier has no store")
	}
	if b == nil {
		return nil, errors.New("imports: cannot apply a nil bundle")
	}
	if progress == nil {
		progress = NopProgress
	}
	if a.Now == nil {
		a.Now = func() time.Time { return time.Now().UTC() }
	}
	a.Limits = a.Limits.withDefaults()
	a.projects = map[string]pgtype.UUID{}
	a.sprints = map[string]pgtype.UUID{}
	a.labels = map[string]pgtype.UUID{}
	a.issues = map[string]pgtype.UUID{}

	result := newResult()

	if err := a.applyContainers(ctx, b, result); err != nil {
		return result, err
	}
	if err := a.applyLabels(ctx, b, result); err != nil {
		return result, err
	}
	if err := a.applyIterations(ctx, b, result); err != nil {
		return result, err
	}
	if err := a.applyIssues(ctx, b, result, progress); err != nil {
		return result, err
	}
	// Relations last: an edge can only be written once both ends exist.
	if err := a.applyRelations(ctx, b, result); err != nil {
		return result, err
	}
	return result, nil
}

// --- containers → projects --------------------------------------------------

// applyContainers links or creates one Agora project per source container. The
// linkage lives in project.settings.external_ref — NOT in project.description,
// which is where the Bitrix integration put its `bitrix_group:<id>` marker
// purely to avoid writing a migration. That marker leaks provenance into
// user-visible copy; project.settings is a jsonb column that already exists.
func (a *Applier) applyContainers(ctx context.Context, b *Bundle, result *Result) error {
	if len(b.Containers) == 0 {
		return nil
	}
	existing, err := a.Store.ListProjects(ctx, db.ListProjectsParams{WorkspaceID: a.WorkspaceID})
	if err != nil {
		return fmt.Errorf("imports: list projects: %w", err)
	}
	byExternal := map[string]pgtype.UUID{}
	byTitle := map[string]pgtype.UUID{}
	for _, p := range existing {
		if ref, ok := ReadExternalRef(p.Settings); ok && ref.Source == a.Source {
			byExternal[ref.ID] = p.ID
		}
		byTitle[foldKey(p.Title)] = p.ID
	}

	for _, c := range b.Containers {
		if err := ctx.Err(); err != nil {
			return err
		}
		// 1. Pinned by the operator's mapping.
		if pinned, ok := a.Mapping.ContainerProject(c); ok {
			if id, perr := util.ParseUUID(pinned); perr == nil {
				a.projects[c.ExternalID] = id
				result.totals(KindProject).Updated++
				continue
			}
			result.fail(a.Limits.MaxFailures, KindProject, c.Name,
				fmt.Errorf("mapped project id %q is not a uuid", pinned))
		}
		// 2. Already linked by a previous run.
		if id, ok := byExternal[c.ExternalID]; ok {
			a.projects[c.ExternalID] = id
			result.totals(KindProject).Updated++
			continue
		}
		// 3. An existing project with the same title — the second import of a
		// team someone already made a project for by hand. Adopt it and stamp
		// the linkage so the third run takes branch 2.
		if id, ok := byTitle[foldKey(c.Name)]; ok {
			a.projects[c.ExternalID] = id
			if err := a.stampProjectRef(ctx, id, c); err != nil {
				result.fail(a.Limits.MaxFailures, KindProject, c.Name, err)
			}
			result.totals(KindProject).Updated++
			continue
		}
		// 4. Create.
		created, err := a.Store.CreateProject(ctx, db.CreateProjectParams{
			WorkspaceID: a.WorkspaceID,
			Title:       containerTitle(c),
			Description: pgtype.Text{String: c.Description, Valid: c.Description != ""},
			Icon:        pgtype.Text{String: c.Icon, Valid: c.Icon != ""},
			Status:      "active",
			Priority:    PriorityNameNone,
		})
		if err != nil {
			result.fail(a.Limits.MaxFailures, KindProject, c.Name, err)
			continue
		}
		a.projects[c.ExternalID] = created.ID
		if err := a.stampProjectRef(ctx, created.ID, c); err != nil {
			result.fail(a.Limits.MaxFailures, KindProject, c.Name, err)
		}
		result.totals(KindProject).Created++
	}
	return nil
}

func containerTitle(c Container) string {
	if title := strings.TrimSpace(c.Name); title != "" {
		return title
	}
	if key := strings.TrimSpace(c.Key); key != "" {
		return key
	}
	return "Imported project"
}

func (a *Applier) stampProjectRef(ctx context.Context, projectID pgtype.UUID, c Container) error {
	ref := ExternalRef{
		Source:     a.Source,
		ID:         c.ExternalID,
		Identifier: c.Key,
		URL:        c.URL,
		ImportedAt: a.Now().UTC().Format(time.RFC3339),
		ImportID:   a.ImportID,
	}
	blob, err := json.Marshal(ref)
	if err != nil {
		return err
	}
	_, err = a.Store.SetProjectSettingKey(ctx, db.SetProjectSettingKeyParams{
		Key:         ExternalRefKey,
		Value:       blob,
		ID:          projectID,
		WorkspaceID: a.WorkspaceID,
	})
	return err
}

// --- labels -----------------------------------------------------------------

// applyLabels name-matches case-insensitively and creates what is missing, with
// the source's colour when there is one. A `source:linear` label is explicitly
// NOT added to every issue: provenance lives in the linkage blob, not in the
// label namespace the team has to look at forever (§3.4).
func (a *Applier) applyLabels(ctx context.Context, b *Bundle, result *Result) error {
	if len(b.Labels) == 0 {
		return nil
	}
	existing, err := a.Store.ListLabels(ctx, a.WorkspaceID)
	if err != nil {
		return fmt.Errorf("imports: list labels: %w", err)
	}
	byName := map[string]pgtype.UUID{}
	for _, l := range existing {
		byName[foldKey(l.Name)] = l.ID
	}

	for _, l := range b.Labels {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := strings.TrimSpace(l.Name)
		if name == "" {
			continue
		}
		if id, ok := byName[foldKey(name)]; ok {
			a.labels[l.ExternalID] = id
			result.totals(KindLabel).Updated++
			continue
		}
		created, err := a.Store.CreateLabel(ctx, db.CreateLabelParams{
			WorkspaceID: a.WorkspaceID,
			Name:        name,
			Color:       labelColor(l.Color),
		})
		if err != nil {
			result.fail(a.Limits.MaxFailures, KindLabel, name, err)
			continue
		}
		byName[foldKey(name)] = created.ID
		a.labels[l.ExternalID] = created.ID
		result.totals(KindLabel).Created++
	}
	return nil
}

// labelColor keeps the source colour when it is a hex value and otherwise
// hands the label a neutral one. issue_label.color is NOT NULL, so "" is not an
// option, and a source-specific colour name is not a colour Agora renders.
func labelColor(c string) string {
	c = strings.TrimSpace(c)
	if strings.HasPrefix(c, "#") && (len(c) == 7 || len(c) == 4) {
		return c
	}
	return "#6b7280"
}

// --- iterations → sprints ---------------------------------------------------

// applyIterations turns cycles/sprints into Agora sprints. Sprints have no
// settings/metadata column, so the idempotency key here is (project, name) —
// which is also how a human would recognize a re-import. The name is preserved
// verbatim so "Cycle 12" stays "Cycle 12".
func (a *Applier) applyIterations(ctx context.Context, b *Bundle, result *Result) error {
	if len(b.Iterations) == 0 {
		return nil
	}
	cached := map[string]map[string]pgtype.UUID{} // projectID -> folded name -> sprint

	for _, it := range b.Iterations {
		if err := ctx.Err(); err != nil {
			return err
		}
		projectID, ok := a.projects[it.ContainerID]
		if !ok || !projectID.Valid {
			// A cycle whose team was not in scope. Not an error — it simply
			// has nowhere to live.
			result.totals(KindSprint).Skipped++
			continue
		}
		key := util.UUIDToString(projectID)
		if cached[key] == nil {
			rows, err := a.Store.ListSprintsByProject(ctx, projectID)
			if err != nil {
				return fmt.Errorf("imports: list sprints: %w", err)
			}
			cached[key] = map[string]pgtype.UUID{}
			for _, s := range rows {
				cached[key][foldKey(s.Name)] = s.ID
			}
		}
		name := strings.TrimSpace(it.Name)
		if name == "" {
			name = "Imported iteration"
		}
		if id, ok := cached[key][foldKey(name)]; ok {
			a.sprints[it.ExternalID] = id
			result.totals(KindSprint).Updated++
			continue
		}
		created, err := a.Store.CreateSprint(ctx, db.CreateSprintParams{
			WorkspaceID: a.WorkspaceID,
			ProjectID:   projectID,
			Name:        name,
			Goal:        it.Goal,
			Status:      sprintStatus(it),
			StartDate:   timestamptz(it.StartsAt),
			EndDate:     timestamptz(it.EndsAt),
		})
		if err != nil {
			result.fail(a.Limits.MaxFailures, KindSprint, name, err)
			continue
		}
		cached[key][foldKey(name)] = created.ID
		a.sprints[it.ExternalID] = created.ID
		result.totals(KindSprint).Created++
	}
	return nil
}

// sprintStatus maps an iteration onto sprint.status, whose CHECK admits
// planned | active | completed. A cycle the source calls finished is completed;
// one whose window contains now is active; everything else is planned.
func sprintStatus(it Iteration) string {
	if it.Completed {
		return "completed"
	}
	now := time.Now().UTC()
	if it.StartsAt != nil && it.EndsAt != nil && !now.Before(*it.StartsAt) && now.Before(*it.EndsAt) {
		return "active"
	}
	if it.EndsAt != nil && now.After(*it.EndsAt) {
		return "completed"
	}
	return "planned"
}

// --- issues -----------------------------------------------------------------

// applyIssues writes issues parents-first, then each issue's comments and
// attachments. The parent ordering is a topological pass over the bundle's own
// parent ids, so a child never references an issue id that does not exist yet.
func (a *Applier) applyIssues(ctx context.Context, b *Bundle, result *Result, progress ProgressFunc) error {
	ordered := parentsFirst(b.Issues)
	for i := range ordered {
		if err := ctx.Err(); err != nil {
			return err
		}
		issue := ordered[i]
		if err := a.applyIssue(ctx, b, issue, result); err != nil {
			result.fail(a.Limits.MaxFailures, KindIssue, issue.Identifier, err)
		}
		progress(Progress{Phase: "apply", Kind: KindIssue, Done: i + 1, Total: len(ordered)})
	}
	return nil
}

// parentsFirst orders issues so that every parent precedes its children.
// Cycles (which a source should not produce but Jira's link data can) and
// parents outside the scope are tolerated: the issue is emitted anyway and its
// parent link is simply dropped, because refusing to import an issue because of
// a bad edge is exactly the "workflow field blocks sync entirely" failure
// Linear's own importer is criticized for.
func parentsFirst(issues []Issue) []*Issue {
	byID := make(map[string]*Issue, len(issues))
	for i := range issues {
		byID[issues[i].ExternalID] = &issues[i]
	}
	out := make([]*Issue, 0, len(issues))
	state := make(map[string]int, len(issues)) // 0 unseen, 1 in progress, 2 emitted

	var visit func(issue *Issue)
	visit = func(issue *Issue) {
		switch state[issue.ExternalID] {
		case 1, 2:
			return
		}
		state[issue.ExternalID] = 1
		if issue.ParentID != "" {
			if parent, ok := byID[issue.ParentID]; ok {
				visit(parent)
			}
		}
		state[issue.ExternalID] = 2
		out = append(out, issue)
	}
	// Walk in the bundle's own order so a run is deterministic.
	for i := range issues {
		visit(&issues[i])
	}
	return out
}

func (a *Applier) applyIssue(ctx context.Context, b *Bundle, issue *Issue, result *Result) error {
	if issue.ExternalID == "" {
		result.totals(KindIssue).Skipped++
		return nil
	}

	creator, err := a.resolveActor(ctx, b, issue.CreatorID)
	if err != nil {
		// Degraded, not fatal: the row lands on the import identity and the
		// operator sees a failure line explaining why.
		result.fail(a.Limits.MaxFailures, KindIssue, issue.Identifier, err)
	}
	assignee, _ := a.resolveActor(ctx, b, issue.AssigneeID)

	status := StatusTodo
	if state, ok := b.StateByID(issue.StateID); ok {
		status = a.Mapping.Status(state)
	} else if issue.StateID != "" {
		status = a.Mapping.Status(State{ExternalID: issue.StateID, Name: issue.StateID})
	}
	if !ValidStatus(status) {
		status = StatusTodo
	}

	ref := ExternalRef{
		Source:     a.Source,
		ID:         issue.ExternalID,
		Identifier: issue.Identifier,
		URL:        issue.URL,
		ImportedAt: a.Now().UTC().Format(time.RFC3339),
		ImportID:   a.ImportID,
	}
	// The real name is preserved on the blob precisely when it is about to be
	// lost from the row — the author who could not be matched.
	if creator.Via == ViaImportIdentity {
		ref.Author = creator.Name
	}

	existing, err := a.Store.FindIssueByExternalRef(ctx, db.FindIssueByExternalRefParams{
		WorkspaceID: a.WorkspaceID,
		Source:      a.Source,
		ExternalID:  issue.ExternalID,
	})
	found := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("lookup by external ref: %w", err)
	}

	var row db.Issue
	if found {
		metadata, merr := MergeExternalRef(existing.Metadata, ref)
		if merr != nil {
			return merr
		}
		row, err = a.Store.UpdateIssueImported(ctx, db.UpdateIssueImportedParams{
			ID:            existing.ID,
			WorkspaceID:   a.WorkspaceID,
			Title:         issueTitle(issue),
			Description:   pgtype.Text{String: issue.BodyMarkdown, Valid: issue.BodyMarkdown != ""},
			Status:        status,
			Priority:      a.Mapping.Priority(issue.Priority),
			AssigneeType:  assigneeType(assignee),
			AssigneeID:    mustUUID(assignee.UserID),
			ParentIssueID: a.issues[issue.ParentID],
			ProjectID:     a.projects[issue.ContainerID],
			StartDate:     dateFrom(issue.StartedAt),
			DueDate:       dateFrom(issue.DueDate),
			Metadata:      metadata,
			UpdatedAt:     pgtype.Timestamptz{Time: issue.UpdatedAt, Valid: !issue.UpdatedAt.IsZero()},
		})
		if err != nil {
			return fmt.Errorf("update issue: %w", err)
		}
		result.totals(KindIssue).Updated++
	} else {
		metadata, merr := MergeExternalRef(nil, ref)
		if merr != nil {
			return merr
		}
		// The issue number comes from IncrementIssueCounter, which takes
		// GREATEST(counter+1, max(number)+1). That self-heal was paid for by
		// an earlier bulk load that preserved external numbering and desynced
		// the counter until every create failed on uq_issue_workspace_number;
		// an importer writing explicit numbers cannot reintroduce it.
		number, nerr := a.Store.IncrementIssueCounter(ctx, a.WorkspaceID)
		if nerr != nil {
			return fmt.Errorf("issue number: %w", nerr)
		}
		created := issue.CreatedAt
		if created.IsZero() {
			created = a.Now().UTC()
		}
		updated := issue.UpdatedAt
		if updated.IsZero() {
			updated = created
		}
		row, err = a.Store.CreateIssueImported(ctx, db.CreateIssueImportedParams{
			WorkspaceID:   a.WorkspaceID,
			Title:         issueTitle(issue),
			Description:   pgtype.Text{String: issue.BodyMarkdown, Valid: issue.BodyMarkdown != ""},
			Status:        status,
			Priority:      a.Mapping.Priority(issue.Priority),
			AssigneeType:  assigneeType(assignee),
			AssigneeID:    mustUUID(assignee.UserID),
			CreatorType:   "member",
			CreatorID:     mustUUID(creator.UserID),
			ParentIssueID: a.issues[issue.ParentID],
			StartDate:     dateFrom(issue.StartedAt),
			DueDate:       dateFrom(issue.DueDate),
			Number:        number,
			ProjectID:     a.projects[issue.ContainerID],
			Metadata:      metadata,
			CreatedAt:     pgtype.Timestamptz{Time: created, Valid: true},
			UpdatedAt:     pgtype.Timestamptz{Time: updated, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("create issue: %w", err)
		}
		result.totals(KindIssue).Created++
	}
	a.issues[issue.ExternalID] = row.ID

	if sprintID, ok := a.sprints[issue.IterationID]; ok && sprintID.Valid {
		if err := a.Store.SetIssueSprint(ctx, db.SetIssueSprintParams{IssueID: row.ID, SprintID: sprintID}); err != nil {
			result.fail(a.Limits.MaxFailures, KindSprint, issue.Identifier, err)
		}
	}
	for _, labelID := range issue.LabelIDs {
		id, ok := a.labels[labelID]
		if !ok {
			continue
		}
		if err := a.Store.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
			IssueID: row.ID, LabelID: id, WorkspaceID: a.WorkspaceID,
		}); err != nil {
			result.fail(a.Limits.MaxFailures, KindLabel, issue.Identifier, err)
		}
	}

	a.applyComments(ctx, b, issue, row.ID, result)
	a.applyAttachments(ctx, issue, row.ID, result)
	return nil
}

func issueTitle(issue *Issue) string {
	if title := strings.TrimSpace(issue.Title); title != "" {
		return title
	}
	if issue.Identifier != "" {
		return issue.Identifier
	}
	return "Imported issue"
}

// --- comments ---------------------------------------------------------------

// applyComments upserts the discussion. The unique index does the dedup, so a
// re-run updates an edited comment in place; there is no read-modify-write of a
// synced-id array anywhere in this path.
func (a *Applier) applyComments(ctx context.Context, b *Bundle, issue *Issue, issueID pgtype.UUID, result *Result) {
	for _, c := range issue.Comments {
		if err := ctx.Err(); err != nil {
			return
		}
		body := strings.TrimSpace(c.BodyMarkdown)
		if body == "" {
			result.totals(KindComment).Skipped++
			continue
		}
		author, err := a.resolveActor(ctx, b, c.AuthorID)
		if err != nil {
			result.fail(a.Limits.MaxFailures, KindComment, issue.Identifier, err)
		}
		if author.UserID == "" {
			result.fail(a.Limits.MaxFailures, KindComment, issue.Identifier,
				errors.New("no attribution identity available; comment skipped rather than attributed to the operator"))
			result.totals(KindComment).Skipped++
			continue
		}
		if author.Via == ViaImportIdentity && author.Name != "" {
			// Provenance the row itself can no longer carry.
			body = fmt.Sprintf("**%s — %s**:\n%s", ImportIdentityName(a.Source), author.Name, body)
		}
		created := c.CreatedAt
		if created.IsZero() {
			created = issue.CreatedAt
		}
		updated := c.UpdatedAt
		if updated.IsZero() {
			updated = created
		}
		if _, err := a.Store.UpsertCommentImported(ctx, db.UpsertCommentImportedParams{
			IssueID:        issueID,
			WorkspaceID:    a.WorkspaceID,
			AuthorType:     "member",
			AuthorID:       mustUUID(author.UserID),
			Content:        body,
			Type:           "comment",
			ExternalSource: pgtype.Text{String: a.Source, Valid: true},
			ExternalID:     pgtype.Text{String: c.ExternalID, Valid: c.ExternalID != ""},
			CreatedAt:      pgtype.Timestamptz{Time: created, Valid: !created.IsZero()},
			UpdatedAt:      pgtype.Timestamptz{Time: updated, Valid: !updated.IsZero()},
		}); err != nil {
			result.fail(a.Limits.MaxFailures, KindComment, issue.Identifier, err)
			continue
		}
		result.totals(KindComment).Updated++
	}
}

// --- attachments ------------------------------------------------------------

// applyAttachments streams each in-budget file from the source into the sink.
// Over-budget files, and every file when the seam is not wired, are SKIPPED AND
// LISTED — the original URL is already on the issue's linkage blob, so a human
// can still fetch one.
func (a *Applier) applyAttachments(ctx context.Context, issue *Issue, issueID pgtype.UUID, result *Result) {
	if len(issue.Attachments) == 0 {
		return
	}
	if a.Opener == nil || a.Sink == nil {
		result.totals(KindAttachment).Skipped += len(issue.Attachments)
		result.fail(a.Limits.MaxFailures, KindAttachment, issue.Identifier,
			errors.New("attachment copying is not wired for this run; files stay in the source"))
		return
	}
	accepted := 0
	for _, att := range issue.Attachments {
		if err := ctx.Err(); err != nil {
			return
		}
		switch {
		case a.Limits.MaxAttachmentsPerIssue > 0 && accepted >= a.Limits.MaxAttachmentsPerIssue,
			a.Limits.MaxAttachmentBytes > 0 && att.Size > a.Limits.MaxAttachmentBytes,
			a.Limits.MaxTotalAttachmentBytes > 0 && a.attachment.bytes+att.Size > a.Limits.MaxTotalAttachmentBytes:
			result.totals(KindAttachment).Skipped++
			continue
		}
		body, err := a.Opener.OpenAttachment(ctx, att)
		if err != nil {
			result.fail(a.Limits.MaxFailures, KindAttachment, att.Title, err)
			continue
		}
		err = a.Sink.StoreAttachment(ctx, util.UUIDToString(issueID), att, body)
		body.Close()
		if err != nil {
			result.fail(a.Limits.MaxFailures, KindAttachment, att.Title, err)
			continue
		}
		accepted++
		a.attachment.bytes += att.Size
		result.totals(KindAttachment).Created++
	}
}

// --- relations --------------------------------------------------------------

// applyRelations runs last and writes only edges whose BOTH ends landed in this
// import. An edge pointing outside the scope is skipped, not failed: importing
// one team of a five-team workspace legitimately cuts edges, and a dangling
// reference is not an error the operator can act on.
func (a *Applier) applyRelations(ctx context.Context, b *Bundle, result *Result) error {
	for i := range b.Issues {
		issue := &b.Issues[i]
		from, ok := a.issues[issue.ExternalID]
		if !ok {
			continue
		}
		for _, rel := range issue.Relations {
			if err := ctx.Err(); err != nil {
				return err
			}
			to, ok := a.issues[rel.TargetExternalID]
			if !ok || !to.Valid {
				result.totals(KindRelation).Skipped++
				continue
			}
			kind := rel.Kind
			if kind == "" {
				kind, _ = DegradeRelation(rel.SourceType)
			}
			if !validRelation(kind) {
				kind = RelationRelated
			}
			affected, err := a.Store.UpsertIssueDependency(ctx, db.UpsertIssueDependencyParams{
				IssueID:          from,
				DependsOnIssueID: to,
				Type:             string(kind),
			})
			if err != nil {
				result.fail(a.Limits.MaxFailures, KindRelation, issue.Identifier, err)
				continue
			}
			if affected == 0 {
				result.totals(KindRelation).Updated++ // already present; the re-run is a no-op
				continue
			}
			result.totals(KindRelation).Created++
		}
	}
	return nil
}

func validRelation(k RelationKind) bool {
	switch k {
	case RelationBlocks, RelationBlockedBy, RelationRelated, RelationDuplicate:
		return true
	}
	return false
}

// --- shared helpers ---------------------------------------------------------

// resolveActor maps a source actor id to an Agora user. An empty id, an actor
// the bundle does not know, and a resolver failure ALL land on the import
// identity — never on the operator, whose id this type cannot see.
func (a *Applier) resolveActor(ctx context.Context, b *Bundle, externalID string) (Resolution, error) {
	if a.Resolver == nil {
		return Resolution{}, ErrNoIdentityStore
	}
	user, ok := b.UserByID(externalID)
	if !ok {
		user = User{ExternalID: externalID}
	}
	if externalID == "" {
		// Nothing to resolve; go straight to attribution.
		id, err := a.Resolver.ImportIdentity(ctx)
		return Resolution{UserID: id, Via: ViaImportIdentity}, err
	}
	return a.Resolver.Resolve(ctx, user)
}

func assigneeType(r Resolution) pgtype.Text {
	if r.UserID == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: "member", Valid: true}
}

// mustUUID converts a resolved id string. An unparseable id yields the zero
// UUID, which the NOT NULL columns reject loudly — the alternative (silently
// writing a zero uuid that matches no row) is the exact shape of #1661.
func mustUUID(s string) pgtype.UUID {
	if s == "" {
		return pgtype.UUID{}
	}
	id, err := util.ParseUUID(s)
	if err != nil {
		return pgtype.UUID{}
	}
	return id
}

func timestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil || t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func dateFrom(t *time.Time) pgtype.Date {
	if t == nil || t.IsZero() {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: t.UTC().Truncate(24 * time.Hour), Valid: true}
}
