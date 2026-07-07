-- Verdict provenance (audit P1): who produced this QA evidence — the QA agent
-- ('agent'), a human triage ('human'), or machinery ('watchdog'/'system').
-- Without it a watchdog auto-state and a real agent regression were
-- indistinguishable on every surface.
ALTER TABLE qa_evidence
    ADD COLUMN source text NOT NULL DEFAULT 'agent';
