-- Execution lifecycle for a CONFIRMED assistant operation.
--
-- Before this, POST /operations/{id}/confirm claimed the row ('confirmed'),
-- executed, and only then wrote a receipt. A crash in the middle left a row
-- that said "a human authorized this" and nothing at all about whether it ran
-- — indistinguishable, later, from an operation that never started. That is
-- precisely the state docs/agora-assistant-final-plan.md §3 forbids: an
-- operation must be reportable as succeeded, failed-without-effect, or
-- UNCERTAIN, never as a silent nothing.
--
-- Two columns, and the pair is the whole protocol:
--
--   executing_at — stamped in the same compare-and-swap that claims the row,
--                  i.e. BEFORE anything is dispatched. Its presence is the
--                  durable record of "this was handed to the executor".
--   outcome      — written AFTER the execution resolves. NULL means the
--                  answer never came back.
--
-- Read-time rule (evaluated where expiry already is, no sweeper): a confirmed
-- operation whose executing_at is older than a short TTL and whose outcome is
-- still NULL reports as UNCERTAIN. It is never auto-replayed: the row is out
-- of 'pending', so a second confirm still matches nothing and still 409s.
ALTER TABLE assistant_pending_operation
    ADD COLUMN executing_at TIMESTAMPTZ,
    ADD COLUMN outcome      TEXT
        CHECK (outcome IS NULL OR outcome IN ('succeeded', 'failed', 'uncertain'));
