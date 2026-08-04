package handler

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/service"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// Design decomposition turns an APPROVED design proposal into real sub-issues,
// server-side and deterministically (not via an agent prompt). Each child
// carries the design context in its description + flat primitive metadata keys
// so the dependency machinery is reliable:
//   - design_proposal_comment_id (string) — the source proposal comment
//   - design_plan_index (number) — its index in the proposal's sub_issues
//   - design_depends_on (string) — comma-joined effective sibling indices
//
// Dependents are created in `backlog` and promoted to `todo` by
// promoteDesignDependents when their prerequisites finish.

// designMetaKeyCommentID / PlanIndex / DependsOn are the three flat metadata
// keys design decomposition stamps (V1 contract: primitive scalars only).
const (
	designMetaKeyCommentID = "design_proposal_comment_id"
	designMetaKeyPlanIndex = "design_plan_index"
	designMetaKeyDependsOn = "design_depends_on"
)

// childDesignPlan is a child's parsed design-decomposition metadata.
type childDesignPlan struct {
	commentID string
	planIndex int
	present   bool // this child carries design metadata at all
}

func childDesignPlanOf(issue db.Issue) childDesignPlan {
	cid := metaString(issue.Metadata, designMetaKeyCommentID)
	if cid == "" {
		return childDesignPlan{}
	}
	return childDesignPlan{commentID: cid, planIndex: metaInt(issue.Metadata, designMetaKeyPlanIndex), present: true}
}

// metaInt reads a numeric metadata key. JSON numbers decode to float64.
func metaInt(raw []byte, key string) int {
	switch v := parseIssueMetadata(raw)[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

// effectiveDesignPlan is one sub-issue slice after applying reviewer overrides.
type effectiveDesignPlan struct {
	index       int // original index in proposal.sub_issues
	title       string
	description string
	dependsOn   []int // original depends_on minus excluded siblings
	sub         service.DesignProposalSubIssue
}

// buildEffectiveDesignPlan applies the reviewer's overrides to the proposal's
// sub_issues: drops excluded indices, applies title/description edits, and
// prunes excluded indices out of every surviving sibling's depends_on.
func buildEffectiveDesignPlan(proposal service.DesignProposal, overrides []designSubIssueOverride) []effectiveDesignPlan {
	// Index overrides by their target index; default include=true when a
	// sub-issue has no override entry.
	byIndex := map[int]designSubIssueOverride{}
	for _, o := range overrides {
		byIndex[o.Index] = o
	}
	excluded := map[int]bool{}
	for i := range proposal.SubIssues {
		if o, ok := byIndex[i]; ok && !o.Include {
			excluded[i] = true
		}
	}
	var out []effectiveDesignPlan
	for i, sub := range proposal.SubIssues {
		if excluded[i] {
			continue
		}
		title := sub.Title
		desc := sub.Description
		if o, ok := byIndex[i]; ok {
			if o.Title != nil {
				title = *o.Title
			}
			if o.Description != nil {
				desc = *o.Description
			}
		}
		// Keep only VALID dependency indices: in range, not self-referential,
		// and not excluded. An agent-emitted out-of-range / self / phantom index
		// would otherwise be stamped into design_depends_on and, since it can
		// never enter the done set, strand the child in backlog forever (there
		// is no manual recovery — the squad skill forbids flipping design
		// sub-issue status by hand). Dropping such indices makes the child
		// promotable on its real prerequisites.
		var deps []int
		for _, d := range sub.DependsOn {
			if d < 0 || d >= len(proposal.SubIssues) || d == i || excluded[d] {
				continue
			}
			deps = append(deps, d)
		}
		out = append(out, effectiveDesignPlan{index: i, title: title, description: desc, dependsOn: deps, sub: sub})
	}
	return out
}

// designDecompositionPreflight inspects existing children and reports whether an
// approve may proceed:
//   - "" — proceed (fresh, or a resumable partial run for THIS proposal)
//   - "already_decomposed" — every effective plan index already exists for this
//     proposal comment
//   - "previous_decomposition_exists" — children exist from a DIFFERENT proposal
//     comment (a revised proposal), and supersede was not requested
func (h *Handler) designDecompositionPreflight(ctx context.Context, issue db.Issue, sourceCommentID string, plan []effectiveDesignPlan, supersede bool) string {
	children, err := h.Queries.ListChildIssues(ctx, issue.ID)
	if err != nil {
		return "" // best-effort: let the create loop's idempotency handle it
	}
	hasOther := false
	present := map[int]bool{}
	for _, c := range children {
		cp := childDesignPlanOf(c)
		if !cp.present {
			continue
		}
		if cp.commentID == sourceCommentID {
			present[cp.planIndex] = true
		} else {
			hasOther = true
		}
	}
	if hasOther && !supersede {
		return "previous_decomposition_exists"
	}
	// All effective indices already created for this proposal → nothing to do.
	if len(plan) > 0 {
		allPresent := true
		for _, p := range plan {
			if !present[p.index] {
				allPresent = false
				break
			}
		}
		if allPresent {
			return "already_decomposed"
		}
	}
	return ""
}

// decomposeApprovedProposal creates the sub-issues for an approved proposal.
// Idempotent: skips any plan index already created for this proposal comment,
// so a re-approve resumes a partially failed run. Best-effort + logged — the
// approval itself already committed.
func (h *Handler) decomposeApprovedProposal(r *http.Request, issue db.Issue, userID string, proposal service.DesignProposal, sourceCommentID pgtype.UUID, overrides []designSubIssueOverride) {
	ctx := r.Context()
	commentIDStr := uuidToString(sourceCommentID)
	plan := buildEffectiveDesignPlan(proposal, overrides)
	if len(plan) == 0 {
		return
	}

	// Which indices already exist for this proposal (resume support).
	existing := map[int]bool{}
	if children, err := h.Queries.ListChildIssues(ctx, issue.ID); err == nil {
		for _, c := range children {
			if cp := childDesignPlanOf(c); cp.present && cp.commentID == commentIDStr {
				existing[cp.planIndex] = true
			}
		}
	}

	prefix := h.getIssuePrefix(ctx, issue.WorkspaceID)
	parentKey := prefix + "-" + strconv.Itoa(int(issue.Number))
	created := 0
	for _, p := range plan {
		if existing[p.index] {
			continue
		}
		// A child with unmet dependencies parks in backlog; a root child starts
		// in todo so assigned agents pick it up immediately.
		status := "todo"
		if len(p.dependsOn) > 0 {
			status = "backlog"
		}
		description := composeDesignChildDescription(p, proposal, parentKey)

		metadata := map[string]any{
			designMetaKeyCommentID: commentIDStr,
			designMetaKeyPlanIndex: p.index,
		}
		if len(p.dependsOn) > 0 {
			metadata[designMetaKeyDependsOn] = joinInts(p.dependsOn)
		}

		params := service.IssueCreateParams{
			WorkspaceID:   issue.WorkspaceID,
			Title:         p.title,
			Description:   pgtype.Text{String: description, Valid: true},
			Status:        status,
			Priority:      "none",
			CreatorType:   "member",
			CreatorID:     parseUUID(userID),
			ParentIssueID: issue.ID,
			ProjectID:     issue.ProjectID,
			Metadata:      metadata,
			// Sub-issues of the same title are legitimately distinct plan slices;
			// dedup is handled by our design_plan_index check above.
			AllowDuplicate: true,
		}
		// Inherit the parent's assignee so the existing per-child squad/agent
		// enqueue triggers fire (squad leader delegates; agent picks up todo).
		if issue.AssigneeType.Valid && issue.AssigneeID.Valid &&
			(issue.AssigneeType.String == "squad" || issue.AssigneeType.String == "agent") {
			params.AssigneeType = issue.AssigneeType
			params.AssigneeID = issue.AssigneeID
		}

		// BroadcastPayload carries the FULL child so the issue:created WS event
		// lands in every client's cache — without it publishIssueCreated emits a
		// bare {issue_id}, the frontend handler early-returns on the missing
		// `issue`, and the parent's sub-issues panel stays empty until refresh.
		opts := service.IssueCreateOpts{
			ActorID: userID,
			BroadcastPayload: func(iss db.Issue, _ []db.Attachment) map[string]any {
				return map[string]any{"issue": issueToResponse(iss, prefix)}
			},
		}
		if _, err := h.IssueService.Create(ctx, params, opts); err != nil {
			slog.Warn("design decompose: create child failed", "error", err, "parent_id", uuidToString(issue.ID), "plan_index", p.index)
			continue
		}
		created++
	}

	if created == 0 {
		return
	}
	h.TaskService.RecordDesignReviewActivity(ctx, issue, parseUUID(userID), "design_decomposition_completed", map[string]any{
		"source_comment_id": commentIDStr,
		"created":           created,
	})
	h.postDesignSystemComment(r, issue, fmt.Sprintf("🧩 Decomposed into %d sub-issue(s) from the approved design proposal.", created))
	slog.Info("design decomposition completed", "parent_id", uuidToString(issue.ID), "created", created)
}

// composeDesignChildDescription builds the child's "Design context" section:
// the reviewer-approved slice description, the Figma links (with node-ids) for
// its screens, the applicable component verdicts, and a pointer to the parent.
func composeDesignChildDescription(p effectiveDesignPlan, proposal service.DesignProposal, parentKey string) string {
	var b strings.Builder
	if p.description != "" {
		b.WriteString(p.description)
		b.WriteString("\n\n")
	}
	b.WriteString("## Design context\n\n")
	b.WriteString(fmt.Sprintf("From the approved design proposal on parent %s.\n", parentKey))

	// Figma links: match the sub-issue's node_ids against the proposal's figma refs.
	nodeWanted := map[string]bool{}
	for _, n := range p.sub.NodeIDs {
		nodeWanted[n] = true
	}
	var links []string
	for _, f := range proposal.Figma {
		if len(nodeWanted) == 0 || nodeWanted[f.NodeID] {
			if f.URL != "" {
				links = append(links, "- "+f.URL)
			}
		}
	}
	if len(links) > 0 {
		b.WriteString("\n**Figma:**\n")
		b.WriteString(strings.Join(links, "\n"))
		b.WriteString("\n")
	}

	// Component verdicts that apply to this slice's screens.
	screenWanted := map[string]bool{}
	for _, s := range p.sub.Screens {
		screenWanted[s] = true
	}
	var verdicts []string
	for _, c := range proposal.Components {
		// Include a verdict when it targets one of this slice's screens, or when
		// the slice named no screens (then all component decisions are relevant).
		if len(screenWanted) == 0 || (c.FigmaNodeID != "" && screenWanted[c.FigmaNodeID]) {
			line := "- **" + c.Verdict + "** " + c.Name
			if c.CodeRef != "" {
				line += " (`" + c.CodeRef + "`)"
			}
			verdicts = append(verdicts, line)
		}
	}
	if len(verdicts) > 0 {
		b.WriteString("\n**Components:**\n")
		b.WriteString(strings.Join(verdicts, "\n"))
		b.WriteString("\n")
	}
	b.WriteString("\nDo not restyle shared components; QA will compare your result against these frames.\n")
	return b.String()
}

func joinInts(nums []int) string {
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}

// promoteDesignDependents runs when a design-decomposed child transitions into
// done: it promotes every `backlog` sibling whose design_depends_on is now
// fully satisfied to `todo`, enqueues its agent/squad-leader task, and
// publishes issue:updated. This lives OUTSIDE notifyParentOfChildDone's
// member-assignee / parent-status early returns because a Bitrix epic parent is
// typically human-assigned — promotion must fire regardless of who owns the
// parent. Gated only by the approval itself (no env flag).
func (h *Handler) promoteDesignDependents(ctx context.Context, prev, child db.Issue, actorType, actorID string) {
	if prev.Status == "done" || child.Status != "done" {
		return
	}
	cp := childDesignPlanOf(child)
	if !cp.present || !child.ParentIssueID.Valid {
		return
	}
	siblings, err := h.Queries.ListChildIssues(ctx, child.ParentIssueID)
	if err != nil {
		return
	}
	// The set of plan indices (for this proposal) that are now done.
	doneIdx := map[int]bool{}
	for _, s := range siblings {
		scp := childDesignPlanOf(s)
		if scp.present && scp.commentID == cp.commentID && s.Status == "done" {
			doneIdx[scp.planIndex] = true
		}
	}
	for _, s := range siblings {
		if s.Status != "backlog" {
			continue
		}
		scp := childDesignPlanOf(s)
		if !scp.present || scp.commentID != cp.commentID {
			continue
		}
		deps := parseDependsOn(metaString(s.Metadata, designMetaKeyDependsOn))
		if !depsSatisfied(deps, doneIdx) {
			continue
		}
		// Compare-and-swap: flip to todo ONLY if still backlog. When two
		// prerequisite siblings finish concurrently, both promote goroutines
		// reach here for the same dependent — the CAS lets exactly one win, so
		// the enqueue + publish below run once (EnqueueTaskForIssue has no
		// dedup, so a naive UpdateIssueStatus would double-enqueue).
		promoted, err := h.Queries.PromoteIssueFromBacklog(ctx, db.PromoteIssueFromBacklogParams{
			ID: s.ID, WorkspaceID: s.WorkspaceID,
		})
		if err != nil {
			continue // ErrNoRows = another promoter won the swap; nothing to do
		}
		// Replicate the UpdateIssue backlog→todo enqueue block (which lives inline
		// in the HTTP handler and does NOT fire on a direct DB status write).
		if h.isAgentAssigneeReady(ctx, promoted) {
			h.TaskService.EnqueueTaskForIssue(ctx, promoted)
		}
		if h.isSquadLeaderReady(ctx, promoted) {
			h.enqueueSquadLeaderTask(ctx, promoted, pgtype.UUID{}, actorType, actorID)
		}
		h.publish(protocol.EventIssueUpdated, uuidToString(promoted.WorkspaceID), actorType, actorID, map[string]any{
			"issue": issueToResponse(promoted, h.getIssuePrefix(ctx, promoted.WorkspaceID)),
		})
		slog.Info("design dependent promoted", "issue_id", uuidToString(promoted.ID), "parent_id", uuidToString(child.ParentIssueID))
	}
}

// parseDependsOn decodes a comma-joined "0,2" design_depends_on value.
func parseDependsOn(s string) []int {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []int
	for _, part := range strings.Split(s, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func depsSatisfied(deps []int, done map[int]bool) bool {
	for _, d := range deps {
		if !done[d] {
			return false
		}
	}
	return true
}
