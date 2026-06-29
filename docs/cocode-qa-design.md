# Co-code QA Process — Design & Build Plan

**Status:** research-backed design, **not yet built**. Handoff doc (continuation by another session/account).
**Date:** 2026-06-27. **Research:** deep-research workflow `wf_e6896a05-813` — 21 verified claims (3-vote adversarial), 4 refuted, 30 sources.

---

## 0. Goal
Add a **full QA process** to the co-code pipeline that is **trustworthy as a merge gate** and **reuses the embedded Chromium + Playwright**, so an agent's change on `cocode/<issue-key>` is verified (deterministically) before merge — with the QA running **visibly** in the editor's Browser pane.

## 1. Existing pipeline + QA spine (REUSE — do not rebuild)
**Co-code pipeline (built, local self-host):** Ask → agent works on `cocode/<issue-key>` → review (changed-files list, diff, live activity) → Verify (CI checks) → Accept→Open PR → human merges. Embedded headless **Chromium streamed via CDP screencast** + **Preview** pane (dev server) + **Browser** pane (`connectOverCDP`-able) in the editor modal.

**QA spine already in the codebase:**
- `server/internal/handler/merge_readiness.go` — **tiered gate**: `reviewTierForLabels` (trivial/light/full) → required gates; `gateFromLabels`; `MergeReadiness` handler returns `{ready, blocked[]}`. Gates: **ci · qa · security · code-review**. Verdicts are labels `<gate>:pass|fail`. **BACKEND-ONLY — not surfaced in the editor.** Route: `GET /api/issues/{id}/merge-readiness`.
- `server/internal/handler/slice_action.go` — `run_qa` (→ `sd-qa-process` skill) + `run_ci` kinds; `Focus on: <scope>` clause.
- `sd-qa-process` workspace skill — Playwright smoke, **hardcoded to the sddev box** (deploy branch → Playwright → qa label). DB-only.
- QA Tester / Security Reviewer agents; QA plugin; verdict→Bitrix mirror (`mirrorQAVerdictToBitrix` on `EventIssueLabelsChanged`).
- **PROVEN (2026-06-27):** `playwright-core` `chromium.connectOverCDP(<embedded cdp_url>)` drives the embedded Chromium — same browser the Browser pane streams → **watch-along QA**. (Daemon `/editor/browser/start` returns the `cdp_url`.)

## 2. Research findings (verified)
1. **Proof-of-work, not pass/fail** — agent plans a **code-grounded** test → operates the running app → returns **reviewable artifacts** (annotated screenshots, video, traces). [cognition.ai/blog/testing-development]
2. **Playwright ships a 3-agent loop** (v1.56): **Planner → Generator → Healer**; Healer = self-healing/flake recovery (replay → relocate element → patch → re-run, or mark `test.fixme` with a reason). [playwright.dev/docs/test-agents]
3. **a11y-snapshot mode > vision/coordinate** for the gate — reliable, deterministic, ~200–400 tok vs ~3–5k for screenshots. Vision is a fallback (`--caps=vision`). [playwright.dev/mcp/snapshots, /mcp/vision-mode]
4. **Assured-LLMSE filter funnel** (Meta TestGen-LLM, Qodo-Cover): generate → **build → run → keep only if it passes AND raises coverage**. ~25% of raw LLM tests survive (Reels/Stories: 75% build, 57% pass, 25% raise coverage). [arxiv 2402.09171, github.com/qodo-ai/qodo-cover]
5. **Diff→regression test = fail-before / pass-after** the patch (proves the fix). [arxiv 2501.11086] *(medium confidence — single small study)*
6. ⚠️ **Unsupervised "agent clicks through the app" = 85% false positives** (WebProber, Claude 3.7, 120 sites). → exploratory computer-use must be a **triaged signal, NEVER a direct `qa:fail`**. [arxiv 2509.05197]
7. **Gate order**: AI-review-first → **mandatory human** (non-optional). **Any CI-weakening change = hard block** (tests removed/renamed/skipped, coverage threshold lowered, workflow disabled on PRs, `|| true`). **Require a NEW test that fails on the pre-change behavior** as proof. [github.blog/…/agent-pull-requests-are-everywhere]

**Refuted (do NOT rely on):** Devin auto-triggers QA right after PR (it's on-request); Copilot test/lint only from custom-instructions; Playwright Generator runs the live scenario before commit; Qodo-Cover uses Meta's 5× re-run flake heuristic.

## 3. Recommended QA pipeline
```
cocode/<key>  →  ground a test plan in the DIFF + source (Planner)
  → DETERMINISTIC GATE (Playwright a11y-mode, connectOverCDP → embedded Chromium → Preview):
       • smoke/e2e against the dev server
       • diff regression test: fail-before / pass-after, accepted ONLY via build+pass+coverage filter
       • visual-regression (toHaveScreenshot)
       • trace + screenshots = proof-of-work artifacts → attached to the PR
     ⇒ qa:pass / qa:fail   (TRUSTWORTHY — executed, deterministic)
  → EXPLORATORY signal (computer-use on the embedded Chromium): agent clicks through →
     flags issues → TRIAGED (auto re-verify in Playwright) → NEVER a direct qa:fail
  → AI-review  →  CI-weakening hard-block  →  mandatory human
  → MergeReadiness (tiered)  →  merge
```
**Two browser modes on ONE Chromium:** screencast/coordinate = human watch-along + exploration; **Playwright a11y-mode = the gate**. Computer-use explores/grounds; **Playwright asserts/gates**.

## 4. Build plan (phases)
**Phase 1 — Deterministic QA backbone ⭐ (start here)**
- Generic Playwright QA skill (replace the SD-box `sd-qa-process` hardcoding): `connectOverCDP(<embedded cdp_url>)` → smoke the **Preview** dev server in **a11y-mode** → capture **trace + screenshots** → set `qa:pass`/`qa:fail` label + post artifacts as a comment.
  - Wire as a generic `run_qa` executor (project-configurable smoke command/URL via project metadata, not the hardcoded box).
  - The daemon already exposes the embedded Chromium CDP (`/editor/browser/start` → `cdp_url`) + the Preview (`/editor/preview` → url). The QA skill consumes both.
- **Gates panel** in the editor: consume `GET /api/issues/{id}/merge-readiness` → show each gate (ci/qa/security/code-review) pass/fail/pending + "Ready / Blocked by qa", next to **Accept→Open PR**. (Frontend, reuses the existing backend.)
- **Run QA** button in the editor → fire the QA Tester agent on the branch (like `run_qa`, generic).

**Phase 2 — Diff→regression tests**
- From the `cocode/<key>` diff, generate a **fail-before/pass-after** test; accept ONLY via an **executed build+pass+coverage** filter (Assured-LLMSE / Qodo-Cover pattern). Commit the test into the PR (reviewed).

**Phase 3 — Planner→Generator→Healer**
- Adopt Playwright's official 3-agent loop (via Playwright MCP, a11y-mode) so plans are code-grounded + the **Healer** gives flake self-recovery. Bind as a QA plugin/skill.

**Phase 4 — Exploratory + visual-regression + a11y**
- Computer-use exploratory click-through on the embedded Chromium = **triaged signal** (auto re-verify flagged issues in Playwright before any `qa:fail`). Add visual-regression (`toHaveScreenshot`) + accessibility checks.

**Cross-cutting — gate trust**
- **CI-weakening hard-block** (detect test-deletion/skip/coverage-lowering in the diff → block).
- **AI-review-first → mandatory human** (GitHub branch protection: required reviews; already enabling per repo).
- **Flake quarantine policy** (design our own — Meta's 5× was refuted): retry-N + auto-quarantine repeatedly-flaky tests; Healer auto-patches must themselves be reviewed.

## 5. Open decisions (settle before/while building)
1. **Tier→gate matrix** — which gates per trivial/light/full? (e.g. trivial=ci; light=ci+qa-smoke; full=ci+qa-e2e+visual+security+code-review).
2. **Exploratory-signal triage** — how to promote a raw agent-flagged issue to a real `qa:fail` without a human every PR (auto re-run the scenario deterministically in Playwright + require a reproducing assertion).
3. **Flake policy** — retry-N thresholds, quarantine, Healer-patch review.
4. **Test/visual-baseline ownership** — committed to the repo + reviewed in the PR, vs ephemeral CI artifacts; who updates baselines on intentional UI changes.

## 6. Handoff pointers
- **Co-code feature state:** complete + local self-host (NOT on prod). See memory `agora-live-editor` for the full architecture + daemon-swap mechanics + every shipped piece.
- **Daemon = the `agora --profile local` process on `127.0.0.1:20038`** (editor/preview/browser/changes/open-pr/discard endpoints in `server/internal/daemon/health.go` + `browser.go`). Daemon deploy = cross-compile arm64 + `codesign --force -s -` + replace both `/opt/homebrew/bin/agora` + `/usr/local/bin/agora` + force-kill + restart with the captured PATH (a co-located 2nd Claude session auto-restarts it).
- **Branch protection** (the real merge wall): `gh api -X PUT repos/<owner>/<repo>/branches/main/protection` with `enforce_admins:true` + required PR — **the user runs it** (Claude won't modify repo access controls). Done on `jamshidtulaganov/browser-automation`; pending on the johny-mercer4 Octane repos (owner must enable).
- **Co-code branch isolation** is instruction + daemon `ensureCoCodeBranch` (forces `cocode/issue-<n>-<agent8>`), but an autonomous agent can still merge-to-main → **branch protection is the only hard guarantee**.
- **Full cited research report:** was at session scratch `tasks/w74japzw8.output` (ephemeral — findings copied into §2 above).
- **Proven:** `connectOverCDP` test script at the session scratchpad (`cdp_test.js`) — Playwright drives the embedded Chromium.

**Recommended next step:** build **Phase 1** (generic Playwright QA skill via connectOverCDP→Preview + Gates panel on MergeReadiness + Run-QA button). It's the trustworthy core + builds on the proven connectOverCDP.
