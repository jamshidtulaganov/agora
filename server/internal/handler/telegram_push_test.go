package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/integrations/telegram"
)

type capturedDM struct {
	chatID string
	text   string
}

// setupPushMockBot wires testHandler.telegramBot to a mock Bot API that signals
// every sendMessage on a channel (so the async SendIssueInboxDM goroutine can be
// awaited deterministically). Sets the username + Mini App short name so the
// deep-link button path is exercised.
func setupPushMockBot(t *testing.T) <-chan capturedDM {
	t.Helper()
	if testHandler == nil {
		t.Skip("no DB handler; skipping (DATABASE_URL not set)")
	}
	ch := make(chan capturedDM, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ChatID string `json:"chat_id"`
			Text   string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		ch <- capturedDM{chatID: body.ChatID, text: body.Text}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	}))
	t.Cleanup(srv.Close)

	bot := telegram.NewBotClient("PUSHTOKEN")
	bot.BaseURL = srv.URL
	bot.HTTPClient = srv.Client()
	prev := testHandler.telegramBot
	testHandler.telegramBot = bot
	t.Cleanup(func() { testHandler.telegramBot = prev })

	t.Setenv("TELEGRAM_BOT_USERNAME", "agora_test_bot")
	t.Setenv("TELEGRAM_MINIAPP_SHORTNAME", "pm")
	return ch
}

func TestComposeIssueDM(t *testing.T) {
	if got := composeIssueDM("issue_assigned", "MUL-12", "Fix login"); got != "🔔 You were assigned MUL-12: Fix login" {
		t.Fatalf("assigned: got %q", got)
	}
	// Unknown type falls back to a plain bell.
	if got := composeIssueDM("totally_new_type", "MUL-7", "Thing"); got != "🔔 MUL-7: Thing" {
		t.Fatalf("unknown type: got %q", got)
	}
	// Missing identifier falls back to title only.
	if got := composeIssueDM("mentioned", "", "Hi there"); got != "💬 You were mentioned in Hi there" {
		t.Fatalf("no identifier: got %q", got)
	}
}

func TestTelegramPushEnabled(t *testing.T) {
	if testHandler == nil {
		t.Skip("no DB handler; skipping (DATABASE_URL not set)")
	}
	prev := testHandler.telegramBot
	t.Cleanup(func() { testHandler.telegramBot = prev })

	testHandler.telegramBot = nil
	if testHandler.TelegramPushEnabled() {
		t.Fatal("expected push disabled when bot is nil")
	}
	testHandler.telegramBot = telegram.NewBotClient("x")
	if !testHandler.TelegramPushEnabled() {
		t.Fatal("expected push enabled when bot is set")
	}
}

// nonExistentIssueID is a well-formed UUID with no matching row, so GetIssue
// fails and SendIssueInboxDM takes its title-only fallback — letting these tests
// assert delivery without seeding a full issue/workspace fixture.
const nonExistentIssueID = "123e4567-e89b-12d3-a456-426614174000"

func TestSendIssueInboxDM_MemberWithLinkGetsDM(t *testing.T) {
	ch := setupPushMockBot(t)
	ctx := context.Background()

	const telegramID = "55501999"
	email := "push-link@telegram.local"
	user, _, err := testHandler.findOrCreateUser(ctx, email)
	if err != nil {
		t.Fatalf("findOrCreateUser: %v", err)
	}
	userID := uuidToString(user.ID)
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM user_external_identity WHERE provider = $1 AND external_id = $2`, providerTelegram, telegramID)
		testPool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, email)
	})
	if err := testHandler.linkExternalIdentity(ctx, providerTelegram, telegramID, userID); err != nil {
		t.Fatalf("linkExternalIdentity: %v", err)
	}

	testHandler.SendIssueInboxDM(ctx, "member", userID, nonExistentIssueID, "issue_assigned", "Fix login bug")

	select {
	case dm := <-ch:
		if dm.chatID != telegramID {
			t.Fatalf("DM chat_id = %q, want %q", dm.chatID, telegramID)
		}
		if !strings.Contains(dm.text, "Fix login bug") {
			t.Fatalf("DM text = %q, want it to contain the title", dm.text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expected a DM, got none")
	}
}

func TestSendIssueInboxDM_AgentRecipientNoDM(t *testing.T) {
	ch := setupPushMockBot(t)
	testHandler.SendIssueInboxDM(context.Background(), "agent", "00000000-0000-0000-0000-000000000001", nonExistentIssueID, "issue_assigned", "x")
	assertNoDM(t, ch)
}

func TestSendIssueInboxDM_MemberWithoutLinkNoDM(t *testing.T) {
	ch := setupPushMockBot(t)
	ctx := context.Background()
	email := "push-nolink@telegram.local"
	user, _, err := testHandler.findOrCreateUser(ctx, email)
	if err != nil {
		t.Fatalf("findOrCreateUser: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, email)
	})
	// No external identity linked → no telegram chat → no DM.
	testHandler.SendIssueInboxDM(ctx, "member", uuidToString(user.ID), nonExistentIssueID, "issue_assigned", "x")
	assertNoDM(t, ch)
}

func assertNoDM(t *testing.T, ch <-chan capturedDM) {
	t.Helper()
	select {
	case dm := <-ch:
		t.Fatalf("expected no DM, got one to %q", dm.chatID)
	case <-time.After(400 * time.Millisecond):
		// good — nothing sent
	}
}
