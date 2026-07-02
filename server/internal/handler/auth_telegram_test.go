package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/integrations/telegram"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// setupTelegramTest wires the handler's Telegram fields against a mock Bot API
// server and sets the bot username env var. It returns the captured-send hook
// and registers cleanup that restores prior state and deletes any user the
// flow created.
func setupTelegramTest(t *testing.T) (sent *capturedSend) {
	t.Helper()
	if testHandler == nil {
		t.Skip("no DB handler; skipping (DATABASE_URL not set)")
	}

	sent = &capturedSend{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			ChatID string `json:"chat_id"`
			Text   string `json:"text"`
		}
		_ = json.Unmarshal(raw, &body)
		sent.chatID = body.ChatID
		sent.text = body.Text
		sent.count++
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	}))
	t.Cleanup(srv.Close)

	bot := telegram.NewBotClient("TESTTOKEN")
	bot.BaseURL = srv.URL
	bot.HTTPClient = srv.Client()

	prevBot := testHandler.telegramBot
	prevStore := testHandler.telegramLogins
	testHandler.telegramBot = bot
	testHandler.telegramLogins = telegram.NewLoginStore()
	t.Setenv("TELEGRAM_BOT_USERNAME", "agora_test_bot")
	t.Cleanup(func() {
		testHandler.telegramBot = prevBot
		testHandler.telegramLogins = prevStore
	})

	return sent
}

type capturedSend struct {
	chatID string
	text   string
	count  int
}

func TestTelegramStartReturnsNonceAndDeepLink(t *testing.T) {
	setupTelegramTest(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/auth/telegram/start", nil)
	testHandler.TelegramStart(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("TelegramStart: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp telegramStartResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if resp.Nonce == "" {
		t.Fatal("TelegramStart: expected non-empty nonce")
	}
	wantPrefix := "https://t.me/agora_test_bot?start=login_"
	if !strings.HasPrefix(resp.DeepLink, wantPrefix) {
		t.Fatalf("TelegramStart: deep_link = %q, want prefix %q", resp.DeepLink, wantPrefix)
	}
	if resp.DeepLink != wantPrefix+resp.Nonce {
		t.Fatalf("TelegramStart: deep_link nonce mismatch: %q vs nonce %q", resp.DeepLink, resp.Nonce)
	}
}

func TestTelegramStartDisabledWhenUnconfigured(t *testing.T) {
	if testHandler == nil {
		t.Skip("no DB handler; skipping (DATABASE_URL not set)")
	}
	prevBot := testHandler.telegramBot
	testHandler.telegramBot = nil
	t.Cleanup(func() { testHandler.telegramBot = prevBot })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/auth/telegram/start", nil)
	testHandler.TelegramStart(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("TelegramStart unconfigured: expected 503, got %d", w.Code)
	}
}

func TestTelegramWebhookBindsAndDMs(t *testing.T) {
	sent := setupTelegramTest(t)

	// First mint a nonce via Start so the webhook has something to bind.
	w := httptest.NewRecorder()
	testHandler.TelegramStart(w, httptest.NewRequest("POST", "/auth/telegram/start", nil))
	var start telegramStartResponse
	json.NewDecoder(w.Body).Decode(&start)

	// Deliver the "/start login_<nonce>" update in a PRIVATE chat.
	update := map[string]any{
		"message": map[string]any{
			"text": "/start login_" + start.Nonce,
			"from": map[string]any{"id": 987654321},
			"chat": map[string]any{"id": 987654321, "type": "private"},
		},
	}
	body, _ := json.Marshal(update)
	w = httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/telegram/webhook", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	testHandler.TelegramWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("TelegramWebhook: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if sent.count != 1 {
		t.Fatalf("TelegramWebhook: expected 1 DM sent, got %d", sent.count)
	}
	if sent.chatID != "987654321" {
		t.Fatalf("TelegramWebhook: DM chat_id = %q, want 987654321", sent.chatID)
	}
	if !strings.Contains(sent.text, "login code") {
		t.Fatalf("TelegramWebhook: DM text = %q, want it to contain the login code", sent.text)
	}
}

// TestTelegramWebhookSendsToFromIDNotChatID is the regression for the OTP-leak
// finding: even if chat.id differs from from.id (a group), the code must go to
// from.id (the bound user), never chat.id. Here we use a PRIVATE chat whose
// id deliberately differs from from.id to prove the send target is from.id.
func TestTelegramWebhookSendsToFromIDNotChatID(t *testing.T) {
	sent := setupTelegramTest(t)

	w := httptest.NewRecorder()
	testHandler.TelegramStart(w, httptest.NewRequest("POST", "/auth/telegram/start", nil))
	var start telegramStartResponse
	json.NewDecoder(w.Body).Decode(&start)

	const fromID = "555000111"
	update := map[string]any{
		"message": map[string]any{
			"text": "/start login_" + start.Nonce,
			"from": map[string]any{"id": 555000111},
			// chat.id != from.id, but still private. The code must go to from.id.
			"chat": map[string]any{"id": 999999999, "type": "private"},
		},
	}
	body, _ := json.Marshal(update)
	w = httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/telegram/webhook", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	testHandler.TelegramWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("TelegramWebhook: expected 200, got %d", w.Code)
	}
	if sent.count != 1 {
		t.Fatalf("TelegramWebhook: expected 1 DM, got %d", sent.count)
	}
	if sent.chatID != fromID {
		t.Fatalf("TelegramWebhook: DM chat_id = %q, want from.id %q (never chat.id)", sent.chatID, fromID)
	}
}

// TestTelegramWebhookIgnoresNonPrivateChat is the regression for the OTP-leak
// finding: a "/start login_<nonce>" issued in a GROUP (chat.type != "private")
// must be acked 200 and otherwise ignored — no DM sent, no nonce bound.
func TestTelegramWebhookIgnoresNonPrivateChat(t *testing.T) {
	sent := setupTelegramTest(t)

	w := httptest.NewRecorder()
	testHandler.TelegramStart(w, httptest.NewRequest("POST", "/auth/telegram/start", nil))
	var start telegramStartResponse
	json.NewDecoder(w.Body).Decode(&start)

	// Group form: "/start@bot login_<nonce>" delivered in a group chat.
	update := map[string]any{
		"message": map[string]any{
			"text": "/start@agora_test_bot login_" + start.Nonce,
			"from": map[string]any{"id": 424242},
			"chat": map[string]any{"id": -100123456, "type": "group"},
		},
	}
	body, _ := json.Marshal(update)
	w = httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/telegram/webhook", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	testHandler.TelegramWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("TelegramWebhook group: expected 200, got %d", w.Code)
	}
	if sent.count != 0 {
		t.Fatalf("TelegramWebhook group: expected NO DM (OTP leak), got %d", sent.count)
	}
	// The nonce must NOT have been bound: a subsequent verify with any code 401s
	// because the entry is still unbound.
	if _, _, ok := testHandler.telegramLogins.Verify(start.Nonce, "000000"); ok {
		t.Fatal("TelegramWebhook group: nonce must not be bound by a group /start")
	}
}

func TestTelegramWebhookIgnoresNonStartMessage(t *testing.T) {
	sent := setupTelegramTest(t)

	update := map[string]any{
		"message": map[string]any{
			"text": "hello there",
			"from": map[string]any{"id": 111},
			"chat": map[string]any{"id": 111, "type": "private"},
		},
	}
	body, _ := json.Marshal(update)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/telegram/webhook", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	testHandler.TelegramWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("TelegramWebhook non-start: expected 200, got %d", w.Code)
	}
	if sent.count != 0 {
		t.Fatalf("TelegramWebhook non-start: expected no DM, got %d", sent.count)
	}
}

func TestTelegramWebhookRejectsBadSecret(t *testing.T) {
	setupTelegramTest(t)
	t.Setenv("TELEGRAM_WEBHOOK_SECRET", "s3cr3t")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/telegram/webhook", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "wrong")
	testHandler.TelegramWebhook(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("TelegramWebhook bad secret: expected 401, got %d", w.Code)
	}
}

func TestTelegramVerifyWrongCodeReturns401(t *testing.T) {
	setupTelegramTest(t)

	// Mint + bind a nonce so a code exists for it, then verify with the wrong
	// code.
	testHandler.telegramLogins.Start("nonce-verify-1")
	testHandler.telegramLogins.Bind("nonce-verify-1", "555000", "", "123456")

	w := httptest.NewRecorder()
	req := newRequest("POST", "/auth/telegram/verify", map[string]string{
		"nonce": "nonce-verify-1",
		"code":  "000000",
	})
	testHandler.TelegramVerify(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("TelegramVerify wrong code: expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestTelegramVerifyMissingFields(t *testing.T) {
	setupTelegramTest(t)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/auth/telegram/verify", map[string]string{"nonce": "x"})
	testHandler.TelegramVerify(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("TelegramVerify missing code: expected 400, got %d", w.Code)
	}
}

func TestTelegramVerifySuccessIssuesSession(t *testing.T) {
	setupTelegramTest(t)
	ctx := context.Background()

	const telegramID = "778899001"
	email := telegramSyntheticEmail(telegramID)
	t.Cleanup(func() {
		// Remove any user + identity the flow created.
		testPool.Exec(ctx, `DELETE FROM user_external_identity WHERE provider = $1 AND external_id = $2`, providerTelegram, telegramID)
		testPool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, email)
	})

	testHandler.telegramLogins.Start("nonce-ok")
	testHandler.telegramLogins.Bind("nonce-ok", telegramID, "", "246810")

	w := httptest.NewRecorder()
	req := newRequest("POST", "/auth/telegram/verify", map[string]string{
		"nonce": "nonce-ok",
		"code":  "246810",
	})
	testHandler.TelegramVerify(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("TelegramVerify success: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp LoginResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("TelegramVerify success: expected a JWT token")
	}
	if resp.User.Email != email {
		t.Fatalf("TelegramVerify success: user email = %q, want %q", resp.User.Email, email)
	}

	// The external identity must be linked to the new user.
	linked, err := testHandler.userIDByExternalIdentity(ctx, providerTelegram, telegramID)
	if err != nil {
		t.Fatalf("userIDByExternalIdentity: %v", err)
	}
	if linked != resp.User.ID {
		t.Fatalf("external identity linked to %q, want user %q", linked, resp.User.ID)
	}

	// The nonce is single-use: a replay must now 401.
	w = httptest.NewRecorder()
	req = newRequest("POST", "/auth/telegram/verify", map[string]string{
		"nonce": "nonce-ok",
		"code":  "246810",
	})
	testHandler.TelegramVerify(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("TelegramVerify replay: expected 401, got %d", w.Code)
	}
}

// TestTelegramVerifyNewUserGetsFirstName is the regression for the name-seeding
// finding: a NEWLY created Telegram user's display name is set to the bound
// first_name (not the synthetic "tg<id>" email local-part). The first_name is
// carried from the webhook bind through Verify.
func TestTelegramVerifyNewUserGetsFirstName(t *testing.T) {
	setupTelegramTest(t)
	ctx := context.Background()

	const telegramID = "667788990"
	email := telegramSyntheticEmail(telegramID)
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM user_external_identity WHERE provider = $1 AND external_id = $2`, providerTelegram, telegramID)
		testPool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, email)
	})

	// Bind carries the first_name through to the verify step.
	testHandler.telegramLogins.Start("nonce-name")
	testHandler.telegramLogins.Bind("nonce-name", telegramID, "Charlie", "135790")

	w := httptest.NewRecorder()
	req := newRequest("POST", "/auth/telegram/verify", map[string]string{
		"nonce": "nonce-name",
		"code":  "135790",
	})
	testHandler.TelegramVerify(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("TelegramVerify name: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp LoginResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if resp.User.Name != "Charlie" {
		t.Fatalf("new telegram user name = %q, want %q (seeded from first_name)", resp.User.Name, "Charlie")
	}
}

// TestDefaultWorkspaceSlugsEmptyMeansNoAutoJoin locks in the invite-scoping
// fix: with AGORA_DEFAULT_WORKSPACE_SLUGS unset/blank there is NO blanket
// auto-join, so a Telegram login joins only the workspace an invite names. A
// hardcoded fallback here previously force-joined every Mini App user into all
// three SD workspaces regardless of the invite (the cross-workspace leak).
func TestDefaultWorkspaceSlugsEmptyMeansNoAutoJoin(t *testing.T) {
	t.Setenv("AGORA_DEFAULT_WORKSPACE_SLUGS", "")
	if got := defaultWorkspaceSlugs(); len(got) != 0 {
		t.Fatalf("unset env should yield no default slugs (no blanket auto-join), got %v", got)
	}
	t.Setenv("AGORA_DEFAULT_WORKSPACE_SLUGS", "   ")
	if got := defaultWorkspaceSlugs(); len(got) != 0 {
		t.Fatalf("blank env should yield no default slugs, got %v", got)
	}
	// Explicit opt-in still works (comma-separated, lowercased, trimmed, deduped).
	t.Setenv("AGORA_DEFAULT_WORKSPACE_SLUGS", " sd-main , SD-main ,sd-cs ")
	got := defaultWorkspaceSlugs()
	if len(got) != 2 || got[0] != "sd-main" || got[1] != "sd-cs" {
		t.Fatalf("explicit slugs should parse/dedup to [sd-main sd-cs], got %v", got)
	}
}

// TestTelegramVerifyAutoJoinsDefaultWorkspaces is the regression for the SD
// fork auto-join: a Telegram verify for a fresh user results in a 'member'
// membership in every seeded default workspace. Hermetic — it seeds its own
// workspaces (overriding AGORA_DEFAULT_WORKSPACE_SLUGS) and tears down the
// user, identity, memberships, and workspaces on cleanup.
func TestTelegramVerifyAutoJoinsDefaultWorkspaces(t *testing.T) {
	setupTelegramTest(t)
	ctx := context.Background()

	// Two of the three default slugs are seeded; the third is intentionally
	// absent to prove a missing workspace is skipped, not fatal.
	const presentSlugA = "sd-autojoin-a"
	const presentSlugB = "sd-autojoin-b"
	const missingSlug = "sd-autojoin-missing"
	t.Setenv("AGORA_DEFAULT_WORKSPACE_SLUGS", presentSlugA+","+presentSlugB+","+missingSlug)

	seed := func(slug string) pgtype.UUID {
		var id pgtype.UUID
		if err := testPool.QueryRow(ctx, `
			INSERT INTO workspace (name, slug, description, issue_prefix)
			VALUES ($1, $2, $3, $4)
			RETURNING id
		`, slug, slug, "auto-join test workspace", "AJ").Scan(&id); err != nil {
			t.Fatalf("seed workspace %q: %v", slug, err)
		}
		return id
	}
	wsA := seed(presentSlugA)
	wsB := seed(presentSlugB)

	const telegramID = "990011223"
	email := telegramSyntheticEmail(telegramID)
	t.Cleanup(func() {
		// Order: memberships (FK to user + workspace) → identity/user → workspaces.
		testPool.Exec(ctx, `DELETE FROM member WHERE workspace_id = ANY($1)`, []pgtype.UUID{wsA, wsB})
		testPool.Exec(ctx, `DELETE FROM user_external_identity WHERE provider = $1 AND external_id = $2`, providerTelegram, telegramID)
		testPool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, email)
		testPool.Exec(ctx, `DELETE FROM workspace WHERE slug = ANY($1)`, []string{presentSlugA, presentSlugB})
	})

	testHandler.telegramLogins.Start("nonce-autojoin")
	testHandler.telegramLogins.Bind("nonce-autojoin", telegramID, "", "112233")

	w := httptest.NewRecorder()
	req := newRequest("POST", "/auth/telegram/verify", map[string]string{
		"nonce": "nonce-autojoin",
		"code":  "112233",
	})
	testHandler.TelegramVerify(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("TelegramVerify auto-join: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp LoginResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	userID := parseUUID(resp.User.ID)

	// The user is a 'member' of both seeded default workspaces.
	for slug, wsID := range map[string]pgtype.UUID{presentSlugA: wsA, presentSlugB: wsB} {
		m, err := testHandler.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
			UserID:      userID,
			WorkspaceID: wsID,
		})
		if err != nil {
			t.Fatalf("expected membership in %q, got error: %v", slug, err)
		}
		if m.Role != "member" {
			t.Fatalf("membership in %q has role %q, want %q", slug, m.Role, "member")
		}
	}

	// Re-running verify (a second login for the same user) must be idempotent:
	// no duplicate membership, no error. Bind a fresh nonce and verify again.
	testHandler.telegramLogins.Start("nonce-autojoin-2")
	testHandler.telegramLogins.Bind("nonce-autojoin-2", telegramID, "", "445566")
	w = httptest.NewRecorder()
	req = newRequest("POST", "/auth/telegram/verify", map[string]string{
		"nonce": "nonce-autojoin-2",
		"code":  "445566",
	})
	testHandler.TelegramVerify(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("TelegramVerify auto-join replay: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	members, err := testHandler.Queries.ListMembers(ctx, wsA)
	if err != nil {
		t.Fatalf("ListMembers(%q): %v", presentSlugA, err)
	}
	count := 0
	for _, m := range members {
		if uuidToString(m.UserID) == resp.User.ID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 membership for user in %q after replay, got %d", presentSlugA, count)
	}
}
