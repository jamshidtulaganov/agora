package handler

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// TestAcceptInvitation_TelegramOnly covers the Telegram-only accept path (the
// fix for "invitation was issued to a different account" on deployments with
// AGORA_TELEGRAM_ONLY): every account's email there is the synthetic
// tg<id>@telegram.local, so a real-email invite can never be matched by email.
// In that mode an unpinned invite is claimable by a Telegram-synthetic account
// (link-bearer trust), while an invite pinned to a DIFFERENT existing account
// stays rejected, and the exception is inert when the flag is off or the
// accepter is not a Telegram account.
func TestAcceptInvitation_TelegramOnly(t *testing.T) {
	if testPool == nil {
		t.Skip("no database")
	}
	ctx := context.Background()

	seedUser := func(email string) string {
		var uid string
		if err := testPool.QueryRow(ctx,
			`INSERT INTO "user" (name, email) VALUES ('tg', $1) RETURNING id`,
			email).Scan(&uid); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		return uid
	}
	seedTgUser := func() string {
		return seedUser("tg" + uuid.NewString()[:8] + "@telegram.local")
	}
	seedInvite := func(pinUserID string) string {
		var iid string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO workspace_invitation
			  (workspace_id, inviter_id, invitee_email, role, status, invitee_user_id, expires_at)
			VALUES ($1::uuid, $2::uuid, $3, 'member', 'pending', NULLIF($4,'')::uuid, now()+interval '1 day')
			RETURNING id`,
			testWorkspaceID, testUserID, "invited-"+uuid.NewString()[:8]+"@salesdoc.io", pinUserID).Scan(&iid); err != nil {
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
	decline := func(userID, invID string) int {
		req := newRequestAs(userID, "POST", "/api/invitations/"+invID+"/decline", nil)
		req = withURLParam(req, "id", invID)
		w := httptest.NewRecorder()
		testHandler.DeclineInvitation(w, req)
		return w.Code
	}
	isMember := func(userID string) bool {
		var n int
		testPool.QueryRow(ctx,
			`SELECT count(*) FROM member WHERE user_id=$1::uuid AND workspace_id=$2::uuid`,
			userID, testWorkspaceID).Scan(&n)
		return n > 0
	}

	// 1) Flag ON: unpinned real-email invite is claimable by a Telegram account.
	t.Setenv("AGORA_TELEGRAM_ONLY", "true")
	u1 := seedTgUser()
	if code := accept(u1, seedInvite("")); code != 200 {
		t.Fatalf("telegram-only unpinned accept: want 200, got %d", code)
	}
	if !isMember(u1) {
		t.Fatalf("u1 should be a member after accept")
	}

	// 2) Flag ON: invite pinned to a DIFFERENT existing account stays rejected.
	other := seedTgUser()
	u2 := seedTgUser()
	if code := accept(u2, seedInvite(other)); code != 403 {
		t.Fatalf("telegram-only pinned-to-other accept: want 403, got %d", code)
	}
	if isMember(u2) {
		t.Fatalf("u2 should NOT be a member after a rejected accept")
	}

	// 3) Flag ON: a NON-telegram account still cannot claim someone else's
	// real-email invite (exception keys on the accepter's synthetic identity).
	u3 := seedUser("someone-" + uuid.NewString()[:8] + "@x.dev")
	if code := accept(u3, seedInvite("")); code != 403 {
		t.Fatalf("telegram-only non-tg accepter: want 403, got %d", code)
	}

	// 4) Flag ON: the intended invitee can DECLINE too (mirrored exception).
	u4 := seedTgUser()
	if code := decline(u4, seedInvite("")); code != 204 {
		t.Fatalf("telegram-only unpinned decline: want 204, got %d", code)
	}

	// 5) Flag OFF: behavior reverts — synthetic accepter is rejected as before.
	t.Setenv("AGORA_TELEGRAM_ONLY", "false")
	u5 := seedTgUser()
	if code := accept(u5, seedInvite("")); code != 403 {
		t.Fatalf("flag-off accept: want 403, got %d", code)
	}
	if isMember(u5) {
		t.Fatalf("u5 should NOT be a member after a rejected accept")
	}
}
