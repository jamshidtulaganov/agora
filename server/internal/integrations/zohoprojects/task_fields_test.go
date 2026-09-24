package zohoprojects

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMapRole(t *testing.T) {
	cases := map[string]string{
		"admin":         "admin",
		"Administrator": "admin",
		"manager":       "admin",
		"employee":      "member",
		"contractor":    "member",
		"client":        "member",
		"":              "member",
		"something-new": "member",
	}
	for in, want := range cases {
		if got := MapRole(in); got != want {
			t.Errorf("MapRole(%q) = %q, want %q", in, got, want)
		}
	}
}

// Real Octane portal labels: several teams set "Completed"/"Done" with status
// type open, so the label (not the type) decides.
func TestMapStatusOctaneLabels(t *testing.T) {
	cases := []struct{ name, typ, want string }{
		{"Completed", "open", StatusDone},
		{"Done", "open", StatusDone},
		{"Action = done", "open", StatusDone},
		{"To be Tested", "open", StatusInReview},
		{"TODAY", "open", StatusTodo},
		{"Cancelled", "closed", StatusCancelled},
		{"Closed", "closed", StatusDone},
	}
	for _, c := range cases {
		if got := MapStatusWithType(c.name, c.typ); got != c.want {
			t.Errorf("MapStatusWithType(%q,%q) = %q, want %q", c.name, c.typ, got, c.want)
		}
	}
}

func TestMapPriority(t *testing.T) {
	cases := map[string]string{"High": "high", "medium": "medium", "Low": "low", "None": "none", "": "none", "Urgent!": "none"}
	for in, want := range cases {
		if got := MapPriority(in); got != want {
			t.Errorf("MapPriority(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseDate(t *testing.T) {
	if d, ok := ParseDate("09-30-2026"); !ok || d.Format("2006-01-02") != "2026-09-30" {
		t.Errorf("ParseDate(09-30-2026) = %v %v", d, ok)
	}
	for _, bad := range []string{"", "2026-09-30", "30/09/2026"} {
		if _, ok := ParseDate(bad); ok {
			t.Errorf("ParseDate(%q) should fail", bad)
		}
	}
}

func TestHTMLToText(t *testing.T) {
	cases := map[string]string{
		`<div><br /></div>`: "",
		`<div style="font-size:0.9285rem"><div><br/></div></div>`:                         "",
		`<div>Collections &amp; recovery</div>`:                                           "Collections & recovery",
		`<div>line one<br>line two</div>`:                                                 "line one\nline two",
		`<a target="_blank" href="https://sheet.zoho.com/x">https://sheet.zoho.com/x</a>`: "https://sheet.zoho.com/x",
		`see <a href="https://x.io">the doc</a>`:                                          "see [the doc](https://x.io)",
		`<ul><li>one</li><li>two</li></ul>`:                                               "- one\n- two",
		`<p>a</p><p></p><p></p><p>b</p>`:                                                  "a\n\nb",
	}
	for in, want := range cases {
		if got := HTMLToText(in); got != want {
			t.Errorf("HTMLToText(%q) = %q, want %q", in, got, want)
		}
	}
}

func newFieldsServer(t *testing.T, usersStatus int) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case strings.Contains(path, "/oauth/v2/token"):
			io.WriteString(w, `{"access_token":"tok","expires_in":3600}`)
		case strings.HasSuffix(path, "/users/"):
			if usersStatus != http.StatusOK {
				w.WriteHeader(usersStatus)
				io.WriteString(w, `{"error":{"code":6403,"message":"Invalid OAuth scope."}}`)
				return
			}
			io.WriteString(w, `{"users":[
				{"id":"11","name":"Zeyba","email":"zeyba.i@octanefuel.com","role":"manager","active":true},
				{"id":"12","name":"Gone","email":"gone@tsst.ai","role":"employee","active":false},
				{"id":"13","name":"No mail","role":"employee"}]}`)
		case strings.HasSuffix(path, "/tasks/"):
			io.WriteString(w, `{"tasks":[{"id":1,"id_string":"1","name":"Love&#39;s API &amp; CRM",
				"status":{"name":"Open","type":"open"},"priority":"High",
				"start_date":"09-01-2026","end_date":"09-30-2026","is_comment_added":false,
				"created_by_email":"dina.c@tsst.ai","created_by_full_name":"Dina Carter","created_by_zpuid":"77",
				"details":{"owners":[{"name":"Unassigned"},{"zpuid":5,"name":"Ann","email":"ann@tsst.ai"}]}}]}`)
		case strings.HasSuffix(path, "/projects/"):
			io.WriteString(w, `{"projects":[{"id":1,"id_string":"2494","key":"OCT-33","name":"Collections &amp; Recovery",
				"status":"active","custom_status_name":"In Progress",
				"owner_email":"zeyba.i@octanefuel.com","owner_name":"Zeyba Ildarova","owner_zpuid":"88"}]}`)
		}
	}))
	t.Cleanup(srv.Close)
	return NewClient(Config{ClientID: "c", ClientSecret: "s", RefreshToken: "r", AccountsHost: srv.URL, APIHost: srv.URL})
}

func TestClientParsesTaskFields(t *testing.T) {
	c := newFieldsServer(t, http.StatusOK)
	tasks, err := c.ListTasks(context.Background(), "1", "2494", nil, "")
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ListTasks = %v, %v", tasks, err)
	}
	tk := tasks[0]
	if tk.Name != "Love's API & CRM" || tk.Priority != "High" || tk.StartDate != "09-01-2026" || tk.EndDate != "09-30-2026" {
		t.Errorf("task = %+v", tk)
	}
	if !tk.NoComments {
		t.Errorf("is_comment_added=false should set NoComments")
	}
	if len(tk.Owners) != 1 || tk.Owners[0].Email != "ann@tsst.ai" {
		t.Errorf("owners = %+v, want only ann (Unassigned dropped)", tk.Owners)
	}
	if tk.Creator.Email != "dina.c@tsst.ai" || tk.Creator.Name != "Dina Carter" || tk.Creator.ID != "77" {
		t.Errorf("creator = %+v", tk.Creator)
	}
}

func TestTasklistLabel(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"General", "", false},
		{"A quick way to get started!", "", false},
		{"Missing Modules", "", false},
		{"Sprint 7", "", false},
		{"", "", false},
		{"Preparing", "Preparing", true},
		{"  Customer   Service ", "Customer Service", true},
		{"Octane &amp; Fuel", "Octane & Fuel", true},
		{"Провести расчет эффективности и целесообразности наличия международных офисов", "Провести расчет…", true},
	}
	for _, c := range cases {
		got, ok := TasklistLabel(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("TasklistLabel(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
		if len(got) > maxLabelBytes {
			t.Errorf("TasklistLabel(%q) = %d bytes, over the label limit", c.in, len(got))
		}
	}
}
