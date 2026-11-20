-- Growth and marketing numbers for the platform admin page (/admin).
--
-- user_activity_day: one row per person per UTC day they used the app, so the
-- admin page can chart daily active users over time (user.last_active_at only
-- keeps the latest moment). Written by the same throttled middleware stamp.
CREATE TABLE user_activity_day (
    user_id uuid NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    day     date NOT NULL,
    PRIMARY KEY (user_id, day)
);
CREATE INDEX idx_user_activity_day_day ON user_activity_day(day);

INSERT INTO user_activity_day (user_id, day)
SELECT id, (last_active_at AT TIME ZONE 'UTC')::date FROM "user" WHERE last_active_at IS NOT NULL
ON CONFLICT DO NOTHING;

-- user_attribution: where a person came from before signing up (first touch on
-- the landing page: UTM tags, referring site, first page). Written once, by the
-- person's own browser right after sign-up; never overwritten.
CREATE TABLE user_attribution (
    user_id       uuid PRIMARY KEY REFERENCES "user"(id) ON DELETE CASCADE,
    source        text NOT NULL DEFAULT '',
    medium        text NOT NULL DEFAULT '',
    campaign      text NOT NULL DEFAULT '',
    referrer_host text NOT NULL DEFAULT '',
    landing_path  text NOT NULL DEFAULT '',
    first_seen_at timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);
