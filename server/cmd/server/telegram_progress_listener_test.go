package main

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// The wiring, not the handler. Three live runs produced no relay while every
// unit test passed, because the tests exercised the throttle and the parser and
// never the path an actual event takes. This asserts a published task message
// reaches a subscriber with its Content intact and its concrete payload type —
// the two things a silent listener gets wrong.
func TestTaskMessageEventCarriesTypedPayload(t *testing.T) {
	bus := events.New()
	var got protocol.TaskMessagePayload
	var hits int
	bus.Subscribe(protocol.EventTaskMessage, func(e events.Event) {
		payload, ok := e.Payload.(protocol.TaskMessagePayload)
		if !ok {
			t.Errorf("payload is %T, not protocol.TaskMessagePayload", e.Payload)
			return
		}
		got = payload
		hits++
	})

	bus.Publish(events.Event{
		Type:        protocol.EventTaskMessage,
		WorkspaceID: "ws",
		TaskID:      "task-1",
		Payload: protocol.TaskMessagePayload{
			TaskID:  "task-1",
			IssueID: "issue-1",
			Content: "PROGRESS: aggregating by tag",
		},
	})

	if hits != 1 {
		t.Fatalf("subscriber ran %d times, want 1", hits)
	}
	if got.Content == "" || got.TaskID != "task-1" {
		t.Fatalf("payload arrived empty: %+v", got)
	}
}
