package agoramcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jamshidtulaganov/agora/server/internal/cli"
)

// Use Agora's thread summarizer when configured. A workspace without a model
// can still provide a sourced extractive brief for Claude to synthesize.
func (s *Server) summarizeComments(ctx context.Context, issuePath string) (json.RawMessage, error) {
	var result json.RawMessage
	err := s.client.PostJSON(ctx, issuePath+"/comments/summarize", nil, &result)
	if err == nil {
		return result, nil
	}
	var upstream *cli.HTTPError
	if !errors.As(err, &upstream) || (upstream.StatusCode != 502 && upstream.StatusCode != 503) {
		return nil, apiError(err)
	}
	var comments []struct {
		ID      string `json:"id"`
		Content string `json:"content"`
	}
	if err := s.client.GetJSON(ctx, issuePath+"/comments?recent=5&summary=true", &comments); err != nil {
		return nil, apiError(err)
	}
	if len(comments) > 10 {
		comments = comments[len(comments)-10:]
	}
	var lines []string
	for _, comment := range comments {
		if excerpt := strings.TrimSpace(comment.Content); excerpt != "" {
			lines = append(lines, "- "+excerpt+" ["+comment.ID+"]")
		}
	}
	return json.Marshal(map[string]string{"mode": "recent_comment_excerpts", "summary": strings.Join(lines, "\n")})
}
