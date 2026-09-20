package main

import (
	"encoding/base64"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/handler"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// configureSlackEnv sets the four keys that make the integration enabled.
// t.Setenv restores them, so the next test still sees a bare deployment.
func configureSlackEnv(t *testing.T) {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	t.Setenv("AGORA_SLACK_SECRET_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("AGORA_SLACK_CLIENT_ID", "123.456")
	t.Setenv("AGORA_SLACK_CLIENT_SECRET", "client-secret")
	t.Setenv("AGORA_SLACK_SIGNING_SECRET", "signing-secret")
}

func clearSlackEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AGORA_SLACK_SECRET_KEY", "")
	t.Setenv("AGORA_SLACK_CLIENT_ID", "")
	t.Setenv("AGORA_SLACK_CLIENT_SECRET", "")
	t.Setenv("AGORA_SLACK_SIGNING_SECRET", "")
}

// A deployment that never configured Slack must not subscribe at all: the
// listeners are on the synchronous, on-request-path bus, so "registered but
// always a no-op" would still cost every write a map lookup and a closure.
func TestRegisterSlackPushListeners_IdlesWhenNotConfigured(t *testing.T) {
	clearSlackEnv(t)
	bus := events.New()
	if got := registerSlackPushListeners(bus, nil); got != 0 {
		t.Fatalf("expected no subscriptions on an unconfigured deployment, got %d", got)
	}
	// And publishing is inert — nothing subscribed, nothing to panic.
	bus.Publish(events.Event{Type: protocol.EventTaskFailed, WorkspaceID: "ws", Payload: map[string]any{"issue_id": "issue"}})
}

func TestRegisterSlackPushListeners_SubscribesWhenConfigured(t *testing.T) {
	configureSlackEnv(t)
	bus := events.New()
	if got := registerSlackPushListeners(bus, nil); got == 0 {
		t.Fatal("expected subscriptions on a configured deployment")
	}
}

// issue:updated fires on every edit; only a STATUS transition has a route
// kind, and decoding the rest would cost a goroutine per keystroke-sized edit.
func TestSlackIssueUpdatedNotification_OnlyStatusTransitions(t *testing.T) {
	statusEvent := events.Event{
		Type:        protocol.EventIssueUpdated,
		WorkspaceID: "ws-1",
		ActorType:   "member",
		ActorID:     "user-1",
		Payload: map[string]any{
			"issue":          handler.IssueResponse{ID: "issue-1", Status: "in_progress"},
			"status_changed": true,
			"prev_status":    "todo",
		},
	}
	n, ok := slackIssueUpdatedNotification(statusEvent)
	if !ok {
		t.Fatal("a status transition must produce a notification")
	}
	if n.IssueID != "issue-1" || n.Status != "in_progress" || n.PrevStatus != "todo" {
		t.Fatalf("notification = %+v", n)
	}

	priorityEvent := events.Event{
		Type:    protocol.EventIssueUpdated,
		Payload: map[string]any{"issue": handler.IssueResponse{ID: "issue-1"}, "priority_changed": true},
	}
	if _, ok := slackIssueUpdatedNotification(priorityEvent); ok {
		t.Fatal("a priority edit must not reach Slack")
	}

	// A payload shape we do not recognise degrades to "no notification", never
	// a panic on the request path.
	if _, ok := slackIssueUpdatedNotification(events.Event{Payload: "not a map"}); ok {
		t.Fatal("an unexpected payload must be ignored")
	}
	if _, ok := slackIssueUpdatedNotification(events.Event{Payload: map[string]any{"status_changed": true}}); ok {
		t.Fatal("a status flag without an issue must be ignored")
	}
}

func TestSlackVerdictNotificationCarriesTheVerdict(t *testing.T) {
	n := slackVerdictNotification(protocol.EventQAEvidenceReady, events.Event{
		WorkspaceID: "ws-1",
		Payload:     map[string]any{"issue_id": "issue-1", "verdict": "fail"},
	})
	if n.IssueID != "issue-1" || n.Verdict != "fail" {
		t.Fatalf("notification = %+v", n)
	}
	// Review verdicts ride the same shape — that is the point of adding
	// review:verdict to the bus rather than a second direct call.
	n = slackVerdictNotification(protocol.EventReviewVerdict, events.Event{
		WorkspaceID: "ws-1",
		Payload:     map[string]any{"issue_id": "issue-2", "verdict": "pass"},
	})
	if n.EventType != protocol.EventReviewVerdict || n.Verdict != "pass" {
		t.Fatalf("notification = %+v", n)
	}
	// Garbage in, empty out.
	if got := slackVerdictNotification(protocol.EventQAEvidenceReady, events.Event{Payload: 42}); got.IssueID != "" {
		t.Fatalf("expected an empty notification, got %+v", got)
	}
}

func TestSlackTaskNotificationSkipsIssuelessTasks(t *testing.T) {
	// A chat or autopilot task has no issue to link to.
	n := slackTaskNotification(protocol.EventTaskFailed, events.Event{
		WorkspaceID: "ws-1",
		Payload:     map[string]any{"task_id": "task-1", "status": "failed"},
	})
	if n.IssueID != "" {
		t.Fatalf("expected no issue id, got %q", n.IssueID)
	}
	n = slackTaskNotification(protocol.EventTaskFailed, events.Event{
		WorkspaceID: "ws-1",
		Payload:     map[string]any{"task_id": "task-1", "issue_id": "issue-1"},
	})
	if n.IssueID != "issue-1" {
		t.Fatalf("notification = %+v", n)
	}
}

// The dev/E2E fixture marker must suppress the Slack fanout on EVERY event
// that carries it, not just the one that sets it today.
func TestSlackFanoutHonoursSuppressionFlag(t *testing.T) {
	payload := map[string]any{
		"issue_id": "issue-1",
		"verdict":  "fail",
		protocol.EventPayloadSuppressExternalNotifications: true,
	}
	if !slackFanoutSuppressed(payload) {
		t.Fatal("the suppression marker was not honoured")
	}
	if n := slackVerdictNotification(protocol.EventQAEvidenceReady, events.Event{Payload: payload}); !n.Suppressed {
		t.Fatal("a suppressed verdict event must be marked suppressed")
	}
	if n := slackTaskNotification(protocol.EventTaskFailed, events.Event{Payload: payload}); !n.Suppressed {
		t.Fatal("a suppressed task event must be marked suppressed")
	}
	if slackFanoutSuppressed(map[string]any{"issue_id": "issue-1"}) {
		t.Fatal("an ordinary event must not be treated as suppressed")
	}
}
