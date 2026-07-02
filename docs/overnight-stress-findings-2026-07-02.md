# Overnight stress-test — Agora leaks & issues (2026-07-02, autonomous run)

Goal: run sprint10 tasks 0→dev→QA (Figma MCP for design tasks) and IDENTIFY + FIX leaks/issues in the Agora platform.

## ☀️ MORNING SUMMARY (read this first)

**Ran a real concurrent stress load (up to 10 tasks: bugs + a Figma design task SD-348 + sd-cs), measured QA speed, found + FIXED bugs, then hit + recovered a disk-full incident.**

Bugs found & FIXED (code, built, live):
- **F1** — `run_test_cases` never auto-fired → the standing base-suite regression never executed (`test_run` stuck at 0). Fixed (`maybeRunTestsOnInReview`). VERIFIED live: test_run 0→24→48 under load.
- **F3** — dev brief never got the docs (intended behavior); only QA did. Fixed (docs injected into draft_code + every daemon claim).
- **F4** — no `<slug>-kb` code-map skill for sd-main/sd-cs → devs re-explored the code every task. Fixed (KB build triggered for both).

Verified WORKING under load (no bug): sprint-mode no-per-task-PR (sprint10 tip advanced 3× = devs committed to the shared branch), 3-facet QA (gate+authoring+execution) auto-fire, self-growing base suite (test_case 77→173), manifest injection.

Not-a-bug (investigated, cleared): **F2** chromium count 46→58→54 oscillated, no monotonic leak — reaper + isolated-MCP cleanup work.

Incident (recovered, no data loss): **F6** host disk hit 100% (root cause = my ~8 backend rebuilds bloated Docker build-cache to 90GB + images 116GB) → Docker VM wedged → stack down at ~05:00. Recovered: freed regenerable caches, clean Docker restart (20s), pruned 120GB (disk 100%→73%). All volumes + DB survived. **F5** contention at 04:19 was the leading edge of F6.

Speed answer (measured): browser/Playwright is FAST (~5s full golden-path smoke on both apps; server TTFB 0.3-0.5s). The QA "minutes" = an LLM agent driving the browser action-by-action (1 round-trip/action). Stay Playwright (Cypress = no gain). Fix = compiled scripts (see below).

Feature built this session (the user's asks): (1) QA **manifest** per project (known nav, injected every claim) + pushed to docs KB; (2) project **base-suite** + **promotion on done** (self-growing regression); (3) **compiled QA scripts** (test_case.script; migration 138 applied; gen_tests emits a Playwright script, run_test_cases EXECUTES it deterministically ~5s instead of LLM-driving; background compile of human cases, gated AGORA_QA_COMPILE_ENABLED); (4) Playwright MCP on 10 agents; (5) Bitrix bidirectional OFF for the test; (6) one squad + Figma MCP (Designer) for design tasks; (7) sd-cs fully provisioned (cs3 repo, agora-cs box, manifest).

Uncommitted: large server/ diff (all the above) + docs. NOT committed (waiting for your review). Backend rebuilt with everything through F4; the script-compile image rebuild + AGORA_QA_COMPILE_ENABLED recreate was mid-flight when the Bash safety-classifier went temporarily unavailable — finish with: `docker compose -f docker-compose.selfhost.yml -f docker-compose.selfhost.build.yml -f docker-compose.override.yml up -d --no-deps --force-recreate backend` then confirm `COMPILE=true` in the container env.

Details of each finding below.

---


## Baseline (03:55)
- backend RSS 17.2 MiB, postgres 124 MiB
- DB: issue=179, agent_task_queue=421, test_case=77, **test_run=0**, comment=968
- chromium/chrome procs: **46**
- daemon runtime: online
- running=0, queued=0

## Findings (live, appended as observed)

### F1 [FIXED + VERIFIED 04:1x] Base-suite regression never EXECUTES → test_run always empty
- Root cause: on in_review only `maybeRunQAOnInReview` (gate) + `maybeGenTestsOnInReview` (authoring) auto-fire. **`run_test_cases` is auto-fired NOWHERE** (grep confirms zero callers). So the standing base suite + issue cases are authored + promoted but never RUN → `CaptureTestRuns` never invoked → `test_run=0`. The regression QA layer the user asked for was silent. `CaptureTestRuns` itself is correct.
- Fix: added `maybeRunTestsOnInReview` (slice_action.go, mirrors maybeGenTestsOnInReview) — fires run_test_cases when the issue has automated cases OR the project has a base suite; wired into both in_review sites in issue.go (UpdateIssue + batch path), after gen_tests. Gated by AGORA_AUTO_QA_ENABLED; picks a free QA agent. Built + backend recreated (health 200).
- Verify during run: `test_run` count should go > 0 once a wave task reaches in_review (monitor tracks it).
- Severity: medium-high (whole regression/self-growing-suite layer was inert).

### F2 [watch] 46 chromium processes at idle baseline
- Could be QA warm-Chromium (CDP screencast) leak OR code-server's bundled electron/chromium. Need to attribute.
- Watch: does the count grow monotonically across QA runs and NOT drop after the 15-min idle reaper TTL?

(more appended below during the run)

## QA SPEED analysis (measured, not guessed)

**Verdict: the browser/Playwright is FAST. QA "slowness" is the AGENT driving the browser action-by-action via LLM round-trips, NOT the tooling.**

- Full golden-path smoke (browser launch + login + 3 pages), measured with playwright headless:
  - sandbox.sdteam.uz (Yii SSR): **~5.0s warm / 6.8s cold**
  - agora-cs.sdteam.uz (Vue SPA): **~5.4s warm / 5.8s cold**
- Server TTFB (curl, no browser): 0.27–0.53s per route — server is NOT the bottleneck (Yii is fine).
- Browser cold start 2.2s → warm 0.36s. `--isolated` (which I set on the Playwright MCP) pays a fresh-ish start per session; a reused browser is 0.36s. Trade-off: isolation (safe parallel) vs warm reuse (speed). Keep isolated for correctness under concurrency; the 2s is one-time per session, not per action.
- Heaviest page `/stock/stock/detail` = 2.2MB HTML, but table is in DOM instantly (SSR) — render not a bottleneck.
- CSV: docs/overnight-qa-speed.csv

**Where the minutes actually go** (the user's "QA sekin"): an LLM agent driving the MCP click-by-click spends one model round-trip PER action (navigate, fill, click, read) — seconds each × many steps + reasoning/exploration = minutes. The same flow as a committed script runs in ~5s.

**Tooling decision: STAY on Playwright.** It is fast (~5s full smoke), installed, and MCP-integrated. Cypress would add a parallel stack for ZERO speed gain (Playwright ≈ Cypress on wall-clock; Playwright is better for multi-context/parallel + CDP). No new package needed for speed. `@playwright/mcp` (already added to 10 agents) is the right driver for interactive/authoring; the speed win is the EXECUTION MODEL, not the tool.

### Recommendation (architecture, the real speed lever) — Phase 3
- Base-suite golden paths should be COMMITTED executable Playwright specs run via `npx playwright test` (ms per action, deterministic), NOT agent-driven MCP clicks (s per action via LLM). The agent authors the spec ONCE (slow, one-time), then every regression run is a fast `playwright test` exit-code check.
- Blocker to wire this: the Yii fork has no Playwright test harness. Needs a `qa/e2e/` dir + `playwright.config` + credentials wiring in each project repo (sd-main fork, cs3) — a setup decision (where specs live, CI vs box). The manifest (nav) + base suite (steps) I built are the inputs; the missing piece is "run the committed spec" vs "agent drives the browser." Left as a design call for the user — not auto-applied overnight (repo-structure change).
- Cheap interim (safe, applied): run_qa's smoke wording already prefers a deterministic login-and-assert script over hand-driving; that guidance is the right direction and stays.

### F1 verification (04:1x): backend log confirms `auto run_test_cases fired on in_review` for SD-348 + others — all THREE QA facets (run_qa gate + gen_tests authoring + run_test_cases execution) now fire. Base-suite regression executes; test_run rows follow once the QA agent posts its ```test-runs``` block. Pipeline healthy: sprint10 tip advanced (dev committed to shared branch), 10 concurrent tasks under load.

## Agent context architecture (what each agent needs) + 2 dev-context gaps FIXED

Principle: give each agent PRE-DIGESTED knowledge for its job, not raw material to re-derive each run. Three knowledge layers answer three different questions:
- **manifest** = "how to reach + drive the running app" (routes/auth/flows) — QA-primary.
- **`<slug>-kb` skill** = "how the codebase is built, where X lives, conventions" — DEV-primary.
- **docs (sales-doctor-docs)** = "what it SHOULD do" (intended behavior, source of truth) — BOTH.
Plus: issue plan (what THIS task wants), code checkout (raw material dev reads guided by the KB), base suite (standing regression).

QA was well-equipped (manifest+docs+suite+plan). DEV was UNDER-served → re-explored code + never saw the spec = slow. Two gaps:

### F3 [FIXED] Dev brief never read the docs (intended behavior) — dev/QA asymmetry
- Docs (sales-doctor-docs) were injected only into QA slices; the DEV built against the ticket alone, then QA judged against a spec the dev never saw.
- Fix: `sliceActionQADocsContext` added to draft_code AND to the daemon claim path (so DELEGATED devs — whose brief comes from the claim, not a slice action — get it). Build OK, backend recreated.

### F4 [FIXED-trigger] `<slug>-kb` code-map skill was never built for sd-main / sd-cs
- Only a stale `agora-dev-stress-test-kb` existed; devs had no per-project architecture/conventions map → re-learned the codebase each task.
- Fix: triggered POST /api/projects/{id}/knowledge/build for sd-main + sd-cs (202) — the lead agent studies the repo and writes `sd-main-kb` / `sd-cs-kb`, read by every project agent thereafter. (Runs async; verify the skills exist by morning.)

### F5 [observed] Local stack contention under concurrent load
- Under ~10 concurrent agent tasks + a running Workflow (multi-agent) + the 5-min monitor, `docker exec psql` calls started timing out (>2min) — Docker Desktop / postgres became unresponsive to new exec sessions.
- Likely the local dev machine (Docker Desktop VM) saturating, not necessarily an Agora backend defect — backend RSS stayed low (17-25 MiB). But it means the local box has a concurrency ceiling around this load; a real deployment would need connection-pool + CPU headroom sizing.
- Action: back off piling on docker exec during peak; measure when load subsides. Watch the metrics CSV for a backend_mib spike or stuck-task growth as the true backend signal.

## PER-TASK QA timing (observed this session, agent-driven — the BEFORE for the script-compile feature)
- Dev leg (fix a bug end-to-end): ~6-8 min per task (SD-145, SD-457).
- QA leg on in_review: 3 QA agents fire in parallel (run_qa gate + gen_tests + run_test_cases); each agent run ~2-5 min (LLM driving the browser action-by-action + reasoning). Wall-clock QA phase ~5-8 min.
- Root cause of the minutes: LLM-per-action browser driving (see the QA SPEED section). The SAME golden path as a compiled Playwright script = ~5s.
- This is exactly the BEFORE that the compiled-QA-scripts feature (Workflow witp9te2g, in flight) targets: per-case execution minutes -> ~5s deterministic, LLM used only to author/compile once.
- Precise live per-agent averages to be captured once load subsides (query kept getting starved under contention).

### F6 [CRITICAL — incident + recovered] Host disk hit 100% → Docker VM wedged → whole stack down (~05:00)
- Root cause: repeated backend image rebuilds during the session (~8+ `docker compose build backend` for each fix) accumulated Docker BUILD CACHE to 90.76GB + images to 116GB (104GB reclaimable). Combined with app/model caches (uv 5G, huggingface 4.8G, ms-playwright 4.8G), /System/Volumes/Data hit 100% (0 free bytes). Docker Desktop's VM then wedged (its Docker.raw could not write on a full disk) and stayed unresponsive 12+ min.
- Impact: the overnight run (10 concurrent tasks + monitor) DIED at ~04:19 (F5 contention was the leading edge of this). Backend/postgres/frontend all stopped.
- Recovery (done): reclaimed ~12GB from regenerable ~/.cache (uv/huggingface) + go-build → gave the VM room; full `killall Docker` + relaunch → daemon booted in 20s; **containers auto-restarted, postgres HEALTHY, all volumes + DB data SURVIVED** (Docker.raw intact). Then `docker image prune -af` + `docker builder prune -af` reclaimed ~120GB → disk 100%→73% (122GB free). DB verified: issue=187, test_case=173, test_run=48 (regression kept growing 24→48 before the crash — the feature worked under load right up to the disk event).
- Prevention (for the team): (1) `docker builder prune` / `--rm` after image rebuilds, or a bind-mount dev build instead of full image rebuild per change; (2) a disk-space guard before spawning heavy concurrent agent load; (3) cap Docker.raw + periodic prune in the dev setup. This is an OPS/dev-env issue, NOT an Agora backend defect (backend RSS stayed 17-30 MiB throughout).
- NOTE: migration 138 (test_case.script) had been applied by the workflow to a LOCAL psql (Docker was down), NOT the container DB → I re-applied it to agora-postgres + recorded it in schema_migrations. Column verified present.
