package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/multica-ai/multica/server/internal/config"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/service"
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
	sliceActionDraftCode         = "draft_code"
	sliceActionWriteDocs         = "write_docs"
	sliceActionWriteTests        = "write_tests"
	sliceActionReviewPart        = "review_part"
	sliceActionRunQA             = "run_qa"
	sliceActionRunCI             = "run_ci"
	sliceActionAutoDocs          = "auto_docs"
	sliceActionGenTests          = "gen_test_cases"
	sliceActionRunTests          = "run_test_cases"
	sliceActionCompileTests      = "compile_tests"
	sliceActionDesignProposal    = "design_proposal"
	sliceActionGenDesignManifest = "gen_design_manifest"
	sliceActionDesignAudit       = "design_audit"
)

// isKnownSliceActionKind reports whether kind is one of the supported scoped
// actions. Used by the handler to reject unknown kinds with a 400 before any
// agent is resolved or any comment is written.
func isKnownSliceActionKind(kind string) bool {
	switch kind {
	case sliceActionDraftCode, sliceActionWriteDocs, sliceActionWriteTests, sliceActionReviewPart, sliceActionRunQA, sliceActionRunCI, sliceActionAutoDocs, sliceActionGenTests, sliceActionRunTests, sliceActionCompileTests, sliceActionDesignProposal, sliceActionGenDesignManifest, sliceActionDesignAudit:
		return true
	default:
		return false
	}
}

// isQASliceAction reports whether kind is a QA-family action — the QA gate
// (run_qa) or the test authoring/execution slices. These are QA's job to run,
// NOT the developer whose work is under test, so when fired without an explicit
// agent they default to the QA squad leader rather than the issue's dev
// assignee (see resolveSliceActionAgent).
func isQASliceAction(kind string) bool {
	switch kind {
	case sliceActionRunQA, sliceActionGenTests, sliceActionRunTests:
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
			"\"baseline_exit\":<int|null>,\"branch_exit\":<int>,\"kind\":\"pass\"|\"new_failure\"|\"pre_existing\"," +
			"\"error\":\"<short reason, ONLY for new_failure>\"}],\"screenshots\":[\"<path-or-url>\"]}` — " +
			"`baseline_exit` is null for a command that only exists on the branch (e.g. your new tests); `kind` is " +
			"`new_failure` only when baseline passed and the branch failed. For EVERY `new_failure` command, set " +
			"`error` to the ONE line that actually explains it — the failing assertion message or the last " +
			"non-empty stderr line (e.g. `expected 200, got 500` or `AssertionError: title not trimmed`), NOT the " +
			"full stack trace and NOT a restatement of the exit code. Omit `error` (or leave it empty) for `pass` " +
			"and `pre_existing` commands. The JSON must be valid and self-contained (the human-readable sections " +
			"above stay as well). Do NOT merge anything — your verdict is advisory and the human decides next."
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
			"scaffolds consistent with how the repo handles locales. (3) MAINTAIN THE QA MANIFEST: the docs repo keeps " +
			"the project's navigation manifest at qa-manifest/<project-slug>.json (base_url, auth, routes, flows) — the " +
			"map QA agents navigate by instead of exploring. If this change ADDED, RENAMED, MOVED, or REMOVED a route, " +
			"page, or user flow, update that file in the same change (add the route under routes, add/adjust the flow's " +
			"steps+assert); if the file does not exist yet, create it from the PROJECT QA MANIFEST appended below. Skip " +
			"this step only when the change has no navigation impact. (4) Open a review request against the docs repo " +
			"with the doc changes for human review — a GitHub pull request, or, for a GitLab docs repo, the " +
			"merge-request push-option flow described below. Do NOT merge — the human decides. If the change is purely " +
			"internal (no doc-worthy surface), say so in a comment and open nothing rather than inventing content."
	case sliceActionGenTests:
		base = "Author QA test cases for this issue — you are the QA Squad's automation engineer, and you write cases " +
			"like a senior QA engineer: BOTH categories, deliberately, not just a pile of edge cases with no structure. " +
			"Derive cases from the issue's PLAN (its description + acceptance criteria, appended below) and, when a " +
			"diff / linked PR exists, the actual change. For EVERY case, decide its category: `positive` — the golden " +
			"path, valid input, the feature working as intended; `negative` — invalid, malformed, boundary, or " +
			"adversarial input the system must reject or degrade on gracefully (empty/null, wrong type, out-of-range, " +
			"unauthorized, duplicate, conflicting state). A change with only positive cases has NO evidence it fails " +
			"safely — always include negative cases for user-controlled input, permission boundaries, and error paths, " +
			"not just the happy path. COVER EVERY APPLICABLE TEST LAYER and prefix each title with its layer tag: " +
			"`[e2e]` — browser golden path driven with Playwright against the QA box (use the PROJECT QA MANIFEST " +
			"routes/flows below when present); `[api]` — direct authenticated HTTP calls asserting status + response " +
			"shape (curl/fetch, no browser); `[unit]` — the repo's own test framework on the changed function/module; " +
			"`[smoke]` — the cheapest liveness assertion for the changed page. Pick the layers the change actually " +
			"touches — but a UI change with no [e2e] case, or an endpoint change with no [api] case, is an authoring " +
			"gap. Do NOT run anything and do NOT touch code — only WRITE the cases. " +
			"At the END of your comment, append a fenced ```test-cases code block containing ONLY a JSON array the QA " +
			"panel parses: `[{\"title\":\"<short>\",\"steps\":\"<numbered steps, newline-separated>\",\"expected\":" +
			"\"<expected result>\",\"kind\":\"manual\"|\"automated\",\"category\":\"positive\"|\"negative\",\"script\":" +
			"\"<a self-contained runnable Playwright script — REQUIRED for every [e2e]/[api] automated case>\"}]` — " +
			"`automated` for a case a script/HTTP/DOM smoke can run deterministically, `manual` for one a human must " +
			"click through. Keep titles unique and specific. The JSON must be valid and self-contained; a short " +
			"human-readable summary may precede it. " +
			"COMPILED SCRIPT (the biggest speed win): you MUST emit a `script` inline for EVERY [e2e] and [api] automated " +
			"case — authoring it here SKIPS the separate compile step (a whole extra agent run + round-trip), so never " +
			"leave an [e2e]/[api] automated case without one. Each is a " +
			"COMPLETE, self-contained Playwright ESM module that runs with plain `node`: it MUST " +
			"`import { chromium } from \"playwright\";` (for [api] cases you may use only `fetch`), use the PROJECT " +
			"QA MANIFEST's base_url + auth (log in via the manifest's login_path/fields) and the manifest ROUTES/FLOWS, " +
			"perform the case's steps, ASSERT the expected result by deterministic signal (DOM / accessibility-tree TEXT " +
			"via `page.locator(...)`, HTTP status, or response shape — never a screenshot), then `process.exit(0)` on pass " +
			"and `process.exit(1)` on ANY failed assertion or thrown error (wrap the body in try/catch and exit(1) in catch). " +
			"For [e2e] cases you MUST DRIVE THE BROWSER against the SHARED review browser so the reviewer watches it live: " +
			"when `process.env.AGORA_DAEMON_PORT` is set, POST " +
			"`http://127.0.0.1:${process.env.AGORA_DAEMON_PORT}/editor/browser/start` with `{\"workdir\":\"qa-target:<the manifest base_url>\"}`, " +
			"read `cdp_url`, then `const browser = await chromium.connectOverCDP(cdp_url); const context = browser.contexts()[0] ?? " +
			"await browser.newContext(); const page = context.pages()[0] ?? await context.newPage();` (fall back to " +
			"`chromium.launch()` ONLY if that POST fails or AGORA_DAEMON_PORT is unset; close the browser in finally ONLY on " +
			"that launched path). Then `page.goto(route)` / fill / click / `page.locator(...)` the real UI — do NOT shortcut a " +
			"UI case with a raw fetch of the HTML. Add Playwright TRACING so a QA reviewer can replay the " +
			"run step-by-step in-app: when `process.env.TRACE_PATH` is set, `await context.tracing.start({ screenshots: " +
			"true, snapshots: true, sources: true });` after creating the context and " +
			"`await context.tracing.stop({ path: process.env.TRACE_PATH });` in the `finally` before closing the browser " +
			"(guard both on `process.env.TRACE_PATH`). [api]/fetch cases have no browser and capture no trace. " +
			"No test-runner harness, no external config, no CLI args — the script is the whole test. " +
			"Omit `script` for [unit]/[smoke]/manual cases (those stay hand-driven)."
	case sliceActionRunTests:
		base = "Run this issue's AUTOMATED QA test cases as a DETERMINISTIC check — you are the QA Squad's automation " +
			"engineer. The cases (id · title · steps · expected) are listed below. BEFORE you start driving EACH case's " +
			"steps, output the line `RUNNING test_case:<the case's id>` on its own — the QA panel watches your live " +
			"output for this exact marker to show which case is in flight, the way a test runner's terminal shows the " +
			"currently-running spec; skipping it just means that case never shows as \"running\" live, so always include " +
			"it, one per case, right before you start that case. " +
			"The MOMENT a case finishes, output the line `QA_RESULT test_case:<id> pass` or `QA_RESULT test_case:<id> fail` " +
			"on its own — the panel flips that row's ✓/✗ live from this marker, before the final block persists; emit it " +
			"for every case right after you judge it. " +
			"For EACH case: if the case LISTING below includes a COMPILED SCRIPT for that id, do NOT drive the browser " +
			"action-by-action — instead WRITE that script verbatim to a temp file `/tmp/case-<id>.mjs` and RUN it with " +
			"`mkdir -p \"$HOME/.agora/qa-traces\" && TRACE_PATH=\"$HOME/.agora/qa-traces/trace-<id>.zip\" node /tmp/case-<id>.mjs`; " +
			"take the process EXIT CODE as the verdict (0 = pass, " +
			"non-zero = fail) and use the script's stdout/stderr as the one-line `output` evidence. This is deterministic and " +
			"needs no per-action reasoning — that is the whole point. TRACE (time-travel debugging): the compiled script " +
			"records a Playwright trace (DOM snapshots + screenshots + sources per step) to the `TRACE_PATH` you set here, so " +
			"a QA reviewer can replay the run step-by-step in-app. Give each case a DISTINCT `TRACE_PATH` keyed by its id " +
			"(`$HOME/.agora/qa-traces/trace-<id>.zip`) so concurrent cases never overwrite each other's trace — NEVER " +
			"under /tmp: the OS purges it and the in-app trace viewer replays these files days later. After the run, if that trace " +
			"file exists, report its ABSOLUTE path as the case's `trace_path` in the test-runs JSON below; omit `trace_path` " +
			"when no trace was produced. Playwright must be available to `node`: if `node -e \"import('playwright')\"` " +
			"fails, run ONCE `npm i playwright && npx playwright install chromium-headless-shell` in the box (reuse the box's " +
			"existing install when present — do not reinstall per case). Still emit the `RUNNING test_case:<id>` marker before " +
			"each scripted case. ONLY cases with NO compiled script are hand-driven the old way (deterministic HTTP/DOM smoke " +
			"or the embedded browser) — those produce no trace. " +
			"Then, for EACH case, drive its steps against the " +
			"running app — a deterministic HTTP / DOM-text smoke, or the embedded browser; NEVER an external playwright/" +
			"chrome — and judge the EXPECTED result by SIGNAL (status code, DOM text, exit code), never by opinion. Do NOT " +
			"modify code. At the END of your comment, append a fenced ```test-runs code block with ONLY a JSON array the QA " +
			"panel parses: `[{\"test_case_id\":\"<the id from the list>\",\"status\":\"pass\"|\"fail\"|\"blocked\"," +
			"\"output\":\"<one-line evidence — for fail/blocked this IS the human-readable reason shown to the QA " +
			"reviewer, e.g. the failing assertion or HTTP status; for pass, what you observed>\",\"trace_path\":\"<optional: " +
			"the ABSOLUTE path of the Playwright trace .zip this case produced (expand $HOME yourself); omit when no " +
			"trace was captured (hand-driven cases)>\",\"baseline_status\":\"pass\"|\"fail\"|\"unknown\"}]` — one entry per case " +
			"you ran. Use `blocked` if a case could not be exercised (missing data/route). The JSON must be valid and " +
			"self-contained. " +
			"BASELINE DISCRIMINATION — a plan-driven test only proves your change if it FAILS on the pre-change code and " +
			"PASSES after (fail-before / pass-after). For EACH case that has a COMPILED SCRIPT, ALSO run that SAME script " +
			"against the pre-change BASELINE (check out the merge-base — or the sprint last-green ref when a sprint context " +
			"is given below — run the script, then return to the branch), and report `baseline_status`: `fail` if it failed " +
			"on the baseline (GOOD — it discriminates your change), `pass` if it passed there too. A case that is `pass` on " +
			"BOTH baseline and branch is NON-DISCRIMINATING — it proves nothing about your change (tautological / " +
			"happy-path / testing-the-code-not-the-spec); do not rely on it as evidence, strengthen it to fail-before. " +
			"Report `baseline_status:\"unknown\"` for hand-driven / [e2e] / [smoke] cases you cannot re-run against a " +
			"baseline (they stay advisory). Restore the branch checkout before finishing."
	case sliceActionCompileTests:
		base = "COMPILE this project's automated QA test cases into runnable Playwright scripts — you are the QA " +
			"Squad's automation engineer. The cases that STILL NEED a script (id · title · steps · expected) are listed " +
			"below, along with the PROJECT QA MANIFEST (base_url, auth, routes, flows). For EACH case, author a COMPLETE, " +
			"self-contained Playwright ESM module that runs with plain `node`: `import { chromium } from \"playwright\";`. " +
			"BROWSER — PREFER the SHARED review browser so a QA reviewer WATCHES the run live in the review page's pane: " +
			"when `process.env.AGORA_DAEMON_PORT` is set, POST `http://127.0.0.1:${process.env.AGORA_DAEMON_PORT}/editor/browser/start` " +
			"with body `{\"workdir\":\"qa-target:<THE MANIFEST base_url>\"}` (fetch/http), read `cdp_url` from the JSON, then " +
			"`const browser = await chromium.connectOverCDP(cdp_url); const context = browser.contexts()[0] ?? await browser.newContext(); " +
			"const page = context.pages()[0] ?? await context.newPage();`. Use the EXACT `qa-target:<base_url>` key (the manifest base_url) so you " +
			"share ONE browser with the reviewer's pane and they see your actions live. Fall back to " +
			"`const browser = await chromium.launch(); const context = await browser.newContext();` ONLY if AGORA_DAEMON_PORT is unset or that POST fails. " +
			"Open pages from THAT context, log in via the manifest auth, perform the steps " +
			"against the manifest base_url/routes, ASSERT the expected result by deterministic signal " +
			"(DOM / accessibility-tree TEXT via `page.locator(...)`, HTTP status, or response shape — never a screenshot). " +
			"BROWSER-DRIVE UI CASES — REQUIRED: if the case verifies the RENDERED UI (it renders / clicks / fills / " +
			"navigates / logs in / checks a visible element — typically titled `[e2e]`), you MUST actually drive the page " +
			"(`page.goto(route)`, `page.fill/click/waitForSelector`, assert via `page.locator(...).textContent()` / " +
			"`.isVisible()`). Do NOT shortcut a UI case with a raw `fetch()` of the HTML or a filesystem/git check — a real " +
			"browser interaction is what lets the reviewer WATCH it live in the pane AND what actually exercises the UI. " +
			"ONLY a pure API/data case (titled `[api]`, asserting an endpoint's status / JSON with no rendered UI) may use " +
			"`fetch`/HTTP with no page navigation. Every `[e2e]` case opens a page on the connected browser. " +
			"Then `process.exit(0)` on pass / `process.exit(1)` on any failed assertion or thrown error " +
			"(try/catch → exit(1)). In finally: stop tracing (below); then, ONLY if you launched your own browser, " +
			"`await browser.close()`. If you connected to the SHARED browser over CDP, do NOT close it or its context " +
			"(the daemon owns it) — `connectOverCDP`'s browser.close() only disconnects, so either skip it or guard it on the launched path. " +
			"TRACING (so a reviewer can time-travel the run in Agora): the script MUST honor `process.env.TRACE_PATH` — " +
			"when it is set, call `await context.tracing.start({ screenshots: true, snapshots: true, sources: true });` " +
			"right after creating the context, and in the `finally` block call " +
			"`await context.tracing.stop({ path: process.env.TRACE_PATH });` BEFORE closing the browser (guard both on " +
			"`process.env.TRACE_PATH` so the script still runs when it's unset). Do NOT run anything and do NOT touch " +
			"product code — only " +
			"AUTHOR the scripts. At the END of your comment, append a fenced ```scripts code block containing ONLY a JSON " +
			"array the server parses: `[{\"id\":\"<the case id from the list>\",\"script\":\"<the full Playwright module>\"}]` " +
			"— one entry per case you compiled. The JSON must be valid and self-contained."
	case sliceActionDesignProposal:
		base = "You are acting as a DESIGNER-ANALYST. Analyze the design(s) linked from this issue against this " +
			"project's existing design system and produce a decomposition proposal for a human to approve. Do NOT " +
			"write implementation code and do NOT create issues — you only READ, ANALYZE, and PROPOSE. " +
			"(1) READ: for each Figma link referenced by this issue (listed in your context), call " +
			"get_figma_data(fileKey, nodeId) NODE-SCOPED — never fetch a whole file. Download a PNG render of each " +
			"top-level frame with download_figma_images (pngScale=2), name each file `figma-<node-id-with-dashes>.png` " +
			"(e.g. node 208:5147 → figma-208-5147.png), and UPLOAD them as attachments on your reply comment (Figma " +
			"render URLs expire — never hot-link them). " +
			"(2) INVENTORY: list every distinct screen / state — name, Figma node id, one-line purpose, and the visible " +
			"elements, INCLUDING empty / loading / error states, not just the happy path. " +
			"(3) MAP against the PROJECT DESIGN SYSTEM context below. If none is provided, first inspect the " +
			"repository READ-ONLY (do not push, do not open a PR) to enumerate existing components / partials / shared " +
			"styles. Classify EVERY element as REUSE (name the exact existing component / file), EXTEND (an existing " +
			"component plus what must change), or NEW (justify why nothing existing fits). Prefer reuse aggressively — " +
			"on a legacy codebase, matching the existing app beats matching the mock pixel-for-pixel. " +
			"(4) FLAG DEVIATIONS: a Figma value that contradicts the project's tokens / conventions (a one-off color, " +
			"an off-scale spacing, a font the system doesn't use) is a QUESTION for the human, never a silent decision. " +
			"(5) PROPOSE SUB-ISSUES: one per coherent, independently shippable slice, each with a title, a 2-4 sentence " +
			"description that EMBEDS its Figma URL(s) with node-ids and the component decisions that apply, and a " +
			"`depends_on` list of the indices of sibling sub-issues that must ship first. " +
			"(6) OUTPUT a concise human-readable summary WRITTEN IN THE SAME LANGUAGE AS THE ISSUE DESCRIPTION, then " +
			"exactly ONE fenced ```design-proposal code block containing ONLY a JSON object (schema below). JSON keys " +
			"stay in English; free-text field VALUES follow the issue's language. " +
			"(7) If any Figma link is inaccessible (403 / 404) or you are quota-blocked after honoring Retry-After once, " +
			"emit the block with `status:\"blocked\"` and a machine-readable `reason` — a blocked proposal is a valid, " +
			"useful output. NEVER fabricate design content you could not read. " +
			"The ```design-proposal block schema: " +
			"`{\"status\":\"ok\"|\"blocked\",\"reason\":null|\"figma_forbidden\"|\"figma_not_found\"|\"figma_quota\"|" +
			"\"credential_missing\"|\"other\",\"reason_detail\":\"<short>\",\"figma\":[{\"url\":\"\",\"file_key\":\"\"," +
			"\"node_id\":\"\"}],\"screens\":[{\"name\":\"\",\"figma_node_id\":\"\",\"summary\":\"\",\"render\":" +
			"\"figma-208-5147.png\"}],\"components\":[{\"name\":\"\",\"verdict\":\"reuse\"|\"extend\"|\"new\"," +
			"\"code_ref\":null|\"<path>\",\"figma_node_id\":null|\"\",\"notes\":\"\"}],\"deviations\":[{\"aspect\":" +
			"\"color\"|\"typography\"|\"spacing\"|\"other\",\"figma_value\":\"\",\"project_value\":\"\",\"question\":" +
			"\"\"}],\"sub_issues\":[{\"title\":\"\",\"description\":\"\",\"screens\":[\"\"],\"node_ids\":[\"\"]," +
			"\"depends_on\":[0]}],\"open_questions\":[\"\"]}`. The JSON must be valid and self-contained. Budget: one " +
			"structured read per frame, one batched image download — stay within the Figma rate budget. " +
			"BOOTSTRAP: if NO PROJECT DESIGN SYSTEM context was provided below, first derive one from the repo " +
			"(read-only): detect tokens vs a legacy inventory, enumerate the shared components, and emit ONE fenced " +
			"```design-manifest block (kind/tokens/components/conventions/anti_patterns/legacy_notes) BEFORE your " +
			"proposal — the platform captures it onto the project so future runs are faster."
	case sliceActionGenDesignManifest:
		base = "Build or refresh this project's DESIGN MANIFEST — the project's known design-system map that is " +
			"injected into every designer + implementation run so agents build against the KNOWN system instead of " +
			"re-discovering it. Work AUTONOMOUSLY (no questions) and inspect the repository READ-ONLY (do not push, do " +
			"not open a PR). " +
			"(1) REPO CENSUS: detect the stack. TOKEN-BASED repos (tokens.css / a tailwind or theme config / CSS custom " +
			"properties): read the token files and enumerate the shared component library → set kind=\"tokens\". " +
			"LEGACY MONOLITHS (PHP/Yii + Vue like sd-main — no formal token system): DERIVE the de-facto one — enumerate " +
			"the Vue SFCs, Yii widgets/partials, and layout templates; frequency-rank the top ~20 colors, font stacks, " +
			"and spacing values from the shared CSS as de-facto tokens; record the conventions and anti-patterns; write " +
			"honest legacy_notes (e.g. 'no tokens — copy markup from protected/views/...') → set kind=\"inventory\". " +
			"(2) FIGMA CENSUS (only if a library file key is configured in your context): read the published styles + " +
			"component names NODE-SCOPED and map them to repo components by name similarity; leave figma_node_id blank " +
			"when unsure — never invent a mapping. Do NOT attempt the Figma Variables API (enterprise-only). " +
			"(3) OUTPUT exactly ONE fenced ```design-manifest code block containing ONLY a JSON object with this shape: " +
			"`{\"kind\":\"tokens\"|\"inventory\",\"figma\":{\"library_file_key\":\"\",\"notes\":\"\"},\"tokens\":" +
			"{\"colors\":{\"name\":\"#hex\"},\"typography\":{\"name\":\"…\"},\"spacing\":{\"name\":\"…\"}},\"components\":" +
			"[{\"name\":\"\",\"code_ref\":\"path\",\"figma_node_id\":null|\"\",\"usage\":\"\"}],\"conventions\":[\"\"]," +
			"\"anti_patterns\":[\"\"],\"legacy_notes\":\"\",\"screens_reference\":\"\"}`. Keep it UNDER ~150 lines — this " +
			"is a MAP injected into prompts, not documentation. The existing manifest (if any) is in your context: " +
			"UPDATE it and PRESERVE any human-added entries. The server captures this block onto the project; you do " +
			"NOT need to run any command."
	case sliceActionDesignAudit:
		base = "AUDIT this project's design-system HEALTH — find where the code diverges from the design system and " +
			"where the de-facto system should be formalized, so the team can BUILD a real design system out of a " +
			"legacy codebase. Inspect the repository READ-ONLY (do not push, do not open a PR). The PROJECT DESIGN " +
			"SYSTEM manifest (if any) is in your context below — audit AGAINST it. " +
			"(1) OFF-TOKEN VALUES: find hardcoded values that SHOULD be design tokens — raw hex/rgb colors, off-scale " +
			"spacing (px values that don't fit a 4/8px scale), one-off font sizes/families. Frequency-rank them: a " +
			"color hardcoded in 14 places is a missing token; a color used once is a smell. For each, suggest the token " +
			"name it maps to (an existing manifest token, or a proposed new one) and cite a few sample file refs. " +
			"(2) DUPLICATED MARKUP: find the SAME UI structure copy-pasted across files (a table, a card, a modal, a " +
			"form row) that should be ONE shared component — cite occurrences and a suggested component name. " +
			"(3) UNMANAGED COMPONENTS: shared components that exist in code but are NOT in the manifest (the manifest is " +
			"blind to them). (4) PROPOSED TOKENS: from the frequency-ranked off-token colors/spacing, propose the " +
			"concrete token set the project should adopt (name + value + the raw values it would replace) — this is the " +
			"seed of a real tokens file. Prefer the FEWEST tokens that cover the most usage. " +
			"OUTPUT a human-readable summary IN THE SAME LANGUAGE AS THE ISSUE, then exactly ONE fenced " +
			"```design-audit code block (JSON keys English, free-text in the issue's language): " +
			"`{\"summary\":\"\",\"off_token\":[{\"kind\":\"color\"|\"spacing\"|\"typography\",\"value\":\"\"," +
			"\"occurrences\":0,\"suggested_token\":\"\",\"sample_refs\":[\"path:line\"]}],\"duplicates\":[{\"pattern\":" +
			"\"\",\"occurrences\":0,\"suggested_component\":\"\",\"sample_refs\":[\"\"]}],\"unmanaged_components\":" +
			"[{\"name\":\"\",\"code_ref\":\"\",\"note\":\"\"}],\"proposed_tokens\":[{\"name\":\"\",\"value\":\"\"," +
			"\"replaces\":[\"\"]}]}`. Report only REAL findings you saw in the code — never invent counts or refs. The " +
			"JSON must be valid and self-contained. You do NOT change code; a human turns the proposed tokens into a " +
			"`draft_code` task if they accept them."
	default:
		return ""
	}

	// LIVE WATCH — for the browser-driving QA actions, connect to the SHARED
	// review browser instead of launching a private one, so a QA reviewer watches
	// the run in real time in the review page's live pane (they attach to the
	// SAME Chromium). The agent already has AGORA_DAEMON_PORT in its env; the
	// daemon serves the shared browser on that port keyed by workdir. The pane
	// keys it `qa-target:<previewUrl>`, so the agent must use the same key (the QA
	// target base URL it is testing) to share one browser. connectOverCDP coexists
	// with the pane's screencast and with trace capture on the same context
	// (verified). Hand-driven HTTP/DOM smokes that open no browser skip this.
	if kind == sliceActionRunQA || kind == sliceActionRunTests {
		base += qaLiveWatchClause
	}

	// LANGUAGE PARITY — QA verdicts and test cases are read by the project's
	// human QA team, who work in the issue's language (SalesDoctor tickets
	// arrive from Bitrix in Russian/Uzbek). English-only agent output makes the
	// loop unusable for them. Mirrors the design actions' existing same-language
	// clause. Code, commands, JSON keys, and fenced-block schemas stay English —
	// only the human-readable prose follows the issue. auto_docs gets its own
	// clause: the ISSUE COMMENT follows the issue's language, but doc PAGES obey
	// the docs repo's canonical locale (its template already demands matching
	// neighboring pages — forcing issue-language pages would contradict that).
	switch kind {
	case sliceActionRunQA, sliceActionRunCI, sliceActionGenTests, sliceActionRunTests, sliceActionCompileTests:
		base += " LANGUAGE: write every human-readable output — the verdict/report comment, test-case titles, steps and " +
			"expected results, and summaries — IN THE SAME LANGUAGE AS THE ISSUE (its title/description, " +
			"e.g. Russian or Uzbek). Code, shell commands, JSON keys, label names, and fenced-block schemas stay in English."
	case sliceActionAutoDocs:
		base += " LANGUAGE: write the summary COMMENT you post on the issue in the SAME LANGUAGE AS THE ISSUE " +
			"(e.g. Russian or Uzbek). The documentation PAGES themselves follow the docs repo's canonical locale and " +
			"existing conventions — never switch a page's language to match the issue."
	}

	scope = strings.TrimSpace(scope)
	if scope != "" {
		base += " Focus on: " + scope
	}
	return base
}

// qaLiveWatchClause tells a browser-driving QA run to attach to the shared
// review browser (see buildSliceInstruction) so the reviewer watches live.
const qaLiveWatchClause = " LIVE WATCH (so a QA reviewer can watch you drive the browser in real time): when you drive a real browser, do NOT launch your own — attach to the SHARED review browser. With AGORA_DAEMON_PORT set, POST http://127.0.0.1:$AGORA_DAEMON_PORT/editor/browser/start with body {\"workdir\":\"qa-target:<THE QA TARGET BASE URL you are testing, e.g. the manifest base_url>\"}; it returns {\"cdp_url\":\"http://127.0.0.1:<port>\"}. Then in your Playwright script use `const browser = await chromium.connectOverCDP(cdp_url); const context = browser.contexts()[0] ?? await browser.newContext(); const page = context.pages()[0] ?? await context.newPage();` INSTEAD of chromium.launch(). Use the SAME `qa-target:<url>` key the review pane uses (the exact QA target base URL) so you and the reviewer share ONE browser and they see your actions live. Tracing (TRACE_PATH) still works on this connected context. Fall back to chromium.launch() ONLY if that POST fails or AGORA_DAEMON_PORT is unset."

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

// qaManifestRoute is one named path in the project QA manifest.
type qaManifestFlow struct {
	Name   string   `json:"name"`
	Path   string   `json:"path"`
	Steps  []string `json:"steps"`
	Assert string   `json:"assert"`
}

// qaManifest is the project's KNOWN navigation + golden-path map (stored in
// project.settings.qa_manifest). It exists so a QA/test agent goes STRAIGHT to
// the right page and flow instead of exploring the app by hand every run — the
// slow part of driving a Chromium against a big legacy monolith. Authored ONCE
// per project (from the app + docs) and reused by every QA run.
type qaManifest struct {
	BaseURL string `json:"base_url"`
	Auth    struct {
		LoginPath       string `json:"login_path"`
		UserField       string `json:"user_field"`
		PassField       string `json:"pass_field"`
		Username        string `json:"username"`
		Password        string `json:"password"`
		SuccessContains string `json:"success_contains"`
	} `json:"auth"`
	Routes map[string]string `json:"routes"`
	Flows  []qaManifestFlow  `json:"flows"`
	// KnownIssues are pre-existing dead routes / disabled modules / standing
	// server errors on the QA target. Injected so an agent neither wastes a
	// run exploring them nor fails a task on a failure that predates it.
	KnownIssues []string `json:"known_issues"`
	// Notes carry target-specific ground rules (rendering model, selector
	// conventions, role of the QA account) that don't fit routes/flows.
	Notes string `json:"notes"`
	// CrossManifests point at INTEGRATION manifests (docs repo qa-manifest/
	// <a>--<b>.json) for flows that span this project and a partner system —
	// e.g. sd-cs (supplier) ↔ sd-main (distributor) data exchange. Injected as
	// an explicit "read this file" pointer so an integration QA run doesn't
	// depend on the agent stumbling onto it while browsing the docs repo.
	CrossManifests []qaCrossManifest `json:"cross_manifests"`
	// Accounts are ADDITIONAL role-specific logins for flows the default Auth
	// account can't reach — e.g. an agent / ROLE=4 account for endpoints that
	// reject the admin login ("войдите под аккаунтом агента"). The default Auth
	// stays the primary login; the agent picks the account whose role matches
	// the case it is exercising.
	Accounts []qaManifestAccount `json:"accounts"`
}

// qaCrossManifest names a partner project + the docs-repo integration manifest
// documenting the seam between the two (shared entities + exchange endpoints +
// cross-system flows). Doc is a path within the project's docs_repo.
type qaCrossManifest struct {
	Partner string `json:"partner"` // e.g. "sd-cs"
	Doc     string `json:"doc"`     // e.g. "qa-manifest/sd-main--sd-cs.json"
	Summary string `json:"summary"` // one line: what the two exchange
}

// qaManifestAccount is one role-specific QA login (see qaManifest.Accounts).
type qaManifestAccount struct {
	Role     string `json:"role"` // human label, e.g. "agent (ROLE=4)"
	Username string `json:"username"`
	Password string `json:"password"`
	Note     string `json:"note"` // when to use it, e.g. "for /api3/stock/*"
}

// sliceActionQAManifestContext injects the project QA manifest + critical paths
// so the agent NAVIGATES by a known map (routes, auth, golden flows) instead of
// exploring — the single biggest speed win for QA on a large legacy app. Also
// folds in the previously-dead `qa_critical_paths` config. Returns "" when the
// project configures neither.
func (h *Handler) sliceActionQAManifestContext(ctx context.Context, issue db.Issue) string {
	if !issue.ProjectID.Valid {
		return ""
	}
	project, err := h.Queries.GetProject(ctx, issue.ProjectID)
	if err != nil || len(project.Settings) == 0 {
		return ""
	}
	// Parse qa_manifest and qa_critical_paths INDEPENDENTLY: a legacy/foreign
	// shape in one must never nuke the other. qa_critical_paths has shipped as
	// BOTH an array of {name,assert,why} objects AND a bare array of strings
	// (older seed) — a single combined struct unmarshal fails on the string
	// form and silently dropped the WHOLE manifest for every affected project
	// (found live: sd-main). Decoupled + shape-tolerant here (parse, don't
	// crash — the API Response Compatibility rule applies to our own settings
	// blob too).
	var settings struct {
		Manifest *qaManifest `json:"qa_manifest"`
	}
	_ = json.Unmarshal(project.Settings, &settings)
	criticalPaths := parseQACriticalPaths(project.Settings)
	// Inheritance: a project with no manifest of its own (e.g. a Bitrix-imported
	// sprint project that carries sd-main work but no repo/manifest) falls back
	// to the workspace-default project's manifest, so its QA runs still get a
	// navigation map instead of nothing. Own manifest always wins; the default
	// is only consulted when this project has none.
	inherited := false
	if settings.Manifest == nil || (settings.Manifest.Auth.LoginPath == "" && len(settings.Manifest.Routes) == 0 && len(settings.Manifest.Flows) == 0) {
		if def := h.defaultManifestForWorkspace(ctx, project.WorkspaceID, project.ID); def != nil {
			settings.Manifest = def
			inherited = true
		}
	}
	var b strings.Builder
	if m := settings.Manifest; m != nil && (m.Auth.LoginPath != "" || len(m.Routes) > 0 || len(m.Flows) > 0) {
		if inherited {
			b.WriteString(" PROJECT QA MANIFEST (INHERITED from the workspace's main project — this sub-project/sprint runs against the SAME app; use this navigation map).")
		} else {
			b.WriteString(" PROJECT QA MANIFEST — the app's navigation is KNOWN; go straight to these instead of exploring/auto-detecting (only fall back to discovery if a path 404s).")
		}
		if m.Auth.LoginPath != "" {
			b.WriteString(fmt.Sprintf(" AUTH: log in at %s%s with %s=%s and %s=%s", m.BaseURL, m.Auth.LoginPath, m.Auth.UserField, m.Auth.Username, m.Auth.PassField, m.Auth.Password))
			if m.Auth.SuccessContains != "" {
				b.WriteString(fmt.Sprintf("; success when the page contains %q", m.Auth.SuccessContains))
			}
			b.WriteString(".")
		}
		for _, a := range m.Accounts {
			b.WriteString(fmt.Sprintf(" ACCOUNT [%s]: log in at the same form with %s=%s and %s=%s", a.Role, m.Auth.UserField, a.Username, m.Auth.PassField, a.Password))
			if a.Note != "" {
				b.WriteString(" — use " + a.Note)
			}
			b.WriteString(". If a case needs this role and the account is not configured, mark it blocked (do NOT invent credentials).")
		}
		if len(m.Routes) > 0 {
			b.WriteString(" ROUTES:")
			for name, path := range m.Routes {
				b.WriteString(" " + name + "=" + path + ";")
			}
		}
		for _, f := range m.Flows {
			b.WriteString(" FLOW " + f.Name)
			if f.Path != "" {
				b.WriteString(" (go to " + f.Path + ")")
			}
			if len(f.Steps) > 0 {
				b.WriteString(" — steps: " + strings.Join(f.Steps, " → "))
			}
			if f.Assert != "" {
				b.WriteString("; assert: " + f.Assert)
			}
			b.WriteString(".")
		}
		if m.Notes != "" {
			b.WriteString(" NOTES: " + m.Notes)
		}
		if len(m.KnownIssues) > 0 {
			b.WriteString(" KNOWN ISSUES (pre-existing — do NOT test these paths and NEVER fail a task on them; report separately if relevant):")
			for _, k := range m.KnownIssues {
				b.WriteString(" " + k + ";")
			}
		}
		if len(m.CrossManifests) > 0 {
			b.WriteString(" INTEGRATION MANIFESTS — if this task touches a cross-system flow, READ the named file in the docs repo (it maps the shared entities, exchange endpoints, and end-to-end flows that span BOTH systems; verify both ends + the sync):")
			for _, c := range m.CrossManifests {
				b.WriteString(fmt.Sprintf(" %s ↔ %s: read %s", "this project", c.Partner, c.Doc))
				if c.Summary != "" {
					b.WriteString(" (" + c.Summary + ")")
				}
				b.WriteString(";")
			}
		}
	}
	if len(criticalPaths) > 0 {
		b.WriteString(" CRITICAL (daily-critical golden paths — always smoke these):")
		for _, c := range criticalPaths {
			if strings.TrimSpace(c.Assert) != "" {
				b.WriteString(" " + c.Name + " (assert: " + c.Assert + ");")
			} else {
				b.WriteString(" " + c.Name + ";")
			}
		}
	}
	return b.String()
}

// qaCriticalPath is one daily-critical golden path. name is always present;
// assert is optional (the string-array legacy form carries only a name).
type qaCriticalPath struct {
	Name   string
	Assert string
}

// parseQACriticalPaths reads settings.qa_critical_paths tolerantly: it accepts
// BOTH the rich [{name,assert,why}] object form and the legacy ["name", ...]
// string-array form, so neither shape breaks the caller (and neither can take
// the qa_manifest down with it). Returns nil on absence/garbage.
func parseQACriticalPaths(settingsRaw []byte) []qaCriticalPath {
	if len(settingsRaw) == 0 {
		return nil
	}
	var top struct {
		CP json.RawMessage `json:"qa_critical_paths"`
	}
	if json.Unmarshal(settingsRaw, &top) != nil || len(top.CP) == 0 {
		return nil
	}
	// Object form first.
	var objs []struct {
		Name   string `json:"name"`
		Assert string `json:"assert"`
	}
	if json.Unmarshal(top.CP, &objs) == nil {
		out := make([]qaCriticalPath, 0, len(objs))
		for _, o := range objs {
			if strings.TrimSpace(o.Name) != "" {
				out = append(out, qaCriticalPath{Name: o.Name, Assert: o.Assert})
			}
		}
		return out
	}
	// Legacy string-array form.
	var names []string
	if json.Unmarshal(top.CP, &names) == nil {
		out := make([]qaCriticalPath, 0, len(names))
		for _, n := range names {
			if strings.TrimSpace(n) != "" {
				out = append(out, qaCriticalPath{Name: n})
			}
		}
		return out
	}
	return nil
}

// defaultManifestForWorkspace loads the workspace-default project's qa_manifest
// (labs.qa_default_manifest_project) for a project that has none of its own.
// Returns nil when no default is configured, it points at THIS project (no
// self-inherit), or the default project has no usable manifest. Best-effort.
func (h *Handler) defaultManifestForWorkspace(ctx context.Context, workspaceID, selfProjectID pgtype.UUID) *qaManifest {
	ws, err := h.Queries.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return nil
	}
	defID := strings.TrimSpace(util.ParseWorkspaceLabs(ws.Settings).QADefaultManifestProject)
	if defID == "" {
		return nil
	}
	defUUID, perr := util.ParseUUID(defID)
	if perr != nil || defUUID.Bytes == selfProjectID.Bytes {
		return nil // unset / self — nothing to inherit
	}
	def, derr := h.Queries.GetProject(ctx, defUUID)
	if derr != nil || len(def.Settings) == 0 {
		return nil
	}
	var s struct {
		Manifest *qaManifest `json:"qa_manifest"`
	}
	if json.Unmarshal(def.Settings, &s) != nil || s.Manifest == nil {
		return nil
	}
	if s.Manifest.Auth.LoginPath == "" && len(s.Manifest.Routes) == 0 && len(s.Manifest.Flows) == 0 {
		return nil
	}
	return s.Manifest
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

// sliceActionQADocsContext is the READ-side counterpart to
// sliceActionDocsRepoContext: auto_docs WRITES the project's docs repo after a
// pass; this tells run_qa / run_test_cases / gen_test_cases to READ it (and the
// already-injected workspace.context system prompt) as REAL context BEFORE
// judging — the documented/expected behavior, not just the ticket's acceptance
// criteria, which the implementer wrote and may have gotten wrong. Returns ""
// when the project has no docs_repo configured (workspace.context alone is
// already part of every agent's brief regardless of this helper, so there is
// nothing to add when the per-project repo is unset).
func (h *Handler) sliceActionQADocsContext(ctx context.Context, issue db.Issue) string {
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
	repo := strings.TrimSpace(settings.DocsRepo)
	if repo == "" {
		return ""
	}
	return " PROJECT DOCUMENTATION: this project's documentation repository is " + repo +
		" — clone/read it (and the Workspace Context above, when present) as the REAL source of " +
		"intended behavior, alongside this issue's acceptance criteria. When the implementation, the " +
		"acceptance criteria, and the documented behavior disagree, treat the DISAGREEMENT itself as " +
		"something to surface in your verdict/comment (do not silently pick one to trust) — note which " +
		"one the code actually matches and which it contradicts, so the human resolves it."
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
	return config.Bool("AGORA_AUTO_DOCS_ENABLED")
}

// autoQAEnabled gates the in_review → run_qa auto-trigger (the QA squad smokes a
// dev's work the moment it's ready for review). Default off — opt-in.
func autoQAEnabled() bool {
	return config.Bool("AGORA_AUTO_QA_ENABLED")
}

// sprintWorktreeEnabled gates the shared-sprint-branch worktree model
// (worktree-per-task on one sprint branch, so N users work one sprint branch in
// parallel each with their own co-editor). Default OFF — the per-task fork model
// stays the default and the migration is fully reversible by unsetting the flag.
// See docs/sprint-worktree-design.md.
func sprintWorktreeEnabled() bool {
	return config.Bool("AGORA_SPRINT_WORKTREE_ENABLED")
}

// sprintPRModeEnabled gates the per-task-PR-into-the-sprint-branch dev flow
// (Phase 1 of auto sprint review): a sprint task opens a PR from its own branch
// into the sprint branch — for the squad lead to review + merge — instead of
// committing straight onto the shared branch. Mirrors the daemon-side gate of the
// same name; both read AGORA_SPRINT_PR_MODE so the agent instruction path and the
// co-code accept path switch together. Default OFF → direct-commit sprint mode.
// Only meaningful when sprintWorktreeEnabled() + the project is in sprint mode.
func sprintPRModeEnabled() bool { return util.SprintPRModeEnabled() }

// sprintAutoMergeEnabled gates whether the squad LEAD auto-merges a sprint PR
// once it passes QA (Phase 3). Default OFF: the lead prepares the PR and QA
// gates it, but a HUMAN does the final review + merge into the sprint branch —
// the safe default while the loop is being trusted. Set on to let the lead run
// `gh pr merge` fully autonomously. Only meaningful with sprintPRModeEnabled().
func sprintAutoMergeEnabled() bool {
	v := strings.TrimSpace(os.Getenv("AGORA_SPRINT_AUTO_MERGE"))
	return v == "1" || strings.EqualFold(v, "true")
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

// projectSprintModeEnabled reads the project's `sprint_mode` flag
// (project.settings.sprint_mode). In sprint mode ALL of a sprint's work lands on
// one shared sprint branch and NO per-task PR is opened — the branch is reviewed
// and merged to base once, by a human, at sprint end. Unset defaults to false
// here (the caller ALSO requires sprintWorktreeEnabled() as the workspace
// kill-switch and the issue actually being in a sprint), so a project that never
// opted in keeps the per-task branch + PR flow.
func (h *Handler) projectSprintModeEnabled(ctx context.Context, issue db.Issue) bool {
	if !issue.ProjectID.Valid {
		return false
	}
	project, err := h.Queries.GetProject(ctx, issue.ProjectID)
	if err != nil || len(project.Settings) == 0 {
		return false
	}
	var s struct {
		SprintMode bool `json:"sprint_mode"`
	}
	if json.Unmarshal(project.Settings, &s) != nil {
		return false
	}
	return s.SprintMode
}

// sliceActionSprintContext resolves the shared sprint branch a PR-producing slice
// must commit to instead of opening a per-task PR. ok=true only when the whole
// sprint-mode contract holds: the workspace kill-switch is on, the project is in
// sprint mode, the issue is IN a sprint, and that sprint resolves a branch. When
// ok, the caller emits sprintCommitInstruction(branch) and SKIPS the per-task
// branch/PR instruction; when not, the per-task PR flow is unchanged.
func (h *Handler) sliceActionSprintContext(ctx context.Context, issue db.Issue) (string, bool) {
	if !sprintWorktreeEnabled() || !h.projectSprintModeEnabled(ctx, issue) {
		return "", false
	}
	sprint, err := h.Queries.GetSprintForIssue(ctx, issue.ID)
	if err != nil {
		return "", false
	}
	branch := SprintBranchFor(sprint)
	if branch == "" {
		return "", false
	}
	return branch, true
}

// sprintCommitInstruction is the pure "commit to the shared sprint branch, no PR"
// directive. It is appended AFTER (and explicitly SUPERSEDES) the slice base
// wording that tells the agent to open a pull request, so a sprint-mode dev task
// lands on the one shared branch instead of forking a per-task PR.
func sprintCommitInstruction(branch string) string {
	return " SPRINT MODE — DO NOT OPEN A PULL REQUEST. This project is running a sprint: every task's work lands on the ONE shared sprint branch `" + branch +
		"`, which is already checked out in your worktree. Commit your change directly to `" + branch + "` and push it there. Do NOT create a new branch, and do NOT open a pull/merge request. " +
		"This SUPERSEDES any 'open a pull request' wording above. The whole sprint branch is reviewed and merged to the base branch ONCE, by a human, at sprint end."
}

// sprintPRInstruction is the sprint-PR-mode dev directive (AGORA_SPRINT_PR_MODE
// on): the task opens a PR from its OWN branch into the sprint branch, rather than
// committing straight onto it. The worktree is already on a per-task branch that
// tracks the sprint branch (pulled to its latest tip), so the agent just commits,
// pushes ITS branch, and opens a PR with base=<sprint branch>. The squad lead
// reviews + merges it after QA — the agent must not merge or target main.
func sprintPRInstruction(branch string) string {
	return " SPRINT MODE (PR REVIEW) — open a pull request INTO the sprint branch `" + branch +
		"`; do NOT push onto it directly. Steps, exactly: (1) make sure the sprint branch is fresh — `git fetch origin " + branch +
		"`; (2) create your feature branch OFF the sprint branch — `git checkout -B <feature> origin/" + branch +
		"` (a name like `fix/<issue-key>-<slug>`); (3) do your work and commit it to that feature branch; (4) push it as ITS OWN branch — `git push -u origin <feature>` — NOT onto `" + branch +
		"`; (5) open the PR with BASE `" + branch + "` — `gh pr create --base " + branch + " --head <feature> --fill`. " +
		"CRITICAL: the PR base MUST be exactly `" + branch + "` (the shared sprint branch). Your worktree may sit on a per-task `sprint-wt-*` alias — NEVER open the PR against that alias or the repo's main/default branch. After creating it, VERIFY: `gh pr view <pr> --json baseRefName` must show `" + branch +
		"`; if it shows anything else (a `sprint-wt-*` alias, main, etc.) FIX it immediately — `gh pr edit <pr> --base " + branch + "`. " +
		"Do NOT merge the PR yourself — it is reviewed and merged after QA. This SUPERSEDES any other branch/PR wording above."
}

// sliceActionLandingInstruction returns the "where does this task's code land"
// directive appended to a PR-producing slice action, picking the model in one
// place so it is testable: sprint-PR-mode (PR into the sprint branch, flag on),
// sprint direct-commit (flag off), or — for a non-sprint issue — the default
// per-task branch + PR-into-main.
func (h *Handler) sliceActionLandingInstruction(ctx context.Context, issue db.Issue) string {
	if branch, ok := h.sliceActionSprintContext(ctx, issue); ok {
		if sprintPRModeEnabled() {
			instr := sprintPRInstruction(branch)
			// Tell the dev its landing mode up front: critical/guarded work
			// NEVER auto-merges (risk-map policy), so it plans for a human
			// review instead of expecting the lead to land it on qa:pass.
			if tier := h.issueRiskTier(ctx, issue); tier == "critical" || tier == "guarded" {
				instr += " LANDING MODE: this issue is RISK TIER " + strings.ToUpper(tier) +
					" — after qa:pass a HUMAN reviews and merges your PR (auto-merge is refused for this tier); " +
					"keep the PR small and reviewable."
			}
			return instr
		}
		return sprintCommitInstruction(branch)
	}
	return h.sliceActionBranchInstruction(ctx, issue)
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
	instruction := buildSliceInstruction(sliceActionAutoDocs, "") + docsCtx + h.sliceActionQAManifestContext(ctx, issue)
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

// qaFailAutorouteEnabled gates the qa:fail -> dev-lead auto-reassignment.
// Default off — opt-in, matching every other auto-* gate in this file.
func qaFailAutorouteEnabled() bool {
	return config.Bool("AGORA_QA_FAIL_AUTOROUTE_ENABLED")
}

// qaGateEnforced gates the STRUCTURAL QA gate: when on, a squad-orchestrated
// issue cannot jump straight to `done` without a QA sign-off — a direct
// →done transition is redirected to →in_review (which fires the QA lead via
// maybeRunQAOnInReview) unless the issue already carries qa:pass. This turns
// "the dev lead and QA lead are always in communication" from an instruction
// the leader might omit into a platform guarantee. Default off — opt-in,
// matching every other auto-* gate in this file.
func qaGateEnforced() bool {
	return config.Bool("AGORA_QA_GATE_ENFORCED")
}

// qaDiscriminationEnforced gates the TEST-ACCURACY guard: when on, a qa:pass is
// not enough to reach done — the issue must ALSO carry at least one plan-driven
// test that DISCRIMINATES the change (passed on the branch, FAILED on the
// pre-change baseline). This blocks a tautological / circular / happy-path-only
// test (green on both baseline and branch) from certifying buggy code. Default
// off — opt-in, fail-safe: with no baseline data (all runs "unknown") the guard
// simply doesn't apply unless a project deliberately turns it on.
func qaDiscriminationEnforced() bool {
	return config.Bool("AGORA_QA_DISCRIMINATION_ENFORCED")
}

// riskTierGateEnforced gates the RISK-TIER human-sign-off guard: when on, a
// CRITICAL-tier issue can only be moved to done by a HUMAN — an agent's own
// transition (even with a self-attached qa:pass) is held at in_review for human
// review. This makes the risk_map's documented "critical → human review
// mandatory" invariant a real gate, not just advisory prompt text. Default off;
// fail-open on a tier-lookup error (never block on infra failure). A human actor
// is never blocked, so turning it on can only ADD safety, never wedge a human.
func riskTierGateEnforced() bool {
	return config.Bool("AGORA_RISK_TIER_GATE_ENFORCED")
}

// issueDevOrchestrated reports whether the issue's dev-side assignee is
// squad-managed — assigned straight to a squad, or to an agent that belongs
// to at least one squad. This is the signal that the work is run by a lead
// orchestrator (dev lead) rather than a solo agent, so both the auto-QA
// leader routing and the structural QA gate key off it.
func (h *Handler) issueDevOrchestrated(ctx context.Context, issue db.Issue) bool {
	if !issue.AssigneeType.Valid {
		return false
	}
	switch issue.AssigneeType.String {
	case "squad":
		return true
	case "agent":
		if !issue.AssigneeID.Valid {
			return false
		}
		squads, err := h.Queries.ListSquadsByMember(ctx, db.ListSquadsByMemberParams{
			WorkspaceID: issue.WorkspaceID, MemberType: "agent", MemberID: issue.AssigneeID,
		})
		return err == nil && len(squads) > 0
	}
	return false
}

// issueHasLabel reports whether the issue currently carries the named label
// (case-insensitive match). Best-effort: a query error reports false.
func (h *Handler) issueHasLabel(ctx context.Context, issue db.Issue, name string) bool {
	labels, err := h.Queries.ListLabelsByIssue(ctx, db.ListLabelsByIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return false
	}
	want := strings.ToLower(strings.TrimSpace(name))
	for _, l := range labels {
		if strings.ToLower(strings.TrimSpace(l.Name)) == want {
			return true
		}
	}
	return false
}

// enforceQAGateBeforeDone decides the status a write should actually land on.
// When the QA gate is enforced and a squad-orchestrated issue is being moved
// directly into `done` without a qa:pass sign-off — and it isn't already in
// in_review — the target is rewritten to `in_review` so the QA lead runs
// before the issue can complete. Once qa:pass is present the →done write
// passes through untouched, so the loop (dev → in_review → QA → qa:pass →
// done) always converges. Returns (statusToWrite, redirected).
//
// Applies uniformly to every actor (agent or human): a squad's work is QA
// work, and a human who genuinely wants to bypass can apply qa:pass first.
func (h *Handler) enforceQAGateBeforeDone(ctx context.Context, issue db.Issue, actorType, prevStatus, targetStatus string) (string, bool) {
	if targetStatus != "done" || prevStatus == "done" {
		return targetStatus, false
	}

	// Risk-tier human-sign-off gate (its OWN flag, independent of the QA gate): a
	// CRITICAL-tier issue must be CLOSED by a human. An agent transition to done —
	// from ANY prior status, including in_review, and even with its own qa:pass —
	// is held at in_review for human review. This upholds the risk_map's
	// documented "critical → human review mandatory" invariant, which was
	// previously advisory-only (an agent could self-attach qa:pass and close it).
	// Fail-open: a tier-lookup error never blocks. Humans are never held.
	if riskTierGateEnforced() && actorType == "agent" && h.issueRiskTier(ctx, issue) == "critical" {
		return "in_review", true
	}

	if !qaGateEnforced() {
		return targetStatus, false
	}
	if !h.issueDevOrchestrated(ctx, issue) {
		return targetStatus, false
	}
	// A present qa:fail ALWAYS blocks done — the audit found in_review→done was
	// ungated, so an issue the cockpit showed as "need fix" could be closed
	// anyway, splitting the done-gate from the merge gate and the cockpit lane.
	// The label is replace-on-write now, so a stale fail can't wedge this: a
	// re-QA pass (or a human triage Pass) removes it. The same applies to a
	// missing verdict: leaving in_review straight to done without qa:pass is
	// the silent-green path the watchdog exists to catch.
	if h.issueHasLabel(ctx, issue, "qa:fail") {
		return "in_review", true
	}
	if h.issueHasLabel(ctx, issue, "qa:pass") {
		// qa:pass present. Unless test-accuracy is enforced, that's enough.
		if !qaDiscriminationEnforced() {
			return targetStatus, false
		}
		// Test-accuracy enforced: the qa:pass must rest on a DISCRIMINATING test
		// (fail-before/pass-after), not a tautological/happy-path one. A
		// discriminating run present → done proceeds; none → hold at in_review so
		// QA re-runs and the author must add a test that actually exercises the
		// change. Fail-open on a query error (never block on infra failure).
		if ok, err := h.Queries.HasDiscriminatingRunForIssue(ctx, issue.ID); err != nil || ok {
			return targetStatus, false
		}
		// The hold is only meaningful when discrimination is POSSIBLE: e2e/
		// smoke/hand-driven cases report baseline "unknown" and can never
		// satisfy it, so an e2e-only issue used to wedge at in_review forever
		// (audit P2). No baseline-capable run at all → let qa:pass stand.
		if capable, err := h.Queries.HasBaselineCapableRunForIssue(ctx, issue.ID); err != nil || !capable {
			return targetStatus, false
		}
		return "in_review", true
	}
	return "in_review", true
}

// sprintPRMergeGateMarker tags the hold comment so it's posted ONCE per open PR
// (not on every retried →done attempt). The PR number is appended so a fresh PR
// re-notifies.
const sprintPRMergeGateMarker = "<!-- sprint-pr-merge-gate:%d -->"

// sprintPRMergeOverrideLabel lets a human force a sprint-PR issue to done despite
// an unmerged PR (abandoned branch, merged out-of-band, deliberate). The escape
// hatch: the gate would otherwise wedge such an issue with no way out.
const sprintPRMergeOverrideLabel = "merge:override"

// enforceSprintPRMergedBeforeDone holds a sprint-PR issue from reading "done"
// while its code is still an OPEN, UNMERGED pull request into the sprint branch.
// qa:pass alone is not completion — in PR mode a human (or the lead) still has
// to merge, and an issue that reaches done with an unmerged PR is code that
// never landed but reports complete (worst for critical/guarded tiers).
//
// It holds ONLY when there is a still-OPEN unmerged PR: a merged PR passes (the
// code landed), and a CLOSED-unmerged PR also passes (the branch was abandoned —
// holding forever would wedge the issue). The `merge:override` label is a manual
// escape. The hold comment is posted once per PR (marker-deduped) so repeated
// →done attempts don't spam the thread. Returns (prevStatus, true) to hold.
func (h *Handler) enforceSprintPRMergedBeforeDone(ctx context.Context, issue db.Issue, prevStatus, targetStatus string) (string, bool) {
	if !sprintPRModeEnabled() {
		return targetStatus, false
	}
	if targetStatus != "done" || prevStatus == "done" {
		return targetStatus, false
	}
	// Manual escape hatch — a human accepts responsibility for the unmerged PR.
	if h.issueHasLabel(ctx, issue, sprintPRMergeOverrideLabel) {
		return targetStatus, false
	}
	// Only sprint work opens a PR into a sprint branch.
	if _, err := h.Queries.GetSprintForIssue(ctx, issue.ID); err != nil {
		return targetStatus, false
	}
	prs, err := h.Queries.ListPullRequestsByIssue(ctx, issue.ID)
	if err != nil || len(prs) == 0 {
		return targetStatus, false // no linked PR — nothing to gate (direct commits, etc.)
	}
	// Find a PR that is still OPEN and unmerged. A merged PR means the code
	// landed (pass); a closed-unmerged PR means the branch was abandoned (pass,
	// so the issue is never wedged) — only an open unmerged PR is "in flight".
	var openPR *db.ListPullRequestsByIssueRow
	for i := range prs {
		pr := &prs[i]
		if pr.MergedAt.Valid {
			return targetStatus, false // landed
		}
		if openPR == nil && strings.EqualFold(strings.TrimSpace(pr.State), "open") {
			openPR = pr
		}
	}
	if openPR == nil {
		return targetStatus, false // no open PR — abandoned/closed; don't wedge
	}

	// Hold, and post the explanation ONCE per PR (marker dedup).
	marker := fmt.Sprintf(sprintPRMergeGateMarker, openPR.PrNumber)
	alreadyNoted := false
	if comments, cerr := h.Queries.ListCommentsForIssue(ctx, db.ListCommentsForIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID, Limit: 200,
	}); cerr == nil {
		for _, c := range comments {
			if strings.Contains(c.Content, marker) {
				alreadyNoted = true
				break
			}
		}
	}
	if !alreadyNoted {
		note := fmt.Sprintf("⛔ Not done yet — this task's pull request (#%d) into the sprint branch is still OPEN and "+
			"UNMERGED. qa:pass is the merge GATE, not completion: a human (or the squad lead) must merge the PR first, then "+
			"this can move to done. Holding the status until then. (To force done anyway, add the `%s` label.) %s",
			openPR.PrNumber, sprintPRMergeOverrideLabel, marker)
		if _, cerr := h.Queries.CreateComment(ctx, db.CreateCommentParams{
			IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
			AuthorType: "system", AuthorID: pgtype.UUID{Valid: true},
			Content: note, Type: "system", ParentID: pgtype.UUID{Valid: false},
		}); cerr != nil {
			slog.Warn("sprint-pr merge gate: comment failed", "error", cerr, "issue_id", uuidToString(issue.ID))
		}
	}
	slog.Info("sprint-pr merge gate: held →done, PR open+unmerged",
		"issue_id", uuidToString(issue.ID), "pr_number", openPR.PrNumber)
	return prevStatus, true
}

// squadFailureRecoveryMarker tags the recovery comment so repeated failures on
// the same issue can be capped (a broken member re-delegated to forever would
// otherwise loop). Kept out of the visible text via an HTML comment.
const squadFailureRecoveryMarker = "<!-- squad-failure-recovery -->"

// maxSquadFailureRecoveries bounds how many times a single issue may be
// auto-recovered before it's left for a human — enough to route around a
// transient/one-off provider hiccup, not enough to spin forever on a member
// that fails every time.
const maxSquadFailureRecoveries = 3

// squadFailureRecoveryEnabled gates the delegated-sub-task failure recovery.
// Default off — opt-in, matching every other auto-* gate in this file.
func squadFailureRecoveryEnabled() bool {
	return config.Bool("AGORA_SQUAD_FAILURE_RECOVERY_ENABLED")
}

// maybeRecoverSquadTaskFailure re-wakes the squad LEADER when a delegated
// member task dies (timeout, idle/startup watchdog, provider crash) so a
// squad-orchestrated issue doesn't wedge silently — the exact gap the
// concurrency stress test surfaced (issue 388: its only delegation went to a
// hung opencode dev, the dev task failed, and the Dev Lead was never
// re-triggered, so the issue sat in in_progress with no recovery).
//
// A failed task posts no completion signal, so nothing normally wakes the
// orchestrator. This closes that hole: on a recoverable failure of a
// squad-member task, post an @-mention comment to the leader carrying the
// failure reason (the comment IS the re-trigger) so it can re-delegate to a
// different member or handle it. Best-effort + detached, gated by
// AGORA_SQUAD_FAILURE_RECOVERY_ENABLED.
//
// Guards (each a real no-op case, not paranoia):
//   - no issue (chat task) → nothing to recover;
//   - clean cancellation → the user stopped it on purpose;
//   - issue already past dev (in_review/done/cancelled) → the work landed
//     elsewhere (a sibling delegation won the race), recovery is redundant;
//   - failing agent is in no squad → solo agent, today's manual flow stands;
//   - failing agent IS the leader → re-triggering the orchestrator from its
//     own failure risks a self-loop; leave it;
//   - the issue already has a pending/running task → it's being worked, don't
//     stack another leader task on top;
//   - recovery already fired maxSquadFailureRecoveries times → give up and
//     leave it for a human rather than loop on a member that always fails.
func (h *Handler) maybeRecoverSquadTaskFailure(ctx context.Context, task db.AgentTaskQueue, failureReason string) {
	if !squadFailureRecoveryEnabled() {
		return
	}
	if !task.IssueID.Valid || !task.AgentID.Valid {
		return
	}
	// A deliberate stop is not a failure to route around.
	switch strings.ToLower(strings.TrimSpace(failureReason)) {
	case "cancelled", "canceled", "superseded":
		return
	}

	issue, err := h.Queries.GetIssue(ctx, task.IssueID)
	if err != nil {
		return
	}
	switch issue.Status {
	case "in_review", "done", "cancelled":
		return // already progressed past dev — a sibling delegation succeeded
	}

	// A leader's own failed task must not re-spawn the leader (self-loop).
	squads, err := h.Queries.ListSquadsByMember(ctx, db.ListSquadsByMemberParams{
		WorkspaceID: issue.WorkspaceID, MemberType: "agent", MemberID: task.AgentID,
	})
	if err != nil || len(squads) == 0 {
		return
	}
	leaderID := squads[0].LeaderID
	if !leaderID.Valid || uuidToString(leaderID) == uuidToString(task.AgentID) {
		return
	}
	leader, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
		ID: leaderID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil || leader.ArchivedAt.Valid {
		return
	}

	// Something is already queued/running on this issue → don't stack.
	if pending, err := h.Queries.HasPendingTaskForIssue(ctx, issue.ID); err == nil && pending {
		return
	}

	// Cap retries: count prior recovery markers on this issue.
	if comments, err := h.Queries.ListCommentsForIssue(ctx, db.ListCommentsForIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID, Limit: 200,
	}); err == nil {
		n := 0
		for _, c := range comments {
			if strings.Contains(c.Content, squadFailureRecoveryMarker) {
				n++
			}
		}
		if n >= maxSquadFailureRecoveries {
			return
		}
	}

	failingName := "a delegated agent"
	if a, err := h.Queries.GetAgent(ctx, task.AgentID); err == nil && strings.TrimSpace(a.Name) != "" {
		failingName = a.Name
	}
	reason := strings.TrimSpace(failureReason)
	if reason == "" {
		reason = "the task failed"
	}

	// Attribute the recovery comment to the issue's creator (member or agent) —
	// there is no human actor on the daemon's fail-report path.
	authorType := issue.CreatorType
	if authorType != "member" && authorType != "agent" {
		authorType = "member"
	}
	content := fmt.Sprintf("[@%s](mention://agent/%s) ", sanitizeMentionLabel(leader.Name), uuidToString(leader.ID)) +
		fmt.Sprintf("The delegated task for **%s** did not complete (%s). Re-triage: re-delegate this to a "+
			"different squad member or handle it yourself, then move the issue forward. ", sanitizeMentionLabel(failingName), reason) +
		squadFailureRecoveryMarker
	comment, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
		AuthorType: authorType, AuthorID: issue.CreatorID,
		Content: content, Type: "comment", ParentID: pgtype.UUID{Valid: false},
	})
	if err != nil {
		slog.Warn("squad failure recovery: create comment failed", "error", err, "issue_id", uuidToString(issue.ID))
		return
	}
	h.triggerTasksForComment(ctx, issue, comment, nil, authorType, uuidToString(issue.CreatorID), nil)
	slog.Info("squad failure recovery: re-woke squad lead after a member task failure",
		"issue_id", uuidToString(issue.ID),
		"failed_agent_id", uuidToString(task.AgentID),
		"lead_agent_id", uuidToString(leaderID),
		"failure_reason", reason)
}

// maybeRouteToDevLeadOnQAFail closes the QA<->dev loop automatically: when an
// issue gains qa:fail, find the FAILING dev agent's squad (if any) and hand the
// issue to that squad's LEADER — the orchestrator who triages and re-delegates
// — instead of leaving it for a human to notice and manually reassign. The
// reassignment moves the issue back to "todo" so the leader's claim fires
// through the normal agent-assignment dispatch path (same as any fresh
// assignment) — the dev board picks it back up automatically.
//
// The comment this posts IS the QA<->dev communication: it lands in the
// issue's ONE shared timeline, so the dev-facing Issue Detail (which already
// renders QAEvidenceSection) and the QA review page (which links to "Open
// full issue") both read the same story — no separate channel to keep in
// sync, and the QA verdict's summary travels WITH the reassignment instead of
// requiring the lead to go hunt for why.
//
// Degrades to a no-op, past the label + agent-assignee gate, when: the
// failing agent isn't in any squad (no lead to route to — a solo-agent setup
// keeps today's manual triage), or the squad's leader IS the failing agent
// itself (reassigning to itself teaches nothing).
func (h *Handler) maybeRouteToDevLeadOnQAFail(ctx context.Context, issue db.Issue, labelName, userID string) {
	if !qaFailAutorouteEnabled() {
		return
	}
	if strings.ToLower(strings.TrimSpace(labelName)) != "qa:fail" {
		return
	}
	if !issue.AssigneeType.Valid || issue.AssigneeType.String != "agent" || !issue.AssigneeID.Valid {
		return // nothing to route from — no failing dev agent to find a lead for
	}
	failingAgentID := issue.AssigneeID

	squads, err := h.Queries.ListSquadsByMember(ctx, db.ListSquadsByMemberParams{
		WorkspaceID: issue.WorkspaceID, MemberType: "agent", MemberID: failingAgentID,
	})
	if err != nil || len(squads) == 0 {
		return // solo agent, no squad -> no lead to route to; today's manual flow stands
	}
	leaderID := squads[0].LeaderID
	if !leaderID.Valid || uuidToString(leaderID) == uuidToString(failingAgentID) {
		return // the failing agent IS the leader -> reassigning to itself teaches nothing
	}
	leader, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
		ID: leaderID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return
	}

	summary := "QA failed."
	if evidence, err := h.Queries.GetLatestQAEvidenceForIssue(ctx, db.GetLatestQAEvidenceForIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	}); err == nil && strings.TrimSpace(evidence.Summary) != "" {
		summary = "QA failed: " + strings.TrimSpace(evidence.Summary)
	}

	if _, err := h.Queries.UpdateIssueAssignee(ctx, db.UpdateIssueAssigneeParams{
		ID: issue.ID, AssigneeType: pgtype.Text{String: "agent", Valid: true},
		AssigneeID: leaderID, WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		slog.Warn("qa-fail autoroute: reassign failed", "error", err, "issue_id", uuidToString(issue.ID))
		return
	}
	if _, err := h.Queries.UpdateIssueStatus(ctx, db.UpdateIssueStatusParams{
		ID: issue.ID, Status: "todo", WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		slog.Warn("qa-fail autoroute: status reset failed", "error", err, "issue_id", uuidToString(issue.ID))
	}

	content := fmt.Sprintf("[@%s](mention://agent/%s) ", sanitizeMentionLabel(leader.Name), uuidToString(leader.ID)) +
		summary + " Reassigned back to you for triage — re-delegate to a dev agent or fix it yourself, " +
		"then move this to in_review to re-fire QA."
	comment, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
		AuthorType: "member", AuthorID: parseUUID(userID),
		Content: content, Type: "comment", ParentID: pgtype.UUID{Valid: false},
	})
	if err != nil {
		slog.Warn("qa-fail autoroute: create comment failed", "error", err, "issue_id", uuidToString(issue.ID))
		return
	}
	h.triggerTasksForComment(ctx, issue, comment, nil, "member", userID, nil)
	slog.Info("qa-fail autoroute: reassigned to squad lead",
		"issue_id", uuidToString(issue.ID),
		"failing_agent_id", uuidToString(failingAgentID),
		"lead_agent_id", uuidToString(leaderID))
}

func qaFailAutoFileBugEnabled() bool {
	return config.Bool("AGORA_QA_FAIL_AUTO_FILE_BUG_ENABLED")
}

// maybeAutoFileBugOnQAFail opens a `bug`-labelled child issue when an issue is
// labelled qa:fail, so a failed verdict becomes a tracked, triageable bug
// instead of relying on a human clicking "File bug" in the QA cockpit. The bug
// links to the failed issue (parent), inherits its project + priority, and
// carries the QA evidence summary. Gated (AGORA_QA_FAIL_AUTO_FILE_BUG_ENABLED),
// detached + best-effort, and runs alongside the qa-fail autoroute. Deduped via
// a `qa_bug_filed` metadata stamp on the parent so repeated qa:fail labels
// (re-QA loops) don't spawn duplicate bugs.
func (h *Handler) maybeAutoFileBugOnQAFail(ctx context.Context, issue db.Issue, labelName, actorID string) {
	if !qaFailAutoFileBugEnabled() {
		return
	}
	if strings.ToLower(strings.TrimSpace(labelName)) != "qa:fail" {
		return
	}
	// Dedup: one auto-filed bug per failed issue.
	if len(issue.Metadata) > 0 {
		var meta map[string]any
		if json.Unmarshal(issue.Metadata, &meta) == nil {
			if _, done := meta["qa_bug_filed"]; done {
				return
			}
		}
	}

	summary := ""
	if evidence, err := h.Queries.GetLatestQAEvidenceForIssue(ctx, db.GetLatestQAEvidenceForIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	}); err == nil {
		summary = strings.TrimSpace(evidence.Summary)
	}

	parentKey := fmt.Sprintf("%s-%d", h.getIssuePrefix(ctx, issue.WorkspaceID), issue.Number)
	titleText := issue.Title
	if summary != "" {
		titleText = summary
	}
	title := "Bug: " + titleText
	if r := []rune(title); len(r) > 160 {
		title = string(r[:159]) + "…"
	}
	detail := summary
	if detail == "" {
		detail = "See the QA verdict on the parent issue."
	}
	desc := fmt.Sprintf("Filed automatically from a failed QA verdict on %s — %s.\n\n%s", parentKey, issue.Title, detail)

	// The verdict author (comment path) is usually the QA agent, the label path
	// usually a human — resolve which so creator_type is honest (creator_id has
	// no FK, but the CHECK only allows member|agent).
	actorUUID := parseUUID(actorID)
	creatorType := "member"
	if _, aerr := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: actorUUID, WorkspaceID: issue.WorkspaceID}); aerr == nil {
		creatorType = "agent"
	}

	res, err := h.IssueService.Create(ctx, service.IssueCreateParams{
		WorkspaceID:    issue.WorkspaceID,
		Title:          title,
		Description:    pgtype.Text{String: desc, Valid: true},
		Status:         "todo",
		Priority:       issue.Priority,
		CreatorType:    creatorType,
		CreatorID:      actorUUID,
		ParentIssueID:  issue.ID,
		ProjectID:      issue.ProjectID,
		AllowDuplicate: true,
	}, service.IssueCreateOpts{ActorID: actorID})
	if err != nil {
		slog.Warn("qa-fail auto-file bug: create failed", "error", err, "issue_id", uuidToString(issue.ID))
		return
	}

	if labelID, lerr := h.ensureLabel(ctx, issue.WorkspaceID, "bug", "#ef4444"); lerr == nil {
		if err := h.Queries.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
			IssueID: res.Issue.ID, LabelID: labelID, WorkspaceID: issue.WorkspaceID,
		}); err != nil {
			slog.Warn("qa-fail auto-file bug: attach bug label failed", "error", err, "bug_id", uuidToString(res.Issue.ID))
		}
	}
	// Stamp the parent so a re-QA loop doesn't file the same bug twice.
	if _, err := h.Queries.SetIssueMetadataKey(ctx, db.SetIssueMetadataKeyParams{
		ID: issue.ID, WorkspaceID: issue.WorkspaceID, Key: "qa_bug_filed", Value: []byte("true"),
	}); err != nil {
		slog.Warn("qa-fail auto-file bug: dedup stamp failed", "error", err, "issue_id", uuidToString(issue.ID))
	}
	slog.Info("qa-fail auto-filed bug", "parent_id", uuidToString(issue.ID), "bug_id", uuidToString(res.Issue.ID))
}

// devSquadLeaderForIssue resolves the leader of the DEV squad an orchestrated
// issue belongs to — the squad the issue is assigned to, or the squad of the
// agent it is assigned to. Returns false for solo / non-squad issues.
func (h *Handler) devSquadLeaderForIssue(ctx context.Context, issue db.Issue) (db.Agent, bool) {
	if !issue.AssigneeType.Valid || !issue.AssigneeID.Valid {
		return db.Agent{}, false
	}
	var leaderID pgtype.UUID
	switch issue.AssigneeType.String {
	case "squad":
		sq, err := h.Queries.GetSquad(ctx, issue.AssigneeID)
		if err != nil {
			return db.Agent{}, false
		}
		leaderID = sq.LeaderID
	case "agent":
		squads, err := h.Queries.ListSquadsByMember(ctx, db.ListSquadsByMemberParams{
			WorkspaceID: issue.WorkspaceID, MemberType: "agent", MemberID: issue.AssigneeID,
		})
		if err != nil || len(squads) == 0 {
			return db.Agent{}, false
		}
		leaderID = squads[0].LeaderID
	default:
		return db.Agent{}, false
	}
	if !leaderID.Valid {
		return db.Agent{}, false
	}
	leader, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
		ID: leaderID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return db.Agent{}, false
	}
	return leader, true
}

// maybeMergeOnQAPass is the sprint-PR-mode merge gate (Phase 3 of auto sprint
// review): when an orchestrated sprint task's PR passes QA (gains qa:pass), route
// the squad LEAD to review the PR diff and, if it holds up, merge it into the
// sprint branch — the final gate before code lands on the shared branch, with no
// human in the loop. On real problems the lead comments + routes back to the dev
// instead of merging. Detached + best-effort (mirrors maybeRouteToDevLeadOnQAFail),
// gated by AGORA_SPRINT_PR_MODE + a sprint + a dev squad. No-op otherwise.
func (h *Handler) maybeMergeOnQAPass(ctx context.Context, issue db.Issue, labelName, userID string) {
	if !sprintPRModeEnabled() {
		return
	}
	if strings.ToLower(strings.TrimSpace(labelName)) != "qa:pass" {
		return
	}
	// Only sprint work has a PR into a sprint branch.
	sprint, err := h.Queries.GetSprintForIssue(ctx, issue.ID)
	if err != nil {
		return
	}
	branch := SprintBranchFor(sprint)

	// Human-merge (default): the PR passed QA and is READY FOR A HUMAN to review +
	// merge. Post a plain human-facing note (NO agent mention, so no agent acts)
	// and stop — a person does the final review + merge into the sprint branch.
	if !sprintAutoMergeEnabled() {
		content := "✅ QA passed (qa:pass) on this task's pull request into `" + branch +
			"`. READY FOR HUMAN REVIEW + MERGE — a person reviews the PR and merges it into `" + branch +
			"`. Auto-merge is off (AGORA_SPRINT_AUTO_MERGE); no agent will merge it."
		if posted, cerr := h.Queries.CreateComment(ctx, db.CreateCommentParams{
			IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
			AuthorType: "member", AuthorID: parseUUID(userID),
			Content: content, Type: "comment", ParentID: pgtype.UUID{Valid: false},
		}); cerr != nil {
			slog.Warn("qa-pass human-merge note: create comment failed", "error", cerr, "issue_id", uuidToString(issue.ID))
		} else {
			h.publish(protocol.EventCommentCreated, uuidToString(issue.WorkspaceID), "member", userID, map[string]any{
				"comment": map[string]any{
					"id": uuidToString(posted.ID), "issue_id": uuidToString(posted.IssueID),
					"author_type": posted.AuthorType, "author_id": uuidToString(posted.AuthorID),
					"content": posted.Content, "type": posted.Type,
					"created_at": posted.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
				},
			})
		}
		slog.Info("qa-pass: PR ready for human review+merge (auto-merge off)", "issue_id", uuidToString(issue.ID))
		return
	}

	// TIERED AUTONOMY: even with auto-merge opted in, a critical- or
	// guarded-tier issue (risk_map projects; unknown = guarded, fail closed)
	// NEVER auto-merges — a human reviews and merges, period. Only risk:safe
	// issues (or projects without a risk map) reach the lead auto-merge below.
	if tier := h.issueRiskTier(ctx, issue); tier == "critical" || tier == "guarded" {
		content := "✅ QA passed (qa:pass) on this task's pull request into `" + branch +
			"`. RISK TIER: **" + tier + "** — auto-merge is structurally refused for this tier " +
			"(risk map policy); a HUMAN reviews the PR and merges it into `" + branch + "`."
		if owners := h.issueRiskOwners(ctx, issue); len(owners) > 0 {
			content += " Module owner(s): " + strings.Join(owners, ", ") + "."
		}
		if posted, cerr := h.Queries.CreateComment(ctx, db.CreateCommentParams{
			IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
			AuthorType: "member", AuthorID: parseUUID(userID),
			Content: content, Type: "comment", ParentID: pgtype.UUID{Valid: false},
		}); cerr != nil {
			slog.Warn("qa-pass tier gate: create comment failed", "error", cerr, "issue_id", uuidToString(issue.ID))
		} else {
			// Publish so the note renders live for the humans it addresses —
			// a direct CreateComment bypasses the event bus.
			h.publish(protocol.EventCommentCreated, uuidToString(issue.WorkspaceID), "member", userID, map[string]any{
				"comment": map[string]any{
					"id": uuidToString(posted.ID), "issue_id": uuidToString(posted.IssueID),
					"author_type": posted.AuthorType, "author_id": uuidToString(posted.AuthorID),
					"content": posted.Content, "type": posted.Type,
					"created_at": posted.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
				},
			})
		}
		slog.Info("qa-pass: auto-merge refused by risk tier — human merge required",
			"issue_id", uuidToString(issue.ID), "tier", tier)
		return
	}

	// Auto-merge (opt-in via AGORA_SPRINT_AUTO_MERGE): route the DEV squad LEAD to
	// review the diff + merge the PR into the sprint branch, no human in the loop.
	leader, ok := h.devSquadLeaderForIssue(ctx, issue)
	if !ok {
		return // solo / non-squad — no lead owns the merge; today's flow stands
	}
	content := fmt.Sprintf("[@%s](mention://agent/%s) ", sanitizeMentionLabel(leader.Name), uuidToString(leader.ID)) +
		"QA passed (qa:pass) on this task's pull request into `" + branch + "`. As the squad LEAD this is the FINAL gate " +
		"before code lands on the shared sprint branch — no human reviews it. Find the task's open PR (`gh pr list --base " + branch +
		" --state open`) and review its diff (`gh pr diff <pr>`): if it is correct, safe, and matches the ticket, MERGE it into `" + branch +
		"` with `gh pr merge <pr> --squash --delete-branch`. If it has real problems, do NOT merge — comment the specific issues and " +
		"@mention the dev who wrote it to fix, then let QA re-run. Never target the repository's main/default branch."
	comment, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
		AuthorType: "member", AuthorID: parseUUID(userID),
		Content: content, Type: "comment", ParentID: pgtype.UUID{Valid: false},
	})
	if err != nil {
		slog.Warn("qa-pass merge gate: create comment failed", "error", err, "issue_id", uuidToString(issue.ID))
		return
	}
	h.triggerTasksForComment(ctx, issue, comment, nil, "member", userID, nil)
	slog.Info("qa-pass merge gate: routed PR review+merge to squad lead",
		"issue_id", uuidToString(issue.ID), "lead_agent_id", uuidToString(leader.ID))
}

// qaSquadLeader resolves the QA squad's leader agent for a workspace — the squad
// whose name contains "qa" (case-insensitive), e.g. "QA" / "QA Squad". The leader
// agent is what runs an auto-fired run_qa. ok=false when there is no QA squad, it
// has no leader, or the leader agent is archived / not ready.
func (h *Handler) qaSquadLeader(ctx context.Context, wsID pgtype.UUID) (db.Agent, bool) {
	squads, err := h.Queries.ListSquads(ctx, wsID)
	if err != nil {
		return db.Agent{}, false
	}
	for _, s := range squads {
		if !strings.Contains(strings.ToLower(s.Name), "qa") || !s.LeaderID.Valid {
			continue
		}
		leader, err := h.Queries.GetAgent(ctx, s.LeaderID)
		if err == nil && !leader.ArchivedAt.Valid && sliceAgentReady(leader) {
			return leader, true
		}
	}
	return db.Agent{}, false
}

// qaSquadAgents returns ALL ready agents of the QA squad — its leader plus its
// agent members — so auto-QA can fan across the whole roster instead of funneling
// every in_review issue through one leader (the hard throughput ceiling). Each is
// non-archived + has a runtime (sliceAgentReady). Empty when there's no QA squad
// or no ready agent.
func (h *Handler) qaSquadAgents(ctx context.Context, wsID pgtype.UUID) []db.Agent {
	squads, err := h.Queries.ListSquads(ctx, wsID)
	if err != nil {
		return nil
	}
	var squad db.Squad
	found := false
	for _, s := range squads {
		if strings.Contains(strings.ToLower(s.Name), "qa") {
			squad = s
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	seen := map[string]bool{}
	var agents []db.Agent
	add := func(id pgtype.UUID) {
		if !id.Valid {
			return
		}
		k := uuidToString(id)
		if seen[k] {
			return
		}
		seen[k] = true
		a, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: id, WorkspaceID: wsID})
		if err == nil && !a.ArchivedAt.Valid && sliceAgentReady(a) {
			agents = append(agents, a)
		}
	}
	add(squad.LeaderID)
	if members, err := h.Queries.ListSquadMembers(ctx, squad.ID); err == nil {
		for _, m := range members {
			if m.MemberType == "agent" {
				add(m.MemberID)
			}
		}
	}
	return agents
}

// pickLeastBusyQAAgent spreads QA across the roster: the agent with the fewest
// in-flight tasks (first-min wins, so a stable order tie-breaks deterministically).
// Concurrent dispatches each see the prior pick's task counted, so load spreads.
func (h *Handler) pickLeastBusyQAAgent(ctx context.Context, agents []db.Agent) db.Agent {
	best := agents[0]
	bestN := int64(1) << 62
	for _, a := range agents {
		n, err := h.Queries.CountRunningTasks(ctx, a.ID)
		if err != nil {
			n = 0
		}
		if n < bestN {
			bestN = n
			best = a
		}
	}
	return best
}

// qaTrivialCeiling is appended to a run_qa instruction for a low-risk change so
// the gate stays SOLO and fast — the exact over-delegation the sd-cs stress test
// surfaced (a doc-only issue pulled Security Reviewer + Designer into a review
// panel and stalled).
const qaTrivialCeiling = " SCOPE — TRIVIAL / low-risk change: gate it SOLO and FAST. Do NOT @mention, delegate to, or summon any other agent — no Security Reviewer, no Designer, no additional QA members — UNLESS the diff actually touches security-sensitive code or the UI/design. A one-file docs or config change does not need a review panel: run the minimal check that proves it is safe and post the qa:pass/qa:fail verdict yourself."

// issueQAScopeTrivial decides whether an issue's QA effort should be scoped DOWN
// to a solo, no-fan-out gate. It only ever DOWNGRADES on a reliably-small signal
// (mirrors issue_tier.go's fail-safe: unknown size stays FULL), and never
// downgrades a risk:guarded / risk:critical issue. Signals, cheapest first:
//   - labels: risk:guarded|risk:critical veto; tier:trivial|tier:light|
//     risk:safe|type:docs ⇒ trivial;
//   - PR diff size (HINT only): a non-empty, small diff (<=2 files AND <=20
//     lines). A 0/0/0 PR row is unsynced webhook stats = UNKNOWN ⇒ stays full,
//     never downgraded on absent data.
func (h *Handler) issueQAScopeTrivial(ctx context.Context, issue db.Issue) bool {
	if labels, err := h.Queries.ListLabelsByIssue(ctx, db.ListLabelsByIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	}); err == nil {
		has := make(map[string]bool, len(labels))
		for _, l := range labels {
			has[strings.ToLower(strings.TrimSpace(l.Name))] = true
		}
		if has["risk:guarded"] || has["risk:critical"] {
			return false // high blast radius — always full QA
		}
		if has["tier:trivial"] || has["tier:light"] || has["risk:safe"] || has["type:docs"] {
			return true
		}
	}
	prs, err := h.Queries.ListPullRequestsByIssue(ctx, issue.ID)
	if err != nil || len(prs) == 0 {
		return false // no PR / unknown → full
	}
	pr := prs[0]
	return pr.ChangedFiles > 0 && pr.ChangedFiles <= 2 && (pr.Additions+pr.Deletions) <= 20
}

// filterQAAgentsForScope drops specialist reviewers (Security / Designer) from a
// QA roster when the change is trivial, so a doc/config change never fans out to
// a full review panel. Never returns empty — a trivial gate still needs ONE
// runner, so a roster of only specialists falls back to the original slice.
func filterQAAgentsForScope(agents []db.Agent, trivial bool) []db.Agent {
	if !trivial {
		return agents
	}
	kept := make([]db.Agent, 0, len(agents))
	for _, a := range agents {
		n := strings.ToLower(a.Name)
		if strings.Contains(n, "security") || strings.Contains(n, "design") {
			continue
		}
		kept = append(kept, a)
	}
	if len(kept) == 0 {
		return agents
	}
	return kept
}

// issueQALocks serializes the auto-QA triggers (run_qa / gen_test_cases) per
// issue WITHIN this backend process. The status write is a read-modify-write
// with no row lock, so two concurrent transitions into the same state both see
// the old prevStatus and both launch the detached trigger goroutine; their
// per-agent / existing-cases guards then race (neither has enqueued yet) and
// each posts a duplicate @QA trigger comment. Holding a per-issue lock around
// the check+enqueue makes the second goroutine observe the first's queued task
// (HasPendingTaskForIssueAndAgent) and bail, so exactly one fires. Single
// instance only — a multi-replica deployment would still need a DB guard, but
// the self-host backend is one process.
var issueQALocks sync.Map // issueID string -> *sync.Mutex

func lockIssueQA(issueID string) func() {
	m, _ := issueQALocks.LoadOrStore(issueID, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// maybeRunQAOnInReview fires the QA squad's run_qa when an issue enters
// in_review — automating the QA team's previously-manual smoke (deterministic
// smoke on the assignee developer's box + plan-driven tests). Best-effort +
// detached (mirrors maybeAutoDocsOnLabel): any miss (disabled, no QA squad, no
// ready leader) silently no-ops, so a status change never fails because of it.
// Gated by AGORA_AUTO_QA_ENABLED; the caller guards the prev!=in_review→in_review
// transition so it runs once per entry.
func (h *Handler) maybeRunQAOnInReview(ctx context.Context, issue db.Issue, actorType, actorID string) {
	if !autoQAEnabled() {
		return
	}
	defer lockIssueQA(uuidToString(issue.ID))()
	// The in-process mutex above only guards ONE replica. On a multi-replica
	// deploy each backend would dispatch the full QA fan-out for the same
	// transition (audit P2). A per-issue DB advisory try-lock held for the
	// dispatch closes that: the loser sees the lock taken and bows out.
	// Best-effort — lock-infra failure proceeds single-replica-style.
	if lockTx, err := h.TxStarter.Begin(ctx); err == nil {
		defer func() { _ = lockTx.Rollback(ctx) }()
		var got bool
		if qerr := lockTx.QueryRow(ctx,
			`SELECT pg_try_advisory_xact_lock(hashtext($1))`,
			"qa-dispatch:"+uuidToString(issue.ID)).Scan(&got); qerr == nil && !got {
			slog.Info("qa dispatch already in flight on another replica; skipping", "issue_id", uuidToString(issue.ID))
			return
		}
	}

	// Orchestrator-to-orchestrator (product rule: "the QA lead and dev lead must
	// always be in communication"): when the DEV side is squad-managed — the
	// issue is assigned straight to a squad, or to an agent who belongs to
	// one — route QA to the QA squad's LEADER specifically, not a load-balanced
	// roster pick, so the two leads are always the ones talking to each other on
	// orchestrator-managed work. qaSquadLeader already existed for exactly this
	// (previously unused in production — see its own unit test). Solo-agent /
	// non-squad assignments are UNCHANGED below: they still fan across the whole
	// QA roster so many in_review issues run QA concurrently instead of queuing
	// behind one agent — this branch never touches that path.
	// A trivial / low-risk change (doc-only, risk:safe, tiny diff) is gated SOLO
	// and fast: it does NOT route to the QA lead (which would delegate = 2 agents
	// minimum) and its roster excludes specialist reviewers, so a one-file docs
	// change never spins up a review panel. Guarded/critical/unknown work is
	// unaffected — it takes the exact path it does today.
	trivial := h.issueQAScopeTrivial(ctx, issue)

	var runner db.Agent
	var agents []db.Agent
	routedToLead := false
	devOrchestrated := h.issueDevOrchestrated(ctx, issue)
	if devOrchestrated && !trivial {
		if leader, ok := h.qaSquadLeader(ctx, issue.WorkspaceID); ok {
			runner = leader
			agents = []db.Agent{leader}
			routedToLead = true
		}
	}
	if runner.ID == (pgtype.UUID{}) {
		// Fan across the WHOLE QA roster (not just the leader) + pick the least-busy
		// ready agent, so many in_review issues run QA concurrently instead of queuing
		// behind one agent. The per-box sync lock keeps concurrent runs on the shared
		// sprint branch safe.
		agents = filterQAAgentsForScope(h.qaSquadAgents(ctx, issue.WorkspaceID), trivial)
		if len(agents) == 0 {
			return
		}
		runner = h.pickLeastBusyQAAgent(ctx, agents)
	}

	// Shared-sprint-branch model: when the issue belongs to a sprint, QA runs on
	// the SPRINT branch (the integrated tip), not an isolated per-task branch.
	// scope=task attributes a failure to this task's delta via the last-green ref;
	// deploy the sprint branch to the project's sprint box so the smoke hits the
	// integrated state. No sprint → fall back to the generic gate + the dev box.
	scope := ""
	smokeURL := ""
	sprintNote := ""
	// An issue's sprint is the issue_to_sprint join (no column on issue).
	if sprint, err := h.Queries.GetSprintForIssue(ctx, issue.ID); err == nil {
		scope = "task"
		sid := uuidToString(sprint.ID)
		branch := SprintBranchFor(sprint)
		sprintNote = " SPRINT CONTEXT: this task is on the shared sprint branch " + branch +
			"; for the scope=task baseline use <sprintId>=" + sid + " (refs/sprint/" + sid + "/last-green)."
		switch {
		case sprintPRModeEnabled():
			// PR-review mode: the task's work lives on its OWN pull-request branch,
			// NOT yet merged into the sprint branch. QA must smoke the PR branch on
			// the dev's box — smoking the sprint tip would judge code the PR hasn't
			// landed. This run's qa:pass/qa:fail is the merge gate (Phase 3): the
			// squad lead merges the PR into the sprint branch only after qa:pass.
			smokeURL = h.devBoxSmokeURL(ctx, issue)
			sprintNote += " PR-REVIEW MODE: this task is an OPEN pull request INTO `" + branch +
				"`, not yet merged. QA the PULL REQUEST's OWN branch, not the sprint tip: deploy the task's branch to the dev QA box (the deploy-qa git-sync) and smoke THAT url — do NOT deploy or smoke `" + branch +
				"` itself. Your qa:pass/qa:fail IS the merge gate: the squad lead merges the PR into `" + branch + "` only after qa:pass."
		default:
			if box, synced, derr := h.DeploySprintBranch(ctx, sprint.ID, issue.WorkspaceID); derr != nil || !synced {
				// Fail CLOSED: the sprint branch is NOT confirmed live on the QA box,
				// so smoking it would judge STALE code and let a false qa:pass stand.
				// Withhold the smoke target and tell the gate to block, not pass.
				slog.Warn("auto run_qa: sprint branch not deployed — blocking QA", "sprint_id", sid, "error", derr, "synced", synced)
				sprintNote += " QA BLOCKED — the sprint branch could not be deployed to the QA box (it is not serving this branch), so QA cannot judge the real change. Do NOT smoke a stale environment and do NOT set qa:pass; set the `qa:blocked` label and report that the box is not serving the sprint branch."
			} else {
				smokeURL = boxSmokeURL(box)
			}
		}
	}

	// The developer's own machine ranks ahead of a deployed box (non-sprint
	// path only — sprint QA smokes the integrated sprint box, resolved above).
	// Order: dev_apps URL (concrete, already running) > local_directory (folder
	// on an online daemon — pin + start-via-preview, no URL yet) > connected
	// box. Sprint mode already set smokeURL, so leave it alone there.
	localDirQAPath := ""
	if scope != "task" && smokeURL == "" {
		if url := h.devLocalAppURL(ctx, issue); url != "" {
			smokeURL = url
		} else if _, lp, ok := h.localDirectoryQATarget(ctx, issue); ok {
			localDirQAPath = lp
		} else {
			smokeURL = h.devBoxSmokeURL(ctx, issue)
		}
	}

	instruction := buildSliceInstruction(sliceActionRunQA, scope) + sprintNote
	if smokeURL != "" {
		instruction += " SMOKE TARGET: the branch is served at " + smokeURL +
			" — smoke THAT url. It OVERRIDES any project smoke url below."
	} else if localDirQAPath != "" {
		instruction += qaLocalDirectoryClause(localDirQAPath)
	}
	// Risk map intentionally NOT appended here: the claim path injects it into
	// the same run's instructions (daemon.go) — appending again would duplicate.
	instruction += h.sliceActionQASmokeContext(ctx, issue)
	instruction += h.sliceActionQAManifestContext(ctx, issue)
	instruction += h.sliceActionDesignManifestContext(ctx, issue)
	instruction += h.sliceActionQADocsContext(ctx, issue)
	instruction += h.sliceActionProjectBaseSuiteContext(ctx, issue)
	instruction += qaPlanContext(issue.Description.String, issue.AcceptanceCriteria)
	instruction += h.sliceActionDesignCompareContext(ctx, issue)
	instruction += h.sliceActionDesignLintContext(ctx, issue)

	// Trivial change: cap the fleet explicitly (the runner is already a single
	// non-specialist, but the ceiling stops it from @mentioning its way to a panel).
	if trivial {
		instruction += qaTrivialCeiling
	}

	// When this routed to the QA LEAD (orchestrated dev side), the lead should
	// ORCHESTRATE, not execute: delegate the actual gate run to a QA member so
	// the fast execution model does the mechanical work while the lead keeps the
	// dev-lead↔QA-lead coordination and owns the qa:pass/qa:fail rollup. The lead
	// running run_qa itself is the wall-clock bottleneck (a heavy model doing
	// mechanical smoke). Falls back to self-run only if no member is available.
	// Never runs when trivial (routedToLead is false above), but the explicit
	// !trivial guard documents the invariant.
	if routedToLead && !trivial {
		if strings.TrimSpace(h.sliceActionQAManifestContext(ctx, issue)) != "" {
			// SPEED: the project has a QA MANIFEST, so the stack + navigation are
			// already KNOWN — skip the lead's heavy "read the repo to determine the
			// tooling" hop (that determination was the slow part) and delegate
			// immediately, pointing the member at the manifest.
			instruction = "As the QA LEAD: this project's stack, auth, and navigation are in the PROJECT QA MANIFEST below — " +
				"do NOT re-read the repo to determine them. DELEGATE this QA gate to a QA squad member via @mention right away " +
				"(they execute it on a faster model), pointing them at the manifest; run it yourself ONLY if no member is " +
				"available. You own the qa:pass/qa:fail rollup and stay in sync with the dev lead. The gate to delegate: " + instruction
		} else {
			instruction = "As the QA LEAD, FIRST determine THIS project's stack and testing tooling yourself — read " +
				"the repo (package.json/go.mod/composer.json, existing test dirs, CI config) rather than assuming; a " +
				"Jest/Vitest project needs `npm test`/`vitest run`, a Go repo needs `go test ./...`, a PHP monolith with " +
				"no unit-test layer (e.g. Yii1 with a mixed jQuery/Vue2/Vue3/Angular frontend) has no build/test command " +
				"at all — for that stack the rendered page IS the contract, so route the delegate to browser-driven " +
				"verification (deterministic HTTP/DOM assertions against the deployed QA box) instead of a nonexistent " +
				"test suite. Then DELEGATE this QA gate to a QA squad member via @mention, TELLING them which tooling " +
				"you determined applies (they execute it on a faster model) — do NOT run the gate yourself unless no " +
				"member is available. You own the qa:pass/qa:fail rollup and stay in sync with the dev lead. " +
				"The gate to delegate: " + instruction
		}
	}

	authorID, ok := actorAuthorID(actorID)
	if !ok {
		slog.Warn("auto run_qa: invalid actor id, skipping", "actor_id", actorID, "issue_id", uuidToString(issue.ID))
		return
	}
	content := fmt.Sprintf("[@%s](mention://agent/%s) ", sanitizeMentionLabel(runner.Name), uuidToString(runner.ID)) + instruction
	comment, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  actorType,
		AuthorID:    authorID,
		Content:     content,
		Type:        "comment",
		ParentID:    pgtype.UUID{Valid: false},
	})
	if err != nil {
		slog.Warn("auto run_qa: create comment failed", "error", err, "issue_id", uuidToString(issue.ID))
		return
	}
	h.triggerTasksForComment(ctx, issue, comment, nil, actorType, actorID, nil)
	slog.Info("auto run_qa fired on in_review",
		"issue_id", uuidToString(issue.ID), "agent_id", uuidToString(runner.ID),
		"qa_agents", len(agents))
}

// maybeGenTests fires the QA squad's gen_test_cases for an issue. Two
// triggers, same machinery:
//   - SHIFT-LEFT PREP, on dev start (status → in_progress): while dev agents
//     work the task, a QA agent authors the cases AND compiles their Playwright
//     scripts from the acceptance criteria + the project QA manifest — so by
//     the time the issue reaches in_review the suite is sitting ready and the
//     gate only has to EXECUTE it.
//   - BACKFILL, on in_review: catches issues that skipped in_progress (or
//     pre-dated the prep trigger).
//
// Idempotent: skips if the issue already has test cases (the in_review call
// after a prep run, or a re-review after a hotfix, won't duplicate them).
// Best-effort + detached, gated by AGORA_AUTO_QA_ENABLED, mirrors
// maybeRunQAOnInReview.
func (h *Handler) maybeGenTests(ctx context.Context, issue db.Issue, actorType, actorID string, prep bool) {
	if !autoQAEnabled() {
		return
	}
	defer lockIssueQA(uuidToString(issue.ID))()
	// Already have cases (authored earlier, or by a prior in_review) → don't dup.
	if n, err := h.Queries.CountActiveTestCasesForIssue(ctx, db.CountActiveTestCasesForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	}); err != nil || n > 0 {
		return
	}
	agents := filterQAAgentsForScope(h.qaSquadAgents(ctx, issue.WorkspaceID), h.issueQAScopeTrivial(ctx, issue))
	if len(agents) == 0 {
		return
	}
	// Pick a QA agent that does NOT already have a pending task on this issue —
	// run_qa fired first (sequenced by the caller) and took one agent, so this
	// excludes it and gen_test_cases lands on a different agent instead of being
	// deduped away. If every QA agent is busy on this issue, skip (the gate is
	// the priority; cases can be authored on demand).
	var free []db.Agent
	anyPending := false
	for _, a := range agents {
		pending, err := h.Queries.HasPendingTaskForIssueAndAgent(ctx, db.HasPendingTaskForIssueAndAgentParams{
			IssueID: issue.ID,
			AgentID: a.ID,
		})
		if err == nil && pending {
			anyPending = true
		}
		if err == nil && !pending {
			free = append(free, a)
		}
	}
	// SHIFT-LEFT PREP fires standalone (no run_qa precedes it), so it must fire
	// EXACTLY ONCE per issue. If any QA agent already has a pending task on this
	// issue, authoring is already in flight (a prior prep goroutine, held off
	// only by the per-issue lock above) — bail instead of posting a duplicate
	// trigger comment. The in_review path keeps the different-agent behavior.
	if prep && anyPending {
		return
	}
	if len(free) == 0 {
		return
	}
	runner := h.pickLeastBusyQAAgent(ctx, free)

	instruction := buildSliceInstruction(sliceActionGenTests, "") +
		h.sliceActionQAManifestContext(ctx, issue) +
		h.sliceActionQADocsContext(ctx, issue) +
		qaPlanContext(issue.Description.String, issue.AcceptanceCriteria)
	if prep {
		// Dev is still working — there is no diff to read yet. The whole value
		// of the prep run is a suite that is EXECUTABLE the moment the issue
		// reaches in_review, so scripts are mandatory here, not optional.
		instruction += " SHIFT-LEFT PREP: the developer is still working on this task — do NOT look for a diff or a deployed change, and do NOT run anything. Author the cases from the acceptance criteria + the PROJECT QA MANIFEST above, and for EVERY automatable case also emit its runnable Playwright script (the script field) targeting the manifest's base_url/auth/routes — the in_review gate will only EXECUTE what you prepare now."
	}
	authorID, ok := actorAuthorID(actorID)
	if !ok {
		slog.Warn("auto gen_test_cases: invalid actor id, skipping", "actor_id", actorID, "issue_id", uuidToString(issue.ID))
		return
	}
	content := fmt.Sprintf("[@%s](mention://agent/%s) ", sanitizeMentionLabel(runner.Name), uuidToString(runner.ID)) + instruction
	comment, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  actorType,
		AuthorID:    authorID,
		Content:     content,
		Type:        "comment",
		ParentID:    pgtype.UUID{Valid: false},
	})
	if err != nil {
		slog.Warn("auto gen_test_cases: create comment failed", "error", err, "issue_id", uuidToString(issue.ID))
		return
	}
	h.triggerTasksForComment(ctx, issue, comment, nil, actorType, actorID, nil)
	slog.Info("auto gen_test_cases fired",
		"issue_id", uuidToString(issue.ID), "agent_id", uuidToString(runner.ID), "prep", prep)
}

// maybeRunTestsOnInReview fires the QA squad's run_test_cases when an issue
// enters in_review AND there are automated cases to EXECUTE — the issue's own
// (authored earlier) OR the project's standing base suite (regression). Without
// this the base suite is authored + promoted but never actually RUN, so no
// test_run rows / regression history are ever produced — the regression QA layer
// stays silent. Detached, best-effort, gated by AGORA_AUTO_QA_ENABLED; mirrors
// maybeGenTests. Fires alongside run_qa (the gate) and gen_tests
// (authoring) — three facets of one in_review.
func (h *Handler) maybeRunTestsOnInReview(ctx context.Context, issue db.Issue, actorType, actorID string) {
	if !autoQAEnabled() {
		return
	}
	// Need automated cases to run: the issue's own, or the project base suite.
	haveIssue := 0
	if cs, err := h.Queries.ListAutomatedTestCasesForIssue(ctx, db.ListAutomatedTestCasesForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	}); err == nil {
		haveIssue = len(cs)
	}
	haveBase := 0
	if issue.ProjectID.Valid {
		if cs, err := h.Queries.ListAutomatedTestCasesForProject(ctx, db.ListAutomatedTestCasesForProjectParams{
			ProjectID:   issue.ProjectID,
			WorkspaceID: issue.WorkspaceID,
		}); err == nil {
			haveBase = len(cs)
		}
	}
	if haveIssue == 0 && haveBase == 0 {
		return // nothing to execute yet
	}
	agents := filterQAAgentsForScope(h.qaSquadAgents(ctx, issue.WorkspaceID), h.issueQAScopeTrivial(ctx, issue))
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

	// Mirror the CreateSliceAction run_test_cases assembly so the auto-fired run
	// carries the same smoke target, manifest, docs, base suite, and case list.
	instruction := buildSliceInstruction(sliceActionRunTests, "")
	if url := h.devBoxSmokeURL(ctx, issue); url != "" {
		instruction += " SMOKE TARGET: the app is served at " + url + " — run the cases against THAT url."
	}
	instruction += h.sliceActionQASmokeContext(ctx, issue)
	instruction += h.sliceActionQAManifestContext(ctx, issue)
	instruction += h.sliceActionQADocsContext(ctx, issue)
	baseSuite := h.sliceActionProjectBaseSuiteContext(ctx, issue)
	instruction += h.sliceActionTestCasesContext(ctx, issue, baseSuite != "")
	instruction += baseSuite

	authorID, ok := actorAuthorID(actorID)
	if !ok {
		slog.Warn("auto run_test_cases: invalid actor id, skipping", "actor_id", actorID, "issue_id", uuidToString(issue.ID))
		return
	}
	content := fmt.Sprintf("[@%s](mention://agent/%s) ", sanitizeMentionLabel(runner.Name), uuidToString(runner.ID)) + instruction
	comment, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  actorType,
		AuthorID:    authorID,
		Content:     content,
		Type:        "comment",
		ParentID:    pgtype.UUID{Valid: false},
	})
	if err != nil {
		slog.Warn("auto run_test_cases: create comment failed", "error", err, "issue_id", uuidToString(issue.ID))
		return
	}
	h.triggerTasksForComment(ctx, issue, comment, nil, actorType, actorID, nil)
	slog.Info("auto run_test_cases fired on in_review", "issue_id", uuidToString(issue.ID), "agent_id", uuidToString(runner.ID))
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
//     the sprint-end full regression (and any manual mid-sprint re-run).
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
	// blow the comment size and we never split a multi-byte rune. 4000 runes —
	// Bitrix-imported legacy tickets routinely carry the whole spec in the
	// description; the old 1500 cap dropped the part QA was judging against.
	const maxDescRunes = 4000
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

// issueBriefNote renders the issue's full description + acceptance criteria for
// the CLAIM brief. The task-claim brief otherwise carries only the title + the
// trigger comment — for Bitrix-imported legacy tickets (long RU/UZ descriptions)
// that meant the dev agent never saw the actual spec. Neutral wording (unlike
// qaPlanContext's test-authoring frame) so it reads correctly for dev, QA, and
// design runs alike. PURE; returns "" when there is nothing beyond the title.
func issueBriefNote(description string, acceptanceCriteria []byte) string {
	desc := strings.TrimSpace(description)
	// Same rune-safe cap rationale as qaPlanContext.
	const maxBriefRunes = 4000
	if r := []rune(desc); len(r) > maxBriefRunes {
		desc = string(r[:maxBriefRunes]) + "…"
	}
	criteria := parseAcceptanceCriteria(acceptanceCriteria)
	if desc == "" && len(criteria) == 0 {
		return ""
	}
	var b strings.Builder
	// Precedence: newest human instruction wins. Comments are the platform's
	// live steering channel (mid-issue redirects, narrowed delegation scopes) —
	// the brief is the BASE spec, never an override of a newer comment.
	b.WriteString("ISSUE BRIEF — the full base spec of this issue (the title alone is NOT the spec). " +
		"A newer comment on the issue may NARROW or SUPERSEDE this brief — on conflict, follow the newest " +
		"human instruction (or ask); when the triggering comment is only a bare nudge, this brief IS the spec.")
	if desc != "" {
		b.WriteString("\nDescription: " + desc)
	}
	if len(criteria) > 0 {
		b.WriteString("\nAcceptance criteria:")
		for i, c := range criteria {
			b.WriteString(fmt.Sprintf(" (%d) %s;", i+1, c))
		}
	}
	return b.String()
}

// sliceActionTestCasesContext lists the issue's AUTOMATED test cases (id · title ·
// steps · expected) for run_test_cases, so the agent drives each one and reports
// a verdict per case keyed by the id we hand it. hasBaseSuite tells the no-cases
// note whether a PROJECT BASE SCRIPTS block follows: when it does, the note must
// NOT read as terminal ("author first, then re-run") — an agent following that
// directive would abort without running the standing regression suite.
func (h *Handler) sliceActionTestCasesContext(ctx context.Context, issue db.Issue, hasBaseSuite bool) string {
	cases, err := h.Queries.ListAutomatedTestCasesForIssue(ctx, db.ListAutomatedTestCasesForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil || len(cases) == 0 {
		if hasBaseSuite {
			return " NOTE: this issue has no issue-specific automated test cases — still run the PROJECT BASE SCRIPTS below (author issue-level cases via gen_test_cases when needed)."
		}
		return " NOTE: this issue has no automated test cases yet — author them first (gen_test_cases) or add some, then re-run."
	}
	var b strings.Builder
	b.WriteString(" AUTOMATED TEST CASES TO RUN (report a verdict for each by its id):")
	for _, c := range cases {
		steps := strings.ReplaceAll(strings.TrimSpace(c.Steps), "\n", " ")
		b.WriteString(fmt.Sprintf(" [id=%s] %s — steps: %s; expected: %s.",
			uuidToString(c.ID), c.Title, steps, strings.TrimSpace(c.Expected)))
		if s := strings.TrimSpace(c.Script); s != "" {
			b.WriteString(fmt.Sprintf(" COMPILED SCRIPT for [id=%s] — write to /tmp/case-%s.mjs and run `node` it; exit code is the verdict:\n```javascript\n%s\n```",
				uuidToString(c.ID), uuidToString(c.ID), s))
		}
	}
	return b.String()
}

// sliceActionProjectBaseSuiteContext lists the project's STANDING automated
// base scripts (golden-path regression cases stored with project_id set and
// issue_id NULL), so every run_qa / run_test_cases executes the known suite
// instead of re-inventing checks. Mirrors sliceActionTestCasesContext's
// id/title/steps/expected line format. Returns "" when the issue has no
// project or the project has no base cases.
func (h *Handler) sliceActionProjectBaseSuiteContext(ctx context.Context, issue db.Issue) string {
	if !issue.ProjectID.Valid {
		return ""
	}
	cases, err := h.Queries.ListAutomatedTestCasesForProject(ctx, db.ListAutomatedTestCasesForProjectParams{
		ProjectID:   issue.ProjectID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil || len(cases) == 0 {
		return ""
	}
	// Quarantine skip-list (project.settings.qa_quarantine: case-id strings) —
	// a flaky base script is EXCLUDED from the standing suite instead of
	// training agents to ignore red. Humans park a case here while it is fixed.
	quarantined := map[string]bool{}
	if project, perr := h.Queries.GetProject(ctx, issue.ProjectID); perr == nil && len(project.Settings) > 0 {
		var s struct {
			Quarantine []string `json:"qa_quarantine"`
		}
		if json.Unmarshal(project.Settings, &s) == nil {
			for _, id := range s.Quarantine {
				quarantined[strings.TrimSpace(id)] = true
			}
		}
	}
	if len(quarantined) > 0 {
		kept := cases[:0]
		for _, c := range cases {
			if !quarantined[uuidToString(c.ID)] {
				kept = append(kept, c)
			}
		}
		cases = kept
		if len(cases) == 0 {
			// Every base case is quarantined: the regression gate is effectively
			// OFF for this project. Say so loudly instead of silently injecting
			// nothing — a fully-parked suite reading as "no regression to run"
			// was an audit finding (coverage dropped to zero with no signal).
			slog.Warn("project base suite fully quarantined — regression gate is a no-op",
				"project_id", uuidToString(issue.ProjectID))
			return " PROJECT BASE SCRIPTS: every standing regression case is currently QUARANTINED, so NO base-suite " +
				"regression will run for this issue. Note this in your verdict summary — the project's regression gate " +
				"is effectively disabled until cases are un-quarantined or replaced."
		}
	}
	var b strings.Builder
	// The wording self-describes the test-runs JSON format because run_qa's base
	// instruction never defines it — only run_test_cases' base does.
	b.WriteString(" PROJECT BASE SCRIPTS — the project's STANDING golden-path regression suite (not this issue's cases). " +
		"Run them EVERY time, in order, exactly as written — do not invent replacements. Report a verdict for EACH by its id " +
		"in the fenced ```test-runs code block at the END of your comment: a JSON array " +
		"`[{\"test_case_id\":\"<id>\",\"status\":\"pass\"|\"fail\"|\"blocked\",\"output\":\"<one-line evidence>\"}]` " +
		"(the same block/format as issue test cases — merge both suites' entries into ONE block). " +
		"A base-script failure is a REGRESSION and blocks qa:pass even when this issue's own change looks fine:")
	for _, c := range cases {
		steps := strings.ReplaceAll(strings.TrimSpace(c.Steps), "\n", " ")
		b.WriteString(fmt.Sprintf(" [id=%s] %s — steps: %s; expected: %s.",
			uuidToString(c.ID), c.Title, steps, strings.TrimSpace(c.Expected)))
		if s := strings.TrimSpace(c.Script); s != "" {
			b.WriteString(fmt.Sprintf(" COMPILED SCRIPT for [id=%s] — write to /tmp/case-%s.mjs and run `node` it; exit code is the verdict:\n```javascript\n%s\n```",
				uuidToString(c.ID), uuidToString(c.ID), s))
		}
	}
	return b.String()
}

// uncompiledAutomatedCases collects the automated cases (issue's own + the
// project's standing base suite) that still lack a compiled script — the set
// compile_tests must author scripts for. Deduped by id.
func (h *Handler) uncompiledAutomatedCases(ctx context.Context, issue db.Issue) []db.TestCase {
	var out []db.TestCase
	seen := map[string]bool{}
	add := func(cases []db.TestCase) {
		for _, c := range cases {
			if strings.TrimSpace(c.Script) != "" {
				continue
			}
			k := uuidToString(c.ID)
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, c)
		}
	}
	if cs, err := h.Queries.ListAutomatedTestCasesForIssue(ctx, db.ListAutomatedTestCasesForIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	}); err == nil {
		add(cs)
	}
	if issue.ProjectID.Valid {
		if cs, err := h.Queries.ListAutomatedTestCasesForProject(ctx, db.ListAutomatedTestCasesForProjectParams{
			ProjectID:   issue.ProjectID,
			WorkspaceID: issue.WorkspaceID,
		}); err == nil {
			add(cs)
		}
	}
	return out
}

// sliceActionUncompiledCasesContext lists the automated cases that still need a
// compiled script (issue + project base) for compile_tests — id/title/steps/
// expected only, since the agent is AUTHORING the script, not running it.
func (h *Handler) sliceActionUncompiledCasesContext(ctx context.Context, issue db.Issue) string {
	cases := h.uncompiledAutomatedCases(ctx, issue)
	if len(cases) == 0 {
		return " NOTE: every automated case already has a compiled script — nothing to compile."
	}
	var b strings.Builder
	b.WriteString(" CASES TO COMPILE (author a script for EACH by its id):")
	for _, c := range cases {
		steps := strings.ReplaceAll(strings.TrimSpace(c.Steps), "\n", " ")
		b.WriteString(fmt.Sprintf(" [id=%s] %s — steps: %s; expected: %s.",
			uuidToString(c.ID), c.Title, steps, strings.TrimSpace(c.Expected)))
	}
	return b.String()
}

// qaCompileEnabled gates the auto-compile hook (a human-authored automated case
// gets a runnable script authored in the background). Default off — opt-in, and
// separate from AGORA_AUTO_QA_ENABLED because compilation is a pure authoring
// convenience, not the QA gate.
func qaCompileEnabled() bool {
	return config.Bool("AGORA_QA_COMPILE_ENABLED")
}

// maybeCompileTestCases fires the QA squad's compile_tests when an automated case
// on this issue still lacks a compiled Playwright script, so run_test_cases can
// later EXECUTE it deterministically instead of hand-driving. Detached, best-
// effort, gated by AGORA_QA_COMPILE_ENABLED. Only wired from the issue-scoped
// create handler (CreateIssueTestCase): compile needs an issue timeline to post
// the @mention comment to. Project-base cases are compiled on demand via the
// /compile_tests slice action a human/agent fires against an issue in that
// project — the create handler has no timeline to post to.
func (h *Handler) maybeCompileTestCases(ctx context.Context, issue db.Issue) {
	if !qaCompileEnabled() {
		return
	}
	// Nothing to compile → skip (also covers the case whose script was authored
	// inline by gen_test_cases).
	if len(h.uncompiledAutomatedCases(ctx, issue)) == 0 {
		return
	}
	agents := filterQAAgentsForScope(h.qaSquadAgents(ctx, issue.WorkspaceID), h.issueQAScopeTrivial(ctx, issue))
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

	instruction := buildSliceInstruction(sliceActionCompileTests, "") +
		h.sliceActionQAManifestContext(ctx, issue) +
		h.sliceActionQADocsContext(ctx, issue) +
		h.sliceActionUncompiledCasesContext(ctx, issue)
	// No request user on this detached path — attribute to the issue's creator,
	// mirroring the squad-failure-recovery path.
	authorType := issue.CreatorType
	if authorType != "member" && authorType != "agent" {
		authorType = "member"
	}
	content := fmt.Sprintf("[@%s](mention://agent/%s) ", sanitizeMentionLabel(runner.Name), uuidToString(runner.ID)) + instruction
	comment, err := h.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  authorType,
		AuthorID:    issue.CreatorID,
		Content:     content,
		Type:        "comment",
		ParentID:    pgtype.UUID{Valid: false},
	})
	if err != nil {
		slog.Warn("auto compile_tests: create comment failed", "error", err, "issue_id", uuidToString(issue.ID))
		return
	}
	h.triggerTasksForComment(ctx, issue, comment, nil, authorType, uuidToString(issue.CreatorID), nil)
	slog.Info("auto compile_tests fired on new automated case", "issue_id", uuidToString(issue.ID), "agent_id", uuidToString(runner.ID))
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

	agent, ok := h.resolveSliceActionAgent(w, r, issue, userID, strings.TrimSpace(req.AgentID), req.Kind)
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
			// Give the DEV the intended-behavior source of truth (the docs repo),
			// not just the ticket — so it builds against what the feature SHOULD
			// do, the same spec QA later judges it against (closes the dev/QA
			// docs asymmetry).
			instruction += h.sliceActionQADocsContext(r.Context(), issue)
			// A UI change builds against the project's known design system, so
			// reuse beats re-inventing components. "" when no manifest.
			instruction += h.sliceActionDesignManifestContext(r.Context(), issue)
		}
		instruction += h.sliceActionLandingInstruction(r.Context(), issue)
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
		// Risk map intentionally NOT appended here — the claim path injects it
		// into the same run's instructions (see daemon.go).
		instruction += h.sliceActionQASmokeContext(r.Context(), issue)
		instruction += h.sliceActionQAManifestContext(r.Context(), issue)
		instruction += h.sliceActionDesignManifestContext(r.Context(), issue)
		instruction += h.sliceActionQADocsContext(r.Context(), issue)
		instruction += h.sliceActionProjectBaseSuiteContext(r.Context(), issue)
		instruction += qaPlanContext(issue.Description.String, issue.AcceptanceCriteria)
		instruction += h.sliceActionDesignCompareContext(r.Context(), issue)
		instruction += h.sliceActionDesignLintContext(r.Context(), issue)
	}
	// auto_docs targets the project's configured docs repo when set.
	if req.Kind == sliceActionAutoDocs {
		instruction += h.sliceActionDocsRepoContext(r.Context(), issue)
		instruction += h.sliceActionQAManifestContext(r.Context(), issue)
	}
	// gen_test_cases authors cases from the issue's plan (description + criteria)
	// and the QA manifest so authored cases target KNOWN routes/flows, not guesses.
	if req.Kind == sliceActionGenTests {
		instruction += h.sliceActionQAManifestContext(r.Context(), issue)
		instruction += h.sliceActionQADocsContext(r.Context(), issue)
		instruction += qaPlanContext(issue.Description.String, issue.AcceptanceCriteria)
	}
	// run_test_cases drives the issue's automated cases against the box — same
	// smoke target as run_qa, plus the cases (id/title/steps/expected) to run.
	if req.Kind == sliceActionRunTests {
		if url := h.devBoxSmokeURL(r.Context(), issue); url != "" {
			instruction += " SMOKE TARGET: the app is served at " + url + " — run the cases against THAT url."
		}
		instruction += h.sliceActionQASmokeContext(r.Context(), issue)
		instruction += h.sliceActionQAManifestContext(r.Context(), issue)
		instruction += h.sliceActionQADocsContext(r.Context(), issue)
		// Compute the base suite FIRST: the no-cases note's wording depends on
		// whether the project's standing scripts follow it.
		baseSuite := h.sliceActionProjectBaseSuiteContext(r.Context(), issue)
		instruction += h.sliceActionTestCasesContext(r.Context(), issue, baseSuite != "")
		instruction += baseSuite
	}
	// compile_tests authors runnable Playwright scripts for the automated cases
	// that still lack one (issue + project base), against the QA manifest.
	if req.Kind == sliceActionCompileTests {
		instruction += h.sliceActionQAManifestContext(r.Context(), issue)
		instruction += h.sliceActionQADocsContext(r.Context(), issue)
		instruction += h.sliceActionUncompiledCasesContext(r.Context(), issue)
	}
	// design_proposal reads the issue's Figma designs and maps them against the
	// project's design system. Append the Figma how-to (fileKey/nodeId calls) and
	// the project design manifest.
	if req.Kind == sliceActionDesignProposal {
		if note := figmaContextForIssue(issueFigmaRefs(issue)); note != "" {
			instruction += "\n\n" + note
		}
		instruction += h.sliceActionDesignManifestContext(r.Context(), issue)
	}
	// gen_design_manifest builds/refreshes the project design system. Hand the
	// agent the CURRENT manifest so it updates (not clobbers) human entries, and
	// the Figma how-to when the issue references a library file.
	if req.Kind == sliceActionGenDesignManifest {
		if note := figmaContextForIssue(issueFigmaRefs(issue)); note != "" {
			instruction += "\n\n" + note
		}
		instruction += h.sliceActionDesignManifestContext(r.Context(), issue)
	}
	// design_audit scans the repo against the project design manifest.
	if req.Kind == sliceActionDesignAudit {
		instruction += h.sliceActionDesignManifestContext(r.Context(), issue)
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
func (h *Handler) resolveSliceActionAgent(w http.ResponseWriter, r *http.Request, issue db.Issue, userID, agentID, kind string) (db.Agent, bool) {
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

	// (a.5) QA-family actions (run_qa / gen_test_cases / run_test_cases) are the
	// QA team's responsibility, NOT the developer whose work is under test.
	// Without an explicit agent, default to the QA squad LEADER — the same
	// routing the auto-QA trigger uses — so a manual "Re-run QA" is owned by QA
	// validation, never routed to the issue's dev assignee (which produced
	// "@Developer 3 Run QA" on a dev-assigned issue). Falls through to the
	// assignee/own-agent defaults below only when the workspace has no ready QA
	// squad leader, so a setup without a QA squad still works.
	if isQASliceAction(kind) {
		if leader, ok := h.qaSquadLeader(r.Context(), issue.WorkspaceID); ok {
			return leader, true
		}
	}

	// (a.6) design_proposal is the designer-analyst's job. Without an explicit
	// agent, resolve the project's configured design agent, else a "design"
	// squad's leader — the same way QA routes to its squad. Falls through to the
	// assignee / own-agent defaults when neither resolves.
	if kind == sliceActionDesignProposal {
		if designer, ok := h.resolveDesignerAgent(r.Context(), issue); ok &&
			h.canAccessPrivateAgent(r.Context(), designer, "member", userID, workspaceID) {
			return designer, true
		}
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
