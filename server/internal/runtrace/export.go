package runtrace

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/internal/util"
)

// Example is one fine-tuning training record assembled from a run trace:
// input (what the agent was asked) -> output (what it did) + the human-judged
// outcome. Consumers filter by Outcome (e.g. accepted-only for SFT) or use the
// label as a preference signal.
type Example struct {
	RunID   string         `json:"run_id"`
	TaskID  string         `json:"task_id"`
	Outcome string         `json:"outcome"`
	Input   ExampleInput   `json:"input"`
	Output  []ExampleTurn  `json:"output"`
	Signals ExampleSignals `json:"signals"`
}

type ExampleInput struct {
	IssueTitle        string `json:"issue_title,omitempty"`
	IssueDescription  string `json:"issue_description,omitempty"`
	AgentInstructions string `json:"agent_instructions,omitempty"`
}

type ExampleTurn struct {
	Seq     int32  `json:"seq"`
	Type    string `json:"type"`
	Tool    string `json:"tool,omitempty"`
	Content string `json:"content,omitempty"`
	Output  string `json:"output,omitempty"`
}

type ExampleSignals struct {
	FinalStatus   string `json:"final_status,omitempty"`
	Reopened      bool   `json:"reopened"`
	HumanRevised  bool   `json:"human_revised"`
	ReactionScore int32  `json:"reaction_score"`
}

// ExportParams selects which traces to export.
type ExportParams struct {
	WorkspaceID pgtype.UUID
	Outcome     pgtype.Text // invalid/NULL = all outcomes
	Limit       int32
	Offset      int32
}

// assembleExample shapes one training example from a trace and its joined rows.
// Pure: issue/agent may be nil (a chat run with no issue, or a deleted row) and
// are then simply omitted from the input.
func assembleExample(tr db.AgentRunTrace, issue *db.Issue, agent *db.Agent, msgs []db.TaskMessage) Example {
	ex := Example{
		RunID:   util.UUIDToString(tr.ID),
		TaskID:  util.UUIDToString(tr.TaskID),
		Outcome: tr.OutcomeLabel,
		Signals: ExampleSignals{
			Reopened:      tr.Reopened,
			HumanRevised:  tr.HumanRevised,
			ReactionScore: tr.ReactionScore,
		},
	}
	if tr.FinalIssueStatus.Valid {
		ex.Signals.FinalStatus = tr.FinalIssueStatus.String
	}
	if issue != nil {
		ex.Input.IssueTitle = issue.Title
		if issue.Description.Valid {
			ex.Input.IssueDescription = issue.Description.String
		}
	}
	if agent != nil {
		ex.Input.AgentInstructions = agent.Instructions
	}
	ex.Output = make([]ExampleTurn, 0, len(msgs))
	for _, m := range msgs {
		turn := ExampleTurn{Seq: m.Seq, Type: m.Type}
		if m.Tool.Valid {
			turn.Tool = m.Tool.String
		}
		if m.Content.Valid {
			turn.Content = m.Content.String
		}
		if m.Output.Valid {
			turn.Output = m.Output.String
		}
		ex.Output = append(ex.Output, turn)
	}
	return ex
}

// BuildExamples fetches matching traces and assembles each into a training
// example by joining issue (input), agent (input), and task_message (output).
// Per-trace join failures degrade gracefully (missing input omitted) rather
// than dropping the row.
func BuildExamples(ctx context.Context, q *db.Queries, p ExportParams) ([]Example, error) {
	traces, err := q.ListRunTracesForExport(ctx, db.ListRunTracesForExportParams{
		WorkspaceID: p.WorkspaceID,
		Outcome:     p.Outcome,
		Lim:         p.Limit,
		Off:         p.Offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Example, 0, len(traces))
	for _, tr := range traces {
		var issue *db.Issue
		if tr.IssueID.Valid {
			if i, err := q.GetIssue(ctx, tr.IssueID); err == nil {
				issue = &i
			}
		}
		var agent *db.Agent
		if a, err := q.GetAgent(ctx, tr.AgentID); err == nil {
			agent = &a
		}
		msgs, _ := q.ListTaskMessages(ctx, tr.TaskID)
		out = append(out, assembleExample(tr, issue, agent, msgs))
	}
	return out, nil
}
