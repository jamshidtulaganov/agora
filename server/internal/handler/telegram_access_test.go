package handler

import (
	"context"
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

// A bot installed at runtime must start listening immediately. Before this the
// poller only opened at startup, so a freshly installed bot was silent until
// the next deploy while the API reported it active.
func TestAgentTelegramPollerRegistry(t *testing.T) {
	agentA := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	agentB := pgtype.UUID{Bytes: [16]byte{2}, Valid: true}

	t.Run("no base context means nothing is started", func(t *testing.T) {
		// Tests and deployments that never call StartAgentTelegramPollers must
		// not spawn goroutines with no lifetime to cancel.
		h := &Handler{}
		h.startAgentTelegramPoller(db.TelegramInstallation{AgentID: agentA})
		if h.tgPollers.ready() {
			t.Error("a poller was registered without a base context")
		}
	})

	t.Run("re-install cancels the previous loop", func(t *testing.T) {
		// Telegram allows ONE getUpdates consumer per bot; a second makes both
		// fail with 409, so the old loop must be cancelled before the new one.
		h := &Handler{}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		h.tgPollers.base = ctx
		h.tgPollers.cancel = map[string]context.CancelFunc{}

		cancelled := false
		h.tgPollers.cancel[uuidToString(agentA)] = func() { cancelled = true }

		h.startAgentTelegramPoller(db.TelegramInstallation{AgentID: agentA})
		if !cancelled {
			t.Error("re-install left the previous loop running")
		}
		if _, ok := h.tgPollers.cancel[uuidToString(agentA)]; !ok {
			t.Error("no replacement loop was registered")
		}
	})

	t.Run("uninstall stops only that agent's loop", func(t *testing.T) {
		h := &Handler{}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		h.tgPollers.base = ctx

		stoppedA, stoppedB := false, false
		h.tgPollers.cancel = map[string]context.CancelFunc{
			uuidToString(agentA): func() { stoppedA = true },
			uuidToString(agentB): func() { stoppedB = true },
		}

		h.stopAgentTelegramPoller(agentA)
		if !stoppedA {
			t.Error("uninstalled agent kept polling")
		}
		if stoppedB {
			t.Error("uninstalling one agent stopped another's loop")
		}
		if _, ok := h.tgPollers.cancel[uuidToString(agentA)]; ok {
			t.Error("cancelled loop left in the registry")
		}
	})

	t.Run("stopping an agent with no loop is harmless", func(t *testing.T) {
		h := &Handler{}
		h.tgPollers.cancel = map[string]context.CancelFunc{}
		h.stopAgentTelegramPoller(agentA) // must not panic
	})
}
