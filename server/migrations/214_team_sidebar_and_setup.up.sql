-- Department setup (docs/workspace-knowledge-plan.md §9): an owner/admin can
-- choose what the team's sidebar shows. That team default applies only to
-- people who never customized their own sidebar, so we need to know who did —
-- hidden_nav = [] can't tell "never touched it" from "chose to show all".
ALTER TABLE "user" ADD COLUMN hidden_nav_customized_at timestamptz;

-- Anyone who already hid something has customized.
UPDATE "user" SET hidden_nav_customized_at = updated_at
WHERE hidden_nav IS NOT NULL AND hidden_nav <> '[]'::jsonb;
