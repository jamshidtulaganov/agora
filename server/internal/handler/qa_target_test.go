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

// createTestProject inserts a throwaway project row (connected_box.project_id
// is a real FK) and returns its id with cleanup registered.
func createTestProject(t *testing.T, ctx context.Context, title string) pgtype.UUID {
	t.Helper()
	var id string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title, status) VALUES ($1, $2, 'planned') RETURNING id`,
		testWorkspaceID, title).Scan(&id); err != nil {
		t.Fatalf("create test project: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM project WHERE id=$1`, id) })
	return testUUID(id)
}

// TestConnectedBoxForIssuePerDeveloper pins the PROJECT-SCOPED per-dev
// contract: a developer's box wins for their issue ONLY when the box is
// explicitly scoped to the issue's project — an unscoped personal box (or one
// scoped to a different project) must never match, because each project's
// boxes serve a different app (no cross-project defaults).
func TestConnectedBoxForIssuePerDeveloper(t *testing.T) {
	ctx := context.Background()
	agentID, ownerID, _ := privateAgentTestFixture(t)
	projectID := createTestProject(t, ctx, "per-dev-box-test")

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

	issue := db.Issue{
		WorkspaceID:  testUUID(testWorkspaceID),
		ProjectID:    projectID,
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
		AssigneeID:   testUUID(agentID),
	}

	// Unscoped personal box: owner matches, project does not — no match.
	if box, ok := testHandler.connectedBoxForIssue(ctx, issue); ok {
		t.Errorf("unscoped personal box must not match, got %s", uuidToString(box.ID))
	}

	// Scope the box to the issue's project — now the per-dev step matches.
	if _, err := testHandler.Queries.BindConnectedBoxProject(ctx, db.BindConnectedBoxProjectParams{
		ID: devBox.ID, WorkspaceID: testUUID(testWorkspaceID), ProjectID: projectID,
	}); err != nil {
		t.Fatalf("bind project: %v", err)
	}
	box, ok := testHandler.connectedBoxForIssue(ctx, issue)
	if !ok || uuidToString(box.ID) != uuidToString(devBox.ID) {
		t.Errorf("expected the project-scoped dev box %s, got ok=%v id=%s", uuidToString(devBox.ID), ok, uuidToString(box.ID))
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
	// Per-dev boxes are project-scoped (no cross-project defaults) — bind it
	// to the issue's project or the resolver skips it by design.
	smokeProject := createTestProject(t, ctx, "dev-box-smoke-test")
	if _, err := testHandler.Queries.BindConnectedBoxProject(ctx, db.BindConnectedBoxProjectParams{
		ID: box.ID, WorkspaceID: testUUID(testWorkspaceID), ProjectID: smokeProject,
	}); err != nil {
		t.Fatalf("bind project: %v", err)
	}

	issue := db.Issue{
		WorkspaceID:  testUUID(testWorkspaceID),
		ProjectID:    smokeProject,
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

// TestResponseBlocksFraming is the regression guard for a real bug caught
// during live testing against sd-main's box (agora.sdteam.uz): its CSP is
// `frame-ancestors 'self' https://web.telegram.org https://*.telegram.org`
// — a SCOPED subdomain wildcard inside one source value, not the CSP
// special token `*` (any origin). A naive strings.Contains(directive, "*")
// matches that substring and wrongly concludes the policy is wide open —
// exactly backwards, since sd-main's policy explicitly does NOT list
// Agora's origin. Confirmed live: the buggy version returned
// embeddable=true for a URL that every manual curl/wget check proved
// blocks framing.
func TestResponseBlocksFraming(t *testing.T) {
	tests := []struct {
		name        string
		headers     map[string]string
		wantBlocked bool
	}{
		{
			name:        "no headers at all -> not blocked",
			headers:     map[string]string{},
			wantBlocked: false,
		},
		{
			name:        "X-Frame-Options: DENY",
			headers:     map[string]string{"X-Frame-Options": "DENY"},
			wantBlocked: true,
		},
		{
			name:        "X-Frame-Options: SAMEORIGIN",
			headers:     map[string]string{"X-Frame-Options": "sameorigin"},
			wantBlocked: true,
		},
		{
			name:        "CSP frame-ancestors bare wildcard -> open, not blocked",
			headers:     map[string]string{"Content-Security-Policy": "frame-ancestors *"},
			wantBlocked: false,
		},
		{
			name: "sd-main's real policy: scoped subdomain wildcard inside a source value -> BLOCKED",
			headers: map[string]string{
				"Content-Security-Policy": "frame-ancestors 'self' https://web.telegram.org https://*.telegram.org",
			},
			wantBlocked: true,
		},
		{
			name:        "frame-ancestors 'self' only -> blocked (no wildcard token)",
			headers:     map[string]string{"Content-Security-Policy": "frame-ancestors 'self'"},
			wantBlocked: true,
		},
		{
			name:        "unrelated CSP directives, no frame-ancestors -> not blocked",
			headers:     map[string]string{"Content-Security-Policy": "default-src 'self'; script-src 'self' *.example.com"},
			wantBlocked: false,
		},
		{
			name: "frame-ancestors among multiple directives -> still detected",
			headers: map[string]string{
				"Content-Security-Policy": "default-src 'self'; frame-ancestors 'self'; script-src 'self'",
			},
			wantBlocked: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			for k, v := range tt.headers {
				h.Set(k, v)
			}
			if got := responseBlocksFraming(h); got != tt.wantBlocked {
				t.Errorf("responseBlocksFraming(%v) = %v, want %v", tt.headers, got, tt.wantBlocked)
			}
		})
	}
}

// TestUrlAllowsFraming_WalksRedirectChain proves the function checks EVERY
// hop's headers, not just the final response Go's client happens to land
// on — the redirect-chain equivalent of the sd-main bug: a block on an
// intermediate hop must not be missed just because the final destination
// looks clean.
func TestUrlAllowsFraming_WalksRedirectChain(t *testing.T) {
	t.Run("block on the redirect hop is caught even though the final page is clean", func(t *testing.T) {
		final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK) // no restrictive headers here
		}))
		defer final.Close()

		redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Security-Policy", "frame-ancestors 'self'")
			http.Redirect(w, r, final.URL, http.StatusFound)
		}))
		defer redirector.Close()

		if urlAllowsFraming(context.Background(), redirector.URL) {
			t.Error("expected false — the redirect hop itself carries a blocking CSP")
		}
	})

	t.Run("clean chain end to end -> true", func(t *testing.T) {
		final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer final.Close()

		redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, final.URL, http.StatusFound)
		}))
		defer redirector.Close()

		if !urlAllowsFraming(context.Background(), redirector.URL) {
			t.Error("expected true — neither hop sets a blocking header")
		}
	})

	t.Run("unreachable target -> false (fail closed)", func(t *testing.T) {
		if urlAllowsFraming(context.Background(), "http://127.0.0.1:1") {
			t.Error("expected false on a connection failure")
		}
	})
}
