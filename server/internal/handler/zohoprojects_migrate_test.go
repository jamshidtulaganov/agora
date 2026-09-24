package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/zohoprojects"
)

func TestZohoEmailAllowed(t *testing.T) {
	company := []string{"tsst.ai", "octanefuel.com"}
	cases := []struct {
		email   string
		domains []string
		allowed bool
	}{
		{"dina.c@tsst.ai", company, true},
		{"Zeyba.I@OctaneFuel.com", company, true},
		{"someone@gmail.com", company, false},
		{"partner@acme.com", company, false},
		{"partner@acme.com", nil, true},
		{"someone@gmail.com", nil, false},
		{"someone@gmail.com", []string{"gmail.com"}, true},
		{"not-an-email", nil, false},
		{"", company, false},
	}
	for _, c := range cases {
		got := zohoEmailAllowed(c.email, c.domains) == ""
		if got != c.allowed {
			t.Errorf("zohoEmailAllowed(%q, %v) allowed=%v, want %v", c.email, c.domains, got, c.allowed)
		}
	}
}

func TestZohoProjectIsActive(t *testing.T) {
	cases := []struct {
		status, custom string
		want           bool
	}{
		{"active", "Active", true},
		{"active", "In Progress", true},
		{"active", "Cancelled", false},
		{"active", "Completed", false},
		{"archived", "Active", false},
		{"", "", true},
	}
	for _, c := range cases {
		got := zohoProjectIsActive(zohoprojects.Project{Status: c.status, CustomStatus: c.custom})
		if got != c.want {
			t.Errorf("zohoProjectIsActive(%q,%q) = %v, want %v", c.status, c.custom, got, c.want)
		}
	}
}

func TestZohoWorkspaceSlug(t *testing.T) {
	cases := map[string]string{
		"Collections Department":      "collections-department",
		"DWH (Analytics Department)":  "dwh-analytics-department",
		"RnD / CRM Department":        "rnd-crm-department",
		"Customer Experience Team Q3": "customer-experience-team-q3",
		"!!!":                         "zoho-project",
	}
	for in, want := range cases {
		if got := zohoWorkspaceSlug(in); got != want {
			t.Errorf("zohoWorkspaceSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

// migrateMock serves a two-project portal: 211 (Active) with two tasks and 212
// (Cancelled). Project users answer with Zoho's scope error unless
// usersReadable is set.
type migrateMock struct {
	srv           *httptest.Server
	usersReadable bool
}

const (
	migDomain     = "acme-mig.test"
	migOwnerEmail = "owner@" + migDomain
	migAliceEmail = "alice@" + migDomain
	migBobEmail   = "bob@" + migDomain
	migCarolEmail = "carol@" + migDomain
	migEveEmail   = "eve.mig@gmail.com"
)

func newMigrateMock(t *testing.T) *migrateMock {
	t.Helper()
	m := &migrateMock{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case strings.Contains(path, "/oauth/v2/token"):
			io.WriteString(w, `{"access_token":"tok-123","expires_in":3600,"token_type":"Zoho-oauthtoken"}`)
		case strings.HasSuffix(path, "/users/"):
			if !m.usersReadable {
				w.WriteHeader(http.StatusUnauthorized)
				io.WriteString(w, `{"error":{"code":6403,"message":"Invalid OAuth scope."}}`)
				return
			}
			io.WriteString(w, `{"users":[
				{"id":"1","name":"Owner","email":"`+migOwnerEmail+`","role":"admin","active":true},
				{"id":"2","name":"Carol","email":"`+migCarolEmail+`","role":"manager","active":true},
				{"id":"3","name":"Alice","email":"`+migAliceEmail+`","role":"employee","active":true}]}`)
		case strings.HasSuffix(path, "/comments/"):
			io.WriteString(w, `{"comments":[{"id":9,"id_string":"9","content":"first note","added_person":"Alice","created_time":"09-01-2026"}]}`)
		case strings.HasSuffix(path, "/subtasks/"), strings.HasSuffix(path, "/tasklists/"):
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(path, "/projects/211/tasks/"):
			io.WriteString(w, `{"tasks":[
				{"id":9101,"id_string":"9101","name":"Chase overdue &amp; escalate",
				 "description":"<div>Call <b>daily</b><br/>see <a href=\"https://x.io/d\">doc</a></div>",
				 "status":{"name":"In Progress","type":"open"},"priority":"High",
				 "start_date":"09-01-2026","end_date":"09-30-2026","is_comment_added":true,
				 "details":{"owners":[{"zpuid":1,"name":"Alice","email":"`+migAliceEmail+`"}]},
				 "created_by_email":"`+migBobEmail+`","created_by_full_name":"Bob","created_by_zpuid":"4",
				 "tasklist":{"id":601,"id_string":"601","name":"General"}},
				{"id":9102,"id_string":"9102","name":"Close ledger",
				 "status":{"name":"Closed","type":"closed"},"priority":"None","is_comment_added":false,
				 "details":{"owners":[{"zpuid":5,"name":"Eve","email":"`+migEveEmail+`"}]},
				 "created_by_email":"`+migOwnerEmail+`","created_by_full_name":"Owner",
				 "tasklist":{"id":601,"id_string":"601","name":"General"}}]}`)
		case strings.HasSuffix(path, "/tasks/"):
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(path, "/projects/"):
			io.WriteString(w, `{"projects":[
				{"id":211,"id_string":"211","key":"MIG-1","name":"Migration Test Dept","status":"active",
				 "custom_status_name":"Active","owner_email":"`+migOwnerEmail+`","owner_name":"Owner","owner_zpuid":"1",
				 "description":"<div>Collections &amp; recovery</div>"},
				{"id":212,"id_string":"212","key":"MIG-2","name":"Migration Dead Dept","status":"active",
				 "custom_status_name":"Cancelled","owner_email":"`+migOwnerEmail+`"}]}`)
		default:
			io.WriteString(w, `{"result":true}`)
		}
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func cleanupMigrateFixtures(t *testing.T) {
	t.Helper()
	clean := func() {
		ctx := context.Background()
		testPool.Exec(ctx, `DELETE FROM workspace WHERE settings->>'zoho_project_id' IN ('211','212')`)
		testPool.Exec(ctx, `DELETE FROM "user" WHERE email LIKE '%@`+migDomain+`' OR email = $1`, migEveEmail)
	}
	clean()
	t.Cleanup(clean)
}

func runMigrate(t *testing.T, body map[string]any) (int, ZohoMigrateResponse) {
	t.Helper()
	w := httptest.NewRecorder()
	testHandler.MigrateZohoWorkspaces(w, newRequest("POST", "/api/zoho-projects/migrate-workspaces", body))
	var resp ZohoMigrateResponse
	if w.Code == http.StatusOK || w.Code == http.StatusAccepted {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v (%s)", err, w.Body.String())
		}
	}
	return w.Code, resp
}

func personByEmail(people []ZohoMigratePerson, email string) *ZohoMigratePerson {
	for i := range people {
		if people[i].Email == email {
			return &people[i]
		}
	}
	return nil
}

func TestZohoMigrateDisabledByDefault(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	t.Setenv("AGORA_ZOHO_MIGRATE", "")
	if code, _ := runMigrate(t, map[string]any{"dry_run": true}); code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", code)
	}
}

// TestZohoMigrateWorkspaces: dry run plans without writing; a real run creates
// one workspace per active project with the Zoho people as members, imports the
// tasks with status/priority/dates/assignee, and a second run reuses everything.
func TestZohoMigrateWorkspaces(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	mock := newMigrateMock(t)
	configureZohoEnv(t, mock.srv.URL)
	t.Setenv("AGORA_ZOHO_MIGRATE", "1")
	cleanupMigrateFixtures(t)
	ctx := context.Background()
	body := map[string]any{"allowed_email_domains": []string{migDomain}}

	// --- dry run ---
	body["dry_run"] = true
	code, plan := runMigrate(t, body)
	if code != http.StatusOK {
		t.Fatalf("dry run code = %d", code)
	}
	if len(plan.Projects) != 1 || plan.Projects[0].ZohoProjectID != "211" {
		t.Fatalf("planned projects = %+v, want only 211", plan.Projects)
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0].ZohoProjectID != "212" {
		t.Fatalf("skipped = %+v, want 212", plan.Skipped)
	}
	if !strings.HasPrefix(plan.MembershipSource, "tasks") {
		t.Errorf("membership_source = %q, want tasks fallback", plan.MembershipSource)
	}
	p := plan.Projects[0]
	if p.Action != "create" || p.WorkspaceSlug != "migration-test-dept" || p.Tasks != 2 || p.OpenTasks != 1 {
		t.Errorf("plan = action %q slug %q tasks %d open %d", p.Action, p.WorkspaceSlug, p.Tasks, p.OpenTasks)
	}
	for email, role := range map[string]string{migOwnerEmail: "owner", handlerTestEmail: "owner", migAliceEmail: "member", migBobEmail: "member"} {
		if got := personByEmail(p.People, email); got == nil || got.Role != role || got.Skipped != "" {
			t.Errorf("person %s = %+v, want role %s", email, got, role)
		}
	}
	if eve := personByEmail(p.People, migEveEmail); eve == nil || eve.Skipped == "" {
		t.Errorf("gmail person should be planned as skipped, got %+v", eve)
	}
	var n int
	testPool.QueryRow(ctx, `SELECT count(*) FROM workspace WHERE settings->>'zoho_project_id' = '211'`).Scan(&n)
	if n != 0 {
		t.Fatalf("dry run created %d workspaces", n)
	}

	// --- real run ---
	body["dry_run"] = false
	code, res := runMigrate(t, body)
	if code != http.StatusAccepted {
		t.Fatalf("real run code = %d", code)
	}
	wsID := res.Projects[0].WorkspaceID
	if wsID == "" {
		t.Fatalf("no workspace id in response: %+v", res.Projects[0])
	}
	roles := map[string]string{}
	rows, err := testPool.Query(ctx,
		`SELECT u.email, m.role FROM member m JOIN "user" u ON u.id = m.user_id WHERE m.workspace_id = $1::uuid`, wsID)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	for rows.Next() {
		var e, r string
		rows.Scan(&e, &r)
		roles[e] = r
	}
	rows.Close()
	want := map[string]string{migOwnerEmail: "owner", handlerTestEmail: "owner", migAliceEmail: "member", migBobEmail: "member"}
	for e, r := range want {
		if roles[e] != r {
			t.Errorf("member %s role = %q, want %q (all: %v)", e, roles[e], r, roles)
		}
	}
	if _, ok := roles[migEveEmail]; ok {
		t.Errorf("gmail person must not become a member")
	}
	var onboarded bool
	testPool.QueryRow(ctx, `SELECT onboarded_at IS NOT NULL FROM "user" WHERE email = $1`, migAliceEmail).Scan(&onboarded)
	if !onboarded {
		t.Errorf("provisioned user should be marked onboarded")
	}

	// Background task import.
	var issues int
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		testPool.QueryRow(ctx, `SELECT count(*) FROM issue WHERE workspace_id = $1::uuid`, wsID).Scan(&issues)
		if issues == 2 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if issues != 2 {
		t.Fatalf("imported issues = %d, want 2", issues)
	}
	var title, status, priority, desc, assignee, creator, due string
	if err := testPool.QueryRow(ctx,
		`SELECT i.title, i.status, i.priority, COALESCE(i.description,''), COALESCE(a.email,''), COALESCE(c.email,''),
		        COALESCE(to_char(i.due_date,'YYYY-MM-DD'),'')
		   FROM issue i
		   LEFT JOIN "user" a ON a.id = i.assignee_id
		   LEFT JOIN "user" c ON c.id = i.creator_id
		  WHERE i.workspace_id = $1::uuid AND i.metadata->>'zoho_task_id' = '9101'`, wsID,
	).Scan(&title, &status, &priority, &desc, &assignee, &creator, &due); err != nil {
		t.Fatalf("issue 9101: %v", err)
	}
	if title != "Chase overdue & escalate" || status != "in_progress" || priority != "high" ||
		assignee != migAliceEmail || creator != migBobEmail || due != "2026-09-30" {
		t.Errorf("issue 9101 = %q %s %s assignee=%s creator=%s due=%s", title, status, priority, assignee, creator, due)
	}
	if desc != "Call daily\nsee [doc](https://x.io/d)" {
		t.Errorf("description = %q", desc)
	}
	var closedAssignee string
	testPool.QueryRow(ctx,
		`SELECT status || '/' || COALESCE(assignee_id::text,'none') FROM issue
		  WHERE workspace_id = $1::uuid AND metadata->>'zoho_task_id' = '9102'`, wsID).Scan(&closedAssignee)
	if closedAssignee != "done/none" {
		t.Errorf("issue 9102 = %q, want done/none (gmail owner stays unassigned)", closedAssignee)
	}

	// The operator co-owns the workspace, so the owner-only visibility gate
	// shows them every migrated issue.
	lw := httptest.NewRecorder()
	lreq := newRequest("GET", "/api/issues?limit=50", nil)
	lreq.Header.Set("X-Workspace-ID", wsID)
	testHandler.ListIssues(lw, lreq)
	var listed struct {
		Total int `json:"total"`
	}
	json.Unmarshal(lw.Body.Bytes(), &listed)
	if lw.Code != http.StatusOK || listed.Total != 2 {
		t.Errorf("operator issue list = %d total=%d, want 200 with 2", lw.Code, listed.Total)
	}

	// --- re-run is idempotent ---
	code, again := runMigrate(t, body)
	if code != http.StatusAccepted || again.Projects[0].Action != "reuse" || again.Projects[0].WorkspaceID != wsID {
		t.Fatalf("re-run = %d %+v", code, again.Projects)
	}
	time.Sleep(time.Second)
	testPool.QueryRow(ctx, `SELECT count(*) FROM issue WHERE workspace_id = $1::uuid`, wsID).Scan(&issues)
	var comments int
	testPool.QueryRow(ctx, `SELECT count(*) FROM comment WHERE workspace_id = $1::uuid`, wsID).Scan(&comments)
	if issues != 2 || comments != 1 {
		t.Errorf("after re-run issues=%d comments=%d, want 2 and 1", issues, comments)
	}
}

// TestZohoMigrateUsesProjectRoles: with the users scope, Zoho project roles map
// onto Agora roles (manager → admin, employee → member).
func TestZohoMigrateUsesProjectRoles(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	mock := newMigrateMock(t)
	mock.usersReadable = true
	configureZohoEnv(t, mock.srv.URL)
	t.Setenv("AGORA_ZOHO_MIGRATE", "1")
	cleanupMigrateFixtures(t)

	code, plan := runMigrate(t, map[string]any{"dry_run": true, "allowed_email_domains": []string{migDomain}})
	if code != http.StatusOK || len(plan.Projects) != 1 {
		t.Fatalf("dry run = %d %+v", code, plan.Projects)
	}
	if plan.MembershipSource != "project_users" {
		t.Errorf("membership_source = %q", plan.MembershipSource)
	}
	people := plan.Projects[0].People
	for email, role := range map[string]string{migOwnerEmail: "owner", migCarolEmail: "admin", migAliceEmail: "member"} {
		if got := personByEmail(people, email); got == nil || got.Role != role {
			t.Errorf("person %s = %+v, want role %s", email, got, role)
		}
	}
}

// TestZohoMigrateEmailAliases: an aliased address folds into the main account —
// it is not planned as its own person and its tasks resolve to the main user.
func TestZohoMigrateEmailAliases(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	mock := newMigrateMock(t)
	configureZohoEnv(t, mock.srv.URL)
	t.Setenv("AGORA_ZOHO_MIGRATE", "1")
	cleanupMigrateFixtures(t)

	code, plan := runMigrate(t, map[string]any{
		"dry_run":               true,
		"allowed_email_domains": []string{migDomain},
		"email_aliases":         map[string]string{"BOB@" + migDomain: handlerTestEmail},
	})
	if code != http.StatusOK || len(plan.Projects) != 1 {
		t.Fatalf("dry run = %d %+v", code, plan.Projects)
	}
	people := plan.Projects[0].People
	if personByEmail(people, migBobEmail) != nil {
		t.Errorf("aliased address must not be planned as its own person: %+v", people)
	}
	if me := personByEmail(people, handlerTestEmail); me == nil || me.Role != "owner" {
		t.Errorf("alias target = %+v, want the operator as owner", me)
	}

	policy := newZohoMigratePolicy([]string{migDomain}, map[string]string{"bob@" + migDomain: handlerTestEmail})
	if got := policy.canonical("  Bob@" + migDomain); got != handlerTestEmail {
		t.Errorf("canonical = %q", got)
	}
}
