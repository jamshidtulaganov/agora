---
name: agora-figma
description: "Use when an issue references a Figma design (a figma.com/design|file|proto URL in the description or your task context) — to read the design through the injected `figma` MCP server correctly: node-scoped reads, render downloads that survive URL expiry, rate-limit discipline, and the failure protocol when a file is private or quota-blocked."
user-invocable: false
---

# Reading Figma designs on Agora

When your task's issue references a Figma design, the platform injects a
`figma` MCP server (Framelink `figma-developer-mcp`) into your MCP config at
claim time, authenticated with the workspace's Figma token. Your task context
lists each referenced design as a ready-made call:

```text
FIGMA DESIGNS REFERENCED BY THIS ISSUE — read them with your `figma` MCP tools:
- https://www.figma.com/design/<fileKey>/...?node-id=208-5147
  → get_figma_data(fileKey="<fileKey>", nodeId="208:5147")
```

Every contract below is traced to source in
`references/figma-source-map.md`.

## URL → parameters

- The file key is the path segment after `/design/`, `/file/`, or `/proto/`.
- The node id in a URL is dash-separated (`208-5147`, sometimes `208%3A5147`);
  the API form is colon-separated (`208:5147`). The claim context already
  normalizes this — use its values verbatim.

## The two tools

- `get_figma_data(fileKey, nodeId)` — returns the simplified node tree
  (layout, text, styles) for the referenced node.
- `download_figma_images(fileKey, nodes, localPath, pngScale)` — downloads
  PNG/SVG renders of specific nodes into your workdir.

## Rules

1. **Node-scoped reads only.** Always pass `nodeId`. Never fetch a whole file
   — large files blow the response and burn quota. Don't pass `depth` unless
   a shallow overview is genuinely needed.
2. **Persist, never hot-link.** Figma render URLs expire (~30 days; image
   fills ~14). If you post images anywhere, `download_figma_images` into your
   workdir first and upload them as Agora comment attachments. Never commit
   renders into a git repo.
3. **Rate discipline.** The workspace token's REST budget is limited
   (~10-20 req/min on Dev/Full seats). Batch node ids into one call where
   possible, cache what you already fetched within the task, honor
   `Retry-After` on 429 exactly once before reporting yourself blocked.
4. **Failure protocol — never guess design content.**
   - 403/404: say the file/node is inaccessible (the workspace token's user
     likely lacks access to that file) and stop the design part of the task.
   - 429 with a monthly-bucket hint: the workspace token is from a View/Collab
     seat (~6 requests/MONTH) — tell the user an admin must replace it with a
     Dev/Full-seat token in Settings → Integrations → Figma.
   - Expired/invalid or missing credential: your claim context says so
     explicitly (and the figma tools are NOT injected in that case) — ask the
     user to renew/add it in Settings → Integrations → Figma.
   - In every case: report the failure in your comment; NEVER fabricate what
     a design "probably" looks like.

## The design_proposal action (analyze, don't build)

When you are fired for the `design_proposal` slice action you are a
DESIGNER-ANALYST: read the design, map it against the project's design system,
and PROPOSE an implementation decomposition for a human to approve. You do NOT
write code and you do NOT create issues.

Output a concise human-readable summary **in the same language as the issue
description** (Russian TZ → Russian summary), then exactly ONE fenced
```design-proposal``` block. JSON keys stay English; free-text VALUES follow
the issue's language. The server parses this block, attaches the
`design:proposed` label, and notifies the issue's humans.

Block contract:

```design-proposal
{
  "status": "ok" | "blocked",
  "reason": null | "figma_forbidden" | "figma_not_found" | "figma_quota" | "credential_missing" | "other",
  "reason_detail": "…",
  "figma":      [{"url": "…", "file_key": "…", "node_id": "208:5147"}],
  "screens":    [{"name": "…", "figma_node_id": "208:5147", "summary": "…", "render": "figma-208-5147.png"}],
  "components": [{"name": "…", "verdict": "reuse"|"extend"|"new", "code_ref": null|"path", "figma_node_id": null|"…", "notes": "…"}],
  "deviations": [{"aspect": "color"|"typography"|"spacing"|"other", "figma_value": "…", "project_value": "…", "question": "…"}],
  "sub_issues": [{"title": "…", "description": "…", "screens": ["…"], "node_ids": ["…"], "depends_on": [0]}],
  "open_questions": ["…"]
}
```

Rules:
- `render` MUST match the filename you uploaded (`figma-<node-id-dashed>.png`,
  e.g. node `208:5147` → `figma-208-5147.png`) so the review UI pairs each
  screen with its image.
- Classify aggressively toward REUSE — matching the existing app beats matching
  the mock pixel-for-pixel, especially on legacy codebases.
- A Figma value that contradicts the project's tokens/conventions is a
  `deviations` QUESTION, never a silent decision.
- If a link is inaccessible or you are quota-blocked (after honoring
  `Retry-After` once), emit `status:"blocked"` with a machine-readable `reason`.
  A blocked proposal is a valid output — NEVER fabricate design content.

## The gen_design_manifest action (the project's design memory)

When fired for `gen_design_manifest`, build/refresh the project's DESIGN
MANIFEST — the standing map of the project's design system that gets injected
into every future designer + implementation run so agents reuse components
instead of re-discovering them. Inspect the repo READ-ONLY (no push, no PR).

- **Token-based repos** (tokens.css / tailwind or theme config): read the token
  files, enumerate the shared component library → `kind:"tokens"`.
- **Legacy monoliths** (PHP/Yii + Vue like sd-main, no formal tokens): DERIVE
  the de-facto system — frequency-rank the top ~20 colors / font stacks /
  spacing values from shared CSS as de-facto tokens, enumerate Vue SFCs + Yii
  widgets/partials, record conventions + anti-patterns, write honest
  `legacy_notes` ("no tokens — copy markup from protected/views/…") →
  `kind:"inventory"`.

Output exactly ONE fenced ```design-manifest``` block, UNDER ~150 lines — this
is a MAP injected into prompts, not documentation. The existing manifest (if
any) is in your context: UPDATE it, PRESERVE human-added entries. Do NOT attempt
the Figma Variables API (enterprise-only). The server captures the block onto
the project (key-scoped — it never clobbers other project settings); you do NOT
run any command. A human-curated manifest (`source:"manual"`) is never
overwritten — your block becomes a proposal comment instead.

The `design_proposal` action also lazy-bootstraps a manifest: if no PROJECT
DESIGN SYSTEM context is provided, emit a ```design-manifest``` block before your
proposal.

## Design-aware QA (run_qa)

When a `run_qa` gate fires on an issue that implements a Figma design, your
context carries a DESIGN VERIFICATION appendix. After the functional checks:
download the reference render(s), open the implemented screen in the embedded
Chromium over CDP, and compare DETERMINISTICALLY — from the Figma node tree +
the PROJECT DESIGN SYSTEM, assert text/inventory/order and key colors, font
sizes, spacing via `getComputedStyle`. NEVER pixel-diff a screenshot.

Extend your `qa-result` JSON with a `design` object:
`"design":{"verdict":"pass"|"fail"|"skipped","reference_node":"208:5147","mismatches":[{"kind":"color"|"typography"|"spacing"|"layout"|"missing_element"|"other","selector":"…","expected":"…","actual":"…"}]}`.

The design verdict is ADVISORY: sub-pixel/font-rendering differences and
proposal-approved deviations are NOT mismatches; predating design debt is out of
scope. Apply `qa:fail` only when functional checks fail OR mismatches are severe
(missing elements, wrong colors on primary surfaces). If Figma is unreachable
(429 after one Retry-After, 403, expired credential) set `verdict:"skipped"`
with the reason — never fail the issue for an infra reason.

## The design_audit action (build the system, don't just consume it)

`design_audit` scans the repo READ-ONLY against the project manifest to find
where to BUILD a real design system out of the existing code — the inverse of
design_proposal. Output ONE fenced ```design-audit``` block:
- **off_token**: hardcoded values that should be tokens (raw hex/rgb, off-scale
  spacing, one-off fonts), frequency-ranked with a suggested token + sample refs.
- **duplicates**: the same markup copy-pasted across files that should be ONE
  shared component.
- **unmanaged_components**: shared components in code but missing from the manifest.
- **proposed_tokens**: the concrete token set to adopt (fewest tokens, most
  coverage) — the seed of a real tokens file.

Report only REAL findings you saw in the code — never invent counts or refs. You
do NOT change code; a human turns accepted proposals into a draft_code task.

## Design-system lint (run_qa, manifest projects)

When a `run_qa` gate fires on a project that has a design manifest, your context
carries a DESIGN-SYSTEM LINT appendix. If your diff touches UI, check whether the
CHANGE erodes the design system relative to the manifest — a raw hardcoded value
where a token exists, or a NEW component duplicating one the system provides.
Flag ONLY what the change introduces (pre-existing debt is out of scope). Record
findings under the qa-result `design.lint` array:
`{"kind":"off_token"|"duplicate_component"|"other","where":"path:line","issue":"…","severity":"warn"|"block"}`.
Use `block` only for a clear regression. A lint finding does not by itself set
the verdict — the platform gates on `block` findings when
AGORA_DESIGN_LINT_ENFORCED is on.

## What the platform does for you (don't redo it)

- Detects figma.com links in the issue (metadata stamp + live extraction).
- Injects/fills the `figma` MCP server with the workspace token at claim time
  — a token already set in your agent's own MCP config env wins over the
  workspace one.
- Pins the server version; the daemon image has it preinstalled (no cold npx
  fetch).

Figma's official MCP servers (remote OAuth / Dev-Mode desktop) are not usable
on headless runtimes — do not try to configure them.
