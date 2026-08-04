package handler

import (
	"strings"
	"testing"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
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

func TestTelegramCommanderRoleRequiresAWorkspaceAdmin(t *testing.T) {
	// The authority for /allow and /deny is the caller's Agora role, not a
	// Telegram-side list. Walks the outcomes against the real DB.
	ctx := t.Context()
	row := db.TelegramInstallation{WorkspaceID: parseUUID(testWorkspaceID)}

	link := func(t *testing.T, telegramID, userID string) {
		t.Helper()
		if err := testHandler.linkExternalIdentity(ctx, providerTelegram, telegramID, userID); err != nil {
			t.Fatalf("link %s: %v", telegramID, err)
		}
		t.Cleanup(func() {
			testPool.Exec(ctx, `DELETE FROM user_external_identity WHERE provider = $1 AND external_id = $2`,
				providerTelegram, telegramID)
		})
	}

	// No linked Agora account at all — the state every stranger in the group is
	// in. Must not be mistaken for a plain lack of privilege.
	if got := testHandler.telegramCommanderRole(ctx, row, 987654321012); got != telegramCommanderUnlinked {
		t.Fatalf("unlinked telegram id: got %v, want unlinked", got)
	}

	// The workspace owner.
	link(t, "987654321013", testUserID)
	if got := testHandler.telegramCommanderRole(ctx, row, 987654321013); got != telegramCommanderAdmin {
		t.Fatalf("workspace owner: got %v, want admin", got)
	}

	// A plain member of the same workspace. This is the case the feature turns
	// on: they can be on the allowlist and instruct the agent, yet must not be
	// able to widen who else can.
	// Delete first: a run that dies between insert and cleanup would otherwise
	// leave this test permanently red on the unique email.
	testPool.Exec(ctx, `DELETE FROM "user" WHERE email = 'tg-member-access@test.local'`)
	var memberUserID string
	if err := testPool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ('TG Member', 'tg-member-access@test.local') RETURNING id`).
		Scan(&memberUserID); err != nil {
		t.Fatalf("create member user: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM "user" WHERE id = $1`, memberUserID) })
	if _, err := testPool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')`,
		testWorkspaceID, memberUserID); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	link(t, "987654321014", memberUserID)
	if got := testHandler.telegramCommanderRole(ctx, row, 987654321014); got != telegramCommanderNotAdmin {
		t.Fatalf("plain member: got %v, want not-admin", got)
	}

	// Linked to Agora but not a member of THIS workspace. The account exists;
	// the standing does not.
	testPool.Exec(ctx, `DELETE FROM "user" WHERE email = 'tg-outsider-access@test.local'`)
	var outsiderUserID string
	if err := testPool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ('TG Outsider', 'tg-outsider-access@test.local') RETURNING id`).
		Scan(&outsiderUserID); err != nil {
		t.Fatalf("create outsider: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM "user" WHERE id = $1`, outsiderUserID) })
	link(t, "987654321015", outsiderUserID)
	if got := testHandler.telegramCommanderRole(ctx, row, 987654321015); got != telegramCommanderNotAdmin {
		t.Fatalf("outsider: got %v, want not-admin", got)
	}
}

func TestResetIsRecognisedAsACommand(t *testing.T) {
	// /reset exists because an agent carries its answers forward: once it has
	// said "that data is not available" it repeats the claim from memory even
	// after the capability lands, and re-asking cannot dislodge it.
	for _, cmd := range []string{"/reset", "/RESET", "/reset@sd_pm_agent_bot"} {
		fields := strings.Fields(cmd)
		normalised := strings.ToLower(fields[0])
		if at := strings.Index(normalised, "@"); at > 0 {
			normalised = normalised[:at]
		}
		if normalised != "/reset" {
			t.Errorf("%q normalised to %q", cmd, normalised)
		}
	}
}

func TestChatSessionHistorySurvivesANewSession(t *testing.T) {
	// The bug: the mapping table allowed one row per chat, so opening a new
	// session REPLACED the link and any task still running against the previous
	// one lost its route home. It wrote an answer nobody could receive.
	ctx := t.Context()
	agentID := parseUUID(testAgentIDForTelegram(t))
	const chatID = "-100999000111"
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM telegram_chat_session WHERE chat_id = $1`, chatID)
	})

	newSession := func(title string) db.ChatSession {
		s, err := testHandler.Queries.CreateChatSession(ctx, db.CreateChatSessionParams{
			WorkspaceID: parseUUID(testWorkspaceID),
			AgentID:     agentID,
			CreatorID:   parseUUID(testUserID),
			Title:       title,
		})
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		if _, err := testHandler.Queries.UpsertTelegramChatSession(ctx, db.UpsertTelegramChatSessionParams{
			AgentID: agentID, ChatID: chatID, ChatSessionID: s.ID,
		}); err != nil {
			t.Fatalf("link session: %v", err)
		}
		return s
	}

	first := newSession("first")
	// Retire it the way /reset does, then open the next conversation.
	if err := testHandler.Queries.ArchiveTelegramChatSession(ctx, db.ArchiveTelegramChatSessionParams{
		AgentID: agentID, ChatID: chatID,
	}); err != nil {
		t.Fatalf("archive: %v", err)
	}
	second := newSession("second")

	// The old mapping must still resolve: a late reply from the superseded
	// session has to reach the chat that asked.
	old, err := testHandler.Queries.GetTelegramChatSessionBySession(ctx, first.ID)
	if err != nil {
		t.Fatalf("the superseded session lost its route home: %v", err)
	}
	if old.ChatID != chatID {
		t.Fatalf("old mapping points at %s", old.ChatID)
	}

	// And "current" must be the new one, not the archived predecessor.
	current, err := testHandler.Queries.GetTelegramChatSession(ctx, db.GetTelegramChatSessionParams{
		AgentID: agentID, ChatID: chatID,
	})
	if err != nil {
		t.Fatalf("no current session: %v", err)
	}
	if uuidToString(current.ChatSessionID) != uuidToString(second.ID) {
		t.Fatal("current resolved to the archived session")
	}
}

func TestArchiveRetiresEveryActiveSession(t *testing.T) {
	// /reset must not leave an older active session behind — the lookup would
	// resume it, which is the stale-conversation failure /reset exists to fix.
	ctx := t.Context()
	agentID := parseUUID(testAgentIDForTelegram(t))
	const chatID = "-100999000222"
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM telegram_chat_session WHERE chat_id = $1`, chatID)
	})
	for _, title := range []string{"older", "newer"} {
		s, err := testHandler.Queries.CreateChatSession(ctx, db.CreateChatSessionParams{
			WorkspaceID: parseUUID(testWorkspaceID), AgentID: agentID,
			CreatorID: parseUUID(testUserID), Title: title,
		})
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		if _, err := testHandler.Queries.UpsertTelegramChatSession(ctx, db.UpsertTelegramChatSessionParams{
			AgentID: agentID, ChatID: chatID, ChatSessionID: s.ID,
		}); err != nil {
			t.Fatalf("link %s: %v", title, err)
		}
	}
	if err := testHandler.Queries.ArchiveTelegramChatSession(ctx, db.ArchiveTelegramChatSessionParams{
		AgentID: agentID, ChatID: chatID,
	}); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if _, err := testHandler.Queries.GetTelegramChatSession(ctx, db.GetTelegramChatSessionParams{
		AgentID: agentID, ChatID: chatID,
	}); err == nil {
		t.Fatal("an active session survived the reset")
	}
}

// testAgentIDForTelegram returns the suite's fixture agent.
func testAgentIDForTelegram(t *testing.T) string {
	t.Helper()
	var id string
	if err := testPool.QueryRow(t.Context(),
		`SELECT id::text FROM agent WHERE workspace_id = $1::uuid LIMIT 1`, testWorkspaceID).Scan(&id); err != nil {
		t.Fatalf("no fixture agent: %v", err)
	}
	return id
}
