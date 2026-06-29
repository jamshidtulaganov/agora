-- Optional per-agent fallback runtime. When the agent's primary runtime hits a
-- provider usage/rate limit (a weekly/monthly quota or an exhausted 429), the
-- task is re-dispatched onto this runtime instead of dead-stopping — removing
-- the single-provider point of failure where one account's weekly limit takes
-- the whole agent (and the squad it sits in) offline. Nullable: no fallback =
-- the prior behaviour. ON DELETE SET NULL so deleting the fallback runtime
-- never dangles the agent row.
ALTER TABLE agent
    ADD COLUMN fallback_runtime_id uuid REFERENCES agent_runtime(id) ON DELETE SET NULL;
