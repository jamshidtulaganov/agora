# agora-figma source map

Evidence layer for `SKILL.md`. Every contract the skill states is traced to a
current `file:line` here (derived on `sd-platform` at the Phase-1 design-stage
PR). Re-confirm exact lines after later phases move code.

## Link detection & claim context

| Behavior | File:line |
|---|---|
| URL regex (file/design/proto, file-key capture) | `server/internal/figma/links.go` (`urlRe`) |
| node-id normalization `208-5147`/`%3A` → `208:5147` | `server/internal/figma/links.go` (`nodeIDFromURL`) |
| `figma_links` metadata stamp (JSON-encoded string, ≤5 links, size-capped) | `server/internal/figma/links.go` (`LinksMetadataValue`); stamped in `server/internal/service/issue.go` (Create, inside the tx) |
| Union of stamp + live description extraction | `server/internal/handler/figma_links.go` (`issueFigmaRefs`) |
| Claim-time context note (ready-made `get_figma_data` calls, node-scoped mandate, persist-never-hotlink, 429 policy) | `server/internal/handler/figma_links.go` (`figmaContextForIssue`) |

## Credential & injection

| Behavior | File:line |
|---|---|
| Workspace credential table (single row per workspace, expiry/probe columns) | `server/migrations/139_figma_credential.up.sql` |
| Sealed at rest via `AGORA_FIGMA_SECRET_KEY` secretbox; 503 fail-closed when unset | `server/internal/handler/figma_credential.go` (`figmaCredentialBox`, `PutFigmaCredential`) |
| Save-time probe `GET /v1/me` → 422 only on 401/403; 429/5xx save as `unreachable` (Figma outage never blocks saving) | `server/internal/handler/figma_credential.go` (`probeFigmaToken`, `classifyFigmaProbe`, `PutFigmaCredential`) |
| Seat heuristic (monthly-bucket 429 ⇒ `low_seat`) | `server/internal/handler/figma_credential.go` (`probeFigmaSeat`) |
| Claim-time fill/auto-provision of the `figma` MCP server (synthesizes full doc for empty configs; operator non-blank env wins; empty-string key counts as blank and is filled) | `server/internal/handler/figma_mcp.go` (`injectFigmaMcpCreds`, `applyFigmaMcp`, `mergeFigmaMcpEnv`, `provisionFigmaMcpServer`) |
| The how-to note is gated on the tools actually being available; missing credential → `figmaMissingCredentialNote`, expired → `figmaExpiredCredentialNote` | `server/internal/handler/figma_mcp.go` (`applyFigmaMcp`); `server/internal/handler/daemon.go` (figma block) |
| Pinned server version (single constant; Dockerfile + UI preset track it) | `server/internal/handler/figma_mcp.go` (`figmaMcpVersion`); `Dockerfile.daemon`; `packages/core/mcp/types.ts` |
| Expired/invalid credential → instruction note instead of injection | `server/internal/handler/figma_mcp.go` (`figmaExpiredCredentialNote`) |
| Wire-in on the claim path (after Lark injection, next to QA-manifest context) | `server/internal/handler/daemon.go` (`ClaimTaskByRuntime`, figma block) |

## Endpoints

| Behavior | File:line |
|---|---|
| `GET /api/workspaces/{id}/figma-credential` — member-visible status, never token material | `server/cmd/server/router.go` (member group); `figma_credential.go` (`GetFigmaCredentialStatus`) |
| `PUT` / `DELETE /api/workspaces/{id}/figma-credential` — admin-only | `server/cmd/server/router.go` (admin group); `figma_credential.go` |

## design_proposal action (Phase 2)

| Behavior | File:line |
|---|---|
| `design_proposal` slice-action kind + recipe (analyze-not-build, node-scoped, block schema, language rule, blocked protocol) | `server/internal/handler/slice_action.go` (`sliceActionDesignProposal`, `buildSliceInstruction`) |
| Recipe assembly appends Figma how-to + design-manifest context | `server/internal/handler/slice_action.go` (`CreateSliceAction` design_proposal branch) |
| Designer-agent resolution (project.settings.design_agent → design squad leader) | `server/internal/handler/design_action.go` (`resolveDesignerAgent`); routed in `resolveSliceActionAgent` |
| Block parse (none/invalid/ok/blocked, fail-closed) | `server/internal/service/design_proposal.go` (`ParseDesignProposalBlock`) |
| Server-side capture: attach `design:proposed`, activity, inbox notify — both agent-comment ingest points | `server/internal/service/design_proposal.go` (`CaptureDesignProposal`); wired at `comment.go` + `task.go` |
| Design-state labels (mutually exclusive, publish EventIssueLabelsChanged) | `server/internal/service/design_proposal.go` (`SetDesignStateLabel`, `DesignLabel*`) |
| Review endpoint `POST /api/issues/{id}/design-review` (approve / request_changes + overrides; 404/409 matrix) | `server/internal/handler/design_review.go` (`CreateDesignReview`); route in `cmd/server/router.go` |
| Filename contract `figma-<node-id-dashed>.png` pairs a screen render to its comment attachment | recipe + `packages/views/issues/components/design-review-dialog.tsx` |

## design manifest (Phase 3)

| Behavior | File:line |
|---|---|
| `gen_design_manifest` slice-action kind + recipe (tokens vs inventory, legacy frequency-rank, 150-line cap, Variables-API prohibition) | `server/internal/handler/slice_action.go` (`sliceActionGenDesignManifest`, `buildSliceInstruction`) |
| Manifest context injector (rendered into designer + draft_code + every claim) | `server/internal/handler/design_action.go` (`sliceActionDesignManifestContext`, `renderDesignManifestContext`); wired in `slice_action.go` + `daemon.go` |
| Capture onto project.settings.design_manifest (key-scoped jsonb_set; manual-source → propose-via-comment) | `server/internal/service/design_manifest.go` (`CaptureDesignManifest`); wired at `comment.go` + `task.go` |
| Key-scoped write queries (never clobber sibling settings) | `server/pkg/db/queries/project.sql` (`SetProjectDesignManifest`, `SetProjectSettingKey`) |
| Human editor endpoint `PUT /api/projects/{id}/design-manifest` (manifest source=manual, design_agent, design_auto) | `server/internal/handler/design_manifest.go` (`PutProjectDesignManifest`) |
| Agent-generation trigger `POST /api/projects/{id}/design-manifest/sync` (chore issue + fire designer) | `server/internal/handler/design_manifest.go` (`SyncProjectDesignManifest`) |
| Lazy bootstrap inside design_proposal | `server/internal/handler/slice_action.go` (design_proposal recipe BOOTSTRAP clause) |

## decomposition + promotion (Phase 4)

| Behavior | File:line |
|---|---|
| Approve preflight (409 already_decomposed / previous_decomposition_exists) before label swap | `server/internal/handler/design_review.go` (`approveDesignProposal`, `designDecompositionPreflight`) |
| Deterministic sub-issue creation from the override-filtered plan; flat metadata keys design_proposal_comment_id/design_plan_index/design_depends_on stamped inside the create tx | `server/internal/handler/design_decompose.go` (`decomposeApprovedProposal`, `buildEffectiveDesignPlan`); `server/internal/service/issue.go` (`IssueCreateParams.Metadata`) |
| "Design context" child description (Figma links + component verdicts + parent pointer) | `server/internal/handler/design_decompose.go` (`composeDesignChildDescription`) |
| Dependency promotion on child-done (before notifyParentOfChildDone early returns; enqueues + publishes EventIssueUpdated; no env flag) | `server/internal/handler/design_decompose.go` (`promoteDesignDependents`); wired at top of `issue_child_done.go` (`notifyParentOfChildDone`) |
| Variant child-done comment ("promoted automatically — do not change sibling statuses") | `server/internal/handler/issue_child_done.go` (design-child branch) + `agora-squads/SKILL.md` |
| Supersede a prior decomposition (supersede_previous) | `server/internal/handler/design_review.go` (`createDesignReviewRequest.SupersedePrevious`) |

## External facts (not in-repo)

- Figma PATs hard-cap at 90 days (policy change 2025-04-28); View/Collab-seat
  tokens get ~6 Tier-1 requests/month — Figma REST rate-limit docs.
- Render URLs expire ~30 days, image fills ~14 — Figma REST `GET /v1/images`
  docs.
- Official Figma remote MCP is OAuth-browser-only; Dev-Mode MCP requires the
  desktop app — Figma help center / support statements (re-verified 2026-05).
