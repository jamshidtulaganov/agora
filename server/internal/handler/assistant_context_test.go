package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jamshidtulaganov/agora/server/internal/assistant"
)

func TestAssistantRepoReferenceRedactsURLSecrets(t *testing.T) {
	got := assistantRepoReference("https://alice:secret@github.com/example/repo.git?token=signed#fragment")
	want := "https://github.com/example/repo.git"
	if got != want {
		t.Fatalf("repository reference = %q, want %q", got, want)
	}
	if got := assistantRepoReference("git@github.com:example/repo.git"); got != "git@github.com:example/repo.git" {
		t.Fatalf("SSH shorthand changed: %q", got)
	}
}

func seedAssistantContextAttachment(t *testing.T, store *mockStorage, ws, user, filename, contentType string, body []byte) string {
	t.Helper()
	id := uuid.NewString()
	url, err := store.Upload(context.Background(), "assistant-context/"+id, body, contentType, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `INSERT INTO attachment(id,workspace_id,uploader_type,uploader_id,filename,url,content_type,size_bytes) VALUES($1,$2,'member',$3,$4,$5,$6,$7)`, id, ws, user, filename, url, contentType, len(body)); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAssistantContextCapturesProjectResourcesAndFileText(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-context-snapshot@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-context-snapshot-ws", "ACS")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	project := newAssistantTestProject(t, ws, "Atlas")
	if _, err := testPool.Exec(context.Background(), `UPDATE project SET description='Plan from September' WHERE id=$1`, project); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `INSERT INTO project_resource(project_id,workspace_id,resource_type,resource_ref,label) VALUES($1,$2,'github_repo','{"url":"https://github.com/example/atlas"}'::jsonb,'source')`, project, ws); err != nil {
		t.Fatal(err)
	}
	store := &mockStorage{}
	prev := testHandler.Storage
	testHandler.Storage = store
	t.Cleanup(func() { testHandler.Storage = prev })
	file := seedAssistantContextAttachment(t, store, ws, user, "notes.md", "text/markdown", []byte("# Notes\nShip Atlas.\n"))
	durableAssistantFixture(t, session)
	payload := map[string]any{"content": "Summarize this project and file", "request_id": uuid.NewString(), "context": map[string]any{"workspace_id": ws, "project_id": project, "attachment_ids": []string{file}, "timezone": "UTC"}}
	w := sendRecoveryRequest(t, user, session, payload)
	if w.Code != http.StatusAccepted {
		t.Fatalf("send: %d %s", w.Code, w.Body.String())
	}
	var accepted SendAssistantMessageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := testPool.QueryRow(context.Background(), `SELECT context_snapshot FROM assistant_run WHERE id=$1`, accepted.RunID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var snapshot assistant.ContextSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Project == nil || snapshot.Project.ID != project || snapshot.Project.Description != "Plan from September" || len(snapshot.Project.Resources) != 1 {
		t.Fatalf("project snapshot: %+v", snapshot.Project)
	}
	if len(snapshot.Files) != 1 || snapshot.Files[0].Content != "# Notes\nShip Atlas.\n" {
		t.Fatalf("file snapshot: %+v", snapshot.Files)
	}
	if snapshot.Project.Resources[0].Reference != "https://github.com/example/atlas" {
		t.Fatalf("resource inventory: %+v", snapshot.Project.Resources)
	}
	if _, err := testPool.Exec(context.Background(), `UPDATE project SET description='changed later' WHERE id=$1`, project); err != nil {
		t.Fatal(err)
	}
	store.Delete(context.Background(), "assistant-context/"+file)
	replay := sendRecoveryRequest(t, user, session, payload)
	if replay.Code != http.StatusAccepted || replay.Body.String() != w.Body.String() {
		t.Fatalf("replay after source changed: %d %s", replay.Code, replay.Body.String())
	}
	if _, err := testPool.Exec(context.Background(), `DELETE FROM project WHERE id=$1`, project); err != nil {
		t.Fatal(err)
	}
	if err := testHandler.validateAssistantRunContext(context.Background(), user, assistant.RunContext{WorkspaceID: &ws, ProjectID: &project, AttachmentIDs: []string{file}}); err == nil {
		t.Fatal("deleted selected project remained executable context")
	}
}

func TestAssistantContextRejectsForeignProjectAndFiles(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-context-denied@agora.dev")
	other := newAssistantTestUser(t, "assistant-context-owner@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-context-own-ws", "ACW")
	foreign := newAssistantTestWorkspace(t, "assistant-context-other-ws", "ACF")
	addAssistantTestMember(t, ws, user, "member")
	addAssistantTestMember(t, foreign, other, "owner")
	session := newAssistantTestSession(t, user)
	project := newAssistantTestProject(t, foreign, "private")
	store := &mockStorage{}
	prev := testHandler.Storage
	testHandler.Storage = store
	t.Cleanup(func() { testHandler.Storage = prev })
	file := seedAssistantContextAttachment(t, store, foreign, other, "secret.txt", "text/plain", []byte("secret"))
	durableAssistantFixture(t, session)
	for _, ctx := range []map[string]any{
		{"workspace_id": ws, "project_id": project},
		{"workspace_id": ws, "attachment_ids": []string{file}},
	} {
		w := sendRecoveryRequest(t, user, session, map[string]any{"content": "read it", "request_id": uuid.NewString(), "context": ctx})
		if w.Code < 400 || w.Code >= 500 {
			t.Fatalf("foreign context accepted: %d %s", w.Code, w.Body.String())
		}
	}
	var count int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM assistant_run WHERE session_id=$1`, session).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("invalid context created %d runs", count)
	}
}

func TestAssistantContextRejectsUnsupportedAndOversizeFiles(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-context-limits@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-context-limits-ws", "ACL")
	addAssistantTestMember(t, ws, user, "owner")
	session := newAssistantTestSession(t, user)
	store := &mockStorage{}
	prev := testHandler.Storage
	testHandler.Storage = store
	t.Cleanup(func() { testHandler.Storage = prev })
	pdf := seedAssistantContextAttachment(t, store, ws, user, "binary.pdf", "application/pdf", []byte("%PDF"))
	large := seedAssistantContextAttachment(t, store, ws, user, "large.md", "text/markdown", make([]byte, assistantMaxFileBytes+1))
	durableAssistantFixture(t, session)
	for _, tc := range []struct {
		id     string
		status int
	}{{pdf, http.StatusUnsupportedMediaType}, {large, http.StatusRequestEntityTooLarge}} {
		w := sendRecoveryRequest(t, user, session, map[string]any{"content": "read it", "request_id": uuid.NewString(), "context": map[string]any{"workspace_id": ws, "attachment_ids": []string{tc.id}}})
		if w.Code != tc.status {
			t.Fatalf("file %s: %d %s, want %d", tc.id, w.Code, w.Body.String(), tc.status)
		}
	}
}

// The composer's member chip: "assign it to her" only works if "her" was
// resolved to an id BEFORE the model saw the message.
func TestAssistantContextCapturesAttachedMember(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-context-member@agora.dev")
	teammate := newAssistantTestUser(t, "assistant-context-teammate@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-context-member-ws", "ACM")
	addAssistantTestMember(t, ws, user, "owner")
	addAssistantTestMember(t, ws, teammate, "member")
	session := newAssistantTestSession(t, user)
	durableAssistantFixture(t, session)

	payload := map[string]any{
		"content":    "assign the login bug to her",
		"request_id": uuid.NewString(),
		"context":    map[string]any{"workspace_id": ws, "member_id": teammate, "timezone": "UTC"},
	}
	w := sendRecoveryRequest(t, user, session, payload)
	if w.Code != http.StatusAccepted {
		t.Fatalf("send: %d %s", w.Code, w.Body.String())
	}
	var accepted SendAssistantMessageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}

	var raw []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT context_snapshot FROM assistant_run WHERE id=$1`, accepted.RunID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var snapshot assistant.ContextSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Member == nil || snapshot.Member.UserID != teammate {
		t.Fatalf("member snapshot: %+v", snapshot.Member)
	}
	if snapshot.Member.Name == "" {
		t.Fatal("member snapshot carries no name — the model cannot address the person by id alone")
	}

	// The snapshot is only useful if it reaches the prompt: this is the exact
	// text buildSystemPrompt appends for this run.
	prompt := snapshot.Prompt()
	if !strings.Contains(prompt, teammate) || !strings.Contains(prompt, snapshot.Member.Name) {
		t.Fatalf("prompt does not name the attached member: %q", prompt)
	}
	if !strings.Contains(prompt, "Resolve pronouns") {
		t.Fatalf("prompt never tells the model what the attached member is for: %q", prompt)
	}

	// No member attached, no member sentence — an empty context stays empty.
	if got := (assistant.ContextSnapshot{}).Prompt(); got != "" {
		t.Fatalf("empty snapshot rendered %q", got)
	}
}

// The picker is a convenience, not the authority: the workspace roster is
// re-checked server-side, so a stale composer (or a hand-made request) cannot
// attach somebody from a workspace this message is not running in.
func TestAssistantContextRejectsMemberOutsideTheWorkspace(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-context-member-denied@agora.dev")
	outsider := newAssistantTestUser(t, "assistant-context-member-outsider@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-context-member-own-ws", "AMO")
	other := newAssistantTestWorkspace(t, "assistant-context-member-other-ws", "AMT")
	addAssistantTestMember(t, ws, user, "owner")
	addAssistantTestMember(t, other, outsider, "owner")
	session := newAssistantTestSession(t, user)
	durableAssistantFixture(t, session)

	w := sendRecoveryRequest(t, user, session, map[string]any{
		"content":    "what is on their plate",
		"request_id": uuid.NewString(),
		"context":    map[string]any{"workspace_id": ws, "member_id": outsider},
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("foreign member accepted: %d %s", w.Code, w.Body.String())
	}

	// Not a UUID at all is a 400 from the send path, before any query runs.
	bad := sendRecoveryRequest(t, user, session, map[string]any{
		"content":    "what is on their plate",
		"request_id": uuid.NewString(),
		"context":    map[string]any{"workspace_id": ws, "member_id": "her"},
	})
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("malformed member_id: %d %s", bad.Code, bad.Body.String())
	}

	var count int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM assistant_run WHERE session_id=$1`, session).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("invalid member context created %d runs", count)
	}

	// And a member who LEAVES between send and execution stops being usable
	// context — the run-time revalidation the service calls before it builds
	// the prompt.
	teammate := newAssistantTestUser(t, "assistant-context-member-left@agora.dev")
	addAssistantTestMember(t, ws, teammate, "member")
	if err := testHandler.validateAssistantRunContext(context.Background(), user,
		assistant.RunContext{WorkspaceID: &ws, MemberID: &teammate}); err != nil {
		t.Fatalf("current member rejected: %v", err)
	}
	if _, err := testPool.Exec(context.Background(),
		`DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, ws, teammate); err != nil {
		t.Fatal(err)
	}
	if err := testHandler.validateAssistantRunContext(context.Background(), user,
		assistant.RunContext{WorkspaceID: &ws, MemberID: &teammate}); err == nil {
		t.Fatal("a member who left the workspace remained executable context")
	}
}

// The workspace picker names a per-message target, so the send path — not just
// the tools — has to hold the membership line. Without this, a composer
// pointed at a workspace the user was removed from would open a run scoped
// there and let the tools inherit it.
func TestAssistantSendRefusesAWorkspaceTheSenderIsNotIn(t *testing.T) {
	user := newAssistantTestUser(t, "assistant-send-scope@agora.dev")
	stranger := newAssistantTestUser(t, "assistant-send-scope-owner@agora.dev")
	mine := newAssistantTestWorkspace(t, "assistant-send-scope-mine", "SSM")
	theirs := newAssistantTestWorkspace(t, "assistant-send-scope-theirs", "SST")
	addAssistantTestMember(t, mine, user, "owner")
	addAssistantTestMember(t, theirs, stranger, "owner")
	session := newAssistantTestSession(t, user)
	durableAssistantFixture(t, session)

	w := sendRecoveryRequest(t, user, session, map[string]any{
		"content":    "what is open here",
		"request_id": uuid.NewString(),
		"context":    map[string]any{"workspace_id": theirs},
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("send into a non-member workspace: %d %s", w.Code, w.Body.String())
	}

	// The same message into a workspace they ARE in is accepted and the run
	// records that workspace, which is what every tool then defaults to.
	ok := sendRecoveryRequest(t, user, session, map[string]any{
		"content":    "what is open here",
		"request_id": uuid.NewString(),
		"context":    map[string]any{"workspace_id": mine},
	})
	if ok.Code != http.StatusAccepted {
		t.Fatalf("send into own workspace: %d %s", ok.Code, ok.Body.String())
	}
	var accepted SendAssistantMessageResponse
	if err := json.Unmarshal(ok.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := testPool.QueryRow(context.Background(),
		`SELECT context_workspace_id::text FROM assistant_run WHERE id=$1`, accepted.RunID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != mine {
		t.Fatalf("run workspace = %s, want %s", stored, mine)
	}
}
