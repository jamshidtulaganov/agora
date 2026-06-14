package telegram

import (
	"crypto/subtle"
	"sync"
	"time"
)

// DefaultLoginTTL is how long a login nonce stays valid from the moment it is
// minted by Start. Five minutes is long enough for a human to tap the deep
// link, let the bot DM the code, and type it back; short enough that a leaked
// nonce/code pair is useless soon after.
const DefaultLoginTTL = 5 * time.Minute

// maxVerifyAttempts caps how many WRONG codes a single nonce tolerates before
// it is invalidated. Without a cap a 6-digit code (10^6 space) is brute-forced
// well within the 5-minute TTL — the per-IP HTTP limiter is best-effort and
// fails open when Redis is unavailable, so the authoritative attempt limit must
// live here, scoped to the nonce. After this many failures the nonce is deleted
// and even the correct code fails: the user must restart the flow.
const maxVerifyAttempts = 5

// loginEntry is one in-flight login attempt keyed by nonce.
type loginEntry struct {
	// identity is the Telegram user/chat id (rendered as a string) that
	// claimed this nonce by sending "/start login_<nonce>". Empty until
	// Bind is called.
	identity string
	// firstName is the sender's Telegram first_name (may be empty). Carried
	// through so a NEWLY created user can be seeded with it. Set by Bind.
	firstName string
	// code is the 6-digit OTP DMed to the user. Empty until Bind.
	code string
	// createdAt anchors the TTL. Set by Start and NOT refreshed by Bind, so
	// the whole flow (start -> bind -> verify) must complete within the TTL.
	createdAt time.Time
	// attempts counts FAILED Verify calls against this nonce. At
	// maxVerifyAttempts the nonce is invalidated (brute-force defence).
	attempts int
	// bound reports whether Bind has populated identity+code yet. Verify
	// rejects an unbound nonce (the user has not started the bot).
	bound bool
}

// LoginStore is an in-memory, single-node store mapping a login nonce to its
// in-flight state (Telegram identity + OTP code + creation time) with a TTL.
//
// SINGLE-NODE ONLY. State lives in process memory: a nonce minted on one
// replica is invisible to another, so a multi-replica deployment behind a load
// balancer needs a shared backend (e.g. Redis with a per-nonce key + TTL)
// instead. The method set here (Start/Bind/Verify/cleanup) maps cleanly onto
// Redis SETEX/GET/DEL, so swapping the backend later is a localized change.
//
// All methods are safe for concurrent use.
type LoginStore struct {
	mu      sync.Mutex
	entries map[string]*loginEntry
	ttl     time.Duration

	// now is the clock. Injectable so tests can drive expiry deterministically
	// without sleeping. Nil is treated as time.Now.
	now func() time.Time
}

// NewLoginStore builds an empty store with DefaultLoginTTL and the real clock.
func NewLoginStore() *LoginStore {
	return &LoginStore{
		entries: make(map[string]*loginEntry),
		ttl:     DefaultLoginTTL,
		now:     time.Now,
	}
}

// SetClock overrides the store's clock. Intended for tests; production code
// leaves the default time.Now in place. Passing nil restores time.Now.
func (s *LoginStore) SetClock(clock func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if clock == nil {
		clock = time.Now
	}
	s.now = clock
}

// SetTTL overrides the validity window. Intended for tests / operators tuning
// the flow; values <= 0 are ignored.
func (s *LoginStore) SetTTL(ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ttl = ttl
}

func (s *LoginStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// Start registers a freshly minted nonce as awaiting bind. The caller is
// responsible for generating an unguessable nonce. Re-Starting an existing
// nonce resets its state and TTL — harmless because the nonce is single-use and
// the caller never reuses one. Each Start opportunistically sweeps expired
// entries so an idle store does not grow unbounded.
func (s *LoginStore) Start(nonce string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	s.entries[nonce] = &loginEntry{createdAt: s.clock()}
}

// Bind attaches the Telegram identity, sender first_name, and OTP code to a
// previously Started nonce. firstName may be empty (Telegram users can hide it);
// it is carried only to seed a newly created user's display name. It reports
// ok=false when the nonce is unknown or already expired — the webhook should
// then ignore the update (the deep link is stale). The nonce's original
// createdAt (and therefore its TTL) is preserved.
func (s *LoginStore) Bind(nonce, identity, firstName, code string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.entries[nonce]
	if !ok {
		return false
	}
	if s.expiredLocked(e) {
		delete(s.entries, nonce)
		return false
	}
	e.identity = identity
	e.firstName = firstName
	e.code = code
	e.bound = true
	return true
}

// Verify checks code against the OTP bound to nonce. On success it returns the
// bound Telegram identity, the sender's first_name (may be empty), and consumes
// the nonce (single-use), so a replay of the same nonce+code fails. It returns
// ok=false for an unknown nonce, an unbound nonce (user never started the bot),
// an expired nonce, or a wrong code. The code comparison is constant-time to
// avoid leaking how many leading digits matched.
//
// Brute-force defence: every FAILED comparison increments a per-nonce counter;
// once it reaches maxVerifyAttempts the nonce is deleted, so subsequent calls —
// even with the correct code — fail and the user must restart. This bounds an
// attacker to maxVerifyAttempts guesses against the 10^6 code space regardless
// of any (best-effort, fail-open) HTTP-layer rate limiting.
func (s *LoginStore) Verify(nonce, code string) (identity, firstName string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, exists := s.entries[nonce]
	if !exists {
		return "", "", false
	}
	if s.expiredLocked(e) {
		delete(s.entries, nonce)
		return "", "", false
	}
	if !e.bound {
		return "", "", false
	}
	// Constant-time compare. subtle.ConstantTimeCompare already returns 0
	// for length-mismatched inputs without an early-out, so a wrong-length
	// code does not short-circuit and leak timing.
	if subtle.ConstantTimeCompare([]byte(code), []byte(e.code)) != 1 {
		// Wrong code: count the failure and invalidate the nonce once the cap
		// is hit so it cannot be brute-forced within the TTL.
		e.attempts++
		if e.attempts >= maxVerifyAttempts {
			delete(s.entries, nonce)
		}
		return "", "", false
	}
	// Single-use: a verified nonce is consumed so it cannot be replayed.
	identity = e.identity
	firstName = e.firstName
	delete(s.entries, nonce)
	return identity, firstName, true
}

// Cleanup removes every expired entry. Start calls this opportunistically; an
// operator can also schedule it. Exported so callers can sweep on their own
// cadence (e.g. a background ticker) without waiting for the next Start.
func (s *LoginStore) Cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
}

func (s *LoginStore) cleanupLocked() {
	for nonce, e := range s.entries {
		if s.expiredLocked(e) {
			delete(s.entries, nonce)
		}
	}
}

func (s *LoginStore) expiredLocked(e *loginEntry) bool {
	return s.clock().Sub(e.createdAt) >= s.ttl
}
