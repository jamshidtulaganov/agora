# Decision memory — the tracker remembers *why*, not just *what*

Status: G5 from `docs/market-research-synthesis.md` · designed 2026-09-20 ·
plan only, nothing built

## Thesis

The named small-team gap, verbatim: **"knowing a task was completed is less
useful than knowing *why*."** Devs answer it today by hand-rolling context
wikis on their laptops — the artifact the research calls out as a symptom,
not a solution.

Agora is unusually close to closing this, and does not know it. Tonight's
shipped primitives are not "features that happen to log things" — they are
**decisions with rationale, already structured, already attributed to a human,
already sitting in Postgres**:

- a human answering `task_escalation` is literally *question → options → choice
  → who → when → risk tier → issue* (migration 209);
- a `review-decision` carries an approve/request-changes verdict plus the
  human's note;
- a `qa-override` carries a verdict **and a free-text reason** and refuses
  machine credentials;
- an `assistant_pending_operation` records what a human authorized and which
  rows they unchecked;
- `github_pull_request.changed_paths` (migration 210) tells the server which
  code a decision was made *about*.

None of it is recalled. Not by a human three weeks later, not by the agent that
touches the same file next week, not by the assistant. The KB flywheel already
has a `decision` kind — and by construction it can never reach an agent
(§1.3).

So this plan is mostly **not new capture**. It is: harvest the decisions the
product already makes people type, give them a decider and a supersession link,
and push them back at the moment somebody is about to touch the code they
govern.

**Why this is defensible.** A landscape sweep found nobody shipping a
first-class decision object with rationale, rejected alternatives, an explicit
human sign-off, and a link back to the work that produced it (§2.5). Linear is
closest and has the right instinct — AI drafts, human approves, authorship stays
visible — but aimed at project-status narrative, not decisions. Confluence's
2026 answer is agents over existing pages; Slack's is recapping chat afterwards,
which cannot represent authority or rejected options. The standalone ADR tools
capture rationale properly and are disconnected from the tracker and largely
unmaintained. **Nobody has shipped an ADR wired into the task that produced it,
and nobody handles supersession.** Agora is the only product here that already
owns the decision *moment* — the escalation, the override, the review — because
it owns the agents that create them.

---

# Part 1 — Repo grounding

## 1.1 Where a decision with rationale already transits the system

Every row below is a human decision with its reason, already persisted. The
"recalled?" column is the whole point of this plan.

| # | Decision | Where the text lives | Rationale field | Decider | Recalled? |
|---|---|---|---|---|---|
| D1 | Agent asked, human answered | `task_escalation` — migration `server/migrations/209_task_escalation.up.sql:25-70`; written by `service.ResolveEscalation` `server/internal/service/escalation.go:188-250` | `prompt` (the ask) + `detail` ("what I tried") + `options[]` (alternatives) + `answer` | `answered_by` + `answered_at` (human-only: `RequireHumanActor`, `server/internal/handler/escalation.go:164-207`) | **No.** The answer becomes one comment (`postEscalationAnswerComment`, `escalation.go:357-383`) and is replayed into the parked run. After that it is thread scrollback. The issue card renders only the **open** one (`packages/views/issues/components/escalation-card.tsx:31`), so answered history is invisible in the UI. |
| D2 | Human approved / rejected a merge | `server/internal/handler/review_decision.go:49-130` (`CreateReviewDecision`) | `note` (≤2000 runes), plus the `merge:approved` label as the assertion | `requireUserID` + `RequireHumanActor` (`review_decision.go:22-27`) | **No.** Note → one audit comment (`review_decision.go:405`) + a label. |
| D3 | Human overrode a QA verdict | `server/internal/handler/qa_override.go:61-180` | `reason` — literally "WHY the human overrode" (`qa_override.go:48`), stamped into `qa_evidence.result_json.override` as `qaOverrideStamp{ByUserID, ByName, Reason}` (`qa_override.go:51-58`) | user id + name, human-only route | **No.** Buried in a jsonb blob on one evidence row. |
| D4 | Agent reviewed, verdict + findings | ```` ```review-result` ```` fenced block on the reviewer's comment; parsed by `server/internal/service/review_evidence.go:28-60`; read back by `server/internal/handler/review_verdict.go:41` | `summary` + `findings[]{file,line,severity,title,detail}` + `commit_sha` | reviewer agent (author ≠ author of the code) | **No.** Deliberately no table — "the comment IS the record" (`review_evidence.go:20-24`). Fine for a verdict; useless as memory. |
| D5 | Human authorized an assistant operation / plan | `assistant_pending_operation` — migration `198_assistant_pending_operation.up.sql:21-46`; plan confirm at `server/internal/handler/assistant_plans.go:426-490` | `summary` (built from resolved data *before* mutation), `arguments` (exactly what was authorized), `target`, and **`skipped_items`** — the rows the human deliberately unchecked (`assistant_plans.go:146-148`, executed at `:438`) | `user_id`; `status` records the human decision, `outcome` records what happened (migration `199`) | **Partially.** The receipt survives (FK relaxed to `ON DELETE SET NULL`, migration `200`) but is scoped to one assistant session transcript; nothing team-visible, nothing agent-visible. |
| D6 | Import mapping decisions | `import_job.plan` / `import_job.mapping` — "the mapping actually used, frozen at confirm" (migration `206_import_job.up.sql:22-25`); edited by `update_import_mapping` (`server/internal/assistant/tools.go:159`, `handler/assistant_imports.go:449`) writing `workspace.settings.import_mapping` | the mapping itself ("Waiting on customer means blocked") | `import_job.created_by` | **No.** A frozen receipt; never surfaced as "why our statuses are named this way". |
| D7 | Derived status moves | `postDerivedStatusProvenance`, `server/internal/handler/living_truth.go:278-320` — a `type='system'`, `author_type='system'` comment: *"Status: in_progress → done. PR #42 merged (close intent)."* | the provenance sentence | system, attributed | **N/A as memory** — but it is the house pattern for "never move state silently", and the pattern decision memory must copy for supersession. |
| D8 | Orchestrator stage casting | issue metadata keys `cast_qa_agent_id`, `cast_review_agent_id`, `pipeline_mode`, `orchestrator_agent_id` (`server/internal/handler/slice_action.go:1120-1136`, `handler/orchestration.go:571`) | none — the *choice* is stored, the *reason* is not | orchestrator or a human in the cockpit | **No**, and there is nothing to recall: casting has no rationale field at all. |
| D9 | Agent-declared decisions | issue metadata key `decision`, documented as a high-signal key in `server/internal/service/builtin_skills/agora-working-on-issues/SKILL.md:118` | free-form jsonb value | the agent | **No.** Unstructured, unreviewed, per-issue, never compiled. |
| D10 | Risk tiering of a module | `project.settings.risk_map` — `riskMapEntry{Module,Tier,Paths[],Owner,Notes}` (`server/internal/handler/project_risk_map.go:35-41`) | `Notes` | a human via `PUT /api/projects/{id}/risk-map` | **Yes, for agents** — injected on every claim via `sliceActionRiskMapContext` and kept even on lean briefs (`server/internal/handler/daemon.go:1731-1737`). This is the existence proof that path-scoped context injection works. |
| D11 | What is waiting on a human right now | `server/internal/handler/decision_queue.go` + `packages/core/types/decision-queue.ts`; rendered as the **Queue** tab of the Release page (`packages/views/qa/components/qa-page.tsx:30,51`) | `needed` / `needed_code` per row | — | **Open decisions only.** Computed on read, nothing stored, by design. Agora has a decision *queue*; it has no decision *log*. |

**The shape of the gap, in one line:** Agora is excellent at routing a decision
*to* a human and terrible at remembering it *from* one.

## 1.2 The KB flywheel Phase 1 pipeline, end to end

Shipped and live (plan: `docs/kb-flywheel-implementation-plan.md`).

**Capture trigger.** `maybeEnqueueKnowledgeCapture`,
`server/internal/handler/issue.go:2432-2492`. Fires on a genuine
`prev != done → done` transition only. Requires `issue.project_id` and a
resolvable KB name. Resolves (and auto-provisions) the workspace's
`"KB Synthesizer"` agent — `resolveKBSynthesizer`,
`server/internal/handler/knowledge_synth.go:77-170` — whose UUID is stamped
into `workspace.settings.kb_synthesizer_agent_id` and is the **ingest trust
anchor** (`service.findKBSynthesizer`, `service/knowledge_item.go:224-256`;
no name matching, because agent names are mintable). Backlog guard at 10
in-flight. Model escalated haiku→sonnet above `kbLargeContextRunes` on
claude runtimes (`issue.go:2496-2523`). Kill switch
`AGORA_KB_CAPTURE_DISABLED=1`; per-workspace opt-out = archive the agent.

**The contract.** `buildKnowledgeCapturePrompt`, `issue.go:2526-2536`:
"up to 5 new English items shaped as
`[{"kind","module","title","body"}]`. Allowed kinds: architecture, gotcha,
convention, nav, **decision**."

**Ingest.** `CaptureKnowledgeItems`, `service/knowledge_item.go:265-460`.
Extracts the ```` ```knowledge-items` ```` fence (`:52`), sanitizes every field
(NUL strip, HTML-comment-token strip, 3+-backtick collapse, 160/1200/64-rune
caps, `:88-160`), derives `norm_title` (`:166-180`), dedupes exactly (partial
unique index) and near-ly (Jaccard ≥0.6 same-kind / ≥0.8 cross-kind,
`:41-44`, `:201-217`), and gates by proposer trust. Spam guards:
`kbCaptureMaxItems=10` per comment, `kbCaptureProposedCeiling=100` unreviewed
proposals per KB. Wired from `handler/comment.go` and
`service.createAgentComment` (`service/task.go:2897`), always on
**pre-expansion** content.

**Trust gate.** `kbAutoAcceptKinds = {gotcha, nav}` — `knowledge_item.go:47`.
Comment, verbatim: *"Instruction-bearing kinds (convention, **decision**,
architecture) always land proposed — the human review gate against prompt
injection riding into future agents' context."*

**Storage.** `knowledge_item`, migration `146_knowledge_item.up.sql:19-47`:
`workspace_id, project_id, kb_name, module, kind, title, body, norm_title,
source_issue_id, created_by_type, created_by_id, status(active|proposed|
archived), hits, last_confirmed_at`. Dedupe/compile key is **`kb_name`**, not
`project_id`, because many projects may share one KB via
`project.settings.kb_skill`.

**Compile.** `RecompileKB`, `service/knowledge_compile.go:283-410`. Pure and
deterministic: active rows → a marker-delimited managed region
(`<!-- agora:kb:items:begin … -->`, `:69-70`) of the `<slug>-kb` **skill**,
sectioned by kind in fixed order — `Architecture, Conventions, Gotchas,
Navigation, **Decisions**` (`:88-103`). Rank order comes from SQL
(`ListActiveKnowledgeItemsForCompile`, `queries/knowledge_item.sql:40-46`:
`hits DESC, COALESCE(last_confirmed_at, created_at) DESC, …`); the budget is a
**ranking cutoff, not truncation** (`:160-190`). Region budget =
`clamp(20000 − runes outside the region, 4000, 12000)`. Serialized per
`(workspace, kb_name)` with `pg_advisory_xact_lock`. The region header
(`:78-84`) explicitly frames entries as *reference data, not instructions* —
the prompt-injection defense.

**Retrieval / injection.** `projectKBSkills`,
`server/internal/handler/project_knowledge_modules.go:142-210`, called from the
claim path at `server/internal/handler/daemon.go:1698-1714`. The base
`<slug>-kb` skill rides **every** claim (capped `projectKBBaseMaxChars = 16000`
runes, `:39`), plus up to `projectModuleKBMax = 3` module KBs
(`<slug>-kb-<module>`, 8000 runes each, `:25-31`) selected by the issue's
`module:<x>` labels (`:174-199`). Comment at `:55-57`: without this auto-ride
"the KB reaches an agent only via manual `agent_skill` binding — i.e. nobody."

**Human review API.** `server/internal/handler/knowledge_item.go` — `GET/POST
/api/projects/{id}/knowledge/items`, `PATCH/DELETE /api/knowledge-items/{itemId}`
(router `server/cmd/server/router.go:1267-1268`, `:1362-1365`). All mutations
`RequireHumanActor`: *"approving a proposed item IS the prompt-injection review
gate"*.

## 1.3 What is missing between "we store solutions" and "we store decisions"

Ten gaps, each grounded:

1. **No `why` field.** `title` is "one factual sentence", `body` is "why it
   matters and what to do" (`issue.go:2531`). That is an *instruction*, not a
   *rationale*. Nothing records the reasoning or what the choice cost.
2. **No options considered.** A decision you cannot re-evaluate is trivia. The
   only place alternatives exist today is `task_escalation.options[]`
   (migration 209:46-48) — and it is never carried forward.
3. **No decider.** `created_by_type/created_by_id` is the *proposer* — in
   practice always the KB Synthesizer agent. The human who approved leaves no
   trace: the `PATCH` that flips `proposed → active` writes no actor
   (`handler/knowledge_item.go:232-300`). **This violates the research
   anti-goal directly** — "never let AI author the artifacts humans must audit
   later without attribution".
4. **No supersession.** No link, no status, and the partial unique index
   `knowledge_item_kb_norm_title_idx … WHERE status <> 'archived'`
   (migration 146:42-44) plus `ListKnowledgeItemKeysForDedupe` (`… status <>
   'archived'`) means a *reversed* decision collides with the one it reverses.
5. **The dedupe merges reversals.** `kbJaccardSameKind = 0.6`
   (`service/knowledge_item.go:41`). Token sets for *"use postgres for the job
   queue"* vs *"use redis for the job queue"* are `{use,postgres,for,the,job,
   queue}` vs `{use,redis,for,the,job,queue}` → 5/7 = **0.71 ≥ 0.6** → the
   reversal is silently swallowed as a hit-bump on the decision it reverses.
   This is a live correctness bug for the `decision` kind.
6. **No source link beyond `source_issue_id`.** No PR, no escalation id, no
   comment anchor, no review verdict. You cannot open the argument.
7. **No governed-code link.** Nothing says *which code* a decision constrains,
   so nothing can surface it when someone touches that code — the single
   validated failure mode of every decision-record system (§2, source S9).
8. **Capture fires only on →done.** Decisions happen mid-flight — at the
   escalation, at the review, at the override — and on issues that never reach
   done. `cancelled` is *"we decided not to"* and captures nothing.
9. **No human surface, at all.** The review API exists; **no client calls it**.
   `packages/core/api/client.ts` has only `POST /knowledge/build` (`:2987-2990`);
   there is no `knowledge/items` call anywhere in `packages/`, no
   `ProjectKnowledgeSection`, no `project-conventions-section.tsx` (the KB
   plan's Phase 3 template reference points at a file that was never written).
   Net effect: **`decision`-kind items always land `proposed`, nothing can
   approve them, so they never compile and never reach an agent.** The decision
   half of the KB flywheel is write-only today.
10. **Ranking locks humans out.** Compile order is `hits DESC` first, and
    `BumpKnowledgeItemHits` is reachable only from the trusted-synthesizer path
    (`service/knowledge_item.go:349-360`; untrusted near-dupes are `skipped`).
    A human-authored decision starts at `hits=0` and can never climb.

---

# Part 2 — External evidence (2025–2026)

Only findings that change a design choice. Full URLs + dates in Appendix A;
`[Sn]` refers to that list.

## 2.1 Why ADRs get abandoned — and the one thing that makes them stick

- **Writing them was never the problem; reading them was.** Decision Guardian's
  motivating line is *"Nobody read them before opening a PR"* `[S9]`. Its entire
  mechanism is PR-time surfacing: match changed files against a decision
  registry, post a comment. **Design consequence:** the primary retrieval
  surface is not a page, it is an interception at the moment of relevant
  change. Agora already has the input (`changed_paths`, migration 210) and the
  glob matcher (`riskGlobMatch`, `handler/risk_tier.go:143`).
- **Prescribed process ≠ observed behaviour.** AWS's 2025 best-practice piece
  prescribes async "readouts", a central repo, and explicit supersession
  linking `[S5]`; `[S9]` exists because in practice nobody consults the log.
  Believe the observed side.
- **Lightweight, close to the work, and called "decisions".** Every practitioner
  source converges: one decision per record, context/decision/consequences,
  in-repo rather than wiki, heavy MADR-style templates kill adoption
  `[S5][S6]`. The canonical ADR repo notes teams get more buy-in from the word
  *"decisions"* than *"ADRs"* `[S2]`. **Consequence:** no 8-field form, and the
  product word is "Decisions".
- **Immutability + supersession is the settled norm.** *"You don't edit old
  ADRs… superseding ADRs preserve historical reasoning"* `[S6]`. This is
  exactly Agora's own living-truth discipline applied to memory.
- **Scaling friction is social, not technical.** Decentralised ADRs work until
  people start asking whether a decision is "official" without sign-off `[S4]`.
  At 2–10 people this argues for a single explicit decider field and no
  approval hierarchy.

## 2.2 AI-authored decision records: the sceptics have the data

- **ThoughtWorks Technology Radar Vol 34 (Apr 2026)** puts *agent instruction
  bloat* in **Caution**, *curated shared instructions* in **Adopt**, and
  *progressive context disclosure* in **Trial** `[S1]`. Its load-bearing line:
  *"many teams use AI to generate these instruction files, but research suggests
  hand-written versions often prove more effective than LLM-generated
  alternatives."* The prescribed fix is architectural — load only what is
  relevant to the current task — not better templates.
- The enthusiast counter-case (auto-generate a backlog of ADRs from a codebase,
  agent proposes an ADR after implementing) ships **no human-review step and no
  accuracy discussion** `[S3]`. Treat it as the naive baseline.
- The canonical ADR repo now ships Claude Code skills for writing decisions and
  recommends *"retrieval-led over pre-training-led reasoning"* `[S2]` — make the
  model read the actual diff/issue, never invent a rationale.
- Fowler's bliki, **updated March 2026, still mentions AI nowhere** `[S7]`. The
  "ADRs as agent memory" thesis is real but not canon.
- **The distrust is measured, not vibes.** Stack Overflow Developer Survey
  2025: **46%** of developers *actively distrust* AI output vs **33%** who trust
  it; only **3.1%** highly trust AI-generated code against **19.6%** who highly
  distrust it; **66%** are frustrated by answers that are *"almost right, but
  not quite"*; overall favourable sentiment toward AI tools fell from >70%
  (2023–24) to **60%** `[S26]`. An unsigned AI-written rationale is, on this
  data, more likely to be disbelieved than believed. Even *labelling* AI
  authorship is contested — Torvalds publicly dismissed the framing in the
  kernel-docs "AI slop" debate `[S27]` — which is why Agora's answer must be a
  **named human accepter**, not a robot badge.
- **Consequence for Agora:** synthesis is the *third* capture path, never the
  first; it drafts from text a human already wrote (escalation answer, review
  note, override reason) rather than from the model's own reasoning; and it
  lands `proposed` with an explicit "AI-drafted · approved by <human>" byline.
  That is already the repo's posture (`kbAutoAcceptKinds` excludes `decision`)
  and this plan keeps it.

## 2.3 The sharpest critiques of "company brain" decision capture

From the Launch HN thread for a 2026 YC decision-memory startup `[S8]`:

- *"loses sooooooo much meaningful context… completely fails to capture
  intent"* — knowledge-graph capture surfaces tangential facts in unrelated
  sessions because it knows *that* a decision exists, not *why it mattered*.
  **Consequence:** the decision object must carry the question and the rejected
  options, or it is just another fact table.
- *"context accurate Monday becomes silently wrong by Friday"* — the temporal
  failure mode; nobody notices until a decision is made on stale grounds.
  **Consequence:** staleness must be a computed, visible property, and
  superseded records must be invisible to agents.
- The founders attributed prior failures (wikis, Confluence, ADRs) to UX; the
  commenters pushed back that it is an intent/provenance/staleness problem
  better UX will not fix. Side with the commenters.
- Landscape note: at least five 2026 startups (Hyper, Hopsule, Decispher,
  TraceMem, Praxos) are converging on this exact problem `[S8][S9][S10]`. Two
  observations matter: (a) the best of them gate on human approval —
  Hopsule's *"humans always decide"* `[S10]`; (b) **none of them discloses a
  staleness or supersession mechanism.** That is the differentiated surface.

## 2.4 Lean context: more retrieved decisions makes agents *worse*

This is the evidence that sets the injection budget.

- **Context rot is measurable well inside the window.** Chroma, July 2025,
  18 models, input length as the only controlled variable: accuracy degrades as
  input grows *even on trivial tasks*, worse with distractors and low
  needle-question similarity `[S14]`.
- **NoLiMa (ICML 2025):** with lexical overlap removed, **11 of 13** long-context
  models fell below **50%** of their short-context baseline at **32K tokens**;
  GPT-4o **99.3% → 69.7%** `[S15]`.
- **Retrieval quality plateaus then declines**: Databricks' 2,000+ experiment
  sweep found degradation past ~16K tokens for GPT-4-turbo / Claude-3-sonnet,
  and as early as 4K–8K for weaker models `[S16]`.
- **Context clash is the stale-record failure mode, quantified.** Breunig's
  survey names poisoning / distraction / confusion / clash, and cites a
  Microsoft–Salesforce result: conflicting information delivered across turns
  caused an **average 39% accuracy drop** (o3: 98.1 → 64.1) `[S18]`. **A
  superseded decision injected alongside its replacement is worse than no
  decision at all.**
- **Anthropic's own numbers favour externalise-and-trim**: memory tool +
  context editing = **+39%** on an internal agentic-search eval; context editing
  alone cut token consumption **84%** over a 100-turn run `[S17]`.
- **Progressive disclosure is the shipped pattern, twice.** Claude Code memory:
  target **<200 lines** per file, auto-`MEMORY.md` hard-capped at 200 lines /
  25KB, topic files read on demand; a `project` memory type is explicitly
  defined for *"decisions that Claude can't derive from the code or git
  history"* `[S11]`. Agent Skills: frontmatter only at rest (1,536 chars across
  *all* skills), body on invocation, references on explicit read; on compaction
  each skill is capped at 5,000 tokens inside a **25,000-token combined
  ceiling** `[S13]`. Anthropic's context-engineering guidance frames the goal as
  *"the smallest possible set of high-signal tokens"* and sub-agent returns of
  **1,000–2,000 tokens** `[S12]`.
- **Hybrid retrieval beats pure vectors, and BM25 wins on identifiers.**
  Anthropic's Contextual Retrieval: embeddings-only 5.7% top-20 failure →
  contextual embeddings **−35%** → **+ BM25 −49%** → **+ reranking −67%**
  `[S19]`. Decision records are dense with exact tokens (issue keys, file paths,
  library names) — precisely where lexical matching wins.
- **No universal RAG-vs-long-context answer.** NVIDIA's "long context
  consistently outperforms RAG" `[S20]` is directly rebutted by LaRA
  ("No Silver Bullet", 11 models / 2,326 cases) `[S21]`. Tune k empirically;
  do not assume more context is better.

**Consequences, concretely:** inject **top-3 to top-5** decisions, **≤2,000
tokens total**, question + choice + one-line rationale only (options-considered
is human audit material and stays out of agent context), **superseded filtered
at retrieval time**, and never a dump of the log. Note the starting position is
already heavy: the base KB alone is capped at **16,000 runes** on every claim
plus up to **3 × 8,000** module runes (`project_knowledge_modules.go:25-39`) —
roughly 10K tokens before a single decision is added.

## 2.5 Nobody owns this — and the closest competitor's house style is attribution

A deliberate sweep of the tracker/wiki/chat landscape found **no product that
ships a first-class decision object with rationale, rejected alternatives, an
explicit human sign-off, and a link back to the work that produced it** `[S28]`.
What exists instead:

- **Linear is closest, and is building the right instinct in the wrong place.**
  Three 2026 shipments stack up: *Initiatives* carry a `Latest Update` and a
  computed on-track/at-risk health rollup `[S29]`; *Documents* now render
  **agent edits visually separated from human edits** with a "show author names"
  toggle so you can tell "if text was written by a colleague or added by a loop"
  `[S30]`; *Loops* and *agent-assisted project updates* have an agent read the
  changes since the last update plus the linked Slack channel, draft an update
  for a human to refine, and — for a Loop — "update the document and leave a
  note explaining why" `[S31][S32]`. **The house style is explicitly "AI drafts,
  human approves, authorship stays visible."** But it is scoped to *project
  status narrative*, not to a structured decision entity. That is the open gap,
  and it is the gap G5 names.
- **Atlassian's 2026 answer to knowledge sitting unused in Confluence is not a
  better wiki — it is agents bolted on top of one** (Confluence Agents, "over 5M
  invocations… every month") `[S33]`. The incumbent is conceding that flat pages
  are not self-serving knowledge. Building another doc surface is building the
  thing they are routing around.
- **Slack's 2026 bet is "chat is the memory, recap it later"** — AI recaps of
  long threads, auto-conversion of "requests and decisions into assigned tasks"
  `[S34]`. Structurally it cannot represent *who had the authority to decide* or
  *what was explicitly rejected*, only what was said. This is the anti-pattern.
- **The ADR tool ecosystem is stagnant.** `log4brains` (~1.6k stars, no recent
  release activity) and `adr-tools` (~5.7k stars, 32 open issues / 37 open PRs,
  no visible 2025–2026 activity) are static-site-generator-era software living
  in a repo folder, disconnected from any tracker; the Backstage ADR plugin
  survived only by being folded into a generic community monorepo `[S28]`.
  **Nobody has shipped "an ADR wired into the task that produced it."**

**Consequences:** (a) bind every decision to the exact issue / PR / escalation
that produced it — that is what stops it rotting, because it is *discovered in
context* rather than browsed; (b) match Linear's attribution floor and go one
better with a named human **decider** and a named human **accepter**; (c) do not
build a doc surface; (d) never make retroactive thread summarisation the primary
capture path.

## 2.6 Where decisions actually get made, and the format that survives

- `AGENTS.md` (Agentic AI Foundation / Linux Foundation; OpenAI Codex, Cursor,
  Amp, Jules; 60K+ repos) is the emerging cross-tool convention for agent-facing
  context distinct from a human README `[S22]`. Plain markdown, no required
  fields, nested with closest-wins precedence. Agora's compiled
  `<slug>-kb` skill region is the same idea, server-owned.
- The MCP reference memory server is an entity/relation/observation knowledge
  graph with **nothing about pruning or scale** in its own docs `[S23]`; the
  2025–2026 proliferation of competing reimplementations branded around
  "long-term", "causal", "local-first" is the market's verdict on the flat
  unpruned design.
- Skills cost "a few dozen tokens" at rest vs MCP tool definitions that can burn
  tens of thousands just declaring themselves `[S24]`. **Consequence:** a
  decision-query MCP/assistant tool is fine; a decision *memory server* that
  dumps a graph into context is not.

---

# Part 3 — The plan

## 3.1 The decision object

**Storage choice: extend the KB, do not build a parallel store.**

`knowledge_item` already owns everything a decision store needs and would
otherwise have to be rebuilt: sanitisation, dedupe, the human review gate
against prompt injection, a deterministic compile, a bounded injection budget,
and a live path into every agent claim. A separate `decision` table means a
second compile, a second review gate, a second budget and a second thing that
rots — the parallel abstraction `CLAUDE.md` forbids. `kind='decision'` already
exists and already has its own section in the compiled region
(`knowledge_compile.go:102`).

What it lacks is decision-specific structure. Add it as a **1:1 sidecar**, so
the general table stays general:

```
-- migration 211 (additive)

ALTER TABLE knowledge_item
  ADD COLUMN decided_by_type text,           -- 'member' | 'agent' | 'system'
  ADD COLUMN decided_by_id   uuid,           -- WHO decided; NULL until accepted
  ADD COLUMN decided_at      timestamptz,
  ADD COLUMN accepted_by     uuid REFERENCES "user"(id) ON DELETE SET NULL,
  ADD COLUMN superseded_by   uuid REFERENCES knowledge_item(id) ON DELETE SET NULL;

CREATE TABLE knowledge_decision (
  item_id     uuid PRIMARY KEY REFERENCES knowledge_item(id) ON DELETE CASCADE,
  question    text NOT NULL DEFAULT '',   -- what was being decided
  rationale   text NOT NULL DEFAULT '',   -- WHY. the whole point of G5.
  options     text[] NOT NULL DEFAULT '{}', -- what was considered and rejected
  origin      text NOT NULL DEFAULT 'manual',
      -- escalation | review | qa_override | assistant_op | import
      -- | comment_marker | synthesis | manual   (NO CHECK — new origins
      -- must not need a migration; unknown renders generically)
  origin_id   uuid,                        -- task_escalation.id / operation id / …
  origin_url  text NOT NULL DEFAULT '',    -- PR url, external ref
  paths       text[] NOT NULL DEFAULT '{}' -- the code this decision governs
);
CREATE INDEX idx_knowledge_decision_origin ON knowledge_decision(origin, origin_id);
```

Field-by-field against the brief's shape:

| Asked for | Lives in | Note |
|---|---|---|
| question / context | `knowledge_decision.question` + `knowledge_item.title` | title stays the one-line recall string (the compiled bullet) |
| options considered | `knowledge_decision.options[]` | seeded free from `task_escalation.options` |
| choice | `knowledge_item.body` | already sanitised, 1200-rune cap |
| rationale | `knowledge_decision.rationale` | from `detail` / `note` / `reason` — the human's own words |
| who decided | `knowledge_item.decided_by_*` + `accepted_by` | **two distinct people**: the decider and whoever accepted it into memory. Attribution anti-goal closed. |
| links | `source_issue_id` (exists) + `origin`/`origin_id`/`origin_url` | opens the argument |
| governed code | `knowledge_decision.paths[]` | the retrieval key |
| supersession | `knowledge_item.superseded_by` + status `'superseded'` | §3.5 |

**Three migration details that are load-bearing, not incidental:**

1. `knowledge_item_kb_norm_title_idx` is `WHERE status <> 'archived'`
   (migration 146:42-44). It must become `WHERE status IN ('active','proposed')`
   or a superseded decision keeps blocking its own replacement.
2. `ListKnowledgeItemKeysForDedupe` (`queries/knowledge_item.sql:48-51`) has the
   same `status <> 'archived'` predicate — same fix.
3. **`kind='decision'` must be excluded from the Jaccard near-duplicate merge**
   (`service/knowledge_item.go:201-217`). §1.3 gap 5 shows a reversal scores
   0.71 against the decision it reverses and would be swallowed as a hit-bump.
   For decisions, a near-duplicate is a *supersession candidate*, never a
   confirmation: surface it in review, do not merge it.

## 3.2 Capture — three paths, in strict priority order

### Path 1 (primary): harvest the shipped primitives — zero new friction

An escalation resolution **is** a decision. No new object, no new ritual, no LLM.
One server-side seam, `service/decision.go`:

```go
func (s *TaskService) RecordDecision(ctx, in RecordDecisionInput) (db.KnowledgeItem, error)
// in: workspace, project, issue, kind='decision', question, choice, rationale,
//     options, decidedByType/Id, decidedAt, origin, originID, originURL, paths
```

Call sites, each harvesting one shipped primitive:

| Call site | Harvests | question ← | choice ← | rationale ← | decider ← |
|---|---|---|---|---|---|
| `service.ResolveEscalation` `escalation.go:188`, after `postEscalationAnswerComment` | `task_escalation` (migration 209) | `esc.prompt` | `esc.answer` | `esc.detail` | `esc.answered_by` |
| `handler.approveReviewDecision` / `requestReviewChanges` `review_decision.go:88,427` | review verdicts | "Merge \<issue\> at \<sha\>?" | approve / request_changes | `note` | `userID` |
| `handler.OverrideQAVerdict` `qa_override.go:61` | QA overrides | "QA verdict was \<agent verdict\> — ship anyway?" | `verdict` | `reason` | `userID` |
| `handler.confirmAssistantPlan` `assistant_plans.go:426` | plan confirmations | `op.summary` | authorized rows | which rows were **unchecked** (`skipped_items`) | `op.user_id` |
| import confirm (`import_job` → `awaiting_confirm → running`) | mapping decisions | "How do \<source\> statuses map?" | `import_job.mapping` | plan diff | `created_by` |

**The promotion gate.** Not every answer is durable — *"which of these three
files did you mean?"* is not memory. So harvesting always writes
`status='proposed'` and the human's one gesture promotes it. That gesture is a
**checkbox in the box they are already typing in**, not a separate ritual:

> ☑ **Remember this decision** — pre-checked for escalation `kind ∈
> {permission, risk, blocked}`, for every `qa-override` (an override always has
> a reason and always contradicts a machine), and for `request_changes` with a
> non-empty note. Unchecked by default for `kind ∈ {question, budget}` and for
> bare `approve`.

Checked → the row is written `status='active'` with `decided_by` = the human
and `accepted_by` = the same human, and `RecompileKB` fires. Unchecked → no row
at all (not a proposal; an un-promoted proposal is just review-queue debt, and
the queue already saturates at 100).

This is the whole answer to §2.1: capture happens *at the moment of decision*,
in the surface the decider is already in, costing one already-moving click.

### Path 2 (secondary): explicit human record

- **On the issue:** a "Record decision" action in the comment composer opening a
  three-field form — *What was decided · Why · What else we considered*. Three
  fields, never eight `[S5][S6]`.
- **From the CLI / an agent:** `agora issue decide --question … --choice …
  --because … --option …` writes `status='proposed'` with
  `decided_by_type='agent'`. It can never self-accept —
  `kbAutoAcceptKinds` (`knowledge_item.go:47`) already excludes `decision` and
  stays that way.
- **The assistant:** `record_decision` routed through the existing
  `assistantAwaitConfirmation` seam, because it writes team-visible
  authoritative content (assistant parity rule: confirm-then-do).

### Path 3 (tertiary, review-gated): synthesis

Keep the on-done synthesiser's `decision` kind, with three changes that make it
honest per the §2.2 anti-goals:

1. **Ground it.** The capture prompt (`issue.go:2526`) gains: *"A `decision`
   item must quote the escalation answer, review note, override reason, or
   comment that contains the human's own words, and cite it. If no human stated
   the decision, do not emit one."* Retrieval-led, not pre-training-led `[S2]`.
2. **Attribute it.** `decided_by_type='agent'`; on approval, `accepted_by` is
   stamped and the UI + compiled bullet render *"AI-drafted · accepted by
   \<name\>"*. No unattributed AI authorship reaches the log.
3. **Extend the trigger beyond →done** (KB Phase 2's multi-signal ambition,
   narrowed): also fire on `→ cancelled` — *"we decided not to"* is the decision
   most worth keeping, and today it captures nothing.

## 3.3 Retrieval

### (a) Humans

Three surfaces, in build order, and deliberately **no new top-level route**.

1. **Inline on the issue — the cheapest, highest-value fix.** `EscalationCard`
   (`packages/views/issues/components/escalation-card.tsx:29-34`) renders only
   `status === "open"` and drops the answered history the endpoint already
   returns. Rename to a **Decisions** strip: open escalation first (unchanged),
   then the issue's recorded decisions — question, choice, who, when, a link to
   the origin. This alone turns D1–D5 from scrollback into memory.
2. **Per project — the never-built KB Phase 3 UI.** `ProjectDecisionsSection` in
   `packages/views/projects/components/`, template
   `project-risk-map-section.tsx`, mounted in `project-detail.tsx` beside
   Resources (`:886`). Backed by the API that already exists and that **no
   client calls** (`router.go:1267-1268`, `:1362-1365`). Needs
   `knowledgeItemsOptions` in `packages/core/projects/queries.ts`, zod schemas +
   `EMPTY_` fallbacks in `packages/core/api/schemas.ts` with the mandatory
   malformed-response test, and optimistic accept/supersede/archive mutations.
   Web + desktop wiring per the cross-platform rules.
3. **Search.** Extend the project items endpoint with `?q=` over
   `title/body/question/rationale` (ILIKE + token overlap — the repo has no
   tsvector and does not need one at this corpus size), and surface decisions in
   the existing global search. No dedicated search page.

**Why no `/[ws]/decisions` route:** the Release page already owns *open*
decisions as its **Queue** tab (`packages/views/qa/components/qa-page.tsx:30,51`).
A decision *log* is a reference surface, not a daily one; a second top-level
"Decisions" entry next to a "Queue" that means something different is exactly
the config sprawl the research names as the root cause of tracker hatred, and it
violates the standing UI-subtraction rule. Revisit only if usage proves the
project section is being hunted for.

### (b) Agents — lean, path-scoped, top-k

**Not** as a section of the always-injected KB blob. Decisions get their own
small, computed block, appended where the risk map already is
(`handler/daemon.go:1735-1740`) — the existence proof that path-scoped injection
works and survives a lean brief.

**Relevance, pre-pgvector.** The join already exists; no embeddings required:

1. `github_pull_request.changed_paths` (migration 210) — the PR's real file
   list, never self-reported.
2. `riskGlobMatch` (`handler/risk_tier.go:143-185`) matches those paths against
   `project.settings.risk_map` entry globs → the **module** names. A
   `riskModulesForPaths(entries, paths) []string` sibling of `riskTierForPath`
   (`:186`) is ~15 pure lines.
3. `knowledge_item.module` is already that same vocabulary, and already drives
   module-KB injection (`project_knowledge_modules.go:174-199`).
4. `knowledge_decision.paths[]` gives a direct glob hit, ranked above a module
   hit.

Ranking, in order: direct `paths[]` glob hit on a changed file → module match →
`module:<x>` label match (the no-PR-yet fallback, same source triage already
uses) → title/body token overlap with the issue title + acceptance criteria
(lexical, which is where the evidence says the win is for identifier-dense text
`[S19]`) → recency. Ties break on `decided_at DESC`, **not** on `hits` — §1.3
gap 10 means `hits` structurally excludes human-authored rows.

**Budget and shape** (from §2.4):

- `k = 5`, `k = 3` when `leanBriefApplies` (`daemon.go:1718-1730`) is true.
- **≤2,000 tokens total**, hard-capped server-side.
- Each entry renders as: `**<title>** — decided <date> by <name> (<origin
  link>)` + one rationale line. **Options-considered is never injected** — it is
  human audit material and it doubles the cost.
- `status='superseded'` is filtered at retrieval, always `[S18]`.
- The block carries the same defensive header the KB region uses
  (`knowledge_compile.go:78-84`): reference data, not instructions.
- Empty result → **no block at all**. A "no relevant decisions" line is pure
  token cost.
- Config, in `server/internal/config/registry.go` next to the QA keys
  (`:43-48`), `ProjectScoped: true`:
  `AGORA_DECISION_INJECT_ENABLED` (KindBool, default `true`),
  `AGORA_DECISION_INJECT_K` (KindInt, default `5`). Every AI surface
  disableable — the standing rule.

**When pgvector earns its place.** Only when a single project exceeds ~400
active decisions **and** instrumentation shows the path join missing relevant
records. Migration 146's schema was written for it (stable `id`, short
self-contained `body`, `module`/`kind` filters); adding `embedding vector(…)` is
one additive migration. Until then the deterministic join is *better*, not just
cheaper: it answers "which decisions govern this diff", which cosine similarity
does not `[S19]`.

### (c) The assistant

One read tool now, one write tool behind confirmation:

- `search_decisions` — registered in `server/internal/assistant/tools.go`
  alongside `ToolListStaleIssues` (`:194`); args `{query?, project_id?,
  issue_id?, path?, include_superseded?}`; returns ≤10 rows with question,
  choice, rationale, decider, date, origin link, superseded-by. Read-only, no
  confirmation.
- `record_decision` — through `assistantAwaitConfirmation`, the seam every
  destructive tool uses, so it inherits expiry, single-use and the ConfirmCard.
  Writes team-visible authoritative content, so confirm-then-do applies.

That satisfies the parity rule (anything the UI can do, the assistant can do;
destructive/authoritative actions confirm first) without a new mechanism.

## 3.4 The wiki-lite question — fold it in, but build no wiki

**The ask:** *"I hate sending people a word doc with git hygiene rules."*

**The answer: fold it in as `kind='convention'` on the same object, promoted
from decisions, compiled into the same skill. Build no editor, no page tree, no
rich text.**

The argument:

1. **It is the same object at a later lifecycle point.** A decision that keeps
   getting confirmed and stops being contested *is* a convention. `PATCH
   /api/knowledge-items/{id}` already accepts `Kind *string`
   (`handler/knowledge_item.go:221`), so promotion is a one-field edit, not a
   migration. Two objects would mean two review gates, two compiles, two
   budgets, and two things that drift.
2. **The delivery mechanism already works.** Conventions compile into the
   `<slug>-kb` section that rides **every** claim
   (`knowledge_compile.go:99`, `daemon.go:1698-1714`). That is strictly better
   than a word doc: the agents read it too.
3. **Only one thing is genuinely missing: workspace scope.**
   `knowledge_item.project_id` is `NOT NULL` (migration 146:20) and
   `ListKnowledgeItemsByProject` filters on it. Git hygiene rules are not
   per-project. Fix additively: `ALTER COLUMN project_id DROP NOT NULL`, with
   `kb_name` resolving to a workspace-level KB (`<workspace-slug>-conventions`)
   that the claim path injects on every claim regardless of project. The
   compile already keys on `kb_name`, not `project_id`
   (`knowledge_compile.go:25-29`) — it needs no change at all.
4. **The incumbents already conceded the doc surface.** Atlassian's 2026 answer
   to unused Confluence knowledge is an agent layer over existing pages, not a
   better wiki `[S33]`; Slab, the purpose-built wiki competitor, has not moved
   its positioning toward decisions or AI at all `[S28]`. Building a doc space
   here is entering a saturated market to solve a retrieval problem with more
   authoring.
5. **A free-form editor already exists** for anything that does not fit an item:
   the Skills page edits markdown, workspace-scoped, human-owned, and the
   compile deliberately preserves everything outside the managed markers
   (`spliceManagedRegion`, `knowledge_compile.go:268-284`). That is wiki-lite,
   shipped.

**What stays separate:** `risk_map` and `qa_manifest`. They are machine-consumed
structured config with their own editors and their own injection paths, not
prose knowledge. Folding them in would be the config-sprawl failure.

## 3.5 Staleness — living truth, applied to memory

The living-truth discipline (`docs/living-truth-plan.md`) is: **derive and
apply only facts; compute and surface inference; never write inference.** Memory
gets the same split.

**Never a silent edit.** Today `UpdateKnowledgeItem`
(`handler/knowledge_item.go:232-300`) rewrites `title`/`body` in place. For
`kind='decision'` past acceptance that is forbidden: a changed decision is a
**new row** with `supersedes` the old, and the old flips to
`status='superseded'` — never deleted, never rewritten. In-place edits stay
allowed while `status='proposed'` (typo fixes before the record is real).
This is `[S6]`'s settled norm and the repo's own provenance instinct
(`postDerivedStatusProvenance`, `living_truth.go:278`).

**Supersession is a link, readable both ways.** The old row renders
*"Superseded by \<new\> on \<date\> by \<who\>"*; the new renders
*"Supersedes \<old\>"*. Humans can see the chain; **agents never see a
superseded row** — filtered at retrieval, because a contradicting record in
context measurably degrades the run `[S18]`.

**Confidence is computed on read; it never changes a status.** The one thing no
competitor in §2.3 has. Derived at read time from facts that already exist, so
the signal can never itself go stale:

| signal | source |
|---|---|
| age since `decided_at` | the row |
| governed code churn | count of merged `github_pull_request` rows whose `changed_paths` intersect `knowledge_decision.paths[]` since `decided_at` (migration 210) |
| source issue reopened | `activity_log` status_changed + the living-truth `reopened_work` rule |
| never re-confirmed | `last_confirmed_at IS NULL` |

Rendered as a human prompt — *"this decision governs code that has changed 14
times since it was made"* — with a one-click **Still true / Supersede** pair.
Never an automatic archive: auto-expiring a decision on a timer is inventing
truth, and a record that silently vanished is indistinguishable from one that
was never made.

**Re-confirmation must be reachable by humans.** `BumpKnowledgeItemHits` is
synthesiser-only today (§1.3 gap 10). The "Still true" click must bump
`last_confirmed_at` (and the decision ranker must sort on `decided_at` /
`last_confirmed_at`, not `hits`), or human-recorded decisions permanently rank
below agent-proposed ones.

## 3.6 What NOT to build — the kill list

1. **No wiki editor.** No page tree, no rich text, no nested docs, no
   permissions model. The Skills page is the markdown escape hatch and it exists.
2. **No embeddings / pgvector before the path join is proven to miss.** The
   deterministic `changed_paths → risk_map globs → module` join answers the
   right question and costs nothing; hybrid/lexical beats pure vectors on
   identifier-dense text anyway `[S19]`. Schema stays vector-ready.
3. **No auto-ADR PRs into the repo.** Writing `docs/adr/*.md` from an agent puts
   AI authorship into the artifact humans audit — the named anti-goal — and
   creates a second store that drifts from the database. A read-only *export*
   is acceptable later; a write-back loop never is.
4. **No `/[ws]/decisions` top-level route in Phases 0–4.** §3.3(a).
5. **No auto-expiry, auto-archive, or auto-supersede on a timer.** Compute and
   surface; never write inference (§3.5).
6. **No LLM synthesis as the primary capture path.** Third, grounded,
   review-gated, attributed — or not at all `[S1][S3]`.
7. **No agent self-acceptance.** `kbAutoAcceptKinds` must never gain
   `decision`, `convention`, or `architecture`.
8. **No decision metrics.** No decisions-per-week, no decisions-per-person, no
   "knowledge coverage score". Counts of what is *waiting* are allowed (the
   decision queue already does exactly and only that,
   `types/decision-queue.ts:92-97`); rates and per-person comparison are banned.
9. **No heavyweight template.** Three fields. Never a MADR form `[S5][S6]`.
10. **No second inbox.** The decision *queue* is the inbox; the decision *log*
    is not a queue and must not notify.
11. **No cross-workspace or global decision store.** Multi-tenancy rule; and a
    "company brain" that spans tenants is the `[S8]` failure mode with a
    security bug attached.
12. **No dumping the decision log into agent context**, and no "no relevant
    decisions found" filler line.
13. **No new escalation-like object.** Everything here rides `knowledge_item`
    and the existing review API.
14. **No retroactive thread/channel summarisation as a capture path.** That is
    Slack's own 2026 bet `[S34]` and it structurally cannot record who had
    authority to decide or what was rejected — only what was said. Agora
    captures *at the decision*, from the decider, in their own words.
15. **No "AI-authored" badge as the trust mechanism.** Labelling is contested
    and gameable `[S27]`; a **named human accepter** is the trust mechanism.
    The byline says who signed off, not which model drafted.

## 3.7 Sequencing

Each phase names the shipped primitive it harvests. Phases 0–2 are independent
of 3–5 and deliver value alone.

### Phase 0 — Make the decision kind reachable (1 PR, frontend-only)

The `decision` kind ships today, always lands `proposed`, and **nothing can
approve it**, so it never compiles and never reaches an agent (§1.3 gap 9).
Fixing that needs zero backend.

- `ProjectDecisionsSection` (`packages/views/projects/components/`), mounted in
  `project-detail.tsx`; list / accept / edit-while-proposed / archive.
- `packages/core`: `knowledgeItemsOptions`, zod schemas + `EMPTY_` fallbacks in
  `api/schemas.ts` + the mandatory malformed-response test, client methods for
  the four existing endpoints, optimistic mutations.
- Web + desktop wiring.
- **Harvests:** nothing new — it unlocks the synthesiser output already being
  written.
- Scope: ~7 files, 0 migrations, 0 backend.

### Phase 1 — Harvest the escalation (1 PR)

The highest-value, lowest-friction capture in the product.

- Migration 211: the sidecar + the five new `knowledge_item` columns + the two
  unique/dedupe predicate fixes + `decision` excluded from the Jaccard merge
  (§3.1).
- `server/internal/service/decision.go`: `RecordDecision` seam.
- Wire into `ResolveEscalation` (`service/escalation.go:188`) after the answer
  comment; `escalationResolveRequest` (`handler/escalation.go:51`) gains `remember bool`.
- `escalation-card.tsx`: the pre-checked **Remember this decision** box, and the
  answered-decision strip the card currently drops.
- Go tests in `internal/service`; views tests in `packages/views`.
- **Harvests:** `task_escalation` (migration 209).
- Scope: 1 migration, ~8 files.

### Phase 2 — Harvest the gates (1 PR)

- Same seam, three more call sites: `review_decision.go:88,427`,
  `qa_override.go:61`, `assistant_plans.go:426`. Each pre-fills question /
  choice / rationale from text the human already typed; each gets the
  remember-checkbox with the defaults in §3.2.
- Import-confirm harvest is optional in this PR and can slip to Phase 5.
- **Harvests:** review verdicts, QA overrides with reason, plan confirmations
  (including which rows were unchecked).
- Scope: 0 migrations, ~7 files.

### Phase 3 — Path-scoped injection (1 PR) — the retrieval half

- `riskModulesForPaths` beside `riskTierForPath` (`handler/risk_tier.go:186`).
- `decisionsForIssueContext(issue) → []decision` with the §3.3(b) ranking and
  budget; block appended in `handler/daemon.go` next to
  `sliceActionRiskMapContext` (`daemon.go:1737`).
- Two `ProjectScoped` config keys in `internal/config/registry.go`.
- **Skill contract update is mandatory in this PR** (`CLAUDE.md` rule):
  `builtin_skills/agora-projects-and-resources/SKILL.md` §"Project knowledge
  base" **and**
  `references/projects-and-resources-source-map.md:21-22`.
- Pure-function Go tests for ranking + budget (no DB).
- **Harvests:** `github_pull_request.changed_paths` (migration 210) +
  `project.settings.risk_map`.
- Scope: 0 migrations, ~6 files.

### Phase 4 — Supersession, confidence, assistant (1–2 PRs)

- Supersede flow end to end; in-place edit blocked once `status='active'`;
  superseded filtered from every agent path and hidden behind a toggle for
  humans.
- Computed confidence (§3.5) with **Still true / Supersede**; "Still true"
  bumps `last_confirmed_at` from a human action for the first time.
- Assistant `search_decisions` (read) + `record_decision` (confirm-gated).
- **Harvests:** `github_pull_request.changed_paths` again (for churn) +
  `activity_log`.

### Phase 5 — Workspace conventions, the wiki-lite answer (1 PR)

- `project_id` nullable; workspace-level KB name resolution; inject on every
  claim regardless of project.
- Decision → convention promotion in the UI (one `PATCH`).
- Grounded-synthesis prompt change + the `→ cancelled` capture trigger (§3.2
  path 3).
- Import-mapping harvest if it slipped.

### Deferred, explicitly

pgvector (§3.3(b) trigger condition); ADR markdown export; Slack/Telegram
decision capture; decision templates per project; any decision analytics.

## 3.8 Risks and open questions

1. **Promotion-gate fatigue.** A checkbox on every escalation answer is one
   click, but it is a click on the critical path of an already-slow human step
   (measured median wait 24.9h). If usage shows it is being ignored, the
   fallback is *capture silently as `proposed`, review in batch* — worse for the
   review queue (which saturates at 100), better for capture rate. Instrument
   before choosing.
2. **Rationale quality.** `esc.detail` is "what I tried", not "why we chose
   this". The rationale we harvest is genuinely the human's words but may be
   thin. Accept thin-but-true over rich-but-synthesised; the Phase 2 form gives
   anyone who cares a place to expand.
3. **`kb_name` sharing.** Sibling projects sharing one KB
   (`settings.kb_skill`, live on sd-main) will share a decision log. That is
   probably correct — they share a codebase — but the project section must show
   which project a decision came from (`project_id` is already provenance).
4. **Context budget regression.** Base KB 16K runes + 3×8K module runes is
   already ~10K tokens per claim before decisions. Phase 3 should measure the
   claim payload, and if the ceiling is being hit, the right fix is trimming the
   always-injected KB toward progressive disclosure `[S1][S13]`, not shrinking
   decisions.
5. **`origin` has no CHECK by design** (API-response-compatibility rule): an
   unknown origin must render generically, not fail a write or blank a list.
   Every `switch` on it needs a `default`.

---

# Appendix A — Sources

Web research, 2025–2026 unless noted.

**ADR practice and decision capture**

- `[S1]` ThoughtWorks Technology Radar Vol 34 — *Agent instruction bloat*
  (Caution), *Curated shared instructions for software teams* (Adopt),
  *Progressive context disclosure* (Trial), April 2026 ·
  https://www.thoughtworks.com/radar/techniques/summary/agent-instruction-bloat ·
  https://www.thoughtworks.com/radar/techniques/summary/curated-shared-instructions-for-software-teams ·
  https://www.thoughtworks.com/radar/techniques/summary/progressive-context-disclosure
- `[S2]` joelparkerhenderson/architecture-decision-record — canonical community
  ADR repo, 2025–2026 additions incl. Claude Code skills ·
  https://github.com/joelparkerhenderson/architecture-decision-record
- `[S3]` Dennis Adolfi, *AI Generated Architecture Decision Records (ADR)*,
  2025-11-24 · https://adolfi.dev/blog/ai-generated-adr/
- `[S4]` Alexander Holbreich, *Architecture Decision Records: A Tool for
  Experienced Engineers*, 2025-02-08 · https://alexander.holbreich.org/adr_method/
- `[S5]` AWS Architecture Blog, *Master architecture decision records (ADRs):
  Best practices*, 2025-03-20 ·
  https://aws.amazon.com/blogs/architecture/master-architecture-decision-records-adrs-best-practices-for-effective-decision-making/
- `[S6]` Tomas Jurasek, *Architecture Decision Records: Capturing the Why*,
  2026-01-05 · https://tomasjurasek.substack.com/p/architecture-decision-records-capturing
- `[S7]` Martin Fowler bliki, *Architecture Decision Record*, updated 2026-03-24
  (notable: no mention of AI agents) ·
  https://martinfowler.com/bliki/ArchitectureDecisionRecord.html

**The 2026 decision-memory startup wave, and its critics**

- `[S8]` Hacker News, *Launch HN: Hyper (YC P26) — Company brain to power
  agentic development*, 2026-06-03, 79 pts / 78 comments ·
  https://news.ycombinator.com/item?id=48387095 · product: https://heyhyper.ai/
- `[S9]` Decision Guardian (DecispherHQ) — PR-time decision surfacing;
  motivating line *"Nobody read them before opening a PR"*; HN discussion
  2026-03-02 · https://github.com/DecispherHQ/decision-guardian
- `[S10]` Hopsule — decision capture via IDE/GitHub App/CLI/MCP with a mandatory
  human-approval gate ("humans always decide"), 2026 · https://hopsule.com

**Agent memory conventions**

- `[S11]` Claude Code — *Memory* (CLAUDE.md, auto-`MEMORY.md` 200-line/25KB cap,
  topic files, `project` memory type for "decisions Claude can't derive from the
  code or git history"), live doc through v2.1.277, Sept 2026 ·
  https://code.claude.com/docs/en/memory
- `[S12]` Anthropic, *Effective context engineering for AI agents*, 2025-09-29 ·
  https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents
- `[S13]` Anthropic, *Equipping agents for the real world with Agent Skills*,
  2025-10-16, + Claude Code Skills docs ·
  https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills ·
  https://code.claude.com/docs/en/skills

**Lean-context / retrieval evidence**

- `[S14]` Chroma, *Context Rot* research report, 2025-07-14 ·
  https://www.trychroma.com/research/context-rot
- `[S15]` *NoLiMa: Long-Context Evaluation Beyond Literal Matching*, arXiv
  2502.05167, 2025-02-07 (v3 2025-07-09, ICML 2025) ·
  https://arxiv.org/abs/2502.05167
- `[S16]` Databricks, *Long Context RAG Performance of LLMs*, 2024-08-12
  (pre-window; the base numbers 2025 work cites) ·
  https://www.databricks.com/blog/long-context-rag-performance-llms
- `[S17]` Anthropic, *Context management* (memory tool + context editing;
  +39% eval, −84% tokens), 2025-09-29 · https://claude.com/blog/context-management
- `[S18]` Drew Breunig, *How Contexts Fail and How to Fix Them*, 2025-06-22 ·
  https://www.dbreunig.com/2025/06/22/how-contexts-fail-and-how-to-fix-them.html
- `[S19]` Anthropic, *Introducing Contextual Retrieval*, 2024-09-19
  (pre-window; foundational, cited throughout 2025–2026) ·
  https://www.anthropic.com/news/contextual-retrieval
- `[S20]` NVIDIA et al., *Retrieval Augmented Generation or Long-Context LLMs?*
  (Self-Route), arXiv 2407.16833, 2024-07-23 (EMNLP 2024) ·
  https://arxiv.org/abs/2407.16833
- `[S21]` *LaRA: No Silver Bullet for LC or RAG Routing*, arXiv 2502.09977,
  2025-02-14 (rev. 2025-03-05) — rebuts `[S20]`'s universal claim ·
  https://arxiv.org/abs/2502.09977

**Trust in AI-authored artifacts**

- `[S26]` Stack Overflow Developer Survey 2025 — 46% actively distrust AI output
  vs 33% trust; 3.1% highly trust AI-generated code vs 19.6% highly distrust;
  66% frustrated by "almost right, but not quite"; favourable sentiment
  >70% (2023–24) → 60% (2025) · https://survey.stackoverflow.co/2025/
- `[S27]` PC Gamer, *Linus Torvalds weighs in on the AI-slop debate in Linux
  kernel documentation*, 2026-01-09 ·
  https://www.pcgamer.com/software/ai/there-is-zero-point-in-talking-about-ai-slop-thats-just-plain-stupid-linus-torvalds-weighs-in-on-ai-debate-in-linux-kernel-documentation/

**Competitive landscape (who owns decision capture — nobody)**

- `[S28]` Landscape sweep, Sept 2026: `log4brains`
  (https://github.com/thomvaill/log4brains, ~1.6k stars, no recent release
  activity), `adr-tools` (https://github.com/npryce/adr-tools, ~5.7k stars, 32
  open issues / 37 open PRs, no visible 2025–2026 activity), Backstage ADR
  plugin folded into https://github.com/backstage/community-plugins
  (`@backstage-community/plugin-adr`), Slab (https://slab.com/, positioning
  unchanged, no AI/decision language as of Sept 2026)
- `[S29]` Linear Docs — *Initiatives* (`Latest Update`, computed health rollup)
  · https://linear.app/docs/initiatives · and *Documents* ·
  https://linear.app/docs/documents
- `[S30]` Linear changelog — *Text attribution & agent-assisted editing*
  (agent edits visually separated; "show author names"), 2026-07-23 ·
  https://linear.app/changelog/2026-07-23-agent-assisted-editing
- `[S31]` Linear changelog — *Agent-assisted project updates*, 2026-06-18 ·
  https://linear.app/changelog/2026-06-18-agent-assisted-project-updates
- `[S32]` Linear, *Introducing Loops* (a Loop reviews projects against a release
  plan, updates the document and "leaves a note explaining why"), 2026-07-20 ·
  https://linear.app/now/introducing-loops
- `[S33]` Atlassian blog, *How customers are using Confluence Agents to turn
  knowledge into action* (5M+ agent invocations/month), 2026-05-12 ·
  https://www.atlassian.com/blog
- `[S34]` Slack, *The Front Door to the Agentic Enterprise*, 2026-09-19, and
  *AI-Powered Collaboration: How Teams Turn Ideas Into Action*, 2026-06-04 ·
  https://slack.com/blog

**Conventions and tooling landscape**

- `[S22]` AGENTS.md specification (Agentic AI Foundation / Linux Foundation;
  60K+ repos), actively maintained · https://agents.md/
- `[S23]` MCP reference memory server (knowledge-graph entities/relations/
  observations; no pruning or scale guidance in its own docs) ·
  https://github.com/modelcontextprotocol/servers/tree/main/src/memory
- `[S24]` Simon Willison, *Claude Skills are awesome, maybe a bigger deal than
  MCP*, 2025-10-16 · https://simonwillison.net/2025/Oct/16/claude-skills/
- `[S25]` LangChain, *Memory for Agents*, 2024-10-19 (taxonomy; no pollution/
  pruning story) · https://www.langchain.com/blog/memory-for-agents

*Research caveats:* the ADR pass ran without WebSearch (session quota
exhausted) and used targeted WebFetch + HN Algolia instead — coverage is good
but not exhaustive. `[S33]` and `[S34]` were reached through news aggregation
rather than a direct permalink; re-verify the exact URLs before quoting the
figures externally. Hard 2025–2026 data on *wiki rot specifically* was thin —
the argument in §2.5 rests on vendor behaviour (what Atlassian built) rather
than on a survey.

---

# Appendix B — Repo anchor index

Everything this plan touches, in one list.

**KB flywheel Phase 1 (shipped)**
- `server/migrations/146_knowledge_item.up.sql` — the table, the compile key
  rationale, the partial unique index
- `server/internal/service/knowledge_item.go` — ingest, sanitise, dedupe, trust
  gate (`:41` Jaccard, `:47` auto-accept set, `:224` trust anchor, `:265`
  `CaptureKnowledgeItems`)
- `server/internal/service/knowledge_compile.go` — `:69` markers, `:78` defensive
  header, `:88` kind sections, `:160` ranking cutoff, `:270` splice, `:283`
  `RecompileKB`
- `server/pkg/db/queries/knowledge_item.sql` — `:40` compile order, `:48` dedupe
  keys
- `server/internal/handler/issue.go:2432` `maybeEnqueueKnowledgeCapture`,
  `:2526` the capture prompt
- `server/internal/handler/knowledge_synth.go:77` `resolveKBSynthesizer`
- `server/internal/handler/knowledge_item.go` — the review API nothing calls
- `server/internal/handler/project_knowledge_modules.go:25,39,142` — injection
  budgets and `projectKBSkills`
- `server/internal/handler/daemon.go:1698` — KB auto-ride on claim;
  `:1729` lean gate; `:1737` risk-map block (the injection template)
- `server/cmd/server/router.go:1267,1362` — the routes
- `docs/kb-flywheel-implementation-plan.md` §12 — Phase 2/3/4 outlines this plan
  supersedes and narrows

**Decision primitives to harvest**
- `server/migrations/209_task_escalation.up.sql` + `server/internal/service/escalation.go:118,188,357`
  + `server/internal/handler/escalation.go:61,164`
- `server/internal/handler/review_decision.go:29,49,88,427`
- `server/internal/handler/qa_override.go:48,51,61`
- `server/internal/service/review_evidence.go:28` + `handler/review_verdict.go:41`
- `server/migrations/198_assistant_pending_operation.up.sql`,
  `199_assistant_operation_outcome.up.sql`, `200_assistant_confirmation_retention.up.sql`
  + `server/internal/handler/assistant_plans.go:146,426`
- `server/migrations/206_import_job.up.sql:22` + `handler/assistant_imports.go:449`
- `server/migrations/210_pr_changed_paths.up.sql` + `handler/risk_tier.go:143,186,321`
- `server/internal/handler/project_risk_map.go:35`
- `server/internal/handler/living_truth.go:278` — the provenance pattern
- `server/internal/handler/slice_action.go:1120` — stage casting (no rationale
  field; noted, not harvested)
- `server/internal/service/builtin_skills/agora-working-on-issues/SKILL.md:118`
  — the `decision` metadata key convention

**Surfaces**
- `packages/views/issues/components/escalation-card.tsx:29`
- `packages/views/projects/components/project-detail.tsx:886`,
  `project-risk-map-section.tsx` (template)
- `packages/views/qa/components/qa-page.tsx:30,51` + `decision-queue.tsx`
- `packages/core/types/decision-queue.ts`, `packages/core/api/schemas.ts:1260`,
  `packages/core/api/client.ts:1985,2987`
- `server/internal/assistant/tools.go:194`
- `server/internal/config/registry.go:43`

**Plans this one sits beside**
- `docs/market-research-synthesis.md` (G5, and the anti-goals)
- `docs/living-truth-plan.md` (G1 — the derive/compute/never-write discipline)
- `docs/orchestration-upgrade-plan.md` §A2/§B1 (the decision queue and
  escalation; this plan is their memory half)
