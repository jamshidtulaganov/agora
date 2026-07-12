# Deploy-Stage Integration via MCP — GitLab CI/CD (and other providers)

**Status:** design, not a build plan for P0. No code changes in this doc.
**Companion:** `docs/deploy-stage-research.md` (the tier ladder and `deploy_event`
schema this doc plugs into — read that first). `docs/sdlc-stage-cockpit-plan.md`
(the cockpit this stage renders inside).
**Question:** `deploy-stage-research.md` §3.2 proposes a "Tier 3: provider
integrations" step — bespoke Go clients for the GitHub Deployments API and the
Fly Machines API — and explicitly defers it. This doc argues Tier 3 should
never be built as bespoke REST clients at all. Agora already has a proven
mechanism for handing an agent a third-party API surface with zero
provider-specific Go code: **MCP**. This doc designs the GitLab case in full
(the team's repos live on self-hosted GitLab, `ssh-gitlab.sdteam.uz`) and
sketches how the same shape extends to GitHub/Fly/Vercel later.

---

## TL;DR

- Agora already proves the "hand an agent a live third-party API via MCP,
  zero bespoke Go client" pattern in production: 8 hosted Zoho MCP servers,
  182 tools, driven entirely by `agent.mcp_config` flowing verbatim into the
  CLI's `--mcp-config` flag. The same shape closes `deploy-stage-research.md`'s
  Tier 3 gap for GitLab without a `gitlab_api.go`.
- A **workspace-level MCP-attachment mechanism already shipped**
  (`default_mcp_config`, migration 141, merged into every agent's config at
  claim time — `applyWorkspaceDefaultMcpServers`,
  `server/internal/handler/workspace_mcp.go:194-217`). It is not "proposed,"
  it is live and unit-tested
  (`TestClaimTaskByRuntime_MergesWorkspaceDefaultMcpServers`,
  `workspace_mcp_test.go:228-288`). This doc's design reuses it — but not by
  asking a human to paste a raw GitLab PAT into that JSONB column (see
  §3, why not).
- **`git_credential` (migration 132) already has a `provider` column whose
  comment literally says `github | gitlab`** — no schema change needed to
  store a GitLab PAT. It's sealed with `AGORA_GIT_SECRET_KEY`
  (secretbox), the same key `editor_token.go` already uses for GitHub/GitLab
  PATs pasted for the co-code editor.
- **Recommended GitLab MCP server: `zereight/gitlab-mcp`** (community, npm
  `@zereight/mcp-gitlab`, actively released — v2.1.30 as of the date of this
  research). It has a self-hosted-first design (`GITLAB_API_URL` env var),
  PAT auth (`GITLAB_PERSONAL_ACCESS_TOKEN`), a pipelines toolset
  (`GITLAB_TOOLSETS=pipelines`, 19 tools: `create_pipeline`, `get_pipeline`,
  `get_pipeline_job_output`, `retry_pipeline`, `cancel_pipeline`,
  `list_deployments`, `list_environments`, …), and a
  `GITLAB_PERMISSION_MODE=modify` knob that allows create/update while
  blocking every delete tool — the right default for a deploy agent.
  GitLab's own first-party MCP server is Premium/Ultimate-only and its CLI
  companion (`glab mcp serve`) is explicitly marked non-production by
  GitLab's own docs — neither is viable for a self-hosted CE-tier instance
  today.
- **Design recommendation: two independent, composable layers**, not one
  config blob. (1) A **credential/attachment layer** — a `git_credential`
  row with `provider='gitlab'` triggers auto-injection of a complete,
  pinned `"gitlab"` MCP server entry at claim time, following the exact
  precedent of `injectFigmaMcpCreds`/`injectZohoMcpProxy` — zero new
  concept, one new Go function. (2) A **target-parameterization layer** —
  `project.settings.deploy_environments[].target` (already proposed in
  `deploy-stage-research.md` §3.1) gets a new `kind: "gitlab_pipeline"`
  with non-secret fields (`project_path`, `ref`, `environment`) — the
  existing `qa_smoke_url`/`docs_repo` JSONB pattern, zero migration.
- **The deploy agent is a new `target.kind` branch inside the `deploy`
  slice-action `deploy-stage-research.md` P1 already proposes** — not a new
  tier, not a new kind. `run_ci` already exists but runs checks *locally on
  the daemon*, not via an external pipeline API — the naming distinction
  matters and is why this reuses `deploy`, not `run_ci`.
- **Write-back reuses the exact mechanism `qa_evidence` already uses**: the
  agent's terminal comment carries a fenced JSON block
  (proposed: ` ```deploy-result``` `), parsed server-side by a new
  `CaptureDeployEvent` alongside today's `CaptureQAEvidence` call in
  `server/internal/handler/comment.go:1044-1052` — no new authenticated API
  endpoint for the agent to call.
- Prod gate: `RequireHumanActor` (`actor_guards.go:96`) on the route that
  fires a `production`-flagged environment's deploy, exactly as
  `deploy-stage-research.md` §3.5 already specifies — this doc does not
  change that placement, it only adds the MCP-driven `target.kind` the gate
  sits in front of.
- Phasing: **MCP-P1** (attach GitLab MCP + manual "Deploy" button + agent
  drives the pipeline + `deploy_event` write-back) is config + one Go
  injector + one template + wiring into the `deploy` slice-action already
  being built — **not** a green-field backend project. **MCP-P2** adds
  auto-trigger on `qa:pass` for non-prod environments, reusing the
  `maybeAutoDocsOnLabel` label-trigger shape. **MCP-P3** repeats the same
  two-layer pattern for GitHub Actions / Fly.io / Vercel.

---

## 1. What's already shipped vs. proposed (grounding this doc in the real codebase)

`deploy-stage-research.md` is the companion doc; this section only adds what
it didn't cover — the MCP plumbing.

| Piece | Status | Where |
|---|---|---|
| `agent.mcp_config` → CLI `--mcp-config` | **Shipped, in prod** (Zoho: 182 tools) | `server/internal/handler/daemon.go` (claim flow, see §2) |
| `workspace.default_mcp_config` merge-at-claim | **Shipped** | migration 141, `workspace_mcp.go:194-217`, wired at `daemon.go:1235` |
| `git_credential` table, `provider='gitlab'` | **Shipped** (schema supports it; no GitLab MCP consumer yet) | migration 132 |
| Editor-token GitLab PAT (`GITLAB_TOKEN` env, for code-server's `glab`/git) | **Shipped**, separate table/purpose | `editor_token.go:38-39` |
| `deploy_event` table | **In progress right now** (another agent, P0) | not yet migrated as of this doc |
| `deploy` slice-action kind, `deploy_cmd`, `deploy_environments` | **Proposed** (P1 in the companion doc) | `deploy-stage-research.md` §3.1-3.2 |
| GitLab MCP attachment (this doc) | **Proposed** | — |

---

## 2. The MCP-attachment precedent: four real patterns, not one

Every MCP server Agora hands an agent today follows one of these four
call-time patterns. Reading them side by side is the fastest way to see
which one GitLab should copy.

### 2.1 `agent.mcp_config` — the base layer

Loaded from the DB per-agent (`server/pkg/db/queries/agent.sql`), decoded in
`ClaimTaskByRuntime` (`daemon.go:1226-1230`) into `mcpConfig json.RawMessage`,
then threaded through `TaskAgentData.McpConfig` (`daemon/types.go:138`) →
`execenv.PrepareParams.McpConfig` (`execenv/execenv.go:42-46`) →
`agent.ExecOptions.McpConfig` (`daemon.go:3246`) → the provider CLI's
`--mcp-config` flag. Shape, enforced by `validateMcpConfigShape`
(`workspace_mcp.go:219-245`):

```json
{"mcpServers": {"<name>": { /* stdio or http entry */ }}}
```

Two entry shapes coexist in the codebase today:

- **Hosted HTTP** (Zoho): `{"type":"http","url":"https://.../mcp/zoho","headers":{"Authorization":"Bearer <token>"}}`
- **Stdio subprocess** (Figma): `{"command":"npx","args":["-y","figma-developer-mcp@0.13.2","--stdio","--no-telemetry"],"env":{"FIGMA_API_KEY":"<token>"}}`
  (`figmaMcpServerEntry`, `figma_mcp.go:221-227`)

### 2.2 `applyWorkspaceDefaultMcpServers` — workspace-wide static defaults

`daemon.go:1235`, called first in the merge chain, before any
credential-filling injector:

```go
mcpConfig = h.applyWorkspaceDefaultMcpServers(r.Context(), runtime.WorkspaceID, mcpConfig)
```

Loads `workspace.default_mcp_config`, adds any server name **not already
present** in the agent's own config (agent entries always win —
`workspace_mcp.go:206-212`), via `mergeMcpServers` (`plugin.go:301-316`,
a straight map union keyed by server name). This is a human-configured,
static JSONB blob edited via `PUT /api/workspaces/{id}/default-mcp-config`
(`router.go:732`), owner/admin only (`authorizeWorkspaceDefaultMcp`,
`workspace_mcp.go:40-68`).

**Important caveat for secret-bearing entries**: `default_mcp_config` is a
plain `jsonb` column (migration 141) — it is **not** sealed with any
secretbox. It is protected only by the owner/admin `PUT`/`GET` ACL, unlike
`git_credential.secret_encrypted` which is encrypted at rest. The Zoho/Zapier
test fixture (`workspace_mcp_test.go:235`) stores a bare `url` with no PAT in
it, because Zoho's proxy pattern (§2.4) never puts a raw secret in this
column. **A GitLab PAT must not go into `default_mcp_config` directly** — see
§3.

### 2.3 `injectFigmaMcpCreds` / `injectLarkMcpCreds` — credential-fill or full auto-provision

Two closely related shapes, both keyed off a workspace- or agent-scoped
*connection* rather than a human-edited MCP blob:

- `injectLarkMcpCreds` (`lark_mcp.go:34`, called `daemon.go:~1240`, right
  after the workspace-default merge): only fills a **blank** credential
  field on a `"lark"` entry the agent/workspace config *already declares* —
  it does not invent the entry.
- `injectFigmaMcpCreds` (`figma_mcp.go:77`, called `daemon.go:1501`, inside
  the per-issue instruction-building block): resolves the workspace's Figma
  OAuth connection, and if the issue references a Figma file, **builds the
  entire `"figma"` server entry from scratch** (`figmaMcpServerEntry`,
  §2.1) — the human never hand-writes a Figma MCP config at all. Returns an
  `Available`/`Note` pair so the instruction text can say "Figma tools are
  ready" or explain why not.

### 2.4 `injectZohoMcpProxy` — hosted-proxy auto-provision

`zoho_mcp_proxy.go:353-372`, called `daemon.go:2014`, **after** the task
token is minted (the proxy's only credential is that token, so it must run
after mint):

```go
if resp.Agent != nil && resp.AuthToken != "" {
    resp.Agent.McpConfig = h.injectZohoMcpProxy(r.Context(), parseUUID(resp.WorkspaceID), resp.Agent.McpConfig, resp.AuthToken)
}
```

If the workspace has a Zoho connection (`GetZohoConnectionForWorkspace`) and
no operator-declared `"zoho"` entry exists, it auto-provisions:

```json
{"type":"http","url":"<AGORA_PUBLIC_URL>/mcp/zoho","headers":{"Authorization":"Bearer <task_token>"}}
```

Agora's own backend proxies the real Zoho calls; the agent's task token is
the only secret it ever sees. This is the safest pattern (secrets never
leave the server) but the most implementation work — it requires a Go-side
JSON-RPC handler per tool (`zoho_mcp_proxy.go` implements
`UpdateRecord`/etc. by hand), which is exactly the "bespoke integration
code" this doc is trying to avoid for GitLab, where a mature third-party
MCP server already exists.

### 2.5 Which pattern fits GitLab

| | Fits GitLab? | Why / why not |
|---|---|---|
| §2.2 static `default_mcp_config` blob | No, for secrets | Unsealed column; a human pasting a live PAT there is a plaintext-at-rest regression the Zoho/Figma/Lark patterns all specifically avoid |
| §2.3 Figma-style (auto-build full entry from a workspace credential) | **Yes** | `git_credential` already IS the "workspace connection" — `provider='gitlab'`, sealed, host-scoped. Building the full stdio entry server-side (like `figmaMcpServerEntry`) means the human never sees or edits raw MCP JSON |
| §2.4 Zoho-style hosted proxy | No, not yet | Would mean hand-rolling a Go GitLab API client behind `/mcp/gitlab` — the exact bespoke-integration cost this doc argues against, when `zereight/gitlab-mcp` already exists and is well-maintained. Revisit only if the community server's trust/maintenance story degrades (see §6, MCP-server-down fallback) |

---

## 3. Config model: two composable layers, not one blob

The prompt's own framing offered three options — per-agent `mcp_config`,
workspace `default_mcp_config` merge, or a `deploy_tooling` block inside
`project.settings.deploy_environments`. The codebase recon shows these
aren't mutually exclusive; they answer two different questions and the
existing precedents (§2) already split them the same way for Figma:

**Layer 1 — "does this workspace have GitLab CI/CD access at all, and with
what token?"** → `git_credential`, `provider='gitlab'`. Already schema-ready
(migration 132: `provider text NOT NULL DEFAULT 'github', host, owner,
secret_encrypted`). A workspace admin adds one row via the existing
`CreateGitCredential` endpoint (`git_credential.go:91-175`) — same UI/flow
already used to store GitHub PATs for private clones, just with
`provider=gitlab, host=ssh-gitlab.sdteam.uz`. **Zero new schema, zero new
UI surface**, only a new consumer of an existing table.

**Layer 2 — "for THIS project's THIS environment, which GitLab project/ref
does a deploy target?"** → extend `deploy-stage-research.md`'s proposed
`project.settings.deploy_environments[].target` (§3.1 of that doc) with a
new `kind`:

```jsonc
project.settings.deploy_environments: [
  { key: "staging", label: "Staging", kind: "gitlab_pipeline",
    target: { project_path: "salesdoctor/sd-main", ref: "staging",
              environment: "staging" /* GitLab Environments name, optional */ } },
  { key: "production", label: "Production", kind: "gitlab_pipeline",
    target: { project_path: "salesdoctor/sd-main", ref: "main",
              environment: "production" },
    requires_human: true }
]
```

This is the exact `qa_smoke_url`/`docs_repo` JSONB-key pattern already used
elsewhere in `project.settings` — **zero migration**. `target` holds only
non-secret routing info (project path, ref, GitLab environment name); the
PAT never appears here.

**Wiring at claim time** (new function, modeled 1:1 on `injectFigmaMcpCreds`):

```go
// injectGitLabMcpCreds auto-provisions the "gitlab" MCP server entry when
// the workspace has a git_credential with provider='gitlab' for a host the
// issue's project actually deploys against. Mirrors injectFigmaMcpCreds:
// builds the full entry (not just a credential fill), so a human never
// hand-writes MCP JSON. No-op if the agent already declares "gitlab".
func (h *Handler) injectGitLabMcpCreds(ctx context.Context, agentID string, issue db.Issue, mcpConfig json.RawMessage) gitlabMcpResult
```

Resolution: same `(host, owner)` lookup `attachRepoAuth` already does
(`git_credential.go:214-248`, `parseRepoHostOwner`) — the issue's project
resource (`project_resource`, `resource_type="github_repo"` — the generic
git-URL type, already used to detect GitLab by string-matching "gitlab" in
the URL at `slice_action.go:362,750`) gives the host; look up the matching
`git_credential` row; decrypt via `gitCredentialBox()` (same box
`git_credential.go`/`editor_token.go` already share); build:

```go
map[string]any{
    "command": "npx",
    "args":    []string{"-y", "@zereight/mcp-gitlab@<pinned-version>"},
    "env": map[string]string{
        "GITLAB_PERSONAL_ACCESS_TOKEN": token,
        "GITLAB_API_URL":               "https://" + host + "/api/v4",
        "GITLAB_PERMISSION_MODE":       "modify", // create/update pipelines, no deletes
        "GITLAB_TOOLSETS":              "pipelines",
    },
}
```

Call site: alongside the Figma call in the per-issue instruction block
(`daemon.go:1501`), gated the same way (`isKnownSliceActionKind` /
issue-scoped, not blanket-attached to every task — a `deploy_smoke`-only
regular `run_qa` task doesn't need pipeline tools in its MCP surface).

**Why this split and not a single `deploy_tooling` blob with an inline
token**: identical reasoning to why `git_credential` is a dedicated sealed
table and not a `project.settings` key — secrets and routing config have
different sensitivity, different audiences (an admin manages credentials
workspace-wide; any project lead edits `deploy_environments`), and
different lifecycles (a PAT rotates independently of which ref an
environment points at). Splitting them means rotating a GitLab token never
touches `project.settings`, and reconfiguring which branch staging deploys
never touches a secret.

**Migration cost: zero.** Both layers reuse existing tables/columns
(`git_credential`, `project.settings`). The only new code is the injector
function + its template text (§5).

---

## 4. External GitLab MCP landscape

### 4.1 Capability matrix

| Server | Maintenance | Self-hosted GitLab | Auth | Pipeline tools | Notes |
|---|---|---|---|---|---|
| **`zereight/gitlab-mcp`** (recommended) | Active, 1.8k★, releases days apart (v2.1.30 as of this research) | Yes — `GITLAB_API_URL` points at any instance | PAT (`GITLAB_PERSONAL_ACCESS_TOKEN`), OAuth2 (local browser or MCP proxy), remote per-request auth | 19 tools behind `GITLAB_TOOLSETS=pipelines`: `create_pipeline`, `retry_pipeline`, `cancel_pipeline`, `list_pipelines`, `get_pipeline`, `list_pipeline_jobs`, `get_pipeline_job`, `get_pipeline_job_output`, `play_pipeline_job`, `retry_pipeline_job`, `cancel_pipeline_job`, `list_deployments`, `get_deployment`, `list_environments`, `get_environment`, `list_job_artifacts`, `download_job_artifacts`, `get_job_artifact_file`, `list_pipeline_trigger_jobs`. Also MRs, issues, wiki, releases, CI/CD variables. | `GITLAB_PERMISSION_MODE=readonly\|modify\|full` and `GITLAB_TOOLSETS`/`GITLAB_TOOLS`/`GITLAB_DENIED_TOOLS_REGEX` give real least-privilege knobs. Transports: stdio, SSE, Streamable HTTP — can run local (Figma-style) or hosted (Zoho-style) later without a rewrite. Install: `npm i -g @zereight/mcp-gitlab`, Docker `zereight050/gitlab-mcp`, Homebrew. |
| GitLab first-party MCP server | GitLab 18.3 experiment → 18.6 beta → 18.7 (MCP 2025-06-18 spec) | Yes (Self-Managed/Dedicated) | OAuth 2.0 Dynamic Client Registration | Unspecified in docs — generic "project info, issues, MRs, GitLab APIs" | **Premium/Ultimate tier only** — verify sdteam.uz's GitLab edition before considering this; if it's CE, this option is off the table entirely. |
| `glab mcp serve` (official CLI) | Real, shipped | Likely (glab supports custom hosts generally) — unverified for the MCP subcommand specifically | Inherits `glab` auth | Docs claim "CI/CD pipeline and job management" but tool list unpublished | GitLab's own docs: *"This feature is an experiment and is not ready for production use."* Do not build on it yet. |

### 4.2 Other deploy-tool MCPs (for MCP-P3, one paragraph each)

- **GitHub** (`github/github-mcp-server`, official): covers Actions —
  `list_workflow_runs`, `list_workflow_jobs`, `list_workflow_run_artifacts`.
  Would slot into the same two-layer design: `git_credential
  provider='github'` (already the default provider today) + a
  `deploy_environments[].target.kind="github_actions"`.
- **Fly.io**: official `flyctl mcp server` exists (manages apps/machines/
  certs) but Fly's own docs call it experimental. Directly relevant since
  Agora's own deploy (`deploy/fly/deploy.sh`) is Fly — could dogfood this
  before GitLab, but it's manual shell today (§1 of the companion doc),
  not a pipeline-trigger problem the way GitLab CI is.
- **Kubernetes**: no single official server; `containers/kubernetes-mcp-server`
  (native Go) and `Flux159/mcp-server-kubernetes` (npm) are the notable
  community options, both with a read-only mode. Not relevant to Agora's
  stated 2-10-person-team target (§2 of the companion doc explicitly rules
  out GitOps/K8s-shaped tooling as overkill for this audience).
- **Vercel / Netlify**: both ship official, OAuth-based, remote MCP servers
  (project/deployment management, env vars). Relevant if a future customer
  is a Vercel/Netlify-deployed Next.js app — same two-layer shape applies.

### 4.3 Security posture

- **Where the MCP server runs**: `zereight/gitlab-mcp` supports stdio
  (local subprocess, Figma-style) or HTTP/SSE (hosted, Zoho-style). This
  doc recommends **stdio on the daemon** for MCP-P1 — the PAT lands in a
  subprocess env on the same daemon machine that *already* receives
  decrypted git PATs today for clone auth (`repocache/cache.go`'s
  `tokenAuthConfig`, `GIT_CONFIG_KEY_N`/`GIT_CONFIG_VALUE_N` env
  injection) — **not a new trust boundary**, the daemon already holds this
  exact secret for git operations on the exact same box. A hosted-proxy
  model (Zoho-style) is a strictly stronger isolation posture (secret never
  leaves the server) but costs a bespoke Go JSON-RPC layer — defer it to a
  "harden later" item, not a P1 blocker (see §6).
- **Token scope**: GitLab PAT `api` scope is required for the write tools
  (`create_pipeline`/`retry_pipeline`/`cancel_pipeline`) — `read_api` alone
  only covers status/log polling. `api` is broader than "just pipelines" (it
  also grants repo + registry read/write), which is a real over-grant.
  GitLab's own guidance recommends **pipeline trigger tokens** or **CI/CD
  job tokens** over PATs for pipeline-only automation, but neither
  `zereight/gitlab-mcp` nor `glab mcp serve` confirms native support for
  those narrower token types — flagged as **unverified, needs a spike**
  before MCP-P1 locks in the token type. Until verified, use a
  **project-scoped** (not group/instance-scoped) PAT stored per
  `(workspace, host, owner)` in `git_credential`, and lean on
  `GITLAB_PERMISSION_MODE=modify` (blocks deletes) + `GITLAB_TOOLSETS=pipelines`
  (blocks everything outside pipelines/deployments/environments) as the
  compensating least-privilege control at the MCP-server layer.

---

## 5. Flow

```
qa:pass on issue (Review stage green)
        │
        ▼
Human clicks "Deploy to <env>" in the Deploy lens   (MCP-P1: manual only)
        │  route: POST /issues/{id}/deploy  (or similar), production-flagged
        │  environments RequireHumanActor-gated (actor_guards.go:96), same
        │  r.With(handler.RequireHumanActor) one-liner as instance-config/PAT
        │  routes (router.go:613-1092) — see deploy-stage-research.md §3.5
        ▼
CreateSliceAction(kind="deploy", scope=<environment key>)
        │  agent resolution mirrors resolveAutoDocsAgent's fallback chain:
        │  project's configured deploy_agent (new project.settings key) →
        │  issue's agent assignee → triggering user's own agent
        ▼
Task claimed by runtime → ClaimTaskByRuntime (daemon.go:1226)
        │  1. applyWorkspaceDefaultMcpServers (unrelated static defaults)
        │  2. injectGitLabMcpCreds (§3) — resolves git_credential by the
        │     issue's project repo host, builds the full "gitlab" MCP entry
        │  3. instruction = buildSliceInstruction("deploy", scope) +
        │     deployGitLabPipelineClause(target) (§6) appended, mirroring how
        │     qaLocalDirectoryClause is appended for run_qa
        ▼
Agent runs on daemon (local dev box or always-on cloud daemon), calls
GitLab MCP tools directly:
        │  create_pipeline(project_path, ref) → pipeline_id
        │  poll get_pipeline(pipeline_id) until status ∈ {success, failed,
        │  canceled} (bounded — see §6 watchdog)
        │  on failure: get_pipeline_job_output(job_id) for the failing job,
        │  included in the write-back for a human to read without opening
        │  GitLab
        ▼
Agent posts terminal comment with a fenced ```deploy-result``` JSON block
(mirrors run_qa's ```qa-result```, run_qa.md's terminal-report convention)
        │  {"environment":"staging","ref":"staging","status":"success",
        │   "pipeline_url":"https://ssh-gitlab.sdteam.uz/.../-/pipelines/123",
        │   "duration_s":184}
        ▼
CreateComment handler (comment.go:1044-1052), authorType=="agent":
        │  new CaptureDeployEvent(ctx, issue, comment.Content) — parses the
        │  fenced block, INSERTs into deploy_event (once P0 ships the table),
        │  exactly the same call shape as today's
        │  h.TaskService.CaptureQAEvidence(...)
        ▼
use-stage-pipeline.ts picks up the new deploy_event row (already the
recommended P0 wiring in the companion doc) → deploySynced/deployState
flips → stepper shows real state for the first time
```

**Human-gate placement**: unchanged from `deploy-stage-research.md` §3.5 —
`RequireHumanActor` sits on the **route that fires the deploy**, not
anywhere inside the MCP call chain. An agent can prepare everything (verify
staging is green, draft release notes, diff since last prod deploy) but the
button that creates the `deploy` slice-action for a `requires_human:true`
environment is a human-only route. This doc doesn't move that boundary, it
only adds a second `target.kind` the boundary already covers.

---

## 6. Instruction / prompt layer

The `deploy` slice-action's base instruction (new
`slice_action_templates/deploy.md`, following `sliceActionTemplate`'s
`//go:embed` convention, `slice_action.go:90-99`) states the generic
contract (write `deploy_event`, never merge/never touch prod without the
gate having already let you in). A `target.kind`-specific clause is
appended the same way `qaLocalDirectoryClause` is appended for `run_qa`
(`connected_box.go:625-634`) — here's the GitLab-pipeline variant:

```go
// deployGitLabPipelineClause instructs a deploy agent that has GitLab MCP
// tools attached (injectGitLabMcpCreds ran and found a credential) to drive
// a real CI/CD pipeline instead of running a local command. Mirrors
// qaLocalDirectoryClause's shape: names the exact tools, states the
// variables, states the write-back contract, states the hard invariant.
func deployGitLabPipelineClause(target gitlabPipelineTarget) string {
	return " DEPLOY TARGET = GitLab CI/CD pipeline (MCP): trigger a pipeline for GitLab project `" +
		target.ProjectPath + "` on ref `" + target.Ref + "` using the `gitlab` MCP server's " +
		"`create_pipeline` tool. Poll `get_pipeline` every ~15s until status is `success`, `failed`, " +
		"or `canceled` — do NOT poll for more than 10 minutes; if it is still `running`/`pending` past " +
		"that, report status=\"timeout\" and stop (a human or the watchdog decides what's next, see " +
		"below). On failure, call `get_pipeline_job_output` for each failed job and include the last " +
		"~50 lines of each in your write-back. NEVER call `retry_pipeline`/`cancel_pipeline` unless " +
		"explicitly asked — your job is to trigger and report, not to manage the pipeline's lifecycle. " +
		"When finished, post a comment containing a fenced ```deploy-result``` JSON block: " +
		"{\"environment\":\"" + target.Environment + "\",\"ref\":\"" + target.Ref + "\"," +
		"\"status\":\"success\"|\"failed\"|\"timeout\",\"pipeline_url\":\"<web_url from get_pipeline>\"," +
		"\"duration_s\":<int>,\"failed_jobs\":[{\"name\":...,\"log_tail\":...}]}. " +
		"Do NOT merge, redeploy, or roll back anything yourself — your report is advisory; a human or " +
		"the next automation phase decides on failure."
}
```

This mirrors `run_qa.md`'s own terminal convention ("Do NOT merge anything —
your verdict is advisory") applied to deploy: the agent's write-back is
data, not a decision.

---

## 7. Failure modes

| Failure | Detection | Response |
|---|---|---|
| **Pipeline pending/running forever** | Agent-side 10-minute poll cap (§6) reports `status="timeout"` rather than blocking the task indefinitely | Mirrors `AGORA_QA_WATCHDOG_WINDOW_HOURS` (`registry.go:45`) — add an analogous `AGORA_DEPLOY_WATCHDOG_WINDOW_MINUTES` so a `deploy_event` stuck at `status="pending"` past the window escalates the same way a silent QA gate escalates to `qa:stale` (`deploy-stage-research.md`'s own P0 section anticipates a `pending` status value for exactly this). |
| **MCP server itself won't start** (npm registry down, daemon can't reach it, self-hosted GitLab unreachable) | The agent's task fails to get pipeline tools at all — `injectGitLabMcpCreds`'s `Available=false` path (mirroring `figmaRes.Available`) appends a note instead of the pipeline clause | Fall back to Tier 2 (`deploy_cmd`, `deploy-stage-research.md` §3.2) if the project has one configured for the same environment — e.g. `curl -X POST <trigger-token-url>` as a plain shell command needs no MCP server at all. Recommend `deploy_environments[].target` allow **both** a `kind="gitlab_pipeline"` and a fallback `command` on the same environment entry for exactly this case. |
| **Wrong-ref deploys** | Agent-supplied `ref` in the instruction is server-computed (from the issue's PR branch or the environment's configured `ref`), never agent-chosen | `deployGitLabPipelineClause` embeds the ref as a literal string in the instruction — the agent is told which ref, not asked to infer it. `deploy_event.ref` is recorded from the same server-side value, so the write-back can be cross-checked against what was actually requested, not just what the agent claims it deployed. |
| **Token expiry / revoked PAT** | `create_pipeline` call fails with a 401 from the MCP server; agent's `deploy-result` reports `status="failed"` with the raw MCP error in `failed_jobs`/a top-level `error` field | Same UX as any other `git_credential` auth failure today (`attachRepoAuth`'s no-op-on-decrypt-failure, `git_credential.go:232-235`) — surfaces as a failed deploy, not a silent hang. A workspace admin rotates the credential the same way as for git clone PATs (`CreateGitCredential` upsert on `(workspace, host, owner)`, `git_credential.go:150-160`). |
| **Agent mutates something outside pipeline scope** (deletes a branch, force-pushes, etc.) | `GITLAB_PERMISSION_MODE=modify` (server-enforced, not just prompted) blocks every delete tool at the MCP-server layer, and `GITLAB_TOOLSETS=pipelines` means MR/repo-write tools aren't even present in the tool list the agent sees | This is the same "code-level, not just prompt-level" enforcement principle `deploy-stage-research.md` §3.4 already argues for `deploy_smoke`'s read-only contract — here it's free (a config flag on the MCP server) instead of new Go code, which is the concrete payoff of the MCP approach over a hand-rolled Go client. |

---

## 8. Phasing

| Phase | What | Layer | Effort |
|---|---|---|---|
| **MCP-P1** | `injectGitLabMcpCreds` (new Go function, ~mirrors `injectFigmaMcpCreds` in size); `deploy_environments[].target.kind="gitlab_pipeline"` support in whatever P1 project-settings UI the companion doc's own P1 builds (this doc adds one `kind` value to a surface already being built, not a new surface); `slice_action_templates/deploy.md` + `deployGitLabPipelineClause`; `CaptureDeployEvent` comment-block parser (mirrors `CaptureQAEvidence`). Manual "Deploy to `<env>`" button only — no auto-trigger. | Backend (small-medium: one injector, one capture function, wiring into the `deploy` kind P1 already adds) + Agent prompt (one template) + zero new frontend beyond what the companion doc's P1 already builds | ~3-4 days **on top of** the companion doc's P1 (which this doc assumes ships first — MCP-P1 adds a `target.kind`, it doesn't duplicate P1's slice-action/route/gate work) |
| **MCP-P2** | Auto-trigger `deploy` on `qa:pass` for non-prod (`kind="gitlab_pipeline"`, `requires_human` unset) environments, reusing `maybeAutoDocsOnLabel`'s label-trigger shape (`slice_action.go:684-975`) gated by a new `AGORA_AUTO_DEPLOY_ENABLED` config flag (one `config.Def{}` entry, `registry.go` pattern). Prod stays manual + `RequireHumanActor`-gated regardless of this phase — auto-trigger never applies to `requires_human:true` environments. Watchdog for stuck pipelines (§7). | Backend (small: one label-trigger function + one config flag, both direct copies of existing patterns) | ~2-3 days |
| **MCP-P3** | Repeat §3's two-layer pattern for GitHub Actions (`git_credential provider='github'` already exists) and optionally Fly/Vercel. Each provider is: one new `injectXMcpCreds` function + one new `target.kind` value + one instruction clause — no new architecture, just more instances of the same shape. | Backend (small per provider, same shape each time) + Agent prompt (one clause per provider) | ~2-3 days per additional provider |

**What stays config-only vs. what's real backend work**: the `git_credential`
row (Layer 1) and the `deploy_environments[].target` JSON (Layer 2) are
**pure configuration** — no code ships for a workspace to start using this
once MCP-P1 lands; only the injector/capture functions and the template are
backend work, and both are direct structural copies of functions that
already exist and are already tested (`injectFigmaMcpCreds`,
`CaptureQAEvidence`). This is the concrete argument for why this design
costs less than the deferred Tier-3 bespoke-API plan in
`deploy-stage-research.md` §3.2/§4: that plan requires a hand-written Go
GitHub/Fly client; this plan requires zero hand-written Go HTTP client for
GitLab, because `zereight/gitlab-mcp` already is that client.

---

## 9. Open questions

- **Verify GitLab tier on `ssh-gitlab.sdteam.uz`.** If it's Premium/Ultimate,
  GitLab's first-party MCP server becomes a second viable option worth a
  side-by-side eval against `zereight/gitlab-mcp` before MCP-P1 locks in a
  choice; if CE, `zereight/gitlab-mcp` is the only realistic path and this
  question is moot.
- **Pipeline trigger tokens vs. PAT.** Flagged in §4.3 as unverified —
  worth a short hands-on spike (point `zereight/gitlab-mcp` at a trigger
  token instead of a PAT and see whether `create_pipeline` accepts it)
  before MCP-P1 finalizes what gets stored in `git_credential` for the
  GitLab MCP use case specifically (a workspace could plausibly want a
  narrower-scoped credential for MCP-pipeline-triggering than the PAT it
  already stores for git-clone auth on the same host — that would mean two
  `git_credential` rows per `(workspace, host, owner)` differentiated by
  purpose, which the current unique index doesn't support and would need a
  follow-up migration if adopted).
- **`deploy_agent` project-setting resolution.** This doc assumes a
  `resolveAutoDocsAgent`-style fallback chain (§5) but doesn't specify
  whether "the deploy agent" should default to the QA squad lead (like
  `isQASliceAction`'s default, `slice_action.go:72-79`) or the dev
  assignee — a deploy is neither pure QA nor pure dev work. Recommend
  defaulting to the project's release-notes/docs agent if one exists
  (`resolveAutoDocsAgent`, `slice_action.go:916`, already resolves one),
  else the triggering human's own agent — but this is a product call, not
  something the codebase recon resolves.
- **`GITLAB_PERMISSION_MODE=modify` vs `readonly` for MCP-P1's own status
  polling.** MCP-P1 needs `create_pipeline` (a write), so `readonly` isn't
  viable even for a "just watch the pipeline" agent. Once `deploy_smoke`
  (companion doc §3.4) reuses the same GitLab MCP attachment for a
  post-deploy verification agent that should never trigger anything, that
  agent's injected entry should use `GITLAB_PERMISSION_MODE=readonly` — a
  second, more restricted `injectGitLabMcpCreds` call variant, not the
  same entry reused for both purposes.
