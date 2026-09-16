package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
)

// Assistant artifacts, end to end through the real executor and the real
// endpoints. Three things are being defended here:
//
//  1. Nothing invalid is ever persisted. A malformed spec must cost the model
//     one correctable tool error and leave the session unchanged — a half-
//     written row is what makes the viewer pane crash on someone else's
//     machine a week later.
//  2. Ownership is session ownership. Another person's artifact is NOT FOUND,
//     not forbidden, on every surface that can reach it.
//  3. Iteration bumps a version in place rather than accumulating artifacts.

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const testChartSpec = `{"type":"bar","x":"day","series":[{"key":"runs","label":"Runs"}],"rows":[{"day":"Mon","runs":12}]}`

// assistantArtifactRows counts what a session actually holds — the assertion
// that a rejected tool call wrote nothing.
func assistantArtifactRows(t *testing.T, sessionID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM assistant_artifact WHERE session_id = $1`, sessionID).Scan(&n); err != nil {
		t.Fatalf("count artifacts: %v", err)
	}
	return n
}

// createTestArtifact runs the real create tool and returns the new id.
func createTestArtifact(t *testing.T, userID, sessionID, title, kind, content string) string {
	t.Helper()
	args, err := json.Marshal(map[string]string{"title": title, "kind": kind, "content": content})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	result, err := executeAssistantSessionTool(t, userID, sessionID, assistant.ToolCreateArtifact, string(args))
	if err != nil {
		t.Fatalf("create_artifact: %v", err)
	}
	id, _ := result["artifact_id"].(string)
	if id == "" {
		t.Fatalf("create_artifact returned no artifact_id: %v", result)
	}
	return id
}

// ---------------------------------------------------------------------------
// create_artifact
// ---------------------------------------------------------------------------

func TestAssistantCreateArtifactPersistsAndReturnsTheCard(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-artifact-create@agora.dev")
	session := newAssistantTestSession(t, user)

	result, err := executeAssistantSessionTool(t, user, session, assistant.ToolCreateArtifact,
		`{"title":"Runs by day","kind":"chart","content":`+strconv.Quote(testChartSpec)+`}`)
	if err != nil {
		t.Fatalf("create_artifact: %v", err)
	}

	// The tool_result is what the transcript renders an ArtifactCard from,
	// with no second fetch — so all four fields must be there and correct.
	artifactID, _ := result["artifact_id"].(string)
	if artifactID == "" {
		t.Fatalf("result carries no artifact_id: %v", result)
	}
	if result["title"] != "Runs by day" || result["kind"] != "chart" {
		t.Fatalf("result = %v", result)
	}
	if version, _ := result["version"].(float64); version != 1 {
		t.Fatalf("version = %v, want 1", result["version"])
	}

	var sessionID, ownerID, title, kind, content string
	var version int
	if err := testPool.QueryRow(context.Background(), `
		SELECT session_id, user_id, title, kind, content, version FROM assistant_artifact WHERE id = $1
	`, artifactID).Scan(&sessionID, &ownerID, &title, &kind, &content, &version); err != nil {
		t.Fatalf("read back artifact: %v", err)
	}
	if sessionID != session || ownerID != user {
		t.Fatalf("artifact belongs to session %s / user %s, want %s / %s", sessionID, ownerID, session, user)
	}
	if title != "Runs by day" || kind != "chart" || content != testChartSpec || version != 1 {
		t.Fatalf("row = %q %q v%d, content %q", title, kind, version, content)
	}
}

func TestAssistantCreateArtifactAcceptsEveryKind(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-artifact-kinds@agora.dev")
	for _, tc := range []struct{ kind, content string }{
		{assistant.ArtifactKindChart, testChartSpec},
		{assistant.ArtifactKindTable, `{"columns":["Agent","Runs"],"rows":[["claude",3]]}`},
		{assistant.ArtifactKindMarkdown, "# Sprint report\n\nAll green."},
		{assistant.ArtifactKindHTML, "<!doctype html><html><body><p>hi</p></body></html>"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			// A fresh session per kind keeps the per-session cap out of it.
			session := newAssistantTestSession(t, user)
			id := createTestArtifact(t, user, session, "A "+tc.kind, tc.kind, tc.content)
			if id == "" {
				t.Fatalf("%s artifact was not created", tc.kind)
			}
		})
	}
}

func TestAssistantCreateArtifactRejectsUnknownKind(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-artifact-kind@agora.dev")
	session := newAssistantTestSession(t, user)

	_, err := executeAssistantSessionTool(t, user, session, assistant.ToolCreateArtifact,
		`{"title":"Thing","kind":"svg","content":"<svg/>"}`)
	if err == nil {
		t.Fatal("an unknown kind was accepted")
	}
	// The refusal must list the kinds that DO exist, or the model guesses.
	for _, want := range assistant.ArtifactKinds {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not name %q", err.Error(), want)
		}
	}
	if n := assistantArtifactRows(t, session); n != 0 {
		t.Fatalf("a rejected kind persisted %d rows", n)
	}
}

func TestAssistantCreateArtifactRejectsOversizedContent(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-artifact-size@agora.dev")
	session := newAssistantTestSession(t, user)

	oversized, err := json.Marshal(map[string]string{
		"title":   "Huge",
		"kind":    assistant.ArtifactKindMarkdown,
		"content": strings.Repeat("x", assistant.MaxArtifactContentBytes+1),
	})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	if _, err := executeAssistantSessionTool(t, user, session, assistant.ToolCreateArtifact, string(oversized)); err == nil {
		t.Fatal("content over the cap was accepted")
	} else if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error = %q, want it to say the content is too large", err.Error())
	}
	if n := assistantArtifactRows(t, session); n != 0 {
		t.Fatalf("oversized content persisted %d rows", n)
	}
}

// The whole point of validating server-side: a spec the renderer cannot draw
// bounces back as a correction naming the field, and NOTHING is written.
func TestAssistantCreateArtifactRejectsMalformedChartSpecWithoutPersisting(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-artifact-spec@agora.dev")
	session := newAssistantTestSession(t, user)

	cases := []struct{ name, content, wants string }{
		{"unknown type", `{\"type\":\"donut\",\"x\":\"day\",\"series\":[{\"key\":\"runs\"}],\"rows\":[]}`, `"type"`},
		{"missing x", `{\"type\":\"bar\",\"series\":[{\"key\":\"runs\"}],\"rows\":[]}`, `"x"`},
		{"series without key", `{\"type\":\"bar\",\"x\":\"day\",\"series\":[{\"label\":\"Runs\"}],\"rows\":[]}`, "series[0].key"},
		{"rows not objects", `{\"type\":\"bar\",\"x\":\"day\",\"series\":[{\"key\":\"runs\"}],\"rows\":[[1,2]]}`, "rows[0]"},
		{"not json", `not a spec at all`, "valid JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := executeAssistantSessionTool(t, user, session, assistant.ToolCreateArtifact,
				`{"title":"Broken","kind":"chart","content":"`+tc.content+`"}`)
			if err == nil {
				t.Fatalf("accepted a malformed chart spec: %s", tc.content)
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("error = %q, want it to name %s", err.Error(), tc.wants)
			}
		})
	}
	if n := assistantArtifactRows(t, session); n != 0 {
		t.Fatalf("malformed specs persisted %d rows", n)
	}
}

func TestAssistantCreateArtifactRejectsMalformedTableSpec(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-artifact-table@agora.dev")
	session := newAssistantTestSession(t, user)

	_, err := executeAssistantSessionTool(t, user, session, assistant.ToolCreateArtifact,
		`{"title":"Broken","kind":"table","content":"{\"columns\":[\"A\"],\"rows\":[{\"A\":1}]}"}`)
	if err == nil {
		t.Fatal("a table whose rows are objects was accepted")
	}
	if !strings.Contains(err.Error(), "rows[0]") {
		t.Fatalf("error = %q, want it to name rows[0]", err.Error())
	}
	if n := assistantArtifactRows(t, session); n != 0 {
		t.Fatalf("a malformed table persisted %d rows", n)
	}
}

// The cap exists to catch a model that answers every follow-up with a NEW
// artifact instead of updating the one already on screen — so the refusal
// points at update_artifact.
func TestAssistantCreateArtifactEnforcesPerSessionCap(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-artifact-cap@agora.dev")
	session := newAssistantTestSession(t, user)

	for i := 0; i < assistant.MaxArtifactsPerSession; i++ {
		createTestArtifact(t, user, session, "Report", assistant.ArtifactKindMarkdown, "# body")
	}
	_, err := executeAssistantSessionTool(t, user, session, assistant.ToolCreateArtifact,
		`{"title":"One too many","kind":"markdown","content":"# body"}`)
	if err == nil {
		t.Fatalf("a %dst artifact was accepted", assistant.MaxArtifactsPerSession+1)
	}
	if !strings.Contains(err.Error(), assistant.ToolUpdateArtifact) {
		t.Fatalf("cap error = %q, want it to point at update_artifact", err.Error())
	}
	if n := assistantArtifactRows(t, session); n != assistant.MaxArtifactsPerSession {
		t.Fatalf("session holds %d artifacts, want the cap of %d", n, assistant.MaxArtifactsPerSession)
	}
}

// Outside a run there is no conversation to attach to. This cannot happen from
// the run loop; it must be a readable refusal rather than a panic.
func TestAssistantCreateArtifactRefusesWithoutASession(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-artifact-nosession@agora.dev")
	if _, err := executeAssistantTool(t, user, assistant.ToolCreateArtifact,
		`{"title":"Orphan","kind":"markdown","content":"# body"}`); err == nil {
		t.Fatal("an artifact was created outside a conversation")
	}
}

// ---------------------------------------------------------------------------
// update_artifact
// ---------------------------------------------------------------------------

func TestAssistantUpdateArtifactBumpsVersionAndKeepsTitle(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-artifact-update@agora.dev")
	session := newAssistantTestSession(t, user)
	artifactID := createTestArtifact(t, user, session, "Runs by day", assistant.ArtifactKindChart, testChartSpec)

	updatedSpec := `{"type":"line","x":"day","series":[{"key":"runs"},{"key":"failures"}],"rows":[{"day":"Mon","runs":12,"failures":1}]}`
	result, err := executeAssistantSessionTool(t, user, session, assistant.ToolUpdateArtifact,
		`{"artifact_id":"`+artifactID+`","content":`+strconv.Quote(updatedSpec)+`}`)
	if err != nil {
		t.Fatalf("update_artifact: %v", err)
	}
	if version, _ := result["version"].(float64); version != 2 {
		t.Fatalf("version = %v, want 2", result["version"])
	}
	// Omitting title keeps the label the user is already looking at.
	if result["title"] != "Runs by day" || result["kind"] != "chart" {
		t.Fatalf("result = %v", result)
	}

	var content, title string
	var version int
	if err := testPool.QueryRow(context.Background(),
		`SELECT content, title, version FROM assistant_artifact WHERE id = $1`, artifactID,
	).Scan(&content, &title, &version); err != nil {
		t.Fatalf("read back artifact: %v", err)
	}
	if content != updatedSpec {
		t.Fatalf("content = %q, want the replacement spec", content)
	}
	if title != "Runs by day" || version != 2 {
		t.Fatalf("row = %q v%d", title, version)
	}
	// Iteration updates in place — it must not accumulate artifacts.
	if n := assistantArtifactRows(t, session); n != 1 {
		t.Fatalf("session holds %d artifacts after an update, want 1", n)
	}
}

func TestAssistantUpdateArtifactRenamesWhenAsked(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-artifact-rename@agora.dev")
	session := newAssistantTestSession(t, user)
	artifactID := createTestArtifact(t, user, session, "Draft", assistant.ArtifactKindMarkdown, "# draft")

	result, err := executeAssistantSessionTool(t, user, session, assistant.ToolUpdateArtifact,
		`{"artifact_id":"`+artifactID+`","content":"# final","title":"Sprint 12 report"}`)
	if err != nil {
		t.Fatalf("update_artifact: %v", err)
	}
	if result["title"] != "Sprint 12 report" {
		t.Fatalf("result title = %v, want the new title", result["title"])
	}
}

// The kind is read from the stored row, never restated by the model — so an
// update whose body no longer fits the kind is rejected, not silently stored
// behind a pane already rendering it as a chart.
func TestAssistantUpdateArtifactValidatesAgainstTheStoredKind(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-artifact-updatekind@agora.dev")
	session := newAssistantTestSession(t, user)
	artifactID := createTestArtifact(t, user, session, "Runs by day", assistant.ArtifactKindChart, testChartSpec)

	_, err := executeAssistantSessionTool(t, user, session, assistant.ToolUpdateArtifact,
		`{"artifact_id":"`+artifactID+`","content":"# just some markdown now"}`)
	if err == nil {
		t.Fatal("a chart artifact accepted a non-spec body")
	}
	if !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("error = %q, want a chart-spec correction", err.Error())
	}

	var content string
	var version int
	if err := testPool.QueryRow(context.Background(),
		`SELECT content, version FROM assistant_artifact WHERE id = $1`, artifactID,
	).Scan(&content, &version); err != nil {
		t.Fatalf("read back artifact: %v", err)
	}
	if content != testChartSpec || version != 1 {
		t.Fatalf("a rejected update still wrote: content %q, version %d", content, version)
	}
}

func TestAssistantUpdateArtifactRejectsUnknownID(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-artifact-unknown@agora.dev")
	session := newAssistantTestSession(t, user)

	for _, id := range []string{"", "not-a-uuid", "11111111-1111-1111-1111-111111111111"} {
		if _, err := executeAssistantSessionTool(t, user, session, assistant.ToolUpdateArtifact,
			`{"artifact_id":"`+id+`","content":"# body"}`); err == nil {
			t.Fatalf("update_artifact accepted artifact_id %q", id)
		}
	}
}

// An artifact from another of the same user's conversations is theirs to read
// but not to rewrite from here — the pane this run drives is a different one.
func TestAssistantUpdateArtifactRefusesAnotherSession(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-artifact-xsession@agora.dev")
	sessionA := newAssistantTestSession(t, user)
	sessionB := newAssistantTestSession(t, user)
	artifactID := createTestArtifact(t, user, sessionA, "In A", assistant.ArtifactKindMarkdown, "# a")

	if _, err := executeAssistantSessionTool(t, user, sessionB, assistant.ToolUpdateArtifact,
		`{"artifact_id":"`+artifactID+`","content":"# rewritten from B"}`); err == nil {
		t.Fatal("a run rewrote an artifact belonging to another conversation")
	}
}

// ---------------------------------------------------------------------------
// Ownership isolation
// ---------------------------------------------------------------------------

// The invariant of the whole feature: user B can neither read nor rewrite user
// A's artifact, and every surface says NOT FOUND rather than forbidden.
func TestAssistantArtifactOwnershipIsolation(t *testing.T) {
	alice := newAssistantTestUser(t, "assistant-artifact-alice@agora.dev")
	bob := newAssistantTestUser(t, "assistant-artifact-bob@agora.dev")
	aliceSession := newAssistantTestSession(t, alice)
	bobSession := newAssistantTestSession(t, bob)
	artifactID := createTestArtifact(t, alice, aliceSession, "Alice's chart", assistant.ArtifactKindChart, testChartSpec)

	// Tool: Bob cannot rewrite it from his own conversation...
	if _, err := executeAssistantSessionTool(t, bob, bobSession, assistant.ToolUpdateArtifact,
		`{"artifact_id":"`+artifactID+`","content":"# stolen"}`); err == nil {
		t.Fatal("update_artifact rewrote another user's artifact")
	}
	// ...nor by naming Alice's session, which is not his to run in.
	if _, err := executeAssistantSessionTool(t, bob, aliceSession, assistant.ToolUpdateArtifact,
		`{"artifact_id":"`+artifactID+`","content":"# stolen"}`); err == nil {
		t.Fatal("update_artifact ran inside another user's session")
	}
	if _, err := executeAssistantSessionTool(t, bob, aliceSession, assistant.ToolCreateArtifact,
		`{"title":"Planted","kind":"markdown","content":"# planted"}`); err == nil {
		t.Fatal("create_artifact wrote into another user's session")
	}
	if n := assistantArtifactRows(t, aliceSession); n != 1 {
		t.Fatalf("Alice's session holds %d artifacts, want the 1 she made", n)
	}
	var content string
	if err := testPool.QueryRow(context.Background(),
		`SELECT content FROM assistant_artifact WHERE id = $1`, artifactID).Scan(&content); err != nil {
		t.Fatalf("read back artifact: %v", err)
	}
	if content != testChartSpec {
		t.Fatalf("Alice's artifact was modified: %q", content)
	}

	// Endpoint: the detail read is a 404 for Bob and a 200 for Alice.
	w := httptest.NewRecorder()
	testHandler.GetAssistantArtifact(w, withURLParam(
		newAssistantRequest("GET", "/api/assistant/artifacts/"+artifactID, bob, ""), "id", artifactID))
	if w.Code != http.StatusNotFound {
		t.Fatalf("Bob read Alice's artifact: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	testHandler.GetAssistantArtifact(w, withURLParam(
		newAssistantRequest("GET", "/api/assistant/artifacts/"+artifactID, alice, ""), "id", artifactID))
	if w.Code != http.StatusOK {
		t.Fatalf("Alice could not read her own artifact: %d %s", w.Code, w.Body.String())
	}

	// Endpoint: the session list is a 404 for Bob.
	w = httptest.NewRecorder()
	testHandler.ListAssistantSessionArtifacts(w, withURLParam(
		newAssistantRequest("GET", "/api/assistant/sessions/"+aliceSession+"/artifacts", bob, ""), "id", aliceSession))
	if w.Code != http.StatusNotFound {
		t.Fatalf("Bob listed Alice's artifacts: %d %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Endpoints
// ---------------------------------------------------------------------------

func TestGetAssistantArtifactReturnsTheFullContract(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-artifact-get@agora.dev")
	session := newAssistantTestSession(t, user)
	artifactID := createTestArtifact(t, user, session, "Runs by day", assistant.ArtifactKindChart, testChartSpec)
	if _, err := executeAssistantSessionTool(t, user, session, assistant.ToolUpdateArtifact,
		`{"artifact_id":"`+artifactID+`","content":`+strconv.Quote(testChartSpec)+`}`); err != nil {
		t.Fatalf("update_artifact: %v", err)
	}

	w := httptest.NewRecorder()
	testHandler.GetAssistantArtifact(w, withURLParam(
		newAssistantRequest("GET", "/api/assistant/artifacts/"+artifactID, user, ""), "id", artifactID))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}

	// Field names are the wire contract the frontend's zod schema pins —
	// decoded generically so a renamed JSON tag fails here, not in the app.
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	for _, key := range []string{"id", "session_id", "title", "kind", "content", "version", "created_at", "updated_at"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("response is missing %q: %v", key, body)
		}
	}
	if body["id"] != artifactID || body["session_id"] != session {
		t.Fatalf("response = %v", body)
	}
	if body["kind"] != "chart" || body["title"] != "Runs by day" || body["content"] != testChartSpec {
		t.Fatalf("response = %v", body)
	}
	if version, _ := body["version"].(float64); version != 2 {
		t.Fatalf("version = %v, want 2 after one update", body["version"])
	}
	if ts, _ := body["created_at"].(string); ts == "" {
		t.Fatalf("created_at is empty: %v", body)
	}
}

func TestGetAssistantArtifactRejectsBadIDs(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-artifact-badid@agora.dev")
	for _, id := range []string{"not-a-uuid", "11111111-1111-1111-1111-111111111111"} {
		w := httptest.NewRecorder()
		testHandler.GetAssistantArtifact(w, withURLParam(
			newAssistantRequest("GET", "/api/assistant/artifacts/"+id, user, ""), "id", id))
		if w.Code != http.StatusNotFound {
			t.Fatalf("id %q returned %d, want 404", id, w.Code)
		}
	}
}

func TestListAssistantSessionArtifactsOmitsContent(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-artifact-list@agora.dev")
	session := newAssistantTestSession(t, user)
	first := createTestArtifact(t, user, session, "First", assistant.ArtifactKindMarkdown, "# first")
	second := createTestArtifact(t, user, session, "Second", assistant.ArtifactKindChart, testChartSpec)

	w := httptest.NewRecorder()
	testHandler.ListAssistantSessionArtifacts(w, withURLParam(
		newAssistantRequest("GET", "/api/assistant/sessions/"+session+"/artifacts", user, ""), "id", session))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}

	// A bare array, in production order — the shape the client parses.
	var rows []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode body: %v (%s)", err, w.Body.String())
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %v", len(rows), rows)
	}
	if rows[0]["id"] != first || rows[1]["id"] != second {
		t.Fatalf("rows are out of production order: %v", rows)
	}
	for _, row := range rows {
		if _, present := row["content"]; present {
			t.Fatalf("the list shipped an artifact body: %v", row)
		}
		for _, key := range []string{"id", "session_id", "title", "kind", "version", "created_at", "updated_at"} {
			if _, ok := row[key]; !ok {
				t.Fatalf("list row is missing %q: %v", key, row)
			}
		}
	}
	if rows[1]["kind"] != "chart" || rows[1]["title"] != "Second" {
		t.Fatalf("row = %v", rows[1])
	}
}

func TestListAssistantSessionArtifactsIsEmptyArrayNotNull(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-artifact-empty@agora.dev")
	session := newAssistantTestSession(t, user)

	w := httptest.NewRecorder()
	testHandler.ListAssistantSessionArtifacts(w, withURLParam(
		newAssistantRequest("GET", "/api/assistant/sessions/"+session+"/artifacts", user, ""), "id", session))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	// `null` would parse as a missing list on the client; `[]` is the contract.
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Fatalf("body = %q, want []", got)
	}
}
