# Agora decoupling manifest — SalesDoctor → general dev-team product

Repositioning Agora from a SalesDoctor-internal tool to a product for 2-100
person **dev teams**. Source: a 4-agent codebase audit (Bitrix/Zoho/Lark,
Telegram/auth, QA-box/infra, product-surface). Plus two owner directives:
1. **Identity/member model must be tool-agnostic** — members join via
   signup/invite/SSO, never Bitrix-department provisioning.
2. **Bitrix (and Zoho/Lark) = just an external tool you connect via MCP** — no
   bespoke Agora↔tool coupling shipped in the core product.

## The good news (audit headline)

Agora was built with a **removable-connector pattern**. Almost everything
SD-specific is already **env-gated / default-OFF**. The core — nav, settings
frame, task/agent/SDLC/Release, `deploy_environments`, `qa_smoke_url`, auth — is
**generic**. The SD coupling is mostly **surface** (UI tabs, seed data, example
strings) that hides trivially, plus a few genuinely-woven tails (one Bitrix
comment column). The generic **"bring your own environment"** mechanism already
exists (`project.settings.qa_smoke_url` + `deploy_environments` with a `command`
kind = any `vercel/kubectl/fly/./deploy.sh`).

**One change demotes Bitrix+Zoho+Lark at once:** add `bitrix_enabled` /
`zoho_enabled` / `lark_enabled` to `/api/config` (`config.go`, derived from the
env gates the backend already checks) and wrap the three sections in
`integrations-tab.tsx` (+ the Bitrix JSX in `issue-detail.tsx:1364-1367` and
`project-detail.tsx:888`). SD sets the env → tabs render; a dev-team deployment
leaves them unset → clean Integrations tab (GitHub, MCP, Figma, Release).

---

## TIER 1 — Blocks the sale / day-one embarrassing (small, safe, do first)

| # | What | Where | Change | Effort |
|---|---|---|---|---|
| 1 | **SD skill-seed** — every new team's "Agora Helper" agent is preloaded with SalesDoctor's PHP-CRM architecture, `github.com/azizkh/sd`, `sddev.uz` QA, `billing` branch | `packages/views/workspace/sd-skills.ts` + `welcome-after-onboarding.tsx:24,176,191` | Delete the two `void seedSdSkills(...)` calls + import + the module | tiny |
| 2 | **`salesdoctor/<slug>` onboarding URL pill** — every customer sees "salesdoctor/" as their workspace host | `step-workspace.tsx:245,447,583` | Replace the `"salesdoctor/"` literal with a neutral host / bare slug | tiny |
| 3 | **Bitrix/Zoho/Lark integration tabs always visible** (Bitrix card doesn't even self-gate) | `config.go` (+ `packages/core/config`), `integrations-tab.tsx:22-27`, `issue-detail.tsx:1364-1367`, `project-detail.tsx:888` | Add `{bitrix,zoho,lark}_enabled` to `/api/config` from the env gates; gate the 3 sections + Bitrix issue/project JSX. (These already no-op without metadata — hiding is deleting imports/JSX, zero SD-backend risk) | small |
| 4 | **Telegram-only login forced on prod** — dev teams expect email/SSO | `deploy/fly/backend/fly.toml:27` (`AGORA_TELEGRAM_ONLY=true`) + set `GOOGLE_CLIENT_ID` | Flip to `false` (un-hides email OTP + Google button, zero code) + set `GOOGLE_CLIENT_ID` (Google SSO is **already fully built**, just dormant) | config only |

After Tier 1 the **day-one surface reads as a clean general dev-team product.**

### Self-host auth (Tier 1 #4 follow-up)

The login page now leads with **email OTP + Google SSO as the primary sign-in**;
the Telegram button is a secondary option that only appears when a bot is
configured (`TELEGRAM_BOT_USERNAME`). No code change is needed to get a
dev-team-appropriate login — the defaults already produce it:

- **Email OTP** — works out of the box, no configuration.
- **Google SSO** — *already fully built*, just dormant. A new customer enables
  it by setting **`GOOGLE_CLIENT_ID`** (public — surfaced to the web app via
  `/api/config`, which is what makes the Google button render) **and the
  `GOOGLE_CLIENT_SECRET` secret** (server-side, for the `/auth/callback`
  exchange). No rebuild required.
- **Telegram** — optional. Setting `AGORA_TELEGRAM_ONLY=true` is the
  SalesDoctor-only mode that hides email/Google and presents Telegram as the
  sole way in; a general deployment leaves it unset.
- **GitHub SSO** — *not built yet* — this is **Tier 2 #5** (mirror the Google
  handler). GitHub is the expected SSO for dev teams and is the one real auth
  gap.

`deploy/fly/backend/fly.toml` is **SalesDoctor's own** prod config (keeps
`AGORA_TELEGRAM_ONLY=true`) and is intentionally left untouched — the general
default (unset `AGORA_TELEGRAM_ONLY`, no Telegram bot) is already correct.

## TIER 2 — ICP-readiness builds (real work, needed for credibility)

| # | What | Why | Effort |
|---|---|---|---|
| 5 | **GitHub SSO** (net-new) — mirror the existing Google handler (`auth.go:531-692`): `GithubLogin` + `/auth/github` route + `github_client_id` config + login button, reuse `/auth/callback` + `findOrCreateUser` | GitHub is **THE** expected SSO for dev teams; only the GitHub App (webhooks) exists today, no login. The single real auth gap. | small-med |
| 6 | **Dynamic MCP core** (#35) — remote-URL HTTP/SSE + sealed auth | The **replacement mechanism** for Bitrix/Zoho/Lark: any tool connects via MCP instead of bespoke code. This is what makes "Bitrix = just an MCP tool" true. | med |
| 7 | **QA-box → BYO env** — cut the Yii `buildProvisionScript` (`remote_box_provision.go:95-123`) + `deploy/sdteam/` seed from the general product; add a `connected_box.serve_url` column (the one schema fix so boxes are BYO, replacing `boxSmokeURL`'s `/var/www/<fqdn>` convention). Demote the "Connected dev boxes" provisioning surface (already default-off via `AGORA_REMOTE_BOXES_ENABLED`). | A dev team points QA at THEIR staging URL (`qa_smoke_url`, already generic) — no sdteam.uz box machinery. | small-med |

## TIER 3 — Cleanup (cosmetic / latent, opportunistic)

- `slice_action.go:2329` — `billing` base-branch literal (latent: only non-empty for Bitrix-linked issues) → make the base branch configurable.
- Telegram Mini App defaults unknown locale to Russian (`apps/telegram/src/i18n/locale.ts:49`) → `en`.
- SD example strings in agent docs / comments / guide (`salesdoctor`/`sd-main`/`sdteam.uz`/`octane`) → neutral placeholders.

## NOT NOW — Bitrix woven-core tail (leave dormant; hard-fork later)

Bitrix's surface hides as easily as the others (Tier 1 #3), but it carries a
genuinely-woven core tail that should stay **dormant nullable**, NOT be ripped
out until a hard fork fully separates the SD product:
- `comment.bitrix_comment_id` (migration 152) threaded through ~13 generated
  comment queries + the core `Comment` model/response structs.
- `workspace_invitation.invitee_bitrix_id` (migration 150) + Bitrix branches in
  `CreateInvitation`/`AcceptInvitation`.
- `slice_action.go:394` btx-branch naming; `issue_video_frames.go` imports the
  bitrix package directly.
These are harmless unused columns/branches for a general (non-Bitrix) deployment.
Full rip-out = a real project, not needed to get Bitrix off the customer surface.

## Member/identity decoupling (owner directive #1)

Already tool-agnostic: `user_external_identity` (migration 120) is
provider-agnostic; `AGORA_DEFAULT_WORKSPACE_SLUGS` defaults **empty** (no blanket
join); Bitrix member auto-provisioning is env-gated. So member-decoupling =
simply not shipping the Bitrix provisioning surface (covered by Tier 1 #3) +
GitHub/Google SSO + invite as the join paths. No core rewrite.

## Bitrix/Zoho/Lark-as-MCP (owner directive #2)

Confirmed reachable two ways after demote:
- The **release-connector framework** already has Bitrix as a pluggable `kind`
  (`release_connectors.go:62`), alongside the dev-native slack/github/gitlab/
  sentry clients — the model for what a demoted connector looks like.
- **Dynamic MCP** (#35): a team that wants Bitrix/Zoho points the generic
  per-agent `mcp_config` at Bitrix/Zoho's hosted MCP (as Octane's Zoho agents
  already do). No bespoke coupling shipped.

## Recommended execution order

1. **Tier 1** (all four) — small, safe, unblocks the demo/sale. Do together.
2. **Tier 2 #6 (dynamic MCP)** then **#5 (GitHub SSO)** — the two real builds that
   make the ICP story true (extensible + dev-native auth).
3. **Tier 2 #7 (BYO QA)** — cut the Yii runbook, add `serve_url`.
4. **Tier 3** — opportunistic cleanup.
5. Bitrix hard-fork tail — deferred indefinitely (dormant).
