package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// The gen_design_manifest slice action posts the project's design system as a
// fenced ```design-manifest JSON block. Capture lives here (TaskService)
// alongside the design-proposal capture, for the same reason: the task.go
// ingest point has no handler access. Captured onto project.settings via a
// KEY-SCOPED jsonb_set write so it can never clobber sibling settings keys
// (qa_manifest, sprint_mode, ...).

// designManifestBlockRe extracts the ```design-manifest fenced JSON.
var designManifestBlockRe = regexp.MustCompile("(?s)```design-manifest\\s*\\n(.*?)```")

// parseDesignManifestBlock extracts + minimally validates the block: it must be
// a JSON object carrying a recognizable design-system shape (a kind, or tokens,
// or components). Returns the parsed object (so the server can stamp
// revision/source/updated_at) + ok=false on no block / malformed / empty.
func parseDesignManifestBlock(content string) (obj map[string]any, ok bool) {
	m := designManifestBlockRe.FindStringSubmatch(content)
	if m == nil {
		return nil, false
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(m[1])), &obj); err != nil || obj == nil {
		return nil, false
	}
	_, hasKind := obj["kind"]
	_, hasTokens := obj["tokens"]
	_, hasComponents := obj["components"]
	if !hasKind && !hasTokens && !hasComponents {
		return nil, false // an object with none of these is not a manifest
	}
	return obj, true
}

// projectManifestMeta reads the current manifest's source + revision from a
// project's settings, without depending on the handler-layer designManifest
// type. Returns ("", 0) when there is no manifest.
func projectManifestMeta(settings []byte) (source string, revision int) {
	if len(settings) == 0 {
		return "", 0
	}
	var s struct {
		Manifest *struct {
			Source   string `json:"source"`
			Revision int    `json:"revision"`
		} `json:"design_manifest"`
	}
	if json.Unmarshal(settings, &s) != nil || s.Manifest == nil {
		return "", 0
	}
	return s.Manifest.Source, s.Manifest.Revision
}

// CaptureDesignManifest persists a gen_design_manifest agent comment's block
// onto the project. If the project's current manifest was human-authored
// (source=="manual"), the agent NEVER overwrites it — instead a system comment
// carries the proposed update for the human to review + paste. Otherwise it
// writes via the key-scoped jsonb_set query, stamping revision+1 / source /
// updated_at, and publishes project:updated. Best-effort + detached.
func (s *TaskService) CaptureDesignManifest(ctx context.Context, issue db.Issue, comment db.Comment, authorID pgtype.UUID) {
	if !issue.ProjectID.Valid {
		return
	}
	obj, ok := parseDesignManifestBlock(comment.Content)
	if !ok {
		return
	}
	project, err := s.Queries.GetProject(ctx, issue.ProjectID)
	if err != nil {
		return
	}
	source, revision := projectManifestMeta(project.Settings)

	// A human owns the manifest — never overwrite it. Surface the agent's
	// proposal as a comment the human can review and paste into Project → Design.
	// Posted via a DIRECT CreateComment (NOT createAgentComment): the proposal
	// comment itself carries a ```design-manifest``` block, and createAgentComment
	// re-runs CaptureDesignManifest on every comment it posts — routing through it
	// would re-enter this branch on the new comment and recurse without bound. A
	// direct write skips that capture tail.
	if source == "manual" {
		proposal := "Proposed design-manifest update — the project manifest is human-curated, so it was NOT " +
			"overwritten. Review and paste into Project → Design if you accept it:\n\n```design-manifest\n" +
			mustCompactJSON(obj) + "\n```"
		posted, cerr := s.Queries.CreateComment(ctx, db.CreateCommentParams{
			IssueID:     issue.ID,
			WorkspaceID: issue.WorkspaceID,
			AuthorType:  "agent",
			AuthorID:    authorID,
			Content:     proposal,
			Type:        "comment",
			ParentID:    pgtype.UUID{Valid: false},
		})
		if cerr != nil {
			slog.Warn("capture design manifest: proposal comment failed", "error", cerr, "issue_id", util.UUIDToString(issue.ID))
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
		s.recordDesignActivity(ctx, issue, authorID, "design_manifest_proposed", map[string]any{
			"comment_id": util.UUIDToString(comment.ID),
		})
		return
	}

	// Stamp server-owned fields; the agent supplies kind/tokens/components/etc.
	obj["revision"] = revision + 1
	obj["source"] = "agent"
	obj["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	manifestJSON, err := json.Marshal(obj)
	if err != nil {
		return
	}

	updated, err := s.Queries.SetProjectDesignManifest(ctx, db.SetProjectDesignManifestParams{
		ID:          issue.ProjectID,
		WorkspaceID: issue.WorkspaceID,
		Manifest:    manifestJSON,
	})
	if err != nil {
		slog.Warn("capture design manifest: write failed", "error", err, "project_id", util.UUIDToString(issue.ProjectID))
		return
	}
	s.recordDesignActivity(ctx, issue, authorID, "design_manifest_updated", map[string]any{
		"revision": revision + 1,
	})
	s.publishProjectUpdated(ctx, updated)
	slog.Info("design manifest captured", "project_id", util.UUIDToString(issue.ProjectID), "revision", revision+1)
}

// publishProjectUpdated broadcasts project:updated with the full project payload
// (matching the canonical handler publisher) so any live project view refreshes
// without a manual refetch. Counts are computed like loadProjectIssueStats.
func (s *TaskService) publishProjectUpdated(ctx context.Context, p db.Project) {
	var total, done int64
	if stats, err := s.Queries.GetProjectIssueStats(ctx, []pgtype.UUID{p.ID}); err == nil && len(stats) > 0 {
		total, done = stats[0].TotalCount, stats[0].DoneCount
	}
	var resourceCount int64
	if rows, err := s.Queries.GetProjectResourceCounts(ctx, []pgtype.UUID{p.ID}); err == nil && len(rows) > 0 {
		resourceCount = rows[0].ResourceCount
	}
	var settings any
	if len(p.Settings) > 0 {
		_ = json.Unmarshal(p.Settings, &settings)
	}
	if settings == nil {
		settings = map[string]any{}
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventProjectUpdated,
		WorkspaceID: util.UUIDToString(p.WorkspaceID),
		ActorType:   "agent",
		ActorID:     "",
		Payload: map[string]any{
			"project": map[string]any{
				"id":             util.UUIDToString(p.ID),
				"workspace_id":   util.UUIDToString(p.WorkspaceID),
				"title":          p.Title,
				"description":    util.TextToPtr(p.Description),
				"icon":           util.TextToPtr(p.Icon),
				"status":         p.Status,
				"priority":       p.Priority,
				"lead_type":      util.TextToPtr(p.LeadType),
				"lead_id":        util.UUIDToPtr(p.LeadID),
				"squad_id":       util.UUIDToPtr(p.SquadID),
				"settings":       settings,
				"created_at":     util.TimestampToString(p.CreatedAt),
				"updated_at":     util.TimestampToString(p.UpdatedAt),
				"issue_count":    total,
				"done_count":     done,
				"resource_count": resourceCount,
			},
		},
	})
}

func mustCompactJSON(obj map[string]any) string {
	b, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}
