-- The board / issue-list endpoint (handler.ListIssues, paged path) always
-- filters by workspace_id and, by default, orders by (position ASC,
-- created_at DESC). None of the existing issue indexes cover that ordering:
-- idx_issue_workspace stops at workspace_id, so Postgres seq-scanned every
-- workspace row and top-N heapsorted them on each request. Measured on a
-- 20k-issue workspace this was a Seq Scan of all rows + sort for a LIMIT 50
-- (≈3.7ms at the DB, ~26ms end-to-end, RPS collapsing as the workspace grew).
--
-- This composite index matches the default ordering so the planner walks it
-- and stops after LIMIT — no scan, no sort. It is intentionally NOT partial on
-- archived_at: the paged endpoint does not add `archived_at IS NULL`, so a
-- partial predicate would exclude the index from the actual query.
--
-- Non-default sorts (title/created_at/due_date/priority) still sort; the board
-- default (position) is the hot path this targets.
CREATE INDEX IF NOT EXISTS idx_issue_board_order
    ON issue (workspace_id, position ASC, created_at DESC);
