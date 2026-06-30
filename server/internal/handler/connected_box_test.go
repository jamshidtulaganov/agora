package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestRepoBasename covers the fork/upstream-tolerant repo matching used to
// resolve an issue's QA box: a project bound to one owner's repo must match a
// box bound to a different owner's fork of the same repo, across https/ssh/.git
// forms.
func TestRepoBasename(t *testing.T) {
	cases := map[string]string{
		"https://github.com/jamshidtulaganov/sd-main.git": "sd-main",
		"https://github.com/azizkh/sd.git":                "sd",
		"git@github.com:jamshidtulaganov/cs3.git":         "cs3",
		"https://gitlab.sdteam.uz/g/Repo":                 "repo",
		"  HTTPS://github.com/x/SD-Main.GIT  ":            "sd-main",
		"":                                                "",
	}
	for in, want := range cases {
		if got := repoBasename(in); got != want {
			t.Errorf("repoBasename(%q) = %q, want %q", in, got, want)
		}
	}
}

// qaHostEnv sets a complete QA-host control-plane config for a provision test.
func qaHostEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AGORA_QA_HOST_SSH_HOST", "qa.sdteam.uz")
	t.Setenv("AGORA_QA_HOST_SSH_USER", "deploy")
	t.Setenv("AGORA_QA_HOST_BASE_DOMAIN", "sdteam.uz")
	t.Setenv("AGORA_QA_HOST_WEB_ROOT", "/var/www")
	t.Setenv("AGORA_QA_HOST_REPO_URL", "https://github.com/x/sd.git")
	t.Setenv("AGORA_QA_HOST_SEED_DIR", "/var/www/agora.sdteam.uz")
	t.Setenv("AGORA_QA_HOST_SEED_DB", "dbt_agora")
}

// TestProvisionConnectedBoxDryRun covers the review gate: a dry run returns the
// exact runbook + computed placement, touches the host nothing, and registers no
// box row (the host is a real prod server — nothing runs until the operator has
// seen the script).
func TestProvisionConnectedBoxDryRun(t *testing.T) {
	t.Setenv("AGORA_REMOTE_BOXES_ENABLED", "true")
	qaHostEnv(t)
	ctx := context.Background()

	member, err := testHandler.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID:      testUUID(testUserID),
		WorkspaceID: testUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatalf("get member: %v", err)
	}

	var before int
	testPool.QueryRow(ctx, `SELECT count(*) FROM connected_box WHERE workspace_id=$1`, testUUID(testWorkspaceID)).Scan(&before)

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/remote-boxes/provision?workspace_id="+testWorkspaceID, map[string]any{
		"member_id": uuidToString(member.ID), "handle": "shakhzod", "dry_run": true,
	})
	testHandler.ProvisionConnectedBoxForMember(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("dry-run: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp ProvisionConnectedBoxResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The box inherits the seed's DB (dbt_agora from qaHostEnv) — "keep each box's
	// existing DB", no isolated clone.
	if resp.Subdomain != "shakhzod.sdteam.uz" || resp.WorkDir != "/var/www/shakhzod.sdteam.uz" || resp.Database != "dbt_agora" {
		t.Errorf("wrong placement: %+v", resp)
	}
	if resp.Box != nil || resp.Ran {
		t.Errorf("dry-run must neither run nor create a box: ran=%v box=%v", resp.Ran, resp.Box)
	}
	for _, want := range []string{"/var/www/shakhzod.sdteam.uz", "git clone --depth 1"} {
		if !strings.Contains(resp.Script, want) {
			t.Errorf("script missing %q: %s", want, resp.Script)
		}
	}
	var after int
	testPool.QueryRow(ctx, `SELECT count(*) FROM connected_box WHERE workspace_id=$1`, testUUID(testWorkspaceID)).Scan(&after)
	if after != before {
		t.Errorf("dry-run must not create a box row: %d -> %d", before, after)
	}
}

// TestProvisionConnectedBoxRequiresQAHost: with the QA host unconfigured the
// endpoint fails closed (503) so a half-configured deployment can't provision.
func TestProvisionConnectedBoxRequiresQAHost(t *testing.T) {
	t.Setenv("AGORA_REMOTE_BOXES_ENABLED", "true")
	for _, k := range []string{
		"AGORA_QA_HOST_SSH_HOST", "AGORA_QA_HOST_SSH_USER", "AGORA_QA_HOST_BASE_DOMAIN",
		"AGORA_QA_HOST_WEB_ROOT", "AGORA_QA_HOST_REPO_URL", "AGORA_QA_HOST_SEED_DIR", "AGORA_QA_HOST_SEED_DB",
	} {
		t.Setenv(k, "")
	}
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/remote-boxes/provision?workspace_id="+testWorkspaceID, map[string]any{
		"member_id": testUserID, "dry_run": true,
	})
	testHandler.ProvisionConnectedBoxForMember(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured QA host: expected 503, got %d: %s", w.Code, w.Body.String())
	}
}

// TestConnectedBoxForIssuePerDeveloper: the box owned by the developer behind an
// issue (its assignee agent's owner) wins over any project-bound box, so the
// dev's branch deploys to their own isolated box.
func TestConnectedBoxForIssuePerDeveloper(t *testing.T) {
	ctx := context.Background()
	agentID, ownerID, _ := privateAgentTestFixture(t)

	devBox, err := testHandler.Queries.CreateConnectedBox(ctx, db.CreateConnectedBoxParams{
		WorkspaceID: testUUID(testWorkspaceID),
		OwnerID:     testUUID(ownerID),
		Label:       "dev", SshHost: "qa.sdteam.uz", SshUser: "deploy", SshPort: 22,
	})
	if err != nil {
		t.Fatalf("create dev box: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM connected_box WHERE id=$1`, uuidToString(devBox.ID))
	})

	// Issue assigned to that agent. ProjectID need only be Valid for the resolver
	// to proceed; the dev-axis matches on owner before any project binding.
	issue := db.Issue{
		WorkspaceID:  testUUID(testWorkspaceID),
		ProjectID:    testUUID(testWorkspaceID), // any valid uuid; not a real project
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
		AssigneeID:   testUUID(agentID),
	}
	box, ok := testHandler.connectedBoxForIssue(ctx, issue)
	if !ok || uuidToString(box.ID) != uuidToString(devBox.ID) {
		t.Errorf("expected the dev box %s, got ok=%v id=%s", uuidToString(devBox.ID), ok, uuidToString(box.ID))
	}
}

// TestDevBoxSmokeURL: run_qa smokes the assignee developer's own box — the URL
// is derived from the resolved box's work_dir (/var/www/<subdomain>).
func TestDevBoxSmokeURL(t *testing.T) {
	t.Setenv("AGORA_REMOTE_BOXES_ENABLED", "true")
	ctx := context.Background()
	agentID, ownerID, _ := privateAgentTestFixture(t)

	box, err := testHandler.Queries.CreateConnectedBox(ctx, db.CreateConnectedBoxParams{
		WorkspaceID: testUUID(testWorkspaceID),
		OwnerID:     testUUID(ownerID),
		Label:       "shahzod", SshHost: "193.149.18.99", SshUser: "deploy", SshPort: 22,
		RepoUrl: "https://github.com/x/sd.git", WorkDir: "/var/www/shahzod.sdteam.uz",
	})
	if err != nil {
		t.Fatalf("create box: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM connected_box WHERE id=$1`, uuidToString(box.ID))
	})

	issue := db.Issue{
		WorkspaceID:  testUUID(testWorkspaceID),
		ProjectID:    testUUID(testWorkspaceID),
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
		AssigneeID:   testUUID(agentID),
	}
	if got := testHandler.devBoxSmokeURL(ctx, issue); got != "https://shahzod.sdteam.uz" {
		t.Errorf("devBoxSmokeURL = %q, want https://shahzod.sdteam.uz", got)
	}

	// Flag off → no dev-box smoke override (falls back to project qa_smoke_url).
	t.Setenv("AGORA_REMOTE_BOXES_ENABLED", "")
	if got := testHandler.devBoxSmokeURL(ctx, issue); got != "" {
		t.Errorf("flag off must yield no dev-box url, got %q", got)
	}
}

// TestConnectedBoxFeatureFlag pins the additive/opt-in contract: with the flag
// OFF the endpoints fail closed (404), so the Remote Boxes feature is inert for
// any deployment that hasn't enabled it.
func TestConnectedBoxFeatureFlag(t *testing.T) {
	t.Setenv("AGORA_REMOTE_BOXES_ENABLED", "") // explicitly off
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/remote-boxes?workspace_id="+testWorkspaceID, map[string]any{
		"label": "jamshid", "ssh_host": "jamshid.sdteam.uz", "ssh_user": "dev",
	})
	testHandler.CreateConnectedBox(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("flag off: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestConnectedBoxCRUD covers create → list → delete with the flag on, plus the
// ssh_port default and required-field validation.
func TestConnectedBoxCRUD(t *testing.T) {
	t.Setenv("AGORA_REMOTE_BOXES_ENABLED", "true")

	// --- create ---
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/remote-boxes?workspace_id="+testWorkspaceID, map[string]any{
		"label": "jamshid", "ssh_host": "jamshid.sdteam.uz", "ssh_user": "dev",
	})
	testHandler.CreateConnectedBox(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var box ConnectedBoxResponse
	if err := json.NewDecoder(w.Body).Decode(&box); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM connected_box WHERE id = $1`, box.ID)
	})
	if box.SSHPort != 22 {
		t.Errorf("ssh_port should default to 22, got %d", box.SSHPort)
	}
	if box.Status != "pending" {
		t.Errorf("new box status should be pending, got %q", box.Status)
	}
	if box.OwnerID == nil || *box.OwnerID != testUserID {
		t.Errorf("owner should be the caller %q, got %v", testUserID, box.OwnerID)
	}

	// --- list contains it ---
	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/remote-boxes?workspace_id="+testWorkspaceID, nil)
	testHandler.ListConnectedBoxes(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Boxes []ConnectedBoxResponse `json:"boxes"`
	}
	if err := json.NewDecoder(w.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, b := range listResp.Boxes {
		if b.ID == box.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("created box not present in list")
	}

	// --- delete ---
	w = httptest.NewRecorder()
	req = newRequest("DELETE", "/api/remote-boxes/"+box.ID+"?workspace_id="+testWorkspaceID, nil)
	req = withURLParam(req, "id", box.ID)
	testHandler.DeleteConnectedBox(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d: %s", w.Code, w.Body.String())
	}

	// --- gone from list ---
	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/remote-boxes?workspace_id="+testWorkspaceID, nil)
	testHandler.ListConnectedBoxes(w, req)
	_ = json.NewDecoder(w.Body).Decode(&listResp)
	for _, b := range listResp.Boxes {
		if b.ID == box.ID {
			t.Fatal("box should be gone after delete")
		}
	}
}

// TestConnectedBoxValidation covers required-field rejection.
func TestConnectedBoxValidation(t *testing.T) {
	t.Setenv("AGORA_REMOTE_BOXES_ENABLED", "true")
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/remote-boxes?workspace_id="+testWorkspaceID, map[string]any{
		"label": "  ", "ssh_host": "", "ssh_user": "dev",
	})
	testHandler.CreateConnectedBox(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("blank required fields: expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestConnectedBoxTenancy proves the WHERE workspace_id scoping at the query
// layer: a box created in one workspace is invisible to another (Get/Delete with
// a foreign workspace id never matches), so no cross-tenant leak is possible.
func TestConnectedBoxTenancy(t *testing.T) {
	t.Setenv("AGORA_REMOTE_BOXES_ENABLED", "true")
	ctx := context.Background()
	box, err := testHandler.Queries.CreateConnectedBox(ctx, db.CreateConnectedBoxParams{
		WorkspaceID:  testUUID(testWorkspaceID),
		OwnerID:      testUUID(testUserID),
		Label:        "qa",
		SshHost:      "qa.sdteam.uz",
		SshUser:      "qa",
		SshPort:      22,
		DeployPubkey: "",
	})
	if err != nil {
		t.Fatalf("seed box: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM connected_box WHERE id = $1`, box.ID)
	})

	foreignWs := testUUID("99999999-9999-9999-9999-999999999999")
	if _, err := testHandler.Queries.GetConnectedBox(ctx, db.GetConnectedBoxParams{
		ID:          box.ID,
		WorkspaceID: foreignWs,
	}); err == nil {
		t.Error("Get with a foreign workspace must NOT find the box")
	}

	// Delete scoped to a foreign workspace is a no-op; the row survives.
	_ = testHandler.Queries.DeleteConnectedBox(ctx, db.DeleteConnectedBoxParams{
		ID:          box.ID,
		WorkspaceID: foreignWs,
	})
	if _, err := testHandler.Queries.GetConnectedBox(ctx, db.GetConnectedBoxParams{
		ID:          box.ID,
		WorkspaceID: testUUID(testWorkspaceID),
	}); err != nil {
		t.Errorf("box must survive a foreign-workspace delete, got err: %v", err)
	}
}
