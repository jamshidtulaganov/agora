# Agora Design Stage — Final Implementation Plan

**Figma-aware agents, a designer-analysis stage, per-project design systems, human-approved decomposition, and design-aware QA — as a native stage of the existing slice-action pipeline.**

---

## 1. Executive summary

Today a Bitrix epic like MUL-348 ("UI/UX Верстка раздела Notifications в SD Billing (По Figma)") syncs into Agora carrying a TZ and a Figma URL, gets assigned to an agent, and produces a PR — while the agent never sees the design. The Figma link is dead text.

This plan ships, in six independently shippable PRs:

1. **Figma eyes for every agent** — one workspace PAT, encrypted at rest, auto-injected into any agent's MCP config at claim time when its issue references Figma — including agents with **no MCP config at all** (the config is synthesized from scratch). Fixes the MUL-348 class of failure with zero per-agent setup.
2. **A `design_proposal` slice action** — a designer agent reads the linked frames node-scoped, maps every UI element against the project's design system (reuse / extend / new), downloads renders, and posts a structured proposal reviewed in a full-screen review dialog.
3. **A per-project design manifest** — `project.settings.design_manifest`, agent-generated and human-editable, dual-shaped for modern token repos AND legacy PHP/Yii+Vue monoliths, injected into every implementation and QA prompt. All writes — agent and human — go through key-scoped `jsonb_set`, so nothing can clobber sibling settings.
4. **Approval → deterministic decomposition** — one endpoint atomically approves (with per-sub-issue overrides), then the **server** creates the children: design context stamped as flat primitive metadata keys (transactionally, inside the create) + a rich description section, sprint inherited, dependencies parked in backlog and **auto-promoted with full task enqueue + WS publish** when prerequisites finish. Squad leaders engage per-child through existing assign triggers.
5. **Design-aware QA** — `run_qa` gains an advisory design-verification section: deterministic DOM/`getComputedStyle` checks against manifest tokens and Figma node values (never pixel-diffing), side-by-side evidence in the QA cockpit.
6. **Automation + hardening** — auto-fired proposals on Bitrix epics behind a per-project toggle and a pure, table-tested heuristic; inbox notifications (rendered end-to-end, incl. the inbox type map) for pending proposals and credential expiry; nightly credential probe with notification dedup; docs; E2E.

**Design philosophy (judge-validated):** every mechanism rides a proven rail — secretbox credentials (`git_credential`/Lark), claim-time MCP injection (`lark_mcp.go`), fenced comment blocks (`test-cases`/`qa-result`), `project.settings` context injection (`qa_manifest`), labels as state (`qa:pass`), comment-trigger dispatch, `IssueService.Create` for children, `issue_child_done` for promotion, the QA cockpit for verification UI. New tables, endpoints, and surfaces exist only where the rails genuinely cannot express the need. And every new server-side write publishes the WS event its rail already publishes — label changes, project updates, issue updates, inbox rows.

Migration numbering: latest is `138_test_case_script` → this feature starts at **139**.

---

## 2. Architecture decisions

| # | Decision | Choice | Rationale |
|---|---|---|---|
| D1 | **Headless Figma access** | Framelink `figma-developer-mcp@0.13.2` (pinned, `--stdio --no-telemetry`) over a plain PAT, agent-side only. **No server-side Go Figma client in v1.** | Figma's official remote MCP is OAuth-browser + client-allowlist only (Figma support: PAT auth "cannot be enabled"); Dev-Mode MCP needs the desktop app open. Both impossible on `sd-agora-daemon`. Framelink is already the preset at `packages/core/mcp/types.ts` (~:139) and flows through `agent.mcp_config → --mcp-config` verbatim (`server/internal/handler/daemon.go:1213`) with zero new dispatch plumbing. Daemon image `node:22-bookworm-slim` satisfies node ≥20.20. A server-side REST client + render cache is deferred (see Out of scope) — at 2-10-person scale, agent-fetched renders persisted as attachments suffice. |
| D2 | **Token storage** | New `figma_credential` table (migration 139), NaCl secretbox via new `AGORA_FIGMA_SECRET_KEY`, one credential per workspace, **plus** `expires_at` + probe fields + an `expiry_notified_at` dedup column. | Verbatim mirror of `git_credential` (migration 132) / Lark: proven, boring, reviewable — including the nullable `created_by … ON DELETE SET NULL` column definition. Figma-specific additions: PATs hard-cap at 90 days (since 2025-04-28); View/Collab-seat tokens get ~6 Tier-1 req/**month** (feature-dead) — both must be surfaced at save time and re-probed nightly, with expiring-soon warnings deduped via `expiry_notified_at`. Generic `workspace_credential(kind)` consolidation considered and deferred: refactoring two working integrations mid-feature is risk without user value. |
| D3 | **Credential → agent injection** | `injectFigmaMcpCreds` mirrors `injectLarkMcpCreds` (blank-fill env merge, operator-set wins) **plus auto-provisions** the pinned figma server entry when the issue carries Figma refs — synthesizing the entire `{"mcpServers":{…}}` document **from scratch when the agent has no MCP config at all** (the `lark_mcp.go:35` empty-config short-circuit is deliberately NOT mirrored, because the flagship MUL-348 agent has an empty config). The same PR pins the UI preset. | Auto-provisioning is the one deliberate extension beyond the Lark precedent, named and constrained: fires only when (credential exists ∧ unexpired ∧ issue has refs ∧ no `figma` entry); pinned version; structured-logged per claim; documented in the built-in skill. This is what makes "paste one PAT → every agent reads Figma" true. Pinning `types.ts` in the same PR kills the two-sources-of-truth drift. |
| D4 | **Design-system storage** | `project.settings.design_manifest` JSONB key (no table), dual-kind `"tokens" \| "inventory"`. **Every write path is a key-scoped `jsonb_set` query** — agent captures AND the human editor (via a dedicated `PUT /api/projects/{id}/design-manifest` endpoint; never the whole-blob `updateProject` replace). **Code-level manual protection** (`source=="manual"` → agent output becomes a review comment, never a direct write). | Matches sibling `qa_manifest` exactly — one home for per-project agent context. `inventory` kind is the only honest answer for sd-main/sd-cs legacy monoliths. `PUT /api/projects/{id}` replaces the whole settings blob from a client-side stale-snapshot spread (`project_qa_manifest.go:141-145`, `project-qa-section.tsx:81`) — routing the human editor through it would reintroduce the clobber race the agent-side `jsonb_set` fixes, so both sides get key-scoped writes. The manual guard turns "don't discard human entries" from a prompt hope into code. |
| D5 | **Proposal storage & surface** | Fenced ```` ```design-proposal ```` comment block (no table), **captured server-side in `TaskService`** at the existing agent-comment ingest points; UI = proposal section in issue-detail + full-screen **review dialog**. No new routes, no Design cockpit. | Keeps the `test-cases`/`qa-result` idiom — and its layering: every existing capture (`CaptureQAEvidence`, `CaptureTestCases`) lives wholly in `TaskService` using only `Queries` + `Bus`, because one of the two ingest points (`service/task.go`) has no handler access and handler→service is the only legal dependency direction. Server-side capture makes labeling/notification independent of agent CLI compliance. Brittleness fixes: revision list ordered by comment timestamp (v1..vN), approve pins the source comment id, malformed blocks render an explicit error card. The dialog gives epic-scale review room without routes; a cockpit was judged premature chrome for 2-10-person teams. |
| D6 | **Approval gate** | Labels `design:proposed/approved/changes_requested` as visible state (server-enforced mutual exclusivity, every swap publishing `EventIssueLabelsChanged`) **plus** one canonical endpoint `POST /api/issues/{id}/design-review` (atomic label swap + note + `sub_issue_overrides` + hook invocation). Manual label attach remains a thin second entrance to the same service core. | Pure labels were non-atomic and couldn't carry overrides; a pure endpoint would abandon the qa:pass-style operating model the team knows. Hybrid gets atomicity, approve-with-overrides (top judge steal), audit (activity rows), and the CLI/label path for power users — two entrances, one implementation. |
| D7 | **Decomposition & promotion** | **Server-side deterministic creation** of children from the approved, override-filtered proposal (`IssueService.Create` per entry; metadata stamped **inside the create transaction** via a new `Metadata` field on `IssueCreateParams`; idempotent per `(design_proposal_comment_id, design_plan_index)`). Children assigned to the parent's squad/agent; `depends_on` entries created as `backlog`. Promotion is a **self-contained `promoteDesignDependents`** in the child-done path that runs **before** the member-assignee and parent-status early returns, and per promoted child (1) updates status, (2) enqueues via `h.TaskService.EnqueueTaskForIssue` / `h.enqueueSquadLeaderTask` per readiness, (3) publishes `EventIssueUpdated` — because the existing backlog→todo enqueue logic lives inline in the HTTP `UpdateIssue` handler (`issue.go:2588-2599`) and does NOT fire on direct DB writes. Promotion is human-gated by the approval itself — **no separate env flag**. Approving a *revised* proposal after an earlier decomposition returns 409 `previous_decomposition_exists` unless the human explicitly supersedes. | Replaces the leader-CLI briefing. Fixes the judged flaws at once: hand-wavy dedup, fire-time markers blocking retries, prompt-compliance risk on the most important transition, the unverified `--metadata` CLI dependency — and the three promotion flaws (no enqueue/no WS event on direct status writes; `notifyParentOfChildDone` early-returning for member-assigned Bitrix epics at `issue_child_done.go:66-74`; a dark env flag deadlocking `depends_on` children in `backlog`). Squad orchestration still engages — per child, through existing `shouldEnqueueSquadLeaderOnAssign`. Human approved exactly this plan, so server creation is human-gated, not agent-autonomous. |
| D8 | **Implementer context** | Children carry a server-composed "Design context" description section (Figma URLs with node-ids + applicable component verdicts) **and flat primitive metadata keys** `design_proposal_comment_id` / `design_plan_index` / `design_depends_on` — never nested objects, per the issue-metadata V1 contract (`issue_metadata.go:18-32`: primitive scalars only, ≤50 keys, 8KB `pg_column_size` CHECK from migration 105). Claim-time injection = Phase 1's Figma note + Phase 3's manifest + Phase 4's design-context specifics; returns `""` fast for non-design work. | The description link IS the inheritance mechanism (agents read descriptions; Phase 1 claim-time extraction fires on them automatically); the flat metadata keys make the dependency/dedup machinery reliable without breaking the contract the metadata `@>` filter and panel rendering rely on (Bitrix already JSON-encodes into string values for exactly this reason, `bitrix_sync.go:690-704`). Rich structured context lives in the description only. Zero new implementer-side plumbing beyond one helper. |
| D9 | **Visual QA** | Extend `run_qa` + the `qa-result` block with an **advisory** `design` section: deterministic DOM/`getComputedStyle` checks vs manifest tokens + Figma node values; screenshots as human evidence; verdict `pass\|fail\|skipped` (infra → `skipped`, never fails the issue); severe mismatches flow through the existing `qa:fail` gate; optional `AGORA_DESIGN_GATE_ENFORCED` ships dark. Rendered as a Design section inside the existing QA review page. | Matches the repo's anti-vision QA doctrine (run_qa already mandates DOM/text assertions and documents `chromium.connectOverCDP`). No parallel gate, no extra `design-qa:*` label pair (label sprawl flagged by judges). Advisory-first + `skipped` + dogfood-before-enforce answers skepticism about legacy markup. |
| D10 | **Image persistence** | Figma render URLs are **never** stored or hot-linked (renders expire in 30 days, fills ≤14). Agents `download_figma_images` to the workdir and re-upload as comment attachments via existing rails; renders are named by a filename contract `figma-<node-id-dashed>.png` matched to `screens[].render`. | Solves URL rot with zero new storage code and gives the UI free rendering via `AttachmentList`/`ReadonlyContent`. The filename contract fixes the previously unspecified screen↔screenshot association. |
| D11 | **Epic auto-fire** | `maybeProposeDesignOnCreate` gated by `AGORA_AUTO_DESIGN_ENABLED` + per-project `design_auto ∈ {off, epics, all}` (default `epics`) + pure `looksLikeDesignEpic`: label `type:epic` always wins; else Bitrix origin ∧ (≥2 distinct Figma refs ∨ description ≥600 chars). No title-keyword regex. **Exactly one fire site per origin:** an explicit call at the end of the Bitrix create path (after comment/attachment import, on a re-loaded issue — `metadata.bitrix_task_id` is stamped only *after* `IssueService.Create` returns, so a create-time hook would always miss it) and an `EventIssueCreated` bus subscriber in `cmd/server` for non-Bitrix creates (there is no "IssueService.Create post-hook" rail; the rail is the bus, `activity_listeners.go:23`). Suppressed for agent creators, Bitrix-origin issues in the subscriber, child issues, existing design state, expired credential, re-syncs. | Tunable, table-tested, locale-independent (the Cyrillic regex judges called brittle is dropped). The per-project toggle stops single-screen tweak tasks from burning designer runs and Figma quota. Agent-creator suppression prevents proposal loops on decomposed children; the one-site-per-origin rule prevents the generic path racing the Bitrix path mid-import. |
| D12 | **Notifications** | In scope from the start: proposal captured/blocked → `inbox_item` rows for the issue's human subscribers + creator (existing `CreateInboxItem` + `subscriber` machinery, **with the `inbox:new` publish** per the `publishQuickCreateInbox` pattern routed by `cmd/server/listeners.go:50`); nightly credential probe loop (pattern: `server/cmd/server/bitrix_poll.go` / `autopilot_scheduler.go`) → probe status + admin inbox notification on invalid/expired/expiring <14d, **deduped via `expiry_notified_at`**. Every new inbox type is registered in the `inbox-detail-label.tsx` type→label map + `inbox.json` (4 locales) in the same PR, with a `default` branch for unknown types. | "Proposal awaiting review" is the PM's most important moment — it must not depend on checking a page, and it must actually render (an unmapped inbox type shows an undefined label). The nightly probe also makes expiry detection independent of the user-entered `expires_at`: a 403 on probe flips status regardless of human memory. |
| D13 | **Skill & language** | Built-in skill `agora-figma` (+ `references/figma-source-map.md`) ships in **Phase 1** and grows per phase. Proposal free-text follows the **issue's language** (Russian TZ → Russian summary); JSON keys stay English. | Any agent touching a Figma link needs the etiquette (node-scoped reads, quota, persist-never-hotlink, failure protocol) from day one. The language rule makes proposals readable by their actual reviewers. Source maps per the CLAUDE.md built-in-skill contract in every touching PR. |
| D14 | **File organization** | New Go code in dedicated files: `handler/figma_credential.go`, `figma_mcp.go`, `figma_links.go`, `design_action.go`, `design_review.go`, `design_decompose.go`, `design_context.go`; `service/design_proposal.go`, `design_manifest.go` (capture + label-state helpers as `TaskService` methods, since the ingest points and Bus live there). `slice_action.go` / `daemon.go` receive only constant/switch/call-site edits. | Answers the god-file concern directly; same package keeps call sites trivial while each concern stays independently testable — and the service/handler split respects the one-way dependency direction (handler imports service, never the reverse). |

---

## 3. Global conventions (every PR)

- Migrations paired up/down; `make sqlc` regenerated; `make migrate-up` clean.
- UUID convention: `loadIssueForUser` for mixed identifiers; `parseUUIDOrBadRequest` for boundary UUIDs; never round-trip raw strings into writes.
- **Every server-side write publishes its rail's WS event** — label changes → `EventIssueLabelsChanged` (`label.go:396`; even the watchdog's direct attach publishes it, `qa_watchdog.go:79-81`), issue status changes → `EventIssueUpdated`, project settings writes → `EventProjectUpdated` (`project.go:580`), inbox rows → `inbox:new`. Each phase enumerates its publications and asserts them in Go tests.
- Issue metadata writes obey the V1 contract (`issue_metadata.go:18-32`): primitive scalar values only, ≤50 keys, 8KB CHECK — nested objects never; JSON-encode into a string value when needed (Bitrix precedent).
- Every UI-consumed response/block: zod + `parseWithFallback` (`packages/core/api/schema.ts`) with explicit fallback **and a malformed-response test in the same PR**. Enum drift downgrades via `default` branches, never crashes.
- Views in `packages/views/`, pure logic in `packages/core/` (zero react-dom / next / router imports), wired to both web and desktop. Zero new global routes → reserved-slug list untouched. Zero new Zustand stores; server state in TanStack Query keyed on `wsId`; label mutations optimistic with rollback.
- i18n: every string in `en`, `ru`, `uz`, `zh-Hans` in the same PR — including `inbox.json` entries for every new inbox item type; consult `apps/docs/content/docs/developers/conventions.mdx` (glossary + zh voice guide) before writing ru/uz/zh-Hans.
- Built-in skill + `references/*-source-map.md` updated in the same PR whenever agent-visible behavior changes.
- Conventional commits `feat(figma)` / `feat(design)`; `make check` green before merge. Prod = `master`; `sd-platform` is integration.

---

## 4. Phases

### Phase 1 — Figma eyes for every agent (PR 1, `feat(figma)`)

**User value:** an admin pastes one Figma PAT in workspace settings; from then on, any agent whose issue references a Figma design can actually open and read it — structured tree + downloaded PNGs. Solves MUL-348.

#### 1.1 Migration `server/migrations/139_figma_credential.up.sql`

```sql
CREATE TABLE figma_credential (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id       UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    label              TEXT NOT NULL DEFAULT '',
    token_encrypted    BYTEA NOT NULL,             -- secretbox(AGORA_FIGMA_SECRET_KEY)
    token_last4        TEXT NOT NULL DEFAULT '',
    token_kind         TEXT NOT NULL DEFAULT 'pat',      -- 'pat' | 'plan_access_token'
    expires_at         TIMESTAMPTZ,                       -- PAT cap: 90d; PlanAccessToken: 365d
    seat_probe         TEXT NOT NULL DEFAULT 'unknown',   -- 'ok' | 'low_seat' | 'unknown'
    probe_status       TEXT NOT NULL DEFAULT '',          -- 'ok' | 'invalid' | 'expired' | 'unreachable'
    probed_at          TIMESTAMPTZ,
    expiry_notified_at TIMESTAMPTZ,                       -- dedup for the <14d expiring warning
    created_by         UUID REFERENCES "user"(id) ON DELETE SET NULL,  -- verbatim git_credential column
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id)
);
```
`139_figma_credential.down.sql`: `DROP TABLE figma_credential;`

(`created_by` copies `132_git_credential.up.sql` verbatim — nullable + `ON DELETE SET NULL`, so deleting a credential-creating user never FK-fails.)

#### 1.2 sqlc — `server/pkg/db/queries/figma_credential.sql`

`UpsertFigmaCredential :one` (`INSERT … ON CONFLICT (workspace_id) DO UPDATE SET token_encrypted, token_last4, token_kind, label, expires_at, seat_probe, probe_status, probed_at, expiry_notified_at = NULL, updated_at = now()` — token rotation resets the expiry-warning dedup), `GetFigmaCredentialForWorkspace :one`, `DeleteFigmaCredential :exec`, `UpdateFigmaCredentialProbe :exec` (probe fields only), `SetFigmaCredentialExpiryNotified :exec`, `ListFigmaCredentialsForProbe :many` (Phase 6). Run `make sqlc`.

#### 1.3 `server/internal/handler/figma_credential.go` (mirror `git_credential.go` end to end)

- `figmaSecretBox()` — `sync.Once` loader of `AGORA_FIGMA_SECRET_KEY` (same helper family as `git_credential.go:28-38`); handlers return 503 `figma_not_configured` when unset.
- `PutFigmaCredential` — `PUT /api/workspaces/{id}/figma-credential`, admin-gated, body `{token, label?, expires_at?, probe_file_key?}`. **`probe_file_key` is validated `^[A-Za-z0-9]{10,}$`** (same charset as `figmaURLRe`'s file-key group) before any URL is built — a crafted value must not redirect the server-side probe (`x/../../v1/teams/…`) to arbitrary api.figma.com endpoints with the workspace token; invalid → 400. **Probe before save:** `GET https://api.figma.com/v1/me` with `X-Figma-Token` (10s timeout); non-200 → 422 `figma_token_invalid`. Best-effort seat heuristic: if `probe_file_key` supplied, one Tier-1 `GET /v1/files/:key?depth=1`; a 429 whose `X-Figma-Rate-Limit-Type` header indicates a monthly bucket ⇒ `seat_probe='low_seat'` (saved but flagged); header absent/ambiguous ⇒ `'unknown'`. Seal token; store `token_last4`; never echo the token.
- `GetFigmaCredentialStatus` — `GET /api/workspaces/{id}/figma-credential` → `{configured, label, token_last4, token_kind, expires_at, expiring_soon (<14d), seat_probe, probe_status, probed_at}`. Never token material.
- `DeleteFigmaCredential` — `DELETE /api/workspaces/{id}/figma-credential`.
- `decryptWorkspaceFigmaToken(ctx, wsID) (token string, expired bool, ok bool)` — internal helper for injection + probes.

**Router** (`server/cmd/server/router.go`) — split across groups, following the deliberate GitHub/Lark precedent ("the integrations tab no longer renders blank for non-admins", `router.go:640-644, :689-692`):
```go
// member-level workspace group (~:640) — status must render for every member
r.Get("/figma-credential", h.GetFigmaCredentialStatus)

// admin-gated workspace group (next to git-credentials, ~:676)
r.Put("/figma-credential", h.PutFigmaCredential)
r.Delete("/figma-credential", h.DeleteFigmaCredential)
```
All three resolve the workspace via `workspaceIDFromURL`; the frontend client uses the full nested path.

#### 1.4 `server/internal/handler/figma_links.go`

- `figmaURLRe = regexp.MustCompile(`https://(?:www\.)?figma\.com/(?:file|design|proto)/([A-Za-z0-9]{10,})[^\s)\]"'<>]*`)` + `node-id` query-param extraction, normalized `208-5147 → 208:5147` (also handles `%3A`).
- `type figmaRef struct { URL, FileKey, NodeID string }`; `figmaRefsFrom(text string) []figmaRef` — **pure, exhaustively table-tested** (this is the platform primitive).
- `issueFigmaRefs(issue db.Issue) []figmaRef` — reads the `figma_links` metadata key when stamped, else live-extracts from description (lazy fallback → no backfill needed). Dedup by `(FileKey, NodeID)`.
- `figmaContextForIssue(refs []figmaRef) string` — claim-time note:
  > *"FIGMA DESIGNS REFERENCED BY THIS ISSUE: `<url>` → `get_figma_data(fileKey="cF4PF…", nodeId="208:5147")` … Read ONLY the referenced node(s), scoped by nodeId — never fetch a whole file. Download frames you need with `download_figma_images` (pngScale=2) into your workdir. Figma render URLs expire — if you post images, upload them as comment attachments. Quota is ~10-20 req/min: batch node ids, honor Retry-After on 429."*

**Metadata stamping — contract-compliant:** the issue-metadata V1 contract allows **primitive scalar values only** (`issue_metadata.go:18-32`; 8KB `pg_column_size` CHECK from migration 105). So the stamp is a single key `figma_links` whose value is a **JSON-encoded string** (`"[{\"url\":…,\"file_key\":…,\"node_id\":…}]"`) — the exact idiom Bitrix uses for `bitrix_task_id` (`bitrix_sync.go:690-704`). Cap at 5 links; skip stamping entirely (live-extraction still works) if the write would approach the 8KB CHECK on top of existing keys. Stamped in `IssueService.Create` (next to the existing origin-stamping/sprint-inheritance region of `server/internal/service/issue.go`) and on description updates, when refs found and stamp differs (best-effort, idempotent). Bitrix create path gets it free via `IssueService.Create`; add the same stamping to the Bitrix UPDATE reconcile branch (`bitrix_sync.go` ~:554-646).

#### 1.5 `server/internal/handler/figma_mcp.go` (modeled on `lark_mcp.go:34-146`, with one deliberate divergence)

`injectFigmaMcpCreds(ctx, h, workspaceID, issue, mcpConfig []byte) (out []byte, note string)`:
1. No credential → unchanged.
2. Expired (`expires_at < now()` or `probe_status ∈ {expired, invalid}`) → unchanged + note *"The workspace Figma credential is expired/invalid — tell the user to renew it in Settings → Integrations → Figma instead of failing silently."*
3. `mcpServers.figma` present → `mergeFigmaMcpEnv` fills `FIGMA_API_KEY` **only when blank** (operator-set wins; exact `mergeLarkMcpEnv` semantics).
4. `mcpServers.figma` absent ∧ `issueFigmaRefs` non-empty → **auto-provision** the entry `{"figma":{"command":"npx","args":["-y","figma-developer-mcp@0.13.2","--stdio","--no-telemetry"],"env":{"FIGMA_API_KEY":"<decrypted>"}}}`. **Unlike `injectLarkMcpCreds` (which short-circuits on `len(mcpConfig)==0`, `lark_mcp.go:35`), this function does NOT bail on a nil/empty config** — most agents have no MCP config at all (`daemon.go:1212-1216` leaves `mcpConfig` nil), including the MUL-348 assignee. When the config is empty and refs exist, synthesize the full `{"mcpServers":{"figma":{…}}}` document from scratch. Structured log `figma_mcp_injected{ws, agent, auto_provisioned, synthesized}` per claim.

**Wire in `ClaimTaskByRuntime`** (`daemon.go` immediately after `injectLarkMcpCreds` ~:1220 — note the call site must run even when `agent.McpConfig` is empty); append `figmaContextForIssue` + the expiry note in the instruction-assembly region next to the QA-manifest injection (~:1380-1391).

**Daemon image:** `Dockerfile.daemon` — `npm install -g figma-developer-mcp@0.13.2` next to `@larksuiteoapi/lark-mcp` (kills cold npx fetches).

**Preset pinning:** `packages/core/mcp/types.ts` figma template → `argsText: "-y figma-developer-mcp@0.13.2 --stdio --no-telemetry"` + `scopeHint` ("PAT from a Dev/Full seat; scopes: file content read. View seats are rate-limited to ~6 req/month."). Comment cross-references the backend constant.

#### 1.6 Built-in skill — `server/internal/service/builtin_skills/agora-figma/`

`SKILL.md`: URL→`fileKey`/`nodeId` parsing (dash→colon); the two tools (`get_figma_data`, `download_figma_images`); node-scoped-reads-only + no `depth` unless necessary; persist-never-hotlink (re-upload renders as Agora attachments); rate discipline (Tier-1 budget, Retry-After, per-task workdir cache keyed `(fileKey, version)`, batch ids); failure protocol (403/404 → say so and stop, never guess; monthly-bucket 429 → "workspace token is a View seat — ask an admin to replace it"); note that Figma's official MCPs are unusable headless. `references/figma-source-map.md` traces each claim to `figma_mcp.go` / `figma_credential.go` / `figma_links.go` / Framelink docs.

#### 1.7 Frontend

- `packages/core/figma/links.ts` — TS twin of `figmaRefsFrom` (pure; `import type`-safe). Tests: design/file/proto URLs, `node-id` variants (`208-5147`, `208%3A5147`), multiples, trailing punctuation, no-match — include the real MUL-348 URL.
- `packages/core/api/schemas.ts` — `figmaCredentialStatusSchema` (all fields optional-with-defaults; check `configured === true`), parsed via `parseWithFallback` (fallback `{configured:false}`). `client.ts`: `getFigmaCredentialStatus()`, `putFigmaCredential(body)`, `deleteFigmaCredential()` — all against `/api/workspaces/{id}/figma-credential`. Query options keyed on `wsId`.
- `packages/views/settings/components/figma-integration-section.tsx` — password token field (never re-displayed), label, expiry picker (**prefill +90d**), optional probe-file-key field, status card (`label • …last4 • expires`), amber `expiring_soon` badge, red `seat_probe='low_seat'` warning, Save/Remove. Status renders for all members (member-level GET); Save/Remove affordances admin-only. Mounted in the existing workspace-settings integrations surface on **both** web and desktop (shared component; no new route).
- Issue-detail chip: `figma-links-chip.tsx` — "Figma design linked (N)" in the sidebar when the shared TS extractor finds refs in `issue.description` (client-side; works against any server version).

#### 1.8 i18n (4 locales)

`settings.json` → `figma.{title, description, token_label, token_placeholder, label_label, expires_label, probe_file_label, save, remove, configured_as, not_configured, expiring_soon, seat_warning, status_ok, status_invalid, status_expired, toast_saved, toast_removed, toast_invalid_token}`. `issues.json` → `figma_links.{chip_label, open_in_figma}`.

#### 1.9 Tests

- Go: `figma_links_test.go` (URL matrix incl. MUL-348); stamping idempotency + primitive-scalar shape + size-cap skip; `figma_credential_test.go` (seal/open roundtrip, upsert replace resets `expiry_notified_at`, probe-fail → 422, `probe_file_key` injection strings rejected with 400, non-admin 403 on PUT/DELETE, member 200 on GET, status never leaks token, 503 without secret key); `figma_mcp_test.go` (no credential → unchanged; blank-fill only; operator env preserved; auto-provision pinned entry only with refs; **empty/nil config + refs → full `mcpServers` document synthesized** — the MUL-348 case; expired → note, no injection).
- Vitest: `links.test.ts`; schema malformed suite (missing `configured`, `expires_at: 123`, `null` body → fallback, `[api-schema]` warning); `figma-integration-section.test.tsx` (states + save flow, mocked `@agora/core/api`).

#### 1.10 Rollout

Add `AGORA_FIGMA_SECRET_KEY` to Fly secrets, `.env.example`, `docker-compose.override.yml`. Rebuild the daemon image. No behavior change for unconfigured workspaces. **Acceptance:** re-fire MUL-348's assignee; transcript shows `get_figma_data(fileKey="cF4PFq3P5NOyZvp01JSHnE", nodeId="208:5147")` returning real content from the cloud daemon.

---

### Phase 2 — `design_proposal` action, capture, review dialog, approval endpoint (PR 2, `feat(design)`)

**User value:** on any Figma-linked issue, fire **Design proposal**; the designer agent posts a structured, rendered proposal; humans review it in a full-screen dialog and Approve / Request changes. (Approve completes the label/state flow now; actual decomposition lands in Phase 4 behind the same seam.)

#### 2.1 Slice action (`handler/design_action.go`; tiny edits in `slice_action.go`)

- `slice_action.go`: add `sliceActionDesignProposal = "design_proposal"` to the const block (:35-46) and `isKnownSliceActionKind` (:51-58). **Not** in `sliceActionOpensPR` — no branch/PR.
- `buildSliceInstruction` case (pure, unit-tested). **Recipe sketch:**

> *"You are acting as a DESIGNER-ANALYST. Analyze the design(s) linked from this issue against this project's existing design system and produce a decomposition proposal for humans to approve. Do NOT write implementation code and do NOT create issues.*
> *1) READ: for each Figma link (listed in your context), call `get_figma_data(fileKey, nodeId)` node-scoped. Download a PNG render of each top-level frame (`download_figma_images`, pngScale=2), name each file `figma-<node-id-with-dashes>.png`, and upload them as attachments on your reply comment.*
> *2) INVENTORY: list every distinct screen/state (name, node id, one-line purpose, visible elements incl. empty/error/loading states).*
> *3) MAP against the PROJECT DESIGN SYSTEM context below. If none is provided, first inspect the repository read-only using the repo(s) in your task context (do not push, do not open PRs): enumerate existing components/partials/shared CSS. Classify every element: REUSE (name the exact file), EXTEND (existing + what changes), or NEW (justify why nothing fits). Prefer reuse aggressively — on legacy codebases, matching the existing app beats matching the mock pixel-perfectly.*
> *4) FLAG DEVIATIONS: Figma values that contradict the project's tokens/conventions are questions for the human, not silent decisions.*
> *5) PROPOSE SUB-ISSUES: one per coherent shippable slice, each with title, 2-4 sentence description embedding its Figma URL(s) with node-ids, the component decisions that apply, and `depends_on` indices for ordering.*
> *6) OUTPUT: a human-readable summary WRITTEN IN THE SAME LANGUAGE AS THE ISSUE DESCRIPTION, then exactly one fenced block tagged `design-proposal` (schema below; JSON keys in English, free-text fields in the issue's language).*
> *7) If any Figma link is inaccessible (403/404) or quota-blocked after honoring Retry-After once, emit `status:"blocked"` with a machine-readable reason — a blocked proposal is a valid, useful output. Never fabricate design content. Budget: one structured read per frame, one batched image download."*

- **Block contract** (documented in recipe + skill):
```jsonc
{
  "status": "ok" | "blocked",
  "reason": null | "figma_forbidden" | "figma_not_found" | "figma_quota" | "credential_missing" | "other",
  "reason_detail": "…",
  "figma":      [{"url","file_key","node_id"}],
  "screens":    [{"name","figma_node_id","summary","render":"figma-208-5147.png"}],
  "components": [{"name","verdict":"reuse"|"extend"|"new","code_ref":null|"path","figma_node_id":null|"…","notes"}],
  "deviations": [{"aspect":"color|typography|spacing|other","figma_value","project_value","question"}],
  "sub_issues": [{"title","description","screens":["…"],"node_ids":["…"],"depends_on":[0]}],
  "open_questions": ["…"]
}
```
- `CreateSliceAction` assembly: append `figmaContextForIssue` + `sliceActionDesignManifestContext` (Phase 3 helper — call site lands now, returns `""`) + `sliceActionQADocsContext` + existing repo context.
- **Designer resolution** — `resolveDesignerAgent(ctx, h, ws, issue, explicitID)` in `design_action.go`: explicit `agent_id` → `project.settings.design_agent` (helper mirroring `projectDocsAgentID`, :558-573) → leader of a squad whose name contains "design" (clone `qaSquadLeader`, :1006-1025) → fall through to the existing `resolveSliceActionAgent` chain (assignee-if-agent → caller's own agent). All candidates pass `sliceAgentReady` + private-agent access checks.
- **Extract a shared instruction-builder helper** so the manual path (this PR) and the auto path (Phase 6) render byte-identical prompts.

#### 2.2 Server-side capture — `service/design_proposal.go` (**`TaskService` methods**)

Capture lives **wholly in `TaskService`**, like every existing capture (`CaptureQAEvidence`, `CaptureTestCases`): the second ingest point (`service/task.go` ~:2427, `createAgentComment`) has no handler access, and handler→service is the only legal dependency direction. `TaskService` already has everything needed — `Queries`, `Bus`, `CreateInboxItem` (`task.go:2609`).

- `ParseDesignProposalBlock(content string) (*DesignProposal, error)` — regex fence extraction (pattern: `parseTestCasesBlock`, `service/test_case.go`), strict unmarshal, fail closed. Distinguishes *no block* / *invalid block* / *ok* / *blocked*.
- `(s *TaskService) CaptureDesignProposal(ctx, issue, comment, authorID)` — invoked from **both** agent-comment ingest points: `handler/comment.go` `authorType == "agent"` block (:1041-1046, next to `CaptureTestCases`, via `h.TaskService`) and internally from `service/task.go` (~:2427). On valid block: attach state label via the service-level `SetDesignStateLabel` (below) — `design:proposed` (or leave state untouched and record when `blocked`); activity row `design_proposal_generated` (details: comment id, screen/component counts, status/reason); **inbox notifications** via `CreateInboxItem` for the issue's human subscribers + human creator (types `design_proposal_ready` / `design_proposal_blocked`), **each followed by the `inbox:new` publish** (`publishQuickCreateInbox` pattern, routed by `cmd/server/listeners.go:50`). On invalid block: activity `design_proposal_invalid` + structured log (no label).
- `LatestDesignProposalForIssue(ctx, issueID) (proposal, sourceCommentID, err)` — newest agent comment containing a block.

#### 2.3 Design-state labels — **service-level**, `service/design_proposal.go`

- `(s *TaskService) ensureDesignLabel(ctx, wsID, name, color)` — port of the `h.ensureLabel` pattern (`qa_watchdog.go:25`) onto `Queries`: `design:proposed` `#8b5cf6`, `design:approved` `#22c55e`, `design:changes_requested` `#f59e0b`.
- `(s *TaskService) SetDesignStateLabel(ctx, issue, target)` — attaches the target, **detaches the other two** (mutual exclusivity enforced server-side), and **publishes `EventIssueLabelsChanged`** via the Bus (matching `label.go:396` and the watchdog's own direct-attach publish at `qa_watchdog.go:79-81`). Also invoked from the generic label-attach path so a CLI `agora label attach … design:proposed` after changes-requested cannot leave contradictory states.
- The handler layer (`design_review.go`, `design_action.go`) calls these service methods; no label logic in handlers.

#### 2.4 Review endpoint — `handler/design_review.go`

`POST /api/issues/{id}/design-review` (router: issues group), body `{action: "approve"|"request_changes", note?, sub_issue_overrides?: [{index, include, title?, description?}]}`. Issue via `loadIssueForUser`; member-gated. Delegates state changes to the `TaskService` core (§2.2/2.3) — two entrances, one implementation.
- Common: `LatestDesignProposalForIssue`; 404 `no_design_proposal`; 409 `design_proposal_unparseable`; approve additionally 409 `design_proposal_blocked` when `status=="blocked"`.
- **approve**: `SetDesignStateLabel(approved)` (publishes `EventIssueLabelsChanged`); activity `design_approved` (details: source comment id, overrides, reviewer); system comment quoting the note; invoke `decomposeApprovedProposal(...)` — **a no-op seam until Phase 4** (which also adds the 409 `previous_decomposition_exists` guard, §4.1).
- **request_changes**: `SetDesignStateLabel(changes_requested)`; post a comment that **@mentions the resolved designer agent** (`resolveDesignerAgent`; 409 `no_designer_available` if unresolvable): *"Changes were requested on your design proposal: <note>. Read the latest human comments, revise, and post a new `design-proposal` block."* Routes through `triggerTasksForComment` (`comment.go:1069`) like every slice action; the new block's capture re-attaches `design:proposed` (exclusivity detaches `changes_requested`).
- Manual label attach of `design:approved` (CLI path) fires a small hook calling the same service core with empty overrides.

#### 2.5 Frontend

- `packages/core/design/proposal.ts` — `designProposalSchema` (zod: arrays default `[]`; unknown `verdict` → `"new"`-style generic badge via `default` branch; unknown `status` → treated as `blocked` with generic reason), `extractDesignProposalBlocks(comments)` returning **all** blocks ordered by comment timestamp (`v1..vN`, newest active) with per-block parse status (`ok | blocked | invalid`).
- `packages/views/issues/components/design-proposal-section.tsx` — mounts in `issue-detail.tsx` (progressive disclosure: Figma refs or a block exist). Shows: state banner from labels; latest proposal summary (screens count, reuse/extend/new counts); **explicit error card when the newest block is unparseable** ("Proposal could not be parsed — request a re-run") with a re-fire button; blocked banner with reason-specific remediation copy; "Open review" button.
- `packages/views/issues/components/design-review-dialog.tsx` — full-screen overlay (shared component; works on web + desktop; **no routes**): screens gallery (thumbnails matched by `screens[].render` filename against the proposal comment's `TimelineEntry.attachments`, click-to-zoom via existing attachment preview); component verdict table (reuse/extend/new badges, `code_ref` inline code); deviations list (question-styled); sub-issue plan list with **include/exclude checkboxes + inline title/description edit** (feeds `sub_issue_overrides`); open questions; revision dropdown (v1..vN); triage bar: **Approve** (confirm dialog: "Approving will create the sub-issues below" — wording effective from Phase 4) and **Request changes** (note input required). Mutations → `POST design-review`, optimistic label state, invalidate `issueKeys.detail(wsId, id)`.
- **Inbox rendering** — new types `design_proposal_ready` / `design_proposal_blocked` added to the type→label map in `packages/views/inbox/components/inbox-detail-label.tsx` (with a `default` branch for unknown types, per the enum-drift rule) — an unmapped type renders an undefined label.
- `slice-actions-section.tsx` — add the `design_proposal` button, rendered only when the shared extractor finds Figma refs. Extend the kind union in `packages/core/api/client.ts`.

#### 2.6 Skill + i18n

- `agora-figma/SKILL.md`: proposal block contract, language rule, blocked protocol, filename contract; source map updated.
- `issues.json` (4 locales): `slice_actions.action_design_proposal`, `slice_actions.toast_design_proposal_fired`; `design_proposal.{title, open_review, status_proposed, status_approved, status_changes_requested, status_blocked, status_invalid, invalid_hint, refire, screens, components, verdict_reuse, verdict_extend, verdict_new, deviations, sub_issues, open_questions, include_sub_issue, edit_title, edit_description, approve, approve_confirm_title, approve_confirm_body, request_changes, changes_note_placeholder, revision, blocked_reason_figma_forbidden, blocked_reason_figma_not_found, blocked_reason_figma_quota, blocked_reason_credential_missing, blocked_reason_other, toast_approved, toast_changes_requested, no_proposal_yet}`.
- `inbox.json` (4 locales): `design_proposal_ready`, `design_proposal_blocked`.

#### 2.7 Tests

- Go: recipe content assertions (node-scoped mandate, language rule, no-issue-creation, blocked contract, filename contract); `ParseDesignProposalBlock` (valid / malformed / blocked / two blocks → newest / human author ignored); capture (label attach, exclusivity swap, **`EventIssueLabelsChanged` published on every swap**, inbox items created **with `inbox:new` published**, invalid → activity only, capture callable from both ingest points — compile-level guarantee that no handler symbol is referenced); review endpoint state machine (approve, request_changes @mention present, 404/409 matrix, non-member 403); `resolveDesignerAgent` chain; manual-label entrance calls the same service core.
- Vitest: proposal schema malformed suite (missing `screens`, `components: null`, unknown `verdict`/`status` downgrade); section states (ok/blocked/invalid/empty); dialog (gallery filename matching, overrides payload shape, approve/request flows with mocked api); button gating; inbox label map renders the new types + default branch.

**Rollout:** manual fire only. Dogfood on MUL-348: fire, verify block + PNGs + panel + approve/request-changes round-trip.

---

### Phase 3 — Per-project design manifest (PR 3, `feat(design)`)

**User value:** each project gets a durable design-system memory — agent-generated, human-editable — injected into designer, implementation, and (later) QA prompts. sd-billing gets tokens+components; sd-main gets an honest inventory of its legacy system.

#### 3.1 Shape — `project.settings.design_manifest` (no migration; column exists since 123)

```jsonc
{
  "kind": "tokens" | "inventory",
  "source": "agent" | "manual" | "mixed",
  "revision": 3, "updated_at": "…",
  "figma": { "library_file_key": "cF4PF…", "notes": "SD Dashboard master" },
  "tokens": { "colors": {"primary":"#2563EB"}, "typography": {"body":"Inter 14/20"}, "spacing": {"unit":"4px"} },
  "components": [
    {"name":"DataTable","code_ref":"frontend/src/components/DataTable.vue","figma_node_id":"12:34","usage":"all list screens"},
    {"name":"Yii grid wrapper","code_ref":"protected/widgets/SdGrid.php","usage":"legacy admin tables"}
  ],
  "conventions": ["BEM-ish classes in css/main.css", "no CSS build step"],
  "anti_patterns": ["no new global CSS"],
  "legacy_notes": "no tokens; match existing pages by copying markup from protected/views/billing/…",
  "screens_reference": "https://agora-cs.sdteam.uz"
}
```
All fields optional. Go struct `designManifest` next to `qaManifest` (`slice_action.go:379-391`-adjacent, in `design_action.go`).

#### 3.2 Backend

- **sqlc** `project.sql`: `SetProjectDesignManifest :exec` → `UPDATE project SET settings = jsonb_set(COALESCE(settings,'{}'::jsonb), '{design_manifest}', $2::jsonb, true) WHERE id = $1` — **key-scoped write; cannot clobber `qa_manifest`/`sprint_mode`**. Sibling `SetProjectDesignSettingKey :exec` for the scalar keys (`design_agent`, `design_auto`), same `jsonb_set` shape.
- **Key-scoped human write endpoint** — `PUT /api/projects/{id}/design-manifest` (handler mirroring the `SetProjectQAManifest` handler shape in `project_qa_manifest.go`, but backed by the `jsonb_set` queries), body `{manifest?, design_agent?, design_auto?}`: each **provided** key is written via its own `jsonb_set`; omitted keys untouched. This is deliberate: `PUT /api/projects/{id}` **replaces the whole settings blob** from a client-side stale-snapshot spread (`project_qa_manifest.go:141-145`, `project-qa-section.tsx:81`) — routing the design editor through it would let a human save wipe a concurrent agent-written `design_manifest` or `qa_manifest`. Manifest writes from this endpoint set `source:"manual"`. **Publishes `EventProjectUpdated`** (`project.go:580` pattern).
- `sliceActionDesignManifestContext(ctx, h, issue) string` — exact clone of `sliceActionQAManifestContext` (:398-454): renders "PROJECT DESIGN SYSTEM (rev N, kind=…): tokens…, components (name → code_ref, usage)…, conventions…, anti-patterns…, legacy notes…". Caps: top ~40 components / ~4KB with overflow note. `""` when unset. **Wire:** `design_proposal` assembly (Phase 2 call site goes live), every claim in `daemon.go` (next to the QA-manifest injection ~:1383-1391), and the `draft_code`/`write_tests` assembly block (:1884-1905).
- **New slice action** `sliceActionGenDesignManifest = "gen_design_manifest"` (+ const/switch edits). **Recipe sketch:**

> *"Build or refresh this project's DESIGN MANIFEST. 1) REPO CENSUS (read-only): detect the stack. Token-based repos: read tokens.css / tailwind config / theme files; enumerate shared components → kind='tokens'. **Legacy monoliths (PHP/Yii + Vue like sd-main): there is no formal token system — derive the de-facto one:** enumerate Vue SFCs, Yii widgets/partials, layout templates; frequency-rank the top ~20 colors/font-stacks/spacing values from shared CSS as de-facto tokens; record conventions and anti-patterns; write honest legacy_notes ('no tokens; copy markup from <path>') → kind='inventory'. 2) FIGMA CENSUS only if a library fileKey is configured: read published styles + component names node-scoped; map to repo components by name similarity; leave figma_node_id blank when unsure — never invent. Do NOT attempt the Figma Variables API. 3) OUTPUT exactly one fenced block tagged `design-manifest` matching the schema. Keep it under ~150 lines — this is a map injected into prompts, not documentation. The existing manifest (if any) is in your context: update it, preserve human-added entries."*

- **Capture** — `service/design_manifest.go` (**`TaskService` method**, same layering as §2.2): `CaptureDesignManifest(ctx, issue, comment, authorID)` invoked from the same two agent-comment ingest points. Agent-author only; parse + strict-validate; **if the existing manifest has `source=="manual"` → do NOT write; post a system comment "Proposed manifest update (review & paste into Project → Design if accepted)" containing the block**; else `SetProjectDesignManifest` with `revision+1`, `source` `agent`/`mixed`, `updated_at`, **followed by an `EventProjectUpdated` publish** (project page must not go stale); activity `design_manifest_updated`.
- **Sync trigger** — `POST /api/projects/{id}/design-manifest/sync` (`handler/design_action.go`): creates (or reuses an open) chore issue "Design manifest sync — <project>" in the project via `IssueService.Create`, then fires `gen_design_manifest` on it targeting `resolveDesignerAgent` (409 `sync_already_running` if an open sync issue has a pending task; 409 `no_designer_available`). Visible, auditable, zero new dispatch machinery. `gen_design_manifest` is also fireable manually on any issue in the project.
- **Lazy bootstrap:** append to the Phase-2 `design_proposal` recipe: *"If no PROJECT DESIGN SYSTEM context was provided, first derive one (same rules as the manifest recipe) and emit a `design-manifest` block before your proposal — it will be captured for the project."*

#### 3.3 Frontend

- `packages/views/projects/components/project-design-section.tsx` (pattern: `project-qa-section.tsx:22-187`, mounted in `project-detail.tsx` under the QA section): summary chips (kind, N components, rev, source badge, updated_at); **Design agent** picker; Figma library file-key input; **validated JSON editor** (the `mcp-config-tab.tsx` object-validation pattern); **Generate with agent** button → sync endpoint + toast; empty state with hint. **All saves go through the key-scoped `PUT /api/projects/{id}/design-manifest` endpoint — never the whole-blob `updateProject` merge** (which would reintroduce the stale-snapshot clobber race).
- `packages/core/design/manifest.ts` — `designManifestSchema` (deep-optional, defaults; unknown `kind` renders generic). Client: `putDesignManifest(projectId, body)`, `syncDesignManifest(projectId)`.

#### 3.4 i18n (4 locales)

`projects.json` → `design.{title, description, kind_tokens, kind_inventory, components_count, tokens, conventions, source_agent, source_manual, source_mixed, revision, updated, agent_label, agent_placeholder, figma_library_label, edit_json, invalid_json, saved_toast, generate_button, generate_fired_toast, sync_running, empty_state}`.

#### 3.5 Tests

- Go: context injector (unset → `""`, tokens render, inventory render, truncation, malformed settings → `""` not panic); capture (agent write bumps revision + preserves other settings keys — assert `qa_manifest` intact; **`EventProjectUpdated` published**; **manual-source → comment, no write**; human author ignored; malformed ignored + logged); key-scoped endpoint (partial bodies touch only provided keys; concurrent-write clobber test: agent `jsonb_set` between human read and human save survives; `EventProjectUpdated` published; non-member 403); sync endpoint (chore issue create/reuse, 409s); recipe content (legacy branch, Variables-API prohibition, 150-line cap).
- Vitest: manifest schema malformed suite; `project-design-section.test.tsx` (JSON gate, key-scoped save payload — asserts the dedicated endpoint is called, not `updateProject`; agent picker; generate flow).

**Rollout:** fire sync once for sd-main / sd-cs / sd-billing; hand-review in the editor. No flags.

---

### Phase 4 — Approval-driven deterministic decomposition (PR 4, `feat(design)`)

**User value:** Approve (with per-sub-issue trims) turns the proposal into real sub-issues — design context baked in, sprint inherited, dependencies ordered — and every implementing agent claims with the manifest + node-scoped Figma refs already in its prompt.

#### 4.1 `handler/design_decompose.go` — `decomposeApprovedProposal(ctx, h, issue, proposal, sourceCommentID, overrides, reviewerID)`

Called from the approve branch (detached ctx, best-effort, structured-logged); the manual-label entrance calls it with empty overrides.
- **Effective plan** = `proposal.sub_issues` filtered by `overrides` (exclude / title / description replacements). Excluded indices removed from siblings' `depends_on`.
- **Metadata — flat primitive keys only** (V1 contract, `issue_metadata.go:18-32`; nested objects are forbidden and would render as junk / risk the 8KB CHECK): `design_proposal_comment_id` (string UUID), `design_plan_index` (number), `design_depends_on` (comma-joined string, e.g. `"0,2"`). All rich context (Figma URLs + node-ids, component verdicts) lives in the description section only.
- **Transactional stamping:** `IssueCreateParams` (`service/issue.go:52-71`) gains an optional `Metadata map[string]any` (primitive scalars, validated) written **inside the existing create transaction** — a post-create `SetIssueMetadataKey` round-trip (the Bitrix pattern) would let a crash between create and stamp produce a child invisible to dedup and a duplicate on re-approve. This is the chosen mechanism; no `(parent, title)` fallback matching.
- Per entry, `IssueService.Create` with: `ParentIssueID = issue.ID`; project = parent's; title/description from the plan **plus a server-composed "Design context" section**: Figma URL(s) with node-ids for its screens, applicable component verdicts ("reuse `src/components/ListRow.vue` for NotificationRow; new: …"), and "Approved design proposal on parent <KEY>"; status `todo` when `depends_on` empty, else `backlog` (parking-lot rule); assignee = parent's squad if squad-assigned, parent's agent if agent-assigned, else unassigned; sprint inheritance automatic (commit e9cefe80); the three `design_*` metadata keys. (`IssueService.Create` already publishes the issue-created bus event — children need no extra publish.)
- **Idempotency/dedup (same proposal):** before creating, list children and skip any `design_plan_index` already present for this `design_proposal_comment_id`. Re-invoking approve **resumes** a partially failed run (fills gaps only); a fully decomposed issue returns 409 `already_decomposed` from the endpoint (UI shows the decomposed state instead of the button). No fire-time marker labels — a wedged run never permanently blocks retry.
- **Revised proposal after an earlier decomposition:** a designer re-run posts a new comment (new id), so `(comment_id, plan_index)` dedup alone would happily create a **second overlapping batch**. Rule: if any child exists whose `design_proposal_comment_id` differs from the proposal being approved, the endpoint returns **409 `previous_decomposition_exists`** with the prior batch's children listed. The review dialog surfaces the prior batch (links) and requires the human to explicitly re-approve with `supersede_previous: true` — which proceeds with the new batch and records both comment ids in the `design_decomposition_completed` activity, but never touches the old children (the human cancels obsolete ones through normal issue operations, guided by the dialog).
- Completion: system comment "Decomposed into N sub-issues from the approved design proposal"; activity `design_decomposition_completed`.
- Squad flow: children assigned to the squad fire the existing `shouldEnqueueSquadLeaderOnAssign` per child — the leader delegates each through the normal protocol. No bespoke decomposition briefing.

#### 4.2 Dependency promotion — `promoteDesignDependents` (self-contained; wired into the child-done path)

Three facts drive the design (verified against the repo):
- The backlog→todo enqueue logic lives **inline in the HTTP `UpdateIssue` handler** (`issue.go:2588-2599`) — a bare `Queries.UpdateIssueStatus` write produces a child that sits in `todo` forever: no agent task, no squad-leader trigger, no WS event.
- `notifyParentOfChildDone` early-returns when the parent's assignee is a **member** (`issue_child_done.go:72-74`) or the parent is done/cancelled (`:66-68`) — and Bitrix epics are typically human-assigned.
- Promotion completes a **human-approved** plan; gating it behind a dark env flag would deadlock `depends_on` children in `backlog` in the default configuration.

Therefore:
- `promoteDesignDependents(ctx, h, parent, doneChild)` is called from the child-done path **before** the parent-status and member-assignee early returns in `notifyParentOfChildDone` (`issue_child_done.go:51-126`) — it must run regardless of who the parent is assigned to.
- When the done child carries `design_plan_index`: collect done siblings' plan indices; for each `backlog` sibling whose `design_depends_on ⊆ done set`, per promoted child: (1) update status to `todo`; (2) replicate the `UpdateIssue` enqueue block — `h.TaskService.EnqueueTaskForIssue` for agent-assigned / `h.enqueueSquadLeaderTask` for squad-assigned, per the same readiness checks; (3) **publish `EventIssueUpdated`**. Best-effort, structured-logged, never double-fires (status guard).
- **Gating: none beyond the approval itself.** Promotion runs whenever children carry design metadata — it is the tail of a human-gated decomposition. `AGORA_AUTO_DESIGN_ENABLED` gates **only** Phase 6 auto-fire.
- **Child-done comment conflict:** the existing system comment (`issue_child_done.go:87`) literally instructs the parent agent/squad leader to promote waiting `backlog` sub-issues itself — with server-side promotion this means double promotion or premature promotion of unsatisfied dependents. When the done child carries `design_plan_index`, emit a **variant system comment**: *"Sub-issue <KEY> is done. Its dependent sub-issues are promoted automatically by the platform — do NOT change sibling statuses."* The same rule is stated in `agora-squads/SKILL.md` (§4.3).

#### 4.3 Context plumbing

- `handler/design_context.go` — `designContextForIssue(ctx, h, issue) string`: fast `""` when no figma metadata/refs AND no manifest; else composes Phase 1's Figma note + (when `design_proposal_comment_id` metadata present) the component-verdict directives + *"do not restyle shared components; QA will compare your result against these frames"* + parent-proposal pointer. Replaces the bare Phase-1 note at the claim-time call site and in the `draft_code`/`write_tests` assembly.
- Hand-created children under a design parent: ~15 lines in `service/issue.go` next to sprint inheritance — copy the parent's `figma_links` metadata stamp into the child **only when the child has none of its own**.
- Skill updates (same PR): `agora-squads/SKILL.md` (leaders receiving design-context children must preserve the Design context section, never implement on the parent, and **never promote design-decomposed backlog siblings — the platform does it**) and `agora-working-on-issues/SKILL.md` ("Implementing against a design": node-scoped reads, render download, reuse-per-manifest, deviation-raising, never commit renders to the repo) + both source maps.

#### 4.4 Frontend

- Review dialog: approve sends `sub_issue_overrides`; post-approval state shows "Decomposed into N sub-issues" with links (existing `childIssuesOptions`); backlog children badge "waits on #<n>". On 409 `previous_decomposition_exists`: prior-batch panel with child links + explicit "Supersede previous decomposition" confirm that re-sends with `supersede_previous: true` and reminds the human to cancel obsolete children.
- Children created from a proposal render a palette icon + tooltip "From design proposal" in issue lists/detail (reads `design_proposal_comment_id` metadata presence client-side, description-safe fallback).
- i18n (`issues.json`, 4 locales): `design_proposal.{decomposed_n, waits_on, from_proposal, already_decomposed, resume_hint, previous_decomposition_title, previous_decomposition_body, supersede_confirm, cancel_obsolete_hint}`.

#### 4.5 Tests

- Go: decomposition matrix (overrides include/exclude/title/description; statuses todo/backlog; assignee inheritance squad/agent/none; **flat primitive metadata keys + 8KB/50-key contract respected**; Design-context section content; sprint inherited; **metadata written inside the create tx** — crash-simulation leaves no unstamped child; resume fills gaps only; 409 `already_decomposed` when complete; **409 `previous_decomposition_exists` on a new proposal comment id + `supersede_previous` path creates the new batch and leaves old children untouched**; excluded index pruned from `depends_on`); promotion (**flips exactly-satisfied backlog siblings AND enqueues via `EnqueueTaskForIssue`/`enqueueSquadLeaderTask` AND publishes `EventIssueUpdated`**; fires even when the parent is member-assigned or done — i.e. runs before the `issue_child_done.go:66-74` guards; never double-fires; ignores non-design children; **no env flag consulted**); variant child-done comment emitted for design children; figma-stamp copy guard; workspace/cycle guards still hold (existing service checks).
- Vitest: dialog overrides payload; decomposed state; waits-on badges; supersede flow.

**Rollout:** the whole phase ships live — every path is human-gated by the approval itself (endpoint or explicit label attach). `AGORA_AUTO_DESIGN_ENABLED` is **not** used here; it is reserved for Phase 6 auto-fire. Supervised first run on one sd-billing epic.

---

### Phase 5 — Design-aware QA (PR 5, `feat(design)`)

**User value:** when a design-context issue hits `in_review`, the auto-fired `run_qa` also pulls the ground-truth Figma render, opens the implemented screen on the dev's own QA box, and reports a structured design verdict with a concrete mismatch table — side-by-side in the QA cockpit.

#### 5.1 Backend

- `sliceActionDesignCompareContext(ctx, h, issue) string` (`design_action.go`) — `""` unless the issue (or parent, one level) carries Figma refs AND the credential is valid. Appended to `run_qa` at **both** call sites: `CreateSliceAction` and `maybeRunQAOnInReview` (~:1167-1176, alongside smoke/manifest/docs/base-suite). **Recipe appendix sketch:**

> *"DESIGN VERIFICATION (this issue implements a Figma design): after functional checks — 1) Download the reference render(s) for the design node(s) (`download_figma_images`, pngScale=2). 2) Open the implemented screen in the embedded Chromium over CDP (or headless Chromium) at the smoke URL. 3) Compare DETERMINISTICALLY, not by pixels: from the Figma node tree and the DESIGN MANIFEST, assert in the live DOM — text content present, element inventory/order, key colors / font sizes / spacing via `getComputedStyle`. 4) Screenshot both sides and attach them. 5) Extend your `qa-result` JSON block with: `\"design\": {\"verdict\":\"pass\"|\"fail\"|\"skipped\", \"reference_node\":\"208:5147\", \"mismatches\":[{\"kind\":\"color\"|\"typography\"|\"spacing\"|\"layout\"|\"missing_element\"|\"other\",\"selector\":\"…\",\"expected\":\"…\",\"actual\":\"…\"}]}`. Sub-pixel and font-rendering differences are NOT mismatches; deviations the design proposal explicitly approved are NOT mismatches; design debt predating this task's diff is OUT of scope — note it, don't fail on it. The design verdict is advisory: apply `qa:fail` only when functional checks fail OR mismatches are severe (missing elements, wrong colors on primary surfaces). If Figma is unreachable (429 after one Retry-After honor, 403, expired credential), set `verdict:\"skipped\"` with the reason — never fail the issue for infra reasons."*

- Extend the existing qa-result capture (`CaptureQAEvidence`, `service/`) to tolerate + persist the optional `design` object alongside current fields (rides the existing `qa_evidence.result_json` — no migration).
- **Optional gate** — `AGORA_DESIGN_GATE_ENFORCED` (default off): in the `enforceQAGateBeforeDone` region (:754-778), an issue carrying `design_proposal_comment_id` metadata moving to done additionally requires the latest qa-result design verdict ∈ {pass, skipped} or a human `qa:pass` override. `skipped` never blocks.

#### 5.2 Frontend

- Extend the qa-result schema in `packages/core` with the optional `design` field (zod defaults; unknown `kind` → generic "other" row via `default` branch; missing `design` → section hidden; `mismatches: null` → empty table; unknown verdict → neutral badge).
- `packages/views/qa/components/qa-design-compare.tsx` — collapsible section in the `qa-review-page.tsx` rail (between checks and test cases, section pattern ~:287-306): reference vs implementation thumbnails side-by-side (attachments off the QA comment; click-to-zoom), verdict badge, mismatch table (kind badge, selector code, expected→actual). Same subcomponent reused inside `QAEvidenceSection` in issue-detail (no duplication). QA cockpit rows (`qa-page.tsx`): small design-verdict dot next to the qa status.

#### 5.3 Skill + i18n

- `agora-figma/SKILL.md`: design-QA section (render → CDP → deterministic checks → block format, skipped semantics); source map.
- `issues.json` (4 locales): `qa_review.{design_heading, design_pass, design_fail, design_skipped, design_reference, design_implementation, mismatch_kind_color, mismatch_kind_typography, mismatch_kind_spacing, mismatch_kind_layout, mismatch_kind_missing_element, mismatch_kind_other, mismatch_expected, mismatch_actual, design_no_data, design_skipped_reason}`.

#### 5.4 Tests

- Go: compare-context conditions (no refs → `""`; expired credential → `""`; parent walk; content mentions getComputedStyle, approved-deviation carve-out, 429 policy); presence in `maybeRunQAOnInReview` output; capture round-trip with/without design; gate matrix (flag off/on × verdicts × qa:pass override; `skipped` never blocks).
- Vitest: design-section malformed suite (the mandatory fail-closed test); compare component states; cockpit dot.

**Rollout:** rides `AGORA_AUTO_QA_ENABLED`; validate one real sd-billing screen vs its Figma node on `jamshid.sdteam.uz` before enabling the gate flag anywhere.

---

### Phase 6 — Epic autopilot, notifications, hardening, docs (PR 6, `feat(design)`)

**User value:** the loop closes — a Bitrix epic with TZ + Figma link produces a reviewable proposal within minutes of syncing, with nobody clicking anything; admins are warned before credentials die; the behavior is documented for humans and agents.

#### 6.1 Auto-fire — `maybeProposeDesignOnCreate(ctx, h, issue)` (`design_action.go`)

**Exactly one fire site per origin** (there is no "`IssueService.Create` post-hook" rail for handler-level logic; the rail is the bus):
- **Bitrix creates:** an explicit call at the **end** of the Bitrix create path (`bitrix_sync.go` ~:670+), *after* comment/attachment import completes **and on a re-loaded issue** — `metadata.bitrix_task_id` is stamped only after `IssueService.Create` returns (`bitrix_sync.go:696-704`), so any create-time hook would always see it missing and the heuristic would never match.
- **Non-Bitrix creates:** an `EventIssueCreated` bus subscriber in `cmd/server` (pattern: `activity_listeners.go:23`, `notification_listeners.go:548`) that first cheap-skips issues with no Figma refs and skips Bitrix-origin creates (Bitrix sync actor / bitrix metadata) — otherwise the generic path would race the explicit Bitrix call mid-import (dedup is only `HasPendingTaskForIssueAndAgent`).
- No auto-fire on description update — late-added links are covered by the manual `design_proposal` button.

**All guards must pass:** `AGORA_AUTO_DESIGN_ENABLED=true`; `project.settings.design_auto ∈ {"epics"(default),"all"} ≠ "off"`; issue has Figma refs; issue has **no parent**; issue creator is **not an agent** (loop suppression — decomposed children carry Figma links by design); no existing `design:*` state label and no existing proposal block (Bitrix ONTASKUPDATE re-syncs cannot re-fire); credential configured + unexpired; `resolveDesignerAgent` resolves via `design_agent` or design-squad leader only (**auto never falls through to arbitrary agents** — skip + log otherwise); for `"epics"`: pure `looksLikeDesignEpic(issue)` — label `type:epic` always wins; else `metadata.bitrix_task_id` present ∧ (≥2 distinct Figma refs ∨ description ≥600 chars). Dispatch reuses the Phase-2 shared instruction builder; detached ctx, best-effort, `HasPendingTaskForIssueAndAgent` dedup.

#### 6.2 Credential lifecycle — `server/cmd/server/figma_probe.go`

Nightly loop (pattern: `bitrix_poll.go` / `autopilot_scheduler.go`; advisory-lock guard for multi-instance): for each credential, `GET /v1/me`; update `probe_status/probed_at` via `UpdateFigmaCredentialProbe`; notify workspace admins via `inbox_item` (type `figma_credential_warning`, **`inbox:new` published per row**) on:
- transition to `invalid`/`expired` (state-transition dedup via `probe_status`);
- `expires_at < 14d` — **deduped via `expiry_notified_at`** (set on send via `SetFigmaCredentialExpiryNotified`; reset to NULL on token rotation in the upsert, §1.2), since "expiring soon" is not a `probe_status` transition and would otherwise spam daily for 14 days.

The Phase-1 settings badge and Phase-2 blocked-proposal banners consume the same status.

#### 6.3 Frontend

- `project-design-section.tsx`: `design_auto` select (off / epics / all) with helper text — saved through the key-scoped Phase-3 endpoint.
- Passive expiry banner in the design-proposal section when the latest proposal is `blocked` for credential reasons.
- Inbox: `figma_credential_warning` added to the `inbox-detail-label.tsx` type map (default branch already in place from Phase 2).
- i18n (`projects.json`): `design.{auto_label, auto_off, auto_epics, auto_all, auto_hint}`; (`issues.json`): `design_proposal.auto_fired_note`; (`settings.json`): `figma.probe_warning_inbox`; (`inbox.json`): `figma_credential_warning`.

#### 6.4 Docs & contracts

- `apps/docs` page "Design stage" (en + zh): setup (Dev/Full-seat PAT, scopes, 90-day renewal), manifest schema + authoring, lifecycle diagram, block contracts, env flags, label glossary (`design:*` entries added to conventions).
- Built-in skills final pass: `agora-figma` covers the full lifecycle; `agora-squads` / `agora-working-on-issues` / `agora-projects-and-resources` sections re-verified; all source maps traced to final line numbers.
- E2E: `e2e/design-lifecycle.spec.ts` (specs live flat in `e2e/`, next to `e2e/fixtures.ts` / `e2e/helpers.ts` — there is no `e2e/tests/` directory) — seed Figma-linked issue via `TestApiClient`, stub proposal comment + attachments, panel renders, approve with overrides → children exist with Design context + sprint; blocked-credential path renders remediation. (Agent execution stays out of E2E scope, consistent with QA e2e style.)

#### 6.5 Tests

Auto-fire guard matrix (flag off / project off / no refs / parent / agent creator / existing label / existing block / expired credential / no designer / re-sync no-refire / epics-vs-all heuristic table); **fire-site tests** (Bitrix path fires post-import with `bitrix_task_id` visible; bus subscriber skips Bitrix-origin and no-ref issues; no double-fire between the two sites); probe loop transitions + notification dedup (**expiring-soon fires once, resets on rotation**); `looksLikeDesignEpic` table test (≥10 rows); activity-row emission sweep; inbox type map renders `figma_credential_warning`.

#### 6.6 Rollout

Enable `design_auto=epics` on sd-billing first (Figma-heavy); observe one real Bitrix epic end-to-end (MUL-348-class); then sd-main/sd-cs.

---

## 5. Failure-mode matrix

| Failure | Behavior (encoded where) | Verified by |
|---|---|---|
| **Figma link private / 404** | Agent posts proposal with `status:"blocked"`, `reason: figma_forbidden/figma_not_found` (recipe + skill); capture notifies subscribers; panel shows reason-specific remediation ("share the file with the token's user / check Settings → Figma"); approve returns 409 | capture + panel + endpoint tests |
| **429 / quota exhaustion** | Recipes: honor `Retry-After` once, then `blocked` (proposal) / `skipped` (QA design verdict) with reason — never burn the run, never fail the issue; node-scoped reads + batched downloads keep budget small; save-time probe warns on View-seat tokens (`seat_probe='low_seat'`) | recipe content tests; probe tests |
| **Huge Figma file** | Whole-file fetch forbidden by recipe + skill (`nodeId` mandatory in claim context; `depth` discouraged per Framelink guidance); one structured read per frame | recipe/skill assertions |
| **Agent has no MCP config at all (the MUL-348 case)** | `injectFigmaMcpCreds` synthesizes the full `{"mcpServers":{"figma":…}}` document from scratch when refs exist — deliberately NOT mirroring the Lark empty-config short-circuit | explicit empty-config Go test |
| **Legacy project (no design system)** | Manifest `kind:"inventory"`: frequency-derived de-facto tokens, `legacy_notes` with copy-markup-from paths; QA design checks assert against manifest values, `skipped` when unmappable; lazy bootstrap self-provisions on first proposal | manifest recipe + injector tests |
| **Dead / wrong node-id in URL** | `get_figma_data` fails or `/images` returns null render → recipe: report the specific node as inaccessible in `blocked`/`skipped` detail, never guess content | skill + recipe wording tests |
| **Credential missing / expired** | Injection skipped + claim-time note tells the agent to say so; auto-fire guard skips; settings badge + inbox warning (nightly probe, expiring-soon deduped via `expiry_notified_at`); blocked proposals carry `credential_missing` | `figma_mcp_test.go`; probe tests |
| **Malformed agent block** | Go parser fails closed → activity `design_proposal_invalid`, **explicit error card + re-fire button** in UI, 409 from approve; manifest capture ignores + logs; qa design section hidden with functional QA untouched | parser + zod malformed suites |
| **Ephemeral Figma URLs (30d/14d)** | Never stored: renders re-uploaded as comment attachments (filename contract); UI renders via attachment rails only | recipe + gallery tests |
| **Agent-created issues with Figma links** | Auto-fire suppressed when creator is an agent → no proposal loops on decomposed children | guard matrix test |
| **Bitrix ONTASKUPDATE re-sync** | Dedup by existing `design:*` label / existing block → no re-fire; metadata re-stamp idempotent; auto-fire only after import completes, on a re-loaded issue | sync integration + fire-site tests |
| **Partial decomposition failure** | Idempotent per `(design_proposal_comment_id, design_plan_index)` flat metadata keys stamped **inside the create tx** — re-approve resumes, fills gaps only; no permanent marker; no crash window between create and stamp | decomposition resume + tx tests |
| **Revised proposal approved after an earlier decomposition** | Children with a different `design_proposal_comment_id` → 409 `previous_decomposition_exists`; explicit `supersede_previous: true` re-approve creates the new batch, old children untouched, both batches recorded in activity; dialog guides cancelling obsolete children | supersede tests |
| **Promoted child sits idle / promotion never fires** | Promotion helper runs before the `issue_child_done.go:66-74` guards (works for member-assigned Bitrix epics) and per child does status update + `EnqueueTaskForIssue`/`enqueueSquadLeaderTask` + `EventIssueUpdated` publish; no env flag — human-gated by approval | promotion tests (enqueue + publish + guard-order) |
| **Parent agent double-promotes siblings** | Design children get a variant child-done system comment ("promoted automatically — do not change sibling statuses") + the rule in `agora-squads/SKILL.md` | comment-variant + skill tests |
| **Metadata contract / 8KB CHECK** | All design metadata is flat primitive scalars (`figma_links` JSON-string, `design_*` keys); link stamping capped + skipped near the size limit; live extraction as fallback | stamping + decomposition metadata tests |
| **Label surgery (manual detach/attach)** | Server-enforced exclusivity on state labels (service core, publishes `EventIssueLabelsChanged`); manual `design:approved` attach enters the same idempotent core; contradictory states impossible | exclusivity + entrance tests |
| **Manifest write conflicts** | ALL writes key-scoped `jsonb_set` — agent capture AND the human editor (dedicated endpoint; never whole-blob `updateProject`); `source=="manual"` → agent proposes via comment, never overwrites | capture + endpoint clobber tests asserting `qa_manifest` intact |
| **Stale UI after server-side writes** | Every write publishes its WS event: label swaps → `EventIssueLabelsChanged`, promotion → `EventIssueUpdated`, manifest writes → `EventProjectUpdated`, inbox rows → `inbox:new` | per-phase publish assertions |

---

## 6. Security notes

- **Encryption at rest:** the Figma token is secretbox-sealed with `AGORA_FIGMA_SECRET_KEY` (same primitive and key-handling as `AGORA_GIT_SECRET_KEY` / `AGORA_LARK_SECRET_KEY`). Decrypted only (a) inside claim-time injection, (b) inside probe calls. No GET endpoint ever returns token material (`token_last4` only). Logs never print the token; injection logs carry workspace/agent ids only.
- **Probe input hygiene:** `probe_file_key` is validated `^[A-Za-z0-9]{10,}$` before URL construction — a crafted value cannot redirect the server-side probe (path traversal / query injection) to arbitrary api.figma.com endpoints with the workspace token.
- **Trust boundary of injection:** the plaintext key flows into `mcp_config` in the daemon claim payload — exactly the boundary Lark `app_secret` crosses today; no new exposure class. Operator-set env always wins (blank-fill merge), so a deliberately scoped per-agent token overrides the workspace one.
- **Prompt-injection surface (acknowledged residual risk):** any agent processing an issue whose description contains a Figma link gains read access to whatever the workspace token can read — same class as workspace git credentials attached to repos. Mitigations: admin-gated credential writes (member-level access is status-only); settings UI + docs prescribe **minimum scopes** (`file_content:read`, `file_metadata:read`; optional `file_comments:read`, `library_content:read`) and a dedicated Dev/Full-seat account; the skill instructs agents to read only issue-referenced files; injection is conditional on refs, not blanket. Per-project tokens are a named future extension.
- **Agent write paths are constrained:** agents cannot approve proposals or create sub-issues — children are created server-side only after an authenticated human approval (or an explicit human label attach); manifest auto-writes are blocked when a human owns the manifest (`source=="manual"`); all agent-sourced writes are strict-parsed and fail closed; all agent-sourced metadata is primitive-scalar by construction.
- **UUID hygiene:** all boundary UUIDs via `parseUUIDOrBadRequest`; issue resolution via `loadIssueForUser`; no raw string round-trips into writes (per the handler convention / issue #1661 lesson).
- 90-day PAT expiry is handled as a first-class lifecycle (probe + deduped notify), not an incident.

---

## 7. Rollout & flag strategy

**Env vars:** `AGORA_FIGMA_SECRET_KEY` (P1 — enables the subsystem), `AGORA_AUTO_DESIGN_ENABLED` (P6 — **auto-fire only**; default off), `AGORA_DESIGN_GATE_ENFORCED` (P5 — optional done-gate; default off, enable only after false-fail rate observed). **Per-project settings:** `design_agent` (P3), `design_auto` off|epics|all (P6). **No flag** for manual `design_proposal` / manifest / review endpoint / decomposition / dependency promotion — every one of those paths is human-initiated or human-gated (the approval) and ships live.

**Order:** (1) P1 → set secret on Fly + local, rebuild daemon image, configure the SalesDoctor workspace PAT, acceptance-test on MUL-348. (2) P2 → manual proposals on 2-3 real Bitrix epics; tune the recipe (recipes iterate without deploys). (3) P3 → sync manifests for sd-main/sd-cs/sd-billing, hand-review. (4) P4 → one supervised approve→decompose on sd-billing with the squad (decomposition + promotion live from merge, human-gated). (5) P5 → advisory design verdicts for one sprint on the per-dev QA boxes; consider the gate flag only after. (6) P6 → set `AGORA_AUTO_DESIGN_ENABLED=true` + `design_auto=epics` on sd-billing → observe one un-touched Bitrix epic end-to-end → expand. Prod deploys target `master` (sd-platform is integration); each phase is independently revertable (flags dark or endpoints simply unused).

---

## 8. Explicitly OUT of scope (v1)

| Item | Why deferred / re-open condition |
|---|---|
| Design cockpit (queue page) + design metrics view | Judged premature chrome at 2-10-person scale; the issue panel + review dialog + labels-as-filters carry the value. Re-open if proposal volume makes a queue necessary (then mirror `qa-page.tsx`). |
| Server-side Go Figma client, `figma_render` cache table, version-polling staleness chip | Agents fetch + persist renders per task; volume is low. Re-open on recurring 429s or when stale-design detection is demanded (design sketch: cache keyed `(file_key, node_id, file_version, scale, format)` through attachment storage). |
| Generic `workspace_credential(kind)` consolidation of git/lark/figma | Risky refactor of two working integrations mid-feature. Revisit as standalone chore. |
| Per-project Figma tokens / multi-credential workspaces | UNIQUE(workspace_id) suffices for target teams; extend the table with project scoping if a workspace spans Figma plans. |
| Structured (non-JSON) manifest editor | Validated-JSON textarea matches `mcp-config-tab` precedent; build a form editor if non-technical editing becomes real. |
| Figma webhooks; posting agent findings back into Figma comments | Webhooks need paid-plan + `webhooks:write`; comment write-back is nice-to-have. Poll-free v1. |
| Designer agent preset chip in create-agent UI | Persona text ships in the skill + docs instead; UI affordance unverified. |
| Bitrix parent-task / epic-flag import | Bitrix Task struct exposes neither today; Figma-link heuristic is source-agnostic. Additive later. |
| Pixel-diff visual QA | Deliberately rejected — deterministic structural checks are the platform doctrine. |
| Adding brand-new sub-issues inside the review dialog | Trim/edit only; humans create extra children through normal issue creation. |
| Server-side cancellation of superseded decomposition batches | Supersede records both batches and guides the human; bulk auto-cancel of old children is a destructive write we keep manual in v1. |
| Mobile surfaces | Mobile shares types/pure functions only (`packages/core/figma`, `packages/core/design` are import-type-safe by construction). |