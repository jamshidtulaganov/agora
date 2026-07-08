package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestLocalDirectoryRuntimeForProject(t *testing.T) {
	pool := knowledgeTestPool(t)
	q := db.New(pool)
	svc := NewTaskService(q, pool, nil, events.New())
	ctx := context.Background()

	wsID := seedKnowledgeWorkspace(t, pool)
	proj := seedKnowledgeProject(t, pool, q, wsID, "LocalDir Pin Proj", "")
	issue := db.Issue{WorkspaceID: wsID, ProjectID: proj.ID}

	daemonID := "pin-daemon-" + uuid.NewString()[:8]

	seedRuntime := func(status string) string {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO agent_runtime (
				workspace_id, daemon_id, name, runtime_mode, provider, status,
				device_info, metadata, visibility, last_seen_at
			) VALUES ($1, $2, $3, 'local', 'claude', $4, '', '{}'::jsonb, 'private', now())
			RETURNING id
		`, util.UUIDToString(wsID), daemonID, "Pin Daemon", status).Scan(&id); err != nil {
			t.Fatalf("seed runtime: %v", err)
		}
		t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, id) })
		return id
	}

	seedResource := func() {
		ref := `{"local_path":"/Users/dev/app","daemon_id":"` + daemonID + `"}`
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO project_resource (workspace_id, project_id, resource_type, resource_ref)
			VALUES ($1, $2, 'local_directory', $3::jsonb) RETURNING id
		`, util.UUIDToString(wsID), util.UUIDToString(proj.ID), ref).Scan(&id); err != nil {
			t.Fatalf("seed resource: %v", err)
		}
		t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM project_resource WHERE id = $1`, id) })
	}

	t.Run("no resource -> miss", func(t *testing.T) {
		if _, ok := svc.localDirectoryRuntimeForProject(ctx, issue); ok {
			t.Error("no local_directory resource should miss")
		}
	})

	t.Run("resource + online daemon -> hit", func(t *testing.T) {
		runtimeID := seedRuntime("online")
		seedResource()
		rt, ok := svc.localDirectoryRuntimeForProject(ctx, issue)
		if !ok {
			t.Fatal("online daemon with local_directory should resolve")
		}
		if util.UUIDToString(rt.ID) != runtimeID {
			t.Errorf("resolved runtime %s, want %s", util.UUIDToString(rt.ID), runtimeID)
		}
	})

	t.Run("resource + offline daemon -> miss", func(t *testing.T) {
		// Fresh project so the online-runtime subtest's rows don't bleed in.
		p2 := seedKnowledgeProject(t, pool, q, wsID, "Offline Pin Proj", "")
		issue2 := db.Issue{WorkspaceID: wsID, ProjectID: p2.ID}
		offlineDaemon := "pin-offline-" + uuid.NewString()[:8]
		if _, err := pool.Exec(ctx, `
			INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, visibility, last_seen_at)
			VALUES ($1, $2, 'Offline', 'local', 'claude', 'offline', '', '{}'::jsonb, 'private', now())
		`, util.UUIDToString(wsID), offlineDaemon); err != nil {
			t.Fatalf("seed offline runtime: %v", err)
		}
		ref := `{"local_path":"/Users/dev/app2","daemon_id":"` + offlineDaemon + `"}`
		if _, err := pool.Exec(ctx, `
			INSERT INTO project_resource (workspace_id, project_id, resource_type, resource_ref)
			VALUES ($1, $2, 'local_directory', $3::jsonb)
		`, util.UUIDToString(wsID), util.UUIDToString(p2.ID), ref); err != nil {
			t.Fatalf("seed resource: %v", err)
		}
		if _, ok := svc.localDirectoryRuntimeForProject(ctx, issue2); ok {
			t.Error("offline daemon must not resolve")
		}
	})
}
