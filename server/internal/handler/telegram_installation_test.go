package handler

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// A bot token is full control of that bot — it can post to every chat the bot
// is in. The wire shape must never carry it, not even masked: a partial token
// still narrows a brute-force search.
func TestTelegramInstallationResponseHidesTheToken(t *testing.T) {
	row := db.TelegramInstallation{
		AgentID:           pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
		BotTokenEncrypted: []byte("sealed-bytes-here"),
		BotUsername:       "sd_pm_agent_bot",
		BotUserID:         8935986908,
		ChatID:            pgtype.Text{String: "-1003107704922", Valid: true},
		Status:            "active",
	}
	resp := telegramInstallationToResponse(row)

	if resp.BotUsername != "sd_pm_agent_bot" || resp.BotUserID != "8935986908" {
		t.Errorf("identity fields wrong: %+v", resp)
	}
	if resp.ChatID != "-1003107704922" {
		t.Errorf("chat id missing: %+v", resp)
	}

	// Nothing in the struct may echo the sealed bytes.
	for _, field := range []string{resp.AgentID, resp.BotUsername, resp.BotUserID, resp.ChatID, resp.Status, resp.InstalledAt} {
		if strings.Contains(field, "sealed-bytes-here") {
			t.Fatalf("response leaked the token material: %+v", resp)
		}
	}
}

func TestTelegramInstallationResponseOmitsAbsentChat(t *testing.T) {
	// An installation can exist before the bot has been added to any group.
	resp := telegramInstallationToResponse(db.TelegramInstallation{
		BotUsername: "b", BotUserID: 1, Status: "active",
	})
	if resp.ChatID != "" {
		t.Errorf("chat id should be empty when unset, got %q", resp.ChatID)
	}
}

// Storing a bot token in plaintext is not an acceptable fallback, so a missing
// seal key must fail the install rather than degrade to it.
func TestTelegramSealBoxRequiresKey(t *testing.T) {
	t.Setenv("AGORA_TELEGRAM_SECRET_KEY", "")
	if _, err := telegramSealBox(); err == nil {
		t.Fatal("expected an error with no seal key configured")
	} else if err != ErrTelegramSealKeyMissing {
		t.Errorf("got %v, want ErrTelegramSealKeyMissing", err)
	}
}

func TestTelegramSealRoundTrip(t *testing.T) {
	// 32 raw bytes, base64 — the shape LoadKey expects.
	t.Setenv("AGORA_TELEGRAM_SECRET_KEY", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	box, err := telegramSealBox()
	if err != nil {
		t.Fatalf("seal box: %v", err)
	}
	const token = "8935986908:AAEU6w3t-hf_snb0O5trRdzdWOzdHC-tfgo"
	sealed, err := box.Seal([]byte(token))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if strings.Contains(string(sealed), "8935986908") {
		t.Fatal("sealed bytes still contain the token in the clear")
	}
	opened, err := box.Open(sealed)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if string(opened) != token {
		t.Errorf("round trip changed the token")
	}
}
