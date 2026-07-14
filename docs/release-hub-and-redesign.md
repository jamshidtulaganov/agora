# Release page redesign + Release-integrations hub

Two threads, phased. Thread A = comfortable Release-page redesign (frontend).
Thread B = pluggable release-integrations hub (backend + settings UI). The user
wants both; A ships first (visible, demoable), B follows.

## Why (the critique that started this)

The current Release page reads like a QA problem-list, not a release cockpit you
feel good using:

1. All-negative, dense framing everywhere: `60 in review · 5 need fix · 35 stale
   · 42 pending`, "Not ready" ×3, "9 need a decision", "Needs fix", "FAIL". No
   positive/progress signal.
2. Cryptic unlabeled icons: `✓✗◌`, `◌ (1 no-QA)/1`, "regression: never run".
3. Cramped health strip: truncated sprint names, no hierarchy, chip floats off.
4. Explanatory paragraphs instead of visual state (Ship tab opens with a 4-line
   blurb).
5. The 115-pending stale backlog on sd-main pollutes the release view.
6. No release-readiness gestalt — no ring/%, no "2 from shipping", no changelog
   of what's going out, no celebratory shipped state.
7. No tool integrations — purely internal QA verdicts.

## Thread A — comfortable redesign (frontend only)

Building blocks that already exist (reuse, do not reinvent): `verdictIcon` /
`verdictTone` (`qa/components/verdict.tsx`), `regressionStatusMeta`
(`regression-status.ts`), `useStateBadgeMeta` (`qa-lane.tsx`), the shared
`qaReleaseKeys` query factories (`packages/core/qa/queries.ts`), `Lane` /
`QAIssueRow`, `BreadcrumbHeader`, `ViewToggle`, the `<Card>` / `<Badge>` /
linear `<Progress>` primitives in `packages/ui`.

Net-new: a **radial readiness ring** (SVG stroke-dasharray — only a linear
`Progress` exists today).

Design vocabulary stays the codebase norm: **emerald = QA/ready**, **amber =
warn/stale**, **destructive = fail**, **brand blue = primary/logo only**.

### A1. Ship tab (the hero) — `qa-sprint-readiness-view.tsx`
- Kill the opening paragraph. State is shown, not explained (a small `?`
  hover-card carries the one-line "a sprint ships when every task passed & none
  failing/pending").
- Per sprint → a `<Card>`:
  - LEFT: **readiness ring** — `passed / total`, colored by state (emerald when
    `mergeable`, amber when close/regression-pending, muted when far). Center =
    `8/10` with a sub-label: **Ready to ship** / **2 blocking** / **regression
    pending**.
  - RIGHT: primary CTA — **Ship it** (enabled iff `mergeable`) → opens the
    deploy panel / triggers the ship flow; or **See N blockers** → Queue
    filtered to this sprint's fails. Secondary: Run regression, Attach tasks.
  - **Changelog panel** ("What's shipping"): the sprint's issues, positive-first
    ("12 passed · 2 to go"), collapsible, grouped by verdict. This is also the
    changelog source Thread B feeds to Slack/GitHub/GitLab.
  - Deploy panel (`SprintDeployPanel`) stays, mounted below.
- Celebratory shipped state: `mergeable` → emerald ring + "Ready to ship 🚀".

### A2. Health strip — `release-health-strip.tsx`
- Give sprint names room; tooltip the `✓✗◌` counts with labels.
- Add a mini readiness indicator (small ring or "8/10 ready").
- Sort sprints closest-to-shipping first (hierarchy).
- Integrate the "need a decision" chip inline, not floating.

### A3. Queue summary — `release-queue.tsx`
- Positive-first: lead with passed/ready, then needs-fix, then the rest.
- **Separate the stale backlog**: collapse the 35-stale historical noise into a
  secondary "Stale backlog (35)" affordance so it doesn't dominate the active
  release set.

### A4. Adopt `<Card>` / `<Badge>` in place of ad-hoc `rounded-lg border` divs;
labeled/tooltip'd iconography throughout. i18n: new keys in `qa_cockpit`, ALL 4
locales (en/ru/uz/zh-Hans — parity test enforces).

## Thread B — release-integrations hub (backend + settings)

The critical gap: **no ship/release/deploy event on the bus today.** deploy_event
rows are captured but never published; `sprintReadiness.Mergeable` is read-only;
the merge is a human action outside the system.

### B1. Release-events backbone
- Add event constants in `pkg/protocol/events.go`: `deploy:recorded`,
  `release:shipped` (a sprint's branch merged/deployed to a real environment).
- Publish them at the capture seams: `service/deploy_capture.go` after
  `InsertDeployEvent`, and where a sprint deploy to a `requires_human` /
  production environment succeeds (`connected_box.go` deploy path).
- Changelog builder: from the shipped sprint's issues (reuse sprint-readiness
  rows).

### B2. Per-workspace integration config (sealed)
- New workspace-scoped table `release_integration` (follow `lark_installation` /
  `zoho_connection`): `workspace_id`, `kind` (`slack|github_release|
  gitlab_release|sentry|bitrix|webhook`), `config jsonb` (non-secret: channel,
  repo, org…), `secret_encrypted bytea` (webhook URL / bot token / API token),
  `events text[]` (which lifecycle events fire it), `enabled`, probe status.
- Sealed via a new `AGORA_RELEASE_SECRET_KEY` (secretbox `sync.Once` loader,
  registry entry, fail-closed when unset). Admin-write, member-visible status
  (no secrets), probe-before-seal.

### B3. Dispatcher
- `registerReleaseOutbound` in `cmd/server/main.go`, modeled on
  `registerLarkPushListeners` / `registerBitrixOutbound`: subscribe the new
  release events, load enabled `release_integration` rows for the workspace, fan
  to connectors on detached goroutines with a bounded timeout. Reuse
  `webhook_delivery`-style dedupe + rate-limit + delivery log.

### B4. Connectors
- **Slack** (net-new, simplest — proves the pipeline): POST to an Incoming
  Webhook URL (or `chat.postMessage` with a bot token). Messages: "🚀 Sprint 9
  shipped" + changelog; "approval needed".
- **Bitrix** (reuse `integrations/bitrix/client.go` `AddTaskComment`): comment
  "shipped in release X" on each shipped issue's linked Bitrix task.
- **GitHub Release** / **GitLab Release**: `gh release create` in the daemon
  path (github: `daemon/health.go` gh usage) OR `POST /repos/.../releases` with
  an App installation token; GitLab via `/api/v4/projects/{id}/releases` or a
  `release` toolset on the injected MCP.
- **Sentry** (net-new): post-deploy health — after a release event, poll Sentry
  for new issues since the deploy and surface "0 new errors" back into the
  Release page / an inbox note.
- **Generic webhook** (extensibility — the "something else"): signed POST of the
  release event payload to an arbitrary URL.

### B5. Settings UI — `<ReleaseHubTab>` section in `integrations-tab.tsx`
- Credential-submit cards (zoho/figma/git pattern): per-connector enable +
  secret field (write-only, cleared after save) + non-secret config + which
  events fire it. Admin-gated affordances; backend enforces. API methods + zod
  schemas + malformed-response tests per the API-compat rule.

## Phasing
- **Phase 1** = Thread A (comfortable redesign). Ships first, demoable.
- **Phase 2** = B1 + B2 + B3 + generic webhook (backbone proven end-to-end).
- **Phase 3** = Slack + Bitrix connectors (highest value, reuse Bitrix client).
- **Phase 4** = GitHub/GitLab Releases + Sentry + the settings UI polish.
