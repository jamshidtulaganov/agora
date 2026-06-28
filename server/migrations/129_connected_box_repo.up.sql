-- Remote Boxes git-sync target. A locked-down box (git pull + branch checkout
-- only, no installs) can't run agents/build; instead Agora SSHes in and checks
-- out the agent's pushed branch into a working directory the box already serves
-- (e.g. an nginx-served PHP site under /var/www), so the box reflects — and QA
-- can test — the right branch. repo_url is the https clone URL Agora fetches
-- from (a token is injected at sync time, never stored here); work_dir is the
-- absolute path on the box to keep the checkout in. last_branch records the most
-- recently synced branch for display. All nullable/defaulted → additive.
ALTER TABLE connected_box
    ADD COLUMN repo_url    text NOT NULL DEFAULT '',
    ADD COLUMN work_dir    text NOT NULL DEFAULT '',
    ADD COLUMN last_branch text NOT NULL DEFAULT '';
