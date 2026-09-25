-- A second email address that signs in to the same account. Created when two
-- accounts that belong to one person are merged (e.g. a gmail sign-up and the
-- company account made by the Zoho migration): the merged-away address keeps
-- working for login and resolves to the kept account.
CREATE TABLE user_email_alias (
    email      text PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT user_email_alias_email_lower CHECK (email = lower(email))
);

CREATE INDEX idx_user_email_alias_user ON user_email_alias(user_id);
