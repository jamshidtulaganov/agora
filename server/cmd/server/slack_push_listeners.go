package main

import (
	"log/slog"
	"strings"

	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/handler"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// Slack push listeners — the bus seam for the Slack app's outbound half
// (docs/slack-integration-plan.md §Phase 1 "Event seams").
//
// A direct transliteration of telegram_push_listeners.go, and deliberately so:
// this file only DESTRUCTURES bus payloads and hands the facts to
// handler.SlackNotify. Every product decision — which kinds a channel hears,
// what the copy says, how deliveries are paced — lives in the handler package,
// so adding an event here is a five-line change and never a policy change.
//
// Two properties are load-bearing and both come for free from where these
// subscriptions hang:
//
//   - Mute inheritance. Personal DMs hang off EventInboxNew, and a muted
//     notification never creates an inbox item — so a user's existing
//     notification preferences already govern their Slack DMs, with no second
//     configuration surface to keep in sync. This is the single best property
//     of the design and it costs nothing; do not add a DM path that bypasses
//     the inbox.
//   - Suppression. Every fanout honours
//     EventPayloadSuppressExternalNotifications, so a dev/E2E fixture event
//     still reaches websocket subscribers and never leaves the deployment.
//
// The bus is synchronous and on the HTTP request path, so each subscriber
// destructures and returns; handler.SlackNotify detaches before touching the
// database or the network.

// registerSlackPushListeners subscribes the routed event kinds. It returns how
// many subscriptions it made so the boot log can state, in one line, whether
// this deployment will speak to Slack at all — a half-configured deployment
// that silently never posts is the failure mode this line exists to prevent.
// SlackEnabled reads only the environment, so this gate is safe before the
// handler is fully wired.
func registerSlackPushListeners(bus *events.Bus, h *handler.Handler) int {
	if !h.SlackEnabled() {
		slog.Info("slack: push listeners not registered (integration not configured)")
		return 0
	}

	// Personal DMs. Covers every inbox source (assign, mention, comment,
	// reaction, task_failed) without touching the inbox-creating notify*
	// functions.
	bus.Subscribe(protocol.EventInboxNew, func(e events.Event) {
		payload, ok := e.Payload.(map[string]any)
		if !ok {
			return
		}
		item, ok := payload["item"].(map[string]any)
		if !ok {
			return
		}
		// issue_id is a *string in inboxItemToResponse (UUIDToPtr); nil for
		// non-issue notifications, which have nothing to link to.
		issueID := ""
		if p, ok := item["issue_id"].(*string); ok && p != nil {
			issueID = *p
		}
		recipientType, _ := item["recipient_type"].(string)
		recipientID, _ := item["recipient_id"].(string)
		notifType, _ := item["type"].(string)
		h.SlackNotify(handler.SlackNotification{
			EventType:     protocol.EventInboxNew,
			WorkspaceID:   e.WorkspaceID,
			IssueID:       issueID,
			ActorType:     derefString(item["actor_type"]),
			ActorID:       derefString(item["actor_id"]),
			InboxType:     notifType,
			RecipientType: recipientType,
			RecipientID:   recipientID,
			Suppressed:    slackFanoutSuppressed(payload),
		})
	})

	// An agent task failed — the one channel event a fresh install hears that
	// is not a gate verdict.
	bus.Subscribe(protocol.EventTaskFailed, func(e events.Event) {
		h.SlackNotify(slackTaskNotification(protocol.EventTaskFailed, e))
	})
	bus.Subscribe(protocol.EventTaskCompleted, func(e events.Event) {
		h.SlackNotify(slackTaskNotification(protocol.EventTaskCompleted, e))
	})

	// QA and review verdicts. Both fan out failures only (slack_notify.go
	// decides that, not this file).
	bus.Subscribe(protocol.EventQAEvidenceReady, func(e events.Event) {
		h.SlackNotify(slackVerdictNotification(protocol.EventQAEvidenceReady, e))
	})
	bus.Subscribe(protocol.EventReviewVerdict, func(e events.Event) {
		h.SlackNotify(slackVerdictNotification(protocol.EventReviewVerdict, e))
	})

	// Opt-in kinds: a channel hears these only if an admin ticked them.
	bus.Subscribe(protocol.EventIssueCreated, func(e events.Event) {
		payload, ok := e.Payload.(map[string]any)
		if !ok {
			return
		}
		h.SlackNotify(handler.SlackNotification{
			EventType:   protocol.EventIssueCreated,
			WorkspaceID: e.WorkspaceID,
			IssueID:     issueCreatedEventID(payload),
			ActorType:   e.ActorType,
			ActorID:     e.ActorID,
			Suppressed:  slackFanoutSuppressed(payload),
		})
	})
	bus.Subscribe(protocol.EventIssueUpdated, func(e events.Event) {
		n, ok := slackIssueUpdatedNotification(e)
		if !ok {
			return
		}
		h.SlackNotify(n)
	})

	const subscriptions = 7
	slog.Info("slack: push listeners registered", "subscriptions", subscriptions)
	return subscriptions
}

// slackTaskNotification destructures a task:* event. The payload is
// broadcastTaskEvent's map: task_id / agent_id / issue_id / status.
func slackTaskNotification(eventType string, e events.Event) handler.SlackNotification {
	n := handler.SlackNotification{
		EventType:   eventType,
		WorkspaceID: e.WorkspaceID,
		ActorType:   e.ActorType,
		ActorID:     e.ActorID,
	}
	payload, ok := e.Payload.(map[string]any)
	if !ok {
		return n
	}
	issueID, _ := payload["issue_id"].(string)
	n.IssueID = strings.TrimSpace(issueID)
	n.Suppressed = slackFanoutSuppressed(payload)
	// A chat or autopilot task has no issue; SlackNotify drops it on the empty
	// issue id, which is the right answer — a channel notification with
	// nothing to link to is noise.
	return n
}

// slackVerdictNotification destructures a gate verdict event
// (qa_evidence:ready, review:verdict): issue_id + verdict.
func slackVerdictNotification(eventType string, e events.Event) handler.SlackNotification {
	n := handler.SlackNotification{
		EventType:   eventType,
		WorkspaceID: e.WorkspaceID,
		ActorType:   e.ActorType,
		ActorID:     e.ActorID,
	}
	payload, ok := e.Payload.(map[string]any)
	if !ok {
		return n
	}
	issueID, _ := payload["issue_id"].(string)
	verdict, _ := payload["verdict"].(string)
	n.IssueID = strings.TrimSpace(issueID)
	n.Verdict = strings.TrimSpace(verdict)
	n.Suppressed = slackFanoutSuppressed(payload)
	return n
}

// slackIssueUpdatedNotification destructures issue:updated, keeping only a
// STATUS transition. Everything else on that event (priority, dates, labels)
// has no route kind, so decoding it further would cost a goroutine per edit.
func slackIssueUpdatedNotification(e events.Event) (handler.SlackNotification, bool) {
	payload, ok := e.Payload.(map[string]any)
	if !ok {
		return handler.SlackNotification{}, false
	}
	if statusChanged, _ := payload["status_changed"].(bool); !statusChanged {
		return handler.SlackNotification{}, false
	}
	issue, ok := payload["issue"].(handler.IssueResponse)
	if !ok {
		return handler.SlackNotification{}, false
	}
	prevStatus, _ := payload["prev_status"].(string)
	return handler.SlackNotification{
		EventType:   protocol.EventIssueUpdated,
		WorkspaceID: e.WorkspaceID,
		IssueID:     strings.TrimSpace(issue.ID),
		ActorType:   e.ActorType,
		ActorID:     e.ActorID,
		Status:      strings.TrimSpace(issue.Status),
		PrevStatus:  strings.TrimSpace(prevStatus),
		Suppressed:  slackFanoutSuppressed(payload),
	}, true
}

// slackFanoutSuppressed reports the dev/E2E fixture marker. Checked on EVERY
// fanout, not just the one event that sets it today: the flag's contract is
// "this event must not leave the deployment", and a listener that honours it
// only where it is currently set would break the first time it is set
// somewhere else.
func slackFanoutSuppressed(payload map[string]any) bool {
	suppressed, _ := payload[protocol.EventPayloadSuppressExternalNotifications].(bool)
	return suppressed
}
