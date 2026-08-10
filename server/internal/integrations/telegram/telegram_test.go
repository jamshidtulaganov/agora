package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseStartPayload(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		wantNonce string
		wantOK    bool
	}{
		{"happy", "/start login_abc123", "abc123", true},
		{"leading/trailing space", "   /start login_xyz   ", "xyz", true},
		{"group @botname suffix", "/start@my_bot login_n0nce", "n0nce", true},
		{"nonce with underscores", "/start login_a_b_c", "a_b_c", true},
		{"bare start", "/start", "", false},
		{"start no payload prefix", "/start hello", "", false},
		{"empty nonce", "/start login_", "", false},
		{"wrong command", "/help login_abc", "", false},
		{"not a command", "login_abc", "", false},
		{"empty", "", "", false},
		{"whitespace only", "   ", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nonce, ok := ParseStartPayload(tc.text)
			if ok != tc.wantOK {
				t.Fatalf("ParseStartPayload(%q) ok = %v, want %v", tc.text, ok, tc.wantOK)
			}
			if nonce != tc.wantNonce {
				t.Fatalf("ParseStartPayload(%q) nonce = %q, want %q", tc.text, nonce, tc.wantNonce)
			}
		})
	}
}

func TestSendMessageHitsMockServer(t *testing.T) {
	var (
		gotPath        string
		gotContentType string
		gotBody        sendMessageRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true,"result":{"message_id":42}}`)
	}))
	defer srv.Close()

	c := NewBotClient("TESTTOKEN")
	c.BaseURL = srv.URL
	c.HTTPClient = srv.Client()

	if err := c.SendMessage(context.Background(), "123456", "Your code is 654321"); err != nil {
		t.Fatalf("SendMessage: unexpected error: %v", err)
	}

	if want := "/botTESTTOKEN/sendMessage"; gotPath != want {
		t.Fatalf("request path = %q, want %q", gotPath, want)
	}
	if !strings.HasPrefix(gotContentType, "application/json") {
		t.Fatalf("content-type = %q, want application/json", gotContentType)
	}
	if gotBody.ChatID != "123456" {
		t.Fatalf("chat_id = %q, want 123456", gotBody.ChatID)
	}
	if gotBody.Text != "Your code is 654321" {
		t.Fatalf("text = %q, want the code message", gotBody.Text)
	}
}

func TestSendMessageTelegramError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`)
	}))
	defer srv.Close()

	c := NewBotClient("TESTTOKEN")
	c.BaseURL = srv.URL
	c.HTTPClient = srv.Client()

	err := c.SendMessage(context.Background(), "123456", "hi")
	if err == nil {
		t.Fatal("SendMessage: expected error on ok=false response, got nil")
	}
	if !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("SendMessage error = %v, want Telegram description", err)
	}
}

func TestDeleteMessageHitsMockServer(t *testing.T) {
	var (
		gotPath string
		gotBody struct {
			ChatID    string `json:"chat_id"`
			MessageID int64  `json:"message_id"`
		}
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true,"result":true}`)
	}))
	defer srv.Close()

	c := NewBotClient("TESTTOKEN")
	c.BaseURL = srv.URL
	c.HTTPClient = srv.Client()
	if err := c.DeleteMessage(context.Background(), "-100123", 77); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if gotPath != "/botTESTTOKEN/deleteMessage" || gotBody.ChatID != "-100123" || gotBody.MessageID != 77 {
		t.Fatalf("delete request = path %q body %+v", gotPath, gotBody)
	}
}

func TestSetWebhookHitsMockServer(t *testing.T) {
	var (
		gotPath string
		gotBody setWebhookRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true,"result":true,"description":"Webhook was set"}`)
	}))
	defer srv.Close()

	c := NewBotClient("TESTTOKEN")
	c.BaseURL = srv.URL
	c.HTTPClient = srv.Client()

	err := c.SetWebhook(context.Background(), "https://app.example.com/telegram/webhook", "s3cr3t", []string{"message"})
	if err != nil {
		t.Fatalf("SetWebhook: unexpected error: %v", err)
	}
	if want := "/botTESTTOKEN/setWebhook"; gotPath != want {
		t.Fatalf("request path = %q, want %q", gotPath, want)
	}
	if gotBody.URL != "https://app.example.com/telegram/webhook" {
		t.Fatalf("url = %q, want the webhook url", gotBody.URL)
	}
	if gotBody.SecretToken != "s3cr3t" {
		t.Fatalf("secret_token = %q, want s3cr3t", gotBody.SecretToken)
	}
	if len(gotBody.AllowedUpdates) != 1 || gotBody.AllowedUpdates[0] != "message" {
		t.Fatalf("allowed_updates = %v, want [message]", gotBody.AllowedUpdates)
	}
}

func TestSetWebhookEmptyURL(t *testing.T) {
	c := NewBotClient("TESTTOKEN")
	if err := c.SetWebhook(context.Background(), "  ", "secret", []string{"message"}); err == nil {
		t.Fatal("SetWebhook: expected error on blank url, got nil")
	}
}

func TestSetWebhookTelegramError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: bad webhook: HTTPS url must be provided"}`)
	}))
	defer srv.Close()

	c := NewBotClient("TESTTOKEN")
	c.BaseURL = srv.URL
	c.HTTPClient = srv.Client()

	err := c.SetWebhook(context.Background(), "http://insecure.example.com/telegram/webhook", "", nil)
	if err == nil {
		t.Fatal("SetWebhook: expected error on ok=false response, got nil")
	}
	if !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("SetWebhook error = %v, want Telegram description", err)
	}
}

func TestSendMessageNoToken(t *testing.T) {
	c := NewBotClient("   ")
	if err := c.SendMessage(context.Background(), "1", "x"); err != ErrNoToken {
		t.Fatalf("SendMessage with blank token = %v, want ErrNoToken", err)
	}
}

// fixedClock is a mutable test clock for driving LoginStore expiry.
type fixedClock struct{ t time.Time }

func (f *fixedClock) now() time.Time { return f.t }
func (f *fixedClock) advance(d time.Duration) {
	f.t = f.t.Add(d)
}

func newTestStore() (*LoginStore, *fixedClock) {
	clk := &fixedClock{t: time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)}
	s := NewLoginStore()
	s.SetClock(clk.now)
	return s, clk
}

func TestLoginStoreHappyPath(t *testing.T) {
	s, _ := newTestStore()

	s.Start("nonce1")
	if !s.Bind("nonce1", "tg-555", "Alice", "123456") {
		t.Fatal("Bind: expected ok for a started nonce")
	}

	identity, firstName, ok := s.Verify("nonce1", "123456")
	if !ok {
		t.Fatal("Verify: expected ok for correct code")
	}
	if identity != "tg-555" {
		t.Fatalf("Verify identity = %q, want tg-555", identity)
	}
	if firstName != "Alice" {
		t.Fatalf("Verify firstName = %q, want Alice", firstName)
	}

	// Single-use: replay must fail after a successful verify.
	if _, _, ok := s.Verify("nonce1", "123456"); ok {
		t.Fatal("Verify: nonce should be consumed (single-use) after success")
	}
}

func TestLoginStoreWrongCode(t *testing.T) {
	s, _ := newTestStore()

	s.Start("nonce2")
	s.Bind("nonce2", "tg-1", "", "111111")

	if _, _, ok := s.Verify("nonce2", "999999"); ok {
		t.Fatal("Verify: expected failure for wrong code")
	}
	// Wrong code must NOT consume the nonce — the user can retry the code.
	if _, _, ok := s.Verify("nonce2", "111111"); !ok {
		t.Fatal("Verify: correct code after a wrong attempt should still succeed")
	}
}

func TestLoginStoreUnboundNonce(t *testing.T) {
	s, _ := newTestStore()

	s.Start("nonce3") // never bound (user never started the bot)
	if _, _, ok := s.Verify("nonce3", "123456"); ok {
		t.Fatal("Verify: expected failure for an unbound nonce")
	}
}

func TestLoginStoreUnknownNonce(t *testing.T) {
	s, _ := newTestStore()
	if _, _, ok := s.Verify("does-not-exist", "123456"); ok {
		t.Fatal("Verify: expected failure for an unknown nonce")
	}
}

func TestLoginStoreExpiry(t *testing.T) {
	s, clk := newTestStore()

	s.Start("nonce4")
	s.Bind("nonce4", "tg-9", "", "424242")

	// Advance to exactly the TTL boundary — entry is expired (>= ttl).
	clk.advance(DefaultLoginTTL)
	if _, _, ok := s.Verify("nonce4", "424242"); ok {
		t.Fatal("Verify: expected failure at TTL boundary (entry expired)")
	}
}

func TestLoginStoreBindAfterExpiry(t *testing.T) {
	s, clk := newTestStore()

	s.Start("nonce5")
	clk.advance(DefaultLoginTTL + time.Second)
	if s.Bind("nonce5", "tg-2", "", "555555") {
		t.Fatal("Bind: expected failure for an expired nonce")
	}
}

func TestLoginStoreVerifyJustBeforeExpiry(t *testing.T) {
	s, clk := newTestStore()

	s.Start("nonce6")
	s.Bind("nonce6", "tg-3", "", "777777")

	clk.advance(DefaultLoginTTL - time.Nanosecond)
	identity, _, ok := s.Verify("nonce6", "777777")
	if !ok {
		t.Fatal("Verify: expected success just before TTL boundary")
	}
	if identity != "tg-3" {
		t.Fatalf("Verify identity = %q, want tg-3", identity)
	}
}

func TestLoginStoreCleanup(t *testing.T) {
	s, clk := newTestStore()

	s.Start("old")
	s.Start("fresh-anchor") // anchors createdAt at t0 too, but we re-start it later

	clk.advance(DefaultLoginTTL + time.Minute)
	// "fresh" started after the advance stays alive; "old" is swept.
	s.Start("fresh")

	s.Cleanup()

	if _, _, ok := s.Verify("old", "x"); ok {
		t.Fatal("Cleanup: expected old nonce to be removed")
	}
	// fresh was just started (and is unbound) so Verify returns false, but it
	// must still EXIST — bind then verify proves it survived cleanup.
	if !s.Bind("fresh", "tg-x", "", "000000") {
		t.Fatal("Cleanup: fresh nonce should have survived")
	}
	if _, _, ok := s.Verify("fresh", "000000"); !ok {
		t.Fatal("Cleanup: fresh nonce should verify after surviving cleanup")
	}
}

// TestLoginStoreAttemptCapInvalidatesNonce is the regression for the brute-force
// finding: after maxVerifyAttempts wrong codes the nonce is invalidated, so even
// the CORRECT code fails afterwards. Before the fix the nonce survived every
// wrong attempt and the 6-digit code was guessable within the TTL.
func TestLoginStoreAttemptCapInvalidatesNonce(t *testing.T) {
	s, _ := newTestStore()

	s.Start("brute")
	s.Bind("brute", "tg-b", "", "123456")

	// maxVerifyAttempts wrong codes.
	for i := 0; i < maxVerifyAttempts; i++ {
		if _, _, ok := s.Verify("brute", "000000"); ok {
			t.Fatalf("Verify: wrong code attempt %d should fail", i+1)
		}
	}
	// The nonce is now invalidated: the correct code must NOT succeed.
	if _, _, ok := s.Verify("brute", "123456"); ok {
		t.Fatal("Verify: nonce should be invalidated after attempt cap; correct code must fail")
	}
}

// TestLoginStoreFewerThanCapThenCorrect proves the cap does not lock out a
// legitimate user who fat-fingers a few digits before getting it right.
func TestLoginStoreFewerThanCapThenCorrect(t *testing.T) {
	s, _ := newTestStore()

	s.Start("retry")
	s.Bind("retry", "tg-r", "Bob", "654321")

	// One short of the cap: all wrong.
	for i := 0; i < maxVerifyAttempts-1; i++ {
		if _, _, ok := s.Verify("retry", "111111"); ok {
			t.Fatalf("Verify: wrong code attempt %d should fail", i+1)
		}
	}
	// The correct code still succeeds because the cap was not reached.
	identity, firstName, ok := s.Verify("retry", "654321")
	if !ok {
		t.Fatal("Verify: correct code below the attempt cap should still succeed")
	}
	if identity != "tg-r" {
		t.Fatalf("Verify identity = %q, want tg-r", identity)
	}
	if firstName != "Bob" {
		t.Fatalf("Verify firstName = %q, want Bob", firstName)
	}
}

// TestVerifyConstantTimeComparison documents the constant-time contract. We
// cannot reliably measure wall-clock timing in a unit test, so this asserts the
// behavioral guarantee subtle.ConstantTimeCompare provides: codes of differing
// length and codes differing only in the last digit are both rejected without
// any early-out that would change the result.
func TestVerifyConstantTimeComparison(t *testing.T) {
	s, _ := newTestStore()

	s.Start("ct")
	s.Bind("ct", "tg-ct", "", "123456")

	// Differing length must be rejected (no panic, no partial match). Three
	// failed attempts here stay below maxVerifyAttempts (5), so the nonce
	// survives for the final correct check below.
	if _, _, ok := s.Verify("ct", "12345"); ok {
		t.Fatal("Verify: shorter code must be rejected")
	}
	if _, _, ok := s.Verify("ct", "1234567"); ok {
		t.Fatal("Verify: longer code must be rejected")
	}
	// Off-by-one-digit at the end must be rejected.
	if _, _, ok := s.Verify("ct", "123457"); ok {
		t.Fatal("Verify: last-digit-wrong code must be rejected")
	}
	// The genuinely correct code still works (nonce was not consumed by the
	// failed attempts above).
	if _, _, ok := s.Verify("ct", "123456"); !ok {
		t.Fatal("Verify: correct code must succeed after failed attempts")
	}
}
