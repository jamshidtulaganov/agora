package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// This file pins the WS1 PR-A security contract for local_directory project
// resources (local-dev-mode v2, Phase 0a):
//
//  1. local_directory mutations are human-only. A machine credential (mat_
//     task token, mcn_ cloud-node PAT) gets 403 on create, retarget, and
//     delete — on both the standalone resource endpoints AND the bundled
//     project-create path. github_repo stays machine-creatable (agent repo
//     bootstrap is a supported flow).
//  2. The ref's daemon_id must resolve to a registered runtime in the
//     workspace (else 400) that the caller is allowed to use per
//     canUseRuntimeForAgent — owner, workspace owner/admin, or
//     visibility=public (else 403).

// createHandlerTestDaemonRuntime seeds an agent_runtime row keyed on the
// given daemon_id so local_directory refs targeting it clear the daemon
// ownership gate. ownerID may be "" for an unowned row. Cleaned up with the
// test.
func createHandlerTestDaemonRuntime(t *testing.T, daemonID, provider, ownerID, visibility string) string {
	t.Helper()

	var ownerArg any
	if ownerID != "" {
		ownerArg = ownerID
	}
	var id string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, owner_id, visibility, last_seen_at
		)
		VALUES ($1, $2, $3, 'local', $4, 'online', '', '{}'::jsonb, $5, $6, now())
		RETURNING id
	`, testWorkspaceID, daemonID, "Daemon "+daemonID, provider, ownerArg, visibility).Scan(&id); err != nil {
		t.Fatalf("failed to seed daemon runtime %s: %v", daemonID, err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, id)
	})
	return id
}

// createHandlerTestMember seeds a second user with the given workspace role
// and returns its user ID. Used to exercise the non-admin branches of
// canUseRuntimeForAgent (the fixture user is a workspace owner, for whom the
// role override always passes).
func createHandlerTestMember(t *testing.T, email, role string) string {
	t.Helper()

	var userID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, "Resource Security "+role, email).Scan(&userID); err != nil {
		t.Fatalf("failed to seed user %s: %v", email, err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, $3)
	`, testWorkspaceID, userID, role); err != nil {
		t.Fatalf("failed to seed member %s: %v", email, err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM member WHERE workspace_id = $1 AND user_id = $2`, testWorkspaceID, userID)
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	return userID
}

func createSecurityTestProject(t *testing.T, title string) ProjectResponse {
	t.Helper()

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": title,
	})
	testHandler.CreateProject(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateProject(%s): %d %s", title, w.Code, w.Body.String())
	}
	var p ProjectResponse
	if err := json.NewDecoder(w.Body).Decode(&p); err != nil {
		t.Fatalf("decode CreateProject: %v", err)
	}
	t.Cleanup(func() {
		r := newRequest("DELETE", "/api/projects/"+p.ID, nil)
		r = withURLParam(r, "id", p.ID)
		testHandler.DeleteProject(httptest.NewRecorder(), r)
	})
	return p
}

// attachLocalDirectory creates a local_directory resource as the fixture
// (human, workspace-owner) user and returns it. Callers seed the daemon
// runtime themselves.
func attachLocalDirectory(t *testing.T, projectID, localPath, daemonID string) ProjectResourceResponse {
	t.Helper()

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/projects/"+projectID+"/resources", map[string]any{
		"resource_type": "local_directory",
		"resource_ref": map[string]any{
			"local_path": localPath,
			"daemon_id":  daemonID,
		},
	})
	req = withURLParam(req, "id", projectID)
	testHandler.CreateProjectResource(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("attachLocalDirectory: %d %s", w.Code, w.Body.String())
	}
	var created ProjectResourceResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode attachLocalDirectory: %v", err)
	}
	return created
}

// TestLocalDirectoryResourceMachineActorForbidden walks the machine-actor
// matrix: every local_directory mutation by a task_token or cloud_pat actor
// must 403 before any DB write, while github_repo creation by the same actor
// keeps working (agent bootstrap non-regression).
func TestLocalDirectoryResourceMachineActorForbidden(t *testing.T) {
	project := createSecurityTestProject(t, "Machine actor gate project")
	createHandlerTestDaemonRuntime(t, "d-machine-gate", "claude_code", testUserID, "private")
	createHandlerTestDaemonRuntime(t, "d-machine-gate-2", "claude_code", testUserID, "private")

	for _, actorSource := range []string{"task_token", "cloud_pat"} {
		t.Run("POST local_directory "+actorSource, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := newRequest("POST", "/api/projects/"+project.ID+"/resources", map[string]any{
				"resource_type": "local_directory",
				"resource_ref": map[string]any{
					"local_path": "/Users/foo/work/machine",
					"daemon_id":  "d-machine-gate",
				},
			})
			req.Header.Set("X-Actor-Source", actorSource)
			req = withURLParam(req, "id", project.ID)
			testHandler.CreateProjectResource(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
			}
		})
	}

	// github_repo by a machine actor must still succeed — the first-repo
	// bootstrap flow (KB + QA-manifest builds) is agent-driven.
	t.Run("POST github_repo task_token non-regression", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/projects/"+project.ID+"/resources", map[string]any{
			"resource_type": "github_repo",
			"resource_ref":  map[string]any{"url": "https://github.com/agora-ai/machine-bootstrap"},
		})
		req.Header.Set("X-Actor-Source", "task_token")
		req = withURLParam(req, "id", project.ID)
		testHandler.CreateProjectResource(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}
	})

	// Seed a row as a human so PUT/DELETE have a target.
	created := attachLocalDirectory(t, project.ID, "/Users/foo/work/machine", "d-machine-gate")

	t.Run("PUT retarget by machine actor", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := newRequest("PUT", "/api/projects/"+project.ID+"/resources/"+created.ID, map[string]any{
			"resource_ref": map[string]any{
				"local_path": "/Users/foo/work/elsewhere",
				"daemon_id":  "d-machine-gate-2",
			},
		})
		req.Header.Set("X-Actor-Source", "task_token")
		req = withURLParams(req, "id", project.ID, "resourceId", created.ID)
		testHandler.UpdateProjectResource(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("DELETE by machine actor", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := newRequest("DELETE", "/api/projects/"+project.ID+"/resources/"+created.ID, nil)
		req.Header.Set("X-Actor-Source", "cloud_pat")
		req = withURLParams(req, "id", project.ID, "resourceId", created.ID)
		testHandler.DeleteProjectResource(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
		}
		// The row must survive the rejected delete.
		lw := httptest.NewRecorder()
		lreq := newRequest("GET", "/api/projects/"+project.ID+"/resources", nil)
		lreq = withURLParam(lreq, "id", project.ID)
		testHandler.ListProjectResources(lw, lreq)
		var list struct {
			Resources []ProjectResourceResponse `json:"resources"`
		}
		if err := json.NewDecoder(lw.Body).Decode(&list); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		found := false
		for _, res := range list.Resources {
			if res.ID == created.ID {
				found = true
			}
		}
		if !found {
			t.Fatal("local_directory row deleted despite 403")
		}
	})
}

// TestCreateProjectBundledLocalDirectoryMachineActorForbidden pins the
// easiest bypass route: bundling a local_directory into POST /api/projects.
// A machine actor must get a 403 naming the offending index, and no project
// may survive. A github_repo-only bundle by the same actor stays allowed.
func TestCreateProjectBundledLocalDirectoryMachineActorForbidden(t *testing.T) {
	createHandlerTestDaemonRuntime(t, "d-bundle-machine", "claude_code", testUserID, "private")

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Machine bundled local_directory",
		"resources": []map[string]any{
			{
				"resource_type": "github_repo",
				"resource_ref":  map[string]any{"url": "https://github.com/agora-ai/bundled-gate"},
			},
			{
				"resource_type": "local_directory",
				"resource_ref": map[string]any{
					"local_path": "/Users/foo/work/bundled",
					"daemon_id":  "d-bundle-machine",
				},
			},
		},
	})
	req.Header.Set("X-Actor-Source", "task_token")
	testHandler.CreateProject(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("bundled local_directory by machine actor: expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "resources[1]") {
		t.Errorf("403 body should name the offending index, got: %s", body)
	}

	// No project row may survive the rejection.
	w = httptest.NewRecorder()
	req = newRequest("GET", "/api/projects?workspace_id="+testWorkspaceID, nil)
	testHandler.ListProjects(w, req)
	var list struct {
		Projects []ProjectResponse `json:"projects"`
	}
	if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
		t.Fatalf("decode ListProjects: %v", err)
	}
	for _, p := range list.Projects {
		if p.Title == "Machine bundled local_directory" {
			t.Errorf("project survived rejected bundled create: %s", p.ID)
		}
	}

	// github_repo-only bundle by a machine actor stays allowed.
	w = httptest.NewRecorder()
	req = newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Machine bundled github_repo only",
		"resources": []map[string]any{
			{
				"resource_type": "github_repo",
				"resource_ref":  map[string]any{"url": "https://github.com/agora-ai/bundled-ok"},
			},
		},
	})
	req.Header.Set("X-Actor-Source", "task_token")
	testHandler.CreateProject(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("github_repo-only bundle by machine actor: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	r := newRequest("DELETE", "/api/projects/"+resp.ID, nil)
	r = withURLParam(r, "id", resp.ID)
	testHandler.DeleteProject(httptest.NewRecorder(), r)
}

// TestLocalDirectoryDaemonOwnershipBinding walks the daemon_id ownership
// matrix on the standalone create endpoint and the retargeting update.
func TestLocalDirectoryDaemonOwnershipBinding(t *testing.T) {
	otherUserID := createHandlerTestMember(t, "resource-security-member@agora.dev", "member")

	postLocalDir := func(asUserID, projectID, path, daemonID string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/projects/"+projectID+"/resources", map[string]any{
			"resource_type": "local_directory",
			"resource_ref": map[string]any{
				"local_path": path,
				"daemon_id":  daemonID,
			},
		})
		req.Header.Set("X-User-ID", asUserID)
		req = withURLParam(req, "id", projectID)
		testHandler.CreateProjectResource(w, req)
		return w
	}

	t.Run("unknown daemon_id is 400", func(t *testing.T) {
		project := createSecurityTestProject(t, "Daemon binding unknown")
		w := postLocalDir(testUserID, project.ID, "/Users/foo/work/x", "d-never-registered")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("other member's private runtime is 403", func(t *testing.T) {
		project := createSecurityTestProject(t, "Daemon binding foreign private")
		// Runtime owned by the fixture (owner-role) user; the request
		// comes from the plain member, who is neither the owner nor an
		// admin.
		createHandlerTestDaemonRuntime(t, "d-foreign-private", "claude_code", testUserID, "private")
		w := postLocalDir(otherUserID, project.ID, "/Users/foo/work/x", "d-foreign-private")
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("runtime owner succeeds", func(t *testing.T) {
		project := createSecurityTestProject(t, "Daemon binding owner")
		createHandlerTestDaemonRuntime(t, "d-owned-by-member", "claude_code", otherUserID, "private")
		w := postLocalDir(otherUserID, project.ID, "/Users/foo/work/x", "d-owned-by-member")
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("workspace admin override on foreign private runtime", func(t *testing.T) {
		project := createSecurityTestProject(t, "Daemon binding admin override")
		// Runtime owned by the plain member; the fixture user's role is
		// 'owner', which passes the same roleAllowed(owner, admin) check
		// the admin override uses.
		createHandlerTestDaemonRuntime(t, "d-member-private", "claude_code", otherUserID, "private")
		w := postLocalDir(testUserID, project.ID, "/Users/foo/work/x", "d-member-private")
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("public runtime usable by non-owner member", func(t *testing.T) {
		project := createSecurityTestProject(t, "Daemon binding public")
		createHandlerTestDaemonRuntime(t, "d-public", "claude_code", testUserID, "public")
		w := postLocalDir(otherUserID, project.ID, "/Users/foo/work/x", "d-public")
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("PUT retarget to unowned daemon is 403", func(t *testing.T) {
		project := createSecurityTestProject(t, "Daemon binding retarget")
		createHandlerTestDaemonRuntime(t, "d-retarget-own", "claude_code", otherUserID, "private")
		createHandlerTestDaemonRuntime(t, "d-retarget-foreign", "claude_code", testUserID, "private")

		w := postLocalDir(otherUserID, project.ID, "/Users/foo/work/x", "d-retarget-own")
		if w.Code != http.StatusCreated {
			t.Fatalf("seed create: %d %s", w.Code, w.Body.String())
		}
		var created ProjectResourceResponse
		if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
			t.Fatalf("decode: %v", err)
		}

		uw := httptest.NewRecorder()
		ureq := newRequest("PUT", "/api/projects/"+project.ID+"/resources/"+created.ID, map[string]any{
			"resource_ref": map[string]any{
				"local_path": "/Users/foo/work/x",
				"daemon_id":  "d-retarget-foreign",
			},
		})
		ureq.Header.Set("X-User-ID", otherUserID)
		ureq = withURLParams(ureq, "id", project.ID, "resourceId", created.ID)
		testHandler.UpdateProjectResource(uw, ureq)
		if uw.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d: %s", uw.Code, uw.Body.String())
		}
	})

	t.Run("multi-provider daemon passes when any row is accessible", func(t *testing.T) {
		project := createSecurityTestProject(t, "Daemon binding multi provider")
		// Same daemon_id registered under two providers: one row owned by
		// the fixture user (inaccessible to the plain member), one owned
		// by the member. Any accessible row grants the binding.
		createHandlerTestDaemonRuntime(t, "d-multi", "claude_code", testUserID, "private")
		createHandlerTestDaemonRuntime(t, "d-multi", "codex", otherUserID, "private")
		w := postLocalDir(otherUserID, project.ID, "/Users/foo/work/x", "d-multi")
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("bundled create validates daemon ownership too", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
			"title": "Bundled unknown daemon",
			"resources": []map[string]any{
				{
					"resource_type": "local_directory",
					"resource_ref": map[string]any{
						"local_path": "/Users/foo/work/x",
						"daemon_id":  "d-bundled-unknown",
					},
				},
			},
		})
		testHandler.CreateProject(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}

		// Foreign private daemon in a bundle → 403 for a plain member.
		createHandlerTestDaemonRuntime(t, "d-bundled-foreign", "claude_code", testUserID, "private")
		w = httptest.NewRecorder()
		req = newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
			"title": "Bundled foreign daemon",
			"resources": []map[string]any{
				{
					"resource_type": "local_directory",
					"resource_ref": map[string]any{
						"local_path": "/Users/foo/work/x",
						"daemon_id":  "d-bundled-foreign",
					},
				},
			},
		})
		req.Header.Set("X-User-ID", otherUserID)
		testHandler.CreateProject(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
		}
	})
}

// TestIsMachineActor pins the exported helper's classification: exactly the
// two machine credential markers return true; the human shapes (empty) and
// unknown future values return false — mirroring RequireHumanActor's
// deliberate denylist semantics.
func TestIsMachineActor(t *testing.T) {
	cases := []struct {
		name        string
		actorSource string
		want        bool
	}{
		{"task_token", "task_token", true},
		{"cloud_pat", "cloud_pat", true},
		{"human (no header)", "", false},
		{"unknown future kind", "future_kind", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/projects/x/resources", nil)
			if tc.actorSource != "" {
				req.Header.Set("X-Actor-Source", tc.actorSource)
			}
			if got := IsMachineActor(req); got != tc.want {
				t.Fatalf("IsMachineActor(%q) = %v, want %v", tc.actorSource, got, tc.want)
			}
		})
	}
}
