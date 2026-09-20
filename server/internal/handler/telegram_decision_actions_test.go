package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// DECIDE FROM THE PHONE (docs/orchestration-upgrade-plan.md §A3).
//
// The properties that make a chat button safe to act on:
//
//   - the acting human is resolved from the Telegram identity BINDING, never
//     from the callback payload — an unbound chat gets a link to the app and
//     nothing happens;
//   - the answer text comes from the STORED options, so a forged payload cannot
//     answer with words that were never offered;
//   - a tap runs the SAME handler the web button posts to, so the two can never
//     drift;
//   - a decision is settled once — the second tap loses and says so;
//   - a forwarded message cannot decide anything.

func decodeDecisionUpdate(t *testing.T, raw string) telegramUpdate {
	t.Helper()
	var update telegramUpdate
	if err := json.Unmarshal([]byte(raw), &update); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	return update
}

// privateTap builds a callback_query as it arrives from a person's own DM.
func privateTap(t *testing.T, tgID int64, data string) telegramUpdate {
	t.Helper()
	return decodeDecisionUpdate(t, fmt.Sprintf(
		`{"callback_query":{"id":"cb1","data":%q,"from":{"id":%d,"language_code":"en"},
		  "message":{"message_id":5,"chat":{"id":%d,"type":"private"}}}}`, data, tgID, tgID))
}

func TestParseTelegramDecisionCallback(t *testing.T) {
	id := "6f1b0a4e-1111-4222-8333-444444444444"
	ok := []struct {
		data   string
		action string
		arg    string
	}{
		{"d:e:" + id + ":0", telegramDecisionEscalation, "0"},
		{"d:e:" + id + ":3", telegramDecisionEscalation, "3"},
		{"d:r:" + id + ":a", telegramDecisionReview, "a"},
		{"d:r:" + id + ":c", telegramDecisionReview, "c"},
	}
	for _, c := range ok {
		got, parsed := parseTelegramDecisionCallback(c.data)
		if !parsed || got.Action != c.action || got.ID != id || got.Arg != c.arg {
			t.Errorf("parse(%q) = %+v, %v", c.data, got, parsed)
		}
		// Telegram caps callback_data at 64 bytes; a payload we cannot send is
		// a button that silently never appears.
		if len(c.data) > 64 {
			t.Errorf("callback payload %q is %d bytes — over Telegram's 64-byte cap", c.data, len(c.data))
		}
	}
	for _, bad := range []string{
		"nw:ws:" + id,            // the create wizard's namespace
		"q:" + id + ":0",         // the agent-question namespace
		"d:x:" + id + ":0",       // an action we do not own
		"d:e:not-a-uuid:0",       // a forged id
		"d:e:" + id,              // truncated
		"d:e:" + id + ":0:extra", // padded
		"",                       // empty
	} {
		if _, parsed := parseTelegramDecisionCallback(bad); parsed {
			t.Errorf("parse(%q) must not be ours", bad)
		}
	}
}

// telegramDecisionIssue seeds an issue plus an open escalation with options, and
// (optionally) binds a Telegram id to a member of the test workspace.
//
// It creates its OWN user rather than reusing the suite's: a user may hold at
// most one Telegram identity (idx_user_external_identity_telegram_user), so
// tests that share a user would fight over the binding when the package runs as
// a whole. The user is an admin so the non-owner issue-visibility gate is not
// what is under test here.
func telegramDecisionIssue(t *testing.T, bindTelegram bool) (issueID, escalationID, userID string, tgID int64) {
	t.Helper()
	ctx := t.Context()

	if err := testPool.QueryRow(ctx,
		`INSERT INTO "user" (name, email)
		 VALUES ('Telegram Decider', 'tg-decider-'||substr(gen_random_uuid()::text,1,12)||'@test.local')
		 RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, userID) })
	if _, err := testPool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'admin')`,
		testWorkspaceID, userID); err != nil {
		t.Fatalf("add member: %v", err)
	}

	if err := testPool.QueryRow(ctx,
		`INSERT INTO issue (workspace_id, title, status, creator_type, creator_id, number)
		 VALUES ($1::uuid, 'telegram decision', 'in_progress', 'member', $2::uuid,
		         (5000000 + floor(random()*900000))::int)
		 RETURNING id::text`, testWorkspaceID, userID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		testPool.Exec(c, `DELETE FROM task_escalation WHERE issue_id = $1::uuid`, issueID)
		testPool.Exec(c, `DELETE FROM issue WHERE id = $1::uuid`, issueID)
	})

	if err := testPool.QueryRow(ctx,
		`INSERT INTO task_escalation (workspace_id, issue_id, kind, prompt, detail, options, risk_tier)
		 VALUES ($1::uuid, $2::uuid, 'question', 'Which tariff applies to returns?', '',
		         ARRAY['Standard','Reduced'], 'guarded')
		 RETURNING id::text`, testWorkspaceID, issueID).Scan(&escalationID); err != nil {
		t.Fatalf("raise escalation: %v", err)
	}

	var seq int64
	testPool.QueryRow(ctx, `SELECT (900000000 + floor(random()*80000000))::bigint`).Scan(&seq)
	tgID = seq
	if bindTelegram {
		if err := testHandler.linkExternalIdentity(ctx, providerTelegram, fmt.Sprint(tgID), userID); err != nil {
			t.Fatalf("bind telegram identity: %v", err)
		}
		t.Cleanup(func() {
			testPool.Exec(context.Background(),
				`DELETE FROM user_external_identity WHERE provider = $1 AND external_id = $2`,
				providerTelegram, fmt.Sprint(tgID))
		})
	}
	return issueID, escalationID, userID, tgID
}

func escalationStatus(t *testing.T, escalationID string) (status string, answer *string, answeredBy *string) {
	t.Helper()
	if err := testPool.QueryRow(t.Context(),
		`SELECT status, answer, answered_by::text FROM task_escalation WHERE id = $1::uuid`,
		escalationID).Scan(&status, &answer, &answeredBy); err != nil {
		t.Fatalf("read escalation: %v", err)
	}
	return status, answer, answeredBy
}

// The happy path: a bound human taps an option and the escalation is answered
// through the real resolve endpoint, attributed to them.
func TestTelegramDecisionAnswersEscalationAsBoundUser(t *testing.T) {
	_, escalationID, userID, tgID := telegramDecisionIssue(t, true)

	owned := testHandler.handleTelegramDecisionCallback(t.Context(),
		privateTap(t, tgID, escalationOptionCallback(escalationID, 1)))
	if !owned {
		t.Fatal("the decision dispatcher must claim its own callback")
	}

	status, answer, answeredBy := escalationStatus(t, escalationID)
	if status != "answered" {
		t.Fatalf("expected the escalation to be answered, got %q", status)
	}
	// The label came from the STORED option list, not from the payload.
	if answer == nil || *answer != "Reduced" {
		t.Fatalf("answer = %v, want the stored option 'Reduced'", answer)
	}
	if answeredBy == nil || *answeredBy != userID {
		t.Fatalf("answered_by = %v, want the bound member %s", answeredBy, userID)
	}

	// Settled once: the second tap loses the race inside the handler.
	testHandler.handleTelegramDecisionCallback(t.Context(),
		privateTap(t, tgID, escalationOptionCallback(escalationID, 0)))
	_, answerAgain, _ := escalationStatus(t, escalationID)
	if answerAgain == nil || *answerAgain != "Reduced" {
		t.Fatalf("a second tap rewrote a settled decision: %v", answerAgain)
	}
}

// An UNBOUND chat cannot decide anything. This is the gate that makes a chat
// button as trustworthy as a session: with no binding there is no human to
// attribute the decision to.
func TestTelegramDecisionRefusesUnboundChat(t *testing.T) {
	_, escalationID, _, tgID := telegramDecisionIssue(t, false)

	owned := testHandler.handleTelegramDecisionCallback(t.Context(),
		privateTap(t, tgID, escalationOptionCallback(escalationID, 1)))
	if !owned {
		t.Fatal("the dispatcher must still claim (and refuse) its own callback")
	}
	if status, answer, _ := escalationStatus(t, escalationID); status != "open" || answer != nil {
		t.Fatalf("an unbound chat answered an escalation: status=%q answer=%v", status, answer)
	}
}

// A forged option index cannot answer with text that was never offered.
func TestTelegramDecisionRejectsOutOfRangeOption(t *testing.T) {
	_, escalationID, _, tgID := telegramDecisionIssue(t, true)
	for _, idx := range []int{-1, 2, 99} {
		testHandler.handleTelegramDecisionCallback(t.Context(),
			privateTap(t, tgID, escalationOptionCallback(escalationID, idx)))
	}
	if status, _, _ := escalationStatus(t, escalationID); status != "open" {
		t.Fatalf("an out-of-range option index settled the escalation: %q", status)
	}
}

// The keyboard is delivered to one person's private chat. A message forwarded
// into a group must not act.
func TestTelegramDecisionRefusesTapOutsideTheRecipientsDM(t *testing.T) {
	_, escalationID, _, tgID := telegramDecisionIssue(t, true)
	group := decodeDecisionUpdate(t, fmt.Sprintf(
		`{"callback_query":{"id":"cb1","data":%q,"from":{"id":%d,"language_code":"en"},
		  "message":{"message_id":5,"chat":{"id":-100999,"type":"supergroup"}}}}`,
		escalationOptionCallback(escalationID, 0), tgID))
	testHandler.handleTelegramDecisionCallback(t.Context(), group)
	if status, _, _ := escalationStatus(t, escalationID); status != "open" {
		t.Fatalf("a tap from a group settled a DM's decision: %q", status)
	}
}

// A payload that is not ours is left alone, so the create wizard keeps its own
// buttons.
func TestTelegramDecisionIgnoresForeignCallbacks(t *testing.T) {
	if testHandler.handleTelegramDecisionCallback(t.Context(), privateTap(t, 4242, "nw:ws:abc")) {
		t.Error("the decision dispatcher claimed a create-wizard callback")
	}
}

// The merge_ready pair: Approve runs CreateReviewDecision — the SAME handler
// the web button posts to — as the bound member. There is no orchestration run
// on this fixture, so the handler's own release-gate check is what refuses it
// (409), which is precisely the assertion: the tap reached the real decision
// endpoint as a human, and only the gate state stopped it. A 401/403 here would
// mean the identity never arrived.
func TestTelegramApproveInvokesTheRealReviewDecision(t *testing.T) {
	issueID, _, userID, tgID := telegramDecisionIssue(t, true)

	status, body := testHandler.invokeAsMember(t.Context(), userID, testWorkspaceID,
		testHandler.CreateReviewDecision, http.MethodPost,
		"/api/issues/"+issueID+"/review-decision",
		map[string]string{"id": issueID},
		map[string]string{"action": "approve"})

	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		t.Fatalf("the bound member's identity did not reach the handler: %d %s", status, body)
	}
	if status != http.StatusConflict {
		t.Fatalf("expected the release-gate conflict, got %d: %s", status, body)
	}
	if !strings.Contains(string(body), "release") {
		t.Errorf("the refusal must come from the real handler's gate check: %s", body)
	}

	// And the tap path reaches the same place.
	owned := testHandler.handleTelegramDecisionCallback(t.Context(),
		privateTap(t, tgID, reviewDecisionCallback(issueID, telegramReviewApprove)))
	if !owned {
		t.Fatal("the dispatcher must claim a review callback")
	}
}

// "Request changes" cannot be one tap — the endpoint requires a note, because
// the note becomes the correction worker's instruction. The tap opens a short
// window; the next plain message is the note; a command is not.
func TestTelegramRequestChangesCapturesTheNote(t *testing.T) {
	issueID, _, _, tgID := telegramDecisionIssue(t, true)
	tg := fmt.Sprint(tgID)

	testHandler.handleTelegramDecisionCallback(t.Context(),
		privateTap(t, tgID, reviewDecisionCallback(issueID, telegramReviewRequestChange)))

	if _, waiting := decisionNoteWaits.Load(tg); !waiting {
		t.Fatal("the tap must open a note window")
	}
	// A command must not be swallowed as a note.
	if testHandler.maybeCaptureDecisionNote(t.Context(), tg, "en", "/tasks") {
		t.Error("a command must not be captured as a review note")
	}
	// The next plain message is consumed as the note (the write itself is
	// refused here — there is no orchestration plan on this fixture — but it
	// must not fall through to the create wizard and become a new task).
	if !testHandler.maybeCaptureDecisionNote(t.Context(), tg, "en", "the totals are wrong on the invoice page") {
		t.Fatal("the note must be consumed by the decision flow, not by the create wizard")
	}
	// The window is single-use.
	if testHandler.maybeCaptureDecisionNote(t.Context(), tg, "en", "another sentence") {
		t.Error("the note window must be consumed exactly once")
	}
}

// Keyboards: an escalation with options gets option buttons; merge_ready gets
// the approve/request pair; anything else gets none (and the caller falls back
// to the plain app link).
func TestDecisionDMKeyboard(t *testing.T) {
	issueID, _, _, _ := telegramDecisionIssue(t, true)
	ctx := t.Context()

	rows := testHandler.decisionDMKeyboard(ctx, "en", "escalation", issueID, "https://app.test/i")
	if len(rows) != 3 {
		t.Fatalf("expected two option rows plus the open link, got %d: %+v", len(rows), rows)
	}
	if rows[0][0].Text != "Standard" || rows[0][0].CallbackData == "" {
		t.Errorf("option buttons must carry the stored label and a callback: %+v", rows[0][0])
	}
	if rows[2][0].URL == "" {
		t.Error("the app link must remain available beside the buttons")
	}

	merge := testHandler.decisionDMKeyboard(ctx, "en", "merge_ready", issueID, "")
	if len(merge) != 1 || len(merge[0]) != 2 {
		t.Fatalf("merge_ready must offer the approve/request pair, got %+v", merge)
	}

	if got := testHandler.decisionDMKeyboard(ctx, "en", "new_comment", issueID, "https://app.test/i"); got != nil {
		t.Errorf("an ordinary notification carries no decision buttons, got %+v", got)
	}
}
