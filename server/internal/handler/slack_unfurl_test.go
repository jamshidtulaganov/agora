package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const (
	slackUnfurlBotToken = "xoxb-unfurl-plaintext"
	slackUnfurlPoster   = "U0POSTER"
	slackUnfurlChannel  = "C0CHANNEL"
	slackUnfurlTS       = "1735689600.000100"
)

// recordedUnfurl is one chat.unfurl call the fake Slack captured.
type recordedUnfurl struct {
	Channel          string                     `json:"channel"`
	TS               string                     `json:"ts"`
	Unfurls          map[string]json.RawMessage `json:"unfurls"`
	UserAuthRequired bool                       `json:"user_auth_required"`
	UserAuthURL      string                     `json:"user_auth_url"`
	Raw              string                     `json:"-"`
}

// unfurlRecorder is a fake slack.com that only knows chat.unfurl. Any other
// method is a test failure: the unfurl path must not quietly start calling
// something else.
type unfurlRecorder struct {
	mu    sync.Mutex
	calls []recordedUnfurl
}

func (rec *unfurlRecorder) snapshot() []recordedUnfurl {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	out := make([]recordedUnfurl, len(rec.calls))
	copy(out, rec.calls)
	return out
}

// fakeSlackUnfurl points the Web API client at a recorder and returns it.
func fakeSlackUnfurl(t *testing.T) *unfurlRecorder {
	t.Helper()
	rec := &unfurlRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.unfurl" {
			t.Errorf("unexpected slack call: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var call recordedUnfurl
		_ = json.Unmarshal(raw, &call)
		call.Raw = string(raw)
		rec.mu.Lock()
		rec.calls = append(rec.calls, call)
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("AGORA_SLACK_API_BASE_URL", srv.URL)
	return rec
}

// seedSlackUnfurlInstall writes an active installation for the test workspace
// whose bot token is REALLY sealed, so the unfurl path exercises the same
// unseal it will in production.
func seedSlackUnfurlInstall(t *testing.T) string {
	t.Helper()
	box, err := slackSealBox()
	if err != nil {
		t.Fatalf("seal box: %v", err)
	}
	sealed, err := box.Seal([]byte(slackUnfurlBotToken))
	if err != nil {
		t.Fatalf("seal token: %v", err)
	}
	var id string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO slack_installation (
			workspace_id, team_id, team_name, app_id, bot_user_id,
			bot_token_encrypted, scopes, installer_user_id
		) VALUES ($1::uuid, $2, 'Agora Test', 'A0APP', 'U0BOT', $3, 'chat:write,links:read,links:write', $4::uuid)
		ON CONFLICT (workspace_id, team_id) DO UPDATE SET
			status = 'active', bot_token_encrypted = EXCLUDED.bot_token_encrypted
		RETURNING id::text
	`, testWorkspaceID, slackTestTeamID, sealed, testUserID).Scan(&id); err != nil {
		t.Fatalf("seed installation: %v", err)
	}
	return id
}

// linkSlackUser binds a Slack user id to an Agora user, the way the personal
// link flow does.
func linkSlackUser(t *testing.T, slackUserID, userID string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO user_external_identity (provider, external_id, user_id)
		VALUES ('slack', $1, $2::uuid)
		ON CONFLICT (provider, external_id) DO UPDATE SET user_id = EXCLUDED.user_id
	`, slackUserID, userID); err != nil {
		t.Fatalf("link slack user: %v", err)
	}
}

// slackIssueLink builds the canonical link for an issue id, resolving its
// human identifier the same way the notification path does — so the test
// exercises the exact string a teammate would copy out of Slack.
func slackIssueLink(t *testing.T, issueID string) string {
	t.Helper()
	ctx := context.Background()
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	identifier := testHandler.issueKey(ctx, issue)
	if identifier == "" {
		t.Fatal("issue has no identifier")
	}
	return "https://app.agora.test/" + handlerTestWorkspaceSlug + "/issues/" + identifier
}

// slackLinkSharedFor builds the event a link_shared delivery carries.
func slackLinkSharedFor(urls ...string) slackLinkSharedEvent {
	links := make([]slackSharedLink, 0, len(urls))
	for _, u := range urls {
		links = append(links, slackSharedLink{URL: u, Domain: "app.agora.test"})
	}
	return slackLinkSharedEvent{
		TeamID:      slackTestTeamID,
		Channel:     slackUnfurlChannel,
		MessageTS:   slackUnfurlTS,
		SlackUserID: slackUnfurlPoster,
		Links:       links,
	}
}

// ---------------------------------------------------------------------------
// The happy path
// ---------------------------------------------------------------------------

func TestSlackUnfurl_OwnedIssueRendersACard(t *testing.T) {
	configureSlack(t)
	cleanupSlackRows(t)
	rec := fakeSlackUnfurl(t)
	seedSlackUnfurlInstall(t)
	linkSlackUser(t, slackUnfurlPoster, testUserID)

	ctx := context.Background()
	issueID := insertIssueTo(t, ctx, testWorkspaceID,
		fmt.Sprintf("unfurl owned %d", time.Now().UnixNano()), "member", testUserID)
	link := slackIssueLink(t, issueID)

	testHandler.unfurlSlackLinks(ctx, slackLinkSharedFor(link))

	calls := rec.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected exactly one chat.unfurl, got %d", len(calls))
	}
	call := calls[0]
	if call.Channel != slackUnfurlChannel || call.TS != slackUnfurlTS {
		t.Fatalf("unfurl addressed the wrong message: %+v", call)
	}
	if call.UserAuthRequired {
		t.Fatalf("a visible issue must not ask for auth: %s", call.Raw)
	}
	blocks, ok := call.Unfurls[link]
	if !ok {
		t.Fatalf("unfurls not keyed by the shared URL: %s", call.Raw)
	}
	rendered := string(blocks)
	for _, want := range []string{"HAN-", "unfurl owned", "Open in Agora", handlerTestName} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("card missing %q: %s", want, rendered)
		}
	}
	// The noise/leak posture: no description body reaches the channel.
	if strings.Contains(rendered, `"type":"markdown"`) || strings.Contains(rendered, `"type":"section"`) {
		t.Fatalf("unexpected body block in an unfurl: %s", rendered)
	}
}

// The canonical link shape carries the human identifier, but a copied deep
// link may carry the UUID. Both resolve, and both stay workspace-scoped.
func TestSlackUnfurl_IssueByUUIDAlsoResolves(t *testing.T) {
	configureSlack(t)
	cleanupSlackRows(t)
	rec := fakeSlackUnfurl(t)
	seedSlackUnfurlInstall(t)
	linkSlackUser(t, slackUnfurlPoster, testUserID)

	ctx := context.Background()
	issueID := insertIssueTo(t, ctx, testWorkspaceID,
		fmt.Sprintf("unfurl by uuid %d", time.Now().UnixNano()), "member", testUserID)
	link := "https://app.agora.test/" + handlerTestWorkspaceSlug + "/issues/" + issueID

	testHandler.unfurlSlackLinks(ctx, slackLinkSharedFor(link))

	calls := rec.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected one chat.unfurl, got %d", len(calls))
	}
	if _, ok := calls[0].Unfurls[link]; !ok {
		t.Fatalf("uuid link did not unfurl: %s", calls[0].Raw)
	}
}

// ---------------------------------------------------------------------------
// The fences
// ---------------------------------------------------------------------------

// Fence 2 — tenant. A Slack team that does not appear in the workspace's
// installation rows gets silence: not a card, and not an auth prompt either,
// because a prompt would confirm the workspace exists.
func TestSlackUnfurl_ForeignTeamIsSilent(t *testing.T) {
	configureSlack(t)
	cleanupSlackRows(t)
	rec := fakeSlackUnfurl(t)
	seedSlackUnfurlInstall(t)
	linkSlackUser(t, slackUnfurlPoster, testUserID)

	ctx := context.Background()
	issueID := insertIssueTo(t, ctx, testWorkspaceID,
		fmt.Sprintf("unfurl foreign team %d", time.Now().UnixNano()), "member", testUserID)

	ev := slackLinkSharedFor(slackIssueLink(t, issueID))
	ev.TeamID = "T0SOMEONEELSE" // a different Slack workspace entirely
	testHandler.unfurlSlackLinks(ctx, ev)

	if calls := rec.snapshot(); len(calls) != 0 {
		t.Fatalf("a foreign team must produce no slack call at all, got %+v", calls)
	}
}

// A workspace that exists but has never installed the app on this team is the
// same answer: silence.
func TestSlackUnfurl_WorkspaceWithoutAnInstallIsSilent(t *testing.T) {
	configureSlack(t)
	cleanupSlackRows(t)
	rec := fakeSlackUnfurl(t)
	seedSlackUnfurlInstall(t)
	linkSlackUser(t, slackUnfurlPoster, testUserID)

	ctx := context.Background()
	slug := fmt.Sprintf("unfurl-foreign-%d", time.Now().UnixNano())
	var otherWS string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ('Unfurl Foreign', $1, '', 'UNF') RETURNING id::text
	`, slug).Scan(&otherWS); err != nil {
		t.Fatalf("create foreign workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, otherWS)
	})
	issueID := insertIssueTo(t, ctx, otherWS,
		fmt.Sprintf("foreign ws issue %d", time.Now().UnixNano()), "member", testUserID)

	link := "https://app.agora.test/" + slug + "/issues/" + issueID
	testHandler.unfurlSlackLinks(ctx, slackLinkSharedFor(link))

	if calls := rec.snapshot(); len(calls) != 0 {
		t.Fatalf("a workspace this team has not installed must stay silent, got %+v", calls)
	}
}

// Fence 1 — host, plus every shape the parser refuses. None of them reaches
// the network.
func TestSlackUnfurl_UnrecognisedLinksAreIgnored(t *testing.T) {
	configureSlack(t)
	cleanupSlackRows(t)
	rec := fakeSlackUnfurl(t)
	seedSlackUnfurlInstall(t)
	linkSlackUser(t, slackUnfurlPoster, testUserID)

	ctx := context.Background()
	testHandler.unfurlSlackLinks(ctx, slackLinkSharedFor(
		"not a url at all",
		"https://evil.example.com/"+handlerTestWorkspaceSlug+"/issues/HAN-1", // right path, wrong host
		"https://app.agora.test/"+handlerTestWorkspaceSlug+"/settings/slack", // a section we do not unfurl
		"https://app.agora.test/inbox",                                       // a global route
		"mailto:someone@agora.dev",
	))

	if calls := rec.snapshot(); len(calls) != 0 {
		t.Fatalf("unrecognised links must produce no slack call, got %+v", calls)
	}
}

// Fence 3 — identity. A Slack user with no Agora account gets Slack's own
// private "connect your account" prompt, and the channel learns nothing.
func TestSlackUnfurl_UnlinkedPosterGetsTheAuthPrompt(t *testing.T) {
	configureSlack(t)
	cleanupSlackRows(t)
	rec := fakeSlackUnfurl(t)
	seedSlackUnfurlInstall(t)
	// Deliberately NO linkSlackUser.

	ctx := context.Background()
	title := fmt.Sprintf("unfurl secret title %d", time.Now().UnixNano())
	issueID := insertIssueTo(t, ctx, testWorkspaceID, title, "member", testUserID)

	testHandler.unfurlSlackLinks(ctx, slackLinkSharedFor(slackIssueLink(t, issueID)))

	calls := rec.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected one chat.unfurl, got %d", len(calls))
	}
	call := calls[0]
	if !call.UserAuthRequired {
		t.Fatalf("expected user_auth_required: %s", call.Raw)
	}
	if len(call.Unfurls) != 0 {
		t.Fatalf("an auth prompt must carry no preview: %s", call.Raw)
	}
	if strings.Contains(call.Raw, title) {
		t.Fatalf("the issue title leaked into an auth prompt: %s", call.Raw)
	}
	if !strings.Contains(call.UserAuthURL, "tab=notifications") {
		t.Fatalf("user_auth_url must land where connecting happens: %q", call.UserAuthURL)
	}
}

// Fence 4 — visibility. The non-owner issue-visibility gate applies to
// unfurls unchanged: a member who could not open the issue in Agora does not
// get to put its title in a channel.
func TestSlackUnfurl_HiddenIssueDoesNotLeak(t *testing.T) {
	configureSlack(t)
	cleanupSlackRows(t)
	rec := fakeSlackUnfurl(t)
	seedSlackUnfurlInstall(t)

	ctx := context.Background()
	suffix := time.Now().UnixNano()

	// A plain member of the workspace, linked to Slack.
	var restrictedID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id::text
	`, "Unfurl Restricted", fmt.Sprintf("unfurl-restricted-%d@agora.dev", suffix)).Scan(&restrictedID); err != nil {
		t.Fatalf("create restricted user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, restrictedID)
	})
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'member')
	`, testWorkspaceID, restrictedID); err != nil {
		t.Fatalf("add restricted member: %v", err)
	}
	linkSlackUser(t, slackUnfurlPoster, restrictedID)

	// An issue that belongs to the workspace OWNER, not to them.
	hiddenTitle := fmt.Sprintf("unfurl hidden %d", suffix)
	hidden := insertIssueTo(t, ctx, testWorkspaceID, hiddenTitle, "member", testUserID)
	hiddenLink := slackIssueLink(t, hidden)

	testHandler.unfurlSlackLinks(ctx, slackLinkSharedFor(hiddenLink))

	calls := rec.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected one chat.unfurl, got %d", len(calls))
	}
	if !calls[0].UserAuthRequired || len(calls[0].Unfurls) != 0 {
		t.Fatalf("a hidden issue must answer with the auth prompt: %s", calls[0].Raw)
	}
	if strings.Contains(calls[0].Raw, hiddenTitle) {
		t.Fatalf("hidden issue title leaked into the channel: %s", calls[0].Raw)
	}

	// The same member's OWN issue unfurls normally — the gate restricts, it
	// does not disable the feature.
	rec.mu.Lock()
	rec.calls = nil
	rec.mu.Unlock()

	ownTitle := fmt.Sprintf("unfurl own %d", suffix)
	own := insertIssueTo(t, ctx, testWorkspaceID, ownTitle, "member", restrictedID)
	ownLink := slackIssueLink(t, own)

	testHandler.unfurlSlackLinks(ctx, slackLinkSharedFor(ownLink))
	calls = rec.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected one chat.unfurl for the member's own issue, got %d", len(calls))
	}
	if calls[0].UserAuthRequired {
		t.Fatalf("the member's own issue must unfurl: %s", calls[0].Raw)
	}
	if !strings.Contains(calls[0].Raw, ownTitle) {
		t.Fatalf("own issue card missing its title: %s", calls[0].Raw)
	}
}

// An issue that does not exist answers like a hidden one: the auth prompt,
// never a "not found" card. Telling a channel that HAN-999999 is missing is
// itself information about the workspace.
func TestSlackUnfurl_UnknownIssueDoesNotConfirmAbsence(t *testing.T) {
	configureSlack(t)
	cleanupSlackRows(t)
	rec := fakeSlackUnfurl(t)
	seedSlackUnfurlInstall(t)
	linkSlackUser(t, slackUnfurlPoster, testUserID)

	testHandler.unfurlSlackLinks(context.Background(), slackLinkSharedFor(
		"https://app.agora.test/"+handlerTestWorkspaceSlug+"/issues/HAN-999999"))

	calls := rec.snapshot()
	if len(calls) != 1 || !calls[0].UserAuthRequired {
		t.Fatalf("expected a single auth prompt, got %+v", calls)
	}
}

// ---------------------------------------------------------------------------
// The events path
// ---------------------------------------------------------------------------

// link_shared arrives on the same signed ingress as everything else: an
// unsigned or tampered delivery is rejected before any resolution work, and
// nothing reaches Slack.
func TestSlackEvents_LinkSharedStillRequiresASignature(t *testing.T) {
	configureSlack(t)
	cleanupSlackRows(t)
	t.Setenv("AGORA_SLACK_SIGNING_SECRET", slackEventsSigningSecret)
	rec := fakeSlackUnfurl(t)
	seedSlackUnfurlInstall(t)
	linkSlackUser(t, slackUnfurlPoster, testUserID)

	ctx := context.Background()
	issueID := insertIssueTo(t, ctx, testWorkspaceID,
		fmt.Sprintf("unfurl unsigned %d", time.Now().UnixNano()), "member", testUserID)
	body := slackLinkSharedBody(slackIssueLink(t, issueID))

	unsigned := httptest.NewRequest(http.MethodPost, "/slack/events", strings.NewReader(body))
	unsigned.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	testHandler.SlackEvents(recorder, unsigned)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recorder.Code)
	}
	if calls := rec.snapshot(); len(calls) != 0 {
		t.Fatalf("an unsigned delivery must not reach slack, got %+v", calls)
	}
}

// End to end: a signed link_shared is acknowledged immediately (the three
// second budget) and the unfurl happens after, off the request path.
func TestSlackEvents_LinkSharedAcksAndThenUnfurls(t *testing.T) {
	configureSlack(t)
	cleanupSlackRows(t)
	t.Setenv("AGORA_SLACK_SIGNING_SECRET", slackEventsSigningSecret)
	rec := fakeSlackUnfurl(t)
	seedSlackUnfurlInstall(t)
	linkSlackUser(t, slackUnfurlPoster, testUserID)

	ctx := context.Background()
	issueID := insertIssueTo(t, ctx, testWorkspaceID,
		fmt.Sprintf("unfurl e2e %d", time.Now().UnixNano()), "member", testUserID)
	link := slackIssueLink(t, issueID)

	recorder := httptest.NewRecorder()
	testHandler.SlackEvents(recorder, signedSlackEvent(slackLinkSharedBody(link), time.Now()))
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if calls := rec.snapshot(); len(calls) == 1 {
			if _, ok := calls[0].Unfurls[link]; ok {
				return
			}
			t.Fatalf("unfurl did not carry the shared link: %s", calls[0].Raw)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the detached unfurl")
}

// slackLinkSharedBody builds the raw JSON Slack posts for a link_shared event.
func slackLinkSharedBody(urls ...string) string {
	links := make([]string, 0, len(urls))
	for _, u := range urls {
		raw, _ := json.Marshal(u)
		links = append(links, `{"url":`+string(raw)+`,"domain":"app.agora.test"}`)
	}
	return `{"type":"event_callback","team_id":"` + slackTestTeamID + `","event":{` +
		`"type":"link_shared","user":"` + slackUnfurlPoster + `","channel":"` + slackUnfurlChannel + `",` +
		`"message_ts":"` + slackUnfurlTS + `","links":[` + strings.Join(links, ",") + `]}}`
}

// ---------------------------------------------------------------------------
// The personal link (the other half of the unfurl's growth loop)
// ---------------------------------------------------------------------------

// fakeSlackUserExchange stands in for slack.com during the personal-link
// flow: oauth.v2.access answers with an authed_user and NO bot token, which is
// exactly what a user_scope-only consent returns.
func fakeSlackUserExchange(t *testing.T, slackUserID string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth.v2.access" {
			t.Errorf("unexpected slack call: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		io.WriteString(w, `{"ok":true,"app_id":"A0APP","authed_user":{"id":"`+slackUserID+`","scope":"users:read","access_token":"xoxp-discarded"}}`)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("AGORA_SLACK_API_BASE_URL", srv.URL)
}

// The consent screen a person sees when they connect their own account must
// NOT install the app: no bot scopes, only the identity scope.
func TestBeginSlackUserLink_RequestsUserScopeOnly(t *testing.T) {
	configureSlack(t)

	rec := httptest.NewRecorder()
	testHandler.BeginSlackUserLink(rec, newRequest(http.MethodPost, "/api/me/links/slack/begin", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp SlackInstallBeginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	u, err := neturl.Parse(resp.AuthorizeURL)
	if err != nil {
		t.Fatalf("parse authorize_url: %v", err)
	}
	q := u.Query()
	if got := q.Get("scope"); got != "" {
		t.Fatalf("a personal link must request no bot scopes, got %q", got)
	}
	if got := q.Get("user_scope"); got == "" {
		t.Fatalf("user_scope missing: %q", resp.AuthorizeURL)
	}
	box, err := slackSealBox()
	if err != nil {
		t.Fatalf("seal box: %v", err)
	}
	state, err := openSlackState(box, q.Get("state"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if state.Kind != slackStateKindLink {
		t.Fatalf("state kind = %q, want %q", state.Kind, slackStateKindLink)
	}
	if state.UserID != testUserID {
		t.Fatalf("state user = %q", state.UserID)
	}
}

func TestBeginSlackUserLink_NotConfiguredReturns503(t *testing.T) {
	t.Setenv("AGORA_SLACK_SECRET_KEY", "")
	t.Setenv("AGORA_SLACK_CLIENT_ID", "")
	t.Setenv("AGORA_SLACK_CLIENT_SECRET", "")
	t.Setenv("AGORA_SLACK_SIGNING_SECRET", "")

	rec := httptest.NewRecorder()
	testHandler.BeginSlackUserLink(rec, newRequest(http.MethodPost, "/api/me/links/slack/begin", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// The callback's link branch binds the identity and touches no installation.
func TestSlackOAuthCallback_PersonalLinkBindsIdentityOnly(t *testing.T) {
	configureSlack(t)
	cleanupSlackRows(t)
	fakeSlackUserExchange(t, "U0PERSONAL")

	box, err := slackSealBox()
	if err != nil {
		t.Fatalf("seal box: %v", err)
	}
	state, err := sealSlackState(box, slackOAuthState{
		Kind:        slackStateKindLink,
		WorkspaceID: testWorkspaceID,
		UserID:      testUserID,
	})
	if err != nil {
		t.Fatalf("seal state: %v", err)
	}

	rec := httptest.NewRecorder()
	testHandler.SlackOAuthCallback(rec,
		httptest.NewRequest(http.MethodGet, "/slack/oauth/callback?code=c&state="+neturl.QueryEscape(state), nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d body=%s", rec.Code, rec.Body.String())
	}
	location := rec.Header().Get("Location")
	if !strings.Contains(location, "slack_linked=1") || !strings.Contains(location, "tab=notifications") {
		t.Fatalf("unexpected redirect: %q", location)
	}

	got, err := testHandler.userIDByExternalIdentity(context.Background(), providerSlack, "U0PERSONAL")
	if err != nil {
		t.Fatalf("lookup identity: %v", err)
	}
	if got != testUserID {
		t.Fatalf("identity bound to %q, want %q", got, testUserID)
	}

	var installs int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM slack_installation WHERE workspace_id = $1::uuid`, testWorkspaceID).Scan(&installs); err != nil {
		t.Fatalf("count installations: %v", err)
	}
	if installs != 0 {
		t.Fatalf("a personal link must not create an installation, found %d", installs)
	}
}
