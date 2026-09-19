// Vendor structs and their translation into the canonical shape.
//
// This is the only file that knows what Linear's JSON looks like. Everything it
// emits is imports.* and nothing it emits touches a database, which is what
// makes the recorded fixture in linear_test.go a complete test of the adapter.
//
// Two mappings are settled here rather than configured:
//
//   - PRIORITY IS A BIJECTION. Linear's schema documents its own integer
//     meaning — "0 = No priority, 1 = Urgent, 2 = High, 3 = Medium, 4 = Low" —
//     and the canonical ladder was defined to be exactly that, so the map is
//     the identity and no operator ever has to think about it.
//   - STATUS IS CATEGORY-FIRST. WorkflowState.type is one of six stable values
//     across every Linear workspace; the state NAME is whatever that team
//     called its column. So the category picks the bucket and the name only
//     refines WITHIN it — which is how "In Review" (a `started` state) reaches
//     in_review rather than flattening to in_progress.
//
// What Linear does NOT have is as important: no custom-field system at all, so
// the single ugliest part of a Jira import does not exist here.
package linear

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/imports"
)

// --- vendor structs ---------------------------------------------------------

type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type idRef struct {
	ID string `json:"id"`
}

type nodeRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type vendorTeam struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Archived    string `json:"archivedAt"`
}

type vendorState struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Position float64 `json:"position"`
	Team     *idRef  `json:"team"`
}

type vendorLabel struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type vendorUser struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Active bool   `json:"active"`
}

type vendorCycle struct {
	ID          string `json:"id"`
	Number      int    `json:"number"`
	Name        string `json:"name"`
	StartsAt    string `json:"startsAt"`
	EndsAt      string `json:"endsAt"`
	CompletedAt string `json:"completedAt"`
	Team        *idRef `json:"team"`
}

type vendorIssue struct {
	ID          string   `json:"id"`
	Identifier  string   `json:"identifier"`
	Number      float64  `json:"number"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Priority    *int     `json:"priority"`
	Estimate    *float64 `json:"estimate"`
	URL         string   `json:"url"`
	CreatedAt   string   `json:"createdAt"`
	UpdatedAt   string   `json:"updatedAt"`
	StartedAt   string   `json:"startedAt"`
	CompletedAt string   `json:"completedAt"`
	DueDate     string   `json:"dueDate"`
	ArchivedAt  string   `json:"archivedAt"`

	State    *idRef   `json:"state"`
	Team     *idRef   `json:"team"`
	Cycle    *idRef   `json:"cycle"`
	Parent   *idRef   `json:"parent"`
	Assignee *idRef   `json:"assignee"`
	Creator  *idRef   `json:"creator"`
	Project  *nodeRef `json:"project"`

	Labels struct {
		Nodes []vendorLabel `json:"nodes"`
	} `json:"labels"`
}

type vendorComment struct {
	ID        string   `json:"id"`
	Body      string   `json:"body"`
	URL       string   `json:"url"`
	CreatedAt string   `json:"createdAt"`
	UpdatedAt string   `json:"updatedAt"`
	Issue     *idRef   `json:"issue"`
	Parent    *idRef   `json:"parent"`
	User      *idRef   `json:"user"`
	BotActor  *nodeRef `json:"botActor"`
}

type vendorAttachment struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Subtitle  string `json:"subtitle"`
	URL       string `json:"url"`
	CreatedAt string `json:"createdAt"`
	Issue     *idRef `json:"issue"`
}

type vendorRelation struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Issue        *idRef `json:"issue"`
	RelatedIssue *idRef `json:"relatedIssue"`
}

// --- mapping defaults -------------------------------------------------------

// Linear's WorkflowState.type values. Six, stable across every workspace, which
// is exactly why the mapping leads with them.
const (
	stateTriage    = "triage"
	stateBacklog   = "backlog"
	stateUnstarted = "unstarted"
	stateStarted   = "started"
	stateCompleted = "completed"
	stateCanceled  = "canceled"
)

// Defaults is the adapter's contribution to the mapping table.
//
// `triage` maps to todo rather than backlog deliberately: a Linear team using
// triage puts NEW, UNSORTED work there, and burying it in the backlog on day
// one of a migration is how a team loses the inbox they were relying on.
func Defaults() imports.Defaults {
	return imports.Defaults{
		StatusByCategory: map[string]string{
			stateTriage:    imports.StatusTodo,
			stateBacklog:   imports.StatusBacklog,
			stateUnstarted: imports.StatusTodo,
			stateStarted:   imports.StatusInProgress,
			stateCompleted: imports.StatusDone,
			stateCanceled:  imports.StatusCancelled,
		},
		// Refinements apply only WITHIN the `started` bucket, because that is
		// the only Linear category that covers several Agora columns. Order
		// matters: review before blocked, so "Blocked in review" lands on the
		// more specific one.
		StatusRefine: map[string][]imports.Refinement{
			stateStarted: {
				{Keywords: []string{"review", "qa", "testing", "verif"}, Status: imports.StatusInReview},
				{Keywords: []string{"blocked", "on hold", "waiting"}, Status: imports.StatusBlocked},
			},
		},
		Fallback: imports.StatusTodo,
	}
}

// mapPriority turns Linear's integer into the canonical ladder. Out-of-range
// values (a sixth priority Linear has not shipped) land on none rather than
// dropping the issue — enum drift downgrades, it never crashes.
func mapPriority(p *int) imports.Priority {
	if p == nil {
		return imports.PriorityNone
	}
	switch *p {
	case 1:
		return imports.PriorityUrgent
	case 2:
		return imports.PriorityHigh
	case 3:
		return imports.PriorityMedium
	case 4:
		return imports.PriorityLow
	}
	return imports.PriorityNone
}

// mapRelation translates IssueRelationType. Linear has four values and
// issue_dependency.type admits four, but they are not the SAME four: Linear's
// `similar` has no Agora equivalent and degrades to `related` with its own name
// preserved on the edge (migration 207's comment says so).
func mapRelation(t string) (imports.RelationKind, string) {
	kind, _ := imports.DegradeRelation(t)
	return kind, strings.TrimSpace(t)
}

// --- normalization ----------------------------------------------------------

func normalizeTeam(t vendorTeam) imports.Container {
	return imports.Container{
		ExternalID:  t.ID,
		Key:         t.Key,
		Name:        t.Name,
		Description: t.Description,
		Icon:        t.Icon,
		Archived:    strings.TrimSpace(t.Archived) != "",
	}
}

func normalizeState(s vendorState) imports.State {
	state := imports.State{
		ExternalID: s.ID,
		Name:       s.Name,
		Category:   s.Type,
		Position:   s.Position,
	}
	if s.Team != nil {
		state.ContainerID = s.Team.ID
	}
	return state
}

func normalizeLabel(l vendorLabel) imports.Label {
	return imports.Label{ExternalID: l.ID, Name: l.Name, Color: l.Color}
}

func normalizeUser(u vendorUser) imports.User {
	return imports.User{
		ExternalID: u.ID,
		Name:       u.Name,
		Email:      u.Email,
		Active:     u.Active,
	}
}

// normalizeCycle turns a cycle into an iteration. Linear cycles are often
// unnamed and identified by number, so an empty name becomes "Cycle <n>" rather
// than an anonymous sprint nobody can find.
func normalizeCycle(c vendorCycle) imports.Iteration {
	it := imports.Iteration{
		ExternalID: c.ID,
		Name:       strings.TrimSpace(c.Name),
		StartsAt:   parseTimePtr(c.StartsAt),
		EndsAt:     parseTimePtr(c.EndsAt),
		Completed:  strings.TrimSpace(c.CompletedAt) != "",
	}
	if it.Name == "" && c.Number > 0 {
		it.Name = "Cycle " + strconv.Itoa(c.Number)
	}
	if c.Team != nil {
		it.ContainerID = c.Team.ID
	}
	return it
}

func normalizeIssue(v vendorIssue) imports.Issue {
	issue := imports.Issue{
		ExternalID:   v.ID,
		Identifier:   v.Identifier,
		URL:          v.URL,
		Title:        v.Title,
		BodyMarkdown: v.Description, // Linear bodies are already Markdown.
		Priority:     mapPriority(v.Priority),
		Estimate:     v.Estimate,
		CreatedAt:    parseTime(v.CreatedAt),
		UpdatedAt:    parseTime(v.UpdatedAt),
		StartedAt:    parseTimePtr(v.StartedAt),
		CompletedAt:  parseTimePtr(v.CompletedAt),
		DueDate:      parseTimePtr(v.DueDate),
		Archived:     strings.TrimSpace(v.ArchivedAt) != "",
	}
	if v.State != nil {
		issue.StateID = v.State.ID
	}
	if v.Team != nil {
		issue.ContainerID = v.Team.ID
	}
	if v.Cycle != nil {
		issue.IterationID = v.Cycle.ID
	}
	if v.Parent != nil {
		issue.ParentID = v.Parent.ID
	}
	if v.Assignee != nil {
		issue.AssigneeID = v.Assignee.ID
	}
	if v.Creator != nil {
		issue.CreatorID = v.Creator.ID
	}
	for _, l := range v.Labels.Nodes {
		issue.LabelIDs = append(issue.LabelIDs, l.ID)
	}
	// A Linear PROJECT is not modelled as an Agora container in Phase 1 (the
	// team is — it owns the identifier prefix and the cycles). Rather than
	// dropping it, it rides in Raw, which is exactly what Raw is for.
	if v.Project != nil && v.Project.ID != "" {
		if blob, err := json.Marshal(map[string]any{
			"linear_project": map[string]string{"id": v.Project.ID, "name": v.Project.Name},
		}); err == nil {
			issue.Raw = blob
		}
	}
	return issue
}

func normalizeComment(c vendorComment) (imports.Comment, imports.User, bool) {
	comment := imports.Comment{
		ExternalID:   c.ID,
		BodyMarkdown: c.Body,
		URL:          c.URL,
		CreatedAt:    parseTime(c.CreatedAt),
		UpdatedAt:    parseTime(c.UpdatedAt),
	}
	if c.Parent != nil {
		comment.ParentID = c.Parent.ID
	}
	switch {
	case c.User != nil && c.User.ID != "":
		comment.AuthorID = c.User.ID
	case c.BotActor != nil && c.BotActor.ID != "":
		// An integration wrote this. It gets a synthesized bundle user marked
		// Bot so the resolver sends it to the attribution identity instead of
		// matching a human who happens to share the integration's address.
		comment.AuthorID = "bot:" + c.BotActor.ID
		return comment, imports.User{
			ExternalID: comment.AuthorID,
			Name:       c.BotActor.Name,
			Bot:        true,
		}, true
	}
	return comment, imports.User{}, false
}

// normalizeAttachment carries what Linear exposes and no more. Note the
// omission: Linear's Attachment has NO size field, so the plan's byte total for
// a Linear import is a floor rather than a measurement. The adapter says so in
// a bundle warning rather than letting the report imply a number it does not
// have.
func normalizeAttachment(a vendorAttachment) imports.Attachment {
	title := strings.TrimSpace(a.Title)
	if title == "" {
		title = strings.TrimSpace(a.Subtitle)
	}
	if title == "" {
		title = a.URL
	}
	return imports.Attachment{
		ExternalID: a.ID,
		Title:      title,
		URL:        a.URL,
		CreatedAt:  parseTime(a.CreatedAt),
	}
}

// --- small helpers ----------------------------------------------------------

// parseTime accepts the ISO-8601 shapes Linear returns. An unparseable or empty
// value yields the zero time, which the applier replaces with a sensible
// default rather than refusing the row: losing an issue over a timestamp is a
// worse outcome than losing the timestamp.
func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func parseTimePtr(s string) *time.Time {
	t := parseTime(s)
	if t.IsZero() {
		return nil
	}
	return &t
}
