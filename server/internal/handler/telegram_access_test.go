package handler

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	testBoundChat  int64 = -1003107704922
	testOtherChat  int64 = -1009999999999
	testAllowedUID int64 = 111
	testOtherUID   int64 = 222
)

func installation(policy string, allowed ...int64) db.TelegramInstallation {
	return db.TelegramInstallation{
		BotUsername:            "sd_pm_agent_bot",
		ChatID:                 pgtype.Text{String: "-1003107704922", Valid: true},
		AccessPolicy:           policy,
		AllowedTelegramUserIds: allowed,
	}
}

// The default must refuse everyone. These agents hold repo, git, QA and deploy
// tooling, so an inbound group message is an instruction to something that can
// change code — and every member of the group can send one.
func TestTelegramSenderAllowedDefaultsClosed(t *testing.T) {
	if telegramSenderAllowed(installation("closed"), testBoundChat, testAllowedUID) {
		t.Error("closed policy admitted a sender")
	}
	// An unrecognised policy must fail shut, not open.
	if telegramSenderAllowed(installation("something-new"), testBoundChat, testAllowedUID) {
		t.Error("unknown policy admitted a sender; it must fail shut")
	}
	// Zero value: a row created before the policy column existed.
	if telegramSenderAllowed(db.TelegramInstallation{}, testBoundChat, testAllowedUID) {
		t.Error("zero-value installation admitted a sender")
	}
}

// An invite is not an authorization: adding the bot to another group must not
// grant that group the same power.
func TestTelegramSenderAllowedRequiresTheBoundChat(t *testing.T) {
	for _, policy := range []string{"open", "allowlist"} {
		row := installation(policy, testAllowedUID)
		if telegramSenderAllowed(row, testOtherChat, testAllowedUID) {
			t.Errorf("%s policy admitted a message from an unbound chat", policy)
		}
	}
	// Never bound to any chat at all.
	unbound := installation("open")
	unbound.ChatID = pgtype.Text{}
	if telegramSenderAllowed(unbound, testBoundChat, testAllowedUID) {
		t.Error("unbound installation admitted a sender")
	}
}

func TestTelegramSenderAllowedAllowlist(t *testing.T) {
	row := installation("allowlist", testAllowedUID, 333)

	if !telegramSenderAllowed(row, testBoundChat, testAllowedUID) {
		t.Error("listed user was refused")
	}
	if !telegramSenderAllowed(row, testBoundChat, 333) {
		t.Error("second listed user was refused")
	}
	if telegramSenderAllowed(row, testBoundChat, testOtherUID) {
		t.Error("unlisted user was admitted")
	}
	// An empty allowlist behaves exactly like closed.
	if telegramSenderAllowed(installation("allowlist"), testBoundChat, testAllowedUID) {
		t.Error("empty allowlist admitted a sender")
	}
}

func TestTelegramSenderAllowedOpen(t *testing.T) {
	row := installation("open")
	if !telegramSenderAllowed(row, testBoundChat, testOtherUID) {
		t.Error("open policy refused a sender in the bound chat")
	}
}
