-- THE RANKED DECISION QUEUE (docs/orchestration-upgrade-plan.md §A2).
--
-- One query, one row per issue, for everything in a workspace that is waiting
-- on a HUMAN decision: an open escalation, a change awaiting review/approval, a
-- QA or review verdict nobody has acted on, an approved-but-unmerged pull
-- request.
--
-- Two disciplines carried over from the living-truth staleness read
-- (docs/living-truth-plan.md), and both are load-bearing:
--
--  1. NOTHING IS STORED AND NOTHING IS RANKED HERE. The query returns FACTS
--     (labels, PR states, escalation rows, activity clocks); the score is
--     computed on read in Go from those facts, so there is no stored rank to
--     itself go stale and no sweeper to keep it fresh.
--  2. IT IS A SEPARATE QUERY, never a join into the board's hot list path. The
--     candidate CTE narrows to one workspace's non-archived issues first, and
--     the LATERALs then run once per surviving row against indexed foreign keys.
--
-- There is deliberately NO LIMIT. The caller reports an exact total ("you have
-- 11 decisions waiting"), and a count that is really "the first 200" is a lie a
-- decision queue cannot afford.

-- name: ListDecisionQueueIssues :many
WITH candidate AS (
    SELECT i.id, i.number, i.title, i.status, i.project_id, i.updated_at
    FROM issue i
    WHERE i.workspace_id = sqlc.arg('workspace_id')
      AND i.archived_at IS NULL
      AND (sqlc.narg('project_id')::uuid IS NULL OR i.project_id = sqlc.narg('project_id')::uuid)
      -- Same non-owner visibility gate as ListIssues / ListStaleIssues: a
      -- restricted member sees only issues that are theirs. NULL disables it.
      AND (
        sqlc.narg('restrict_to_user')::uuid IS NULL
        OR (i.creator_type = 'member' AND i.creator_id = sqlc.narg('restrict_to_user')::uuid)
        OR (i.assignee_type = 'member' AND i.assignee_id = sqlc.narg('restrict_to_user')::uuid)
        OR (i.assignee_type = 'agent' AND i.assignee_id IN (
              SELECT a.id FROM agent a
               WHERE a.workspace_id = sqlc.arg('workspace_id')
                 AND a.owner_id = sqlc.narg('restrict_to_user')::uuid))
        OR (i.assignee_type = 'squad' AND i.assignee_id IN (
              SELECT sm.squad_id FROM squad_member sm JOIN squad s ON s.id = sm.squad_id
               WHERE s.workspace_id = sqlc.arg('workspace_id') AND sm.member_type = 'member'
                 AND sm.member_id = sqlc.narg('restrict_to_user')::uuid
              UNION
              SELECT s.id FROM squad s JOIN agent a ON a.id = s.leader_id
               WHERE s.workspace_id = sqlc.arg('workspace_id')
                 AND a.owner_id = sqlc.narg('restrict_to_user')::uuid
              UNION
              SELECT sm.squad_id FROM squad_member sm JOIN squad s ON s.id = sm.squad_id
                JOIN agent a ON a.id = sm.member_id
               WHERE s.workspace_id = sqlc.arg('workspace_id') AND sm.member_type = 'agent'
                 AND a.owner_id = sqlc.narg('restrict_to_user')::uuid))
      )
),
enriched AS (
    SELECT
        c.id,
        c.number,
        c.title,
        c.status,
        c.project_id,
        -- "Waiting since" for a label-driven decision. issue_to_label carries no
        -- timestamp, so the honest measured-from is the freshest of the three
        -- clocks the issue does have — the same GREATEST the staleness read uses.
        -- An escalation carries its own raised_at and uses that instead.
        GREATEST(
            c.updated_at,
            COALESCE(cm.last_comment_at, c.updated_at),
            COALESCE(tk.last_task_at, c.updated_at)
        )::timestamptz AS last_activity_at,
        lb.label_names,
        -- The escalation columns come through an OUTER lateral, so every one of
        -- them is NULL for an issue with no open escalation. They are coalesced
        -- here rather than left nullable so the Go side reads one shape and
        -- branches on escalation_id alone.
        esc.escalation_id,
        COALESCE(esc.escalation_kind, '')::text      AS escalation_kind,
        COALESCE(esc.escalation_prompt, '')::text    AS escalation_prompt,
        COALESCE(esc.escalation_detail, '')::text    AS escalation_detail,
        COALESCE(esc.escalation_options, '{}')::text[] AS escalation_options,
        COALESCE(esc.escalation_risk_tier, '')::text AS escalation_risk_tier,
        esc.escalation_raised_at,
        COALESCE(prs.linked_count, 0)::bigint AS linked_pr_count,
        COALESCE(prs.open_count, 0)::bigint   AS open_pr_count,
        COALESCE(prs.merged_count, 0)::bigint AS merged_pr_count,
        COALESCE(pth.changed_paths, '{}')::text[] AS changed_paths
    FROM candidate c
    LEFT JOIN LATERAL (
        SELECT COALESCE(array_agg(lower(btrim(l.name))), '{}')::text[] AS label_names
        FROM issue_to_label itl
        JOIN issue_label l ON l.id = itl.label_id
        WHERE itl.issue_id = c.id
    ) lb ON TRUE
    LEFT JOIN LATERAL (
        SELECT
            e.id         AS escalation_id,
            e.kind       AS escalation_kind,
            e.prompt     AS escalation_prompt,
            e.detail     AS escalation_detail,
            e.options    AS escalation_options,
            e.risk_tier  AS escalation_risk_tier,
            e.raised_at  AS escalation_raised_at
        FROM task_escalation e
        WHERE e.issue_id = c.id
          AND e.workspace_id = sqlc.arg('workspace_id')
          AND e.status = 'open'
        LIMIT 1
    ) esc ON TRUE
    LEFT JOIN LATERAL (
        SELECT
            COUNT(*)::bigint AS linked_count,
            COALESCE(SUM(CASE WHEN pr.state IN ('open', 'draft') THEN 1 ELSE 0 END), 0)::bigint AS open_count,
            COALESCE(SUM(CASE WHEN pr.state = 'merged' THEN 1 ELSE 0 END), 0)::bigint AS merged_count
        FROM issue_pull_request ipr
        JOIN github_pull_request pr ON pr.id = ipr.pull_request_id
        WHERE ipr.issue_id = c.id
    ) prs ON TRUE
    LEFT JOIN LATERAL (
        -- The server-derived tier's evidence: every repo-relative path the
        -- issue's pull requests touch (migration 210). Empty means UNKNOWN —
        -- never "nothing changed".
        SELECT COALESCE(array_agg(DISTINCT p), '{}')::text[] AS changed_paths
        FROM issue_pull_request ipr
        JOIN github_pull_request pr ON pr.id = ipr.pull_request_id
        CROSS JOIN LATERAL unnest(pr.changed_paths) AS p
        WHERE ipr.issue_id = c.id
    ) pth ON TRUE
    LEFT JOIN LATERAL (
        SELECT MAX(cc.created_at) AS last_comment_at
        FROM comment cc
        WHERE cc.issue_id = c.id
    ) cm ON TRUE
    LEFT JOIN LATERAL (
        SELECT MAX(GREATEST(
            t.created_at,
            COALESCE(t.started_at, t.created_at),
            COALESCE(t.completed_at, t.created_at)
        )) AS last_task_at
        FROM agent_task_queue t
        WHERE t.issue_id = c.id
    ) tk ON TRUE
)
SELECT *
FROM enriched
WHERE
    -- An OPEN ESCALATION is always a pending decision, whatever the status says:
    -- the run has ended and cannot restart without a person.
    escalation_id IS NOT NULL
    OR (
        status NOT IN ('done', 'cancelled')
        AND (
            -- Awaiting a human review / approval.
            status = 'in_review'
            -- A red verdict nobody has acted on, or an approval whose pull
            -- request is still sitting unmerged.
            OR label_names && ARRAY['qa:fail', 'review:fail', 'qa:blocked', 'merge:approved']::text[]
        )
    );
