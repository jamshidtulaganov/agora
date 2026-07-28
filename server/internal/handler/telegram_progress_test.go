package handler

import (
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/integrations/telegram"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestProgressHeadlineIsNotLineAnchored(t *testing.T) {
	// Agents write the marker mid-sentence often enough that requiring it at
	// the start of a line drops exactly the updates worth relaying — the same
	// mistake already made once with the QA phase markers.
	cases := map[string]string{
		"PROGRESS: importing 412 tasks":                    "importing 412 tasks",
		"Fetched the window. PROGRESS: aggregating by tag": "aggregating by tag",
		"a\nb\nPROGRESS: still running\nmore text":         "still running",
		"  PROGRESS:   padded  ":                           "padded",
	}
	for in, want := range cases {
		if got := progressHeadline(in); got != want {
			t.Errorf("progressHeadline(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProgressHeadlineIgnoresUnmarkedText(t *testing.T) {
	for _, in := range []string{"", "just some output", "progress: lowercase is not the marker"} {
		if got := progressHeadline(in); got != "" {
			t.Errorf("progressHeadline(%q) = %q, want empty", in, got)
		}
	}
}

func TestProgressStaysSilentOnAShortRun(t *testing.T) {
	// A run that finishes quickly should produce exactly one message: its
	// report. Narrating a four-minute run is how a group learns to ignore the
	// channel.
	s := &progressRelayState{last: map[string]progressRelayEntry{}}
	started := time.Now()
	if _, ok := s.reserve("run-1", "working", started, started.Add(30*time.Second)); ok {
		t.Fatal("reserved before the start delay")
	}
	if _, ok := s.reserve("run-1", "working", started, started.Add(telegramProgressStartDelay+time.Second)); !ok {
		t.Fatal("did not reserve once the run was long enough")
	}
}

func TestProgressSuppressesRepeatsAndFloods(t *testing.T) {
	s := &progressRelayState{last: map[string]progressRelayEntry{}}
	started := time.Now()
	base := started.Add(telegramProgressStartDelay + time.Second)

	res, ok := s.reserve("run-1", "step one", started, base)
	if !ok {
		t.Fatal("first reservation was refused")
	}
	s.finish("run-1", res, true, base)

	// Same headline again is not news.
	if _, ok := s.reserve("run-1", "step one", started, base.Add(telegramProgressInterval+time.Minute)); ok {
		t.Error("an unchanged headline was reserved again")
	}
	// A new headline still waits for the interval.
	if _, ok := s.reserve("run-1", "step two", started, base.Add(time.Minute)); ok {
		t.Error("reserved inside the throttle interval")
	}
	if _, ok := s.reserve("run-1", "step two", started, base.Add(telegramProgressInterval+time.Second)); !ok {
		t.Error("a changed headline was refused past the interval")
	}
}

func TestProgressStateIsPerRun(t *testing.T) {
	// Two autopilots running at once must not throttle each other.
	s := &progressRelayState{last: map[string]progressRelayEntry{}}
	started := time.Now()
	at := started.Add(telegramProgressStartDelay + time.Second)
	_, okA := s.reserve("run-1", "x", started, at)
	_, okB := s.reserve("run-2", "x", started, at)
	if !okA || !okB {
		t.Fatal("one run's reservation suppressed another's")
	}
}

func TestForgetDropsRunState(t *testing.T) {
	// Without this the map grows for the life of the process.
	s := &progressRelayState{last: map[string]progressRelayEntry{}}
	started := time.Now()
	at := started.Add(telegramProgressStartDelay + time.Second)
	s.reserve("run-1", "x", started, at)
	s.forget("run-1")
	if _, ok := s.last["run-1"]; ok {
		t.Fatal("run state survived forget")
	}
}

func TestProgressFailedDeliveryRollsBackReservation(t *testing.T) {
	s := &progressRelayState{last: map[string]progressRelayEntry{}}
	started := time.Now()
	at := started.Add(telegramProgressStartDelay + time.Second)
	reservation, ok := s.reserve("run-1", "still working", started, at)
	if !ok {
		t.Fatal("first delivery was not reserved")
	}
	s.finish("run-1", reservation, false, at)
	if _, ok := s.reserve("run-1", "still working", started, at.Add(time.Second)); !ok {
		t.Fatal("failed delivery permanently suppressed the same headline")
	}
}

func TestProgressPendingReservationSuppressesConcurrentDuplicate(t *testing.T) {
	s := &progressRelayState{last: map[string]progressRelayEntry{}}
	started := time.Now()
	at := started.Add(telegramProgressStartDelay + time.Second)
	if _, ok := s.reserve("run-1", "still working", started, at); !ok {
		t.Fatal("first delivery was not reserved")
	}
	if _, ok := s.reserve("run-1", "still working", started, at); ok {
		t.Fatal("concurrent duplicate received a second reservation")
	}
}

func TestRelayResolvesACreateIssueRun(t *testing.T) {
	// The gap this closes: a create_issue autopilot NEVER sets the run's
	// task_id and never reaches status 'running' — it opens an issue and stays
	// at 'issue_created' until it finishes. Keying the lookup on task_id alone
	// made the relay dead for the common mode, which only a live run revealed.
	ctx := t.Context()

	var agentID, issueID, autopilotID, runID string
	if err := testPool.QueryRow(ctx,
		`SELECT id::text FROM agent WHERE workspace_id = $1::uuid LIMIT 1`, testWorkspaceID).
		Scan(&agentID); err != nil {
		t.Fatalf("no fixture agent: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, creator_type, creator_id)
		VALUES ($1::uuid, 'autopilot progress fixture', 'todo', 'member', $2::uuid) RETURNING id::text`,
		testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1::uuid`, issueID) })

	if err := testPool.QueryRow(ctx, `
		INSERT INTO autopilot (workspace_id, title, assignee_id, assignee_type, created_by_type, created_by_id)
		VALUES ($1::uuid, 'progress fixture', $2::uuid, 'agent', 'member', $3::uuid) RETURNING id::text`,
		testWorkspaceID, agentID, testUserID).Scan(&autopilotID); err != nil {
		t.Fatalf("create autopilot: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM autopilot WHERE id = $1::uuid`, autopilotID) })

	// status 'issue_created' with a NULL task_id is exactly the shape a live
	// create_issue run has while it is working.
	if err := testPool.QueryRow(ctx, `
		INSERT INTO autopilot_run (autopilot_id, source, status, issue_id)
		VALUES ($1::uuid, 'manual', 'issue_created', $2::uuid) RETURNING id::text`,
		autopilotID, issueID).Scan(&runID); err != nil {
		t.Fatalf("create run: %v", err)
	}

	got, err := testHandler.Queries.GetActiveAutopilotRunForTaskOrIssue(ctx,
		db.GetActiveAutopilotRunForTaskOrIssueParams{IssueID: parseUUID(issueID)})
	if err != nil {
		t.Fatalf("a live create_issue run did not resolve: %v", err)
	}
	if uuidToString(got.ID) != runID {
		t.Fatalf("resolved the wrong run: %s", uuidToString(got.ID))
	}

	// Once finished it must stop matching — a completed run's report has
	// already been posted, and further chatter would follow it.
	if _, err := testPool.Exec(ctx,
		`UPDATE autopilot_run SET completed_at = now(), status = 'completed' WHERE id = $1::uuid`, runID); err != nil {
		t.Fatalf("complete run: %v", err)
	}
	if _, err := testHandler.Queries.GetActiveAutopilotRunForTaskOrIssue(ctx,
		db.GetActiveAutopilotRunForTaskOrIssueParams{IssueID: parseUUID(issueID)}); err == nil {
		t.Fatal("a completed run still resolved")
	}
}

func TestRelayNeedsAtLeastOneKey(t *testing.T) {
	// With neither key set the query would match the newest unfinished run in
	// the whole table — every autopilot in the workspace narrating someone
	// else's task.
	ctx := t.Context()
	if _, err := testHandler.Queries.GetActiveAutopilotRunForTaskOrIssue(ctx,
		db.GetActiveAutopilotRunForTaskOrIssueParams{}); err == nil {
		t.Fatal("a query with no keys matched a run")
	}
}

func TestDestinationPrefersTheProjectChat(t *testing.T) {
	// The project override is a specific instruction ("this project reports
	// HERE"); the agent's default chat is a general one. Letting the general
	// win sent two projects' reports into whichever group their shared agent
	// happened to be bound to.
	agentBot := &telegram.BotClient{}
	platformBot := &telegram.BotClient{}

	bot, chat := chooseAutopilotDestination(agentBot, "-100agent", true, platformBot, "-100project")
	if bot != agentBot || chat != "-100project" {
		t.Fatalf("got chat %q, want the project chat spoken by the agent bot", chat)
	}

	// The agent's bot is only used where it can actually reach — otherwise the
	// send fails silently at the Bot API.
	bot, chat = chooseAutopilotDestination(agentBot, "-100agent", false, platformBot, "-100project")
	if bot != platformBot || chat != "-100project" {
		t.Fatalf("got %v/%q, want the platform bot in the project chat", bot == agentBot, chat)
	}
}

func TestDestinationFallsBackToTheAgentDefault(t *testing.T) {
	agentBot := &telegram.BotClient{}
	bot, chat := chooseAutopilotDestination(agentBot, "-100agent", false, nil, "")
	if bot != agentBot || chat != "-100agent" {
		t.Fatalf("got %q, want the agent's own chat when no project chat is set", chat)
	}
	// Nothing anywhere is not a delivery.
	if bot, chat := chooseAutopilotDestination(nil, "", false, nil, ""); bot != nil || chat != "" {
		t.Fatal("a destination appeared from nowhere")
	}
	// A project chat with no bot able to reach it is also not a delivery.
	if bot, _ := chooseAutopilotDestination(nil, "", false, nil, "-100project"); bot != nil {
		t.Fatal("a project chat resolved without any bot")
	}
}

func TestThrottleOnlyAdvancesOnDelivery(t *testing.T) {
	// A headline marked sent and then dropped by a Bot API error would never be
	// retried, because the next identical headline reads as a repeat. A failed
	// delivery must roll the reservation back.
	s := &progressRelayState{last: map[string]progressRelayEntry{}}
	started := time.Now()
	at := started.Add(telegramProgressStartDelay + time.Second)

	res, ok := s.reserve("run-1", "step one", started, at)
	if !ok {
		t.Fatal("first headline was refused")
	}
	// While in flight, a second event must not send the same update again.
	if _, ok := s.reserve("run-1", "step one", started, at.Add(time.Second)); ok {
		t.Error("a reservation in flight did not block a concurrent send")
	}
	// Delivery failed: the headline becomes eligible again.
	s.finish("run-1", res, false, at)
	res2, ok := s.reserve("run-1", "step one", started, at.Add(2*time.Second))
	if !ok {
		t.Fatal("an undelivered headline was treated as sent")
	}
	s.finish("run-1", res2, true, at.Add(2*time.Second))
	if _, ok := s.reserve("run-1", "step one", started, at.Add(3*time.Second)); ok {
		t.Fatal("a delivered headline was offered again")
	}
}
