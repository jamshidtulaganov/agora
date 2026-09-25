package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A merged-away address (a login alias) signs in to the kept account: the
// code goes to the address the person typed, and the session is the kept
// account's — no new account is created for the alias.
func TestVerifyCodeWithLoginAliasSignsInToTheKeptAccount(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const company = "alias-keep@company.agora-example.com"
	const alias = "alias-login@gmail.agora-example.com"
	ctx := context.Background()

	var keptID string
	if err := testPool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ('Alias Kept', $1) RETURNING id::text`, company).Scan(&keptID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO user_email_alias (email, user_id) VALUES ($1, $2)`, alias, keptID); err != nil {
		t.Fatalf("create alias: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM verification_code WHERE email = $1`, alias)
		testPool.Exec(ctx, `DELETE FROM "user" WHERE email IN ($1, $2)`, company, alias)
	})

	post := func(path string, body map[string]string, h http.HandlerFunc) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		json.NewEncoder(&buf).Encode(body)
		req := httptest.NewRequest("POST", path, &buf)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h(w, req)
		return w
	}

	if w := post("/auth/send-code", map[string]string{"email": alias, "intent": "login"}, testHandler.SendCode); w.Code != http.StatusOK {
		t.Fatalf("SendCode: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	dbCode, err := testHandler.Queries.GetLatestVerificationCode(ctx, alias)
	if err != nil {
		t.Fatalf("code was not stored for the alias address: %v", err)
	}

	w := post("/auth/verify-code", map[string]string{"email": alias, "code": dbCode.Code}, testHandler.VerifyCode)
	if w.Code != http.StatusOK {
		t.Fatalf("VerifyCode: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp LoginResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.User.ID != keptID || resp.User.Email != company {
		t.Fatalf("signed in as %s <%s>, want the kept account %s <%s>", resp.User.ID, resp.User.Email, keptID, company)
	}
	var n int
	testPool.QueryRow(ctx, `SELECT count(*) FROM "user" WHERE email = $1`, alias).Scan(&n)
	if n != 0 {
		t.Fatal("an account was created for the alias address")
	}
}
