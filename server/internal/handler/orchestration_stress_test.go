package handler

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Randomized invariant checks for the pure scheduling and plan-validation
// layers. Seeded PRNG keeps every failure reproducible from the logged seed.

func stressUUID(t *testing.T, r *rand.Rand) pgtype.UUID {
	t.Helper()
	return orchestrationTestUUID(t, fmt.Sprintf(
		"%08x-%04x-4%03x-8%03x-%012x",
		r.Uint32(), r.Intn(0x10000), r.Intn(0x1000), r.Intn(0x1000), r.Int63n(1<<48),
	))
}

func TestSelectDispatchableOrchestrationStepsStressInvariants(t *testing.T) {
	statuses := []string{"pending", "queued", "running", "completed", "failed", "waiting_approval", "cancelled", "skipped"}
	for seed := int64(0); seed < 500; seed++ {
		r := rand.New(rand.NewSource(seed))

		agents := make([]pgtype.UUID, 1+r.Intn(6))
		for i := range agents {
			agents[i] = stressUUID(t, r)
		}
		steps := make([]db.OrchestrationStep, r.Intn(30))
		for i := range steps {
			step := db.OrchestrationStep{
				ID:       stressUUID(t, r),
				Status:   statuses[r.Intn(len(statuses))],
				Position: int32(i),
			}
			if r.Intn(10) > 0 { // ~10% unrouted steps
				step.AgentID = agents[r.Intn(len(agents))]
			}
			step.ApprovalRequired = r.Intn(5) == 0
			steps[i] = step
		}
		var runnable []db.OrchestrationStep
		for _, step := range steps {
			if step.Status == "pending" && r.Intn(2) == 0 {
				runnable = append(runnable, step)
			}
		}
		maxConcurrency := r.Intn(6) // includes 0 (invalid, must select nothing)

		selected := selectDispatchableOrchestrationSteps(steps, runnable, maxConcurrency)

		// Invariant 1: never exceed remaining run capacity.
		active := 0
		busy := map[string]bool{}
		for _, step := range steps {
			if step.Status == "queued" || step.Status == "running" {
				active++
				if step.AgentID.Valid {
					busy[uuidToString(step.AgentID)] = true
				}
			}
		}
		capacity := maxConcurrency - active
		if capacity < 0 {
			capacity = 0
		}
		if maxConcurrency < 1 {
			capacity = 0
		}
		if len(selected) > capacity {
			t.Fatalf("seed %d: selected %d with %d active exceeds cap %d", seed, len(selected), active, maxConcurrency)
		}

		seen := map[string]bool{}
		agentTaken := map[string]bool{}
		for _, step := range selected {
			id := uuidToString(step.ID)
			// Invariant 2: no duplicate step selection.
			if seen[id] {
				t.Fatalf("seed %d: step %s selected twice", seed, id)
			}
			seen[id] = true
			// Invariant 3: human-only approval rows never consume a slot.
			if step.ApprovalRequired && !step.AgentID.Valid {
				t.Fatalf("seed %d: unrouted approval step %s selected", seed, id)
			}
			if step.AgentID.Valid {
				agent := uuidToString(step.AgentID)
				// Invariant 4: an agent already active in the run is never
				// double-booked, and one dispatch batch books each agent once.
				if busy[agent] || agentTaken[agent] {
					t.Fatalf("seed %d: agent %s double-booked", seed, agent)
				}
				agentTaken[agent] = true
			}
		}
	}
}

func TestPrepareOrchestrationPlanStressNeverPanicsAndStaysBackward(t *testing.T) {
	stages := []string{"plan", "dev", "qa", "review", "release", "", "bogus"}
	kinds := []string{"", "task", "integration", "bogus"}
	capabilities := []string{"", "implementation", "backend", "frontend", "qa", "review", "integration", "coordination", "release", "bogus"}
	for seed := int64(0); seed < 1000; seed++ {
		r := rand.New(rand.NewSource(seed))
		count := r.Intn(12)
		steps := make([]orchestrationStepRequest, count)
		for i := range steps {
			step := orchestrationStepRequest{
				Key:        fmt.Sprintf("s%d", r.Intn(count+1)), // collisions on purpose
				Title:      "t",
				Stage:      stages[r.Intn(len(stages))],
				Kind:       kinds[r.Intn(len(kinds))],
				Capability: capabilities[r.Intn(len(capabilities))],
			}
			if r.Intn(4) == 0 {
				step.Title = "" // exercise the missing-title rejection
			}
			for d := 0; d < r.Intn(3); d++ {
				// Mix of backward, self, forward, and unknown references.
				step.DependsOnKeys = append(step.DependsOnKeys, fmt.Sprintf("s%d", r.Intn(count+2)))
			}
			if r.Intn(3) == 0 {
				step.ParentKey = fmt.Sprintf("s%d", r.Intn(count+2))
			}
			steps[i] = step
		}

		err := prepareOrchestrationPlan(steps) // must never panic
		if err != nil {
			continue
		}
		// Accepted plans must be provably acyclic: every dependency and parent
		// resolves to a strictly earlier step with a distinct key.
		position := map[string]int{}
		for i, step := range steps {
			if _, dup := position[step.Key]; dup {
				t.Fatalf("seed %d: accepted plan has duplicate key %q", seed, step.Key)
			}
			for _, dep := range step.DependsOnKeys {
				at, ok := position[dep]
				if !ok || at >= i {
					t.Fatalf("seed %d: accepted plan has non-backward dependency %q -> %q", seed, step.Key, dep)
				}
			}
			if step.ParentKey != "" {
				at, ok := position[step.ParentKey]
				if !ok || at >= i {
					t.Fatalf("seed %d: accepted plan has non-backward parent %q -> %q", seed, step.Key, step.ParentKey)
				}
			}
			position[step.Key] = i
		}
	}
}
