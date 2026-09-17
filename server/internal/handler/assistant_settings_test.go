package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
)

// Tests for the settings tools — the ones that let the assistant change the
// user's own preferences instead of describing where the switch lives.
//
// What each of them is actually protecting:
//
//   - The MERGE. hidden_nav and the notification map are whole-value columns,
//     so "hide usage" that forgot to carry the rest would silently un-hide
//     everything else the person had hidden. Both merge tests assert the
//     untouched entries survive.
//   - The VALIDATION staying the handler's. A bad language, a bad notification
//     value and "hide settings" must all come back in the product's own words,
//     because that text is what the model relays to the user.
//   - The BLAST RADIUS. These tools take no member id and no user id: the only
//     row any of them can reach is the caller's own.

func assistantStoredHiddenNav(t *testing.T, userID string) []string {
	t.Helper()
	var raw []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT hidden_nav FROM "user" WHERE id = $1`, userID).Scan(&raw); err != nil {
		t.Fatalf("read hidden_nav: %v", err)
	}
	keys := []string{}
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("decode hidden_nav %q: %v", raw, err)
	}
	return keys
}

func assistantResultStrings(t *testing.T, result map[string]any, key string) []string {
	t.Helper()
	raw, ok := result[key].([]any)
	if !ok {
		t.Fatalf("%s = %v, want a list", key, result[key])
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		out = append(out, item.(string))
	}
	return out
}

// ---------------------------------------------------------------------------
// get_my_settings
// ---------------------------------------------------------------------------

func TestAssistantGetMySettingsReadsProfileAndNotifications(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-getsettings@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-getsettings-ws", "GMS")
	addAssistantTestMember(t, ws, user, "owner")
	if _, err := testPool.Exec(context.Background(),
		`UPDATE "user" SET language='ru', timezone='Asia/Tashkent', hidden_nav='["usage"]' WHERE id=$1`,
		user); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO notification_preference (workspace_id, user_id, preferences) VALUES ($1,$2,'{"comments":"muted"}'::jsonb)`,
		ws, user); err != nil {
		t.Fatalf("seed preferences: %v", err)
	}

	result, err := executeAssistantTool(t, user, assistant.ToolGetMySettings, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("get_my_settings: %v", err)
	}
	if result["language"] != "ru" || result["timezone"] != "Asia/Tashkent" {
		t.Fatalf("profile = %v", result)
	}
	if got := assistantResultStrings(t, result, "hidden_nav"); len(got) != 1 || got[0] != "usage" {
		t.Fatalf("hidden_nav = %v", got)
	}
	prefs, ok := result["notification_preferences"].(map[string]any)
	if !ok || prefs["comments"] != "muted" {
		t.Fatalf("notification_preferences = %v", result["notification_preferences"])
	}
	// The group vocabulary ships with the answer so the model does not have to
	// guess a key for the follow-up write.
	if groups := assistantResultStrings(t, result, "notification_groups"); len(groups) != len(validNotifGroups) {
		t.Fatalf("notification_groups = %v", groups)
	}
	workspace, ok := result["workspace"].(map[string]any)
	if !ok || workspace["slug"] != "assistant-getsettings-ws" {
		t.Fatalf("workspace = %v", result["workspace"])
	}

	// With no workspace named and none in focus, the per-workspace half is
	// reported as unknown rather than answered from an arbitrary membership.
	unscoped, err := executeAssistantTool(t, user, assistant.ToolGetMySettings, `{}`)
	if err != nil {
		t.Fatalf("get_my_settings unscoped: %v", err)
	}
	if unscoped["notification_preferences"] != nil || unscoped["workspace"] != nil {
		t.Fatalf("unscoped answer invented a workspace: %v", unscoped)
	}
	if unscoped["language"] != "ru" {
		t.Fatalf("unscoped profile = %v", unscoped)
	}
}

// The run's workspace reaches the executor the same way its timezone does, so
// "am I muted here?" needs no grounding round-trip.
func TestAssistantGetMySettingsFollowsTheRunFocusWorkspace(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-getsettings-focus@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-getsettings-focus-ws", "GMF")
	addAssistantTestMember(t, ws, user, "member")

	ctx := assistant.WithFocusWorkspace(context.Background(), ws)
	raw, err := testHandler.Execute(ctx, user, "", assistant.ToolGetMySettings, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("get_my_settings: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	workspace, ok := result["workspace"].(map[string]any)
	if !ok || workspace["id"] != ws {
		t.Fatalf("focus workspace not used: %v", result["workspace"])
	}
	// Never changed anything there, so every group is on its default.
	if prefs, ok := result["notification_preferences"].(map[string]any); !ok || len(prefs) != 0 {
		t.Fatalf("notification_preferences = %v, want an empty map", result["notification_preferences"])
	}
}

// ---------------------------------------------------------------------------
// update_my_settings
// ---------------------------------------------------------------------------

func TestAssistantUpdateMySettingsChangesOnlyWhatWasNamed(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-updatesettings@agora.dev")
	bystander := newAssistantTestUser(t, "assistant-updatesettings-other@agora.dev")
	if _, err := testPool.Exec(context.Background(),
		`UPDATE "user" SET language='en', timezone='UTC' WHERE id=$1`, user); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	result, err := executeAssistantTool(t, user, assistant.ToolUpdateMySettings,
		`{"language":"uz","timezone":"Asia/Tashkent"}`)
	if err != nil {
		t.Fatalf("update_my_settings: %v", err)
	}
	if got := assistantResultStrings(t, result, "updated"); len(got) != 2 {
		t.Fatalf("updated = %v", got)
	}
	if result["language"] != "uz" || result["timezone"] != "Asia/Tashkent" {
		t.Fatalf("result = %v", result)
	}

	var language, timezone, name string
	if err := testPool.QueryRow(context.Background(),
		`SELECT language, timezone, name FROM "user" WHERE id=$1`, user).Scan(&language, &timezone, &name); err != nil {
		t.Fatalf("read user: %v", err)
	}
	if language != "uz" || timezone != "Asia/Tashkent" {
		t.Fatalf("stored = %s / %s", language, timezone)
	}
	// The name was not in the call, so UpdateMe kept it — a settings tool that
	// blanked unmentioned fields would be worse than no tool.
	if name == "" {
		t.Fatal("display name was cleared by a call that never mentioned it")
	}

	// Nobody else's row moved.
	var otherLanguage *string
	if err := testPool.QueryRow(context.Background(),
		`SELECT language FROM "user" WHERE id=$1`, bystander).Scan(&otherLanguage); err != nil {
		t.Fatalf("read bystander: %v", err)
	}
	if otherLanguage != nil && *otherLanguage == "uz" {
		t.Fatal("update_my_settings reached another user's row")
	}
}

// The handler owns the rules; the tool only relays them. A second
// implementation here is how the assistant and the settings page start
// disagreeing about what a valid language is.
func TestAssistantUpdateMySettingsRelaysHandlerValidation(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-updatesettings-bad@agora.dev")

	assistantToolRejects(t, user, assistant.ToolUpdateMySettings,
		`{"language":"klingon"}`, "unsupported language")
	assistantToolRejects(t, user, assistant.ToolUpdateMySettings,
		`{"timezone":"Mars/Olympus"}`, "invalid timezone")
	assistantToolRejects(t, user, assistant.ToolUpdateMySettings,
		`{"name":"   "}`, "name is required")
	assistantToolRejects(t, user, assistant.ToolUpdateMySettings,
		`{}`, "at least one setting")
}

// ---------------------------------------------------------------------------
// update_sidebar
// ---------------------------------------------------------------------------

func TestAssistantUpdateSidebarMergesAgainstTheCurrentList(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-sidebar@agora.dev")
	if _, err := testPool.Exec(context.Background(),
		`UPDATE "user" SET hidden_nav='["usage","mcp"]' WHERE id=$1`, user); err != nil {
		t.Fatalf("seed hidden_nav: %v", err)
	}

	// hide ADDS without disturbing what is already hidden.
	result, err := executeAssistantTool(t, user, assistant.ToolUpdateSidebar, `{"hide":["runtimes"]}`)
	if err != nil {
		t.Fatalf("update_sidebar hide: %v", err)
	}
	if got := assistantResultStrings(t, result, "hidden"); len(got) != 1 || got[0] != "runtimes" {
		t.Fatalf("hidden = %v", got)
	}
	if got := assistantStoredHiddenNav(t, user); strings.Join(got, ",") != "usage,mcp,runtimes" {
		t.Fatalf("stored = %v, want the previous two plus the new one", got)
	}

	// show REMOVES only the named key.
	result, err = executeAssistantTool(t, user, assistant.ToolUpdateSidebar, `{"show":["usage"]}`)
	if err != nil {
		t.Fatalf("update_sidebar show: %v", err)
	}
	if got := assistantResultStrings(t, result, "shown"); len(got) != 1 || got[0] != "usage" {
		t.Fatalf("shown = %v", got)
	}
	if got := assistantStoredHiddenNav(t, user); strings.Join(got, ",") != "mcp,runtimes" {
		t.Fatalf("stored = %v", got)
	}

	// Both directions in one call, and a key that was never hidden is simply
	// not reported as a change.
	result, err = executeAssistantTool(t, user, assistant.ToolUpdateSidebar,
		`{"hide":["projects"],"show":["mcp","inbox"]}`)
	if err != nil {
		t.Fatalf("update_sidebar both: %v", err)
	}
	if got := assistantResultStrings(t, result, "shown"); len(got) != 1 || got[0] != "mcp" {
		t.Fatalf("shown = %v, want only the key that was actually hidden", got)
	}
	if got := assistantStoredHiddenNav(t, user); strings.Join(got, ",") != "runtimes,projects" {
		t.Fatalf("stored = %v", got)
	}

	// The sidebar's real keys are camelCase; a tool that could not hide My
	// Issues would be a tool with a hole in it.
	if _, err := executeAssistantTool(t, user, assistant.ToolUpdateSidebar, `{"hide":["myIssues"]}`); err != nil {
		t.Fatalf("update_sidebar camelCase key: %v", err)
	}
	if got := assistantStoredHiddenNav(t, user); strings.Join(got, ",") != "runtimes,projects,myIssues" {
		t.Fatalf("stored = %v", got)
	}
}

func TestAssistantUpdateSidebarRefusesSettingsAndContradictions(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-sidebar-refuse@agora.dev")

	// Settings is the only route back to the screen that restores a hidden
	// item, and the refusal is the product's own wording.
	assistantToolRejects(t, user, assistant.ToolUpdateSidebar,
		`{"hide":["settings"]}`, "cannot be hidden")
	assistantToolRejects(t, user, assistant.ToolUpdateSidebar,
		`{"hide":["usage"],"show":["usage"]}`, "both hide and show")
	assistantToolRejects(t, user, assistant.ToolUpdateSidebar,
		`{}`, "at least one sidebar item")
	assistantToolRejects(t, user, assistant.ToolUpdateSidebar,
		`{"hide":["../../etc/passwd"]}`, "invalid hidden_nav key")

	// A refused call leaves the stored list exactly as it was.
	if got := assistantStoredHiddenNav(t, user); len(got) != 0 {
		t.Fatalf("a refused call still wrote %v", got)
	}
}

// ---------------------------------------------------------------------------
// update_notification_preferences
// ---------------------------------------------------------------------------

func TestAssistantUpdateNotificationPreferencesMerges(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-notifprefs@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-notifprefs-ws", "NPR")
	addAssistantTestMember(t, ws, user, "member")
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO notification_preference (workspace_id, user_id, preferences) VALUES ($1,$2,'{"assignments":"muted"}'::jsonb)`,
		ws, user); err != nil {
		t.Fatalf("seed preferences: %v", err)
	}

	result, err := executeAssistantTool(t, user, assistant.ToolUpdateNotificationPreferences,
		`{"workspace_id":"`+ws+`","preferences":{"comments":"muted"}}`)
	if err != nil {
		t.Fatalf("update_notification_preferences: %v", err)
	}
	prefs, ok := result["preferences"].(map[string]any)
	if !ok {
		t.Fatalf("preferences = %v", result["preferences"])
	}
	// THE regression this test exists for: PUT replaces the whole map, so a
	// tool that sent only the named group would have silently un-muted
	// assignments.
	if prefs["assignments"] != "muted" || prefs["comments"] != "muted" {
		t.Fatalf("preferences = %v, want the untouched group preserved", prefs)
	}
	changed, ok := result["changed"].(map[string]any)
	if !ok || len(changed) != 1 {
		t.Fatalf("changed = %v, want just the group that moved", result["changed"])
	}
	if result["workspace_slug"] != "assistant-notifprefs-ws" {
		t.Fatalf("workspace_slug = %v", result["workspace_slug"])
	}

	var stored []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT preferences FROM notification_preference WHERE workspace_id=$1 AND user_id=$2`,
		ws, user).Scan(&stored); err != nil {
		t.Fatalf("read preferences: %v", err)
	}
	var storedPrefs map[string]string
	if err := json.Unmarshal(stored, &storedPrefs); err != nil {
		t.Fatal(err)
	}
	if storedPrefs["assignments"] != "muted" || storedPrefs["comments"] != "muted" {
		t.Fatalf("stored = %v", storedPrefs)
	}
}

func TestAssistantUpdateNotificationPreferencesRelaysHandlerValidation(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-notifprefs-bad@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-notifprefs-bad-ws", "NPB")
	addAssistantTestMember(t, ws, user, "member")

	assistantToolRejects(t, user, assistant.ToolUpdateNotificationPreferences,
		`{"workspace_id":"`+ws+`","preferences":{"comments":"sometimes"}}`, "invalid preference value")
	assistantToolRejects(t, user, assistant.ToolUpdateNotificationPreferences,
		`{"workspace_id":"`+ws+`","preferences":{"weather":"all"}}`, "invalid preference group")
	assistantToolRejects(t, user, assistant.ToolUpdateNotificationPreferences,
		`{"workspace_id":"`+ws+`","preferences":{}}`, "at least one notification group")

	var count int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM notification_preference WHERE workspace_id=$1 AND user_id=$2`,
		ws, user).Scan(&count); err != nil {
		t.Fatalf("count preferences: %v", err)
	}
	if count != 0 {
		t.Fatal("a refused call still wrote a preference row")
	}
}

// ---------------------------------------------------------------------------
// Catalog properties
// ---------------------------------------------------------------------------

// A preference change is invisible in the transcript, so the receipt is the
// only durable record of what moved.
func TestAssistantSettingsWritesCarryReceipts(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-settings-receipt@agora.dev")

	result, err := executeAssistantTool(t, user, assistant.ToolUpdateSidebar, `{"hide":["usage"]}`)
	if err != nil {
		t.Fatalf("update_sidebar: %v", err)
	}
	receipt, ok := result["receipt"].(map[string]any)
	if !ok {
		t.Fatalf("no receipt on a settings write: %v", result)
	}
	if receipt["action"] != assistant.ToolUpdateSidebar {
		t.Fatalf("receipt action = %v", receipt["action"])
	}
	effects, ok := receipt["effects"].([]any)
	if !ok || len(effects) != 1 || !strings.Contains(effects[0].(string), "usage") {
		t.Fatalf("receipt effects = %v, want the key that was hidden", receipt["effects"])
	}
}

// Settings changes are reversible personal preferences. Binding a confirmation
// card to "mute comments" is how a confirmation gate stops meaning anything,
// so the catalog must keep them out of the destructive list.
func TestAssistantSettingsToolsAreNotConfirmationBound(t *testing.T) {
	for _, tool := range []string{
		assistant.ToolUpdateMySettings,
		assistant.ToolUpdateSidebar,
		assistant.ToolUpdateNotificationPreferences,
	} {
		if assistant.RequiresConfirmation(tool) {
			t.Fatalf("%s is confirmation-bound — settings changes are reversible", tool)
		}
		if !assistant.IsMutating(tool) {
			t.Fatalf("%s writes but is not in MutatingTools", tool)
		}
	}
	if assistant.IsMutating(assistant.ToolGetMySettings) {
		t.Fatal("get_my_settings is a read")
	}
}
