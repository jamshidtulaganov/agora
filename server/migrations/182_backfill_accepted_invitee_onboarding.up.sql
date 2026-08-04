-- Invitation acceptance is a complete workspace-entry path. Before migration
-- 182, accepted invitees could have a membership while onboarded_at remained
-- NULL, causing web and desktop guards to send them to workspace creation.
-- Limit the repair to users tied to an accepted invitation; normal workspace
-- creators still complete the separate onboarding flow.
UPDATE "user" AS u
SET onboarded_at = COALESCE(u.onboarded_at, wi.updated_at, u.created_at),
    updated_at = now()
FROM workspace_invitation AS wi
JOIN member AS m ON m.workspace_id = wi.workspace_id
WHERE wi.status = 'accepted'
  AND m.user_id = u.id
  AND (
    wi.invitee_user_id = u.id
    OR lower(wi.invitee_email) = lower(u.email)
  )
  AND u.onboarded_at IS NULL;
