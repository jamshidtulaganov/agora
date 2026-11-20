-- name: TouchUserLastLogin :exec
WITH u AS (
    UPDATE "user" SET last_login_at = now(), last_active_at = now() WHERE id = $1 RETURNING id
)
INSERT INTO user_activity_day (user_id, day)
SELECT id, (now() AT TIME ZONE 'UTC')::date FROM u
ON CONFLICT DO NOTHING;

-- name: TouchUserLastActive :exec
WITH u AS (
    UPDATE "user" SET last_active_at = now() WHERE id = $1 RETURNING id
)
INSERT INTO user_activity_day (user_id, day)
SELECT id, (now() AT TIME ZONE 'UTC')::date FROM u
ON CONFLICT DO NOTHING;

-- name: AdminListUsers :many
-- Platform admin page only. Every account with the workspaces it belongs to.
SELECT
    u.id, u.name, u.email, u.created_at, u.last_login_at, u.last_active_at,
    COALESCE(
        (SELECT json_agg(json_build_object('slug', w.slug, 'name', w.name, 'role', m.role) ORDER BY w.name)
         FROM member m JOIN workspace w ON w.id = m.workspace_id
         WHERE m.user_id = u.id),
        '[]'::json
    )::text AS workspaces
FROM "user" u
ORDER BY u.last_active_at DESC NULLS LAST, u.created_at DESC
LIMIT 2000;

-- name: AdminListWorkspaces :many
-- Platform admin page only. Every workspace with its member count, first
-- owner and plan row (NULL columns when the workspace has no row = Free).
SELECT
    w.id, w.name, w.slug, w.created_at,
    (SELECT count(*) FROM member m WHERE m.workspace_id = w.id)::int AS members,
    COALESCE((SELECT u.email FROM member m JOIN "user" u ON u.id = m.user_id
              WHERE m.workspace_id = w.id AND m.role = 'owner'
              ORDER BY m.created_at LIMIT 1), '')::text AS owner_email,
    (SELECT max(u.last_active_at) FROM member m JOIN "user" u ON u.id = m.user_id
     WHERE m.workspace_id = w.id)::timestamptz AS last_active_at,
    s.workspace_id AS sub_workspace_id,
    COALESCE(s.plan, '')::text AS sub_plan,
    COALESCE(s.status, '')::text AS sub_status,
    COALESCE(s.billing_interval, '')::text AS sub_interval,
    COALESCE(s.seats_paid, 0)::int AS sub_seats_paid,
    s.trial_ends_at AS sub_trial_ends_at,
    s.current_period_end AS sub_current_period_end,
    COALESCE(s.provider, '')::text AS sub_provider,
    COALESCE(s.note, '')::text AS sub_note
FROM workspace w
LEFT JOIN workspace_subscription s ON s.workspace_id = w.id
ORDER BY w.created_at DESC
LIMIT 2000;

-- name: AdminGrowthDaily :many
-- One row per UTC day for the last N days (oldest first): sign-ups, people who
-- used the app that day, and workspaces created.
WITH days AS (
    SELECT generate_series(
        (now() AT TIME ZONE 'UTC')::date - (sqlc.arg(days)::int - 1),
        (now() AT TIME ZONE 'UTC')::date,
        interval '1 day'
    )::date AS day
)
SELECT
    d.day::date AS day,
    (SELECT count(*) FROM "user" u WHERE (u.created_at AT TIME ZONE 'UTC')::date = d.day)::int AS signups,
    (SELECT count(*) FROM user_activity_day a WHERE a.day = d.day)::int AS active_users,
    (SELECT count(*) FROM workspace w WHERE (w.created_at AT TIME ZONE 'UTC')::date = d.day)::int AS new_workspaces
FROM days d
ORDER BY d.day;

-- name: AdminSignupAttribution :many
-- Every person who signed up since $1, with where they came from and how far
-- they got: created (own) a workspace, and own a workspace on a paid plan.
-- Grandfathered workspaces never paid, so they do not count as paying.
SELECT
    u.id,
    COALESCE(a.source, '')::text AS source,
    COALESCE(a.medium, '')::text AS medium,
    COALESCE(a.campaign, '')::text AS campaign,
    COALESCE(a.referrer_host, '')::text AS referrer_host,
    COALESCE(a.landing_path, '')::text AS landing_path,
    EXISTS (SELECT 1 FROM member m WHERE m.user_id = u.id AND m.role = 'owner')::bool AS owns_workspace,
    EXISTS (
        SELECT 1 FROM member m
        JOIN workspace_subscription s ON s.workspace_id = m.workspace_id
        WHERE m.user_id = u.id AND m.role = 'owner'
          AND s.plan IN ('team', 'enterprise') AND s.status = 'active'
          AND s.provider <> 'grandfathered'
    )::bool AS paying
FROM "user" u
LEFT JOIN user_attribution a ON a.user_id = u.id
WHERE u.created_at >= $1
ORDER BY u.created_at DESC
LIMIT 20000;

-- name: CreateUserAttribution :exec
-- Write-once: a person's first touch never changes after it is recorded.
INSERT INTO user_attribution (user_id, source, medium, campaign, referrer_host, landing_path, first_seen_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (user_id) DO NOTHING;
