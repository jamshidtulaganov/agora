package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/llm"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// The platform bot in a group.
//
// What has to hold:
//
//   - nothing is recorded, and nobody is answered, before a workspace admin
//     runs /track; /untrack deletes what was recorded;
//   - a message addressed to the bot runs the Assistant as the Agora member
//     who wrote it, in a conversation separate from their private one;
//   - group history is readable only by a workspace member whom Telegram
//     confirms is in the group.

// telegramTestGroup returns a fresh group chat id, cleaned up afterwards.
func telegramTestGroup(t *testing.T) int64 {
	t.Helper()
	var id int64
	if err := testPool.QueryRow(t.Context(), `SELECT -(1000000000000 + floor(random()*900000000))::bigint`).Scan(&id); err != nil {
		t.Fatalf("pick group id: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM telegram_tracked_chat WHERE chat_id = $1`, id)
		testPool.Exec(context.Background(), `DELETE FROM telegram_assistant_session WHERE chat_id = $1`, fmt.Sprint(id))
	})
	return id
}

var telegramGroupMessageSeq int64 = 1000

func groupText(t *testing.T, chatID, tgID int64, name, text string) telegramUpdate {
	t.Helper()
	telegramGroupMessageSeq++
	return decodeDecisionUpdate(t, fmt.Sprintf(
		`{"message":{"message_id":%d,"date":%d,"text":%q,
		  "from":{"id":%d,"first_name":%q,"language_code":"en"},
		  "chat":{"id":%d,"type":"supergroup","title":"Release room"}}}`,
		telegramGroupMessageSeq, time.Now().Unix(), text, tgID, name, chatID))
}

func recordedMessages(t *testing.T, chatID int64) (n int) {
	t.Helper()
	testPool.QueryRow(t.Context(), `SELECT count(*) FROM telegram_chat_message WHERE chat_id = $1`, chatID).Scan(&n)
	return n
}

func TestTelegramGroupRecordsOnlyAfterTrack(t *testing.T) {
	calls := telegramAssistantBot(t)
	telegramAssistantService(t, &scriptedToolChat{})
	_, _, admin := telegramAssistantUser(t, "tg-group-track", "TGT", true)
	chatID := telegramTestGroup(t)

	testHandler.processBotMessage(t.Context(), groupText(t, chatID, admin, "Ada", "shipping on friday"))
	if recordedMessages(t, chatID) != 0 {
		t.Fatal("an untracked group was recorded")
	}
	expectNoTelegramMessage(t, calls)

	// Spoken to before /track: say why, still record nothing.
	testHandler.processBotMessage(t.Context(), groupText(t, chatID, admin, "Ada", "@agora_test_bot what is open?"))
	if got := awaitTelegramMessage(t, calls); got.text != botT("en", "grp.nottracked") {
		t.Fatalf("untracked mention answered %q", got.text)
	}

	testHandler.processBotMessage(t.Context(), groupText(t, chatID, admin, "Ada", "/track"))
	if got := awaitTelegramMessage(t, calls); !strings.HasPrefix(got.text, "✅") || got.chatID != fmt.Sprint(chatID) {
		t.Fatalf("track answered %+v", got)
	}
	testHandler.processBotMessage(t.Context(), groupText(t, chatID, admin, "Ada", "shipping on friday"))
	testHandler.processBotMessage(t.Context(), groupText(t, chatID, admin, "Ada", "/role"))
	awaitTelegramMessage(t, calls)
	if got := recordedMessages(t, chatID); got != 1 {
		t.Fatalf("recorded %d messages, want the one plain message (commands are not conversation)", got)
	}

	testHandler.processBotMessage(t.Context(), groupText(t, chatID, admin, "Ada", "/untrack"))
	if got := awaitTelegramMessage(t, calls); got.text != botT("en", "grp.untracked") {
		t.Fatalf("untrack answered %q", got.text)
	}
	if recordedMessages(t, chatID) != 0 {
		t.Fatal("/untrack kept the recorded messages")
	}
}

// The Agora role decides, not presence in the Telegram group.
func TestTelegramGroupTrackNeedsAWorkspaceAdmin(t *testing.T) {
	calls := telegramAssistantBot(t)
	telegramAssistantService(t, &scriptedToolChat{})
	_, workspaceID, admin := telegramAssistantUser(t, "tg-group-admin", "TGD", true)
	chatID := telegramTestGroup(t)

	member := newAssistantTestUser(t, "tg-group-plain-member@agora.dev")
	addAssistantTestMember(t, workspaceID, member, "member")
	const memberTG = 611000111
	if err := testHandler.linkExternalIdentity(t.Context(), providerTelegram, fmt.Sprint(memberTG), member); err != nil {
		t.Fatalf("bind: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM user_external_identity WHERE provider = $1 AND external_id = $2`, providerTelegram, fmt.Sprint(memberTG))
	})

	for _, tc := range []struct {
		from int64
		want string
	}{
		{memberTG, "grp.notadmin"},
		{599000999, "grp.notlinked"}, // nobody's account
	} {
		testHandler.processBotMessage(t.Context(), groupText(t, chatID, tc.from, "X", "/track"))
		if got := awaitTelegramMessage(t, calls); got.text != botT("en", tc.want) {
			t.Fatalf("track by %d answered %q, want %s", tc.from, got.text, tc.want)
		}
	}
	if _, err := testHandler.Queries.GetTelegramTrackedChat(t.Context(), chatID); err == nil {
		t.Fatal("a non-admin connected the group")
	}

	testHandler.processBotMessage(t.Context(), groupText(t, chatID, admin, "Ada", "/track"))
	awaitTelegramMessage(t, calls)
	// A member may not disconnect it or change the role either.
	for _, command := range []string{"/untrack", "/role be rude"} {
		testHandler.processBotMessage(t.Context(), groupText(t, chatID, memberTG, "Bob", command))
		if got := awaitTelegramMessage(t, calls); got.text != botT("en", "grp.notadmin") {
			t.Fatalf("%s by a member answered %q", command, got.text)
		}
	}
	chat, err := testHandler.Queries.GetTelegramTrackedChat(t.Context(), chatID)
	if err != nil || chat.Role != "" {
		t.Fatalf("a member changed the group: %+v %v", chat, err)
	}
}

func TestTelegramGroupAnswersWhenAddressedInItsRole(t *testing.T) {
	calls := telegramAssistantBot(t)
	script := &scriptedToolChat{replies: []llm.Message{
		{Role: "assistant", Content: "private answer"},
		{Role: "assistant", Content: "Two tasks are open."},
	}}
	telegramAssistantService(t, script)
	userID, workspaceID, tgID := telegramAssistantUser(t, "tg-group-answer", "TGG", true)
	chatID := telegramTestGroup(t)

	// A private conversation first, so the test can prove the group turn does
	// not join it.
	testHandler.processBotMessage(t.Context(), privateText(t, tgID, "something private"))
	awaitTelegramMessage(t, calls)

	testHandler.processBotMessage(t.Context(), groupText(t, chatID, tgID, "Ada", "/track"))
	awaitTelegramMessage(t, calls)
	testHandler.processBotMessage(t.Context(), groupText(t, chatID, tgID, "Ada", "/role You are the release manager"))
	if got := awaitTelegramMessage(t, calls); got.text != botT("en", "grp.role.set") {
		t.Fatalf("role answered %q", got.text)
	}

	// Ordinary talk is recorded and not answered.
	testHandler.processBotMessage(t.Context(), groupText(t, chatID, tgID, "Ada", "lunch?"))
	expectNoTelegramMessage(t, calls)

	testHandler.processBotMessage(t.Context(), groupText(t, chatID, tgID, "Ada", "@agora_test_bot what is open?"))
	got := awaitTelegramMessage(t, calls)
	if got.chatID != fmt.Sprint(chatID) || got.text != "Two tasks are open." {
		t.Fatalf("group answer = %+v", got)
	}

	// The model saw the setting and the role, the question without the
	// mention, and none of the private conversation.
	script.mu.Lock()
	last := script.seen[len(script.seen)-1]
	script.mu.Unlock()
	var transcript strings.Builder
	for _, m := range last {
		if m.Role != "system" {
			transcript.WriteString(m.Content + "\n")
		}
	}
	turn := transcript.String()
	for _, want := range []string{`Telegram group "Release room"`, "You are the release manager", "what is open?"} {
		if !strings.Contains(turn, want) {
			t.Errorf("the group turn is missing %q:\n%s", want, turn)
		}
	}
	for _, leak := range []string{"something private", "private answer", "@agora_test_bot"} {
		if strings.Contains(turn, leak) {
			t.Errorf("the group turn carries %q:\n%s", leak, turn)
		}
	}

	// Two conversations: the private chat's and the group's, the latter
	// focused on the group's workspace.
	var conversations int
	var focus string
	testPool.QueryRow(t.Context(), `SELECT count(*) FROM telegram_assistant_session WHERE user_id = $1`, userID).Scan(&conversations)
	testPool.QueryRow(t.Context(), `
		SELECT COALESCE(s.focus_workspace_id::text, '') FROM telegram_assistant_session t
		JOIN assistant_session s ON s.id = t.session_id WHERE t.user_id = $1 AND t.chat_id = $2`,
		userID, fmt.Sprint(chatID)).Scan(&focus)
	if conversations != 2 || focus != workspaceID {
		t.Fatalf("%d conversations, group focus %q (want 2, %s)", conversations, focus, workspaceID)
	}
}

// Being in the Telegram group is not membership of the workspace.
func TestTelegramGroupRefusesOutsiders(t *testing.T) {
	calls := telegramAssistantBot(t)
	script := &scriptedToolChat{replies: []llm.Message{{Role: "assistant", Content: "should never be sent"}}}
	telegramAssistantService(t, script)
	_, _, admin := telegramAssistantUser(t, "tg-group-owner", "TGO", true)
	_, _, outsider := telegramAssistantUser(t, "tg-group-outsider", "TGX", true) // another workspace
	chatID := telegramTestGroup(t)
	testHandler.processBotMessage(t.Context(), groupText(t, chatID, admin, "Ada", "/track"))
	awaitTelegramMessage(t, calls)

	for _, tc := range []struct {
		from int64
		want string
	}{
		{outsider, "grp.notmember"},
		{588000888, "grp.notlinked"},
	} {
		for _, text := range []string{"@agora_test_bot list everything", "/summary"} {
			testHandler.processBotMessage(t.Context(), groupText(t, chatID, tc.from, "Eve", text))
			if got := awaitTelegramMessage(t, calls); got.text != botT("en", tc.want) {
				t.Fatalf("%q from %d answered %q, want %s", text, tc.from, got.text, tc.want)
			}
		}
	}
	if len(script.seen) != 0 {
		t.Fatal("an outsider reached the assistant")
	}
}

func TestTelegramSummaryAsksForTheGroupHistory(t *testing.T) {
	calls := telegramAssistantBot(t)
	script := &scriptedToolChat{replies: []llm.Message{{Role: "assistant", Content: "Decided: ship Friday."}}}
	telegramAssistantService(t, script)
	_, _, tgID := telegramAssistantUser(t, "tg-group-summary", "TGY", true)
	chatID := telegramTestGroup(t)
	testHandler.processBotMessage(t.Context(), groupText(t, chatID, tgID, "Ada", "/track"))
	awaitTelegramMessage(t, calls)

	testHandler.processBotMessage(t.Context(), groupText(t, chatID, tgID, "Ada", "/summary 48"))
	if got := awaitTelegramMessage(t, calls); got.chatID != fmt.Sprint(chatID) || got.text != "Decided: ship Friday." {
		t.Fatalf("summary = %+v", got)
	}
	script.mu.Lock()
	last := script.seen[len(script.seen)-1]
	script.mu.Unlock()
	prompt := last[len(last)-1].Content
	for _, want := range []string{"read_telegram_chat", fmt.Sprintf("chat_id %d", chatID), "hours 48"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("summary prompt is missing %q: %s", want, prompt)
		}
	}
}

func TestAssistantReadsTelegramChatOnlyForPeopleInIt(t *testing.T) {
	calls := telegramAssistantBot(t)
	telegramAssistantService(t, &scriptedToolChat{})
	userID, _, tgID := telegramAssistantUser(t, "tg-history-reader", "TGH", true)
	strangerID, _, _ := telegramAssistantUser(t, "tg-history-stranger", "TGZ", true) // another workspace
	chatID := telegramTestGroup(t)
	testHandler.processBotMessage(t.Context(), groupText(t, chatID, tgID, "Ada", "/track"))
	awaitTelegramMessage(t, calls)
	for _, line := range []string{"we ship on friday", "bob takes the migration"} {
		testHandler.processBotMessage(t.Context(), groupText(t, chatID, tgID, "Ada", line))
	}
	// Outside the window.
	if err := testHandler.Queries.RecordTelegramChatMessage(t.Context(), db.RecordTelegramChatMessageParams{
		ChatID: chatID, MessageID: 1, SenderID: tgID, SenderName: "Ada", Text: "last week's news",
		SentAt: pgtype.Timestamptz{Time: time.Now().Add(-72 * time.Hour), Valid: true},
	}); err != nil {
		t.Fatalf("seed old message: %v", err)
	}

	session := newAssistantTestSession(t, userID)
	args := fmt.Sprintf(`{"chat_id":"%d"}`, chatID)

	listed, err := executeAssistantSessionTool(t, userID, session, assistant.ToolListTelegramChats, `{}`)
	if err != nil || listed["total"] != float64(1) {
		t.Fatalf("list = %v %v", listed, err)
	}
	read, err := executeAssistantSessionTool(t, userID, session, assistant.ToolReadTelegramChat, args)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	transcript, _ := read["transcript"].(string)
	first, second := strings.Index(transcript, "we ship on friday"), strings.Index(transcript, "bob takes the migration")
	if first < 0 || second < first || strings.Contains(transcript, "last week") || read["message_count"] != float64(2) {
		t.Fatalf("transcript = %q (%v messages)", transcript, read["message_count"])
	}
	if !strings.Contains(transcript, "Ada: we ship on friday") {
		t.Fatalf("transcript does not name the sender: %q", transcript)
	}

	// A workspace the group is not tracked for.
	strangerSession := newAssistantTestSession(t, strangerID)
	if _, err := executeAssistantSessionTool(t, strangerID, strangerSession, assistant.ToolReadTelegramChat, args); err == nil {
		t.Fatal("a member of another workspace read the group")
	}

	// Removed from the Telegram group: the history goes with it.
	telegramTestMembership.Store("left")
	if _, err := executeAssistantSessionTool(t, userID, session, assistant.ToolReadTelegramChat, args); err == nil {
		t.Fatal("someone who left the group still read it")
	}
	listed, _ = executeAssistantSessionTool(t, userID, session, assistant.ToolListTelegramChats, `{}`)
	if listed["total"] != float64(0) {
		t.Fatalf("a left group is still listed: %v", listed)
	}
}

func TestTelegramTranscriptKeepsTheNewestWhenTooLarge(t *testing.T) {
	at := func(minutes int) pgtype.Timestamptz {
		return pgtype.Timestamptz{Time: time.Date(2026, 1, 2, 10, minutes, 0, 0, time.UTC), Valid: true}
	}
	transcript, count, truncated := telegramTranscript([]db.TelegramChatMessage{
		{SenderName: "B", Text: "second", SentAt: at(5)},
		{SenderName: "A", Text: "first", SentAt: at(1)},
	})
	if transcript != "2026-01-02 10:01 A: first\n2026-01-02 10:05 B: second" || count != 2 || truncated {
		t.Fatalf("transcript = %q %d %v", transcript, count, truncated)
	}

	big := make([]db.TelegramChatMessage, 0, telegramHistoryMaxMessages+1)
	for i := 0; i <= telegramHistoryMaxMessages; i++ {
		big = append(big, db.TelegramChatMessage{SenderName: "A", Text: strings.Repeat("x", 200), SentAt: at(0)})
	}
	big[0].Text = "newest"
	transcript, count, truncated = telegramTranscript(big)
	if !truncated || count >= len(big) || len(transcript) > telegramHistoryMaxChars || !strings.HasSuffix(transcript, "newest") {
		t.Fatalf("large transcript: %d messages, %d chars, truncated %v", count, len(transcript), truncated)
	}
}

func TestTelegramToolsAreReadOnlyInReviewMode(t *testing.T) {
	for _, name := range []string{assistant.ToolListTelegramChats, assistant.ToolReadTelegramChat} {
		if !assistant.IsTelegramTool(name) || !assistant.ReviewToolAllowed(name) {
			t.Errorf("%s must be a telegram tool allowed in review mode", name)
		}
	}
	var specs []json.RawMessage
	for _, spec := range assistant.TelegramToolSpecs() {
		specs = append(specs, spec.Parameters)
		if !json.Valid(spec.Parameters) {
			t.Errorf("%s has invalid parameters", spec.Name)
		}
	}
	if len(specs) != 2 {
		t.Fatalf("%d telegram tools", len(specs))
	}
}
