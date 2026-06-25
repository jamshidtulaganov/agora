package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// signMiniAppInitData builds a genuinely-signed initData query string for the
// Mini App login tests. Mirrors Telegram's WebApp signing so the handler runs
// its real VerifyInitData path. botToken must match what telegramBotToken()
// reads from TELEGRAM_BOT_TOKEN in the test.
func signMiniAppInitData(botToken, userJSON string, authDate time.Time) string {
	fields := map[string]string{
		"user":      userJSON,
		"auth_date": strconv.FormatInt(authDate.Unix(), 10),
	}

	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(fields[k])
	}
	secret := hmacSum([]byte("WebAppData"), []byte(botToken))
	mac := hmacSum(secret, []byte(b.String()))

	vals := url.Values{}
	for k, v := range fields {
		vals.Set(k, v)
	}
	vals.Set("hash", hex.EncodeToString(mac))
	return vals.Encode()
}

func hmacSum(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}

func TestMiniAppLoginValidInitDataIssuesSession(t *testing.T) {
	setupTelegramTest(t)
	t.Setenv("TELEGRAM_BOT_TOKEN", "TESTTOKEN")
	ctx := context.Background()

	const telegramID = "778899771"
	email := telegramSyntheticEmail(telegramID)
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM user_external_identity WHERE provider = $1 AND external_id = $2`, providerTelegram, telegramID)
		testPool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, email)
	})

	initData := signMiniAppInitData("TESTTOKEN", `{"id":778899771,"first_name":"Dana"}`, time.Now())

	w := httptest.NewRecorder()
	req := newRequest("POST", "/auth/telegram/miniapp", map[string]string{"init_data": initData})
	testHandler.TelegramMiniAppLogin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("MiniApp login: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp LoginResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("MiniApp login: expected a JWT token")
	}
	if resp.User.Email != email {
		t.Fatalf("MiniApp login: user email = %q, want %q", resp.User.Email, email)
	}

	linked, err := testHandler.userIDByExternalIdentity(ctx, providerTelegram, telegramID)
	if err != nil {
		t.Fatalf("userIDByExternalIdentity: %v", err)
	}
	if linked != resp.User.ID {
		t.Fatalf("external identity linked to %q, want user %q", linked, resp.User.ID)
	}
}

func TestMiniAppLoginNewUserGetsFirstName(t *testing.T) {
	setupTelegramTest(t)
	t.Setenv("TELEGRAM_BOT_TOKEN", "TESTTOKEN")
	ctx := context.Background()

	const telegramID = "667788662"
	email := telegramSyntheticEmail(telegramID)
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM user_external_identity WHERE provider = $1 AND external_id = $2`, providerTelegram, telegramID)
		testPool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, email)
	})

	initData := signMiniAppInitData("TESTTOKEN", `{"id":667788662,"first_name":"Charlie"}`, time.Now())

	w := httptest.NewRecorder()
	req := newRequest("POST", "/auth/telegram/miniapp", map[string]string{"init_data": initData})
	testHandler.TelegramMiniAppLogin(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("MiniApp login name: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp LoginResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.User.Name != "Charlie" {
		t.Fatalf("new miniapp user name = %q, want Charlie (seeded from first_name)", resp.User.Name)
	}
}

func TestMiniAppLoginBadHashReturns401(t *testing.T) {
	setupTelegramTest(t)
	t.Setenv("TELEGRAM_BOT_TOKEN", "TESTTOKEN")

	initData := signMiniAppInitData("TESTTOKEN", `{"id":123,"first_name":"X"}`, time.Now())
	// Replace the hash with a wrong (but well-formed hex) value.
	v, _ := url.ParseQuery(initData)
	v.Set("hash", "deadbeef")
	tampered := v.Encode()

	w := httptest.NewRecorder()
	req := newRequest("POST", "/auth/telegram/miniapp", map[string]string{"init_data": tampered})
	testHandler.TelegramMiniAppLogin(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("MiniApp bad hash: expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMiniAppLoginExpiredReturns401(t *testing.T) {
	setupTelegramTest(t)
	t.Setenv("TELEGRAM_BOT_TOKEN", "TESTTOKEN")

	// auth_date 25h in the past — older than the 24h TTL.
	initData := signMiniAppInitData("TESTTOKEN", `{"id":321,"first_name":"Y"}`, time.Now().Add(-25*time.Hour))

	w := httptest.NewRecorder()
	req := newRequest("POST", "/auth/telegram/miniapp", map[string]string{"init_data": initData})
	testHandler.TelegramMiniAppLogin(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("MiniApp expired: expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMiniAppLoginMissingInitData(t *testing.T) {
	setupTelegramTest(t)
	t.Setenv("TELEGRAM_BOT_TOKEN", "TESTTOKEN")

	w := httptest.NewRecorder()
	req := newRequest("POST", "/auth/telegram/miniapp", map[string]string{"init_data": ""})
	testHandler.TelegramMiniAppLogin(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("MiniApp missing init_data: expected 400, got %d", w.Code)
	}
}

func TestMiniAppLoginDisabledWhenUnconfigured(t *testing.T) {
	if testHandler == nil {
		t.Skip("no DB handler; skipping (DATABASE_URL not set)")
	}
	prevBot := testHandler.telegramBot
	testHandler.telegramBot = nil
	t.Cleanup(func() { testHandler.telegramBot = prevBot })

	w := httptest.NewRecorder()
	req := newRequest("POST", "/auth/telegram/miniapp", map[string]string{"init_data": "x"})
	testHandler.TelegramMiniAppLogin(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("MiniApp unconfigured: expected 503, got %d", w.Code)
	}
}
