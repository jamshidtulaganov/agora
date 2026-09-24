package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Not a reserved TLD: .test addresses are treated as undeliverable and skipped.
// Nothing is really sent — the test EmailService has no relay configured.
const welcomeDomain = "welcome-mig.agora-example.com"

// welcomeFixture: one migrated workspace the test user owns with three other
// members — one who never signed in, one who already did (a used login
// code), one already welcomed — plus a migrated workspace the test user does
// NOT own, whose member must never be mailed.
func welcomeFixture(t *testing.T) (neverSignedIn, signedIn, welcomed, stranger string) {
	t.Helper()
	ctx := context.Background()
	neverSignedIn = "new@" + welcomeDomain
	signedIn = "active@" + welcomeDomain
	welcomed = "done@" + welcomeDomain
	stranger = "other@" + welcomeDomain

	clean := func() {
		testPool.Exec(ctx, `DELETE FROM workspace WHERE settings->>'zoho_project_id' IN ('w-1','w-2')`)
		testPool.Exec(ctx, `DELETE FROM verification_code WHERE email LIKE '%@`+welcomeDomain+`'`)
		testPool.Exec(ctx, `DELETE FROM "user" WHERE email LIKE '%@`+welcomeDomain+`'`)
	}
	clean()
	t.Cleanup(clean)

	var owned, foreign string
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(testPool.QueryRow(ctx, `INSERT INTO workspace (name, slug, issue_prefix, settings)
		VALUES ('Finance Department', 'welcome-finance', 'FIN', '{"zoho_project_id":"w-1"}') RETURNING id`).Scan(&owned))
	must(testPool.QueryRow(ctx, `INSERT INTO workspace (name, slug, issue_prefix, settings)
		VALUES ('Someone Else', 'welcome-else', 'ELS', '{"zoho_project_id":"w-2"}') RETURNING id`).Scan(&foreign))
	_, err := testPool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, owned, testUserID)
	must(err)

	addUser := func(email, name, ws, role string, welcomedAlready bool) {
		var id string
		must(testPool.QueryRow(ctx, `INSERT INTO "user" (name, email, onboarded_at, welcome_sent_at)
			VALUES ($1, $2, now(), CASE WHEN $3 THEN now() END) RETURNING id`, name, email, welcomedAlready).Scan(&id))
		_, err := testPool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, $3)`, ws, id, role)
		must(err)
	}
	addUser(neverSignedIn, "Nora New", owned, "member", false)
	addUser(signedIn, "Ada Active", owned, "admin", false)
	addUser(welcomed, "Dan Done", owned, "member", true)
	addUser(stranger, "Olga Other", foreign, "member", false)
	_, err = testPool.Exec(ctx, `INSERT INTO verification_code (email, code, expires_at, used)
		VALUES ($1, '123456', now() + interval '10 minutes', true)`, signedIn)
	must(err)
	return
}

func runWelcome(t *testing.T, body map[string]any) (int, ZohoWelcomeResponse) {
	t.Helper()
	w := httptest.NewRecorder()
	testHandler.SendZohoWelcomeEmails(w, newRequest("POST", "/api/zoho-projects/welcome-emails", body))
	var resp ZohoWelcomeResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v (%s)", err, w.Body.String())
		}
	}
	return w.Code, resp
}

func recipient(resp ZohoWelcomeResponse, email string) *ZohoWelcomeRecipient {
	for i := range resp.Recipients {
		if resp.Recipients[i].Email == email {
			return &resp.Recipients[i]
		}
	}
	return nil
}

func userFlags(t *testing.T, email string) (onboarded, welcomed bool) {
	t.Helper()
	if err := testPool.QueryRow(context.Background(),
		`SELECT onboarded_at IS NOT NULL, welcome_sent_at IS NOT NULL FROM "user" WHERE email = $1`, email,
	).Scan(&onboarded, &welcomed); err != nil {
		t.Fatalf("flags for %s: %v", email, err)
	}
	return
}

func TestZohoWelcomeDisabledByDefault(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	t.Setenv("AGORA_ZOHO_MIGRATE", "")
	if code, _ := runWelcome(t, map[string]any{"dry_run": true}); code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", code)
	}
}

func TestZohoWelcomeEmails(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	t.Setenv("AGORA_ZOHO_MIGRATE", "1")
	neverSignedIn, signedIn, welcomed, stranger := welcomeFixture(t)

	// Dry run: plans the caller's workspace only, sends and changes nothing.
	code, plan := runWelcome(t, map[string]any{"dry_run": true})
	if code != http.StatusOK {
		t.Fatalf("dry run code = %d", code)
	}
	if recipient(plan, stranger) != nil {
		t.Error("a member of a workspace the caller does not own must never be listed")
	}
	nora := recipient(plan, neverSignedIn)
	if nora == nil || len(nora.Workspaces) != 1 || nora.Workspaces[0] != "Finance Department" || nora.Sent {
		t.Fatalf("plan for %s = %+v", neverSignedIn, nora)
	}
	if dan := recipient(plan, welcomed); dan == nil || dan.Skipped != "already welcomed" {
		t.Errorf("already-welcomed member = %+v", dan)
	}
	if plan.Totals.ToSend != 2 || plan.Totals.Sent != 0 {
		t.Errorf("dry-run totals = %+v", plan.Totals)
	}
	if _, w := userFlags(t, neverSignedIn); w {
		t.Fatal("dry run must not mark anyone welcomed")
	}

	// A test send to one address touches only that person.
	code, one := runWelcome(t, map[string]any{"emails": []string{"NEW@" + welcomeDomain}})
	if code != http.StatusOK || len(one.Recipients) != 1 || !one.Recipients[0].Sent || !one.Recipients[0].SetupReset {
		t.Fatalf("test send = %d %+v", code, one.Recipients)
	}
	if onboarded, w := userFlags(t, neverSignedIn); onboarded || !w {
		t.Errorf("%s: onboarded=%v welcomed=%v, want setup pending + welcomed", neverSignedIn, onboarded, w)
	}
	if _, w := userFlags(t, signedIn); w {
		t.Error("the test send must not reach anyone else")
	}

	// Everyone else: the signed-in member is welcomed but keeps their state.
	code, all := runWelcome(t, map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("send all code = %d", code)
	}
	if ada := recipient(all, signedIn); ada == nil || !ada.Sent || ada.SetupReset {
		t.Errorf("signed-in member = %+v, want sent without a setup reset", ada)
	}
	if onboarded, _ := userFlags(t, signedIn); !onboarded {
		t.Error("someone who already signs in must stay onboarded")
	}
	if n := recipient(all, neverSignedIn); n == nil || n.Skipped != "already welcomed" || n.Sent {
		t.Errorf("re-run must skip the already-welcomed test recipient, got %+v", n)
	}
	if all.Totals.Sent != 1 {
		t.Errorf("send-all totals = %+v, want exactly one new send", all.Totals)
	}
}
