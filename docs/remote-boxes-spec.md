# Agora Remote Boxes — Design Spec

> **Status:** proposed. **Type:** new, opt-in, strictly-additive feature.
> **One-liner:** onboard a whole dev team onto Agora where each developer (and QA) works on
> their own remote box; the developer provides only SSH access, and Agora bootstraps a
> *native* daemon on the box and proxies the co-code editor over an SSH tunnel.
>
> Builds on the code-verified research in [`remote-dev-servers-research.md`](remote-dev-servers-research.md).
> The load-bearing design choice — **the box ends up as an ordinary `runtime_mode='local'`
> self-host daemon** — is what makes this additive: the agent / task / runtime core is never
> touched; the "remoteness" lives entirely in a new parallel layer.

---

## 1. Goals / Non-goals

**Goals**
- Move an entire technical team (frontend, backend, QA) onto Agora, each working on their own named remote box (`jamshid.sdteam.uz`, `qa.sdteam.uz`).
- **Onboarding = the dev authorizes an Agora SSH deploy key.** No manual daemon install, no per-box hand-config.
- Agents (`claude`, `codex`, …), the co-code editor, and the QA gate run **natively on the box**, against the dev's own checkouts, with the dev's own agent/git credentials.
- **Productizable**: multi-tenant, self-serve, sellable to other teams as "Agora Remote Boxes / BYO dev server" with zero per-customer code.

**Non-goals**
- **Not** SSH-exec (rejected in research): commands are not run via `ssh user@box <cmd>`; the daemon executes natively on the box. SSH is used only for (a) one-time bootstrap and (b) editor transport.
- **Not** a replacement for the existing `local` (laptop) or `cloud` (Fly fleet) runtimes — it runs alongside them.
- **Not** a change to the agent / task / runtime data model or any existing API contract.

## 2. Non-breaking guarantees (hard constraint)

**Principle:** at the end of onboarding, the box runs a **normal self-host daemon** — `runtime_mode='local'`, native `os/exec`, registers exactly like a laptop daemon (`handler/daemon.go:267`). Nothing in the agent/task/runtime contract changes. The new feature is a **parallel onboarding + transport layer** that simply *produces* such a daemon and *feeds* its editor address into the **existing** cloud editor reverse-proxy.

### Existing feature → why it stays untouched
| Existing feature | Status | Why |
|---|---|---|
| Laptop local daemon (current dev flow) | unchanged | never references the new box layer |
| Cloud runtime (Fly fleet) | unchanged | separate code path (`cloud_runtime.go`, `cloudruntime/client.go`) |
| Self-host daemon | unchanged | a remote box registers identically |
| Desktop app | unchanged | consumes the same API; all new fields optional/nullable |
| Co-code editor (local + cloud modes) | unchanged | a box reuses the **existing cloud proxy** (`editor.go:122-273`); the frontend keys on response `mode` (`editor-section.tsx:231`) and never learns it is "remote" |
| QA gate | unchanged | agent-driven, co-located `127.0.0.1` on the box (`slice_action.go:88`) |
| `make dev` / local stack | unchanged | the control-plane is behind a feature flag; off = zero new code paths |

### Additive-only change list (no core-contract edits)
- **DB:** one **new** table (`connected_box`). **Zero** destructive changes to `agent_runtime` / `agent` / `agent_task_queue`. The per-runtime editor address rides in `agent_runtime.metadata` (JSON, additive — exactly how `editor_port` was added this session at `daemon.go:362`).
- **`runtime_mode` stays `'local'`** for box daemons. Remoteness lives in `connected_box`, **not** in the runtime row → the `CHECK ('local','cloud')` constraint and the `hooks.ts:28` (`runtime_mode !== "local"`) guard are **never tripped** (no new enum value needed).
- **API:** only **new** endpoints (`/api/remote-boxes*`). Every new response field is optional/nullable per the CLAUDE.md API-Response-Compatibility rules, so older desktop installs and older daemons keep working.
- **Shared code touched in exactly one place:** the editor address resolution (`daemonEditorBase` / `daemonInternalAddr`, `editor.go`). This was already made additive this session (`env → reported port → 19514 fallback`). The feature appends one branch — "if this runtime is a managed box, use its tunnel address" — with the existing fallback preserved.
- **Feature flag:** the whole control-plane (onboarder, bootstrapper, tunnel manager) sits behind `AGORA_REMOTE_BOXES_ENABLED`. Disabled → none of it loads, no request path changes.

## 3. Architecture overview

Two data flows; only one is new work.

- **Flow 1 — box → backend (outbound HTTPS):** task poll, registration, results, heartbeat. Outbound from the box; works over the public internet today, unchanged (`daemon/client.go` is 100% HTTP, `postJSON:562`).
- **Flow 2 — backend → box (inbound, editor/preview/CDP):** the only thing that needs the box reachable. Today (Fly) this uses 6PN `*.internal`; for a remote box it is an **on-demand SSH tunnel** the control-plane opens (the backend already holds the deploy key). The browser only ever talks to the backend (same-origin `/editor/proxy/{token}/`, `editor.go:172`).

**Components**
1. **Box daemon** — an ordinary native self-host daemon (no new daemon code for Phases 0–2). Runs agents/git/QA; binds health/editor ports to **loopback only**.
2. **`connected_box` entity** — new table: the SSH target + deploy-key reference + bootstrap/runtime status for boxes Agora onboarded.
3. **Control-plane service** (new, flag-gated):
   - **Onboarder** — registers a box, issues/authorizes the deploy key.
   - **Bootstrapper** — SSHes in once, installs + configures + starts the daemon.
   - **Tunnel manager** — on editor-launch for a box runtime, opens an SSH forward to the box's editor port and feeds the local address into the existing `registerEditorTarget` / `ProxyEditor` path.

See [`remote-team-architecture` diagram] in the prior message for the picture.

## 4. Data model

**New table `connected_box`** (illustrative — final columns in the migration PR):

| column | type | notes |
|---|---|---|
| `id` | uuid pk | |
| `workspace_id` | uuid | tenant scope (every query filters by it) |
| `owner_id` | uuid | the developer who owns the box (sets attribution) |
| `label` | text | e.g. "jamshid" — flows to `AGORA_DAEMON_DEVICE_NAME` |
| `ssh_host` | text | `jamshid.sdteam.uz` |
| `ssh_user` | text | |
| `ssh_port` | int | default 22 |
| `deploy_key_id` | uuid/ref | reference to the Agora-generated keypair (private key stored encrypted, see §9) |
| `daemon_id` | uuid null | set once the box's daemon registers (links to `agent_runtime.daemon_id`) |
| `status` | text | `pending` / `bootstrapping` / `online` / `offline` / `error` |
| `last_bootstrap_at`, `last_error`, `created_at`, `updated_at` | | |

**Per-runtime editor address:** `agent_runtime.metadata.editor_addr` (additive JSON key). Set at register from the box's reachable tunnel endpoint, read by the editor handler — mirrors the `editor_port` precedent (`daemon.go:362`, `editor.go:51`).

**No other schema changes.** `agent_runtime`, `agent`, `agent_task_queue` columns are untouched.

## 5. Control-plane components

### 5a. Onboarder
- `POST /api/remote-boxes` `{label, ssh_host, ssh_user, ssh_port}` → creates `connected_box(status=pending)`, generates a **per-box keypair**, returns the **public** key + a one-line `authorized_keys` command for the dev to run (or an "authorize" action if Agora has a provisioning path). Agora never receives the dev's private key.
- `GET /api/remote-boxes` (workspace-scoped, owner-filtered) — list + status for the runtime UI.
- `DELETE /api/remote-boxes/{id}` — deprovision (stop daemon, remove key, drop row).

### 5b. Bootstrapper (idempotent, re-runnable)
SSHes in with the deploy key and:
1. installs agent CLIs (`claude`, `codex`, …), `code-server`, Chromium/playwright, and the `agora` daemon binary;
2. writes `~/.agora/config.json` (server_url, the dev's **PAT**, `AGORA_DAEMON_DEVICE_NAME=<label>`), binds health/editor to **loopback only**;
3. installs a `systemd --user` (or system) unit and starts the daemon;
4. waits for the daemon to register, links `connected_box.daemon_id`, sets `status=online`.
Re-running is safe (upgrade path).

### 5c. Tunnel manager
- On `GET /api/issues/{id}/editor` resolving to a box runtime: open (or reuse) an SSH local-forward `backend-local-port → box:editor_port`, then call the **existing** `registerEditorTarget(backend-local-addr)` and return the same-origin `/editor/proxy/{token}/` URL. `ProxyEditor` is unchanged.
- Lifecycle: open on demand, idle-TTL close, re-open on next launch. Health-checked; `connected_box.status` reflects tunnel + daemon liveness.

### 5d. Liveness
The box daemon already heartbeats outbound (unchanged). `connected_box.status` derives from runtime liveness (existing) + tunnel health (new). Surface "box offline" in the runtime list.

## 6. Onboarding flow (developer experience)

1. Admin/dev: **"Add remote box"** → enters `label`, `ssh_host`, `ssh_user`.
2. Agora shows: *"Run this on your box:"* a one-liner that appends Agora's deploy public key to `authorized_keys` (with a forced-command + source restriction, §9).
3. Dev runs it. Clicks **Connect**.
4. Agora bootstraps (installs + starts the daemon). Within ~a minute, **"Claude (jamshid)"** (and any other agent CLIs) appear online, owned by the dev.
5. Assign issues to the dev's agent; open the co-code editor — it loads over the proxy. QA runs on the box.

That is the entire dev-facing flow: **one command to authorize a key.**

## 7. Editor transport (only inbound flow)

Reuse the cloud editor proxy verbatim:
`GetIssueEditor` (cloud branch, `editor.go:122`) → `launchEditorOnDaemon` (`:190`, now over the tunnel-local address) → `registerEditorTarget` (`:225`) → same-origin `/editor/proxy/{token}/` (`:172`) → `ProxyEditor` (`:254`) reverse-proxies HTTP+WebSocket.
The box's `code-server`/health/editor ports stay bound to **loopback**; the SSH tunnel is the only inbound path. Per-runtime address comes from `connected_box` / `agent_runtime.metadata.editor_addr` (replacing the single global `daemonInternalAddr()` env — additive, fallback preserved).

## 8. QA on the box

- **Agent-driven QA works unchanged.** The agent runs on the box, so its `127.0.0.1` *is* the preview server and Chromium CDP (`health.go:509,513,546`; `browser.go:120,133`). The deterministic gate (`slice_action.go:88`) needs **no edit**. `qa:pass`/`qa:fail` + screenshots post back over the normal agent-output channel.
- **QA box** = a `connected_box` owned by a QA user, with a `local_directory` resource (`local_directory.go:56-102`) pinning the QA checkout to that `daemon_id`. QA tasks route there via `agent.runtime_id`.
- **Live human-watched preview/screencast** (a human watching the QA browser live) is the *only* QA-specific extra work → deferred to P3 (bind preview/CDP off loopback + extend `ProxyEditor`). Optional for launch.

## 9. Security model

- **Deploy-key model.** Agora generates a per-box keypair; the dev authorizes Agora's **public** key only. Add `command="..."`, `from="<backend-ip>"`, `no-pty` restrictions in `authorized_keys` so the key can only open the editor forward + run the bootstrap, not a general shell.
- **Box binds loopback only** (`AGORA_HEALTH_BIND`/`AGORA_EDITOR_BIND` = `127.0.0.1`). The public `sdteam.uz:19514` is **never** opened. Today's `code-server --auth none` (`health.go:301`) and zero-auth health endpoints are safe because they are reachable *only* through the encrypted SSH tunnel.
- **Key storage:** private keys encrypted at rest (KMS / sealed secret), scoped to `workspace_id`.
- **Multi-tenant isolation:** every `connected_box` query filters by `workspace_id`; a box is owned by `owner_id`. Mirrors Agora's existing tenancy.
- **Defense in depth (P4):** `code-server --auth password`, a workspace-membership re-check inside `ProxyEditor` (`editor.go:254` currently checks token + session, not membership), audit log of bootstraps + tunnel opens.

## 10. Phased plan

| Phase | Deliverable | New app code? |
|---|---|---|
| **P0** | One box, **manual** onboard: dev installs daemon (existing self-host flow) + a hand-opened SSH tunnel + backend `AGORA_DAEMON_INTERNAL` pointed at it. Proves native daemon + editor-over-tunnel end-to-end. | **None** (config/ops only) |
| **P1** | Per-runtime editor address: store box address in `agent_runtime.metadata`; replace global `daemonInternalAddr()` (`editor.go:115`) with a per-runtime lookup. Unblocks multiple boxes. | small, additive (`editor.go`, `daemon.go`) |
| **P2** | `connected_box` table + Onboarder API + Bootstrapper (auto-install over SSH) + Tunnel manager. The "just add a deploy key" UX. | new module, flag-gated |
| **P3** | QA box (no daemon code) + live preview/CDP off loopback (`AGORA_PREVIEW_BIND`/`AGORA_CDP_BIND` + `ProxyEditor` token kinds) for human-watched QA. | daemon + handler |
| **P4** | Productization + hardening: self-serve runtime UI, `code-server --auth`, `ProxyEditor` membership re-check, Redis `editorTargets` (multi-replica), offline fail-fast, audit, key rotation. | incremental |

## 11. API additions (all additive)
- `POST /api/remote-boxes`, `GET /api/remote-boxes`, `GET /api/remote-boxes/{id}`, `DELETE /api/remote-boxes/{id}`, `POST /api/remote-boxes/{id}/bootstrap` (re-run).
- Existing endpoints unchanged. `GET /api/issues/{id}/editor` keeps its response shape; a box runtime just yields a `mode:"cloud"`-style `daemon_url` (the proxy URL) the frontend already handles.

## 12. Migrations
- **One additive migration:** `CREATE TABLE connected_box (...)` + indexes on `(workspace_id)`, `(workspace_id, owner_id)`, `(daemon_id)`.
- No `ALTER` on `agent_runtime`/`agent`/`agent_task_queue`. The editor address is a JSON key in the existing `metadata` column.
- `runtime_mode` CHECK constraint **unchanged** (box daemons remain `'local'`).

## 13. Feature flag + rollout
- `AGORA_REMOTE_BOXES_ENABLED` (default off). Off → control-plane routes are not mounted, bootstrapper/tunnel-manager not started; the editor resolver's box branch is dead code (fallback path identical to today).
- Roll out on sdteam first (dogfood), then expose as a product setting per workspace.

## 14. Testing / non-breaking verification
- **Regression (proves nothing broke):** existing editor tests (local `self-host` mode), daemon-register tests, the `TestDaemonEditorBase` resolution-order test (extend with a box-address case keeping the 19514 fallback), cloud-runtime path tests.
- **New:** `connected_box` CRUD + tenancy isolation; bootstrapper idempotency (mock SSH); tunnel-manager lifecycle; editor resolves a box runtime to its tunnel address; flag-off → no new routes mounted.
- **E2E:** onboard a throwaway box (container over SSH), assign an issue, open the editor through the proxy, run QA.

## 15. Open decisions (for the user)
1. **Transport — confirmed SSH control-plane** (matches the "just add a key" goal). Mesh (Tailscale) remains a drop-in alternative for Flow 2 if you later prefer no key-holding.
2. **Where the backend runs** relative to the boxes — if the backend is co-located with the boxes on one private network (sdteam VPC), Flow 2 can be native private DNS (no tunnel at all), which is even simpler. Confirm topology.
3. **Key storage** mechanism (KMS vs sealed secret) and the `authorized_keys` restriction set.
4. **Offline-box failover policy** — `agent.runtime_id` is immutable, so an offline box queues its tasks indefinitely today; choose fail-fast-on-offline vs the existing manual fallback runtime.
5. **Is live human-watched QA required for launch?** If `qa:pass`/`qa:fail` + posted screenshots suffice, P3 is optional (screenshots already transport via agent output; only a live screencast needs P3).
