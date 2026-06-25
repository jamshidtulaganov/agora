-- Revert 124_external_pr_linking.
-- Removes the trigger + parser. Leaves already-materialized PR rows/links in
-- place (dropping them would discard real MR references); drop the provider
-- column last.

DROP TRIGGER IF EXISTS after_comment_insert_link_prs ON comment;
DROP FUNCTION IF EXISTS trg_link_prs_from_comment();
DROP FUNCTION IF EXISTS link_prs_from_comment(uuid, uuid, text, uuid, text);
ALTER TABLE github_pull_request DROP COLUMN IF EXISTS provider;
