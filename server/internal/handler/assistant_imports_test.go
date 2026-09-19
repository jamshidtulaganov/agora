package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/imports"
)

// THE MIGRATION CONCIERGE — tests for the assistant's five import tools.
//
// What they protect, in order of how badly it would hurt to lose it:
//
//  1. THE TOKEN NEVER REACHES THE MODEL. Asserted on the MARSHALED JSON of
//     every tool's answer, not on the structs — the bytes are what the model
//     sees, and a field added to a struct three months from now travels
//     whether or not anyone remembered this rule.
//  2. NOTHING IS WRITTEN BEFORE THE CLICK. A dry run opens a job row and a
//     plan and no issues; confirm_import parks a card and mutates nothing
//     until POST /operations/{id}/confirm arrives from the session's owner.
//  3. THE GATES HOLD. An outsider gets the membership refusal; a plain member
//     gets the role refusal on every write; a second import is refused with a
//     POINTER at the running one rather than by cancelling it.

// ---------------------------------------------------------------------------
// A recorded Linear host
// ---------------------------------------------------------------------------

// assistantImportTestKey is the token the fixture expects. It is a fake with a
// recognisable shape precisely so the leak test has something to grep for: any
// tool answer containing this string is a tool answer containing a credential.
const assistantImportTestKey = "lin_api_ASSISTANTLEAKCANARY0001"

const assistantImportFixtureViewer = `{
  "viewer": {"id":"usr-kim","name":"Kim Ryu","email":"kim@acme.io"},
  "organization": {"id":"org-1","name":"Acme","urlKey":"acme"}
}`

const assistantImportFixtureTeams = `{
  "teams": {
    "pageInfo": {"hasNextPage": false, "endCursor": ""},
    "nodes": [{"id":"team-eng","key":"ENG","name":"Engineering","description":"","icon":null,"archivedAt":null}]
  }
}`

const assistantImportFixtureStates = `{
  "workflowStates": {
    "pageInfo": {"hasNextPage": false, "endCursor": ""},
    "nodes": [
      {"id":"st-todo","name":"Todo","type":"unstarted","position":1,"team":{"id":"team-eng"}},
      {"id":"st-progress","name":"In Progress","type":"started","position":2,"team":{"id":"team-eng"}},
      {"id":"st-hold","name":"Customer reply","type":"deferred","position":3,"team":{"id":"team-eng"}}
    ]
  }
}`

const assistantImportFixtureLabels = `{
  "issueLabels": {
    "pageInfo": {"hasNextPage": false, "endCursor": ""},
    "nodes": [{"id":"lab-bug","name":"Bug","color":"#ef4444"}]
  }
}`

// usr-dana is deliberately NOT a member of the target workspace: the plan must
// report an unmatched author, and the applier must attribute her rows to the
// import identity rather than to whoever confirmed the job.
const assistantImportFixtureUsers = `{
  "users": {
    "pageInfo": {"hasNextPage": false, "endCursor": ""},
    "nodes": [
      {"id":"usr-kim","name":"Kim Ryu","email":"kim@acme.io","active":true},
      {"id":"usr-dana","name":"Dana Wu","email":"dana@gone.example","active":false}
    ]
  }
}`

const assistantImportFixtureCycles = `{
  "cycles": {"pageInfo": {"hasNextPage": false, "endCursor": ""}, "nodes": []}
}`

const assistantImportFixtureIssues = `{
  "issues": {
    "pageInfo": {"hasNextPage": false, "endCursor": ""},
    "nodes": [
      {
        "id":"iss-142","identifier":"ENG-142","number":142,
        "title":"Fix the login redirect loop","description":"The redirect loops on SSO.",
        "priority":2,"estimate":null,"url":"https://linear.app/acme/issue/ENG-142",
        "createdAt":"2024-03-07T09:30:00.000Z","updatedAt":"2025-11-02T16:05:00.000Z",
        "startedAt":null,"completedAt":null,"dueDate":null,"archivedAt":null,
        "state":{"id":"st-progress"},"team":{"id":"team-eng"},"cycle":null,
        "parent":null,"assignee":{"id":"usr-kim"},"creator":{"id":"usr-kim"},
        "project":null,
        "labels":{"nodes":[{"id":"lab-bug","name":"Bug","color":"#ef4444"}]}
      },
      {
        "id":"iss-143","identifier":"ENG-143","number":143,
        "title":"Rotate the session key","description":"",
        "priority":3,"estimate":null,"url":"https://linear.app/acme/issue/ENG-143",
        "createdAt":"2024-04-01T08:00:00.000Z","updatedAt":"2024-04-02T08:00:00.000Z",
        "startedAt":null,"completedAt":null,"dueDate":null,"archivedAt":null,
        "state":{"id":"st-hold"},"team":{"id":"team-eng"},"cycle":null,
        "parent":null,"assignee":null,"creator":{"id":"usr-dana"},
        "project":null,
        "labels":{"nodes":[]}
      }
    ]
  }
}`

const assistantImportFixtureComments = `{
  "comments": {
    "pageInfo": {"hasNextPage": false, "endCursor": ""},
    "nodes": [
      {"id":"cmt-1","body":"Reproduced on staging.","url":"https://linear.app/acme/issue/ENG-142#comment-cmt-1",
       "createdAt":"2024-03-08T10:00:00.000Z","updatedAt":"2024-03-08T10:00:00.000Z",
       "issue":{"id":"iss-142"},"parent":null,"user":{"id":"usr-dana"},"botActor":null}
    ]
  }
}`

const assistantImportFixtureAttachments = `{
  "attachments": {"pageInfo": {"hasNextPage": false, "endCursor": ""}, "nodes": []}
}`

const assistantImportFixtureRelations = `{
  "issueRelations": {"pageInfo": {"hasNextPage": false, "endCursor": ""}, "nodes": []}
}`

// assistantImportLinearHost is a stub Linear GraphQL endpoint. It exists so
// these tests exercise the REAL adapter, the real plan and the real applier
// against a fixed workspace, without a network and without Linear.
func assistantImportLinearHost(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(body, &req)

		// The credential arrives verbatim, with no Bearer prefix — Linear's
		// documented shape. A request without it is refused, so a test that
		// "passes" with an empty key fails loudly instead.
		if r.Header.Get("Authorization") != assistantImportTestKey {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"errors":[{"message":"Authentication required - not authenticated"}]}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":`+assistantImportFixtureFor(req.Query)+`}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func assistantImportFixtureFor(query string) string {
	switch {
	case strings.Contains(query, "query Viewer"):
		return assistantImportFixtureViewer
	case strings.Contains(query, "query Teams"):
		return assistantImportFixtureTeams
	case strings.Contains(query, "query States"):
		return assistantImportFixtureStates
	case strings.Contains(query, "query Labels"):
		return assistantImportFixtureLabels
	case strings.Contains(query, "query Users"):
		return assistantImportFixtureUsers
	case strings.Contains(query, "query Cycles"):
		return assistantImportFixtureCycles
	case strings.Contains(query, "query Issues"):
		return assistantImportFixtureIssues
	case strings.Contains(query, "query Comments"):
		return assistantImportFixtureComments
	case strings.Contains(query, "query Attachments"):
		return assistantImportFixtureAttachments
	case strings.Contains(query, "query Relations"):
		return assistantImportFixtureRelations
	}
	return `{}`
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// assistantImportSealKey installs a fresh seal key for the test process. The
// import credential is stored sealed or not at all (§3.8), so a test that
// forgot this one line would be testing the 503 path by accident.
func assistantImportSealKey(t *testing.T) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	t.Setenv(imports.SecretKeyEnv, base64.StdEncoding.EncodeToString(key))
}

// newAssistantTestImportConnection seals a token into a connection row for a
// workspace, pointed at `endpoint`. Exported to the rest of the handler tests
// because the destructive-tools meta-test needs one too.
func newAssistantTestImportConnection(t *testing.T, workspaceID, endpoint, token string) string {
	t.Helper()
	sealed, err := imports.SealSecret(token)
	if err != nil {
		t.Fatalf("seal import secret: %v", err)
	}
	var id string
	// The label is unique per row because (workspace_id, source, label) is:
	// a workspace may hold several connections, and a fixture that collides
	// with itself would fail for a reason that has nothing to do with imports.
	label := fmt.Sprintf("Acme Linear %d", time.Now().UnixNano())
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO import_connection (workspace_id, source, label, base_url, secret_encrypted, probe_status)
		VALUES ($1, 'linear', $2, $3, $4, 'ok')
		RETURNING id
	`, workspaceID, label, endpoint, sealed).Scan(&id); err != nil {
		t.Fatalf("insert import connection: %v", err)
	}
	return id
}

// newAssistantTestImportJob parks a job in `awaiting_confirm` with a plan on
// it, which is the only state confirm_import will bind to.
func newAssistantTestImportJob(t *testing.T, workspaceID, connectionID, plan string) string {
	t.Helper()
	var id string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO import_job (workspace_id, connection_id, source, status, scope, plan, mapping)
		VALUES ($1, $2, 'linear', 'awaiting_confirm', '{}'::jsonb, $3::jsonb, '{}'::jsonb)
		RETURNING id
	`, workspaceID, connectionID, plan).Scan(&id); err != nil {
		t.Fatalf("insert import job: %v", err)
	}
	return id
}

// assistantImportJobStatus reads the row back, because the receipt the model
// is shown must agree with the database rather than with itself.
func assistantImportJobStatus(t *testing.T, jobID string) string {
	t.Helper()
	var status string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM import_job WHERE id = $1`, jobID).Scan(&status); err != nil {
		t.Fatalf("read import job: %v", err)
	}
	return status
}

// assistantImportAwaitTerminal waits for a detached run to finish. The confirm
// path deliberately answers {job_id, status:"running"} and executes out of
// band (§4.2), so the test waits on the ROW rather than on the response.
func assistantImportAwaitTerminal(t *testing.T, jobID string) string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		status := assistantImportJobStatus(t, jobID)
		if imports.TerminalJobStatus(status) {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("import job %s never reached a terminal status (stuck at %s)", jobID, status)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// ---------------------------------------------------------------------------
// 1. Reads are workspace-gated
// ---------------------------------------------------------------------------

func TestAssistantImportReadsAreWorkspaceGated(t *testing.T) {
	assistantImportSealKey(t)
	owner := newAssistantTestUser(t, "assistant-import-owner@agora.dev")
	outsider := newAssistantTestUser(t, "assistant-import-outsider@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-import-gate-ws", "IMG")
	addAssistantTestMember(t, ws, owner, "owner")
	connectionID := newAssistantTestImportConnection(t, ws, "https://example.invalid", assistantImportTestKey)
	jobID := newAssistantTestImportJob(t, ws, connectionID, `{"source":{"kind":"linear"},"issues":{"total":2,"create":2,"update":0}}`)

	result, err := executeAssistantTool(t, owner, assistant.ToolListImportConnections, `{"workspace_id":"`+ws+`"}`)
	if err != nil {
		t.Fatalf("list_import_connections: %v", err)
	}
	rows, _ := result["connections"].([]any)
	if len(rows) != 1 {
		t.Fatalf("connections = %v, want the one row", result["connections"])
	}
	row := rows[0].(map[string]any)
	if row["id"] != connectionID || row["source"] != "linear" || row["probe_status"] != "ok" {
		t.Fatalf("connection row = %v", row)
	}
	// The listing query does not even select the sealed column, so the shape
	// cannot carry one — assert the absence anyway, on the row the model gets.
	for _, forbidden := range []string{"secret", "secret_encrypted", "token", "api_key"} {
		if _, leaked := row[forbidden]; leaked {
			t.Fatalf("connection row carries %q: %v", forbidden, row)
		}
	}
	if result["can_manage"] != true {
		t.Fatalf("an owner must be able to manage imports: %v", result["can_manage"])
	}

	status, err := executeAssistantTool(t, owner, assistant.ToolImportStatus,
		`{"workspace_id":"`+ws+`","job_id":"`+jobID+`"}`)
	if err != nil {
		t.Fatalf("import_status: %v", err)
	}
	if status["job_id"] != jobID || status["status"] != imports.JobAwaitingConfirm {
		t.Fatalf("import_status = %v", status)
	}
	// awaiting_confirm is NOT terminal: a job this build considers live must
	// never be narrated as finished.
	if status["terminal"] != false {
		t.Fatalf("awaiting_confirm reported as terminal: %v", status)
	}

	// And the whole surface refuses an outsider with the membership wording,
	// including the tools whose other arguments are missing entirely.
	for _, tc := range []struct{ tool, args string }{
		{assistant.ToolListImportConnections, `{"workspace_id":"` + ws + `"}`},
		{assistant.ToolImportStatus, `{"workspace_id":"` + ws + `","job_id":"` + jobID + `"}`},
		{assistant.ToolDryRunImport, `{"workspace_id":"` + ws + `","connection_id":"` + connectionID + `"}`},
		{assistant.ToolUpdateImportMapping, `{"workspace_id":"` + ws + `","status":{"Waiting on customer":"blocked"}}`},
		{assistant.ToolConfirmImport, `{"workspace_id":"` + ws + `","job_id":"` + jobID + `"}`},
	} {
		if _, err := executeAssistantTool(t, outsider, tc.tool, tc.args); err == nil {
			t.Fatalf("%s answered a non-member", tc.tool)
		} else if err.Error() != errAssistantNoAccess.Error() {
			t.Fatalf("%s refused a non-member with %q, want the membership wording", tc.tool, err.Error())
		}
	}
}

// An unknown job status renders generically rather than being reported as
// finished or as broken: enum drift downgrades, it never crashes.
func TestAssistantImportStatusDowngradesAnUnknownStatus(t *testing.T) {
	assistantImportSealKey(t)
	owner := newAssistantTestUser(t, "assistant-import-drift@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-import-drift-ws", "IMD")
	addAssistantTestMember(t, ws, owner, "owner")
	connectionID := newAssistantTestImportConnection(t, ws, "https://example.invalid", assistantImportTestKey)
	jobID := newAssistantTestImportJob(t, ws, connectionID, `{"source":{"kind":"linear"}}`)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE import_job SET status = 'reticulating_splines' WHERE id = $1`, jobID); err != nil {
		t.Fatalf("drift the status: %v", err)
	}

	result, err := executeAssistantTool(t, owner, assistant.ToolImportStatus,
		`{"workspace_id":"`+ws+`","job_id":"`+jobID+`"}`)
	if err != nil {
		t.Fatalf("import_status: %v", err)
	}
	if result["status"] != "reticulating_splines" {
		t.Fatalf("status = %v, want the value relayed verbatim", result["status"])
	}
	if result["terminal"] != false {
		t.Fatalf("an unknown status must not be reported as terminal: %v", result)
	}
	if note, _ := result["status_note"].(string); !strings.Contains(note, "does not know") {
		t.Fatalf("status_note does not tell the model what an unknown value means: %v", result["status_note"])
	}
}

// ---------------------------------------------------------------------------
// 2. The dry run surveys and writes nothing
// ---------------------------------------------------------------------------

func TestAssistantDryRunImportOpensAJobAndWritesNothing(t *testing.T) {
	assistantImportSealKey(t)
	host := assistantImportLinearHost(t)
	owner := newAssistantTestUser(t, "assistant-dryrun@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-dryrun-ws", "IDR")
	addAssistantTestMember(t, ws, owner, "owner")
	connectionID := newAssistantTestImportConnection(t, ws, host.URL, assistantImportTestKey)

	result, err := executeAssistantTool(t, owner, assistant.ToolDryRunImport,
		`{"workspace_id":"`+ws+`","connection_id":"`+connectionID+`"}`)
	if err != nil {
		t.Fatalf("dry_run_import: %v", err)
	}

	jobID, _ := result["job_id"].(string)
	if jobID == "" {
		t.Fatalf("dry_run_import returned no job id: %v", result)
	}
	if result["job_status"] != imports.JobAwaitingConfirm {
		t.Fatalf("job_status = %v, want the plan parked awaiting confirmation", result["job_status"])
	}
	if result["wrote_nothing"] != true {
		t.Fatalf("the survey must declare that it wrote nothing: %v", result)
	}

	issues, _ := result["issues"].(map[string]any)
	if issues["total"].(float64) != 2 || issues["create"].(float64) != 2 || issues["update"].(float64) != 0 {
		t.Fatalf("issue counts = %v, want 2 creates from the fixture", issues)
	}
	if comments, _ := result["comments"].(map[string]any); comments["total"].(float64) != 1 {
		t.Fatalf("comment counts = %v", result["comments"])
	}
	// Dana is not a member of this workspace, so the plan must SAY so — this
	// is the row the operator acts on and the one a comfortable report drops.
	if result["unmatched_users"].(float64) < 1 {
		t.Fatalf("unmatched_users = %v, want Dana reported", result["unmatched_users"])
	}
	people, _ := result["unmatched_people"].([]any)
	found := false
	for _, p := range people {
		if p.(map[string]any)["email"] == "dana@gone.example" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the unmatched author is not named: %v", people)
	}
	// "Customer reply" arrives in a workflow CATEGORY this build has never
	// heard of ("deferred"), so it falls through to the todo fallback and is
	// listed for the operator to assign. Enum drift downgrades: it must not
	// drop the issue, and it must not be silent about having downgraded.
	unmapped, _ := result["unmapped_statuses"].([]any)
	if len(unmapped) == 0 {
		t.Fatalf("the unmapped source state was not reported: %v", result)
	}

	// Nothing moved: no issues, no users, no members.
	var issueCount, userCount int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue WHERE workspace_id = $1`, ws).Scan(&issueCount); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	if issueCount != 0 {
		t.Fatalf("the dry run wrote %d issues", issueCount)
	}
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM "user" WHERE email IN ('dana@gone.example', 'linear-import@linear.local')`).Scan(&userCount); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if userCount != 0 {
		t.Fatalf("the dry run provisioned %d users", userCount)
	}

	// The plan is frozen on the row, which is what the confirmation binds to.
	var status string
	var plan []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT status, plan FROM import_job WHERE id = $1`, jobID).Scan(&status, &plan); err != nil {
		t.Fatalf("read job: %v", err)
	}
	if status != imports.JobAwaitingConfirm || len(plan) == 0 {
		t.Fatalf("job row = %s / %d bytes of plan", status, len(plan))
	}
}

// A second import is REFUSED WITH A POINTER at the running one, never by
// cancelling it — the property that replaced the Bitrix process-global.
func TestAssistantDryRunImportAnswersWithTheRunningJob(t *testing.T) {
	assistantImportSealKey(t)
	host := assistantImportLinearHost(t)
	owner := newAssistantTestUser(t, "assistant-import-busy@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-import-busy-ws", "IMB")
	addAssistantTestMember(t, ws, owner, "owner")
	connectionID := newAssistantTestImportConnection(t, ws, host.URL, assistantImportTestKey)
	running := newAssistantTestImportJob(t, ws, connectionID, `{"source":{"kind":"linear"}}`)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE import_job SET status = 'running' WHERE id = $1`, running); err != nil {
		t.Fatalf("mark running: %v", err)
	}

	result, err := executeAssistantTool(t, owner, assistant.ToolDryRunImport,
		`{"workspace_id":"`+ws+`","connection_id":"`+connectionID+`"}`)
	if err != nil {
		t.Fatalf("a busy workspace must be an ANSWER, not an error: %v", err)
	}
	if result["import_in_flight"] != true || result["job_id"] != running {
		t.Fatalf("result = %v, want a pointer at job %s", result, running)
	}
	if message, _ := result["message"].(string); !strings.Contains(message, "NOT cancelled") {
		t.Fatalf("the refusal never says the running job survived: %v", result["message"])
	}
	if assistantImportJobStatus(t, running) != "running" {
		t.Fatal("the running job was cancelled by a second request")
	}
	// And no second job row was opened.
	var jobs int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM import_job WHERE workspace_id = $1`, ws).Scan(&jobs); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if jobs != 1 {
		t.Fatalf("%d job rows, want the original one only", jobs)
	}
}

// ---------------------------------------------------------------------------
// 3. Confirm is the gesture that starts the job
// ---------------------------------------------------------------------------

func TestAssistantConfirmImportParksAndThenRuns(t *testing.T) {
	assistantImportSealKey(t)
	host := assistantImportLinearHost(t)
	owner := newAssistantTestUser(t, "assistant-import-confirm@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-import-confirm-ws", "IMC")
	addAssistantTestMember(t, ws, owner, "owner")
	session := newAssistantTestSession(t, owner)
	connectionID := newAssistantTestImportConnection(t, ws, host.URL, assistantImportTestKey)

	// Survey first — the plan is what the human authorizes.
	survey, err := executeAssistantSessionTool(t, owner, session, assistant.ToolDryRunImport,
		`{"workspace_id":"`+ws+`","connection_id":"`+connectionID+`"}`)
	if err != nil {
		t.Fatalf("dry_run_import: %v", err)
	}
	jobID, _ := survey["job_id"].(string)

	// Calling confirm_import does NOT import: it parks a card.
	asked := assistantAsk(t, owner, session, assistant.ToolConfirmImport,
		`{"workspace_id":"`+ws+`","job_id":"`+jobID+`"}`)
	op := assistantOperationOf(t, asked)
	summary, _ := op["summary"].(string)
	// The summary carries the PLAN'S OWN counts, and the bad news with them.
	for _, want := range []string{"2 issues", "Linear", "unmatched author"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q does not carry %q", summary, want)
		}
	}
	target, _ := op["target"].(map[string]any)
	if target["type"] != "import" || target["identifier"] != jobID {
		t.Fatalf("the confirmation is not bound to the job: %v", target)
	}
	if assistantImportJobStatus(t, jobID) != imports.JobAwaitingConfirm {
		t.Fatal("parking the card moved the job")
	}
	var beforeIssues int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue WHERE workspace_id = $1`, ws).Scan(&beforeIssues); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	if beforeIssues != 0 {
		t.Fatalf("%d issues were written before the click", beforeIssues)
	}

	// The click.
	receipt := assistantConfirm(t, owner, assistantOperationID(t, asked))
	if receipt["job_id"] != jobID || receipt["started"] != true {
		t.Fatalf("receipt = %v", receipt)
	}
	if next, _ := receipt["next_step"].(string); !strings.Contains(next, "do NOT poll") {
		t.Fatalf("the receipt never tells the model to stop talking: %v", receipt["next_step"])
	}

	if status := assistantImportAwaitTerminal(t, jobID); status != imports.JobDone {
		var failures []byte
		testPool.QueryRow(context.Background(), `SELECT failures FROM import_job WHERE id = $1`, jobID).Scan(&failures)
		t.Fatalf("import finished %s: %s", status, failures)
	}

	// The rows landed, and the attribution rule held: Dana was not a member,
	// so her issue belongs to the LINEAR IMPORT IDENTITY, never to the owner
	// who pressed Confirm.
	rows, err := testPool.Query(context.Background(),
		`SELECT title, creator_id::text FROM issue WHERE workspace_id = $1 ORDER BY title`, ws)
	if err != nil {
		t.Fatalf("read issues: %v", err)
	}
	defer rows.Close()
	creators := map[string]string{}
	for rows.Next() {
		var title, creator string
		if err := rows.Scan(&title, &creator); err != nil {
			t.Fatalf("scan: %v", err)
		}
		creators[title] = creator
	}
	if len(creators) != 2 {
		t.Fatalf("imported %d issues, want 2: %v", len(creators), creators)
	}
	var importIdentity string
	if err := testPool.QueryRow(context.Background(),
		`SELECT id::text FROM "user" WHERE email = $1`, imports.ImportIdentityEmail("linear")).Scan(&importIdentity); err != nil {
		t.Fatalf("the import identity was never created: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE email = $1`, imports.ImportIdentityEmail("linear"))
	})
	if creators["Rotate the session key"] != importIdentity {
		t.Fatalf("an unmatched author's issue was attributed to %q, want the import identity %q",
			creators["Rotate the session key"], importIdentity)
	}
	if creators["Rotate the session key"] == owner {
		t.Fatal("an unmatched author was written as the importing user — the one rule §3.3 makes structural")
	}

	// import_status reads the receipt back, failures included.
	status, err := executeAssistantTool(t, owner, assistant.ToolImportStatus,
		`{"workspace_id":"`+ws+`","job_id":"`+jobID+`"}`)
	if err != nil {
		t.Fatalf("import_status: %v", err)
	}
	if status["status"] != imports.JobDone || status["terminal"] != true {
		t.Fatalf("import_status = %v", status)
	}
	totals, _ := status["totals"].(map[string]any)
	issueTotals, _ := totals["issues"].(map[string]any)
	if issueTotals == nil || issueTotals["created"].(float64) != 2 {
		t.Fatalf("totals = %v, want 2 issues created", totals)
	}
	if _, present := status["failures"]; !present {
		t.Fatal("import_status hides the failure list")
	}
}

// Importing is owner/admin work, and the refusal is the product's own.
func TestAssistantImportWritesAreRoleGated(t *testing.T) {
	assistantImportSealKey(t)
	host := assistantImportLinearHost(t)
	member := newAssistantTestUser(t, "assistant-import-plain@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-import-role-ws", "IMR")
	addAssistantTestMember(t, ws, member, "member")
	session := newAssistantTestSession(t, member)
	connectionID := newAssistantTestImportConnection(t, ws, host.URL, assistantImportTestKey)
	jobID := newAssistantTestImportJob(t, ws, connectionID, `{"source":{"kind":"linear"},"issues":{"total":2}}`)

	for _, tc := range []struct{ tool, args string }{
		{assistant.ToolDryRunImport, `{"workspace_id":"` + ws + `","connection_id":"` + connectionID + `"}`},
		{assistant.ToolUpdateImportMapping, `{"workspace_id":"` + ws + `","status":{"Waiting on customer":"blocked"}}`},
		{assistant.ToolConfirmImport, `{"workspace_id":"` + ws + `","job_id":"` + jobID + `"}`},
	} {
		_, err := executeAssistantSessionTool(t, member, session, tc.tool, tc.args)
		if err == nil {
			t.Fatalf("%s ran for a plain member", tc.tool)
		}
		if !strings.Contains(err.Error(), "owner or an admin") {
			t.Fatalf("%s refused a member with %q, want the role wording", tc.tool, err.Error())
		}
	}
	// A refused confirm parks nothing: there is no card for a person who may
	// not perform the action.
	var pending int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM assistant_pending_operation WHERE session_id = $1`, session).Scan(&pending); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	if pending != 0 {
		t.Fatalf("%d confirmation cards were parked for a member who cannot import", pending)
	}
	if assistantImportJobStatus(t, jobID) != imports.JobAwaitingConfirm {
		t.Fatal("a role-refused confirm moved the job")
	}
}

// Confirming a job that has no plan is refused: the report is the thing the
// human authorizes, and a confirm without one is a card with no card.
func TestAssistantConfirmImportRefusesAJobWithNoPlan(t *testing.T) {
	assistantImportSealKey(t)
	owner := newAssistantTestUser(t, "assistant-import-noplan@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-import-noplan-ws", "IMN")
	addAssistantTestMember(t, ws, owner, "owner")
	session := newAssistantTestSession(t, owner)
	connectionID := newAssistantTestImportConnection(t, ws, "https://example.invalid", assistantImportTestKey)
	var jobID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO import_job (workspace_id, connection_id, source, status, scope)
		VALUES ($1, $2, 'linear', 'pending', '{}'::jsonb) RETURNING id
	`, ws, connectionID).Scan(&jobID); err != nil {
		t.Fatalf("insert job: %v", err)
	}

	_, err := executeAssistantSessionTool(t, owner, session, assistant.ToolConfirmImport,
		`{"workspace_id":"`+ws+`","job_id":"`+jobID+`"}`)
	if err == nil {
		t.Fatal("confirm_import accepted a job with no plan")
	}
	if !strings.Contains(err.Error(), "dry_run_import") {
		t.Fatalf("the refusal does not point at the survey: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 4. The mapping is argued with, not guessed at
// ---------------------------------------------------------------------------

func TestAssistantUpdateImportMappingMergesAndRejects(t *testing.T) {
	assistantImportSealKey(t)
	owner := newAssistantTestUser(t, "assistant-import-map@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-import-map-ws", "IMM")
	addAssistantTestMember(t, ws, owner, "owner")
	projectID := newAssistantTestProject(t, ws, "Engineering")

	result, err := executeAssistantTool(t, owner, assistant.ToolUpdateImportMapping,
		`{"workspace_id":"`+ws+`","status":{"Waiting on customer":"blocked","Made up":"nonsense"},`+
			`"priority":{"low":"low"},"containers":{"team-eng":"Engineering"},`+
			`"users":{"Dana@Gone.example":"dana@acme.io"}}`)
	if err != nil {
		t.Fatalf("update_import_mapping: %v", err)
	}

	mapping, _ := result["mapping"].(map[string]any)
	statuses, _ := mapping["status"].(map[string]any)
	if statuses["waiting on customer"] != "blocked" {
		t.Fatalf("the status override did not land: %v", mapping)
	}
	// A value that is not an Agora status is DROPPED AND NAMED, never stored:
	// it would become a CHECK violation mid-run, hours after the click.
	if _, stored := statuses["made up"]; stored {
		t.Fatalf("an invalid status was stored: %v", statuses)
	}
	rejected, _ := result["rejected"].([]any)
	if len(rejected) != 1 || !strings.Contains(rejected[0].(string), "Made up") {
		t.Fatalf("rejected = %v, want the bad status named", rejected)
	}
	containers, _ := mapping["containers"].(map[string]any)
	if containers["team-eng"] != projectID {
		t.Fatalf("the container was not grounded to a real project: %v", containers)
	}
	aliases, _ := result["identity_aliases"].(map[string]any)
	if aliases["dana@gone.example"] != "dana@acme.io" {
		t.Fatalf("identity alias = %v, want case-folded addresses", aliases)
	}

	// It MERGES rather than replacing: a second call naming one state leaves
	// the first one alone, and leaves sibling settings keys untouched.
	if _, err := executeAssistantTool(t, owner, assistant.ToolUpdateImportMapping,
		`{"workspace_id":"`+ws+`","status":{"Triage":"todo"}}`); err != nil {
		t.Fatalf("second update_import_mapping: %v", err)
	}
	var settings []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT settings FROM workspace WHERE id = $1`, ws).Scan(&settings); err != nil {
		t.Fatalf("read settings: %v", err)
	}
	overrides := imports.ParseOverrides(settings, "linear")
	if overrides.Status["waiting on customer"] != "blocked" || overrides.Status["triage"] != "todo" {
		t.Fatalf("settings = %s", settings)
	}
	if imports.ParseAliases(settings)["dana@gone.example"] != "dana@acme.io" {
		t.Fatalf("the second write clobbered the identity aliases: %s", settings)
	}

	// Nothing to change is a refusal, not an empty write.
	if _, err := executeAssistantTool(t, owner, assistant.ToolUpdateImportMapping,
		`{"workspace_id":"`+ws+`"}`); err == nil {
		t.Fatal("update_import_mapping accepted an empty change")
	}
}

// ---------------------------------------------------------------------------
// 5. The token never reaches the model
// ---------------------------------------------------------------------------

// The rule, asserted on the MARSHALED JSON of every import tool rather than on
// their structs: the bytes are what the model sees and what the transcript
// keeps forever. A field added three months from now travels whether or not
// anyone remembered assistant.ExcludedCapabilities.
func TestAssistantImportToolsNeverLeakTheToken(t *testing.T) {
	assistantImportSealKey(t)
	host := assistantImportLinearHost(t)
	owner := newAssistantTestUser(t, "assistant-import-leak@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-import-leak-ws", "IML")
	addAssistantTestMember(t, ws, owner, "owner")
	session := newAssistantTestSession(t, owner)
	connectionID := newAssistantTestImportConnection(t, ws, host.URL, assistantImportTestKey)

	answers := map[string]json.RawMessage{}
	record := func(tool, args string) {
		t.Helper()
		raw, err := testHandler.Execute(context.Background(), owner, session, tool, json.RawMessage(args))
		if err != nil {
			// A refusal is an answer too, and it must be clean as well.
			answers[tool+"/error"] = json.RawMessage(fmt.Sprintf("%q", err.Error()))
			return
		}
		answers[tool] = raw
	}

	record(assistant.ToolListImportConnections, `{"workspace_id":"`+ws+`"}`)
	record(assistant.ToolDryRunImport, `{"workspace_id":"`+ws+`","connection_id":"`+connectionID+`"}`)
	record(assistant.ToolUpdateImportMapping, `{"workspace_id":"`+ws+`","status":{"Waiting on customer":"blocked"}}`)

	var jobID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT id::text FROM import_job WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT 1`, ws).Scan(&jobID); err != nil {
		t.Fatalf("read the survey's job: %v", err)
	}
	record(assistant.ToolConfirmImport, `{"workspace_id":"`+ws+`","job_id":"`+jobID+`"}`)
	record(assistant.ToolImportStatus, `{"workspace_id":"`+ws+`","job_id":"`+jobID+`"}`)

	// A connection whose key cannot be decrypted at all: the error path is the
	// one most likely to quote the ciphertext or the key.
	brokenSeal := newAssistantTestImportConnection(t, ws, host.URL, "lin_api_ROTATEDAWAY")
	if _, err := testPool.Exec(context.Background(),
		`UPDATE import_connection SET secret_encrypted = $2 WHERE id = $1`, brokenSeal, []byte("not-a-sealed-box")); err != nil {
		t.Fatalf("corrupt the seal: %v", err)
	}
	record(assistant.ToolDryRunImport, `{"workspace_id":"`+ws+`","connection_id":"`+brokenSeal+`"}`)

	if len(answers) < 5 {
		t.Fatalf("only %d tool answers were recorded", len(answers))
	}
	for tool, blob := range answers {
		body := string(blob)
		for _, canary := range []string{assistantImportTestKey, "lin_api_", "not-a-sealed-box", "secret_encrypted"} {
			if strings.Contains(body, canary) {
				t.Fatalf("%s leaked %q into the transcript: %s", tool, canary, body)
			}
		}
	}
}

// The pending operation is persisted too, and its arguments are replayed at
// confirm time — so the row itself must be free of anything credential-shaped.
func TestAssistantImportPendingOperationCarriesNoSecret(t *testing.T) {
	assistantImportSealKey(t)
	host := assistantImportLinearHost(t)
	owner := newAssistantTestUser(t, "assistant-import-oprow@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-import-oprow-ws", "IMO")
	addAssistantTestMember(t, ws, owner, "owner")
	session := newAssistantTestSession(t, owner)
	connectionID := newAssistantTestImportConnection(t, ws, host.URL, assistantImportTestKey)
	jobID := newAssistantTestImportJob(t, ws, connectionID,
		`{"source":{"kind":"linear"},"issues":{"total":2,"create":2,"update":0},"exact":true}`)

	asked := assistantAsk(t, owner, session, assistant.ToolConfirmImport,
		`{"workspace_id":"`+ws+`","job_id":"`+jobID+`"}`)
	operationID := assistantOperationID(t, asked)

	var arguments, target []byte
	var summary string
	if err := testPool.QueryRow(context.Background(),
		`SELECT arguments, target, summary FROM assistant_pending_operation WHERE id = $1`,
		operationID).Scan(&arguments, &target, &summary); err != nil {
		t.Fatalf("read pending operation: %v", err)
	}
	for _, blob := range []string{string(arguments), string(target), summary} {
		if strings.Contains(blob, "lin_api_") {
			t.Fatalf("the pending operation carries a credential: %s", blob)
		}
	}
	if !strings.Contains(summary, "2 issues") {
		t.Fatalf("summary %q does not name the plan's counts", summary)
	}
}
