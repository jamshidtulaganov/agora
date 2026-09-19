package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/util/secretbox"
)

// The plaintext bot token the fake Slack hands back. Every assertion about
// "sealed at rest" is a search for these bytes.
const slackTestBotToken = "xoxb-9999-plaintext-bot-token"

const (
	slackTestTeamID    = "T0TESTTEAM"
	slackTestSlackUser = "U0INSTALLER"
)

// slackTestKey returns a deterministic 32-byte secretbox key, base64 encoded
// the way LoadKey expects it.
func slackTestKey() string {
	key := make([]byte, secretbox.KeySize)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return base64.StdEncoding.EncodeToString(key)
}

// configureSlack sets the four keys that make SlackEnabled() true, plus the
// public origin the redirect_uri is built from. t.Setenv restores everything
// when the test ends, so a later test still sees an unconfigured deployment.
func configureSlack(t *testing.T) {
	t.Helper()
	t.Setenv("AGORA_SLACK_SECRET_KEY", slackTestKey())
	t.Setenv("AGORA_SLACK_CLIENT_ID", "123.456")
	t.Setenv("AGORA_SLACK_CLIENT_SECRET", "client-secret")
	t.Setenv("AGORA_SLACK_SIGNING_SECRET", "signing-secret")
	t.Setenv("AGORA_PUBLIC_URL", "https://api.agora.test")
	t.Setenv("FRONTEND_ORIGIN", "https://app.agora.test")
}

// fakeSlack stands in for slack.com: the two calls the install flow makes.
func fakeSlack(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth.v2.access":
			io.WriteString(w, `{
				"ok": true,
				"access_token": "`+slackTestBotToken+`",
				"token_type": "bot",
				"scope": "chat:write,links:read",
				"bot_user_id": "U0BOT",
				"app_id": "A0APP",
				"team": {"id": "`+slackTestTeamID+`", "name": "Agora Test"},
				"enterprise": null,
				"authed_user": {"id": "`+slackTestSlackUser+`", "access_token": "xoxp-discarded"}
			}`)
		case "/auth.test":
			io.WriteString(w, `{"ok":true,"team_id":"`+slackTestTeamID+`","user_id":"U0BOT","bot_id":"B0BOT"}`)
		default:
			t.Errorf("unexpected slack call: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("AGORA_SLACK_API_BASE_URL", srv.URL)
}

// cleanupSlackRows removes what a test wrote. Installation rows would
// otherwise block the shared fixture teardown (installer_user_id is RESTRICT
// on "user").
func cleanupSlackRows(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = testPool.Exec(ctx, `DELETE FROM slack_installation WHERE team_id LIKE 'T0%'`)
		_, _ = testPool.Exec(ctx, `DELETE FROM user_external_identity WHERE provider = 'slack'`)
	})
}

func TestSlackEnabled_RequiresAllFourKeys(t *testing.T) {
	// A half-configured deployment must report false: an install button that
	// dies at the exchange costs an admin a real Slack consent grant.
	t.Setenv("AGORA_SLACK_SECRET_KEY", "")
	t.Setenv("AGORA_SLACK_CLIENT_ID", "")
	t.Setenv("AGORA_SLACK_CLIENT_SECRET", "")
	t.Setenv("AGORA_SLACK_SIGNING_SECRET", "")
	if slackEnabled() {
		t.Fatal("expected disabled with no keys")
	}

	t.Setenv("AGORA_SLACK_SECRET_KEY", slackTestKey())
	if slackEnabled() {
		t.Fatal("seal key alone must not enable slack")
	}
	t.Setenv("AGORA_SLACK_CLIENT_ID", "123.456")
	t.Setenv("AGORA_SLACK_CLIENT_SECRET", "client-secret")
	if slackEnabled() {
		t.Fatal("a missing signing secret must keep slack disabled")
	}
	t.Setenv("AGORA_SLACK_SIGNING_SECRET", "signing-secret")
	if !slackEnabled() {
		t.Fatal("expected enabled with all four keys")
	}
}

func TestBeginSlackInstall_NotConfiguredReturns503(t *testing.T) {
	t.Setenv("AGORA_SLACK_SECRET_KEY", "")
	t.Setenv("AGORA_SLACK_CLIENT_ID", "")
	t.Setenv("AGORA_SLACK_CLIENT_SECRET", "")
	t.Setenv("AGORA_SLACK_SIGNING_SECRET", "")

	req := withURLParam(newRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/slack/install/begin", nil), "id", testWorkspaceID)
	w := httptest.NewRecorder()
	testHandler.BeginSlackInstall(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

// The seal key alone being absent is the case that matters most: without it a
// successful OAuth would have nowhere safe to put the token, so the flow must
// refuse to start rather than fall back to plaintext.
func TestBeginSlackInstall_MissingSealKeyReturns503(t *testing.T) {
	configureSlack(t)
	t.Setenv("AGORA_SLACK_SECRET_KEY", "")

	req := withURLParam(newRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/slack/install/begin", nil), "id", testWorkspaceID)
	w := httptest.NewRecorder()
	testHandler.BeginSlackInstall(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestBeginSlackInstall_ReturnsAuthorizeURLWithSealedState(t *testing.T) {
	configureSlack(t)

	req := withURLParam(newRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/slack/install/begin", nil), "id", testWorkspaceID)
	w := httptest.NewRecorder()
	testHandler.BeginSlackInstall(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp SlackInstallBeginResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	u, err := url.Parse(resp.AuthorizeURL)
	if err != nil {
		t.Fatalf("parse authorize_url: %v", err)
	}
	if u.Host != "slack.com" {
		t.Fatalf("authorize host = %q", u.Host)
	}
	if got := u.Query().Get("redirect_uri"); got != "https://api.agora.test/slack/oauth/callback" {
		t.Fatalf("redirect_uri = %q", got)
	}
	state := u.Query().Get("state")
	if state == "" {
		t.Fatal("no state on the authorize url")
	}
	// The state is opaque to Slack and to the browser, but must open here.
	box, err := slackSealBox()
	if err != nil {
		t.Fatalf("seal box: %v", err)
	}
	opened, err := openSlackState(box, state)
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if opened.WorkspaceID != testWorkspaceID || opened.UserID != testUserID {
		t.Fatalf("state = %+v", opened)
	}
	if opened.Kind != slackStateKindInstall {
		t.Fatalf("state kind = %q", opened.Kind)
	}
	// A state minted by a different deployment must not open here.
	otherKey := make([]byte, secretbox.KeySize)
	otherBox, err := secretbox.New(otherKey)
	if err != nil {
		t.Fatalf("other box: %v", err)
	}
	if _, err := openSlackState(otherBox, state); err == nil {
		t.Fatal("a state sealed with another key must not open")
	}
}

// The install round trip: begin → Slack redirect → callback. Asserts the row
// lands, the token is SEALED (the plaintext appears nowhere in the row), the
// installer's Slack identity is bound, and the API response carries no
// credential at all.
func TestSlackOAuthCallback_InstallRoundTripStoresSealedToken(t *testing.T) {
	configureSlack(t)
	fakeSlack(t)
	cleanupSlackRows(t)

	// --- begin ---
	beginReq := withURLParam(newRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/slack/install/begin", nil), "id", testWorkspaceID)
	beginRec := httptest.NewRecorder()
	testHandler.BeginSlackInstall(beginRec, beginReq)
	if beginRec.Code != http.StatusOK {
		t.Fatalf("begin: expected 200, got %d body=%s", beginRec.Code, beginRec.Body.String())
	}
	var begin SlackInstallBeginResponse
	if err := json.Unmarshal(beginRec.Body.Bytes(), &begin); err != nil {
		t.Fatalf("decode begin: %v", err)
	}
	authorizeURL, err := url.Parse(begin.AuthorizeURL)
	if err != nil {
		t.Fatalf("parse authorize url: %v", err)
	}
	state := authorizeURL.Query().Get("state")

	// --- callback ---
	cbReq := httptest.NewRequest(http.MethodGet, "/slack/oauth/callback?code=the-code&state="+url.QueryEscape(state), nil)
	cbRec := httptest.NewRecorder()
	testHandler.SlackOAuthCallback(cbRec, cbReq)
	if cbRec.Code != http.StatusFound {
		t.Fatalf("callback: expected 302, got %d body=%s", cbRec.Code, cbRec.Body.String())
	}
	location := cbRec.Header().Get("Location")
	if !strings.Contains(location, "/"+handlerTestWorkspaceSlug+"/settings") {
		t.Fatalf("redirect location = %q", location)
	}
	if !strings.Contains(location, "slack_installed=1") {
		t.Fatalf("redirect did not report success: %q", location)
	}
	if strings.Contains(location, slackTestBotToken) {
		t.Fatalf("redirect leaked the bot token: %q", location)
	}

	// --- the row ---
	ctx := context.Background()
	var sealed []byte
	var teamID, teamName, botUserID, status, scopes string
	if err := testPool.QueryRow(ctx, `
		SELECT bot_token_encrypted, team_id, team_name, bot_user_id, status, scopes
		FROM slack_installation WHERE workspace_id = $1::uuid AND team_id = $2
	`, testWorkspaceID, slackTestTeamID).Scan(&sealed, &teamID, &teamName, &botUserID, &status, &scopes); err != nil {
		t.Fatalf("installation row: %v", err)
	}
	if status != "active" || teamName != "Agora Test" || botUserID != "U0BOT" {
		t.Fatalf("unexpected row: status=%q team=%q bot=%q", status, teamName, botUserID)
	}
	// Sealed at rest: the plaintext must not appear in the stored bytes.
	if strings.Contains(string(sealed), slackTestBotToken) {
		t.Fatal("bot token stored in plaintext")
	}
	if len(sealed) == 0 {
		t.Fatal("no ciphertext stored")
	}
	box, err := slackSealBox()
	if err != nil {
		t.Fatalf("seal box: %v", err)
	}
	plain, err := box.Open(sealed)
	if err != nil {
		t.Fatalf("open sealed token: %v", err)
	}
	if string(plain) != slackTestBotToken {
		t.Fatalf("sealed token round trip = %q", string(plain))
	}

	// --- installer identity, bound in the same transaction ---
	var linkedUser string
	if err := testPool.QueryRow(ctx,
		`SELECT user_id::text FROM user_external_identity WHERE provider = 'slack' AND external_id = $1`,
		slackTestSlackUser).Scan(&linkedUser); err != nil {
		t.Fatalf("installer identity: %v", err)
	}
	if linkedUser != testUserID {
		t.Fatalf("identity bound to %q, want %q", linkedUser, testUserID)
	}

	// --- the API surface carries no credential ---
	listReq := withURLParam(newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/slack/installations", nil), "id", testWorkspaceID)
	listRec := httptest.NewRecorder()
	testHandler.ListSlackInstallations(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", listRec.Code)
	}
	listBody := listRec.Body.String()
	if !strings.Contains(listBody, slackTestTeamID) {
		t.Fatalf("list did not include the installation: %s", listBody)
	}
	if strings.Contains(listBody, slackTestBotToken) ||
		strings.Contains(listBody, "bot_token") ||
		strings.Contains(listBody, base64.StdEncoding.EncodeToString(sealed)) {
		t.Fatalf("list leaked the credential: %s", listBody)
	}
	if !strings.Contains(listBody, `"configured":true`) {
		t.Fatalf("configured flag missing: %s", listBody)
	}

	// --- re-install rotates in place rather than accumulating rows ---
	cbReq2 := httptest.NewRequest(http.MethodGet, "/slack/oauth/callback?code=the-code&state="+url.QueryEscape(state), nil)
	cbRec2 := httptest.NewRecorder()
	testHandler.SlackOAuthCallback(cbRec2, cbReq2)
	if cbRec2.Code != http.StatusFound {
		t.Fatalf("re-install: expected 302, got %d", cbRec2.Code)
	}
	var count int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM slack_installation WHERE workspace_id = $1::uuid AND team_id = $2`,
		testWorkspaceID, slackTestTeamID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("re-install created %d rows, want 1", count)
	}
}

func TestSlackOAuthCallback_RejectsForgedState(t *testing.T) {
	configureSlack(t)
	fakeSlack(t)
	cleanupSlackRows(t)

	tests := []struct {
		name  string
		query string
		want  string
	}{
		{"forged state", "?code=c&state=not-a-sealed-state", "invalid_state"},
		{"missing state", "?code=c", "missing_params"},
		{"missing code", "?state=x", "missing_params"},
		{"user cancelled", "?error=access_denied", "access_denied"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/slack/oauth/callback"+tc.query, nil)
			rec := httptest.NewRecorder()
			testHandler.SlackOAuthCallback(rec, req)
			if rec.Code != http.StatusFound {
				t.Fatalf("expected a redirect, got %d", rec.Code)
			}
			if got := rec.Header().Get("Location"); !strings.Contains(got, "slack_error="+tc.want) {
				t.Fatalf("location = %q, want slack_error=%s", got, tc.want)
			}
		})
	}

	var count int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM slack_installation WHERE team_id = $1`, slackTestTeamID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("a rejected callback wrote %d installation rows", count)
	}
}

// Workspace fencing: an installation belonging to workspace A must be
// invisible — and un-revokable — from workspace B, even with its UUID in hand.
func TestSlackInstallation_WorkspaceFencing(t *testing.T) {
	configureSlack(t)
	fakeSlack(t)
	cleanupSlackRows(t)
	ctx := context.Background()

	// Workspace B, a tenant that must never see workspace A's install.
	var otherWorkspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ('Slack Fence', 'slack-fence-tests', '', 'SFT')
		RETURNING id::text
	`).Scan(&otherWorkspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE slug = 'slack-fence-tests'`)
	})
	if _, err := testPool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'owner')`,
		otherWorkspaceID, testUserID); err != nil {
		t.Fatalf("create member: %v", err)
	}

	// Install into workspace A.
	beginReq := withURLParam(newRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/slack/install/begin", nil), "id", testWorkspaceID)
	beginRec := httptest.NewRecorder()
	testHandler.BeginSlackInstall(beginRec, beginReq)
	var begin SlackInstallBeginResponse
	if err := json.Unmarshal(beginRec.Body.Bytes(), &begin); err != nil {
		t.Fatalf("decode begin: %v", err)
	}
	authorizeURL, _ := url.Parse(begin.AuthorizeURL)
	cbReq := httptest.NewRequest(http.MethodGet, "/slack/oauth/callback?code=c&state="+url.QueryEscape(authorizeURL.Query().Get("state")), nil)
	cbRec := httptest.NewRecorder()
	testHandler.SlackOAuthCallback(cbRec, cbReq)
	if cbRec.Code != http.StatusFound {
		t.Fatalf("install: expected 302, got %d", cbRec.Code)
	}

	var installationID string
	if err := testPool.QueryRow(ctx,
		`SELECT id::text FROM slack_installation WHERE workspace_id = $1::uuid`, testWorkspaceID).Scan(&installationID); err != nil {
		t.Fatalf("installation id: %v", err)
	}

	// Workspace B's listing must be empty.
	listReq := withURLParam(newRequest(http.MethodGet, "/api/workspaces/"+otherWorkspaceID+"/slack/installations", nil), "id", otherWorkspaceID)
	listRec := httptest.NewRecorder()
	testHandler.ListSlackInstallations(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", listRec.Code)
	}
	var listed struct {
		Installations []SlackInstallationResponse `json:"installations"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Installations) != 0 {
		t.Fatalf("workspace B sees %d installations of workspace A", len(listed.Installations))
	}

	// Revoking workspace A's installation from workspace B is a 404, not a
	// revoke — the write is workspace-scoped at the query level.
	revokeReq := withURLParams(
		newRequest(http.MethodDelete, "/api/workspaces/"+otherWorkspaceID+"/slack/installations/"+installationID, nil),
		"id", otherWorkspaceID, "installationId", installationID)
	revokeRec := httptest.NewRecorder()
	testHandler.RevokeSlackInstallation(revokeRec, revokeReq)
	if revokeRec.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace revoke: expected 404, got %d", revokeRec.Code)
	}
	var status string
	if err := testPool.QueryRow(ctx,
		`SELECT status FROM slack_installation WHERE id = $1::uuid`, installationID).Scan(&status); err != nil {
		t.Fatalf("status: %v", err)
	}
	if status != "active" {
		t.Fatalf("cross-workspace revoke changed status to %q", status)
	}

	// The owning workspace can revoke.
	okRevoke := withURLParams(
		newRequest(http.MethodDelete, "/api/workspaces/"+testWorkspaceID+"/slack/installations/"+installationID, nil),
		"id", testWorkspaceID, "installationId", installationID)
	okRec := httptest.NewRecorder()
	testHandler.RevokeSlackInstallation(okRec, okRevoke)
	if okRec.Code != http.StatusNoContent {
		t.Fatalf("revoke: expected 204, got %d body=%s", okRec.Code, okRec.Body.String())
	}
	if err := testPool.QueryRow(ctx,
		`SELECT status FROM slack_installation WHERE id = $1::uuid`, installationID).Scan(&status); err != nil {
		t.Fatalf("status: %v", err)
	}
	if status != "revoked" {
		t.Fatalf("status after revoke = %q", status)
	}
}

func TestListSlackInstallations_UnconfiguredStillRenders(t *testing.T) {
	// The Integrations tab must render for a deployment with no Slack keys:
	// an empty list plus configured:false, never an error.
	t.Setenv("AGORA_SLACK_SECRET_KEY", "")
	t.Setenv("AGORA_SLACK_CLIENT_ID", "")
	t.Setenv("AGORA_SLACK_CLIENT_SECRET", "")
	t.Setenv("AGORA_SLACK_SIGNING_SECRET", "")

	req := withURLParam(newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/slack/installations", nil), "id", testWorkspaceID)
	rec := httptest.NewRecorder()
	testHandler.ListSlackInstallations(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"configured":false`) {
		t.Fatalf("expected configured:false, got %s", rec.Body.String())
	}
}

func TestSlackInstallationResponse_HasNoTokenField(t *testing.T) {
	// Belt and braces against a future field: the wire struct must never gain
	// anything token-shaped, not even masked — a partial token narrows a search.
	raw, err := json.Marshal(SlackInstallationResponse{TeamID: "T1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, forbidden := range []string{"token", "secret", "encrypted"} {
		if strings.Contains(strings.ToLower(string(raw)), forbidden) {
			t.Fatalf("response shape contains %q: %s", forbidden, raw)
		}
	}
}
