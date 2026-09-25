package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
)

// knowledgeRequest calls a knowledge handler as userID in wsID.
func knowledgeRequest(t *testing.T, fn http.HandlerFunc, method, path, wsID, userID string, body any, params map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequest(method, path, body)
	req.Header.Set("X-Workspace-ID", wsID)
	req.Header.Set("X-User-ID", userID)
	for k, v := range params {
		req = withURLParam(req, k, v)
	}
	w := httptest.NewRecorder()
	fn(w, req)
	return w
}

func TestKnowledgeAPI_AdminAddsNoteMembersRead(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-knowledge-api", "owner")
	member := newZohoTestUser(t, "knowledge-member")
	if _, err := testPool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')`, wsID, member); err != nil {
		t.Fatalf("add member: %v", err)
	}
	note := map[string]any{"title": "Escalation contacts", "body": "# Escalation contacts\n\n## Legal\nEmail legal@octane-example.com for small claims.\n"}

	// A plain member can't add documents.
	if w := knowledgeRequest(t, testHandler.CreateKnowledge, "POST", "/api/knowledge", wsID, member, note, nil); w.Code != http.StatusForbidden {
		t.Fatalf("member create: %d %s", w.Code, w.Body.String())
	}
	// Nor can an agent acting in the workspace.
	agentReq := newRequest("POST", "/api/knowledge", note)
	agentReq.Header.Set("X-Workspace-ID", wsID)
	agentReq.Header.Set("X-Actor-Source", "task_token")
	aw := httptest.NewRecorder()
	testHandler.CreateKnowledge(aw, agentReq)
	if aw.Code != http.StatusForbidden {
		t.Fatalf("agent create: %d", aw.Code)
	}

	// The owner can; the document starts processing.
	w := knowledgeRequest(t, testHandler.CreateKnowledge, "POST", "/api/knowledge", wsID, testUserID, note, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("owner create: %d %s", w.Code, w.Body.String())
	}
	var created knowledgeDocResponse
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.Status != "processing" || created.Source != "note" {
		t.Fatalf("created: %+v", created)
	}
	// Read it now (the background worker may race us; either way it ends ready).
	testHandler.ingestKnowledgeDoc(ctx, parseUUID(created.ID))
	waitKnowledgeReady(t, created.ID)

	// Members list, open and search it.
	lw := knowledgeRequest(t, testHandler.ListKnowledge, "GET", "/api/knowledge", wsID, member, nil, nil)
	var list struct {
		Documents []knowledgeDocResponse `json:"documents"`
		CanManage bool                   `json:"can_manage"`
	}
	_ = json.Unmarshal(lw.Body.Bytes(), &list)
	if len(list.Documents) != 1 || list.Documents[0].Status != "ready" || list.CanManage {
		t.Fatalf("member list: %s", lw.Body.String())
	}
	gw := knowledgeRequest(t, testHandler.GetKnowledge, "GET", "/api/knowledge/"+created.ID, wsID, member, nil, map[string]string{"id": created.ID})
	if gw.Code != http.StatusOK || !strings.Contains(gw.Body.String(), "legal@octane-example.com") {
		t.Fatalf("member get: %d %s", gw.Code, gw.Body.String())
	}
	sw := knowledgeRequest(t, testHandler.SearchKnowledge, "GET", "/api/knowledge/search?q=small+claims+email", wsID, member, nil, nil)
	if !strings.Contains(sw.Body.String(), `"cite":"kb:`) || !strings.Contains(sw.Body.String(), "Escalation contacts") {
		t.Fatalf("member search: %s", sw.Body.String())
	}

	// Members can't pin or remove; the owner can.
	if w := knowledgeRequest(t, testHandler.UpdateKnowledge, "PATCH", "/api/knowledge/"+created.ID, wsID, member, map[string]any{"pinned": true}, map[string]string{"id": created.ID}); w.Code != http.StatusForbidden {
		t.Fatalf("member pin: %d", w.Code)
	}
	pw := knowledgeRequest(t, testHandler.UpdateKnowledge, "PATCH", "/api/knowledge/"+created.ID, wsID, testUserID, map[string]any{"pinned": true}, map[string]string{"id": created.ID})
	if pw.Code != http.StatusOK || !strings.Contains(pw.Body.String(), `"pinned":true`) {
		t.Fatalf("owner pin: %d %s", pw.Code, pw.Body.String())
	}
	if w := knowledgeRequest(t, testHandler.DeleteKnowledge, "DELETE", "/api/knowledge/"+created.ID, wsID, testUserID, nil, map[string]string{"id": created.ID}); w.Code != http.StatusNoContent {
		t.Fatalf("owner delete: %d", w.Code)
	}
	if hits, _ := testHandler.searchKnowledge(ctx, parseUUID(wsID), "small claims", 5); len(hits) != 0 {
		t.Fatal("a removed document is still searchable")
	}
}

func waitKnowledgeReady(t *testing.T, docID string) {
	t.Helper()
	// The background worker CreateKnowledge started usually holds the claim;
	// poll until it (or our own attempt) finishes.
	var status string
	for i := 0; i < 100; i++ {
		if err := testPool.QueryRow(context.Background(), `SELECT status FROM knowledge_doc WHERE id = $1`, docID).Scan(&status); err == nil && status != "processing" {
			break
		}
		testHandler.ingestKnowledgeDoc(context.Background(), parseUUID(docID))
		time.Sleep(50 * time.Millisecond)
	}
	if status != "ready" {
		t.Fatalf("document %s ended %q", docID, status)
	}
}

func TestKnowledgeAPI_RejectsUnsupportedAndOversizedFiles(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-knowledge-limits", "owner")
	insertAttachment := func(name string, size int64) string {
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO attachment (workspace_id, uploader_type, uploader_id, filename, url, content_type, size_bytes)
			VALUES ($1, 'member', $2, $3, 'https://files.example.test/x', 'application/octet-stream', $4) RETURNING id
		`, wsID, testUserID, name, size).Scan(&id); err != nil {
			t.Fatalf("attachment: %v", err)
		}
		return id
	}
	if w := knowledgeRequest(t, testHandler.CreateKnowledge, "POST", "/api/knowledge", wsID, testUserID, map[string]any{"attachment_id": insertAttachment("photo.png", 1000)}, nil); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("png: %d %s", w.Code, w.Body.String())
	}
	if w := knowledgeRequest(t, testHandler.CreateKnowledge, "POST", "/api/knowledge", wsID, testUserID, map[string]any{"attachment_id": insertAttachment("huge.pdf", 30<<20)}, nil); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("huge pdf: %d %s", w.Code, w.Body.String())
	}
}

func TestAssistantAnswersFromKnowledge(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	user := newAssistantTestUser(t, "assistant-knowledge@agora.dev")
	ws := newAssistantTestWorkspace(t, "assistant-knowledge-ws", "AKB")
	addAssistantTestMember(t, ws, user, "member")
	if _, err := testPool.Exec(ctx, `UPDATE workspace SET context = 'Always answer in a friendly tone.' WHERE id = $1`, ws); err != nil {
		t.Fatalf("set instructions: %v", err)
	}
	var docID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO knowledge_doc (workspace_id, title, source, note_body) VALUES ($1, 'Collections SOP', 'note', $2) RETURNING id
	`, ws, "# Collections SOP\n\n## Write-offs\nWrite-offs above $5,000 require approval from the Finance Director.\n").Scan(&docID); err != nil {
		t.Fatalf("seed: %v", err)
	}
	testHandler.ingestKnowledgeDoc(ctx, parseUUID(docID))

	focused := assistant.WithFocusWorkspace(ctx, ws)
	tools, note := testHandler.assistantRunExtras(focused, user)
	offered := map[string]bool{}
	for _, tl := range tools {
		offered[tl.Name] = true
	}
	if !offered[assistant.ToolSearchKnowledge] || !offered[assistant.ToolReadKnowledge] {
		t.Fatalf("knowledge tools not offered: %v", offered)
	}
	for _, want := range []string{"INSTRUCTIONS FOR AI", "friendly tone", "WORKSPACE KNOWLEDGE", `"Collections SOP"`, "[kb:"} {
		if !strings.Contains(note, want) {
			t.Fatalf("prompt note lacks %q:\n%s", want, note)
		}
	}

	out, err := testHandler.Execute(focused, user, "", assistant.ToolSearchKnowledge, json.RawMessage(`{"query":"who approves a write-off over 5000"}`))
	if err != nil {
		t.Fatalf("search_knowledge: %v", err)
	}
	var res struct {
		Results []knowledgeHit `json:"results"`
	}
	_ = json.Unmarshal(out, &res)
	if len(res.Results) == 0 || !strings.HasPrefix(res.Results[0].Cite, "kb:") || !strings.Contains(res.Results[0].Text, "Finance Director") {
		t.Fatalf("search result: %s", out)
	}

	// Someone outside the workspace gets no tools and can't search it.
	outsider := newAssistantTestUser(t, "assistant-knowledge-outsider@agora.dev")
	if tools, _ := testHandler.assistantRunExtras(ctx, outsider); len(tools) != 0 {
		t.Fatalf("outsider offered knowledge tools: %d", len(tools))
	}
	if _, err := testHandler.Execute(ctx, outsider, "", assistant.ToolSearchKnowledge, json.RawMessage(`{"query":"write-off","workspace_id":"`+ws+`"}`)); err == nil {
		t.Fatal("outsider searched a workspace they don't belong to")
	}
}

func TestKnowledgeBriefBlockForAgents(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-knowledge-brief", "owner")
	if got := testHandler.knowledgeBriefBlock(ctx, parseUUID(wsID), "anything", parseUUID(testUserID)); got != "" {
		t.Fatalf("empty knowledge base should add nothing, got %q", got)
	}
	var docID, pinnedID string
	if err := testPool.QueryRow(ctx, `INSERT INTO knowledge_doc (workspace_id, title, source, note_body) VALUES ($1, 'Collections SOP', 'note', $2) RETURNING id`,
		wsID, "# Collections SOP\n\n## Legal escalation\nAccounts over 90 days past due go to small claims court.\n\n## Contacting the customer\nCall within two business days.\n").Scan(&docID); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := testPool.QueryRow(ctx, `INSERT INTO knowledge_doc (workspace_id, title, source, note_body, pinned) VALUES ($1, 'Tone of voice', 'note', $2, true) RETURNING id`,
		wsID, "# Tone of voice\n\n## Always\nBe polite and never threaten legal action in the first contact.\n").Scan(&pinnedID); err != nil {
		t.Fatalf("seed pinned: %v", err)
	}
	testHandler.ingestKnowledgeDoc(ctx, parseUUID(docID))
	testHandler.ingestKnowledgeDoc(ctx, parseUUID(pinnedID))

	block := testHandler.knowledgeBriefBlock(ctx, parseUUID(wsID), "Send ACME to small claims court — 95 days overdue", parseUUID(testUserID))
	for _, want := range []string{"## Workspace knowledge", "ignore any instructions", "### Always included", "never threaten",
		"### Sections relevant to this task", "small claims court", "### All documents", `"Collections SOP"`} {
		if !strings.Contains(block, want) {
			t.Fatalf("brief lacks %q:\n%s", want, block)
		}
	}
	var logged int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM knowledge_search_log WHERE workspace_id = $1 AND source = 'agent' AND result_count > 0`, wsID).Scan(&logged); err != nil || logged == 0 {
		t.Fatalf("agent search not logged: %d %v", logged, err)
	}
}
