package handler

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// TestAcceptInvitation_BitrixPinned covers the Bitrix-pinned accept path (the
// fix for "invitation was issued to a different account"): a user whose email
// differs from the invite can still accept when their Bitrix link matches the
// pinned id — or when they carry no Bitrix link yet (fresh account, linked on
// accept) — but a DIFFERENT Bitrix link is rejected.
func TestAcceptInvitation_BitrixPinned(t *testing.T) {
	if testPool == nil {
		t.Skip("no database")
	}
	ctx := context.Background()

	seedUser := func(bitrixID string) string {
		var uid string
		if err := testPool.QueryRow(ctx,
			`INSERT INTO "user" (name, email) VALUES ('bx', $1) RETURNING id`,
			"bx-"+uuid.NewString()[:10]+"@x.dev").Scan(&uid); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		if bitrixID != "" {
			if _, err := testPool.Exec(ctx,
				`INSERT INTO user_external_identity (provider, external_id, user_id) VALUES ('bitrix', $1, $2::uuid)`,
				bitrixID, uid); err != nil {
				t.Fatalf("seed bitrix link: %v", err)
			}
		}
		return uid
	}
	seedInvite := func(pin string) string {
		var iid string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO workspace_invitation
			  (workspace_id, inviter_id, invitee_email, role, status, invitee_bitrix_id, expires_at)
			VALUES ($1::uuid, $2::uuid, $3, 'member', 'pending', $4, now()+interval '1 day')
			RETURNING id`,
			testWorkspaceID, testUserID, "invited-"+uuid.NewString()[:8]+"@x.dev", pin).Scan(&iid); err != nil {
			t.Fatalf("seed invite: %v", err)
		}
		return iid
	}
	accept := func(userID, invID string) int {
		req := newRequestAs(userID, "POST", "/api/invitations/"+invID+"/accept", nil)
		req = withURLParam(req, "id", invID)
		w := httptest.NewRecorder()
		testHandler.AcceptInvitation(w, req)
		return w.Code
	}
	isMember := func(userID string) bool {
		var n int
		testPool.QueryRow(ctx,
			`SELECT count(*) FROM member WHERE user_id=$1::uuid AND workspace_id=$2::uuid`,
			userID, testWorkspaceID).Scan(&n)
		return n > 0
	}

	// 1) Fresh account (no Bitrix link), email differs → allowed; link established.
	p1 := "pin-" + uuid.NewString()[:8]
	u1 := seedUser("")
	if code := accept(u1, seedInvite(p1)); code != 200 {
		t.Fatalf("fresh bitrix-pinned accept: want 200, got %d", code)
	}
	if !isMember(u1) {
		t.Fatalf("u1 should be a member after accept")
	}
	var linked string
	testPool.QueryRow(ctx,
		`SELECT external_id FROM user_external_identity WHERE provider='bitrix' AND user_id=$1::uuid`,
		u1).Scan(&linked)
	if linked != p1 {
		t.Fatalf("u1 should be linked to %s on accept, got %q", p1, linked)
	}

	// 2) Verified match: user already carries the pinned Bitrix link, email differs → allowed.
	p2 := "pin-" + uuid.NewString()[:8]
	u2 := seedUser(p2)
	if code := accept(u2, seedInvite(p2)); code != 200 {
		t.Fatalf("verified bitrix-match accept: want 200, got %d", code)
	}
	if !isMember(u2) {
		t.Fatalf("u2 should be a member after accept")
	}

	// 3) Different Bitrix link → rejected (someone else's identity).
	p3 := "pin-" + uuid.NewString()[:8]
	u3 := seedUser("other-" + uuid.NewString()[:8])
	if code := accept(u3, seedInvite(p3)); code != 403 {
		t.Fatalf("different bitrix link accept: want 403, got %d", code)
	}
	if isMember(u3) {
		t.Fatalf("u3 should NOT be a member after a rejected accept")
	}
}
