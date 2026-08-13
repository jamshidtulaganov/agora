package handler

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

func issueForTaskLevel(title, description string) db.Issue {
	return db.Issue{Title: title, Description: pgtype.Text{String: description, Valid: description != ""}}
}

func TestResolveTaskExecutionLevelDefaultsToStandard(t *testing.T) {
	resolved := resolveTaskExecutionLevel(issueForTaskLevel("Fix empty state spacing", "Small visual correction."), "auto")
	if resolved.Resolved != "standard" || resolved.Score != 2 {
		t.Fatalf("resolved = %#v, want standard baseline", resolved)
	}
}

func TestResolveTaskExecutionLevelFindsCoordinatedScope(t *testing.T) {
	resolved := resolveTaskExecutionLevel(issueForTaskLevel(
		"Add authenticated uploads", "Implement frontend and backend support with a schema migration across multiple repos.",
	), "auto")
	if resolved.Resolved != "coordinated" {
		t.Fatalf("resolved = %#v, want coordinated", resolved)
	}
	if resolved.Score < 4 || len(resolved.Signals) < 2 {
		t.Fatalf("resolved = %#v, want auditable scope signals", resolved)
	}
}

func TestResolveTaskExecutionLevelEnforcesControlledSafetyFloor(t *testing.T) {
	resolved := resolveTaskExecutionLevel(issueForTaskLevel(
		"Rotate credentials", "Rotate production credentials and deploy to production.",
	), "direct")
	if resolved.Requested != "direct" || resolved.Resolved != "controlled" {
		t.Fatalf("resolved = %#v, want requested direct elevated to controlled", resolved)
	}
	if resolved.Signals[len(resolved.Signals)-1] != "safety_floor_applied" {
		t.Fatalf("signals = %#v, want safety floor audit signal", resolved.Signals)
	}
}

func TestAssistExecutionProducesPlanOnly(t *testing.T) {
	agentID := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	steps := assistOrchestrationSteps(orchestrationRouting{DevelopmentAgent: agentID}, "solo")
	if len(steps) != 1 || steps[0].Stage != "plan" || steps[0].AgentID == "" {
		t.Fatalf("steps = %#v, want one routed planning step", steps)
	}
}

func TestControlledExecutionCannotAutoStartOrUseAutomaticProgression(t *testing.T) {
	requestedAutoStart := true
	if autoStartForTaskExecutionLevel("controlled", &requestedAutoStart, nil) {
		t.Fatal("controlled execution must remain a draft even when a client requests auto-start")
	}
	if got := progressionForTaskExecutionLevel("controlled", "automatic"); got != "gated" {
		t.Fatalf("controlled progression = %q, want gated", got)
	}
}
