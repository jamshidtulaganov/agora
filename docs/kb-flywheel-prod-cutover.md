# KB flywheel — prod cutover runbook

Two ordered procedures. **Do NOT run Part 2 before Part 1 is fully deployed** —
clearing `kb_synthesizer_agent_id` on prod while the old (opt-in, blob-editing)
backend is still live STOPS knowledge capture entirely and, without migration
146, the new capture path cannot write. The fresh-rebuild only works once the
Phase 1 code + migration 146 are on prod.

Prod = `master` branch, deployed on Fly. As of writing, `origin/master` is at
migration **144** and is **50 commits behind** `sd-platform`; the 7 KB commits
are `08888545..0063956c`.

---

## Part 1 — Deploy Phase 1 to prod

### 1a. Release scope decision (READ FIRST)

Merging `sd-platform → master` ships **all 50 commits**, not just the KB
flywheel — it includes concurrent work (bitrix gating, qa gates, editor
worktrees, ui guards, etc.) and migration **145** (`test_run_baseline_status`)
in addition to **146** (`knowledge_item`). This is a full integration release,
not a KB-only release. Confirm prod is meant to take all 50 before proceeding.
If only KB should ship, cherry-pick `08888545..0063956c` onto a release branch
instead (verify they don't depend on sibling commits first).

Local merge is clean (no conflicts) as of this writing — re-verify at cutover:

```bash
git fetch origin master
git merge-tree $(git merge-base sd-platform origin/master) sd-platform origin/master | grep -i conflict
```

### 1b. Merge + push

```bash
git checkout master && git pull --ff-only origin master
git merge --no-ff sd-platform -m "release: sd-platform -> master (incl. KB flywheel Phase 1)"
# resolve if anything appeared since the dry-run, then:
git push origin master
```

### 1c. Deploy backend + run prod migrations

Migrations 145 + 146 must apply to the prod DB. Order: **migrate, then flip
traffic** (146 is purely additive — new table + nullable column — so it is safe
to run against the running old backend before the new image is live).

```bash
# from the deploy host / CI with prod Fly + DB creds:
# 1. apply migrations against prod DB (145, 146)
make migrate-up            # or the prod-scoped migrate command / release cmd
# 2. deploy the new backend image
fly deploy -a <prod-backend-app>
# 3. confirm
#    - `select max(version) from schema_migrations;`  -> 146
#    - `\d knowledge_item`                            -> table exists
#    - `\d agent_task_queue`                          -> model_override column exists
```

### 1d. CLI release (required — CLAUDE.md)

The synthesizer prompt and agent workflows lean on the agora CLI; a prod deploy
must be accompanied by a CLI release.

```bash
git tag v0.x.x            # bump patch from the latest tag on master
git push origin v0.x.x    # triggers release.yml -> GoReleaser -> Homebrew tap
```

### 1e. Smoke (prod, non-mutating)

- Complete one throwaway issue in a NON-SD test project (or watch the next
  natural →done): confirm a `KB Synthesizer` agent auto-provisions and a
  `knowledge-items` capture task enqueues (`select name from agent where
  name='KB Synthesizer';`, check `knowledge_item` gets rows).
- Only after this looks healthy, do Part 2.

---

## Part 2 — SD workspace fresh-start (qaror 4)

Goal: drop the ~3-month-stale sd-docs-derived KB blob on the **sd-main** and
**sd-cs** projects and let the new default-on flywheel rebuild from scratch.

> sd-main / sd-cs are **projects** inside the SalesDoctor **workspace** (per the
> ecosystem note). `kb_synthesizer_agent_id` is a **workspace** setting, so
> clearing it re-provisions the synthesizer for the whole SD workspace (intended).
> The blob lives in the per-project `<slug>-kb` skills (`sd-main-kb`, `sd-cs-kb`
> — sd-main uses a `settings.kb_skill` override, so confirm the real names).

### 2a. Discovery (run first, note the UUIDs)

```sql
-- the SalesDoctor workspace (adjust the name/slug match to reality)
SELECT id, name, slug, settings->>'kb_synthesizer_agent_id' AS kb_synth
FROM workspace
WHERE name ILIKE '%salesdoc%' OR slug ILIKE '%sd%';

-- the KB skills for the two projects (resolve real names, incl. kb_skill override)
SELECT p.id AS project_id, p.title, p.settings->>'kb_skill' AS kb_skill_override,
       s.id AS skill_id, s.name, length(s.content) AS content_len,
       (s.content LIKE '%agora:kb:items:begin%') AS has_managed_region
FROM project p
LEFT JOIN skill s
  ON s.workspace_id = p.workspace_id
 AND s.name = COALESCE(NULLIF(p.settings->>'kb_skill',''),
                       /* else derived <slug>-kb; verify against ProjectKBSkillName */
                       lower(regexp_replace(p.title,'[^a-zA-Z0-9]+','-','g')) || '-kb')
WHERE p.title ILIKE 'sd-main%' OR p.title ILIKE 'sd-cs%'
   OR s.name IN ('sd-main-kb','sd-cs-kb');
```

### 2b. Back up the current blobs (so this is reversible)

```sql
SELECT id, name, content FROM skill
WHERE workspace_id = '<SD_WS_UUID>' AND name IN ('sd-main-kb','sd-cs-kb');
-- save the output to a file before mutating.
```

### 2c. Clear the workspace synthesizer setting (jsonb key delete)

```sql
UPDATE workspace
SET settings = settings - 'kb_synthesizer_agent_id', updated_at = now()
WHERE id = '<SD_WS_UUID>';
```

The next →done issue in the SD workspace triggers `resolveKBSynthesizer`, which
auto-provisions a fresh `KB Synthesizer` agent and stamps a new UUID. (If a
stale/archived "KB Synthesizer" agent already exists from earlier experiments,
it will be adopted or block provisioning — check `SELECT id,name,archived_at
FROM agent WHERE workspace_id='<SD_WS_UUID>' AND name='KB Synthesizer';` and
delete/rename it if it is the wrong agent.)

### 2d. Drop the stale blob (empty content, keep the row)

Emptying beats deleting: preserves the skill id/config, and the first post-deploy
capture's `RecompileKB` splices a fresh managed region into the empty skill.

```sql
UPDATE skill
SET content = '', updated_at = now()
WHERE workspace_id = '<SD_WS_UUID>' AND name IN ('sd-main-kb','sd-cs-kb');
```

(If you instead want the skill gone until rebuilt: `DELETE FROM skill WHERE ...`
— check `skill_file` FK cascade first; the new compile recreates the row by
name on the first captured item.)

### 2e. Verify rebuild

- Complete (or wait for) an issue in sd-main / sd-cs → a `knowledge-items`
  capture task runs, `knowledge_item` rows appear, and the `<slug>-kb` skill
  content grows a fresh `agora:kb:items` region.
- `SELECT kind,title,status,hits FROM knowledge_item WHERE workspace_id='<SD_WS_UUID>';`
- Approve any `proposed` items via `PATCH /api/knowledge-items/{id} {"status":"active"}`.

---

## Rollback

- **Blob**: restore from the 2b backup (`UPDATE skill SET content=... `).
- **Setting**: re-stamp the old agent id if needed (`UPDATE workspace SET
  settings = settings || '{"kb_synthesizer_agent_id":"<old>"}'::jsonb`).
- **Capture off entirely**: set `AGORA_KB_CAPTURE_DISABLED=1` on the prod
  backend (env) — no redeploy of code needed, just a restart.
- **Deploy**: standard Fly rollback to the previous release; migration 146 is
  additive and safe to leave in place on rollback.
