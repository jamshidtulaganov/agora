ALTER TABLE connected_box
    DROP COLUMN IF EXISTS repo_url,
    DROP COLUMN IF EXISTS work_dir,
    DROP COLUMN IF EXISTS last_branch;
