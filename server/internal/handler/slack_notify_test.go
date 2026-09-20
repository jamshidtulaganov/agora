package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/slack"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// ---------------------------------------------------------------------------
// Pure mapping tests
// ---------------------------------------------------------------------------

func TestSlackKindForNotification(t *testing.T) {
	tests := []struct {
		name string
		n    SlackNotification
		want string
	}{
		{name: "task failure", n: SlackNotification{EventType: protocol.EventTaskFailed}, want: slackEventFailed},
		{name: "task completion", n: SlackNotification{EventType: protocol.EventTaskCompleted}, want: slackEventAgentDone},
		{name: "issue created", n: SlackNotification{EventType: protocol.EventIssueCreated}, want: slackEventCreated},
		{name: "assignment", n: SlackNotification{EventType: protocol.EventInboxNew, InboxType: "issue_assigned"}, want: slackEventAssigned},
		{name: "mention", n: SlackNotification{EventType: protocol.EventInboxNew, InboxType: "mention"}, want: slackEventMentioned},
		// A comment is DM-only: there is no channel kind it could ever map to.
		{name: "comment has no channel kind", n: SlackNotification{EventType: protocol.EventInboxNew, InboxType: "comment"}, want: ""},
		{name: "qa failure", n: SlackNotification{EventType: protocol.EventQAEvidenceReady, Verdict: "fail"}, want: slackEventQAVerdict},
		// "channel ON, fail only" — a green gate in a channel is the thing
		// that makes people stop reading it.
		{name: "qa pass is not channel noise", n: SlackNotification{EventType: protocol.EventQAEvidenceReady, Verdict: "pass"}, want: ""},
		{name: "review changes requested", n: SlackNotification{EventType: protocol.EventReviewVerdict, Verdict: "fail"}, want: slackEventReviewVerdict},
		{name: "review pass is not channel noise", n: SlackNotification{EventType: protocol.EventReviewVerdict, Verdict: "pass"}, want: ""},
		{name: "status transition", n: SlackNotification{EventType: protocol.EventIssueUpdated, Status: "done", PrevStatus: "in_review"}, want: slackEventStatusChanged},
		{name: "status non-transition", n: SlackNotification{EventType: protocol.EventIssueUpdated, Status: "done", PrevStatus: "done"}, want: ""},
		{name: "unknown event", n: SlackNotification{EventType: "something:new"}, want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := slackKindForNotification(test.n); got != test.want {
				t.Fatalf("kind = %q, want %q", got, test.want)
			}
		})
	}
}

// DMs cover every inbox type — the item's existence already proves the
// recipient wants it — so an unknown type gets a generic headline instead of
// being dropped.
func TestSlackDMKindCoversUnknownInboxTypes(t *testing.T) {
	if got := slackDMKindFor("comment"); got != slackEventCommented {
		t.Fatalf("comment DM kind = %q", got)
	}
	if got := slackDMKindFor("reaction_added"); got != slackDMKindUpdate {
		t.Fatalf("unknown inbox types must still DM, got %q", got)
	}
}

func TestSlackIssueURLIsTheCanonicalShape(t *testing.T) {
	t.Setenv("AGORA_APP_URL", "https://app.agora.test/")
	t.Setenv("FRONTEND_ORIGIN", "")
	if got := slackIssueURL("https://api.agora.test", "acme", "MUL-12"); got != "https://app.agora.test/acme/issues/MUL-12" {
		t.Fatalf("issue url = %q", got)
	}
	// The API origin is the last resort, and a missing slug means no link at
	// all rather than a link that 404s.
	t.Setenv("AGORA_APP_URL", "")
	if got := slackIssueURL("https://api.agora.test", "acme", "MUL-12"); got != "https://api.agora.test/acme/issues/MUL-12" {
		t.Fatalf("issue url = %q", got)
	}
	if got := slackIssueURL("https://api.agora.test", "", "MUL-12"); got != "" {
		t.Fatalf("expected no link without a slug, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Fanout, end to end against a fake Slack
// ---------------------------------------------------------------------------

// slackPostRecorder is a fake slack.com that records chat.postMessage bodies
// and answers conversations.open.
type slackPostRecorder struct {
	mu    sync.Mutex
	posts []slack.PostMessageRequest
	opens []string
}

func (r *slackPostRecorder) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/chat.postMessage":
			var body slack.PostMessageRequest
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("decode chat.postMessage: %v", err)
			}
			r.mu.Lock()
			r.posts = append(r.posts, body)
			r.mu.Unlock()
			io.WriteString(w, `{"ok":true,"channel":"`+body.Channel+`","ts":"1700000000.000100"}`)
		case "/conversations.open":
			var body struct {
				Users string `json:"users"`
			}
			json.NewDecoder(req.Body).Decode(&body)
			r.mu.Lock()
			r.opens = append(r.opens, body.Users)
			r.mu.Unlock()
			io.WriteString(w, `{"ok":true,"channel":{"id":"D0`+body.Users+`"}}`)
		default:
			t.Errorf("unexpected slack call: %s", req.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func (r *slackPostRecorder) snapshot() ([]slack.PostMessageRequest, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	posts := make([]slack.PostMessageRequest, len(r.posts))
	copy(posts, r.posts)
	opens := make([]string, len(r.opens))
	copy(opens, r.opens)
	return posts, opens
}

// startSlackFanoutTest wires a fake Slack, an installation and a delivery
// worker with a negligible coalescing window, and returns the recorder plus
// the dispatcher to drain.
func startSlackFanoutTest(t *testing.T, teamID string) (*slackPostRecorder, *slack.Dispatcher, string) {
	t.Helper()
	configureSlack(t)
	recorder := &slackPostRecorder{}
	fakeSlackAPI(t, recorder.handler(t))
	installID := seedSlackInstallation(t, teamID)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// A nanosecond window still exercises coalescing and pacing; the
	// production 10s would make every case a 10-second test.
	d := testHandler.startSlackDelivery(ctx, slack.Options{Window: time.Nanosecond, Interval: time.Nanosecond})
	t.Cleanup(func() { testHandler.StopSlackDelivery() })
	return recorder, d, installID
}

func TestSlackFanout_DeliversOnlyToSubscribedChannels(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	recorder, dispatcher, installID := startSlackFanoutTest(t, "T0FAN1")

	issueID := createTestIssue(t, "Slack fanout target", "todo", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	putSlackRoute(t, "", map[string]any{
		"installation_id": installID, "channel_id": "C0ENG", "events": []string{slackEventFailed},
	})
	putSlackRoute(t, "", map[string]any{
		"installation_id": installID, "channel_id": "C0NOISE", "events": []string{slackEventCreated},
	})

	testHandler.fanOutSlackNotification(context.Background(), SlackNotification{
		EventType:   protocol.EventTaskFailed,
		WorkspaceID: testWorkspaceID,
		IssueID:     issueID,
	})
	dispatcher.Wait()

	posts, _ := recorder.snapshot()
	if len(posts) != 1 {
		t.Fatalf("expected exactly one message, got %d (%+v)", len(posts), posts)
	}
	if posts[0].Channel != "C0ENG" {
		t.Fatalf("delivered to %q; a channel that did not subscribe must stay quiet", posts[0].Channel)
	}
	if posts[0].Text == "" {
		t.Fatal("every message needs fallback text for push notifications")
	}
	if len(posts[0].Blocks) == 0 {
		t.Fatal("expected a Block Kit body")
	}
}

func TestSlackFanout_SkipsUnroutedKinds(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	recorder, dispatcher, installID := startSlackFanoutTest(t, "T0FAN2")
	issueID := createTestIssue(t, "Slack unrouted kind", "todo", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	putSlackRoute(t, "", map[string]any{
		"installation_id": installID, "channel_id": "C0ENG", "events": []string{slackEventFailed},
	})

	// issue:created is opt-in and this route did not opt in.
	testHandler.fanOutSlackNotification(context.Background(), SlackNotification{
		EventType:   protocol.EventIssueCreated,
		WorkspaceID: testWorkspaceID,
		IssueID:     issueID,
	})
	// A PASSING gate verdict is never channel noise, even on a route that
	// subscribed to the verdict kind.
	putSlackRoute(t, "", map[string]any{
		"installation_id": installID, "channel_id": "C0QA", "events": []string{slackEventQAVerdict},
	})
	testHandler.fanOutSlackNotification(context.Background(), SlackNotification{
		EventType:   protocol.EventQAEvidenceReady,
		WorkspaceID: testWorkspaceID,
		IssueID:     issueID,
		Verdict:     "pass",
	})
	dispatcher.Wait()

	if posts, _ := recorder.snapshot(); len(posts) != 0 {
		t.Fatalf("expected silence, got %+v", posts)
	}

	// The same route DOES hear the failure.
	testHandler.fanOutSlackNotification(context.Background(), SlackNotification{
		EventType:   protocol.EventQAEvidenceReady,
		WorkspaceID: testWorkspaceID,
		IssueID:     issueID,
		Verdict:     "fail",
	})
	dispatcher.Wait()
	posts, _ := recorder.snapshot()
	if len(posts) != 1 || posts[0].Channel != "C0QA" {
		t.Fatalf("expected one message to C0QA, got %+v", posts)
	}
}

// A project-scoped route hears that project only — otherwise "route this
// channel to the mobile project" quietly becomes "route everything".
func TestSlackFanout_ProjectScopedRouteIgnoresOtherProjects(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	recorder, dispatcher, installID := startSlackFanoutTest(t, "T0FAN3")

	var projectID string
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO project (workspace_id, title, status, priority) VALUES ($1::uuid, 'Slack Scoped', 'planned', 'none') RETURNING id::text`,
		testWorkspaceID).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1::uuid`, projectID)
	})

	issueID := createTestIssue(t, "Slack project scope", "todo", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	putSlackRoute(t, "", map[string]any{
		"installation_id": installID, "channel_id": "C0MOBILE",
		"project_id": projectID, "events": []string{slackEventFailed},
	})
	testHandler.fanOutSlackNotification(context.Background(), SlackNotification{
		EventType:   protocol.EventTaskFailed,
		WorkspaceID: testWorkspaceID,
		IssueID:     issueID, // created without a project
	})
	dispatcher.Wait()
	if posts, _ := recorder.snapshot(); len(posts) != 0 {
		t.Fatalf("a project-scoped route heard an issue from another project: %+v", posts)
	}
}

// The dev/E2E fixture marker must never leave the deployment.
func TestSlackNotify_SuppressedEventProducesNoDelivery(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	recorder, dispatcher, installID := startSlackFanoutTest(t, "T0FAN4")
	issueID := createTestIssue(t, "Slack suppressed", "todo", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	putSlackRoute(t, "", map[string]any{
		"installation_id": installID, "channel_id": "C0ENG", "events": []string{slackEventFailed},
	})

	testHandler.SlackNotify(SlackNotification{
		EventType:   protocol.EventTaskFailed,
		WorkspaceID: testWorkspaceID,
		IssueID:     issueID,
		Suppressed:  true,
	})
	dispatcher.Wait()
	if posts, _ := recorder.snapshot(); len(posts) != 0 {
		t.Fatalf("a suppressed event reached Slack: %+v", posts)
	}

	// The same event without the marker does deliver — so the assertion above
	// is about suppression and not about a broken fixture.
	testHandler.SlackNotify(SlackNotification{
		EventType:   protocol.EventTaskFailed,
		WorkspaceID: testWorkspaceID,
		IssueID:     issueID,
	})
	waitForSlackPosts(t, recorder, 1)
}

// With the integration switched off, the entry point must not even look at
// the database: it is called from a synchronous, on-request-path bus handler.
func TestSlackNotify_InertWhenIntegrationIsDisabled(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	recorder, dispatcher, installID := startSlackFanoutTest(t, "T0FAN5")
	issueID := createTestIssue(t, "Slack disabled", "todo", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })
	putSlackRoute(t, "", map[string]any{
		"installation_id": installID, "channel_id": "C0ENG", "events": []string{slackEventFailed},
	})

	t.Setenv("AGORA_SLACK_CLIENT_ID", "")
	testHandler.SlackNotify(SlackNotification{
		EventType:   protocol.EventTaskFailed,
		WorkspaceID: testWorkspaceID,
		IssueID:     issueID,
	})
	dispatcher.Wait()
	if posts, _ := recorder.snapshot(); len(posts) != 0 {
		t.Fatalf("a disabled integration delivered anyway: %+v", posts)
	}
}

// Mute inheritance in action: the DM hangs off an inbox item, and the item
// exists only because the recipient's own notification preferences allowed it.
func TestSlackFanout_DMsTheLinkedInboxRecipient(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	recorder, dispatcher, _ := startSlackFanoutTest(t, "T0FAN6")
	issueID := createTestIssue(t, "Slack DM target", "todo", "medium")
	t.Cleanup(func() { deleteTestIssue(t, issueID) })

	// No channel route at all: this case is purely the personal DM.
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO user_external_identity (provider, external_id, user_id) VALUES ('slack', 'U0ALICE', $1::uuid)`,
		testUserID); err != nil {
		t.Fatalf("link slack identity: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM user_external_identity WHERE provider = 'slack' AND external_id = 'U0ALICE'`)
	})

	testHandler.fanOutSlackNotification(context.Background(), SlackNotification{
		EventType:     protocol.EventInboxNew,
		WorkspaceID:   testWorkspaceID,
		IssueID:       issueID,
		InboxType:     "comment",
		RecipientType: "member",
		RecipientID:   testUserID,
	})
	dispatcher.Wait()

	posts, opens := recorder.snapshot()
	if len(opens) != 1 || opens[0] != "U0ALICE" {
		t.Fatalf("expected one conversations.open for the linked user, got %v", opens)
	}
	if len(posts) != 1 || posts[0].Channel != "D0U0ALICE" {
		t.Fatalf("expected one DM to the IM channel, got %+v", posts)
	}

	// An unlinked recipient is silence, not an error: they simply have not
	// connected Slack.
	testHandler.fanOutSlackNotification(context.Background(), SlackNotification{
		EventType:     protocol.EventInboxNew,
		WorkspaceID:   testWorkspaceID,
		IssueID:       issueID,
		InboxType:     "comment",
		RecipientType: "member",
		RecipientID:   "00000000-0000-0000-0000-000000000000",
	})
	dispatcher.Wait()
	if posts, _ := recorder.snapshot(); len(posts) != 1 {
		t.Fatalf("an unlinked recipient produced a message: %+v", posts)
	}
}

// waitForSlackPosts waits for an asynchronous SlackNotify to land.
func waitForSlackPosts(t *testing.T, recorder *slackPostRecorder, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if posts, _ := recorder.snapshot(); len(posts) >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	posts, _ := recorder.snapshot()
	t.Fatalf("expected %d message(s), got %d (%+v)", want, len(posts), posts)
}
