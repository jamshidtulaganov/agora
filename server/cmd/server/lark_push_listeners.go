package main

import (
	"context"
	"encoding/json"

	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/handler"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// registerLarkPushListeners fans out a best-effort Lark card for each new inbox
// item, sent from the bot bound to the issue's assigned agent. Like the
// Telegram fan-out it hangs off the SAME EventInboxNew the WS broadcaster uses,
// so it covers every inbox source (assign, mention, comment, reaction,
// task_failed) and inherits mute filtering for free — a muted notification
// never creates an inbox item, so it never pushes.
//
// No-op unless Lark is wired (encryption key + live API client). The actual
// resolve + send runs on a detached goroutine inside SendIssueInboxLarkDM, so
// this subscriber never sits on the request path.
func registerLarkPushListeners(bus *events.Bus, h *handler.Handler) {
	if h.LarkInstallations == nil || h.LarkAPIClient == nil {
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
		// non-issue notifications, which we skip (the card opens an issue).
		var issueID string
		if p, ok := item["issue_id"].(*string); ok && p != nil {
			issueID = *p
		}
		if issueID == "" {
			return
		}
		body, _ := item["body"].(*string)
		actorType := derefString(item["actor_type"])
		actorID := derefString(item["actor_id"])
		details, _ := item["details"].(json.RawMessage)
		h.SendIssueInboxLarkDM(context.Background(), recipientType, recipientID, issueID, notifType, title, body, actorType, actorID, details)
	})
}
