# Importer plan — making a Linear or Jira team switch in an afternoon

Status: proposed · Owner: Jamshid · Drafted 2026-09-19

## Thesis

Nobody adopts a task tracker. They *leave* one. Every Agora signup that
matters is a team already carrying two years of issues, comments, and habits
somewhere else, and the honest question they ask on day one is not "is this
better" — it is "what happens to everything we already have". Today Agora
answers that question with silence, and the answer a prospect fills in is
"start from scratch", which is the same as "no".

Linear won a decade of Jira accounts with a single feature that was not a
feature of the product at all: an importer that made switching a Tuesday
afternoon instead of a quarter. We should take that lesson twice over —
once for the mechanism, once for the twist. The mechanism is a source-agnostic
import pipeline. The twist is that Agora is the only tracker with a built-in
assistant that can hold a **conversation about the migration** — look at your
Linear workspace, tell you what it found, show you the mapping it proposes,
let you argue with it in plain language, and then run the whole thing on one
confirmation and hand back a receipt. Every competitor's importer is a wizard
with four screens and a progress bar. Ours is a colleague who did the survey
first.

The good news is that Agora has already built this once. The Bitrix24
integration is a production two-way sync with identity mapping, external-id
linkage, stage mapping with per-workspace overrides, poll+webhook
reconciliation, attachment ingestion and idempotent re-runs. It is not a
prototype — it runs the team's own work. What it is *not* is reusable: every
one of those mechanisms is spelled `bitrix` and lives in `handler`. Zoho
Projects was added later and duplicated the whole shape (its `mapping.go`
literally says "Mirrors bitrix.MapStatus"). A third copy for Linear would be
the point at which the monorepo's own No-Duplication Rule stops being advice
and starts being a bug.

So: generalize what Bitrix proved, keep what Bitrix got wrong out of the
framework, and put the assistant in front of it.

## 1. What already exists, and what of it generalizes

### 1.1 The reusable half

| Mechanism | Where it lives today | Generalizes as | Note |
| --- | --- | --- | --- |
| External identity → Agora user | `user_external_identity(provider, external_id) → user_id`, migration 120; helpers in `server/internal/handler/external_identity.go` | **As-is.** `provider` is already free-form text and the PK is `(provider, external_id)` — `('linear', '<uuid>')` and `('jira', '<accountId>')` need no schema change | The link-steal guard (`linkExternalIdentity` returns `errExternalIdentityClaimed` rather than overwriting another user's mapping) is exactly the property an importer needs when two sources claim one person |
| Never attribute an unmatched author to the operator | `ensureBitrixAuthorMember` + `bitrixAuthorUserEmail = "bitrix-import@bitrix.local"` in `bitrix_import.go` | **The single most important thing to lift.** A dedicated, undeliverable, un-loggable-in attribution identity per source | The code comment names the bug it fixed: comments "mis-showed every external author as the operator who ran the import (the reported bug)" |
| External-id linkage on the issue | `issue.metadata->>'bitrix_task_id'`, JSONB `@>` containment lookup (`findIssueByBitrixTaskID`) | **Pattern generalizes, key must not.** One `external_ref` shape on `issue.metadata`, not one key per vendor | `issue` already has a `metadata jsonb` column and the `ListIssues` filter already supports `?metadata={...}` containment, so provenance is filterable for free |
| External-id linkage on the comment | `comment.bitrix_comment_id text` + partial index, migration 152 | **Rename, don't repeat.** A generic `comment.external_ref jsonb` (or `external_source` + `external_id`) | Adding `linear_comment_id`, `jira_comment_id`, … is the anti-pattern the migration itself warns about |
| Status/stage mapping with per-workspace override | `bitrix.MapStage` keyword defaults + `workspace.settings.bitrix_stage_map` (exact stage name, lowercased) read by `bitrixRoutingForWorkspace` | **As-is, generalized to a per-source `mapping` blob.** Defaults in the adapter, overrides in `workspace.settings` | `MergeWorkspaceSettings` / the key-scoped `jsonb_set` query already exist, so the assistant can rewrite one mapping key atomically |
| Identity aliases (external email → canonical Agora email) | `workspace.settings.bitrix_identity_aliases` | **As-is.** The "same human, two email addresses" problem is universal | |
| Project routing (which source container becomes which Agora project) | `bitrix_project_prefixes` / `bitrix_default_project`, longest-prefix-first | **Generalizes as the mapping UI's project table** — one row per source project/team | |
| Durable container linkage | `project.description` carries a `bitrix_group:<id>` marker, matched with `LIKE` | **Generalize the intent, fix the mechanism.** `project` has a `settings jsonb` column; put the external ref there | The marker-in-description hack exists only because the author did not want a migration; it leaks provenance into user-visible copy |
| Attachment ingestion | `importBitrixAttachments` → download → `Storage.Upload` → `attachment` row, idempotent via a synced-id set | **As-is**, minus the video-frame extraction (that is a Bitrix/QA-specific enrichment) | |
| Incremental, idempotent re-run | `issue.metadata.bitrix_synced_comment_ids` / `…_file_ids` arrays, "import only what's new" | **Generalizes as the framework's upsert contract**, though as a set on the issue it does not scale; see §3.5 | |
| Poll + webhook reconciliation | `cmd/server/bitrix_poll.go` safety-net poll beside the `ONTASKADD/ONTASKUPDATE` webhook | **Generalizes to the bridge tier only** (§5), not to one-shot imports | |
| Fail-closed scope gates | `BITRIX_SYNC_USER_EMAILS`, `AGORA_BITRIX_BULK_IMPORT`, `AGORA_BITRIX_IMPORT_OWN_ONLY` | **Generalizes as import scope + a role gate**, but as registry config keys, not raw env reads | |
| Sealed per-workspace credential | `git_credential` (migration 132) and `zoho_connection` (migration 142): value in `bytea`, sealed with `secretbox` (AES-256-GCM), key from `AGORA_*_SECRET_KEY`, endpoints **fail closed with 503 if the key is unset rather than storing plaintext** | **As-is. This is the pattern the Linear/Jira token must follow.** | `server/internal/util/secretbox/secretbox.go` |
| Issue-number self-healing | `IncrementIssueCounter` takes `GREATEST(counter+1, max(number)+1)` because "a bulk data load that preserved external numbering" desynced the counter and every subsequent create failed on `uq_issue_workspace_number` | **Already paid for.** An importer that writes explicit numbers cannot break creates any more | The incident is recorded in `server/pkg/db/queries/workspace.sql` and reproduced in `issue_number_counter_test.go` (sd-main: counter 179 vs max number 319) |
| Second-adapter proof | `server/internal/integrations/zohoprojects/` mirrors the Bitrix shape one-for-one | **Evidence, not code.** Two copies is the argument for the framework | |

### 1.2 The Bitrix-specific half (do not lift)

- **BBCode.** `bitrix/bbcode.go` is a portal-specific markup converter. Linear
  is already Markdown; Jira v3 is ADF. Each adapter owns its own body
  conversion; the framework only says "the normalized shape holds Markdown".
- **Video-frame extraction.** `frames.go` + ffmpeg exists so the planner agent
  can "see" a bug recording. That is an Agora QA feature that happens to live
  in the Bitrix importer, not an import concern.
- **The `ai` tag gate and stage-keyword table.** `MapStage`'s ordering comments
  ("release BEFORE done", Russian/English mixed labels with typos) are a
  hand-tuned artifact of one customer's portal. The *mechanism* (keyword
  defaults + exact-name overrides) generalizes; the table does not.
- **Bitrix-as-source-of-truth outbound push.** `BITRIX_PUSH_STATUS`,
  `BITRIX_PUSH_HUMAN_COMMENTS`, the fail-closed single outbound human — all of
  that is the *bridge* posture (§5), and it is the wrong default for a
  migration, where Agora becomes the source of truth.
- **Process-wide in-memory progress.** `bitrixImportProgressState` is a single
  package-level struct with a mutex, a run id, and a `Cancel` func —
  one import at a time, per process, lost on restart, invisible to a second
  instance. It is fine for an operator backfill and disqualifying for a
  customer-facing importer. The framework needs a **row**, not a global (§3.7).
- **Env-var-only configuration.** Bitrix reads `os.Getenv` in a dozen places.
  New flags go in `server/internal/config/registry.go` so they appear in
  Settings → Configs and can be flipped without a redeploy.
- **Operator-only ergonomics.** `requireBitrixOperator` plus a
  `BITRIX_WEBHOOK_URL` that is instance-global means one portal per
  deployment. An importer is per-workspace and self-serve by definition.

### 1.3 The three things Agora's write path cannot do yet

These are not opinions; they are the schema as it stands, and each one is a
Phase 1 work item.

1. **Original timestamps.** `CreateIssueParams` and `CreateCommentParams`
   carry no `created_at` — both default to `now()`. Bitrix side-stepped this
   by printing the original date inside the comment body
   (`**Bitrix — <author> (<date>)**:`). For a migration that is not
   acceptable: a two-year backlog that all says "created today" is not a
   migration, it is a paste. Needs explicit-timestamp sqlc queries
   (`CreateIssueImported` / `CreateCommentImported`) used **only** by the
   importer.
2. **Relation vocabulary.** `issue_dependency.type` is
   `CHECK (type IN ('blocks','blocked_by','related'))`. Linear's
   `IssueRelationType` has four values — `blocks`, `duplicate`, `related` and
   `similar` — and Jira's link types are installation-defined ("Duplicate",
   "Cloners", "Causes", …). The call: widen the CHECK to admit `duplicate`
   (a real, universal relation every tracker has), and **downgrade everything
   else to `related`** with the source's own type name preserved in the
   linkage blob. Enum drift downgrades, it never crashes — the same rule the
   frontend already follows for server-driven enums.
3. **Identifier preservation.** An Agora issue is `<workspace.issue_prefix>-<number>`.
   `ENG-142` cannot be reproduced unless the workspace prefix happens to be
   `ENG` and `142` is free. The importer therefore preserves the source
   identifier in the linkage blob and renders it as a provenance line with a
   deep link back — and offers, as a one-time option at workspace level, to
   adopt the source's prefix so the numbers at least *look* continuous.

## 2. The source surfaces

Everything below was verified against the vendors' own documentation (and, for
Linear, against `schema.graphql` in the `linear/linear` repository) on
**2026-09-19**. URLs are in the Appendix. Where a claim could not be verified
it is marked *unverified* rather than asserted.

### 2.1 Linear

**Shape.** One GraphQL endpoint, `https://api.linear.app/graphql`, with
introspection on. Root query fields cover everything an import needs:
`issues`, `teams`, `cycles`, `workflowStates`, `issueLabels`, `users`,
`comments`, `attachments`, `projects`, `initiatives`, `documents`,
`issueRelations`. `Issue` carries `identifier` ("ENG-142"), `number`, `title`,
`description`, `estimate`, `priority`, `state`, `cycle`, `project`,
`projectMilestone`, `assignee`, `creator`, `dueDate`, `parent`, `children`,
`comments`, `attachments`, `relations`/`inverseRelations`, `history`
(`IssueHistory`, a structured change log) and `stateHistory`.

**The mapping is almost embarrassingly clean.** Linear's priority is an `Int`
whose meaning the schema itself documents: *"0 = No priority, 1 = Urgent,
2 = High, 3 = Medium, 4 = Low."* Agora's is
`none | urgent | high | medium | low`. That is a bijection, no configuration
needed. Cycles are sprints. Teams and projects are projects. Descriptions and
comments are already Markdown. Linear has **no custom-field system at all**,
so the single ugliest part of a Jira import does not exist here.

**Where it is not clean** — `IssueRelationType` has four values:
`blocks`, `duplicate`, `related`, **`similar`**. Agora's
`issue_dependency.type` CHECK allows three: `blocks`, `blocked_by`, `related`.
So `duplicate` and `similar` need a decision (widen the CHECK for `duplicate`,
downgrade `similar` to `related` with the original type preserved on the
linkage blob).

**Auth.** Two mechanisms, and the choice matters more than it looks:

- **Personal API key** — `Authorization: <key>`, with **no `Bearer` prefix**
  (a real trap; `Bearer lin_api_…` fails). Scopable at creation to
  Read / Write / Admin. No documented expiry.
- **OAuth 2.0** — `Authorization: Bearer <token>`; scopes `read`, `write`,
  `issues:create`, `comments:create`, `timeSchedule:write`, `admin`. **Access
  tokens are valid 24 hours**, and Linear's docs state all OAuth apps were
  migrated to the refresh-token system on **1 April 2026**; for
  non-interactive use they point at client-credentials tokens valid 30 days.

That 24-hour expiry is the reason **Phase 1 ships with the personal API key
path only.** An import job that can outlive its own credential is a support
ticket, and OAuth's refresh dance buys nothing for a one-shot read. OAuth is
Phase 2+ work, motivated by "the buyer will not paste a personal key", not by
capability.

**Rate limits** (documented, per user):

| Auth | Requests/hour | Complexity points/hour |
| --- | --- | --- |
| Personal API key | 2,500 | 3,000,000 |
| OAuth app | 5,000 | 2,000,000 |
| Unauthenticated | 600 (per IP) | 100,000 |

A single query may not exceed **10,000 complexity points**. Complexity is
computed structurally: each scalar 0.1, each object 1, and **each connection
multiplies its children's cost by its page size** (defaulting to 50 when
`first` is omitted). Responses carry `X-Complexity` and
`X-RateLimit-Complexity-Remaining`.

The design consequence is concrete: a naive
`issues(first:50){ comments(first:50) attachments(first:50) history(first:50) }`
multiplies out fast and can trip the 10,000-point per-query ceiling on its own.
The adapter therefore fetches **shallow** — a page of issues with scalars and
ids, then comments and attachments in their own passes — and **reads
`X-Complexity` off every response to tune its own page size**, rather than
hard-coding one. For 10k issues the binding constraint is complexity, not the
2,500 requests/hour; exact throughput is *unverified* and must be measured
against the headers before any runtime is promised to a user.

Pagination is Relay-style (`first`/`after`, `PageInfo.hasNextPage`,
`endCursor`), default page size 50. **No universal maximum `first` is
documented** for the standard connections — one unrelated field
(`templateSearch`) documents a 250 cap, which must not be generalized. Treat
the max as unknown and discover it.

**Bulk export: there isn't one.** No bulk endpoint, no export mutation. The
in-app CSV export is worse than it sounds: download links expire in 12 hours,
members can export 250 issues at a time and admins 2,000, and the documented
field list — `ID, Team, Title, Description, Status, Estimate, Priority,
Project, Creator, Assignee, Labels, Cycle …, Created, Updated, …, Parent issue,
Initiatives, Project Milestone, SLA Status` — **contains no comments and no
attachment files**. So CSV is not a shortcut for Phase 1; the GraphQL walk is
the only path that carries a discussion.

**Attachments.** Files live on `https://uploads.linear.app` and are **not
public**: the same `Authorization` header used for GraphQL fetches them. There
is a neat opt-in — send the request header `public-file-urls-expire-in: <secs>`
alongside a GraphQL request and the file URLs in that response come back
pre-signed and time-limited. Our applier does not need it (it downloads
server-side with the token), but it is the correct mechanism if we ever hand a
URL to a browser. There is no bulk-download endpoint; attachments are walked
per issue.

**Linear's own importers** are the growth lever we are copying, and their
documented limits are the specification for what "good enough" looked like
when it worked: Jira **Issue Type flattens to "Task"**; **Constraints are not
migrated**; **Components become labels** formatted `"Component: Engineering"`;
a required Jira workflow field with no Linear equivalent can **block sync
entirely**; users must link their Atlassian accounts inside Linear or imported
issues land **unassigned**. Authorship is explicitly conditional — Linear's own
words are that creators, assignees, mentions and comments import properly
*"if users already exist in both services with the same email address."*
The legacy `@linear/import` CLI's per-source table is a useful map of what
every importer settles for: title, description, labels, state, assignee,
sometimes comments, sometimes created date — and nothing else.

**Getting out of Linear** is undocumented by Linear. The migration guide is
import-only. In practice people hand-roll GraphQL pagination; the open-source
`terrastruct/byelinear` (Linear → GitHub) is the reference implementation, and
its README is candid about the same three losses everyone hits: comments
*"appear from the importing account"*, timestamps are not preserved, and
workflow states get remapped lossily. One community Linear → Jira guide hits a
gotcha worth remembering in the other direction: Jira's importer requires
attachments at a **public** URL, so authenticated `uploads.linear.app` files
must be re-hosted first.

### 2.2 Jira Cloud

**The search API changed under everyone's feet, and this is the single most
important fact for anyone writing a Jira importer today.**
`GET/POST /rest/api/3/search` was deprecated on 1 May 2025 and progressively
shut off through October 2025; it is gone. The replacement is
`POST /rest/api/3/search/jql`, and it differs in two ways that reshape an
importer:

- **`startAt` is gone.** Paging is cursor-only via `nextPageToken` — forward,
  sequential, no jump-to-page. Atlassian's own RFC gives the reason plainly:
  random-access pagination over large JQL results is "slow for the user, costly
  for us, and not scalable."
- **`total` is gone.** There is no exact count in the response. A count comes
  from a separate `POST /rest/api/3/search/approximate-count`, which requires a
  **bounded** JQL query and returns an explicitly *estimated* number.

That second one lands directly on our UX: **the dry-run report cannot promise
"240 issues" for Jira.** It reports an estimate, labels it an estimate, and
reconciles against the real count in the receipt. A migration report that
quotes a precise number the API cannot produce is a lie the first user will
catch.

Page tokens **expire after 7 days**, page size varies ("100 to 5000" depending
on field payload), and the endpoint had a rough first year in production —
an Atlassian Community thread with ~100k views reports *"Pagination is broken.
`isLast` never returns true, `nextPageToken` chains endlessly … always loading
the first page again and again,"* with follow-ups reporting tokens that
*"all just return expired."* Whether those specific defects persist today is
*unverified*; that an importer must be **resumable and idempotent against
external ids rather than cursor positions** follows either way, and our upsert
design already is.

The recommended bulk pattern (Atlassian's own guidance) is two-phase: page
`search/jql` requesting **no fields** to collect issue ids cheaply, then batch
those ids into groups of **100** against `POST /rest/api/3/issue/bulkfetch` for
full field data. `bulkfetch` returns partial success as a **200** with an
`issueErrors[]` array — so "did every issue come back" is a body check, not a
status check.

**Everything else is where you'd expect,** with two structural surprises:

- **Changelog.** `expand=changelog` on a search silently caps at **40
  entries** per issue. Full history needs `GET /issue/{key}/changelog` or the
  bulk `POST /rest/api/3/changelog/bulkfetch`. Comments are not in the
  changelog; they are their own paginated resource.
- **Sprints and boards live in a different API.** `/rest/agile/1.0/board`,
  `/board/{id}/sprint`, `/board/{id}/epic` — a second base path, a second
  client, and data that also surfaces redundantly on the issue as a
  `customfield_*`. Any "full fidelity" Jira import is two API clients.

Reference data is conventional: `/project/search` (still classic
`startAt` paging), `/issuetype`, `/status`, `/statuscategory`, `/priority`,
`/label`, `/users/search`, `/issue/{key}/comment`, `/issueLink`,
`/issueLinkType`, `/attachment/{id}` + `/attachment/content/{id}`,
`/issue/{key}/worklog`, `/field`, `/version`, `/component`.

**ADF.** v3 returns descriptions, comment bodies and textarea custom fields as
**Atlassian Document Format JSON** — a hierarchical node tree, not wiki markup
(that is the v2 behaviour). There is **no official Atlassian ADF → Markdown
converter**; third-party npm packages exist with the usual maintenance risk.
Every importer writes its own node walker. Ours will too, with an explicit
fallback to plain-text extraction for unhandled node types, and a count of how
many nodes it degraded, reported in the receipt.

**Auth.** Basic auth with email + API token (`Authorization: Basic
base64(email:token)`) is the pragmatic path. A trap worth writing down:
**scoped** API tokens must be used against
`https://api.atlassian.com/ex/jira/{cloudId}/…`, not
`https://<site>.atlassian.net/…` — only unscoped legacy tokens work on the site
domain. Atlassian is also forcing expiry on tokens created before 15 Dec 2024,
so a stored token needs a re-auth path. OAuth 2.0 (3LO) needs
`read:jira-user` + `read:jira-work` (classic) or
`read:project:jira`, `read:issue:jira`, `read:comment:jira`,
`read:attachment:jira`, `read:user:jira` (granular). And the line that governs
every completeness claim we make: *"Jira permissions also control access to
data and aren't overridden by scopes."*

**Rate limits** are three simultaneous limiters: a points-based hourly quota
(global app pool 65,000/hr; per-tenant Standard 100,000 + 10×users, Premium
130,000 + 20×users, Enterprise 150,000 + 30×users, with a GET on a core object
costing ~1 point), a **burst cap of 100 req/s** for GET/POST (50 for
PUT/DELETE), and per-issue write limits (20 writes / 2s). 429s carry
`RateLimit-Reason` and `Retry-After`.

For a 50k-issue export the hourly quota is a non-issue — ~500 bulkfetch calls
for the bodies. The burst cap plus **N+1 fetches for comments, changelog and
worklogs** is what actually kills naive importers, and Atlassian's own
community article warns about the blast radius: *"one user doing a migration,
bulk update, large export … could consume the app's budget and disrupt the app
for everyone else in that same organization."* Our adapter therefore holds a
per-connection token bucket well under the burst cap and backs off on
`Retry-After` rather than racing it.

**Native export is not a shortcut either.** The UI CSV export now handles up to
10,000 items asynchronously (the widely-cited 1,000-row ceiling applies to
older/sync export paths — the two limits could not be fully reconciled and the
distinction is *unverified*), "Export CSV (all fields)" is explicitly slow and
discouraged, and anything larger is Atlassian telling you to split by JQL and
merge client-side. And on the inbound side, Jira's **own** CSV importer
stamps comments with the importing user when the original author is not present
on the destination site — the exact bug we are designing against, shipped by
the vendor.

**Five gotchas that must appear in the dry-run report, not be discovered
later:**

1. **Archived issues are unreachable.** They are removed from the JQL index
   and excluded from every JQL-based search, with no public operator to include
   them (JRACLOUD-94071); they can only be fetched by direct key. An importer
   that walks `search/jql` **silently skips every archived issue**.
2. **Issue security schemes hide issues silently.** An export run under a
   non-admin service account produces an incomplete dataset with no error.
3. **Restricted comments** (project-role or group visibility) are omitted for
   a token lacking that role.
4. **Email is usually not available.** Since the 2019 GDPR migration all user
   references are opaque `accountId`s, and email is returned only if the
   site-wide visibility setting *and* the individual's profile privacy both
   allow it. **An email-keyed identity map cannot be built from the Jira API
   alone** — display-name matching plus an explicit human mapping step is
   mandatory, not a nicety. This is the strongest argument in the whole
   document for the conversational mapping step.
5. **Deleted users become "Former user"** with no recoverable name.

**What the other tools drop.** Even Atlassian's own JCMA (Jira → Jira, the
highest-fidelity migration that exists anywhere) does not migrate dashboards,
filter subscriptions, webhooks, cross-project boards, user avatars, or the
activity stream. Shortcut flattens subtasks into stories with a dependency
link. Height is Cloud-only and expects a second pass to patch unmapped fields.
Across every tool surveyed, **comment authorship is the single most fragile
field.** The complaint literature agrees: a Jira → Linear account of a
2,147-issue migration lists what actually hurt — *"Custom workflows didn't
survive"*, *"JQL queries were a total loss … 23 saved filters, and they all
died"*, *"Time tracking vanished"* — a different failure class from authorship,
and one we should say out loud rather than let a team discover.

### 2.3 Asana, ClickUp, Trello — one paragraph each

**Asana.** REST at `app.asana.com/api/1.0`; a Personal Access Token (inherits
the creating user's full permissions, no scoping) or OAuth 2.0. Rate limiting
is **cost-based per token**: 150 req/min free, 1,500 paid, with cost computed
after the response from graph-traversal complexity, a separate 60 req/min cap
on Search, and — the trap — **rejected 429s still count against quota**, so a
retry loop makes throttling worse. Tasks, projects, sections, custom fields and
attachments are all reachable. Two structural problems for an importer: comments
are **"stories"**, a single feed that mixes human comments with system events
(field changes, reassignment, completion), so the adapter must filter on
`resource_subtype` or import an audit log as discussion; and a task is
**multi-homed** — it can live in several projects at once, with no single
parent — so any one-project-per-issue model needs a declared primary-project
heuristic, or it will either duplicate tasks or silently drop memberships.

**ClickUp.** REST v2 at `api.clickup.com/api/v2` is the complete generation;
v3 is rolling out **endpoint by endpoint**, so an adapter must pick a version
per endpoint rather than per API. Auth is a personal token (`pk_…`) or OAuth 2.
Rate limits are per token and tied to the workspace's plan: 100 req/min on
Free/Unlimited/Business, 1,000 on Business Plus, 10,000 on Enterprise, with
standard `X-RateLimit-*` headers. The hierarchy is five deep — Workspace →
Space → Folder (**optional**) → List → Task, with subtasks under tasks — and the
optional Folder layer is the mapping decision, since Agora has projects and
sprints and nothing in between. Custom fields, threaded comments and
attachments are separately paginated sub-resources per task.

**Trello.** REST at `api.trello.com/1` with plain key + token auth (no OAuth
needed server-side). Limits are **300 req/10s per API key and 100 req/10s per
token**, plus a much stricter 100 req/900s on `/1/members/`; a key that
accumulates more than ~200 429s in a window gets hard-blocked for the rest of
it. Board data comes from `GET /1/boards/{id}?fields=all&actions=all&…`, but
**actions cap at 1,000 per query** and full comment history on an active board
needs the dedicated `/1/boards/{id}/actions` endpoint paged with
`before`/`since`. Comments are an action subtype (`commentCard`) inside the
same feed as every card move and member change — the same filtering problem as
Asana's stories. The first-party "Export as JSON" has the same 1,000-action
ceiling, and the Premium CSV export **excludes comments entirely**. Attachment
URLs that are Trello-hosted need the same key + token to fetch the bytes.
(The exact current `?fields=all&actions=all` response schema is *unverified* —
the docs page is JS-rendered and could not be fetched directly.)

### 2.4 What this research actually changes in the design

- **Idempotent upsert keyed on external id is not a nicety, it is the only way
  a Jira import survives its own pagination.** Tokens expire in 7 days, page
  cursors are not stable, and the endpoint has a public defect history. Resume
  means "re-walk and upsert", never "continue from cursor N".
- **The dry-run report must lead with what it cannot see.** Jira alone has four
  separate invisibility mechanisms (archived issues, security schemes,
  restricted comments, hidden emails). A report that counts only what it found
  is structurally dishonest on this source.
- **Identity mapping must not assume email.** Jira frequently will not give one.
  The conversational mapping step is therefore load-bearing for Jira, not a
  nicety — and it is the part of this plan that no competitor's wizard has.
- **Linear needs no mapping configuration to work, and that is the Phase 1
  demo.** Priority is a bijection, states have categories, bodies are already
  Markdown, and there are no custom fields. The conversation for a Linear import
  is short by design — which is exactly why it is the right first surface.
- **Attachments are always a second, authenticated fetch**, on every single
  source. Budget them explicitly and report bytes.

## 3. The framework

### 3.1 The pipeline

Six stages, one direction, each one independently testable:

    fetch → normalize → map → dry-run diff → apply → link back

- **fetch** — adapter-owned. Talks the source's protocol (Linear GraphQL,
  Jira REST v3 + Agile), handles that source's auth, pagination, rate limits
  and retries. Returns raw vendor structs. This is the only stage that knows
  the vendor exists.
- **normalize** — adapter-owned, framework-typed. Vendor structs become the
  **canonical shape** (§3.2). Body conversion (ADF → Markdown, BBCode →
  Markdown, none for Linear) happens here.
- **map** — framework-owned, config-driven. Canonical values become Agora
  values: statuses → `backlog|todo|in_progress|in_review|done|blocked|cancelled`,
  priorities → `urgent|high|medium|low|none`, users → member ids, containers
  → projects, iterations → sprints. Every mapping has an adapter default and a
  per-workspace override.
- **dry-run diff** — framework-owned. Runs everything above with **zero
  writes** and produces the `ImportPlan`: counts, the proposed mapping table,
  unmatched users, unmapped statuses, attachment byte total, and the
  create/update split against anything already linked. This artifact is the
  product.
- **apply** — framework-owned, job-backed. Walks the plan, upserts by external
  ref, records a per-entity outcome.
- **link back** — framework-owned. Writes the external ref on every created
  row so the next run is an update, and (optionally, source permitting) posts
  a comment or link on the source object pointing at the Agora issue.

The seam that matters: **the adapter never writes to the database.** It
returns canonical structs and nothing else. That is what makes a recorded
fixture a complete adapter test, and what keeps the multi-tenancy rules in one
place — every write goes through the framework's applier, which takes a
`workspace_id` and filters by it, exactly as every other query does.

### 3.2 The canonical shape

Deliberately smaller than any source. Anything the shape cannot hold is either
dropped with an accounted-for count in the report, or preserved verbatim in a
`raw` blob on the entity's linkage — never silently lost.

```go
// server/internal/imports/canonical.go
type Source struct {
    Kind string // "linear" | "jira" | "asana" | "clickup" | "trello" | "csv"
    Ref  string // the source workspace/site identifier, for display
}

type Bundle struct {
    Source      Source
    Users       []User        // every actor referenced anywhere in the bundle
    Containers  []Container   // Linear team/project, Jira project, Trello board
    Iterations  []Iteration   // Linear cycle, Jira sprint
    States      []State       // workflow states, with the source's own category
    Labels      []Label
    Issues      []Issue
    Truncated   map[string]int // entity kind -> how many were not fetched, and why
}

type Issue struct {
    ExternalID   string    // stable id; the upsert key
    Identifier   string    // human key: "ENG-142", "PROJ-77"
    URL          string
    Title        string
    BodyMarkdown string
    StateID      string
    Priority     Priority  // canonical 0..4, none..urgent
    CreatorID    string    // -> Users
    AssigneeID   string
    LabelIDs     []string
    ContainerID  string
    IterationID  string
    ParentID     string
    Estimate     *float64
    CreatedAt    time.Time
    UpdatedAt    time.Time
    CompletedAt  *time.Time
    Comments     []Comment
    Attachments  []Attachment
    Relations    []Relation   // typed: blocks | blocked_by | related | duplicate
    Raw          json.RawMessage // everything the shape could not hold
}
```

`Truncated` is load-bearing. A report that says "240 issues" when the token
could only see 190 of them is the failure mode that destroys trust in the
whole product. Jira alone produces it four different ways — archived issues
are absent from the JQL index entirely, issue-security schemes hide issues
with no error, role-restricted comments are silently omitted, and `total` no
longer exists so the headline number is an *estimate* to begin with. Every
adapter is required to count and name what it could not reach, and to mark
any count it cannot make exact. This is the same "truncation honesty" rule the
assistant prompt already enforces for tool results, applied to the one place
where a comfortable number would be most tempting.

### 3.3 Identity mapping

Resolution order for every external actor, first hit wins:

1. `user_external_identity(provider=<source>, external_id=<source user id>)` —
   a link made by a previous run, or by the user themselves.
2. `workspace.settings.import_identity_aliases` — the operator's explicit
   "this address is that person" override, lifted from
   `bitrix_identity_aliases`.
3. Email match against an existing **member of this workspace**, case-folded.
   Members only: matching against all users would let an import bind a
   stranger's account.
4. **Provision**, only when the operator turned it on for this run: create the
   user + member and link the external identity. Off by default — silently
   growing the member roster during an import is a billing surprise and a
   security surprise at once.
5. **Fallback: the source's import identity.** A single, global, per-source
   attribution user — `linear-import@linear.local`, `jira-import@jira.local`,
   following `bitrix-import@bitrix.local` — added as a member of the target
   workspace, with the real name preserved on the row's linkage blob and
   rendered as a provenance line.

**Rule, stated as a rule because it is the complaint every migration
generates:** an unmatched author is NEVER written as the importing user. The
Bitrix code fixed exactly this bug and the comment says so. The framework
makes it structural — the applier takes an `actor resolver`, and the resolver
has no access to the operator's user id at all.

Matching is proposed in the dry run and confirmed by a human. The report lists
every source user with its proposed resolution and how it was reached, and the
operator can correct any row in conversation before confirming.

### 3.4 Value mapping

Three tables, all the same shape: adapter default, per-workspace override,
both visible in the dry-run report.

- **Status.** The adapter maps using the source's own *category* first
  (Linear's `WorkflowState.type`, Jira's `statusCategory`) and the state name
  second, because categories are stable and names are not. Unmapped → the
  report lists them; the operator assigns them in conversation; the override
  lands in `workspace.settings.import_mapping.<source>.status`. Never drop an
  issue for an unknown status — `todo` is the "never dropped" default, same
  contract as `bitrix.MapStatus` and `zohoprojects.MapStatus`.
- **Priority.** Canonical 0..4. Both Linear and Jira have a five-ish priority
  ladder and Agora's is `urgent|high|medium|low|none`, so the default table is
  a straight ordinal map; overrides exist for the shops that renamed theirs.
- **Labels.** Name-matched to existing `issue_label` rows case-insensitively,
  created when absent, with the source colour when the source has one. A
  `source:linear` label is **not** added to every issue — provenance lives in
  the linkage blob, not in the label namespace the team has to look at forever.

### 3.5 External linkage and idempotency

One shape, on both entities, replacing the per-vendor columns:

```jsonc
// issue.metadata.external_ref  /  comment.external_ref
{
  "source": "linear",
  "id": "a1b2c3d4-…",          // the upsert key
  "identifier": "ENG-142",
  "url": "https://linear.app/acme/issue/ENG-142",
  "author": "Dana Wu",          // preserved name when the author was unmatched
  "imported_at": "2026-09-19T…",
  "import_id": "<import job uuid>"
}
```

- **Issues** upsert on `(workspace_id, metadata->'external_ref'->>'source',
  metadata->'external_ref'->>'id')`, reusing the JSONB containment lookup
  Bitrix already proved (`findIssueByBitrixTaskID`), with an expression index
  so it stays cheap at 50k rows.
- **Comments** get a real column pair, `external_source text` +
  `external_id text`, with a partial unique index on
  `(issue_id, external_source, external_id) WHERE external_source IS NOT NULL`.
  This is the fix for the thing that does not scale in the Bitrix design: a
  `bitrix_synced_comment_ids` **array on the issue** means every re-sync reads
  and rewrites an unbounded array, and a 400-comment Jira issue turns that into
  a hot row. A unique index does the same job in the database.
  `comment.bitrix_comment_id` is backfilled into the new columns and dropped
  in the same migration — there is no live external consumer of that column,
  so the compatibility-layer prohibition applies.
- **Re-import is an upsert, always.** Running the same import twice produces
  zero duplicates and a receipt of `created: 0, updated: N`. This is also what
  makes "import now, import again after we finish the sprint in Linear" a
  supported workflow rather than a mess.
- **Attachments** dedupe on `(issue_id, external_source, external_id)` too,
  and the applier never re-downloads a file it already has.

### 3.6 Attachments

Attachments are the most common thing importers quietly drop, and they are the
reason a dry run must report **bytes, not just counts**. The applier
streams each file from the source (with the source's auth, server-side — a
signed URL handed to a browser is a leak) into `Storage.Upload` and writes an
`attachment` row. Budgets are explicit and surfaced in the report: a per-file
size cap, a per-import total cap, and a per-issue count cap. Anything over
budget is **skipped and listed**, never silently truncated, and the original
URL stays in the linkage blob so a human can fetch it.

Attachments are also the right place to put the one honest warning the report
must carry: if the team cancels their Linear or Jira subscription, any file
Agora did not copy becomes unreachable. That sentence belongs in the dry-run
artifact, in bold.

### 3.7 The job model

A `import_job` row, not a package-level variable:

```sql
import_job(
  id uuid pk,
  workspace_id uuid not null references workspace on delete cascade,
  connection_id uuid references import_connection on delete set null,
  source text not null,                  -- linear | jira | …
  status text not null,                  -- pending|dry_run|awaiting_confirm|running|done|failed|cancelled
  scope jsonb not null,                  -- which teams/projects, date window, options
  plan jsonb,                            -- the dry-run result the human confirmed
  mapping jsonb,                         -- the mapping actually used (frozen at confirm)
  totals jsonb not null default '{}',    -- created/updated/skipped/failed per entity kind
  failures jsonb not null default '[]',  -- bounded list: {kind, identifier, reason}
  artifact_id uuid,                      -- the receipt artifact
  created_by uuid references "user",
  started_at, finished_at, created_at, updated_at
)
```

Properties that follow from it being a row: progress survives a restart;
two workspaces import concurrently; a second import in the same workspace is
refused with a pointer at the running one rather than cancelling it (the
Bitrix global cancels whatever was running, which is only safe because there
is one operator); the receipt is reconstructible after the fact; and the whole
thing is queryable per tenant like everything else.

Progress is published on the existing event bus as `import:progress` scoped to
the workspace, at a throttled cadence (every N rows or every 2s, whichever is
slower) so a 10k-issue import does not turn into 10k WS frames. The mistake to
avoid is the one already on record in the stress findings: synchronous fanout
on the write path.

Ordering inside `apply` is fixed and dependency-driven: users → containers →
labels → states → iterations → issues (parents before children, by a
topological pass) → comments → attachments → relations. Relations last, in
their own pass, because a blocks-link can point at an issue that only exists
after the pass that creates it.

### 3.8 Credentials

The source token is a secret, and the repo already has exactly one right way
to hold one.

```sql
import_connection(
  id uuid pk,
  workspace_id uuid not null references workspace on delete cascade,
  source text not null,                     -- linear | jira | …
  label text not null default '',
  base_url text not null default '',        -- Jira site URL; empty for Linear
  account_email text not null default '',   -- Jira basic-auth email
  secret_encrypted bytea not null,          -- secretbox-sealed API token / refresh token
  scopes text not null default '',
  probe_status text not null default '',    -- ok | invalid | unreachable
  probed_at timestamptz,
  created_by uuid references "user",
  created_at, updated_at,
  unique (workspace_id, source, label)
)
```

- Sealed with `secretbox` (AES-256-GCM) under a new registry secret
  **`AGORA_IMPORT_SECRET_KEY`**, following `AGORA_GIT_SECRET_KEY`,
  `AGORA_ZOHO_SECRET_KEY`, `AGORA_LARK_SECRET_KEY`, `AGORA_MCP_SECRET_KEY`.
  Registered in `config.Registry` as `KindSecret`, so Settings → Configs shows
  set/not-set and never the value.
- **Fail closed.** If the key is unset the write endpoints answer 503 rather
  than store plaintext — the exact behaviour of `gitCredentialBox()` /
  `zohoConnectionBox()`.
- Plaintext is decrypted server-side only, inside the adapter's HTTP client,
  and is never returned by any endpoint, logged, or put in an event payload.
- Creating a connection is owner/admin-gated and `RequireHumanActor`.

**And the token never touches the assistant.** `ExcludedCapabilities` in
`server/internal/assistant/tools.go` already carries exactly one standing "no",
and it is this one: *"Accepting a raw secret VALUE — an API key, an access
token … the chat transcript is persisted, so a secret pasted into it outlives
the conversation."* An import token clears that bar identically. So the
assistant's import tools take a `connection_id` and nothing else; when no
connection exists, the assistant does what it already does for provider keys —
names the page, explains what to paste, and waits. The brief's phrasing
("user pastes a token to the assistant") is the one thing in this design that
must be inverted: the user pastes it into **Settings → Import**, and then
talks to the assistant about it.

## 4. Migration as a conversation

### 4.1 The flow

1. **Connect.** Settings → Import → Linear → paste a personal API key (or run
   OAuth). The endpoint probes it immediately and stores
   `probe_status`, so a bad token fails in two seconds instead of at row 4000.
2. **Survey.** The user says "import our Linear workspace" — or clicks
   **Preview import**, which sends the same message. The assistant calls
   `dry_run_import(connection_id, scope?)`. The server runs stages 1–4 of the
   pipeline and returns a compact summary; the model renders a **markdown
   artifact**, the *migration report*:

   > **Linear → Acme (dry run)**
   > 3 teams · 240 issues · 1,180 comments · 8 members · 1.2 GB of attachments
   >
   > **Projects**: Engineering → new project *Engineering*; Design → new
   > project *Design*; Growth → merge into existing *Growth*
   > **Statuses**: 11 of 13 mapped. Unmapped: *Waiting on customer*,
   > *Won't fix* — propose `blocked` and `cancelled`.
   > **People**: 6 of 8 matched by email. Unmatched: *Dana Wu*,
   > *ex-contractor@…* — will import as **Linear (imported)** with names
   > preserved unless you map them.
   > **Not reachable with this token**: 1 private team (12 issues).
   > **Attachments**: 1.2 GB, 340 files; 2 files over the 100 MB cap will be
   > linked, not copied.
   > **Re-run safe**: 0 of these issues are already in Agora.

3. **Argue with it.** "Dana is dana@acme.com." "Put Growth's issues in
   Engineering." "Skip anything closed before 2025." "Don't copy attachments."
   Each is a normal turn; the assistant calls `update_import_mapping` and
   **revises the same artifact** rather than minting a new one — the artifact
   revision machinery from migration 201 is already there for this.
4. **One confirm.** `confirm_import` parks a pending operation whose summary is
   the plan's headline; the user clicks Confirm once; the server starts the
   job. Nothing before this point wrote anything.
5. **Watch it.** A progress line in the artifact pane, fed by `import:progress`.
6. **Receipt.** On completion the job writes a second artifact — the *import
   receipt*: per-project created/updated counts, per-entity skips with
   reasons, the failure list, the unmatched-author list with a one-click way
   to map them, and the "here is what to do in Linear now" paragraph. Pinnable
   to a project (Phase 2a machinery) so the whole team can read it.

### 4.2 Where `propose_plan` fits — and where it does not

It does not fit the rows, and that is the important design call.

`propose_plan` is capped at `MaxPlanItems = 25` and its allowlist
(`PlanAllowedTools`) is deliberately short; the prompt tells the model that a
bigger job is proposed *in slices*, with the remainder named. A 240-issue
import is not 240 plan rows — it is ten confirmations of twenty-five rows each,
which is worse than the wizard we are trying to beat, and it would put
`create_issue` volume through the interactive assistant run loop, which is not
what that loop is for.

What *does* generalize is the **shape**: propose → render → one human gesture
→ execute → per-row receipt. Import reuses the machinery one level up:

| Plan (Phase 3a) | Import |
| --- | --- |
| `propose_plan` parks an `assistant_pending_operation`, `kind:"plan"` | `confirm_import` parks an `assistant_pending_operation`, `kind:"import"` |
| Items = N tool calls, ≤ 25 | Items = **one job**; the rows live in `import_job.plan` |
| Rendered as a checklist card | Rendered as the migration-report artifact + a confirm card |
| Confirm replays stored calls through the executor, in-band | Confirm **starts the job**; execution is server-side and out-of-band |
| Receipt = per-item outcome rows on the operation | Receipt = `import_job.totals/failures` + the receipt artifact |
| `RequireHumanActor` on confirm | identical |
| Allowlist re-checked at confirm | connection + scope + mapping re-checked at confirm; the mapping is **frozen** into the job at that moment |

Two properties carry over verbatim and must: **calling the propose tool
changes nothing**, and the model must not claim the work is done after
proposing it. Both are already written into `writePlanGuidance`; the import
prompt section says the same in the same voice.

The one genuine extension: a plan is in-band and synchronous, an import is
neither. So `confirm_import`'s response is `{job_id, status:"running"}` and the
model's job after that is to say what is happening and stop talking — not to
poll.

### 4.3 The assistant tool surface

Five tools, one of them a write, none of them touching a secret:

| Tool | Kind | Args | Returns |
| --- | --- | --- | --- |
| `list_import_connections` | read | `{workspace_id?}` | connections with `source`, `label`, `probe_status` — never the secret |
| `dry_run_import` | read (no writes) | `{connection_id, scope?: {containers?, since?, include_archived?}}` | counts, proposed mapping, unmatched users, unreachable set, attachment bytes |
| `update_import_mapping` | write (settings) | `{connection_id, status?, priority?, users?, containers?}` | the merged mapping |
| `confirm_import` | **confirmation-bound** | `{connection_id, scope, options}` | `needs_confirmation` → on confirm, `{job_id}` |
| `import_status` | read | `{job_id}` | status, totals, failures, receipt artifact id |

`confirm_import` joins `DestructiveTools` — not because an import destroys
anything, but because the confirmation-binding path is the product's one
mechanism for "a human read this and pressed a button", and a job that writes
thousands of rows into a workspace deserves it. It is **not** added to
`PlanAllowedTools`: an import is never a row inside somebody else's plan.

`update_import_mapping` writes to `workspace.settings` via the existing
key-scoped merge query, so it inherits the workspace role gate. It is the one
place the standing "no settings writes in plans" rule needs a carve-out
argument — and it does not need one, because it is not a plan item.

### 4.4 Prompt section

One new `writeImportGuidance` block in `server/internal/assistant/prompt.go`,
in the voice of `writePlanGuidance`, written against the three failure modes
this flow actually has:

- **Confirming before surveying.** Never call `confirm_import` in the same
  turn as `dry_run_import`. The report is the thing the human authorizes; a
  confirm that arrives before they have read it is a plan card with the box
  pre-ticked.
- **Rounding the bad news off.** The unreachable set, the unmatched authors,
  the skipped attachments and the size caps are reported **first**, in the
  artifact, in the user's own numbers. A migration report that leads with
  "240 issues found!" and buries "12 we cannot see" is the single fastest way
  to lose a team in week two.
- **Narrating a job it cannot see.** After confirm, say what was started and
  where the receipt will appear. Do not invent progress and do not poll
  `import_status` in a loop.

And one arithmetic rule, because one source makes it unavoidable: **a count the
adapter marked as an estimate is reported as an estimate.** Jira's search API
no longer returns a total; the number comes from `approximate-count` and
Atlassian calls it approximate. "About 4,800 issues" is the honest headline,
and the receipt reports the real figure once the walk is done.

## 5. Bridge or cut-over

Three postures, and the honest answer differs by source.

| Posture | What it is | Cost |
| --- | --- | --- |
| **Cut-over** | One-shot import, re-runnable until the team stops working in the old tool | The pipeline, and nothing else |
| **Read-only mirror** | Import + a poll that keeps imported issues fresh; Agora writes nothing back | Pipeline + poller + freshness rules |
| **Two-way bridge** | Mirror + outbound status/comment push, with a declared source of truth | Everything above + conflict policy + an outbound allowlist |

**Linear: clean cut-over.** Linear's audience is small, fast teams who move as
one unit, own their whole workspace, and are choosing Agora precisely because
they want one tool. A bridge would be scaffolding nobody keeps. Ship the
one-shot, make re-running it free, and let the team run both for a week by
re-importing — which is exactly what idempotent upsert buys.

**Jira: bridge, yes.** Jira lives in organizations where one squad can choose
Agora and the other forty cannot, where Jira is wired to compliance,
reporting, and release management, and where "turn Jira off" is not a decision
the buyer is allowed to make. This is the same shape as the Bitrix deployment,
which is why Bitrix is a two-way sync in the first place — and it is Agora's
land-and-expand path into big accounts. Sequence it as: one-shot import first
(Phase 2), read-only mirror second, two-way third, gated behind a declared
source of truth per project.

**The others: cut-over only.** Asana, ClickUp, Trello and CSV are one-shot
territory. A team leaving Trello is leaving Trello.

The bridge tier reuses the Bitrix reconciliation design unchanged in spirit:
webhook for latency, poll for truth, dedupe on external id, and an outbound
side that is fail-closed and explicitly enumerated rather than "mirror
everything".

## 6. Phasing

### Phase 1 — Linear, one-shot

Smallest surface, closest audience: Linear's users are Agora's ICP almost
exactly — small software teams that already believe a tracker should be fast.
Everything in §3 gets built, but only enough of it to serve one adapter.

**Two scope decisions up front, both from §2.1.** Auth is the **personal API
key only** — OAuth access tokens expire in 24 hours and an import job must not
be able to outlive its own credential. And the source is the **GraphQL API
only** — Linear's CSV export carries no comments and no attachment files, so it
cannot produce the thing a migration is for.

**Migrations** (3)

- `205_import_connection` — the sealed credential table (§3.8).
- `206_import_job` — the job row (§3.7).
- `207_comment_external_ref` — `comment.external_source` + `external_id`,
  partial unique index, backfill from `bitrix_comment_id`, drop the old
  column; plus the expression index on `issue.metadata->'external_ref'` and
  the `issue_dependency.type` CHECK widened to include `duplicate`
  (`similar` and Jira's installation-defined link types downgrade to
  `related`).

**Backend**

    server/internal/imports/            # NEW — the framework
      canonical.go      # Bundle/Issue/User/... + Truncated
      mapping.go        # status/priority/label/user mapping + overrides
      plan.go           # dry-run diff -> ImportPlan
      apply.go          # ordered applier, upsert by external ref
      job.go            # job row lifecycle + throttled progress events
      identity.go       # the 5-step resolver + per-source import identity
    server/internal/imports/linear/     # NEW — the adapter
      client.go         # GraphQL client: `Authorization: <key>` (NO Bearer),
                        # cursor paging, and an ADAPTIVE page size driven by the
                        # X-Complexity / X-RateLimit-Complexity-Remaining headers
                        # so a query never approaches the 10,000-point ceiling
      fetch.go          # shallow issue pages, then comments/attachments/relations
                        # in their own passes (nesting multiplies complexity)
      normalize.go      # vendor structs -> canonical Bundle
      linear_test.go    # recorded-fixture tests (httptest.NewServer)
    server/internal/handler/
      import_connections.go   # NEW  CRUD + probe, secretbox, fail-closed 503
      import_jobs.go          # NEW  dry-run, confirm, status, cancel
      assistant_imports.go    # NEW  the 5 tool executors
      assistant_tools.go      # tool constants + DestructiveTools entry
    server/internal/assistant/
      tools.go          # 5 tool specs
      prompt.go         # writeImportGuidance
    server/internal/config/registry.go  # AGORA_IMPORT_SECRET_KEY (secret),
                                        # AGORA_IMPORT_ENABLED, caps
    server/cmd/server/router.go         # route wiring

**Endpoints** (workspace-scoped, `X-Workspace-ID`, membership gate; writes
owner/admin + `RequireHumanActor`)

    GET    /api/workspaces/{id}/import/connections
    POST   /api/workspaces/{id}/import/connections        {source, label, secret, base_url?}
    POST   /api/workspaces/{id}/import/connections/{cid}/probe
    DELETE /api/workspaces/{id}/import/connections/{cid}
    POST   /api/workspaces/{id}/import/dry-run            {connection_id, scope} -> ImportPlan
    POST   /api/workspaces/{id}/import/jobs               {connection_id, scope, mapping} -> 202 {job_id}
    GET    /api/workspaces/{id}/import/jobs/{jid}
    POST   /api/workspaces/{id}/import/jobs/{jid}/cancel

**Frontend**

    packages/core/imports/{index,queries,mutations,types}.ts   # mirrors core/bitrix
    packages/core/api/schemas.ts        # zod schemas; parseWithFallback on every response
    packages/core/paths/paths.ts        # imports: () => `${ws}/import`
    packages/views/imports/             # ImportPage: connect -> preview -> mapping -> run
      components/import-connect-card.tsx
      components/import-preview.tsx     # the mapping tables, editable
      components/import-progress.tsx
    packages/views/settings/components/import-tab.tsx   # Integrations card -> "Open import"
    apps/web/app/[workspaceSlug]/(dashboard)/import/page.tsx   # 1-line re-export
    apps/desktop/src/renderer/src/routes.tsx                   # path "import"
    packages/views/locales/{en,zh-Hans,ru,uz}/imports.json      # parity.test.ts green

**Tests**

- `server/internal/imports/linear/linear_test.go` — a recorded Linear GraphQL
  fixture served by `httptest.NewServer`, following
  `zohoprojects_test.go`'s mock-host idiom: canned responses for viewer,
  teams, workflow states, labels, a paged issue connection with comments and
  attachments, and a second page proving cursor paging. The fixture is the
  contract; the adapter test never touches the network or the database.
- `server/internal/imports/*_test.go` — mapping defaults and overrides;
  identity resolution through all five steps, **with an explicit test that an
  unmatched author is not the operator**; plan diffing (fresh vs already-linked);
  applier ordering with parents and relations.
- `server/internal/handler/import_jobs_test.go` — 503 when the seal key is
  unset; role gate; `RequireHumanActor` on confirm; re-run produces
  `created: 0, updated: N`; a malformed adapter response degrades to a
  reported failure rather than a panic.
- `packages/core/api/schemas.test.ts` — the malformed-response test CLAUDE.md
  requires for every new endpoint: missing field, wrong type, `null` array.
- `packages/views/imports/*.test.tsx` — the preview renders unmatched users and
  unmapped statuses prominently; jsdom, `@agora/core` mocked.
- One e2e (`e2e/tests/import-linear.spec.ts`) against a stubbed Linear host.

**Scope estimate — 5 PRs, ~60 files**

| PR | Content | Files |
| --- | --- | --- |
| 1 | Migrations 205–207 + sqlc queries + explicit-timestamp create queries | ~10 |
| 2 | `imports` framework (canonical, mapping, identity, plan, apply, job) + tests | ~12 |
| 3 | Linear adapter + recorded fixture tests | ~6 |
| 4 | Handlers, endpoints, router, config registry + handler tests | ~10 |
| 5 | Assistant tools + prompt + frontend (core module, views, both apps, 4 locales) + tests | ~22 |

PRs 2 and 3 are parallelizable after 1. PR 5 is the one that touches both
apps and therefore the one that must not be rushed past the No-Duplication
Rule: the import page is shared-package UI with a one-line re-export in each
app, exactly like `bitrix`.

### Phase 2 — Jira Cloud

Same framework, second adapter, and the first real test of whether the
canonical shape was drawn correctly. New work beyond a copy of Phase 1:

- **The two-phase fetch.** Page `search/jql` with no `fields` to collect issue
  ids, then `issue/bulkfetch` in batches of 100 for bodies — checking
  `issueErrors[]` on every 200, because partial success is not a status code.
  Comments, changelog (`changelog/bulkfetch`, never `expand=changelog` with its
  silent 40-entry cap) and worklogs are separate passes.
- **Resumption that ignores cursors.** `nextPageToken` expires in 7 days and
  has a public defect history; a resumed job re-walks and upserts.
- **ADF → Markdown conversion** (adapter-owned, the analogue of `bbcode.go`),
  with a fixture suite covering panels, code blocks, tables, mentions, emoji
  and media nodes, an explicit plain-text fallback for unhandled node types,
  and a degraded-node count in the receipt.
- **The Agile API as a second client** (`/rest/agile/1.0/`) for sprints,
  boards and epics — plus reconciliation against the redundant
  `customfield_*` copies that surface on the issue itself.
- **Attachment download** through `/attachment/content/{id}`, following the
  redirect to Atlassian's media CDN **with the Authorization header preserved
  across the hop** — a client that drops it fails silently.
- **Custom fields**: a declared allowlist mapped to labels or appended to the
  description, everything else preserved in `Raw` and counted in the report.
- **Identity without email.** Display-name matching plus an explicit human
  mapping step in the conversation, because `accountId` is opaque and email is
  usually withheld. This is the piece that must be built well, not quickly.
- **The blind-spot report.** Archived issues (unreachable via JQL at all),
  issue-security-hidden issues, role-restricted comments, and the fact that the
  headline count is an `approximate-count` estimate — all surfaced as
  `Truncated` and rendered at the top of the dry-run artifact.
- **A conservative token bucket** well under the 100 req/s burst cap, honouring
  `Retry-After`, because a Jira import shares a rate-limit budget with every
  other integration in that customer's org.
- **OAuth 3LO** as an alternative to an API token, since enterprise buyers will
  not hand a personal token to a vendor — and since scoped API tokens must talk
  to `api.atlassian.com/ex/jira/{cloudId}` rather than the site domain.
- Then, separately, the bridge tier (§5).

### Phase 3 — generic CSV and the rest

A CSV adapter is the universal escape hatch: every tool exports one, and it
makes Asana / ClickUp / Trello / Monday / "our old spreadsheet" a
column-mapping exercise instead of four more API clients. The framework work
is a mapping UI that binds source columns to canonical fields; the adapter
itself is trivial. Native adapters for Asana, ClickUp and Trello follow only
where the CSV loses something the team actually asks about.

## 7. Growth mechanics

The importer is not a settings page. It is a landing page, an onboarding step,
and an artifact the team reads.

- **Onboarding.** `packages/views/onboarding/` is a persisted, ordered flow
  (welcome → workspace → runtime). Add **one optional row on the workspace
  step**, not a new step: "Bring your issues — Linear · Jira · CSV", which
  deep-links into Settings → Import and returns to the flow. Not a step,
  because a mandatory migration screen in front of a solo user evaluating the
  product is a wall; a row they can ignore costs nothing.
- **Empty states.** The issues list of a brand-new workspace says "no issues
  yet" today. It should also say "or import them from Linear". That is the
  single highest-intent surface in the product.
- **The receipt as the announcement.** The import receipt artifact is the
  migration announcement: what came over, what did not, where to find things,
  what changed in the team's vocabulary (Linear cycles are Agora sprints,
  Linear projects are Agora projects), and one line of "here is what to do
  next". Pin it to a project and every member reads the same page. This is a
  thing no competitor's importer produces, because none of them has an
  artifact system to produce it with.
- **Public proof.** A docs page — `apps/docs/content/docs/importing-from-linear.mdx`
  (+ `.zh`) — that states exactly what is imported, what is not, and what
  happens to attachments. Migration pages rank, and the honest one wins the
  comparison against the vendor who lists only the good news.
- **The second-order effect.** Once a workspace has a history, the agents have
  a corpus: the KB flywheel has real issues to learn from on day one instead of
  week six. An imported workspace is a *better* demo of what Agora is for than
  an empty one, which is the actual reason this is the highest-leverage
  feature on the list.

## 8. Non-goals

- No migration **out** of Agora in this plan (worth building eventually as a
  trust signal; not now).
- No two-way sync for Linear, ever, unless a customer pays for it.
- No preservation of issue *history* as history — Linear's `IssueHistory` and
  Jira's changelog are imported as a **compact provenance summary**, not
  replayed into `activity_log` as fake events with fake actors. Faking the
  audit trail is worse than not having it.
- No custom-field system in Agora to receive Jira's custom fields. Allowlist,
  map, or preserve in `Raw`.
- No background re-import on a schedule in Phase 1. Re-running is a button.
- No importing of Linear/Jira *users* as Agora agents. Agents are created
  deliberately; an imported bot account is a member or it is the import
  identity.

## Appendix — sources

All fetched **2026-09-19**. Where a claim in this document conflicts with
something remembered rather than fetched, the fetched page wins.

**Linear**

- `https://linear.app/developers/graphql` — endpoint URL; `Authorization: <key>`
  with no `Bearer` prefix for personal API keys.
- `https://linear.app/developers/rate-limiting` — the rate table (2,500 req/hr +
  3M complexity for API keys; 5,000 + 2M for OAuth; 600/hr unauthenticated),
  the 10,000-point per-query ceiling, the complexity formula, and the
  `X-Complexity` / `X-RateLimit-*` headers.
- `https://linear.app/developers/pagination` — Relay `first`/`after` cursors,
  default page size 50, no documented universal maximum.
- `https://linear.app/developers/oauth-2-0-authentication` — OAuth flow, the
  scope list, **24-hour access tokens**, the 1 April 2026 refresh-token
  migration, and 30-day client-credentials tokens for non-interactive use.
- `https://linear.app/developers/oauth-actor-authorization` — `actor=app`
  (successor to `actor=application`) attribution mode.
- `https://linear.app/developers/attachments` — the `Attachment` type and its
  queries.
- `https://linear.app/developers/file-storage-authentication` —
  `uploads.linear.app` requires the API token; the
  `public-file-urls-expire-in` request header for pre-signed URLs.
- `https://linear.app/docs/exporting-data` — CSV export: 12-hour link expiry,
  250-issue member cap / 2,000 admin cap, and the full field list (no comments,
  no attachment files).
- `https://linear.app/docs/import-issues` — supported sources, admin
  requirement, and the "same email address" authorship condition.
- `https://linear.app/docs/jira` — the Jira importer's documented losses:
  Issue Type → Task, Constraints dropped, Components → `"Component: X"` labels,
  required-field sync blocking, unmatched users land unassigned.
- `https://github.com/linear/linear/blob/master/packages/import/README.md` —
  the per-source field-mapping table for the legacy `@linear/import` CLI.
- `raw.githubusercontent.com/linear/linear/master/packages/sdk/src/schema.graphql`
  — the highest-confidence source used. Confirmed the root query fields;
  `Issue.parent`/`children`; `IssueHistory` and `stateHistory`;
  `IssueRelationType { blocks, duplicate, related, similar }`; that
  `IssueCreateInput` and `CommentCreateInput` both carry `createAsUser` and
  `displayIconUrl` gated to `actor=app`; and the priority doc-comment
  *"0 = No priority, 1 = Urgent, 2 = High, 3 = Medium, 4 = Low."*
- `https://github.com/linear/linear/issues/1000` — "Import Linear CSV keep
  importing user as issue creator": *"After import, I get all issues are
  created by the user that created the API key."* Closed **not planned**.
- `https://github.com/terrastruct/byelinear` — open-source Linear → GitHub
  migrator; documents that migrated comments *"appear from the importing
  account"*, that timestamps are not preserved, and that its write step is not
  resumable.
- `https://cotera.co/articles/linear-vs-jira-comparison` — a 2,147-issue
  Jira → Linear migration account: lost custom workflows, 23 dead JQL filters,
  no time tracking.
- `https://wiki.jamesravey.me/books/jira/…` — community Linear → Jira guide;
  source of the "Jira's importer needs attachments at a public URL" gotcha.
- `https://linear.app/switch/migration-guide` — confirms the guide is
  import-only; Linear documents no exit path.

**Jira Cloud**

- `https://developer.atlassian.com/cloud/jira/platform/rate-limiting/` — the
  points quota by tier, the 100 req/s burst cap, per-issue write limits, and
  the 429 `RateLimit-Reason` / `Retry-After` headers.
- `https://developer.atlassian.com/cloud/jira/platform/scopes-for-oauth-2-3LO-and-forge-apps/`
  — classic vs granular read scopes, and *"Jira permissions also control access
  to data and aren't overridden by scopes."*
- `https://developer.atlassian.com/cloud/jira/platform/basic-auth-for-rest-apis/`
  — API-token creation and the Basic header format.
- `https://developer.atlassian.com/cloud/jira/platform/apis/document/structure/`
  — ADF root shape; confirms no official format converter is documented.
- `https://developer.atlassian.com/cloud/jira/platform/swagger-v3.v3.json` —
  the v3 endpoint inventory used for §2.2's reference-data list.
- `https://community.developer.atlassian.com/t/rfc-61-evolving-search-capabilities-…/83027`
  — Atlassian's own rationale for removing `startAt` and `total`, the page-size
  range, and the 7-day token expiry.
- `https://community.atlassian.com/forums/Jira-questions/When-are-JQL-search-endpoints-…/qaq-p/3029221`
  — the `/search` deprecation and shutdown timeline (May 2025 → end of October
  2025).
- `https://community.atlassian.com/forums/Jira-articles/Avoiding-Pitfalls-A-Guide-to-Smooth-Migration-to-Enhanced-JQL/ba-p/2985433`
  — `nextPageToken` mechanics and the recommended two-phase ids-then-bulkfetch
  bulk pattern.
- `https://community.atlassian.com/forums/Jira-questions/REST-The-new-rest-api-3-search-jql-endpoint-is-a-complete/qaq-p/3101716`
  — the ~100k-view defect thread: *"`isLast` never returns true, `nextPageToken`
  chains endlessly"*, and token-expiry reports.
- `https://community.developer.atlassian.com/t/bulk-fetch-changelogs-experimental-api/87240`
  — `changelog/bulkfetch`, and the 40-entry cap that `expand=changelog`
  silently imposes.
- `https://community.atlassian.com/forums/Jira-Cloud-Admins-articles/Burst-API-Rate-Limits-…/ba-p/3219856`
  — *"one user doing a migration … could consume the app's budget and disrupt
  the app for everyone else in that same organization."*
- `https://support.atlassian.com/jira/kb/export-over-10-000-work-items-in-jira-cloud/`
  — the async CSV export ceiling and the split-by-JQL workaround.
- `https://support.atlassian.com/jira/kb/export-jira-project-attachments-using-rest-api/`
  — attachment content download needs redirect-following (`curl -L`) with auth
  preserved across the redirect.
- `https://confluence.atlassian.com/jirakb/resolving-email-visibility-issues-in-jira-cloud-rest-api-responses-1528536519.html`
  — the two-layer (site setting + user profile) gate that makes email usually
  unavailable.
- `https://community.atlassian.com/forums/Jira-Service-Management/Jira-REST-API-returns-archived-issues-but-does-not-expose-archive/qaq-p/3170667`
  and `https://jira.atlassian.com/browse/JRACLOUD-94071` — archived issues are
  excluded from the JQL index with no public way to enumerate them.
- `https://support.atlassian.com/migration/docs/what-gets-migrated-with-the-jira-cloud-migration-assistant/`
  — JCMA's own not-migrated list (dashboards, webhooks, avatars, activity
  stream, cross-project boards/filters).
- `https://krevt8mwkh.apidog.io/bulk-fetch-issues-19180038e0` and
  `…/count-issues-using-jql-19180197e0` — mirrored specs for
  `issue/bulkfetch` (100-issue cap, `issueErrors[]` on a 200) and
  `search/approximate-count` (bounded JQL required, estimate only).

**Asana / ClickUp / Trello**

- `https://developers.asana.com/docs/rate-limits` — cost-based 150 / 1,500
  req/min tiers, the 60 req/min Search cap, and that 429s still consume quota.
- `https://developers.asana.com/docs/authentication` — PAT vs OAuth scoping.
- `https://developer.clickup.com/docs/rate-limits` — 100 / 1,000 / 10,000
  req/min by plan.
- `https://developer.clickup.com/docs/general-v2-v3-api` — v3 rolls out
  endpoint by endpoint, not as a parallel API.
- `https://developer.clickup.com/docs/authentication` — personal token and
  OAuth flows.
- `https://developer.atlassian.com/cloud/trello/guides/rest-api/rate-limits/` —
  300 req/10s per key, 100 req/10s per token, 100 req/900s on `/1/members/`.
- `https://developer.atlassian.com/cloud/trello/guides/rest-api/authorization/`
  — key + token auth.
- `https://support.atlassian.com/trello/docs/exporting-data-from-trello/` —
  the 1,000-action ceiling on JSON export; Premium CSV excludes comments.

**Explicitly unverified**

- Linear's maximum `first` on the standard connections (only `templateSearch`
  documents a 250 cap; do not generalize it).
- Practical wall-clock throughput for a 10k-issue Linear export — an estimate
  from the complexity formula, not a documented figure. Measure against
  `X-Complexity` before promising a runtime.
- Whether the `search/jql` pagination defects reported in late 2025 persist
  today.
- Exactly which Linear queries require the `admin` OAuth scope.
- The reconciliation between Jira's legacy 1,000-row CSV limit and the newer
  10,000-item async export — these appear to be different export surfaces.
- Trello's current full `?fields=all&actions=all` response schema (the docs
  page is JS-rendered and could not be fetched).
