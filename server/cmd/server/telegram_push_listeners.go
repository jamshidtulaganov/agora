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

// registerAutopilotReportListener posts a completed autopilot run's write-up to
// its configured Telegram chat. Hangs off autopilot:run_done, which the service
// publishes exactly once per run — UpdateAutopilotRunCompleted is guarded on
// completed_at, so an issue walking in_review -> done cannot publish twice and
// the group cannot receive the same report twice.
//
// Only `completed` runs post: a failed run has no report worth broadcasting,
// and the failure already surfaces in the run list.
func registerAutopilotReportListener(bus *events.Bus, h *handler.Handler) {
	// No TelegramPushEnabled() guard: that asks whether the PLATFORM bot
	// exists, and an autopilot whose agent owns its own bot needs nothing from
	// it. Guarding here refused to even subscribe, so a deployment with
	// per-agent bots and no TELEGRAM_BOT_TOKEN never reported at all. Whether
	// there is anywhere to send is decided inside, per run.
	bus.Subscribe(protocol.EventAutopilotRunDone, func(e events.Event) {
		payload, ok := e.Payload.(map[string]any)
		if !ok {
			return
		}
		runID, _ := payload["run_id"].(string)
		if runID == "" {
			return
		}
		// The run is over whatever its outcome, so its throttle state is dead
		// weight. Dropping it only for `completed` leaked an entry for every
		// run that failed or was cancelled — small each, unbounded over weeks.
		h.ForgetAutopilotProgress(runID)
		if status, _ := payload["status"].(string); status != "completed" {
			return
		}
		// Detached: the Bot API call must not sit on the event bus.
		go h.SendAutopilotReport(context.Background(), runID)
	})
}

// registerAutopilotProgressListener relays an agent's own `PROGRESS:` headline
// to the autopilot's Telegram chat while a long run is still going.
//
// The completed-run report answers "what happened" but not "is it still going",
// which is the question a group has while a run that usually takes four minutes
// is twenty minutes in. Throttling and the opt-in flag live in the handler —
// see telegram_progress.go for why each rule is there.
func registerAutopilotProgressListener(bus *events.Bus, h *handler.Handler) {
	bus.Subscribe(protocol.EventTaskMessage, func(e events.Event) {
		payload, ok := e.Payload.(protocol.TaskMessagePayload)
		if !ok {
			return
		}
		if payload.Content == "" {
			return
		}
		go h.RelayAutopilotProgress(context.Background(), payload.TaskID, payload.IssueID, payload.Content)
	})
}

// registerAgentChatReplyListener posts an agent's chat reply back to the
// Telegram group it was asked in.
//
// Hangs off EventTaskCompleted rather than a chat-specific event because the
// assistant message is written on the task-completion path — there is no
// separate "assistant replied" event to subscribe to. Tasks with no chat
// session are ignored here, and SendAgentChatReplyToTelegram then no-ops for
// any session no bot is bound to, so ordinary web chat is untouched.
func registerAgentChatReplyListener(bus *events.Bus, h *handler.Handler) {
	bus.Subscribe(protocol.EventTaskCompleted, func(e events.Event) {
		payload, ok := e.Payload.(map[string]any)
		if !ok {
			return
		}
		sessionID, _ := payload["chat_session_id"].(string)
		if sessionID == "" {
			return
		}
		go h.SendAgentChatReplyToTelegram(context.Background(), sessionID)
	})
}
