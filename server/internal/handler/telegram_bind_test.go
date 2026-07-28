package handler

import (
	"strings"
	"testing"
)

func TestNewBindToken(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		tok, err := newBindToken()
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		if seen[tok] {
			t.Fatal("minted a duplicate token")
		}
		seen[tok] = true

		// Telegram deep-link payloads accept only [A-Za-z0-9_-]; anything else
		// silently breaks the link rather than erroring, so the alphabet is a
		// correctness constraint, not cosmetics.
		for _, r := range tok {
			ok := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
				(r >= '0' && r <= '9') || r == '_' || r == '-'
			if !ok {
				t.Fatalf("token %q contains %q, which Telegram will not carry", tok, r)
			}
		}
		// Payload cap is 64 chars and ours is prefixed.
		if len(telegramBindPayloadPrefix+tok) > 64 {
			t.Fatalf("payload too long for a deep link: %d", len(telegramBindPayloadPrefix+tok))
		}
	}
}

// Only the hash is stored, so a leaked database cannot be replayed into a
// binding.
func TestHashBindToken(t *testing.T) {
	raw := "abc123-token_value"
	h1 := hashBindToken(raw)

	if strings.Contains(h1, raw) {
		t.Fatal("hash echoes the raw token")
	}
	if h1 != hashBindToken(raw) {
		t.Error("hash is not stable")
	}
	if h1 == hashBindToken(raw+"x") {
		t.Error("different tokens hashed the same")
	}
	if len(h1) != 64 {
		t.Errorf("hash length = %d, want 64 hex chars", len(h1))
	}
}

// The prefix exists so an unrelated /start — notably the login deep link —
// is never mistaken for a binding attempt.
func TestBindPayloadPrefixIsolatesTheFlow(t *testing.T) {
	if !strings.HasPrefix(telegramBindPayloadPrefix, "bind") {
		t.Errorf("prefix %q should be self-describing", telegramBindPayloadPrefix)
	}
	// A login payload must not survive the TrimPrefix test used at redemption:
	// TrimPrefix returns the input unchanged when the prefix is absent, which
	// is exactly how the handler detects "not ours".
	login := "login_abc123"
	if strings.TrimPrefix(login, telegramBindPayloadPrefix) != login {
		t.Error("a login payload would be treated as a binding token")
	}
}
