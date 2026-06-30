# Agora QA process — design

The best automated, multi-test-type QA process for Agora. Deterministic-first,
label-driven, cadence-tiered, self-improving — built on Agora's existing
primitives (slice-actions, label gates, merge-readiness tiers, autopilot,
deploy-qa, the agent/skill fleet), not a parallel system.

> Source: synthesized from a 3-angle design panel (test-pyramid / automation-chain /
> agent-fleet) judged on coverage, determinism+speed, fit-to-Agora, and feasibility.

## Core principles

1. **Deterministic-first, vision-last.** Every verdict is decided by an EXIT CODE
   or a structured signal (HTTP status, console output, network responses, asserted
   DOM / accessibility-tree TEXT). A screenshot is *only ever* post-verdict
   documentation of an already-confirmed failure — never the thing that decides
   pass/fail. (This is the fix for the #1 pain: slow screenshot+vision QA.)
2. **Test inside Agora.** Drive the co-code editor's embedded Chromium over CDP
   (`playwright-core` `connectOverCDP`), never an external Playwright/Chrome spun up
   to eyeball screenshots.
3. **Baseline-diff = judge the change, not the repo.** Run each check on the
   merge-base and on the branch; a signal red on BOTH is pre-existing (note, do not
   block); green-on-base + red-on-branch is a NEW failure (blocks). Baseline-diff is
   also the primary flake filter.
4. **Cadence-tiered.** Fast per task, cheap backstop on merge, full per sprint —
   so the PR gate stays ~2 min while heavy suites still run.
5. **Advisory by default; the human merges.** Gates are signals; correctness gates
   (ci, qa) hard-block, quality signals (perf/a11y/visual) surface but never block
   except at the `release` tier. The agent never merges.
6. **Self-improving.** Every confirmed bug becomes a permanent fail-before/pass-after
   regression test, captured into a workspace skill so the whole squad inherits it.

## Cadence model — one branch per sprint (this team)

The team runs **one git branch per sprint** (`sprint/<sprintId>`, cut from `main`/`billing`
at sprint start); ALL tasks for the sprint commit onto that shared branch — there is no
per-task PR branch. Sprints are 2 weeks; at sprint end the whole branch gets a **full
regression**, then a human merges it. The cadence has three tiers:

| Cadence | Trigger | Branch | Runs | Baseline | Gate |
|---|---|---|---|---|---|
| **Per task** | issue → `in_review` / slice done (`run_qa`) | sprint tip | `run_qa scope=task`: deterministic build+lint+test diff + smoke (`qa_smoke_cmd`/`url`) + the task's FE/BE unit + API tests | **`refs/sprint/<id>/last-green`** (moving) — diff last-green → tip attributes a NEW failure to *this* task's commits | `ci:pass` + `qa:pass` on the issue |
| **Daily backstop** | cron `0 8 * * 1-5` (run_only autopilot → QA squad) | sprint tip | `run_qa scope=regression` (full suite + smoke) — no issue, posts a sprint-level qa-result | **sprint-root** (`merge-base main..sprint/<id>`, fixed) — whole-branch-vs-base catches **cross-task** drift | `sprint:regression:fail` (advisory; blocks close on final day) |
| **Sprint end** | autopilot polls sprints `status='active' AND end_date<=now()` | sprint tip → merge | FULL: regression + integration + e2e + perf + security + a11y, Lead QA orchestrates the squad; deploy-qa syncs the sprint branch to the box first | **sprint-root** — the entire sprint's net change vs the real merge target | `sprint:qa:pass` → merge-ready (human merges) |

### The `last-green` ref — shared-branch attribution

On a shared sprint branch the stock baseline (`merge-base main..sprint/<id>`) freezes at
sprint start, so after task 1 you can't tell which task turned a check red. Fix: a
**moving `refs/sprint/<id>/last-green`** — created at sprint start (= sprint-root), advanced
to the tested SHA **only after a fully-green run**, never backward. It's a git ref (survives
across agent runs + boxes; no DB state).

- **Per-task baseline = last-green** → diff isolates *exactly what landed since last-known-good* = this task. `green-on-last-green + red-on-tip` = NEW (blocks); `red-on-both` = pre-existing (advisory).
- **Whole-branch baseline = sprint-root** (daily + sprint-end) → answers "is the accumulated sprint healthy vs the base we'll merge into" — the cross-task question.
- Run the per-task gate **eagerly** (on each `in_review`) so last-green advances often and each delta stays one-task-sized. Force-push that orphans last-green falls back to sprint-root (coarser for one run) — note it in the verdict.

## Test strategy — per-task tests, golden paths, analytics-driven

**1. Per task: FE + BE unit + API tests (mandatory) — derived from the PLAN, not the diff.**
Every task's `run_qa` WRITE-TESTS step authors tests that assert the task's **intended behavior**,
read from the issue's **acceptance criteria + description** (injected into the `run_qa` instruction
via `qaPlanContext`, since the task-claim brief otherwise carries only the title + trigger comment).
The gate rejects a task with no coverage for its plan:
- **Source of truth = the plan, not the implementation.** The diff tells you WHERE the behavior
  lives; the acceptance criteria tell you WHAT is correct. Each criterion → at least one test. If
  the implementation diverges from the plan, the test must **fail** (a real bug surfaced) — never
  rewrite or weaken a test to match the code. Deriving tests from the diff is circular: the same
  agent that wrote the code then writes tests that only confirm "the code does what the code does",
  so a mis-implementation is encoded as a passing test instead of caught. A criterion with no
  covering test is a coverage gap, reported in the verdict.
- **FE unit** — sd-bridge: vitest (changed component/logic).
- **BE unit** — sd-main/cs3: phpunit/codeception (changed function).
- **API tests** — request→response contract (status, schema, auth) for changed/new endpoints.
- Bug fix → the criterion is "the bug no longer reproduces": fail-before/pass-after proof;
  test-weakening/skip/coverage-drop → gate fail.

**2. Golden paths — the daily-critical flows (permanent, release-blocking).** The features
clients use every day form a permanent e2e + API regression suite that ALWAYS runs at
sprint-end (and a subset daily):
- **Create order** · **Kassa (cash/payments)** · **Warehouses**
- Deterministic e2e: login → create order → assert; kassa payment → assert; warehouse stock
  → assert — plus API tests on the underlying endpoints. A regression here = `sprint:qa:fail`
  (blocks merge), non-negotiable. Configured as `project.settings.qa_critical_paths`.

**3. Analytics-driven prioritization (Google Analytics).** Beyond the known golden paths,
rank the regression/e2e suite by **real usage** from GA4 — test what's used most and breaks
most painfully (data-driven, risk-based). The Lead QA Engineer pulls GA top-flows
(monthly/per-sprint) and scopes the regression to: golden paths (always) + the top-N
most-used flows. Wiring: a GA4 read (property + service-account) → a ranked
`qa_critical_paths` list the sprint-end regression iterates.

## Test matrix

| Type | When | Trigger | Gate label | Deterministic signal | Owner |
|---|---|---|---|---|---|
| **smoke** | every PR (fast gate) | `run_qa`; editor Run-QA button | `qa:pass`/`qa:fail` | `qa_smoke_cmd` exit code, else Playwright a11y-mode over embedded Chromium: 0 console errors+warnings (a vue-i18n missing-key warning is a FAIL), 0 4xx/5xx, required elements present, 0 raw i18n placeholder keys. Baseline-diff. | QA Tester + deterministic-smoke skill |
| **unit** | every PR (in run_qa CHECKS) | `run_qa`; dev pre-commit | folds into `qa` | test-runner exit code (vitest/jest/phpunit/go test); green-base+red-branch = NEW | Developer writes; QA Tester accepts |
| **integration** | full tier + sprint | `run_qa` CHECKS; sprint | folds into `qa` | suite exit under baseline-diff; mock external deps, never live | Developer + QA Tester |
| **e2e** | full tier / risky; full set per sprint | `run_qa` scope=e2e; sprint | folds into `qa` | Playwright a11y-mode exit over CDP — assert TEXT + element presence + network status, never pixels/vision | QA Tester + playwright-e2e skill |
| **regression** | every bug fix (mandatory); re-run after | `run_qa` WRITE-TESTS on `type:bug`; merge + sprint | `qa:pass` (+fail-before/pass-after proof) | test fails on pre-change, passes after — proven on-branch by exit code; committed for review | Developer + regression-capture skill |
| **visual-regression** | opt-in (UI-heavy); only when .tsx/.css change | `run_qa` when `qa_visual_enabled` | `visual:fail` (advisory) | `toHaveScreenshot()` pixel-diff exit vs committed baseline; intent inferred from the code diff, never vision | Designer + visual-regression skill |
| **performance** | opt-in; full tier + release | `run_qa` when `qa_perf_threshold` set; sprint | `perf:warn`/`perf:fail` (blocks release only) | Lighthouse-over-CDP JSON (LCP/CLS/bundle) vs baseline; > threshold = fail | QA Tester + performance skill |
| **security** | every PR (cheap diff scan); deep on demand + sprint | `run_qa` diff scan; Security Reviewer; sprint dep-scan | `sec:pass`/`sec:fail` (advisory full, required release) | SAST (semgrep/codeql) + dependency audit + secret scan exit under baseline-diff; NEW critical/high in the diff blocks; structured `{severity,file,line,remediation}` | Security Reviewer + sd-security-review |
| **accessibility** | full tier (co-produced with smoke) | `run_qa` smoke a11y assertions; dedicated axe run when UI-heavy | `a11y:warn`/`a11y:fail` (advisory) | axe-core JSON + `getByRole`/heading-hierarchy exit under baseline-diff; NEW violations block, pre-existing noted | Designer + accessibility-review (WCAG) |

## Automation model

- **FAST PATH** (every PR): `ci:pending` + `run_ci` ∥ `run_qa` smoke-only → verdict in ~2 min; each posts a fenced `ci-result`/`qa-result` JSON block and sets its label; merge-readiness polls labels.
- **HEAVY PATH** (per sprint, batched per project): sprint-close fires `run_qa scope=regression` over the sprint's aggregate diff on the deployed box → the release gate.
- **MERGE BACKSTOP** (merge→main): cheap regression-suite re-run (exit-code only) so cross-task breaks surface within a day.
- **SELF-IMPROVING LOOP**: `type:bug` → Developer authors a fail-before/pass-after test → on `qa:pass` it's committed → bug + root cause appended to the `regression-capture` workspace skill → every agent inherits it at task-claim (`LoadAgentSkills` → `CLAUDE.md`). `capture-experience` logs flaky-investigation lessons into the same skill.
- **DOCS CHAIN** (exists): `qa:pass` → `maybeAutoDocsOnLabel` → `auto_docs` to the docs agent when `docs_repo` is set.
- **FLAKE HANDLING**: baseline-diff is the primary filter; on a branch-only failure re-run baseline+branch 2× — if the branch flips while baseline stays consistent, mark FLAKY (do not block); where Playwright Healer can self-patch a selector, commit it as a separate auditable commit.
- **DEPLOY-QA** (opt-in, advisory): SSH git-sync an issue's PR branch to its bound QA box → smoke the box at `qa_smoke_url` with `qa_smoke_cmd` → `qa:deploy:pass`/`qa:deploy:fail` (separate advisory label, NOT in the required gate). High value for the SD PHP stack.
- **EXPLORATORY** (never a direct verdict): an agent may click through the embedded browser and FLAG anomalies as a comment — a flag is never `qa:fail`; the scenario is re-run deterministically and only a reproducing assertion is promoted.

## Merge-gate tiers

Extends `merge_readiness.reviewTierForLabels` / `gateFromLabels`. `{gate}:pass`=pass,
`{gate}:fail`=fail (blocks), neither=pending (also blocks). `ready` = all REQUIRED
gates pass; `reviews[]` lists advisory gates. The agent never merges — branch
protection is the hard wall, merge-readiness is the signal.

| Tier | When | Required gates |
|---|---|---|
| `tier:trivial` | docs/config/comment, zero runtime risk | `ci` |
| `tier:light` | low-blast-radius code | `ci` (QA recommended, not required) |
| **full** (DEFAULT) | API/UI/business logic/auth/billing/data | `ci` + `qa` (+ advisory `sec`/`perf`/`a11y`/`visual`/`code-review`) |
| `release` (NEW) | pre-prod cut / billing / auth | `ci` + `qa` + `security` + `code-review`; `perf:fail` blocks when `qa_perf_threshold` set |

Hard invariants: `ci:fail`/`ci:pending` always blocks; test-weakening forces `ci:fail`
even when exit codes are green; advisory gates never flip `ready=false` on their own;
a human clicks Merge.

**Sprint-level gate (sprint-branch model).** Two altitudes: per-ISSUE gates (above) gate
each task; a new **`sprint:qa:pass`/`fail`/`pending`** gate (set by the Lead QA at the
sprint-end regression) gates the SPRINT BRANCH. A `SprintReadiness` aggregation makes the
sprint merge-ready only when `sprint:qa:pass` **AND** every constituent issue is `qa:pass`.
The daily backstop's `sprint:regression:fail` is advisory but blocks close on the final day.
`sprint:qa:pass` is advisory merge-readiness — never wire an auto-merge to it; a human merges
sprint → main.

## Mapping to Agora primitives

- **`slice_action.go`** — NO new slice-action kinds (set stays closed). Extend the
  `run_qa` instruction in-place to enumerate integration/e2e/a11y-smoke steps + the
  `type:bug` fail-before/pass-after mandate. Use the existing `scope` param to carry
  depth (`scope='regression'|'e2e'`). `run_ci` keeps the agent-fixes-the-test hard-block.
- **`merge_readiness.go`** — add a `release` case to `reviewTierForLabels`
  (required `ci,qa,security,code-review`). `gateFromLabels` needs no change (it
  resolves any gate name from labels).
- **Labels** — required `ci:*`/`qa:*` exist; add advisory `sec:*`, `code-review:*`,
  `perf:*`, `a11y:*`, `visual:*`, `qa:deploy:*`, plus `tier:release`, created on
  demand behind `project.settings.qa_auto_create_gates`.
- **Skills** (workspace-scoped, injected into `CLAUDE.md` at task-claim) — bind by
  agent: QA Tester → deterministic-smoke + playwright-e2e + performance; Developer →
  test-framework + regression-capture (self-improving); Designer → visual-regression
  + accessibility-review; Security Reviewer → sd-security-review; Reviewer →
  code-review; `capture-experience` feeds regression-capture.
- **`project.settings`** — existing: `qa_smoke_cmd`, `qa_smoke_url`, `docs_repo`,
  `docs_agent`. Add (parsed via `parseWithFallback`): `qa_perf_threshold`,
  `qa_visual_enabled`, `qa_a11y_enabled`, `qa_box_id`, `qa_auto_create_gates`.
- **Sprint-branch wiring** — branch `sprint/<sprintId>` (cut from base at sprint start);
  `refs/sprint/<id>/last-green` created = sprint-root, advanced by the agent (`git update-ref`)
  after each green run. `run_qa scope` ∈ `task` (baseline=last-green) / `regression`
  (baseline=sprint-root); each scope encodes its baseline ref in the instruction. A thin
  `DeploySprintQA` wrapper resolves the sprint's project box and passes the sprint branch to
  `DeployIssueQA` (already takes an explicit `{branch}`) — zero transport change.
- **Autopilot** — daily backstop = `run_only` autopilot, assignee=**QA squad**, schedule
  cron `0 8 * * 1-5`, payload `{scope:regression, branch:sprint/<active>, baseline:sprint-root}`.
  Sprint-end = a frequent (hourly) `run_only` autopilot whose dispatch resolves sprints
  `status='active' AND end_date<=now()` (no fixed 2-week cron — sprints start on arbitrary
  dates), runs `scope=regression`, marks the sprint completed. **Required Phase-1 change:**
  extend `DispatchAutopilot`'s `TriggerPayload`→instruction rendering from webhook-only to
  the `schedule` source so the agent sees `scope/branch/baseline`. `resolveAutopilotLeader`
  routes the squad to the Lead QA Engineer. `shouldSkipDispatch` guards the offline-daemon SPOF.
- **deploy-qa** — `DeployIssueQA` + `remote_box_sync.buildGitSyncScript` (SSH
  git-sync, ephemeral token, glue-preserving force-checkout) already wired + verified.

## Roadmap

- **Phase 0 — exists today.** run_qa deterministic baseline-diff gate; run_ci with
  test-weakening hard-block; merge tiers trivial/light/full; qa:pass→auto_docs;
  deploy-qa SSH git-sync; agent fleet + skills.
- **Phase 1 — generic deterministic QA + scheduling (highest ROI, S–M).** Generic
  deterministic-smoke skill (done); extend run_qa enumeration; nightly/sprint
  heavy-suite autopilot (the speed lever); advisory-label plumbing.
- **Phase 2 — release tier + self-improving regression (M).** Add `release` tier
  (Go table tests); make Security/Reviewer emit `sec:*`/`code-review:*` labels;
  build the regression-capture skill; wire capture-experience lessons in.
- **Phase 3 — advisory quality layers, opt-in (M–L).** visual-regression
  (`toHaveScreenshot`, baselines in git); performance (Lighthouse/CDP); dedicated
  axe a11y; deterministic flake handling (2× re-run, Healer self-patch committed).

## Risks

- **Cost/latency** — full QA on every PR is expensive. Mitigated by smoke-first on
  PR, sprint-batched heavy suite, tier downgrade, opt-in advisory layers, exit-code
  verdicts cheap enough for a small model.
- **Mis-tiering** — a risky change mislabeled trivial merges on ci-only. Mitigated:
  `full` is the default (you opt DOWN), cost-tier model makes wrong tiers expensive,
  Reviewer + human can re-label.
- **Advisory gates ignored** — surface them prominently in the editor QA panel +
  the sprint dashboard; promote to required per project when ready.
- **Regression suite unbounded growth** — keep tests targeted; full set only at
  merge + sprint; periodically consolidate.
- **Vision creep** — visual-regression must stay pixel-diff + code-diff-intent,
  never an LLM eyeballing a screenshot. Keep this explicit in skill text.
- **API drift on new `project.settings` keys** — `parseWithFallback` +
  optional-chaining + a malformed-response test per key.
- **Label-emitting reviewers** — only add `security`/`code-review` to a tier's
  required[] AFTER its agent reliably emits the label (missing = pending = block).
- **Daemon/runtime SPOF** — nightly autopilot + deploy-qa depend on an online
  daemon + a single runtime; add a runtime fallback so a wedged daemon doesn't
  silently drop the sprint gate.
