package config

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LoadFunc reads the current DB overrides. Provided by the server-startup
// layer (which owns db.Queries) so the config package stays free of a DB
// import and the resulting cycle.
type LoadFunc func(ctx context.Context) (map[string]string, error)

// Store resolves config values with precedence: DB override > env > default.
// A package-level singleton is initialized at server startup; the read helpers
// (Bool/Int/String) fall back to env when the store is nil, so code paths and
// tests that never initialize it keep working exactly as before.
type Store struct {
	mu   sync.RWMutex
	over map[string]string // DB overrides, refreshed periodically + on Set
	load LoadFunc
}

var singleton *Store

// Init wires the store to a DB loader, loads the current overrides, and starts
// a background refresh so changes made on another replica propagate. Safe to
// call once at startup.
func Init(ctx context.Context, load LoadFunc) {
	s := &Store{over: map[string]string{}, load: load}
	s.reload(ctx)
	singleton = s
	go s.refreshLoop()
}

func (s *Store) reload(ctx context.Context) {
	if s.load == nil {
		return
	}
	m, err := s.load(ctx)
	if err != nil || m == nil {
		return
	}
	s.mu.Lock()
	s.over = m
	s.mu.Unlock()
}

func (s *Store) refreshLoop() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for range t.C {
		s.reload(context.Background())
	}
}

func (s *Store) override(key string) (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.RLock()
	v, ok := s.over[key]
	s.mu.RUnlock()
	return v, ok
}

// NotifySet updates the in-memory override immediately (call after a DB write
// so the change is visible before the next refresh tick). No-op if the store
// was never initialized.
func NotifySet(key, value string) {
	s := singleton
	if s == nil {
		return
	}
	s.mu.Lock()
	s.over[key] = value
	s.mu.Unlock()
}

// NotifyDelete drops an override locally (call after deleting the DB row to
// reset a key back to its env/default).
func NotifyDelete(key string) {
	s := singleton
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.over, key)
	s.mu.Unlock()
}

// resolve returns the effective raw string value for a key: DB override, else
// environment, else the registry default. Never returns secret values through
// the override path (secrets are env-only, enforced at write time).
func resolve(key string) string {
	if v, ok := singleton.override(key); ok {
		return v
	}
	if v := os.Getenv(key); v != "" {
		return v
	}
	if d, ok := byKey[key]; ok {
		return d.Default
	}
	return ""
}

// Resolve returns the effective raw string value for a key (exported for the
// snapshot API). Callers must not use it for secret keys — gate on the def Kind.
func Resolve(key string) string { return resolve(key) }

// Source reports where the effective value came from ("override" | "env" |
// "default"), for the UI badge.
func Source(key string) string {
	if _, ok := singleton.override(key); ok {
		return "override"
	}
	if os.Getenv(key) != "" {
		return "env"
	}
	return "default"
}

// SecretIsSet reports whether a secret key has a non-empty env value, WITHOUT
// exposing the value.
func SecretIsSet(key string) bool { return strings.TrimSpace(os.Getenv(key)) != "" }

// Bool reports whether key resolves to a truthy value ("1" or "true").
func Bool(key string) bool {
	v := strings.TrimSpace(resolve(key))
	return v == "1" || strings.EqualFold(v, "true")
}

// String returns the resolved string value (trimmed).
func String(key string) string { return strings.TrimSpace(resolve(key)) }

// Int returns the resolved integer value, or def when unset/unparseable.
func Int(key string, def int) int {
	v := strings.TrimSpace(resolve(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
