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

	"github.com/jamshidtulaganov/agora/server/internal/integrations/telegram"
)

func TestComposeIssueCreatedGroupNotifyText_ShowsAssigneeName(t *testing.T) {
	got := composeIssueCreatedGroupNotifyText(
		"TGN-1",
		"Telegram group create notify",
		"Alice Chen",
	)

	wantParts := []string{
		"🆕 <b>New issue created</b>",
		"📌 <b>TGN-1</b> — Telegram group create notify",
		"👤 <b>Assigned to:</b> Alice Chen",
	}
	for _, part := range wantParts {
		if !strings.Contains(got, part) {
			t.Fatalf("text missing %q\nfull: %q", part, got)
		}
	}
	if strings.Contains(got, "issue-tg-notify-") {
		t.Fatalf("text leaked synthetic creator id: %q", got)
	}
	if strings.Contains(got, issueCreatedUnassignedLabel) {
		t.Fatalf("text should not say Unassigned when assignee name is set: %q", got)
	}
}

func TestComposeIssueCreatedGroupNotifyText_Unassigned(t *testing.T) {
	got := composeIssueCreatedGroupNotifyText("TGN-2", "No owner yet", "")
	if !strings.Contains(got, "👤 <b>Assigned to:</b> Unassigned") {
		t.Fatalf("expected Unassigned line, got %q", got)
	}
	if !strings.Contains(got, "<b>TGN-2</b> — No owner yet") {
		t.Fatalf("expected identifier+title, got %q", got)
	}
}

func TestComposeIssueCreatedGroupNotifyText_EscapesHTML(t *testing.T) {
	got := composeIssueCreatedGroupNotifyText(
		"<b>ID</b>",
		`Title & "quote"`,
		`Bob <script>`,
	)
	if strings.Contains(got, "<script>") || strings.Contains(got, "<b>ID</b>") {
		t.Fatalf("raw HTML leaked into Telegram body: %q", got)
	}
	if !strings.Contains(got, "&lt;b&gt;ID&lt;/b&gt;") {
		t.Fatalf("identifier not escaped: %q", got)
	}
	if !strings.Contains(got, "Title &amp; &#34;quote&#34;") && !strings.Contains(got, "Title &amp; \"quote\"") {
		// html.EscapeString turns " into &#34;
		if !strings.Contains(got, "Title &amp;") {
			t.Fatalf("title not escaped: %q", got)
		}
	}
	if !strings.Contains(got, "Bob &lt;script&gt;") {
		t.Fatalf("assignee not escaped: %q", got)
	}
}

func TestSuppressExternalNotificationsForClientIsDevOnly(t *testing.T) {
	t.Setenv("APP_ENV", "")
	t.Setenv("AGORA_DEV_VERIFICATION_CODE", "888888")
	if !suppressExternalNotificationsForClient("e2e") {
		t.Fatal("expected the explicit local E2E client to suppress external fanout")
	}
	if suppressExternalNotificationsForClient("web") {
		t.Fatal("ordinary clients must not suppress external fanout")
	}

	t.Setenv("APP_ENV", "production")
	if suppressExternalNotificationsForClient("e2e") {
		t.Fatal("production must ignore a spoofed E2E client header")
	}
}

func TestSendIssueCreatedGroupNotify_PostsToReportChat(t *testing.T) {
	if testHandler == nil {
		t.Skip("no DB handler; skipping (DATABASE_URL not set)")
	}

	type capturedIssueNotify struct {
		chatID    string
		text      string
		parseMode string
	}
	ch := make(chan capturedIssueNotify, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ChatID    string `json:"chat_id"`
			Text      string `json:"text"`
			ParseMode string `json:"parse_mode"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		ch <- capturedIssueNotify{chatID: body.ChatID, text: body.Text, parseMode: body.ParseMode}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	}))
	t.Cleanup(srv.Close)

	bot := telegram.NewBotClient("ISSUENOTIFYTOKEN")
	bot.BaseURL = srv.URL
	bot.HTTPClient = srv.Client()
	prev := testHandler.telegramBot
	testHandler.telegramBot = bot
	t.Cleanup(func() { testHandler.telegramBot = prev })

	t.Setenv("AGORA_TELEGRAM_REPORT_CHAT_ID", "-1003501835836")
	t.Setenv("TELEGRAM_DM_LINK_MODE", "")

	issue := IssueResponse{
		ID:          "00000000-0000-4000-8000-000000000099",
		WorkspaceID: "00000000-0000-4000-8000-000000000001",
		Identifier:  "TST-99",
		Title:       "Notify the group",
		CreatorType: "member",
		CreatorID:   "00000000-0000-4000-8000-000000000002",
	}
	testHandler.SendIssueCreatedGroupNotify(context.Background(), issue)

	select {
	case got := <-ch:
		if got.chatID != "-1003501835836" {
			t.Fatalf("chat_id = %q, want -1003501835836", got.chatID)
		}
		if got.parseMode != "HTML" {
			t.Fatalf("parse_mode = %q, want HTML", got.parseMode)
		}
		if !containsAll(got.text, "New issue created", "TST-99", "Notify the group", "Assigned to:</b> Unassigned") {
			t.Fatalf("text missing identifier/title/unassigned: %q", got.text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for telegram send")
	}
}

func TestSendIssueCreatedGroupNotify_NoChatNoop(t *testing.T) {
	if testHandler == nil {
		t.Skip("no DB handler; skipping (DATABASE_URL not set)")
	}
	ch := setupPushMockBot(t)
	t.Setenv("AGORA_TELEGRAM_REPORT_CHAT_ID", "")

	testHandler.SendIssueCreatedGroupNotify(context.Background(), IssueResponse{
		ID: "00000000-0000-4000-8000-000000000098", Title: "Silent",
	})

	select {
	case got := <-ch:
		t.Fatalf("expected no send when chat unset, got %+v", got)
	case <-time.After(200 * time.Millisecond):
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
