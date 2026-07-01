# Zoho Suite Integration — Consolidated Plan

> Two research passes back this spec:
> - **§A Build spec** (unified Zoho ↔ Octane connector: Desk, CRM, Projects, Sprints) — below.
> - **§B Current-state audit** (existing Zoho Projects/Sprints code, file-anchored) — appendix at the end.
>
> Both agree the existing `zohoprojects_sync.go` was written to mirror `bitrix_sync.go`; Desk/CRM mirror the same skeleton a third time. **Do not invent a new framework.**

---

## §0. UI workstream — Settings → Integrations (required)

The connector must be **discoverable and operable from the app**, not env-only. Today `Settings → Integrations` (`packages/views/settings/components/integrations-tab.tsx`) hosts only `<LarkTab />` + `<BitrixTab />`; there is **no Zoho card** and **no `/[ws]/zoho` page**, even though the Projects/Sprints import endpoints already exist. Mirror the Bitrix UI exactly.

### U1 — `ZohoTab` card in Settings → Integrations
- **New:** `packages/views/settings/components/zoho-tab.tsx` — mirror `bitrix-tab.tsx`: heading "Zoho", a short description, connection status (from a `GET /api/zoho/status` — token present + which channels enabled), and a primary button that deep-links to the import page via `nav.push(paths.zoho())`.
- **Edit:** `integrations-tab.tsx` — render `<ZohoTab />` alongside `<BitrixTab />` under its own section heading (the file's comment already says new integrations "slot in without changing the IA").
- The card grows with the suite: initially shows Projects/Sprints channels; Desk/CRM rows appear as those channels ship (gated on the same enabled-flags the backend reads).

### U2 — Zoho import page `/[ws]/zoho`
- **New:** `packages/views/zoho/` — a `ZohoPage` mirroring the Bitrix import browser (`packages/views/bitrix/components/bitrix-sync-panel.tsx`): a **channel tab strip** (Projects · Sprints · Desk · CRM), each tab listing that channel's importable entities (projects / sprint-projects / Desk departments+tickets / CRM modules) with checkboxes and an **Import** action.
  - Projects/Sprints tabs reuse the existing endpoints (`/api/zoho-projects/{projects,import}`, `/api/zoho-sprints/{projects,import}`).
  - Desk/CRM tabs render "Coming soon" until P2/P3 land, then wire to their `list`/`import` endpoints — same pattern as Bitrix's "By group / By user" mode toggle.
- **New route (web):** `apps/web/app/[workspaceSlug]/(dashboard)/zoho/page.tsx` → `export { ZohoPage as default } from "@agora/views/zoho"` (mirror `.../bitrix/page.tsx`).
- **Desktop:** add the session route in the desktop router (mirror the Bitrix route).

### U3 — Paths + routing wiring
- **Edit:** `packages/core/paths/paths.ts` — add `zoho: () => \`${ws}/zoho\`` to `paths.workspace()` (mirror the just-added `qa`/`policy`).
- **Edit:** `packages/core/paths/consistency.test.ts` — add `"zoho"` to the shape set + `["zoho","zoho"]` to the segment map (the C4 contract test — it fails on drift).
- **Edit:** `packages/views/editor/utils/link-handler.ts` — add `"zoho"` to `WORKSPACE_ROUTE_SEGMENTS` so `/…/zoho` deep-links resolve.
- **Backend status endpoint:** `GET /api/zoho/status` (workspace-scoped) → `{ connected: bool, channels: {projects,sprints,desk,crm}: {enabled,lastSyncedAt} }`, so the card renders live state and follows the API-response-compat rules (schema + fallback).

### U4 — Files this workstream touches
```
NEW  packages/views/settings/components/zoho-tab.tsx
NEW  packages/views/zoho/…                (ZohoPage + channel panels)
NEW  apps/web/app/[workspaceSlug]/(dashboard)/zoho/page.tsx
NEW  server/internal/handler/zoho_status.go  (+ route GET /api/zoho/status)
EDIT packages/views/settings/components/integrations-tab.tsx
EDIT packages/core/paths/paths.ts  +  paths/consistency.test.ts
EDIT packages/views/editor/utils/link-handler.ts
EDIT apps/desktop … router (session route)
```

**Sequencing:** U1+U2 (Projects/Sprints channels) ship in **P1** — the entry point exists day one and grows as Desk/CRM (P2/P3) add their channel tabs. No card should link to a dead page: Desk/CRM tabs render "Coming soon" until their backend lands.

---


## §A. Build spec — unified Zoho suite connector

# Build Spec: Unified Zoho ↔ Octane Workspace Integration

## 0. What already exists (grounding — read before implementing)

The team does **not** start from zero. The Bitrix integration is the proven reference, and **two Zoho connectors already ship**:

| Concern | Reference file(s) | Status |
|---|---|---|
| Bitrix inbound webhook + poll + dedup + routing + provisioning | `server/internal/handler/bitrix_sync.go`, `bitrix_import.go` | Live, battle-tested |
| Bitrix group→workspace routing (`BITRIX_GROUP_MAP`) | `bitrix_sync.go:158-218` (`bitrixRouteConfig`) | Live |
| Bitrix assignee provisioning (department-gated) | `bitrix_sync.go:1033-1204` (`provisionBitrixAssignee`, `bitrixUserInTeam`) | Live |
| External-identity bridge | `server/internal/handler/external_identity.go` (`providerBitrix`, `linkExternalIdentity`, `userIDByExternalIdentity`) | Live |
| **Zoho Projects** DB-free client + mapping | `server/internal/integrations/zohoprojects/{client.go,mapping.go}` | Live |
| **Zoho Projects** sync handler (import, incremental cursor, outbound status mirror, poller) | `server/internal/handler/zohoprojects_sync.go` (1402 lines), `zohoprojects_endpoints.go` | Live but **poller currently effectively idle** — see §5 P1 |
| **Zoho Sprints** client + mapping + handler | `server/internal/integrations/zohosprints/*`, `zohosprints_sync.go`, `zohosprints_endpoints.go` | Live |
| Routes | `cmd/server/router.go:605-614` (`/api/zoho-projects/*`, `/api/zoho-sprints/*`) | Live |
| Poller + outbound wiring | `cmd/server/main.go:395` (`RunZohoSyncPoller`), `handler.go:234` (`registerZohoOutbound`) | Live |
| **Zoho Desk** | — | **Does not exist** |
| **Zoho CRM** | — | **Does not exist** |

**The single most important architectural finding:** `zohoprojects_sync.go` was written to *deliberately mirror* `bitrix_sync.go` line-for-line (its own comments say so — e.g. "Mirrors bitrixTaskIDMetaKey", "Mirrors getOrCreateBitrixProject exactly", "Mirrors bitrixResolveAssignee"). Desk and CRM must mirror **the same skeleton a third time**, not invent a new one.

---

## 1. Executive summary

**Vision.** One Zoho connector per workspace. A single OAuth grant (Zoho One, multi-app scopes) authorizes N app **channels** — Projects, Sprints, Desk, CRM (+ BugTracker as a cheap follow-on). Every channel pulls its native work items (Desk tickets, CRM Tasks/Cases, Projects tasks/bugs, Sprints work items) through **one shared "external work item → Agora issue" reconcile loop** and lands them as issues in the **Octane workspace**, routed to the right Agora **project** per app/department/module. Where the app supports it, Agora status changes flow back.

**The research is unanimous on the enabler:** a single authorization request with a comma-separated scope list spanning services yields **one refresh token** covering the whole granted set (Zoho unified-OAuth research, note "SINGLE REFRESH TOKEN, MULTI-APP: YES"). So the "whole Zoho suite" story costs one consent screen, not four integrations — *provided every scope is requested up front* (adding a scope later forces re-consent).

**Shortest viable path (what to actually build, in order):**

1. **P1 — Finish + enable Projects** as the reference channel: auto-discover all portal projects, full pagination (already present, `maxPageSize=200`), turn the poller on for Octane. This proves the "connected, self-healing, all-projects" model with zero new packages.
2. **P2 — Desk** (`zohodesk` package + `zohodesk_sync.go`), tickets→issues, **poll** on `modifiedTimeRange`. Highest-value new source; ticket→issue is ~1:1.
3. **P3 — CRM** (`zohocrm` package + `zohocrm_sync.go`), **Tasks + Cases only**, COQL poll on `Modified_Time`.
4. **P4 — Two-way status** for Desk + CRM (Projects/Sprints already have it).
5. **P5 — Webhooks** for realtime on Desk + CRM (poll stays as the reconciliation backstop).
6. **P6 — Sprints parity** (poller + outbound) + optional BugTracker (rides the Projects client).

Each phase is independently shippable and gated behind env config, so nothing half-built is reachable in prod.

---

## 2. Unified connector architecture

### 2.1 One "Zoho source" per workspace, with per-app channels

Model the connector as **one credential + a set of channels**. Each channel is an existing-or-new integration package that implements a tiny common interface. The credential (refresh token + DC + org identifiers) is shared; the channels differ only in *which remote client, which status map, which dedup key, and which routing knob*.

```
Zoho source (per workspace = Octane)
├── OAuth grant: 1 refresh token, DC-scoped   (§2.2)
├── channel: projects   → zohoprojects.Client  (exists)
├── channel: sprints    → zohosprints.Client   (exists)
├── channel: desk       → zohodesk.Client       (P2, new)
├── channel: crm        → zohocrm.Client         (P3, new)
└── channel: bugtracker → zohoprojects.Client    (P6, reuses Projects client)
```

**Do NOT invent a `ChannelManager` abstraction layer.** The existing code already establishes the pattern: each channel is a `*_sync.go` handler file + a DB-free client package + a `mapping.go`. Keep that. "Unification" here means **shared conventions and shared helpers**, not a new runtime framework — matching the CLAUDE.md rule "Prefer existing patterns/components over introducing parallel abstractions."

The one genuinely shared new piece worth extracting (P2, when the second poller appears) is a **provider registry for the sweep**: today `runZohoIncrementalSweep` (`zohoprojects_sync.go:1345`) hardcodes the `zoho_project:` marker. Generalize the sweep loop to iterate a slice of `{markerPrefix, syncFn}` so Desk/CRM pollers don't each re-implement the ticker+sweep lifecycle. That is the *only* new abstraction the spec authorizes, and it's a 30-line refactor of code that already exists.

### 2.2 OAuth: one grant, multi-app scopes, DC-aware (do this once, in P1)

**Decision: one OAuth client, one refresh token, union of all scopes, requested up front.** This is the research's explicit recommendation ("register ONE server-based OAuth client … with the union of all needed scopes; store one refresh token per (Zoho user/org, data center)").

Today each Zoho app reads **separate** env credentials:
- `ZOHO_PROJECTS_CLIENT_ID/SECRET/REFRESH_TOKEN` (`zohoprojects_sync.go:109-111`)
- `ZOHO_SPRINTS_CLIENT_ID/SECRET/REFRESH_TOKEN` (`zohosprints_sync.go:50-53`)

**Target:** a shared `ZOHO_CLIENT_ID / ZOHO_CLIENT_SECRET / ZOHO_REFRESH_TOKEN / ZOHO_DC` block, with the per-app vars kept as **optional overrides** (a channel that has its own token uses it; otherwise falls back to the shared one). This is an internal, non-boundary change, so per CLAUDE.md we *replace* rather than dual-write — but keep the per-app override because a customer mid-migration may still hold app-specific tokens. Concretely:

```
ZOHO_CLIENT_ID           # shared OAuth client (all apps)
ZOHO_CLIENT_SECRET
ZOHO_REFRESH_TOKEN       # one grant, scopes below
ZOHO_DC                  # us|eu|in|au|jp|ca|sa|uk  (LOAD-BEARING — see below)
# optional per-app override, falls back to shared:
ZOHO_DESK_REFRESH_TOKEN, ZOHO_CRM_REFRESH_TOKEN, ...
```

**Scope superset to request at consent (from the research):**
```
ZohoProjects.projects.ALL,ZohoProjects.tasks.ALL,ZohoProjects.bugs.ALL,ZohoProjects.portals.READ,ZohoProjects.users.READ,
ZohoSprints.projects.ALL,ZohoSprints.items.ALL,ZohoSprints.teams.READ,
Desk.tickets.ALL,Desk.contacts.READ,Desk.settings.READ,Desk.search.READ,Desk.basic.READ,
ZohoCRM.modules.tasks.ALL,ZohoCRM.modules.cases.ALL,ZohoCRM.modules.deals.READ,ZohoCRM.coql.READ,ZohoCRM.settings.READ,ZohoCRM.users.READ,ZohoCRM.notifications.ALL,
AaaServer.profile.READ
```
Request with `access_type=offline`. Use `.ALL` (not `.READ`) for the apps we intend two-way sync on (Desk tickets, CRM Tasks/Cases, Projects tasks/bugs); `.READ` where we only read (Deals, contacts, settings).

**DC is load-bearing — this is the #1 footgun in all four research entries.** The existing clients already parametrize hosts (`zohoprojects/client.go:15-20`, `DefaultAccountsHost`, `DefaultAPIHost`, both env-overridable). Extend that: derive **all** app hosts from a single `ZOHO_DC` value via a lookup table, instead of six independent host envs. Persist the DC per connection. Never hardcode `.com`. Map:

| DC | accounts | CRM api-domain | Desk host | Projects host | Sprints host |
|---|---|---|---|---|---|
| us | accounts.zoho.com | www.zohoapis.com | desk.zoho.com | projectsapi.zoho.com | sprintsapi.zoho.com |
| eu | accounts.zoho.eu | www.zohoapis.eu | desk.zoho.eu | projectsapi.zoho.eu | sprintsapi.zoho.eu |
| in | accounts.zoho.in | www.zohoapis.in | desk.zoho.in | projectsapi.zoho.in | sprintsapi.zoho.in |
| au/jp/ca/sa/uk | …zoho.com.au / .jp / zohocloud.ca / .sa / .uk | analogous | analogous | analogous | analogous |

For Octane specifically the DC is fixed and known — but the table is what makes the connector *reusable* for future non-Octane Zoho customers, which is the stated vision.

**Token minting caps to respect (research):** 10 access tokens / 10 min per client; ≤20 refresh tokens per user; ≤5 refreshes/min. The existing clients already cache the access token in-process and refresh at ~55m (`zohoprojects/client.go:56`, `accessTokenTTL`). New channels **must** reuse that cache discipline — never refresh per request. Ideally the shared credential means one cached access token is reused across channels of the same DC.

### 2.3 Common inbound pipeline (poll + optional webhook → shared mapper)

Every channel follows the identical reconcile loop already implemented in `reconcileZohoTask` (`zohoprojects_sync.go:420-614`). Copy this skeleton per app:

1. **Advisory lock** per `(workspace, provider, externalID)` — `SELECT pg_advisory_xact_lock(hashtext($1))` with key `"<wsID>:<provider>:<externalID>"`. Projects uses `":zoho:"` (`zohoprojects_sync.go:433`); Desk uses `":zohodesk:"`, CRM `":zohocrm:"`, so numeric-id collisions across apps are impossible. Serializes concurrent syncs of the same item.
2. **Dedup lookup** by JSONB containment: `WHERE workspace_id=$1 AND metadata @> $2::jsonb` with `{"<provider>_<entity>_id": "<id>"}`. Reuses the GIN-indexed `metadata @>` filter (`findIssueByZohoTaskID`, `zohoprojects_sync.go:993`).
3. **Found → RAW bus-free reconcile.** Update status/assignee/metadata with raw `UPDATE issue SET …` that does **not** publish `EventIssueUpdated` (`zohoprojects_sync.go:473-535`). This is the echo-break for two-way sync.
4. **Not found → `IssueService.Create` with `AllowDuplicate: true`**, then `SetIssueMetadataKey` to stamp the external id. `AllowDuplicate: true` is mandatory — otherwise title-normalization dedup silently returns an unrelated issue (research gotcha; enforced at `zohoprojects_sync.go:565`).
5. **Comments** imported once (create path), throttle-breaker on Zoho rate limit (`commentsThrottled`, `zohoprojects_sync.go:824`).

**Inbound method per app** (research-driven):
- **Projects, Sprints:** poll only (webhook coverage is thin). Modified-since cursor already implemented (`loadZohoCursor`/`saveZohoCursor`, `zohoprojects_sync.go:1083-1122`).
- **Desk:** poll on `modifiedTimeRange` (P2), add webhooks later (P5). Webhooks are UI-configured, best-effort, capped per plan — **always back them with the reconciliation poll** (research: "no documented delivery guarantee").
- **CRM:** poll via COQL on `Modified_Time` (P3), add the Notifications/watch API later (P5). **Channels expire ≤1 week — you own a renewal cron** (research). Poll is the safety net.

The **shared mapper** is not one function but one *contract*: each `mapping.go` exposes `MapXToIssue(...) IssueDraft` (title/description/status) + `MapStatus(name, type) string`. Already the shape in `zohoprojects/mapping.go:151-178`. Desk/CRM add their own `mapping.go` with the same signatures.

### 2.4 Provenance + dedup in issue metadata (mirror Bitrix exactly)

Metadata is a small KV bucket: `NOT NULL DEFAULT '{}'`, `jsonb_typeof='object'`, `pg_column_size ≤ 8192`, GIN(`jsonb_path_ops`) for `@>` only, **primitive values only** (string/number/bool — no nested objects/arrays per key). Do **not** dump raw Zoho payloads. Write discrete keys via `SetIssueMetadataKey` (`issue.sql:299`), one call per key. See §4 for the exact key list.

Dedup key convention: `<provider>_<entity>_id` as a JSON string, mirroring `bitrix_task_id` / `zoho_task_id`. One issue per external id per workspace (the `@>` filter + `LIMIT 1` assumes this).

### 2.5 Per-app → Agora project routing (mirror BITRIX_GROUP_MAP)

Two routing styles already exist; **pick per channel**:

- **Marker-in-description** (Projects/Sprints today): the Agora project carries `zoho_project:<id>` in its description (`getOrCreateZohoProject`, `zohoprojects_sync.go:622`); dedup is a `description LIKE '%zoho_project:<id>%'`. A Zoho project → one Agora project, auto-created on first sight. **This is the right default for Projects/Sprints/BugTracker** (each remote project maps naturally to one Agora project).

- **Config map → workspace/project** (Bitrix's `BITRIX_GROUP_MAP` = `"123:sd-main,456:sd-cs"`, `bitrix_sync.go:169-207`): needed for **Desk and CRM**, where the structural unit (Desk *department*, CRM *module*) doesn't map 1:1 to an Agora project and the operator must decide the fan-out. Add:
  - `ZOHO_DESK_DEPARTMENT_MAP = "<deptId>:<projectMarkerOrSlug>,…"` — department → Agora project.
  - `ZOHO_CRM_MODULE_MAP = "Tasks:<project>,Cases:<project>"` — module → Agora project.
  - Both with a **default catch-all project** for unmapped departments/modules, exactly as `bitrixRouteConfig` unions the default slug (`bitrix_sync.go:200`).

Since Octane is **one workspace**, routing resolves to a **project** inside Octane (not a workspace slug like Bitrix). So reuse the Bitrix *map-parsing shape* (`strings.Cut` on `:`, drop malformed pairs — `bitrix_sync.go:178-185`) but target a project marker. For unmapped values, use the `getOrCreateZohoXProject`-style auto-create with a per-app default project ("Zoho Desk", "Zoho CRM").

### 2.6 Assignee provisioning (reuse the department-gated pattern)

Reuse `provisionBitrixAssignee` structurally (`bitrix_sync.go:1147-1204`). Resolution order per research (agora-issue-model, assignee provisioning note):
1. **External-identity link** → member. Add providers to `external_identity.go`: `providerZohoDesk = "zoho_desk"`, `providerZohoCRM = "zoho_crm"` (Projects/Sprints currently resolve by email only — `zohoResolveAssignee`, `zohoprojects_sync.go:894` — but should gain `providerZohoProjects` in P4 so round-trips dedup cleanly).
2. **Email match** → member (`GetUserByEmail`, then `assigneeIfMember`, `bitrix_sync.go:1206`).
3. **Gated provision** → if the workspace opts in AND the person passes a team/department filter, create shadow user (`CreateUser`) + `CreateMember(role="member")`, then `linkExternalIdentity`. Requires an email; no email → unassigned but name recorded in metadata.

All best-effort: any failure degrades to unassigned, never blocks the import. Assignee is polymorphic (`member|agent|squad`) but a Zoho assignee is **always `type=member`**. Creator on imports is the **workspace owner** (`zohoWorkspaceOwner`, `zohoprojects_sync.go:976`), because the integration is a system actor with no member row.

The department gate reuses `BITRIX_TEAM_DEPARTMENTS` semantics (`bitrix_sync.go:113`): add `ZOHO_DESK_TEAMS` / `ZOHO_CRM_PROVISION_DOMAINS` (e.g. only provision `@salesdoc.io` owners) so a random external Desk contact never becomes a provisioned member. **Desk contacts are external customers → NEVER a member; model as reporter metadata only** (research, Desk mapsToAgora).

---

## 3. Per-app mapping tables

### 3.1 Zoho Desk — **Ticket → Agora issue**

| Agora field | Zoho Desk source | Notes |
|---|---|---|
| `title` | ticket `subject` | |
| `description` | ticket `description` / first inbound thread | rich email HTML degrades to text |
| `status` | `statusType` (Open\|OnHold\|Closed) as spine **+** per-workspace status-name map | statuses are admin-configurable free text — map via `statusType` first, default to `todo`/`in_progress`/`done`; **enum-drift downgrade, never crash** |
| `priority` | `priority` (High/Medium/Low/blank) | normalize → urgent/high/medium/low/none |
| `assignee` | `assigneeId` → Desk agent → member (email map) | unassigned ticket = null `assigneeId` → leave unassigned |
| reporter | `contact` (name/email/phone) | **metadata only** — external customer, not a member |
| comments | ticket `comments` (`isPublic=false` internal notes) | → issue comments; provenance header `**Zoho Desk — <author> (<date>)**:` |
| threads | customer↔agent conversation | decide separately whether to import public threads (P2: skip; P5: optional) |
| link-back | `desk.zoho.<dc>/agent/{portal}/{dept}/tickets/details/{id}` + `id` | metadata `zoho_desk_ticket_url`, dedup key `zoho_desk_ticket_id` |
| routing | `departmentId` → Agora project via `ZOHO_DESK_DEPARTMENT_MAP` | **use immutable ticket `id`, not `ticketNumber`** (per-department, not unique) |

- **Mandatory `orgId` header** on every call except `/organizations` (research). Store per connection.
- **Inbound:** poll `GET /tickets/search?modifiedTimeRange=…&departmentIds=…` (P2). `Desk.search.READ` scope. Pagination `from`+`limit` (max 50 tickets; deep offsets are credit-expensive — narrow the `modifiedTimeRange`).
- **Two-way status (P4):** `PATCH /tickets/{id}` (`Desk.tickets.UPDATE`), map Agora status → org's configured status name; guard the webhook echo loop with the RAW-write split.

### 3.2 Zoho CRM — **Task / Case → Agora issue** (Deals = context only)

| Agora field | Task | Case |
|---|---|---|
| `title` | `Subject` | `Subject` |
| `status` | `Status` (Not Started/In Progress/Completed/Deferred/Waiting) | `Status` (Open/On Hold/Escalated/Closed) |
| `priority` | `Priority` | `Priority` |
| `assignee` | `Owner` (Zoho user id+email) → member | `Owner` |
| due date | `Due_Date` | — (Cases have none) |
| description/comments | `Description` + Notes related list (`/{module}/{id}/Notes`) | same |
| link-back | `crm.zoho.<dc>/crm/tab/{Module}/{id}` | same |
| dedup key | `zoho_crm_task_id` | `zoho_crm_case_id` |
| parent context | `What_Id/Who_Id` (Deal/Account/Lead/Contact) → metadata | `Account_Name`/`Contact_Name` → metadata |

- **Only Tasks + Cases are issues.** Deals ride along as **linked context** in metadata (`zoho_crm_deal_name`, `zoho_crm_deal_id`) — never cloned as issues. Leads: sync the *Task* attached to them, reference the Lead. (research: "cloning a whole sales pipeline into an issue tracker creates noise.")
- Status is a per-org picklist (custom values allowed) → per-workspace map + default branch.
- **Inbound:** COQL poll (P3): `POST /crm/v8/coql` with `SELECT … FROM Tasks WHERE Modified_Time > '<cursor>' ORDER BY Modified_Time DESC LIMIT offset,limit`. `ZohoCRM.coql.READ` scope. Cheaper than per-record GETs; page via `page_token` beyond 2,000 rows.
- **Two-way status (P4):** `PUT /crm/v8/{module}/{id}` (`ZohoCRM.modules.{module}.ALL`); tag Agora-originated writes to ignore the resulting notification.
- **Routing:** `ZOHO_CRM_MODULE_MAP` (Tasks→projectA, Cases→projectB) or default project.

### 3.3 Zoho Projects — **Task/Bug → Agora issue** (mostly done)

Already implemented (`zohoprojects_sync.go` + `mapping.go`). Field map: `name→title`, `description→description`, status via `MapStatusWithType(name,type)` (`mapping.go:39-74`), owner→assignee by email, comments→comments, `zoho_task_id` dedup, `zoho_project:<id>` project marker, sprint-named tasklists→Agora sprints. **Two-way status already built** (`mirrorIssueStatusToZoho`, `ResolveCustomStatusID`). **BugTracker (P6)** rides this same client (shares the Projects API — `/bugs/` endpoints) with a new `zoho_bug_id` key.

### 3.4 Zoho Sprints — **Work Item → Agora issue** (built, needs parity)

Implemented (`zohosprints_sync.go`, `reconcileZohoSprintsItem:238`). Item name→title, status via Sprints map, assignee by email, `zoho_sprints_item_id` dedup, sprints→Agora sprints. **Missing vs Projects:** no incremental poller, no outbound status mirror. P6 brings it to Projects parity.

---

## 4. Data model + config

### 4.1 Metadata keys per app (all primitive string/number/bool)

| App | Dedup key | Provenance keys |
|---|---|---|
| Projects | `zoho_task_id` | `zoho_project_id`, `zoho_owner_id/name/email`, `zoho_comments_imported` |
| Sprints | `zoho_sprints_item_id` | `zoho_sprints_project_id`, `zoho_sprints_owner_*` |
| Desk | `zoho_desk_ticket_id` | `zoho_desk_org_id`, `zoho_desk_department_id`, `zoho_desk_ticket_number`, `zoho_desk_ticket_url`, `zoho_desk_status_name`, `zoho_desk_contact_name/email`, `zoho_desk_channel`, `zoho_desk_agent_email`, `zoho_desk_synced_comment_ids` (array — the one allowed array key, under 8KB, mirrors `bitrix_synced_comment_ids`) |
| CRM | `zoho_crm_task_id` / `zoho_crm_case_id` | `zoho_crm_module`, `zoho_crm_record_url`, `zoho_crm_status_name`, `zoho_crm_owner_email`, `zoho_crm_deal_id/name` (linked context) |
| BugTracker | `zoho_bug_id` | `zoho_project_id`, `zoho_bug_url` |

Keep each issue's key count modest — the 8KB / primitive-only / key-regex handler rules apply.

### 4.2 Config knobs (mirror the Bitrix env + settings model)

**Shared (P1):** `ZOHO_CLIENT_ID`, `ZOHO_CLIENT_SECRET`, `ZOHO_REFRESH_TOKEN`, `ZOHO_DC`.
**Existing (keep as override):** `ZOHO_PROJECTS_*`, `ZOHO_SPRINTS_*`, `ZOHO_PROJECTS_SYNC_INTERVAL` (default 15m, `0` disables — `zohoprojects_sync.go:105`), `ZOHO_PROJECTS_PUSH_STATUS`.
**Desk (P2/P4/P5):** `ZOHO_DESK_ORG_ID`, `ZOHO_DESK_DEPARTMENT_MAP`, `ZOHO_DESK_DEFAULT_PROJECT`, `ZOHO_DESK_SYNC_INTERVAL`, `ZOHO_DESK_PUSH_STATUS`, `ZOHO_DESK_WEBHOOK_SECRET`, `ZOHO_DESK_TEAMS` (provision gate).
**CRM (P3/P4/P5):** `ZOHO_CRM_MODULES` (e.g. `Tasks,Cases`), `ZOHO_CRM_MODULE_MAP`, `ZOHO_CRM_DEFAULT_PROJECT`, `ZOHO_CRM_SYNC_INTERVAL`, `ZOHO_CRM_PUSH_STATUS`, `ZOHO_CRM_PROVISION_DOMAINS`, `ZOHO_CRM_NOTIFY_URL` (webhook), `ZOHO_CRM_CHANNEL_RENEW` (cron for the ≤1-week expiry).

**Per-project settings jsonb** (already used): `zoho_synced_at` cursor, `zoho_owner_filter` — merged via `settings || jsonb_build_object(...)` so toggles survive (`saveZohoCursor`, `zohoprojects_sync.go:1109`). Desk/CRM cursors live the same way on their default/routed Agora project.

### 4.3 Token/DC storage

Per connection, persist: `refresh_token`, `dc`, and per-app org identifiers (**Desk `orgId`**, **Projects `portalId`**, **Sprints `teamId`**, CRM is token-implicit). For Octane these are single-valued env; the reusable form is a `zoho_connection` row keyed by workspace. **All four org identifiers differ and are all required** (research). A user can belong to multiple orgs/portals/teams — the connector enumerates and the operator picks (`ListPortals` already does this for Projects, `zohoprojects_sync.go:242`).

---

## 5. Phased plan

Legend per phase: **Files** · **Risk** · **Verify**.

### P1 — Finish + enable Projects (all projects, paginated, auto-discover) — *no new packages*
- **Do:** (a) call `POST /api/zoho-projects/import {all:true}` for Octane to pull every portal project; (b) confirm the poller is actually running — `RunZohoSyncPoller` is wired (`main.go:395`) but only sweeps projects already carrying the `zoho_project:` marker (`runZohoIncrementalSweep:1345`), so a project must be imported once before the poller keeps it fresh; (c) verify `ZOHO_PROJECTS_SYNC_INTERVAL` is a positive duration in the Octane env (not `0`); (d) introduce the shared `ZOHO_CLIENT_ID/SECRET/REFRESH_TOKEN/ZOHO_DC` block with per-app fallback (§2.2) and the DC host table.
- **Files:** `zohoprojects_sync.go` (env fallback + DC table), `zohoprojects/client.go` (host derivation from DC), `.env`/deploy config.
- **Risk:** low. Full pagination already bounded (`maxPageSize=200`, `maxPages=200`). Credit-based rate limits — keep the 15m interval; incremental cursor already trims to deltas.
- **Verify:** `cd server && go test ./internal/integrations/zohoprojects/ ./internal/handler/ -run Zoho`; import in Octane, confirm all projects appear as Agora projects and the poller log line "zoho sync poller started" fires.

### P2 — Desk (tickets → issues, poll)
- **Do:** new `server/internal/integrations/zohodesk/{client.go,mapping.go}` (copy `zohoprojects` structure: OAuth via shared token, `orgId` header, `ListTickets(modifiedTimeRange, departmentIds)`, `GetTicketComments`, `GetAgents`, `GetDepartments`, throttle detection). New `server/internal/handler/zohodesk_sync.go` mirroring `zohoprojects_sync.go`: `reconcileZohoDeskTicket`, advisory lock `":zohodesk:"`, `findIssueByZohoDeskTicketID`, department→project routing (`ZOHO_DESK_DEPARTMENT_MAP` parsed like `bitrixRouteConfig`), contact→reporter metadata, agent→assignee. New `zohodesk_endpoints.go` (`/api/zoho-desk/import`, `/departments`). Generalize the sweep into a provider registry (§2.1) and register the Desk poller in `main.go`. Add `providerZohoDesk` to `external_identity.go`.
- **Files:** `internal/integrations/zohodesk/*` (new), `internal/handler/zohodesk_sync.go` + `_endpoints.go` (new), `internal/handler/external_identity.go`, `cmd/server/router.go`, `cmd/server/main.go`, `internal/handler/handler.go`.
- **Risk:** medium. Status is free-text per org (map via `statusType` spine + default). Credit pool is shared with the customer's other Zoho automations — narrow `modifiedTimeRange`, respect throttle breaker. Multi-department fan-out is the main design call (§6).
- **Verify:** `go test ./internal/integrations/zohodesk/` with `httptest` mock (mirror `zohoprojects_test.go`), plus a handler test feeding a **malformed ticket** (missing status, null assignee, unknown `statusType`) to prove enum-drift downgrades not crashes. Import against a real Desk sandbox; confirm tickets land in the routed project with dedup on re-run.

### P3 — CRM (Tasks + Cases)
- **Do:** new `zohocrm` package (COQL client: `RunCOQL(query)`, `GetRecord(module,id)`, `ListNotes`, `ListUsers`; `page_token` pagination). New `zohocrm_sync.go`: `reconcileZohoCRMRecord` per module, lock `":zohocrm:"`, dedup `zoho_crm_task_id`/`zoho_crm_case_id`, module→project routing, Owner→assignee, Notes→comments, Deal linked-context metadata. `zohocrm_endpoints.go`. Register CRM poller.
- **Files:** `internal/integrations/zohocrm/*`, `internal/handler/zohocrm_sync.go` + `_endpoints.go`, `router.go`, `main.go`, `external_identity.go` (`providerZohoCRM`).
- **Risk:** medium. Field/status API names are per-org (custom picklists) — discover via `ZohoCRM.settings.READ` or map with default branch. Confirm whether the customer's Cases live in CRM or Desk before wiring CRM Cases (§6).
- **Verify:** `go test ./internal/integrations/zohocrm/` (mock COQL responses incl. malformed/`null` arrays); confirm Tasks+Cases import, Deals appear only as metadata context, dedup holds.

### P4 — Two-way status (Desk + CRM)
- **Do:** add `registerZohoDeskOutbound` / `registerZohoCRMOutbound` mirroring `registerZohoOutbound` (`zohoprojects_sync.go:1187`): subscribe to `EventIssueUpdated`, gate on `zoho*ShouldMirror` (status_changed==true), re-read issue, map Agora status → app status, `PATCH /tickets/{id}` / `PUT /crm/{module}/{id}`. Off unless `ZOHO_DESK_PUSH_STATUS` / `ZOHO_CRM_PUSH_STATUS`. Echo-safety is already structural (inbound uses RAW bus-free writes).
- **Files:** the two `*_sync.go` files, `handler.go:234` (register alongside `registerZohoOutbound`).
- **Risk:** medium-high. Desk/CRM enforce configured status names / blueprint transitions that may reject arbitrary jumps — resolve the target status from the org's real status list (like `ResolveCustomStatusID`, `zohoprojects/mapping.go:120`) and skip the push when no mapping exists rather than guessing.
- **Verify:** unit-test the mirror core (issue not linked → no-op; linked → correct app call); manual round-trip: change status in Agora, confirm it lands in Desk/CRM and does **not** bounce back as a duplicate inbound.

### P5 — Webhooks for realtime (Desk + CRM)
- **Do:** Desk — public `POST /zoho-desk/webhook` with shared-secret gate (copy `BitrixWebhook` always-200 + constant-time compare + IP rate-limit, `bitrix_sync.go:230-269`); UI-configured Ticket Add/Update with `departmentId` filter. CRM — `POST /crm/v8/actions/watch` channel registration + `POST /zoho-crm/webhook` callback; **renewal cron** for the ≤1-week `channel_expiry`. Both: on event, fetch the record by id and run the *same* `reconcile*` used by the poll. **Keep the polls as the reconciliation backstop** (webhooks are best-effort, capped per plan).
- **Files:** the two `*_sync.go` + `_endpoints.go`, `router.go` (public webhook routes with rate-limit middleware), a renewal ticker in `main.go`.
- **Risk:** high. No delivery guarantee; per-plan active-webhook cap (Desk Prof ~5 / Ent ~10); channel expiry silently stops CRM inbound. Mitigated entirely by the poll safety net — **never rely on webhooks alone**.
- **Verify:** post a synthetic webhook payload (mirror `bitrix_sync_test.go`), confirm 200 + reconcile; simulate a dropped event and confirm the next poll sweep catches it; confirm CRM channel auto-renews before expiry.

### P6 — Sprints parity + BugTracker
- **Do:** add incremental cursor + outbound mirror to `zohosprints_sync.go` (copy from Projects). Add BugTracker as a `zoho_bug_id` reconcile path on the existing `zohoprojects.Client` (`/bugs/` endpoints) — cheap follow-on, no new OAuth family.
- **Files:** `zohosprints_sync.go`, `zohoprojects/client.go` (+`ListBugs`), a small `zohobug` reconcile in the projects handler.
- **Risk:** low. Same patterns, proven twice.
- **Verify:** `go test ./internal/integrations/zohosprints/`; confirm Sprints incremental sweep + status push-back; BugTracker bugs import as issues.

**Final gate every phase:** `make check` (typecheck, unit, Go tests, E2E) per CLAUDE.md's AI Agent Verification Loop.

---

## 6. Open decisions for the user

1. **CRM: which modules count as issues?** Recommended: **Tasks + Cases only**; Deals/Leads as linked context, not issues. Confirm — and confirm **where the customer's support tickets actually live** (Desk vs CRM Cases): if they run Desk, do **not** also sync CRM Cases or you double-import the same support work.
2. **Desk: departments → projects.** All departments → one "Zoho Desk" Agora project, or per-department → project (`ZOHO_DESK_DEPARTMENT_MAP`)? Which departments to sync at all (the `departmentIds` filter keeps volume + credit cost down)?
3. **Poll interval per channel.** Default 15m (Projects today). Desk/CRM are credit-metered on a shared 24h org pool — a tight interval can starve the customer's other Zoho automations. Confirm an acceptable cadence (suggest 15m Projects/Sprints, 10–15m Desk, 15–30m CRM COQL).
4. **Realtime vs poll.** Ship poll-only first (P1–P3) and add webhooks (P5) only if latency matters. Given Zoho's no-guarantee webhooks + per-plan caps + CRM channel expiry, poll-only may be *sufficient* for Octane — confirm whether near-real-time is a hard requirement before investing in P5.
5. **One workspace vs per-app workspaces.** Spec assumes **one Octane workspace, routing to per-app projects inside it** (matches the request). Confirm — the alternative (Desk→its own workspace, CRM→another) is supported by the Bitrix `GroupMap→slug` model but contradicts the "Octane workspace connected to the whole suite" framing.
6. **Assignee provisioning scope.** Should Desk agents / CRM owners outside `@salesdoc.io` ever be auto-provisioned as members? Recommended: **no** — gate provisioning to known domains/teams (`ZOHO_*_PROVISION_DOMAINS` / `ZOHO_DESK_TEAMS`), record everyone else as metadata only. Desk **contacts** (external customers) are never members.
7. **Shared vs per-app OAuth token.** Confirm the team can create **one** Zoho OAuth client with the full scope superset (§2.2). If org policy forces per-app clients, the per-app env fallback still works but you lose the "one consent" story and must renew N refresh tokens.

---

### Key files the implementer will touch (all absolute)
- Reference to copy: `/Users/jamshid/Projects/agora/server/internal/handler/bitrix_sync.go`, `bitrix_import.go`
- Reference already-mirrored: `/Users/jamshid/Projects/agora/server/internal/handler/zohoprojects_sync.go`, `zohoprojects_endpoints.go`, `/Users/jamshid/Projects/agora/server/internal/integrations/zohoprojects/{client.go,mapping.go}`
- Sprints: `/Users/jamshid/Projects/agora/server/internal/handler/zohosprints_sync.go`, `/Users/jamshid/Projects/agora/server/internal/integrations/zohosprints/`
- Identity bridge: `/Users/jamshid/Projects/agora/server/internal/handler/external_identity.go`
- Wiring: `/Users/jamshid/Projects/agora/server/cmd/server/router.go` (605-614), `/Users/jamshid/Projects/agora/server/cmd/server/main.go` (395), `/Users/jamshid/Projects/agora/server/internal/handler/handler.go` (234)
- New (P2/P3): `server/internal/integrations/zohodesk/*`, `server/internal/integrations/zohocrm/*`, `server/internal/handler/zohodesk_sync.go`, `zohodesk_endpoints.go`, `zohocrm_sync.go`, `zohocrm_endpoints.go`
---

## §B. Appendix — current-state audit (Zoho Projects + Sprints)

All anchors confirmed. Here is the document.

---

# Full Zoho Integration Across ALL Projects — Research & Implementation Plan

## 1. Executive Summary

Agora has **two independent Zoho integrations**, each with its own env prefix, client package, and handler code path:

- **Zoho Projects** (`server/internal/integrations/zohoprojects/` + `server/internal/handler/zohoprojects_*.go`) — the more advanced one. It is already **multi-project and portal-scoped by design**: import supports `all=true` (every project in the portal), a periodic poller (`RunZohoSyncPoller`) sweeps every marker-carrying project across all workspaces, and there is an opt-in outbound **status** mirror. Two-way is partial (status-only); inbound is poll-only (no webhook).
- **Zoho Sprints** (`server/internal/integrations/zohosprints/` + `server/internal/handler/zohosprints_*.go`) — a separate product (Team → Project → Sprint → Item). It is a **one-way, one-shot importer only**: no poller, no push-back, read-only client (GET-only).

The reference for "full" is **Bitrix24** (`server/internal/handler/bitrix_*.go`), which has: real-time webhook + safety-net poll, per-task cross-workspace routing (`BITRIX_GROUP_MAP`), within-workspace group→project splitting from `workspace.settings`, department-gated user provisioning, full pagination, and an always-on outbound mirror. **Zoho already borrowed the poller, cursor, project provisioning, and outbound status mirror from Bitrix** — it lacks the webhook, multi-workspace routing, group→project splitting, and user provisioning.

**Shortest path to "all projects, running":** three config-only changes that require zero code:

1. `.env:14` — set `ZOHO_PROJECTS_SYNC_INTERVAL=15m` (currently `0`, which **disables the poller entirely** — `zohoprojects_sync.go:1312`).
2. Set `ZOHO_PROJECTS_PUSH_STATUS=true` (currently absent → outbound status mirror off — `zohoprojects_sync.go:120`).
3. Run a one-time `POST /api/zoho-projects/import` with `{"all": true}` to onboard every portal project (capped at 50 — `zohoprojects_endpoints.go:36`).

That gets **poll-based, continuous, status-two-way sync of all imported projects today**. Everything beyond that (auto-onboarding newly-created Zoho projects, >50 projects, multi-workspace fan-out, comments/assignee provisioning, real-time webhook, Sprints continuous sync) is net-new code, laid out in Sections 3–5.

---

## 2. Current State

### 2.1 Zoho Projects

| Aspect | Detail | Anchor |
|---|---|---|
| **Client (read)** | ListPortals, ListProjects (paginated), ListTaskLists, ListTasks (status/owner/`last_modified_time` filters, paginated), ListSubtasks, GetTaskComments, ListTaskCustomStatuses | `zohoprojects/client.go:431,467,527,657,718,785,861` |
| **Client (write)** | UpdateTaskStatus — the **only** outbound call (form POST `custom_status`) | `zohoprojects/client.go:899` |
| **OAuth** | Refresh-token grant, in-process token cache (~55m TTL), 401-retry force-refresh | `zohoprojects/client.go:136,188,205` |
| **Import** | `POST /api/zoho-projects/import` — `{project_ids[], all, owner_zpuid}`; `all=true` expands to every portal project via ListProjects | `zohoprojects_endpoints.go:84,152` |
| **Incremental sync** | `POST /api/zoho-projects/sync` — per-project `settings.zoho_synced_at` cursor drives `last_modified_time` | `zohoprojects_sync.go:260` |
| **Poller** | `RunZohoSyncPoller` → `runZohoIncrementalSweep` sweeps every project with `zoho_project:` marker across ALL workspaces | `zohoprojects_sync.go:1312,1345` |
| **Outbound** | `registerZohoOutbound` → `mirrorIssueStatusToZoho` on `issue:updated`; echo-safe (inbound uses bus-free UPDATEs) | `zohoprojects_sync.go:1187,1258` |
| **Mapping** | `mapStatus` name+type bucketing; `ResolveCustomStatusID` reverse; `MapTaskToIssue`; `nameDenotesSprint`/`TasklistIsSprint`/`TaskIsSprint` | `zohoprojects/mapping.go:39,120,163,184` |
| **Project dedup** | `getOrCreateZohoProject` — 1:1, `zoho_project:<id>` marker in `project.description` | `zohoprojects_sync.go:622` |
| **Assignee** | `zohoResolveAssignee` — email-match to existing member only, **no provisioning** | `zohoprojects_sync.go:887` |

**Key limitations (Projects):**

- Poller **DISABLED** in checked-in `.env` (`ZOHO_PROJECTS_SYNC_INTERVAL=0`). A non-positive duration makes `RunZohoSyncPoller` return immediately — `zohoprojects_sync.go:1316-1320`, `zohoSyncInterval()` `:131`.
- Outbound **OFF by default** (`ZOHO_PROJECTS_PUSH_STATUS` unset) — `zohoprojects_sync.go:120`. Even when on, **status only**; no title/description/assignee/comment/creation push-back.
- **50-project cap per request** — `zohoImportMaxProjects=50`, `zohoprojects_endpoints.go:36`; `addID()` stops appending at the cap.
- **Single portal**: `ZOHO_PROJECTS_PORTAL=octane` in `.env`; blank resolves to `portals[0]` (`resolveZohoPortalID`, `zohoprojects_sync.go:234`). No multi-portal iteration.
- **Poller only re-syncs already-imported projects** — it will not auto-discover Zoho projects created *after* the initial import (sweep enumerates Agora projects carrying the marker, not the portal — `zohoprojects_sync.go:1345`).
- **Comments imported once** (create-path only, gated by `zoho_comments_imported` flag — `zohoprojects_sync.go:824`); not backfilled on re-sync. GetTaskComments reads a single page capped at 50, not looped — `zohoprojects/client.go:785`.
- **Single-workspace-per-request**: destination is the `X-Workspace-ID` header, owner/admin-gated. No `BITRIX_GROUP_MAP` equivalent.
- **No inbound webhook**: only `/api/zoho-projects/{projects,sync,import}` exist — `router.go:605-607`. Compare `/bitrix/webhook` at `router.go:499`.

### 2.2 Zoho Sprints

| Aspect | Detail | Anchor |
|---|---|---|
| **Client (read-only)** | ResolveTeamID, ListProjects, ListSprints (`type=[1,2,3,4]`), BacklogID, ListItemStatuses, ListItems (paginated `subitem=true`) — **GET-only, no writes** | `zohosprints/client.go:342,364,395,432,456,489` |
| **Import** | `POST /api/zoho-sprints/import` — `{project_ids[], all}`, 50 cap, fire-and-forget goroutine, 202 | `zohosprints_endpoints.go:34,105` |
| **Per-project sync** | `syncZohoSprintsProject`: project → sprints → containers/backlog → items pass 1 → parent-link pass 2 | `zohosprints_sync.go:123` |
| **Project dedup** | `getOrCreateZohoSprintsProject` — `zoho_sprints_project:` marker; title `<name> (Sprints)` | `zohosprints_sync.go:366` |
| **Sprint dedup** | `getOrCreateZohoSprintsSprint` — `zsprint:` marker, real start/end dates, status **hardcoded `active`** | `zohosprints_sync.go:407` |
| **Item dedup** | `findIssueByZohoSprintsItemID` — `zoho_sprint_item_id` metadata (`@>` jsonb) | `zohosprints_sync.go:483` |

**Key limitations (Sprints):**

- **One-way only** — no write path anywhere; low-level transport is GET-only (`zohosprints/client.go` — no POST/PUT).
- **One-shot, no sync loop** — no ticker/cron/scheduler; freshness requires re-hitting the endpoint. No poller wired in `cmd/server/main.go`.
- **Single team** — `ZOHO_SPRINTS_TEAM` is one value (auto-resolves to first portal). No multi-team.
- **Update path partial** — re-import refreshes only status/sprint/project link/metadata; **title/description/priority/assignee changes NOT propagated**.
- **No assignee mapping** — raw Zoho owner ids stashed in metadata; issues owned by workspace owner (`zohoSprintsWorkspaceOwner`), priority hardcoded `none`.
- **Sprint status hardcoded `active`** regardless of real Zoho state.
- **No deletion reconciliation** — items removed in Zoho leave orphaned Agora issues.

---

## 3. Gap Analysis — What Blocks "All Projects, Full Two-Way"

| # | Gap | Projects | Sprints | Bitrix has it? | Blocker for "all projects"? |
|---|---|---|---|---|---|
| G1 | **Running sync loop** | Present but **disabled** (`SYNC_INTERVAL=0`) | **Absent entirely** | Yes (poll + webhook) | Yes — no loop = no continuous sync |
| G2 | **Auto-discovery of NEW projects** | Poller re-syncs marker-carrying projects only; won't onboard new Zoho projects | Same (one-shot) | Yes (webhook `ONTASKADD` + poll) | **Yes** — "all projects" must include future projects |
| G3 | **Pagination past 50-project cap** | `zohoImportMaxProjects=50` | `zohoSprintsImportMaxProjects=50` | Yes (cap 200, full pagination) | Yes if portal >50 projects |
| G4 | **Status push-back** | Present, **off by default** | Absent | Yes (always-on comment + optional status) | No (config) for Projects; Yes (code) for Sprints |
| G5 | **Comment sync** | Inbound, one-shot at create, single page ≤50 | Absent | Yes (delta-tracked, both directions) | Partial — fidelity gap |
| G6 | **Assignee provisioning** | Email-match to existing member only | Not mapped at all | Yes (`bitrixResolveOrProvisionAssignee` shadow user+member) | Partial — unassigned issues |
| G7 | **Multi-workspace routing** | Single header-scoped workspace | Single header-scoped workspace | Yes (`ResolveWorkspaceSlug` / `BITRIX_GROUP_MAP`) | Yes if "all projects" span multiple Agora workspaces |
| G8 | **Group→project splitting** | Rigid 1:1 Zoho project → Agora project | Rigid 1:1 | Yes (`resolveBitrixTarget` title-prefix rules) | Optional |
| G9 | **Real-time webhook** | Absent (poll only) | Absent | Yes (`/bitrix/webhook` + self-register) | No (latency, not coverage) |
| G10 | **Multi-portal / multi-team OAuth** | Single global env portal | Single global env team | Single portal | Yes if token spans multiple portals |
| G11 | **Deletion reconciliation** | Not handled | Not handled | Not handled (parity) | No |

### Projects vs Sprints — the crucial difference

**Projects is ~70% of the way to "full"** — it already has the poller (disabled), cursor, project provisioning, and status mirror. The remaining work is *incremental hardening* (raise cap, auto-discover, extend two-way).

**Sprints is ~30%** — it has none of the continuous-sync machinery. It needs the poller/cursor/outbound patterns *ported from Projects*, plus write methods added to a currently GET-only client. Bringing Sprints to parity is a larger lift than finishing Projects.

**Recommendation: finish Projects first (Phases 1–4), then bring Sprints to parity (Phase 5) by porting the now-proven Projects poller/outbound code.**

---

## 4. Target Architecture

Model on Bitrix. **Reuse existing patterns — do not invent parallel abstractions.**

### 4.1 Project auto-discovery (closes G2)

The poller must onboard projects created after the initial import. Extend `runZohoIncrementalSweep` (`zohoprojects_sync.go:1345`) so that, per configured portal, it **also enumerates the portal** via `ListProjects` and imports any project not yet carrying a `zoho_project:` marker — reusing `getOrCreateZohoProject` (`:622`) and `syncZohoProject` (`:260`). This mirrors how Bitrix's poll re-syncs active tracked tasks *and* the webhook catches new ones; since Zoho has no webhook (yet), the sweep is the sole discovery path and must enumerate, not just re-sync.

Gate discovery frequency separately (e.g. enumerate the portal every Nth sweep) to avoid a full `ListProjects` on every 15m tick.

### 4.2 Routing config in workspace settings (closes G7/G8)

Introduce a `ZohoRouteConfig` analogous to `bitrix.RouteConfig` (`bitrix_sync.go:169,202`):

- `ZOHO_PROJECTS_PROJECT_MAP` = `"zohoProjectID:slug,..."` CSV → destination workspace per Zoho project.
- `ZOHO_PROJECTS_WORKSPACE_SLUG` = default slug when a project isn't mapped.
- A `ResolveWorkspaceSlug(project, cfg)` analogous to `bitrix.ResolveWorkspaceSlug` (`bitrix_sync.go:488`).

For within-workspace splitting (G8, optional), reuse the `bitrixRoutingForWorkspace` pattern (`bitrix_import.go:219`) reading title-prefix rules from `workspace.settings` — a `zohoRoutingForWorkspace`/`resolveZohoTarget` pair. This lets the poller run **login-free and config-driven across all workspaces**, dropping the per-request `X-Workspace-ID` requirement for the automated path (the manual import endpoint keeps header-scoping).

### 4.3 Pagination past the cap (closes G3)

Two options, prefer the first:
- **Chunk the `all=true` walk**: keep the per-request cap as a safety valve but have the *poller's* discovery enumerate the full portal (ListProjects already paginates — `client.go:467`) and import in batches, so no single request must exceed 50.
- **Raise `zohoImportMaxProjects`** (`zohoprojects_endpoints.go:36`) toward Bitrix's 200. Simpler but leaves a hard ceiling.

### 4.4 Two-way beyond status (closes G4/G5)

Keep the echo-safe pattern from `mirrorIssueStatusToZoho` (`zohoprojects_sync.go:1258`) — inbound reconciles use bus-free raw UPDATEs so outbound doesn't loop. Extend the `zohoprojects/client.go` write surface beyond `UpdateTaskStatus` (`:899`): add comment-write, and optionally assignee/title/description. Each new mirror echo-guards the same way. For comments (G5), loop `GetTaskComments` over `index/range` like the other list endpoints and diff by Zoho comment id instead of the one-shot `zoho_comments_imported` gate.

### 4.5 Assignee provisioning (closes G6)

Add `zohoResolveOrProvisionAssignee` mirroring `bitrixResolveOrProvisionAssignee` (`bitrix_sync.go:1038`) + `provisionBitrixAssignee` (`:1147`): create a shadow Agora user + workspace member for a Zoho owner with no account, gated by a `ProvisionAssignees` flag + optional department/team filter (Bitrix uses `BITRIX_TEAM_DEPARTMENTS`, `bitrix_sync.go:114`). Replaces email-match-only `zohoResolveAssignee` (`:887`).

### 4.6 Real-time webhook (closes G9, optional)

Add `POST /zoho/webhook` mirroring `BitrixWebhook` (`bitrix_sync.go:252`): public, optional shared-secret via `ConstantTimeCompare`, IP rate-limited, **always 200**, detached bounded sync of the single changed task. Register via Zoho business-rule/webhook config (Zoho Projects supports outbound webhooks). This upgrades Projects from minutes-late poll to real-time. Not required for coverage — the poll already covers all projects — so this is a latency optimization, sequenced last.

### 4.7 Sprints parity (closes G1 for Sprints)

Port the Projects poller (`RunZohoSyncPoller`), cursor (`settings.zoho_synced_at`), and outbound mirror to `zohosprints_sync.go`. Add write methods to `zohosprints/client.go` (currently GET-only). Wire `RunZohoSprintsSyncPoller` in `cmd/server/main.go` next to the Projects poller (`main.go:395`).

---

## 5. Phased Implementation Plan

### Phase 0 — Turn it on (config only, zero code)

**Changes:** `.env:14` → `ZOHO_PROJECTS_SYNC_INTERVAL=15m`; add `ZOHO_PROJECTS_PUSH_STATUS=true`; run one-time `POST /api/zoho-projects/import {"all":true}`.
**Files:** `.env` only.
**Risk:** Low. Push-status now writes to Zoho — verify echo-guard holds (it does: inbound uses bus-free UPDATEs, `:1258`). Poller load is one `ListTasks` per imported project per 15m.
**Verify:** Restart server; confirm log is NOT "zoho sync poller disabled" (`:1316`). Change an issue status in Agora → confirm Zoho task status updates. Edit a Zoho task → confirm it appears in Agora within one interval.

### Phase 1 — Auto-discover all projects + pagination (closes G2, G3)

**Changes:** Extend `runZohoIncrementalSweep` (`zohoprojects_sync.go:1345`) to enumerate each configured portal via `ListProjects` and import unmarked projects (reuse `getOrCreateZohoProject` `:622` + `syncZohoProject` `:260`). Add a discovery-throttle (enumerate every Nth tick). Chunk imports so no request exceeds the cap; leave `zohoImportMaxProjects` as a safety valve.
**Files:** `zohoprojects_sync.go` (sweep), `zohoprojects_endpoints.go` (chunking helper if extracted).
**Risk:** Medium. Full-portal enumeration adds API calls — throttle to respect Zoho rate limits (per-endpoint breakers already exist). Guard against importing archived projects.
**Verify:** Create a new project in Zoho → confirm it appears in Agora after a discovery tick with no manual import. Portal with >50 projects fully onboards across multiple ticks.

### Phase 2 — Multi-workspace routing config (closes G7, G8)

**Changes:** Add `ZohoRouteConfig` + `ResolveWorkspaceSlug` (model on `bitrix_sync.go:169,488`); read `ZOHO_PROJECTS_PROJECT_MAP` + default slug. Poller resolves destination workspace from config, not a header. Optional: `zohoRoutingForWorkspace`/`resolveZohoTarget` for title-prefix splitting (model on `bitrix_import.go:219`, `bitrix_sync.go:781`).
**Files:** new `zohoprojects_routing.go` (or extend `zohoprojects_sync.go`), `zohoprojects_endpoints.go`.
**Risk:** Medium. Wrong mapping routes a project to the wrong workspace; default-slug fallback prevents silent drops. Keep the manual import endpoint header-scoped for backward compat.
**Verify:** Map two Zoho projects to two different workspace slugs → confirm each lands in the correct workspace. Unmapped project falls to default slug.

### Phase 3 — Comment two-way + comment fidelity (closes G5, extends G4)

**Changes:** Loop `GetTaskComments` over `index/range` (`client.go:785`); drop one-shot `zoho_comments_imported` gate, diff by comment id (`:824`). Add a comment-write method to `zohoprojects/client.go`; add outbound comment mirror echo-guarded like status (`:1258`).
**Files:** `zohoprojects/client.go`, `zohoprojects_sync.go`.
**Risk:** Medium-high. Echo loops are the main hazard — every mirrored comment must carry a provenance marker that inbound skips. Rate limits on comments endpoint (~100/2min) — keep the per-run breaker.
**Verify:** Long Zoho thread (>50) imports fully. New Zoho comment appears on re-sync. Agora comment appears in Zoho once, no duplicate/loop.

### Phase 4 — Assignee provisioning (closes G6)

**Changes:** Add `zohoResolveOrProvisionAssignee` + `provisionZohoAssignee` (model on `bitrix_sync.go:1038,1147`), gated by a `ProvisionAssignees` flag + optional filter. Replace `zohoResolveAssignee` (`:887`) call sites at `zohoprojects_sync.go:489,548`.
**Files:** `zohoprojects_sync.go`.
**Risk:** Medium. Shadow-user creation can bloat membership — gate carefully and dedup by email. Preserve existing metadata-chip behavior for un-provisionable owners.
**Verify:** Zoho owner with no Agora account → shadow user + member created, issue assigned. Existing member still matched by email.

### Phase 5 — Sprints parity (closes G1 for Sprints; G4/G5/G6 for Sprints)

**Changes:** Port the Projects poller, cursor, and outbound mirror into `zohosprints_sync.go`; wire `RunZohoSprintsSyncPoller` in `cmd/server/main.go` (next to `:395`). Add write methods to `zohosprints/client.go` (GET-only today). Derive real sprint status instead of hardcoded `active` (`zohosprints_sync.go:407`). Propagate full item field updates on re-import. Map owner ids to members (reuse Phase 4 provisioning).
**Files:** `zohosprints/client.go`, `zohosprints_sync.go`, `zohosprints_endpoints.go`, `cmd/server/main.go`, `router.go`.
**Risk:** High — largest lift. Sprints client has no write transport; adding POST/PUT is new surface. Test against a real Sprints team.
**Verify:** Sprints project stays fresh without manual re-import. Agora status change pushes to Zoho Sprints item. Sprint status reflects real Zoho state.

### Phase 6 — Real-time webhook (closes G9, optional latency win)

**Changes:** Add `POST /zoho/webhook` (model on `bitrix_sync.go:252`) + rate-limit middleware; wire in `router.go` next to `:499`. Add a register endpoint or document Zoho business-rule config.
**Files:** `zohoprojects_sync.go` (webhook handler), `router.go`.
**Risk:** Medium. Public endpoint — must always 200, validate shared-secret in constant time, detach sync. Webhook + poll must not double-process (advisory lock already guards — `:420`).
**Verify:** Edit a Zoho task → Agora updates within seconds, not minutes. Malformed payload still returns 200, no crash.

---

## 6. Config Changes

Mirror the Bitrix multi-group approach. Add to `.env` / deployment env:

| Env var | Current | Target | Purpose | Phase |
|---|---|---|---|---|
| `ZOHO_PROJECTS_SYNC_INTERVAL` | `0` (disabled) | `15m` | Enable poller (`zohoprojects_sync.go:131`) | 0 |
| `ZOHO_PROJECTS_PUSH_STATUS` | absent (off) | `true` | Enable outbound status mirror (`:120`) | 0 |
| `ZOHO_PROJECTS_PORTAL` | `octane` | keep, or CSV for multi-portal | Portal selection (`:234`) | 1/G10 |
| `ZOHO_PROJECTS_DISCOVERY_INTERVAL` | — | e.g. `1h` | Throttle full-portal enumeration in sweep | 1 |
| `ZOHO_PROJECTS_PROJECT_MAP` | — | `"zohoProjID:slug,..."` | Per-project → workspace routing (à la `BITRIX_GROUP_MAP`) | 2 |
| `ZOHO_PROJECTS_WORKSPACE_SLUG` | — | default slug | Fallback destination (à la `BITRIX_SYNC_WORKSPACE_SLUG`) | 2 |
| `ZOHO_PROJECTS_PROVISION_ASSIGNEES` | — | `true`/`false` | Shadow-user provisioning gate (à la `ProvisionAssignees`) | 4 |
| `ZOHO_PROJECTS_TEAM_DEPARTMENTS` | — | CSV | Provisioning filter (à la `BITRIX_TEAM_DEPARTMENTS`) | 4 |
| `ZOHO_PROJECTS_PUSH_COMMENTS` | — | `true`/`false` | Outbound comment mirror gate | 3 |
| `ZOHO_WEBHOOK_SECRET` / `ZOHO_WEBHOOK_PUBLIC_URL` | — | secret / URL | Webhook auth + self-register (à la `BITRIX_WEBHOOK_*`) | 6 |
| `ZOHO_SPRINTS_SYNC_INTERVAL` | absent | `15m` | Enable Sprints poller (new) | 5 |
| `ZOHO_SPRINTS_PUSH_STATUS` | absent | `true` | Sprints outbound (new) | 5 |
| `ZOHO_SPRINTS_TEAM` | absent (auto) | keep / CSV | Team selection; multi-team if needed | 5 |

Env is read live per-call via `os.Getenv` (no cached config struct), consistent with Bitrix — new vars follow the same pattern.

---

## 7. Open Questions / Decisions for the User

1. **Scope of "ALL projects" — one workspace or many?** Does every Zoho project land in one Agora workspace, or should projects fan out to multiple workspaces? This decides whether Phase 2 (routing config) is required or optional. Bitrix fans out via `BITRIX_GROUP_MAP`; if all Zoho projects belong to one team, a single default slug suffices.

2. **How "two-way" is required?** Phase 0 gives status-only push-back. Do you need comments (Phase 3), assignees, and title/description mirrored back to Zoho, or is status enough? Full field mirroring materially increases echo-loop risk and build cost.

3. **Assignee provisioning policy.** Should Zoho owners without an Agora account get **shadow users + workspace membership** (Bitrix-style, Phase 4), or is a metadata chip enough? If provisioning, what's the department/team filter to avoid importing every Zoho user?

4. **Sprints — worth the parity lift?** Bringing Sprints to continuous two-way (Phase 5) is the largest chunk (GET-only client needs write methods). Is Sprints actively used, or is Projects the real target and Sprints can stay one-shot import-only?

5. **Real-time webhook (Phase 6) — needed?** The poll already covers all projects; the webhook only reduces latency from ~minutes to seconds. Worth the public-endpoint surface, or acceptable to stay poll-only?

6. **Multi-portal / multi-team (G10).** Does the current OAuth token span more than the `octane` portal / one Sprints team? If so, the client must iterate portals/teams — otherwise single-portal is fine and this is out of scope.

7. **Discovery cadence & rate limits.** Full-portal enumeration on the poller adds `ListProjects` calls. What sweep/discovery interval balances freshness against Zoho's per-endpoint rate limits (comments ~100/2min, subtasks throttled)?

8. **Deletion reconciliation (G11).** Neither Zoho nor Bitrix handles items deleted upstream (orphaned Agora issues). Is tombstoning in scope, or acceptable to leave as-is for parity?

---

### Key file map (build reference)

- **Projects client:** `server/internal/integrations/zohoprojects/client.go` (list `:467`, comments `:785`, write `:899`)
- **Projects mapping:** `server/internal/integrations/zohoprojects/mapping.go` (`:39,120,163,184`)
- **Projects handler:** `server/internal/handler/zohoprojects_endpoints.go` (`:36,84,152`), `server/internal/handler/zohoprojects_sync.go` (poller `:1312,1345`; outbound `:1187,1258`; assignee `:887`; project dedup `:622`; env `:105,120,131`)
- **Sprints client:** `server/internal/integrations/zohosprints/client.go` (read `:342-513`, GET-only)
- **Sprints handler:** `server/internal/handler/zohosprints_endpoints.go` (`:34,105`), `server/internal/handler/zohosprints_sync.go` (`:123,238,366,407,483`)
- **Bitrix reference:** `server/internal/handler/bitrix_sync.go` (webhook `:252`, route config `:169`, ResolveWorkspaceSlug `:488`, resolveBitrixTarget `:781`, provision `:1038,1147`), `server/internal/handler/bitrix_import.go` (`:219`), `server/cmd/server/bitrix_poll.go:32`
- **Wiring:** `server/cmd/server/main.go:395` (poller), `server/cmd/server/router.go` (`:499` bitrix webhook, `:605-607` zoho projects, `:613-614` zoho sprints)
- **Config:** `.env:10-20` (`ZOHO_PROJECTS_SYNC_INTERVAL=0`, `ZOHO_PROJECTS_PORTAL=octane`, no push-status line)