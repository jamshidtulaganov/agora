package telegram

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// initData verification for the Telegram Mini App (NOT the bot-OTP login flow in
// login_store.go). A Mini App loads inside Telegram and is handed a signed
// `window.Telegram.WebApp.initData` query string; the bot token is the shared
// secret. Verifying the HMAC proves the caller is a genuine Telegram client and
// authenticates the embedded user without any code round-trip.
//
// Like the rest of this package, everything here is DB-free and unit-testable
// with pure functions and an injectable clock — no DATABASE_URL required.

// initDataSecretKeyMessage is the fixed HMAC key Telegram specifies for deriving
// the per-bot secret key from the bot token (see VerifyInitData).
const initDataSecretKeyMessage = "WebAppData"

// InitDataUser is the parsed `user` field of a Telegram WebApp initData payload.
// Only the fields the login flow consumes are modeled, mirroring the `From`
// shape that telegramUpdate uses in the handler layer.
type InitDataUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

// Sentinel errors. Callers MUST collapse all of these to a single generic 401 at
// the HTTP boundary — never surface which check failed, so an attacker cannot
// distinguish "bad signature" from "expired" from "no user" (a verification
// oracle). Log the specific sentinel server-side only.
var (
	// ErrInitDataInvalid covers a missing/forged hash, a tampered field, an
	// unconfigured bot token, or an absent/zero-id user.
	ErrInitDataInvalid = errors.New("telegram: init data failed verification")
	// ErrInitDataExpired means the signature is valid but auth_date is older
	// than the caller's maxAge — a replayed (but genuine) payload.
	ErrInitDataExpired = errors.New("telegram: init data auth_date too old")
)

// VerifyInitData validates a raw window.Telegram.WebApp.initData query string
// against botToken per the Telegram WebApp spec and returns the embedded user.
//
//	secret_key        = HMAC_SHA256(key="WebAppData", message=botToken)
//	data_check_string = sorted "k=v" lines (every field except "hash"), joined "\n"
//	expected          = HMAC_SHA256(key=secret_key, message=data_check_string)
//	valid             = constant_time_equal(expected, hex_decode(hash))
//
// When maxAge > 0 the payload is rejected with ErrInitDataExpired once
// now-auth_date exceeds it, bounding replay of a captured initData string.
// maxAge <= 0 disables the freshness check (tests only; production passes a
// positive TTL). Uses time.Now; verifyInitDataAt is the clock-injected core.
func VerifyInitData(botToken, initData string, maxAge time.Duration) (*InitDataUser, error) {
	return verifyInitDataAt(botToken, initData, maxAge, time.Now())
}

func verifyInitDataAt(botToken, initData string, maxAge time.Duration, now time.Time) (*InitDataUser, error) {
	// Empty-token guard FIRST, before any HMAC work. With a blank token the
	// secret key is HMAC("WebAppData", "") — a value any attacker can compute —
	// which would let them forge a valid initData for any Telegram id. Refuse
	// outright so an unconfigured bot can never authenticate anyone.
	if strings.TrimSpace(botToken) == "" {
		return nil, ErrInitDataInvalid
	}

	values, err := url.ParseQuery(initData)
	if err != nil {
		return nil, ErrInitDataInvalid
	}

	hash := values.Get("hash")
	if hash == "" {
		return nil, ErrInitDataInvalid
	}
	// Telegram sends the hash hex-encoded; decode to raw bytes so the compare is
	// over bytes (hex-case insensitive) and constant-time via hmac.Equal.
	gotMAC, err := hex.DecodeString(hash)
	if err != nil {
		return nil, ErrInitDataInvalid
	}

	// data_check_string: every field except hash, as "key=value", sorted by key,
	// joined with newlines. Every remaining field participates — do not
	// cherry-pick keys, or a forged extra field would slip past the signature.
	// url.ParseQuery has already percent-decoded the values, which is exactly
	// what the spec hashes over.
	keys := make([]string, 0, len(values))
	for k := range values {
		if k == "hash" {
			continue
		}
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
		b.WriteString(values.Get(k))
	}
	dataCheckString := b.String()

	secretKey := hmacSHA256([]byte(initDataSecretKeyMessage), []byte(botToken))
	expectedMAC := hmacSHA256(secretKey, []byte(dataCheckString))
	if !hmac.Equal(expectedMAC, gotMAC) {
		return nil, ErrInitDataInvalid
	}

	// Signature is valid. Now bound replay via auth_date freshness.
	if maxAge > 0 {
		authDateRaw := values.Get("auth_date")
		authUnix, err := strconv.ParseInt(authDateRaw, 10, 64)
		if err != nil {
			return nil, ErrInitDataInvalid
		}
		if now.Sub(time.Unix(authUnix, 0)) > maxAge {
			return nil, ErrInitDataExpired
		}
	}

	// Parse the embedded user. A signed-but-userless payload (e.g. an inline
	// query context) must not reach find-or-create with a zero id.
	rawUser := values.Get("user")
	if strings.TrimSpace(rawUser) == "" {
		return nil, ErrInitDataInvalid
	}
	var user InitDataUser
	if err := json.Unmarshal([]byte(rawUser), &user); err != nil {
		return nil, ErrInitDataInvalid
	}
	if user.ID == 0 {
		return nil, ErrInitDataInvalid
	}

	return &user, nil
}

// hmacSHA256 returns HMAC-SHA256(message) keyed by key.
func hmacSHA256(key, message []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(message)
	return mac.Sum(nil)
}
