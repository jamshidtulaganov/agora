package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

func (h *Handler) assistantAttachmentVisible(ctx context.Context, userID string, ws db.Workspace, role string, att db.Attachment) bool {
	userUUID := parseUUID(userID)
	if att.ChatSessionID.Valid || att.ChatMessageID.Valid {
		return false
	}
	if !att.IssueID.Valid && !att.CommentID.Valid {
		return att.UploaderType == "member" && util.UUIDToString(att.UploaderID) == userID
	}
	issueID := att.IssueID
	if !issueID.Valid {
		comment, err := h.Queries.GetComment(ctx, att.CommentID)
		if err != nil || comment.WorkspaceID != ws.ID {
			return false
		}
		issueID = comment.IssueID
	}
	if !assistantVisibilityRestriction(role, userUUID).Valid {
		return true
	}
	visible, err := h.Queries.IssueBelongsToUser(ctx, db.IssueBelongsToUserParams{IssueID: issueID, WorkspaceID: ws.ID, UserID: userUUID})
	return err == nil && visible
}

func (h *Handler) validateAssistantRunContext(ctx context.Context, userID string, runContext assistant.RunContext) error {
	if runContext.WorkspaceID == nil {
		return errors.New("workspace missing")
	}
	ws, role, err := h.assistantMembership(ctx, parseUUID(userID), *runContext.WorkspaceID)
	if err != nil {
		return err
	}
	if runContext.ProjectID != nil {
		projectUUID, err := util.ParseUUID(*runContext.ProjectID)
		if err != nil {
			return err
		}
		if _, err := h.Queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{ID: projectUUID, WorkspaceID: ws.ID}); err != nil {
			return err
		}
	}
	if runContext.MemberID != nil {
		memberUUID, err := util.ParseUUID(*runContext.MemberID)
		if err != nil {
			return err
		}
		if _, err := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
			UserID:      memberUUID,
			WorkspaceID: ws.ID,
		}); err != nil {
			return errors.New("attached member is no longer in that workspace")
		}
	}
	for _, id := range runContext.AttachmentIDs {
		attUUID, err := util.ParseUUID(id)
		if err != nil {
			return err
		}
		att, err := h.Queries.GetAttachment(ctx, db.GetAttachmentParams{ID: attUUID, WorkspaceID: ws.ID})
		if err != nil {
			return err
		}
		if !h.assistantAttachmentVisible(ctx, userID, ws, role, att) {
			return errors.New("file no longer visible")
		}
	}
	return nil
}

const (
	assistantMaxFiles              = 5
	assistantMaxFileBytes          = 64 << 10
	assistantMaxTotalFileBytes     = 128 << 10
	assistantMaxProjectDescription = 8 << 10
	assistantMaxProjectResources   = 20
)

func assistantBoundString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max] + " [truncated]"
}

// Repository URLs can contain embedded credentials or signed query parameters.
// The model only needs the repository location, so omit those parts from the
// captured prompt context.
func assistantRepoReference(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw // scp-style SSH references have no URL host.
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String()
}

// prepareAssistantContext validates each explicit reference under the current
// human's permissions and captures only bounded, readable source data. It
// never follows a resource URL or accesses a daemon's local path.
func (h *Handler) prepareAssistantContext(w http.ResponseWriter, r *http.Request, userID string, context assistant.RunContext) ([]byte, bool) {
	snapshot := assistant.ContextSnapshot{}
	if context.ProjectID == nil && context.MemberID == nil && len(context.AttachmentIDs) == 0 {
		return []byte(`{}`), true
	}
	if context.WorkspaceID == nil {
		writeError(w, http.StatusBadRequest, "select a workspace for project, member or file context")
		return nil, false
	}
	userUUID := parseUUID(userID)
	ws, role, err := h.assistantMembership(r.Context(), userUUID, *context.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusForbidden, "workspace not available")
		return nil, false
	}
	if context.ProjectID != nil {
		projectUUID, err := util.ParseUUID(*context.ProjectID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid project_id")
			return nil, false
		}
		project, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{ID: projectUUID, WorkspaceID: ws.ID})
		if err != nil {
			writeError(w, http.StatusNotFound, "project not found in selected workspace")
			return nil, false
		}
		p := &assistant.ProjectSnapshot{ID: *context.ProjectID, Title: assistantBoundString(project.Title, 256)}
		if project.Description.Valid {
			p.Description = assistantBoundString(project.Description.String, assistantMaxProjectDescription)
		}
		resources, err := h.Queries.ListProjectResources(r.Context(), projectUUID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not load project resources")
			return nil, false
		}
		p.ResourcesTruncated = len(resources) > assistantMaxProjectResources
		for i, res := range resources {
			if i >= assistantMaxProjectResources {
				break
			}
			item := assistant.ResourceSnapshot{Type: res.ResourceType}
			if res.Label.Valid {
				item.Label = assistantBoundString(res.Label.String, 128)
			}
			switch res.ResourceType {
			case "github_repo":
				var ref githubRepoRef
				if json.Unmarshal(res.ResourceRef, &ref) == nil {
					item.Reference = assistantBoundString(assistantRepoReference(ref.URL), 512)
					if ref.DefaultBranchHint != "" {
						item.Reference += " branch=" + assistantBoundString(ref.DefaultBranchHint, 128)
					}
				}
			case "local_directory":
				var ref localDirectoryRef
				if json.Unmarshal(res.ResourceRef, &ref) == nil {
					item.Reference = "local directory (" + assistantBoundString(ref.Access, 32) + " access; contents unavailable to Assistant)"
				}
			}
			p.Resources = append(p.Resources, item)
		}
		snapshot.Project = p
	}
	// A member the user picked is only a member of THIS workspace's roster.
	// Checking membership here (rather than trusting the picker) is what stops
	// a stale composer — or a hand-made request — from naming somebody outside
	// the workspace this message runs in.
	if context.MemberID != nil {
		memberUUID, err := util.ParseUUID(*context.MemberID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid member_id")
			return nil, false
		}
		if _, err := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
			UserID:      memberUUID,
			WorkspaceID: ws.ID,
		}); err != nil {
			writeError(w, http.StatusNotFound, "member not found in selected workspace")
			return nil, false
		}
		member, err := h.Queries.GetUser(r.Context(), memberUUID)
		if err != nil {
			writeError(w, http.StatusNotFound, "member not found in selected workspace")
			return nil, false
		}
		snapshot.Member = &assistant.MemberSnapshot{
			UserID: uuidToString(memberUUID),
			Name:   assistantBoundString(member.Name, 256),
		}
	}
	if len(context.AttachmentIDs) > assistantMaxFiles {
		writeError(w, http.StatusBadRequest, "too many files (max 5)")
		return nil, false
	}
	if len(context.AttachmentIDs) > 0 && h.Storage == nil {
		writeError(w, http.StatusServiceUnavailable, "file storage is unavailable")
		return nil, false
	}
	seen := map[string]bool{}
	total := 0
	for _, id := range context.AttachmentIDs {
		if seen[id] {
			writeError(w, http.StatusBadRequest, "duplicate attachment_id")
			return nil, false
		}
		seen[id] = true
		attUUID, err := util.ParseUUID(id)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid attachment_id")
			return nil, false
		}
		att, err := h.Queries.GetAttachment(r.Context(), db.GetAttachmentParams{ID: attUUID, WorkspaceID: ws.ID})
		if err != nil {
			writeError(w, http.StatusNotFound, "file not found in selected workspace")
			return nil, false
		}
		if !h.assistantAttachmentVisible(r.Context(), userID, ws, role, att) {
			writeError(w, http.StatusNotFound, "file not available")
			return nil, false
		}
		if !isTextPreviewable(att.ContentType, att.Filename) {
			writeError(w, http.StatusUnsupportedMediaType, "Assistant supports text, code, CSV, JSON and Markdown files only")
			return nil, false
		}
		if att.SizeBytes > assistantMaxFileBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "file exceeds Assistant's 64 KiB text limit")
			return nil, false
		}
		reader, err := h.Storage.GetReader(r.Context(), h.Storage.KeyFromURL(att.Url))
		if err != nil {
			writeError(w, http.StatusBadGateway, "could not read selected file")
			return nil, false
		}
		body, readErr := io.ReadAll(io.LimitReader(reader, assistantMaxFileBytes+1))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil {
			writeError(w, http.StatusBadGateway, "could not read selected file")
			return nil, false
		}
		if len(body) > assistantMaxFileBytes || total+len(body) > assistantMaxTotalFileBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "selected files exceed Assistant's 128 KiB total limit")
			return nil, false
		}
		if !utf8.Valid(body) || strings.ContainsRune(string(body), '\x00') {
			writeError(w, http.StatusUnsupportedMediaType, "selected file is not UTF-8 text")
			return nil, false
		}
		total += len(body)
		snapshot.Files = append(snapshot.Files, assistant.FileSnapshot{ID: id, Filename: assistantBoundString(att.Filename, 256), ContentType: att.ContentType, Content: string(body)})
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not capture selected context")
		return nil, false
	}
	return encoded, true
}
