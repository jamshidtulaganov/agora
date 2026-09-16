package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// suppress_external_notifications on the create body must ride the
// issue:created event payload — that marker is the only thing standing
// between onboarding seed issues and a configured Telegram report room.
// It must NOT appear for an ordinary create.
func TestCreateIssueSuppressExternalNotificationsMarker(t *testing.T) {
	create := func(t *testing.T, body map[string]any) map[string]any {
		t.Helper()
		drain := recordBusEvents(t, protocol.EventIssueCreated)
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/issues?workspace_id="+testWorkspaceID, body)
		testHandler.CreateIssue(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("CreateIssue: expected 201, got %d: %s", w.Code, w.Body.String())
		}
		var issue IssueResponse
		if err := json.NewDecoder(w.Body).Decode(&issue); err != nil {
			t.Fatalf("decode issue: %v", err)
		}
		t.Cleanup(func() {
			testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issue.ID)
		})
		events := drain()
		if len(events) != 1 {
			t.Fatalf("expected 1 issue:created event, got %d", len(events))
		}
		payload, ok := events[0].Payload.(map[string]any)
		if !ok {
			t.Fatalf("payload type %T", events[0].Payload)
		}
		return payload
	}

	seeded := create(t, map[string]any{
		"title": "Seed issue", "status": "todo", "priority": "low",
		"suppress_external_notifications": true,
	})
	if suppressed, _ := seeded[protocol.EventPayloadSuppressExternalNotifications].(bool); !suppressed {
		t.Fatalf("expected suppress marker on seeded create, payload keys: %v", keysOf(seeded))
	}

	normal := create(t, map[string]any{
		"title": "Ordinary issue", "status": "todo", "priority": "low",
	})
	if _, present := normal[protocol.EventPayloadSuppressExternalNotifications]; present {
		t.Fatal("ordinary create must not carry the suppress marker")
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
