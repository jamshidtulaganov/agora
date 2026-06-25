package main

import (
	"context"
	"encoding/json"

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
		// Enrichment fields — all optional (see SendIssueInboxDM). body carries
		// the comment text; actor_* names who acted; details holds {from,to} for
		// status/priority transitions.
		body, _ := item["body"].(*string)
		actorType := derefString(item["actor_type"])
		actorID := derefString(item["actor_id"])
		details, _ := item["details"].(json.RawMessage)
		h.SendIssueInboxDM(context.Background(), recipientType, recipientID, issueID, notifType, title, body, actorType, actorID, details)
	})
}

// derefString unwraps the *string values inboxItemToResponse stores for nullable
// text columns (actor_type / actor_id), returning "" for nil or a non-string.
func derefString(v any) string {
	if p, ok := v.(*string); ok && p != nil {
		return *p
	}
	return ""
}
