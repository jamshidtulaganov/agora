# sdteam QA-box seed

`agora-box-seed` automates the sdteam "dbt_ clone" process: it gives a
per-developer QA box its own test database — a fresh clone of the golden seed
`dbt_agora` (served at `/var/www/agora.sdteam.uz`) — and repoints the site's
Yii config at it.

    jamshid.sdteam.uz → dbt_jamshid   (cloned/refreshed from dbt_agora)

## Install (per QA host, as the box SSH user)

    scp deploy/sdteam/agora-box-seed <host>:~/bin/agora-box-seed
    ssh <host> chmod +x ~/bin/agora-box-seed
    ~/bin/agora-box-seed <sub>.sdteam.uz --dry-run   # preview
    ~/bin/agora-box-seed <sub>.sdteam.uz             # run

## Wire into Agora

    fly secrets set -a sd-agora-backend \
      AGORA_QA_BOX_SEED_COMMAND='~/bin/agora-box-seed {subdomain}'

Agora's **Settings → Labs → box row → Seed** button (and an agent calling
`POST /api/remote-boxes/{id}/seed`) then runs it. `{subdomain}` is expanded
from the box's `work_dir`; the caller never chooses the command.

## Safety

- Target DB is always derived as `dbt_<handle>`; anything not `dbt_*` is
  refused, so a prod `d0_*` DB can never be a target.
- The golden seed (`dbt_agora`) and the seed site are never written.
- Config rewrite touches only the `dbname=` token, with a one-time
  `main.php.bak-agora` backup.
- Credentials are read from each site's own `main.php` on the host and never
  printed.
