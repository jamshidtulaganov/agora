package handler

import (
	"context"
	"testing"
)

// Project affinity: the live complaint was an SD Bridge engineer running QA on a
// Bitrix-project issue. These tests pin the new preference — a project that has
// bound a squad gets ITS agents on its issues; everything else keeps the
// workspace-wide pool.

// affinityFixture builds: a project bound to a squad with one ready agent, and a
// separate workspace-level "QA Squad" with its own ready agent. Returns
// (projectID, projectAgentID, qaAgentID).
func affinityFixture(t *testing.T, ctx context.Context, tag string) (string, string, string) {
	t.Helper()

	projectAgent := createHandlerTestAgent(t, "Project Agent "+tag, nil)
	qaAgent := createHandlerTestAgent(t, "QA Agent "+tag, nil)

	var squadID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
		VALUES ($1::uuid, $2, '', $3::uuid, $4::uuid) RETURNING id::text`,
		testWorkspaceID, "Affinity Dev Squad "+tag, projectAgent, testUserID).Scan(&squadID); err != nil {
		t.Fatalf("create project squad: %v", err)
	}
	var qaSquadID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, description, leader_id, creator_id)
		VALUES ($1::uuid, $2, '', $3::uuid, $4::uuid) RETURNING id::text`,
		testWorkspaceID, "QA Squad "+tag, qaAgent, testUserID).Scan(&qaSquadID); err != nil {
		t.Fatalf("create qa squad: %v", err)
	}
	var projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title, squad_id)
		VALUES ($1::uuid, $2, $3::uuid) RETURNING id::text`,
		testWorkspaceID, "Affinity Project "+tag, squadID).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		testPool.Exec(bg, `DELETE FROM project WHERE id = $1::uuid`, projectID)
		testPool.Exec(bg, `DELETE FROM squad WHERE id IN ($1::uuid, $2::uuid)`, squadID, qaSquadID)
	})
	return projectID, projectAgent, qaAgent
}

// TestQAAgentsForIssueProjectAffinity: an issue in a squad-bound project gets the
// PROJECT's agents; an issue without a project keeps the workspace QA pool.
func TestQAAgentsForIssueProjectAffinity(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	projectID, projectAgent, qaAgent := affinityFixture(t, ctx, "pool")

	issueID := sliceActionTestIssue(t, "", "")
	if _, err := testPool.Exec(ctx, `UPDATE issue SET project_id = $2::uuid WHERE id = $1::uuid`, issueID, projectID); err != nil {
		t.Fatalf("bind project: %v", err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	got := testHandler.qaAgentsForIssue(ctx, issue)
	if len(got) == 0 {
		t.Fatal("project-bound issue resolved no agents")
	}
	for _, a := range got {
		if uuidToString(a.ID) == qaAgent {
			t.Errorf("workspace QA agent selected despite the project's own squad being ready")
		}
	}
	found := false
	for _, a := range got {
		if uuidToString(a.ID) == projectAgent {
			found = true
		}
	}
	if !found {
		t.Error("the project squad's agent is missing from the pool")
	}

	// No project → the workspace pool, exactly as before.
	bare := sliceActionTestIssue(t, "", "")
	bareIssue, err := testHandler.Queries.GetIssue(ctx, testUUID(bare))
	if err != nil {
		t.Fatalf("load bare: %v", err)
	}
	pool := testHandler.qaAgentsForIssue(ctx, bareIssue)
	for _, a := range pool {
		if uuidToString(a.ID) == projectAgent {
			t.Error("a project squad's agent leaked into a no-project issue's pool")
		}
	}
}

// TestQAAgentsForIssueExcludesAuthor: when the pool falls through to the
// project's own squad, the issue's AUTHOR agent must not be in it — an agent
// never passes its own work.
func TestQAAgentsForIssueExcludesAuthor(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	projectID, projectAgent, _ := affinityFixture(t, ctx, "author")

	issueID := sliceActionTestIssue(t, "agent", projectAgent)
	if _, err := testPool.Exec(ctx, `UPDATE issue SET project_id = $2::uuid WHERE id = $1::uuid`, issueID, projectID); err != nil {
		t.Fatalf("bind project: %v", err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, a := range testHandler.qaAgentsForIssue(ctx, issue) {
		if uuidToString(a.ID) == projectAgent {
			t.Error("the author agent is in its own QA pool")
		}
	}
}

// TestResolveReviewerAgentPrefersProjectSquad: a member-assigned issue in a
// squad-bound project gets the PROJECT's leader as its reviewer, not the
// workspace QA leader from an unrelated project.
func TestResolveReviewerAgentPrefersProjectSquad(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	projectID, projectAgent, qaAgent := affinityFixture(t, ctx, "review")

	issueID := sliceActionTestIssue(t, "", "")
	if _, err := testPool.Exec(ctx, `UPDATE issue SET project_id = $2::uuid WHERE id = $1::uuid`, issueID, projectID); err != nil {
		t.Fatalf("bind project: %v", err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	reviewer, ok := testHandler.resolveReviewerAgent(ctx, issue)
	if !ok {
		t.Fatal("no reviewer resolved")
	}
	if uuidToString(reviewer.ID) == qaAgent {
		t.Errorf("reviewer = the workspace QA leader, want the project squad's agent")
	}
	if uuidToString(reviewer.ID) != projectAgent {
		t.Errorf("reviewer = %s, want the project squad leader %s", uuidToString(reviewer.ID), projectAgent)
	}
}
