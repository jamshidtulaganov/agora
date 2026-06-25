package telegram

import (
	"encoding/hex"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// base is a fixed reference instant so freshness tests are deterministic.
var base = time.Unix(1_700_000_000, 0)

// signInitData builds a genuinely-signed initData query string for botToken from
// the given decoded fields, so tests exercise the real HMAC path rather than a
// hand-rolled stub. The data_check_string is over the decoded values (sorted by
// key); the returned string is URL-encoded exactly as Telegram delivers it.
func signInitData(botToken string, fields map[string]string) string {
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
	secret := hmacSHA256([]byte(initDataSecretKeyMessage), []byte(botToken))
	mac := hmacSHA256(secret, []byte(b.String()))

	vals := url.Values{}
	for k, v := range fields {
		vals.Set(k, v)
	}
	vals.Set("hash", hex.EncodeToString(mac))
	return vals.Encode()
}

const testBotToken = "123456:TESTTOKEN"

func validFields(authDate time.Time) map[string]string {
	return map[string]string{
		"user":      `{"id":42,"first_name":"Ann","last_name":"Lee","username":"ann"}`,
		"auth_date": strconv.FormatInt(authDate.Unix(), 10),
	}
}

func TestVerifyInitData_Valid(t *testing.T) {
	data := signInitData(testBotToken, validFields(base))
	user, err := verifyInitDataAt(testBotToken, data, 24*time.Hour, base)
	if err != nil {
		t.Fatalf("valid initData: unexpected error %v", err)
	}
	if user.ID != 42 || user.FirstName != "Ann" || user.LastName != "Lee" || user.Username != "ann" {
		t.Fatalf("parsed user = %+v, want id=42 first=Ann last=Lee username=ann", user)
	}
}

func TestVerifyInitData_ExtraSignedFieldsTolerated(t *testing.T) {
	fields := validFields(base)
	fields["query_id"] = "AAEbc123"
	fields["chat_instance"] = "-987654321"
	fields["signature"] = "abc"
	data := signInitData(testBotToken, fields)
	user, err := verifyInitDataAt(testBotToken, data, 24*time.Hour, base)
	if err != nil {
		t.Fatalf("extra fields: unexpected error %v", err)
	}
	if user.ID != 42 {
		t.Fatalf("extra fields: user.ID = %d, want 42", user.ID)
	}
}

func TestVerifyInitData_BadHash(t *testing.T) {
	data := signInitData(testBotToken, validFields(base))
	// Flip the last hex char of the hash.
	last := data[len(data)-1]
	flip := byte('0')
	if last == '0' {
		flip = '1'
	}
	data = data[:len(data)-1] + string(flip)
	if _, err := verifyInitDataAt(testBotToken, data, 24*time.Hour, base); !errors.Is(err, ErrInitDataInvalid) {
		t.Fatalf("bad hash: err = %v, want ErrInitDataInvalid", err)
	}
}

func TestVerifyInitData_TamperedFieldAfterSigning(t *testing.T) {
	good := signInitData(testBotToken, validFields(base))
	v, _ := url.ParseQuery(good)
	// Swap in a different user but keep the original hash.
	v.Set("user", `{"id":999,"first_name":"Mallory"}`)
	tampered := v.Encode()
	if _, err := verifyInitDataAt(testBotToken, tampered, 24*time.Hour, base); !errors.Is(err, ErrInitDataInvalid) {
		t.Fatalf("tampered field: err = %v, want ErrInitDataInvalid", err)
	}
}

func TestVerifyInitData_MissingHash(t *testing.T) {
	vals := url.Values{}
	for k, v := range validFields(base) {
		vals.Set(k, v)
	}
	if _, err := verifyInitDataAt(testBotToken, vals.Encode(), 24*time.Hour, base); !errors.Is(err, ErrInitDataInvalid) {
		t.Fatalf("missing hash: err = %v, want ErrInitDataInvalid", err)
	}
}

func TestVerifyInitData_MissingUser(t *testing.T) {
	// Sign a payload with no user field — valid hash, but no user to authenticate.
	data := signInitData(testBotToken, map[string]string{
		"auth_date": strconv.FormatInt(base.Unix(), 10),
	})
	if _, err := verifyInitDataAt(testBotToken, data, 24*time.Hour, base); !errors.Is(err, ErrInitDataInvalid) {
		t.Fatalf("missing user: err = %v, want ErrInitDataInvalid", err)
	}
}

func TestVerifyInitData_ZeroUserID(t *testing.T) {
	data := signInitData(testBotToken, map[string]string{
		"user":      `{"id":0,"first_name":"Nobody"}`,
		"auth_date": strconv.FormatInt(base.Unix(), 10),
	})
	if _, err := verifyInitDataAt(testBotToken, data, 24*time.Hour, base); !errors.Is(err, ErrInitDataInvalid) {
		t.Fatalf("zero user id: err = %v, want ErrInitDataInvalid", err)
	}
}

func TestVerifyInitData_Expired(t *testing.T) {
	data := signInitData(testBotToken, validFields(base))
	// Verify 25h after auth_date with a 24h TTL.
	if _, err := verifyInitDataAt(testBotToken, data, 24*time.Hour, base.Add(25*time.Hour)); !errors.Is(err, ErrInitDataExpired) {
		t.Fatalf("expired: err = %v, want ErrInitDataExpired", err)
	}
}

func TestVerifyInitData_FreshJustBeforeExpiry(t *testing.T) {
	data := signInitData(testBotToken, validFields(base))
	if _, err := verifyInitDataAt(testBotToken, data, 24*time.Hour, base.Add(24*time.Hour-time.Second)); err != nil {
		t.Fatalf("just-before-expiry: unexpected error %v", err)
	}
}

func TestVerifyInitData_MaxAgeZeroDisablesFreshness(t *testing.T) {
	data := signInitData(testBotToken, validFields(base))
	if _, err := verifyInitDataAt(testBotToken, data, 0, base.Add(1000*time.Hour)); err != nil {
		t.Fatalf("maxAge<=0 should disable freshness, got %v", err)
	}
}

// TestVerifyInitData_EmptyBotToken is the hash-bypass regression: with an unset
// bot token the WebApp secret is computable by anyone, so a self-consistent
// forged payload must STILL be rejected. The empty-token guard must fire before
// any HMAC comparison.
func TestVerifyInitData_EmptyBotToken(t *testing.T) {
	// Forge a payload signed against the empty-token secret.
	forged := signInitData("", validFields(base))
	if _, err := verifyInitDataAt("", forged, 24*time.Hour, base); !errors.Is(err, ErrInitDataInvalid) {
		t.Fatalf("empty bot token: err = %v, want ErrInitDataInvalid (hash-bypass guard)", err)
	}
	if _, err := verifyInitDataAt("   ", forged, 24*time.Hour, base); !errors.Is(err, ErrInitDataInvalid) {
		t.Fatalf("blank bot token: err = %v, want ErrInitDataInvalid", err)
	}
}

func TestVerifyInitData_WrongToken(t *testing.T) {
	// Signed with one token, verified with another — signature mismatch.
	data := signInitData(testBotToken, validFields(base))
	if _, err := verifyInitDataAt("999999:OTHER", data, 24*time.Hour, base); !errors.Is(err, ErrInitDataInvalid) {
		t.Fatalf("wrong token: err = %v, want ErrInitDataInvalid", err)
	}
}
