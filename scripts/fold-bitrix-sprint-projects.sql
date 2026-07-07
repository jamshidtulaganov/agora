-- Model-A migration: fold sprint-named Bitrix workgroup-PROJECTS into the
-- sd-main code project as SPRINTS (sprint table). Idempotent + transactional.
-- The Bitrix group marker moves to the sprint's goal so the next sync dedups
-- against the migrated sprint instead of re-creating a project.
--
-- Params (psql -v): ws=<workspace id>, sdmain=<sd-main project id>
\set ON_ERROR_STOP on
BEGIN;

SELECT set_config('mig.ws', :'ws', true);
SELECT set_config('mig.sdmain', :'sdmain', true);

DO $$
DECLARE
  ws       uuid := current_setting('mig.ws')::uuid;
  sdmain   uuid := current_setting('mig.sdmain')::uuid;
  p        RECORD;
  sid      uuid;
  gid      text;
  moved    int;
BEGIN
  IF sdmain IS NULL OR NOT EXISTS (SELECT 1 FROM project WHERE id = sdmain) THEN
    RAISE EXCEPTION 'sd-main project % not found', sdmain;
  END IF;

  FOR p IN
    SELECT id, title, description, status
    FROM project
    WHERE workspace_id = ws
      AND id <> sdmain
      AND (lower(title) LIKE '%sprint%' OR lower(title) LIKE '%спринт%')
  LOOP
    gid := substring(COALESCE(p.description,'') from 'bitrix_group:([0-9]+)');

    -- Find (or create) the sprint under sd-main carrying the same Bitrix marker
    -- in its goal — the exact key getOrCreateBitrixSprint dedups on.
    SELECT s.id INTO sid FROM sprint s
      WHERE s.project_id = sdmain AND s.workspace_id = ws
        AND ( (gid IS NOT NULL AND s.goal LIKE '%bitrix_group:' || gid || '%')
              OR s.name = p.title )
      ORDER BY s.created_at ASC LIMIT 1;

    IF sid IS NULL THEN
      INSERT INTO sprint (workspace_id, project_id, name, goal, status)
      VALUES (ws, sdmain, p.title,
              CASE WHEN gid IS NOT NULL THEN 'bitrix_group:' || gid ELSE '' END,
              CASE WHEN p.status IN ('done','cancelled') THEN 'completed' ELSE 'active' END)
      RETURNING id INTO sid;
      RAISE NOTICE 'created sprint % (%) from project %', sid, p.title, p.id;
    ELSE
      RAISE NOTICE 'reusing sprint % for project % (%)', sid, p.title, p.id;
    END IF;

    -- Attach this project's issues to the sprint (one sprint per issue), THEN
    -- re-parent them to sd-main. Attach first, while they still identify with p.
    INSERT INTO issue_to_sprint (issue_id, sprint_id)
      SELECT i.id, sid FROM issue i WHERE i.project_id = p.id
      ON CONFLICT (issue_id) DO UPDATE SET sprint_id = EXCLUDED.sprint_id;

    UPDATE issue SET project_id = sdmain WHERE project_id = p.id;
    GET DIAGNOSTICS moved = ROW_COUNT;

    -- QA cases follow their work to the code project.
    UPDATE test_case SET project_id = sdmain WHERE project_id = p.id;

    -- A sprint-project should carry no repos/autopilots/boxes; any present are
    -- anomalies (sd-main owns those) — drop so the project can be deleted.
    DELETE FROM project_resource WHERE project_id = p.id;
    DELETE FROM autopilot        WHERE project_id = p.id;
    UPDATE connected_box SET project_id = NULL WHERE project_id = p.id;

    DELETE FROM project WHERE id = p.id;
    RAISE NOTICE 'folded project % -> sprint % (% issues)', p.title, sid, moved;
  END LOOP;
END $$;

COMMIT;
