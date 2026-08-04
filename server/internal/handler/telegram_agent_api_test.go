package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

func TestResolveAgentTargetChatRefusesAnUnboundChat(t *testing.T) {
	// The failure this prevents: an agent naming a group it was never bound to
	// and the server posting there anyway. A message delivered to the wrong
	// room is worse than one not delivered.
	row := db.TelegramInstallation{
		AllowedChatIds: []int64{-100, -200},
		ChatID:         pgtype.Text{String: "-100", Valid: true},
	}
	if _, ok := resolveAgentTargetChat(httptest.NewRecorder(), row, "-999"); ok {
		t.Fatal("an unbound chat was accepted")
	}
	// And it must NOT quietly fall back to the default — that would turn a
	// mistargeted message into a silently misdelivered one.
	got, ok := resolveAgentTargetChat(httptest.NewRecorder(), row, "-200")
	if !ok || got != "-200" {
		t.Fatalf("a bound chat was refused: got %q ok=%v", got, ok)
	}
}

func TestResolveAgentTargetChatUsesTheDefaultWhenUnnamed(t *testing.T) {
	row := db.TelegramInstallation{
		AllowedChatIds: []int64{-100},
		ChatID:         pgtype.Text{String: "-100", Valid: true},
	}
	got, ok := resolveAgentTargetChat(httptest.NewRecorder(), row, "")
	if !ok || got != "-100" {
		t.Fatalf("got %q ok=%v, want the default chat", got, ok)
	}
	// With no default configured, an unnamed chat is an error rather than a
	// guess at the first allowed chat.
	bare := db.TelegramInstallation{AllowedChatIds: []int64{-100}}
	if _, ok := resolveAgentTargetChat(httptest.NewRecorder(), bare, ""); ok {
		t.Fatal("a missing default silently resolved to something")
	}
}

func TestResolveAgentTargetChatRejectsNonNumeric(t *testing.T) {
	// @username is not a chat id and never resolves to one.
	row := db.TelegramInstallation{AllowedChatIds: []int64{-100}}
	if _, ok := resolveAgentTargetChat(httptest.NewRecorder(), row, "@sd_team"); ok {
		t.Fatal("a username was accepted as a chat id")
	}
}

func TestParseQuestionCallback(t *testing.T) {
	id := "11111111-1111-1111-1111-111111111111"
	got, index, ok := parseQuestionCallback("q:" + id + ":2")
	if !ok || index != 2 || uuidToString(got) != id {
		t.Fatalf("got id=%s index=%d ok=%v", uuidToString(got), index, ok)
	}
}

func TestParseQuestionCallbackRejectsForeignPayloads(t *testing.T) {
	// A callback payload is client-supplied. Anything not minted by the ask
	// flow must be ignored rather than half-parsed — the prefix is what keeps
	// a stray button from another flow out.
	for _, data := range []string{
		"",
		"login_abc",
		"q:not-a-uuid:0",
		"q:11111111-1111-1111-1111-111111111111",
		"q:11111111-1111-1111-1111-111111111111:x",
		"q:11111111-1111-1111-1111-111111111111:0:extra",
	} {
		if _, _, ok := parseQuestionCallback(data); ok {
			t.Errorf("%q was accepted", data)
		}
	}
}

func TestTelegramQuestionMatchesOnlyItsOriginalChat(t *testing.T) {
	question := db.TelegramQuestion{ChatID: "-100123"}
	if !telegramQuestionMatchesChat(question, -100123) {
		t.Fatal("question did not match the chat where it was asked")
	}
	if telegramQuestionMatchesChat(question, -100456) {
		t.Fatal("a different allowed chat could answer the question")
	}
}
