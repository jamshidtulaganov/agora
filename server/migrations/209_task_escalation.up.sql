-- Escalation as a first-class state (docs/orchestration-upgrade-plan.md §B1).
--
-- Agora's agents structurally cannot ask a question: every Claude invocation
-- carries `--disallowedTools AskUserQuestion` (pkg/agent/claude.go) and the
-- convention "user-facing clarification belongs in an issue comment" is a
-- convention, not a state — a comment does not park the run, does not reach an
-- inbox, and does not stop the agent from guessing and carrying on.
--
-- `task_escalation` makes "I am stuck" a row: one OPEN row per issue, fanned
-- out to the inbox, resolved by a human, and replayed into the agent's session
-- on resume.
--
-- Deliberately NO expires_at. This is the break from telegram_question
-- (migration 180), which needs one because it BLOCKS an agent process polling
-- for an answer. An escalation waits for a human as long as a human takes; the
-- task is not waiting, it has ended. Expiry would re-import the exact failure
-- mode it exists to remove — an agent told "nobody answered" when the truth is
-- "nobody has looked yet".
--
-- `agora telegram ask` keeps its job (a blocking yes/no where a human is
-- demonstrably present); its table becomes one delivery channel of this one,
-- not a parallel object.

CREATE TABLE task_escalation (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    issue_id      UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    -- The parked run. ON DELETE SET NULL so pruning task history never
    -- destroys the open question a human still owes an answer to.
    task_id       UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    agent_id      UUID REFERENCES agent(id) ON DELETE SET NULL,
    -- question  : "which of these did you mean?"
    -- blocked   : "I cannot proceed without X"
    -- budget    : a spend / turn / wall-clock budget was exhausted (raised by
    --             the failure handler, never by the agent)
    -- permission: the agent needs an access decision it must not take itself
    -- risk      : the change is riskier than the agent was cast to decide
    kind          TEXT NOT NULL DEFAULT 'question'
                    CHECK (kind IN ('question', 'blocked', 'budget', 'permission', 'risk')),
    -- What I need, in one sentence. This is what the human reads first, so it
    -- is the inbox title and the card headline.
    prompt        TEXT NOT NULL,
    -- What I tried. Optional; keeps the human from re-asking the obvious.
    detail        TEXT NOT NULL DEFAULT '',
    -- Empty ⇒ free-text answer. Non-empty ⇒ the human picks one (and may
    -- still type instead — options are a shortcut, never a constraint).
    options       TEXT[] NOT NULL DEFAULT '{}',
    -- Risk tier snapshot (handler.issueRiskTier) taken at raise time, so the
    -- ranked decision queue (plan §A2) can order without re-deriving.
    risk_tier     TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'open'
                    CHECK (status IN ('open', 'answered', 'cancelled')),
    answer        TEXT,
    answered_by   UUID REFERENCES "user"(id) ON DELETE SET NULL,
    answered_at   TIMESTAMPTZ,
    -- The task the answer was replayed into, so the card can link the run the
    -- human's decision produced.
    resumed_task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    raised_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One open escalation per issue. A second `agora issue escalate` on the same
-- issue UPDATES the open row rather than stacking a second question onto the
-- same human — the partial unique index is what makes that a database
-- guarantee instead of a convention.
CREATE UNIQUE INDEX idx_task_escalation_open_issue
    ON task_escalation(issue_id) WHERE status = 'open';

-- The decision-queue read: open escalations for a workspace, oldest first
-- (age IS the ranking signal until §A2 lands).
CREATE INDEX idx_task_escalation_workspace_open
    ON task_escalation(workspace_id, raised_at) WHERE status = 'open';

-- Issue-detail card read (open + recently answered, newest first).
CREATE INDEX idx_task_escalation_issue ON task_escalation(issue_id, raised_at DESC);

-- `waiting_human` — the parked state an escalation puts its task in. Modelled
-- on `waiting_local_directory` (migration 109) but with the opposite liveness
-- contract: the daemon owns a waiting_local_directory row and will flip it
-- within seconds, while a waiting_human row is owned by a PERSON and may sit
-- for a day. It is therefore deliberately absent from every runtime-capacity,
-- offline-sweep and stale-task set (those all enumerate
-- dispatched/running/waiting_local_directory) so that parking an escalation
-- frees the runtime slot and a daemon restart never fails the question.
ALTER TABLE agent_task_queue DROP CONSTRAINT IF EXISTS agent_task_queue_status_check;
ALTER TABLE agent_task_queue ADD CONSTRAINT agent_task_queue_status_check
    CHECK (status IN ('queued', 'dispatched', 'running', 'waiting_local_directory',
                      'waiting_human', 'completed', 'failed', 'cancelled'));
