package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExternalIdentityLinkAndLookup(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM user_external_identity WHERE external_id = $1`, "btx-link-777")
	})

	if err := testHandler.linkExternalIdentity(ctx, providerBitrix, "btx-link-777", testUserID); err != nil {
		t.Fatalf("link: %v", err)
	}
	got, err := testHandler.userIDByExternalIdentity(ctx, providerBitrix, "btx-link-777")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got != testUserID {
		t.Fatalf("expected user %s, got %s", testUserID, got)
	}

	// upsert: re-linking the same external id is idempotent (no error).
	if err := testHandler.linkExternalIdentity(ctx, providerBitrix, "btx-link-777", testUserID); err != nil {
		t.Fatalf("relink: %v", err)
	}

	// missing mapping returns "" not an error.
	missing, err := testHandler.userIDByExternalIdentity(ctx, providerBitrix, "no-such-id")
	if err != nil {
		t.Fatalf("lookup missing: %v", err)
	}
	if missing != "" {
		t.Fatalf("expected empty for missing, got %s", missing)
	}
}

// TestExternalIdentityNoSteal: a (provider, external_id) already bound to one
// user cannot be re-linked (stolen) by a different user. Same-user re-link stays
// idempotent (nil error); a different-user re-link returns the sentinel and
// leaves the mapping pointing at the original owner. Regression for the
// ON CONFLICT DO UPDATE identity-steal hole.
func TestExternalIdentityNoSteal(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ctx := context.Background()
	const extID = "btx-steal-555"

	// Create a second distinct user to play the attacker role.
	var otherUserID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		"Steal Test Other", "steal-other@telegram.local").Scan(&otherUserID); err != nil {
		t.Fatalf("create other user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM user_external_identity WHERE external_id = $1`, extID)
		testPool.Exec(ctx, `DELETE FROM "user" WHERE id = $1`, otherUserID)
	})

	// Owner links the identity.
	if err := testHandler.linkExternalIdentity(ctx, providerBitrix, extID, testUserID); err != nil {
		t.Fatalf("owner link: %v", err)
	}

	// Same-user re-link is idempotent (no error).
	if err := testHandler.linkExternalIdentity(ctx, providerBitrix, extID, testUserID); err != nil {
		t.Fatalf("owner re-link should be idempotent, got: %v", err)
	}

	// Different user tries to steal -> sentinel error, mapping unchanged.
	err := testHandler.linkExternalIdentity(ctx, providerBitrix, extID, otherUserID)
	if !errors.Is(err, errExternalIdentityClaimed) {
		t.Fatalf("steal attempt: got err %v, want errExternalIdentityClaimed", err)
	}
	got, lookupErr := testHandler.userIDByExternalIdentity(ctx, providerBitrix, extID)
	if lookupErr != nil {
		t.Fatalf("lookup after steal: %v", lookupErr)
	}
	if got != testUserID {
		t.Fatalf("mapping changed after steal attempt: got %s, want original owner %s", got, testUserID)
	}
}

// TestLinkBitrixIdentityEndpointConflict: the HTTP endpoint translates a steal
// attempt into 409 and leaves the mapping intact, while the original owner's
// re-link still returns 200.
func TestLinkBitrixIdentityEndpointConflict(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	ctx := context.Background()
	const extID = "ep-conflict-321"

	var otherUserID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		"Conflict Test Other", "conflict-other@telegram.local").Scan(&otherUserID); err != nil {
		t.Fatalf("create other user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM user_external_identity WHERE external_id = $1`, extID)
		testPool.Exec(ctx, `DELETE FROM "user" WHERE id = $1`, otherUserID)
	})

	// Owner links via endpoint -> 200.
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/me/links/bitrix", strings.NewReader(`{"bitrix_user_id":"`+extID+`"}`))
	req.Header.Set("X-User-ID", testUserID)
	testHandler.LinkBitrixIdentity(w, req)
	if w.Code != 200 {
		t.Fatalf("owner link expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Owner re-links -> still 200 (idempotent).
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/api/me/links/bitrix", strings.NewReader(`{"bitrix_user_id":"`+extID+`"}`))
	req2.Header.Set("X-User-ID", testUserID)
	testHandler.LinkBitrixIdentity(w2, req2)
	if w2.Code != 200 {
		t.Fatalf("owner re-link expected 200, got %d: %s", w2.Code, w2.Body.String())
	}

	// Different user tries to claim the same id -> 409.
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("POST", "/api/me/links/bitrix", strings.NewReader(`{"bitrix_user_id":"`+extID+`"}`))
	req3.Header.Set("X-User-ID", otherUserID)
	testHandler.LinkBitrixIdentity(w3, req3)
	if w3.Code != http.StatusConflict {
		t.Fatalf("steal attempt expected 409, got %d: %s", w3.Code, w3.Body.String())
	}

	// Mapping still points at the original owner.
	got, err := testHandler.userIDByExternalIdentity(ctx, providerBitrix, extID)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got != testUserID {
		t.Fatalf("mapping changed after 409: got %s, want %s", got, testUserID)
	}
}

func TestLinkBitrixIdentityEndpoint(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM user_external_identity WHERE external_id = $1`, "ep-999")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/me/links/bitrix", strings.NewReader(`{"bitrix_user_id":"ep-999"}`))
	req.Header.Set("X-User-ID", testUserID)
	testHandler.LinkBitrixIdentity(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	gotUser, err := testHandler.userIDByExternalIdentity(ctx, providerBitrix, "ep-999")
	if err != nil || gotUser != testUserID {
		t.Fatalf("expected mapping to %s, got %s err=%v", testUserID, gotUser, err)
	}

	// ListMyLinks surfaces the new mapping.
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/me/links", nil)
	req2.Header.Set("X-User-ID", testUserID)
	testHandler.ListMyLinks(w2, req2)
	if w2.Code != 200 {
		t.Fatalf("list expected 200, got %d: %s", w2.Code, w2.Body.String())
	}
	var resp struct {
		Links []externalLinkResponse `json:"links"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, l := range resp.Links {
		if l.Provider == providerBitrix && l.ExternalID == "ep-999" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected bitrix:ep-999 in links, got %+v", resp.Links)
	}
}

func TestLinkBitrixIdentityGuards(t *testing.T) {
	// 401 without X-User-ID (unauthenticated).
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/me/links/bitrix", strings.NewReader(`{"bitrix_user_id":"123"}`))
	testHandler.LinkBitrixIdentity(w, req)
	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	// 400 on blank bitrix_user_id.
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/api/me/links/bitrix", strings.NewReader(`{"bitrix_user_id":"   "}`))
	req2.Header.Set("X-User-ID", testUserID)
	testHandler.LinkBitrixIdentity(w2, req2)
	if w2.Code != 400 {
		t.Fatalf("expected 400, got %d", w2.Code)
	}
}
