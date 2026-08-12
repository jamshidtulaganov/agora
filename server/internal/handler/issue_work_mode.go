package handler

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

const unknownAgentProtocolKind = "__unknown__"

// issueTaskType maps the issue's fundamental type:* label to the execution
// contract used by implementation agents. type:* is the classifier; Agora
// does not persist a second mode:* label family.
func (h *Handler) issueTaskType(ctx context.Context, issue db.Issue) string {
	return resolveTaskType(h.issueLabelNames(ctx, issue.ID))
}

func resolveTaskType(labelNames []string) string {
	hasBug := false
	hasFeature := false
	hasQuestion := false
	hasChore := false
	for _, name := range labelNames {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "type:bug":
			hasBug = true
		case "type:feature":
			hasFeature = true
		case "type:question":
			hasQuestion = true
		case "type:chore", "type:refactor":
			hasChore = true
		}
	}
	// Triage is expected to attach exactly one type. Keep a deterministic,
	// safety-first precedence if historical data contains conflicting types:
	// a possible defect must not bypass the reproduce/root-cause gate.
	switch {
	case hasBug:
		return "bug"
	case hasFeature:
		return "feature"
	case hasQuestion:
		return "question"
	case hasChore:
		return "chore"
	default:
		return ""
	}
}

// issueGitBranchName returns the stable, human-readable branch used by an
// ordinary issue task. Bugs use fix/*; every other issue uses feature/* so an
// untyped feature cannot fall back to an opaque agent/task UUID branch.
func issueGitBranchName(issuePrefix string, issueNumber int32, issueID string, labelNames []string) string {
	kind := "feature"
	for _, name := range labelNames {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "type:bug", "bug":
			kind = "fix"
		}
	}

	identifier := ""
	if prefix := gitBranchSlug(issuePrefix); prefix != "" && issueNumber > 0 {
		identifier = fmt.Sprintf("%s-%d", prefix, issueNumber)
	}
	if identifier == "" {
		identifier = gitBranchSlug(issueID)
		if len(identifier) > 8 {
			identifier = identifier[:8]
		}
	}
	if identifier == "" {
		return ""
	}
	return kind + "/" + identifier
}

func gitBranchSlug(value string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// taskModeInstructionFor returns the issue-type execution contract used by
// implementation runs. It is role-neutral enough for a solo developer or a
// squad lead: a lead must require the same evidence from delegated workers.
// PURE (unit-tested without a DB).
func taskModeInstructionFor(taskType string) string {
	switch taskType {
	case "bug":
		return " ISSUE WORKFLOW — DEBUGGING (`type:bug`). Do not begin with a speculative patch. " +
			"(1) REPRODUCE the reported behavior with a failing automated test or a concrete runnable repro; record the failing evidence. " +
			"If it cannot be reproduced, inspect the supplied logs, comments, attachments, and environment differences and report what evidence is missing instead of guessing. " +
			"(2) Form explicit hypotheses and trace the ROOT CAUSE through the actual code path. Check the installed version, local types, and primary docs for any dependency you touch; do not assume an API from memory. " +
			"(3) Change the smallest surface that fixes the cause, without unrelated cleanup. " +
			"(4) Add or strengthen a regression test and prove failing-before / passing-after. Run the relevant build, type-check, and tests. " +
			"In the handoff or PR, state the repro, root cause, fix, and exact verification. If you delegate, require the worker to return those same four artifacts."
	case "feature":
		return " ISSUE WORKFLOW — PLAN THEN BUILD (`type:feature`). Do not begin by editing code. " +
			"(1) Inspect the issue, comments, attachments, current code paths, and existing conventions; restate concrete acceptance criteria and identify open questions. " +
			"(2) Decide the implementation shape before changing files: outline affected modules, data/API boundaries, edge cases, migration or compatibility impact, and verification. For meaningful UI or architecture choices, compare 2-3 viable variants with tradeoffs; do not manufacture variants for a trivial change. " +
			"(3) If a critical product decision is unresolved, stop at a concise plan and ask one blocking question rather than inventing policy. Otherwise record the chosen plan, then implement the smallest coherent version. " +
			"(4) Verify every acceptance criterion with focused tests plus the relevant build/type-check. In the handoff or PR, summarize the plan, decisions, implementation, and exact verification. If you delegate, give workers outcome-based scopes from the plan."
	case "question":
		return " ISSUE WORKFLOW — INVESTIGATE AND PLAN (`type:question`). Do not assume the issue requires a code change. " +
			"Inspect the issue, comments, attachments, and relevant code or runtime evidence; state what is known, what remains uncertain, and the available options with tradeoffs. " +
			"Answer the question or produce a concrete implementation plan. Only modify code when the current task instruction, issue, or a human follow-up clearly defines an accepted deliverable; otherwise return findings and the single most important next decision. Verify every factual claim you can test."
	default:
		return ""
	}
}

// planningStageInstructionFor adapts the issue workflow for a persisted plan
// step. A planner defines evidence, decisions, and outcome-based work; it must
// not compete with downstream dev workers by implementing the plan itself.
// PURE.
func planningStageInstructionFor(taskType string) string {
	switch taskType {
	case "bug":
		return " ISSUE PLANNING CONTRACT — DEBUGGING (`type:bug`). Produce an evidence-driven debugging plan, not a speculative fix. " +
			"Define the failing behavior and how a worker will reproduce it; list the most likely hypotheses and exact code/runtime evidence that will confirm or reject each one; identify the smallest likely fix boundary; and define the regression test plus failing-before / passing-after verification. " +
			"If the existing evidence is insufficient, make the missing evidence or one blocking question explicit. Do not edit implementation code in the plan stage."
	case "feature":
		return " ISSUE PLANNING CONTRACT — FEATURE (`type:feature`). Produce an implementation-ready plan before any worker edits code. " +
			"Restate testable acceptance criteria; inspect current code paths and conventions; resolve data/API boundaries, edge cases, migration or compatibility impact, and verification. Compare 2-3 viable variants only where a meaningful UI or architecture decision exists, choose one with rationale, and split work into non-overlapping outcome-based scopes. " +
			"If a critical product decision is unresolved, ask one blocking question instead of inventing policy. Do not edit implementation code in the plan stage."
	case "question":
		return " ISSUE PLANNING CONTRACT — INVESTIGATION (`type:question`). Determine whether this is answerable research or a defined code deliverable. " +
			"Summarize known evidence, uncertainties, options and tradeoffs, and the single next decision. If implementation is actually required, state its acceptance criteria and an outcome-based plan; otherwise plan no code work. Do not edit implementation code in the plan stage."
	default:
		return ""
	}
}

func taskModeInstructionForClaim(taskType, orchestrationStage string) string {
	if orchestrationStage == "plan" {
		return planningStageInstructionFor(taskType)
	}
	return taskModeInstructionFor(taskType)
}

// taskRunModeInstructionForClaim applies a human's per-run override. Auto keeps
// the fundamental type:* behavior; every other value is independent of labels
// and affects only the task row that is being claimed.
func taskRunModeInstructionForClaim(runMode, taskType, orchestrationStage string) string {
	switch runMode {
	case "debug":
		return " RUN MODE — DEBUG (explicit human override for this run). Work as an evidence-first debugger regardless of the issue type. " +
			"Reproduce the failure with a runnable repro or failing test; form and test concrete hypotheses; trace the root cause through the actual code path; make the smallest causal fix; and prove failing-before / passing-after with a regression test plus relevant checks. " +
			"Do not substitute a speculative patch for reproduction and root-cause evidence. Report the repro, root cause, fix, and exact verification."
	case "plan":
		return " RUN MODE — PLAN (explicit human override for this run). This run is read-only planning. Inspect the issue, comments, attachments, code, and runtime evidence; restate testable acceptance criteria; identify open decisions, affected boundaries, edge cases, risks, and verification; compare viable variants where a meaningful decision exists; and return an implementation-ready plan. " +
			"Do not edit files, create branches or commits, open a pull request, change issue status, or start implementation. Ask one concise blocking question when a critical decision cannot be resolved from evidence."
	case "build":
		return " RUN MODE — BUILD (explicit human override for this run). Implement the accepted request now regardless of the issue type. Inspect enough existing code and conventions to make a coherent change, but do not stop at a plan or require design variants unless a genuinely blocking decision remains. " +
			"Keep the change scoped, satisfy the stated acceptance criteria, add or update focused tests, run the relevant verification, and report the implementation plus exact evidence."
	default:
		return taskModeInstructionForClaim(taskType, orchestrationStage)
	}
}

// taskTriggerSliceActionKind returns the explicit backend slice-action marker
// carried by a triggering comment. An empty string means an ordinary issue
// assignment/comment. Unknown markers are still returned so claim routing can
// fail closed and avoid injecting implementation instructions into a future
// non-development action.
func (h *Handler) taskTriggerSliceActionKind(ctx context.Context, triggerCommentID pgtype.UUID) string {
	if !triggerCommentID.Valid {
		return ""
	}
	c, err := h.Queries.GetComment(ctx, triggerCommentID)
	if err != nil {
		return unknownAgentProtocolKind
	}
	return sliceActionKindFromComment(c.Content)
}

func sliceActionKindFromComment(content string) string {
	const prefix = "<!--agent-protocol:"
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(content, prefix)
	end := strings.Index(rest, "-->")
	if end < 0 {
		return unknownAgentProtocolKind
	}
	return strings.TrimSpace(rest[:end])
}

// claimNeedsIssueWorkMode reports whether the canonical task-claim brief must
// carry the type-driven execution contract.
//
// Direct assignments and ordinary issue comments are implementation work.
// Persisted orchestration receives a stage-adapted contract for plan/task and
// dev/task steps. A draft_code slice already embeds the same contract in its
// triggering instruction; every other slice action (QA, review, design, docs,
// deploy, etc.) must not receive implementation behavior.
func claimNeedsIssueWorkMode(orchestration bool, stage, stepKind, protocolKind string) bool {
	if orchestration {
		return (stage == "plan" || stage == "dev") && stepKind == "task"
	}
	return protocolKind == ""
}

// agentContextNote formats human-authored, per-issue guidance for injection
// into every agent run. Historical issues may already carry this metadata even
// though the retired embedded-editor control no longer edits it.
func agentContextNote(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return "\n\n## Context from the human (applies to this issue)\n" +
		"Treat the following as authoritative guidance for this task — rules, files to focus on, links, and constraints the human set:\n\n" +
		raw
}
