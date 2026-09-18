package handler

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/llm"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// ---------------------------------------------------------------------------
// Extra fixtures for the full tool catalog
// ---------------------------------------------------------------------------

func newAssistantTestProject(t *testing.T, workspaceID, title string) string {
	t.Helper()
	var projectID string
	if err := testPool.QueryRow(context.Background(),
		`INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id`,
		workspaceID, title,
	).Scan(&projectID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	return projectID
}

func newAssistantTestRuntime(t *testing.T, workspaceID string) string {
	t.Helper()
	var runtimeID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at)
		VALUES ($1, NULL, 'Assistant Test Runtime', 'cloud', 'assistant_test', 'online', 'assistant', '{}'::jsonb, now())
		RETURNING id
	`, workspaceID).Scan(&runtimeID); err != nil {
		t.Fatalf("insert runtime: %v", err)
	}
	return runtimeID
}

// newAssistantTestAgent creates an agent with an explicit visibility + owner so
// the private-agent gate can be exercised.
func newAssistantTestAgent(t *testing.T, workspaceID, runtimeID, name, visibility, ownerID string) string {
	t.Helper()
	var agentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent (workspace_id, name, description, runtime_mode, runtime_config,
		                   runtime_id, visibility, max_concurrent_tasks, owner_id)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, $4, 1, $5)
		RETURNING id
	`, workspaceID, name, runtimeID, visibility, ownerID).Scan(&agentID); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	return agentID
}

// recordBusEvents captures events of one type for the duration of a test. The
// bus has no unsubscribe, so the listener is disarmed on cleanup rather than
// removed — otherwise every later test in the package would keep feeding it.
func recordBusEvents(t *testing.T, eventType string) func() []events.Event {
	t.Helper()
	var mu sync.Mutex
	var seen []events.Event
	armed := true
	t.Cleanup(func() {
		mu.Lock()
		armed = false
		mu.Unlock()
	})
	testHandler.Bus.Subscribe(eventType, func(e events.Event) {
		mu.Lock()
		defer mu.Unlock()
		if armed {
			seen = append(seen, e)
		}
	})
	return func() []events.Event {
		mu.Lock()
		defer mu.Unlock()
		return append([]events.Event(nil), seen...)
	}
}

func storedIssueMetadata(t *testing.T, issueID string) map[string]any {
	t.Helper()
	var raw []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT metadata FROM issue WHERE id = $1`, issueID).Scan(&raw); err != nil {
		t.Fatalf("read issue metadata: %v", err)
	}
	out := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode metadata %q: %v", raw, err)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// get_issue
// ---------------------------------------------------------------------------

func TestAssistantGetIssueReturnsDetail(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-get@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-get-ws", "GET")
	addAssistantTestMember(t, ws, user, "owner")
	issueID := newAssistantTestIssue(t, ws, "login form is broken", user, user)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE issue SET description = 'Steps to reproduce: open /login', due_date = '2026-10-01' WHERE id = $1`,
		issueID); err != nil {
		t.Fatalf("set description: %v", err)
	}

	// Both ref forms must resolve to the same issue.
	for _, ref := range []string{"GET-1", issueID} {
		result, err := executeAssistantTool(t, user, assistant.ToolGetIssue,
			`{"workspace_id":"`+ws+`","ref":"`+ref+`"}`)
		if err != nil {
			t.Fatalf("get_issue(%s): %v", ref, err)
		}
		if result["identifier"] != "GET-1" || result["title"] != "login form is broken" {
			t.Fatalf("get_issue(%s) = %v", ref, result)
		}
		if result["url_path"] != "/assistant-get-ws/issues/GET-1" {
			t.Fatalf("url_path = %v", result["url_path"])
		}
		if !strings.Contains(result["description"].(string), "open /login") {
			t.Fatalf("description = %v", result["description"])
		}
		if result["due_date"] != "2026-10-01" {
			t.Fatalf("due_date = %v", result["due_date"])
		}
	}
}

// The single-issue read is gated exactly like the HTTP GET: a non-owner member
// cannot pull someone else's issue by naming its identifier directly.
func TestAssistantGetIssueHonorsVisibilityGate(t *testing.T) {
	owner := newAssistantTestUser(t, "assistant-getgate-owner@agora.dev")
	member := newAssistantTestUser(t, "assistant-getgate-member@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-getgate", "GGT")
	addAssistantTestMember(t, ws, owner, "owner")
	addAssistantTestMember(t, ws, member, "member")

	newAssistantTestIssue(t, ws, "owner private work", owner, owner) // GGT-1
	newAssistantTestIssue(t, ws, "member own work", member, member)  // GGT-2

	// The member may read their own issue...
	if _, err := executeAssistantTool(t, member, assistant.ToolGetIssue,
		`{"workspace_id":"`+ws+`","ref":"GGT-2"}`); err != nil {
		t.Fatalf("member should read their own issue: %v", err)
	}
	// ...but not the owner's.
	_, err := executeAssistantTool(t, member, assistant.ToolGetIssue,
		`{"workspace_id":"`+ws+`","ref":"GGT-1"}`)
	if err == nil {
		t.Fatal("a non-owner must not be able to read another member's issue")
	}
	// Not-found, never "forbidden" — the tool must not be an existence oracle.
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("refusal should be not-found, got %q", err)
	}
	// The owner sees both.
	if _, err := executeAssistantTool(t, owner, assistant.ToolGetIssue,
		`{"workspace_id":"`+ws+`","ref":"GGT-1"}`); err != nil {
		t.Fatalf("owner read failed: %v", err)
	}
}

// An issue UUID from a different workspace must not resolve, even for a
// workspace owner — the lookup is workspace-scoped, not global.
func TestAssistantGetIssueRefusesCrossWorkspaceRef(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-getcross@agora.dev")
	other := newAssistantTestUser(t, "assistant-getcross-other@agora.dev")
	mine := newAssistantTestWorkspace(t, "assistant-getcross-mine", "GCM")
	foreign := newAssistantTestWorkspace(t, "assistant-getcross-foreign", "GCF")
	addAssistantTestMember(t, mine, user, "owner")
	addAssistantTestMember(t, foreign, other, "owner")
	foreignIssue := newAssistantTestIssue(t, foreign, "their secret", other, other)

	if _, err := executeAssistantTool(t, user, assistant.ToolGetIssue,
		`{"workspace_id":"`+mine+`","ref":"`+foreignIssue+`"}`); err == nil {
		t.Fatal("an issue from another workspace must not resolve")
	}
}

func TestAssistantGetIssueRejectsGarbageRef(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-getgarbage@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-getgarbage-ws", "GGB")
	addAssistantTestMember(t, ws, user, "owner")

	for _, ref := range []string{"", "not-an-issue", "../../etc/passwd", "ZZZ-999"} {
		blob, _ := json.Marshal(map[string]string{"workspace_id": ws, "ref": ref})
		if _, err := executeAssistantTool(t, user, assistant.ToolGetIssue, string(blob)); err == nil {
			t.Fatalf("ref %q should have been refused", ref)
		}
	}
}

// ---------------------------------------------------------------------------
// list_projects / list_agents / list_members
// ---------------------------------------------------------------------------

func TestAssistantListProjects(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-projects@agora.dev")
	outsider := newAssistantTestUser(t, "assistant-projects-out@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-projects-ws", "PRJ")
	addAssistantTestMember(t, ws, user, "owner")
	newAssistantTestProject(t, ws, "Payments rewrite")

	result, err := executeAssistantTool(t, user, assistant.ToolListProjects, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("list_projects: %v", err)
	}
	rows, _ := result["projects"].([]any)
	if len(rows) != 1 {
		t.Fatalf("projects = %v", result["projects"])
	}
	row := rows[0].(map[string]any)
	if row["title"] != "Payments rewrite" || row["id"] == "" || row["status"] == nil {
		t.Fatalf("project row = %v", row)
	}

	if _, err := executeAssistantTool(t, outsider, assistant.ToolListProjects,
		`{"workspace_id":"`+ws+`"}`); err == nil {
		t.Fatal("a non-member must not list projects")
	}
}

func TestAssistantListMembers(t *testing.T) {
	owner := newAssistantTestUser(t, "assistant-members-owner@agora.dev")
	member := newAssistantTestUser(t, "assistant-members-member@agora.dev")
	outsider := newAssistantTestUser(t, "assistant-members-out@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-members-ws", "MEM")
	addAssistantTestMember(t, ws, owner, "owner")
	addAssistantTestMember(t, ws, member, "member")

	result, err := executeAssistantTool(t, member, assistant.ToolListMembers, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("list_members: %v", err)
	}
	rows, _ := result["members"].([]any)
	roles := map[string]string{}
	for _, r := range rows {
		m := r.(map[string]any)
		roles[m["user_id"].(string)] = m["role"].(string)
		if m["name"] == "" {
			t.Fatalf("member row has no name: %v", m)
		}
	}
	if roles[owner] != "owner" || roles[member] != "member" {
		t.Fatalf("roles = %v", roles)
	}

	if _, err := executeAssistantTool(t, outsider, assistant.ToolListMembers,
		`{"workspace_id":"`+ws+`"}`); err == nil {
		t.Fatal("a non-member must not list members")
	}
}

// list_agents runs through the same private-agent predicate the chat and
// assignment surfaces use: a plain member sees public agents and their own
// private ones, never someone else's private agent.
func TestAssistantListAgentsRespectsPrivateAccess(t *testing.T) {
	owner := newAssistantTestUser(t, "assistant-agents-owner@agora.dev")
	member := newAssistantTestUser(t, "assistant-agents-member@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-agents-ws", "AGT")
	addAssistantTestMember(t, ws, owner, "owner")
	addAssistantTestMember(t, ws, member, "member")
	runtimeID := newAssistantTestRuntime(t, ws)

	newAssistantTestAgent(t, ws, runtimeID, "Public Helper", "workspace", owner)
	newAssistantTestAgent(t, ws, runtimeID, "Owners Private", "private", owner)
	newAssistantTestAgent(t, ws, runtimeID, "Members Private", "private", member)

	names := func(userID string) map[string]bool {
		result, err := executeAssistantTool(t, userID, assistant.ToolListAgents, `{"workspace_id":"`+ws+`"}`)
		if err != nil {
			t.Fatalf("list_agents: %v", err)
		}
		out := map[string]bool{}
		rows, _ := result["agents"].([]any)
		for _, r := range rows {
			out[r.(map[string]any)["name"].(string)] = true
		}
		return out
	}

	memberSees := names(member)
	if !memberSees["Public Helper"] || !memberSees["Members Private"] {
		t.Fatalf("member should see public + own private agents, got %v", memberSees)
	}
	if memberSees["Owners Private"] {
		t.Fatalf("member must NOT see another user's private agent: %v", memberSees)
	}

	// A workspace owner sees everything (roleAllowed owner/admin).
	ownerSees := names(owner)
	if !ownerSees["Owners Private"] || !ownerSees["Members Private"] {
		t.Fatalf("owner should see every agent, got %v", ownerSees)
	}
}

// ---------------------------------------------------------------------------
// create_issue
// ---------------------------------------------------------------------------

func TestAssistantCreateIssuePersistsWithCorrectActor(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-create@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-create-ws", "CRE")
	addAssistantTestMember(t, ws, user, "owner")
	projectID := newAssistantTestProject(t, ws, "Inbox")
	created := recordBusEvents(t, protocol.EventIssueCreated)

	result, err := executeAssistantTool(t, user, assistant.ToolCreateIssue,
		`{"workspace_id":"`+ws+`","title":"Fix the login form","description":"It 500s on submit.",`+
			`"priority":"high","project_id":"`+projectID+`"}`)
	if err != nil {
		t.Fatalf("create_issue: %v", err)
	}
	if result["created"] != true {
		t.Fatalf("result = %v", result)
	}
	issue, _ := result["issue"].(map[string]any)
	if issue["identifier"] != "CRE-1" || issue["url_path"] != "/assistant-create-ws/issues/CRE-1" {
		t.Fatalf("issue = %v", issue)
	}

	var issueID, title, priority, creatorType, creatorID, storedProject string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id, title, priority, creator_type, creator_id, project_id
		  FROM issue WHERE workspace_id = $1 AND number = 1
	`, ws).Scan(&issueID, &title, &priority, &creatorType, &creatorID, &storedProject); err != nil {
		t.Fatalf("issue was not persisted: %v", err)
	}
	if title != "Fix the login form" || priority != "high" || storedProject != projectID {
		t.Fatalf("stored %s / %s / %s", title, priority, storedProject)
	}
	// The mutation is attributed to the HUMAN, not to a synthetic actor.
	if creatorType != "member" || creatorID != user {
		t.Fatalf("creator = %s/%s, want member/%s", creatorType, creatorID, user)
	}

	// Attribution marker so the timeline can say "via Agora Assistant".
	if meta := storedIssueMetadata(t, issueID); meta["via_assistant"] != true {
		t.Fatalf("metadata = %v", meta)
	}

	// The real create path ran, so the create event reached the bus — that is
	// what drives WS invalidation, notifications and automations.
	found := false
	for _, e := range created() {
		if payload, ok := e.Payload.(map[string]any); ok {
			if resp, ok := payload["issue"].(IssueResponse); ok && resp.ID == issueID {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("no issue:created event carried the new issue")
	}
}

func TestAssistantCreateIssueRefusesNonMember(t *testing.T) {
	outsider := newAssistantTestUser(t, "assistant-create-out@agora.dev")
	insider := newAssistantTestUser(t, "assistant-create-in@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-create-closed", "CRC")
	addAssistantTestMember(t, ws, insider, "owner")

	_, err := executeAssistantTool(t, outsider, assistant.ToolCreateIssue,
		`{"workspace_id":"`+ws+`","title":"sneaky"}`)
	if err == nil || !strings.Contains(err.Error(), "not a member") {
		t.Fatalf("expected a membership refusal, got %v", err)
	}

	var count int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue WHERE workspace_id = $1`, ws).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("a refused create wrote %d issue(s)", count)
	}
}

func TestAssistantCreateIssueValidatesArguments(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-create-valid@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-create-valid-ws", "CRV")
	addAssistantTestMember(t, ws, user, "owner")

	cases := []struct {
		name, args, wants string
	}{
		{"no title", `{"workspace_id":"` + ws + `","title":"   "}`, "title is required"},
		{"bad priority", `{"workspace_id":"` + ws + `","title":"x","priority":"URGENT!!"}`, "priority must be one of"},
		{"bad status", `{"workspace_id":"` + ws + `","title":"x","status":"shipped"}`, "status must be one of"},
		{"half assignee", `{"workspace_id":"` + ws + `","title":"x","assignee_type":"member"}`, "must be sent together"},
		{"bad assignee type", `{"workspace_id":"` + ws + `","title":"x","assignee_type":"robot","assignee_id":"` + user + `"}`, "assignee_type must be one of"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := executeAssistantTool(t, user, assistant.ToolCreateIssue, tc.args)
			if err == nil {
				t.Fatal("expected a tool error")
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("error = %q, want it to mention %q", err, tc.wants)
			}
		})
	}

	var count int
	testPool.QueryRow(context.Background(), `SELECT count(*) FROM issue WHERE workspace_id = $1`, ws).Scan(&count)
	if count != 0 {
		t.Fatalf("rejected creates still wrote %d issue(s)", count)
	}
}

// A forged assignee id must be refused by the SAME validation the HTTP handler
// runs — the executor does not get its own, weaker, policy.
func TestAssistantCreateIssueRejectsUnknownAssignee(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-create-assignee@agora.dev")
	other := newAssistantTestUser(t, "assistant-create-assignee-other@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-create-assignee-ws", "CRA")
	addAssistantTestMember(t, ws, user, "owner")

	// `other` is a real user but not a member of this workspace.
	if _, err := executeAssistantTool(t, user, assistant.ToolCreateIssue,
		`{"workspace_id":"`+ws+`","title":"x","assignee_type":"member","assignee_id":"`+other+`"}`); err == nil {
		t.Fatal("assigning to a non-member must be refused")
	}
}

// ---------------------------------------------------------------------------
// update_issue
// ---------------------------------------------------------------------------

func TestAssistantUpdateIssueChangesFieldsAndEmitsEvent(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-update@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-update-ws", "UPD")
	addAssistantTestMember(t, ws, user, "owner")
	issueID := newAssistantTestIssue(t, ws, "ship the thing", user, user)
	updated := recordBusEvents(t, protocol.EventIssueUpdated)

	result, err := executeAssistantTool(t, user, assistant.ToolUpdateIssue,
		`{"workspace_id":"`+ws+`","ref":"UPD-1","status":"in_progress","priority":"urgent","due_date":"2026-12-31"}`)
	if err != nil {
		t.Fatalf("update_issue: %v", err)
	}
	if result["updated"] != true {
		t.Fatalf("result = %v", result)
	}

	var status, priority, due string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status, priority, to_char(due_date, 'YYYY-MM-DD') FROM issue WHERE id = $1`, issueID,
	).Scan(&status, &priority, &due); err != nil {
		t.Fatalf("read issue: %v", err)
	}
	if status != "in_progress" || priority != "urgent" || due != "2026-12-31" {
		t.Fatalf("stored %s / %s / %s", status, priority, due)
	}
	if meta := storedIssueMetadata(t, issueID); meta["via_assistant"] != true {
		t.Fatalf("metadata = %v", meta)
	}

	// The real update path ran: the event carries the status transition the
	// rest of the platform keys off.
	sawStatusChange := false
	for _, e := range updated() {
		payload, ok := e.Payload.(map[string]any)
		if !ok {
			continue
		}
		resp, ok := payload["issue"].(IssueResponse)
		if !ok || resp.ID != issueID {
			continue
		}
		if payload["status_changed"] == true && payload["prev_status"] == "todo" {
			sawStatusChange = true
		}
		if e.ActorType != "member" || e.ActorID != user {
			t.Fatalf("event actor = %s/%s, want member/%s", e.ActorType, e.ActorID, user)
		}
	}
	if !sawStatusChange {
		t.Fatal("no issue:updated event reported the status transition")
	}
}

// Only the fields the model passed may move.
func TestAssistantUpdateIssueLeavesUnmentionedFieldsAlone(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-update-partial@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-update-partial-ws", "UPP")
	addAssistantTestMember(t, ws, user, "owner")
	issueID := newAssistantTestIssue(t, ws, "keep my priority", user, user)

	if _, err := executeAssistantTool(t, user, assistant.ToolUpdateIssue,
		`{"workspace_id":"`+ws+`","ref":"UPP-1","status":"in_progress"}`); err != nil {
		t.Fatalf("update_issue: %v", err)
	}
	var title, priority string
	var assignee *string
	if err := testPool.QueryRow(context.Background(),
		`SELECT title, priority, assignee_id::text FROM issue WHERE id = $1`, issueID,
	).Scan(&title, &priority, &assignee); err != nil {
		t.Fatalf("read issue: %v", err)
	}
	if title != "keep my priority" || priority != "medium" {
		t.Fatalf("untouched fields moved: %s / %s", title, priority)
	}
	if assignee == nil || *assignee != user {
		t.Fatalf("assignee was clobbered: %v", assignee)
	}
}

// A bogus enum must come back as a correctable tool error — never a panic, and
// never a half-applied write.
func TestAssistantUpdateIssueRejectsBogusStatus(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-update-enum@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-update-enum-ws", "UPE")
	addAssistantTestMember(t, ws, user, "owner")
	issueID := newAssistantTestIssue(t, ws, "enum guard", user, user)

	_, err := executeAssistantTool(t, user, assistant.ToolUpdateIssue,
		`{"workspace_id":"`+ws+`","ref":"UPE-1","status":"bogus"}`)
	if err == nil {
		t.Fatal("expected a tool error")
	}
	if !strings.Contains(err.Error(), "status must be one of") {
		t.Fatalf("error = %q", err)
	}

	var status string
	testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id = $1`, issueID).Scan(&status)
	if status != "todo" {
		t.Fatalf("the rejected update still moved the issue to %q", status)
	}
}

func TestAssistantUpdateIssueRequiresSomethingToChange(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-update-noop@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-update-noop-ws", "UPN")
	addAssistantTestMember(t, ws, user, "owner")
	newAssistantTestIssue(t, ws, "nothing to do", user, user)

	if _, err := executeAssistantTool(t, user, assistant.ToolUpdateIssue,
		`{"workspace_id":"`+ws+`","ref":"UPN-1"}`); err == nil {
		t.Fatal("an update with no fields must be refused")
	}
}

// A non-owner cannot blind-write an issue they are not allowed to READ.
func TestAssistantUpdateIssueHonorsVisibilityGate(t *testing.T) {
	owner := newAssistantTestUser(t, "assistant-updgate-owner@agora.dev")
	member := newAssistantTestUser(t, "assistant-updgate-member@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-updgate-ws", "UPG")
	addAssistantTestMember(t, ws, owner, "owner")
	addAssistantTestMember(t, ws, member, "member")
	ownerIssue := newAssistantTestIssue(t, ws, "owner only", owner, owner)

	_, err := executeAssistantTool(t, member, assistant.ToolUpdateIssue,
		`{"workspace_id":"`+ws+`","ref":"UPG-1","status":"cancelled"}`)
	if err == nil {
		t.Fatal("a non-owner must not update another member's issue")
	}

	var status string
	testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id = $1`, ownerIssue).Scan(&status)
	if status != "todo" {
		t.Fatalf("the blocked update still landed: %q", status)
	}
}

func TestAssistantUpdateIssueRefusesNonMember(t *testing.T) {
	outsider := newAssistantTestUser(t, "assistant-upd-out@agora.dev")
	insider := newAssistantTestUser(t, "assistant-upd-in@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-upd-closed", "UPC")
	addAssistantTestMember(t, ws, insider, "owner")
	newAssistantTestIssue(t, ws, "theirs", insider, insider)

	if _, err := executeAssistantTool(t, outsider, assistant.ToolUpdateIssue,
		`{"workspace_id":"`+ws+`","ref":"UPC-1","status":"done"}`); err == nil ||
		!strings.Contains(err.Error(), "not a member") {
		t.Fatalf("expected a membership refusal, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// comment_issue
// ---------------------------------------------------------------------------

func TestAssistantCommentIssuePostsAsTheUser(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-comment@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-comment-ws", "CMT")
	addAssistantTestMember(t, ws, user, "owner")
	issueID := newAssistantTestIssue(t, ws, "needs a note", user, user)
	created := recordBusEvents(t, protocol.EventCommentCreated)

	result, err := executeAssistantTool(t, user, assistant.ToolCommentIssue,
		`{"workspace_id":"`+ws+`","ref":"CMT-1","body":"Deployed to staging, please retest."}`)
	if err != nil {
		t.Fatalf("comment_issue: %v", err)
	}
	if result["commented"] != true || result["issue_identifier"] != "CMT-1" {
		t.Fatalf("result = %v", result)
	}

	var content, authorType, authorID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT content, author_type, author_id FROM comment WHERE issue_id = $1`, issueID,
	).Scan(&content, &authorType, &authorID); err != nil {
		t.Fatalf("comment was not persisted: %v", err)
	}
	if content != "Deployed to staging, please retest." {
		t.Fatalf("content = %q", content)
	}
	// Authored by the human, through the normal comment path.
	if authorType != "member" || authorID != user {
		t.Fatalf("author = %s/%s, want member/%s", authorType, authorID, user)
	}
	if meta := storedIssueMetadata(t, issueID); meta["via_assistant"] != true {
		t.Fatalf("metadata = %v", meta)
	}
	if len(created()) == 0 {
		t.Fatal("no comment:created event reached the bus")
	}
}

func TestAssistantCommentIssueValidates(t *testing.T) {
	outsider := newAssistantTestUser(t, "assistant-comment-out@agora.dev")
	user := newAssistantTestUser(t, "assistant-comment-valid@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-comment-valid-ws", "CMV")
	addAssistantTestMember(t, ws, user, "owner")
	newAssistantTestIssue(t, ws, "guarded", user, user)

	if _, err := executeAssistantTool(t, outsider, assistant.ToolCommentIssue,
		`{"workspace_id":"`+ws+`","ref":"CMV-1","body":"hello"}`); err == nil ||
		!strings.Contains(err.Error(), "not a member") {
		t.Fatalf("expected a membership refusal, got %v", err)
	}
	if _, err := executeAssistantTool(t, user, assistant.ToolCommentIssue,
		`{"workspace_id":"`+ws+`","ref":"CMV-1","body":"   "}`); err == nil {
		t.Fatal("an empty comment must be refused")
	}

	var count int
	testPool.QueryRow(context.Background(), `SELECT count(*) FROM comment WHERE workspace_id = $1`, ws).Scan(&count)
	if count != 0 {
		t.Fatalf("rejected comments still wrote %d row(s)", count)
	}
}

// ---------------------------------------------------------------------------
// Analytics
// ---------------------------------------------------------------------------

func TestAssistantUsageSummaryShape(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-usage@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-usage-ws", "USG")
	addAssistantTestMember(t, ws, user, "owner")
	runtimeID := newAssistantTestRuntime(t, ws)
	agentID := newAssistantTestAgent(t, ws, runtimeID, "Usage Agent", "workspace", user)
	issueID := newAssistantTestIssue(t, ws, "burned some tokens", user, user)

	ctx := context.Background()
	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, issue_id, runtime_id, status, started_at, completed_at, created_at)
		VALUES ($1, $2, $3, 'completed', now() - interval '30 minutes', now() - interval '20 minutes', now())
		RETURNING id
	`, agentID, issueID, runtimeID).Scan(&taskID); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO task_usage (task_id, provider, model, input_tokens, output_tokens, created_at)
		VALUES ($1, 'claude', 'claude-3-5-sonnet', 1200, 300, now())
	`, taskID); err != nil {
		t.Fatalf("insert task_usage: %v", err)
	}
	// The dashboard rollups read task_usage_hourly; in production a cron tick
	// fills it, so the fixture drives the same window function directly.
	if _, err := testPool.Exec(ctx,
		`SELECT rollup_task_usage_hourly_window('1970-01-01'::timestamptz, now() + interval '1 hour')`); err != nil {
		t.Fatalf("rollup: %v", err)
	}

	result, err := executeAssistantTool(t, user, assistant.ToolUsageSummary,
		`{"workspace_id":"`+ws+`","range_days":7}`)
	if err != nil {
		t.Fatalf("usage_summary: %v", err)
	}
	if result["range_days"].(float64) != 7 {
		t.Fatalf("range_days = %v", result["range_days"])
	}
	if got := result["total_tokens"].(float64); got != 1500 {
		t.Fatalf("total_tokens = %v, want 1500", got)
	}
	byAgent, _ := result["by_agent"].([]any)
	if len(byAgent) != 1 {
		t.Fatalf("by_agent = %v", result["by_agent"])
	}
	agentRow := byAgent[0].(map[string]any)
	if agentRow["agent_id"] != agentID || agentRow["agent_name"] != "Usage Agent" {
		t.Fatalf("agent row = %v", agentRow)
	}
	byDay, _ := result["by_day"].([]any)
	if len(byDay) != 1 {
		t.Fatalf("by_day = %v", result["by_day"])
	}

	// Window clamp: a model asking for a year gets the documented maximum.
	clamped, err := executeAssistantTool(t, user, assistant.ToolUsageSummary,
		`{"workspace_id":"`+ws+`","range_days":365}`)
	if err != nil {
		t.Fatalf("usage_summary(365): %v", err)
	}
	if clamped["range_days"].(float64) != float64(assistant.MaxUsageRangeDays) {
		t.Fatalf("range_days = %v, want clamped to %d", clamped["range_days"], assistant.MaxUsageRangeDays)
	}
}

func TestAssistantUsageSummaryRefusesNonMember(t *testing.T) {
	outsider := newAssistantTestUser(t, "assistant-usage-out@agora.dev")
	insider := newAssistantTestUser(t, "assistant-usage-in@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-usage-closed", "USC")
	addAssistantTestMember(t, ws, insider, "owner")

	if _, err := executeAssistantTool(t, outsider, assistant.ToolUsageSummary,
		`{"workspace_id":"`+ws+`"}`); err == nil {
		t.Fatal("a non-member must not read usage")
	}
}

func seedAssistantActivity(t *testing.T, workspaceID, issueID, actorID, action string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO activity_log (workspace_id, issue_id, actor_type, actor_id, action, details)
		VALUES ($1, $2, 'member', $3, $4, '{}'::jsonb)
	`, workspaceID, issueID, actorID, action); err != nil {
		t.Fatalf("insert activity: %v", err)
	}
}

func TestAssistantActivityDigest(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-digest@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-digest-ws", "DIG")
	addAssistantTestMember(t, ws, user, "owner")
	issueID := newAssistantTestIssue(t, ws, "digest me", user, user)
	seedAssistantActivity(t, ws, issueID, user, "status_changed")

	result, err := executeAssistantTool(t, user, assistant.ToolActivityDigest, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("activity_digest: %v", err)
	}
	rows, _ := result["activity"].([]any)
	if len(rows) != 1 {
		t.Fatalf("activity = %v", result["activity"])
	}
	row := rows[0].(map[string]any)
	if row["issue_identifier"] != "DIG-1" || row["type"] != "status_changed" {
		t.Fatalf("row = %v", row)
	}
	if row["workspace_slug"] != "assistant-digest-ws" {
		t.Fatalf("workspace_slug = %v", row["workspace_slug"])
	}
	if !strings.Contains(row["actor"].(string), "assistant-digest@agora.dev") {
		t.Fatalf("actor should resolve to a name, got %v", row["actor"])
	}
	if row["created_at"] == "" {
		t.Fatalf("created_at missing: %v", row)
	}
}

// The digest is gated too: a non-owner must not learn about activity on issues
// they cannot read.
func TestAssistantActivityDigestHonorsVisibilityGate(t *testing.T) {
	owner := newAssistantTestUser(t, "assistant-digestgate-owner@agora.dev")
	member := newAssistantTestUser(t, "assistant-digestgate-member@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-digestgate-ws", "DGG")
	addAssistantTestMember(t, ws, owner, "owner")
	addAssistantTestMember(t, ws, member, "member")

	ownerIssue := newAssistantTestIssue(t, ws, "owner work", owner, owner)
	memberIssue := newAssistantTestIssue(t, ws, "member work", member, member)
	seedAssistantActivity(t, ws, ownerIssue, owner, "status_changed")
	seedAssistantActivity(t, ws, memberIssue, member, "status_changed")

	result, err := executeAssistantTool(t, member, assistant.ToolActivityDigest, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("activity_digest: %v", err)
	}
	rows, _ := result["activity"].([]any)
	if len(rows) != 1 {
		t.Fatalf("a non-owner should see only their own issue's activity, got %v", result["activity"])
	}
	if rows[0].(map[string]any)["issue_identifier"] != "DGG-2" {
		t.Fatalf("row = %v", rows[0])
	}

	ownerResult, err := executeAssistantTool(t, owner, assistant.ToolActivityDigest, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("owner digest: %v", err)
	}
	if ownerRows, _ := ownerResult["activity"].([]any); len(ownerRows) != 2 {
		t.Fatalf("owner should see both, got %v", ownerResult["activity"])
	}
}

// Unscoped, the digest fans out over MEMBERSHIPS only.
func TestAssistantActivityDigestUnscopedFansOutToMemberships(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-digestfan@agora.dev")
	other := newAssistantTestUser(t, "assistant-digestfan-other@agora.dev")
	mine := newAssistantTestWorkspace(t, "assistant-digestfan-mine", "DFM")
	foreign := newAssistantTestWorkspace(t, "assistant-digestfan-foreign", "DFF")
	addAssistantTestMember(t, mine, user, "owner")
	addAssistantTestMember(t, foreign, other, "owner")

	mineIssue := newAssistantTestIssue(t, mine, "mine", user, user)
	foreignIssue := newAssistantTestIssue(t, foreign, "theirs", other, other)
	seedAssistantActivity(t, mine, mineIssue, user, "status_changed")
	seedAssistantActivity(t, foreign, foreignIssue, other, "status_changed")

	result, err := executeAssistantTool(t, user, assistant.ToolActivityDigest, `{"since_days":30}`)
	if err != nil {
		t.Fatalf("activity_digest: %v", err)
	}
	rows, _ := result["activity"].([]any)
	for _, r := range rows {
		if slug := r.(map[string]any)["workspace_slug"]; slug == "assistant-digestfan-foreign" {
			t.Fatalf("a non-membership workspace leaked into the digest: %v", r)
		}
	}
	if len(rows) != 1 || rows[0].(map[string]any)["workspace_slug"] != "assistant-digestfan-mine" {
		t.Fatalf("activity = %v", result["activity"])
	}
}

func seedAssistantInbox(t *testing.T, workspaceID, recipientID, title string, read bool) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO inbox_item (workspace_id, recipient_type, recipient_id, type, severity, title, body, read)
		VALUES ($1, 'member', $2, 'mention', 'info', $3, 'some body text', $4)
	`, workspaceID, recipientID, title, read); err != nil {
		t.Fatalf("insert inbox item: %v", err)
	}
}

func TestAssistantInboxSummary(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-inbox@agora.dev")
	other := newAssistantTestUser(t, "assistant-inbox-other@agora.dev")
	wsA := newAssistantTestWorkspace(t, "assistant-inbox-a", "INA")
	wsB := newAssistantTestWorkspace(t, "assistant-inbox-b", "INB")
	foreign := newAssistantTestWorkspace(t, "assistant-inbox-foreign", "INF")
	addAssistantTestMember(t, wsA, user, "owner")
	addAssistantTestMember(t, wsB, user, "member")
	addAssistantTestMember(t, foreign, other, "owner")

	seedAssistantInbox(t, wsA, user, "unread in A", false)
	seedAssistantInbox(t, wsA, user, "already read in A", true)
	seedAssistantInbox(t, wsB, user, "unread in B", false)
	seedAssistantInbox(t, wsA, other, "not for me", false)
	// Addressed to the user in a workspace they are NOT a member of.
	seedAssistantInbox(t, foreign, user, "unreachable", false)

	result, err := executeAssistantTool(t, user, assistant.ToolInboxSummary, `{}`)
	if err != nil {
		t.Fatalf("inbox_summary: %v", err)
	}
	rows, _ := result["items"].([]any)
	titles := map[string]string{}
	for _, r := range rows {
		m := r.(map[string]any)
		titles[m["title"].(string)] = m["workspace_slug"].(string)
	}
	if len(titles) != 2 {
		t.Fatalf("expected the two unread membership items, got %v", titles)
	}
	if titles["unread in A"] != "assistant-inbox-a" || titles["unread in B"] != "assistant-inbox-b" {
		t.Fatalf("items = %v", titles)
	}
	for _, unwanted := range []string{"already read in A", "not for me", "unreachable"} {
		if _, leaked := titles[unwanted]; leaked {
			t.Fatalf("%q must not appear: %v", unwanted, titles)
		}
	}
	if result["unread_count"].(float64) != 2 {
		t.Fatalf("unread_count = %v", result["unread_count"])
	}
	if rows[0].(map[string]any)["preview"] != "some body text" {
		t.Fatalf("preview = %v", rows[0])
	}
}

func TestAssistantQAStatusShape(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-qa@agora.dev")
	outsider := newAssistantTestUser(t, "assistant-qa-out@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-qa-ws", "QAS")
	addAssistantTestMember(t, ws, user, "owner")

	result, err := executeAssistantTool(t, user, assistant.ToolQAStatus, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("qa_status: %v", err)
	}
	for _, key := range []string{"runs_total", "runs_passed", "runs_failed", "runs_skipped", "cases_automated", "cases_scripted"} {
		if _, ok := result[key].(float64); !ok {
			t.Fatalf("%s is not a number: %v", key, result[key])
		}
	}
	if result["workspace_slug"] != "assistant-qa-ws" || result["window_days"].(float64) != 30 {
		t.Fatalf("result = %v", result)
	}

	if _, err := executeAssistantTool(t, outsider, assistant.ToolQAStatus,
		`{"workspace_id":"`+ws+`"}`); err == nil {
		t.Fatal("a non-member must not read QA status")
	}
}

// ---------------------------------------------------------------------------
// Cross-cutting
// ---------------------------------------------------------------------------

// assistantWorkspaceGateArgs is one well-formed argument blob per
// workspace-scoped tool. Shared by the membership-gate meta-test and the
// denial-wording test below so the two can never drift apart: a tool added to
// one is covered by both.
func assistantWorkspaceGateArgs(ws, insider string) map[string]string {
	return map[string]string{
		assistant.ToolListMyIssues:       `{"workspace_id":"` + ws + `"}`,
		assistant.ToolSearchIssues:       `{"workspace_id":"` + ws + `","query":"guarded"}`,
		assistant.ToolGetIssue:           `{"workspace_id":"` + ws + `","ref":"ALG-1"}`,
		assistant.ToolListComments:       `{"workspace_id":"` + ws + `","ref":"ALG-1"}`,
		assistant.ToolListProjects:       `{"workspace_id":"` + ws + `"}`,
		assistant.ToolGetProject:         `{"workspace_id":"` + ws + `","project":"anything"}`,
		assistant.ToolListSprints:        `{"workspace_id":"` + ws + `"}`,
		assistant.ToolListLabels:         `{"workspace_id":"` + ws + `"}`,
		assistant.ToolListAgents:         `{"workspace_id":"` + ws + `"}`,
		assistant.ToolListSquads:         `{"workspace_id":"` + ws + `"}`,
		assistant.ToolListMembers:        `{"workspace_id":"` + ws + `"}`,
		assistant.ToolListRuntimes:       `{"workspace_id":"` + ws + `"}`,
		assistant.ToolListSkills:         `{"workspace_id":"` + ws + `"}`,
		assistant.ToolCreateIssue:        `{"workspace_id":"` + ws + `","title":"x"}`,
		assistant.ToolUpdateIssue:        `{"workspace_id":"` + ws + `","ref":"ALG-1","status":"done"}`,
		assistant.ToolCommentIssue:       `{"workspace_id":"` + ws + `","ref":"ALG-1","body":"x"}`,
		assistant.ToolArchiveIssue:       `{"workspace_id":"` + ws + `","ref":"ALG-1"}`,
		assistant.ToolAddIssueLabel:      `{"workspace_id":"` + ws + `","ref":"ALG-1","label":"bug"}`,
		assistant.ToolRemoveIssueLabel:   `{"workspace_id":"` + ws + `","ref":"ALG-1","label":"bug"}`,
		assistant.ToolMoveIssueToSprint:  `{"workspace_id":"` + ws + `","ref":"ALG-1","sprint":"Sprint 1"}`,
		assistant.ToolCreateProject:      `{"workspace_id":"` + ws + `","title":"x"}`,
		assistant.ToolUpdateProject:      `{"workspace_id":"` + ws + `","project":"x","status":"completed"}`,
		assistant.ToolCreateSprint:       `{"workspace_id":"` + ws + `","project_id":"x","name":"Sprint 1"}`,
		assistant.ToolCreateLabel:        `{"workspace_id":"` + ws + `","name":"bug"}`,
		assistant.ToolCreateAgent:        `{"workspace_id":"` + ws + `","name":"Bot","runtime_id":"` + ws + `"}`,
		assistant.ToolUpdateAgent:        `{"workspace_id":"` + ws + `","agent_id":"Bot","name":"Bot2"}`,
		assistant.ToolAddSkill:           `{"workspace_id":"` + ws + `","url":"https://github.com/acme/skill"}`,
		assistant.ToolAttachSkillToAgent: `{"workspace_id":"` + ws + `","agent_id":"Bot","skill_id":"s"}`,
		assistant.ToolUsageSummary:       `{"workspace_id":"` + ws + `"}`,
		assistant.ToolActivityDigest:     `{"workspace_id":"` + ws + `"}`,
		assistant.ToolQAStatus:           `{"workspace_id":"` + ws + `"}`,

		// The MAIN-RULE parity tools. Every one of them is workspace-scoped, so
		// every one of them owes an outsider a refusal — including the deletes,
		// which are sent WITH confirm:true here on purpose: the confirm gate
		// runs before dispatch, so a blob without it would pass this test for
		// the wrong reason (refused for a missing boolean rather than for a
		// missing membership) and hide an ungated delete.
		assistant.ToolListIssues:           `{"workspace_id":"` + ws + `"}`,
		assistant.ToolListAutopilots:       `{"workspace_id":"` + ws + `"}`,
		assistant.ToolListAutomations:      `{"workspace_id":"` + ws + `"}`,
		assistant.ToolListIntegrations:     `{"workspace_id":"` + ws + `"}`,
		assistant.ToolDeleteIssue:          `{"workspace_id":"` + ws + `","ref":"ALG-1","confirm":true}`,
		assistant.ToolDeleteProject:        `{"workspace_id":"` + ws + `","project":"x","confirm":true}`,
		assistant.ToolDeleteSprint:         `{"workspace_id":"` + ws + `","sprint":"Sprint 1","confirm":true}`,
		assistant.ToolDeleteLabel:          `{"workspace_id":"` + ws + `","label":"bug","confirm":true}`,
		assistant.ToolDeleteComment:        `{"workspace_id":"` + ws + `","comment_id":"` + ws + `","confirm":true}`,
		assistant.ToolUpdateComment:        `{"workspace_id":"` + ws + `","comment_id":"` + ws + `","body":"x"}`,
		assistant.ToolResolveComment:       `{"workspace_id":"` + ws + `","comment_id":"` + ws + `"}`,
		assistant.ToolMarkInboxRead:        `{"workspace_id":"` + ws + `"}`,
		assistant.ToolPinItem:              `{"workspace_id":"` + ws + `","item_type":"issue","item":"ALG-1"}`,
		assistant.ToolSubscribeIssue:       `{"workspace_id":"` + ws + `","ref":"ALG-1"}`,
		assistant.ToolInviteMember:         `{"workspace_id":"` + ws + `","email":"outsider@agora.dev"}`,
		assistant.ToolUpdateMemberRole:     `{"workspace_id":"` + ws + `","user_id":"` + insider + `","role":"admin"}`,
		assistant.ToolRemoveMember:         `{"workspace_id":"` + ws + `","user_id":"` + insider + `","confirm":true}`,
		assistant.ToolUpdateWorkspace:      `{"workspace_id":"` + ws + `","name":"Renamed"}`,
		assistant.ToolLeaveWorkspace:       `{"workspace_id":"` + ws + `","confirm":true}`,
		assistant.ToolDeleteWorkspace:      `{"workspace_id":"` + ws + `","confirm":true}`,
		assistant.ToolCreateAutopilot:      `{"workspace_id":"` + ws + `","title":"Nightly","assignee_id":"` + ws + `"}`,
		assistant.ToolUpdateAutopilot:      `{"workspace_id":"` + ws + `","autopilot":"Nightly","status":"paused"}`,
		assistant.ToolRunAutopilotNow:      `{"workspace_id":"` + ws + `","autopilot":"Nightly"}`,
		assistant.ToolCreateAutomation:     `{"workspace_id":"` + ws + `","name":"R","trigger_type":"issue.created","actions":"[{\"type\":\"add_label\",\"config\":{\"name\":\"bug\"}}]"}`,
		assistant.ToolSetAutomationEnabled: `{"workspace_id":"` + ws + `","automation":"R","enabled":false}`,
		assistant.ToolDeleteAutomation:     `{"workspace_id":"` + ws + `","automation":"R","confirm":true}`,

		// The settings tools that DO name a workspace. get_my_settings reads
		// the caller's own profile and would otherwise be user-scoped, but the
		// half of its answer that is per workspace (notification preferences)
		// must never be readable from outside — so when it is handed a
		// workspace it owes an outsider the same refusal as every other tool.
		assistant.ToolGetMySettings:                 `{"workspace_id":"` + ws + `"}`,
		assistant.ToolUpdateNotificationPreferences: `{"workspace_id":"` + ws + `","preferences":{"comments":"muted"}}`,
	}
}

// assistantUnscopedTools are the catalog entries that take no workspace at all.
// Their gates are asserted individually (session ownership for the artifact
// tools, membership-inside-the-tool for the two fan-outs, the auth layer plus
// DISABLE_WORKSPACE_CREATION for create_workspace).
var assistantUnscopedTools = map[string]bool{
	assistant.ToolListWorkspaces:  true,
	assistant.ToolInboxSummary:    true,
	assistant.ToolCreateArtifact:  true,
	assistant.ToolUpdateArtifact:  true,
	assistant.ToolCreateWorkspace: true,
	// Personal preferences on the caller's own user row. There is no
	// workspace to gate and no other person's row they can reach — the only
	// id in play is the caller's, resolved before dispatch. Their own gate
	// (they change nobody else's settings) is asserted in
	// assistant_settings_test.go.
	assistant.ToolUpdateSidebar:    true,
	assistant.ToolUpdateMySettings: true,
	// A plan takes no workspace of its own — its ITEMS name theirs, and each
	// item's membership check is what gates it. The per-item gate is asserted
	// in TestAssistantPlanItemStillHitsWorkspaceGates.
	assistant.ToolProposePlan: true,
}

// Every tool in the catalog must be dispatchable and must refuse a caller who
// is not a member — no tool may be added without a workspace gate.
func TestEveryWorkspaceScopedToolRefusesNonMembers(t *testing.T) {
	outsider := newAssistantTestUser(t, "assistant-allgates@agora.dev")
	insider := newAssistantTestUser(t, "assistant-allgates-in@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-allgates-ws", "ALG")
	addAssistantTestMember(t, ws, insider, "owner")
	newAssistantTestIssue(t, ws, "guarded issue", insider, insider)

	args := assistantWorkspaceGateArgs(ws, insider)
	for name, blob := range args {
		t.Run(name, func(t *testing.T) {
			if _, err := executeAssistantTool(t, outsider, name, blob); err == nil {
				t.Fatalf("%s returned data to a non-member", name)
			}
		})
	}

	// The artifact tools are gated by SESSION ownership rather than workspace
	// membership (an artifact may aggregate several workspaces at once), so
	// their gate is proven here rather than by the workspace loop above.
	insiderSession := newAssistantTestSession(t, insider)
	insiderArtifact := createTestArtifact(t, insider, insiderSession,
		"Insider's report", assistant.ArtifactKindMarkdown, "# private")
	for _, tc := range []struct{ name, blob string }{
		{assistant.ToolCreateArtifact, `{"title":"x","kind":"markdown","content":"# x"}`},
		{assistant.ToolUpdateArtifact, `{"artifact_id":"` + insiderArtifact + `","content":"# x"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := executeAssistantSessionTool(t, outsider, insiderSession, tc.name, tc.blob); err == nil {
				t.Fatalf("%s acted inside a session the caller does not own", tc.name)
			}
		})
	}

	// Every catalog entry is either covered above or is genuinely
	// workspace-free (list_workspaces, inbox_summary are scoped by
	// membership inside themselves; the artifact tools by session ownership,
	// asserted just above).
	for _, spec := range assistant.ToolSpecs() {
		if _, covered := args[spec.Name]; !covered && !assistantUnscopedTools[spec.Name] {
			t.Fatalf("tool %q has no membership-gate test — add one", spec.Name)
		}
	}
}

// A non-member gets ONE sentence, and it is always the same one.
//
// This is not tidiness. The refusal is relayed verbatim by the model, so it is
// product copy: "you are not a member of that workspace" tells the user what
// happened and what to do about it, while "query is required" — which is what
// search_issues used to answer, because it validated its arguments before it
// checked membership — tells them the assistant is broken. It also leaks the
// ordering of the checks, which is the beginning of an existence oracle.
//
// So membership is resolved FIRST in every workspace-scoped tool, and this test
// is what keeps it there.
func TestWorkspaceToolDenialsUseTheMembershipWording(t *testing.T) {
	outsider := newAssistantTestUser(t, "assistant-denial@agora.dev")
	insider := newAssistantTestUser(t, "assistant-denial-in@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-denial-ws", "DNL")
	addAssistantTestMember(t, ws, insider, "owner")
	newAssistantTestIssue(t, ws, "guarded issue", insider, insider)

	bare := `{"workspace_id":"` + ws + `"}`
	for name, blob := range assistantWorkspaceGateArgs(ws, insider) {
		t.Run(name, func(t *testing.T) {
			// Both a well-formed call AND one missing everything except the
			// workspace. The second is the probe that caught this: a model
			// guessing at a workspace it cannot see rarely fills the other
			// arguments in, and the answer it gets must still be about
			// membership rather than about a missing field.
			for _, args := range []string{blob, bare} {
				_, err := executeAssistantTool(t, outsider, name, args)
				if err == nil {
					t.Fatalf("%s returned data to a non-member (args %s)", name, args)
				}
				if err.Error() != errAssistantNoAccess.Error() {
					t.Fatalf("%s refused a non-member with %q (args %s), want %q — resolve membership before validating arguments",
						name, err.Error(), args, errAssistantNoAccess.Error())
				}
			}
		})
	}
}

// Malformed argument blobs must never panic the run: every tool turns them
// into a tool error the model can correct.
func TestEveryToolSurvivesMalformedArguments(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-malformed@agora.dev")
	// A real session, so the session-scoped tools reach their own argument
	// handling instead of bouncing off a missing conversation.
	session := newAssistantTestSession(t, user)
	for _, spec := range assistant.ToolSpecs() {
		t.Run(spec.Name, func(t *testing.T) {
			for _, blob := range []string{`{`, `[]`, `"a string"`, `{"workspace_id":12345}`} {
				// A panic here fails the test; an error is the expected shape.
				_, _ = testHandler.Execute(context.Background(), user, session, spec.Name, json.RawMessage(blob))
			}
		})
	}
}

// A run must not hang on a tool: each execution is bounded, so an already-dead
// context comes back as an error rather than blocking.
func TestExecuteRespectsContextCancellation(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-ctx@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-ctx-ws", "CTX")
	addAssistantTestMember(t, ws, user, "owner")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := testHandler.Execute(ctx, user, "", assistant.ToolListWorkspaces, json.RawMessage(`{}`)); err == nil {
		t.Fatal("a cancelled context must surface as a tool error")
	}
}

// The whole feature, end to end: a run that asks for create_issue must produce
// a real issue through the real create path, and come back with a text answer
// quoting the identifier. This is the Phase-1 acceptance scenario.
func TestAssistantRunCreatesAnIssueEndToEnd(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-e2e@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-e2e-ws", "E2E")
	addAssistantTestMember(t, ws, user, "owner")
	sessionID := newAssistantTestSession(t, user)
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO assistant_message (session_id, role, content) VALUES ($1, 'user', 'open a bug about the login form')`,
		sessionID); err != nil {
		t.Fatalf("seed user message: %v", err)
	}

	script := &scriptedToolChat{replies: []llm.Message{
		// Round 1: ground the workspace id.
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: assistant.ToolListWorkspaces, Arguments: "{}"},
		}},
		// Round 2: write, using the id the tool returned.
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "c2", Name: assistant.ToolCreateIssue,
				Arguments: `{"workspace_id":"` + ws + `","title":"Login form returns 500","priority":"high"}`},
		}},
		// Round 3: answer.
		{Role: "assistant", Content: "Created E2E-1 — Login form returns 500."},
	}}

	bus := events.New()
	svc := assistant.NewService(testHandler.Queries, bus, func() (llm.ToolChat, string, error) {
		return script, "test-model", nil
	})
	svc.Exec = testHandler
	svc.Run(context.Background(), sessionID, "run-e2e-1", user)

	// The issue exists, created by the human, marked as assistant-driven.
	var issueID, title, priority, creatorType, creatorID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id, title, priority, creator_type, creator_id FROM issue WHERE workspace_id = $1
	`, ws).Scan(&issueID, &title, &priority, &creatorType, &creatorID); err != nil {
		t.Fatalf("the run did not create an issue: %v", err)
	}
	if title != "Login form returns 500" || priority != "high" {
		t.Fatalf("issue = %s / %s", title, priority)
	}
	if creatorType != "member" || creatorID != user {
		t.Fatalf("creator = %s/%s", creatorType, creatorID)
	}
	if meta := storedIssueMetadata(t, issueID); meta["via_assistant"] != true {
		t.Fatalf("metadata = %v", meta)
	}

	// The transcript records both tool answers plus the final text.
	rows, err := testPool.Query(context.Background(),
		`SELECT role, coalesce(tool_name, ''), content FROM assistant_message
		  WHERE session_id = $1 ORDER BY created_at ASC, id ASC`, sessionID)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	defer rows.Close()
	var roles, tools []string
	var lastContent string
	for rows.Next() {
		var role, tool, content string
		if err := rows.Scan(&role, &tool, &content); err != nil {
			t.Fatalf("scan: %v", err)
		}
		roles = append(roles, role)
		if tool != "" {
			tools = append(tools, tool)
		}
		lastContent = content
	}
	wantRoles := "user,assistant,tool,assistant,tool,assistant"
	if strings.Join(roles, ",") != wantRoles {
		t.Fatalf("transcript roles = %v, want %s", roles, wantRoles)
	}
	wantTools := assistant.ToolListWorkspaces + "," + assistant.ToolCreateIssue
	if strings.Join(tools, ",") != wantTools {
		t.Fatalf("tool answers = %v, want %s", tools, wantTools)
	}
	if lastContent != "Created E2E-1 — Login form returns 500." {
		t.Fatalf("final answer = %q", lastContent)
	}

	// The create tool's answer carried the link the UI renders as a chip.
	var toolResult string
	if err := testPool.QueryRow(context.Background(),
		`SELECT content FROM assistant_message WHERE session_id = $1 AND tool_name = $2`,
		sessionID, assistant.ToolCreateIssue).Scan(&toolResult); err != nil {
		t.Fatalf("read create tool result: %v", err)
	}
	if !strings.Contains(toolResult, "/assistant-e2e-ws/issues/E2E-1") {
		t.Fatalf("tool result has no link: %s", toolResult)
	}
}
