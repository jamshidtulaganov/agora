-- Link a workspace invite to a Bitrix user chosen by the inviter, so that when
-- the invitee accepts, their new Agora account is bound to the right Bitrix
-- identity (user_external_identity provider='bitrix'). This closes the
-- "one person, two records" gap that appears when the Bitrix email differs
-- from the invite email (the email-match auto-link in provisionBitrixAssignee
-- can't cover that case). Nullable — most invites carry no Bitrix link.
ALTER TABLE workspace_invitation ADD COLUMN IF NOT EXISTS invitee_bitrix_id text;
