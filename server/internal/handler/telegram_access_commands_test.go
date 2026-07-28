package handler

import (
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestParseAccessTargetDefaultsToThisChat(t *testing.T) {
	// The common case: someone standing in a new group types /allow and means
	// "this room". Requiring an argument there would be the main friction the
	// feature exists to remove.
	target, id, errMsg := parseAccessTarget([]string{"/allow"}, -100777, 42)
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if target != "chat" || id != -100777 {
		t.Fatalf("got %s/%d, want chat/-100777", target, id)
	}
}

func TestParseAccessTargetInfersKindFromSign(t *testing.T) {
	// Telegram numbers groups negative and users positive, so a bare id is
	// never ambiguous and the keyword can be dropped.
	cases := []struct {
		arg    string
		target string
		id     int64
	}{
		{"905434593", "user", 905434593},
		{"-1004336001519", "chat", -1004336001519},
	}
	for _, c := range cases {
		target, id, errMsg := parseAccessTarget([]string{"/allow", c.arg}, -1, 1)
		if errMsg != "" {
			t.Fatalf("%s: unexpected error: %s", c.arg, errMsg)
		}
		if target != c.target || id != c.id {
			t.Fatalf("%s: got %s/%d, want %s/%d", c.arg, target, id, c.target, c.id)
		}
	}
}

func TestParseAccessTargetRefusesUsernames(t *testing.T) {
	// The Bot API offers no username→id lookup. Silently guessing would grant
	// the wrong person, so this must fail loudly rather than approximately.
	for _, args := range [][]string{
		{"/allow", "@jamshid"},
		{"/allow", "user", "@jamshid"},
	} {
		if _, _, errMsg := parseAccessTarget(args, -1, 1); errMsg == "" {
			t.Fatalf("%v: expected a refusal for a @username", args)
		}
	}
}

func TestParseAccessTargetRejectsGarbage(t *testing.T) {
	if _, _, errMsg := parseAccessTarget([]string{"/allow", "user", "abc"}, -1, 1); errMsg == "" {
		t.Fatal("expected usage text for a non-numeric id")
	}
}

func TestToggleIDIsIdempotent(t *testing.T) {
	// Granting twice must not duplicate: the list is rendered back to admins in
	// /access, and a doubled entry reads as a second, separate grant.
	list := toggleID(nil, 7, true)
	list = toggleID(list, 7, true)
	if len(list) != 1 || list[0] != 7 {
		t.Fatalf("got %v, want [7]", list)
	}
	list = toggleID(list, 7, false)
	if len(list) != 0 {
		t.Fatalf("got %v, want empty after deny", list)
	}
	// Denying someone absent is a no-op, not an error — an admin re-running
	// /deny to be sure must not corrupt the list.
	if got := toggleID([]int64{1, 2}, 9, false); len(got) != 2 {
		t.Fatalf("got %v, want [1 2]", got)
	}
}

func TestToggleIDPreservesOrder(t *testing.T) {
	got := toggleID([]int64{5, 6, 7}, 6, false)
	if len(got) != 2 || got[0] != 5 || got[1] != 7 {
		t.Fatalf("got %v, want [5 7]", got)
	}
}

func TestTelegramChatAllowedRequiresMembership(t *testing.T) {
	row := db.TelegramInstallation{AllowedChatIds: []int64{-100, -200}}
	if !telegramChatAllowed(row, -200) {
		t.Fatal("a listed chat must be allowed")
	}
	if telegramChatAllowed(row, -300) {
		t.Fatal("an unlisted chat must be refused")
	}
	// An installation bound to nothing trusts nobody. Being invited to a group
	// is not authorization to act in it.
	if telegramChatAllowed(db.TelegramInstallation{}, -100) {
		t.Fatal("an unbound installation must trust no chat")
	}
}

func TestTelegramSenderAllowedAcrossChats(t *testing.T) {
	// The regression this guards: multi-chat means the gate can no longer be an
	// equality check against one bound chat.
	row := db.TelegramInstallation{
		AllowedChatIds:         []int64{-100, -200},
		AccessPolicy:           "allowlist",
		AllowedTelegramUserIds: []int64{42},
	}
	if !telegramSenderAllowed(row, -200, 42) {
		t.Fatal("an allowed user in a second allowed chat must pass")
	}
	if telegramSenderAllowed(row, -999, 42) {
		t.Fatal("an allowed user in an unlisted chat must be refused")
	}
	if telegramSenderAllowed(row, -100, 43) {
		t.Fatal("an unlisted user must be refused")
	}
}

func TestTelegramSenderAllowedFailsShut(t *testing.T) {
	row := db.TelegramInstallation{AllowedChatIds: []int64{-100}, AccessPolicy: "closed"}
	if telegramSenderAllowed(row, -100, 42) {
		t.Fatal("closed must refuse everyone")
	}
	// An unrecognised policy — a value from a newer build, or a typo — must
	// deny, never default to open.
	row.AccessPolicy = "whatever"
	if telegramSenderAllowed(row, -100, 42) {
		t.Fatal("an unknown policy must fail shut")
	}
}

func TestTelegramUserIsAdminIsSeparateFromAllowed(t *testing.T) {
	// The whole point of two lists: being able to ask must not imply being able
	// to grant.
	row := db.TelegramInstallation{
		AllowedTelegramUserIds: []int64{42},
		AdminTelegramUserIds:   []int64{7},
	}
	if telegramUserIsAdmin(row, 42) {
		t.Fatal("an allowed user must not be an admin by default")
	}
	if !telegramUserIsAdmin(row, 7) {
		t.Fatal("a listed admin must be recognised")
	}
}
