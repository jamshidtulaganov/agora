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

## Cadence model (the refined decision)

QA cadence is tied to Agora's work units (task, sprint), not wall-clock — with one
cheap backstop so cross-task regressions don't hide until sprint close.

| Cadence | Runs | Purpose | Gate |
|---|---|---|---|
| **Per TASK** (every issue/PR) | FAST PATH: `run_ci` (lint+build+test by exit code; any test weakening/deletion/skip = fail) ∥ `run_qa` smoke (deterministic baseline-diff) + a fail-before/pass-after regression test for the change | fast feedback; the task's own regressions caught immediately | `ci:pass` + `qa:pass` (blocks merge) |
| **Merge → main** (cheap backstop) | regression **suite** re-run — exit-code only, NO browser/perf | catch **cross-task** regressions within a day, not at sprint end | `regression:fail` (advisory) |
| **Per SPRINT** (sprint close) | FULL PATH: full regression + integration + e2e + performance + security + accessibility across the sprint's integrated changes, on the deployed QA box | sprint / release gate | `release` tier (`ci+qa+security+code-review`) |

Self-improvement happens **per task** (each bug → a regression test); its payoff
shows at the **merge** + **sprint** cadences, which re-run the growing suite.

Why not per-task-full or nightly-only: per-task full is too slow (the pain);
nightly-only hides cross-task regressions on a wall-clock that's meaningless to a
task board. Task=fast / merge=cheap-backstop / sprint=full is the balance.

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
- **Autopilot** — nightly/sprint heavy suite = a scheduled autopilot (`run_only`,
  QA Tester assignee, `scope=regression`); `shouldSkipDispatch` pre-flight guards
  the offline-daemon SPOF. Merge backstop hangs off a merge-to-main event.
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
