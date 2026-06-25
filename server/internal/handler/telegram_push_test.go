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

func strptr(s string) *string { return &s }

func TestComposeIssueDM(t *testing.T) {
	cases := []struct {
		name      string
		lang      string // empty → "en"
		notifType string
		id        string
		title     string
		body      *string
		actor     string
		details   []byte
		want      string
	}{
		{
			name: "assigned", notifType: "issue_assigned", id: "MUL-12", title: "Fix login",
			want: "🔔 <b>Assigned to you</b>\n<b>MUL-12</b> Fix login",
		},
		{
			name: "unknown type falls back to bell", notifType: "totally_new_type", id: "MUL-7", title: "Thing",
			want: "🔔 <b>Update</b>\n<b>MUL-7</b> Thing",
		},
		{
			name: "missing identifier → title only", notifType: "mentioned", id: "", title: "Hi there",
			want: "💬 <b>You were mentioned</b>\nHi there",
		},
		{
			name: "actor suffix", notifType: "issue_assigned", id: "MUL-1", title: "T", actor: "Alice",
			want: "🔔 <b>Assigned to you</b> · <i>Alice</i>\n<b>MUL-1</b> T",
		},
		{
			name: "status transition", notifType: "status_changed", id: "MUL-3", title: "T",
			details: []byte(`{"from":"todo","to":"in_progress"}`),
			want:    "🔄 <b>Status changed</b>\n<b>MUL-3</b> T\nTodo → In Progress",
		},
		{
			name: "comment snippet in blockquote", notifType: "new_comment", id: "MUL-4", title: "T",
			body: strptr("Looks good to me"),
			want: "💬 <b>New comment</b>\n<b>MUL-4</b> T\n<blockquote>Looks good to me</blockquote>",
		},
		{
			name: "html-escapes dynamic content", notifType: "issue_assigned", id: "MUL-5", title: "a < b & c",
			want: "🔔 <b>Assigned to you</b>\n<b>MUL-5</b> a &lt; b &amp; c",
		},
		// Localized (ru/uz) + fallback.
		{
			name: "ru assigned", lang: "ru", notifType: "issue_assigned", id: "MUL-9", title: "Починить вход",
			want: "🔔 <b>Назначено вам</b>\n<b>MUL-9</b> Починить вход",
		},
		{
			name: "ru status transition localizes tokens", lang: "ru", notifType: "status_changed", id: "MUL-3", title: "T",
			details: []byte(`{"from":"todo","to":"in_progress"}`),
			want:    "🔄 <b>Статус изменён</b>\n<b>MUL-3</b> T\nК выполнению → В работе",
		},
		{
			name: "uz new comment", lang: "uz", notifType: "new_comment", id: "MUL-4", title: "T",
			body: strptr("Yaxshi"),
			want: "💬 <b>Yangi izoh</b>\n<b>MUL-4</b> T\n<blockquote>Yaxshi</blockquote>",
		},
		{
			name: "unknown lang falls back to ru", lang: "fr", notifType: "issue_assigned", id: "MUL-1", title: "T",
			want: "🔔 <b>Назначено вам</b>\n<b>MUL-1</b> T",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lang := c.lang
			if lang == "" {
				lang = "en"
			}
			if got := composeIssueDM(lang, c.notifType, c.id, c.title, c.body, c.actor, c.details); got != c.want {
				t.Fatalf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestCommentSnippet_Truncates(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := commentSnippet(&long)
	if r := []rune(got); len(r) != dmSnippetMaxLen+1 || string(r[len(r)-1]) != "…" {
		t.Fatalf("expected truncation to %d runes + ellipsis, got %d runes", dmSnippetMaxLen, len([]rune(got)))
	}
	if commentSnippet(nil) != "" {
		t.Fatal("nil body should yield empty snippet")
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

	testHandler.SendIssueInboxDM(ctx, "member", userID, nonExistentIssueID, "issue_assigned", "Fix login bug", nil, "", "", nil)

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
	testHandler.SendIssueInboxDM(context.Background(), "agent", "00000000-0000-0000-0000-000000000001", nonExistentIssueID, "issue_assigned", "x", nil, "", "", nil)
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
	testHandler.SendIssueInboxDM(ctx, "member", uuidToString(user.ID), nonExistentIssueID, "issue_assigned", "x", nil, "", "", nil)
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
