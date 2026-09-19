package linear

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/imports"
	"github.com/jamshidtulaganov/agora/server/internal/imports/importmem"
	"github.com/jamshidtulaganov/agora/server/internal/util"
)

// A recorded Linear GraphQL fixture, served by httptest, following
// zohoprojects_test.go's mock-host idiom. THE FIXTURE IS THE CONTRACT: this
// test never touches the network or a database, so everything it proves about
// the adapter is proved about the adapter alone.
//
// The workspace it describes is small but real-shaped:
//
//	2 teams (ENG, OPS) · 6 workflow states across all six Linear categories
//	2 labels · 2 users, one of whom is NOT a member of the target workspace
//	1 cycle · 4 issues arriving over TWO pages, one of them a child
//	3 comments (one by a bot actor) · 1 attachment
//	2 relations, one of which is Linear's `similar` and must degrade

const testAPIKey = "lin_api_TESTONLYxxxxxxxxxxxxxxxx"

const fixtureViewer = `{
  "viewer": {"id":"usr-kim","name":"Kim Ryu","email":"kim@acme.io"},
  "organization": {"id":"org-1","name":"Acme","urlKey":"acme"}
}`

const fixtureTeams = `{
  "teams": {
    "pageInfo": {"hasNextPage": false, "endCursor": ""},
    "nodes": [
      {"id":"team-eng","key":"ENG","name":"Engineering","description":"Product engineering","icon":"Rocket","archivedAt":null},
      {"id":"team-ops","key":"OPS","name":"Operations","description":"","icon":null,"archivedAt":null}
    ]
  }
}`

const fixtureStates = `{
  "workflowStates": {
    "pageInfo": {"hasNextPage": false, "endCursor": ""},
    "nodes": [
      {"id":"st-triage","name":"Triage","type":"triage","position":0,"team":{"id":"team-eng"}},
      {"id":"st-backlog","name":"Backlog","type":"backlog","position":1,"team":{"id":"team-eng"}},
      {"id":"st-todo","name":"Todo","type":"unstarted","position":2,"team":{"id":"team-eng"}},
      {"id":"st-progress","name":"In Progress","type":"started","position":3,"team":{"id":"team-eng"}},
      {"id":"st-review","name":"In Review","type":"started","position":4,"team":{"id":"team-eng"}},
      {"id":"st-done","name":"Done","type":"completed","position":5,"team":{"id":"team-eng"}},
      {"id":"st-cancel","name":"Canceled","type":"canceled","position":6,"team":{"id":"team-eng"}},
      {"id":"st-ops-done","name":"Shipped","type":"completed","position":1,"team":{"id":"team-ops"}},
      {"id":"st-sec","name":"Hidden","type":"unstarted","position":1,"team":{"id":"team-sec"}}
    ]
  }
}`

const fixtureLabels = `{
  "issueLabels": {
    "pageInfo": {"hasNextPage": false, "endCursor": ""},
    "nodes": [
      {"id":"lab-bug","name":"Bug","color":"#ef4444"},
      {"id":"lab-ui","name":"UI","color":"#3b82f6"}
    ]
  }
}`

const fixtureUsers = `{
  "users": {
    "pageInfo": {"hasNextPage": false, "endCursor": ""},
    "nodes": [
      {"id":"usr-kim","name":"Kim Ryu","email":"kim@acme.io","active":true},
      {"id":"usr-dana","name":"Dana Wu","email":"dana@gone.example","active":false}
    ]
  }
}`

const fixtureCycles = `{
  "cycles": {
    "pageInfo": {"hasNextPage": false, "endCursor": ""},
    "nodes": [
      {"id":"cyc-12","number":12,"name":"","startsAt":"2025-10-06T00:00:00.000Z","endsAt":"2025-10-20T00:00:00.000Z","completedAt":"2025-10-20T00:00:00.000Z","team":{"id":"team-eng"}},
      {"id":"cyc-sec","number":3,"name":"Security 3","startsAt":null,"endsAt":null,"completedAt":null,"team":{"id":"team-sec"}}
    ]
  }
}`

// Page one of the issue connection. hasNextPage proves the adapter follows the
// cursor rather than stopping at the first page.
const fixtureIssuesPage1 = `{
  "issues": {
    "pageInfo": {"hasNextPage": true, "endCursor": "cursor-page-2"},
    "nodes": [
      {
        "id":"iss-142","identifier":"ENG-142","number":142,
        "title":"Fix the login redirect loop","description":"The redirect **loops** on SSO.",
        "priority":2,"estimate":3,"url":"https://linear.app/acme/issue/ENG-142",
        "createdAt":"2024-03-07T09:30:00.000Z","updatedAt":"2025-11-02T16:05:00.000Z",
        "startedAt":"2024-03-08T10:00:00.000Z","completedAt":null,"dueDate":"2025-12-01","archivedAt":null,
        "state":{"id":"st-review"},"team":{"id":"team-eng"},"cycle":{"id":"cyc-12"},
        "parent":null,"assignee":{"id":"usr-kim"},"creator":{"id":"usr-kim"},
        "project":{"id":"prj-auth","name":"Auth hardening"},
        "labels":{"nodes":[{"id":"lab-bug","name":"Bug","color":"#ef4444"}]}
      },
      {
        "id":"iss-143","identifier":"ENG-143","number":143,
        "title":"Rotate the session key","description":"",
        "priority":1,"estimate":null,"url":"https://linear.app/acme/issue/ENG-143",
        "createdAt":"2024-04-01T08:00:00.000Z","updatedAt":"2024-04-02T08:00:00.000Z",
        "startedAt":null,"completedAt":null,"dueDate":null,"archivedAt":null,
        "state":{"id":"st-todo"},"team":{"id":"team-eng"},"cycle":null,
        "parent":null,"assignee":null,"creator":{"id":"usr-dana"},
        "project":null,
        "labels":{"nodes":[]}
      }
    ]
  }
}`

// Page two carries the child (declared after its parent here, but the applier's
// topological pass must not depend on that) and an issue in the second team.
const fixtureIssuesPage2 = `{
  "issues": {
    "pageInfo": {"hasNextPage": false, "endCursor": ""},
    "nodes": [
      {
        "id":"iss-144","identifier":"ENG-144","number":144,
        "title":"Invalidate old sessions","description":"Child of ENG-143.",
        "priority":3,"estimate":null,"url":"https://linear.app/acme/issue/ENG-144",
        "createdAt":"2024-04-03T08:00:00.000Z","updatedAt":"2024-04-03T08:00:00.000Z",
        "startedAt":null,"completedAt":null,"dueDate":null,"archivedAt":null,
        "state":{"id":"st-progress"},"team":{"id":"team-eng"},"cycle":{"id":"cyc-12"},
        "parent":{"id":"iss-143"},"assignee":{"id":"usr-dana"},"creator":{"id":"usr-kim"},
        "project":null,
        "labels":{"nodes":[{"id":"lab-ui","name":"UI","color":"#3b82f6"}]}
      },
      {
        "id":"iss-7","identifier":"OPS-7","number":7,
        "title":"Renew the TLS certificate","description":"",
        "priority":0,"estimate":null,"url":"https://linear.app/acme/issue/OPS-7",
        "createdAt":"2023-11-20T12:00:00.000Z","updatedAt":"2023-12-01T12:00:00.000Z",
        "startedAt":null,"completedAt":"2023-12-01T12:00:00.000Z","dueDate":null,"archivedAt":null,
        "state":{"id":"st-ops-done"},"team":{"id":"team-ops"},"cycle":null,
        "parent":null,"assignee":null,"creator":{"id":"usr-kim"},
        "project":null,
        "labels":{"nodes":[]}
      }
    ]
  }
}`

const fixtureComments = `{
  "comments": {
    "pageInfo": {"hasNextPage": false, "endCursor": ""},
    "nodes": [
      {"id":"cm-1","body":"On it.","url":"https://linear.app/acme/issue/ENG-142#comment-cm-1",
       "createdAt":"2024-03-07T10:00:00.000Z","updatedAt":"2024-03-07T10:00:00.000Z",
       "issue":{"id":"iss-142"},"parent":null,"user":{"id":"usr-kim"},"botActor":null},
      {"id":"cm-2","body":"Reproduced on staging.","url":"",
       "createdAt":"2024-03-08T11:00:00.000Z","updatedAt":"2024-03-08T11:00:00.000Z",
       "issue":{"id":"iss-142"},"parent":{"id":"cm-1"},"user":{"id":"usr-dana"},"botActor":null},
      {"id":"cm-3","body":"Deploy 1.4.2 shipped.","url":"",
       "createdAt":"2024-04-02T09:00:00.000Z","updatedAt":"2024-04-02T09:00:00.000Z",
       "issue":{"id":"iss-143"},"parent":null,"user":null,"botActor":{"id":"bot-deploy","name":"Deploy Bot"}}
    ]
  }
}`

const fixtureAttachments = `{
  "attachments": {
    "pageInfo": {"hasNextPage": false, "endCursor": ""},
    "nodes": [
      {"id":"att-1","title":"redirect-loop.png","subtitle":"","url":"https://uploads.linear.app/att-1",
       "createdAt":"2024-03-07T09:40:00.000Z","issue":{"id":"iss-142"}}
    ]
  }
}`

// Two relations: one Agora understands verbatim, and one Linear-only value
// (`similar`) that must degrade to `related` with its own name preserved.
const fixtureRelations = `{
  "issueRelations": {
    "pageInfo": {"hasNextPage": false, "endCursor": ""},
    "nodes": [
      {"id":"rel-1","type":"blocks","issue":{"id":"iss-142"},"relatedIssue":{"id":"iss-143"}},
      {"id":"rel-2","type":"similar","issue":{"id":"iss-142"},"relatedIssue":{"id":"iss-144"}},
      {"id":"rel-3","type":"blocks","issue":{"id":"iss-142"},"relatedIssue":{"id":"iss-outside-scope"}}
    ]
  }
}`

// --- the mock host ----------------------------------------------------------

type recordedCall struct {
	Operation  string
	Query      string
	Variables  map[string]any
	AuthHeader string
}

type linearMock struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []recordedCall

	// complexity is reported in X-Complexity on every response; a test sets it
	// to drive the adaptive page size.
	complexity int
	// rateLimitOnce makes the next call answer 429 with Retry-After.
	rateLimitOnce bool
	// unauthorized makes every call answer with a GraphQL authentication error.
	unauthorized bool
	// unreachable makes every call answer 503.
	unreachable bool
}

func newLinearMock(t *testing.T) *linearMock {
	t.Helper()
	m := &linearMock{complexity: 400}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(body, &req)

		m.mu.Lock()
		m.calls = append(m.calls, recordedCall{
			Operation:  operationOf(req.Query),
			Query:      req.Query,
			Variables:  req.Variables,
			AuthHeader: r.Header.Get("Authorization"),
		})
		rateLimited := m.rateLimitOnce
		m.rateLimitOnce = false
		complexity := m.complexity
		unauthorized := m.unauthorized
		unreachable := m.unreachable
		m.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Complexity", strconv.Itoa(complexity))
		w.Header().Set("X-RateLimit-Complexity-Remaining", "2500000")

		switch {
		case unreachable:
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"errors":[{"message":"service unavailable"}]}`)
			return
		case rateLimited:
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"errors":[{"message":"rate limited"}]}`)
			return
		case unauthorized:
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"errors":[{"message":"Authentication required - not authenticated"}]}`)
			return
		}

		io.WriteString(w, `{"data":`+m.dataFor(req.Query, req.Variables)+`}`)
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *linearMock) dataFor(query string, vars map[string]any) string {
	switch operationOf(query) {
	case "Viewer":
		return fixtureViewer
	case "Teams":
		return fixtureTeams
	case "States":
		return fixtureStates
	case "Labels":
		return fixtureLabels
	case "Users":
		return fixtureUsers
	case "Cycles":
		return fixtureCycles
	case "Issues":
		page := fixtureIssuesPage1
		if after, _ := vars["after"].(string); after == "cursor-page-2" {
			page = fixtureIssuesPage2
		}
		// Real Linear applies IssueFilter server-side. The mock does too, so a
		// test that asserts "nothing out of scope came back" is asserting
		// something the adapter actually caused rather than something the
		// fixture happened not to contain.
		return applyTeamFilter(page, vars)
	case "Comments":
		return fixtureComments
	case "Attachments":
		return fixtureAttachments
	case "Relations":
		return fixtureRelations
	}
	return `{}`
}

// applyTeamFilter narrows an issue page by the filter the adapter sent, the way
// the server would.
func applyTeamFilter(page string, vars map[string]any) string {
	wanted := map[string]bool{}
	if filter, ok := vars["filter"].(map[string]any); ok {
		if team, ok := filter["team"].(map[string]any); ok {
			if id, ok := team["id"].(map[string]any); ok {
				if in, ok := id["in"].([]any); ok {
					for _, v := range in {
						if s, ok := v.(string); ok {
							wanted[s] = true
						}
					}
				}
			}
		}
	}
	if len(wanted) == 0 {
		return page
	}
	var doc struct {
		Issues struct {
			PageInfo json.RawMessage   `json:"pageInfo"`
			Nodes    []json.RawMessage `json:"nodes"`
		} `json:"issues"`
	}
	if err := json.Unmarshal([]byte(page), &doc); err != nil {
		return page
	}
	kept := []json.RawMessage{}
	for _, node := range doc.Issues.Nodes {
		var probe struct {
			Team *struct {
				ID string `json:"id"`
			} `json:"team"`
		}
		if err := json.Unmarshal(node, &probe); err != nil || probe.Team == nil || wanted[probe.Team.ID] {
			kept = append(kept, node)
		}
	}
	doc.Issues.Nodes = kept
	out, err := json.Marshal(doc)
	if err != nil {
		return page
	}
	return string(out)
}

// operationOf reads the operation name off the document, which is how the mock
// routes. Real Linear routes on the document itself; the name is a stand-in and
// the queries are named precisely so this works.
func operationOf(query string) string {
	const prefix = "query "
	i := strings.Index(query, prefix)
	if i < 0 {
		return ""
	}
	rest := query[i+len(prefix):]
	for j := 0; j < len(rest); j++ {
		if rest[j] == '(' || rest[j] == ' ' || rest[j] == '{' || rest[j] == '\n' {
			return rest[:j]
		}
	}
	return rest
}

func (m *linearMock) adapter(t *testing.T) *Adapter {
	t.Helper()
	return NewAdapter(New(Config{
		APIKey:   testAPIKey,
		Endpoint: m.srv.URL,
		Sleep:    func(context.Context, time.Duration) error { return nil },
	}))
}

func (m *linearMock) callsFor(op string) []recordedCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []recordedCall{}
	for _, c := range m.calls {
		if c.Operation == op {
			out = append(out, c)
		}
	}
	return out
}

// --- client + probe ---------------------------------------------------------

// A personal API key is sent VERBATIM. `Bearer lin_api_…` fails against Linear,
// and this is the one line of the adapter most likely to be "fixed" by someone
// who knows how every other API works.
func TestAuthorizationHeaderHasNoBearerPrefix(t *testing.T) {
	m := newLinearMock(t)
	if _, err := m.adapter(t).Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	calls := m.callsFor("Viewer")
	if len(calls) != 1 {
		t.Fatalf("Viewer called %d times, want 1", len(calls))
	}
	if calls[0].AuthHeader != testAPIKey {
		t.Fatalf("Authorization = %q, want the bare key", calls[0].AuthHeader)
	}
	if strings.HasPrefix(calls[0].AuthHeader, "Bearer") {
		t.Fatal("the key was sent with a Bearer prefix; Linear rejects that")
	}
}

func TestProbeIdentifiesTheWorkspace(t *testing.T) {
	m := newLinearMock(t)
	adapter := m.adapter(t)

	got, err := adapter.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if got.Status != imports.ProbeOK || got.Account != "kim@acme.io" || got.Ref != "acme" {
		t.Fatalf("Probe = %+v", got)
	}
	if src := adapter.Source(); src.Kind != imports.SourceLinear || src.Ref != "acme" {
		t.Errorf("Source after probe = %+v", src)
	}
}

// "Your key is wrong" and "Linear is down" have different next actions for the
// operator, so the probe must not collapse them into one failure.
func TestProbeDistinguishesInvalidKeyFromUnreachable(t *testing.T) {
	m := newLinearMock(t)
	m.unauthorized = true
	got, err := m.adapter(t).Probe(context.Background())
	if err != nil {
		t.Fatalf("an invalid key should be a verdict, not a transport error: %v", err)
	}
	if got.Status != imports.ProbeInvalid {
		t.Fatalf("status = %q, want invalid", got.Status)
	}
	if strings.Contains(got.Detail, testAPIKey) {
		t.Fatal("the probe result echoed the token")
	}

	m2 := newLinearMock(t)
	m2.unreachable = true
	down, err := m2.adapter(t).Probe(context.Background())
	if err == nil {
		t.Fatal("an unreachable host should surface the transport error too")
	}
	if down.Status != imports.ProbeUnreachable {
		t.Fatalf("status = %q, want unreachable", down.Status)
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatal("the error echoed the token")
	}
}

// A 429 is retried once Retry-After has elapsed, and the page size shrinks —
// the clearest possible signal that the last request was too ambitious.
func TestRateLimitIsRetriedAndShrinksThePage(t *testing.T) {
	m := newLinearMock(t)
	client := New(Config{
		APIKey:   testAPIKey,
		Endpoint: m.srv.URL,
		PageSize: 50,
		Sleep:    func(context.Context, time.Duration) error { return nil },
	})
	m.rateLimitOnce = true

	var out viewerResponse
	if err := client.Query(context.Background(), viewerQuery, nil, &out); err != nil {
		t.Fatalf("Query did not recover from a 429: %v", err)
	}
	if out.Organization.URLKey != "acme" {
		t.Fatalf("retry returned %+v", out.Organization)
	}
	if len(m.callsFor("Viewer")) != 2 {
		t.Errorf("Viewer called %d times, want 2 (the 429 and the retry)", len(m.callsFor("Viewer")))
	}
	if client.PageSize() >= 50 {
		t.Errorf("page size after a 429 = %d, want smaller than 50", client.PageSize())
	}
}

// The page size is tuned from X-Complexity so a query never walks up to the
// 10,000-point per-query ceiling.
func TestPageSizeAdaptsToReportedComplexity(t *testing.T) {
	m := newLinearMock(t)
	m.complexity = 9000 // most of the 10,000-point ceiling in one query
	client := New(Config{
		APIKey: testAPIKey, Endpoint: m.srv.URL, PageSize: 80,
		Sleep: func(context.Context, time.Duration) error { return nil },
	})
	if err := client.Query(context.Background(), viewerQuery, nil, &viewerResponse{}); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if client.PageSize() >= 80 {
		t.Fatalf("page size after a 9,000-point query = %d, want it to shrink", client.PageSize())
	}
	if client.LastComplexity() != 9000 {
		t.Errorf("LastComplexity = %d, want 9000", client.LastComplexity())
	}

	// A cheap query grows it back, slowly.
	m.complexity = 200
	before := client.PageSize()
	if err := client.Query(context.Background(), viewerQuery, nil, &viewerResponse{}); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if client.PageSize() <= before {
		t.Errorf("page size after a cheap query = %d, want it to grow from %d", client.PageSize(), before)
	}
	if client.PageSize() > 100 {
		t.Errorf("page size grew to %d; the real maximum `first` is undocumented and must be treated as unknown", client.PageSize())
	}
}

// --- normalization ----------------------------------------------------------

func fetchFixture(t *testing.T, m *linearMock, scope imports.Scope) *imports.Bundle {
	t.Helper()
	adapter := m.adapter(t)
	if _, err := adapter.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	bundle, err := adapter.Fetch(context.Background(), scope, nil)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	return bundle
}

func TestFetchNormalizesTheWorkspace(t *testing.T) {
	bundle := fetchFixture(t, newLinearMock(t), imports.Scope{})

	if bundle.Source.Kind != imports.SourceLinear || bundle.Source.Ref != "acme" {
		t.Errorf("source = %+v", bundle.Source)
	}
	if len(bundle.Containers) != 2 {
		t.Fatalf("containers = %d, want 2 teams", len(bundle.Containers))
	}
	if bundle.Containers[0].Key != "ENG" || bundle.Containers[0].Name != "Engineering" {
		t.Errorf("first container = %+v", bundle.Containers[0])
	}
	if len(bundle.Issues) != 4 {
		t.Fatalf("issues = %d, want 4 across both pages", len(bundle.Issues))
	}
	if len(bundle.Users) < 2 {
		t.Fatalf("users = %d, want at least the two humans", len(bundle.Users))
	}

	// States belonging to a team outside the scope are not carried: the bundle
	// is what THIS import is about, not the whole workspace.
	for _, s := range bundle.States {
		if s.ContainerID == "team-sec" {
			t.Errorf("a state from an out-of-scope team leaked in: %+v", s)
		}
	}
	for _, it := range bundle.Iterations {
		if it.ContainerID == "team-sec" {
			t.Errorf("a cycle from an out-of-scope team leaked in: %+v", it)
		}
	}

	// A Linear cycle with no name is identified by its number, not left
	// anonymous.
	if len(bundle.Iterations) != 1 {
		t.Fatalf("iterations = %d, want 1", len(bundle.Iterations))
	}
	if bundle.Iterations[0].Name != "Cycle 12" || !bundle.Iterations[0].Completed {
		t.Errorf("cycle = %+v, want the numbered name and completed=true", bundle.Iterations[0])
	}

	eng142 := issueByID(t, bundle, "iss-142")
	if eng142.Identifier != "ENG-142" || eng142.Title != "Fix the login redirect loop" {
		t.Errorf("ENG-142 = %+v", eng142)
	}
	// Linear bodies are already Markdown: no conversion, no mangling.
	if eng142.BodyMarkdown != "The redirect **loops** on SSO." {
		t.Errorf("body = %q, want the source Markdown verbatim", eng142.BodyMarkdown)
	}
	if eng142.CreatedAt.Format(time.RFC3339) != "2024-03-07T09:30:00Z" {
		t.Errorf("created_at = %s, want the source's", eng142.CreatedAt)
	}
	if eng142.UpdatedAt.Format(time.RFC3339) != "2025-11-02T16:05:00Z" {
		t.Errorf("updated_at = %s, want the source's", eng142.UpdatedAt)
	}
	if eng142.StateID != "st-review" || eng142.ContainerID != "team-eng" || eng142.IterationID != "cyc-12" {
		t.Errorf("ENG-142 references = %+v", eng142)
	}
	if eng142.AssigneeID != "usr-kim" || eng142.CreatorID != "usr-kim" {
		t.Errorf("ENG-142 actors = assignee %q creator %q", eng142.AssigneeID, eng142.CreatorID)
	}
	if len(eng142.LabelIDs) != 1 || eng142.LabelIDs[0] != "lab-bug" {
		t.Errorf("ENG-142 labels = %v", eng142.LabelIDs)
	}
	if eng142.Estimate == nil || *eng142.Estimate != 3 {
		t.Errorf("ENG-142 estimate = %v", eng142.Estimate)
	}
	// A Linear PROJECT is not an Agora container in Phase 1, but it is not
	// dropped either — it rides in Raw.
	if !strings.Contains(string(eng142.Raw), "Auth hardening") {
		t.Errorf("the Linear project was lost rather than preserved in Raw: %s", eng142.Raw)
	}

	child := issueByID(t, bundle, "iss-144")
	if child.ParentID != "iss-143" {
		t.Errorf("child parent = %q, want iss-143", child.ParentID)
	}
}

// Linear's priority integers ARE the canonical ladder. This is the bijection
// §2.1 calls "almost embarrassingly clean"; the fixture pins every rung.
func TestPriorityIsABijectionWithLinear(t *testing.T) {
	bundle := fetchFixture(t, newLinearMock(t), imports.Scope{})
	want := map[string]imports.Priority{
		"iss-142": imports.PriorityHigh,   // Linear 2
		"iss-143": imports.PriorityUrgent, // Linear 1
		"iss-144": imports.PriorityMedium, // Linear 3
		"iss-7":   imports.PriorityNone,   // Linear 0
	}
	for id, wantPriority := range want {
		issue := issueByID(t, bundle, id)
		if issue.Priority != wantPriority {
			t.Errorf("%s priority = %d, want %d", id, issue.Priority, wantPriority)
		}
	}
	// And a value Linear has not shipped degrades rather than dropping an issue.
	six := 6
	if got := mapPriority(&six); got != imports.PriorityNone {
		t.Errorf("mapPriority(6) = %d, want none", got)
	}
	if got := mapPriority(nil); got != imports.PriorityNone {
		t.Errorf("mapPriority(nil) = %d, want none", got)
	}
}

// Category first, name second: `started` covers three Agora columns and only
// the name can tell them apart.
func TestWorkflowStateCategoryDrivesTheStatus(t *testing.T) {
	bundle := fetchFixture(t, newLinearMock(t), imports.Scope{})
	mapping := imports.NewMapping(imports.SourceLinear, Defaults(), imports.Overrides{})

	want := map[string]string{
		"Triage":      imports.StatusTodo,
		"Backlog":     imports.StatusBacklog,
		"Todo":        imports.StatusTodo,
		"In Progress": imports.StatusInProgress,
		"In Review":   imports.StatusInReview,
		"Done":        imports.StatusDone,
		"Canceled":    imports.StatusCancelled,
		// A team that renamed its terminal column still lands on done, because
		// the CATEGORY is what decided it.
		"Shipped": imports.StatusDone,
	}
	for _, state := range bundle.States {
		expected, ok := want[state.Name]
		if !ok {
			continue
		}
		if got := mapping.Status(state); got != expected {
			t.Errorf("state %q (%s) mapped to %q, want %q", state.Name, state.Category, got, expected)
		}
		delete(want, state.Name)
	}
	if len(want) != 0 {
		t.Errorf("the fixture did not exercise every state: %v", want)
	}
	if len(mapping.Unmapped()) != 0 {
		t.Errorf("a Linear state fell back rather than mapping: %+v", mapping.Unmapped())
	}
}

// The comments/attachments/relations passes are SEPARATE requests, because
// nesting them inside the issue connection multiplies complexity.
func TestFetchWalksShallowThenSeparatePasses(t *testing.T) {
	m := newLinearMock(t)
	bundle := fetchFixture(t, m, imports.Scope{})

	issueCalls := m.callsFor("Issues")
	if len(issueCalls) != 2 {
		t.Fatalf("Issues called %d times, want 2 (the cursor was followed)", len(issueCalls))
	}
	if after, _ := issueCalls[1].Variables["after"].(string); after != "cursor-page-2" {
		t.Errorf("second page requested after=%q, want cursor-page-2", after)
	}
	// The shallow rule, asserted against the document itself.
	for _, forbidden := range []string{"comments(", "attachments(", "history(", "relations("} {
		if strings.Contains(issueCalls[0].Query, forbidden) {
			t.Errorf("the issue query nests %s; complexity multiplies through connections", forbidden)
		}
	}
	if len(m.callsFor("Comments")) == 0 || len(m.callsFor("Attachments")) == 0 || len(m.callsFor("Relations")) == 0 {
		t.Fatal("comments, attachments and relations must each be their own pass")
	}

	eng142 := issueByID(t, bundle, "iss-142")
	if len(eng142.Comments) != 2 {
		t.Fatalf("ENG-142 comments = %d, want 2", len(eng142.Comments))
	}
	if eng142.Comments[1].ParentID != "cm-1" {
		t.Errorf("threaded comment lost its parent: %+v", eng142.Comments[1])
	}
	if len(eng142.Attachments) != 1 || eng142.Attachments[0].Title != "redirect-loop.png" {
		t.Errorf("ENG-142 attachments = %+v", eng142.Attachments)
	}

	// Two relations survive; the one pointing outside the import is dropped at
	// the adapter rather than carried as a dangling reference.
	if len(eng142.Relations) != 2 {
		t.Fatalf("ENG-142 relations = %+v, want 2 in-scope edges", eng142.Relations)
	}
	var sawBlocks, sawDegraded bool
	for _, rel := range eng142.Relations {
		switch rel.SourceType {
		case "blocks":
			sawBlocks = rel.Kind == imports.RelationBlocks
		case "similar":
			// Linear's fourth value has no Agora equivalent: it degrades to
			// `related` and keeps its own name on the edge.
			sawDegraded = rel.Kind == imports.RelationRelated
		}
	}
	if !sawBlocks || !sawDegraded {
		t.Errorf("relations = %+v, want blocks kept and `similar` degraded to related", eng142.Relations)
	}
	if !warned(bundle, "downgraded to \"related\"") {
		t.Errorf("the degrade was not reported to the operator: %v", bundle.Warnings)
	}
	// Linear exposes no attachment size, and the report must not imply one.
	if !warned(bundle, "does not report attachment sizes") {
		t.Errorf("the missing-size caveat was not reported: %v", bundle.Warnings)
	}
}

// A comment written by an integration becomes a bot actor, not a human match.
func TestBotAuthoredCommentsBecomeBotUsers(t *testing.T) {
	bundle := fetchFixture(t, newLinearMock(t), imports.Scope{})
	eng143 := issueByID(t, bundle, "iss-143")
	if len(eng143.Comments) != 1 {
		t.Fatalf("ENG-143 comments = %d, want 1", len(eng143.Comments))
	}
	author := eng143.Comments[0].AuthorID
	user, ok := bundle.UserByID(author)
	if !ok {
		t.Fatalf("the bot author %q is not in the bundle's user table", author)
	}
	if !user.Bot || user.Name != "Deploy Bot" {
		t.Errorf("bot user = %+v, want Bot=true", user)
	}
}

// The scope picker's entries resolve by id, key or name, and an entry the
// credential cannot see is NAMED rather than silently producing a smaller
// import.
func TestScopeSelectsTeamsAndNamesTheOnesItCannotSee(t *testing.T) {
	m := newLinearMock(t)
	bundle := fetchFixture(t, m, imports.Scope{Containers: []string{"ENG", "team-sec"}})

	if len(bundle.Containers) != 1 || bundle.Containers[0].Key != "ENG" {
		t.Fatalf("containers = %+v, want just ENG", bundle.Containers)
	}
	if bundle.Truncated["containers"] != 1 {
		t.Errorf("truncated = %v, want the unreachable team counted", bundle.Truncated)
	}
	if !warned(bundle, "team-sec") {
		t.Errorf("the unreachable team was not named: %v", bundle.Warnings)
	}
	for _, issue := range bundle.Issues {
		if issue.ContainerID != "team-eng" {
			t.Errorf("issue %s came from an out-of-scope team", issue.Identifier)
		}
	}
	// The narrowing went to the SERVER as a team-id filter. Fetching the whole
	// workspace and discarding the rest would burn the complexity budget the
	// client spends the rest of its life protecting.
	calls := m.callsFor("Issues")
	if len(calls) == 0 {
		t.Fatal("no issue query was made")
	}
	filter, _ := calls[0].Variables["filter"].(map[string]any)
	team, _ := filter["team"].(map[string]any)
	id, _ := team["id"].(map[string]any)
	in, _ := id["in"].([]any)
	if len(in) != 1 || in[0] != "team-eng" {
		t.Errorf("issue filter = %v, want a server-side team-id filter on team-eng", filter)
	}
}

// A cap the operator set still makes the count an estimate, because "240
// issues" when only 200 were fetched is the lie this framework exists to
// prevent.
func TestIssueCapIsReportedAsTruncation(t *testing.T) {
	bundle := fetchFixture(t, newLinearMock(t), imports.Scope{MaxIssues: 2})
	if len(bundle.Issues) != 2 {
		t.Fatalf("issues = %d, want the cap of 2", len(bundle.Issues))
	}
	if bundle.Truncated["issues"] == 0 {
		t.Errorf("a capped run reported no truncation: %v", bundle.Truncated)
	}
}

func issueByID(t *testing.T, b *imports.Bundle, id string) imports.Issue {
	t.Helper()
	for _, issue := range b.Issues {
		if issue.ExternalID == id {
			return issue
		}
	}
	t.Fatalf("issue %q not in the bundle", id)
	return imports.Issue{}
}

func warned(b *imports.Bundle, fragment string) bool {
	for _, w := range b.Warnings {
		if strings.Contains(w, fragment) {
			return true
		}
	}
	return false
}

// --- the fixture, applied ---------------------------------------------------
//
// The adapter's output is only as good as what the applier can do with it, and
// the properties below are the ones a customer notices: run it twice and get no
// duplicates; an author who is not on the team does not show up as the person
// who clicked Import.

const applyWorkspaceID = "33333333-3333-4333-8333-333333333333"

func applyFixture(t *testing.T, store *importmem.Store, bundle *imports.Bundle, importID string) *imports.Result {
	t.Helper()
	applier := &imports.Applier{
		Store: store,
		Resolver: imports.NewActorResolver(store, imports.ResolverConfig{
			Source:      imports.SourceLinear,
			WorkspaceID: applyWorkspaceID,
		}),
		Mapping:     imports.NewMapping(imports.SourceLinear, Defaults(), imports.Overrides{}),
		WorkspaceID: util.MustParseUUID(applyWorkspaceID),
		Source:      imports.SourceLinear,
		ImportID:    importID,
	}
	result, err := applier.Apply(context.Background(), bundle, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return result
}

// Run the same recorded import twice: created 4 / updated 0, then created 0 /
// updated 4, with no row growth anywhere.
func TestFixtureAppliesIdempotently(t *testing.T) {
	m := newLinearMock(t)
	store := importmem.New()
	ws := util.MustParseUUID(applyWorkspaceID)
	store.AddMember(ws, "Kim Ryu", "kim@acme.io", "member")

	first := applyFixture(t, store, fetchFixture(t, m, imports.Scope{}), "job-1")
	if got := first.Totals[imports.KindIssue]; got.Created != 4 || got.Updated != 0 {
		t.Fatalf("first run issues = %+v, want created 4 / updated 0", got)
	}
	issues, comments := len(store.Issues), len(store.Comments)
	projects, labels, sprints := len(store.Projects), len(store.Labels), len(store.Sprints)
	if projects != 2 || sprints != 1 {
		t.Fatalf("first run wrote %d projects and %d sprints, want 2 and 1", projects, sprints)
	}

	second := applyFixture(t, store, fetchFixture(t, m, imports.Scope{}), "job-2")
	if got := second.Totals[imports.KindIssue]; got.Created != 0 || got.Updated != 4 {
		t.Fatalf("second run issues = %+v, want created 0 / updated 4", got)
	}
	if len(store.Issues) != issues {
		t.Errorf("re-run grew issues from %d to %d", issues, len(store.Issues))
	}
	if len(store.Comments) != comments {
		t.Errorf("re-run grew comments from %d to %d", comments, len(store.Comments))
	}
	if len(store.Projects) != projects || len(store.Labels) != labels || len(store.Sprints) != sprints {
		t.Errorf("re-run duplicated containers: %d projects, %d labels, %d sprints",
			len(store.Projects), len(store.Labels), len(store.Sprints))
	}
	if len(store.Dependencies) != 2 {
		t.Errorf("re-run stacked %d dependency rows, want the 2 in-scope edges", len(store.Dependencies))
	}
}

// The rule, end to end from a recorded fixture: Dana Wu is not a member of this
// workspace, and her comment must land on linear-import@linear.local — never on
// the operator who ran the import.
func TestUnmatchedAuthorLandsOnTheLinearImportIdentity(t *testing.T) {
	m := newLinearMock(t)
	store := importmem.New()
	ws := util.MustParseUUID(applyWorkspaceID)
	operator := store.AddMember(ws, "Operator", "operator@acme.io", "owner")
	kim := store.AddMember(ws, "Kim Ryu", "kim@acme.io", "member")

	applyFixture(t, store, fetchFixture(t, m, imports.Scope{}), "job-1")

	issue, ok := store.IssueByExternalID(imports.SourceLinear, "iss-142")
	if !ok {
		t.Fatal("ENG-142 was not written")
	}
	var fromKim, fromDana bool
	for _, c := range store.CommentsForIssue(issue.ID) {
		author := util.UUIDToString(c.AuthorID)
		if author == operator {
			t.Fatalf("a Linear comment was attributed to the operator: %q", c.Content)
		}
		switch c.ExternalID.String {
		case "cm-1":
			fromKim = author == kim
		case "cm-2":
			user, _ := store.UserByID(author)
			if user.Email != "linear-import@linear.local" {
				t.Errorf("Dana's comment went to %q, want linear-import@linear.local", user.Email)
			}
			if !strings.Contains(c.Content, "Dana Wu") {
				t.Errorf("the real author's name was lost: %q", c.Content)
			}
			fromDana = true
		}
	}
	if !fromKim || !fromDana {
		t.Fatalf("expected both comments written (kim=%v dana=%v)", fromKim, fromDana)
	}

	// ENG-143's CREATOR is Dana too, so the issue's linkage blob keeps her name
	// even though the row cannot.
	eng143, _ := store.IssueByExternalID(imports.SourceLinear, "iss-143")
	ref, ok := imports.ReadExternalRef(eng143.Metadata)
	if !ok || ref.Author != "Dana Wu" {
		t.Errorf("ENG-143 external_ref = %+v, want the unmatched creator's name preserved", ref)
	}
	if ref.Identifier != "ENG-143" || ref.URL != "https://linear.app/acme/issue/ENG-143" {
		t.Errorf("ENG-143 provenance = %+v, want the source identifier and a link back", ref)
	}
}

// Status and priority survive the whole pipeline, not just the mapping unit.
func TestFixtureLandsWithTheMappedStatusAndPriority(t *testing.T) {
	m := newLinearMock(t)
	store := importmem.New()
	applyFixture(t, store, fetchFixture(t, m, imports.Scope{}), "job-1")

	want := map[string]struct{ status, priority string }{
		"iss-142": {imports.StatusInReview, imports.PriorityNameHigh},
		"iss-143": {imports.StatusTodo, imports.PriorityNameUrgent},
		"iss-144": {imports.StatusInProgress, imports.PriorityNameMedium},
		"iss-7":   {imports.StatusDone, imports.PriorityNameNone},
	}
	for id, expected := range want {
		issue, ok := store.IssueByExternalID(imports.SourceLinear, id)
		if !ok {
			t.Fatalf("%s was not written", id)
		}
		if issue.Status != expected.status {
			t.Errorf("%s status = %q, want %q", id, issue.Status, expected.status)
		}
		if issue.Priority != expected.priority {
			t.Errorf("%s priority = %q, want %q", id, issue.Priority, expected.priority)
		}
	}

	// Source timestamps, not today's date.
	eng142, _ := store.IssueByExternalID(imports.SourceLinear, "iss-142")
	if eng142.CreatedAt.Time.Format(time.RFC3339) != "2024-03-07T09:30:00Z" {
		t.Errorf("created_at = %s, want the source's 2024 date", eng142.CreatedAt.Time)
	}
}

// Attachments are skipped AND LISTED when nothing is wired to store them —
// never dropped quietly, because the original URL is the only way back to the
// file once the team cancels their Linear subscription.
func TestUnwiredAttachmentsAreSkippedAndListed(t *testing.T) {
	m := newLinearMock(t)
	store := importmem.New()
	result := applyFixture(t, store, fetchFixture(t, m, imports.Scope{}), "job-1")

	totals := result.Totals[imports.KindAttachment]
	if totals == nil || totals.Skipped != 1 {
		t.Fatalf("attachments = %+v, want 1 skipped", totals)
	}
	var listed bool
	for _, f := range result.Failures {
		if f.Kind == imports.KindAttachment {
			listed = true
		}
	}
	if !listed {
		t.Error("a skipped attachment was not listed in the receipt")
	}
}
