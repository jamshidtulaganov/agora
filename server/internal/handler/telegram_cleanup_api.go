package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/config"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// deleteTelegramFixtureMessagesRequest deliberately accepts message links,
// rather than raw chat/message ids. The operator must select the exact Telegram
// messages to remove, and the handler can derive the private-supergroup chat id
// without ever exposing the platform bot token to the client.
type deleteTelegramFixtureMessagesRequest struct {
	MessageLinks []string `json:"message_links"`
}

type telegramFixtureMessageTarget struct {
	ChatID    string
	MessageID int64
}

// DeleteTelegramFixtureMessages removes explicitly selected notices from the
// workspace's configured report chat. The route is owner/admin and human-only,
// accepts only canonical private-group message links, and cannot target a chat
// outside the workspace's effective report configuration.
func (h *Handler) DeleteTelegramFixtureMessages(w http.ResponseWriter, r *http.Request) {
	workspaceID := chi.URLParam(r, "id")
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	if h.telegramBot == nil {
		writeError(w, http.StatusServiceUnavailable, "telegram platform bot is not configured")
		return
	}

	var req deleteTelegramFixtureMessagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.MessageLinks) == 0 || len(req.MessageLinks) > 50 {
		writeError(w, http.StatusBadRequest, "message_links must contain 1 to 50 links")
		return
	}

	targets := make([]telegramFixtureMessageTarget, 0, len(req.MessageLinks))
	seen := make(map[telegramFixtureMessageTarget]struct{}, len(req.MessageLinks))
	for _, raw := range req.MessageLinks {
		target, err := parseTelegramPrivateMessageLink(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid Telegram private message link")
			return
		}
		if _, duplicate := seen[target]; duplicate {
			continue
		}
		seen[target] = struct{}{}
		targets = append(targets, target)
	}

	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	for _, target := range targets {
		if !h.workspaceUsesTelegramReportChat(r.Context(), wsUUID, target.ChatID) {
			writeError(w, http.StatusForbidden, "message chat is not configured for this workspace")
			return
		}
	}

	deleted := make([]int64, 0, len(targets))
	for _, target := range targets {
		if err := h.telegramBot.DeleteMessage(r.Context(), target.ChatID, target.MessageID); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error":               "telegram failed to delete a selected message",
				"telegram_error":      err.Error(),
				"deleted_message_ids": deleted,
			})
			return
		}
		deleted = append(deleted, target.MessageID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted_message_ids": deleted})
}

func parseTelegramPrivateMessageLink(raw string) (telegramFixtureMessageTarget, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "https") || !strings.EqualFold(u.Host, "t.me") {
		return telegramFixtureMessageTarget{}, strconv.ErrSyntax
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 3 || parts[0] != "c" {
		return telegramFixtureMessageTarget{}, strconv.ErrSyntax
	}
	internalChatID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || internalChatID <= 0 {
		return telegramFixtureMessageTarget{}, strconv.ErrSyntax
	}
	messageID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || messageID <= 0 {
		return telegramFixtureMessageTarget{}, strconv.ErrSyntax
	}
	return telegramFixtureMessageTarget{
		ChatID:    "-100" + strconv.FormatInt(internalChatID, 10),
		MessageID: messageID,
	}, nil
}

func (h *Handler) workspaceUsesTelegramReportChat(ctx context.Context, workspaceID pgtype.UUID, chatID string) bool {
	if strings.TrimSpace(config.StringFrom(nil, "AGORA_TELEGRAM_REPORT_CHAT_ID")) == chatID {
		return true
	}
	projects, err := h.Queries.ListProjects(ctx, db.ListProjectsParams{WorkspaceID: workspaceID})
	if err != nil {
		return false
	}
	for _, project := range projects {
		overrides := parseProjectConfigOverrides(project.Settings)
		if strings.TrimSpace(config.StringFrom(overrides, "AGORA_TELEGRAM_REPORT_CHAT_ID")) == chatID {
			return true
		}
	}
	return false
}
