package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/llm"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/telegram"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// The platform bot's private chat as an Assistant conversation.
//
// What has to hold:
//
//   - the person is resolved from the Telegram identity BINDING; an unbound
//     chat never reaches the Assistant;
//   - the answer goes to the chat that asked, once, even if the completion
//     event is replayed;
//   - a parked action is confirmed through the SAME handler the web card uses,
//     as the bound member, so a tap cannot do more than a click.

type telegramAssistantCall struct {
	method string
	chatID string
	text   string
	markup string
}

// telegramAssistantBot wires the platform bot to a mock Bot API and records
// every call. The buffer is deep enough that a test never blocks the handler.
func telegramAssistantBot(t *testing.T) <-chan telegramAssistantCall {
	t.Helper()
	calls := make(chan telegramAssistantCall, 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ChatID      string          `json:"chat_id"`
			Text        string          `json:"text"`
			ReplyMarkup json.RawMessage `json:"reply_markup"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls <- telegramAssistantCall{
			method: r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:],
			chatID: body.ChatID, text: body.Text, markup: string(body.ReplyMarkup),
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	}))
	t.Cleanup(srv.Close)

	bot := telegram.NewBotClient("ASSISTANTTOKEN")
	bot.BaseURL = srv.URL
	bot.HTTPClient = srv.Client()
	previous := testHandler.telegramBot
	testHandler.telegramBot = bot
	t.Cleanup(func() { testHandler.telegramBot = previous })
	return calls
}

// telegramAssistantService installs an Assistant that replays script, with the
// run_finished subscriber production registers in cmd/server.
func telegramAssistantService(t *testing.T, script *scriptedToolChat) {
	t.Helper()
	t.Setenv("ZHIPU_API_KEY", "telegram-assistant-test-key")
	t.Setenv("AGORA_ASSISTANT_PROVIDER", "zhipu")
	t.Setenv("AGORA_ASSISTANT_ENABLED", "true")

	bus := events.New()
	svc := assistant.NewService(testHandler.Queries, bus, func() (llm.ToolChat, string, error) {
		return script, "test-model", nil
	})
	svc.Store = testHandler.DB
	svc.TxStarter = testHandler.TxStarter
	svc.Exec = testHandler
	bus.Subscribe(protocol.EventAssistantRunFinished, func(e events.Event) {
		if payload, ok := e.Payload.(protocol.AssistantRunFinishedPayload); ok {
			testHandler.DeliverTelegramAssistantRun(context.Background(), payload.RunID, payload.Error)
		}
	})

	previous := testHandler.Assistant
	testHandler.Assistant = svc
	t.Cleanup(func() { testHandler.Assistant = previous })
}

// telegramAssistantUser creates a member of one workspace and returns a fresh
// Telegram id, bound to them when bind is true.
func telegramAssistantUser(t *testing.T, name, prefix string, bind bool) (userID, workspaceID string, tgID int64) {
	t.Helper()
	userID = newAssistantTestUser(t, name+"@agora.dev")
	workspaceID = newAssistantTestWorkspace(t, name+"-ws", prefix)
	addAssistantTestMember(t, workspaceID, userID, "owner")
	if err := testPool.QueryRow(t.Context(), `SELECT (700000000 + floor(random()*80000000))::bigint`).Scan(&tgID); err != nil {
		t.Fatalf("pick telegram id: %v", err)
	}
	if bind {
		if err := testHandler.linkExternalIdentity(t.Context(), providerTelegram, fmt.Sprint(tgID), userID); err != nil {
			t.Fatalf("bind telegram identity: %v", err)
		}
		t.Cleanup(func() {
			testPool.Exec(context.Background(),
				`DELETE FROM user_external_identity WHERE provider = $1 AND external_id = $2`,
				providerTelegram, fmt.Sprint(tgID))
		})
	}
	return userID, workspaceID, tgID
}

func privateText(t *testing.T, tgID int64, text string) telegramUpdate {
	t.Helper()
	return decodeDecisionUpdate(t, fmt.Sprintf(
		`{"message":{"message_id":9,"text":%q,"from":{"id":%d,"language_code":"en"},"chat":{"id":%d,"type":"private"}}}`,
		text, tgID, tgID))
}

// awaitTelegramMessage returns the next sendMessage, skipping chat actions.
func awaitTelegramMessage(t *testing.T, calls <-chan telegramAssistantCall) telegramAssistantCall {
	t.Helper()
	deadline := time.After(20 * time.Second)
	for {
		select {
		case call := <-calls:
			if call.method == "sendMessage" {
				return call
			}
		case <-deadline:
			t.Fatal("the bot sent no message")
		}
	}
}

func expectNoTelegramMessage(t *testing.T, calls <-chan telegramAssistantCall) {
	t.Helper()
	for {
		select {
		case call := <-calls:
			if call.method == "sendMessage" {
				t.Fatalf("unexpected message: %q", call.text)
			}
		case <-time.After(300 * time.Millisecond):
			return
		}
	}
}

func TestTelegramAssistantAnswersInTheChatThatAsked(t *testing.T) {
	calls := telegramAssistantBot(t)
	telegramAssistantService(t, &scriptedToolChat{replies: []llm.Message{
		{Role: "assistant", Content: "You have **2** open tasks."},
	}})
	userID, workspaceID, tgID := telegramAssistantUser(t, "tg-assistant-answer", "TGA", true)

	testHandler.processBotMessage(t.Context(), privateText(t, tgID, "what is assigned to me?"))

	got := awaitTelegramMessage(t, calls)
	if got.chatID != fmt.Sprint(tgID) || got.text != "You have <b>2</b> open tasks." {
		t.Fatalf("reply = %+v", got)
	}

	// The turn is an ordinary assistant session of the bound member, focused on
	// their only workspace, and its run is marked delivered.
	var sessionUser, focus, runID string
	if err := testPool.QueryRow(t.Context(), `
		SELECT s.user_id::text, COALESCE(s.focus_workspace_id::text, ''), r.id::text
		FROM telegram_assistant_session t
		JOIN assistant_session s ON s.id = t.session_id
		JOIN assistant_run r ON r.session_id = s.id
		WHERE t.user_id = $1`, userID).Scan(&sessionUser, &focus, &runID); err != nil {
		t.Fatalf("read telegram session: %v", err)
	}
	if sessionUser != userID || focus != workspaceID {
		t.Fatalf("session user %s focus %s, want %s %s", sessionUser, focus, userID, workspaceID)
	}

	// A replayed completion event must not post the answer again.
	testHandler.DeliverTelegramAssistantRun(t.Context(), runID, "")
	expectNoTelegramMessage(t, calls)
}

func TestTelegramAssistantReportsAFailedRun(t *testing.T) {
	calls := telegramAssistantBot(t)
	telegramAssistantService(t, &scriptedToolChat{}) // an exhausted script fails the run
	_, _, tgID := telegramAssistantUser(t, "tg-assistant-failed", "TGF", true)

	testHandler.processBotMessage(t.Context(), privateText(t, tgID, "hello"))

	if got := awaitTelegramMessage(t, calls); !strings.HasPrefix(got.text, botT("en", "ai.failed")) {
		t.Fatalf("a failed run must say so, got %q", got.text)
	}
}

// An unbound chat keeps the older behavior: no Assistant, no session, and the
// wizard's own "sign in first" answer.
func TestTelegramAssistantIgnoresUnboundChat(t *testing.T) {
	calls := telegramAssistantBot(t)
	script := &scriptedToolChat{replies: []llm.Message{{Role: "assistant", Content: "should never be sent"}}}
	telegramAssistantService(t, script)
	userID, _, tgID := telegramAssistantUser(t, "tg-assistant-unbound", "TGU", false)

	testHandler.processBotMessage(t.Context(), privateText(t, tgID, "what is assigned to me?"))

	if got := awaitTelegramMessage(t, calls); got.text != botT("en", "notlinked") {
		t.Fatalf("unbound chat got %q", got.text)
	}
	var sessions int
	testPool.QueryRow(t.Context(), `SELECT count(*) FROM assistant_session WHERE user_id = $1`, userID).Scan(&sessions)
	if sessions != 0 || len(script.seen) != 0 {
		t.Fatalf("an unbound chat reached the assistant (%d sessions, %d model calls)", sessions, len(script.seen))
	}
}

func TestTelegramAssistantResetOpensANewConversation(t *testing.T) {
	calls := telegramAssistantBot(t)
	telegramAssistantService(t, &scriptedToolChat{replies: []llm.Message{
		{Role: "assistant", Content: "first"}, {Role: "assistant", Content: "second"},
	}})
	userID, _, tgID := telegramAssistantUser(t, "tg-assistant-reset", "TGR", true)
	current := func() (id string) {
		testPool.QueryRow(t.Context(), `SELECT session_id::text FROM telegram_assistant_session WHERE user_id = $1`, userID).Scan(&id)
		return id
	}

	testHandler.processBotMessage(t.Context(), privateText(t, tgID, "one"))
	awaitTelegramMessage(t, calls)
	first := current()

	testHandler.processBotMessage(t.Context(), privateText(t, tgID, "/reset"))
	if got := awaitTelegramMessage(t, calls); got.text != botT("en", "ai.reset") {
		t.Fatalf("reset answered %q", got.text)
	}
	testHandler.processBotMessage(t.Context(), privateText(t, tgID, "two"))
	awaitTelegramMessage(t, calls)

	if second := current(); first == "" || second == "" || first == second {
		t.Fatalf("reset kept the conversation (%q → %q)", first, second)
	}
}

// A parked delete gets a card with buttons, and a tap runs the real confirm
// handler as the bound member.
func TestTelegramAssistantConfirmsAParkedOperation(t *testing.T) {
	calls := telegramAssistantBot(t)
	userID, workspaceID, tgID := telegramAssistantUser(t, "tg-assistant-confirm", "TGC", true)
	issueID := newAssistantTestIssue(t, workspaceID, "doomed", userID, userID)
	telegramAssistantService(t, &scriptedToolChat{replies: []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{{
			ID: "call-1", Name: assistant.ToolDeleteIssue,
			Arguments: `{"workspace_id":"` + workspaceID + `","ref":"TGC-1"}`,
		}}},
		{Role: "assistant", Content: "I can delete TGC-1 once you confirm."},
	}})

	testHandler.processBotMessage(t.Context(), privateText(t, tgID, "delete TGC-1"))
	awaitTelegramMessage(t, calls) // the assistant's text
	card := awaitTelegramMessage(t, calls)

	var operationID string
	if err := testPool.QueryRow(t.Context(),
		`SELECT id::text FROM assistant_pending_operation WHERE user_id = $1 AND status = 'pending'`, userID,
	).Scan(&operationID); err != nil {
		t.Fatalf("no parked operation: %v", err)
	}
	for _, data := range []string{telegramAssistantConfirmPrefix + operationID, telegramAssistantRejectPrefix + operationID} {
		if !strings.Contains(card.markup, data) {
			t.Fatalf("card %q is missing button %q", card.markup, data)
		}
		// Telegram caps callback_data at 64 bytes.
		if len(data) > 64 {
			t.Fatalf("callback payload %q is %d bytes", data, len(data))
		}
	}

	// Someone else's tap decides nothing.
	_, _, stranger := telegramAssistantUser(t, "tg-assistant-stranger", "TGS", true)
	testHandler.handleTelegramAssistantCallback(t.Context(), privateTap(t, stranger, telegramAssistantConfirmPrefix+operationID))
	if got := assistantPendingOperationStatus(t, operationID); got != "pending" {
		t.Fatalf("a stranger's tap moved the operation to %q", got)
	}

	if !testHandler.handleTelegramAssistantCallback(t.Context(), privateTap(t, tgID, telegramAssistantConfirmPrefix+operationID)) {
		t.Fatal("the assistant dispatcher must claim its own callback")
	}
	if got := assistantPendingOperationStatus(t, operationID); got != "confirmed" {
		t.Fatalf("operation status = %q after confirm", got)
	}
	var remaining int
	testPool.QueryRow(t.Context(), `SELECT count(*) FROM issue WHERE id = $1`, issueID).Scan(&remaining)
	if remaining != 0 {
		t.Fatal("the confirmed delete did not run")
	}
}

func TestTelegramAssistantRejectLeavesTheIssue(t *testing.T) {
	telegramAssistantBot(t)
	telegramAssistantService(t, &scriptedToolChat{})
	userID, workspaceID, tgID := telegramAssistantUser(t, "tg-assistant-reject", "TGJ", true)
	issueID := newAssistantTestIssue(t, workspaceID, "kept", userID, userID)
	session := newAssistantTestSession(t, userID)
	operationID := assistantOperationID(t, assistantAsk(t, userID, session, assistant.ToolDeleteIssue,
		`{"workspace_id":"`+workspaceID+`","ref":"TGJ-1"}`))

	testHandler.handleTelegramAssistantCallback(t.Context(), privateTap(t, tgID, telegramAssistantRejectPrefix+operationID))

	if got := assistantPendingOperationStatus(t, operationID); got != "rejected" {
		t.Fatalf("operation status = %q after reject", got)
	}
	var remaining int
	testPool.QueryRow(t.Context(), `SELECT count(*) FROM issue WHERE id = $1`, issueID).Scan(&remaining)
	if remaining != 1 {
		t.Fatal("a rejected delete removed the issue")
	}
}

func TestTelegramAssistantIgnoresForeignCallbacks(t *testing.T) {
	for _, data := range []string{"", "nw:ws:x", "d:e:6f1b0a4e-1111-4222-8333-444444444444:0", "ao:x:1"} {
		if testHandler.handleTelegramAssistantCallback(t.Context(), privateTap(t, 1, data)) {
			t.Errorf("claimed foreign callback %q", data)
		}
	}
}

func TestSplitTelegramText(t *testing.T) {
	if got := splitTelegramText("short", 10); len(got) != 1 || got[0] != "short" {
		t.Fatalf("short text = %q", got)
	}
	if got := splitTelegramText("", 10); len(got) != 0 {
		t.Fatalf("empty text = %q", got)
	}
	// Prefers the paragraph break over cutting a sentence.
	text := strings.Repeat("a", 30) + "\n\n" + strings.Repeat("b", 30)
	got := splitTelegramText(text, 40)
	if len(got) != 2 || got[0] != strings.Repeat("a", 30) || got[1] != strings.Repeat("b", 30) {
		t.Fatalf("paragraph split = %q", got)
	}
	// Counts runes, never splits one, and loses nothing when there is no break.
	cyrillic := strings.Repeat("я", 95)
	parts := splitTelegramText(cyrillic, 40)
	if strings.Join(parts, "") != cyrillic {
		t.Fatalf("rune split lost text: %q", parts)
	}
	for _, part := range parts {
		if n := len([]rune(part)); n > 40 {
			t.Fatalf("part has %d runes", n)
		}
	}
}
