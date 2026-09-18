-- Scheduled refresh of a pinned report — the standing, unattended half of
-- Phase 2 (docs/assistant-domain-plan.md §Phase 2b).
--
-- A due schedule starts an ORDINARY assistant run, under the artifact owner's
-- identity, in the session that owns the artifact, with a synthetic refresh
-- message. Nothing about the run is special: the owner sees exactly what the
-- robot did in their own chat history, and the pinned view refreshes through
-- the same update_artifact -> report:updated path migration 203 already built.
-- That is why this table holds a CADENCE and nothing else — no prompt, no
-- recipe, no output. The report is still the artifact; this row only says when
-- to ask for it again.
--
-- PRESETS, NOT CRON (frequency + at_time + weekday), and the reason is spend,
-- not simplicity. A cron string can express "every minute", so a single typo
-- becomes an unattended model-spend loop in someone else's workspace. The
-- tightest schedule expressible here is once a day, so the worst case an
-- operator can configure has a floor they can reason about. weekday uses JS
-- getDay() numbering (0=Sunday..6=Saturday) because the browser is what fills
-- it in, and a second numbering convention on the wire is a silent off-by-one
-- waiting to happen.
--
-- next_run_at is CLAIMED BEFORE the run: the scheduler advances it to the
-- following slot in the same UPDATE that marks the row running, and only then
-- starts the assistant. That makes a slot at-most-once rather than at-least-
-- once, which is the right side to fail on here — a missed refresh costs one
-- stale report until the next slot, while a retried refresh costs real model
-- spend and can race a run the owner started by hand. It also means a crash
-- mid-run cannot resurrect the same slot on restart, and two server processes
-- cannot both serve it: the claim is a compare-and-swap on the slot value.
--
-- UNIQUE (pin_id) is the model, not an optimization. A published report has
-- ONE cadence: the question "how fresh is this report" must have a single
-- answer on the project page, and two schedules on one pin would mean two
-- unattended runs racing to rewrite the same artifact. Create-or-replace on
-- the endpoint is expressible only because this constraint exists. The
-- CASCADE from the pin is the other half: unpinning withdraws the disclosure,
-- and a schedule that outlived its pin would keep spending money to refresh a
-- report nobody can read.
CREATE TABLE assistant_report_schedule (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pin_id UUID NOT NULL UNIQUE REFERENCES assistant_artifact_pin(id) ON DELETE CASCADE,
    frequency TEXT NOT NULL,
    at_time TEXT NOT NULL,
    weekday INT,
    timezone TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_by UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_run_at TIMESTAMPTZ,
    last_status TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    -- NOT NULL: a schedule with no next slot is not a paused schedule, it is a
    -- row the ticker can never see again. Pausing is `enabled = false`, which
    -- keeps the slot computable for when it is switched back on.
    next_run_at TIMESTAMPTZ NOT NULL
);

-- The ticker's only read, once a minute forever, so it must never be a scan of
-- every schedule in the install. Leading `enabled` is what lets a paused row
-- cost nothing at all, and the next_run_at range then narrows the enabled ones
-- to the handful that are actually due.
CREATE INDEX idx_assistant_report_schedule_due ON assistant_report_schedule(enabled, next_run_at);
