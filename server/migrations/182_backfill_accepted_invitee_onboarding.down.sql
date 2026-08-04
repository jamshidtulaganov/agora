-- Data repair is intentionally irreversible: clearing onboarded_at would send
-- valid accepted invitees back through workspace creation.
SELECT 1;
