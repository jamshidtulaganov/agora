// The dry run (docs/importers-plan.md §3.1, §3.4, §3.6).
//
// This stage runs everything the real import runs — the normalize output, the
// mapping, the identity resolution — with ZERO WRITES, and produces the
// ImportPlan. The plan is not a progress bar's preamble; it IS the product.
// It is the artifact the assistant shows, the operator argues with, and the
// thing frozen into import_job.plan so the receipt can be reconciled against
// what was authorized.
//
// Three properties it must have, each one a trust failure if missing:
//
//   - It reports the CREATE/UPDATE split, computed against rows already linked
//     to this source. "Re-import after we finish the sprint in Linear" is a
//     supported workflow, and the operator has to be able to see that it is
//     240 updates and 3 creates rather than 243 duplicates.
//   - It reports attachments in BYTES, not just counts, and it carries the one
//     honest warning about them: a file Agora does not copy becomes unreachable
//     the day the team cancels their Linear subscription.
//   - It never quotes a number it cannot produce. Exact says whether the counts
//     are counted or estimated; Truncated says what the credential could not
//     reach. A comfortable number here is the most tempting lie in the product.
package imports

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// ExistingIssue is one row this source already put in the workspace, as
// ListIssuesByExternalSource returns it. The create/update split is computed
// from a single query rather than N per-issue lookups.
type ExistingIssue struct {
	ID     string `json:"id"`
	Number int32  `json:"number"`
}

// CountPlan is the create/update split for one entity kind.
type CountPlan struct {
	Create int `json:"create"`
	Update int `json:"update"`
	Total  int `json:"total"`
}

// ContainerPlan is one source container and what will happen to it: a new Agora
// project, or issues landing in a project the operator pinned it to.
type ContainerPlan struct {
	ExternalID string `json:"external_id"`
	Key        string `json:"key,omitempty"`
	Name       string `json:"name"`
	Issues     int    `json:"issues"`
	ProjectID  string `json:"project_id,omitempty"` // set when pinned to an existing project
	Action     string `json:"action"`               // create_project | link_project
}

// LabelPlan is one source label and whether it already exists by name.
type LabelPlan struct {
	Name   string `json:"name"`
	Color  string `json:"color,omitempty"`
	Issues int    `json:"issues"`
}

// AttachmentPlan reports the file budget: what would be copied, how much of it
// there is, and what falls outside the caps and will therefore be listed rather
// than fetched.
type AttachmentPlan struct {
	Count   int   `json:"count"`
	Bytes   int64 `json:"bytes"`
	Skipped int   `json:"skipped"`
	// SkippedReasons is bounded and deduplicated: "12 over the 25 MB per-file
	// cap", not twelve lines.
	SkippedReasons []string `json:"skipped_reasons,omitempty"`
	// Unsupported is set when nothing will be copied at all because the
	// adapter cannot stream files or no sink is wired. Better an explicit
	// "none of them" than a silent zero.
	Unsupported bool `json:"unsupported,omitempty"`
}

// RelationPlan reports the edges, and how many of them had to degrade because
// the source's vocabulary is wider than issue_dependency.type.
type RelationPlan struct {
	Total int `json:"total"`
	// Degraded counts edges whose source type has no Agora equivalent and
	// landed on `related` with the original name preserved.
	Degraded int `json:"degraded"`
	// Dangling counts edges pointing at an issue outside this scope. They are
	// skipped, not errors: importing one team of a five-team workspace
	// legitimately cuts edges.
	Dangling int `json:"dangling"`
}

// Plan is the dry-run artifact.
type Plan struct {
	Source      Source    `json:"source"`
	Scope       Scope     `json:"scope"`
	GeneratedAt time.Time `json:"generated_at"`

	Containers []ContainerPlan `json:"containers"`
	Iterations int             `json:"iterations"`
	Labels     []LabelPlan     `json:"labels,omitempty"`

	Issues      CountPlan      `json:"issues"`
	Comments    CountPlan      `json:"comments"`
	Attachments AttachmentPlan `json:"attachments"`
	Relations   RelationPlan   `json:"relations"`

	Statuses         []StatusMapping   `json:"statuses"`
	UnmappedStatuses []UnmappedStatus  `json:"unmapped_statuses,omitempty"`
	Priorities       []PriorityMapping `json:"priorities"`

	Users          []UserPlan `json:"users"`
	UnmatchedUsers int        `json:"unmatched_users"`

	// Exact is false when any headline count is an estimate rather than a
	// count — Jira's search API no longer returns a total, so its plan must
	// say so rather than quoting a number the API cannot produce.
	Exact     bool           `json:"exact"`
	Truncated map[string]int `json:"truncated,omitempty"`
	Warnings  []string       `json:"warnings,omitempty"`
}

// AttachmentWarning is the sentence §3.6 requires the report to carry, in bold,
// every time. It is not boilerplate: it is the one consequence of this decision
// the operator cannot discover later.
const AttachmentWarning = "**Files Agora does not copy stay only in the source.** " +
	"If the team cancels their subscription, every attachment listed as skipped becomes unreachable."

// BuildPlan walks a normalized bundle and produces the plan. It writes nothing:
// the resolver it is given is in dry-run mode, and no Store is reachable from
// here at all.
//
// existing is the set of issues this source already linked in the workspace,
// keyed by source id — the caller runs ListIssuesByExternalSource once and
// passes the result, so this stays a pure function over data.
func BuildPlan(
	ctx context.Context,
	b *Bundle,
	m *Mapping,
	resolver *ActorResolver,
	existing map[string]ExistingIssue,
	scope Scope,
	limits Limits,
) (*Plan, error) {
	if b == nil {
		return nil, fmt.Errorf("imports: cannot plan a nil bundle")
	}
	limits = limits.withDefaults()

	plan := &Plan{
		Source:      b.Source,
		Scope:       scope,
		GeneratedAt: time.Now().UTC(),
		Exact:       true,
		Truncated:   b.Truncated,
		Warnings:    append([]string(nil), b.Warnings...),
		Iterations:  len(b.Iterations),
	}
	// Anything the adapter could not reach makes the headline count an
	// estimate, whatever else it claims.
	if len(b.Truncated) > 0 {
		plan.Exact = false
	}

	issuesPerContainer := map[string]int{}
	labelIssues := map[string]int{}
	seenIssue := map[string]bool{}

	for i := range b.Issues {
		issue := &b.Issues[i]
		if issue.ExternalID == "" {
			plan.Warnings = append(plan.Warnings, "an issue arrived with no source id and will be skipped")
			continue
		}
		if seenIssue[issue.ExternalID] {
			// The same issue twice in one bundle is an adapter paging bug. It
			// would be idempotent anyway (the second write is an update), but
			// the operator should know the count is soft.
			plan.Exact = false
			plan.Warn("the source returned issue %s more than once; counts are approximate", issue.Identifier)
			continue
		}
		seenIssue[issue.ExternalID] = true

		plan.Issues.Total++
		if _, linked := existing[issue.ExternalID]; linked {
			plan.Issues.Update++
		} else {
			plan.Issues.Create++
		}
		if issue.ContainerID != "" {
			issuesPerContainer[issue.ContainerID]++
		}

		// Touch the status mapping for every issue so Mapping.Unmapped()
		// counts issues rather than distinct states.
		if state, ok := b.StateByID(issue.StateID); ok {
			m.Status(state)
		} else if issue.StateID != "" {
			m.Status(State{ExternalID: issue.StateID, Name: issue.StateID})
		}

		for _, labelID := range issue.LabelIDs {
			if label, ok := b.LabelByID(labelID); ok {
				labelIssues[label.Name]++
			}
		}

		planComments(plan, issue, existing)
		planAttachments(plan, issue, limits)
		planRelations(plan, issue, seenIssueLookup(b))
	}

	plan.Containers = planContainers(b, m, issuesPerContainer)
	plan.Labels = planLabels(b, labelIssues)
	plan.Statuses = m.StatusTable(b.States)
	plan.UnmappedStatuses = m.Unmapped()
	plan.Priorities = m.PriorityTable()

	if resolver != nil {
		plan.Users = resolver.Preview(ctx, b.Users)
		for _, u := range plan.Users {
			if !u.Matched() {
				plan.UnmatchedUsers++
			}
		}
	}
	if plan.UnmatchedUsers > 0 {
		plan.Warn("%d source user(s) could not be matched to a member; their rows will be attributed to %s, never to you",
			plan.UnmatchedUsers, ImportIdentityName(b.Source.Kind))
	}
	if len(plan.UnmappedStatuses) > 0 {
		plan.Warn("%d source state(s) have no mapping and will land on %q; assign them before confirming",
			len(plan.UnmappedStatuses), StatusTodo)
	}
	if plan.Attachments.Count > 0 || plan.Attachments.Skipped > 0 {
		plan.Warnings = append(plan.Warnings, AttachmentWarning)
	}

	return plan, nil
}

// Warn appends a formatted warning, deduplicated.
func (p *Plan) Warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	for _, existing := range p.Warnings {
		if existing == msg {
			return
		}
	}
	p.Warnings = append(p.Warnings, msg)
}

// seenIssueLookup builds the in-scope id set once per plan, so the relations
// pass can tell "points outside this import" from "points at nothing".
func seenIssueLookup(b *Bundle) map[string]bool {
	ids := make(map[string]bool, len(b.Issues))
	for i := range b.Issues {
		ids[b.Issues[i].ExternalID] = true
	}
	return ids
}

func planComments(plan *Plan, issue *Issue, existing map[string]ExistingIssue) {
	_, linked := existing[issue.ExternalID]
	for range issue.Comments {
		plan.Comments.Total++
		// A comment on an issue we have never seen is unambiguously a create.
		// On a linked issue it is an upsert whose side we cannot know without
		// a per-comment lookup the dry run refuses to pay for; it is reported
		// as an update, which is the honest description of "upsert".
		if linked {
			plan.Comments.Update++
		} else {
			plan.Comments.Create++
		}
	}
}

func planAttachments(plan *Plan, issue *Issue, limits Limits) {
	// The per-issue cap is counted here rather than derived, because "the
	// eleventh file on this issue" and "the file that pushed the import over
	// its total" are different skip reasons and the operator needs to be told
	// which one they hit.
	accepted := 0
	for _, att := range issue.Attachments {
		switch {
		case limits.MaxAttachmentsPerIssue > 0 && accepted >= limits.MaxAttachmentsPerIssue:
			plan.Attachments.Skipped++
			plan.addSkipReason(fmt.Sprintf("over the per-issue cap of %d files", limits.MaxAttachmentsPerIssue))
		case limits.MaxAttachmentBytes > 0 && att.Size > limits.MaxAttachmentBytes:
			plan.Attachments.Skipped++
			plan.addSkipReason(fmt.Sprintf("over the per-file cap of %s", humanBytes(limits.MaxAttachmentBytes)))
		case limits.MaxTotalAttachmentBytes > 0 &&
			plan.Attachments.Bytes+att.Size > limits.MaxTotalAttachmentBytes:
			plan.Attachments.Skipped++
			plan.addSkipReason(fmt.Sprintf("over the per-import cap of %s", humanBytes(limits.MaxTotalAttachmentBytes)))
		default:
			accepted++
			plan.Attachments.Count++
			plan.Attachments.Bytes += att.Size
		}
	}
}

func planRelations(plan *Plan, issue *Issue, inScope map[string]bool) {
	for _, rel := range issue.Relations {
		plan.Relations.Total++
		if rel.SourceType != "" {
			if _, exact := DegradeRelation(rel.SourceType); !exact {
				plan.Relations.Degraded++
			}
		}
		if rel.TargetExternalID == "" || !inScope[rel.TargetExternalID] {
			plan.Relations.Dangling++
		}
	}
}

func planContainers(b *Bundle, m *Mapping, issues map[string]int) []ContainerPlan {
	out := make([]ContainerPlan, 0, len(b.Containers))
	for _, c := range b.Containers {
		row := ContainerPlan{
			ExternalID: c.ExternalID,
			Key:        c.Key,
			Name:       c.Name,
			Issues:     issues[c.ExternalID],
			Action:     "create_project",
		}
		if projectID, ok := m.ContainerProject(c); ok {
			row.ProjectID = projectID
			row.Action = "link_project"
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func planLabels(b *Bundle, issues map[string]int) []LabelPlan {
	out := make([]LabelPlan, 0, len(b.Labels))
	for _, l := range b.Labels {
		out = append(out, LabelPlan{Name: l.Name, Color: l.Color, Issues: issues[l.Name]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (p *Plan) addSkipReason(reason string) {
	for _, existing := range p.Attachments.SkippedReasons {
		if existing == reason {
			return
		}
	}
	if len(p.Attachments.SkippedReasons) >= 8 {
		return // bounded: the report is for a human, not a log
	}
	p.Attachments.SkippedReasons = append(p.Attachments.SkippedReasons, reason)
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.0f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
