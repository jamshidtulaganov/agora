package runtrace

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

func TestAssembleExample(t *testing.T) {
	tr := db.AgentRunTrace{
		OutcomeLabel:     LabelAccepted,
		HumanRevised:     true,
		ReactionScore:    3,
		FinalIssueStatus: pgtype.Text{String: "done", Valid: true},
	}
	issue := &db.Issue{Title: "Fix login", Description: pgtype.Text{String: "users cannot log in", Valid: true}}
	agent := &db.Agent{Instructions: "be concise"}
	msgs := []db.TaskMessage{
		{Seq: 1, Type: "text", Content: pgtype.Text{String: "looking", Valid: true}},
		{Seq: 2, Type: "tool", Tool: pgtype.Text{String: "bash", Valid: true}, Output: pgtype.Text{String: "ok", Valid: true}},
	}

	ex := assembleExample(tr, issue, agent, msgs)

	if ex.Outcome != LabelAccepted {
		t.Errorf("outcome = %q, want %q", ex.Outcome, LabelAccepted)
	}
	if ex.Input.IssueTitle != "Fix login" || ex.Input.IssueDescription != "users cannot log in" {
		t.Errorf("issue input = %+v", ex.Input)
	}
	if ex.Input.AgentInstructions != "be concise" {
		t.Errorf("instructions = %q", ex.Input.AgentInstructions)
	}
	if ex.Signals.FinalStatus != "done" || !ex.Signals.HumanRevised || ex.Signals.ReactionScore != 3 {
		t.Errorf("signals = %+v", ex.Signals)
	}
	if len(ex.Output) != 2 {
		t.Fatalf("output turns = %d, want 2", len(ex.Output))
	}
	if ex.Output[1].Tool != "bash" || ex.Output[1].Output != "ok" {
		t.Errorf("turn2 = %+v", ex.Output[1])
	}
}

func TestAssembleExampleNilInputs(t *testing.T) {
	// Chat run: no issue, no agent row → input omitted, output empty, no panic.
	ex := assembleExample(db.AgentRunTrace{OutcomeLabel: LabelPending}, nil, nil, nil)
	if ex.Input.IssueTitle != "" || ex.Input.AgentInstructions != "" {
		t.Errorf("expected empty input, got %+v", ex.Input)
	}
	if len(ex.Output) != 0 {
		t.Errorf("expected no output turns, got %d", len(ex.Output))
	}
}
