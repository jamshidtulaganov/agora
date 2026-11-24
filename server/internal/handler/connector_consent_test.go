package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/auth"
	"github.com/jamshidtulaganov/agora/server/internal/connectauth"
	"github.com/jamshidtulaganov/agora/server/internal/util/secretbox"
)

func TestConnectorConsent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	const ret = "https://mcp.example/authorize/callback?client_id=agora-claude&state=s"
	key := make([]byte, 32)
	box, _ := secretbox.New(key)
	sealed, err := connectauth.SealRequest(box, connectauth.Request{ClientID: "agora-claude", ClientName: "Claude", Return: ret, Binding: "browser"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	get := func(request string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		testHandler.GetConnectorConsent(rec, newRequest("GET", "/api/connector-consent?"+url.Values{"request": {request}}.Encode(), nil))
		return rec
	}

	t.Setenv(connectauth.KeyEnv, "")
	if rec := get(sealed); rec.Code != http.StatusNotFound {
		t.Fatalf("without the shared key: %d", rec.Code)
	}
	t.Setenv(connectauth.KeyEnv, base64.StdEncoding.EncodeToString(key))

	rec := get(sealed)
	var info ConnectorConsentResponse
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &info) != nil || info.ClientName != "Claude" || info.CancelURL != ret+"&error=access_denied" {
		t.Fatalf("consent info: %d %s", rec.Code, rec.Body)
	}
	if rec := get("garbage"); rec.Code != http.StatusBadRequest {
		t.Fatalf("garbage request: %d", rec.Code)
	}

	approve := httptest.NewRecorder()
	testHandler.ApproveConnectorConsent(approve, newRequest("POST", "/api/connector-consent", map[string]string{"request": sealed}))
	var out ConnectorApproveResponse
	if approve.Code != 200 || json.Unmarshal(approve.Body.Bytes(), &out) != nil || !strings.HasPrefix(out.RedirectURL, ret+"&reply=") {
		t.Fatalf("approve: %d %s", approve.Code, approve.Body)
	}
	u, _ := url.Parse(out.RedirectURL)
	reply, err := connectauth.OpenReply(box, u.Query().Get("reply"), time.Now())
	if err != nil || reply.Binding != "browser" || !strings.HasPrefix(reply.Token, "mul_") {
		t.Fatalf("reply: %+v %v", reply, err)
	}
	if strings.Contains(approve.Body.String(), reply.Token) {
		t.Fatal("token visible in the response body")
	}
	pat, err := testHandler.Queries.GetPersonalAccessTokenByHash(context.Background(), auth.HashToken(reply.Token))
	if err != nil {
		t.Fatalf("minted token not stored: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM personal_access_token WHERE id = $1`, pat.ID)
	})
	if pat.Name != "Claude connector" || uuidToString(pat.UserID) != testUserID || !pat.ExpiresAt.Valid || pat.ExpiresAt.Time.Before(time.Now().Add(89*24*time.Hour)) {
		t.Fatalf("token row: name=%q expires=%v", pat.Name, pat.ExpiresAt.Time)
	}
}
