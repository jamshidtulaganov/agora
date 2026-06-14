-- Maps a Tandem user to their identity on an external system (Telegram,
-- Bitrix24) so inbound events resolve to a member -- e.g. a synced Bitrix task's
-- RESPONSIBLE_ID becomes the assignee on the board, and a Telegram login binds
-- to the same user. Kept as a separate table (not columns on "user") so it
-- needs NO sqlc regen: handlers read/write it with raw pgx via h.DB.
--
-- PK (provider, external_id): one external identity maps to exactly one user;
-- a user may hold many providers. FK -> "user" cascades on delete.
CREATE TABLE IF NOT EXISTS user_external_identity (
    user_id     uuid        NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    provider    text        NOT NULL,
    external_id text        NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, external_id)
);

CREATE INDEX IF NOT EXISTS idx_user_external_identity_user
    ON user_external_identity (user_id);
