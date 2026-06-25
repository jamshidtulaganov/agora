package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// AI slice-actions — the human-protagonist core. On a human-owned issue a
// developer fires a SCOPED, single-shot AI action (draft code, write docs,
// write tests, or review a part) and reviews the resulting draft. The server
// is the single source of truth for what the agent is asked to do: it renders
// a fixed instruction template, posts it as an @mention comment that targets
// the resolved agent, and routes it through the canonical comment-trigger path
// so exactly ONE agent task is queued. The agent always produces a draft for
// the human to REVIEW and is explicitly told never to merge — the human stays
// the protagonist and the decision-maker.

// sliceActionKind enumerates the scoped actions a developer may fire. The set
// is closed: an unknown kind is a 400, never a free-form prompt. Keeping the
// kinds as typed constants (rather than ad-hoc strings sprinkled through the
// handler) makes buildSliceInstruction the SINGLE place that decides what an
// agent is told to do.
const (
	sliceActionDraftCode  = "draft_code"
	sliceActionWriteDocs  = "write_docs"
	sliceActionWriteTests = "write_tests"
	sliceActionReviewPart = "review_part"
	sliceActionRunQA      = "run_qa"
	sliceActionRunCI      = "run_ci"
)

// isKnownSliceActionKind reports whether kind is one of the supported scoped
// actions. Used by the handler to reject unknown kinds with a 400 before any
// agent is resolved or any comment is written.
func isKnownSliceActionKind(kind string) bool {
	switch kind {
	case sliceActionDraftCode, sliceActionWriteDocs, sliceActionWriteTests, sliceActionReviewPart, sliceActionRunQA, sliceActionRunCI:
		return true
	default:
		return false
	}
}

// buildSliceInstruction renders the English instruction the resolved agent
// receives for a scoped action. It is PURE — no I/O, no handler state — so it
// is the single source of truth for slice-action wording and is exhaustively
// unit-tested without a database.
//
// Every template ends by asking the agent to produce a PR / comment for the
// human to REVIEW and explicitly tells it NEVER to merge: the human owns the
// issue and remains the decision-maker. The optional scope is appended as a
// "Focus on: <scope>" clause so the developer can narrow the action to one
// slice (a file, a function, a behaviour) without changing the template.
//
// An unknown kind returns "" — callers validate the kind with
// isKnownSliceActionKind before reaching this function, so a non-empty result
// is guaranteed on the happy path.
func buildSliceInstruction(kind, scope string) string {
	var base string
	switch kind {
	case sliceActionDraftCode:
		base = "Draft a code change for this issue. Open a pull request with your proposed " +
			"implementation so the human can review it. Do NOT merge the pull request — leave " +
			"the merge decision to the human reviewer."
	case sliceActionWriteDocs:
		base = "Write documentation for this issue. Open a pull request with the proposed docs " +
			"so the human can review it. Do NOT merge the pull request — leave the merge " +
			"decision to the human reviewer."
	case sliceActionWriteTests:
		base = "Write tests for this issue. Open a pull request with the proposed tests so the " +
			"human can review it. Do NOT merge the pull request — leave the merge decision to " +
			"the human reviewer."
	case sliceActionReviewPart:
		base = "Review the relevant part of this issue and post your findings as a comment for " +
			"the human to review. Do NOT make or merge any changes yourself — your review is " +
			"advisory and the human reviewer decides what to do next."
	case sliceActionRunQA:
		base = "Run QA for this issue against the dev test box. Resolve the open pull request " +
			"for this issue's branch (e.g. `gh pr list --head btx-<bitrix task id>`), then use " +
			"the sd-qa-process skill to switch the box to that branch, run a smoke test against the " +
			"live UI, and restore the base branch afterwards. Post the pass/fail verdict as a " +
			"comment and set the `qa:pass` or `qa:fail` label. Do NOT make code changes or merge " +
			"anything — your verdict is advisory and the human decides what to do next."
	case sliceActionRunCI:
		base = "Run the CI gate for this issue's branch. Resolve the open pull request " +
			"(e.g. `gh pr list --head btx-<bitrix task id>`) and check out its branch. Detect the " +
			"project's checks and run them, reporting strictly by EXIT CODE — not opinion: for PHP run " +
			"`php -l` on every changed .php file plus any test suite (phpunit / codeception); for JS/TS " +
			"run the lint and test scripts if present (e.g. `pnpm lint`, `pnpm test`); for Go run " +
			"`go build ./...` and `go test ./...`. Then inspect the diff for the agent-fixes-the-test " +
			"failure mode: if it rewrites, weakens, or deletes test assertions, or lowers coverage / " +
			"disables lint to pass, call that out explicitly. Post a comment listing each command, its " +
			"exit status, and any failing output, then set the `ci:pass` label ONLY if every check " +
			"exited 0, otherwise `ci:fail`. Do NOT change code or merge anything — the gate is a " +
			"deterministic signal and the human decides what to do next."
	default:
		return ""
	}

	scope = strings.TrimSpace(scope)
	if scope != "" {
		base += " Focus on: " + scope
	}
	return base
}

// sliceActionOpensPR reports whether a slice-action kind produces a pull request
// (and so benefits from a deterministic, QA-resolvable branch name). review_part
// posts an advisory comment and opens nothing.
func sliceActionOpensPR(kind string) bool {
	switch kind {
	case sliceActionDraftCode, sliceActionWriteDocs, sliceActionWriteTests:
		return true
	default:
		return false
	}
}

// issueRepoIsGitLab reports whether the issue's project is backed by a GitLab
// repo. GitLab repos are bound as github_repo resources (that type is just the
// daemon's checkout trigger) carrying a gitlab URL — e.g. sd-bridge on
// gitlab.sdteam.uz. GitLab has no `gh`/pull-request flow, so PR-producing slice
// actions must steer the agent to the merge-request push-option flow instead.
func (h *Handler) issueRepoIsGitLab(ctx context.Context, issue db.Issue) bool {
	if !issue.ProjectID.Valid {
		return false
	}
	for _, row := range h.listProjectResourcesForProject(ctx, issue.ProjectID) {
		if row.ResourceType != "github_repo" {
			continue
		}
		var ref struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(row.ResourceRef, &ref) == nil && strings.Contains(strings.ToLower(ref.URL), "gitlab") {
			return true
		}
	}
	return false
}

// sliceActionBranchInstruction returns the host-specific branch + review-request
// guidance appended to a PR-producing slice action. GitHub repos get the `gh`
// pull-request flow against `billing` (PROD). GitLab repos get the merge-request
// push-option flow against `main`: a plain `git push -o merge_request.create`
// opens the MR over the SAME SSH remote the clone already uses — no `glab` login,
// no token — because neither `gh` nor a `billing` base exists there. Either way
// the agent never merges; the human reviewer decides.
func (h *Handler) sliceActionBranchInstruction(ctx context.Context, issue db.Issue) string {
	branch := ""
	if tid := bitrixTaskIDFromMetadata(issue.Metadata); tid != "" {
		branch = "btx-" + tid
	}
	return branchInstructionFor(h.issueRepoIsGitLab(ctx, issue), branch)
}

// gitlabBaseBranch is the branch GitLab merge-request slice actions target +
// branch from. Defaults to `main`; set AGORA_GITLAB_MR_TARGET (e.g. "dev") to
// route agent MRs at a staging branch so their work does NOT auto-deploy to
// prod every iteration (main → prod via deploy:main). The human then merges
// the staging branch into main once, for a single prod deploy.
func gitlabBaseBranch() string {
	if b := strings.TrimSpace(os.Getenv("AGORA_GITLAB_MR_TARGET")); b != "" {
		return b
	}
	return "main"
}

// branchInstructionFor is the pure text policy behind sliceActionBranchInstruction
// (split out so it is unit-testable without a DB). GitLab → merge-request push
// options against `main`; GitHub with a known branch → `gh` PR against `billing`;
// GitHub without one → no extra guidance (the agent names its own branch).
func branchInstructionFor(isGitLab bool, branch string) string {
	if isGitLab {
		name := branch
		if name == "" {
			name = "a short descriptive"
		}
		base := gitlabBaseBranch()
		return " This is a GitLab repository — there is no `gh` or GitHub pull-request flow here. Create branch `" + name +
			"` from `" + base + "`, commit your change, and push it WITH GitLab merge-request push options so a Merge Request opens automatically: " +
			"`git push -o merge_request.create -o merge_request.target=" + base + " -o merge_request.remove_source_branch origin <branch>`. " +
			"Do NOT merge the merge request — leave that decision to the human reviewer."
	}
	if branch != "" {
		return " Name the working branch " + branch +
			", branch it from `billing`, and open the pull request against the `billing` base branch (never master)."
	}
	return ""
}

// issueTaskType maps the issue's `type:*` label to a workflow mode: "bug",
// "feature", or "chore" ("" when untyped). The human tags the type (no
// auto-classify), so this just reflects their intent into how the agent works.
func (h *Handler) issueTaskType(ctx context.Context, issue db.Issue) string {
	labels, err := h.Queries.ListLabelsByIssue(ctx, db.ListLabelsByIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return ""
	}
	for _, l := range labels {
		switch strings.ToLower(strings.TrimSpace(l.Name)) {
		case "type:bug":
			return "bug"
		case "type:feature":
			return "feature"
		case "type:chore", "type:refactor":
			return "chore"
		}
	}
	return ""
}

// taskModeInstructionFor returns the type-specific approach appended to a
// draft_code action so the agent works like a real engineer for that kind of
// work: a BUG gets a reproduce → root-cause → verify debugger loop; a FEATURE
// gets design-variants-first. PURE (unit-tested without a DB).
func taskModeInstructionFor(taskType string) string {
	switch taskType {
	case "bug":
		return " This is a BUG (type:bug) — work like a debugger, not a patcher: " +
			"(1) REPRODUCE it first with a failing test or a concrete runnable repro that currently FAILS; " +
			"(2) trace the ROOT CAUSE — and check the ACTUAL installed version of any library/framework you touch " +
			"(read its types/docs) instead of assuming an API from memory; " +
			"(3) apply the smallest fix that addresses the cause; " +
			"(4) prove the failing test/repro now PASSES. Show failing-before / passing-after in the PR."
	case "feature":
		return " This is a FEATURE (type:feature) — work like a product engineer: " +
			"(1) when any UI is involved, lay out 2-3 DESIGN VARIANTS (layout/interaction options + tradeoffs) and get " +
			"the direction reviewed — defer to the designer agent's variants if one is already posted — before " +
			"committing to a single build; (2) build the chosen approach; (3) verify the new behavior with a test that " +
			"exercises it. Note which variant you built and why."
	default:
		return ""
	}
}

// verifyGateInstruction is the universal "prove it works before you call it
// done" clause for draft_code actions. Closes the recurring failure where an
// agent reported success from code inspection and re-ran blindly because it
// never actually exercised the change. PURE.
func verifyGateInstruction() string {
	return " VERIFY before opening the PR — never report success from inspection alone: add or run a test that " +
		"exercises THIS change (for UI, a component test that drives the affected element and asserts the resulting " +
		"state; for logic, a unit/integration test) AND run the build / type-check. State in the PR exactly what you " +
		"ran and its result. If a check cannot run in your environment, say so explicitly rather than assuming it passes."
}

// sanitizeSliceScope neutralizes a caller-supplied scope so it can NEVER form a
// parsable mention once embedded in the slice-action comment body. The comment
// is re-parsed by triggerTasksForComment via util.ParseMentions, whose
// recognizer (util.MentionRe) requires the literal shape
// `[@?label](mention://type/id)`. Stripping the substring "mention://" plus the
// bracket/paren delimiters "]", "(", ")" removes every anchor the regex keys
// off, so no injected scope can smuggle a second mention (and thus a second
// queued task targeting an arbitrary public agent/squad/@all). This mirrors how
// sanitizeMentionLabel guards the display label; here we guard the free-form
// scope. The result stays human-readable — only the mention-forming delimiters
// are dropped.
func sanitizeSliceScope(scope string) string {
	cleaned := strings.ReplaceAll(scope, "mention://", "")
	cleaned = strings.ReplaceAll(cleaned, "]", "")
	cleaned = strings.ReplaceAll(cleaned, "(", "")
	cleaned = strings.ReplaceAll(cleaned, ")", "")
	return strings.TrimSpace(cleaned)
}

// CreateSliceActionRequest is the POST body for firing a slice action.
//
//	kind     — required; one of the supported sliceAction* kinds.
//	scope    — optional; a free-form narrowing clause ("the parser", "auth.go").
//	agent_id — optional; an explicit agent to target. When omitted the handler
//	           falls back to the issue's agent assignee, then to the caller's
//	           own ready agent.
type CreateSliceActionRequest struct {
	Kind    string `json:"kind"`
	Scope   string `json:"scope"`
	AgentID string `json:"agent_id"`
}

// CreateSliceActionResponse is returned on a successful fire. It echoes the
// resolved kind / scope, the rendered instruction (so the UI can show exactly
// what the agent was asked), the targeting comment, and the resolved agent.
type CreateSliceActionResponse struct {
	Kind        string          `json:"kind"`
	Scope       string          `json:"scope,omitempty"`
	Instruction string          `json:"instruction"`
	AgentID     string          `json:"agent_id"`
	Comment     CommentResponse `json:"comment"`
}

// CreateSliceAction handles POST /api/issues/{id}/slice-actions.
//
// Flow:
//  1. Authenticate the caller and load the issue in their workspace.
//  2. Validate the kind (400 on unknown).
//  3. Resolve the target agent, in order:
//     (a) explicit agent_id (must be in this workspace, not archived, has a
//     runtime);
//     (b) the issue's agent assignee;
//     (c) the caller's own first ready agent (resolveOwnAgent).
//     400 when none resolve.
//  4. Render the instruction and post it as an @mention comment that targets
//     the resolved agent, attributed to the caller (member).
//  5. Publish comment:created, then route through the canonical comment-trigger
//     helper so exactly ONE agent task is queued — the named agent. Because the
//     comment @mentions the resolved agent, the mention drives the task; the
//     on_comment assignee trigger is deduped against the same agent id, so the
//     assignee is never double-triggered.
func (h *Handler) CreateSliceAction(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var req CreateSliceActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Kind = strings.TrimSpace(req.Kind)
	if !isKnownSliceActionKind(req.Kind) {
		writeError(w, http.StatusBadRequest, "unknown slice action kind")
		return
	}

	agent, ok := h.resolveSliceActionAgent(w, r, issue, userID, strings.TrimSpace(req.AgentID))
	if !ok {
		return
	}

	// Neutralize the caller-controlled scope BEFORE it is embedded in the
	// comment body. buildSliceInstruction appends the scope verbatim as a
	// "Focus on: <scope>" clause, and triggerTasksForComment re-parses the
	// finished comment with util.ParseMentions — so an un-sanitized scope can
	// smuggle a SECOND mention link ([@x](mention://agent/<other-uuid>)) into
	// the body and queue a task for an arbitrary agent/squad/@all, breaking the
	// "exactly one task, resolved agent only" invariant. Sanitizing here (not
	// inside buildSliceInstruction) keeps that renderer pure and unit-testable.
	scope := sanitizeSliceScope(req.Scope)
	instruction := buildSliceInstruction(req.Kind, scope)

	// For PR-producing actions, pin the working branch to the Bitrix task id so
	// the QA runner can later resolve the PR deterministically
	// (gh pr list --head btx-<id>). Done in the handler, not in the pure
	// buildSliceInstruction, because it depends on the issue's metadata.
	if sliceActionOpensPR(req.Kind) {
		// draft_code adapts to the issue's type label (bug → debugger loop,
		// feature → design-variants-first) and always carries the verify gate
		// so the agent proves the change works before opening the PR.
		if req.Kind == sliceActionDraftCode {
			instruction += taskModeInstructionFor(h.issueTaskType(r.Context(), issue))
			instruction += verifyGateInstruction()
		}
		instruction += h.sliceActionBranchInstruction(r.Context(), issue)
	}

	// Build the @mention link the comment-trigger path keys off:
	// [@Name](mention://agent/<id>). The label is human-display only — the
	// trigger parser matches the mention://agent/<id> URL, not the label — but
	// a sanitized agent name keeps the rendered comment legible.
	mentionPrefix := fmt.Sprintf("[@%s](mention://agent/%s) ", sanitizeMentionLabel(agent.Name), uuidToString(agent.ID))
	content := mentionPrefix + instruction

	// Author the comment as the calling member. This is a human-initiated
	// action on a human-owned issue, so the slice-action comment is attributed
	// to the developer who fired it — not to a system actor — which keeps the
	// triggering-comment author (and downstream task initiator) the real human.
	comment, err := h.Queries.CreateComment(r.Context(), db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "member",
		AuthorID:    parseUUID(userID),
		Content:     content,
		Type:        "comment",
		ParentID:    pgtype.UUID{Valid: false},
	})
	if err != nil {
		slog.Warn("slice action: create comment failed", append(logger.RequestAttrs(r), "error", err, "issue_id", issueID)...)
		writeError(w, http.StatusInternalServerError, "failed to create slice action comment")
		return
	}

	resp := commentToResponse(comment, nil, nil)
	slog.Info("slice action comment created", append(logger.RequestAttrs(r),
		"comment_id", uuidToString(comment.ID),
		"issue_id", issueID,
		"kind", req.Kind,
		"agent_id", uuidToString(agent.ID),
	)...)
	h.publish(protocol.EventCommentCreated, uuidToString(issue.WorkspaceID), "member", userID, map[string]any{
		"comment":             resp,
		"issue_title":         issue.Title,
		"issue_assignee_type": textToPtr(issue.AssigneeType),
		"issue_assignee_id":   uuidToPtr(issue.AssigneeID),
		"issue_status":        issue.Status,
	})

	// Route through the canonical comment-trigger helper so exactly ONE agent
	// task is queued. The comment @mentions the resolved agent, so the mention
	// trigger fires for that agent; computeCommentAgentTriggers dedupes by agent
	// id, so even when the resolved agent IS the issue assignee the assignee is
	// not double-triggered. actorType is "member" / actorID is the caller.
	h.triggerTasksForComment(r.Context(), issue, comment, nil, "member", userID, nil)

	writeJSON(w, http.StatusCreated, CreateSliceActionResponse{
		Kind: req.Kind,
		// Echo the sanitized scope — the same value embedded in the comment —
		// so the response never reports a scope that differs from what the
		// agent actually received.
		Scope:       strings.TrimSpace(scope),
		Instruction: instruction,
		AgentID:     uuidToString(agent.ID),
		Comment:     resp,
	})
}

// resolveSliceActionAgent resolves the agent a slice action targets and writes
// the appropriate error response when none can be resolved (returning ok=false).
//
// Resolution order:
//  1. explicit agentID — validated via GetAgentInWorkspace, must be in this
//     workspace, not archived, and have a runtime. An invalid explicit id is a
//     hard 400 (the caller asked for a specific agent that does not qualify).
//  2. the issue's agent assignee (assignee_type == "agent") — must be ready.
//  3. resolveOwnAgent — the caller's first ready, user-owned agent.
//
// Returns 400 with an explanatory message when nothing resolves.
func (h *Handler) resolveSliceActionAgent(w http.ResponseWriter, r *http.Request, issue db.Issue, userID, agentID string) (db.Agent, bool) {
	workspaceID := uuidToString(issue.WorkspaceID)

	// (a) Explicit agent_id.
	if agentID != "" {
		agentUUID, ok := parseUUIDOrBadRequest(w, agentID, "agent_id")
		if !ok {
			return db.Agent{}, false
		}
		agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
			ID:          agentUUID,
			WorkspaceID: issue.WorkspaceID,
		})
		// Gate private agents the caller may not access with the SAME 400 as a
		// nonexistent/unusable agent. Without this gate the downstream trigger
		// path silently drops the inaccessible agent, so the handler would 201
		// while queuing zero tasks AND leak the private agent's name/id in the
		// posted comment. Returning the identical error keeps an inaccessible
		// private agent indistinguishable from one that does not exist — no
		// existence oracle.
		if err != nil || !sliceAgentReady(agent) ||
			!h.canAccessPrivateAgent(r.Context(), agent, "member", userID, workspaceID) {
			writeError(w, http.StatusBadRequest, "agent_id does not refer to a usable agent in this workspace")
			return db.Agent{}, false
		}
		return agent, true
	}

	// (b) The issue's agent assignee. Treat an inaccessible private assignee as
	// "not resolved" and fall through to the own-agent path (c): the assignee
	// belongs to someone else, so silently queuing nothing (or leaking its
	// identity in the comment) is worse than falling back to the caller's own
	// agent. The own-agent path is owner==caller, so it is always accessible.
	if issue.AssigneeType.Valid && issue.AssigneeType.String == "agent" && issue.AssigneeID.Valid {
		agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
			ID:          issue.AssigneeID,
			WorkspaceID: issue.WorkspaceID,
		})
		if err == nil && sliceAgentReady(agent) &&
			h.canAccessPrivateAgent(r.Context(), agent, "member", userID, workspaceID) {
			return agent, true
		}
	}

	// (c) The caller's own ready agent.
	if agent, ok := h.resolveOwnAgent(r.Context(), issue.WorkspaceID, userID); ok {
		return agent, true
	}

	writeError(w, http.StatusBadRequest, "no agent available for this slice action")
	return db.Agent{}, false
}

// sliceAgentReady reports whether an agent can run a task: it must have a
// runtime bound and must not be archived. GetAgentInWorkspace does NOT filter
// archived rows, so this check is required on every resolution path that uses
// it. ListAgents (used by resolveOwnAgent) already excludes archived agents,
// but re-checking RuntimeID there keeps the predicate honest in one place.
func sliceAgentReady(agent db.Agent) bool {
	return agent.RuntimeID.Valid && !agent.ArchivedAt.Valid
}

// resolveOwnAgent returns the caller's first ready agent in the workspace —
// the earliest-created (ListAgents orders by created_at ASC), non-archived,
// has-a-runtime agent whose owner_id equals the calling userID.
//
// NOTE: agent.owner_id is a USER id (the user who created the agent), so the
// comparison is owner_id == userID directly. ListAgents already excludes
// archived agents; we still gate on a bound runtime so an owner whose only
// agent has no runtime is treated as "no ready agent" rather than enqueuing a
// task that EnqueueTaskForMention would reject.
func (h *Handler) resolveOwnAgent(ctx context.Context, workspaceID pgtype.UUID, userID string) (db.Agent, bool) {
	agents, err := h.Queries.ListAgents(ctx, workspaceID)
	if err != nil {
		slog.Warn("slice action: list agents for own-agent fallback failed",
			"workspace_id", uuidToString(workspaceID), "error", err)
		return db.Agent{}, false
	}
	for _, agent := range agents {
		if !sliceAgentReady(agent) {
			continue
		}
		if uuidToString(agent.OwnerID) == userID {
			return agent, true
		}
	}
	return db.Agent{}, false
}
