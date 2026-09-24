-- When the one-time "welcome to Agora" email went to a person whose account
-- was created for them (Zoho workspace migration). NULL = never sent. Keeps a
-- re-run of the welcome send from emailing the same people twice.
ALTER TABLE "user" ADD COLUMN welcome_sent_at TIMESTAMPTZ;
