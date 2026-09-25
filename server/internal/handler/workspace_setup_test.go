package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"
)

// resetWorkspaceSetupKeys drops the two department-setup keys from the shared
// test workspace (and a sibling key the tests plant) so runs stay independent.
func resetWorkspaceSetupKeys(t *testing.T) {
	t.Helper()
	drop := func() {
		testPool.Exec(context.Background(),
			`UPDATE workspace SET settings = settings - 'team_sidebar' - 'department_setup' - 'setup_test_sibling' WHERE id = $1`,
			testWorkspaceID)
	}
	drop()
	t.Cleanup(drop)
}

func storedWorkspaceSettings(t *testing.T) map[string]any {
	t.Helper()
	var raw []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT settings FROM workspace WHERE id = $1`, testWorkspaceID,
	).Scan(&raw); err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("decode settings %q: %v", raw, err)
	}
	return settings
}

func TestPutTeamSidebarStoresKeysAndKeepsSiblings(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	resetWorkspaceSetupKeys(t)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE workspace SET settings = settings || '{"setup_test_sibling": {"keep": true}}'::jsonb WHERE id = $1`,
		testWorkspaceID,
	); err != nil {
		t.Fatalf("plant sibling key: %v", err)
	}

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("PUT", "/api/workspaces/"+testWorkspaceID+"/team-sidebar",
		map[string]any{"hidden": []string{"runtimes", " skills ", "runtimes", "mcp"}}), "id", testWorkspaceID)
	testHandler.PutTeamSidebar(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	settings := storedWorkspaceSettings(t)
	sidebar, _ := settings["team_sidebar"].(map[string]any)
	if !reflect.DeepEqual(sidebar["hidden"], []any{"runtimes", "skills", "mcp"}) {
		t.Fatalf("team_sidebar.hidden = %v", sidebar["hidden"])
	}
	if sidebar["updated_by"] != testUserID {
		t.Fatalf("team_sidebar.updated_by = %v", sidebar["updated_by"])
	}
	if _, ok := settings["setup_test_sibling"]; !ok {
		t.Fatal("a sibling settings key was clobbered")
	}

	// The response is the workspace, so the client can refresh its cache.
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["id"] != testWorkspaceID {
		t.Fatalf("response is not the workspace: %v", resp)
	}
}

// An empty list is "members see everything" and must persist as [], not be
// read as a missing value.
func TestPutTeamSidebarAcceptsEmptyList(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	resetWorkspaceSetupKeys(t)

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("PUT", "/api/workspaces/"+testWorkspaceID+"/team-sidebar",
		map[string]any{"hidden": []string{}}), "id", testWorkspaceID)
	testHandler.PutTeamSidebar(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	sidebar, _ := storedWorkspaceSettings(t)["team_sidebar"].(map[string]any)
	if hidden, ok := sidebar["hidden"].([]any); !ok || len(hidden) != 0 {
		t.Fatalf("team_sidebar.hidden = %#v, want []", sidebar["hidden"])
	}
}

func TestPutTeamSidebarRejectsInvalidInput(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	resetWorkspaceSetupKeys(t)

	cases := map[string]any{
		"always-visible key": map[string]any{"hidden": []string{"settings"}},
		"malformed key":      map[string]any{"hidden": []string{"../etc"}},
		"not a list":         map[string]any{"hidden": "runtimes"},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := withURLParam(newRequest("PUT", "/api/workspaces/"+testWorkspaceID+"/team-sidebar", body), "id", testWorkspaceID)
			testHandler.PutTeamSidebar(w, req)
			if w.Code != 400 {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
	if _, ok := storedWorkspaceSettings(t)["team_sidebar"]; ok {
		t.Fatal("an invalid request wrote team_sidebar")
	}
}

func TestWorkspaceSetupRejectsAgentActors(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	resetWorkspaceSetupKeys(t)

	for _, source := range []string{"task_token", "cloud_pat"} {
		w := httptest.NewRecorder()
		req := withURLParam(newRequest("PUT", "/api/workspaces/"+testWorkspaceID+"/team-sidebar",
			map[string]any{"hidden": []string{"runtimes"}}), "id", testWorkspaceID)
		req.Header.Set("X-Actor-Source", source)
		testHandler.PutTeamSidebar(w, req)
		if w.Code != 403 {
			t.Fatalf("%s: team-sidebar expected 403, got %d", source, w.Code)
		}

		w = httptest.NewRecorder()
		req = withURLParam(newRequest("POST", "/api/workspaces/"+testWorkspaceID+"/department-setup",
			map[string]any{"status": "done"}), "id", testWorkspaceID)
		req.Header.Set("X-Actor-Source", source)
		testHandler.PostDepartmentSetup(w, req)
		if w.Code != 403 {
			t.Fatalf("%s: department-setup expected 403, got %d", source, w.Code)
		}
	}
	settings := storedWorkspaceSettings(t)
	if _, ok := settings["team_sidebar"]; ok {
		t.Fatal("an agent wrote team_sidebar")
	}
	if _, ok := settings["department_setup"]; ok {
		t.Fatal("an agent wrote department_setup")
	}
}

func TestPostDepartmentSetupRecordsStatus(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	resetWorkspaceSetupKeys(t)

	for _, status := range []string{"skipped", "done"} {
		w := httptest.NewRecorder()
		req := withURLParam(newRequest("POST", "/api/workspaces/"+testWorkspaceID+"/department-setup",
			map[string]any{"status": status}), "id", testWorkspaceID)
		testHandler.PostDepartmentSetup(w, req)
		if w.Code != 200 {
			t.Fatalf("%s: expected 200, got %d: %s", status, w.Code, w.Body.String())
		}
		setup, _ := storedWorkspaceSettings(t)["department_setup"].(map[string]any)
		if setup["status"] != status || setup["by"] != testUserID || setup["at"] == "" {
			t.Fatalf("department_setup = %v", setup)
		}
	}

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("POST", "/api/workspaces/"+testWorkspaceID+"/department-setup",
		map[string]any{"status": "later"}), "id", testWorkspaceID)
	testHandler.PostDepartmentSetup(w, req)
	if w.Code != 400 {
		t.Fatalf("unknown status: expected 400, got %d", w.Code)
	}
}

// hidden_nav_customized tells the client whether a member's sidebar is their
// own or should follow the workspace's team sidebar. Only a PATCH that
// actually sets hidden_nav — including "show all" ([]) — flips it.
func TestUpdateMeMarksSidebarCustomized(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	userID := newHiddenNavTestUser(t, "nav-customized@agora.dev")

	customized := func(body string) bool {
		t.Helper()
		w := httptest.NewRecorder()
		testHandler.UpdateMe(w, newPatchMeRequest(userID, body))
		if w.Code != 200 {
			t.Fatalf("PATCH %s: expected 200, got %d: %s", body, w.Code, w.Body.String())
		}
		var resp map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		v, ok := resp["hidden_nav_customized"].(bool)
		if !ok {
			t.Fatalf("hidden_nav_customized missing: %v", resp)
		}
		return v
	}

	if customized(`{"name":"Still Default"}`) {
		t.Fatal("a name-only PATCH marked the sidebar customized")
	}
	if !customized(`{"hidden_nav":[]}`) {
		t.Fatal(`"show all" should count as a choice`)
	}
	if !customized(`{"timezone":"Asia/Tashkent"}`) {
		t.Fatal("a later unrelated PATCH cleared the customized flag")
	}
}
