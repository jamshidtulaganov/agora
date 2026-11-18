package agoramcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/jamshidtulaganov/agora/server/internal/util"
)

const maxInlineAttachment = 512 << 10

func (s *Server) downloadAttachment(ctx context.Context, id string) (json.RawMessage, error) {
	var attachment struct {
		IssueID     string `json:"issue_id"`
		WorkspaceID string `json:"workspace_id"`
		Filename    string `json:"filename"`
		ContentType string `json:"content_type"`
		Size        int64  `json:"size_bytes"`
	}
	path := "/api/attachments/" + id
	if err := s.client.GetJSON(ctx, path, &attachment); err != nil {
		return nil, apiError(err)
	}
	// The native browser download endpoint checks workspace membership.
	// The connector also requires the containing issue to be visible.
	if _, err := util.ParseUUID(attachment.IssueID); err != nil || attachment.WorkspaceID != s.client.WorkspaceID {
		return nil, errors.New("attachment is not linked to an accessible issue")
	}
	if err := s.client.GetJSON(ctx, "/api/issues/"+attachment.IssueID, nil); err != nil {
		return nil, apiError(err)
	}
	link := s.client.BaseURL + path + "/download"
	result := map[string]any{"filename": attachment.Filename, "content_type": attachment.ContentType, "size_bytes": attachment.Size, "download_url": link}
	if attachment.Size > maxInlineAttachment {
		return json.Marshal(result)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, apiError(err)
	}
	req.Header.Set("Authorization", "Bearer "+s.client.Token)
	req.Header.Set("X-Workspace-ID", s.client.WorkspaceID)
	client := *s.client.HTTPClient
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		// Object storage redirects carry a storage signature and never need
		// the member's credentials. Refuse insecure or chained redirects.
		if len(via) != 1 || next.URL.Scheme != "https" || next.URL.User != nil {
			return errors.New("invalid storage redirect")
		}
		next.Header = make(http.Header)
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, apiError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("Agora could not download the attachment")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxInlineAttachment+1))
	if err != nil {
		return nil, errors.New("attachment download was interrupted")
	}
	if len(data) <= maxInlineAttachment {
		result["content_base64"] = data
	}
	return json.Marshal(result)
}
