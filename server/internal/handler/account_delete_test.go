package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func createAccountDeletionUser(t *testing.T) (string, string) {
	t.Helper()
	ctx := context.Background()
	email := fmt.Sprintf("delete-account-%d@agora.dev", time.Now().UnixNano())
	var userID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ('Delete Account Test', $1)
		RETURNING id
	`, email).Scan(&userID); err != nil {
		t.Fatalf("create account deletion user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	return userID, email
}

func createAccountDeletionWorkspace(t *testing.T, userID string, withSecondOwner bool) (workspaceID, memberID string) {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, issue_prefix)
		VALUES ($1, $2, $3)
		RETURNING id
	`, fmt.Sprintf("Delete Account %d", suffix), fmt.Sprintf("delete-account-%d", suffix), "DEL").Scan(&workspaceID); err != nil {
		t.Fatalf("create account deletion workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
	})

	if err := testPool.QueryRow(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')
		RETURNING id
	`, workspaceID, userID).Scan(&memberID); err != nil {
		t.Fatalf("create account deletion membership: %v", err)
	}
	if withSecondOwner {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO member (workspace_id, user_id, role)
			VALUES ($1, $2, 'owner')
		`, workspaceID, testUserID); err != nil {
			t.Fatalf("create second owner: %v", err)
		}
	}
	return workspaceID, memberID
}

func TestDeleteMeRequiresExactEmailConfirmation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	userID, email := createAccountDeletionUser(t)
	w := httptest.NewRecorder()
	testHandler.DeleteMe(w, newRequestAs(userID, http.MethodDelete, "/api/me", map[string]string{
		"confirmation": "wrong-" + email,
	}))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("DeleteMe: expected 400, got %d: %s", w.Code, w.Body.String())
	}
	var count int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM "user" WHERE id = $1`, userID).Scan(&count); err != nil {
		t.Fatalf("count user after rejected deletion: %v", err)
	}
	if count != 1 {
		t.Fatal("account was deleted despite incorrect confirmation")
	}
}

func TestDeleteMeBlocksSoleWorkspaceOwner(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	userID, email := createAccountDeletionUser(t)
	workspaceID, _ := createAccountDeletionWorkspace(t, userID, false)
	w := httptest.NewRecorder()
	testHandler.DeleteMe(w, newRequestAs(userID, http.MethodDelete, "/api/me", map[string]string{
		"confirmation": email,
	}))

	if w.Code != http.StatusConflict {
		t.Fatalf("DeleteMe: expected 409, got %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Code       string                    `json:"code"`
		Workspaces []accountWorkspaceSummary `json:"workspaces"`
	}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode blocked response: %v", err)
	}
	if response.Code != accountOwnsWorkspacesCode || len(response.Workspaces) != 1 || response.Workspaces[0].ID != workspaceID {
		t.Fatalf("unexpected blocked response: %+v", response)
	}

	var membershipCount int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM member WHERE workspace_id = $1 AND user_id = $2`, workspaceID, userID).Scan(&membershipCount); err != nil {
		t.Fatalf("count membership after blocked deletion: %v", err)
	}
	if membershipCount != 1 {
		t.Fatal("ownership failure partially removed the membership")
	}
}

func TestDeleteMeAtomicallyRevokesMembershipAndPersonalRuntime(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	userID, email := createAccountDeletionUser(t)
	workspaceID, _ := createAccountDeletionWorkspace(t, userID, true)

	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, name, runtime_mode, provider, status, device_info, metadata, owner_id
		)
		VALUES ($1, 'Delete Account Runtime', 'cloud', 'delete_account_test', 'online', '', '{}'::jsonb, $2)
		RETURNING id
	`, workspaceID, userID).Scan(&runtimeID); err != nil {
		t.Fatalf("create owned runtime: %v", err)
	}

	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args
		)
		VALUES ($1, 'Delete Account Agent', '', 'cloud', '{}'::jsonb,
			$2, 'private', 1, $3, '', '{}'::jsonb, '[]'::jsonb)
		RETURNING id
	`, workspaceID, runtimeID, userID).Scan(&agentID); err != nil {
		t.Fatalf("create owned agent: %v", err)
	}

	var skillID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO skill (workspace_id, name, description, content, created_by)
		VALUES ($1, $2, '', '', $3)
		RETURNING id
	`, workspaceID, "delete-account-skill-"+agentID, userID).Scan(&skillID); err != nil {
		t.Fatalf("create authored skill: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO personal_access_token (user_id, name, token_hash, token_prefix)
		VALUES ($1, 'Delete Account PAT', $2, 'mul_delete')
	`, userID, "delete-account-hash-"+agentID); err != nil {
		t.Fatalf("create personal access token: %v", err)
	}

	w := httptest.NewRecorder()
	testHandler.DeleteMe(w, newRequestAs(userID, http.MethodDelete, "/api/me", map[string]string{
		"confirmation": email,
	}))
	if w.Code != http.StatusNoContent {
		t.Fatalf("DeleteMe: expected 204, got %d: %s", w.Code, w.Body.String())
	}

	var userCount, memberCount, workspaceCount, patCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM "user" WHERE id = $1`, userID).Scan(&userCount); err != nil {
		t.Fatalf("count deleted user: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM member WHERE workspace_id = $1 AND user_id = $2`, workspaceID, userID).Scan(&memberCount); err != nil {
		t.Fatalf("count deleted membership: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM workspace WHERE id = $1`, workspaceID).Scan(&workspaceCount); err != nil {
		t.Fatalf("count preserved workspace: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM personal_access_token WHERE user_id = $1`, userID).Scan(&patCount); err != nil {
		t.Fatalf("count revoked personal access tokens: %v", err)
	}
	if userCount != 0 || memberCount != 0 || workspaceCount != 1 || patCount != 0 {
		t.Fatalf("unexpected deletion state: users=%d memberships=%d workspaces=%d pats=%d", userCount, memberCount, workspaceCount, patCount)
	}

	var runtimeOwner pgtype.UUID
	var runtimeStatus string
	if err := testPool.QueryRow(ctx, `SELECT owner_id, status FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&runtimeOwner, &runtimeStatus); err != nil {
		t.Fatalf("load revoked runtime: %v", err)
	}
	if runtimeOwner.Valid || runtimeStatus != "offline" {
		t.Fatalf("runtime not safely revoked: owner=%v status=%q", runtimeOwner, runtimeStatus)
	}

	var agentOwner, archivedBy pgtype.UUID
	var archivedAt pgtype.Timestamptz
	if err := testPool.QueryRow(ctx, `SELECT owner_id, archived_at, archived_by FROM agent WHERE id = $1`, agentID).Scan(&agentOwner, &archivedAt, &archivedBy); err != nil {
		t.Fatalf("load archived agent: %v", err)
	}
	if agentOwner.Valid || !archivedAt.Valid || archivedBy.Valid {
		t.Fatalf("agent not safely archived/anonymized: owner=%v archived_at=%v archived_by=%v", agentOwner, archivedAt, archivedBy)
	}

	var skillCreator pgtype.UUID
	if err := testPool.QueryRow(ctx, `SELECT created_by FROM skill WHERE id = $1`, skillID).Scan(&skillCreator); err != nil {
		t.Fatalf("load preserved skill: %v", err)
	}
	if skillCreator.Valid {
		t.Fatalf("skill creator was not anonymized: %v", skillCreator)
	}

	cookies := w.Result().Cookies()
	cleared := map[string]bool{}
	for _, cookie := range cookies {
		if cookie.MaxAge < 0 {
			cleared[cookie.Name] = true
		}
	}
	if !cleared["agora_auth"] || !cleared["agora_csrf"] {
		t.Fatalf("auth cookies were not cleared: %+v", cleared)
	}
}

func TestDeleteMeRejectsMachineActor(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	userID, email := createAccountDeletionUser(t)
	req := newRequestAs(userID, http.MethodDelete, "/api/me", map[string]string{"confirmation": email})
	req.Header.Set("X-Actor-Source", "task_token")
	w := httptest.NewRecorder()
	RequireHumanActor(http.HandlerFunc(testHandler.DeleteMe)).ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("machine actor: expected 403, got %d: %s", w.Code, w.Body.String())
	}

	var count int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM "user" WHERE id = $1`, userID).Scan(&count); err != nil {
		t.Fatalf("count user after machine rejection: %v", err)
	}
	if count != 1 {
		t.Fatal("machine actor deleted the account")
	}
}
