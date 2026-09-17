package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
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

// ---------------------------------------------------------------------------
// Immutable revisions (docs/agora-assistant-final-plan.md §4 Phase 4)
// ---------------------------------------------------------------------------
//
// The version integer alone was never history: it said a change happened and
// destroyed what changed. These tests defend the three properties that make it
// history instead — every version is stored, no two writers can claim the same
// version, and an update written against a body someone else has already moved
// on from is refused rather than applied.

// assistantRevisionRows counts an artifact's stored history — the assertion
// that a refused update appended nothing.
func assistantRevisionRows(t *testing.T, artifactID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM assistant_artifact_revision WHERE artifact_id = $1`, artifactID).Scan(&n); err != nil {
		t.Fatalf("count revisions: %v", err)
	}
	return n
}

// updateTestArtifact runs the real update tool and returns the new version.
func updateTestArtifact(t *testing.T, userID, sessionID, artifactID, content string) int {
	t.Helper()
	result, err := executeAssistantSessionTool(t, userID, sessionID, assistant.ToolUpdateArtifact,
		`{"artifact_id":"`+artifactID+`","content":`+strconv.Quote(content)+`}`)
	if err != nil {
		t.Fatalf("update_artifact: %v", err)
	}
	version, _ := result["version"].(float64)
	return int(version)
}

// readTestRevision fetches one historical version through the real endpoint,
// as the workbench's version picker does.
func readTestRevision(t *testing.T, userID, artifactID string, version int) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	testHandler.GetAssistantArtifactRevision(w, withURLParams(
		newAssistantRequest("GET", "/api/assistant/artifacts/"+artifactID+"/revisions/"+strconv.Itoa(version), userID, ""),
		"id", artifactID, "version", strconv.Itoa(version)))
	if w.Code != http.StatusOK {
		t.Fatalf("read v%d: status %d: %s", version, w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode v%d: %v (%s)", version, err, w.Body.String())
	}
	return body
}

// listTestRevisions fetches the version picker's list through the real endpoint.
func listTestRevisions(t *testing.T, userID, artifactID string) []map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	testHandler.ListAssistantArtifactRevisions(w, withURLParam(
		newAssistantRequest("GET", "/api/assistant/artifacts/"+artifactID+"/revisions", userID, ""), "id", artifactID))
	if w.Code != http.StatusOK {
		t.Fatalf("list revisions: status %d: %s", w.Code, w.Body.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode revisions: %v (%s)", err, w.Body.String())
	}
	return rows
}

// Creation is where history starts. An artifact whose first stored revision is
// v2 would make the picker lie about where the document began, and the original
// body is gone by then — there is no later moment at which it can be recovered.
func TestAssistantCreateArtifactSeedsRevisionOne(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-revision-seed@agora.dev")
	session := newAssistantTestSession(t, user)
	artifactID := createTestArtifact(t, user, session, "Runs by day", assistant.ArtifactKindChart, testChartSpec)

	if n := assistantRevisionRows(t, artifactID); n != 1 {
		t.Fatalf("a new artifact has %d revisions, want exactly 1", n)
	}
	var version int
	var title, content string
	if err := testPool.QueryRow(context.Background(),
		`SELECT version, title, content FROM assistant_artifact_revision WHERE artifact_id = $1`, artifactID,
	).Scan(&version, &title, &content); err != nil {
		t.Fatalf("read back revision: %v", err)
	}
	if version != 1 || title != "Runs by day" || content != testChartSpec {
		t.Fatalf("seed revision = v%d %q, content %q", version, title, content)
	}
}

// The headline property: three updates leave FOUR readable versions, each with
// the body it actually had. This is what a version picker is for.
func TestAssistantArtifactRevisionsSurviveEveryUpdate(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-revision-history@agora.dev")
	session := newAssistantTestSession(t, user)
	artifactID := createTestArtifact(t, user, session, "Sprint report", assistant.ArtifactKindMarkdown, "# v1 body")

	bodies := []string{"# v1 body", "# v2 body", "# v3 body", "# v4 body"}
	for i, body := range bodies[1:] {
		if got, want := updateTestArtifact(t, user, session, artifactID, body), i+2; got != want {
			t.Fatalf("update %d returned version %d, want %d", i+1, got, want)
		}
	}

	// Every version, including the ones that are no longer current, reads back
	// with its own body — not the latest one, and not an error.
	for i, want := range bodies {
		version := i + 1
		body := readTestRevision(t, user, artifactID, version)
		if got, _ := body["version"].(float64); int(got) != version {
			t.Fatalf("v%d reports version %v", version, body["version"])
		}
		if body["content"] != want {
			t.Fatalf("v%d content = %v, want %q", version, body["content"], want)
		}
		if body["artifact_id"] != artifactID {
			t.Fatalf("v%d artifact_id = %v", version, body["artifact_id"])
		}
		// Field names are the wire contract the workbench's zod schema pins.
		for _, key := range []string{"id", "artifact_id", "version", "title", "content", "created_at"} {
			if _, ok := body[key]; !ok {
				t.Fatalf("v%d response is missing %q: %v", version, key, body)
			}
		}
	}

	// The artifact row stayed the CURRENT pointer, not a fifth history entry.
	var currentVersion int
	var currentContent string
	if err := testPool.QueryRow(context.Background(),
		`SELECT version, content FROM assistant_artifact WHERE id = $1`, artifactID,
	).Scan(&currentVersion, &currentContent); err != nil {
		t.Fatalf("read back artifact: %v", err)
	}
	if currentVersion != 4 || currentContent != "# v4 body" {
		t.Fatalf("artifact = v%d %q, want v4 with the newest body", currentVersion, currentContent)
	}
	if n := assistantRevisionRows(t, artifactID); n != 4 {
		t.Fatalf("history holds %d revisions after 3 updates, want 4", n)
	}
}

// expected_version supplied and matching is the ordinary iterate-on-what-I-read
// path: it must behave exactly like an unguarded update.
func TestAssistantUpdateArtifactAcceptsMatchingExpectedVersion(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-revision-cas-ok@agora.dev")
	session := newAssistantTestSession(t, user)
	artifactID := createTestArtifact(t, user, session, "Report", assistant.ArtifactKindMarkdown, "# v1")

	result, err := executeAssistantSessionTool(t, user, session, assistant.ToolUpdateArtifact,
		`{"artifact_id":"`+artifactID+`","content":"# v2","expected_version":1}`)
	if err != nil {
		t.Fatalf("update_artifact with a matching expected_version: %v", err)
	}
	if version, _ := result["version"].(float64); version != 2 {
		t.Fatalf("version = %v, want 2", result["version"])
	}
	if n := assistantRevisionRows(t, artifactID); n != 2 {
		t.Fatalf("history holds %d revisions, want 2", n)
	}
}

// A mismatch must REFUSE, name the current version so the model can re-read,
// and leave both the artifact and its history byte-for-byte unchanged. An
// update that silently won here would erase a change the user just made.
func TestAssistantUpdateArtifactRefusesStaleExpectedVersion(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-revision-stale@agora.dev")
	session := newAssistantTestSession(t, user)
	artifactID := createTestArtifact(t, user, session, "Report", assistant.ArtifactKindMarkdown, "# v1")
	updateTestArtifact(t, user, session, artifactID, "# v2")

	_, err := executeAssistantSessionTool(t, user, session, assistant.ToolUpdateArtifact,
		`{"artifact_id":"`+artifactID+`","content":"# written against v1","expected_version":1}`)
	if err == nil {
		t.Fatal("an update against a stale version was applied")
	}
	if !strings.Contains(err.Error(), "stale version") {
		t.Fatalf("error = %q, want it to say the version is stale", err.Error())
	}
	// Naming the CURRENT version is the correctable part: without it the model
	// can only guess or drop the guard.
	if !strings.Contains(err.Error(), "version 2") {
		t.Fatalf("error = %q, want it to name the current version", err.Error())
	}

	var version int
	var content string
	if err := testPool.QueryRow(context.Background(),
		`SELECT version, content FROM assistant_artifact WHERE id = $1`, artifactID,
	).Scan(&version, &content); err != nil {
		t.Fatalf("read back artifact: %v", err)
	}
	if version != 2 || content != "# v2" {
		t.Fatalf("a refused update still wrote: v%d %q", version, content)
	}
	if n := assistantRevisionRows(t, artifactID); n != 2 {
		t.Fatalf("a refused update appended history: %d revisions, want 2", n)
	}
}

// Two runs updating the same artifact at the same moment. The version is
// computed by the database under the row lock, so they cannot claim the same
// number — and neither caller may ever see a UNIQUE violation, which would be
// the database's bookkeeping leaking out as a tool error.
func TestAssistantUpdateArtifactConcurrentUpdatesGetDistinctVersions(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-revision-race@agora.dev")
	session := newAssistantTestSession(t, user)
	artifactID := createTestArtifact(t, user, session, "Dashboard", assistant.ArtifactKindMarkdown, "# v1")

	bodies := [2]string{"# from A", "# from B"}
	var versions [2]int
	var errs [2]error
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // both goroutines enter the write path together
			raw, err := testHandler.Execute(context.Background(), user, session, assistant.ToolUpdateArtifact,
				json.RawMessage(`{"artifact_id":"`+artifactID+`","content":`+strconv.Quote(bodies[i])+`}`))
			if err != nil {
				errs[i] = err
				return
			}
			var out map[string]any
			if err := json.Unmarshal(raw, &out); err != nil {
				errs[i] = err
				return
			}
			version, _ := out["version"].(float64)
			versions[i] = int(version)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent update %d failed: %v", i, err)
		}
	}
	if versions[0] == versions[1] {
		t.Fatalf("both concurrent updates claimed version %d", versions[0])
	}
	if (versions[0] != 2 && versions[0] != 3) || (versions[1] != 2 && versions[1] != 3) {
		t.Fatalf("concurrent updates returned versions %d and %d, want 2 and 3 in some order", versions[0], versions[1])
	}

	// Three versions exist and each body is filed under the version its own
	// caller was told about — the history is not just distinct, it is correct.
	if n := assistantRevisionRows(t, artifactID); n != 3 {
		t.Fatalf("history holds %d revisions after two concurrent updates, want 3", n)
	}
	for i, version := range versions {
		body := readTestRevision(t, user, artifactID, version)
		if body["content"] != bodies[i] {
			t.Fatalf("v%d content = %v, want %q", version, body["content"], bodies[i])
		}
	}
	// The pointer ends at the higher of the two, holding that writer's body.
	var currentVersion int
	var currentContent string
	if err := testPool.QueryRow(context.Background(),
		`SELECT version, content FROM assistant_artifact WHERE id = $1`, artifactID,
	).Scan(&currentVersion, &currentContent); err != nil {
		t.Fatalf("read back artifact: %v", err)
	}
	if currentVersion != 3 {
		t.Fatalf("artifact is at v%d after two updates, want v3", currentVersion)
	}
	winner := bodies[0]
	if versions[1] == 3 {
		winner = bodies[1]
	}
	if currentContent != winner {
		t.Fatalf("artifact content = %q, want %q — the body of whichever update got v3", currentContent, winner)
	}
}

// The list backs a picker, so it ships numbers, labels and dates — never
// bodies. Up to 50 revisions x 256 KB is the size this rule is protecting.
func TestListAssistantArtifactRevisionsOmitsContent(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-revision-list@agora.dev")
	session := newAssistantTestSession(t, user)
	artifactID := createTestArtifact(t, user, session, "Draft", assistant.ArtifactKindMarkdown, "# v1")
	if _, err := executeAssistantSessionTool(t, user, session, assistant.ToolUpdateArtifact,
		`{"artifact_id":"`+artifactID+`","content":"# v2","title":"Final"}`); err != nil {
		t.Fatalf("update_artifact: %v", err)
	}

	rows := listTestRevisions(t, user, artifactID)
	if len(rows) != 2 {
		t.Fatalf("got %d revisions, want 2: %v", len(rows), rows)
	}
	// Newest first: a picker opens on the current version.
	if v, _ := rows[0]["version"].(float64); v != 2 {
		t.Fatalf("first row is version %v, want the newest (2)", rows[0]["version"])
	}
	if v, _ := rows[1]["version"].(float64); v != 1 {
		t.Fatalf("second row is version %v, want 1", rows[1]["version"])
	}
	// Each revision keeps the title it was saved under, so a rename is visible
	// in the history rather than rewriting every earlier entry.
	if rows[0]["title"] != "Final" || rows[1]["title"] != "Draft" {
		t.Fatalf("titles = %v / %v, want the title each version was saved with", rows[0]["title"], rows[1]["title"])
	}
	for _, row := range rows {
		if _, present := row["content"]; present {
			t.Fatalf("the list shipped a revision body: %v", row)
		}
		for _, key := range []string{"id", "artifact_id", "version", "title", "created_at"} {
			if _, ok := row[key]; !ok {
				t.Fatalf("list row is missing %q: %v", key, row)
			}
		}
		if row["artifact_id"] != artifactID {
			t.Fatalf("row points at %v, want %s", row["artifact_id"], artifactID)
		}
		if ts, _ := row["created_at"].(string); ts == "" {
			t.Fatalf("created_at is empty: %v", row)
		}
	}
}

// Same invariant as the artifact endpoints: another person's history is NOT
// FOUND on every surface, never forbidden — a 403 would confirm it exists.
func TestAssistantArtifactRevisionOwnershipIsolation(t *testing.T) {
	alice := newAssistantTestUser(t, "assistant-revision-alice@agora.dev")
	bob := newAssistantTestUser(t, "assistant-revision-bob@agora.dev")
	aliceSession := newAssistantTestSession(t, alice)
	artifactID := createTestArtifact(t, alice, aliceSession, "Alice's chart", assistant.ArtifactKindChart, testChartSpec)
	updateTestArtifact(t, alice, aliceSession, artifactID, testChartSpec)

	w := httptest.NewRecorder()
	testHandler.ListAssistantArtifactRevisions(w, withURLParam(
		newAssistantRequest("GET", "/api/assistant/artifacts/"+artifactID+"/revisions", bob, ""), "id", artifactID))
	if w.Code != http.StatusNotFound {
		t.Fatalf("Bob listed Alice's revisions: %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	testHandler.GetAssistantArtifactRevision(w, withURLParams(
		newAssistantRequest("GET", "/api/assistant/artifacts/"+artifactID+"/revisions/1", bob, ""),
		"id", artifactID, "version", "1"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("Bob read a version of Alice's artifact: %d %s", w.Code, w.Body.String())
	}

	// Alice reaches both.
	if rows := listTestRevisions(t, alice, artifactID); len(rows) != 2 {
		t.Fatalf("Alice sees %d revisions, want 2", len(rows))
	}
	if body := readTestRevision(t, alice, artifactID, 1); body["content"] != testChartSpec {
		t.Fatalf("Alice's v1 = %v", body["content"])
	}
}

func TestGetAssistantArtifactRevisionRejectsBadVersions(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-revision-badversion@agora.dev")
	session := newAssistantTestSession(t, user)
	artifactID := createTestArtifact(t, user, session, "Report", assistant.ArtifactKindMarkdown, "# v1")

	// Unparseable, non-positive, and a version this artifact never reached all
	// take the same branch — the client has one not-found path to render.
	for _, version := range []string{"", "abc", "0", "-1", "2", "99"} {
		w := httptest.NewRecorder()
		testHandler.GetAssistantArtifactRevision(w, withURLParams(
			newAssistantRequest("GET", "/api/assistant/artifacts/"+artifactID+"/revisions/"+version, user, ""),
			"id", artifactID, "version", version))
		if w.Code != http.StatusNotFound {
			t.Fatalf("version %q returned %d, want 404", version, w.Code)
		}
	}
	// An unknown artifact is a 404 on the list too, not an empty array.
	w := httptest.NewRecorder()
	testHandler.ListAssistantArtifactRevisions(w, withURLParam(
		newAssistantRequest("GET", "/api/assistant/artifacts/11111111-1111-1111-1111-111111111111/revisions", user, ""),
		"id", "11111111-1111-1111-1111-111111111111"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("an unknown artifact listed %d, want 404", w.Code)
	}
}

// Retention: an artifact refreshed forever must not grow forever. The cap
// keeps v1 — the only version whose meaning is self-contained — plus the
// newest ones, and drops the middle.
func TestAssistantArtifactRevisionsHonourRetentionCap(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-revision-cap@agora.dev")
	session := newAssistantTestSession(t, user)
	artifactID := createTestArtifact(t, user, session, "Live dashboard", assistant.ArtifactKindMarkdown, "# v1")

	// Five versions past the cap, so there is something to drop and something
	// that must survive on either side of it.
	const overflow = 5
	last := assistant.MaxArtifactRevisions + overflow
	for version := 2; version <= last; version++ {
		if got := updateTestArtifact(t, user, session, artifactID, "# v"+strconv.Itoa(version)); got != version {
			t.Fatalf("update returned version %d, want %d", got, version)
		}
	}

	if n := assistantRevisionRows(t, artifactID); n != assistant.MaxArtifactRevisions {
		t.Fatalf("history holds %d revisions after %d versions, want the cap of %d",
			n, last, assistant.MaxArtifactRevisions)
	}

	rows := listTestRevisions(t, user, artifactID)
	kept := make(map[int]bool, len(rows))
	for _, row := range rows {
		version, _ := row["version"].(float64)
		kept[int(version)] = true
	}
	// v1 is pinned: without it the oldest surviving revision would silently
	// start reading as "where this artifact began".
	if !kept[1] {
		t.Fatalf("the cap dropped v1: %v", kept)
	}
	if body := readTestRevision(t, user, artifactID, 1); body["content"] != "# v1" {
		t.Fatalf("v1 content = %v, want the original body", body["content"])
	}
	// The OLDEST above the cap went first...
	for version := 2; version <= overflow+1; version++ {
		if kept[version] {
			t.Fatalf("v%d should have been trimmed: %v", version, kept)
		}
	}
	// ...and everything newer is intact and still readable.
	for version := overflow + 2; version <= last; version++ {
		if !kept[version] {
			t.Fatalf("v%d was trimmed but is inside the cap: %v", version, kept)
		}
	}
	if body := readTestRevision(t, user, artifactID, last); body["content"] != "# v"+strconv.Itoa(last) {
		t.Fatalf("newest revision content = %v", body["content"])
	}
}

// The tool's pre-check is a courtesy that makes the common refusal cheap and
// well-worded. The guard that actually HOLDS is the one in the UPDATE's WHERE
// clause, evaluated under the row lock — it is the only one that can refuse a
// write whose expectation went stale in the microseconds after the tool read
// the row. No public call can interpose a commit at exactly that moment, so
// the statement is exercised directly.
func TestUpdateAssistantArtifactQueryRefusesStaleExpectedVersion(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-revision-cas-sql@agora.dev")
	session := newAssistantTestSession(t, user)
	artifactID := createTestArtifact(t, user, session, "Report", assistant.ArtifactKindMarkdown, "# v1")
	id, err := util.ParseUUID(artifactID)
	if err != nil {
		t.Fatalf("parse artifact id: %v", err)
	}

	_, err = testHandler.Queries.UpdateAssistantArtifact(context.Background(), db.UpdateAssistantArtifactParams{
		ID:              id,
		Content:         "# written against a version this artifact never had",
		ExpectedVersion: pgtype.Int4{Int32: 7, Valid: true},
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("a mismatched expected_version returned %v, want no rows matched", err)
	}
	var version int
	var content string
	if err := testPool.QueryRow(context.Background(),
		`SELECT version, content FROM assistant_artifact WHERE id = $1`, artifactID,
	).Scan(&version, &content); err != nil {
		t.Fatalf("read back artifact: %v", err)
	}
	if version != 1 || content != "# v1" {
		t.Fatalf("the refused statement still wrote: v%d %q", version, content)
	}

	// An absent expected_version leaves the statement unconditional — the
	// default path must not be accidentally guarded by a NULL comparison.
	updated, err := testHandler.Queries.UpdateAssistantArtifact(context.Background(), db.UpdateAssistantArtifactParams{
		ID:      id,
		Content: "# v2",
	})
	if err != nil {
		t.Fatalf("unguarded update: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("unguarded update produced v%d, want v2", updated.Version)
	}
}

// A non-integer expected_version is a malformed argument, not a stale one: it
// must come back as a correction and write nothing, rather than being coerced
// into some version number.
func TestAssistantUpdateArtifactRejectsMalformedExpectedVersion(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-revision-badcas@agora.dev")
	session := newAssistantTestSession(t, user)
	artifactID := createTestArtifact(t, user, session, "Report", assistant.ArtifactKindMarkdown, "# v1")

	for _, raw := range []string{`"2"`, `1.5`, `true`} {
		if _, err := executeAssistantSessionTool(t, user, session, assistant.ToolUpdateArtifact,
			`{"artifact_id":"`+artifactID+`","content":"# v2","expected_version":`+raw+`}`); err == nil {
			t.Fatalf("expected_version %s was accepted", raw)
		}
	}
	var version int
	if err := testPool.QueryRow(context.Background(),
		`SELECT version FROM assistant_artifact WHERE id = $1`, artifactID).Scan(&version); err != nil {
		t.Fatalf("read back artifact: %v", err)
	}
	if version != 1 {
		t.Fatalf("a malformed expected_version still bumped the artifact to v%d", version)
	}
	if n := assistantRevisionRows(t, artifactID); n != 1 {
		t.Fatalf("a malformed expected_version appended history: %d revisions, want 1", n)
	}
}
