import { api } from "@tandem/core/api";

/**
 * SD shared skills, seeded onto every developer's helper agent during
 * onboarding so the whole team's AI assistants share the same SalesDoctor
 * context, conventions, and Dev+QA process.
 *
 * Why this lives in its OWN module (separate from
 * `welcome-after-onboarding.tsx`): the welcome component is heavily tested
 * and the seed is fire-and-forget background work. Keeping it isolated lets
 * the welcome tests mock `seedSdSkills` to a no-op without dragging the skill
 * bodies / dedupe machinery into their fixtures, and lets this module carry
 * its own focused unit tests.
 *
 * The SKILL.md bodies are intentionally English markdown — they are agent
 * prompts (CLI instructions), not UI chrome, so they are NOT localized and
 * add no i18n keys. The daemon receives full skill `content` in the
 * task-claim payload, so a DB skill row + an agent_skill link is enough — no
 * daemon file sync is required.
 *
 * Mechanism:
 *   1. `listSkills()` to dedupe against skills that already exist in the
 *      workspace (by name).
 *   2. `createSkill()` for each missing skill.
 *   3. `addAgentSkills(agentId, skill_ids)` to attach all of them — the
 *      server does `ON CONFLICT DO NOTHING`, so attaching is idempotent and
 *      never clobbers a dev's manual skill picks (unlike `setAgentSkills`,
 *      which replaces wholesale).
 *
 * Best-effort: every failure is swallowed (this must never block or fail
 * onboarding). A per-workspace-per-session guard prevents a second mount /
 * StrictMode double-mount from re-seeding; the guard is RELEASED on failure
 * so a later retry (e.g. the user re-enters onboarding after a transient
 * network blip) can run a fresh attempt.
 */

/** A starter skill definition. `name` is the dedupe key (server enforces
 *  UNIQUE (workspace_id, name)); `content` is the full SKILL.md body. */
export interface SdSkillSeed {
  name: string;
  description: string;
  content: string;
}

const SD_ARCHITECTURE_SKILL = `---
name: sd-architecture
description: SalesDoctor system architecture — the 3 sibling projects, the Yii/PHP + MySQL stack, multi-tenant d0_ prefix, and the billing -> main -> cs data flow.
---

# SalesDoctor Architecture

SalesDoctor (internal product name *Novus Distribution*) is a multi-tenant
Distribution CRM. There are **three sibling projects** that you will be asked
to work across:

## sd-main — Dealer CRM (system of record)
- The core Dealer CRM and the **system of record** for operational data.
- **Yii / PHP**, application code under \`protected/\`.
- **MySQL**, multi-tenant: every tenant's tables carry the **\`d0_\` prefix**.
- Repo: github.com/azizkh/sd

## sd-cs — Country Sales (HQ)
- HQ-facing "Country Sales": **reads many sd-main databases** to produce
  consolidated, cross-dealer reports.
- Read-heavy / reporting layer over the per-dealer sd-main databases.
- Repo: github.com/azizkh/cs3

## sd-billing — Subscriptions & Licensing (upstream)
- Handles subscriptions, licensing and billing for dealers.
- **Upstream**: it *pushes licences down* and *bills dealers*.
- Repo: github.com/azizkh/billing

## Data flow
\`\`\`
sd-billing  ->  sd-main  ->  sd-cs
(licences)     (operations)  (consolidated reports)
\`\`\`
- **sd-billing** is upstream — it provisions licences into and bills against
  the operational system.
- **sd-main** is the operational system of record (per-dealer, \`d0_\` tenant
  tables).
- **sd-cs** sits downstream, aggregating across many sd-main tenant
  databases for HQ reporting.

## Tech stack (all three)
- **Yii / PHP** framework, app code in \`protected/\`.
- **MySQL** with the multi-tenant **\`d0_\`** table prefix.
- Reference docs / RAG: the **sd-doc** Docusaurus site
  (architecture, ecosystem, db-schema, route-map) —
  https://github.com/jamshidtulaganov/sd-doc

Always know **which of the three projects** a task targets before you touch
code — they share conventions but are separate codebases with different
roles in the data flow.
`;

const SD_CODING_STANDARDS_SKILL = `---
name: sd-coding-standards
description: SalesDoctor coding conventions — Yii/PHP, JS, and mobile; tenant isolation; small focused diffs; open a PR, never merge.
---

# SalesDoctor Coding Standards

## Conventions by sub-team
- **Yii / PHP (sd-main, sd-cs, sd-billing)**: follow the existing Yii MVC
  structure under \`protected/\` (models / controllers / views, ActiveRecord).
  Match the surrounding file's style — do not introduce a new framework or
  pattern mid-codebase.
- **JS**: match the existing front-end conventions of the file you are
  editing; keep changes scoped to the feature.
- **Mobile**: follow the mobile app's existing module structure and
  platform idioms.

## Tenant isolation (critical)
- The data model is **multi-tenant**: tenant tables use the **\`d0_\`**
  prefix in MySQL.
- **Never** write a query that crosses tenants or hard-codes a tenant.
- Be especially careful with raw SQL and migrations — a missing tenant scope
  can leak one dealer's data into another's. When in doubt, scope explicitly.

## Diffs & review
- Keep diffs **small and focused** — one logical change per PR. Large,
  multi-concern diffs are hard to review and risky to merge.
- Don't reformat unrelated code or rename things outside the task scope.
- Write/keep tests passing for the code you touch.
- **Open a Pull Request — never merge.** A human reviews and merges. Your job
  is to produce a reviewable PR with a clear summary, not to land code.

## Secrets
- Never commit secrets, credentials, tokens, or \`.env\` values. Use the
  agent's Environment for runtime configuration (see the sd-qa-process skill
  for the QA_* env block).
`;

const SD_REVIEW_CHECKLIST_SKILL = `---
name: sd-review-checklist
description: SalesDoctor PR review checklist — scope, tenant safety, tests, no secrets, backward-compatibility, and a clear summary.
---

# SalesDoctor Review Checklist

Use this checklist when reviewing a change (yours or another agent's) before
asking a human to merge.

## 1. Scope
- Does the diff do **only** what the task asked? Flag unrelated changes,
  drive-by refactors, and reformatting noise.
- One logical change per PR.

## 2. Tenant safety
- Every query and migration respects multi-tenant isolation (the **\`d0_\`**
  prefix). No cross-tenant reads/writes, no hard-coded tenant.
- Raw SQL is scoped to the correct tenant.

## 3. Tests
- New behavior is covered by tests; existing tests still pass.
- Edge cases and failure paths are considered.

## 4. No secrets
- No credentials, tokens, API keys, or \`.env\` values committed.

## 5. Backward compatibility
- No breaking changes to shared schemas, APIs, or the data contract between
  **sd-billing -> sd-main -> sd-cs** without an explicit migration/plan.
- DB migrations are reversible / forward-safe.

## 6. Summary
- The PR has a clear summary: what changed, why, how it was tested, and any
  risks or follow-ups. A human reviewer should understand it without reading
  every line.

If anything fails the checklist, **request changes — do not merge.** Humans
own the merge decision.
`;

const SD_QA_PROCESS_SKILL = `---
name: sd-qa-process
description: SalesDoctor per-developer Dev + QA flow on your own <name>.sddev.uz box — branch btx-<id> from billing, the qa_switch.php hook, restore-to-billing, d0_ caution, and the QA_* env block.
---

# SalesDoctor Dev + QA Process (your own sddev box)

Every SalesDoctor developer has their **own always-on box** at
**\`<name>.sddev.uz\`** (e.g. \`jamshid.sddev.uz\`) — a live PHP/Yii instance
used for **both** the Dev process **and** the QA process. It is **not** a
shared server.

## The flow
1. **Branch**: cut a task branch named **\`btx-<task_id>\`** from the
   **\`billing\`** base branch.
2. **Code** the change, then **push** to your GitHub fork (the \`fork\`
   remote on the box).
3. **QA fast-path (seconds, no Docker)** — call the token-gated switch hook:
   \`\`\`
   POST $QA_SWITCH_URL?branch=btx-<task_id>&remote=fork
   Header: X-QA-Token: $QA_SWITCH_TOKEN
   \`\`\`
   The box does \`git fetch\` / \`checkout\` / \`composer\` / \`migrate\` /
   cache-clear in seconds.
4. **Smoke + UI test**: hit \`$QA_SDDEV_URL\`, log in at \`$QA_LOGIN_PATH\`
   with \`$QA_LOGIN\` / \`$QA_PASSWORD\`, run the test, capture screenshot
   evidence, and record a verdict.
5. **Restore the box to base** when done:
   \`\`\`
   POST $QA_SWITCH_URL?branch=$QA_SDDEV_BASE_BRANCH&remote=origin
   Header: X-QA-Token: $QA_SWITCH_TOKEN
   \`\`\`
   (\`$QA_SDDEV_BASE_BRANCH\` is **\`billing\`** — always leave the box back
   on the base branch so the next task starts clean.)

## qa_switch.php hook contract
- Endpoint: \`$QA_SWITCH_URL\` = \`https://<name>.sddev.uz/qa_switch.php\`.
- Auth: header \`X-QA-Token: $QA_SWITCH_TOKEN\` (token-gated).
- Params: \`branch=<branch>\`, \`remote=<fork|origin>\`.
- Switch to a task branch with \`remote=fork\`; restore with
  \`remote=origin\` + \`branch=billing\`.

## d0_ tenant caution
- The box runs a multi-tenant MySQL (\`test200\`) with the **\`d0_\`** tenant
  prefix. Be careful with migrations and raw SQL — never cross tenants or
  hard-code one. A bad migration here corrupts your whole QA box.

## Environment block (paste into Agent -> Environment)
Set these on **your agent's Environment** tab. Fill in the blanks for your
own box and credentials; leave \`QA_SDDEV_BASE_BRANCH\` and \`QA_LOGIN_PATH\`
as shown unless your box differs:

\`\`\`
QA_SWITCH_URL=https://<name>.sddev.uz/qa_switch.php
QA_SWITCH_TOKEN=
QA_SDDEV_URL=
QA_SDDEV_BASE_BRANCH=billing
QA_LOGIN=
QA_PASSWORD=
QA_LOGIN_PATH=/site/login
\`\`\`

With these set, you can run the full Dev + QA loop on your own
\`<name>.sddev.uz\` box: branch from \`billing\`, switch via \`qa_switch.php\`,
test, and restore — then open a PR for a human to review (**never merge**).
`;

/**
 * The starter skill set seeded onto every dev's helper agent. Order is the
 * attach order; dedupe is by `name`.
 */
export const SD_SKILLS: SdSkillSeed[] = [
  {
    name: "sd-architecture",
    description:
      "SalesDoctor system architecture — the 3 sibling projects (sd-main / sd-cs / sd-billing), the Yii/PHP + MySQL d0_ stack, and the billing -> main -> cs data flow.",
    content: SD_ARCHITECTURE_SKILL,
  },
  {
    name: "sd-coding-standards",
    description:
      "SalesDoctor coding conventions — Yii/JS/mobile, tenant isolation, small focused diffs, open a PR (never merge).",
    content: SD_CODING_STANDARDS_SKILL,
  },
  {
    name: "sd-review-checklist",
    description:
      "SalesDoctor PR review checklist — scope, tenant safety, tests, no secrets, backward-compatibility, summary.",
    content: SD_REVIEW_CHECKLIST_SKILL,
  },
  {
    name: "sd-qa-process",
    description:
      "SalesDoctor per-developer Dev + QA flow on your own <name>.sddev.uz box — branch btx-<id> from billing, the qa_switch.php hook, restore-to-billing, and the QA_* env block.",
    content: SD_QA_PROCESS_SKILL,
  },
];

/**
 * Per-workspace-per-session guard. Module-level (not a React ref) so a
 * StrictMode double-mount or two welcome flows for the same workspace share
 * one in-flight attempt instead of racing two seed passes. Keyed on
 * workspaceId; the value is the in-flight promise.
 *
 * On success the entry stays (don't re-seed this session). On failure the
 * entry is DELETED so a later retry runs a fresh attempt.
 */
const pendingSeed = new Map<string, Promise<void>>();

/**
 * Seed the SD shared skills into `workspaceId` and attach them to `agentId`.
 *
 * Best-effort and idempotent:
 *   - lists existing skills and only creates the ones missing by name,
 *   - attaches ALL SD skills (existing + newly created) via the idempotent
 *     `addAgentSkills` endpoint,
 *   - swallows every error (never throws — must not block onboarding),
 *   - runs at most once per workspace per session (guard released on
 *     failure so a retry can run again).
 *
 * Call as fire-and-forget: `void seedSdSkills(workspaceId, agentId)`.
 */
export async function seedSdSkills(
  workspaceId: string,
  agentId: string,
): Promise<void> {
  // Need both ids; a missing agent means nothing to attach to.
  if (!workspaceId || !agentId) return;

  const existing = pendingSeed.get(workspaceId);
  if (existing) return existing;

  const promise = (async (): Promise<void> => {
    // List once and dedupe by name. listSkills resolves the workspace
    // server-side from the X-Workspace-Slug header (no workspace arg), so
    // it returns this workspace's skills.
    const existingSkills = await api.listSkills();
    const byName = new Map(existingSkills.map((s) => [s.name, s.id]));

    const skillIds: string[] = [];
    for (const seed of SD_SKILLS) {
      const found = byName.get(seed.name);
      if (found) {
        skillIds.push(found);
        continue;
      }
      const created = await api.createSkill({
        name: seed.name,
        description: seed.description,
        content: seed.content,
      });
      skillIds.push(created.id);
      // Guard against duplicate SD_SKILLS names in the same pass.
      byName.set(seed.name, created.id);
    }

    if (skillIds.length > 0) {
      // Idempotent attach (server ON CONFLICT DO NOTHING) — safe to call
      // even if the agent already has some/all of these skills.
      await api.addAgentSkills(agentId, skillIds);
    }
  })();

  pendingSeed.set(workspaceId, promise);

  try {
    await promise;
  } catch {
    // Best-effort: never surface seeding failures to onboarding. Release
    // the guard so a future entry into onboarding can retry from scratch.
    pendingSeed.delete(workspaceId);
  }
}
