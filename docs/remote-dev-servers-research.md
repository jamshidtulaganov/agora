# Making Agora Work with Per-Developer Remote Dev Servers

> Research report (multi-agent, code-verified). Question: how to make Agora's agents +
> co-code editor + QA gate run on per-developer remote boxes (`jamshid.sdteam.uz`) and a
> QA box (`qa.sdteam.uz`), scoped per developer. All load-bearing facts confirmed against
> the code.

**Fact-check anchors (verified):**
- Cloud editor proxy: `editor.go:115` (`AGORA_DAEMON_INTERNAL`), `:190` (`launchEditorOnDaemon`), `:225-227` (in-memory `editorTargets`, 8h TTL), `:254` (`ProxyEditor`), same-origin `/editor/proxy/{token}/` at `:172`.
- `owner_id` from PAT member path at `daemon.go:312`; stays zero on `mdt_` via COALESCE.
- `device_name` from `AGORA_DAEMON_DEVICE_NAME` at `config.go:397` → runtime name at `daemon.go:763`.
- `editor_port == HealthPort`, persisted to `agent_runtime.metadata` at `daemon.go:362`.
- QA preview (`health.go:509,513,546`) and Chromium CDP (`browser.go:120,133,194,227`) hardcoded `127.0.0.1`, **no** bind env → human-watching-QA needs net-new daemon code.
- `code-server --auth none` at `health.go:301`; all editor/CORS gates hardcode localhost.
- `runtime_mode` hardcoded `"local"` (`daemon.go:377`), CHECK `('local','cloud')` (migration 004); `hooks.ts:28` guards `!== "local"`; editor branch keys on response `mode` not `runtime_mode` (`editor-section.tsx:231`).

---

## 1. Problem restated

Every developer gets their own named, networked dev box (`jamshid.sdteam.uz`), plus a dedicated QA box (`qa.sdteam.uz`) — real machines with DNS names, reachable over SSH, holding the developer's working checkouts. Agora must drive real product work on those boxes:

- **Agents** (`claude`, `codex`, …) execute on the dev's box, against the dev's checked-out repos, with the dev's git/agent credentials.
- **Co-code editor** (browser code-server) opens on any issue and shows the agent's worktree *living on that box* — without the dev hand-rolling `ssh -L` tunnels.
- **QA gate** runs on a box (most naturally the QA box) and posts `qa:pass`/`qa:fail`.
- **Scoping**: jamshid's tasks → jamshid's box; QA tasks → QA box; UI distinguishes "Claude (jamshid)" from "Claude (qa)".

"Works with remote servers" is **not** "the daemon can run on a remote Linux host" — that already works (pure local `os/exec`). The hard part is **reachability and identity**: editor/preview/QA-browser HTTP surfaces all bind `127.0.0.1`, designed for a laptop (loopback) or Fly's 6PN private net. A public-DNS box is neither.

## 2. Current state — works vs. assumes-localhost

### Already works, zero code change
- **Native execution on the box** — daemon execs agents/git/build via `os/exec` (`claude.go:59`, `repocache/cache.go:256-315`, `execenv/git.go:77-100`). Run `agora daemon start` on the box → all co-located.
- **Per-box identity is automatic** — `daemon_id` = persistent UUID at `~/.agora/daemon.id` (`identity.go:39-74`). Distinct boxes → distinct UUIDs.
- **Per-developer attribution free via PAT** — `DaemonRegister` sets `owner_id` from the authenticating member when the daemon logs in with a user PAT (`daemon.go:312`). Zero schema change. (`mdt_` daemon tokens leave `owner_id` NULL → onboard devs with their **own PAT**.)
- **Device labeling has a hook** — `AGORA_DAEMON_DEVICE_NAME` (`config.go:397`) → "Claude (jamshid)" (`daemon.go:763`).
- **Cloud editor reverse-proxy exists and is exactly what we need** — with `AGORA_DAEMON_INTERNAL` set, the backend POSTs `/editor/launch`, stores `token→addr`, returns same-origin `/editor/proxy/{token}/`, and `ProxyEditor` reverse-proxies HTTP+WS. **Browser never touches the box.** (`editor.go:122-273`)
- **`editor_port` already rides in `agent_runtime.metadata`** (`daemon.go:362`) — precedent for storing a per-box address.
- **`local_directory` pins a checkout to a box** — `{local_path, daemon_id}`, resolved by `daemon_id` (`local_directory.go:56-102`), at-most-one per `(project, daemon)`.
- **Editor branch keys on response `mode`, not `runtime_mode`** (`editor-section.tsx:231`) → forcing cloud mode needs no frontend change.

### Assumes localhost / 6PN — the real gaps
- **Editor/health port binds `127.0.0.1`** (`health.go:57`), override only via `AGORA_HEALTH_BIND`. Note: `editor_port == HealthPort`; `/editor/launch` lives on the health server; the per-launch code-server runs on a separate random port via `AGORA_EDITOR_BIND` (`health.go:1239`). **Two distinct bind knobs**, both must be backend-reachable.
- **QA preview + Chromium CDP hardcoded `127.0.0.1`, NO bind env** (`health.go:509,513,546`; `browser.go:120,133,194,227`). The single most under-budgeted item.
- **CORS hardcodes localhost origins** on every editor/preview/browser endpoint.
- **`code-server --auth none`** (`health.go:301`) + **zero-auth health endpoints** (`/editor/launch`, `/editor/preview`, `/repo/checkout`, `/editor/browser/*`) → on a public box with `AGORA_HEALTH_BIND=0.0.0.0` this is remote code execution.
- **`editorTargets` is process-local in-memory** (`editor.go:225-227`), 8h TTL → breaks multi-replica backend.
- **`runtime_mode` hardcoded `"local"`** (`daemon.go:377`), CHECK `('local','cloud')`.

## 3. Core technical problems
1. **Editor/preview/QA-browser reachability on a networked box.** code-server moves off loopback via `AGORA_EDITOR_BIND`; **preview + CDP cannot** without new daemon code.
2. **Auth/security of a public box.** `--auth none` + zero-auth health + localhost CORS → the box must **never** expose these ports publicly. The transport must be the security boundary (private mesh/tunnel).
3. **Per-developer / box → runtime → repo mapping.** `agent.runtime_id` single-valued/immutable per task; tasks claim per-runtime. Correct routing already works *if* each box is its own runtime owned by the right dev. Gap = labeling (solved) + no auto-failover.
4. **`daemon_id`/port collisions.** Non-issue for distinct boxes; only matters for two devs on one box without `--profile`.
5. **QA browser/CDP over the network.** When the **agent** runs on the same box (always true daemon-per-box), the agent's `127.0.0.1` *is* the preview/CDP → **agent-driven QA works unchanged**. Only a *human watching live preview* needs preview+CDP off loopback (net-new daemon code).

## 4. Three approaches evaluated

### A. Daemon-per-box (each box = a "cloud-mode" daemon behind the backend proxy) — ✅ VIABLE
Run `agora daemon start` on each box, onboard with the dev's PAT (`owner_id`) + `AGORA_DAEMON_DEVICE_NAME`. Force **cloud mode** by giving the backend a per-runtime reachable address (in `agent_runtime.metadata`, like `editor_port`) so `GetIssueEditor` takes the existing `launchEditorOnDaemon → registerEditorTarget → ProxyEditor` path. Browser only talks to the backend over HTTPS; backend reaches the box over a private channel (WireGuard/Tailscale mesh, or reverse SSH forward). Agents/git/worktrees + **agent-driven QA** run native, unchanged.

**Fit:** highest reuse. New code: (1) per-runtime `AGORA_DAEMON_INTERNAL` from metadata; (2) brokering preview+CDP for human-watched QA; (3) hardening (code-server auth, distributed token map). **Effort:** days→weeks. **Risks:** `--auth none` + zero-auth health on a public box → tunnel-only binding is load-bearing; in-memory `editorTargets`; no auto-failover.

**Adversarial verdict (VIABLE, true):** the identity flag is *not* zero-config — `setup self-host` has no `--device-name` flag, so use the env var. Don't conflate health port and editor port. Budget real daemon code for preview/CDP. `ProxyEditor` does **not** re-check workspace membership (only `GetIssueEditor` does at `:139`) — tighten it.

### B. BYO-server via a backend-initiated reverse tunnel — ❌ NOT VIABLE AS DESIGNED
Premise (daemon dials out, backend multiplexes editor frames over the existing WS) is built on **false claims**, confirmed against code:
- **No daemon websocket in `client.go`** — it's 100% HTTP (`postJSON:562`). The only WS is `wakeup.go` → `/api/daemon/ws`, hub in `daemonws/hub.go`.
- **Tasks don't ride the WS** — it's a best-effort *wakeup hint* (`hub.go:84`); a frame just triggers an HTTP `ClaimTask`.
- **Hub physically can't carry editor traffic** — `SetReadLimit(4096)` (`hub.go:356`), 64 KB daemon read cap, send buffer 16 with slow-client eviction. code-server HTTP/WS + JPEG screencast trip both instantly.
- **Direction inversion** — the shipping cloud path is the *opposite*: daemon listens inbound (`AGORA_EDITOR_BIND=0.0.0.0`), backend dials in (`AGORA_DAEMON_INTERNAL`). A reverse tunnel reuses none of `launchEditorOnDaemon`/`ProxyEditor`.

Buildable, but **net-new bidirectional transport** (~3-5 weeks) for a security posture Approach A gets with off-the-shelf WireGuard.

### C. SSH-execution adapter (one central daemon, `ssh user@box <cmd>`) — ❌ NOT VIABLE
**No exec seam to swap** — **101** `exec.Command`/`LookPath`/`CommandContext` sites across **26** files, each inline. `cmd.Dir`/`cmd.Env` have no SSH equivalent (quoting/injection hazard). `LookPath` checks the *daemon's* PATH. Worktree-on-local-disk pervasive (`os.Stat`, `.gc_meta.json`, file serving). `browser.go` is "self-host only" → QA would silently test the daemon host. This is the broad refactor CLAUDE.md forbids, on the hottest daemon code.

## 5. Recommendation

**Adopt Approach A (daemon-per-box in forced cloud mode), backend↔box link via a WireGuard/Tailscale mesh (or reverse SSH port-forward) — not a hand-built reverse tunnel.** The code supports this directly; B and C fight it.

### Phased plan

**Phase 0 — smallest slice: one box, manual config, agent + editor (a few days).**
1. Put `jamshid.sdteam.uz` on the mesh; backend reaches `jamshid.sdteam.uz:19514` over the private interface only (never public).
2. Box bootstrap: install agent CLIs + code-server + Chromium/playwright; profile env `AGORA_DAEMON_DEVICE_NAME=jamshid`, `AGORA_HEALTH_BIND=<mesh-iface>`, `AGORA_EDITOR_BIND=<mesh-iface>` (mesh only, **not** public `0.0.0.0`). `agora setup self-host … && agora login --token <jamshid-PAT> && agora daemon start`.
3. Backend: set `AGORA_DAEMON_INTERNAL` to the box's mesh address → cloud mode for this runtime. Verify editor opens via `/editor/proxy/{token}/`. *No app code yet.*

**Phase 1 — per-runtime addressing (replace single global env) (~1-2 days).**
4. Store box address in `agent_runtime.metadata` at register (alongside `editor_port`, `daemon.go:362`); add a register field (`daemon_addr`).
5. In `editor.go`, replace process-wide `daemonInternalAddr()` (`:115`) with a per-runtime lookup from `agents[0]`'s `agent_runtime` row (mirror `editor.go:51`). Unblocks multiple boxes. Make "remote box ⇒ cloud mode" an explicit invariant.

**Phase 2 — QA box + agent-driven gate (no daemon code) (~1-2 days).**
6. Onboard `qa.sdteam.uz` identically (`AGORA_DAEMON_DEVICE_NAME=qa`, QA-user PAT). Add a `local_directory` resource scoped to qa's `daemon_id`. QA tasks route via `agent.runtime_id`; agent-driven smoke (preview+CDP at co-located `127.0.0.1`) **works unchanged** — `slice_action.go:88` needs no edit.

**Phase 3 — human-watched QA preview/screencast (real daemon code) (~2-3 days).**
7. Give preview listener (`health.go:513`) + Chromium CDP (`browser.go:120,133`) a bind override (`AGORA_PREVIEW_BIND`/`AGORA_CDP_BIND`), flow their ports back through `/editor/launch`, extend `ProxyEditor` with new token kinds for `/editor/preview` and `/editor/browser/stream`. The under-budgeted item — treat as a feature.

**Phase 4 — hardening (before >1 box in prod).**
8. Move `editorTargets` to Redis with heartbeat TTL.
9. Add workspace-membership re-check inside `ProxyEditor` (`:254`).
10. `code-server --auth password` (`health.go:301`); mesh-interface-only binding as a hard provisioning gate.
11. Fix `hooks.ts:28` (`runtime_mode !== "local"`) only if a non-`local` mode is introduced (Phases 0-3 stay `local`).
12. Surface stale heartbeat as "box offline"; fail-fast on enqueue.

**Files touched:** `handler/editor.go`, `handler/daemon.go`, `daemon/health.go`, `daemon/browser.go`, `daemon/config.go`, `cmd/agora/cmd_setup.go` (optional `--device-name`), `views/runtimes/runtime-list*` (offline UX), + ops (mesh + bootstrap). **Untouched:** the 101 exec sites, `repocache/`, `execenv/git.go`, agent backends.

## 6. Open questions / decisions

1. **Networking transport (biggest decision):**
   - **(a) WireGuard/Tailscale mesh** — reuses the existing inbound cloud-editor path verbatim. Lowest code, strong security. **Recommended.**
   - (b) Reverse SSH port-forward (autossh/systemd) — no app code, but more fragile reconnect mgmt.
   - (c) Hand-built reverse tunnel — rejected (WS hub can't carry it).
   - Direct public HTTPS — not an option without first adding auth to code-server + every health endpoint.
2. **Auth model:** confirm devs onboard with **personal PATs** (sets `owner_id`, labels the runtime). User-scoped daemon-token mint flow doesn't exist (`mdt_` not even live).
3. **How many boxes + failover:** `agent.runtime_id` immutable → an offline box queues its tasks indefinitely (manual fallback only). Want fail-fast-on-offline, manual fallback runtime, or "wait for box"?
4. **Is the QA box special?** No — just another daemon-per-box runtime with a `local_directory` pin, owned by a QA user. Agent-driven gate runs unchanged. Only Phase 3 (human watching live preview/screencast) is QA-specific and optional — confirm if needed for launch, or if `qa:pass`/`qa:fail` + posted screenshots suffice (screenshots already transport via the agent-output channel; only live screencast needs Phase 3).
5. **Per-launch bind interface:** Phase 3 needs new bind envs for preview/CDP — confirm naming, default to loopback (safe), open on mesh only when set (matching `AGORA_EDITOR_BIND`).
