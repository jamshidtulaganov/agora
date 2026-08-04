package handler

import (
	"context"
	"sync"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// eventSink captures bus events of specific types for assertions.
type eventSink struct {
	mu     sync.Mutex
	events map[string][]events.Event
}

func newEventSink(bus *events.Bus, types ...string) *eventSink {
	s := &eventSink{events: map[string][]events.Event{}}
	for _, typ := range types {
		typ := typ
		bus.Subscribe(typ, func(e events.Event) {
			s.mu.Lock()
			s.events[typ] = append(s.events[typ], e)
			s.mu.Unlock()
		})
	}
	return s
}

func (s *eventSink) count(typ string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.events[typ])
}

func (s *eventSink) last(typ string) (events.Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.events[typ]
	if len(list) == 0 {
		return events.Event{}, false
	}
	return list[len(list)-1], true
}

// TestCaptureDeployEvent_PublishesDeployRecorded: a captured deploy-result
// block publishes deploy:recorded (the release-integrations fan-out seam) with
// the expected payload. A non-production target does NOT publish release:shipped.
func TestCaptureDeployEvent_PublishesDeployRecorded(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	sink := newEventSink(testHandler.Bus, protocol.EventDeployRecorded, protocol.EventReleaseShipped)

	issueID := createTestIssue(t, "deploy publishes deploy_recorded", "in_review", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}

	beforeDeploy := sink.count(protocol.EventDeployRecorded)
	beforeShip := sink.count(protocol.EventReleaseShipped)

	content := "Deployed staging.\n\n```deploy-result\n" +
		`{"environment":"staging","ref":"main","status":"success","summary":"green"}` + "\n```\n"
	testHandler.TaskService.CaptureDeployEvent(ctx, issue, content)

	if got := sink.count(protocol.EventDeployRecorded); got != beforeDeploy+1 {
		t.Fatalf("deploy:recorded count = %d, want %d", got, beforeDeploy+1)
	}
	e, _ := sink.last(protocol.EventDeployRecorded)
	m, ok := e.Payload.(map[string]any)
	if !ok {
		t.Fatalf("payload is not a map: %T", e.Payload)
	}
	if m["issue_id"] != issueID || m["target"] != "staging" || m["status"] != "success" || m["ref"] != "main" {
		t.Errorf("unexpected deploy:recorded payload: %v", m)
	}
	// staging is NOT a production-tier env → no release:shipped.
	if got := sink.count(protocol.EventReleaseShipped); got != beforeShip {
		t.Errorf("release:shipped must not fire for a non-production target (got %d, want %d)", got, beforeShip)
	}
}

// TestCaptureDeployEvent_PublishesReleaseShipped: a successful deploy to a
// production-tier environment (project deploy_environments entry with
// requires_human) publishes release:shipped with the routing payload.
func TestCaptureDeployEvent_PublishesReleaseShipped(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	sink := newEventSink(testHandler.Bus, protocol.EventReleaseShipped)

	// A project whose "production" environment is human-gated.
	var projectID string
	settings := `{"deploy_environments":[{"key":"production","label":"Production","requires_human":true}]}`
	if err := testPool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title, status, settings) VALUES ($1, 'release-shipped-proj', 'planned', $2::jsonb) RETURNING id`,
		testWorkspaceID, settings).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM project WHERE id=$1`, projectID) })

	issueID := createTestIssue(t, "prod ship", "in_review", "high")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	if _, err := testPool.Exec(ctx, `UPDATE issue SET project_id=$1 WHERE id=$2`, projectID, issueID); err != nil {
		t.Fatalf("attach project: %v", err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("get issue: %v", err)
	}

	before := sink.count(protocol.EventReleaseShipped)
	content := "Shipped to production.\n\n```deploy-result\n" +
		`{"environment":"production","ref":"main","status":"success","summary":"prod green"}` + "\n```\n"
	testHandler.TaskService.CaptureDeployEvent(ctx, issue, content)

	if got := sink.count(protocol.EventReleaseShipped); got != before+1 {
		t.Fatalf("release:shipped count = %d, want %d", got, before+1)
	}
	e, _ := sink.last(protocol.EventReleaseShipped)
	m, ok := e.Payload.(map[string]any)
	if !ok {
		t.Fatalf("payload is not a map: %T", e.Payload)
	}
	if m["environment"] != "production" || m["project_id"] != projectID {
		t.Errorf("unexpected release:shipped payload: %v", m)
	}
	if _, ok := m["issue_ids"].([]string); !ok {
		t.Errorf("release:shipped payload missing issue_ids: %v", m)
	}
}
