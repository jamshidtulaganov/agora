package handler

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/internal/config"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestParseProjectConfigOverrides(t *testing.T) {
	cases := []struct {
		name string
		json string
		want map[string]string
	}{
		{"empty", "", nil},
		{"no config object", `{"qa_smoke_url":"https://x"}`, nil},
		{"scoped bool string", `{"config":{"AGORA_QA_FAIL_AUTOROUTE_ENABLED":"false"}}`, map[string]string{"AGORA_QA_FAIL_AUTOROUTE_ENABLED": "false"}},
		{"drops non-scoped key", `{"config":{"AGORA_SPRINT_PR_MODE":"true","AGORA_AUTO_QA_ENABLED":"true"}}`, map[string]string{"AGORA_AUTO_QA_ENABLED": "true"}},
		{"tolerates bare bool", `{"config":{"AGORA_AUTO_QA_ENABLED":true}}`, map[string]string{"AGORA_AUTO_QA_ENABLED": "true"}},
		{"malformed config", `{"config": 3}`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseProjectConfigOverrides([]byte(c.json))
			if len(got) != len(c.want) {
				t.Fatalf("len mismatch: got %v want %v", got, c.want)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("%s: got %q want %q", k, got[k], v)
				}
			}
		})
	}
}

// TestProjectConfigRoundTrip proves the whole per-project override path: a
// project.settings.config override is stored, parsed back, and WINS over the
// instance value (autoroute defaults ON at the instance; this project turns it
// OFF). Reset reverts to the instance value.
func TestProjectConfigRoundTrip(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	const key = "AGORA_QA_FAIL_AUTOROUTE_ENABLED"

	var pid string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO project (workspace_id, title, status, priority)
		 VALUES ($1, 'proj-config-test', 'in_progress', 'none') RETURNING id`,
		testWorkspaceID).Scan(&pid); err != nil {
		t.Fatalf("create project: %v", err)
	}

	if _, err := testHandler.Queries.SetProjectConfigKey(ctx, db.SetProjectConfigKeyParams{
		ID: testUUID(pid), WorkspaceID: testUUID(testWorkspaceID), Key: key, Value: "false",
	}); err != nil {
		t.Fatalf("set project config: %v", err)
	}

	proj, err := testHandler.Queries.GetProject(ctx, testUUID(pid))
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	ov := parseProjectConfigOverrides(proj.Settings)
	if ov[key] != "false" {
		t.Fatalf("override not stored: %v", ov)
	}
	// Instance default is true (registry); the project override must win.
	t.Setenv(key, "")
	if config.BoolFrom(ov, key) {
		t.Error("project override false must win over instance default true")
	}
	if config.SourceFrom(ov, key) != "project" {
		t.Errorf("source: got %q want project", config.SourceFrom(ov, key))
	}

	// Reset reverts to the instance value.
	if _, err := testHandler.Queries.DeleteProjectConfigKey(ctx, db.DeleteProjectConfigKeyParams{
		ID: testUUID(pid), WorkspaceID: testUUID(testWorkspaceID), Key: key,
	}); err != nil {
		t.Fatalf("delete project config: %v", err)
	}
	proj, _ = testHandler.Queries.GetProject(ctx, testUUID(pid))
	if parseProjectConfigOverrides(proj.Settings) != nil {
		t.Error("override should be gone after reset")
	}
}
