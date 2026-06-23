package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/util"
)

// newZohoSprintsHandlerMock serves the Zoho Sprints column-format endpoints for
// the handler import test: 1 project, 1 sprint (with dates), backlog with a
// parent item + child item.
func newZohoSprintsHandlerMock(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path
		q := r.URL.Query()
		switch {
		case strings.Contains(p, "/oauth/v2/token"):
			io.WriteString(w, `{"access_token":"tok","expires_in":3600}`)
		case strings.HasSuffix(p, "/teams/"):
			io.WriteString(w, `{"portals":[{"zsoid":"890735457","teamName":"octane"}]}`)
		case strings.HasSuffix(p, "/projects/") && q.Get("action") == "allprojects":
			io.WriteString(w, `{"project_prop":{"projName":0,"startDate":2,"endDate":3},
				"projectIds":["P1"],"projectJObj":{"P1":["RnD / CRM Department","9","-1","-1"]}}`)
		case strings.HasSuffix(p, "/sprints/") && q.Get("action") == "data":
			io.WriteString(w, `{"sprint_prop":{"sprintName":0,"startDate":1,"endDate":2,"duration":4,"sprintNo":10},
				"sprintIds":["S1"],"sprintJObj":{"S1":[null,"2025-11-17T05:00:00.000Z","2025-12-01T04:59:59.999Z","x","14d","x","x","x","x","x","3"]}}`)
		case strings.HasSuffix(p, "/itemstatus/"):
			io.WriteString(w, `{"status_prop":{"statusName":0,"statusDescription":2},
				"statusIds":["ST2","ST3"],"statusJObj":{"ST2":["In progress",true,"Doing"],"ST3":["Completed",false,"Done"]}}`)
		case q.Get("action") == "getbacklog":
			io.WriteString(w, `{"backlogId":"BL1","status":"success"}`)
		case strings.Contains(p, "/item/"):
			io.WriteString(w, `{"item_prop":{"itemName":0,"description":1,"itemNo":2,"ownerId":3,"statusId":4,"parentItem":5,"sprintId":6,"points":7},
				"itemIds":["C1","C2"],"itemJObj":{
					"C1":["Parent item","d","1",["U1"],"ST2","-1","BL1","0"],
					"C2":["Child item","","2",["U1"],"ST3","C1","BL1","0"]}}`)
		default:
			io.WriteString(w, `{"status":"success"}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func configureZohoSprintsEnv(t *testing.T, mockURL string) {
	t.Helper()
	t.Setenv("ZOHO_SPRINTS_CLIENT_ID", "c")
	t.Setenv("ZOHO_SPRINTS_CLIENT_SECRET", "s")
	t.Setenv("ZOHO_SPRINTS_REFRESH_TOKEN", "r")
	t.Setenv("ZOHO_SPRINTS_TEAM", "890735457")
	t.Setenv("ZOHO_SPRINTS_ACCOUNTS_HOST", mockURL)
	t.Setenv("ZOHO_SPRINTS_API_HOST", mockURL)
}

func cleanupZohoSprintsFixtures(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		testPool.Exec(ctx, `DELETE FROM issue WHERE workspace_id=$1::uuid AND metadata ? 'zoho_sprint_item_id'`, testWorkspaceID)
		testPool.Exec(ctx, `DELETE FROM sprint WHERE workspace_id=$1::uuid AND goal LIKE '%zsprint:%'`, testWorkspaceID)
		testPool.Exec(ctx, `DELETE FROM project WHERE workspace_id=$1::uuid AND description LIKE '%zoho_sprints_project:%'`, testWorkspaceID)
	})
}

func issueByZohoSprintsItemID(t *testing.T, itemID string) (id, status, parent string, count int) {
	t.Helper()
	rows, err := testPool.Query(context.Background(),
		`SELECT id::text, status, COALESCE(parent_issue_id::text,'')
		   FROM issue WHERE workspace_id=$1::uuid AND metadata @> ('{"zoho_sprint_item_id":"'||$2||'"}')::jsonb
		  ORDER BY created_at ASC`,
		testWorkspaceID, itemID)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		count++
		if count == 1 {
			rows.Scan(&id, &status, &parent)
		}
	}
	return id, status, parent, count
}

// TestZohoSprintsImport: a Sprints project imports into its own "(Sprints)" Agora
// project, a sprint is created with its real dates, items become issues, and a
// child item is parented under its parent item's issue.
func TestZohoSprintsImport(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	mock := newZohoSprintsHandlerMock(t)
	configureZohoSprintsEnv(t, mock.URL)
	cleanupZohoSprintsFixtures(t)

	wsUUID, _ := util.ParseUUID(testWorkspaceID)
	st := testHandler.newZohoSprintsSyncState()
	if err := testHandler.syncZohoSprintsProject(context.Background(), wsUUID, "P1", st); err != nil {
		t.Fatalf("syncZohoSprintsProject: %v", err)
	}

	// Project created, suffixed "(Sprints)".
	var projTitle string
	if err := testPool.QueryRow(context.Background(),
		`SELECT title FROM project WHERE workspace_id=$1::uuid AND description LIKE '%zoho_sprints_project:P1%'`,
		testWorkspaceID).Scan(&projTitle); err != nil {
		t.Fatalf("project query: %v", err)
	}
	if !strings.Contains(projTitle, "(Sprints)") {
		t.Errorf("project title = %q, want a (Sprints) suffix", projTitle)
	}

	// Sprint created with a real start date.
	var sprintStart string
	if err := testPool.QueryRow(context.Background(),
		`SELECT COALESCE(start_date::text,'') FROM sprint WHERE workspace_id=$1::uuid AND goal LIKE '%zsprint:S1%'`,
		testWorkspaceID).Scan(&sprintStart); err != nil {
		t.Fatalf("sprint query: %v", err)
	}
	if !strings.HasPrefix(sprintStart, "2025-11-17") {
		t.Errorf("sprint start_date = %q, want 2025-11-17...", sprintStart)
	}

	// Items → issues with mapped status.
	parentID, pStatus, _, pc := issueByZohoSprintsItemID(t, "C1")
	if pc != 1 || pStatus != "in_progress" {
		t.Fatalf("parent issue: count=%d status=%q", pc, pStatus)
	}
	childID, cStatus, childParent, cc := issueByZohoSprintsItemID(t, "C2")
	if cc != 1 || cStatus != "done" {
		t.Fatalf("child issue: count=%d status=%q", cc, cStatus)
	}
	if childID == "" || childParent != parentID {
		t.Errorf("child parent_issue_id = %q, want %q", childParent, parentID)
	}
}
