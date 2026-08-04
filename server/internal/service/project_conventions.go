package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// The "Learn from repo" action (LearnProjectConventions) asks the lead agent to
// study the repo and post the project's coding conventions as a fenced
// ```conventions Markdown block. Capture lives here (TaskService) alongside the
// design-manifest capture — the task.go agent-comment ingest point has no
// handler access. Written onto project.settings.conventions via the KEY-SCOPED
// jsonb_set query so it can never clobber sibling settings keys.

// conventionsBlockRe extracts the ```conventions fenced Markdown body.
var conventionsBlockRe = regexp.MustCompile("(?s)```conventions\\s*\\n(.*?)```")

// parseConventionsBlock extracts the trimmed Markdown body of the first
// ```conventions block. ok=false on no block / empty body.
func parseConventionsBlock(content string) (string, bool) {
	m := conventionsBlockRe.FindStringSubmatch(content)
	if m == nil {
		return "", false
	}
	text := strings.TrimSpace(m[1])
	if text == "" {
		return "", false
	}
	return text, true
}

// currentProjectConventions reads the trimmed conventions string from a project
// settings blob, without depending on the handler layer.
func currentProjectConventions(settings []byte) string {
	if len(settings) == 0 {
		return ""
	}
	var s struct {
		Conventions string `json:"conventions"`
	}
	if json.Unmarshal(settings, &s) != nil {
		return ""
	}
	return strings.TrimSpace(s.Conventions)
}

// CaptureProjectConventions persists a "Learn from repo" agent comment's
// ```conventions block onto the project. A human owns the field, so:
//   - if the project has NO conventions yet, write the draft directly (the whole
//     point of learning from an un-configured existing repo) and confirm.
//   - if conventions ALREADY exist, NEVER overwrite — post the proposal as a
//     comment the human can review + paste into Project → Conventions.
//
// Both follow-up comments are posted via a DIRECT CreateComment (NOT
// createAgentComment): the proposal comment itself carries a ```conventions
// block, and routing through the agent-comment ingest would re-enter this
// capture and recurse. Best-effort + detached.
func (s *TaskService) CaptureProjectConventions(ctx context.Context, issue db.Issue, comment db.Comment, authorID pgtype.UUID) {
	if !issue.ProjectID.Valid {
		return
	}
	text, ok := parseConventionsBlock(comment.Content)
	if !ok {
		return
	}
	project, err := s.Queries.GetProject(ctx, issue.ProjectID)
	if err != nil {
		return
	}

	// A human already curated conventions — surface the agent's draft as a
	// proposal instead of clobbering it.
	if currentProjectConventions(project.Settings) != "" {
		proposal := "Proposed conventions update — this project's conventions are human-curated, so they were NOT " +
			"overwritten. Review and paste into Project → Conventions if you accept:\n\n```conventions\n" +
			text + "\n```"
		s.postCapturedConventionsComment(ctx, issue, authorID, proposal)
		return
	}

	// No conventions yet — persist the draft directly via the key-scoped write.
	valueJSON, err := json.Marshal(text)
	if err != nil {
		return
	}
	updated, err := s.Queries.SetProjectSettingKey(ctx, db.SetProjectSettingKeyParams{
		ID:          issue.ProjectID,
		WorkspaceID: issue.WorkspaceID,
		Key:         "conventions",
		Value:       valueJSON,
	})
	if err != nil {
		slog.Warn("capture project conventions: write failed", "error", err, "project_id", util.UUIDToString(issue.ProjectID))
		return
	}
	s.publishProjectUpdated(ctx, updated)
	s.postCapturedConventionsComment(ctx, issue, authorID,
		"Drafted this project's coding conventions from the repo and saved them to Project → Conventions — review and edit as needed. They now ride along on every agent run here.")
	slog.Info("project conventions captured", "project_id", util.UUIDToString(issue.ProjectID))
}

// postCapturedConventionsComment posts a follow-up comment directly (bypassing
// the agent-comment ingest path so it never re-enters capture) and publishes the
// created event so it renders live.
func (s *TaskService) postCapturedConventionsComment(ctx context.Context, issue db.Issue, authorID pgtype.UUID, content string) {
	posted, err := s.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "agent",
		AuthorID:    authorID,
		Content:     content,
		Type:        "comment",
		ParentID:    pgtype.UUID{Valid: false},
	})
	if err != nil {
		slog.Warn("capture project conventions: comment failed", "error", err, "issue_id", util.UUIDToString(issue.ID))
		return
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventCommentCreated,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "agent",
		ActorID:     util.UUIDToString(authorID),
		Payload: map[string]any{
			"comment": map[string]any{
				"id":          util.UUIDToString(posted.ID),
				"issue_id":    util.UUIDToString(posted.IssueID),
				"author_type": posted.AuthorType,
				"author_id":   util.UUIDToString(posted.AuthorID),
				"content":     posted.Content,
				"type":        posted.Type,
				"created_at":  posted.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
			},
			"issue_title":  issue.Title,
			"issue_status": issue.Status,
		},
	})
}
