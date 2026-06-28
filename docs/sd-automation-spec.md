# SalesDoctor Automation System — Design Spec (Agora)

> **Status:** proposed. **Goal:** turn Agora into the full automation system for the
> SalesDoctor platform — every task on **sd-cs**, **sd-main**, or **sd-billing**
> flows, inside Agora, from intake → AI implementation → QA on a real deployed box
> → auto-written documentation, with a human only reviewing and merging.
>
> Most of this is **orchestration of primitives Agora already has** (squad pipeline,
> slice actions, autopilot triggers, Bitrix sync, the deterministic QA gate). The
> genuinely new pieces are **git-sync (deploy a branch to a QA box)** and the
> **auto-docs stage**. Context: [salesdoctor-ecosystem](#) (the 3 projects),
> [remote-boxes-spec.md](remote-boxes-spec.md) (the box layer).

---

## 1. The SalesDoctor platform (what we automate)

UZ B2B distribution platform. Three projects, dependency `sd-cs → sd-main → sd-billing`:

| Project | What it is | Repo | Stack |
|---|---|---|---|
| **sd-cs** (HQ) | "Country Sales 3" — head office, reads many dealer DBs | `jamshidtulaganov/cs3` | — |
| **sd-main** (Dealer CRM) | the CRM each dealer runs (orders, agents, warehouse, finans…) | `azizkh/sd` ← fork `jamshidtulaganov/sd-main` (default `billing`, `btx-*` task branches) | **PHP Yii 1.x**, MySQL multi-tenant (`d0_`), Redis, PHP views + **Vue3/Vuetify CDN — no build step** |
| **sd-billing** | subscriptions / licensing each dealer | (integration surface in sd-main) | — |
| **sd-docs** | the developer + client documentation | `jamshidtulaganov/sd-doc` | Docusaurus 3, en/ru/uz; `sd-docs-author` skill |

**Critical property:** sd-main has **no build step** — a plain `git checkout <branch>` in a served directory is live immediately under nginx + PHP-FPM. This is what makes QA-on-a-box cheap and the locked-down box usable.

## 2. The automation lifecycle (per task)

```
1. INTAKE        Bitrix task (btx-NNNNN) ──► Agora issue          [EXISTS: bitrix sync]
2. IMPLEMENT     squad: lead → developer → reviewer               [EXISTS: squad pipeline]
                 → branch pushed to the fork → PR to upstream
3. QA (in Agora)
   a. DEPLOY     git-sync the branch into a QA box dir            [NEW: git-sync]
                 → nginx serves it (no build, PHP)
   b. GATE       run_qa: baseline-diff build/lint/test +          [EXISTS: QA gate, this session]
                 browser smoke against the box URL (qa_smoke_url)
                 → qa:pass / qa:fail + structured qa-result panel
4. AUTO-DOCS     on task done → an agent (sd-docs-author skill)   [NEW: auto-docs stage]
                 reads the diff → writes/updates sd-docs
                 → PR to the sd-doc repo
5. REVIEW        human reviews the code PR + the docs PR, merges  [human]
```

Each arrow is an Agora **trigger** (comment-mention, label, autopilot, or webhook), so the chain runs without manual hand-offs.

## 3. What already exists in Agora (mapped)

| Lifecycle step | Existing primitive | Where |
|---|---|---|
| Intake | Bitrix → issue sync (btx- branches) | `handler/bitrix_sync.go` |
| Implement | squad pipeline (lead/dev/reviewer/qa), agent assignees, mention-triggered tasks | `service/task.go`, `service/autopilot.go`, `handler/slice_action.go` |
| Slice actions | `draft_code`, **`write_docs`**, `write_tests`, `review_part`, `run_qa`, `run_ci` | `handler/slice_action.go` |
| Repo binding | per-project `github_repo` resource (a project ↔ a repo) | `handler/project_resource.go` |
| QA gate | deterministic baseline-diff gate + project-config smoke (`qa_smoke_cmd`/`qa_smoke_url`) + structured `qa-result` panel | `handler/slice_action.go`, `views/issues/components/editor-tests-panel.tsx` |
| Triggers / autopilot | autopilots, webhook + event triggers, `DispatchAutopilot → dispatchCreateIssue` | `service/autopilot.go`, `handler/autopilot_webhook.go` |
| Project → squad enforcement | only the bound squad/agents may be assigned | `handler/issue.go`, `service/autopilot.go` |
| Remote boxes | `connected_box` + flag-gated CRUD + sync API + onboarding UI | `handler/connected_box.go`, `handler/remote_box_sync.go` |

So steps 1, 2, and 3b already work. The new work is **3a (git-sync deploy)** and **4 (auto-docs)**, plus the **wiring** that chains them.

## 4. QA process — fully inside Agora

The QA gate (built this session) is deterministic: baseline-diff the change (a check red on the base branch is pre-existing, not a regression), run build/lint/test by exit code, drive a real browser, write test cases, set `qa:pass`/`qa:fail`, and emit a structured `qa-result` block the editor panel renders. What it needs to test a **real branch on a real environment** is git-sync (3a).

### 4a. git-sync — deploy a branch to a QA box

The QA/dev boxes are **locked down**: `git pull` + branch checkout only, **no installs**, and (with the box user's own creds) **no access to the private git host**. So:

- The **agent runs on Agora's runner** (which has git-host access) and produces the branch — never on the box.
- Agora delivers the branch to the box and the box **serves** it. For sd-main (PHP, no build) the served checkout *is* the running app.
- **Glue-preservation is mandatory.** sd-main's per-site deployment glue — `index.php`, `protected/config/main.php` (DB connection), `console.php` — is **gitignored** and created once per site. The sync must update tracked code while **preserving** these untracked files. `git checkout` does exactly this; a wipe-and-extract (tar) does **not** → the executor must use git-checkout semantics, not archive overwrite.

**Chosen model — box-fetches-with-ephemeral-token** (matches how the existing dealer demo sites already work):
```
cd <work_dir>                       # an nginx-served site dir, e.g. /var/www/agora.sdteam.uz
[ -d .git ] || { git init; git remote add origin <tokenless-url>; }
git fetch <https://x-access-token:TOKEN@github.com/.../sd-main.git> <branch>   # token only in this command
git checkout -B <branch> FETCH_HEAD # tracked code updated; untracked glue (index.php, main.php) preserved
```
- The token is **ephemeral** (only in the fetch argv, redacted in logs, never `set-url`'d into the box's `.git/config`).
- Token + SSH key are operator config on the runner (`AGORA_REMOTE_BOXES_GIT_TOKEN`, `AGORA_REMOTE_BOXES_SSH_KEY`); the box never stores a token.
- Alternative (no token on box): **runner-pushes** — box `git init` + `receive.denyCurrentBranch=updateInstead`, runner `git push` the branch. Also preserves glue. Heavier; deferred unless token-on-box is unacceptable.

> Implementation note: the current committed executor uses runner-delivers (archive|tar), which **wipes the glue** — it must be reverted to the box-fetches git-checkout model above before QA-on-box is real.

### 4b. QA box provisioning (one-time, SD-infra — not git-sync)

For a box dir to be a *running* sd-main QA target it needs, **once**: `index.php`, `protected/config/main.php` (DB connection), and a **tenant MySQL DB** (`d0_` prefix). These are gitignored glue + a DB → require MySQL/admin access (the box user can write the dir but not provision a DB). Options:
- A dedicated pool of pre-provisioned QA sites (`agora-qa1.sdteam.uz`, …) the admin sets up once; Agora git-syncs branches into them per task.
- Reuse an existing dealer demo site as a QA target (already provisioned) — but never while it's in live use.

DNS is already handled: `*.sdteam.uz` → Cloudflare → the box; nginx wildcard serves `/var/www/$host` with PHP-FPM (verified: a synced `.php` executes; `/` 404s only because the gitignored `index.php` isn't present yet).

### 4c. QA gate against the box

After git-sync, run_qa's smoke phase points at the box URL via the project's `qa_smoke_url` (`https://<qa-site>.sdteam.uz`) and drives a browser there — testing the **exact branch** now served. `qa:pass`/`qa:fail` + screenshots post back into Agora; the editor QA panel renders the structured result. No build/CI on the box (PHP) — the gate's CHECKS run on the runner where the code also lives.

## 5. Auto-docs stage (new)

**Trigger:** a task completing (PR opened/merged, or `qa:pass`) on an sd-* project fires an auto-docs run.

**Mechanism — reuse existing primitives:**
- An agent equipped with the **`sd-docs-author` skill** (already in the sd-doc repo) runs a `write_docs`-style slice action.
- Its **target repo is `sd-doc`**, not the code repo — modeled as a docs project/squad whose bound `github_repo` resource is `jamshidtulaganov/sd-doc`. (Repo binding is per-project via the `github_repo` resource, so pointing an agent at sd-doc is a config, not new code.)
- Input: the finished task's **diff** + issue context. The agent updates the relevant Docusaurus pages (module reference, API, data model, changelog) and opens a **docs PR** to sd-doc.

**Why it fits:** `write_docs` already exists as a slice action that opens a PR; `sd-docs-author` already encodes how to write SalesDoctor docs; the only new piece is the **auto-trigger** (task-done → docs run) and routing the run at the sd-doc repo with the docs skill.

## 6. Orchestration — chaining the stages

The chain is event-driven, reusing autopilot/triggers:
- **Implement → QA:** when the developer's task opens a PR / sets the branch, a trigger fires `run_qa` (and `git-sync` first) on the QA-box-bound agent. (Today `run_qa` is a manual slice action + the squad lead routes to it; the wiring makes it automatic on branch-ready.)
- **QA pass → Auto-docs:** a `qa:pass` label (or PR merge) fires the auto-docs run.
- **All stages** post their status into the Agora issue timeline (comments, labels, the QA panel, merge-readiness gates) so the human sees one coherent thread.

New wiring needed: a small "task lifecycle" autopilot/trigger set that maps `branch-ready → git-sync + run_qa` and `qa:pass → auto-docs`. This builds on `service/autopilot.go` + the event bus; no core change.

## 7. Hard constraints (carried from the whole effort)

- **Additive + opt-in.** Every new piece (git-sync, auto-docs, the lifecycle wiring) is flag-gated (`AGORA_REMOTE_BOXES_ENABLED`, and a new auto-docs flag) and never changes the agent/task/runtime core or any existing API contract. Productizable for other teams.
- **Locked-down boxes:** no installs, no box-side git-host creds, glue preserved. The runner does the heavy lifting.
- **Deterministic QA:** report by exit code, baseline-diff, never fabricate green, never weaken tests.
- **Multi-tenant:** everything scoped by `workspace_id`/`owner_id`.

## 8. Phased build plan

| Phase | Deliverable | Status |
|---|---|---|
| **0** | QA gate (baseline-diff, smoke, qa-result panel, qa_smoke_url config) | ✅ done (this session) |
| **1** | Remote Boxes: connected_box CRUD + onboarding UI + sync API, flag-gated | ✅ done |
| **2** | git-sync executor = **box-fetches-with-ephemeral-token** (glue-preserving git checkout) | ⏳ revert archive→checkout; needs fork token |
| **3** | One QA site provisioned (index.php + main.php + tenant DB) + nginx confirmed | ⏳ SD-infra (admin/MySQL) |
| **4** | Wire **branch-ready → git-sync + run_qa** automatically (lifecycle autopilot) | ⏳ |
| **5** | **Auto-docs**: docs project bound to sd-doc + `sd-docs-author` agent + `task-done → write_docs(sd-doc)` trigger | ⏳ |
| **6** | Productize/harden: per-box secret storage, QA-site pool, audit, status surfacing | ⏳ |

## 9. Open decisions (for the user)

1. **git-sync model:** confirm **box-fetches-with-ephemeral-token** (your fork PAT, ephemeral) vs runner-pushes (no token on box). Recommended: box-fetches (simplest, matches dealer sites).
2. **QA-site provisioning:** who/how creates the running QA site(s) — a dedicated pool (`agora-qa*.sdteam.uz`) provisioned once by admin (index.php + main.php + tenant DB)? This is the only hard external dependency.
3. **Auto-docs trigger point:** on PR **open** (docs in parallel with review) or on **merge / qa:pass** (docs only for accepted work)? Recommended: on `qa:pass` so docs track shipped behavior.
4. **Docs PR target:** confirm sd-docs PRs go to `jamshidtulaganov/sd-doc` (your fork) → PR upstream, mirroring the code-fork flow.
5. **Scope of auto-docs:** every task, or only tasks labeled `type:feature`/`type:bug` (skip chores)? Recommended: gated by label to avoid noise.
