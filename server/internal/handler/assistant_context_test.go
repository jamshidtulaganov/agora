package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jamshidtulaganov/agora/server/internal/assistant"
)

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
