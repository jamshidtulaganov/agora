package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeHiddenNavAcceptsValidKeys(t *testing.T) {
	got, errMsg := normalizeHiddenNav([]string{"usage", "my_issues", "ai-accounts"})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	want := []string{"usage", "my_issues", "ai-accounts"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// An empty list is the "show everything again" reset — it must round-trip as
// a non-nil slice so the column is written as `[]`, never SQL NULL (which
// COALESCE would read as "leave untouched").
func TestNormalizeHiddenNavEmptyIsNonNil(t *testing.T) {
	got, errMsg := normalizeHiddenNav([]string{})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if got == nil {
		t.Fatal("expected a non-nil empty slice")
	}
	if len(got) != 0 {
		t.Fatalf("expected empty slice, got %v", got)
	}
}

func TestNormalizeHiddenNavTrimsAndDeduplicates(t *testing.T) {
	got, errMsg := normalizeHiddenNav([]string{" usage ", "mcp", "usage"})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	want := []string{"usage", "mcp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestNormalizeHiddenNavRejectsMalformedKeys(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{"empty", ""},
		{"uppercase", "Usage"},
		{"path traversal", "../../etc/passwd"},
		{"leading digit", "1usage"},
		{"whitespace inside", "my issues"},
		{"too long", strings.Repeat("a", 41)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, errMsg := normalizeHiddenNav([]string{tc.key}); errMsg == "" {
				t.Fatalf("expected %q to be rejected", tc.key)
			}
		})
	}
}

// Settings is the only route back to the screen that restores a hidden item.
// The client never offers it, but the API must refuse it too — otherwise a
// direct PATCH can lock a user out of their own preference.
func TestNormalizeHiddenNavRejectsAlwaysVisibleKeys(t *testing.T) {
	_, errMsg := normalizeHiddenNav([]string{"usage", "settings"})
	if errMsg == "" {
		t.Fatal("expected settings to be rejected")
	}
	if !strings.Contains(errMsg, "settings") {
		t.Fatalf("error should name the offending key, got %q", errMsg)
	}
}

func TestNormalizeHiddenNavRejectsOversizedList(t *testing.T) {
	keys := make([]string, MaxHiddenNavKeys+1)
	for i := range keys {
		keys[i] = "usage"
	}
	if _, errMsg := normalizeHiddenNav(keys); errMsg == "" {
		t.Fatal("expected an oversized list to be rejected")
	}
}

// The key set is frontend-owned: a nav item this server build has never heard
// of must still persist, so an older backend can serve a newer client.
func TestNormalizeHiddenNavAllowsUnknownKeys(t *testing.T) {
	got, errMsg := normalizeHiddenNav([]string{"some-future-nav-item"})
	if errMsg != "" {
		t.Fatalf("unexpected error: %s", errMsg)
	}
	if !reflect.DeepEqual(got, []string{"some-future-nav-item"}) {
		t.Fatalf("got %v", got)
	}
}

func newHiddenNavTestUser(t *testing.T, email string) string {
	t.Helper()
	ctx := context.Background()

	var userID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		"Hidden Nav Test", email,
	).Scan(&userID); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM "user" WHERE id = $1`, userID)
	})
	return userID
}

func storedHiddenNav(t *testing.T, userID string) []string {
	t.Helper()
	var raw []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT hidden_nav FROM "user" WHERE id = $1`, userID,
	).Scan(&raw); err != nil {
		t.Fatalf("lookup user: %v", err)
	}
	var keys []string
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("decode hidden_nav %q: %v", raw, err)
	}
	return keys
}

func TestUpdateMeStoresHiddenNav(t *testing.T) {
	userID := newHiddenNavTestUser(t, "nav-set@agora.dev")

	w := httptest.NewRecorder()
	testHandler.UpdateMe(w, newPatchMeRequest(userID, `{"hidden_nav":["usage","mcp"]}`))

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := storedHiddenNav(t, userID); !reflect.DeepEqual(got, []string{"usage", "mcp"}) {
		t.Fatalf("stored %v", got)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !reflect.DeepEqual(resp["hidden_nav"], []any{"usage", "mcp"}) {
		t.Fatalf("response hidden_nav = %v", resp["hidden_nav"])
	}
}

// COALESCE semantics — a PATCH that doesn't mention hidden_nav (e.g. the
// timezone picker) must not wipe the user's sidebar customization.
func TestUpdateMePreservesHiddenNavWhenNotProvided(t *testing.T) {
	userID := newHiddenNavTestUser(t, "nav-preserve@agora.dev")

	if _, err := testPool.Exec(context.Background(),
		`UPDATE "user" SET hidden_nav = '["usage"]' WHERE id = $1`, userID,
	); err != nil {
		t.Fatalf("preset hidden_nav: %v", err)
	}

	w := httptest.NewRecorder()
	testHandler.UpdateMe(w, newPatchMeRequest(userID, `{"name":"Updated Name"}`))

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := storedHiddenNav(t, userID); !reflect.DeepEqual(got, []string{"usage"}) {
		t.Fatalf("expected hidden_nav preserved, got %v", got)
	}
}

// "Show all" sends an explicit empty array, which must clear the list rather
// than read as "field omitted".
func TestUpdateMeResetsHiddenNavOnEmptyArray(t *testing.T) {
	userID := newHiddenNavTestUser(t, "nav-reset@agora.dev")

	if _, err := testPool.Exec(context.Background(),
		`UPDATE "user" SET hidden_nav = '["usage","mcp"]' WHERE id = $1`, userID,
	); err != nil {
		t.Fatalf("preset hidden_nav: %v", err)
	}

	w := httptest.NewRecorder()
	testHandler.UpdateMe(w, newPatchMeRequest(userID, `{"hidden_nav":[]}`))

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := storedHiddenNav(t, userID); len(got) != 0 {
		t.Fatalf("expected hidden_nav cleared, got %v", got)
	}
}

func TestUpdateMeRejectsHidingSettings(t *testing.T) {
	userID := newHiddenNavTestUser(t, "nav-settings@agora.dev")

	w := httptest.NewRecorder()
	testHandler.UpdateMe(w, newPatchMeRequest(userID, `{"hidden_nav":["settings"]}`))

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if got := storedHiddenNav(t, userID); len(got) != 0 {
		t.Fatalf("expected hidden_nav untouched, got %v", got)
	}
}

// GetMe must always emit an array, never null — the frontend iterates it
// without a nil guard.
func TestGetMeReturnsEmptyHiddenNavArray(t *testing.T) {
	userID := newHiddenNavTestUser(t, "nav-getme@agora.dev")

	w := httptest.NewRecorder()
	testHandler.GetMe(w, newPatchMeRequest(userID, ""))

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	arr, ok := resp["hidden_nav"].([]any)
	if !ok {
		t.Fatalf("expected hidden_nav array, got %T (%v)", resp["hidden_nav"], resp["hidden_nav"])
	}
	if len(arr) != 0 {
		t.Fatalf("expected empty hidden_nav, got %v", arr)
	}
}
