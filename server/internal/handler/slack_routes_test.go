package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Pure vocabulary tests — no DB, no HTTP.
// ---------------------------------------------------------------------------

func TestNormalizeSlackRouteEvents(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "canonical order and dedupe",
			in:   []string{"created", "failed", "failed"},
			want: []string{"failed", "created"}},
		{name: "unknown kinds are dropped, not stored",
			in:   []string{"failed", "deploy_recorded", "issue:created"},
			want: []string{"failed"}},
		// A comment is context for one person; in a channel it is the thing
		// that makes people mute. It is DM-only by construction.
		{name: "commented is never a channel kind",
			in:   []string{"commented"},
			want: []string{}},
		{name: "case and whitespace tolerated",
			in:   []string{" FAILED ", "Qa_Verdict"},
			want: []string{"failed", "qa_verdict"}},
		{name: "nothing in, nothing out", in: nil, want: []string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := normalizeSlackRouteEvents(test.in)
			if len(got) != len(test.want) {
				t.Fatalf("got %v, want %v", got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("got %v, want %v", got, test.want)
				}
			}
		})
	}
}

// The default set IS the noise rule: a fresh route hears only the events that
// mean a human is needed. If this test has to change, the change is a product
// decision, not a refactor.
func TestSlackDefaultRouteEventsAreTheQuietSet(t *testing.T) {
	got := slackDefaultRouteEvents()
	want := map[string]bool{slackEventFailed: true, slackEventQAVerdict: true, slackEventReviewVerdict: true}
	if len(got) != len(want) {
		t.Fatalf("default events = %v", got)
	}
	for _, kind := range got {
		if !want[kind] {
			t.Fatalf("unexpected loud default %q (defaults = %v)", kind, got)
		}
	}
	for _, kind := range []string{slackEventCreated, slackEventStatusChanged, slackEventAgentDone, slackEventAssigned, slackEventMentioned} {
		for _, def := range got {
			if def == kind {
				t.Fatalf("%q must be opt-in, not a default", kind)
			}
		}
	}
}

func TestSlackRouteMatchesEvent(t *testing.T) {
	filter := []string{slackEventFailed, slackEventQAVerdict}
	if !slackRouteMatchesEvent(filter, slackEventFailed) {
		t.Fatal("a subscribed kind must match")
	}
	if slackRouteMatchesEvent(filter, slackEventCreated) {
		t.Fatal("an unsubscribed kind must not match")
	}
	if slackRouteMatchesEvent(filter, "") {
		t.Fatal("an empty kind must never match")
	}
	if slackRouteMatchesEvent(nil, slackEventFailed) {
		t.Fatal("an empty filter must never match")
	}
}

func TestValidSlackChannelID(t *testing.T) {
	for _, ok := range []string{"C0ENG", "G01ABCDEF", "D0USER123"} {
		if !validSlackChannelID(ok) {
			t.Fatalf("%q should be accepted", ok)
		}
	}
	for _, bad := range []string{"", "   ", "#general", "c0eng lower", "C0ENG;DROP"} {
		if validSlackChannelID(bad) {
			t.Fatalf("%q should be rejected", bad)
		}
	}
}

// ---------------------------------------------------------------------------
// DB-backed CRUD
// ---------------------------------------------------------------------------

// seedSlackInstallation writes an active installation for the test workspace
// and returns its id. The token is sealed exactly as the OAuth callback seals
// it, so the delivery path can open it.
func seedSlackInstallation(t *testing.T, teamID string) string {
	t.Helper()
	box, err := slackSealBox()
	if err != nil {
		t.Fatalf("seal box: %v (configureSlack must run first)", err)
	}
	sealed, err := box.Seal([]byte(slackTestBotToken))
	if err != nil {
		t.Fatalf("seal token: %v", err)
	}
	return seedSlackInstallationIn(t, testWorkspaceID, teamID, sealed)
}

func seedSlackInstallationIn(t *testing.T, workspaceID, teamID string, sealed []byte) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO slack_installation (
			workspace_id, team_id, team_name, app_id, bot_user_id,
			bot_token_encrypted, scopes, installer_user_id
		) VALUES ($1::uuid, $2, 'Slack Route Tests', 'A0APP', 'U0BOT', $3, 'chat:write', $4::uuid)
		RETURNING id::text
	`, workspaceID, teamID, sealed, testUserID).Scan(&id); err != nil {
		t.Fatalf("seed slack installation: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		testPool.Exec(bg, `DELETE FROM slack_channel_route WHERE installation_id = $1::uuid`, id)
		testPool.Exec(bg, `DELETE FROM slack_installation WHERE id = $1::uuid`, id)
	})
	return id
}

// putSlackRoute drives the PUT handler and returns the decoded response.
func putSlackRoute(t *testing.T, routeID string, body map[string]any) (*httptest.ResponseRecorder, SlackChannelRouteResponse) {
	t.Helper()
	req := newRequest(http.MethodPut, "/api/workspaces/"+testWorkspaceID+"/slack/routes", body)
	req = withURLParam(req, "id", testWorkspaceID)
	if routeID != "" {
		req = withURLParams(req, "routeId", routeID)
	}
	w := httptest.NewRecorder()
	testHandler.PutSlackChannelRoute(w, req)
	var route SlackChannelRouteResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &route); err != nil {
			t.Fatalf("decode route: %v (body=%s)", err, w.Body.String())
		}
	}
	return w, route
}

func TestPutSlackChannelRoute_CreateUsesTheQuietDefault(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	configureSlack(t)
	installID := seedSlackInstallation(t, "T0ROUTE1")

	w, route := putSlackRoute(t, "", map[string]any{
		"installation_id": installID,
		"channel_id":      "C0ENG",
		"channel_name":    "#eng",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if route.ChannelID != "C0ENG" || route.ChannelName != "eng" {
		t.Fatalf("route = %+v (the leading # is display sugar, not part of the name)", route)
	}
	if !route.Enabled {
		t.Fatal("a new route must be enabled")
	}
	want := map[string]bool{slackEventFailed: true, slackEventQAVerdict: true, slackEventReviewVerdict: true}
	if len(route.Events) != len(want) {
		t.Fatalf("events = %v, want the quiet default", route.Events)
	}
	for _, e := range route.Events {
		if !want[e] {
			t.Fatalf("events = %v, want the quiet default", route.Events)
		}
	}
}

// Picking the same channel twice must EDIT the route, not fail on the unique
// index — the admin's mental model is "this channel hears these events".
func TestPutSlackChannelRoute_UpsertsTheSameChannel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	configureSlack(t)
	installID := seedSlackInstallation(t, "T0ROUTE2")

	_, first := putSlackRoute(t, "", map[string]any{
		"installation_id": installID, "channel_id": "C0ENG",
	})
	w, second := putSlackRoute(t, "", map[string]any{
		"installation_id": installID, "channel_id": "C0ENG",
		"events": []string{"created", "status_changed"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if first.ID != second.ID {
		t.Fatalf("expected an in-place edit, got a second route (%s vs %s)", first.ID, second.ID)
	}
	if len(second.Events) != 2 {
		t.Fatalf("events = %v", second.Events)
	}

	// Editing by route id keeps the identity fields the body omits.
	w, third := putSlackRoute(t, second.ID, map[string]any{"enabled": false})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if third.ID != second.ID || third.ChannelID != "C0ENG" {
		t.Fatalf("route = %+v", third)
	}
	if third.Enabled {
		t.Fatal("enabled:false was not applied")
	}
	if len(third.Events) != 2 {
		t.Fatalf("an omitted events list must be preserved, got %v", third.Events)
	}
}

func TestPutSlackChannelRoute_RejectsUnroutableEvents(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	configureSlack(t)
	installID := seedSlackInstallation(t, "T0ROUTE3")

	// `commented` is DM-only; "nonsense" is enum drift. Neither may be stored,
	// and a body that contains nothing else is a mistake worth a 400 rather
	// than a route that silently hears nothing.
	w, _ := putSlackRoute(t, "", map[string]any{
		"installation_id": installID, "channel_id": "C0ENG",
		"events": []string{"commented", "nonsense"},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutSlackChannelRoute_FencesForeignInstallation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	configureSlack(t)

	ctx := context.Background()
	var otherWorkspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ('Slack Fence Tests', 'slack-fence-tests', '', 'SFT')
		RETURNING id::text
	`).Scan(&otherWorkspaceID); err != nil {
		t.Fatalf("create other workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, otherWorkspaceID)
	})
	foreignInstall := seedSlackInstallationIn(t, otherWorkspaceID, "T0FENCE", []byte("sealed"))

	// An installation id belonging to another workspace is a 404, never a
	// route that would post this workspace's issues into another tenant's
	// Slack.
	w, _ := putSlackRoute(t, "", map[string]any{
		"installation_id": foreignInstall, "channel_id": "C0ENG",
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a foreign installation, got %d body=%s", w.Code, w.Body.String())
	}

	// So is a project from another workspace.
	installID := seedSlackInstallation(t, "T0ROUTE4")
	var foreignProject string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title, status, priority) VALUES ($1::uuid, 'Foreign', 'planned', 'none') RETURNING id::text`,
		otherWorkspaceID).Scan(&foreignProject); err != nil {
		t.Fatalf("create foreign project: %v", err)
	}
	w, _ = putSlackRoute(t, "", map[string]any{
		"installation_id": installID, "channel_id": "C0ENG", "project_id": foreignProject,
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a foreign project, got %d body=%s", w.Code, w.Body.String())
	}
}

// Routing a channel grants the whole workspace's notifications to a room.
// A plain member must not be able to do it.
func TestPutSlackChannelRoute_RequiresOwnerOrAdmin(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	configureSlack(t)
	installID := seedSlackInstallation(t, "T0ROUTE5")

	ctx := context.Background()
	var memberUserID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ('Slack Plain Member', 'slack-plain-member@agora.test')
		RETURNING id::text
	`).Scan(&memberUserID); err != nil {
		t.Fatalf("create member user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE email = 'slack-plain-member@agora.test'`)
	})
	if _, err := testPool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'member')`,
		testWorkspaceID, memberUserID); err != nil {
		t.Fatalf("add member: %v", err)
	}

	req := newRequestAs(memberUserID, http.MethodPut, "/api/workspaces/"+testWorkspaceID+"/slack/routes", map[string]any{
		"installation_id": installID, "channel_id": "C0ENG",
	})
	req = withURLParam(req, "id", testWorkspaceID)
	w := httptest.NewRecorder()
	testHandler.PutSlackChannelRoute(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a plain member, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPutSlackChannelRoute_RevokedInstallationCannotRoute(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	configureSlack(t)
	installID := seedSlackInstallation(t, "T0ROUTE6")
	if _, err := testPool.Exec(context.Background(),
		`UPDATE slack_installation SET status = 'revoked' WHERE id = $1::uuid`, installID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	w, _ := putSlackRoute(t, "", map[string]any{"installation_id": installID, "channel_id": "C0ENG"})
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a revoked installation, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestListAndDeleteSlackChannelRoutes(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	configureSlack(t)
	installID := seedSlackInstallation(t, "T0ROUTE7")
	_, route := putSlackRoute(t, "", map[string]any{"installation_id": installID, "channel_id": "C0ENG"})

	// --- list ---
	req := withURLParam(newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/slack/routes", nil), "id", testWorkspaceID)
	w := httptest.NewRecorder()
	testHandler.ListSlackChannelRoutes(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var listed struct {
		Routes          []SlackChannelRouteResponse `json:"routes"`
		AvailableEvents []string                    `json:"available_events"`
		DefaultEvents   []string                    `json:"default_events"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, r := range listed.Routes {
		if r.ID == route.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("created route missing from the list: %+v", listed.Routes)
	}
	// The vocabulary ships with the rows so the Settings UI cannot drift from
	// the server's idea of what a route may hear.
	if len(listed.AvailableEvents) == 0 || len(listed.DefaultEvents) == 0 {
		t.Fatalf("list must carry the event vocabulary: %+v", listed)
	}
	for _, e := range listed.AvailableEvents {
		if e == slackEventCommented {
			t.Fatal("commented must never be offered as a channel event")
		}
	}

	// --- delete: a forged id is a 404, not a 204 over zero rows ---
	req = withURLParams(withURLParam(newRequest(http.MethodDelete, "/api/workspaces/"+testWorkspaceID+"/slack/routes/x", nil), "id", testWorkspaceID),
		"routeId", "00000000-0000-0000-0000-000000000000")
	w = httptest.NewRecorder()
	testHandler.DeleteSlackChannelRoute(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown route, got %d", w.Code)
	}

	req = withURLParams(withURLParam(newRequest(http.MethodDelete, "/api/workspaces/"+testWorkspaceID+"/slack/routes/"+route.ID, nil), "id", testWorkspaceID),
		"routeId", route.ID)
	w = httptest.NewRecorder()
	testHandler.DeleteSlackChannelRoute(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	var remaining int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM slack_channel_route WHERE id = $1::uuid`, route.ID).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 0 {
		t.Fatal("the route row survived a 204")
	}
}

// ---------------------------------------------------------------------------
// Channel picker proxy
// ---------------------------------------------------------------------------

// fakeSlackAPI points the Web API client at a local server for one test.
func fakeSlackAPI(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Setenv("AGORA_SLACK_API_BASE_URL", srv.URL)
}

func TestListSlackChannels_ProxiesConversationsList(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	configureSlack(t)
	seedSlackInstallation(t, "T0CHAN1")
	var gotAuth string
	fakeSlackAPI(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		io.WriteString(w, `{"ok":true,"channels":[
			{"id":"C0ENG","name":"eng","is_member":true},
			{"id":"C0OLD","name":"old","is_archived":true}
		],"response_metadata":{"next_cursor":"NEXT"}}`)
	})

	req := withURLParam(newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/slack/channels", nil), "id", testWorkspaceID)
	w := httptest.NewRecorder()
	testHandler.ListSlackChannels(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if gotAuth != "Bearer "+slackTestBotToken {
		t.Fatal("the picker must speak with the installation's own unsealed token")
	}
	var resp struct {
		Channels   []SlackChannelResponse `json:"channels"`
		NextCursor string                 `json:"next_cursor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Channels) != 1 || resp.Channels[0].ID != "C0ENG" {
		t.Fatalf("archived channels must not reach the picker: %+v", resp.Channels)
	}
	if resp.NextCursor != "NEXT" {
		t.Fatalf("cursor = %q; paging is passed through, not walked server-side", resp.NextCursor)
	}
	// The response must never carry the credential it used.
	if strings.Contains(w.Body.String(), slackTestBotToken) {
		t.Fatal("the picker response leaked the bot token")
	}
}

func TestListSlackChannels_DeadTokenRevokesTheInstallation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	configureSlack(t)
	installID := seedSlackInstallation(t, "T0CHAN2")
	fakeSlackAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"ok":false,"error":"invalid_auth"}`)
	})

	req := withURLParam(newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/slack/channels", nil), "id", testWorkspaceID)
	w := httptest.NewRecorder()
	testHandler.ListSlackChannels(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", w.Code, w.Body.String())
	}
	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM slack_installation WHERE id = $1::uuid`, installID).Scan(&status); err != nil {
		t.Fatalf("status: %v", err)
	}
	if status != "revoked" {
		t.Fatalf("a rejected token must mark the installation revoked, status = %q", status)
	}
}

func TestListSlackChannels_NoInstallationIs404(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	configureSlack(t)
	req := withURLParam(newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/slack/channels", nil), "id", testWorkspaceID)
	w := httptest.NewRecorder()
	testHandler.ListSlackChannels(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body.String())
	}
}
