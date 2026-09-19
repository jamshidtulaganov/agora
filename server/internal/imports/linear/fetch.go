// The Adapter implementation: the paged walk that turns a Linear workspace into
// one canonical Bundle.
//
// The shape of the walk is forced by Linear's complexity model, not by taste.
// Complexity multiplies through connections, so the single query that looks
// most natural —
//
//	issues(first:50){ comments(first:50) attachments(first:50) history(first:50) }
//
// — multiplies out and can trip the 10,000-point per-query ceiling on its own.
// So the adapter fetches SHALLOW and then does comments, attachments and
// relations in their OWN passes, batched by the ids it already has. Labels are
// the one exception: they are inline on the issue at a small bounded page,
// because a label is two scalars and the alternative is a third pass for
// nothing.
//
// Reference data (teams, states, labels, users, cycles) is walked first and
// whole. It is small, it is what the mapping tables are built from, and the
// dry-run report needs all of it before a single issue is worth fetching.
package linear

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jamshidtulaganov/agora/server/internal/imports"
)

// Adapter is the Linear source adapter. One per import run; it carries the
// client's adaptive page-size state and nothing else.
type Adapter struct {
	client *Client
	ref    string // the Linear organization's urlKey, for display
}

// NewAdapter builds the adapter over a client.
func NewAdapter(client *Client) *Adapter { return &Adapter{client: client} }

// Compile-time proof that this satisfies the framework's two seams. The second
// one is optional by contract, so it is asserted rather than assumed.
var (
	_ imports.Adapter          = (*Adapter)(nil)
	_ imports.AttachmentOpener = (*Adapter)(nil)
)

// Source names the vendor. Ref is filled in by Probe/Fetch once the
// organization is known; before that it is the bare kind, which is all the
// database column needs.
func (a *Adapter) Source() imports.Source {
	return imports.Source{Kind: imports.SourceLinear, Ref: a.ref}
}

// Defaults is the status/priority table from normalize.go.
func (a *Adapter) Defaults() imports.Defaults { return Defaults() }

const viewerQuery = `query Viewer {
  viewer { id name email }
  organization { id name urlKey }
}`

type viewerResponse struct {
	Viewer struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"viewer"`
	Organization struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		URLKey string `json:"urlKey"`
	} `json:"organization"`
}

// Probe verifies the credential with the cheapest query Linear has. It
// distinguishes "your key is wrong" from "Linear is down", because those have
// different next actions for the operator — and it returns NOTHING derived from
// the token itself.
func (a *Adapter) Probe(ctx context.Context) (imports.ProbeResult, error) {
	var out viewerResponse
	if err := a.client.Query(ctx, viewerQuery, nil, &out); err != nil {
		var gqlErr *Error
		if errors.As(err, &gqlErr) && gqlErr.Unauthorized() {
			return imports.ProbeResult{
				Status: imports.ProbeInvalid,
				Detail: "Linear rejected this API key. Create a personal API key with read access and paste it again.",
			}, nil
		}
		return imports.ProbeResult{
			Status: imports.ProbeUnreachable,
			Detail: "Could not reach Linear. This is usually temporary.",
		}, err
	}
	a.ref = out.Organization.URLKey
	return imports.ProbeResult{
		Status:  imports.ProbeOK,
		Account: out.Viewer.Email,
		Ref:     out.Organization.URLKey,
	}, nil
}

const teamsQuery = `query Teams($first: Int!, $after: String) {
  teams(first: $first, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes { id key name description icon archivedAt }
  }
}`

type teamsResponse struct {
	Teams struct {
		PageInfo pageInfo     `json:"pageInfo"`
		Nodes    []vendorTeam `json:"nodes"`
	} `json:"teams"`
}

// Containers lists the Linear teams. A TEAM, not a Linear project, is the Agora
// project: it owns the identifier prefix ("ENG-142") and it owns the cycles
// that become sprints, so making it the container keeps issues and their
// sprints in the same place. An issue's Linear project is preserved on the
// issue's Raw blob instead of inventing a second container kind (see
// normalizeIssue).
func (a *Adapter) Containers(ctx context.Context) ([]imports.Container, error) {
	var out []imports.Container
	err := a.paginate(ctx, func(cursor string) (pageInfo, error) {
		var page teamsResponse
		if err := a.client.Query(ctx, teamsQuery, a.pageVars(cursor, nil), &page); err != nil {
			return pageInfo{}, err
		}
		for _, t := range page.Teams.Nodes {
			out = append(out, normalizeTeam(t))
		}
		return page.Teams.PageInfo, nil
	})
	return out, err
}

const statesQuery = `query States($first: Int!, $after: String) {
  workflowStates(first: $first, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes { id name type position team { id } }
  }
}`

const labelsQuery = `query Labels($first: Int!, $after: String) {
  issueLabels(first: $first, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes { id name color }
  }
}`

const usersQuery = `query Users($first: Int!, $after: String) {
  users(first: $first, after: $after, includeDisabled: true) {
    pageInfo { hasNextPage endCursor }
    nodes { id name email active }
  }
}`

const cyclesQuery = `query Cycles($first: Int!, $after: String) {
  cycles(first: $first, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes { id number name startsAt endsAt completedAt team { id } }
  }
}`

const issuesQuery = `query Issues($first: Int!, $after: String, $filter: IssueFilter, $includeArchived: Boolean) {
  issues(first: $first, after: $after, filter: $filter, includeArchived: $includeArchived) {
    pageInfo { hasNextPage endCursor }
    nodes {
      id identifier number title description priority estimate url
      createdAt updatedAt startedAt completedAt dueDate archivedAt
      state { id }
      team { id }
      cycle { id }
      parent { id }
      assignee { id }
      creator { id }
      project { id name }
      labels(first: 20) { nodes { id name color } }
    }
  }
}`

const commentsQuery = `query Comments($first: Int!, $after: String, $ids: [ID!]!) {
  comments(first: $first, after: $after, filter: { issue: { id: { in: $ids } } }) {
    pageInfo { hasNextPage endCursor }
    nodes {
      id body url createdAt updatedAt
      issue { id }
      parent { id }
      user { id }
      botActor { id name }
    }
  }
}`

const attachmentsQuery = `query Attachments($first: Int!, $after: String, $ids: [ID!]!) {
  attachments(first: $first, after: $after, filter: { issue: { id: { in: $ids } } }) {
    pageInfo { hasNextPage endCursor }
    nodes { id title subtitle url createdAt issue { id } }
  }
}`

// issueRelations is walked UNFILTERED and matched client-side. Linear exposes
// the connection at the root but not a filter this adapter can rely on, and an
// edge set is small next to an issue set — so the honest move is to walk it and
// keep the edges whose both ends are in scope, which is also what the applier
// does with them.
const relationsQuery = `query Relations($first: Int!, $after: String) {
  issueRelations(first: $first, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes { id type issue { id } relatedIssue { id } }
  }
}`

type statesResponse struct {
	WorkflowStates struct {
		PageInfo pageInfo      `json:"pageInfo"`
		Nodes    []vendorState `json:"nodes"`
	} `json:"workflowStates"`
}

type labelsResponse struct {
	IssueLabels struct {
		PageInfo pageInfo      `json:"pageInfo"`
		Nodes    []vendorLabel `json:"nodes"`
	} `json:"issueLabels"`
}

type usersResponse struct {
	Users struct {
		PageInfo pageInfo     `json:"pageInfo"`
		Nodes    []vendorUser `json:"nodes"`
	} `json:"users"`
}

type cyclesResponse struct {
	Cycles struct {
		PageInfo pageInfo      `json:"pageInfo"`
		Nodes    []vendorCycle `json:"nodes"`
	} `json:"cycles"`
}

type issuesResponse struct {
	Issues struct {
		PageInfo pageInfo      `json:"pageInfo"`
		Nodes    []vendorIssue `json:"nodes"`
	} `json:"issues"`
}

type commentsResponse struct {
	Comments struct {
		PageInfo pageInfo        `json:"pageInfo"`
		Nodes    []vendorComment `json:"nodes"`
	} `json:"comments"`
}

type attachmentsResponse struct {
	Attachments struct {
		PageInfo pageInfo           `json:"pageInfo"`
		Nodes    []vendorAttachment `json:"nodes"`
	} `json:"attachments"`
}

type relationsResponse struct {
	IssueRelations struct {
		PageInfo pageInfo         `json:"pageInfo"`
		Nodes    []vendorRelation `json:"nodes"`
	} `json:"issueRelations"`
}

// Fetch walks the workspace for scope. It honours ctx between pages and records
// in Bundle.Truncated anything it could not reach — including a cap the
// operator themselves set, because "240 issues" when only 200 were fetched is
// the lie this framework exists to prevent.
func (a *Adapter) Fetch(ctx context.Context, scope imports.Scope, progress imports.ProgressFunc) (*imports.Bundle, error) {
	if progress == nil {
		progress = imports.NopProgress
	}
	bundle := &imports.Bundle{Source: imports.Source{Kind: imports.SourceLinear, Ref: a.ref}}

	teams, err := a.Containers(ctx)
	if err != nil {
		return nil, fmt.Errorf("linear: fetch teams: %w", err)
	}
	selected, unknown := selectTeams(teams, scope.Containers)
	for _, name := range unknown {
		// A scope entry the credential cannot see is exactly the kind of
		// silent hole the report must name.
		bundle.AddTruncated("containers", 1, fmt.Sprintf("%q is not a team this API key can see", name))
	}
	bundle.Containers = selected
	if len(selected) == 0 {
		bundle.Warn("this API key can see no Linear teams; check the key's access before importing")
		return bundle, nil
	}
	bundle.Source.Ref = a.ref

	if err := a.fetchStates(ctx, bundle); err != nil {
		return nil, err
	}
	if err := a.fetchLabels(ctx, bundle); err != nil {
		return nil, err
	}
	if err := a.fetchUsers(ctx, bundle); err != nil {
		return nil, err
	}
	if err := a.fetchCycles(ctx, bundle); err != nil {
		return nil, err
	}

	issueIndex, err := a.fetchIssues(ctx, bundle, scope, progress)
	if err != nil {
		return nil, err
	}
	if scope.WantsComments() {
		if err := a.fetchComments(ctx, bundle, issueIndex, progress); err != nil {
			return nil, err
		}
	}
	if scope.WantsAttachments() {
		if err := a.fetchAttachments(ctx, bundle, issueIndex, progress); err != nil {
			return nil, err
		}
	}
	if err := a.fetchRelations(ctx, bundle, issueIndex, progress); err != nil {
		return nil, err
	}
	return bundle, nil
}

// selectTeams resolves the operator's scope entries against the teams the key
// can see. An entry matches a team id, key or name — the assistant may well
// have been told "ENG", and refusing that would be pedantry.
func selectTeams(teams []imports.Container, wanted []string) (selected []imports.Container, unknown []string) {
	if len(wanted) == 0 {
		return teams, nil
	}
	byKey := map[string]imports.Container{}
	for _, t := range teams {
		for _, alias := range []string{t.ExternalID, t.Key, t.Name} {
			if alias != "" {
				byKey[strings.ToLower(alias)] = t
			}
		}
	}
	seen := map[string]bool{}
	for _, name := range wanted {
		t, ok := byKey[strings.ToLower(strings.TrimSpace(name))]
		if !ok {
			unknown = append(unknown, name)
			continue
		}
		if seen[t.ExternalID] {
			continue
		}
		seen[t.ExternalID] = true
		selected = append(selected, t)
	}
	return selected, unknown
}

func (a *Adapter) fetchStates(ctx context.Context, bundle *imports.Bundle) error {
	inScope := containerIDs(bundle.Containers)
	return a.paginate(ctx, func(cursor string) (pageInfo, error) {
		var page statesResponse
		if err := a.client.Query(ctx, statesQuery, a.pageVars(cursor, nil), &page); err != nil {
			return pageInfo{}, fmt.Errorf("linear: fetch workflow states: %w", err)
		}
		for _, s := range page.WorkflowStates.Nodes {
			state := normalizeState(s)
			if state.ContainerID != "" && !inScope[state.ContainerID] {
				continue
			}
			bundle.States = append(bundle.States, state)
		}
		return page.WorkflowStates.PageInfo, nil
	})
}

func (a *Adapter) fetchLabels(ctx context.Context, bundle *imports.Bundle) error {
	return a.paginate(ctx, func(cursor string) (pageInfo, error) {
		var page labelsResponse
		if err := a.client.Query(ctx, labelsQuery, a.pageVars(cursor, nil), &page); err != nil {
			return pageInfo{}, fmt.Errorf("linear: fetch labels: %w", err)
		}
		for _, l := range page.IssueLabels.Nodes {
			bundle.Labels = append(bundle.Labels, normalizeLabel(l))
		}
		return page.IssueLabels.PageInfo, nil
	})
}

func (a *Adapter) fetchUsers(ctx context.Context, bundle *imports.Bundle) error {
	return a.paginate(ctx, func(cursor string) (pageInfo, error) {
		var page usersResponse
		if err := a.client.Query(ctx, usersQuery, a.pageVars(cursor, nil), &page); err != nil {
			return pageInfo{}, fmt.Errorf("linear: fetch users: %w", err)
		}
		for _, u := range page.Users.Nodes {
			bundle.Users = append(bundle.Users, normalizeUser(u))
		}
		return page.Users.PageInfo, nil
	})
}

func (a *Adapter) fetchCycles(ctx context.Context, bundle *imports.Bundle) error {
	inScope := containerIDs(bundle.Containers)
	return a.paginate(ctx, func(cursor string) (pageInfo, error) {
		var page cyclesResponse
		if err := a.client.Query(ctx, cyclesQuery, a.pageVars(cursor, nil), &page); err != nil {
			return pageInfo{}, fmt.Errorf("linear: fetch cycles: %w", err)
		}
		for _, c := range page.Cycles.Nodes {
			it := normalizeCycle(c)
			if it.ContainerID != "" && !inScope[it.ContainerID] {
				continue
			}
			bundle.Iterations = append(bundle.Iterations, it)
		}
		return page.Cycles.PageInfo, nil
	})
}

// fetchIssues is the shallow pass. It returns the id → index map the later
// passes attach their rows through, so no pass has to search the slice.
func (a *Adapter) fetchIssues(
	ctx context.Context,
	bundle *imports.Bundle,
	scope imports.Scope,
	progress imports.ProgressFunc,
) (map[string]int, error) {
	index := map[string]int{}
	filter := issueFilter(bundle.Containers, scope)
	capped := false

	err := a.paginate(ctx, func(cursor string) (pageInfo, error) {
		vars := a.pageVars(cursor, map[string]any{
			"filter":          filter,
			"includeArchived": scope.IncludeArchived,
		})
		var page issuesResponse
		if err := a.client.Query(ctx, issuesQuery, vars, &page); err != nil {
			return pageInfo{}, fmt.Errorf("linear: fetch issues: %w", err)
		}
		for _, v := range page.Issues.Nodes {
			if scope.MaxIssues > 0 && len(bundle.Issues) >= scope.MaxIssues {
				capped = true
				return pageInfo{}, nil
			}
			// Labels ride inline on the issue, so the bundle's label table is
			// completed here for anything the reference pass did not see.
			for _, l := range v.Labels.Nodes {
				if _, known := bundle.LabelByID(l.ID); !known {
					bundle.Labels = append(bundle.Labels, normalizeLabel(l))
				}
			}
			index[v.ID] = len(bundle.Issues)
			bundle.Issues = append(bundle.Issues, normalizeIssue(v))
		}
		progress(imports.Progress{Phase: "fetch", Kind: imports.KindIssue, Done: len(bundle.Issues)})
		return page.Issues.PageInfo, nil
	})
	if err != nil {
		return nil, err
	}
	if capped {
		// The operator asked for a cap, and the report still has to say that
		// the number it quotes is not the whole workspace.
		bundle.AddTruncated("issues", 1, fmt.Sprintf("the run was capped at %d issues", scope.MaxIssues))
	}
	return index, nil
}

// issueFilter builds the IssueFilter. Team ids rather than keys, because ids
// cannot collide and the keys were already resolved in selectTeams.
func issueFilter(containers []imports.Container, scope imports.Scope) map[string]any {
	filter := map[string]any{}
	ids := make([]string, 0, len(containers))
	for _, c := range containers {
		ids = append(ids, c.ExternalID)
	}
	if len(ids) > 0 {
		filter["team"] = map[string]any{"id": map[string]any{"in": ids}}
	}
	if scope.Since != nil && !scope.Since.IsZero() {
		filter["updatedAt"] = map[string]any{"gte": scope.Since.UTC().Format("2006-01-02T15:04:05.000Z")}
	}
	if len(filter) == 0 {
		return nil
	}
	return filter
}

// fetchComments is the comments pass, batched over the issue ids already held.
// Batching keeps the ids list well inside a single query's complexity while
// still costing one request per batch rather than one per issue.
func (a *Adapter) fetchComments(ctx context.Context, bundle *imports.Bundle, index map[string]int, progress imports.ProgressFunc) error {
	done := 0
	return a.eachIssueBatch(index, func(ids []string) error {
		err := a.paginate(ctx, func(cursor string) (pageInfo, error) {
			var page commentsResponse
			if err := a.client.Query(ctx, commentsQuery, a.pageVars(cursor, map[string]any{"ids": ids}), &page); err != nil {
				return pageInfo{}, fmt.Errorf("linear: fetch comments: %w", err)
			}
			for _, c := range page.Comments.Nodes {
				if c.Issue == nil {
					continue
				}
				at, ok := index[c.Issue.ID]
				if !ok {
					continue
				}
				comment, bot, isBot := normalizeComment(c)
				if isBot {
					if _, known := bundle.UserByID(bot.ExternalID); !known {
						bundle.Users = append(bundle.Users, bot)
					}
				}
				bundle.Issues[at].Comments = append(bundle.Issues[at].Comments, comment)
				done++
			}
			progress(imports.Progress{Phase: "fetch", Kind: imports.KindComment, Done: done})
			return page.Comments.PageInfo, nil
		})
		return err
	})
}

func (a *Adapter) fetchAttachments(ctx context.Context, bundle *imports.Bundle, index map[string]int, progress imports.ProgressFunc) error {
	done := 0
	err := a.eachIssueBatch(index, func(ids []string) error {
		return a.paginate(ctx, func(cursor string) (pageInfo, error) {
			var page attachmentsResponse
			if err := a.client.Query(ctx, attachmentsQuery, a.pageVars(cursor, map[string]any{"ids": ids}), &page); err != nil {
				return pageInfo{}, fmt.Errorf("linear: fetch attachments: %w", err)
			}
			for _, att := range page.Attachments.Nodes {
				if att.Issue == nil {
					continue
				}
				at, ok := index[att.Issue.ID]
				if !ok {
					continue
				}
				bundle.Issues[at].Attachments = append(bundle.Issues[at].Attachments, normalizeAttachment(att))
				done++
			}
			progress(imports.Progress{Phase: "fetch", Kind: imports.KindAttachment, Done: done})
			return page.Attachments.PageInfo, nil
		})
	})
	if err != nil {
		return err
	}
	if done > 0 {
		// Honesty about a number the source cannot give: Linear's Attachment
		// carries no size, so the plan's byte total is a floor.
		bundle.Warn("Linear does not report attachment sizes, so the byte total below is a floor, not a measurement")
	}
	return nil
}

// fetchRelations walks every relation and keeps the ones whose BOTH ends are in
// this import. An edge to an issue outside the scope is dropped here rather
// than carried to the applier as a dangling reference.
func (a *Adapter) fetchRelations(ctx context.Context, bundle *imports.Bundle, index map[string]int, progress imports.ProgressFunc) error {
	done, degraded := 0, 0
	err := a.paginate(ctx, func(cursor string) (pageInfo, error) {
		var page relationsResponse
		if err := a.client.Query(ctx, relationsQuery, a.pageVars(cursor, nil), &page); err != nil {
			return pageInfo{}, fmt.Errorf("linear: fetch relations: %w", err)
		}
		for _, rel := range page.IssueRelations.Nodes {
			if rel.Issue == nil || rel.RelatedIssue == nil {
				continue
			}
			at, ok := index[rel.Issue.ID]
			if !ok {
				continue
			}
			if _, inScope := index[rel.RelatedIssue.ID]; !inScope {
				continue
			}
			kind, sourceType := mapRelation(rel.Type)
			if _, exact := imports.DegradeRelation(sourceType); !exact {
				degraded++
			}
			bundle.Issues[at].Relations = append(bundle.Issues[at].Relations, imports.Relation{
				ExternalID:       rel.ID,
				Kind:             kind,
				TargetExternalID: rel.RelatedIssue.ID,
				SourceType:       sourceType,
			})
			done++
		}
		progress(imports.Progress{Phase: "fetch", Kind: imports.KindRelation, Done: done})
		return page.IssueRelations.PageInfo, nil
	})
	if err != nil {
		return err
	}
	if degraded > 0 {
		bundle.Warn("%d Linear relation(s) have no Agora equivalent and were downgraded to \"related\"; the original type is kept on each link", degraded)
	}
	return nil
}

// --- paging plumbing --------------------------------------------------------

// maxIssueBatch bounds how many issue ids go into one filtered query. Well
// inside any plausible argument limit, and small enough that the response page
// itself stays cheap.
const maxIssueBatch = 25

func (a *Adapter) eachIssueBatch(index map[string]int, fn func(ids []string) error) error {
	// Batch in the bundle's own issue order so two runs issue the same
	// requests — a map walk here would make a failure hard to reproduce.
	ordered := make([]string, len(index))
	for id, at := range index {
		if at >= 0 && at < len(ordered) {
			ordered[at] = id
		}
	}
	batch := make([]string, 0, maxIssueBatch)
	for _, id := range ordered {
		if id == "" {
			continue
		}
		batch = append(batch, id)
		if len(batch) == maxIssueBatch {
			if err := fn(batch); err != nil {
				return err
			}
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		return fn(batch)
	}
	return nil
}

// maxPages is a loop guard. Linear's Relay paging is sound, but Atlassian's
// community has ~100k views on a thread about a cursor that "chains endlessly,
// always loading the first page again" — an importer that can spin forever on a
// broken cursor is a worse bug than one that stops and says it stopped.
const maxPages = 2000

// paginate drives one Relay connection to exhaustion, feeding the page size the
// client currently believes is safe and stopping on a cursor that repeats.
func (a *Adapter) paginate(ctx context.Context, fetch func(cursor string) (pageInfo, error)) error {
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < maxPages; page++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := fetch(cursor)
		if err != nil {
			return err
		}
		if !info.HasNextPage || info.EndCursor == "" {
			return nil
		}
		if seen[info.EndCursor] {
			return fmt.Errorf("linear: the cursor %q repeated; stopping rather than looping", info.EndCursor)
		}
		seen[info.EndCursor] = true
		cursor = info.EndCursor
	}
	return fmt.Errorf("linear: stopped after %d pages", maxPages)
}

// pageVars builds the variables every paged query takes, with the page size the
// client chose from the last response's X-Complexity.
func (a *Adapter) pageVars(cursor string, extra map[string]any) map[string]any {
	vars := map[string]any{"first": a.client.PageSize()}
	if cursor != "" {
		vars["after"] = cursor
	}
	for k, v := range extra {
		vars[k] = v
	}
	return vars
}

func containerIDs(containers []imports.Container) map[string]bool {
	out := make(map[string]bool, len(containers))
	for _, c := range containers {
		out[c.ExternalID] = true
	}
	return out
}

// OpenAttachment streams a file out of Linear with the SAME Authorization
// header the GraphQL calls use. Files on uploads.linear.app are NOT public, and
// handing a signed URL to a browser would be a leak — so the applier downloads
// server-side and the token never leaves this process.
//
// (Linear also supports a `public-file-urls-expire-in` request header that
// returns pre-signed, time-limited URLs. That is the right mechanism if we ever
// hand a URL to a browser; it is not needed here.)
func (a *Adapter) OpenAttachment(ctx context.Context, att imports.Attachment) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, att.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("linear: attachment request: %w", err)
	}
	req.Header.Set("Authorization", a.client.apiKey)
	resp, err := a.client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("linear: download %q: %w", att.Title, err)
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, fmt.Errorf("linear: download %q: http %d", att.Title, resp.StatusCode)
	}
	return resp.Body, nil
}
