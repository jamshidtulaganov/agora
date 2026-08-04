package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/service"
)

// KB-skill re-splice + canManageSkill tests. These exercise the server-side
// suspenders of §4.4: a whole-content PUT to a kb_managed skill must
// re-append the machine-managed region (decided from the PRE-update row so a
// same-request config wipe cannot disarm the guard), and a member must be able
// to update a server-created KB skill whose CreatedBy is NULL.

const kbItemsBeginMarkerTest = "<!-- agora:kb:items:begin"
const kbItemsEndMarkerTest = "<!-- agora:kb:items:end -->"

// kbSeedManagedSkill creates a server-style KB skill (CreatedBy NULL,
// kb_managed config) with one active knowledge_item behind it so a recompile
// produces a non-empty region. Returns (skillID, kbName).
func kbSeedManagedSkill(t *testing.T, itemTitle string, stampConfig bool) (string, string) {
	t.Helper()
	ctx := context.Background()
	kbName := fmt.Sprintf("kb-resplice-%d", time.Now().UnixNano())
	projectID := kbTestProject(t, fmt.Sprintf("KB Resplice Project %d", time.Now().UnixNano()))
	kbInsertItem(t, projectID, kbName, "gotcha", itemTitle, strings.ToLower(itemTitle), "active")

	config := []byte(`{}`)
	if stampConfig {
		config = []byte(`{"kb_managed": true, "kb_name": "` + kbName + `"}`)
	}
	var skillID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO skill (workspace_id, name, description, content, config, created_by)
		VALUES ($1, $2, 'KB skill', '', $3::jsonb, NULL)
		RETURNING id
	`, testWorkspaceID, kbName, string(config)).Scan(&skillID); err != nil {
		t.Fatalf("seed managed skill: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM skill WHERE id = $1`, skillID)
	})
	// Prime the managed region so the round-trip starts from a compiled state.
	testHandler.TaskService.RecompileKB(ctx, parseUUID(testWorkspaceID), kbName)
	return skillID, kbName
}

func kbSkillContent(t *testing.T, skillID string) string {
	t.Helper()
	var content string
	if err := testPool.QueryRow(context.Background(), `SELECT content FROM skill WHERE id = $1`, skillID).Scan(&content); err != nil {
		t.Fatalf("read skill content: %v", err)
	}
	return content
}

func kbSkillConfig(t *testing.T, skillID string) []byte {
	t.Helper()
	var config []byte
	if err := testPool.QueryRow(context.Background(), `SELECT config FROM skill WHERE id = $1`, skillID).Scan(&config); err != nil {
		t.Fatalf("read skill config: %v", err)
	}
	return config
}

func TestUpdateSkillResplicesManagedRegion(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	// Case 1: CLI-style whole-content PUT that carries markers but empties the
	// region between them — the re-splice must restore the compiled region.
	t.Run("stamped_content_write_resplices", func(t *testing.T) {
		skillID, _ := kbSeedManagedSkill(t, "Resplice fact one", true)
		// A whole-content write that drops the region body (keeps only the
		// markers, as a naive CLI round-trip might).
		newContent := "Human preamble\n\n" + kbItemsBeginMarkerTest + " — auto-compiled by the server; do not edit between markers -->\n" + kbItemsEndMarkerTest
		w := httptest.NewRecorder()
		req := newRequest(http.MethodPut, "/api/skills/"+skillID, UpdateSkillRequest{Content: strptr(newContent)})
		req = withURLParam(req, "id", skillID)
		testHandler.UpdateSkill(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("UpdateSkill: expected 200, got %d: %s", w.Code, w.Body.String())
		}
		content := kbSkillContent(t, skillID)
		if !strings.Contains(content, "Resplice fact one") {
			t.Fatalf("re-splice must restore the compiled item into the region; content=\n%s", content)
		}
		if !strings.Contains(content, "Human preamble") {
			t.Fatalf("re-splice must preserve content outside the markers; content=\n%s", content)
		}
	})

	// Case 2: config replaced in the same request (kb_managed stamp WIPED) still
	// re-splices — the guard reads the PRE-update row. Then the recompile
	// re-stamps kb_managed via the merge query.
	t.Run("config_wipe_same_request_still_resplices", func(t *testing.T) {
		skillID, _ := kbSeedManagedSkill(t, "Resplice fact two", true)
		w := httptest.NewRecorder()
		req := newRequest(http.MethodPut, "/api/skills/"+skillID, UpdateSkillRequest{
			Content: strptr("Rewritten by CLI with no markers"),
			Config:  map[string]any{"unrelated": true}, // wipes kb_managed in this write
		})
		req = withURLParam(req, "id", skillID)
		testHandler.UpdateSkill(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("UpdateSkill: expected 200, got %d: %s", w.Code, w.Body.String())
		}
		content := kbSkillContent(t, skillID)
		if !strings.Contains(content, "Resplice fact two") {
			t.Fatalf("PRE-update-row guard must re-splice even when the same request wiped the stamp; content=\n%s", content)
		}
		// The recompile re-stamps kb_managed.
		var cfg struct {
			KBManaged bool `json:"kb_managed"`
		}
		json.Unmarshal(kbSkillConfig(t, skillID), &cfg)
		if !cfg.KBManaged {
			t.Fatalf("recompile must re-stamp kb_managed after a config wipe; config=%s", kbSkillConfig(t, skillID))
		}
	})

	// Case 3: study-style whole-content PUT with NO markers → region appended
	// AND (for an unstamped-config fallback KB) the stamp is written by the
	// recompile. Seed with stampConfig=false to exercise the
	// project-name-resolves-to-this-skill fallback in skillIsKBManaged.
	t.Run("no_marker_write_appends_region_and_stamps", func(t *testing.T) {
		skillID, kbName := kbSeedManagedSkillForProject(t, "Resplice fact three")
		w := httptest.NewRecorder()
		req := newRequest(http.MethodPut, "/api/skills/"+skillID, UpdateSkillRequest{
			Content: strptr("Lead-agent study output, no managed markers here."),
		})
		req = withURLParam(req, "id", skillID)
		testHandler.UpdateSkill(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("UpdateSkill: expected 200, got %d: %s", w.Code, w.Body.String())
		}
		content := kbSkillContent(t, skillID)
		if !strings.Contains(content, kbItemsEndMarkerTest) {
			t.Fatalf("no-marker write must append the managed region; content=\n%s", content)
		}
		if !strings.Contains(content, "Resplice fact three") {
			t.Fatalf("appended region must contain the compiled item; content=\n%s", content)
		}
		if !strings.Contains(content, "Lead-agent study output") {
			t.Fatalf("study content outside the region must be preserved; content=\n%s", content)
		}
		var cfg struct {
			KBManaged bool `json:"kb_managed"`
		}
		json.Unmarshal(kbSkillConfig(t, skillID), &cfg)
		if !cfg.KBManaged {
			t.Fatalf("fallback KB skill must be stamped kb_managed after re-splice; config=%s", kbSkillConfig(t, skillID))
		}
		_ = kbName
	})
}

// kbSeedManagedSkillForProject creates a KB skill whose name matches a project's
// resolved ProjectKBSkillName but starts WITHOUT the kb_managed config stamp —
// so skillIsKBManaged must fall back to the project-name resolution. Returns
// (skillID, kbName).
func kbSeedManagedSkillForProject(t *testing.T, itemTitle string) (string, string) {
	t.Helper()
	ctx := context.Background()
	// A slug-safe title so ProjectKBSkillName derives a deterministic "<slug>-kb".
	projectTitle := fmt.Sprintf("kbresplicefallback%d", time.Now().UnixNano())
	projectID := kbTestProject(t, projectTitle)
	project, err := testHandler.Queries.GetProject(ctx, parseUUID(projectID))
	if err != nil {
		t.Fatalf("load project: %v", err)
	}
	kbName := service.ProjectKBSkillName(project)
	kbInsertItem(t, projectID, kbName, "gotcha", itemTitle, strings.ToLower(itemTitle), "active")

	var skillID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO skill (workspace_id, name, description, content, config, created_by)
		VALUES ($1, $2, 'legacy KB', 'legacy blob', '{}'::jsonb, NULL)
		RETURNING id
	`, testWorkspaceID, kbName).Scan(&skillID); err != nil {
		t.Fatalf("seed fallback KB skill: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM skill WHERE id = $1`, skillID)
	})
	return skillID, kbName
}

func TestMemberCanUpdateServerCreatedKBSkill(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	// A plain (non-admin) member: canManageSkill normally 403s a member on a
	// skill they did not create, but a server-created KB skill (CreatedBy NULL +
	// kb_managed) is workspace property any member may update.
	var memberUserID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, "KB Member", fmt.Sprintf("kb-member-%d@agora.dev", time.Now().UnixNano())).Scan(&memberUserID); err != nil {
		t.Fatalf("insert member user: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM "user" WHERE id = $1`, memberUserID) })
	if _, err := testPool.Exec(ctx,
		`INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')`,
		testWorkspaceID, memberUserID,
	); err != nil {
		t.Fatalf("insert member: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM member WHERE workspace_id = $1 AND user_id = $2`, testWorkspaceID, memberUserID)
	})

	skillID, _ := kbSeedManagedSkill(t, "Member-updatable fact", true)

	// Member-actor PUT (X-User-ID = the plain member).
	w := httptest.NewRecorder()
	body := UpdateSkillRequest{Content: strptr("Member edit to the KB skill preamble")}
	req := newRequestAsUser(memberUserID, http.MethodPut, "/api/skills/"+skillID, body)
	req = withURLParam(req, "id", skillID)
	testHandler.UpdateSkill(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("member update of server-created KB skill: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}
