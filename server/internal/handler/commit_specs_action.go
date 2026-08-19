package handler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/config"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Spec promotion: the last stage of the review → E2E chain. The QA agent's
// compiled Playwright scripts live as platform rows (test_case.script) and are
// executed by run_test_cases. That is enough for the gate, but it leaves the
// repository with no test of its own: the next change to the same feature is
// only protected while Agora is in the loop. commit_tests lands the specs that
// ACTUALLY PASSED on the change's own branch, so the project's committed suite
// grows with every reviewed change and CI catches the regression without any
// agent involvement.
//
// Deliberately narrow: only cases whose LATEST run passed, only ones carrying a
// compiled script, only onto the branch that already has an open PR/MR, and only
// when the project opted in. Everything else no-ops.

// specCommitCap bounds how many specs ride in one commit_tests instruction.
// Each spec embeds its full script, so an uncapped list would blow the prompt on
// a large issue. Anything dropped is LOGGED and named in the instruction — a
// silent truncation would read as "the suite is complete" when it is not.
const specCommitCap = 8

// commitSpecsEnabled gates the qa:pass → commit_tests auto-dispatch. Default off
// — opt-in, matching every other auto-* gate. Project-scoped: a project may
// override AGORA_COMMIT_SPECS_ENABLED for its own issues.
func (h *Handler) commitSpecsEnabled(ctx context.Context, issue db.Issue) bool {
	return config.BoolFrom(h.projectConfigOverrides(ctx, issue), "AGORA_COMMIT_SPECS_ENABLED")
}

// specCommitMarker tags an auto-fired commit_tests dispatch so a second qa:pass
// in the same cycle cannot summon a second committer (which would race the first
// on the same branch and produce two commits of the same spec).
const specCommitMarker = "<!--spec-commit:auto-->"

// greenScriptedCasesForIssue returns the issue's automated cases that are
// SAFE to commit: they carry a compiled script AND their latest recorded run
// passed. A case with no run at all is excluded (never executed → unproven), as
// is one whose latest run failed or errored — committing either plants a red
// test that blocks every future pipeline on the branch.
//
// Only the issue's OWN cases are considered. Project base scripts are already
// committed-or-promoted suite material; re-committing them per issue would
// duplicate them across the repo.
func (h *Handler) greenScriptedCasesForIssue(ctx context.Context, issue db.Issue) []db.TestCase {
	cases, err := h.Queries.ListAutomatedTestCasesForIssue(ctx, db.ListAutomatedTestCasesForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil || len(cases) == 0 {
		return nil
	}
	runs, err := h.Queries.ListLatestRunsForIssueCases(ctx, db.ListLatestRunsForIssueCasesParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return nil
	}
	latest := make(map[string]string, len(runs))
	for _, r := range runs {
		latest[uuidToString(r.TestCaseID)] = strings.ToLower(strings.TrimSpace(r.Status))
	}
	var green []db.TestCase
	for _, c := range cases {
		if strings.TrimSpace(c.Script) == "" {
			continue
		}
		if latest[uuidToString(c.ID)] != "pass" {
			continue
		}
		green = append(green, c)
	}
	return green
}

// issueReviewBranch resolves the branch a spec commit must land on:
//  1. the branch of an OPEN linked pull/merge request (GitHub webhook sync
//     records it; a GitLab MR linked from a comment URL often has none, since
//     the URL carries no branch);
//  2. the Bitrix branch convention `btx-<taskId>` — the same name
//     sliceActionBranchInstruction told the dev agent to use, so it is the
//     branch that MR is on.
//
// "" when neither resolves: with no known branch there is nothing to push to,
// and guessing would either fail or commit onto the default branch.
func (h *Handler) issueReviewBranch(ctx context.Context, issue db.Issue) string {
	if prs, err := h.Queries.ListPullRequestsByIssue(ctx, issue.ID); err == nil {
		for _, pr := range prs {
			if pr.MergedAt.Valid || !strings.EqualFold(strings.TrimSpace(pr.State), "open") {
				continue
			}
			if b := strings.TrimSpace(pr.Branch.String); pr.Branch.Valid && b != "" {
				return b
			}
		}
	}
	if tid := bitrixTaskIDFromMetadata(issue.Metadata); tid != "" {
		return "btx-" + tid
	}
	return ""
}

// sliceActionCommitSpecsContext renders the branch + the exact specs to commit.
// Returns ok=false when there is nothing to commit or nowhere to commit it, so
// the caller skips the dispatch rather than sending an agent to figure it out.
func (h *Handler) sliceActionCommitSpecsContext(ctx context.Context, issue db.Issue, branch string, green []db.TestCase) (string, bool) {
	if branch == "" || len(green) == 0 {
		return "", false
	}
	var b strings.Builder
	b.WriteString(" TARGET BRANCH: `" + branch + "` — the branch this issue's change is already on. Check it out; do not create another.")
	if key := h.issueKey(ctx, issue); key != "" {
		b.WriteString(" ISSUE KEY (for the commit message + the traceability comment): " + key + ".")
	}
	b.WriteString(" SPECS TO COMMIT (verified: each one's LATEST run passed against the reviewed change):")
	for i, c := range green {
		if i >= specCommitCap {
			break
		}
		b.WriteString(fmt.Sprintf(" [case_id=%s] %s — asserts: %s.\n```javascript\n%s\n```",
			uuidToString(c.ID), c.Title, strings.TrimSpace(c.Expected), strings.TrimSpace(c.Script)))
	}
	if dropped := len(green) - specCommitCap; dropped > 0 {
		b.WriteString(fmt.Sprintf(" NOTE: %d further passing spec(s) were NOT included in this dispatch (per-dispatch cap of %d) — say so in your report so the rest can be committed by a follow-up run.",
			dropped, specCommitCap))
	}
	return b.String(), true
}

// issueKey renders the human issue key (PREFIX-123) for an issue, or "" when the
// workspace prefix cannot be read.
func (h *Handler) issueKey(ctx context.Context, issue db.Issue) string {
	prefix := strings.TrimSpace(h.getIssuePrefix(ctx, issue.WorkspaceID))
	if prefix == "" {
		return ""
	}
	return fmt.Sprintf("%s-%d", prefix, issue.Number)
}

// maybeCommitSpecsOnQAPass fires commit_tests when the E2E pass went green, so
// the specs that just proved the change become part of the repository. Guards,
// cheapest first:
//   - AGORA_COMMIT_SPECS_ENABLED off (default) → not opted in;
//   - the label is not qa:pass;
//   - a landed qa:fail (a fail-wins surface) → the run is not green after all;
//   - a dispatch already went out for this issue → don't race the branch;
//   - nothing green + scripted to commit, or no resolvable branch;
//   - no free QA agent.
//
// Detached + best-effort: a miss costs a committed spec, never the gate.
func (h *Handler) maybeCommitSpecsOnQAPass(ctx context.Context, issue db.Issue, labelName, actorID string) {
	if h.orchestrationOwnsIssuePipeline(ctx, issue.ID) {
		return
	}
	if !h.commitSpecsEnabled(ctx, issue) {
		return
	}
	if strings.ToLower(strings.TrimSpace(labelName)) != "qa:pass" {
		return
	}
	defer lockIssueQA(uuidToString(issue.ID))()

	if h.issueHasLabel(ctx, issue, "qa:fail") {
		return
	}
	// One committer per issue: unlike the review dispatch (which reopens per
	// review cycle), a second commit of the same specs is pure duplication, and
	// the cases already committed are not re-derivable from the label state.
	if h.issueHasCommentMarker(ctx, issue, specCommitMarker) {
		return
	}
	green := h.greenScriptedCasesForIssue(ctx, issue)
	branch := h.issueReviewBranch(ctx, issue)
	specCtx, ok := h.sliceActionCommitSpecsContext(ctx, issue, branch, green)
	if !ok {
		slog.Info("auto commit_tests: nothing to commit",
			"issue_id", uuidToString(issue.ID), "green_specs", len(green), "branch", branch)
		return
	}
	agents := filterQAAgentsForScope(h.qaAgentsForIssue(ctx, issue), h.issueQAScopeTrivial(ctx, issue))
	if len(agents) == 0 {
		return
	}
	var free []db.Agent
	for _, a := range agents {
		pending, err := h.Queries.HasPendingTaskForIssueAndAgent(ctx, db.HasPendingTaskForIssueAndAgentParams{
			IssueID: issue.ID,
			AgentID: a.ID,
		})
		if err == nil && !pending {
			free = append(free, a)
		}
	}
	if len(free) == 0 {
		return
	}
	runner := h.pickLeastBusyQAAgent(ctx, free)

	instruction := buildSliceInstruction(sliceActionCommitTests, "") + specCtx
	authorType, authorID := h.dispatchAuthor(ctx, issue, "member", actorID)
	if !authorID.Valid {
		slog.Warn("auto commit_tests: no valid dispatch author, skipping",
			"actor_id", actorID, "issue_id", uuidToString(issue.ID))
		return
	}
	content := agentProtocolMarker(sliceActionCommitTests) + specCommitMarker + "\n" +
		fmt.Sprintf("[@%s](mention://agent/%s) ", sanitizeMentionLabel(runner.Name), uuidToString(runner.ID)) + instruction
	comment, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  authorType,
		AuthorID:    authorID,
		Content:     content,
		Type:        "comment",
		ParentID:    pgtype.UUID{Valid: false},
	})
	if err != nil {
		slog.Warn("auto commit_tests: create comment failed", "error", err, "issue_id", uuidToString(issue.ID))
		return
	}
	h.triggerTasksForComment(ctx, issue, comment, nil, authorType, uuidToString(authorID), nil)
	slog.Info("auto commit_tests fired",
		"issue_id", uuidToString(issue.ID), "agent_id", uuidToString(runner.ID),
		"branch", branch, "specs", len(green))
}
