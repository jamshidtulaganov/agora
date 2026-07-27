package telegram

import (
	"testing"
	"time"
)

// Telegram answers flood control with 429 + parameters.retry_after. Retrying
// immediately deepens the throttle window instead of clearing it, so the wait
// must come from Telegram rather than being guessed.
func TestFloodWait(t *testing.T) {
	t.Run("429 with a hint retries after exactly that long", func(t *testing.T) {
		resp := telegramResponse{ErrorCode: 429}
		resp.Parameters.RetryAfter = 7
		wait, retry := floodWait(resp)
		if !retry || wait != 7*time.Second {
			t.Errorf("wait=%v retry=%v, want 7s/true", wait, retry)
		}
	})

	t.Run("429 without a hint still backs off", func(t *testing.T) {
		wait, retry := floodWait(telegramResponse{ErrorCode: 429})
		if !retry || wait <= 0 {
			t.Errorf("wait=%v retry=%v, want a positive backoff", wait, retry)
		}
	})

	// Blocking a goroutine for minutes is worse than surfacing the failure.
	t.Run("an absurd retry_after is refused rather than waited out", func(t *testing.T) {
		resp := telegramResponse{ErrorCode: 429}
		resp.Parameters.RetryAfter = 3600
		if _, retry := floodWait(resp); retry {
			t.Error("expected a 1-hour throttle to fail fast, not block")
		}
	})

	// Retrying a 400 just repeats the same rejection.
	t.Run("non-429 errors are terminal", func(t *testing.T) {
		for _, code := range []int{400, 401, 403, 404, 500} {
			if _, retry := floodWait(telegramResponse{ErrorCode: code}); retry {
				t.Errorf("code %d should not be retried", code)
			}
		}
	})
}
