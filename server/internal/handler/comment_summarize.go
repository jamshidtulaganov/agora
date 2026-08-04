package handler

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/llm"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// zhipuAPIKey reads the free-model key. Empty => the summarize feature 503s.
func zhipuAPIKey() string {
	return strings.TrimSpace(os.Getenv("ZHIPU_API_KEY"))
}

// SummarizeCommentsResponse is returned by POST /api/issues/{id}/comments/summarize.
type SummarizeCommentsResponse struct {
	Summary string `json:"summary"`
}

// SummarizeComments condenses an issue's comment thread into a short brief using
// the free Agora base model (Zhipu glm-4-flash). Best-effort product feature, not
// the agent runtime — a single completion call, no task is created.
func (h *Handler) SummarizeComments(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	apiKey := zhipuAPIKey()
	if apiKey == "" {
		writeError(w, http.StatusServiceUnavailable, "AI summary is not configured")
		return
	}

	comments, err := h.Queries.ListCommentsForIssue(r.Context(), db.ListCommentsForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		Limit:       commentHardCap,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load comments")
		return
	}

	transcript := buildCommentTranscript(issue.Title, comments)
	if transcript == "" {
		writeJSON(w, http.StatusOK, SummarizeCommentsResponse{Summary: ""})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	client := llm.NewZhipuClient(apiKey)
	summary, err := client.Complete(ctx, llm.FreeModel, []llm.Message{
		{Role: "system", Content: commentSummarySystemPrompt},
		{Role: "user", Content: transcript},
	})
	if err != nil {
		if errors.Is(err, llm.ErrNotConfigured) {
			writeError(w, http.StatusServiceUnavailable, "AI summary is not configured")
			return
		}
		writeError(w, http.StatusBadGateway, "AI summary failed, try again")
		return
	}

	writeJSON(w, http.StatusOK, SummarizeCommentsResponse{Summary: summary})
}

const commentSummarySystemPrompt = "You are a concise project-management assistant. " +
	"Summarize the discussion thread on a task into a short brief: the key decisions, " +
	"open questions, and any action items (as a short bullet list). Keep it under 120 words. " +
	"Reply in the SAME language the comments are written in (usually Russian or Uzbek). " +
	"Do not invent information that is not in the thread."

// buildCommentTranscript renders the thread as plain "Role: text" lines, bounded
// so a runaway thread can't blow up the token budget. Empty when nothing usable.
func buildCommentTranscript(title string, comments []db.Comment) string {
	const maxChars = 12000
	var b strings.Builder
	if title != "" {
		b.WriteString("Task: ")
		b.WriteString(title)
		b.WriteString("\n\nComments (oldest first):\n")
	}
	wrote := false
	for _, c := range comments {
		content := strings.TrimSpace(c.Content)
		if content == "" {
			continue
		}
		author := "Team member"
		if c.AuthorType == "agent" {
			author = "Agent"
		}
		line := author + ": " + content + "\n"
		if b.Len()+len(line) > maxChars {
			break
		}
		b.WriteString(line)
		wrote = true
	}
	if !wrote {
		return ""
	}
	return b.String()
}
