---
name: agora-sddev-qa
description: "Use to QA a pull request on a per-dev sddev box: switch the box to the PR's branch, smoke-test the live UI with Playwright, ALWAYS restore the base branch, then post a pass/fail verdict comment and attach a qa:pass or qa:fail label. Needs the agent's QA env (QA_SWITCH_URL, QA_SDDEV_URL, QA_SWITCH_TOKEN, QA_LOGIN, QA_PASSWORD); posts a clear no-op comment when that env is unset."
user-invocable: false
allowed-tools: Bash(agora *), Bash(gh *), Bash(curl *), Bash(bash *), Bash(node *), Bash(npx *)
---

# sddev QA — smoke-test a PR on the dev box

Run a fast, live QA pass for an issue's pull request on the developer's own
`sddev` box (`<name>.sddev.uz`), then report a verdict. This is **advisory**:
never make code changes, never `git push`, never merge — the human decides.

## Prerequisites (fail fast)

This skill needs the agent's QA environment. If any of `QA_SWITCH_URL`,
`QA_SDDEV_URL`, `QA_SWITCH_TOKEN`, `QA_LOGIN`, `QA_PASSWORD` is empty, do NOT
switch anything. Post one comment — "QA box not configured for this agent
(QA_* env unset); skipping" — and stop. Only an agent provisioned with these
vars (the QA Tester) can run a real pass.

## Procedure

### 1. Resolve the PR and its branch
The working branch follows `btx-<bitrixTaskId>` (the slice-action that produced
the PR names it that way). Find the open PR:

```bash
gh pr list --head "btx-<bitrixTaskId>" --state open --json number,headRefName,url
```

No PR found → post a comment saying so and stop (nothing to QA yet). Otherwise
use the resolved `headRefName` as `<branch>`.

### 2. Switch the box to the branch
```bash
bash scripts/qa_switch.sh "<branch>" fork
```
This calls the `qa_switch.php` hook (fetch + hard-checkout + rebuild). A non-zero
exit means the branch could not be built — record that as the failure and go
straight to step 4 (restore).

### 3. Smoke-test the live UI (Playwright — use the `webapp-testing` skill)
Drive `$QA_SDDEV_URL` with Playwright:
- Log in at `${QA_SDDEV_URL}${QA_LOGIN_PATH:-/site/login}` with `$QA_LOGIN` /
  `$QA_PASSWORD` (fill the username/text + password inputs, submit). A login that
  redirects back to the login URL is a **failure**.
- Visit the 1-3 page(s) the PR changes (infer from the issue + diff). If the
  change is not page-specific, load the main dashboard.
- **Fail** on any of: an HTTP **5xx**, a **failed** network request, a **JS
  console error**, or an uncaught page error. Screenshot any page that errors.

Keep it bounded (≤90s, ≤3 pages). Mirrors the legacy bot's `browser_qa.py`.

### 4. ALWAYS restore the base branch
Even when step 2 or 3 failed:
```bash
bash scripts/qa_switch.sh "${QA_SDDEV_BASE_BRANCH:-billing}" origin
```
Leaving the box on a feature branch breaks the next person's testing.

### 5. Post the verdict (comment + label)
Build the verdict markdown, then post ONE comment via stdin:

```bash
printf '%s\n' "$VERDICT_MD" | agora issue comment add <issueId> --content-stdin
```

Attach the label. Labels are referenced by **id**, so resolve the workspace
label by name with `agora label list` (create it once with `agora label create`
if it does not exist — see `agora label --help`), then:

```bash
agora issue label add <issueId> <qa-pass-or-qa-fail-label-id>
```

Verdict markdown shape:

```
**QA — <pass ✅ | fail ❌>** (branch `btx-<id>`, PR #<n>)

- Login: <ok | failed>
- Pages checked: </a>, </b>
- Console errors: <none | first 3>
- Network errors (5xx/failed): <none | first 3>
<screenshot refs if any>
```

Pass only when the box switched cleanly, login succeeded, and no console / 5xx /
failed-request errors were seen. Otherwise fail. When in doubt, fail and explain.

## Guardrails
- Advisory only: no code edits, no `git push`, no merge.
- Always restore the base branch (step 4) before finishing — even on failure.
- One verdict comment + one label per run.
