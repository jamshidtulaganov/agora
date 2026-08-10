package main

import (
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/handler"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

func TestIssueCreatedEventIDAcceptsEveryPublishedShape(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]any
	}{
		{name: "handler response", payload: map[string]any{"issue": handler.IssueResponse{ID: "issue-typed"}}},
		{name: "autopilot map", payload: map[string]any{"issue": map[string]any{"id": "issue-map"}}},
		{name: "minimal service event", payload: map[string]any{"issue_id": "issue-id-only"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := issueCreatedEventID(test.payload); got == "" {
				t.Fatal("valid issue-created payload was ignored")
			}
		})
	}
}

func TestIssueCreatedListenerSuppressesDevFixtureFanout(t *testing.T) {
	payload := map[string]any{
		"issue_id": "fixture-issue",
		protocol.EventPayloadSuppressExternalNotifications: true,
	}
	if !issueCreatedExternalNotificationsSuppressed(payload) {
		t.Fatal("expected the E2E event marker to suppress external notification fanout")
	}
}
