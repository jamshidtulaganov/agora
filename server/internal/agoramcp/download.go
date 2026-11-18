package agoramcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jamshidtulaganov/agora/server/internal/util"
)

const maxInlineAttachment = 512 << 10

// Screenshots routinely exceed the generic limit and are only useful to a
// model when delivered whole.
const maxInlineImage = 3 << 20

// Image formats MCP clients render as image blocks.
var inlineImageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

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
	limit := maxInlineAttachment
	if strings.HasPrefix(attachment.ContentType, "image/") {
		limit = maxInlineImage
	}
	if attachment.Size > int64(limit) {
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
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return nil, errors.New("attachment download was interrupted")
	}
	// Classify by the bytes; the stored content type is uploader-supplied.
	kind, _, _ := strings.Cut(http.DetectContentType(data), ";")
	switch {
	case len(data) > limit:
	case inlineImageTypes[kind]:
		result["content_base64"], result["inline_image_type"] = data, kind
	case len(data) > maxInlineAttachment:
	case strings.HasPrefix(kind, "text/") && utf8.Valid(data):
		result["content_text"] = string(data)
	default:
		result["content_base64"] = data
	}
	return json.Marshal(result)
}

// splitInlineImage separates an inline image from a download result so the
// client receives an image block rather than base64 inside JSON text. It
// returns the result unchanged when there is no inline image.
func splitInlineImage(result json.RawMessage) (text string, image []byte, mimeType string) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(result, &fields) != nil || json.Unmarshal(fields["inline_image_type"], &mimeType) != nil || !inlineImageTypes[mimeType] {
		return string(result), nil, ""
	}
	if json.Unmarshal(fields["content_base64"], &image) != nil || len(image) == 0 {
		return string(result), nil, ""
	}
	delete(fields, "content_base64")
	meta, err := json.Marshal(fields)
	if err != nil {
		return string(result), nil, ""
	}
	return string(meta), image, mimeType
}
