# Daemon-per-Dev — Mesh Onboarding (Phase 3)

> Phase 3 of `docs/daemon-per-dev-affinity-design.md`. Needed ONLY when the
> backend is remote from the developer's machine (cloud backend + laptop) AND
> a human wants the browser surfaces — the co-code editor, the Live-testing
> bay's screencast, the trace viewer — against that machine. **Agent-driven QA
> (phases 1–2) needs none of this**: the agent runs on the dev's daemon and
> reaches the app over its own loopback.

## How reachability works

All three human surfaces already flow through backend reverse proxies keyed by
capability tokens:

```
browser ──HTTPS──▶ backend ──(dials)──▶ daemon health mux / code-server / show-trace
          /editor/proxy/{token}   /trace/proxy/{token}   /browser/proxy/{token}
```

The backend picks the dial address per runtime:
`agent_runtime.metadata.editor_addr` → else global `AGORA_DAEMON_INTERNAL` →
else self-host loopback (`resolveDaemonInternalAddr`). Phase 3 fills
`editor_addr` from the daemon itself: `AGORA_DAEMON_ADVERTISE_ADDR` (or
`agora daemon start --advertise-addr`).

The transport between backend and laptop is the **security boundary** — the
proxied daemon surfaces are deliberately unauthenticated on the wire
(code-server runs `--auth none`; the health mux trusts its network). They must
therefore only ever be reachable over a private channel. **Never** bind them
to a publicly routable interface.

## Setup (Tailscale example)

1. **Join both ends to the mesh** — the backend host/container and the dev
   machine. Note the dev machine's mesh IP (e.g. `100.64.1.5`).

2. **Dev machine — env for the daemon** (profile env or shell):

   ```bash
   export AGORA_HEALTH_BIND=100.64.1.5     # health mux (editor/browser/trace launch + screencast WS)
   export AGORA_EDITOR_BIND=100.64.1.5     # spawned code-server + `playwright show-trace`
   export AGORA_DAEMON_ADVERTISE_ADDR=100.64.1.5:19514   # what the backend dials (health port; named profiles differ)
   agora daemon restart
   ```

   Bind to the mesh IP, not `0.0.0.0`, so nothing listens on the LAN/Wi-Fi
   interface. The daemon warns at startup if you advertise while either bind
   is still loopback.

3. **Nothing to configure on the backend.** `editor_addr` rides the daemon's
   register into runtime metadata; every resolver (editor, live browser,
   trace) already prefers it per-runtime over the global
   `AGORA_DAEMON_INTERNAL`.

4. **Verify** from the backend host:

   ```bash
   curl http://100.64.1.5:19514/health          # daemon reachable over mesh
   ```

   Then open any issue whose latest task ran on that daemon — the editor and
   the Live-testing bay should come up in cloud (proxied) mode.

## Env matrix

| Env | Where | Meaning | Default |
|---|---|---|---|
| `AGORA_DAEMON_ADVERTISE_ADDR` | dev machine | host:port the backend dials (→ runtime metadata `editor_addr`) | unset = not reachable cross-machine |
| `AGORA_HEALTH_BIND` | dev machine | bind host of the daemon health mux | `127.0.0.1` |
| `AGORA_EDITOR_BIND` | dev machine | bind host for spawned code-server and `playwright show-trace` | `127.0.0.1` |
| `AGORA_DAEMON_INTERNAL` | backend | global fallback daemon address (Fly 6PN cloud daemon) | unset = self-host |

## Security notes

- The mesh ACL should allow only backend → dev-machine on the daemon ports.
  Dev machines don't need to reach each other.
- The advertised surfaces stay capability-gated end-to-end for humans: every
  proxied request re-checks the Agora session + workspace membership; the
  daemon itself additionally sees only mesh traffic.
- `dev_apps` URLs (phase 2) are unrelated to this: they are dereferenced only
  by agents running ON the machine and are never proxied.
- Do not advertise a public IP/DNS. If you must operate without a mesh, an
  SSH reverse tunnel from the backend host to the laptop's loopback achieves
  the same boundary (`ssh -L`), at the cost of manual lifecycle.
