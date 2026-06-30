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
	"github.com/multica-ai/multica/server/internal/util"
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
	sliceActionAutoDocs   = "auto_docs"
)

// isKnownSliceActionKind reports whether kind is one of the supported scoped
// actions. Used by the handler to reject unknown kinds with a 400 before any
// agent is resolved or any comment is written.
func isKnownSliceActionKind(kind string) bool {
	switch kind {
	case sliceActionDraftCode, sliceActionWriteDocs, sliceActionWriteTests, sliceActionReviewPart, sliceActionRunQA, sliceActionRunCI, sliceActionAutoDocs:
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
		base = "Run QA for this issue as a DETERMINISTIC gate — report strictly by EXIT CODE, never by " +
			"opinion, and do NOT weaken, skip, or delete any test to make it pass. Judge the CHANGE, not the " +
			"repo: a check that is ALREADY red on the base branch is PRE-EXISTING and must NOT fail this gate — " +
			"only a NEW failure this change introduces does. " +
			"(1) BASELINE: check out the merge-base (the base branch this PR/MR targets, e.g. `main`) and run the " +
			"SAME build + lint + test commands you will run below, recording each exit code. Every command that " +
			"already fails here is pre-existing and out of scope for this gate. " +
			"(2) CHECKS: on the change's branch, detect the project type and run its build + lint + tests, " +
			"recording each command and its exit code — e.g. `pnpm build && pnpm lint && pnpm test` (JS/TS), " +
			"`go build ./... && go test ./...` (Go), `php -l` on changed files plus phpunit/codeception (PHP). " +
			"Diff against BASELINE: a command red on BOTH is pre-existing (note it, do not block); a command " +
			"green on baseline but RED on the branch is a NEW failure this change caused — that fails the gate. " +
			"(3) SMOKE — DETERMINISTIC-FIRST, vision-last: decide the smoke verdict from DETERMINISTIC signals " +
			"(HTTP status, console output, network responses, and asserted DOM / accessibility-tree TEXT), NEVER by " +
			"visually judging a screenshot. If the project configures a smoke command (see its QA smoke below), RUN " +
			"IT and take its EXIT CODE as the smoke verdict — a deterministic login-and-assert script (curl/CLI that " +
			"checks status + page text and exits 0/1) is faster, cheaper, and more reliable than driving the UI by " +
			"hand and consumes no vision tokens. Otherwise bring the app up and exercise it in a real browser — " +
			"prefer the co-code editor's embedded Chromium over CDP (get the preview URL and the Chromium CDP url " +
			"from the local daemon's editor endpoints, then drive it with `playwright-core` " +
			"`chromium.connectOverCDP(<cdp_url>)`); if you cannot reach the embedded browser, launch your own " +
			"headless Chromium. Read the page via its DOM / accessibility-tree snapshot (text), not a screenshot, " +
			"and assert ALL of: (a) NO console errors AND no console warnings — in particular a vue-i18n / intlify " +
			"\"Not found '<key>' key\" or any missing-translation warning is a FAIL; (b) no 4xx/5xx network " +
			"responses; (c) the main UI renders — assert specific expected elements/text are PRESENT in the DOM or " +
			"accessibility tree (assert on TEXT, not pixels); and (d) NO untranslated placeholder keys are visible " +
			"in the rendered text — a raw i18n key showing through (a dotted identifier such as `section.tile.title` " +
			"displayed verbatim) means a translation was never registered and is a FAIL, even when nothing logged. " +
			"Apply the same baseline rule to smoke findings: a console error, network failure, or placeholder that " +
			"ALSO reproduces on the unchanged base page is pre-existing; one that appears only after the change is a " +
			"NEW failure. Capture a screenshot ONLY to DOCUMENT a failure you have already determined from the " +
			"assertions above (attach it to the verdict for the human) — do NOT screenshot the happy path, and " +
			"NEVER vision-analyze a screenshot to decide pass/fail; every smoke verdict must trace to a " +
			"deterministic signal. " +
			"(4) WRITE TEST CASES that assert the task's INTENDED behavior — derived from the TASK PLAN (this " +
			"issue's acceptance criteria + description, appended below), NOT from the diff. The diff tells you WHERE " +
			"the behavior lives (which files/functions/UI to target); the PLAN tells you WHAT the correct behavior " +
			"is. For EACH acceptance criterion, author at least one test asserting that criterion's expected " +
			"outcome — unit tests for the relevant logic/functions in the project's existing framework " +
			"(vitest/jest/phpunit/go test), and a Playwright/e2e case for UI driven against the running preview " +
			"over the embedded Chromium. If no acceptance criteria are listed, derive the intended behavior from " +
			"the issue description. CRITICAL: a test encodes what the PLAN says SHOULD happen — if the " +
			"implementation diverges from the plan, the test MUST FAIL (you have surfaced a real bug); never " +
			"rewrite, weaken, or shape a test to match the code to go green. Follow the repo's existing test layout " +
			"and mock external APIs (never hit live endpoints). Commit a new test when it BUILDS and faithfully " +
			"asserts the plan: if it then PASSES on the branch the implementation meets that criterion; if it FAILS " +
			"on the branch (and the criterion is not already broken on baseline) that is a NEW failure — report " +
			"`qa:fail` and KEEP the test. For a bug fix the criterion is 'the bug no longer reproduces': the test " +
			"must FAIL on the pre-change behaviour and PASS after (fail-before / pass-after). A criterion with NO " +
			"covering test is a coverage GAP — list it in the verdict. NEVER weaken, skip, or delete an existing " +
			"test to go green. " +
			"(5) VERDICT: post a comment with two sections — NEW (regressions this change introduced) and " +
			"PRE-EXISTING (already red on baseline, out of scope) — listing every command with its baseline and " +
			"branch exit code, the tests you added and WHICH acceptance criterion each one covers (plus any " +
			"criterion left uncovered), and the screenshots. Set the `qa:pass` label when this change introduces " +
			"NO new failure AND your plan-driven tests pass (the implementation meets every criterion) AND the " +
			"smoke is clean — even if the repo carries pre-existing red. Set `qa:fail` when the change introduces " +
			"or worsens a failure OR an implemented criterion's test fails. Never fabricate a green result, but " +
			"never blame the change for pre-existing breakage. " +
			"At the END of that comment, append a fenced ```qa-result code block containing ONLY a JSON object the " +
			"editor's QA panel parses to render the result structured: " +
			"`{\"verdict\":\"pass\"|\"fail\",\"summary\":\"<one line>\",\"commands\":[{\"cmd\":\"<command>\"," +
			"\"baseline_exit\":<int|null>,\"branch_exit\":<int>,\"kind\":\"pass\"|\"new_failure\"|\"pre_existing\"}]," +
			"\"screenshots\":[\"<path-or-url>\"]}` — `baseline_exit` is null for a command that only exists on the " +
			"branch (e.g. your new tests); `kind` is `new_failure` only when baseline passed and the branch failed. " +
			"The JSON must be valid and self-contained (the human-readable sections above stay as well). " +
			"Do NOT merge anything — your verdict is advisory and the human decides next."
		if guidance := qaBaselineGuidanceFor(strings.ToLower(strings.TrimSpace(scope))); guidance != "" {
			base += guidance
		}
	case sliceActionRunCI:
		base = "Run the CI gate for this issue's branch. Find the issue's open pull/merge request for its " +
			"branch and check out that branch. Detect the " +
			"project's checks and run them, reporting strictly by EXIT CODE — not opinion: for PHP run " +
			"`php -l` on every changed .php file plus any test suite (phpunit / codeception); for JS/TS " +
			"run the lint and test scripts if present (e.g. `pnpm lint`, `pnpm test`); for Go run " +
			"`go build ./...` and `go test ./...`. Then inspect the diff for the agent-fixes-the-test " +
			"failure mode: if it rewrites, weakens, or deletes test assertions, or lowers coverage / " +
			"disables lint to pass, call that out explicitly. Post a comment listing each command, its " +
			"exit status, and any failing output, then set the `ci:pass` label ONLY if every check " +
			"exited 0, otherwise `ci:fail`. Do NOT change code or merge anything — the gate is a " +
			"deterministic signal and the human decides what to do next."
	case sliceActionAutoDocs:
		base = "Document this issue's change in the project's DOCUMENTATION repository — a SEPARATE repo " +
			"from the code (its URL is appended below when configured; if none is configured, stop and say so). " +
			"(1) DETERMINE WHAT CHANGED from this issue (its diff / linked PR): new or changed modules, API " +
			"endpoints, data-model fields, settings, behavior, or user-facing flows. Documentation-only — do NOT " +
			"touch product code. (2) IN THE DOCS REPO, write or update the pages that cover what changed, following " +
			"the repo's EXISTING structure and conventions (read neighboring pages first; match their headings, " +
			"sidebar entries, and tone). Update the relevant reference page(s) and add a changelog entry; only add " +
			"a new page when no existing one fits. Keep the canonical locale authoritative and leave translation " +
			"scaffolds consistent with how the repo handles locales. (3) Open a review request against the docs repo " +
			"with the doc changes for human review — a GitHub pull request, or, for a GitLab docs repo, the " +
			"merge-request push-option flow described below. Do NOT merge — the human decides. If the change is purely " +
			"internal (no doc-worthy surface), say so in a comment and open nothing rather than inventing content."
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

// sliceActionQASmokeContext appends the project's configured QA smoke target to
// a run_qa instruction when one is set. A project may store `qa_smoke_cmd` (how
// to bring the app up, e.g. "pnpm dev") and/or `qa_smoke_url` (where it serves)
// in project.settings; when present the agent uses them instead of guessing.
// Returns "" when there is no project or no override — the generic recipe's
// auto-detect path then applies. This is what makes the generic gate
// project-configurable without hardcoding any one product's smoke flow.
func (h *Handler) sliceActionQASmokeContext(ctx context.Context, issue db.Issue) string {
	if !issue.ProjectID.Valid {
		return ""
	}
	project, err := h.Queries.GetProject(ctx, issue.ProjectID)
	if err != nil || len(project.Settings) == 0 {
		return ""
	}
	var settings struct {
		QASmokeCmd string `json:"qa_smoke_cmd"`
		QASmokeURL string `json:"qa_smoke_url"`
	}
	if json.Unmarshal(project.Settings, &settings) != nil {
		return ""
	}
	cmd := strings.TrimSpace(settings.QASmokeCmd)
	url := strings.TrimSpace(settings.QASmokeURL)
	if cmd == "" && url == "" {
		return ""
	}
	out := " This project configures its QA smoke:"
	if cmd != "" {
		out += " start the app with `" + cmd + "`;"
	}
	if url != "" {
		out += " smoke it at " + url + ";"
	}
	out += " use these instead of auto-detecting."
	return out
}

// sliceActionDocsRepoContext appends the project's configured documentation repo
// to an auto_docs instruction. A project may store `docs_repo` (the docs
// repository URL, e.g. a Docusaurus site repo separate from the code) in
// project.settings; when present the agent writes the docs there and opens the
// PR against it. Returns "" when unset — the recipe then asks the agent to stop
// (docs need an explicit target). Mirrors sliceActionQASmokeContext.
func (h *Handler) sliceActionDocsRepoContext(ctx context.Context, issue db.Issue) string {
	if !issue.ProjectID.Valid {
		return ""
	}
	project, err := h.Queries.GetProject(ctx, issue.ProjectID)
	if err != nil || len(project.Settings) == 0 {
		return ""
	}
	var settings struct {
		DocsRepo string `json:"docs_repo"`
	}
	if json.Unmarshal(project.Settings, &settings) != nil {
		return ""
	}
	return docsRepoInstruction(settings.DocsRepo)
}

// docsRepoInstruction is the pure text policy for an auto_docs target (split out
// so it is unit-testable without a DB). It names the docs repo and, when that
// repo is a GitLab URL, appends the merge-request push-option flow — keyed on
// the DOCS repo's own host, not the issue's code repo. The two can differ: a
// project's code may live on GitHub/Bitrix while its docs site is a self-hosted
// GitLab repo (e.g. sales-doctor-docs on gitlab.sdteam.uz), which has no
// `gh`/pull-request flow. Returns "" when no docs repo is set.
func docsRepoInstruction(docsRepo string) string {
	repo := strings.TrimSpace(docsRepo)
	if repo == "" {
		return ""
	}
	out := " The documentation repository for this project is " + repo +
		" — write the docs there and open the review request against it."
	if strings.Contains(strings.ToLower(repo), "gitlab") {
		out += branchInstructionFor(true, "")
	}
	return out
}

// autoDocsEnabled gates the qa:pass → auto_docs auto-trigger. Default off so the
// behavior is opt-in and never fires for a deployment that hasn't enabled it.
func autoDocsEnabled() bool {
	return strings.TrimSpace(os.Getenv("AGORA_AUTO_DOCS_ENABLED")) == "true"
}

// projectDocsAgentID reads the project's configured docs agent (an agent UUID in
// project.settings.docs_agent) — the dedicated agent that writes docs into the
// docs repo. Empty when unset.
func (h *Handler) projectDocsAgentID(ctx context.Context, issue db.Issue) string {
	if !issue.ProjectID.Valid {
		return ""
	}
	project, err := h.Queries.GetProject(ctx, issue.ProjectID)
	if err != nil || len(project.Settings) == 0 {
		return ""
	}
	var s struct {
		DocsAgent string `json:"docs_agent"`
	}
	if json.Unmarshal(project.Settings, &s) != nil {
		return ""
	}
	return strings.TrimSpace(s.DocsAgent)
}

// resolveAutoDocsAgent picks the agent to run an auto-fired auto_docs: the
// project's configured docs agent (preferred — it has the docs repo + skill),
// else the issue's agent assignee (the squad working it), else the qa:pass
// setter's own agent. ok=false when none resolve.
func (h *Handler) resolveAutoDocsAgent(ctx context.Context, issue db.Issue, userID string) (db.Agent, bool) {
	if id := h.projectDocsAgentID(ctx, issue); id != "" {
		if aid, err := util.ParseUUID(id); err == nil {
			if agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
				ID: aid, WorkspaceID: issue.WorkspaceID,
			}); err == nil {
				return agent, true
			}
		}
	}
	if issue.AssigneeType.Valid && issue.AssigneeType.String == "agent" && issue.AssigneeID.Valid {
		if agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID: issue.AssigneeID, WorkspaceID: issue.WorkspaceID,
		}); err == nil {
			return agent, true
		}
	}
	return h.resolveOwnAgent(ctx, issue.WorkspaceID, userID)
}

// maybeAutoDocsOnLabel fires an auto_docs run when the just-attached label is
// qa:pass, the feature is enabled, and the project has a docs_repo configured.
// This is the last link of the automation chain: implement → QA → (qa:pass) →
// docs. Best-effort: any miss (disabled, wrong label, no docs repo, no agent)
// silently no-ops, so a label attach never fails because of it. Run detached
// (context.Background) by the caller so it doesn't block or get cancelled with
// the request.
func (h *Handler) maybeAutoDocsOnLabel(ctx context.Context, issue db.Issue, labelName, userID string) {
	if !autoDocsEnabled() {
		return
	}
	if strings.ToLower(strings.TrimSpace(labelName)) != "qa:pass" {
		return
	}
	docsCtx := h.sliceActionDocsRepoContext(ctx, issue)
	if docsCtx == "" {
		return // no docs repo configured → auto_docs has no target
	}
	agent, ok := h.resolveAutoDocsAgent(ctx, issue, userID)
	if !ok {
		return
	}
	instruction := buildSliceInstruction(sliceActionAutoDocs, "") + docsCtx
	content := fmt.Sprintf("[@%s](mention://agent/%s) ", sanitizeMentionLabel(agent.Name), uuidToString(agent.ID)) + instruction
	comment, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "member",
		AuthorID:    parseUUID(userID),
		Content:     content,
		Type:        "comment",
		ParentID:    pgtype.UUID{Valid: false},
	})
	if err != nil {
		slog.Warn("auto_docs: create comment failed", "error", err, "issue_id", uuidToString(issue.ID))
		return
	}
	h.triggerTasksForComment(ctx, issue, comment, nil, "member", userID, nil)
	slog.Info("auto_docs fired on qa:pass", "issue_id", uuidToString(issue.ID), "agent_id", uuidToString(agent.ID))
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

// qaBaselineGuidanceFor returns the scope-keyed baseline guidance appended to a
// run_qa instruction. On a shared sprint branch the stock merge-base baseline
// freezes at sprint start, so it can no longer attribute a NEW failure to one
// task; the scope token selects which baseline ref the gate should diff against:
//
//   - "task"       — baseline is the MOVING per-sprint last-green ref. The gate
//     diffs last-green → branch tip so a NEW failure is attributed to exactly
//     the commits that landed since the last fully-green run (this task). After
//     a fully-green run the agent advances the ref forward (never backward) so
//     the next task's delta stays one-task-sized. If the ref is missing (e.g. a
//     force-push orphaned it), fall back to the sprint-root baseline for that
//     one run and note the coarser attribution in the verdict.
//   - "regression" — baseline is the FIXED sprint-root: the merge-base of the
//     sprint branch against the branch it will merge into. Diffing the whole
//     sprint branch against sprint-root answers "is the accumulated sprint
//     healthy vs the base we'll merge into", catching cross-task drift. Used by
//     the daily backstop and the sprint-end full regression.
//   - "" or unknown — no extra guidance; the instruction keeps its original
//     merge-base wording (backward-compatible default path).
//
// PURE — no I/O, no handler state — so it is unit-testable without a database.
// The wording is product-neutral: it names only git refs (last-green, sprint
// root, merge-base) and never a product, box, or branch prefix.
func qaBaselineGuidanceFor(scope string) string {
	switch scope {
	case "task":
		return " SPRINT-BRANCH BASELINE (scope=task): this runs on a SHARED sprint branch where the plain " +
			"merge-base froze at sprint start and can no longer tell which task turned a check red. For the BASELINE " +
			"step above, diff against the MOVING last-green ref for this sprint instead of the merge-base — read its SHA " +
			"with `git rev-parse refs/sprint/<sprintId>/last-green` (the orchestrator provides <sprintId>) and check that " +
			"SHA out as the baseline. The delta from last-green to the branch tip is exactly what landed since the last " +
			"fully-green run, so a NEW failure is attributable to THIS task. If the ref is missing (never created, or a " +
			"force-push orphaned it), fall back to the sprint-root merge-base for this one run and NOTE the coarser " +
			"attribution in the verdict. After a FULLY-GREEN run — every check green and your new tests passing — advance " +
			"the ref to the tested SHA with `git update-ref refs/sprint/<sprintId>/last-green <testedSha>` (only ever " +
			"FORWARD, never backward) so the next task diffs from this known-good point."
	case "regression":
		return " SPRINT-BRANCH BASELINE (scope=regression): this is a WHOLE-BRANCH regression on the shared sprint " +
			"branch, not a single task. For the BASELINE step above, use the FIXED sprint-root — the merge-base of the " +
			"sprint branch against the branch it will merge into (`git merge-base <baseBranch> <sprintBranch>`, the " +
			"orchestrator provides both refs). Diff the entire sprint branch against sprint-root so the verdict answers " +
			"\"is the accumulated sprint healthy vs the base we'll merge into\" and catches CROSS-TASK drift that a " +
			"per-task baseline would miss. Run the full suite (build + lint + every test tier + smoke); do NOT advance " +
			"the last-green ref from a regression run — last-green only ever moves forward off a per-task scope=task run."
	default:
		return ""
	}
}

// qaPlanContext renders the issue's PLAN — description + acceptance criteria —
// as a block appended to a run_qa instruction, so the QA agent authors tests
// against the INTENDED behavior rather than re-deriving them from the diff it is
// judging (the task-claim brief carries only the title + trigger comment, never
// the description / acceptance_criteria). PURE — reads only the passed
// description and raw acceptance_criteria JSON, so it is unit-testable without a
// DB. Returns "" when there is no plan to add (blank description AND empty /
// `[]` / `null` acceptance_criteria); the recipe's "derive intent from the
// description" fallback then applies with whatever the agent fetches itself.
func qaPlanContext(description string, acceptanceCriteria []byte) string {
	desc := strings.TrimSpace(description)
	// Cap on RUNES (content is often Cyrillic/Uzbek) so a huge description can't
	// blow the comment size and we never split a multi-byte rune.
	const maxDescRunes = 1500
	if r := []rune(desc); len(r) > maxDescRunes {
		desc = string(r[:maxDescRunes]) + "…"
	}
	criteria := parseAcceptanceCriteria(acceptanceCriteria)
	if desc == "" && len(criteria) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(" TASK PLAN — author tests against THIS intended behavior, not the diff.")
	if desc != "" {
		b.WriteString(" Plan/description: ")
		b.WriteString(desc)
		if !strings.HasSuffix(desc, ".") {
			b.WriteString(".")
		}
	}
	if len(criteria) > 0 {
		b.WriteString(" Acceptance criteria:")
		for i, c := range criteria {
			b.WriteString(fmt.Sprintf(" (%d) %s;", i+1, c))
		}
	}
	b.WriteString(" A test must assert what the plan says SHOULD happen; if the implementation " +
		"diverges, the test FAILS (a real bug) — never rewrite the test to match the code.")
	return b.String()
}

// parseAcceptanceCriteria defensively extracts human-readable criterion strings
// from the issue's acceptance_criteria JSONB, whose shape is importer-written and
// not guaranteed: it may be a JSON array of strings, an array of objects (with a
// text-ish field), or something else. Unknown / empty shapes yield nil so the
// caller omits the criteria line rather than dumping raw JSON noise into the
// prompt.
func parseAcceptanceCriteria(raw []byte) []string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "[]" || s == "null" {
		return nil
	}
	// 1. ["criterion a", "criterion b"]
	var strs []string
	if json.Unmarshal(raw, &strs) == nil {
		out := make([]string, 0, len(strs))
		for _, v := range strs {
			if t := strings.TrimSpace(v); t != "" {
				out = append(out, t)
			}
		}
		return out
	}
	// 2. [{"text": "…"}] / {"title": …} / {"description": …}
	var objs []map[string]any
	if json.Unmarshal(raw, &objs) == nil {
		out := make([]string, 0, len(objs))
		for _, o := range objs {
			for _, k := range []string{"text", "title", "description", "criterion", "name"} {
				if v, ok := o[k].(string); ok && strings.TrimSpace(v) != "" {
					out = append(out, strings.TrimSpace(v))
					break
				}
			}
		}
		return out
	}
	return nil
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
	// run_qa is project-configurable: append the project's smoke cmd/url when set,
	// and the issue's PLAN (description + acceptance criteria) so the agent authors
	// tests against the INTENDED behavior rather than re-deriving them from the diff
	// it is judging (the task-claim brief carries only the title + trigger comment).
	if req.Kind == sliceActionRunQA {
		// Smoke the ASSIGNEE DEVELOPER'S own QA box when one resolves, so each dev's
		// branch is verified on their isolated environment (https://<handle>.<host>)
		// rather than a shared project URL. Overrides the project qa_smoke_url below.
		if url := h.devBoxSmokeURL(r.Context(), issue); url != "" {
			instruction += " SMOKE TARGET: the assignee developer's QA box serves this branch at " + url +
				" — deploy the branch to it (the deploy-qa git-sync) and smoke THAT url. It OVERRIDES any project smoke url below."
		}
		instruction += h.sliceActionQASmokeContext(r.Context(), issue)
		instruction += qaPlanContext(issue.Description.String, issue.AcceptanceCriteria)
	}
	// auto_docs targets the project's configured docs repo when set.
	if req.Kind == sliceActionAutoDocs {
		instruction += h.sliceActionDocsRepoContext(r.Context(), issue)
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
