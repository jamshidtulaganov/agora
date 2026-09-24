// Package zohofake is a small in-memory stand-in for Zoho's accounts, CRM and
// Desk APIs, used by tests and by `go run ./cmd/fakezoho` for local
// development. It exists to prove one thing end to end: every read goes out
// with the person's own token, and each person gets back only what their
// Zoho role lets them see.
//
// Two people are built in:
//
//   - dilnoza — Collections Manager, Manager profile: every deal, tickets in
//     Collections and Billing.
//   - shohruh — Collections Agent, Standard profile: only the deals he owns,
//     tickets in Collections only.
//
// Only the endpoints Agora calls are implemented, in the shapes Agora's
// clients read. Writes are not implemented at all: any non-GET to a record
// endpoint (other than the read-only COQL POST) answers 405, so a test that
// accidentally writes fails loudly.
package zohofake

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// User is one fake Zoho person.
type User struct {
	Key         string
	Name        string
	Email       string
	CRMID       string
	Role        string
	Profile     string
	SeeAllDeals bool
	DeskAgentID string
	Departments []string // Desk department ids
}

// Users are the built-in people, in display order.
var Users = []User{
	{Key: "dilnoza", Name: "Dilnoza K.", Email: "dilnoza.k@octane-example.com", CRMID: "9001", Role: "Collections Manager", Profile: "Manager", SeeAllDeals: true, DeskAgentID: "7001", Departments: []string{"501", "502"}},
	{Key: "shohruh", Name: "Shohruh A.", Email: "shohruh.a@octane-example.com", CRMID: "9002", Role: "Collections Agent", Profile: "Standard", DeskAgentID: "7002", Departments: []string{"501"}},
}

const (
	// OrgID is the single fake Desk organization.
	OrgID = "600100"
	// ClientID / ClientSecret are what the fake accepts.
	ClientID     = "1000.FAKEZOHOCLIENT"
	ClientSecret = "fake-zoho-secret"
)

var departments = []map[string]any{
	{"id": "501", "name": "Collections", "isEnabled": true},
	{"id": "502", "name": "Billing", "isEnabled": true},
}

func deals() []map[string]any {
	owner := func(u User) map[string]any { return map[string]any{"id": u.CRMID, "name": u.Name, "email": u.Email} }
	d, s := Users[0], Users[1]
	return []map[string]any{
		{"id": "3001", "Deal_Name": "Trans Union settlement", "Stage": "Negotiation", "Amount": 12500, "Owner": owner(s)},
		{"id": "3002", "Deal_Name": "Fleet card recovery — Midwest", "Stage": "Qualification", "Amount": 8400, "Owner": owner(s)},
		{"id": "3003", "Deal_Name": "Write-off review Q3", "Stage": "Closed Won", "Amount": 30200, "Owner": owner(d)},
		{"id": "3004", "Deal_Name": "Legal small-claims batch", "Stage": "Negotiation", "Amount": 5600, "Owner": owner(d)},
		{"id": "3005", "Deal_Name": "Carrier payment plan", "Stage": "Proposal", "Amount": 19900, "Owner": owner(d)},
	}
}

func tickets() []map[string]any {
	d, s := Users[0], Users[1]
	assignee := func(u User) map[string]any {
		first, last, _ := strings.Cut(u.Name, " ")
		return map[string]any{"id": u.DeskAgentID, "firstName": first, "lastName": last, "email": u.Email}
	}
	t := func(id, number, subject, status, dept string, who User, modified string) map[string]any {
		statusType := "Open"
		if status == "Closed" {
			statusType = "Closed"
		} else if status == "On Hold" {
			statusType = "On Hold"
		}
		return map[string]any{
			"id": id, "ticketNumber": number, "subject": subject, "status": status, "statusType": statusType,
			"priority": "Medium", "channel": "Email", "departmentId": dept, "assigneeId": who.DeskAgentID,
			"email": "customer" + number + "@example.com", "createdTime": "2026-09-20T09:00:00.000Z",
			"modifiedTime": modified, "dueDate": "2026-09-30T17:00:00.000Z",
			"webUrl":      "https://desk.zoho.com/agent/octane/tickets/details/" + id,
			"description": "Customer asks about " + strings.ToLower(subject) + ".",
			"assignee":    assignee(who),
			"contact":     map[string]any{"id": "c" + number, "firstName": "Customer", "lastName": number, "email": "customer" + number + "@example.com"},
		}
	}
	return []map[string]any{
		t("8101", "4821", "Dispute on fuel card charge", "Open", "501", s, "2026-09-24T12:00:00.000Z"),
		t("8102", "4822", "Payment plan request", "On Hold", "501", s, "2026-09-23T12:00:00.000Z"),
		t("8103", "4823", "Chargeback evidence needed", "Open", "501", d, "2026-09-22T12:00:00.000Z"),
		t("8104", "4824", "Invoice copy for September", "Closed", "502", d, "2026-09-21T12:00:00.000Z"),
		t("8105", "4825", "Refund status question", "Open", "502", d, "2026-09-25T08:00:00.000Z"),
	}
}

// Server is the fake. Build it with New and serve it with ServeHTTP.
type Server struct {
	// BaseURL is the externally reachable base of this server; it is sent
	// back to the redirect as accounts-server, like real Zoho does.
	BaseURL string

	mu       sync.Mutex
	tokenSeq int
	revoked  map[string]bool
	// Calls records "METHOD path user" for every API call, for tests.
	Calls []string
}

// New returns a fake with no calls recorded.
func New(baseURL string) *Server {
	return &Server{BaseURL: strings.TrimRight(baseURL, "/"), revoked: map[string]bool{}}
}

// UserByKey finds a built-in person.
func UserByKey(key string) (User, bool) {
	for _, u := range Users {
		if u.Key == key {
			return u, true
		}
	}
	return User{}, false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) record(r *http.Request, u User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calls = append(s.Calls, r.Method+" "+r.URL.Path+" "+u.Key)
}

// ServeHTTP routes one request.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/oauth/v2/auth":
		s.authorize(w, r)
	case r.URL.Path == "/oauth/v2/token":
		s.token(w, r)
	case r.URL.Path == "/oauth/v2/token/revoke":
		s.revoke(w, r)
	case strings.HasPrefix(r.URL.Path, "/crm/v8/"):
		s.crm(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/"):
		s.desk(w, r)
	default:
		http.NotFound(w, r)
	}
}

// authorize renders a consent page: one button per person.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("client_id") != ClientID {
		http.Error(w, "unknown client_id", http.StatusBadRequest)
		return
	}
	redirect := q.Get("redirect_uri")
	if redirect == "" {
		http.Error(w, "redirect_uri required", http.StatusBadRequest)
		return
	}
	var b strings.Builder
	b.WriteString(`<!doctype html><meta charset="utf-8"><title>Fake Zoho sign-in</title>`)
	b.WriteString(`<body style="font-family:system-ui;max-width:28rem;margin:4rem auto;line-height:1.5">`)
	b.WriteString(`<h1 style="font-size:1.25rem">Fake Zoho — sign in as</h1>`)
	b.WriteString(`<p style="color:#555">Agora is asking to read your Zoho CRM and Desk data: ` + html.EscapeString(q.Get("scope")) + `</p>`)
	for _, u := range Users {
		back := url.Values{
			"code":            {"code-" + u.Key},
			"state":           {q.Get("state")},
			"location":        {"us"},
			"accounts-server": {s.BaseURL},
		}
		fmt.Fprintf(&b, `<p><a data-user="%s" href="%s">%s — %s (%s)</a></p>`,
			html.EscapeString(u.Key), html.EscapeString(redirect+"?"+back.Encode()),
			html.EscapeString(u.Name), html.EscapeString(u.Role), html.EscapeString(u.Profile))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if r.Form.Get("client_id") != ClientID || r.Form.Get("client_secret") != ClientSecret {
		writeJSON(w, http.StatusOK, map[string]any{"error": "invalid_client"})
		return
	}
	s.mu.Lock()
	s.tokenSeq++
	seq := s.tokenSeq
	s.mu.Unlock()
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		key := strings.TrimPrefix(r.Form.Get("code"), "code-")
		if _, ok := UserByKey(key); !ok {
			writeJSON(w, http.StatusOK, map[string]any{"error": "invalid_code"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token":  fmt.Sprintf("at-%s-%d", key, seq),
			"refresh_token": "rt-" + key,
			"scope":         "ZohoCRM.modules.READ ZohoCRM.coql.READ ZohoCRM.settings.READ ZohoCRM.users.READ Desk.tickets.READ Desk.basic.READ",
			"api_domain":    s.BaseURL,
			"expires_in":    3600,
		})
	case "refresh_token":
		rt := r.Form.Get("refresh_token")
		s.mu.Lock()
		revoked := s.revoked[rt]
		s.mu.Unlock()
		key := strings.TrimPrefix(rt, "rt-")
		if _, ok := UserByKey(key); !ok || revoked {
			writeJSON(w, http.StatusOK, map[string]any{"error": "invalid_code"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"access_token": fmt.Sprintf("at-%s-%d", key, seq), "expires_in": 3600})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"error": "unsupported_grant_type"})
	}
}

func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.revoked[r.URL.Query().Get("token")] = true
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
}

// caller resolves the person from "Zoho-oauthtoken at-<key>-<n>".
func (s *Server) caller(r *http.Request) (User, bool) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Zoho-oauthtoken ")
	parts := strings.Split(tok, "-")
	if len(parts) != 3 || parts[0] != "at" {
		return User{}, false
	}
	return UserByKey(parts[1])
}

func unauthorized(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnauthorized, map[string]any{"code": "INVALID_TOKEN", "message": "invalid oauth token"})
}

func dealVisible(u User, deal map[string]any) bool {
	if u.SeeAllDeals {
		return true
	}
	owner, _ := deal["Owner"].(map[string]any)
	return owner["id"] == u.CRMID
}

var (
	coqlFrom  = regexp.MustCompile(`(?i)\bfrom\s+([A-Za-z0-9_]+)`)
	coqlWhere = regexp.MustCompile(`(?i)\bwhere\s+([A-Za-z0-9_]+)\s*=\s*'([^']*)'`)
	coqlLimit = regexp.MustCompile(`(?i)\blimit\s+(\d+)`)
	coqlCols  = regexp.MustCompile(`(?is)^\s*select\s+(.+?)\s+from\s`)
)

func (s *Server) crm(w http.ResponseWriter, r *http.Request) {
	u, ok := s.caller(r)
	if !ok {
		unauthorized(w)
		return
	}
	s.record(r, u)
	path := strings.TrimPrefix(r.URL.Path, "/crm/v8")
	switch {
	case path == "/users" && r.Method == http.MethodGet && r.URL.Query().Get("type") == "CurrentUser":
		writeJSON(w, http.StatusOK, map[string]any{"users": []map[string]any{{
			"id": u.CRMID, "full_name": u.Name, "email": u.Email,
			"role":    map[string]any{"id": "r-" + u.Key, "name": u.Role},
			"profile": map[string]any{"id": "p-" + u.Key, "name": u.Profile},
		}}})
	case path == "/settings/modules" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"modules": []map[string]any{
			{"api_name": "Deals", "module_name": "Deals", "singular_label": "Deal", "plural_label": "Deals", "generated_type": "default", "api_supported": true},
			{"api_name": "Collection_Cases", "module_name": "Collection_Cases", "singular_label": "Collection Case", "plural_label": "Collection Cases", "generated_type": "custom", "api_supported": true},
		}})
	case path == "/settings/fields" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"fields": []map[string]any{
			{"api_name": "Deal_Name", "field_label": "Deal Name", "data_type": "text"},
			{"api_name": "Stage", "field_label": "Stage", "data_type": "picklist", "pick_list_values": []map[string]any{
				{"display_value": "Qualification", "actual_value": "Qualification"},
				{"display_value": "Proposal", "actual_value": "Proposal"},
				{"display_value": "Negotiation", "actual_value": "Negotiation"},
				{"display_value": "Closed Won", "actual_value": "Closed Won"},
			}},
			{"api_name": "Amount", "field_label": "Amount", "data_type": "currency"},
			{"api_name": "Owner", "field_label": "Deal Owner", "data_type": "ownerlookup"},
		}})
	case path == "/coql" && r.Method == http.MethodPost:
		s.coql(w, r, u)
	case strings.HasPrefix(path, "/Deals/") && r.Method == http.MethodGet:
		id := strings.TrimPrefix(path, "/Deals/")
		for _, d := range deals() {
			if d["id"] == id {
				if !dealVisible(u, d) {
					writeJSON(w, http.StatusForbidden, map[string]any{"code": "NO_PERMISSION", "message": "permission denied"})
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]any{d}})
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"code": "NOT_SUPPORTED", "message": "the fake only answers reads"})
	}
}

func (s *Server) coql(w http.ResponseWriter, r *http.Request, u User) {
	var body struct {
		SelectQuery string `json:"select_query"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	q := body.SelectQuery
	from := coqlFrom.FindStringSubmatch(q)
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(q)), "select") || from == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "SYNTAX_ERROR", "message": "bad COQL"})
		return
	}
	if from[1] != "Deals" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var cols []string
	if m := coqlCols.FindStringSubmatch(q); m != nil {
		for _, c := range strings.Split(m[1], ",") {
			cols = append(cols, strings.TrimSpace(c))
		}
	}
	where := coqlWhere.FindStringSubmatch(q)
	limit := 200
	if m := coqlLimit.FindStringSubmatch(q); m != nil {
		limit, _ = strconv.Atoi(m[1])
	}
	var rows []map[string]any
	for _, d := range deals() {
		if !dealVisible(u, d) {
			continue
		}
		if where != nil && fmt.Sprint(d[where[1]]) != where[2] {
			continue
		}
		row := map[string]any{"id": d["id"]}
		for _, c := range cols {
			if v, ok := d[c]; ok {
				row[c] = v
			}
		}
		rows = append(rows, row)
		if len(rows) == limit {
			break
		}
	}
	if len(rows) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows, "info": map[string]any{"count": len(rows), "more_records": false}})
}

func (s *Server) deskVisible(u User, t map[string]any) bool {
	for _, d := range u.Departments {
		if t["departmentId"] == d {
			return true
		}
	}
	return false
}

func (s *Server) desk(w http.ResponseWriter, r *http.Request) {
	u, ok := s.caller(r)
	if !ok {
		unauthorized(w)
		return
	}
	s.record(r, u)
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"errorCode": "METHOD_NOT_ALLOWED", "message": "the fake only answers reads"})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	if path == "/organizations" {
		writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]any{{"id": OrgID, "companyName": "Octane (fake)", "portalName": "octane"}}})
		return
	}
	if r.Header.Get("orgId") != OrgID {
		writeJSON(w, http.StatusBadRequest, map[string]any{"errorCode": "INVALID_ORG", "message": "orgId header missing or wrong"})
		return
	}
	q := r.URL.Query()
	switch {
	case path == "/myinfo":
		first, last, _ := strings.Cut(u.Name, " ")
		writeJSON(w, http.StatusOK, map[string]any{
			"id": u.DeskAgentID, "firstName": first, "lastName": last, "name": u.Name, "emailId": u.Email,
			"roleId": "dr-" + u.Key, "profileId": "dp-" + u.Key, "associatedDepartmentIds": u.Departments,
		})
	case path == "/departments":
		var out []map[string]any
		for _, d := range departments {
			for _, mine := range u.Departments {
				if d["id"] == mine {
					out = append(out, d)
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": out})
	case path == "/tickets":
		var out []map[string]any
		for _, t := range tickets() {
			if !s.deskVisible(u, t) {
				continue
			}
			if v := q.Get("departmentId"); v != "" && t["departmentId"] != v {
				continue
			}
			if v := q.Get("assignee"); v != "" && t["assigneeId"] != v {
				continue
			}
			if v := q.Get("status"); v != "" && !containsFold(strings.Split(v, ","), fmt.Sprint(t["status"])) {
				continue
			}
			out = append(out, t)
		}
		sort.SliceStable(out, func(i, j int) bool { return fmt.Sprint(out[i]["modifiedTime"]) > fmt.Sprint(out[j]["modifiedTime"]) })
		if limit, err := strconv.Atoi(q.Get("limit")); err == nil && limit > 0 && len(out) > limit {
			out = out[:limit]
		}
		if len(out) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": out})
	case path == "/tickets/search":
		var out []map[string]any
		for _, t := range tickets() {
			if s.deskVisible(u, t) && t["ticketNumber"] == q.Get("ticketNumber") {
				out = append(out, t)
			}
		}
		if len(out) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": out, "count": len(out)})
	case strings.HasPrefix(path, "/tickets/"):
		rest := strings.TrimPrefix(path, "/tickets/")
		id, sub, _ := strings.Cut(rest, "/")
		for _, t := range tickets() {
			if t["id"] != id {
				continue
			}
			if !s.deskVisible(u, t) {
				writeJSON(w, http.StatusForbidden, map[string]any{"errorCode": "FORBIDDEN", "message": "You do not have permission to access this ticket"})
				return
			}
			if sub == "threads" {
				writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]any{
					{"id": id + "-2", "channel": "EMAIL", "direction": "out", "summary": "We've received your request and are looking into it.", "createdTime": "2026-09-24T10:00:00.000Z", "visibility": "public", "author": map[string]any{"name": fmt.Sprint(t["assignee"].(map[string]any)["firstName"]), "type": "AGENT"}},
					{"id": id + "-1", "channel": "EMAIL", "direction": "in", "summary": fmt.Sprint(t["description"]), "createdTime": "2026-09-20T09:00:00.000Z", "visibility": "public", "author": map[string]any{"name": "Customer", "type": "END_USER"}},
				}})
				return
			}
			writeJSON(w, http.StatusOK, t)
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]any{"errorCode": "URL_NOT_FOUND", "message": "ticket not found"})
	default:
		http.NotFound(w, r)
	}
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(strings.TrimSpace(x), v) {
			return true
		}
	}
	return false
}

// Revoked reports whether a refresh token was revoked through the fake.
func (s *Server) Revoked(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revoked[token]
}

// Revoke revokes a refresh token as if the person or an admin did it in Zoho.
func (s *Server) Revoke(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked[token] = true
}
