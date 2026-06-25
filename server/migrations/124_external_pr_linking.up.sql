-- 124_external_pr_linking
--
-- Link GitLab merge requests / GitHub pull requests that an agent (or human)
-- references in an issue comment to that issue, so the issue-detail
-- "Pull requests" sidebar shows them.
--
-- Why a trigger instead of the normal path: GitHub PR auto-linking is driven by
-- the GitHub App webhook (handler/github.go), which needs a public callback URL
-- + an installed App. A self-host instance behind localhost has neither, and
-- GitLab (gitlab.sdteam.uz) has no equivalent integration at all. The one piece
-- of ground truth we always have is the MR/PR URL the agent posts in a comment,
-- so we materialize the link from that.
--
-- Rows reuse github_pull_request with provider='gitlab' and sentinel
-- installation_id=0 / head_sha='' — NO column is made nullable, so existing
-- sqlc row scans (non-pointer int64 / string) keep working. The frontend
-- PullRequestList already renders generically off html_url/title/state.

ALTER TABLE github_pull_request
  ADD COLUMN IF NOT EXISTS provider text NOT NULL DEFAULT 'github';

-- Parse every MR/PR URL out of a comment body and upsert a linked PR row per
-- match. Idempotent: ON CONFLICT on the existing
-- (workspace_id, repo_owner, repo_name, pr_number) unique key + the
-- issue_pull_request PK means re-running (trigger + backfill) never duplicates.
-- Wrapped so a malformed URL can never abort the comment INSERT.
CREATE OR REPLACE FUNCTION link_prs_from_comment(
  p_issue_id      uuid,
  p_workspace_id  uuid,
  p_content       text,
  p_linked_by_id  uuid,
  p_linked_by_type text
) RETURNS void AS $$
DECLARE
  v_ws    uuid := p_workspace_id;
  g       text[];
  v_owner text;
  v_name  text;
  v_num   int;
  v_url   text;
  v_path  text;
  v_title text;
  v_pr_id uuid;
BEGIN
  IF p_content IS NULL THEN RETURN; END IF;
  IF v_ws IS NULL THEN
    SELECT workspace_id INTO v_ws FROM issue WHERE id = p_issue_id;
  END IF;
  IF v_ws IS NULL THEN RETURN; END IF;

  -- GitLab merge requests: https://host/group/.../repo/-/merge_requests/N
  FOR g IN
    -- [^\s]+? (not .+?): Postgres '.' matches newlines by default, so .+? would
    -- swallow an entire multi-line comment up to the last URL. A repo path has
    -- no whitespace, so [^\s]+? both fixes that and stays minimal per URL.
    SELECT regexp_matches(p_content, '(https?://[^/\s]+)/([^\s]+?)/-/merge_requests/(\d+)', 'g')
  LOOP
    v_path  := g[2];
    v_num   := g[3]::int;
    v_url   := g[1] || '/' || v_path || '/-/merge_requests/' || g[3];
    v_name  := split_part(v_path, '/', array_length(string_to_array(v_path, '/'), 1));
    v_owner := NULLIF(left(v_path, GREATEST(length(v_path) - length(v_name) - 1, 0)), '');
    IF v_owner IS NULL THEN v_owner := v_path; END IF;
    v_title := v_name || ' !' || g[3];

    INSERT INTO github_pull_request
      (id, workspace_id, provider, installation_id, repo_owner, repo_name, pr_number,
       title, state, html_url, head_sha, additions, deletions, changed_files,
       pr_created_at, pr_updated_at, created_at, updated_at)
    VALUES
      (gen_random_uuid(), v_ws, 'gitlab', 0, v_owner, v_name, v_num,
       v_title, 'open', v_url, '', 0, 0, 0, now(), now(), now(), now())
    ON CONFLICT (workspace_id, repo_owner, repo_name, pr_number)
      DO UPDATE SET html_url = EXCLUDED.html_url, provider = 'gitlab', updated_at = now()
    RETURNING id INTO v_pr_id;

    INSERT INTO issue_pull_request
      (issue_id, pull_request_id, linked_by_type, linked_by_id, linked_at, close_intent)
    VALUES (p_issue_id, v_pr_id, p_linked_by_type, p_linked_by_id, now(), false)
    ON CONFLICT (issue_id, pull_request_id) DO NOTHING;
  END LOOP;

  -- GitHub pull requests: https://github.com/owner/repo/pull/N
  -- DO NOTHING on conflict so real GitHub-App-synced rows are never clobbered;
  -- we only ensure the link exists.
  FOR g IN
    SELECT regexp_matches(p_content, '(https?://github\.com)/([^/\s]+)/([^/\s]+)/pull/(\d+)', 'g')
  LOOP
    v_owner := g[2];
    v_name  := g[3];
    v_num   := g[4]::int;
    v_url   := 'https://github.com/' || v_owner || '/' || v_name || '/pull/' || g[4];
    v_title := v_name || ' #' || g[4];

    INSERT INTO github_pull_request
      (id, workspace_id, provider, installation_id, repo_owner, repo_name, pr_number,
       title, state, html_url, head_sha, additions, deletions, changed_files,
       pr_created_at, pr_updated_at, created_at, updated_at)
    VALUES
      (gen_random_uuid(), v_ws, 'github', 0, v_owner, v_name, v_num,
       v_title, 'open', v_url, '', 0, 0, 0, now(), now(), now(), now())
    ON CONFLICT (workspace_id, repo_owner, repo_name, pr_number) DO NOTHING
    RETURNING id INTO v_pr_id;

    IF v_pr_id IS NULL THEN
      SELECT id INTO v_pr_id FROM github_pull_request
      WHERE workspace_id = v_ws AND repo_owner = v_owner
        AND repo_name = v_name AND pr_number = v_num;
    END IF;

    IF v_pr_id IS NOT NULL THEN
      INSERT INTO issue_pull_request
        (issue_id, pull_request_id, linked_by_type, linked_by_id, linked_at, close_intent)
      VALUES (p_issue_id, v_pr_id, p_linked_by_type, p_linked_by_id, now(), false)
      ON CONFLICT (issue_id, pull_request_id) DO NOTHING;
    END IF;
  END LOOP;

  -- GitHub "compare" links — a branch is pushed but the PR isn't opened yet
  -- (an agent without gh API access to a private repo posts the compare /
  -- "open a PR" URL instead of a /pull/N). Surface it in the issue's PR panel
  -- so the human can click through to open it. Synthetic pr_number (>=9e8, from
  -- the repo+head-branch hash) is stable across re-runs and clear of any real
  -- /pull/N number that shows up once the PR is actually opened.
  FOR g IN
    SELECT regexp_matches(p_content, '(https?://github\.com)/([^/\s]+)/([^/\s]+)/compare/([^\s)]+)', 'g')
  LOOP
    v_owner := g[2];
    v_name  := g[3];
    v_url   := g[1] || '/' || v_owner || '/' || v_name || '/compare/' || g[4];
    v_path  := CASE WHEN position('...' in g[4]) > 0 THEN split_part(g[4], '...', 2) ELSE g[4] END;
    v_num   := 900000000 + (abs(hashtext(v_owner || '/' || v_name || '/' || v_path)) % 90000000);
    v_title := 'Open PR: ' || v_path;

    INSERT INTO github_pull_request
      (id, workspace_id, provider, installation_id, repo_owner, repo_name, pr_number,
       title, state, html_url, head_sha, additions, deletions, changed_files,
       pr_created_at, pr_updated_at, created_at, updated_at)
    VALUES
      (gen_random_uuid(), v_ws, 'github', 0, v_owner, v_name, v_num,
       v_title, 'open', v_url, '', 0, 0, 0, now(), now(), now(), now())
    ON CONFLICT (workspace_id, repo_owner, repo_name, pr_number)
      DO UPDATE SET html_url = EXCLUDED.html_url, updated_at = now()
    RETURNING id INTO v_pr_id;

    INSERT INTO issue_pull_request
      (issue_id, pull_request_id, linked_by_type, linked_by_id, linked_at, close_intent)
    VALUES (p_issue_id, v_pr_id, p_linked_by_type, p_linked_by_id, now(), false)
    ON CONFLICT (issue_id, pull_request_id) DO NOTHING;
  END LOOP;

EXCEPTION WHEN OTHERS THEN
  RAISE WARNING 'link_prs_from_comment failed for issue %: %', p_issue_id, SQLERRM;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION trg_link_prs_from_comment() RETURNS trigger AS $$
BEGIN
  PERFORM link_prs_from_comment(
    NEW.issue_id, NEW.workspace_id, NEW.content, NEW.author_id, NEW.author_type);
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS after_comment_insert_link_prs ON comment;
CREATE TRIGGER after_comment_insert_link_prs
  AFTER INSERT ON comment
  FOR EACH ROW
  WHEN (NEW.content ~ 'merge_requests/|/pull/|/compare/')
  EXECUTE FUNCTION trg_link_prs_from_comment();
