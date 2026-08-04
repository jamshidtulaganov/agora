package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// The design_proposal slice action posts its result as a fenced
// ```design-proposal JSON block, the same idiom qa-result / test-cases use.
// Capture lives here in TaskService (not the handler) because one of the two
// agent-comment ingest points — createAgentComment in task.go — has no handler
// access, and handler→service is the only legal dependency direction. So the
// parse, the design-state label, the activity row, and the inbox notification
// all run off Queries + Bus, which TaskService already holds.

// Design-state labels are the human-visible state of an issue's design review.
// They are mutually exclusive (SetDesignStateLabel enforces it).
const (
	DesignLabelProposed         = "design:proposed"
	DesignLabelApproved         = "design:approved"
	DesignLabelChangesRequested = "design:changes_requested"
)

// designStateLabels lists all three state labels + their colors so
// SetDesignStateLabel can detach the siblings when it attaches one.
var designStateLabels = []struct{ name, color string }{
	{DesignLabelProposed, "#8b5cf6"},
	{DesignLabelApproved, "#22c55e"},
	{DesignLabelChangesRequested, "#f59e0b"},
}

// designProposalBlockRe extracts the ```design-proposal fenced JSON the
// design_proposal recipe appends. Mirrors qaResultBlockRe / testCasesBlockRe.
var designProposalBlockRe = regexp.MustCompile("(?s)```design-proposal\\s*\\n(.*?)```")

// DesignProposalComponent is one classified UI element: reuse an existing
// component, extend one, or build new.
type DesignProposalComponent struct {
	Name        string `json:"name"`
	Verdict     string `json:"verdict"` // reuse | extend | new
	CodeRef     string `json:"code_ref"`
	FigmaNodeID string `json:"figma_node_id"`
	Notes       string `json:"notes"`
}

// DesignProposalScreen is one screen/state extracted from the design.
type DesignProposalScreen struct {
	Name        string `json:"name"`
	FigmaNodeID string `json:"figma_node_id"`
	Summary     string `json:"summary"`
	Render      string `json:"render"` // attachment filename contract: figma-<node-id-dashed>.png
}

// DesignProposalDeviation is a Figma value that contradicts the project's
// design system — a question for the human, not a silent decision.
type DesignProposalDeviation struct {
	Aspect       string `json:"aspect"`
	FigmaValue   string `json:"figma_value"`
	ProjectValue string `json:"project_value"`
	Question     string `json:"question"`
}

// DesignProposalSubIssue is one proposed decomposition slice. depends_on holds
// the indices of sibling sub-issues that must ship first (Phase 4 turns these
// into real sub-issues; Phase 2 only stores + renders them).
type DesignProposalSubIssue struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Screens     []string `json:"screens"`
	NodeIDs     []string `json:"node_ids"`
	DependsOn   []int    `json:"depends_on"`
}

// DesignProposal is the parsed ```design-proposal block.
type DesignProposal struct {
	Status        string                    `json:"status"` // ok | blocked
	Reason        string                    `json:"reason"`
	ReasonDetail  string                    `json:"reason_detail"`
	Figma         []designProposalFigmaRef  `json:"figma"`
	Screens       []DesignProposalScreen    `json:"screens"`
	Components    []DesignProposalComponent `json:"components"`
	Deviations    []DesignProposalDeviation `json:"deviations"`
	SubIssues     []DesignProposalSubIssue  `json:"sub_issues"`
	OpenQuestions []string                  `json:"open_questions"`
}

type designProposalFigmaRef struct {
	URL     string `json:"url"`
	FileKey string `json:"file_key"`
	NodeID  string `json:"node_id"`
}

// DesignProposalState is the four-way outcome of parsing a comment.
type DesignProposalState string

const (
	DesignProposalStateNone    DesignProposalState = "none"    // no block present
	DesignProposalStateInvalid DesignProposalState = "invalid" // block present but unparseable
	DesignProposalStateOK      DesignProposalState = "ok"      // valid, status ok
	DesignProposalStateBlocked DesignProposalState = "blocked" // valid, status blocked
)

// ParseDesignProposalBlock extracts + validates the ```design-proposal block.
// Distinguishes no-block / invalid / ok / blocked so callers can render an
// explicit error card for a present-but-broken block instead of silently
// dropping it. Fails closed: unknown status reads as blocked.
func ParseDesignProposalBlock(content string) (raw string, p DesignProposal, state DesignProposalState) {
	m := designProposalBlockRe.FindStringSubmatch(content)
	if m == nil {
		return "", DesignProposal{}, DesignProposalStateNone
	}
	raw = strings.TrimSpace(m[1])
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return raw, DesignProposal{}, DesignProposalStateInvalid
	}
	if p.Status == "blocked" {
		return raw, p, DesignProposalStateBlocked
	}
	if p.Status == "ok" || p.Status == "" {
		p.Status = "ok"
		return raw, p, DesignProposalStateOK
	}
	// Unknown status value → treat as blocked (fail closed).
	return raw, p, DesignProposalStateBlocked
}

// ensureDesignLabel resolves a label id by name, creating it if missing.
// Service-level port of Handler.ensureLabel (qa_watchdog.go) so capture, which
// lives in TaskService, can attach design-state labels without a handler.
func (s *TaskService) ensureDesignLabel(ctx context.Context, wsID pgtype.UUID, name, color string) (pgtype.UUID, error) {
	l, err := s.Queries.GetLabelByName(ctx, db.GetLabelByNameParams{WorkspaceID: wsID, Name: name})
	if err == nil {
		return l.ID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, err
	}
	created, err := s.Queries.CreateLabel(ctx, db.CreateLabelParams{WorkspaceID: wsID, Name: name, Color: color})
	if err != nil {
		return pgtype.UUID{}, err
	}
	return created.ID, nil
}

// SetDesignStateLabel attaches the target design-state label and detaches the
// other two, so an issue is ever in exactly one design state. Publishes
// EventIssueLabelsChanged with the FULL label set (matching the canonical
// label.go attach path — the frontend replaces the issue's labels with the
// payload, so a labels-less event would wipe them). Invoked from capture AND
// the review endpoint, so contradictory states are impossible by construction.
func (s *TaskService) SetDesignStateLabel(ctx context.Context, issue db.Issue, target string) error {
	var color string
	for _, l := range designStateLabels {
		if l.name == target {
			color = l.color
		}
	}
	if color == "" {
		return fmt.Errorf("unknown design state label %q", target)
	}
	labelID, err := s.ensureDesignLabel(ctx, issue.WorkspaceID, target, color)
	if err != nil {
		return err
	}
	if err := s.Queries.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
		IssueID:     issue.ID,
		LabelID:     labelID,
		WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		return err
	}
	// Detach the sibling state labels. Best-effort per label: a detach miss must
	// not abort the attach we already committed.
	for _, l := range designStateLabels {
		if l.name == target {
			continue
		}
		existing, err := s.Queries.GetLabelByName(ctx, db.GetLabelByNameParams{WorkspaceID: issue.WorkspaceID, Name: l.name})
		if err != nil {
			continue // label never created → nothing attached
		}
		if err := s.Queries.DetachLabelFromIssue(ctx, db.DetachLabelFromIssueParams{
			IssueID:     issue.ID,
			LabelID:     existing.ID,
			WorkspaceID: issue.WorkspaceID,
		}); err != nil {
			slog.Warn("design state label: detach sibling failed", "error", err, "issue_id", util.UUIDToString(issue.ID), "label", l.name)
		}
	}
	// Broadcast the FULL label set — the frontend's issue_labels:changed handler
	// does a direct cache replacement of the issue's labels with the payload's
	// `labels` (defaulting to []), so publishing without it would wipe EVERY
	// label off the issue in every client. This mirrors the canonical
	// label.go attach path (NOT qa_watchdog's issue_id-only escalation event).
	// On a read failure we skip the broadcast rather than send an empty list —
	// clients recover the change on their next query.
	labels, err := s.Queries.ListLabelsByIssue(ctx, db.ListLabelsByIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		slog.Warn("design state label: list labels for broadcast failed", "error", err, "issue_id", util.UUIDToString(issue.ID))
		return nil
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventIssueLabelsChanged,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "agent",
		ActorID:     "",
		Payload: map[string]any{
			"issue_id": util.UUIDToString(issue.ID),
			"labels":   labelRowsToPayload(labels),
		},
	})
	return nil
}

// labelRowsToPayload serializes label rows into the JSON shape the frontend
// Label type expects. Kept here (not the handler's labelsToResponse) so the
// service layer stays free of handler imports.
func labelRowsToPayload(list []db.IssueLabel) []map[string]any {
	out := make([]map[string]any, len(list))
	for i, l := range list {
		out[i] = map[string]any{
			"id":           util.UUIDToString(l.ID),
			"workspace_id": util.UUIDToString(l.WorkspaceID),
			"name":         l.Name,
			"color":        l.Color,
			"created_at":   util.TimestampToString(l.CreatedAt),
			"updated_at":   util.TimestampToString(l.UpdatedAt),
		}
	}
	return out
}

// CaptureDesignProposal persists the outcome of a design_proposal agent comment:
// a valid ok proposal attaches design:proposed + notifies the issue's humans; a
// blocked proposal notifies without touching state (the design was unreadable,
// not reviewed); an invalid block records an activity + logs so the UI can show
// an explicit "could not parse — re-run" card. No-op for ordinary comments.
// Best-effort + detached — a proposal comment never fails because of capture.
func (s *TaskService) CaptureDesignProposal(ctx context.Context, issue db.Issue, comment db.Comment, authorID pgtype.UUID) {
	_, p, state := ParseDesignProposalBlock(comment.Content)
	switch state {
	case DesignProposalStateNone:
		return
	case DesignProposalStateInvalid:
		s.recordDesignActivity(ctx, issue, authorID, "design_proposal_invalid", map[string]any{
			"comment_id": util.UUIDToString(comment.ID),
		})
		slog.Warn("design proposal: comment has an unparseable design-proposal block",
			"issue_id", util.UUIDToString(issue.ID), "comment_id", util.UUIDToString(comment.ID))
		return
	case DesignProposalStateBlocked:
		s.recordDesignActivity(ctx, issue, authorID, "design_proposal_blocked", map[string]any{
			"comment_id": util.UUIDToString(comment.ID),
			"reason":     p.Reason,
		})
		s.notifyDesignProposal(ctx, issue, comment, "design_proposal_blocked", p)
		slog.Info("design proposal blocked", "issue_id", util.UUIDToString(issue.ID), "reason", p.Reason)
		return
	case DesignProposalStateOK:
		if err := s.SetDesignStateLabel(ctx, issue, DesignLabelProposed); err != nil {
			slog.Warn("design proposal: set proposed label failed", "error", err, "issue_id", util.UUIDToString(issue.ID))
		}
		s.recordDesignActivity(ctx, issue, authorID, "design_proposal_generated", map[string]any{
			"comment_id": util.UUIDToString(comment.ID),
			"screens":    len(p.Screens),
			"components": len(p.Components),
			"sub_issues": len(p.SubIssues),
			"deviations": len(p.Deviations),
		})
		s.notifyDesignProposal(ctx, issue, comment, "design_proposal_ready", p)
		slog.Info("design proposal captured",
			"issue_id", util.UUIDToString(issue.ID),
			"screens", len(p.Screens), "components", len(p.Components), "sub_issues", len(p.SubIssues))
	}
}

// LatestDesignProposalForIssue returns the newest agent comment carrying a
// design-proposal block, its parse state, and the source comment id. state is
// DesignProposalStateNone when the issue has no proposal comment at all.
func (s *TaskService) LatestDesignProposalForIssue(ctx context.Context, issue db.Issue) (DesignProposal, pgtype.UUID, DesignProposalState, error) {
	comments, err := s.Queries.ListCommentsForIssue(ctx, db.ListCommentsForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		Limit:       2000,
	})
	if err != nil {
		return DesignProposal{}, pgtype.UUID{}, DesignProposalStateNone, err
	}
	// Newest-first: the last agent comment with a block wins.
	for i := len(comments) - 1; i >= 0; i-- {
		c := comments[i]
		if c.AuthorType != "agent" {
			continue
		}
		_, p, state := ParseDesignProposalBlock(c.Content)
		if state == DesignProposalStateNone {
			continue
		}
		return p, c.ID, state, nil
	}
	return DesignProposal{}, pgtype.UUID{}, DesignProposalStateNone, nil
}

// recordDesignActivity writes an agent-actored activity_log row for a design
// event. Best-effort: activity is an audit trail, not a hard dependency.
func (s *TaskService) recordDesignActivity(ctx context.Context, issue db.Issue, actorID pgtype.UUID, action string, details map[string]any) {
	s.recordDesignActivityAs(ctx, issue, "agent", actorID, action, details)
}

// RecordDesignReviewActivity writes a MEMBER-actored activity row — the review
// endpoint's approve / request_changes actions are human decisions. Exported so
// the handler layer can record them.
func (s *TaskService) RecordDesignReviewActivity(ctx context.Context, issue db.Issue, actorID pgtype.UUID, action string, details map[string]any) {
	s.recordDesignActivityAs(ctx, issue, "member", actorID, action, details)
}

func (s *TaskService) recordDesignActivityAs(ctx context.Context, issue db.Issue, actorType string, actorID pgtype.UUID, action string, details map[string]any) {
	raw, _ := json.Marshal(details)
	if _, err := s.Queries.CreateActivity(ctx, db.CreateActivityParams{
		WorkspaceID: issue.WorkspaceID,
		IssueID:     issue.ID,
		ActorType:   pgtype.Text{String: actorType, Valid: true},
		ActorID:     actorID,
		Action:      action,
		Details:     raw,
	}); err != nil {
		slog.Warn("design proposal: record activity failed", "error", err, "action", action, "issue_id", util.UUIDToString(issue.ID))
	}
}

// notifyDesignProposal creates inbox items for the issue's human subscribers +
// human creator (deduped) and publishes inbox:new for each. "Proposal awaiting
// review" is the PM's most important moment — it must not depend on watching a
// page.
func (s *TaskService) notifyDesignProposal(ctx context.Context, issue db.Issue, comment db.Comment, itemType string, p DesignProposal) {
	prefix := s.getIssuePrefix(issue.WorkspaceID)
	identifier := fmt.Sprintf("%s-%d", prefix, issue.Number)
	details, _ := json.Marshal(map[string]any{
		"issue_id":   util.UUIDToString(issue.ID),
		"comment_id": util.UUIDToString(comment.ID),
		"identifier": identifier,
		"screens":    len(p.Screens),
		"sub_issues": len(p.SubIssues),
		"reason":     p.Reason,
	})

	// Recipients: human subscribers + the human creator, deduped.
	recipients := map[string]pgtype.UUID{}
	if subs, err := s.Queries.ListIssueSubscribers(ctx, issue.ID); err == nil {
		for _, sub := range subs {
			if sub.UserType == "member" && sub.UserID.Valid {
				recipients[util.UUIDToString(sub.UserID)] = sub.UserID
			}
		}
	}
	if issue.CreatorType == "member" && issue.CreatorID.Valid {
		recipients[util.UUIDToString(issue.CreatorID)] = issue.CreatorID
	}
	if len(recipients) == 0 {
		return
	}

	title := issue.Title
	body := pgtype.Text{}
	if itemType == "design_proposal_blocked" {
		body = pgtype.Text{String: "The designer agent could not read the linked Figma design (" + p.Reason + ").", Valid: true}
	}
	for _, uid := range recipients {
		item, err := s.Queries.CreateInboxItem(ctx, db.CreateInboxItemParams{
			WorkspaceID:   issue.WorkspaceID,
			RecipientType: "member",
			RecipientID:   uid,
			Type:          itemType,
			Severity:      "action_required",
			IssueID:       issue.ID,
			Title:         title,
			Body:          body,
			ActorType:     pgtype.Text{String: "agent", Valid: true},
			ActorID:       comment.AuthorID,
			Details:       details,
		})
		if err != nil {
			slog.Warn("design proposal: inbox write failed", "error", err, "issue_id", util.UUIDToString(issue.ID))
			continue
		}
		s.publishQuickCreateInbox(item, util.UUIDToString(issue.WorkspaceID), util.UUIDToString(comment.AuthorID), issue.Status)
	}
}
