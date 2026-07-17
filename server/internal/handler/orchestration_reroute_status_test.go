package handler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestReroutePendingOrchestrationStepCoversWaitingApproval pins which statuses
// a reroute may act on.
//
// This existed as untested behavior and drifted: the checked-in generated code
// ran `status IN ('pending', 'waiting_approval')` while pkg/db/queries declared
// `status = 'pending'`, so the next `make sqlc` would have silently stopped
// rerouting approval-gated steps. Nothing failed, because no test covered it.
//
// A step reaches waiting_approval from either side of a run —
// WaitOrchestrationStepApproval parks a pending step before it dispatches, and
// CompleteOrchestrationStep parks an approval_required step that reported
// completion — but both release the same way: ApproveOrchestrationStep sets
// status back to 'pending' with agent_id = COALESCE(agent_id,
// controller_agent_id). So a waiting_approval step's agent_id governs the run
// that FOLLOWS approval, which is what makes rerouting it meaningful. Reroute
// is also the one plan edit not restricted to draft runs, and a live run is
// precisely what pauses at an approval gate.
func TestReroutePendingOrchestrationStepCoversWaitingApproval(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	fromAgentID := createHandlerTestAgent(t, "Reroute source agent", []byte("[]"))
	toAgentID := createHandlerTestAgent(t, "Reroute target agent", []byte("[]"))

	issueID := createRerouteTestIssue(t, ctx)
	runID := createRerouteTestRun(t, ctx, issueID)

	cases := []struct {
		status       string
		wantRerouted bool
	}{
		// A run still lies ahead of these, and agent_id decides who performs
		// it: reroute is the whole point.
		{"pending", true},
		{"waiting_approval", true},
		// Already in flight, or done: the agent is committed, and swapping it
		// under a live task would strand the work.
		{"queued", false},
		{"running", false},
		{"completed", false},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			stepID := createRerouteTestStep(t, ctx, runID, fromAgentID, tc.status)

			_, err := testHandler.Queries.ReroutePendingOrchestrationStep(ctx, db.ReroutePendingOrchestrationStepParams{
				ID:            util.MustParseUUID(stepID),
				AgentID:       util.MustParseUUID(toAgentID),
				ModelOverride: pgtype.Text{},
				Instructions:  "rerouted",
			})

			var gotAgentID string
			if scanErr := testPool.QueryRow(ctx,
				`SELECT agent_id::text FROM orchestration_step WHERE id = $1`, stepID,
			).Scan(&gotAgentID); scanErr != nil {
				t.Fatalf("read back step: %v", scanErr)
			}

			if tc.wantRerouted {
				if err != nil {
					t.Fatalf("reroute of a %s step failed: %v", tc.status, err)
				}
				if gotAgentID != toAgentID {
					t.Errorf("agent_id = %s, want %s — a %s step must be reroutable", gotAgentID, toAgentID, tc.status)
				}
				return
			}
			// The query is :one, so matching zero rows surfaces as an error.
			if err == nil {
				t.Errorf("reroute of a %s step succeeded; want no-op", tc.status)
			}
			if gotAgentID != fromAgentID {
				t.Errorf("agent_id = %s, want unchanged %s — a %s step must not be rerouted", gotAgentID, fromAgentID, tc.status)
			}
		})
	}
}

func createRerouteTestIssue(t *testing.T, ctx context.Context) string {
	t.Helper()
	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number)
		VALUES ($1, $2, 'todo', 'none', 'member', $3, $4)
		RETURNING id
	`, testWorkspaceID, "Reroute status coverage", testUserID, time.Now().UnixNano()%1000000).Scan(&issueID); err != nil {
		t.Fatalf("insert issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })
	return issueID
}

func createRerouteTestRun(t *testing.T, ctx context.Context, issueID string) string {
	t.Helper()
	var runID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO orchestration_run (workspace_id, issue_id, status, mode, created_by)
		VALUES ($1, $2, 'running', 'auto', $3)
		RETURNING id
	`, testWorkspaceID, issueID, testUserID).Scan(&runID); err != nil {
		t.Fatalf("insert orchestration run: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM orchestration_run WHERE id = $1`, runID) })
	return runID
}

func createRerouteTestStep(t *testing.T, ctx context.Context, runID, agentID, status string) string {
	t.Helper()
	var stepID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO orchestration_step (run_id, step_key, title, stage, position, status, agent_id)
		VALUES ($1, $2, 'Reroute status step', 'dev', 1, $3, $4)
		RETURNING id
	`, runID, fmt.Sprintf("dev-%s-%d", status, time.Now().UnixNano()), status, agentID).Scan(&stepID); err != nil {
		t.Fatalf("insert orchestration step (%s): %v", status, err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM orchestration_step WHERE id = $1`, stepID) })
	return stepID
}
