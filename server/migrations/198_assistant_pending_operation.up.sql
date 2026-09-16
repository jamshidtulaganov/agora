-- Confirmation binding for the Agora Assistant.
--
-- A destructive tool call no longer executes on a model-supplied `confirm`
-- boolean. It persists ONE row here — the PRE-AUTHORIZATION record: what the
-- user would be authorizing, resolved against real data, written before
-- anything mutates. The transcript renders it as a ConfirmCard and the human's
-- click on POST /api/assistant/operations/{id}/confirm is the authorization.
--
-- This table is deliberately NOT assistant_operation (migration 197). The two
-- are the two halves of one story and must not be merged:
--
--   assistant_pending_operation (here) = intent + authorization state.
--       Written BEFORE execution, resolved by a human gesture, consumed once.
--   assistant_operation (197)          = execution receipt.
--       Written by the run loop around a tool call that actually ran, and
--       carrying the succeeded / failed / uncertain outcome.
--
-- A confirmed operation therefore produces a row in BOTH: this one flips to
-- 'confirmed', and a second assistant_operation row records what the execution
-- did, keyed to the run that asked.
CREATE TABLE assistant_pending_operation (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- The run that proposed the operation. Nullable because a tool can be
    -- executed outside a run (tests, future non-conversational callers); the
    -- link is what ties the execution receipt back to the asking run.
    run_id UUID REFERENCES assistant_run(id) ON DELETE SET NULL,
    session_id UUID NOT NULL REFERENCES assistant_session(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    tool_name TEXT NOT NULL,
    -- The exact arguments the confirmation authorizes. Execution replays THESE,
    -- never anything the model says afterwards.
    arguments JSONB NOT NULL,
    -- Human-readable, built from resolved target data before any mutation.
    summary TEXT NOT NULL,
    workspace_id UUID REFERENCES workspace(id) ON DELETE CASCADE,
    -- {"type","identifier","title"} — re-checked at confirm time so a changed
    -- target invalidates the confirmation instead of silently redirecting it.
    target JSONB NOT NULL DEFAULT '{}'::jsonb,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'confirmed', 'rejected', 'expired')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,
    -- Expiry is evaluated at READ time (confirm / reject / list); no sweeper.
    expires_at TIMESTAMPTZ NOT NULL DEFAULT now() + interval '30 minutes'
);

CREATE INDEX idx_assistant_pending_operation_session
    ON assistant_pending_operation(session_id, created_at DESC);
