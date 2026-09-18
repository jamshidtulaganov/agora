package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// Pinned reports, end to end through the real endpoints.
//
// An assistant artifact is private by construction — it may hold numbers from
// every workspace its owner belongs to — so every test here is really one
// question: does the pin widen the audience by EXACTLY one project's workspace,
// and no further? That means proving both halves each time: who gains access,
// and who still does not.

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// pinReportRequest posts a pin as the given user and returns the recorder, so
// each test asserts its own status rather than a helper deciding what "worked"
// means.
func pinReportRequest(t *testing.T, userID, artifactID, projectID string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	testHandler.PinAssistantArtifact(w, withURLParam(
		newAssistantRequest(http.MethodPost, "/api/assistant/artifacts/"+artifactID+"/pins", userID,
			`{"project_id":"`+projectID+`"}`), "id", artifactID))
	return w
}

// pinTestReport pins and fails the test if the publish did not take, returning
// the new pin id.
func pinTestReport(t *testing.T, userID, artifactID, projectID string) string {
	t.Helper()
	w := pinReportRequest(t, userID, artifactID, projectID)
	if w.Code != http.StatusCreated {
		t.Fatalf("pin: status %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode pin response: %v (%s)", err, w.Body.String())
	}
	pinID, _ := body["pin_id"].(string)
	if pinID == "" {
		t.Fatalf("pin response carries no pin_id: %v", body)
	}
	return pinID
}

func getReportRequest(t *testing.T, userID, pinID string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	testHandler.GetReport(w, withURLParam(
		newAssistantRequest(http.MethodGet, "/api/reports/"+pinID, userID, ""), "pinId", pinID))
	return w
}

func listProjectReportsRequest(t *testing.T, userID, workspaceID, projectID string) *httptest.ResponseRecorder {
	t.Helper()
	req := newAssistantRequest(http.MethodGet, "/api/projects/"+projectID+"/reports", userID, "")
	req.Header.Set("X-Workspace-ID", workspaceID)
	w := httptest.NewRecorder()
	testHandler.ListProjectReports(w, withURLParam(req, "id", projectID))
	return w
}

func unpinReportRequest(t *testing.T, userID, artifactID, pinID string) *httptest.ResponseRecorder {
	t.Helper()
	req := newAssistantRequest(http.MethodDelete,
		"/api/assistant/artifacts/"+artifactID+"/pins/"+pinID, userID, "")
	w := httptest.NewRecorder()
	testHandler.UnpinAssistantArtifact(w, withURLParams(req, "id", artifactID, "pinId", pinID))
	return w
}

func countReportPins(t *testing.T, artifactID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM assistant_artifact_pin WHERE artifact_id = $1`, artifactID).Scan(&n); err != nil {
		t.Fatalf("count pins: %v", err)
	}
	return n
}

// reportFixture is the standing setup every test here needs: an owner with a
// markdown report, a workspace they belong to, and a project in it.
type reportFixture struct {
	owner      string
	session    string
	artifactID string
	workspace  string
	project    string
}

func newReportFixture(t *testing.T, key, slug, prefix string) reportFixture {
	t.Helper()
	// "owner-" namespaces the fixture email away from the secondary users each
	// test adds by hand — a shared address is a UNIQUE violation, not a failure
	// of the thing under test.
	owner := newAssistantTestUser(t, "assistant-pin-owner-"+key+"@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-pin-"+slug, prefix)
	addAssistantTestMember(t, ws, owner, "owner")
	session := newAssistantTestSession(t, owner)
	artifactID := createTestArtifact(t, owner, session, "Sprint report",
		assistant.ArtifactKindMarkdown, "# Sprint report\n\nAll green.")
	return reportFixture{
		owner:      owner,
		session:    session,
		artifactID: artifactID,
		workspace:  ws,
		project:    newAssistantTestProject(t, ws, "Pinned Reports"),
	}
}

// ---------------------------------------------------------------------------
// Pinning
// ---------------------------------------------------------------------------

func TestPinAssistantArtifactPublishesToTheProject(t *testing.T) {
	f := newReportFixture(t, "create", "create", "PC1")

	w := pinReportRequest(t, f.owner, f.artifactID, f.project)
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}

	// The wire contract the frontend's zod schema pins — decoded generically so
	// a renamed JSON tag fails here rather than in the app.
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v (%s)", err, w.Body.String())
	}
	for _, key := range []string{"pin_id", "artifact_id", "title", "kind", "version", "updated_at", "created_at", "pinned_by", "owner"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("response is missing %q: %v", key, body)
		}
	}
	if body["artifact_id"] != f.artifactID || body["title"] != "Sprint report" || body["kind"] != "markdown" {
		t.Fatalf("response = %v", body)
	}
	// A pin never carries the body — the list it feeds draws cards.
	if _, leaked := body["content"]; leaked {
		t.Fatalf("pin response carries content: %v", body)
	}
	// ...and never the conversation that produced the report.
	if _, leaked := body["session_id"]; leaked {
		t.Fatalf("pin response carries session_id: %v", body)
	}
	pinnedBy, _ := body["pinned_by"].(map[string]any)
	ownerRef, _ := body["owner"].(map[string]any)
	if pinnedBy["id"] != f.owner || ownerRef["id"] != f.owner {
		t.Fatalf("attribution = %v / %v, want the owner %s", pinnedBy, ownerRef, f.owner)
	}
	if name, _ := ownerRef["name"].(string); name == "" {
		t.Fatalf("owner has no display name: %v", ownerRef)
	}

	// The stored row is what authorizes every later read, so the derived
	// workspace must be the project's — not anything the request named.
	var wsID, projectID, pinnedByID string
	if err := testPool.QueryRow(context.Background(),
		`SELECT workspace_id, project_id, pinned_by FROM assistant_artifact_pin WHERE artifact_id = $1`,
		f.artifactID).Scan(&wsID, &projectID, &pinnedByID); err != nil {
		t.Fatalf("read back pin: %v", err)
	}
	if wsID != f.workspace || projectID != f.project || pinnedByID != f.owner {
		t.Fatalf("pin row = ws %s / project %s / by %s", wsID, projectID, pinnedByID)
	}
}

// Only the owner can publish: a report may aggregate workspaces the pinner
// cannot see, so "I am a member of the target project" is not enough.
func TestPinAssistantArtifactRefusesNonOwner(t *testing.T) {
	f := newReportFixture(t, "nonowner", "nonowner", "PC2")
	// Bob is in the same workspace — so the refusal is about the ARTIFACT, not
	// about the project he is pinning into.
	bob := newAssistantTestUser(t, "assistant-pin-bob@agora.dev")
	addAssistantTestMember(t, f.workspace, bob, "member")

	w := pinReportRequest(t, bob, f.artifactID, f.project)
	if w.Code != http.StatusNotFound {
		t.Fatalf("a non-owner pinned someone else's artifact: %d %s", w.Code, w.Body.String())
	}
	if n := countReportPins(t, f.artifactID); n != 0 {
		t.Fatalf("a refused pin wrote %d rows", n)
	}
}

// The owner must also be a member of the target project's workspace — pinning
// is not a way to push a report into a workspace you are not in.
func TestPinAssistantArtifactRefusesForeignProject(t *testing.T) {
	f := newReportFixture(t, "foreign", "foreign", "PC3")
	stranger := newAssistantTestUser(t, "assistant-pin-stranger@agora.dev")
	otherWS := newAssistantTestWorkspace(t, "assistant-pin-otherws", "PC4")
	addAssistantTestMember(t, otherWS, stranger, "owner")
	foreignProject := newAssistantTestProject(t, otherWS, "Not Yours")

	w := pinReportRequest(t, f.owner, f.artifactID, foreignProject)
	if w.Code != http.StatusNotFound {
		t.Fatalf("pinned into a workspace the owner is not in: %d %s", w.Code, w.Body.String())
	}
	if n := countReportPins(t, f.artifactID); n != 0 {
		t.Fatalf("a refused pin wrote %d rows", n)
	}
}

func TestPinAssistantArtifactRejectsBadProjectID(t *testing.T) {
	f := newReportFixture(t, "badproject", "badproject", "PC5")

	for _, tc := range []struct {
		name, projectID string
		want            int
	}{
		{"not a uuid", "not-a-uuid", http.StatusBadRequest},
		{"unknown project", "11111111-1111-1111-1111-111111111111", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := pinReportRequest(t, f.owner, f.artifactID, tc.projectID)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
	if n := countReportPins(t, f.artifactID); n != 0 {
		t.Fatalf("a rejected pin wrote %d rows", n)
	}
}

// UNIQUE (artifact_id, project_id) is the idempotency backstop: a double-click
// answers with the pin that already exists, not a 500 and not a duplicate.
func TestPinAssistantArtifactIsIdempotent(t *testing.T) {
	f := newReportFixture(t, "dup", "dup", "PC6")
	first := pinTestReport(t, f.owner, f.artifactID, f.project)

	w := pinReportRequest(t, f.owner, f.artifactID, f.project)
	if w.Code != http.StatusOK {
		t.Fatalf("second pin: status %d, want 200: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["pin_id"] != first {
		t.Fatalf("second pin returned %v, want the existing pin %s", body["pin_id"], first)
	}
	if n := countReportPins(t, f.artifactID); n != 1 {
		t.Fatalf("%d pins after pinning twice, want 1", n)
	}
}

// ---------------------------------------------------------------------------
// Reading
// ---------------------------------------------------------------------------

// The point of the whole feature: a teammate who could not see the artifact can
// read the report, body included.
func TestGetReportGivesMembersTheContent(t *testing.T) {
	f := newReportFixture(t, "read", "read", "PR1")
	teammate := newAssistantTestUser(t, "assistant-pin-teammate@agora.dev")
	addAssistantTestMember(t, f.workspace, teammate, "member")
	pinID := pinTestReport(t, f.owner, f.artifactID, f.project)

	// Before the read: the teammate genuinely cannot reach the artifact itself.
	w := httptest.NewRecorder()
	testHandler.GetAssistantArtifact(w, withURLParam(
		newAssistantRequest(http.MethodGet, "/api/assistant/artifacts/"+f.artifactID, teammate, ""),
		"id", f.artifactID))
	if w.Code != http.StatusNotFound {
		t.Fatalf("a teammate read the raw artifact: %d %s", w.Code, w.Body.String())
	}

	w = getReportRequest(t, teammate, pinID)
	if w.Code != http.StatusOK {
		t.Fatalf("a member could not read the pinned report: %d %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v (%s)", err, w.Body.String())
	}
	for _, key := range []string{"pin_id", "artifact_id", "title", "kind", "version", "updated_at", "created_at", "pinned_by", "owner", "content"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("response is missing %q: %v", key, body)
		}
	}
	if body["content"] != "# Sprint report\n\nAll green." {
		t.Fatalf("content = %v", body["content"])
	}
	// The grant is the report, never the conversation behind it.
	if _, leaked := body["session_id"]; leaked {
		t.Fatalf("report read leaks session_id: %v", body)
	}
}

func TestGetReportRefusesNonMembers(t *testing.T) {
	f := newReportFixture(t, "outsider", "outsider", "PR2")
	outsider := newAssistantTestUser(t, "assistant-pin-read-outsider@agora.dev")
	pinID := pinTestReport(t, f.owner, f.artifactID, f.project)

	w := getReportRequest(t, outsider, pinID)
	if w.Code != http.StatusNotFound {
		t.Fatalf("a non-member read a pinned report: %d %s", w.Code, w.Body.String())
	}
	// A report id that does not exist answers the same way, so this endpoint is
	// not an existence oracle for other workspaces' reports.
	w = getReportRequest(t, outsider, "11111111-1111-1111-1111-111111111111")
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown pin returned %d, want 404", w.Code)
	}
	w = getReportRequest(t, outsider, "not-a-uuid")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("malformed pin id returned %d, want 400", w.Code)
	}
}

// The report always renders the CURRENT body, which is what makes re-running a
// recipe the refresh — the pin never has to be touched again.
func TestGetReportFollowsTheArtifactsCurrentVersion(t *testing.T) {
	f := newReportFixture(t, "refresh", "refresh", "PR3")
	teammate := newAssistantTestUser(t, "assistant-pin-refresh-mate@agora.dev")
	addAssistantTestMember(t, f.workspace, teammate, "member")
	pinID := pinTestReport(t, f.owner, f.artifactID, f.project)

	if _, err := executeAssistantSessionTool(t, f.owner, f.session, assistant.ToolUpdateArtifact,
		`{"artifact_id":"`+f.artifactID+`","content":"# Sprint report\n\nTwo blockers."}`); err != nil {
		t.Fatalf("update_artifact: %v", err)
	}

	w := getReportRequest(t, teammate, pinID)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["content"] != "# Sprint report\n\nTwo blockers." {
		t.Fatalf("content = %v, want the refreshed body", body["content"])
	}
	if version, _ := body["version"].(float64); version != 2 {
		t.Fatalf("version = %v, want 2 after one update", body["version"])
	}
}

func TestListProjectReportsIsScopedAndOmitsContent(t *testing.T) {
	f := newReportFixture(t, "list", "list", "PL1")
	teammate := newAssistantTestUser(t, "assistant-pin-list-mate@agora.dev")
	addAssistantTestMember(t, f.workspace, teammate, "member")
	pinID := pinTestReport(t, f.owner, f.artifactID, f.project)

	// A second project in the same workspace with no reports — the list must be
	// keyed on the project, not merely on the workspace.
	empty := newAssistantTestProject(t, f.workspace, "No Reports")

	w := listProjectReportsRequest(t, teammate, f.workspace, f.project)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Reports []map[string]any `json:"reports"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v (%s)", err, w.Body.String())
	}
	if len(body.Reports) != 1 {
		t.Fatalf("%d reports, want 1: %s", len(body.Reports), w.Body.String())
	}
	row := body.Reports[0]
	if row["pin_id"] != pinID || row["artifact_id"] != f.artifactID {
		t.Fatalf("row = %v", row)
	}
	if _, leaked := row["content"]; leaked {
		t.Fatalf("the list carries bodies: %v", row)
	}
	if _, leaked := row["session_id"]; leaked {
		t.Fatalf("the list leaks session_id: %v", row)
	}

	w = listProjectReportsRequest(t, teammate, f.workspace, empty)
	if w.Code != http.StatusOK {
		t.Fatalf("empty project: status %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Reports) != 0 {
		t.Fatalf("a project with no pins listed %d reports", len(body.Reports))
	}
}

// The project read is workspace-scoped like every other project read: another
// workspace's id in the header answers 404 rather than the reports.
func TestListProjectReportsRefusesForeignWorkspace(t *testing.T) {
	f := newReportFixture(t, "listscope", "listscope", "PL2")
	pinTestReport(t, f.owner, f.artifactID, f.project)
	otherWS := newAssistantTestWorkspace(t, "assistant-pin-listscope-other", "PL3")
	addAssistantTestMember(t, otherWS, f.owner, "owner")

	w := listProjectReportsRequest(t, f.owner, otherWS, f.project)
	if w.Code != http.StatusNotFound {
		t.Fatalf("a project was listed under the wrong workspace: %d %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Unpinning
// ---------------------------------------------------------------------------

func TestUnpinAssistantArtifactByOwner(t *testing.T) {
	f := newReportFixture(t, "unpin", "unpin", "PU1")
	teammate := newAssistantTestUser(t, "assistant-pin-unpin-mate@agora.dev")
	addAssistantTestMember(t, f.workspace, teammate, "member")
	pinID := pinTestReport(t, f.owner, f.artifactID, f.project)

	w := unpinReportRequest(t, f.owner, f.artifactID, pinID)
	if w.Code != http.StatusNoContent {
		t.Fatalf("owner could not unpin: %d %s", w.Code, w.Body.String())
	}
	if n := countReportPins(t, f.artifactID); n != 0 {
		t.Fatalf("%d pins survive the unpin", n)
	}
	// The grant is gone with the row...
	if w := getReportRequest(t, teammate, pinID); w.Code != http.StatusNotFound {
		t.Fatalf("an unpinned report is still readable: %d", w.Code)
	}
	// ...and the artifact is untouched: unpinning is not a delete.
	w = httptest.NewRecorder()
	testHandler.GetAssistantArtifact(w, withURLParam(
		newAssistantRequest(http.MethodGet, "/api/assistant/artifacts/"+f.artifactID, f.owner, ""),
		"id", f.artifactID))
	if w.Code != http.StatusOK {
		t.Fatalf("unpinning removed the owner's artifact: %d %s", w.Code, w.Body.String())
	}
}

// The workspace can always withdraw something from its own project page, even
// though the artifact stays the owner's.
func TestUnpinAssistantArtifactByWorkspaceAdmin(t *testing.T) {
	f := newReportFixture(t, "admin", "admin", "PU2")
	admin := newAssistantTestUser(t, "assistant-pin-admin@agora.dev")
	addAssistantTestMember(t, f.workspace, admin, "admin")
	pinID := pinTestReport(t, f.owner, f.artifactID, f.project)

	w := unpinReportRequest(t, admin, f.artifactID, pinID)
	if w.Code != http.StatusNoContent {
		t.Fatalf("a workspace admin could not unpin: %d %s", w.Code, w.Body.String())
	}
	if n := countReportPins(t, f.artifactID); n != 0 {
		t.Fatalf("%d pins survive the admin unpin", n)
	}
}

// A plain member sees the report but does not get to un-publish it. They are
// already inside, so 403 rather than the 404 an outsider gets.
func TestUnpinAssistantArtifactRefusesPlainMemberAndOutsider(t *testing.T) {
	f := newReportFixture(t, "unpinrefuse", "unpinrefuse", "PU3")
	member := newAssistantTestUser(t, "assistant-pin-plain@agora.dev")
	addAssistantTestMember(t, f.workspace, member, "member")
	outsider := newAssistantTestUser(t, "assistant-pin-unpin-outsider@agora.dev")
	pinID := pinTestReport(t, f.owner, f.artifactID, f.project)

	if w := unpinReportRequest(t, member, f.artifactID, pinID); w.Code != http.StatusForbidden {
		t.Fatalf("plain member unpin returned %d, want 403: %s", w.Code, w.Body.String())
	}
	if w := unpinReportRequest(t, outsider, f.artifactID, pinID); w.Code != http.StatusNotFound {
		t.Fatalf("outsider unpin returned %d, want 404: %s", w.Code, w.Body.String())
	}
	if n := countReportPins(t, f.artifactID); n != 1 {
		t.Fatalf("%d pins after two refused unpins, want 1", n)
	}
}

// The artifact id in the path is not decoration: a pin can only be deleted
// through the artifact it actually belongs to.
func TestUnpinAssistantArtifactRefusesMismatchedArtifact(t *testing.T) {
	f := newReportFixture(t, "mismatch", "mismatch", "PU4")
	pinID := pinTestReport(t, f.owner, f.artifactID, f.project)
	other := createTestArtifact(t, f.owner, f.session, "Another", assistant.ArtifactKindMarkdown, "# other")

	if w := unpinReportRequest(t, f.owner, other, pinID); w.Code != http.StatusNotFound {
		t.Fatalf("a pin was deleted through the wrong artifact: %d %s", w.Code, w.Body.String())
	}
	if w := unpinReportRequest(t, f.owner, "not-a-uuid", pinID); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed artifact id returned %d, want 400", w.Code)
	}
	if w := unpinReportRequest(t, f.owner, f.artifactID, "not-a-uuid"); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed pin id returned %d, want 400", w.Code)
	}
	if n := countReportPins(t, f.artifactID); n != 1 {
		t.Fatalf("%d pins after refused unpins, want 1", n)
	}
}

// ON DELETE CASCADE is the grant's lifetime guarantee: an artifact that no
// longer exists must not leave a readable report behind it.
func TestDeletingAnArtifactCascadesItsPins(t *testing.T) {
	f := newReportFixture(t, "cascade", "cascade", "PU5")
	pinID := pinTestReport(t, f.owner, f.artifactID, f.project)

	if _, err := testPool.Exec(context.Background(),
		`DELETE FROM assistant_artifact WHERE id = $1`, f.artifactID); err != nil {
		t.Fatalf("delete artifact: %v", err)
	}
	if n := countReportPins(t, f.artifactID); n != 0 {
		t.Fatalf("%d pins survive the artifact", n)
	}
	if w := getReportRequest(t, f.owner, pinID); w.Code != http.StatusNotFound {
		t.Fatalf("a report survived its artifact: %d %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// WS events
// ---------------------------------------------------------------------------

// A project page cannot poll for reports it does not know changed, so the three
// moments that move it are the three events. All are workspace-scoped and carry
// ids only: a payload that carried a body would hand the report to every
// listener in the room, including ones that must refetch through the gate.
func TestReportEventsAreWorkspaceScopedAndCarryOnlyIDs(t *testing.T) {
	f := newReportFixture(t, "events", "events", "PE1")
	pinned := recordBusEvents(t, protocol.EventReportPinned)
	updated := recordBusEvents(t, protocol.EventReportUpdated)
	unpinned := recordBusEvents(t, protocol.EventReportUnpinned)

	pinID := pinTestReport(t, f.owner, f.artifactID, f.project)

	assertReportEvent := func(name string, seen []events.Event) {
		t.Helper()
		if len(seen) != 1 {
			t.Fatalf("%s fired %d times, want 1", name, len(seen))
		}
		e := seen[0]
		if e.WorkspaceID != f.workspace {
			t.Fatalf("%s went to workspace %s, want %s", name, e.WorkspaceID, f.workspace)
		}
		payload, ok := e.Payload.(map[string]any)
		if !ok {
			t.Fatalf("%s payload is not an object: %v", name, e.Payload)
		}
		if payload["pin_id"] != pinID || payload["project_id"] != f.project || payload["artifact_id"] != f.artifactID {
			t.Fatalf("%s payload = %v", name, payload)
		}
		for _, leaked := range []string{"content", "title", "session_id"} {
			if _, bad := payload[leaked]; bad {
				t.Fatalf("%s payload carries %q: %v", name, leaked, payload)
			}
		}
	}
	assertReportEvent("report:pinned", pinned())

	// Re-running the recipe is the refresh, so the update path — not the pin —
	// is what tells the project page to refetch.
	if _, err := executeAssistantSessionTool(t, f.owner, f.session, assistant.ToolUpdateArtifact,
		`{"artifact_id":"`+f.artifactID+`","content":"# Sprint report\n\nRefreshed."}`); err != nil {
		t.Fatalf("update_artifact: %v", err)
	}
	assertReportEvent("report:updated", updated())

	if w := unpinReportRequest(t, f.owner, f.artifactID, pinID); w.Code != http.StatusNoContent {
		t.Fatalf("unpin: %d %s", w.Code, w.Body.String())
	}
	assertReportEvent("report:unpinned", unpinned())

	// An artifact nobody published fans out to nobody — the update path must not
	// start emitting into a workspace just because a run touched an artifact.
	if _, err := executeAssistantSessionTool(t, f.owner, f.session, assistant.ToolUpdateArtifact,
		`{"artifact_id":"`+f.artifactID+`","content":"# Sprint report\n\nPrivate again."}`); err != nil {
		t.Fatalf("update_artifact after unpin: %v", err)
	}
	if got := updated(); len(got) != 1 {
		t.Fatalf("report:updated fired %d times, want 1 — an unpinned artifact still fans out", len(got))
	}
}
