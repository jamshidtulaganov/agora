# Local Dev Mode v2 — Implementation Plan

**Date:** 2026-07-08 · **Status:** planned, not started · **Branch target:** sd-platform

Agora already ships "agents work in your local folder" as the `local_directory` project
resource (see `apps/docs/content/docs/project-resources.mdx`). This plan hardens and
extends that v1 into the full "agent works like you on your machine" story, across four
workstreams. Every file:line reference below was verified against the code on 2026-07-08
(multi-agent scan + adversarial verification; planning run `wf_ca29d1f2`).

**Verified baseline facts:**

- Max migration is `server/migrations/152_comment_bitrix_origin.up.sql`. **No workstream
  needs a migration** — everything rides existing JSONB columns (`project_resource.resource_ref`,
  `project.settings`, `agent_runtime.metadata`).
- `isGitWorkTree` (`server/internal/daemon/local_directory.go:334`) is dead code today;
  local_directory mode invokes zero git logic.
- `POST /api/projects/{id}/resources` (`server/cmd/server/router.go:977`) is not
  human-gated; `daemon_id` in the ref is never validated against runtimes.
- Agents run permission-bypassed as the daemon OS user (`server/pkg/agent/claude.go:507`
  and siblings); the path check is a ~10-entry literal denylist — `~/.ssh` passes today.
- Daemon dev-server/test detection is Node-only (`server/internal/daemon/health.go:1358+`).

---

## Sequencing overview

```
Phase 0   WS1 PR-A (server security)   ∥   WS3 (run/test detection)      ← parallel, zero file overlap
Phase 1   WS1 PR-B (daemon allowlist + denylist)                          ← before WS2 (same function)
Phase 2   WS2 PR-1 (in-place git safety, incl. QA-task exemption)
Phase 3   WS4 core (QA loop wiring)                                       ← hard dep: WS1; soft: WS3, WS2
Phase 4   Deferred: WS2 worktree mode · WS4 preview_apps · compose tier · follow-ups
```

Total estimate: **~8–11 dev-days** across phases (Phase 0 pair runs in parallel).

Docs/skills collision rule: `agora-projects-and-resources/SKILL.md` (+ source map),
`agora-runtimes-and-repos/SKILL.md`, and `project-resources.mdx`/`.zh.mdx` are edited by
three workstreams. Land those edits strictly in phase order (WS1's human-only contract
first), or batch one combined docs pass after Phase 3.

---

## Phase 0a — WS1 PR-A: server-side security (est. ~1 day)

Close the API-level gaps. Default-ON, no feature flags.

### Decisions

1. **Human-gate only `local_directory` mutations, in the handlers — not router-level.**
   Agents legitimately create `github_repo` resources (builtin skill
   `agora-projects-and-resources/SKILL.md:41`, CLI POST at `server/cmd/agora/cmd_project.go:624`)
   and the first-repo KB/QA-manifest bootstrap (`handler/project_resource.go:338-351`)
   is agent-driven. Resource type is only known after body decode, so a chi middleware
   can't do it. Add exported `IsMachineActor(r)` in `handler/actor_guards.go` (same
   `X-Actor-Source` check `RequireHumanActor` uses at `:101-104`, which stays untouched)
   returning true for `task_token` / `cloud_pat`.
2. **Validate `daemon_id` ownership at create/update.** New sqlc query
   `ListAgentRuntimesByDaemonID(workspace_id, daemon_id)` (agent_runtime already stores
   the daemon machine UUID — migration 004, upsert `handler/daemon.go:389`, matches what
   the frontend puts in the ref at `project-resources-section.tsx:203`). Require
   `canUseRuntimeForAgent` (`handler/runtime.go:488-496`) to pass for ≥1 returned row —
   same semantics as agent→runtime binding (owner, workspace owner/admin, or
   visibility=public). Unknown daemon_id → 400; ownership failure → 403.

### Changes

| Step | Files | Detail |
|---|---|---|
| Human gate | `handler/actor_guards.go`, `handler/project_resource.go`, `handler/project.go` | `CreateProjectResource`: after ResourceType trim (`:268-272`), local_directory + machine actor → 403. `UpdateProjectResource`: same check on the loaded `existing.ResourceType` (`:375-385`) — covers agent retargeting via resource_ref. `DeleteProjectResource`: same after load. **`CreateProject` bundled-resources loop (`project.go:331-352`) gets both checks too — easiest bypass route to forget.** |
| daemon_id binding | `pkg/db/queries/runtime.sql`, `handler/project_resource.go`, `handler/project.go` | Query + validation at create and on any ref update; `make sqlc`. |
| Skills/docs | `builtin_skills/agora-projects-and-resources/SKILL.md` + source map, `agora-runtimes-and-repos/SKILL.md`, `project-resources.mdx` + `.zh.mdx` | Drop the agent-facing `resource add --type local_directory` example (SKILL.md:42), document human-only + 403 + daemon-ownership rule. Same PR — mandatory per CLAUDE.md, or agents retry against stale instructions. |

### Tests

- Machine-actor matrix: task_token/cloud_pat POST/PUT/DELETE local_directory → 403;
  github_repo by machine actor still 201 (bootstrap non-regression); bundled-resource
  variants.
- daemon_id: unknown → 400; other member's private runtime → 403; owner/admin/public → 201;
  multi-provider rows (any-row-passes).
- `IsMachineActor` unit + `RequireHumanActor` unchanged.

---

## Phase 0b — WS3: widen run/test detection (est. ~1.5 days, parallel with 0a)

Make `/editor/preview` + `/editor/test` work for the real users: SalesDoctor (PHP-no-build
Yii) and Agora itself (`make dev`). No flags, no migrations, no API shape changes.

### Decisions

1. **Tier list v1: Node (existing, stays first) → Makefile → PHP.**
   - Makefile: line-anchored regex `^(dev|start|run|serve):([^=]|$)` over
     `Makefile`/`makefile`/`GNUmakefile` → `make <target>`; same for `test` → `make test`.
     (Not `make -pRrq` database parsing — heavy, version-sensitive.)
   - PHP: `composer.json` or `index.php` present → `php -S 127.0.0.1:${PORT} -t <docroot>`,
     docroot probed `web/index.php` (Yii2) > `public/index.php` > root. `${PORT}` is
     interpolated by the login shell — `startPreview` already exports `PORT` (`health.go:1551-1557`).
   - **Compose tier CUT from v1** (integration trim): no named user runs compose
     (SD = PHP, Agora = make) and it carries the heaviest machinery (YAML port parsing,
     stop hook in two shutdown paths, container-leak risk). Add when a compose repo materializes.
   - Deferred: bare Go `go run` (Agora covered by Makefile; no scannable URL), Python.
2. **Detection moves to a new sibling file `server/internal/daemon/detect.go`** —
   `health.go` is ~1900 lines; package precedent is small purpose files. Call sites unchanged.
3. **Config-over-detection reuses `qa_smoke_cmd`** (documented as "how to bring the app up",
   `slice_action.go:530-532`) as the dev-command override + one new optional
   `qa_test_cmd` settings key (JSONB, no migration). Precedence: typed pane command >
   project setting > detection. Plumbing is frontend-only — the daemon stays project-agnostic;
   the `/editor/preview` and `/editor/test` bodies already accept `command`.
   (Rejected: new `dev_cmd` setting — parallel abstraction; per-resource `run_cmd` —
   equivalent to project-level given one-local_directory-per-(project,daemon).)
4. **`ensureDeps` rewritten to delegate to `detectSprintDepProvider`**
   (`sprint_shareddeps.go:29-52`) + existing stubbable `runDepInstall` var — kills the
   duplicated Node logic and adds Composer `vendor/` install for free. No `go mod download`
   (go resolves itself).
5. **`/editor/test` tiers:** `package.json scripts.test` > `make test` > `go.mod → go test ./...`
   > `composer.json scripts.test`. `runProjectTests` unchanged (CI=1 harmless).
6. **Frontend zero-parse tolerance:** `parseTestOutput` is vitest-shaped; go/phpunit output
   parses 0 cases. Render "Tests passed (exit 0)" instead of "All 0 tests passed ✓"
   (`editor-preview-pane.tsx:503-507`). No new per-runner parsers.

### Changes

| Step | Files |
|---|---|
| detect.go: move + tier chain (dev & test) | `daemon/detect.go` (new), `daemon/health.go` |
| ensureDeps → sprint dep-provider reuse | `daemon/health.go:1516-1537` |
| `qa_test_cmd` type + settings input | `packages/core/types/project.ts:17-18`, `packages/views/projects/components/project-qa-section.tsx` |
| Precedence wiring in panes | `packages/views/issues/components/editor-section.tsx` (pass `defaultDevCommand`/`defaultTestCommand` from project settings), `editor-preview-pane.tsx` (prefill `s.command \|\| defaultDevCommand \|\| s.detected`; send `command: defaultTestCommand` to `/editor/test`) |
| Tests | `daemon/detect_test.go` (table-driven t.TempDir fixtures: node-over-make precedence, `dev :=` non-match, GNUmakefile, Yii `-t web`, empty dir), ensureDeps via `runDepInstall` stub, `editor-preview-pane.test.tsx` (prefill precedence, test-cmd forwarding, exit-0 rendering) |

### Risks

- Hybrid repos (Laravel: package.json + composer) detect Node first — `qa_smoke_cmd` is
  the escape hatch; do NOT reorder tiers.
- `runProjectTests` 5-min timeout may be tight for big `make test` — pane copy notes
  `qa_test_cmd` can narrow it.
- **Pre-rollout audit (shared with WS4):** existing SD projects' `qa_smoke_cmd` may encode
  a deployed-QA-box flow, not a local dev command — check before prefill ships.
- i18n: `editor-preview-pane.tsx` / `project-qa-section.tsx` verifiably use no locale
  plumbing today — hardcoded English matches the files' actual pattern. Decide the
  i18n question once for these views, don't half-add keys (see cross-cutting).

---

## Phase 1 — WS1 PR-B: daemon-side consent + denylist (est. ~1.5 days)

**Must land before WS2** — WS2 refactors the same function
(`acquireLocalDirectoryLockIfNeeded`, `daemon/daemon.go:2489`); this small insert lands
first, WS2's refactor carries it forward. Ship (a)+(b)+(c) together: without the
on-machine allowlist, visibility=public daemons remain bindable by any workspace member.

### Decisions

1. **Consent = on-machine allowlist** `~/.agora/local-dirs.json` (same profile dir as
   `daemon.id`, `identity.go:40-45`) + additive `AGORA_LOCAL_DIR_ALLOWLIST` env for
   headless daemons (sd-agora-daemon, SSH boxes). Re-read per task — no restart. Enforced
   in `acquireLocalDirectoryLockIfNeeded` right after `validateLocalPath` (`daemon.go:2505`);
   deny → existing `FailTask` path (`local_directory_error`, `:2506-2511`) with a message
   that names `agora daemon allow-dir <path>` — self-healing.
   Server-stored approval rejected: consent must live on the executing machine; anything
   API-writable is forfeit to the same PAT-compromise threat model this fixes.
2. **Approvals written by:** (1) desktop folder picker auto-writes on pick (the pick IS
   the consent gesture — zero-friction primary UX; new Electron IPC
   `local-directory:approve` next to the existing validate IPC in
   `apps/desktop/src/main/local-directory.ts`); (2) new `agora daemon allow-dir <path>`
   CLI for headless. (`list-dirs`/`revoke-dir` trimmed to follow-up — file is tiny and
   hand-editable.)
3. **Denylist tightened with prefix-based protected-subtree checks** in BOTH
   `isBlacklistedLocalPath` and `isBlacklistedRealPath` (`local_directory.go:212-266`,
   currently literal-equality only): block any path equal to or under `$HOME/.<any>`
   (dot-directories as a class — enumerating `.ssh`/`.aws`/... rots), `$HOME/Library`
   (macOS), `%USERPROFILE%\AppData` (Windows). Separator-boundary matching (`~/.sshfoo`
   not caught, `~/.ssh/keys` caught). Keep the denylist as a second belt under the
   allowlist — an approved parent must never smuggle `~/.ssh` via symlink. Also applied
   at approval time (CLI + a minimal dot-dir-under-home check in the desktop approve
   path — not a full TS mirror, the daemon check is authoritative and a mirror would drift).
4. **Deferred:** server→daemon path pre-validation RPC (needs reverse channel for cosmetic
   benefit; desktop already validates client-side at `project-resources-section.tsx:184`).
   `mdt_` scoped daemon-token completion (verified: `GenerateDaemonToken`,
   `auth/jwt.go:41`, has zero callers — daemons auth with full user PATs) is its own
   future workstream; nothing here depends on it.

### Tests

- `isPathApproved` prefix semantics (exact root, nested child, `/a/b` vs `/a/bc` sibling,
  symlink into/out of approved root); allowlist merge file+env; corrupt/missing JSON → deny.
- `isProtectedHomeSubtree`: `~/.ssh`, `~/.ssh/keys`, `~/.aws`, `~/Library` (darwin),
  boundary cases; `validateLocalPath` rejects symlink → `~/.ssh`.
- Unapproved path → task fails with `local_directory_error` + instructive message.
- Views test: attach flow calls approve after validate, before createResource.

### Rollout risks (announce in release notes)

- **One-time break:** every pre-existing local_directory row fails at claim until the
  owner approves (desktop re-pick or `allow-dir`). Self-healing via the fail message.
- **Desktop version skew** (CLAUDE.md installed-app rule): older desktop builds keep
  creating rows WITHOUT writing the allowlist — the deny message must not assume the
  picker flow exists on the client that created the row; always name both approval paths.
- Concurrent file writes (desktop main vs CLI): atomic temp+rename both sides,
  dedupe+sort for stable diffs. Windows: `filepath.Clean` + case-insensitive compare.
- Follow-up ticket: desktop affordance to approve an already-attached row.

---

## Phase 2 — WS2 PR-1: in-place git safety (est. ~2–3 days)

Protect the user's uncommitted work. **Worktree isolation mode is deferred to Phase 4**
(integration trim): dirty-snapshot + auto-branch + summary deliver the bulk of the
protection, and worktree mode carries two unresolved QA semantic conflicts. This makes
WS2 PR-1 a small, daemon-only PR. No env flags — the behavior is strictly protective and
reversible.

### Decisions

1. **Dirty-tree policy: proceed with a zero-touch snapshot, not refuse, not auto-stash.**
   If `git status --porcelain` is non-empty: `git stash create "agora backup <taskid>"`
   (creates the stash commit WITHOUT touching the tree) + pin via
   `git update-ref refs/agora/backup/<short-task-id> <sha>` so it survives gc. Post-task
   summary gives the recovery command. Refusing would block the normal case (dev folders
   are dirty most of the time — users would commit junk WIP to unblock); auto-stash
   push/pop mutates the tree and pop-conflicts are exactly the data-mangling this prevents.
   Documented limitation: `stash create` does not capture untracked files.
2. **Auto-branch:** `git checkout -b agent/{sanitizedAgent}/{shortTaskID}` from current
   HEAD before the agent runs (exact managed-mode naming mirror, `repocache/cache.go:498`).
   Uncommitted changes ride along — git moves no content. Retry → checkout existing branch.
   Detached HEAD → branch from SHA, record it. **After the task: LEAVE the repo on the
   agent branch** — a post-task checkout rewrites tree files under a user who may have
   returned mid-task, can half-fail on conflicts, and hides what happened. The changed
   prompt branch doubles as an "agent worked here" signal; the summary states the original
   branch + the one-liner to return.
3. **QA-task exemption (integration-mandated acceptance criterion):** skip
   snapshot/auto-branch/summary for QA-gate task kinds (run_qa / gen_test_cases /
   run_test_cases context). Otherwise a QA task would `checkout -b` in the user's repo
   and leave it on the QA agent's branch — violating WS4's tree-safety contract.
   Nobody owned this rule in the individual plans; it lives here.
4. **Non-git folders:** gate ALL git machinery on revived `isGitWorkTree`
   (`local_directory.go:334` gets its first real caller; fails closed) — plain dirs take
   exactly today's path.
5. **Post-task summary is daemon-side text appended to `result.Comment`** — zero
   server/API change (`CompleteTask` already flows Comment → issue comment,
   `daemon.go:2602` → `handler/daemon.go:2171`). Record preHEAD before run; after:
   branch + original ref + `git diff --stat preHEAD..HEAD` + porcelain counts + recovery
   hint, capped ~4KB/60 lines, appended on completed AND blocked paths (`daemon.go:3244-3392`).

### Changes

| Step | Files | Detail |
|---|---|---|
| Git helpers + run state | `daemon/local_directory.go` | `gitCurrentRef`, `gitIsDirty`, `gitSnapshotDirty`, `gitCheckoutTaskBranch`; struct `localDirRun{assignment, isGit, origRef, preHEAD, snapshotSHA, branch, release}` |
| Task-flow wiring | `daemon/daemon.go` | Rename/extend `acquireLocalDirectoryLockIfNeeded` → `prepareLocalDirectory` returning `*localDirRun` (carries Phase 1's allowlist check forward); git prep under the lock; failure → `FailTask`. Thread `localDirRun` into `runTask`, replacing the redundant re-resolve at `:2821`. |
| Summary builder | `daemon/local_directory.go`, `daemon/daemon.go` | `buildLocalDirectorySummary` + append sites |
| Skills/docs | `agora-runtimes-and-repos/SKILL.md:72` caveats (dirty-snapshot, agent branch left checked out, backup refs), `project-resources.mdx` + `.zh.mdx` ("will/won't touch" + v1-limits rewrite) | Written against Phase 0a's human-only contract |

### Tests

Fixture git repos (`git init` in `t.TempDir()`, skip if git absent): dirty detect +
snapshot ref recoverable via `git stash apply`; branch from HEAD with dirty tree intact;
retry reuse; detached HEAD; **plain folder → zero git side effects (dir bytes identical)**;
QA-kind task → no branch/snapshot/summary; summary truncation cap.

### Risks

- User commits to the agent branch without noticing (deliberate no-restore) — mitigated
  by summary + prompt visibility.
- `refs/agora/backup/*` accumulate unbounded — tiny objects; document cleanup, follow-up GC.
- git missing/ancient on host: fails closed to today's behavior.

---

## Phase 3 — WS4 core: QA loop wiring (est. ~2–3 days)

When an issue's project carries a local_directory on daemon X: pin the QA gate to daemon X,
tell the agent the app lives at that path and may need starting via `/editor/preview`,
smoke `127.0.0.1:{port}`, queue behind dev tasks via the existing lock, never mutate the
tree. Hard dependency: Phase 0a (reliable daemon_id) + Phase 1 (approved paths). Soft:
Phase 0b (detection covers PHP/make), Phase 2 (QA exemption — until it lands, today's
daemon does no git prep anyway, so nothing violates the contract).

### Decisions

1. **Resolution order: local_directory joins the existing step-0 "dev's machine" tier**,
   ranked `dev_apps` URL > local_directory-derived preview > connected_box >
   `qa_smoke_cmd/url` > auto-detect. A declared dev_apps URL stays first (concrete,
   dev-vouched); local_directory only says WHERE, so it contributes pin + "start via
   /editor/preview" instruction, not a URL. `devBoxSmokeURL` itself unchanged.
2. **Pinning reuses `maybePinTaskToDevRuntime`** (`service/dev_runtime_pin.go:31-83`):
   after the dev_apps miss, resolve the project's local_directory daemon_id to an online
   runtime (new sqlc `GetOnlineRuntimeForDaemon(workspace_id, daemon_id)` next to
   `GetDevRuntimeForProject`, `agent.sql:738-746`) and pin. Same pin marker → the
   offline-fallback watchdog (`SweepStaleDevPinnedTasks`) and `qa_dev_runtimes_strict`
   apply verbatim. Offline daemon ⇒ resolver misses ⇒ falls through to connected_box —
   fails open to today's behavior.
3. **Serial lock kept for QA tasks — no read-only mode.** Verified just-works: the pinned
   QA task parks as `waiting_local_directory` behind the dev task and acquires on release.
   Serialization is desirable (lock-free QA would smoke a mid-edit tree → flaky verdicts).
   The real hazard is the run_qa recipe's step-1 baseline "check out the merge-base"
   (`slice_action.go:117-119`) — fixed in the instruction, not the lock.
4. **Instruction clause** `qaLocalDirectoryClause` (pattern of `qaLiveWatchClause :477`),
   appended at the run_qa build sites (`:2096-2113`, `:2329`, `:2983`, `:3013` — NOT the
   sprint deployed-branch path): LOCAL APP (status → preview → smoke returned URL, which
   is also the shared-browser `qa-target:` key) + TREE SAFETY (never
   checkout/switch/reset/stash/edit in `<local_path>`; baseline via
   `git -C <local_path> worktree add <tmpdir> <merge-base>`, then `git worktree remove`
   **and `git worktree prune`** — the prune guards against agent death leaking worktree
   metadata; daemon adds a best-effort prune at task end for local_directory tasks).
5. **Skip `isolation=worktree` resources in the resolver** (forward-compat rule for
   Phase 4): with worktree isolation, dev changes live in a daemon-scratch worktree — the
   user's checkout does NOT contain them; smoking `<local_path>` would test the pre-change
   tree → false verdicts. Worktree-mode resources fall through to connected_box/qa_smoke_url.
   Document it.
6. **Flags: ride the existing gates** — `AGORA_AUTO_QA_ENABLED` (trigger) +
   `labs.qa_dev_runtimes` (local-machine routing, default OFF, opt-in per workspace).
   No new flag — this is semantically what qa_dev_runtimes already gates.
7. **Human live-watch: no code change** — shared Chromium works self-host today;
   cloud-backend→laptop needs the mesh (authoritative deferral:
   `docs/daemon-per-dev-mesh.md`). Set expectations in docs.
8. **Cut from v1** (integration trim): preview_apps auto-declaration → dev_apps merge
   (WS4 step 5) — collides with Phase 0b's reshaped `health.go`, adds a
   register-on-preview-change trigger with rate-limit risk; steps 1–4 deliver the
   complete loop. Phase 4 candidate.

### Changes

| Step | Files |
|---|---|
| sqlc `GetOnlineRuntimeForDaemon` | `pkg/db/queries/agent.sql`, `make sqlc` |
| `localDirectoryQATarget` resolver | `handler/connected_box.go` (beside `devLocalAppURL :556-580`; labs-gated; ref parse mirrors `project_resource.go:109-137`) |
| Instruction clause at 4 build sites | `handler/slice_action.go` |
| Pin fallback | `service/dev_runtime_pin.go` (service must not import handler — small duplicated ref parse; do NOT require runtime.owner == issue dev for this branch: the resource is an explicit project→daemon binding, unlike per-dev dev_apps — document in function comment) |
| Skills/docs | `agora-squads/SKILL.md:196-237` (+ source map `:403`), `agora-projects-and-resources/SKILL.md`, `agora-runtimes-and-repos/SKILL.md`, `project-resources.mdx` + `.zh.mdx` ("QA on local directories": flag, precedence, preview startup, serial lock), status note in `daemon-per-dev-affinity-design.md` |

### Tests

- Resolver: resolves (online + labs on); misses on labs-off / no resource / offline /
  blank daemon_id / isolation=worktree; dev_apps still wins when both present.
- Instruction: clause present with exact path + tree-safety wording; absent when labs off;
  sprint deployed-branch path excluded; survives the QA-lead delegation wrapper
  (`slice_action.go:2130-2152`) and the delegated member's task also gets pinned
  (pin runs at enqueue, `task.go:596`).
- Pin: dev_apps hit unchanged; local_directory fallback pins with standard marker;
  offline → no pin; non-QA agent → no pin; sweep soft/strict behavior.
- **Integration seam (from review): QA task pinned to a daemon with an unapproved path
  (Phase 1 allowlist) must fail with the instructive message, not hang.** First-ever
  coverage for `devLocalAppURL`/`maybePinTaskToDevRuntime` (verified: zero existing tests).

### Risks

- Tree safety is instruction-enforced until Phase 4 worktree mode — same trust level as
  every local_directory dev task today.
- Long dev task delays the QA verdict (whole-task lock); bounded by wait_reason badge +
  pin watchdog maxWait.

---

## Phase 4 — deferred follow-ups (each independent)

1. **WS2 PR-2 — opt-in worktree isolation** (`isolation: "in_place" | "worktree"` ref
   field; worktree created FROM the user's non-bare checkout into the managed env via a
   new `PrepareParams.ProvisionWorkDir` hook so execenv stays git-free; early lock release
   for parallelism; `GCMeta.WorktreeParentRepo` so GC runs `git worktree remove --force`
   + prune instead of bare RemoveAll; desktop isolation select; `--isolation` CLI flag,
   human-only per Phase 0a). **Blocked on defining the QA story**: worktree-mode smoke
   target + the voided queue-behind-dev guarantee (Phase 3 skips these resources
   meanwhile). Note: `validateLocalDirectoryRef` re-marshals the typed struct and strips
   unknown fields — the server struct must gain the field before any client can persist it.
   Older daemons ignore the key and run in_place (default-equivalent, acceptable).
2. **WS4 step 5 — preview_apps auto-declaration**: daemon reports live previews in its
   register payload; server joins workdir→project via local_directory rows and merges into
   `metadata.dev_apps` (CLI-declared entries win; enforce `devAppURLAllowed` loopback rule
   server-side). Rebase on Phase 0b's reshaped health.go; coalesce re-registers (~1/30s).
3. **Compose tier** for detection (static YAML port fallback + `docker compose stop` hook)
   — when a compose repo materializes.
4. **Desktop "approve existing local_directory row" affordance** + `agora daemon
   list-dirs`/`revoke-dir`.
5. **`mdt_` scoped daemon tokens** — separate security workstream (daemons currently
   authenticate with full user PATs).
6. **`refs/agora/backup/*` GC** (age-based).

---

## Cross-cutting (from integration review)

- **Frontend schema debt:** `listProjectResources` (`packages/core/api/client.ts:2213`)
  returns unparsed fetch results — no zod schema exists. Whoever touches the endpoint
  first (Phase 4 WS2 PR-2's UI reads `isolation` from it) must add the resource-list
  schema + one malformed-response test per the parse-don't-cast rule, or record the
  exception explicitly.
- **i18n decision:** `project-resources-section.tsx`, `project-qa-section.tsx`,
  `editor-preview-pane.tsx` contain zero i18n usage today. Decide once (per
  conventions.mdx) whether these views are English-only; don't half-add locale keys in
  one workstream.
- **Estimates:** Phase 0a ~1d · Phase 0b ~1.5d · Phase 1 ~1.5d · Phase 2 ~2–3d ·
  Phase 3 ~2–3d → **~8–11 dev-days** to the full v1 story, all flags-existing, zero
  migrations.
- **Verification:** each phase runs `make check`; Phase 0b/3 additionally smoke via the
  co-code embedded browser per repo convention (never external playwright/chrome).
