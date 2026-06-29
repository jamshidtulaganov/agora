package handler

import (
	"fmt"
	"strings"
)

// inEditorCoCodeNote is appended to an agent's instructions when the claimed
// issue is in "in_editor" work mode (issue metadata key work_mode). In that
// mode a human is co-coding live in the embedded browser VS Code editor on the
// SAME worktree, so the agent should work in small reviewable steps and treat
// the human's direct edits as authoritative. The default mode (full_pipeline)
// adds nothing.
const inEditorCoCodeNote = "\n\n## Co-coding mode (in-editor)\n" +
	"A human is co-coding with you in a live browser VS Code editor on THIS worktree.\n\n" +
	"**Branch isolation — CRITICAL.** NEVER commit or push to `main` or `master`. Before you change anything in a repo, make sure it is on a dedicated feature branch: if it is on its default branch, run `git checkout -b cocode/<issue-key>` first (use THIS issue's key, e.g. `cocode/OCT-1244`; if that branch already exists, switch to it). Commit and push ONLY to that feature branch. Pushing to the default branch bypasses the human's review — it is not allowed. Their review and merge happen via a pull request opened FROM your feature branch, so every change must live on the branch.\n\n" +
	"Work in small, reviewable steps and commit + push frequently to the feature branch so the human can follow along. The human may edit files directly between your turns — re-read files before changing them and treat their edits as authoritative. Prefer focused diffs over large rewrites, and briefly explain non-obvious changes."

// agentContextNote formats human-authored, per-issue context for injection into
// an agent's instructions. Set via the issue metadata key "agent_context" (the
// editor's Context panel) — rules, files to focus on, links, and constraints the
// human wants the agent to honor. Applies to every agent run on the issue
// (editor or prompts), the same way inEditorCoCodeNote layers onto a co-code run.
func agentContextNote(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return "\n\n## Context from the human (applies to this issue)\n" +
		"Treat the following as authoritative guidance for this task — rules, files to focus on, links, and constraints the human set:\n\n" +
		raw
}

// coCodeBranchName is the dedicated feature branch the daemon forces an
// in_editor worktree onto before the agent runs, so co-code commits can never
// land on main (the git-level guarantee behind the review gate — instructions
// alone don't survive a resumed Claude session). Deterministic per (issue,
// agent) so the branch is stable across the agent's turns and accumulates one
// reviewable PR.
func coCodeBranchName(issueNumber int32, agentID string) string {
	a := strings.ReplaceAll(agentID, "-", "")
	if len(a) > 8 {
		a = a[:8]
	}
	return fmt.Sprintf("cocode/issue-%d-%s", issueNumber, a)
}
