package zohosprints

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newSprintsMock serves the Zoho Sprints column-oriented (prop / Ids / JObj)
// responses the client decodes, plus the OAuth token grant.
func newSprintsMock(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path
		switch {
		case strings.Contains(p, "/oauth/v2/token"):
			io.WriteString(w, `{"access_token":"tok","expires_in":3600}`)
		case strings.HasSuffix(p, "/teams/"):
			io.WriteString(w, `{"portals":[{"zsoid":"890735457","teamName":"octane"}]}`)
		case strings.HasSuffix(p, "/projects/") && r.URL.Query().Get("action") == "allprojects":
			io.WriteString(w, `{"project_prop":{"projName":0,"projNo":1,"startDate":2,"endDate":3},
				"projectIds":["P1","P2"],
				"projectJObj":{
					"P1":["RnD / CRM Department","9","2025-11-01T05:00:00.000Z","-1"],
					"P2":["DGO scope of work","10","-1","-1"]}}`)
		case strings.HasSuffix(p, "/sprints/") && r.URL.Query().Get("action") == "data":
			// sprintName null, sprintNo at 10, dates at 1/2, duration at 4
			io.WriteString(w, `{"sprint_prop":{"sprintName":0,"startDate":1,"endDate":2,"duration":4,"sprintNo":10},
				"sprintIds":["S1"],
				"sprintJObj":{"S1":[null,"2025-11-17T05:00:00.000Z","2025-12-01T04:59:59.999Z","x","14d","x","x","x","x","x","3"]}}`)
		case strings.HasSuffix(p, "/itemstatus/"):
			io.WriteString(w, `{"status_prop":{"statusName":0,"statusDescription":2},
				"statusIds":["ST1","ST2","ST3"],
				"statusJObj":{
					"ST1":["Open",false,"To do"],
					"ST2":["In progress",true,"Doing"],
					"ST3":["Completed",false,"Done"]}}`)
		case strings.Contains(p, "/?") || strings.HasSuffix(p, "/"):
			if r.URL.Query().Get("action") == "getbacklog" {
				io.WriteString(w, `{"backlogId":"BL1","status":"success"}`)
				return
			}
			if strings.Contains(p, "/item/") {
				// two items: parent C1 and child C2 (parentItem=C1)
				io.WriteString(w, `{"item_prop":{"itemName":0,"description":1,"itemNo":2,"ownerId":3,"statusId":4,"parentItem":5,"sprintId":6,"points":7},
					"itemIds":["C1","C2"],
					"itemJObj":{
						"C1":["Parent item","desc","1",["U1","U2"],"ST2","-1","BL1","0"],
						"C2":["Child item","","2",["U1"],"ST3","C1","BL1","0"]}}`)
				return
			}
			io.WriteString(w, `{"status":"success"}`)
		default:
			io.WriteString(w, `{"status":"success"}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func mockClient(srv *httptest.Server) *Client {
	return NewClient(Config{ClientID: "c", ClientSecret: "s", RefreshToken: "r",
		AccountsHost: srv.URL, APIHost: srv.URL})
}

func TestMapStatus(t *testing.T) {
	cases := []struct{ name, bucket, want string }{
		{"Open", "To do", StatusTodo},
		{"In progress", "Doing", StatusInProgress},
		{"In Review", "To do", StatusInReview},
		{"To be Tested", "To do", StatusInReview},
		{"Completed", "Done", StatusDone},
		{"Closed", "Done", StatusDone},
		{"Cancelled", "Done", StatusCancelled},
		{"Backlog", "To do", StatusBacklog},
		{"Fantasy / Ideas", "To do", StatusBacklog},
		{"Recurring", "To do", StatusBacklog},
		{"Reopen", "To do", StatusTodo},
		{"", "Doing", StatusInProgress}, // bucket fallback
		{"", "Done", StatusDone},
		{"Weird Custom", "", StatusTodo}, // default
	}
	for _, c := range cases {
		if got := MapStatus(c.name, c.bucket); got != c.want {
			t.Errorf("MapStatus(%q,%q) = %q, want %q", c.name, c.bucket, got, c.want)
		}
	}
}

func TestParseZohoDate(t *testing.T) {
	if _, ok := ParseZohoDate("-1"); ok {
		t.Error("-1 should be unset")
	}
	if _, ok := ParseZohoDate(""); ok {
		t.Error("empty should be unset")
	}
	tm, ok := ParseZohoDate("2025-11-17T05:00:00.000Z")
	if !ok || tm.Year() != 2025 || tm.Month() != 11 || tm.Day() != 17 {
		t.Errorf("parse = %v ok=%v", tm, ok)
	}
}

func TestIsParentRef(t *testing.T) {
	for _, s := range []string{"", "-1", "0", "  "} {
		if IsParentRef(s) {
			t.Errorf("IsParentRef(%q) = true, want false", s)
		}
	}
	if !IsParentRef("C1") {
		t.Error("IsParentRef(C1) = false, want true")
	}
}

func TestClientDecode(t *testing.T) {
	srv := newSprintsMock(t)
	c := mockClient(srv)
	ctx := context.Background()

	team, err := c.ResolveTeamID(ctx)
	if err != nil || team != "890735457" {
		t.Fatalf("ResolveTeamID = %q, %v", team, err)
	}

	projects, err := c.ListProjects(ctx, team)
	if err != nil || len(projects) != 2 {
		t.Fatalf("ListProjects = %+v, %v", projects, err)
	}
	if projects[0].Name != "RnD / CRM Department" || projects[0].StartDate != "2025-11-01T05:00:00.000Z" {
		t.Errorf("project0 = %+v", projects[0])
	}

	sprints, err := c.ListSprints(ctx, team, "P1")
	if err != nil || len(sprints) != 1 {
		t.Fatalf("ListSprints = %+v, %v", sprints, err)
	}
	if sprints[0].No != "3" || sprints[0].StartDate != "2025-11-17T05:00:00.000Z" || sprints[0].Name != "" {
		t.Errorf("sprint0 = %+v", sprints[0])
	}

	statuses, err := c.ListItemStatuses(ctx, team, "P1")
	if err != nil || len(statuses) != 3 {
		t.Fatalf("statuses = %+v, %v", statuses, err)
	}
	if statuses["ST2"].Name != "In progress" || statuses["ST2"].Bucket != "Doing" {
		t.Errorf("ST2 = %+v", statuses["ST2"])
	}

	backlog, err := c.BacklogID(ctx, team, "P1")
	if err != nil || backlog != "BL1" {
		t.Fatalf("BacklogID = %q, %v", backlog, err)
	}

	items, err := c.ListItems(ctx, team, "P1", "BL1")
	if err != nil || len(items) != 2 {
		t.Fatalf("ListItems = %+v, %v", items, err)
	}
	parent, child := items[0], items[1]
	if parent.Name != "Parent item" || len(parent.OwnerIDs) != 2 || parent.OwnerIDs[1] != "U2" {
		t.Errorf("parent = %+v", parent)
	}
	if child.ParentID != "C1" || child.StatusID != "ST3" || !IsParentRef(child.ParentID) {
		t.Errorf("child = %+v", child)
	}
	// End-to-end status resolution for the child item.
	draft := MapItemToIssue(&child, statuses)
	if draft.Status != StatusDone || draft.Title != "Child item" {
		t.Errorf("child draft = %+v", draft)
	}
}
