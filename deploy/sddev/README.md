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

## Agora side (I wire this once you give the box URL)
QA Tester agent `custom_env`:
```
QA_SWITCH_URL=https://<box>/qa_switch.php
QA_SDDEV_URL=https://<box>
QA_SWITCH_TOKEN=<same as box>
QA_LOGIN=demo   QA_PASSWORD=<staging>   QA_LOGIN_PATH=/site/login   QA_SDDEV_BASE_BRANCH=billing
```
Flow: QA agent → `qa_switch?branch=btx-<id>&remote=fork` → Playwright smoke on `QA_SDDEV_URL` → verdict → restore `?branch=billing&remote=origin`.
