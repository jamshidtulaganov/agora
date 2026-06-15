# sddev QA box — qa_switch.php

`qa_switch.php` is the fast-path hook Agora's **QA agent** calls to switch your
per-dev sddev box (`<name>.sddev.uz`) to a task branch for testing, then restore
the base — in seconds, no Docker rebuild.

## Deploy on the box
1. Copy `qa_switch.php` to the web root of `<name>.sddev.uz` (HTTPS only).
2. Configure (env, or edit the top of the file):
   ```
   export QA_SWITCH_TOKEN=<QA_SWITCH_TOKEN>   # same token Agora uses (see your Agora .env)
   export QA_REPO_DIR=/var/www/sd                                            # your Yii checkout
   ```
3. In `QA_REPO_DIR`, set two git remotes:
   ```
   git remote add fork   https://github.com/<you>/sd-main.git   # your fork (task branches)
   git remote set-url origin https://github.com/azizkh/sd.git    # upstream (base: billing)
   ```
   (use a credential helper / token so the web user can fetch privately).
4. The web user must run `git`/`composer`/`php` and write `protected/runtime/`.
5. Tune `$REBUILD_STEPS` for your Yii 1.x setup (the `yiic` migrate path).

## Smoke-test the hook
```
curl -s -X POST "https://<box>/qa_switch.php?branch=billing&remote=origin" \
     -H "X-QA-Token: <QA_SWITCH_TOKEN>"
# -> {"ok":true,"branch":"billing",...}
```

## Agora side
1. Create a QA agent from the **qa-tester** template (Agents → New → QA Tester).
   It ships with the Playwright `webapp-testing` skill, and every agent already
   carries the builtin **agora-sddev-qa** skill that drives the flow below.
2. Set the agent's `custom_env` to match this box:
   ```
   QA_SWITCH_URL=https://<box>/qa_switch.php
   QA_SDDEV_URL=https://<box>
   QA_SWITCH_TOKEN=<same token as the box>
   QA_LOGIN=demo   QA_PASSWORD=<staging>   QA_LOGIN_PATH=/site/login   QA_SDDEV_BASE_BRANCH=billing
   ```
3. On an issue with an open PR, fire **Run QA** (the `run_qa` slice action) or
   @-mention the QA Tester. The agora-sddev-qa skill then runs:
   `qa_switch?branch=btx-<id>&remote=fork` → Playwright smoke on `QA_SDDEV_URL`
   → restore `?branch=billing&remote=origin` → posts a `qa:pass` / `qa:fail`
   verdict comment + label.

Without the `QA_*` env the skill no-ops with a clear comment, so a non-QA agent
that happens to receive the builtin skill never touches a box.
