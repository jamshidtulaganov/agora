package service

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/designcontext"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

var designContextBlockRe = regexp.MustCompile("(?s)```(?:design-context|design-manifest)\\s*\\n(.*?)```")

func parseDesignContextBlock(content string) (designcontext.Context, bool) {
	match := designContextBlockRe.FindStringSubmatch(content)
	if match == nil {
		return designcontext.Context{}, false
	}
	document, err := designcontext.DecodeProposal([]byte(strings.TrimSpace(match[1])))
	return document, err == nil
}

// CaptureDesignContext stores every agent-generated document as a proposal.
// Only an owner/admin human can activate it through the review endpoint, so
// untrusted repository or Figma text never becomes future agent instruction by
// virtue of appearing in an agent comment.
func (s *TaskService) CaptureDesignContext(ctx context.Context, issue db.Issue, comment db.Comment, authorID pgtype.UUID) {
	if !issue.ProjectID.Valid {
		return
	}
	document, ok := parseDesignContextBlock(comment.Content)
	if !ok {
		return
	}
	baseRevision := int32(0)
	if active, err := s.Queries.GetActiveProjectDesignContext(ctx, db.GetActiveProjectDesignContextParams{WorkspaceID: issue.WorkspaceID, ProjectID: issue.ProjectID}); err == nil {
		baseRevision = active.Revision
	} else if !errors.Is(err, pgx.ErrNoRows) {
		slog.Warn("capture design context: active lookup failed", "error", err, "project_id", util.UUIDToString(issue.ProjectID))
		return
	}
	contextHash, sourceHash, contextJSON, sourcesJSON, err := designcontext.Hash(document)
	if err != nil {
		return
	}
	nextRevision, err := s.Queries.GetNextProjectDesignContextRevision(ctx, db.GetNextProjectDesignContextRevisionParams{WorkspaceID: issue.WorkspaceID, ProjectID: issue.ProjectID})
	if err != nil {
		slog.Warn("capture design context: revision allocation failed", "error", err, "project_id", util.UUIDToString(issue.ProjectID))
		return
	}
	now := time.Now().UTC()
	proposal, err := s.Queries.CreateProjectDesignContextProposal(ctx, db.CreateProjectDesignContextProposalParams{
		WorkspaceID: issue.WorkspaceID, ProjectID: issue.ProjectID, Revision: nextRevision, BaseRevision: baseRevision,
		Context: contextJSON, ContextHash: contextHash, SourceHash: sourceHash, Sources: sourcesJSON,
		ProposedByType: "agent", ProposedByID: authorID, GeneratedAt: pgtype.Timestamptz{Time: now, Valid: true},
	})
	if err != nil {
		slog.Warn("capture design context: proposal write failed", "error", err, "project_id", util.UUIDToString(issue.ProjectID))
		return
	}
	message := "<!-- design-context-proposal -->\nDesign context proposal rev " + strconv.Itoa(int(proposal.Revision)) +
		" is ready for owner/admin review. It is not active and will not be injected into agent runs until approved."
	posted, err := s.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID, AuthorType: "agent", AuthorID: authorID,
		Content: message, Type: "comment", ParentID: pgtype.UUID{Valid: false},
	})
	if err == nil {
		s.Bus.Publish(events.Event{
			Type: protocol.EventCommentCreated, WorkspaceID: util.UUIDToString(issue.WorkspaceID), ActorType: "agent", ActorID: util.UUIDToString(authorID),
			Payload: map[string]any{"comment": map[string]any{
				"id": util.UUIDToString(posted.ID), "issue_id": util.UUIDToString(posted.IssueID), "author_type": posted.AuthorType,
				"author_id": util.UUIDToString(posted.AuthorID), "content": posted.Content, "type": posted.Type,
				"created_at": posted.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
			}, "issue_title": issue.Title, "issue_status": issue.Status},
		})
	}
	s.recordDesignActivity(ctx, issue, authorID, "design_context_proposed", map[string]any{
		"proposal_id": util.UUIDToString(proposal.ID), "revision": proposal.Revision, "context_hash": proposal.ContextHash,
	})
	slog.Info("design context proposal captured", "project_id", util.UUIDToString(issue.ProjectID), "revision", proposal.Revision)
}
