# Slack integration plan — the channel Agora reports into

Status: research + design · Owner: Jamshid · Drafted 2026-09-19
Research verified against api.slack.com / docs.slack.dev on 2026-09-19 (Appendix A).

## Thesis

Agora's target team is 2-10 people who ship software, and that team already has
one room where work is discussed: a Slack channel. Today Agora is a place they
must remember to open. Every integration we have built so far — Lark, Telegram,
Bitrix — serves a market Agora reaches through SalesDoctor. Slack is the one
that serves the market Agora is *sold* into, and it is the only distribution
surface where a non-user watches Agora do work without ever logging in.

The product asset that makes this more than a notification firehose is the
assistant's **pinned report with a scheduled refresh** (Phase 2b, shipped,
migration 204). Every competitor's Slack app pushes *events*. None of them
pushes a **standing weekly report that an AI regenerates from live data**. A
Monday 09:00 sprint report landing in `#eng` — done/total, blocked list, owners,
a link — is the demo that sells Agora, and the machinery for it already exists;
only the last hop to Slack is missing.

So the order is deliberate: notifications earn the install (Phase 1), reports
are the reason anybody keeps it (Phase 2), two-way is what turns Slack into an
input surface (Phase 3). Each phase ships alone and is useful alone.

## What already ships (and what it is not)

`server/internal/integrations/slack/client.go` (71 lines) is a **complete,
working Incoming-Webhook client** — not a stub. Its one caller is the release
connector registry: `releaseSlackConnector` at
`server/internal/handler/release_connectors.go:153`, registered by
`releaseConnectorFor` at `:63-67`, storing a sealed `{webhook_url}` in
`release_integration.secret_encrypted` (migration 158) and firing on
`deploy:recorded` / `release:shipped` via `DispatchReleaseEvent`
(`server/internal/handler/release_outbound.go:64`).

That is Slack-as-a-webhook: one channel, no identity, no read path, no
interactivity, plain `{"text": ...}`. It is genuinely useful for a team that
wants deploy pings and nothing else, and it is **not** superseded by this plan.

**Decision (made, not open): the release connector keeps `release:shipped` and
`deploy:recorded`; the Slack app never claims those events.** There is then no
double-post to reconcile, no migration of existing `release_integration` rows,
and no compatibility shim — the two paths address disjoint event sets. If the
app later wants release events it takes them by the owner deleting the webhook
row, not by us silencing one path from the other.

## The template: what Lark and Telegram already solved

Two live chat integrations, three years of scar tissue. This table is the
honest inventory of what carries to Slack and what does not.

| Piece | Lark | Telegram | Carries to Slack? |
| --- | --- | --- | --- |
| Sealed per-tenant credential | `lark_installation.app_secret_encrypted`, `AGORA_LARK_SECRET_KEY` | `telegram_installation.bot_token_encrypted`, `AGORA_TELEGRAM_SECRET_KEY` | **Yes, verbatim.** `AGORA_SLACK_SECRET_KEY` + `secretbox`, service refuses a nil box (`lark/installation.go:47-49`) |
| Composite-FK tenant fencing | `UNIQUE(id, workspace_id)` as an FK target (`109_lark_integration.up.sql:48-51`) | — | **Yes.** Every child row (routes, thread links) FKs `(installation_id, workspace_id)` so a row cannot claim a workspace its installation does not belong to |
| Degraded mode is explicit and logged | no seal key ⇒ 503s + `configured:false`; broken WS config ⇒ `noop` connector named in the boot log (`router.go:1579`, `:305`) | `ErrTelegramSealKeyMissing`, `TelegramBotsEnabled` on `/api/config` | **Yes.** Missing any of the four Slack keys ⇒ `SlackEnabled:false`, tab hidden, endpoints 503 |
| Install flow | RFC 8628 device flow, QR scan, in-process session, `go runPolling` (`registration_service.go:423`) | operator pastes a BotFather token, `getMe` verify before seal (`telegram_installation.go:148`) | **No.** Slack has a real OAuth v2 redirect flow. No polling, no QR, no in-process session map — a signed `state` and a callback |
| Inbound transport | WebSocket long-conn; the wss URL *is* the credential, no public ingress needed (`ws_endpoint.go:28-42`) | webhook when public, long-poll when not (`auth_telegram.go:359-389`) | **Partly.** Slack has both (Socket Mode / HTTP Events). Agora's backend is publicly reachable → **HTTP Events**; Socket Mode is disqualified (Appendix A2) |
| Inbound verification | none at HTTP level — there is no Lark webhook route | optional `X-Telegram-Bot-Api-Secret-Token` (`auth_telegram.go:65`, `:261`) | **No — Slack is stricter.** HMAC-SHA256 over `v0:ts:body` with a 5-minute window. Closest precedent is the GitHub-style `verifyHubSignature` (`autopilot_webhook.go:271-273`), reused shape, different base string |
| Multi-replica safety | DB lease CAS per installation, 90s TTL, watchdog closes the socket on ctx cancel (`hub.go:643`, `ws_connector.go:242-252`) | one `getUpdates` consumer per bot, poller registry cancels the prior loop (`telegram_agent_chat.go:99-107`) | **Not needed.** HTTP events land on whichever replica the load balancer picks; there is no long-lived connection to lease. This is the single largest complexity saving vs Lark |
| Outbound notify seam | `bus.Subscribe(EventInboxNew, …)` in `cmd/server/lark_push_listeners.go:26` | `cmd/server/telegram_push_listeners.go:27` and four more | **Yes, exactly.** `cmd/server/slack_push_listeners.go` is a direct transliteration |
| Mute inheritance | push hangs off `inbox:new`; a muted notification never creates an inbox item (`lark_push_listeners.go:12-17`) | same (`telegram_push_listeners.go:16-18`, `notification_listeners.go:97`) | **Yes — and it is the whole noise rule.** See Phase 1 |
| Suppression flag | — | `EventPayloadSuppressExternalNotifications` checked at `telegram_push_listeners.go:82` | **Yes.** Honored on every Slack fanout |
| Throttle | — | progress relay: 5-min start delay, 5-min floor, post only when the headline changed (`telegram_progress.go:29-67`) | **Yes, generalized.** Slack's `chat.postMessage` is 1 msg/sec/channel — a per-channel bucket is mandatory, not optional |
| Destination resolution | assignee agent's bot → its bound chat (`lark_notify.go:65-82`) | 3-tier precedence resolver (`telegram_issue_destination.go:24-33`) | **No.** Slack routing is *configured*, not derived: a workspace admin picks channels. Telegram's resolver exists because a bot belongs to an agent; a Slack app belongs to the workspace |
| Card/interactive rendering | JSON cards + `card_action.go` (`set_status`, `qa_pass`, `qa_fail`, `assign_me`) patching the card after a 200 ACK | inline keyboards + `telegram_question` rows | **Yes in spirit, different wire.** Block Kit + a `/slack/interactions` endpoint; Lark's "ACK fast, patch async" discipline is the right instinct because Slack's ACK budget is also 3s |
| User mapping | `lark_user_binding` (per-installation, composite FK to `member`, steal-proof upsert `WHERE agora_user_id = EXCLUDED.agora_user_id`) | `user_external_identity(provider='telegram')` + partial unique index (migration 186) | **Telegram's shape wins.** `user_external_identity(provider='slack')` — the helpers already exist (`handler/external_identity.go:41`, `:62`) and carry the same steal guard |
| Installer auto-bind | `BindInstallerTx` — no token consumed, the device-flow response *is* the proof (`binding_token.go:214-223`) | QR binding token, hash-only storage | **Yes, Lark's.** OAuth v2 returns `authed_user.id` alongside the bot token; the installer is linked in the same transaction as the install |

Two gaps the map exposes, both worth fixing inside this work:

- **Review verdicts bypass the bus.** `SendReviewVerdictGroupNotify` is a direct
  call at `server/internal/handler/review_outcome.go:315`; there is no
  `review:verdict` event. A Slack listener that only subscribes to the bus will
  silently miss review outcomes. Phase 1 adds the event at that call site rather
  than adding a second direct call.
- **A pinned report has no shareable URL.** The API is ready
  (`GET /api/reports/{pinId}`, `router.go:648`, membership-gated) but the web
  surface opens a pin as modal state keyed by `pin_id`
  (`packages/views/projects/components/project-reports-section.tsx:83-85`), not
  a route. Phase 2 cannot deep-link without one.

## Slack in 2026 — the findings that shape the design

### Transport and trust

OAuth v2: redirect to `https://slack.com/oauth/v2/authorize` with `client_id`,
comma-separated `scope`, optional `user_scope`, `redirect_uri`, `state`; exchange
the returned `code` at `oauth.v2.access`. The response carries `access_token`
(`xoxb-`), `token_type: "bot"`, `scope`, `bot_user_id`, `app_id`,
`team: {id, name}`, `enterprise`, and — when `user_scope` was requested —
`authed_user: {id, …}`. Slack's own instruction on `state`: "If it doesn't match
what you sent, consider the authorization a forgery."

Request verification: `X-Slack-Request-Timestamp` + `X-Slack-Signature`,
HMAC-SHA256 with the app's signing secret over the base string
`'v0:' + timestamp + ':' + request_body`, hex digest prefixed `v0=`, reject when
the timestamp "differ[s] from local time by more than five minutes", compare in
constant time. **This requires the raw body**, so the Slack ingress routes must
read and retain bytes before any JSON decode.

Events API: `url_verification` challenge on setup; respond 2xx "within three
seconds"; retries arrive with `x-slack-retry-num` (1, 2, 3) and
`x-slack-retry-reason`, scheduled "nearly immediately", "after 1 minute", "after
5 minutes"; `x-slack-no-retry: 1` suppresses them. If an app fails "more than
95% of delivery attempts within 60 minutes" Slack **temporarily disables event
subscriptions**. Delivery ceiling is 30,000 events per workspace per app per 60
minutes.

Rate limits: tiers 1/2/3/4 at roughly 1+/20+/50+/100+ per minute; `chat.postMessage`
is Special — **one message per second per channel** with burst tolerance; 429
carries `Retry-After` in seconds; limits are per method, per workspace, per app.

### The unlisted-app trap (the single most decision-relevant finding)

Since 2025-05-29, newly-created commercially-distributed apps that are **not**
Marketplace-approved are limited to **1 request per minute returning at most 15
objects** on exactly two methods: `conversations.history` and
`conversations.replies`. Internal single-workspace apps keep 50+/min and 1,000
objects. `chat.postMessage`, `chat.unfurl`, `views.open` and the Events API are
**not affected**.

And the Marketplace cannot be entered early: the review guide requires the app be
"installed on 10 or more active workspaces" before submission, with a
preliminary review "up to 10 business days" and a functional review "up to 10
weeks" for new apps.

Read together these fix the roadmap:

- Phases 1 and 2 use only unaffected methods. They can ship on an unlisted
  install link to real customers **today** with zero platform penalty.
- Phase 3's thread sync must be **event-driven, never history-polling**.
  `message.channels` events carry the text; reading a thread back does not.
  Anything designed around `conversations.replies` would work in dev and die at
  1 req/min in production.
- Marketplace listing is a *consequence* of Phase 1+2 succeeding (10 installs),
  not a prerequisite for starting.

### Block Kit grew up — a report is no longer a translation problem

The 2026 block inventory is: Actions, Alert, Card, Carousel, Container, Context,
Context actions, Data table, Data visualization, Divider, File, Header, Image,
Input, **Markdown**, **Plan**, Rich text, Section, **Table**, **Task card**,
Video. Up to 50 blocks per message, 100 in modals and Home tabs.

Three of those change the Phase 2 design outright:

- **`markdown` block** — messages only; renders headings, ordered/unordered and
  task lists, links, inline and fenced code, **tables**, block quotes, dividers.
  "The cumulative limit for all `markdown` blocks in a single payload is 12,000
  characters." A markdown report artifact is therefore delivered *as itself*, not
  hand-translated into sections.
- **`table` block** — messages and Home tabs; max 100 rows, max 20 cells per
  row, 10,000 characters across all cells in a message; cells are `rich_text`,
  `raw_text` or `raw_number`; `column_settings` gives alignment and wrapping.
  The `table`-kind artifact (QA health) maps 1:1.
- **`plan` / `task_card`** — a plan holds up to 50 task cards, each with a
  `task_id`, `title`, `status` ∈ `in_progress | complete | error`, plus
  `details`, `output` and `sources`. This is Slack shipping a native rendering
  of *exactly* Agora's SDLC stepper and per-task todo list. Phase 3 uses it for
  agent progress; it is also the obvious eventual home for the Phase 3a plan
  card, though **not** for its confirmation (see the hard rule).

Section blocks cap at 3,000 characters of text and 10 fields of 2,000 each —
the constraint that shapes the sprint card below.

### Unfurling

`link_shared` (scope `links:read`) → `chat.unfurl` (scope `links:write`). An app
may register **up to five domains**, must own them, and "adding or removing
domains requires re-installation of your Slack app". Unfurls support Block Kit
(but not `rich_text` elements). The event does not include the surrounding
message text, and an app never gets `link_shared` for its own messages. For
gated content, `chat.unfurl` takes `user_auth_required`, `user_auth_url`,
`user_auth_message` / `user_auth_blocks` — Slack's sanctioned way to say "link
your account to see this", which is also a growth loop.

The five-domain cap plus the reinstall-on-change rule forces a decision up
front: **Agora Cloud ships one Slack app registered to the cloud domain;
self-hosters bring their own Slack app** (their own `client_id` /
`client_secret` / `signing_secret` in the registry), exactly as Lark is env-gated
per deployment today. Encoding a per-deployment domain into one shared app is not
possible.

### The agent surface

Slack now has a first-class agent app model: enable `Agents` in the manifest,
subscribe to `app_context_changed`, `agent_session_stopped`,
`agent_session_title_changed`, `message.im`, `app_home_opened`; drive the loading
state with `agents.sessions.setStatus` (the older `assistant.threads.setStatus`
"still works through the compatibility bridge", and as of 2026-03-05 the status
method accepts `chat:write` as well as `assistant:write`); stream replies with
**`chat.startStream` → `chat.appendStream` → `chat.stopStream`**. One hard
operational rule from Slack's own docs: "the loading UX does not disappear
automatically when your app posts a message. Set `status: "active"` when you
finish, or the session stays in `processing` until it times out after one hour."

The streaming triple maps onto `startAssistantRun`'s existing event stream
(`assistant:message` / `assistant:tool_activity` / `assistant:run_finished`,
`assistant/service.go:743-748`) with no change to the run loop — only a new
subscriber that writes to a stream instead of a websocket.

Separately, the manifest now has an `mcp_servers` array (max 20 servers; each
`{url, auth_type ∈ dynamic_client_registration | no_auth | manual_auth |
slack_identity_auth, auth_provider_key, headers}`), gated by
`settings.is_mcp_enabled`, with agent affordances under `features.agent_view`.
Agora already ships MCP servers. **Before building Phase 3c, spend one day
evaluating whether declaring Agora's MCP server with `slack_identity_auth` gets
80% of "@Agora in Slack" for 5% of the work.** That question is open and is
listed as such.

### What the competition ships

Linear's Slack app is the bar, and its feature list is a checklist for us:
issue-link unfurls showing title, description, status, assignee, with inline
actions (update assignee, comment, subscribe, engage sync); project, document and
initiative unfurls; bare issue-ID mentions auto-replying with a link, deduped to
once per 60 minutes; create-issue via message action (`More actions → Connect to
apps → Create new issue…`), via `@Linear` natural language, and via `/linear`
(explicitly weaker — "doesn't support threads, sync, or file uploads"); **two-way
thread sync** created by the message action, where the Slack thread also updates
when the issue is completed / canceled / duplicated; team, project and view
notification subscriptions to specific channels; personal notifications by DM;
auto-created channels per project.

Two things stand out. First, Linear's sync is *opt-in per thread* — it is created
by an explicit action, not by watching channels. That is exactly the design that
avoids needing broad history scopes, and we should copy it. Second, its
notification model is subscription-shaped (team / project / view → channel),
which is the axis Agora should copy rather than a flat event checkbox list.

The recurring complaint pattern across Jira/Asana/ClickUp Slack apps is the same
in every forum: **volume**, and **granularity that stops one level above where
you need it** — you can subscribe a channel to a project but not filter to the
three statuses you care about, so teams mute the channel and the integration
dies. The lesson is not "add more toggles". It is: ship a default that is quiet
enough that nobody reaches for mute in week one.

## Phase 1 — notify + unfurl (the trust builder)

### Install

OAuth v2, owner/admin only, `RequireHumanActor` on the begin call.

1. `POST /api/workspaces/{id}/slack/install/begin` returns
   `{authorize_url}`. `state` is a `secretbox`-sealed blob
   `{workspace_id, user_id, nonce, exp}` with a 10-minute expiry — no table,
   because Slack's `code` is itself single-use and the seal already proves we
   minted it. (Lark needed a token table because its binding token is handed to a
   *third party* in a chat message; an OAuth state never leaves the browser.)
2. Slack redirects to `GET /slack/oauth/callback` — a top-level unauthenticated
   route in the `/api/webhooks/github` idiom (`router.go:514-516`). Open the
   state, exchange the code at `oauth.v2.access`, seal `access_token`, upsert
   `slack_installation`, link the installer via `linkExternalIdentity(ctx,
   "slack", authed_user.id, state.user_id)` in the same transaction, redirect to
   `/{slug}/settings?tab=integrations&integration=slack`.

`user_scope=users:read` is requested solely so the response carries
`authed_user.id`. **The user token is discarded and never stored** — only the
Slack user id, as the external-identity external_id.

Scopes (Phase 1, minimal by construction):
`chat:write`, `chat:write.public`, `links:read`, `links:write`, `channels:read`,
`groups:read`, `im:write`, `team:read`. Notably absent: any `*:history` scope.
An admin reading that consent screen can see the app cannot read their messages.

Event subscriptions (Phase 1): `link_shared`, `app_uninstalled`,
`tokens_revoked`. Three. No message events, therefore no
`conversations.history`, therefore the unlisted-app rate limits are irrelevant.

### Model — migration 205

    slack_installation(
      id UUID PK, workspace_id → workspace CASCADE,
      team_id TEXT NOT NULL, team_name TEXT NOT NULL DEFAULT '',
      enterprise_id TEXT NOT NULL DEFAULT '', app_id TEXT NOT NULL,
      bot_user_id TEXT NOT NULL, bot_token_encrypted BYTEA NOT NULL,
      scopes TEXT NOT NULL DEFAULT '',
      installer_user_id → "user" RESTRICT,
      status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','revoked')),
      installed_at, created_at, updated_at,
      UNIQUE (workspace_id, team_id),
      UNIQUE (id, workspace_id))          -- composite-FK target, Lark's trick

    slack_channel_route(
      id UUID PK, workspace_id UUID NOT NULL, installation_id UUID NOT NULL,
      channel_id TEXT NOT NULL, channel_name TEXT NOT NULL DEFAULT '',
      project_id UUID NULL → project CASCADE,
      events TEXT[] NOT NULL DEFAULT '{}',
      enabled BOOL NOT NULL DEFAULT true,
      created_by → "user" SET NULL, created_at, updated_at,
      FOREIGN KEY (installation_id, workspace_id)
        REFERENCES slack_installation(id, workspace_id) ON DELETE CASCADE,
      UNIQUE NULLS NOT DISTINCT (installation_id, channel_id, project_id))

    -- partial unique index, migration 186's shape
    CREATE UNIQUE INDEX idx_user_external_identity_slack
      ON user_external_identity(user_id) WHERE provider = 'slack';

Notes on the constraints, because two of them are load-bearing:

- `UNIQUE (workspace_id, team_id)` and **not** `UNIQUE (team_id)`: one Slack
  workspace may host several Agora workspaces (an agency, or a company running a
  separate Agora workspace per product). Slack issues one bot token per (app,
  team), so those rows legitimately share a token — which means
  **revoking one Agora workspace's install must not call `apps.uninstall`** while
  a sibling row for the same `team_id` is still `active`. Revoke marks the row
  and drops its routes; only the last row standing uninstalls.
- `NULLS NOT DISTINCT` (PG15+, and we run pg17) is required or a NULL
  `project_id` would let the same channel be routed twice.
- `events TEXT[]` is deliberately the same shape as
  `release_integration.events` (migration 158) so the matcher is the same pure,
  DB-free predicate (`releaseIntegrationMatchesEvent`,
  `release_outbound.go:46`).

### Endpoints

| Method | Path | Guard |
| --- | --- | --- |
| `POST` | `/api/workspaces/{id}/slack/install/begin` | owner/admin + `RequireHumanActor` |
| `GET` | `/slack/oauth/callback` | none (signed state) |
| `GET` | `/api/workspaces/{id}/slack/installations` | member |
| `DELETE` | `/api/workspaces/{id}/slack/installations/{installationId}` | owner/admin + `RequireHumanActor` |
| `GET` | `/api/workspaces/{id}/slack/channels` | owner/admin — proxies `conversations.list` for the picker |
| `GET`/`PUT`/`DELETE` | `/api/workspaces/{id}/slack/routes[/{routeId}]` | owner/admin + `RequireHumanActor` on writes |
| `POST` | `/api/me/links/slack/begin` | `RequireHumanActor` — personal link OAuth |
| `POST` | `/slack/events` | none (signature-verified) |

Routes live in `cmd/server/router.go` beside the Lark group (`:932-952`) and the
webhook block (`:507-526`). The `/slack/events` handler's entire job is: read raw
body → verify signature and timestamp → answer `url_verification` → enqueue →
`200`. Nothing that can block runs on that path; Lark's `ReplyTimeout` of 2.5s
under Lark's own 3s ACK budget (`hub.go:138-147`) is the same discipline against
the same number.

### The noise rule

**Default LOW. Mentions, assignments and failures only. Everything else opt-in.**

This is not a preference; it is the failure mode that kills every competitor's
Slack app (see the research above). Two mechanisms enforce it, and one of them
we already own for free.

**Personal DMs inherit inbox mute for free.** The Slack DM listener hangs off
`inbox:new`, and a muted notification never creates an inbox item — the exact
property `lark_push_listeners.go:12-17` and `telegram_push_listeners.go:16-18`
already rely on. A user's existing notification preferences (`assignments`,
`status_changes`, `comments`, `updates`, `agent_activity`, each `all` | `muted`
— `handler/notification_preference.go:20-32`) therefore govern Slack DMs with
**zero new configuration surface**. That is the single best thing about this
design and it costs nothing.

**Channel routes ship with a quiet default.** The route event vocabulary is
product-level, not protocol-level:

| Route event | Source | Default on a new install |
| --- | --- | --- |
| `assigned` | `inbox:new`, type `issue_assigned` | DM on · channel **off** |
| `mentioned` | `inbox:new`, type `mention` | DM on · channel **off** |
| `commented` | `inbox:new`, type `comment` | DM on · channel **never offered** |
| `failed` | `task:failed` (`service/task.go:1576`) | channel **ON** |
| `qa_verdict` | `qa_evidence:ready` (`service/qa_evidence.go:637`) | channel **ON, fail only** |
| `review_verdict` | new `review:verdict` event at `review_outcome.go:315` | channel **ON, changes-requested only** |
| `agent_done` | `task:completed` (`service/task.go:1495`) | off |
| `created` | `issue:created` (`service/issue.go:404`) | off |
| `status_changed` | `issue:updated` (`notification_listeners.go:662`) | off |

A fresh install routes `{failed, qa_verdict, review_verdict}` to the one channel
the admin picked. A channel hears about the team's work **only when something
needs a human.** Turning on `status_changed` is one checkbox away and stays the
user's decision.

`commented` is DM-only by construction. A comment is context for one person; in
a channel it is the thing that makes people mute.

Every fanout additionally honours
`EventPayloadSuppressExternalNotifications` (`protocol/events.go:8`), as
`telegram_push_listeners.go:82` does.

### Event seams — exactly where the code hooks

New file `server/cmd/server/slack_push_listeners.go`, a transliteration of
`telegram_push_listeners.go`, registered from `cmd/server/main.go` beside
`registerTelegramPushListeners` (`main.go:354` neighbourhood):

    func registerSlackPushListeners(bus *events.Bus, h *handler.Handler) {
        if !h.SlackEnabled() { return }
        bus.Subscribe(protocol.EventInboxNew,        h.slackOnInboxNew)      // DMs
        bus.Subscribe(protocol.EventTaskFailed,      h.slackOnTaskFailed)
        bus.Subscribe(protocol.EventTaskCompleted,   h.slackOnTaskDone)
        bus.Subscribe(protocol.EventQAEvidenceReady, h.slackOnQAVerdict)
        bus.Subscribe(protocol.EventIssueCreated,    h.slackOnIssueCreated)
        bus.Subscribe(protocol.EventIssueUpdated,    h.slackOnIssueUpdated)
        bus.Subscribe(protocol.EventReviewVerdict,   h.slackOnReviewVerdict)
    }

The bus is **synchronous and on the request path**
(`internal/events/bus.go:61-87`), so every handler destructures the payload and
returns, handing the work to the outbound queue below. Panics are recovered per
handler, but a slow handler is still a slow write.

`protocol.EventReviewVerdict` is new, published at
`handler/review_outcome.go:315` right where `SendReviewVerdictGroupNotify` is
already called. Adding the event rather than a second direct Slack call is the
whole point: the next integration gets review verdicts for free, and Telegram's
existing direct call can migrate later or not at all.

### Delivery discipline

`chat.postMessage` allows **one message per second per channel**, and a bulk
status change can emit dozens of bus events in one request. So delivery is not
"spawn a goroutine per event" (the Telegram/Lark pattern) — it is a small
outbound worker:

- A per-`(installation, channel)` token bucket at 1/s.
- A 10-second coalescing window: N events for the same channel and the same
  route event collapse into one message ("3 tasks failed" with the three
  identifiers), which is also *better copy* than three messages.
- 429 → honour `Retry-After` exactly; 3 attempts then drop with a warn log.
  A dropped notification is a cost; a retry storm is an outage.
- `account_inactive` / `token_revoked` / `invalid_auth` from any call ⇒ mark the
  installation `revoked` and stop. Same hygiene as `app_uninstalled`.
- All Slack API responses are parsed into explicit structs with an `ok` check and
  an explicit error string — never a bare decode into an assumed shape. The
  frontend-side rule in CLAUDE.md ("parse, don't cast") applies with equal force
  to a third-party API the backend consumes.

### Unfurl

On `link_shared`: for each link, parse `https://<host>/{workspaceSlug}/issues/{IDENT}`
and `/{workspaceSlug}/projects/{id}` (one small pure function, table-tested);
resolve slug → workspace → `slack_installation` for `(workspace_id, team_id)`;
resolve the *posting Slack user* to an Agora user via
`userIDByExternalIdentity(ctx, "slack", user_id)`.

- Not linked, or linked but not a member, or the issue-visibility gate says no:
  respond `chat.unfurl` with `user_auth_required: true` and `user_auth_url`
  pointing at the personal-link OAuth. Slack shows a private "Connect your
  account" prompt to that one user. **This is the growth loop**: a teammate
  pastes an Agora link, a non-user sees an invitation to connect, nothing leaks.
- Linked and permitted: a compact unfurl — identifier + title as a header,
  status / assignee (agent assignees keep their distinct label) / priority /
  project as context, and an "Open in Agora" button.

Phase 1 unfurls are **read-only**. Linear's inline actions are the right
eventual shape but they need the interactivity endpoint, and adding it is Phase
3's job.

No dedup table: `chat.unfurl` replaces rather than appends, so a Slack retry of
the same `event_id` is naturally idempotent. Phase 3, whose actions create rows,
does need one.

### Config keys (`server/internal/config/registry.go`)

    {Key: "AGORA_SLACK_SECRET_KEY",     Kind: KindSecret, Category: "Secrets", …}
    {Key: "AGORA_SLACK_CLIENT_SECRET",  Kind: KindSecret, Category: "Secrets", …}
    {Key: "AGORA_SLACK_SIGNING_SECRET", Kind: KindSecret, Category: "Secrets", …}
    {Key: "AGORA_SLACK_CLIENT_ID",      Kind: KindString, Category: "Slack", …}
    {Key: "AGORA_SLACK_APP_ID",         Kind: KindString, Category: "Slack", …}
    {Key: "AGORA_SLACK_NOTIFY_ENABLED", Kind: KindBool, Category: "Slack",
     Default: "true", ProjectScoped: true}

`AppConfig.SlackEnabled` (`handler/config.go`, beside `LarkEnabled` at `:109`)
is true only when the seal key, client id, client secret and signing secret are
all present — the four-way gate keeps a half-configured deployment from showing
an install button that cannot complete.

### UI touchpoints

- `packages/core/slack/{index,queries}.ts` — `slackInstallationsOptions(wsId)`,
  routes, channels; mirrors `packages/core/lark/queries.ts`.
- `packages/views/settings/components/slack-tab.tsx` — install button /
  connected state / channel-route editor. One `IntegrationCard` added to
  `integrations-tab.tsx`, gated on `useConfigStore(s => s.slackEnabled)`, using
  the same status-probe-dedupe pattern the file documents at its head.
- `packages/views/settings/components/slack-notification-setting.tsx` — the
  personal "Connect Slack" row, beside `telegram-notification-setting.tsx`.
- Locales ×4 (`en`, `zh-Hans`, `ru`, `uz`), `parity.test.ts` green, glossary
  terms per the conventions docs page.
- Docs: `apps/docs/content/docs/slack-integration.mdx` + `.zh.mdx` + `meta.json`
  entries, modelled on `lark-bot-integration.mdx`.

### Test strategy

- `integrations/slack`: `httptest` against `oauth.v2.access`, `chat.postMessage`,
  `chat.unfurl`, `conversations.list` — the existing `client_test.go` shape.
- Signature verification: table test covering valid, tampered body, stale
  timestamp (>5 min), missing header, wrong version prefix, and constant-time
  comparison.
- Malformed-response test per Slack method (`ok:false`, missing field, `null`
  array) asserting we degrade rather than panic.
- Route matching: pure function, table test over `(event, routes) → channels`.
- Block render: golden test for each card, asserting the 3,000 / 50-block caps.
- `slack_push_listeners_test.go` in the shape of
  `telegram_push_listeners_issue_created_test.go`, including a
  suppress-flag case and a muted-preference case.
- `packages/views/settings/components/slack-tab.test.tsx` (jsdom, mock
  `@agora/core/api`).
- No E2E — it would need a real Slack workspace.

### Scope

Four PRs, ~50 files.

| PR | Contents | Files |
| --- | --- | --- |
| 1 · install + plumbing | migration 205 (±2), `queries/slack.sql` + sqlc output, `integrations/slack/{oauth,api,signature,blocks}.go` + tests, `handler/{slack,slack_oauth,slack_events}.go` + tests, registry keys, `config.go` flag, router wiring | ~22 |
| 2 · routing + delivery | `handler/slack_routes.go`, `handler/slack_notify.go` (queue + buckets + coalescing), `cmd/server/slack_push_listeners.go`, `protocol.EventReviewVerdict` + its publish site, tests | ~10 |
| 3 · unfurl | link parser, `handler/slack_unfurl.go`, unfurl blocks, `user_auth_required` path, tests | ~6 |
| 4 · UI + docs | `packages/core/slack/*`, `slack-tab.tsx` + test, `slack-notification-setting.tsx`, `integrations-tab.tsx` edit, locales ×4, docs ×2 + meta | ~13 |

PRs 1-3 are backend-only and land independently; PR 4 is what makes it
installable by a human.

## Phase 2 — reports to Slack (the distribution lever)

### The seam

The scheduler's "ok" means *the refresh started*, not that a new version exists
(`assistant_schedules.go:477-480`). So the delivery hook is **not** in the
scheduler. It is the artifact write path:

    server/internal/handler/assistant_artifacts.go:376
        h.notifyPinnedReportUpdated(ctx, updated.ID, caller.ID)
    server/internal/handler/assistant_pins.go:443-451
        → publishReportEvent(protocol.EventReportUpdated, actorID, pin)

Fired after commit ("so a fanout never announces a version that was rolled
back"). Slack delivery is a new `bus.Subscribe(protocol.EventReportUpdated, …)`
in `slack_push_listeners.go` — no change to the scheduler, and a **manual**
re-run delivers identically to a scheduled one, which is the honest reading of
2a's own promise that "re-running the recipe IS the refresh".

One constraint the payload imposes: `report:*` events are **ids-only by design**
(`protocol/events.go:100-104`) so a fanout can never leak a report's contents.
The Slack worker therefore re-reads the body through the membership-gated read,
never from the event payload.

### Model — migration 206

Delivery is a property of the **pin**, not the schedule: a pin is already the
publish act, and Slack is a second publish target.

    ALTER TABLE assistant_artifact_pin
      ADD COLUMN slack_installation_id UUID REFERENCES slack_installation(id) ON DELETE SET NULL,
      ADD COLUMN slack_channel_id      TEXT NOT NULL DEFAULT '',
      ADD COLUMN slack_last_digest     TEXT NOT NULL DEFAULT '',
      ADD COLUMN slack_last_posted_at  TIMESTAMPTZ;

Two guards, both necessary:

- **Digest guard.** Skip the post when the rendered payload's SHA-256 equals
  `slack_last_digest`. A refresh that produced identical numbers is not news.
- **Floor.** At most one post per pin per hour. Without it, an owner iterating
  on a report in the artifact pane spams the channel with every save.

Endpoint: the Slack target rides on the existing pin write —
`POST /api/assistant/artifacts/{id}/pins` gains optional
`{slack_channel_id}` and `PUT …/pins/{pinId}/slack` sets or clears it. Owner-only
and `RequireHumanActor`, for the reason the schedule already carries it
(`router.go:637`): a channel post is standing disclosure to a room that may
include people who are not Agora users at all. The pin dialog must say so in one
plain line.

### The sprint-report card, concretely

The sprint recipe produces: headline (done / total, days left), blocked list with
owners, in-review + QA queue, risks
(`docs/assistant-domain-plan.md` Phase 1, recipe 1). Rendered:

    [ header      ] "Sprint 14 · Mobile"
    [ section     ] "*12 of 18 done* · 3 days left"
    [ section     ] fields: ["*Done*\n12", "*In review*\n3",
                             "*Blocked*\n2", "*QA queue*\n4"]
    [ divider     ]
    [ section     ] "*Blocked*\n• MUL-231 Payments retry — Dilshod · 4d\n
                     • MUL-244 Push tokens — Agent `mobile-dev` · 2d"
    [ section     ] "*Risks*\n• 2 issues in review longer than a week"   (omitted if empty)
    [ actions     ] [Open report] [Open sprint]
    [ context     ] "Refreshed 09:02 · Asia/Tashkent · Agora"

Sizing against the documented caps: 7 blocks of 50; 4 fields of 10 at well under
2,000 characters each; the blocked section capped at 5 rows plus "+N more" to
stay inside 3,000. Every count comes from the artifact body the recipe already
writes — Slack rendering parses the report, it does not re-query Agora.

**Falling back honestly.** When the parse does not find the sprint recipe's
shape (a custom report, a changed heading), the renderer degrades rather than
guesses: a `header` with the artifact title, one `markdown` block carrying the
artifact body truncated to the documented 12,000-character cumulative cap with a
"… full report in Agora" tail, and the same actions row. A `table`-kind artifact
(QA health) renders through the `table` block — 100 rows, 20 columns, 10,000
characters — and degrades to the markdown block above the cap. A `chart`-kind
artifact posts headline + link only; rendering a chart image is a Phase 4
question, not a Phase 2 one.

### The missing URL

`[Open report]` needs a target and there isn't one. Phase 2 adds
`/{workspaceSlug}/reports/{pinId}` — a real route reading the existing
membership-gated `GET /api/reports/{pinId}` (`router.go:648`) and reusing the
artifact renderers the project Reports modal already uses. Per the
cross-platform rule this is one shared view in `packages/views/reports/`, a Next
route in `apps/web/app/`, and a desktop session route. The project page's modal
becomes a link to it.

This is a small, self-contained piece of work that is worth doing regardless of
Slack — a report nobody can link to is a report nobody shares.

### The Monday 09:00 demo, and what it actually promises

Set up: pin the sprint report to the project, schedule `weekly · Monday · 09:00 ·
Asia/Tashkent`, pick `#eng`. Monday at 09:00 the ticker claims the slot, the
owner's session runs the recipe against live data, `update_artifact` commits,
`report:updated` fires, the card lands in `#eng`.

The honest caveat, stated rather than hidden: **the card lands when the model
finishes, not at 09:00 sharp** — typically within a minute or two, longer if the
workspace is large. Slack's `chat.scheduleMessage` (up to 120 days ahead, max 30
messages per 5-minute window per channel) cannot help, because the content does
not exist until the run completes. The context line therefore carries the actual
refresh time, not the scheduled one.

The other caveat: the run happens **as the report owner**, in their session
(`assistant_schedules.go:442-448`, asserted because "the run below executes tools
AS this user"). A report posted to a channel shows what its owner can see. That
is the same disclosure the pin already makes, one room wider, and the dialog says
so.

### Scope

Two PRs, ~18 files.

| PR | Contents | Files |
| --- | --- | --- |
| 5 · report delivery | migration 206, pin API additions, `handler/slack_report_delivery.go` (subscriber + digest/floor guards), `integrations/slack/report_blocks.go` (sprint parser + markdown/table fallback), tests | ~10 |
| 6 · report route + UI | `packages/views/reports/report-page.tsx`, web + desktop routes, pin-dialog Slack target picker, project Reports link change, locales ×4, tests | ~8 |

## Phase 3 — two-way

Phase 3 is three independent pieces. Each one adds a scope to the consent screen,
so each one must earn it.

### 3a — create an issue from a Slack message

Message shortcut (`callback_id: agora_create_issue`) → `views.open` with the
payload's `trigger_id`, which **expires in 3 seconds**, so the interactions
endpoint opens the modal before doing anything else and fills it asynchronously →
`view_submission` creates the issue.

- Adds scope `commands` and an interactivity Request URL
  (`POST /slack/interactions`), same signature verification as `/slack/events`.
- The issue is created **as the mapped Agora user**
  (`userIDByExternalIdentity(ctx, "slack", user_id)`) — never as a service
  account. An unlinked user gets an ephemeral "connect your Slack account"
  message with the personal-link URL, and nothing is created.
- `issue_origin_type_check` gains `'slack_message'`, exactly as migration 111
  added `'lark_chat'`. `origin_id` points at a new `slack_message_link` row
  holding `(workspace_id, installation_id, issue_id, channel_id, message_ts,
  thread_ts, permalink)`, with the composite FK to
  `slack_installation(id, workspace_id)`.
- Modal fields: project, title (prefilled from the message text, trimmed to the
  human-quick-ticket voice — a wall of quoted Slack text makes the dev
  over-build), description (the message permalink plus the text), assignee
  including agents, priority.
- This path **does** need event dedup: `(installation_id, event_id)` with the
  two-phase claim shape of `lark_inbound_message_dedup` (migration 113), because
  a Slack retry of a `view_submission` must not create two issues.

### 3b — thread mirroring, scoped honestly

**What ships: one-way, issue → thread, opt-in per thread.** When an issue is
created from a message (3a), subsequent Agora comments, status changes and the
QA/review verdict post into that Slack thread via `chat.postMessage` with
`thread_ts`. The Slack thread becomes the issue's public trail without anyone
leaving the channel. This costs zero new scopes and zero read calls.

**What is deferred: reading Slack replies back into Agora comments.** Two honest
reasons, in order of weight:

1. It requires `channels:history` + `groups:history` — the broadest scopes in the
   whole app, and the ones that turn a 30-second admin approval into a security
   review. Phase 1's entire pitch is a consent screen with no history scope on
   it. Spending that credibility on a feature nobody asked for yet is the wrong
   trade.
2. Scope changes force a reinstall of every existing workspace. Whatever the
   final scope set is, it should be settled before we ask people to reinstall.

The design when it is time, recorded now so it isn't redesigned from scratch:
subscribe to `message.channels` / `message.groups` and filter to `thread_ts`
values present in `slack_message_link`. The events carry the text, so **no
`conversations.replies` call is needed** — which matters enormously, because
that method is capped at 1 request per minute for unlisted apps. Backfill and
repair *do* need `conversations.replies` and therefore genuinely wait for the
Marketplace listing. The live path does not.

Linear's model confirms the shape: its sync is created by an explicit action on
one message, not by watching channels. Copy that.

### 3c — @Agora in Slack

The assistant already streams; Slack now consumes streams. `startAssistantRun`
(`handler/assistant.go:652`) is the one path that accepts a turn, and the
scheduler already proves a non-HTTP caller works. A Slack bridge is a third
caller plus a subscriber on `assistant:message` / `assistant:tool_activity` /
`assistant:run_finished` that writes `chat.startStream` → `chat.appendStream` →
`chat.stopStream`, with `agents.sessions.setStatus` for the thinking state and a
mandatory `status: "active"` on completion (or the session sits in `processing`
for an hour).

**Before building any of it, evaluate the manifest MCP route.** Slack's manifest
takes up to 20 `mcp_servers` with `auth_type: slack_identity_auth` behind
`settings.is_mcp_enabled`. Agora already ships MCP servers. If Slack's own agent
can call Agora's tools under the asking user's identity, a large part of 3c is
configuration rather than code. This is an open question, not a decision.

Identity, spend and confirmation are the three things that must be settled
before a line is written:

- **Identity.** A run executes tools as a user. The asking Slack user must be
  linked, in the workspace, and the run inherits *their* visibility (the
  non-owner issue-visibility gate applies unchanged). Unlinked ⇒ ephemeral
  connect prompt, no run. There is no fallback service identity and there will
  not be one.
- **Spend.** Every mention is a model run charged to the workspace. Gate with
  `AGORA_SLACK_ASSISTANT_ENABLED` (KindBool, **default false**) and a per-user
  daily run cap. **Open question:** does a Slack run count against the same
  budget as an in-app run, and who sees the number?
- **Confirmation.** See below. Non-negotiable.

### The confirmation rule (hard)

**Destructive and consequential confirmations MUST NOT happen in Slack.**

When an assistant run parks on `needs_confirmation` — a single write, or a
Phase 3a `propose_plan` card — Slack receives the *summary* and a link:

    "I've prepared a plan: move 7 issues out of review. Review and confirm in Agora →"

The Confirm button exists only in the Agora app. Not in a Block Kit button, not
in a modal, not behind a "yes" reply.

The reason is structural, not stylistic. `POST /api/assistant/operations/{id}/confirm`
is gated by `RequireHumanActor` (`router.go:608`), which trusts `X-Actor-Source`
precisely because the auth middleware strips any client-supplied value and stamps
its own (`actor_guards.go:47-53`, `:113-116`). A Slack interaction payload
arrives with no Agora session, no cookie, no device — the only thing proving who
tapped is Slack's signature over a `user_id`. Wiring that into the confirm
endpoint would mean inventing a fourth actor source whose entire security rests
on an external vendor's HMAC, for the one endpoint the codebase has decided must
be hardest to reach. The extension rule in `actor_guards.go:90-95` says any new
machine-credential branch must be reviewed against this gate; this one does not
pass.

The same rule covers anything else `RequireHumanActor` protects reaching Agora
from Slack: pin/unpin, schedules, instance config, billing, QA verdict override,
identity linking, account deletion. Slack can *ask*; Agora confirms.

Slack's own AI guidance points the same way — its first-interaction guidance is
to "send a call to action when a user interacts with an agent for the very first
time … especially important when it is necessary for the user to sign in,
connect an account, agree to terms of service". Linking out is the native
pattern, not a workaround.

The `plan` / `task_card` blocks are still the right *rendering* for a plan or a
running agent task — a read-only mirror of the plan card, with the button
linking home. Rendering is not authorization.

### Scope

Three PRs, ~35 files. 3a (~14) and 3b-one-way (~8) are independent of the
assistant work; 3c (~13) is gated on Phase 3a of the assistant plan landing and
on the MCP evaluation.

## Sequencing

**Build starts only after the assistant's Phase 3a (the plan primitive) lands.**
Not because Phase 1 depends on it — it does not — but because 3a touches
`assistant_pending_operation` semantics, the confirm endpoint's body, the
operation-kind enum, the router and the prompt, and Slack Phase 1 touches the
router, the config registry, `cmd/server/main.go` and the migration counter.
Running both at once guarantees mechanical collisions in exactly the files that
are hardest to merge. 3a is a short piece of work; Slack is not. Let 3a through
first.

Migration numbers: 204 is taken (`assistant_report_schedule`); 3a needs none
("No new table"), so Slack Phase 1 takes **205** and Phase 2 takes **206**.

Within Slack, the order is strict:

1. Phase 1 PR 1-3 (backend) → PR 4 (UI). Installable, useful, quiet.
2. Phase 2 PR 5-6. The reason to keep it.
3. Phase 3, piece by piece, each with its own scope-change reinstall decision.

## Growth mechanics

**Unlisted install link first — and this is mandatory, not a shortcut.** The
Marketplace review requires the app to be "installed on 10 or more active
workspaces" before submission. You cannot list your way to your first ten
installs. Slack's own framing of unlisted distribution — "perfect for when you
want to test out your app by running a pilot for early customers" — describes
exactly where Agora is.

The cost of staying unlisted is bounded and known: 1 req/min and 15 objects on
`conversations.history` and `conversations.replies`, and nothing else. Phases 1
and 2 touch neither. That is not a coincidence; it is why they are Phases 1 and
2.

**The report in the channel is the acquisition surface.** Every Monday, everyone
in `#eng` — including the designer, the founder and the contractor who have never
opened Agora — sees a card that says what the team shipped, what is blocked, and
who owns it, with Agora's name on it. That is a weekly, non-annoying, genuinely
useful advertisement delivered by the customer's own workflow. No other
integration in the product has that property.

**The unfurl is the conversion surface.** A non-user who pastes or clicks an
Agora link gets Slack's native `user_auth_required` prompt — a private, in-place
"connect your account" with no spam and no channel noise. Linking is one click
and lands them in a workspace they already belong to socially.

**The App Home tab is the retention surface** (Phase 2.5, cheap): `views.publish`
on `app_home_opened`, up to 100 blocks, per-user. "Your Agora day" — assigned
issues, inbox count, the workspace's pinned reports — for anyone who opens the
app, with a Connect button for anyone who hasn't linked. It needs no new scope.

Marketplace listing comes after ten installs, with the security questionnaire and
the 10-business-day preliminary / up-to-10-week functional review budgeted as a
quarter, not a sprint. Its payoff is the restored `conversations.*` limits that
unlock Phase 3b's backfill, plus directory presence.

## Non-goals

- No Socket Mode. It is barred from the Marketplace, caps at 10 concurrent
  connections per app, and re-creates the per-installation lease machinery Lark
  needed and Slack does not.
- No Slack Workflow Builder steps, no Slack-hosted functions, no Deno/Slack CLI
  deployment. Agora's backend is the runtime.
- No replacement of the release-hub Slack webhook connector, and no migration of
  its rows.
- No message-history scopes in Phase 1 or Phase 2, at any price.
- No confirmation, approval, or destructive action executed from a Slack
  interaction, ever — restated here because it is the rule most likely to be
  eroded by a reasonable-sounding request.
- No Slack-side storage of Agora content beyond what a posted message contains.
- No per-agent Slack bots. That is Telegram's model, and it exists there because
  a Telegram bot belongs to an agent. A Slack app belongs to the workspace.

---

## Appendix A — sources

All fetched and verified 2026-09-19.

**A1 · App model and install**
- Installing with OAuth — https://docs.slack.dev/authentication/installing-with-oauth/
- App lifecycle & distribution — https://docs.slack.dev/app-management/distribution/
- App manifest reference (incl. `mcp_servers`, `features.agent_view`,
  `settings.is_mcp_enabled`, 10 shortcuts / 50 slash commands / 255 scopes) —
  https://docs.slack.dev/reference/app-manifest/

**A2 · Transport and verification**
- Events API (3-second ack; `x-slack-retry-num` / `x-slack-retry-reason`;
  retries at ~0s / 1min / 5min; `x-slack-no-retry: 1`; subscriptions disabled
  above 95% failures in 60 minutes; 30,000 events per workspace per app per
  hour) — https://docs.slack.dev/apis/events-api/
- Socket Mode (**"Apps using Socket Mode are _not_ currently allowed in the
  public Slack Marketplace"**; max 10 concurrent connections per app; payloads
  may land on any connection) —
  https://docs.slack.dev/apis/events-api/using-socket-mode/
- Verifying requests (`v0:{ts}:{body}`, HMAC-SHA256, `v0=` prefix, five-minute
  window, constant-time compare) —
  https://docs.slack.dev/authentication/verifying-requests-from-slack/

**A3 · Rate limits and distribution economics**
- Web API rate limits (tiers 1-4; `chat.postMessage` Special at 1/sec/channel;
  429 + `Retry-After`) — https://docs.slack.dev/apis/web-api/rate-limits/
- Rate limit changes for non-Marketplace apps (2025-05-29; **1 request per
  minute, 15 objects**, on `conversations.history` and `conversations.replies`
  only; internal apps unaffected at 50+/min and 1,000 objects) —
  https://docs.slack.dev/changelog/2025/05/29/rate-limit-changes-for-non-marketplace-apps
- Marketplace review guide (**10+ active workspaces required**; 10 business days
  preliminary, up to 10 weeks functional) —
  https://docs.slack.dev/slack-marketplace/slack-marketplace-review-guide/

**A4 · Surfaces**
- Block Kit overview (50 blocks per message, 100 in modals/Home tabs) —
  https://docs.slack.dev/block-kit/
- Blocks reference (full 2026 inventory incl. Markdown, Table, Plan, Task card,
  Card, Alert, Carousel, Data table, Data visualization) —
  https://docs.slack.dev/reference/block-kit/blocks/
- Markdown block (messages only; tables, headings, lists, task lists, code;
  **12,000-character cumulative limit per payload**) —
  https://docs.slack.dev/reference/block-kit/blocks/markdown-block/
- Table block (100 rows, 20 cells per row, 10,000 characters per message;
  `rich_text` / `raw_text` / `raw_number` cells; `column_settings`) —
  https://docs.slack.dev/reference/block-kit/blocks/table-block/
- Plan block (up to 50 task cards; unique `task_id`) —
  https://docs.slack.dev/reference/block-kit/blocks/plan-block/
- Task card block (`status` ∈ `in_progress|complete|error`; `details`, `output`,
  `sources`) — https://docs.slack.dev/reference/block-kit/blocks/task-card-block/
- Section block (3,000-character text; 10 fields of 2,000) —
  https://docs.slack.dev/reference/block-kit/blocks/section-block/
- `chat.postMessage` (`chat:write`, `chat:write.public`, `no_permission`
  without it; 4,000-character text guidance; `thread_ts`, `reply_broadcast`,
  `metadata`) — https://docs.slack.dev/reference/methods/chat.postMessage/
- `chat.scheduleMessage` (120 days max; 30 messages per 5-minute window per
  channel; `time_in_past` / `time_too_far` / `restricted_too_many`) —
  https://docs.slack.dev/reference/methods/chat.scheduleMessage/
- Unfurling links (`link_shared`, `chat.unfurl`, `links:read` / `links:write`,
  **five domains max**, reinstall on change, `user_auth_required` /
  `user_auth_url`) — https://docs.slack.dev/messaging/unfurling-links-in-messages/
- Shortcuts (global vs message, `commands` scope, `message_action` payload,
  3-second `trigger_id`, `views.open`) —
  https://docs.slack.dev/interactivity/implementing-shortcuts/
- App Home (`views.publish`, `app_home_opened`, 100 blocks, per-user) —
  https://docs.slack.dev/surfaces/app-home/

**A5 · AI and agents**
- AI in Slack overview — https://docs.slack.dev/ai/
- Developing AI apps (`assistant:write`, `im:history`, `chat:write`;
  `app_context_changed`, `agent_session_stopped`, `agent_session_title_changed`,
  `message.im`, `app_home_opened`; `agents.sessions.setStatus` /
  `agents.sessions.rename`; `chat.startStream` / `chat.appendStream` /
  `chat.stopStream`; **one-hour `processing` timeout if `status: "active"` is
  never set**) — https://docs.slack.dev/ai/developing-ai-apps/
- AI app best practices — https://docs.slack.dev/ai/ai-apps-best-practices
- `assistant.threads.setStatus` (max 10 rotating messages, 2-minute timeout) —
  https://docs.slack.dev/reference/methods/assistant.threads.setStatus/
- Set-status scope update, 2026-03-05 (`chat:write` now accepted;
  `assistant:write` to be retired for this method) —
  https://docs.slack.dev/changelog/2026/03/05/set-status-scope-update/

**A6 · Competition**
- Linear Slack integration — https://linear.app/docs/slack

## Appendix B — scope at a glance

| Phase | PRs | Files | Migrations | New scopes on the consent screen |
| --- | --- | --- | --- | --- |
| 1 · notify + unfurl | 4 | ~50 | 205 | `chat:write`, `chat:write.public`, `links:read`, `links:write`, `channels:read`, `groups:read`, `im:write`, `team:read` |
| 2 · reports to Slack | 2 | ~18 | 206 | none |
| 2.5 · App Home | 1 | ~5 | none | none |
| 3a · create from message | 1 | ~14 | 207 | `commands` |
| 3b · thread mirror (one-way) | 1 | ~8 | — | none |
| 3c · @Agora | 1 | ~13 | 208 | `assistant:write`, `im:history`, `app_mentions:read` |
| 3b′ · thread sync (two-way, deferred) | — | — | — | `channels:history`, `groups:history` — requires a reinstall of every workspace |

The shape of that last column is the plan's argument in one table: the first two
phases — the ones that carry all the product value — add nothing to the consent
screen that an admin has to think about.
