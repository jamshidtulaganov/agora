-- One-time tokens for binding an agent's bot to a group by QR.
--
-- Mirrors lark_binding_token: only the HASH is stored, so a leaked database
-- cannot be replayed into a binding. The raw token exists once, inside the
-- t.me deep link encoded in the QR.
--
-- This is what makes QR binding safe. Adding a bot to a group cannot authorize
-- that group by itself — anyone can invite a bot. Presenting a token an
-- operator minted seconds earlier proves the binding was intended.
CREATE TABLE telegram_binding_token (
    token_hash   text PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id     uuid NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    created_by   uuid REFERENCES "user"(id) ON DELETE SET NULL,
    expires_at   timestamptz NOT NULL,
    consumed_at  timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_telegram_binding_token_agent ON telegram_binding_token(agent_id, expires_at);
