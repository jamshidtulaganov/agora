package main

import (
	"context"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// registerTelegramPushListeners fans out a best-effort Telegram DM for each new
// inbox item to member recipients who have linked Telegram. It hangs off the
// SAME EventInboxNew the WS broadcaster uses, so it covers every inbox source
// (assign, mention, comment, reaction, task_failed) WITHOUT touching the
// inbox-creating notify* functions — and because a muted notification never
// creates an inbox item, push inherits the mute filtering for free.
//
// No-op when the bot is unconfigured. The actual DM (lookup + Bot API call) runs
// on a detached goroutine inside SendIssueInboxDM, so this subscriber never sits
// on the request path.
func registerTelegramPushListeners(bus *events.Bus, h *handler.Handler) {
	if !h.TelegramPushEnabled() {
		return
	}
	bus.Subscribe(protocol.EventInboxNew, func(e events.Event) {
		payload, ok := e.Payload.(map[string]any)
		if !ok {
			return
		}
		item, ok := payload["item"].(map[string]any)
		if !ok {
			return
		}
		recipientType, _ := item["recipient_type"].(string)
		recipientID, _ := item["recipient_id"].(string)
		notifType, _ := item["type"].(string)
		title, _ := item["title"].(string)
		// issue_id is a *string in inboxItemToResponse (UUIDToPtr); nil for
		// non-issue notifications, which we skip (the Mini App opens an issue).
		var issueID string
		if p, ok := item["issue_id"].(*string); ok && p != nil {
			issueID = *p
		}
		if issueID == "" {
			return
		}
		h.SendIssueInboxDM(context.Background(), recipientType, recipientID, issueID, notifType, title)
	})
}
